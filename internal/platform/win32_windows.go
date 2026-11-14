//go:build windows

package platform

// Win32 plumbing the Windows implementations share: the DLL procedures,
// window styles, DPI and virtual-desktop conversion, and the window
// enumeration helpers. Everything here is called on the thread that owns
// the windows (the main thread) unless a caller says otherwise.

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"
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
	combase  = windows.NewLazySystemDLL("combase.dll")
	powrprof = windows.NewLazySystemDLL("powrprof.dll")
)

// Procedures more than one area calls.
var (
	procCoCreateInstance = sysProc(ole32, "CoCreateInstance")
	procGetClassName     = sysProc(user32, "GetClassNameW")
)

func sysProc(dll *windows.LazyDLL, name string) *windows.LazyProc { return dll.NewProc(name) }

// guidFromString parses a GUID in either spelling — the registry's
// "{...}" form or the bare one — into its ABI layout.
// windows.GUIDFromString calls CLSIDFromString, which insists on the braces,
// so a bare identifier would parse to nothing; and a malformed identifier is
// an error the caller must see, not a zero GUID that creates nothing.
func guidFromString(s string) (windows.GUID, error) {
	if !strings.HasPrefix(s, "{") {
		s = "{" + s + "}"
	}
	g, err := windows.GUIDFromString(s)
	if err != nil {
		return windows.GUID{}, fmt.Errorf("platform: %q is not a GUID: %w", s, err)
	}
	return g, nil
}

// ensureCOM makes sure the calling thread has a COM apartment to create the
// shell objects in. mygo initialises the main thread as an STA; this covers a
// call made before that and a test binary, and leaves an apartment that is
// already there alone. The apartment is not released: Rakko owns it for the
// life of the process.
func ensureCOM() error {
	r, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	switch uint32(int32(r)) {
	case 0, 1: // S_OK, S_FALSE (already an STA)
		return nil
	case 0x80010106: // RPC_E_CHANGED_MODE: the thread is an MTA already
		return nil
	}
	return fmt.Errorf("platform: CoInitializeEx: 0x%08x", uint32(int32(r)))
}

