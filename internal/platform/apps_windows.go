//go:build windows

package platform

// Applications: the foreground app, activation, the running-app snapshot,
// app names and paths, and the icon bytes the cards show.

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/image/draw"
	"golang.org/x/sys/windows"
)

// RunningApp is one entry of the app switcher's snapshot.
type RunningApp struct {
	// AppID is the stable identity: an AppUserModelID for a packaged app,
	// the executable's path for a desktop one.
	AppID string
	// Name is the app's display name.
	Name string
	// PID is valid for this snapshot only.
	PID int
}

// OwnPID returns this process's identifier.
func OwnPID() int { return ownProcessID() }

// AppActive reports whether this application has the keyboard.
func AppActive() bool { return mygo.App.IsActive() }

// ActivateSelf brings this application to the front so a window can take
// the keyboard.
func ActivateSelf() { mygo.App.Focus() }

// DeactivateSelf resigns the foreground state. Windows has no app-wide
// hide, and handing the foreground to another process from here would pick
// an arbitrary window; the caller's paste path already targets a specific
// process.
func DeactivateSelf() {}

// ActivateApp brings the application with the process identifier pid to
// the front and reports whether the system accepted the request.
func ActivateApp(pid int) bool {
	if pid <= 0 {
		return false
	}
	h := topWindowOf(pid)
	if h == 0 {
		return false
	}
	if r, _, _ := procIsIconic.Call(h); r != 0 {
		procShowWindow.Call(h, 9 /* SW_RESTORE */)
	}
	// Windows lets only the foreground thread hand the foreground away;
	// from a background panel SetForegroundWindow alone returns success
	// without raising anything. Joining our input to the current
	// foreground thread for the call is the documented way around it.
	fg, _, _ := procGetForegroundWindow.Call()
	if fg != 0 && fg != h {
		myThread, _, _ := procGetCurrentThreadID.Call()
		fgThread, _, _ := procGetWindowThreadPID.Call(fg)
		if fgThread != 0 && fgThread != myThread {
			if ok, _, _ := procAttachThreadInput.Call(myThread, fgThread, 1); ok != 0 {
				procBringWindowToTop.Call(h)
				r, _, _ := procSetForegroundWindow.Call(h)
				procAttachThreadInput.Call(myThread, fgThread, 0)
				return r != 0
			}
		}
	}
	r, _, _ := procSetForegroundWindow.Call(h)
	return r != 0
}

// topWindowOf returns the app window of pid a user would recognize: the
// first visible titled window that is not a tool window.
func topWindowOf(pid int) uintptr {
	var found uintptr
	cb := windowsEnumProc(func(h uintptr) bool {
		if windowPID(h) != pid {
			return true
		}
		if !ordinaryWindow(h) {
			return true
		}
		found = h
		return false
	})
	enumTopLevel(cb)
	return found
}

// FrontmostPID returns the process identifier of the foreground app, 0
// when there is none.
func FrontmostPID() int {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == 0 {
		return 0
	}
	return windowPID(fg)
}

// FrontmostApp returns the foreground application's identity and name.
func FrontmostApp() (appID, name string) {
	pid := FrontmostPID()
	if pid == 0 {
		return "", ""
	}
	return appIDOf(pid), appNameOf(pid)
}

func appIDOf(pid int) string {
	if id := processAUMID(pid); id != "" {
		return id
	}
	return processExe(pid)
}

