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
	"unsafe"
)

// ScreenWindow is one ordinary on-screen window: its handle as the number,
// its owner's process, and its bounds in public DIP coordinates, front to
// back.
type ScreenWindow struct {
	Number int
	PID    int
	Bounds Rect
}

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

// DisplayAsleep reports whether the display with this id is off: display
// sleep, the lock screen or a powered-down monitor. An id the system no
// longer knows counts as awake, matching the Mac answer.
func DisplayAsleep(display int64) bool {
	m, _, ok := findMonitor(display)
	if !ok {
		return false
	}
	return monitorAsleep(m.handle)
}

// MainDisplayAsleep reports whether the primary display is off.
func MainDisplayAsleep() bool {
	h, _, _ := procMonitorFromPoint.Call(0, 1 /* MONITOR_DEFAULTTOPRIMARY */)
	if h == 0 {
		return false
	}
	return monitorAsleep(h)
}

func monitorAsleep(handle uintptr) bool {
	var on int32
	r, _, _ := procGetDevicePowerState.Call(handle, uintptr(unsafe.Pointer(&on)))
	if r == 0 {
		// The query is not available for this display: report awake so no
		// animation is frozen on a guess.
		return false
	}
	return on == 0
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
		wr, ok := windowRect(h)
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

type windowsEnumProc func(h uintptr) bool

func enumTopLevel(fn windowsEnumProc) {
	cb := windowsEnumCallback(fn)
	procEnumWindows.Call(cb, 0)
}

func windowsEnumCallback(fn windowsEnumProc) uintptr {
	return newCallback(func(h, _ uintptr) uintptr {
		if fn(h) {
			return 1
		}
		return 0
	})
}

// ordinaryWindow reports whether h is an app window the user can see:
// visible, not a tool window, not this process's hidden owner, with a
// title (the desktop and the shell's helper windows have none).
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
	return windowText(h) != ""
}