var (
	procSetWindowPos        = sysProc(user32, "SetWindowPos")
	procGetWindowRect       = sysProc(user32, "GetWindowRect")
	procGetClientRect       = sysProc(user32, "GetClientRect")
	procGetForegroundWindow = sysProc(user32, "GetForegroundWindow")
	procSetForegroundWindow = sysProc(user32, "SetForegroundWindow")
	procBringWindowToTop    = sysProc(user32, "BringWindowToTop")
	procAttachThreadInput   = sysProc(user32, "AttachThreadInput")
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
	procGetMessage          = sysProc(user32, "GetMessageW")
	procTranslateMessage    = sysProc(user32, "TranslateMessage")
	procDispatchMessage     = sysProc(user32, "DispatchMessageW")
	procShowWindow          = sysProc(user32, "ShowWindow")
	procCreateWindowEx      = sysProc(user32, "CreateWindowExW")
	procDestroyWindow       = sysProc(user32, "DestroyWindow")

	procGetLastInputInfo                 = sysProc(user32, "GetLastInputInfo")
	procGetTickCount                     = sysProc(kernel32, "GetTickCount64")
	procGetSystemPowerStat               = sysProc(kernel32, "GetSystemPowerStatus")
	procGetFileAttributesEx              = sysProc(kernel32, "GetFileAttributesExW")
	procMoveFileEx                       = sysProc(kernel32, "MoveFileExW")
	procGetDiskFreeSpaceEx               = sysProc(kernel32, "GetDiskFreeSpaceExW")
	procGetCurrentThreadID               = sysProc(kernel32, "GetCurrentThreadId")
	procSetThreadExecState               = sysProc(kernel32, "SetThreadExecutionState")
	procOpenProcess                      = sysProc(kernel32, "OpenProcess")
	procGetDriveType                     = sysProc(kernel32, "GetDriveTypeW")
	procGetVolumeNameForVolumeMountPoint = sysProc(kernel32, "GetVolumeNameForVolumeMountPointW")
	procQueryFullProcessImg              = sysProc(kernel32, "QueryFullProcessImageNameW")
	procCloseHandle                      = sysProc(kernel32, "CloseHandle")
	procGetProcessTimes                  = sysProc(kernel32, "GetProcessTimes")
	procGetConsoleWindow                 = sysProc(kernel32, "GetConsoleWindow")
	procGetModuleFileName                = sysProc(kernel32, "GetModuleFileNameW")

	procShellExecute                = sysProc(shell32, "ShellExecuteW")
	procSHGetFileInfo               = sysProc(shell32, "SHGetFileInfoW")
	procSHCreateItemFromParsingName = sysProc(shell32, "SHCreateItemFromParsingName")
	procSHGetKnownFolderPath        = sysProc(shell32, "SHGetKnownFolderPath")
	procExtractIconEx               = sysProc(shell32, "ExtractIconExW")

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

	procGetDisplayConfigBufferSizes      = sysProc(user32, "GetDisplayConfigBufferSizes")
	procQueryDisplayConfig               = sysProc(user32, "QueryDisplayConfig")
	procDisplayConfigGetDeviceInfo       = sysProc(user32, "DisplayConfigGetDeviceInfo")
	procWTSQuerySession                  = sysProc(wtsapi32, "WTSQuerySessionInformationW")
	procPowerSettingRegisterNotification = sysProc(powrprof, "PowerSettingRegisterNotification")
	procOpenInputDesktop                 = sysProc(user32, "OpenInputDesktop")
	procCloseDesktop                     = sysProc(user32, "CloseDesktop")
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

	fofSilent    = 0x0004
	fofNoConfirm = 0x0010
	fofNoErrorUI = 0x0400

	// The Windows 8 shell flags: recycle instead of deleting, and stop the
	// whole operation on the first error.
	fofxRecycleOnDelete = 0x00080000
	fofxEarlyFailure    = 0x00100000

	driveFixed = 3

	// IFileOperation's class and the interfaces the recycle path creates.
	clsidFileOperation = "{3ad05575-8857-4850-9277-11b85bdb8e09}"
	iidIFileOperation  = "{947aab5f-0a5c-4c13-b4d6-4bf7836fc9f8}"
	iidShellItem       = "{43826d1e-e718-42ee-bc55-a1e261c37bfe}"

	// The vtable slots of IUnknown and IFileOperation this package calls.
	comRelease                     = 2
	fileOperationAdvise            = 3
	fileOperationUnadvise          = 4
	fileOperationSetOperationFlags = 5
	fileOperationDeleteItem        = 18
	fileOperationPerformOperations = 21
	fileOperationAnyAborted        = 22

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

// displayConfigSourceInfo, displayConfigTargetInfo and displayConfigPathInfo
// mirror DISPLAYCONFIG_PATH_SOURCE_INFO, DISPLAYCONFIG_PATH_TARGET_INFO and
// DISPLAYCONFIG_PATH_INFO: one active output of the display configuration,
// named by the adapter's LUID and the output id on it. The source's trailing
// statusFlags is part of the Windows 10 structure and shifts the target
// information, so the sizes here must match the system's exactly.
type displayConfigSourceInfo struct {
	adapterID   windows.LUID
	id          uint32
	modeInfoIdx uint32
	statusFlags uint32
}

type displayConfigRational struct {
	numerator   uint32
	denominator uint32
}

type displayConfigTargetInfo struct {
	adapterID        windows.LUID
	id               uint32
	modeInfoIdx      uint32
	outputTechnology uint32
	rotation         uint32
	scaling          uint32
	refreshRate      displayConfigRational
	scanLineOrdering uint32
	targetAvailable  int32
	statusFlags      uint32
}

type displayConfigPathInfo struct {
	source displayConfigSourceInfo
	target displayConfigTargetInfo
	flags  uint32
}

// displayConfigDeviceInfoHeader mirrors DISPLAYCONFIG_DEVICE_INFO_HEADER,
// which gets the type and buffer size on the way in.
type displayConfigDeviceInfoHeader struct {
	deviceType uint32
	size       uint32
	adapterID  windows.LUID
	id         uint32
}

// displayConfigSourceDeviceName mirrors DISPLAYCONFIG_SOURCE_DEVICE_NAME: the
// GDI device name of an output's source.
type displayConfigSourceDeviceName struct {
	header  displayConfigDeviceInfoHeader
	gdiName [32]uint16
}

// displayConfigTargetDeviceName mirrors DISPLAYCONFIG_TARGET_DEVICE_NAME: the
// physical monitor behind an output.
type displayConfigTargetDeviceName struct {
	header            displayConfigDeviceInfoHeader
	flags             uint32
	outputTechnology  uint32
	edidManufactureID uint16
	edidProductCodeID uint16
	connectorInstance uint32
	friendlyName      [64]uint16
	devicePath        [128]uint16
}

const (
	// qdcOnlyActivePaths asks the display configuration for the outputs in
	// use, the ones GDI enumerates as displays.
	qdcOnlyActivePaths = 0x00000002

	displayConfigGetSourceName = 1
	displayConfigGetTargetName = 2

	// displayConfigModeInfoSize is sizeof(DISPLAYCONFIG_MODE_INFO); the mode
	// array is only a buffer for the query, so it is kept as 8-byte words.
	displayConfigModeInfoSize = 64
)

type winRect struct{ Left, Top, Right, Bottom int32 }

func (r winRect) width() int32  { return r.Right - r.Left }
func (r winRect) height() int32 { return r.Bottom - r.Top }

type winPoint struct{ X, Y int32 }

// winMsg mirrors MSG: one message for GetMessage/TranslateMessage/
// DispatchMessage.
type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
}

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

