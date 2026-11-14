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
	"runtime"
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

// ErrCannotRecycle reports an item the shell would permanently delete
// instead of recycling: a volume without a recycle bin, the bin turned off by
// policy, or an item larger than the bin's quota. TrashFile leaves such an
// item where it is rather than deleting it.
var ErrCannotRecycle = errors.New("windows: the item cannot be moved to the recycle bin")

// TrashFile moves path to the recycle bin the way Explorer's 移到回收站
// does, and answers the path it took. It never falls back to a permanent
// delete: an item the shell cannot recycle is refused with ErrCannotRecycle,
// and a shell that reports a failure or an abort is propagated, so the caller
// can leave the item where it is.
func TrashFile(path string) (trashed string, err error) {
	abs, aerr := filepath.Abs(path)
	if aerr != nil {
		return "", aerr
	}
	fi, serr := os.Lstat(abs)
	if serr != nil {
		return "", mapFileError("trash", serr)
	}
	if err := recyclableItem(abs, fi); err != nil {
		return "", err
	}
	// The bin's item count is the only confirmation the shell gives that the
	// item was recycled rather than deleted.
	before, counted := recycleBinItems(abs)
	if err := recycleItem(abs); err != nil {
		return "", err
	}
	if _, serr := os.Lstat(abs); serr == nil {
		return "", fmt.Errorf("platform: %s is still there after the delete", abs)
	}
	if counted {
		if after, ok := recycleBinItems(abs); ok && after <= before {
			return "", fmt.Errorf("platform: %s was not added to the recycle bin", abs)
		}
	}
	return abs, nil
}

// recyclableItem refuses an item the shell would permanently delete. The
// recycle rules come from the volume and the user's bin settings; an item on
// a network share or removable media has no bin at all, and the bin's quota
// is what Explorer shows in its properties.
func recyclableItem(abs string, fi os.FileInfo) error {
	root := filepath.VolumeName(abs)
	if root == "" {
		return fmt.Errorf("%w: %s has no volume", ErrCannotRecycle, abs)
	}
	mount := root + string(filepath.Separator)
	p, err := windows.UTF16PtrFromString(mount)
	if err != nil {
		return err
	}
	driveType, _, _ := procGetDriveType.Call(uintptr(unsafe.Pointer(p)))
	rules := recycleRules{driveType: uint32(driveType)}
	if nuke, ok := bitBucketDWORD(mount, "NukeOnDelete"); ok {
		rules.nukeOnDelete = nuke != 0
	}
	if rules.driveType == driveFixed && !rules.nukeOnDelete {
		rules.capacity = recycleCapacity(mount)
	}
	return recycleRefusal(rules, abs, fi)
}

// recycleRules is what decides whether the shell may recycle an item.
type recycleRules struct {
	driveType    uint32
	nukeOnDelete bool
	capacity     int64 // 0 when unknown
}

// recycleRefusal explains why an item cannot go to the recycle bin under
// rules, or nil when it can.
func recycleRefusal(rules recycleRules, path string, fi os.FileInfo) error {
	if rules.driveType != driveFixed {
		return fmt.Errorf("%w: %s is not on a fixed drive", ErrCannotRecycle, path)
	}
	if rules.nukeOnDelete {
		return fmt.Errorf("%w: the recycle bin is turned off for this user", ErrCannotRecycle)
	}
	if !fi.IsDir() && rules.capacity > 0 && fi.Size() > rules.capacity {
		return fmt.Errorf("%w: %s is larger than the recycle bin (%d bytes)", ErrCannotRecycle, path, rules.capacity)
	}
	return nil
}

// recycleCapacity is the bin's quota for the volume in bytes: the user's
// setting when there is one, otherwise Explorer's default of 5% of a small
// volume and 10% of a large one.
func recycleCapacity(mount string) int64 {
	if mb, ok := bitBucketDWORD(mount, "MaxCapacity"); ok && mb > 0 {
		return int64(mb) << 20
	}
	p, err := windows.UTF16PtrFromString(mount)
	if err != nil {
		return 0
	}
	var free, total, totalFree uint64
	if r, _, _ := procGetDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree))); r == 0 {
		return 0
	}
	return defaultRecycleCapacity(total)
}

// defaultRecycleCapacity is Explorer's default bin quota for a volume of
// total bytes: 5% up to 40 GB and 10% above that.
func defaultRecycleCapacity(total uint64) int64 {
	const fortyGB = 40 << 30
	if total <= fortyGB {
		return int64(total / 20)
	}
	return int64(total / 10)
}

