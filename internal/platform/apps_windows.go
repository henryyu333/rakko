//go:build windows

package platform

// Applications: the foreground app, activation, the running-app snapshot,
// app names and paths, and the icon bytes the cards show.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

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
// first visible titled window that is not a tool window, minimized ones
// included (ActivateApp restores what it finds here). A packaged app's
// window belongs to ApplicationFrameHost, so a frame hosting pid counts
// too.
func topWindowOf(pid int) uintptr {
	var found uintptr
	appEnumWindows(func(h uintptr) bool {
		if windowPID(h) == pid || hostedAppPID(h) == pid {
			if appWindow(h) {
				found = h
				return false
			}
		}
		return true
	})
	return found
}

// appFrameWindowClass is the top-level window class ApplicationFrameHost
// gives a packaged app: the frame belongs to the host process, while the
// app's own window is a child of it.
const appFrameWindowClass = "ApplicationFrameWindow"

// hostedAppPID returns the packaged app's process inside a frame window,
// or 0 when h is not a frame hosting one. The app's own process owns the
// child window, so this is how a UWP app's identity and window are
// recovered from ApplicationFrameHost's top-level window.
func hostedAppPID(frame uintptr) int {
	if windowClass(frame) != appFrameWindowClass {
		return 0
	}
	owner := windowPID(frame)
	var pid int
	appEnumChildren(frame, func(c uintptr) bool {
		p := windowPID(c)
		if p == owner || p == 0 || p == ownProcessID() {
			return true
		}
		if appAUMID(p) != "" {
			pid = p
			return false
		}
		return true
	})
	return pid
}

// appEnumWindows runs visit over every top-level window, and
// appEnumChildren over every child window of parent. Both go through the
// process's fixed enumeration callback (win32_windows.go).
func appEnumWindows(visit func(h uintptr) bool) { enumTopLevel(visit) }

func appEnumChildren(parent uintptr, visit func(h uintptr) bool) { enumChildren(parent, visit) }

// appWindow reports whether h is a window that names a running app: it is
// visible, not a tool window, not cloaked onto another virtual desktop or
// behind a suspended packaged app, not one of the desktop's own surfaces,
// and has a title. A minimized window still counts — its app is running,
// the switcher lists it, and ActivateApp has to find it to restore it. The
// screen-visible window list keeps using ordinaryWindow, which drops
// minimized windows.
func appWindow(h uintptr) bool {
	if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
		return false
	}
	if isToolWindow(h) {
		return false
	}
	if shellWindows[windowClass(h)] || windowCloaked(h) {
		return false
	}
	return windowText(h) != ""
}

// FrontmostPID returns the process identifier of the window that owns the
// foreground, 0 when there is none. For a packaged app this is
// ApplicationFrameHost, which is the process a targeted input call has to
// reach.
func FrontmostPID() int {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == 0 {
		return 0
	}
	return windowPID(fg)
}

// FrontmostApp returns the foreground application's identity and name.
// The identity is the app's own, even when its window is hosted by
// ApplicationFrameHost.
func FrontmostApp() (appID, name string) {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == 0 {
		return "", ""
	}
	pid := windowPID(fg)
	if hosted := hostedAppPID(fg); hosted != 0 {
		pid = hosted
	}
	if pid == 0 {
		return "", ""
	}
	return appIDOf(pid), appNameOf(pid)
}

// appAUMID is the process's own AppUserModelID. It is a variable so the
// identity rule — one package can publish several apps, and each process
// keeps its own — can be tested without two installed apps; production
// always reads the system query.
var appAUMID = processAUMID

func appIDOf(pid int) string {
	if id := appAUMID(pid); id != "" {
		return id
	}
	return processExe(pid)
}

func appNameOf(pid int) string {
	if exe := processExe(pid); exe != "" {
		base := filepath.Base(exe)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	if id := appAUMID(pid); id != "" {
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
	type windowApp struct {
		pid int
	}
	var windows []windowApp
	self := ownProcessID()
	appEnumWindows(func(h uintptr) bool {
		pid := windowPID(h)
		if pid == self || pid == 0 {
			return true
		}
		if !appWindow(h) {
			return true
		}
		// A packaged app's top-level window belongs to ApplicationFrameHost:
		// the frame host process would collapse every UWP app into one
		// entry, so the hosted app's own process stands in.
		if hosted := hostedAppPID(h); hosted != 0 {
			pid = hosted
		}
		windows = append(windows, windowApp{pid: pid})
		return true
	})
	// Identities and names are resolved after the enumeration: the shell
	// queries behind a package identity and a localized name must not run
	// inside the window callback.
	var out []RunningApp
	seenPID := map[int]bool{}
	seenID := map[string]bool{}
	for _, w := range windows {
		if seenPID[w.pid] {
			continue
		}
		id := appIDOf(w.pid)
		if id == "" {
			continue
		}
		if key := AppIDKey(id); seenID[key] {
			continue
		} else {
			seenID[key] = true
		}
		seenPID[w.pid] = true
		out = append(out, RunningApp{AppID: id, PID: w.pid})
	}
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
	if id == bundleID {
		if name, ok := reverseDomainAppName(id); ok {
			return name
		}
	}
	return id
}

// reverseDomainAppName extracts a readable application name from an unresolved
// reverse-domain bundle identifier synced from macOS (e.g. "com.apple.finder"
// -> "Finder", "com.apple.TextEdit" -> "TextEdit").
func reverseDomainAppName(id string) (string, bool) {
	if strings.ContainsAny(id, "\\/:!_ \t\r\n") {
		return "", false
	}
	parts := strings.Split(id, ".")
	if len(parts) < 3 {
		return "", false
	}
	tld := parts[0]
	if len(tld) < 2 || len(tld) > 10 {
		return "", false
	}
	for i := 0; i < len(tld); i++ {
		if tld[i] < 'a' || tld[i] > 'z' {
			return "", false
		}
	}
	last := parts[len(parts)-1]
	if last == "" {
		return "", false
	}
	for _, p := range parts[1 : len(parts)-1] {
		if p == "" {
			return "", false
		}
	}
	allLowerASCII := true
	hasUpper := false
	for i := 0; i < len(last); i++ {
		c := last[i]
		if c >= 'A' && c <= 'Z' {
			hasUpper = true
		} else if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			allLowerASCII = false
		}
	}
	if allLowerASCII && !hasUpper && last[0] >= 'a' && last[0] <= 'z' {
		last = strings.ToUpper(last[:1]) + last[1:]
	}
	return last, true
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

// iconFilePNG extracts a file's shell icon at the requested size and returns
// it as PNG bytes. The shell's item image factory renders the icon the way
// Explorer does, at the size asked for — a .exe's own icon included — where
// SHGetFileInfo caps a large icon at 32×32 and every larger size is a blurry
// scale of it. A path that does not exist still yields the generic document
// icon, as the Mac implementation does, through the fallback.
func iconFilePNG(path string, size int) []byte {
	if size <= 0 {
		size = 32
	}
	if png := shellItemIconPNG(path, size); png != nil {
		return png
	}
	if strings.HasPrefix(path, "shell:") {
		return nil
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
	return hbitmapPNG(ii.hbmColor, size)
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
