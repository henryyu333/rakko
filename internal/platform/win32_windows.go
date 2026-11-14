//go:build windows

package platform

// Win32 plumbing the Windows implementations share: the DLL procedures,
// window styles, DPI and virtual-desktop conversion, and the window
// enumeration helpers. Everything here is called on the thread that owns
// the windows (the main thread) unless a caller says otherwise.

import (
	"hash/fnv"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	winmm    = windows.NewLazySystemDLL("winmm.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
)

func sysProc(dll *windows.LazyDLL, name string) *windows.LazyProc { return dll.NewProc(name) }

// newCallback wraps a two-argument Windows callback (enumeration procs take
// the handle and the application-supplied value).
func newCallback(fn func(uintptr, uintptr) uintptr) uintptr {
	return windows.NewCallback(fn)
}

var (
	procSetWindowPos        = sysProc(user32, "SetWindowPos")
	procGetWindowRect       = sysProc(user32, "GetWindowRect")
	procGetClientRect       = sysProc(user32, "GetClientRect")
	procGetForegroundWindow = sysProc(user32, "GetForegroundWindow")
	procSetForegroundWindow = sysProc(user32, "SetForegroundWindow")
	procGetActiveWindow     = sysProc(user32, "GetActiveWindow")
	procGetWindowThreadPID  = sysProc(user32, "GetWindowThreadProcessId")
	procEnumWindows         = sysProc(user32, "EnumWindows")
	procIsWindowVisible     = sysProc(user32, "IsWindowVisible")
	procIsWindow            = sysProc(user32, "IsWindow")
	procIsIconic            = sysProc(user32, "IsIconic")
	procGetCursorPos        = sysProc(user32, "GetCursorPos")
	procSetCursorPos        = sysProc(user32, "SetCursorPos")
	procGetWindowTextLen    = sysProc(user32, "GetWindowTextLengthW")
	procGetWindowText       = sysProc(user32, "GetWindowTextW")
	procWindowFromPoint     = sysProc(user32, "WindowFromPoint")
	procGetAncestor         = sysProc(user32, "GetAncestor")
	procMonitorFromPoint    = sysProc(user32, "MonitorFromPoint")
	procMonitorFromWindow   = sysProc(user32, "MonitorFromWindow")
	procGetMonitorInfo      = sysProc(user32, "GetMonitorInfoW")
	procEnumDisplayMonitors = sysProc(user32, "EnumDisplayMonitors")
	procGetDpiForWindow     = sysProc(user32, "GetDpiForWindow")
	procGetAsyncKeyState    = sysProc(user32, "GetAsyncKeyState")
	procSetWindowsHookEx    = sysProc(user32, "SetWindowsHookExW")
	procUnhookWindowsHookEx = sysProc(user32, "UnhookWindowsHookEx")
	procCallNextHookEx      = sysProc(user32, "CallNextHookEx")
	procGetSystemMetrics    = sysProc(user32, "GetSystemMetrics")
	procSetSysColors        = sysProc(user32, "SetSysColors")
	procDestroyIcon         = sysProc(user32, "DestroyIcon")
	procSetWinEventHook     = sysProc(user32, "SetWinEventHook")
	procUnhookWinEvent      = sysProc(user32, "UnhookWinEvent")
	procMsgWaitForMultiple  = sysProc(user32, "MsgWaitForMultipleObjects")
	procShowWindow          = sysProc(user32, "ShowWindow")
	procCreateWindowEx      = sysProc(user32, "CreateWindowExW")
	procDestroyWindow       = sysProc(user32, "DestroyWindow")

	procGetLastInputInfo    = sysProc(kernel32, "GetLastInputInfo")
	procGetTickCount        = sysProc(kernel32, "GetTickCount64")
	procGetSystemPowerStat  = sysProc(kernel32, "GetSystemPowerStatus")
	procGetDevicePowerState = sysProc(kernel32, "GetDevicePowerState")
	procGetFileAttributesEx = sysProc(kernel32, "GetFileAttributesExW")
	procMoveFileEx          = sysProc(kernel32, "MoveFileExW")
	procGetDiskFreeSpaceEx  = sysProc(kernel32, "GetDiskFreeSpaceExW")
	procGetCurrentThreadID  = sysProc(kernel32, "GetCurrentThreadId")
	procSetThreadExecState  = sysProc(kernel32, "SetThreadExecutionState")
	procOpenProcess         = sysProc(kernel32, "OpenProcess")
	procQueryFullProcessImg = sysProc(kernel32, "QueryFullProcessImageNameW")
	procCloseHandle         = sysProc(kernel32, "CloseHandle")
	procGetAUMID            = sysProc(kernel32, "GetApplicationUserModelID")
	procGetConsoleWindow    = sysProc(kernel32, "GetConsoleWindow")
	procGetModuleFileName   = sysProc(kernel32, "GetModuleFileNameW")

	procShellExecute         = sysProc(shell32, "ShellExecuteW")
	procSHGetFileInfo        = sysProc(shell32, "SHGetFileInfoW")
	procSHFileOperation      = sysProc(shell32, "SHFileOperationW")
	procSHGetKnownFolderPath = sysProc(shell32, "SHGetKnownFolderPath")
	procExtractIconEx        = sysProc(shell32, "ExtractIconExW")

	procDwmSetWindowAttr = sysProc(dwmapi, "DwmSetWindowAttribute")
	procDwmGetWindowAttr = sysProc(dwmapi, "DwmGetWindowAttribute")

	procCreateCompatibleDC = sysProc(gdi32, "CreateCompatibleDC")
	procDeleteDC           = sysProc(gdi32, "DeleteDC")
	procSelectObject       = sysProc(gdi32, "SelectObject")
	procDeleteObject       = sysProc(gdi32, "DeleteObject")
	procGetObject          = sysProc(gdi32, "GetObjectW")
	procGetDIBits          = sysProc(gdi32, "GetDIBits")
	procGetIconInfo        = sysProc(user32, "GetIconInfo")
	procDestroyIconW       = sysProc(user32, "DestroyIcon")

	procCoInitializeEx = sysProc(ole32, "CoInitializeEx")
	procCoUninitialize = sysProc(ole32, "CoUninitialize")

	procPlaySound        = sysProc(winmm, "PlaySoundW")
	procGetDpiForMonitor = sysProc(shcore, "GetDpiForMonitor")
	procWTSQuerySession  = sysProc(wtsapi32, "WTSQuerySessionInformationW")
	procOpenInputDesktop = sysProc(user32, "OpenInputDesktop")
	procCloseDesktop     = sysProc(user32, "CloseDesktop")
)

// Window styles and messages this package touches.
const (
	gwlStyle   = -16
	gwlExStyle = -20
	gwlpID     = -12

	wsExTopmost     = 0x00000008
	wsExTransparent = 0x00000020
	wsExToolWindow  = 0x00000080
	wsExLayered     = 0x00080000
	wsExNoActivate  = 0x08000000

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
	swpNoOwnerZ     = 0x0200
	swpShowWindow   = 0x0040

	lwaAlpha = 0x00000002

	gaRoot = 2

	whMouseLL = 14

	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmMButtonDown = 0x0207
	wmXButtonDown = 0x020B

	hcAction = 0

	vkLeftButton   = 0x01
	vkRightButton  = 0x02
	vkMiddleButton = 0x04

	mdvEffDPI = 0

	shgfiIcon         = 0x000000100
	shgfiSmallIcon    = 0x000000001
	shgfiLargeIcon    = 0x000000000
	shgfiUseFileAttrs = 0x000000400
	shgfiSysIconIndex = 0x000004000

	fileAttributeNormal = 0x00000080

	foDelete     = 0x0003
	fofSilent    = 0x0004
	fofNoConfirm = 0x0010
	fofAllowUndo = 0x0040
	fofNoErrorUI = 0x0400

	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
	esContinuous      = 0x80000000

	pppWildcard = 1

	statusCharging = 8

	wmSettingChange = 0x001A
)

// hwndTopmost and hwndNoTopmost are the SetWindowPos insert-after
// sentinels (-1 and -2 as unsigned pointers).
var (
	hwndTopmost   = ^uintptr(0)
	hwndNoTopmost = ^uintptr(1)
	hwndTop       = uintptr(0)
)

type winRect struct{ Left, Top, Right, Bottom int32 }

func (r winRect) width() int32  { return r.Right - r.Left }
func (r winRect) height() int32 { return r.Bottom - r.Top }

type winPoint struct{ X, Y int32 }

type monitorInfoEx struct {
	cbSize    uint32
	rcMonitor winRect
	rcWork    winRect
	dwFlags   uint32
	szDevice  [32]uint16
}

type winBitmap struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       uintptr
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type shFileInfo struct {
	hIcon         uintptr
	iIcon         int32
	dwAttributes  uint32
	szDisplayName [260]uint16
	szTypeName    [80]uint16
}

type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

func getWindowLong(h uintptr, index int32) uintptr {
	r, _, _ := user32.NewProc("GetWindowLongPtrW").Call(h, uintptr(index))
	return r
}

func setWindowLong(h uintptr, index int32, v uintptr) {
	user32.NewProc("SetWindowLongPtrW").Call(h, uintptr(index), v)
}

func windowExStyle(h uintptr) uintptr { return getWindowLong(h, gwlExStyle) }

// setExBits turns one extended-style bit on or off, and re-applies the
// frame so the window manager hit-tests the new style at once (a layered
// window switching WS_EX_TRANSPARENT is only seen after the frame change).
func setExBits(h uintptr, bits uintptr, on bool) {
	ex := windowExStyle(h)
	next := ex | bits
	if !on {
		next = ex &^ bits
	}
	if next == ex {
		return
	}
	setWindowLong(h, gwlExStyle, next)
	procSetWindowPos.Call(h, 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
}

// reorderFront raises h into the topmost band, or re-orders it there
// without touching its geometry. Every Rakko overlay band maps to the
// topmost band on Windows: the desktop has no menu-bar level to stand
// above, and the taskbar owns a band of its own that a topmost window
// covers.
func reorderFront(h uintptr, topmost bool) {
	after := hwndTopmost
	if !topmost {
		after = hwndNoTopmost
	}
	procSetWindowPos.Call(h, after, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoActivate|swpNoOwnerZ)
}

func ensureToolWindow(h uintptr) {
	setExBits(h, wsExToolWindow, true)
}

func setLayeredAlpha(h uintptr) {
	setExBits(h, wsExLayered, true)
	user32.NewProc("SetLayeredWindowAttributes").Call(h, 0, 255, lwaAlpha)
}

// --- displays ---------------------------------------------------------------

type screenMonitor struct {
	handle uintptr
	bounds winRect
	work   winRect
	dpi    int
	device string
}

func enumMonitors() []screenMonitor {
	var out []screenMonitor
	cb := windows.NewCallback(func(handle, _ uintptr, _ uintptr, _ uintptr) uintptr {
		var mi monitorInfoEx
		mi.cbSize = uint32(unsafe.Sizeof(mi))
		if r, _, _ := procGetMonitorInfo.Call(handle, uintptr(unsafe.Pointer(&mi))); r == 0 {
			return 1
		}
		dpi := 96
		var dx, dy uint32
		if r, _, _ := procGetDpiForMonitor.Call(handle, mdvEffDPI, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
			dpi = int(dx)
		}
		out = append(out, screenMonitor{
			handle: handle,
			bounds: mi.rcMonitor,
			work:   mi.rcWork,
			dpi:    dpi,
			device: windows.UTF16ToString(mi.szDevice[:]),
		})
		return 1
	})
	procEnumDisplayMonitors.Call(0, 0, cb, 0)
	return out
}

func primaryMonitor(ms []screenMonitor) int {
	for i, m := range ms {
		if m.bounds.Left == 0 && m.bounds.Top == 0 {
			return i
		}
	}
	if len(ms) > 0 {
		return 0
	}
	return -1
}

// dipOrigin is a display's top-left corner in mygo's DIP space, which is
// the public screen coordinate space: every display's origin is its pixel
// origin scaled by the smallest scale of the set, so displays of different
// scales never overlap in DIPs.
func dipOrigin(ms []screenMonitor, i int) (int, int) {
	if i < 0 || i >= len(ms) {
		return 0, 0
	}
	minDPI := 96
	for _, m := range ms {
		if m.dpi < minDPI {
			minDPI = m.dpi
		}
	}
	return scaleBy(int(ms[i].bounds.Left), 96, minDPI), scaleBy(int(ms[i].bounds.Top), 96, minDPI)
}

func scaleBy(v, num, den int) int {
	if den == 0 {
		return v
	}
	// Round halves away from zero, the same rule mygo's own scaling uses.
	if v >= 0 {
		return (v*num*2 + den) / (2 * den)
	}
	return -((-v*num*2 + den) / (2 * den))
}

// displayAt returns the index of the display a DIP point is on, or the
// nearest one when the point is in a gap between displays.
func displayAt(ms []screenMonitor, x, y int) int {
	if len(ms) == 0 {
		return -1
	}
	for i, m := range ms {
		ox, oy := dipOrigin(ms, i)
		w := scaleBy(int(m.bounds.width()), 96, m.dpi)
		h := scaleBy(int(m.bounds.height()), 96, m.dpi)
		if x >= ox && x < ox+w && y >= oy && y < oy+h {
			return i
		}
	}
	best, bestD := 0, int(^uint(0)>>1)
	for i, m := range ms {
		ox, oy := dipOrigin(ms, i)
		w := scaleBy(int(m.bounds.width()), 96, m.dpi)
		h := scaleBy(int(m.bounds.height()), 96, m.dpi)
		dx, dy := 0, 0
		if x < ox {
			dx = ox - x
		} else if x >= ox+w {
			dx = x - (ox + w)
		}
		if y < oy {
			dy = oy - y
		} else if y >= oy+h {
			dy = y - (oy + h)
		}
		if d := dx*dx + dy*dy; d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// dipToPixels converts a rectangle in the public DIP space to the physical
// pixels SetWindowPos takes, at the scale of the display its top-left
// corner is on.
func dipToPixels(r Rect) winRect { return dipToPixelsIn(enumMonitors(), r) }

// dipToPixelsIn is dipToPixels over an explicit display list, so the DIP
// arithmetic is unit-tested without a screen.
func dipToPixelsIn(ms []screenMonitor, r Rect) winRect {
	i := displayAt(ms, int(r.X), int(r.Y))
	if i < 0 {
		return winRect{
			Left: int32(r.X), Top: int32(r.Y),
			Right: int32(r.X + r.Width), Bottom: int32(r.Y + r.Height),
		}
	}
	m := ms[i]
	ox, oy := dipOrigin(ms, i)
	left := float64(m.bounds.Left) + float64(r.X-float64(ox))*float64(m.dpi)/96
	top := float64(m.bounds.Top) + float64(r.Y-float64(oy))*float64(m.dpi)/96
	w := r.Width * float64(m.dpi) / 96
	h := r.Height * float64(m.dpi) / 96
	p := winRect{
		Left:   int32(roundHalf(float64(left))),
		Top:    int32(roundHalf(float64(top))),
		Right:  int32(roundHalf(float64(left) + w)),
		Bottom: int32(roundHalf(float64(top) + h)),
	}
	return p
}

func roundHalf(v float64) float64 {
	if v >= 0 {
		return float64(int(v + 0.5))
	}
	return float64(-int(-v + 0.5))
}

// pixelsToDIP converts a physical rectangle to the public DIP space of the
// display it is on.
func pixelsToDIP(r winRect) Rect { return pixelsToDIPIn(enumMonitors(), r) }

// pixelsToDIPIn is pixelsToDIP over an explicit display list.
func pixelsToDIPIn(ms []screenMonitor, r winRect) Rect {
	best, bestD := -1, int64(1)<<62
	for i, m := range ms {
		d := int64(abs32(r.Left-m.bounds.Left)) + int64(abs32(r.Top-m.bounds.Top))
		if d < bestD {
			best, bestD = i, d
		}
	}
	if best < 0 {
		return Rect{X: float64(r.Left), Y: float64(r.Top), Width: float64(r.width()), Height: float64(r.height())}
	}
	m := ms[best]
	ox, oy := dipOrigin(ms, best)
	return Rect{
		X:      float64(ox) + float64(r.Left-m.bounds.Left)*96/float64(m.dpi),
		Y:      float64(oy) + float64(r.Top-m.bounds.Top)*96/float64(m.dpi),
		Width:  float64(r.width()) * 96 / float64(m.dpi),
		Height: float64(r.height()) * 96 / float64(m.dpi),
	}
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// StableDisplayID returns a display's persistent identity: a hash of its
// device name ("\\.\DISPLAY1"), which survives a restart even though the
// HMONITOR mygo reports as Display.ID does not. An unknown id is returned
// unchanged, so a stale saved key never matches a real display by accident.
func StableDisplayID(id int64) int64 {
	for _, m := range enumMonitors() {
		if int64(m.handle) == id {
			return stableDisplayID(m)
		}
	}
	return id
}

// stableDisplayID hashes one display's device name into the positive
// int64 range. The high bit is cleared so a stored key never looks like a
// negative number in the settings JSON.
func stableDisplayID(m screenMonitor) int64 {
	h := fnv.New64a()
	h.Write([]byte(m.device))
	return int64(h.Sum64() &^ (1 << 63))
}

// findMonitor resolves either an HMONITOR or a stable display id to the
// monitor, so callers holding a saved id and callers holding mygo's raw id
// both reach the same display.
func findMonitor(displayID int64) (screenMonitor, int, bool) {
	return findMonitorIn(enumMonitors(), displayID)
}

// findMonitorIn resolves an id against an explicit display list: an
// HMONITOR first, then a stable display id.
func findMonitorIn(ms []screenMonitor, displayID int64) (screenMonitor, int, bool) {
	for i, m := range ms {
		if int64(m.handle) == displayID {
			return m, i, true
		}
	}
	for i, m := range ms {
		if stableDisplayID(m) == displayID {
			return m, i, true
		}
	}
	return screenMonitor{}, 0, false
}

// --- window enumeration -----------------------------------------------------

func windowPID(h uintptr) int {
	var pid uint32
	procGetWindowThreadPID.Call(h, uintptr(unsafe.Pointer(&pid)))
	return int(pid)
}

func windowRect(h uintptr) (winRect, bool) {
	var r winRect
	if ok, _, _ := procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return winRect{}, false
	}
	return r, true
}

func windowText(h uintptr) string {
	n, _, _ := procGetWindowTextLen.Call(h)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return windows.UTF16ToString(buf)
}

func isToolWindow(h uintptr) bool { return windowExStyle(h)&wsExToolWindow != 0 }

var (
	exePathMu   sync.Mutex
	exePathMemo = map[uint32]string{}
)

// processExe is the on-disk image of a process, memoized for the process
// lifetime; the enumeration calls it once per window.
func processExe(pid int) string {
	if pid <= 0 {
		return ""
	}
	exePathMu.Lock()
	if p, ok := exePathMemo[uint32(pid)]; ok {
		exePathMu.Unlock()
		return p
	}
	exePathMu.Unlock()
	h, _, _ := procOpenProcess.Call(0x1000 /* PROCESS_QUERY_LIMITED_INFORMATION */, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImg.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	p := windows.UTF16ToString(buf[:size])
	exePathMu.Lock()
	exePathMemo[uint32(pid)] = p
	exePathMu.Unlock()
	return p
}

// processAUMID is the AppUserModelID of a packaged (Store/UWP) process, or
// "" for a desktop one (and on a system whose kernel does not export the
// query).
func processAUMID(pid int) string {
	proc := aumidProc()
	if proc == nil {
		return ""
	}
	h, _, _ := procOpenProcess.Call(0x1000, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	size := uint32(512)
	buf := make([]uint16, size)
	if r, _, _ := proc.Call(h, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0]))); r != 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

var (
	aumidOnce sync.Once
	aumidP    *windows.LazyProc
)

// aumidProc resolves GetApplicationUserModelID without panicking on a
// system that does not export it: an absent query only means the app
// identity falls back to the executable path.
func aumidProc() *windows.LazyProc {
	aumidOnce.Do(func() {
		p := kernel32.NewProc("GetApplicationUserModelID")
		if err := p.Find(); err == nil {
			aumidP = p
		}
	})
	return aumidP
}

// ownPID is the process id, memoized (it cannot change).
var ownPIDOnce sync.Once
var ownPIDVal int

func ownProcessID() int {
	ownPIDOnce.Do(func() { ownPIDVal = int(windows.GetCurrentProcessId()) })
	return ownPIDVal
}