// --- enumeration callbacks --------------------------------------------------

// windowsEnumProc visits one enumerated handle and reports whether the
// enumeration may continue.
type windowsEnumProc func(h uintptr) bool

// The enumeration procs are fixed: a Windows callback is permanent once
// created and takes a slot the runtime never frees, so each kind of
// enumeration has exactly one and the per-call visitor travels through the
// enumeration's own application value (EnumWindows' lParam,
// EnumDisplayMonitors' dwData) as a token into enumCalls.
var (
	enumWindowsProc  = windows.NewCallback(enumWindowHandle)
	enumMonitorsProc = windows.NewCallback(enumMonitorHandle)
)

var enumCalls struct {
	mu      sync.Mutex
	next    uintptr
	byToken map[uintptr]windowsEnumProc
}

// beginEnum registers a visitor for one enumeration and returns the token
// its callback receives.
func beginEnum(fn windowsEnumProc) uintptr {
	enumCalls.mu.Lock()
	defer enumCalls.mu.Unlock()
	if enumCalls.byToken == nil {
		enumCalls.byToken = map[uintptr]windowsEnumProc{}
	}
	enumCalls.next++
	enumCalls.byToken[enumCalls.next] = fn
	return enumCalls.next
}

func endEnum(token uintptr) {
	enumCalls.mu.Lock()
	delete(enumCalls.byToken, token)
	enumCalls.mu.Unlock()
}

// enumVisitor resolves the token an enumeration callback was handed. The
// token is only valid for the call that issued it, and the system never
// calls back after that call returns.
func enumVisitor(token uintptr) windowsEnumProc {
	enumCalls.mu.Lock()
	defer enumCalls.mu.Unlock()
	return enumCalls.byToken[token]
}

func enumWindowHandle(h, lParam uintptr) uintptr {
	if fn := enumVisitor(lParam); fn != nil && fn(h) {
		return 1
	}
	return 0
}

func enumMonitorHandle(handle, _, _, lParam uintptr) uintptr {
	if fn := enumVisitor(lParam); fn != nil && fn(handle) {
		return 1
	}
	return 0
}

// enumTopLevel visits the top-level windows front to back until fn reports
// done.
func enumTopLevel(fn windowsEnumProc) {
	if fn == nil {
		return
	}
	token := beginEnum(fn)
	defer endEnum(token)
	procEnumWindows.Call(enumWindowsProc, token)
}

// enumChildren visits parent's child windows until fn reports done.
func enumChildren(parent uintptr, fn windowsEnumProc) {
	if fn == nil {
		return
	}
	token := beginEnum(fn)
	defer endEnum(token)
	procEnumChildWindows.Call(parent, enumWindowsProc, token)
}

var procEnumChildWindows = sysProc(user32, "EnumChildWindows")

// --- displays ---------------------------------------------------------------

type screenMonitor struct {
	handle uintptr
	bounds winRect
	work   winRect
	dpi    int
	device string
	// target is the physical monitor's device interface path, "" when the
	// display configuration query has no answer for this output.
	target string
}

// displayTargetsTTL bounds how long an output's monitor path may be cached.
// The path identifies the monitor and does not change, but an output can be
// replugged onto another monitor at any time; a couple of seconds costs a
// stale layout key at most, while an uncached query would run on every DIP
// conversion.
const displayTargetsTTL = 2 * time.Second

