//go:build windows

package platform

// Display geometry: the simulated notch anchor, the taskbar-aware work
// area, the display sleep state, and the window lists the overlay uses.
//
// A Windows display has no camera housing. The shelf therefore anchors to
// a centred box at the top of the display (the 灵动岛 default), unless the
// user picked the notch style: then hasNotch is true and the shelf draws
// flush against the top edge, the way it does on a notched Mac. When the
// taskbar sits at the top of the display the box moves under it instead,
// so the island never hides behind the taskbar.

import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NotchRect returns the top-centre anchor for the display whose id is
// displayID, in the public DIP coordinates. A display with a simulated
// housing reports hasNotch; otherwise the caller gets the 180×32 island
// anchor with hasNotch=false. When no display answers for the id the
// result is empty, so no hot zone appears at (0,0).
func NotchRect(displayID int64) (x, y, w, h int, hasNotch bool) {
	return notchRectIn(enumMonitors(), displayID, simulatedNotch.Load())
}

// notchRectIn is NotchRect over an explicit display list, so the anchor
// arithmetic (centring, a top taskbar, the simulated housing) is
// unit-tested without a screen.
func notchRectIn(ms []screenMonitor, displayID int64, simulated bool) (x, y, w, h int, hasNotch bool) {
	m, idx, ok := findMonitorIn(ms, displayID)
	if !ok {
		return 0, 0, 0, 0, false
	}
	ox, oy := dipOrigin(ms, idx)
	ww := scaleBy(int(m.bounds.width()), 96, m.dpi)
	workY := scaleBy(int(m.work.Top-m.bounds.Top), 96, m.dpi)
	w, h = 180, 32
	x = ox + (ww-w)/2
	y = oy + workY
	// A taskbar at the top leaves no strip for a simulated housing; the
	// island then sits below it.
	if simulated && m.work.Top == m.bounds.Top {
		hasNotch = true
		y = oy
	}
	return x, y, w, h, hasNotch
}

// NotchTopInset is the top safe-area inset of the display: the simulated
// housing's height when the notch style is on and the taskbar leaves it
// room, otherwise 0.
func NotchTopInset(displayID int64) int {
	m, _, ok := findMonitor(displayID)
	if !ok {
		return 0
	}
	if simulatedNotch.Load() && m.work.Top == m.bounds.Top {
		return 32
	}
	return 0
}

// DisplayAsleep reports whether the user cannot see the display with this id:
// the console's displays are powered down, or the console is locked or
// switched away. An id the system no longer knows counts as awake, matching
// the Mac answer.
//
// Windows has no per-display power query (GetDevicePowerState takes a device
// handle, not an HMONITOR) and reports the console display state for all
// displays at once, so the state the power notification cached decides.
func DisplayAsleep(display int64) bool {
	if _, _, ok := findMonitor(display); !ok {
		return false
	}
	return displayAsleep()
}

// MainDisplayAsleep reports whether the primary display is dark.
func MainDisplayAsleep() bool {
	h, _, _ := procMonitorFromPoint.Call(0, 1 /* MONITOR_DEFAULTTOPRIMARY */)
	if h == 0 {
		return false
	}
	return displayAsleep()
}

// displayAsleep answers whether the screen is dark right now: the console's
// displays are off, or the session is locked. The lock screen is a desktop of
// its own that covers every window, and Windows gives no per-window answer
// for it, so a locked console counts as dark here — the desktop pet holds its
// pose instead of animating behind the lock.
func displayAsleep() bool {
	if !SessionActive() {
		return true
	}
	return displayPowerState() == powerDisplayOff
}

// powerDisplayOff/On/Dim are the values GUID_CONSOLE_DISPLAY_STATE reports.
const (
	powerDisplayOff = 0
	powerDisplayOn  = 1
	powerDisplayDim = 2
)

// displayPowerGUID is GUID_CONSOLE_DISPLAY_STATE: the console display's power
// state, the only notification Windows offers for a screen turning off.
var displayPowerGUID = windows.GUID{
	Data1: 0x6fe69556,
	Data2: 0x704a,
	Data3: 0x47a0,
	Data4: [8]byte{0x8f, 0x24, 0xc2, 0x8d, 0x93, 0x6f, 0xda, 0x47},
}

const (
	deviceNotifyCallback = 0x00000002
	// PBT_POWERSETTINGCHANGE, the WM_POWERBROADCAST code a
	// POWER_SETTING_REGISTER_NOTIFICATION callback is invoked with.
	pbtPowerSettingChange = 0x8013
)

// powerBroadcastSetting mirrors POWERBROADCAST_SETTING: the setting's GUID,
// its data's length, and then the data itself inline — a DWORD for the console
// display state. The data is part of the structure, not a pointer to it, so it
// is read at offset 20 rather than dereferenced.
type powerBroadcastSetting struct {
	setting    windows.GUID
	dataLength uint32
	data       uint32
}

// deviceNotifySubscribeParams mirrors DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS, the
// recipient a DEVICE_NOTIFY_CALLBACK subscription names.
type deviceNotifySubscribeParams struct {
	callback uintptr
	context  uintptr
}