// bitBucketDWORD reads one of the shell's per-volume recycle-bin settings.
// mount is the volume's mount point ("C:\").
func bitBucketDWORD(mount, name string) (uint32, bool) {
	guid, ok := volumeGUID(mount)
	if !ok {
		return 0, false
	}
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\`+guid, registry.QUERY_VALUE)
	if err != nil {
		return 0, false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// volumeGUID answers the volume's GUID name ("{...}") from its mount point;
// the shell keys the per-volume bin settings by it.
func volumeGUID(mount string) (string, bool) {
	p, err := windows.UTF16PtrFromString(mount)
	if err != nil {
		return "", false
	}
	var buf [64]uint16
	if r, _, _ := procGetVolumeNameForVolumeMountPoint.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); r == 0 {
		return "", false
	}
	name := windows.UTF16ToString(buf[:])
	name = strings.TrimSuffix(strings.TrimPrefix(name, `\\?\Volume`), `\`)
	if name == "" {
		return "", false
	}
	return name, true
}

// recycleBinItems reports how many items the recycle bin holds for the volume
// holding path.
func recycleBinItems(path string) (int64, bool) {
	root := filepath.VolumeName(path)
	if root == "" {
		return 0, false
	}
	p, err := windows.UTF16PtrFromString(root + string(filepath.Separator))
	if err != nil {
		return 0, false
	}
	info := shQueryRBInfo{cbSize: uint32(unsafe.Sizeof(shQueryRBInfo{}))}
	if r, _, _ := procSHQueryRecycleBin.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&info))); int32(r) < 0 {
		return 0, false
	}
	return info.i64NumItems, true
}

// recycleItem hands the item to the shell's IFileOperation with the
// flag that recycles on delete.
func recycleItem(abs string) error {
	// IFileOperation must be used from a single-threaded apartment, so the
	// operation gets a thread of its own rather than borrowing whichever
	// apartment the caller happens to be in.
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		done <- fileOperationDelete(abs)
	}()
	return <-done
}

// comApartmentAction says what to do with a thread's CoInitializeEx result:
// whether the thread may host the operation, and whether the success has to be
// released with CoUninitialize — S_FALSE ("already initialised") counts too,
// because every successful call adds a reference.
func comApartmentAction(hr int32) (usable, release bool, err error) {
	switch uint32(hr) {
	case 0, 1: // S_OK, S_FALSE: the thread is (now) a single-threaded apartment
		return true, true, nil
	case 0x80010106: // RPC_E_CHANGED_MODE: another apartment is already there
		return false, false, errors.New("platform: the recycle thread is not a single-threaded apartment")
	default:
		return false, false, fmt.Errorf("platform: CoInitializeEx: 0x%08x", uint32(hr))
	}
}

// fileOperationDelete recycles one item. The shell reports a refused or
// aborted operation through the HRESULTs here, which the caller propagates.
func fileOperationDelete(abs string) error {
	// IFileOperation requires a single-threaded apartment. The worker thread
	// is this operation's own, so it starts one and releases it before the
	// thread goes back to the pool.
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	usable, release, err := comApartmentAction(int32(hr))
	if !usable {
		return err
	}
	if release {
		defer procCoUninitialize.Call()
	}
	clsid, err := guidFromString(clsidFileOperation)
	if err != nil {
		return err
	}
	iid, err := guidFromString(iidIFileOperation)
	if err != nil {
		return err
	}
	itemIID, err := guidFromString(iidShellItem)
	if err != nil {
		return err
	}
	var op uintptr
	// CLSCTX_INPROC_SERVER|CLSCTX_LOCAL_SERVER = 1|4.
	if r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsid)), 0, 5,
		uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&op))); int32(r) < 0 {
		return fmt.Errorf("platform: IFileOperation: CoCreateInstance failed (0x%08x)", uint32(int32(r)))
	}
	defer comMethod(op, comRelease)

	flags := uintptr(fofSilent | fofNoConfirm | fofNoErrorUI | fofxEarlyFailure | fofxRecycleOnDelete)
	if r := comMethod(op, fileOperationSetOperationFlags, flags); int32(r) < 0 {
		return fmt.Errorf("platform: IFileOperation.SetOperationFlags failed (0x%08x)", uint32(int32(r)))
	}
	p, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return err
	}
	var item uintptr
	if r, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&itemIID)), uintptr(unsafe.Pointer(&item))); int32(r) < 0 {
		return fmt.Errorf("platform: SHCreateItemFromParsingName(%s) failed (0x%08x)", abs, uint32(int32(r)))
	}
	defer comMethod(item, comRelease)

	if r := comMethod(op, fileOperationDeleteItem, item, 0); int32(r) < 0 {
		return fmt.Errorf("platform: IFileOperation.DeleteItem(%s) failed (0x%08x)", abs, uint32(int32(r)))
	}
	if r := comMethod(op, fileOperationPerformOperations); int32(r) < 0 {
		return fmt.Errorf("platform: the move of %s to the recycle bin failed (0x%08x)", abs, uint32(int32(r)))
	}
	var aborted int32
	if r := comMethod(op, fileOperationAnyAborted, uintptr(unsafe.Pointer(&aborted))); int32(r) >= 0 && aborted != 0 {
		return errors.New("platform: the move to the recycle bin was cancelled")
	}
	return nil
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