var displayTargetsCache struct {
	mu      sync.Mutex
	expires time.Time
	paths   map[string]string
}

// displayTargetPaths maps each active output's GDI device name
// ("\\.\DISPLAY1") to the physical monitor's device interface path
// ("\\?\DISPLAY#DEL4098#5&...#{e6f07b5f-...}"). The GDI name numbers an
// adapter output, so it follows output enumeration rather than the monitor
// the user saved a layout for; the device path names the monitor.
func displayTargetPaths() map[string]string {
	displayTargetsCache.mu.Lock()
	defer displayTargetsCache.mu.Unlock()
	if displayTargetsCache.paths != nil && time.Now().Before(displayTargetsCache.expires) {
		return displayTargetsCache.paths
	}
	paths := queryDisplayTargetPaths()
	displayTargetsCache.paths = paths
	displayTargetsCache.expires = time.Now().Add(displayTargetsTTL)
	return paths
}

// queryDisplayTargetPaths reads the active outputs and their monitors from the
// display configuration. A session without a display configuration (a service,
// a headless remote session) yields an empty map, and displays then fall back
// to their GDI name.
func queryDisplayTargetPaths() map[string]string {
	// The display configuration can change between sizing the buffers and
	// querying them; the system then asks for a second attempt.
	for range 2 {
		var numPaths, numModes uint32
		if r, _, _ := procGetDisplayConfigBufferSizes.Call(qdcOnlyActivePaths,
			uintptr(unsafe.Pointer(&numPaths)), uintptr(unsafe.Pointer(&numModes))); int32(r) != 0 || numPaths == 0 {
			return nil
		}
		paths := make([]displayConfigPathInfo, numPaths)
		modes := make([]uint64, (displayConfigModeInfoSize*int(numModes)+7)/8)
		var modesPtr uintptr
		if len(modes) > 0 {
			modesPtr = uintptr(unsafe.Pointer(&modes[0]))
		}
		r, _, _ := procQueryDisplayConfig.Call(qdcOnlyActivePaths,
			uintptr(unsafe.Pointer(&numPaths)), uintptr(unsafe.Pointer(&paths[0])),
			uintptr(unsafe.Pointer(&numModes)), modesPtr, 0)
		switch int32(r) {
		case 0: // ERROR_SUCCESS
		case 122: // ERROR_INSUFFICIENT_BUFFER: re-enumerate and try again
			continue
		default:
			return nil
		}
		out := make(map[string]string, numPaths)
		for _, p := range paths[:numPaths] {
			src := displayConfigSourceDeviceName{header: displayConfigDeviceInfoHeader{
				deviceType: displayConfigGetSourceName,
				size:       uint32(unsafe.Sizeof(displayConfigSourceDeviceName{})),
				adapterID:  p.source.adapterID,
				id:         p.source.id,
			}}
			if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&src))); int32(r) != 0 {
				continue
			}
			tgt := displayConfigTargetDeviceName{header: displayConfigDeviceInfoHeader{
				deviceType: displayConfigGetTargetName,
				size:       uint32(unsafe.Sizeof(displayConfigTargetDeviceName{})),
				adapterID:  p.target.adapterID,
				id:         p.target.id,
			}}
			if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&tgt))); int32(r) != 0 {
				continue
			}
			name := windows.UTF16ToString(src.gdiName[:])
			if path := windows.UTF16ToString(tgt.devicePath[:]); name != "" && path != "" {
				out[name] = path
			}
		}
		return out
	}
	return nil
}