func appNameOf(pid int) string {
	if exe := processExe(pid); exe != "" {
		base := filepath.Base(exe)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	if id := processAUMID(pid); id != "" {
		if i := strings.Index(id, "!"); i > 0 {
			id = id[:i]
		}
		if i := strings.LastIndex(id, "."); i >= 0 && i+1 < len(id) {
			id = id[i+1:]
		}
		return id
	}
	return ""
}

// RunningApps lists the applications running now, in z-order, without this
// one and without apps that have no identity to pin.
func RunningApps() []RunningApp {
	var out []RunningApp
	seen := map[int]bool{}
	self := ownProcessID()
	cb := windowsEnumProc(func(h uintptr) bool {
		pid := windowPID(h)
		if pid == self || pid == 0 || seen[pid] {
			return true
		}
		if !ordinaryWindow(h) {
			return true
		}
		id := appIDOf(pid)
		if id == "" {
			return true
		}
		seen[pid] = true
		out = append(out, RunningApp{AppID: id, PID: pid})
		return true
	})
	enumTopLevel(cb)
	// Names are resolved after the enumeration: the shell query behind a
	// localized name must not run inside the window callback.
	for i := range out {
		if localized, ok := appListName(out[i].AppID); ok && localized != "" {
			out[i].Name = localized
			continue
		}
		out[i].Name = appNameOf(out[i].PID)
	}
	return out
}

// AppName resolves a display name for an app identity (an AppUserModelID
// or an executable path).
func AppName(bundleID string) string {
	if bundleID == "" {
		return ""
	}
	if name, ok := appListName(bundleID); ok {
		return name
	}
	if p := AppPath(bundleID); p != "" {
		base := filepath.Base(p)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	// A packaged app whose files are not directly reachable: name it from
	// its identity, "Microsoft.WindowsCalculator_8wekyb3d8bbwe!App" →
	// "Microsoft.WindowsCalculator".
	id := bundleID
	if i := strings.Index(id, "!"); i > 0 {
		id = id[:i]
	}
	if i := strings.Index(id, "_"); i > 0 {
		id = id[:i]
	}
	return id
}

// AppIDKey is the identity key the app switcher compares identities by.
// A Windows identity is either an AppUserModelID or a file path, and the
// same executable reaches the switcher with the filesystem's own casing —
// sometimes "System32", sometimes "system32" — so the key folds case and
// cleans the separators.
func AppIDKey(id string) string {
	return strings.ToLower(filepath.Clean(id))
}

// AppPath resolves an app identity to the path the shell opens for it,
// or "" when nothing installed answers for it. A desktop app resolves to
// its executable; a packaged app resolves to its Apps folder entry, which
// is the only address the shell accepts for one.
func AppPath(bundleID string) string {
	if bundleID == "" {
		return ""
	}
	if filepath.IsAbs(bundleID) {
		if st, err := os.Stat(bundleID); err == nil && !st.IsDir() {
			return bundleID
		}
		return ""
	}
	if strings.HasPrefix(bundleID, `shell:`) {
		if target, ok := appListResolve(bundleID); ok {
			return target
		}
		return ""
	}
	if p, err := execLookup(bundleID); err == nil {
		return p
	}
	root := os.Getenv("SystemRoot")
	for _, dir := range []string{filepath.Join(root, "System32"), root} {
		p := filepath.Join(dir, bundleID)
		if !strings.Contains(bundleID, ".") {
			p += ".exe"
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if target, ok := appListResolve(bundleID); ok {
		return target
	}
	return ""
}

// appListResolve finds the Apps folder entry an identity names, whether
// the caller holds the AppUserModelID or the entry's shell path.
func appListResolve(id string) (string, bool) {
	for _, e := range appListFresh() {
		if e.Target == id {
			return e.Target, true
		}
		if e.Target == `shell:AppsFolder\`+id {
			return e.Target, true
		}
	}
	return "", false
}

func execLookup(name string) (string, error) {
	if strings.ContainsAny(name, `\/`) {
		return "", os.ErrNotExist
	}
	p, err := exec.LookPath(name)
	if err != nil && !strings.Contains(name, ".") {
		p, err = exec.LookPath(name + ".exe")
	}
	return p, err
}

// ReopenApp opens an app the way its taskbar icon does: an app that is
// already running comes to the front, and one that is not launches. On
// Windows the shell opens another instance of an executable, so the
// running instance has to be found and activated first.
func ReopenApp(bundleID string) bool {
	if bundleID == "" {
		return false
	}
	key := AppIDKey(bundleID)
	for _, r := range RunningApps() {
		if AppIDKey(r.AppID) != key && AppIDKey(`shell:AppsFolder\`+r.AppID) != key {
			continue
		}
		if ActivateApp(r.PID) {
			return true
		}
	}
	if p := AppPath(bundleID); p != "" {
		return shellExecute("open", p, "")
	}
	if strings.Contains(bundleID, "!") {
		return shellExecute("open", `shell:AppsFolder\`+bundleID, "")
	}
	return false
}

func shellExecute(verb, target, params string) bool {
	verbp, _ := windows.UTF16PtrFromString(verb)
	targetp, _ := windows.UTF16PtrFromString(target)
	var paramsp *uint16
	if params != "" {
		paramsp, _ = windows.UTF16PtrFromString(params)
	}
	r, _, _ := procShellExecute.Call(0, uintptr(unsafe.Pointer(verbp)), uintptr(unsafe.Pointer(targetp)),
		uintptr(unsafe.Pointer(paramsp)), 0, 1 /* SW_SHOWNORMAL */)
	return r > 32
}

// AppIcon returns the PNG bytes of an app's icon at size×size pixels.
func AppIcon(bundle string, size int) []byte {
	p := AppPath(bundle)
	if p == "" && filepath.IsAbs(bundle) {
		p = bundle
	}
	if p == "" {
		return nil
	}
	return iconFilePNG(p, size)
}

// FileIcon returns the PNG bytes of the shell icon for path.
func FileIcon(path string, size int) []byte {
	if path == "" {
		return nil
	}
	return iconFilePNG(path, size)
}

// iconFilePNG extracts a file's shell icon at the closest available size
// and scales it to size×size. A path that does not exist still yields the
// generic document icon, as the Mac implementation does.
func iconFilePNG(path string, size int) []byte {
	if size <= 0 {
		size = 32
	}
	if strings.HasPrefix(path, "shell:") {
		return shellItemIconPNG(path, size)
	}
	var shfi shFileInfo
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	flags := uintptr(shgfiIcon | shgfiLargeIcon)
	attr := uintptr(0)
	if _, err := os.Stat(path); err != nil {
		flags |= shgfiUseFileAttrs
		attr = fileAttributeNormal
	}
	if r, _, _ := procSHGetFileInfo.Call(uintptr(unsafe.Pointer(p)), attr,
		uintptr(unsafe.Pointer(&shfi)), unsafe.Sizeof(shfi), flags); r == 0 || shfi.hIcon == 0 {
		return nil
	}
	return iconPNG(shfi.hIcon, size)
}

// iconPNG converts an HICON to PNG bytes, scaled to size. The icon is
// owned by the caller's shell call, which frees it after conversion.
func iconPNG(hicon uintptr, size int) []byte {
	if hicon == 0 {
		return nil
	}
	defer procDestroyIcon.Call(hicon)
	var ii iconInfo
	if r, _, _ := procGetIconInfo.Call(hicon, uintptr(unsafe.Pointer(&ii))); r == 0 {
		return nil
	}
	if ii.hbmColor != 0 {
		defer procDeleteObject.Call(ii.hbmColor)
	}
	if ii.hbmMask != 0 {
		defer procDeleteObject.Call(ii.hbmMask)
	}
	if ii.hbmColor == 0 {
		return nil
	}
	var bm winBitmap
	if r, _, _ := procGetObject.Call(ii.hbmColor, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil
	}
	w, h := int(bm.bmWidth), int(bm.bmHeight)
	if w <= 0 || h <= 0 {
		return nil
	}
	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return nil
	}
	defer procDeleteDC.Call(dc)
	old, _, _ := procSelectObject.Call(dc, ii.hbmColor)
	defer procSelectObject.Call(dc, old)

	bi := bitmapInfoHeader{
		biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:       int32(w),
		biHeight:      -int32(h), // top-down rows
		biPlanes:      1,
		biBitCount:    32,
		biCompression: 0, // BI_RGB
	}
	buf := make([]byte, w*h*4)
	if r, _, _ := procGetDIBits.Call(dc, ii.hbmColor, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0); r == 0 {
		return nil
	}
	// BGRA → RGBA. An icon whose alpha channel is entirely zero came from
	// a 24-bit source, where the alpha byte is unused.
	anyAlpha := false
	for i := 3; i < len(buf); i += 4 {
		if buf[i] != 0 {
			anyAlpha = true
			break
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range w * h {
		b, g, r, a := buf[i*4], buf[i*4+1], buf[i*4+2], buf[i*4+3]
		if !anyAlpha {
			a = 0xff
		}
		img.Pix[i*4] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = a
	}
	var out image.Image = img
	if w != size || h != size {
		dst := image.NewNRGBA(image.Rect(0, 0, size, size))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
		out = dst
	}
	var enc bytes.Buffer
	if err := png.Encode(&enc, out); err != nil {
		return nil
	}
	return enc.Bytes()
}

// ObserveActiveApp reports every foreground-app change. The recorder needs
// the activation order, not just the foreground app at poll time.
func ObserveActiveApp(changed func()) (stop func()) {
	if changed == nil {
		return func() {}
	}
	const (
		eventSystemForeground = 0x0003
		wineventOutOfContext  = 0x0000
		wineventSkipOwnProc   = 0x0002
	)
	cb := windows.NewCallback(func(_, _, _, _, _, _, _ uintptr) uintptr {
		changed()
		return 0
	})
	h, _, _ := procSetWinEventHook.Call(eventSystemForeground, eventSystemForeground, 0, cb, 0, 0,
		wineventOutOfContext|wineventSkipOwnProc)
	if h == 0 {
		return func() {}
	}
	return func() { procUnhookWinEvent.Call(h) }
}
