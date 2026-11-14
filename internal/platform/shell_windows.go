//go:build windows

package platform

// File system and shell calls: the recycle bin, revealing in Explorer,
// free space, creation times, non-overwriting renames, the idle clock, the
// session and desktop state, the alert sound, and the two text helpers.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// SessionActive reports whether the user is at an unlocked console
// session: a locked screen and a switched-away session both report false,
// so no sampled activity counts time in an app the user cannot see.
func SessionActive() bool {
	h, _, _ := procOpenInputDesktop.Call(0, 0, 0x0100 /* DESKTOP_SWITCHDESKTOP */)
	if h == 0 {
		return false
	}
	procCloseDesktop.Call(h)
	return true
}

// IdleSeconds is how long the machine has gone without keyboard or mouse
// input, in seconds.
func IdleSeconds() (float64, error) {
	type lastInputInfo struct {
		cbSize uint32
		dwTime uint32
	}
	var li lastInputInfo
	li.cbSize = uint32(unsafe.Sizeof(li))
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li))); r == 0 {
		return 0, errors.New("platform: GetLastInputInfo failed")
	}
	now, _, _ := procGetTickCount.Call()
	idle := uint32(uint64(now) - uint64(li.dwTime))
	return float64(idle) / 1000, nil
}

// CanReadDownloads reports whether this process may read the folders macOS
// protects with Full Disk Access. Windows has no such gate, so it is true.
func CanReadDownloads() bool { return true }

// CanCaptureScreen reports whether screen capture is permitted. Windows
// has no screen-recording consent gate; a capture can still fail on a
// protected surface, which the capture path reports itself.
func CanCaptureScreen() bool { return true }

// RequestScreenCapture is a no-op on Windows: there is nothing to grant.
func RequestScreenCapture() bool { return true }

// ScreenshotFolder is where Windows' own tools save screenshots: the
// Screenshots folder inside the user's Pictures folder, which follows a
// redirected (OneDrive) Pictures location. A folder that does not exist is
// "" — the system default, which the capture path resolves to the desktop,
// exactly like a never-configured Mac.
func ScreenshotFolder() string {
	dir := picturesFolder()
	if dir == "" {
		return ""
	}
	shots := filepath.Join(dir, "Screenshots")
	if fi, err := os.Stat(shots); err != nil || !fi.IsDir() {
		return ""
	}
	return shots
}

// picturesFolder is the user's Pictures folder from the shell's own
// redirection, or "" when the registry has no answer.
func picturesFolder() string {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("My Pictures")
	if err != nil {
		return ""
	}
	return expandShellPath(v)
}

// expandShellPath replaces %VAR% with the environment's value: the shell
// stores the Pictures folder as a REG_EXPAND_SZ such as
// "%USERPROFILE%\Pictures", and os.ExpandEnv only understands the Unix
// $VAR form. An unknown variable is left in place, which makes the caller's
// os.Stat fail and the default folder win.
func expandShellPath(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '%' {
			if j := strings.IndexByte(v[i+1:], '%'); j >= 0 {
				name := v[i+1 : i+1+j]
				if val, ok := os.LookupEnv(name); ok {
					b.WriteString(val)
					i += j + 1
					continue
				}
			}
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// CreationTime is the filesystem's creation time for path.
func CreationTime(path string) (time.Time, bool) {
	type fileAttributeData struct {
		FileAttributes uint32
		CreationTime   windows.Filetime
		LastAccessTime windows.Filetime
		LastWriteTime  windows.Filetime
		FileSizeHigh   uint32
		FileSizeLow    uint32
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return time.Time{}, false
	}
	var d fileAttributeData
	r, _, _ := procGetFileAttributesEx.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&d)))
	if r == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, d.CreationTime.Nanoseconds()), true
}

// FreeSpace reports the free bytes on the volume holding dir.
func FreeSpace(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	r, _, err := procGetDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return 0, err
	}
	return int64(free), nil
}

// RenameNoReplace moves src to dst without ever overwriting an existing
// entry; a taken name reports ErrExists.
func RenameNoReplace(src, dst string) error {
	s, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	d, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	r, _, callErr := procMoveFileEx.Call(uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(d)), 0)
	if r != 0 {
		return nil
	}
	return mapFileError("rename", callErr)
}