// enumMonitors lists the active displays. The fixed monitor callback reads
// this call's collector through the enumeration's application value, so no
// per-call Windows callback is created: those are permanent, and a query
// repeated a few thousand times would exhaust the runtime's callback slots.
func enumMonitors() []screenMonitor {
	var out []screenMonitor
	targets := displayTargetPaths()
	token := beginEnum(func(handle uintptr) bool {
		var mi monitorInfoEx
		mi.cbSize = uint32(unsafe.Sizeof(mi))
		if r, _, _ := procGetMonitorInfo.Call(handle, uintptr(unsafe.Pointer(&mi))); r == 0 {
			return true
		}
		dpi := 96
		var dx, dy uint32
		if r, _, _ := procGetDpiForMonitor.Call(handle, mdvEffDPI, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
			dpi = int(dx)
		}
		device := windows.UTF16ToString(mi.szDevice[:])
		out = append(out, screenMonitor{
			handle: handle,
			bounds: mi.rcMonitor,
			work:   mi.rcWork,
			dpi:    dpi,
			device: device,
			target: targets[device],
		})
		return true
	})
	defer endEnum(token)
	procEnumDisplayMonitors.Call(0, 0, enumMonitorsProc, token)
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
	best := monitorForPixels(ms, r)
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

// abs32 returns the magnitude of v.
func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// monitorForPixels picks the display a physical rectangle belongs to: the one
// containing its top-left corner, else the one it overlaps most, else the
// nearest to its centre. Choosing the display whose corner happened to be
// nearest put a window that starts beside a neighbouring display on the
// neighbour, so its position and size were scaled by the wrong DPI.
func monitorForPixels(ms []screenMonitor, r winRect) int {
	if len(ms) == 0 {
		return -1
	}
	for i, m := range ms {
		if pointInRect(r.Left, r.Top, m.bounds) {
			return i
		}
	}
	best, bestArea := -1, int64(0)
	for i, m := range ms {
		if a := overlapArea(m.bounds, r); a > bestArea {
			best, bestArea = i, a
		}
	}
	if best >= 0 {
		return best
	}
	// Off every display (a window dragged past the edge of the desktop):
	// the nearest one decides the scale.
	best, bestD := 0, int64(1)<<62
	cx := (int64(r.Left) + int64(r.Right)) / 2
	cy := (int64(r.Top) + int64(r.Bottom)) / 2
	for i, m := range ms {
		dx, dy := int64(0), int64(0)
		if cx < int64(m.bounds.Left) {
			dx = int64(m.bounds.Left) - cx
		} else if cx > int64(m.bounds.Right) {
			dx = cx - int64(m.bounds.Right)
		}
		if cy < int64(m.bounds.Top) {
			dy = int64(m.bounds.Top) - cy
		} else if cy > int64(m.bounds.Bottom) {
			dy = cy - int64(m.bounds.Bottom)
		}
		if d := dx*dx + dy*dy; d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func pointInRect(x, y int32, r winRect) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

// overlapArea is the area two rectangles share, 0 when they do not meet.
func overlapArea(a, b winRect) int64 {
	w := int64(min(a.Right, b.Right) - max(a.Left, b.Left))
	h := int64(min(a.Bottom, b.Bottom) - max(a.Top, b.Top))
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

// StableDisplayID returns a display's persistent identity: a hash of the
// physical monitor's device interface path, which survives a restart even
// though the HMONITOR mygo reports as Display.ID does not, and stays with the
// monitor when it is moved to another output (the GDI device name does not).
//
// A layout key an earlier build saved — the GDI device name's hash — resolves
// to the same display's current identity, so a saved layout can be migrated
// rather than lost. An unknown id is returned unchanged, so a stale saved key
// never matches a real display by accident.
func StableDisplayID(id int64) int64 {
	monitors := enumMonitors()
	for _, m := range monitors {
		if int64(m.handle) == id {
			return stableDisplayID(m)
		}
	}
	for _, m := range monitors {
		if legacyDisplayID(m) == id {
			return stableDisplayID(m)
		}
	}
	return id
}

// stableDisplayID hashes a display's persistent identity: its monitor device
// path, or its GDI device name on a session whose display configuration has
// no answer for the output.
func stableDisplayID(m screenMonitor) int64 {
	identity := m.target
	if identity == "" {
		identity = m.device
	}
	return displayIDHash(identity)
}

// legacyDisplayID is the GDI-device-name hash earlier builds saved as a
// display's layout key. Layouts saved then are still on disk, so a lookup
// accepts the legacy key as well as the current one.
func legacyDisplayID(m screenMonitor) int64 { return displayIDHash(m.device) }

// displayIDHash hashes a display identity into the positive int64 range. The
// high bit is cleared so a stored key never looks like a negative number in
// the settings JSON.
func displayIDHash(identity string) int64 {
	h := fnv.New64a()
	h.Write([]byte(identity))
	return int64(h.Sum64() &^ (1 << 63))
}

// findMonitor resolves either an HMONITOR or a stable display id to the
// monitor, so callers holding a saved id and callers holding mygo's raw id
// both reach the same display.
func findMonitor(displayID int64) (screenMonitor, int, bool) {
	return findMonitorIn(enumMonitors(), displayID)
}

// findMonitorIn resolves an id against an explicit display list: an
// HMONITOR first, then the current stable id, then the legacy one a layout
// saved before the monitor-path identity still carries.
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
	for i, m := range ms {
		if legacyDisplayID(m) == displayID {
			return m, i, true
		}
	}
	return screenMonitor{}, 0, false
}

// --- window enumeration -----------------------------------------------------

// windowFromPointAt asks the window manager which window is at a physical
// screen point. WindowFromPoint takes the point by value — the two
// coordinates packed into one argument on 64-bit Windows, two arguments on
// 32-bit — so handing it a pointer would hit whatever the pointer's bits
// decode to.
func windowFromPointAt(pt winPoint) uintptr {
	if unsafe.Sizeof(uintptr(0)) == 4 {
		r, _, _ := procWindowFromPoint.Call(uintptr(uint32(pt.X)), uintptr(uint32(pt.Y)))
		return r
	}
	r, _, _ := procWindowFromPoint.Call(uintptr(uint32(pt.X)) | uintptr(uint32(pt.Y))<<32)
	return r
}

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

// processExe is the on-disk image of a process, or "" when it cannot be
// opened. It is asked every time: a process id is not an identity, because
// Windows reuses the ids of exited processes, so a memo keyed by the id alone
// would report the old image for whatever process takes the id next — the
// frontmost app, the running-app list and a clipboard source would all name
// the wrong program.
func processExe(pid int) string {
	if pid <= 0 {
		return ""
	}
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
	return windows.UTF16ToString(buf[:size])
}

// processAUMID is the AppUserModelID of a packaged (Store/UWP) process, or
// "" for a desktop one. The answer is memoized by process and creation
// time: one package can publish several apps, so the AUMID is per process,
// and a packaged process's identity never changes — the once-a-second
// foreground poll must not reopen a handle and ask the system again.
func processAUMID(pid int) string {
	if pid <= 0 {
		return ""
	}
	h, _, _ := procOpenProcess.Call(0x1000 /* PROCESS_QUERY_LIMITED_INFORMATION */, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	key := aumidKey{pid: uint32(pid), created: processCreationTime(h)}
	aumidMu.Lock()
	id, ok := aumidCache[key]
	aumidMu.Unlock()
	if ok {
		return id
	}
	id = queryAUMID(h)
	aumidMu.Lock()
	if len(aumidCache) >= 4096 { // pids recycle; the cache must not grow forever
		aumidCache = map[aumidKey]string{}
	}
	aumidCache[key] = id
	aumidMu.Unlock()
	return id
}

type aumidKey struct {
	pid     uint32
	created uint64
}

var (
	aumidMu    sync.Mutex
	aumidCache = map[aumidKey]string{}
)

// processCreationTime is the process's start time, which keeps a recycled
// pid from inheriting the previous process's identity.
func processCreationTime(h uintptr) uint64 {
	var created, exited, kernel, user winFileTime
	if r, _, _ := procGetProcessTimes.Call(h, uintptr(unsafe.Pointer(&created)),
		uintptr(unsafe.Pointer(&exited)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); r == 0 {
		return 0
	}
	return uint64(created.high)<<32 | uint64(created.low)
}

// winFileTime is FILETIME: 100ns ticks since 1601, as two 32-bit halves.
type winFileTime struct {
	low  uint32
	high uint32
}

// queryAUMID asks the system for the handle's AppUserModelID.
func queryAUMID(h uintptr) string {
	proc := aumidProc()
	if proc == nil {
		return ""
	}
	size := uint32(512)
	buf := make([]uint16, size)
	r, _, _ := proc.Call(h, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0])))
	const errorInsufficientBuffer = 122
	if uint32(r) == errorInsufficientBuffer && int(size) > len(buf) {
		buf = make([]uint16, size)
		r, _, _ = proc.Call(h, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0])))
	}
	if r != 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

var (
	aumidOnce sync.Once
	aumidP    *windows.LazyProc
)

// aumidProc resolves the process AppUserModelID query without panicking on
// a system that does not export it: an absent query only means the app
// identity falls back to the executable path. The Windows export is
// GetApplicationUserModelId (lower-case d).
func aumidProc() *windows.LazyProc {
	aumidOnce.Do(func() {
		p := kernel32.NewProc("GetApplicationUserModelId")
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