var displayPower = struct {
	once   sync.Once
	err    error
	state  atomic.Uint32
	params deviceNotifySubscribeParams
	notify uintptr
}{}

// displayPowerCallbackAddr is the subscription's fixed callback: Windows freezes
// a callback for the life of the process, so it must not be created per call.
var displayPowerCallbackAddr = windows.NewCallback(displayPowerCallback)

// displayPowerCallback stores the display state the system reports. It runs on
// a system thread, so it does nothing else.
func displayPowerCallback(_, event, setting uintptr) uintptr {
	if uint32(event) == pbtPowerSettingChange && setting != 0 {
		change := powerBroadcastSettingAt(setting)
		if change.setting == displayPowerGUID && change.dataLength >= 4 && change.data <= powerDisplayDim {
			displayPower.state.Store(change.data)
		}
	}
	return 0
}

// powerBroadcastSettingAt reads the setting the notification handed over. The
// value comes from the system's own stack frame, so the uintptr-to-pointer
// conversion is safe for the callback's duration.
func powerBroadcastSettingAt(p uintptr) *powerBroadcastSetting {
	var v unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&v)) = p
	return (*powerBroadcastSetting)(v)
}

// ensureDisplayPower subscribes the process to the console display state. One
// subscription serves the whole process; a system that refuses it leaves the
// display reported awake, so no animation is frozen on a guess.
func ensureDisplayPower() {
	displayPower.once.Do(func() {
		displayPower.state.Store(powerDisplayOn)
		displayPower.params = deviceNotifySubscribeParams{callback: displayPowerCallbackAddr}
		r, _, err := procPowerSettingRegisterNotification.Call(
			uintptr(unsafe.Pointer(&displayPowerGUID)), deviceNotifyCallback,
			uintptr(unsafe.Pointer(&displayPower.params)), uintptr(unsafe.Pointer(&displayPower.notify)))
		if int32(r) != 0 {
			displayPower.err = fmt.Errorf("platform: PowerSettingRegisterNotification: %w", err)
			return
		}
	})
}

// displayPowerState answers the console display state the notification last
// reported: on until the system says otherwise, because a display that can
// run this process is on when the process starts.
func displayPowerState() uint32 {
	ensureDisplayPower()
	return displayPower.state.Load()
}

// WindowNumber returns the window's identity in the window list: the
// native handle.
func WindowNumber(window uintptr) int { return int(window) }

// WindowScreenRect returns the on-screen rectangle of the window whose
// number is num, in public DIP coordinates.
func WindowScreenRect(num int) (r Rect, ok bool) {
	h := uintptr(num)
	if h == 0 {
		return Rect{}, false
	}
	wr, ok := windowRect(h)
	if !ok {
		return Rect{}, false
	}
	return pixelsToDIP(wr), true
}

// NormalWindows lists the ordinary visible windows of other processes,
// frontmost first. Windows enumerates in z-order, top first, which is the
// order the screenshot picker needs.
func NormalWindows() []ScreenWindow {
	var out []ScreenWindow
	self := ownProcessID()
	cb := windowsEnumProc(func(h uintptr) bool {
		if windowPID(h) == self {
			return true
		}
		if !ordinaryWindow(h) {
			return true
		}
		wr, ok := windowFrameRect(h)
		if !ok {
			return true
		}
		out = append(out, ScreenWindow{Number: int(h), PID: windowPID(h), Bounds: pixelsToDIP(wr)})
		return true
	})
	enumTopLevel(cb)
	return out
}

// WindowsOfPID lists every visible window of pid, at any style, frontmost
// first.
func WindowsOfPID(pid int) []ScreenWindow {
	var out []ScreenWindow
	cb := windowsEnumProc(func(h uintptr) bool {
		if windowPID(h) != pid {
			return true
		}
		if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
			return true
		}
		wr, ok := windowRect(h)
		if !ok {
			return true
		}
		out = append(out, ScreenWindow{Number: int(h), PID: pid, Bounds: pixelsToDIP(wr)})
		return true
	})
	enumTopLevel(cb)
	return out
}

// ordinaryWindow reports whether h is an app window the user can see:
// visible, not a tool window, not cloaked onto another virtual desktop or
// behind a suspended packaged app, not one of the desktop's own surfaces,
// with a title (the desktop and the shell's helper windows have none).
func ordinaryWindow(h uintptr) bool {
	if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
		return false
	}
	if isToolWindow(h) {
		return false
	}
	if r, _, _ := procIsIconic.Call(h); r != 0 {
		return false
	}
	if shellWindows[windowClass(h)] || windowCloaked(h) {
		return false
	}
	return windowText(h) != ""
}

// DisplayFriendlyNames returns each active monitor's user-facing name keyed by
// its stable display id.
func DisplayFriendlyNames() map[int64]string {
	return friendlyNamesIn(enumMonitors())
}

func friendlyNamesIn(ms []screenMonitor) map[int64]string {
	if len(ms) == 0 {
		return nil
	}
	out := make(map[int64]string, len(ms))
	for _, m := range ms {
		if m.friendly != "" {
			out[stableDisplayID(m)] = m.friendly
		}
	}
	return out
}