// IsNotDirectory reports whether err says a path component is not a
// directory, the failure a Downloads folder replaced by a file gives.
func IsNotDirectory(err error) bool {
	return errors.Is(err, windows.ERROR_DIRECTORY)
}

func mapFileError(op string, err error) error {
	switch {
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS), errors.Is(err, windows.ERROR_FILE_EXISTS):
		return fmt.Errorf("%w: %s: %v", ErrExists, op, err)
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return fmt.Errorf("%w: %s: %v", ErrPermission, op, err)
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return fmt.Errorf("%w: %s: %v", ErrNotExist, op, err)
	case errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE):
		return fmt.Errorf("%w: %s: %v", ErrNameTooLong, op, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// TrashFile moves path to the recycle bin the way Explorer's 移到回收站
// does. It fails rather than deleting when the shell cannot recycle the
// item, so the caller can leave the item where it is.
func TrashFile(path string) (trashed string, err error) {
	abs, aerr := filepath.Abs(path)
	if aerr != nil {
		return "", aerr
	}
	if _, serr := os.Lstat(abs); serr != nil {
		return "", mapFileError("trash", serr)
	}
	// SHFileOperation takes a double-NUL-terminated list; one path is a
	// list of one.
	from := windows.StringToUTF16(abs)
	from = append(from, 0)
	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &from[0],
		fFlags: fofAllowUndo | fofNoConfirm | fofSilent | fofNoErrorUI,
	}
	r, _, _ := procSHFileOperation.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 || op.fAnyOperationsAborted != 0 {
		if op.fAnyOperationsAborted != 0 {
			return "", errors.New("platform: the move to the recycle bin was cancelled")
		}
		return "", fmt.Errorf("platform: the recycle bin refused %s (0x%x)", abs, r)
	}
	return abs, nil
}

// RevealInFileManager selects path in a new Explorer window.
func RevealInFileManager(path string) {
	if path == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	shellExecute("open", "explorer.exe", "/select,\""+abs+"\"")
}

// PlayAlertSound starts the system alert sound looping. It reports whether
// the system accepted the request.
func PlayAlertSound() bool {
	const sndAsync = 0x0001
	const sndNoDefault = 0x0002
	const sndLoop = 0x0008
	const sndAlias = 0x00010000
	name, _ := windows.UTF16PtrFromString("SystemAsterisk")
	r, _, _ := procPlaySound.Call(uintptr(unsafe.Pointer(name)), 0, sndAsync|sndNoDefault|sndLoop|sndAlias)
	return r != 0
}

// StopAlertSound silences the loop PlayAlertSound started.
func StopAlertSound() {
	procPlaySound.Call(0, 0, 0)
}

// DecodeGB18030 decodes b as GB18030, reporting whether every byte
// decoded.
func DecodeGB18030(b []byte) (s string, ok bool) {
	out, _, err := transform.String(simplifiedchinese.GB18030.NewDecoder(), string(b))
	if err != nil && !errors.Is(err, transform.ErrShortSrc) {
		return "", false
	}
	if strings.ContainsRune(out, '\uFFFD') {
		return "", false
	}
	return out, true
}

// NormalizeText applies Unicode compatibility composition (NFKC).
func NormalizeText(s string) string { return norm.NFKC.String(s) }

// ReduceTransparency reports whether Windows' transparency effects are
// switched off.
func ReduceTransparency() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("EnableTransparency")
	if err != nil {
		return false
	}
	return v == 0
}

// ObserveAccessibilityChange reports accessibility display-option changes.
// Windows offers no notification for the transparency switch, so the
// watcher polls the setting and reports each change.
func ObserveAccessibilityChange(changed func()) (stop func()) {
	if changed == nil {
		return func() {}
	}
	var once sync.Once
	done := make(chan struct{})
	go func() {
		last := ReduceTransparency()
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				now := ReduceTransparency()
				if now != last {
					last = now
					WakeMain(changed)
				}
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}
