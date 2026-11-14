//go:build windows

package platform

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The system's popup menus are windows of this class, owned by the window
// they were opened from. The overlay's content is composited above the
// window stack, so a menu opened over the panel is painted over wherever
// the panel draws (its page and capsule), hiding the menu's first items.
const popupMenuClass = "#32768"

// ownMenus is the watch's state: the overlay the menus belong to, its UI
// thread, and the menus already moved. The overlay's handle comes from the
// app once the window exists; nothing here guesses which window it is.
var ownMenus struct {
	once   sync.Once
	mu     sync.Mutex
	moved  map[uintptr]bool
	window uintptr // the overlay window, once the app has created it
	thread uint32  // that window's UI thread
}

const (
	guiInMenuMode     = 0x00000004
	guiSystemMenuMode = 0x00000008
	guiPopupMenuMode  = 0x00000010
)

// WatchOwnMenus keeps the system menus this process shows clear of the
// overlay window. A menu opened over the panel is moved just outside the
// panel's window, at the row it was opened from, where nothing of the
// overlay paints over it. Menus of other windows are left where the system
// put them.
//
// window is the overlay's own handle: the watch asks that window's UI
// thread whether a menu loop is running and compares the menus with that
// one rectangle, so an idle overlay costs a thread query every tick and a
// collapsed capsule is measured as what it is. A zero handle (or none) does
// nothing, so the call is a no-op until the app has its window.
func WatchOwnMenus(window uintptr) {
	ownMenus.mu.Lock()
	ownMenus.window = window
	if window != 0 {
		ownMenus.thread = uint32(windowThread(window))
	}
	if ownMenus.moved == nil {
		ownMenus.moved = map[uintptr]bool{}
	}
	ownMenus.mu.Unlock()
	if window == 0 {
		return
	}
	ownMenus.once.Do(func() {
		go func() {
			for {
				time.Sleep(20 * time.Millisecond)
				placeOwnMenus()
			}
		}()
	})
}

// overlayRect is the overlay's window rectangle, empty until the app hands
// the watch its handle.
func overlayRect() (winRect, bool) {
	ownMenus.mu.Lock()
	window := ownMenus.window
	ownMenus.mu.Unlock()
	if window == 0 {
		return winRect{}, false
	}
	return windowRect(window)
}

func placeOwnMenus() {
	if !uiThreadInMenu() {
		return
	}
	panel, hasPanel := overlayRect()
	if !hasPanel {
		return
	}
	pid := windows.GetCurrentProcessId()
	monitors := enumMonitors()
	enumTopLevel(func(h uintptr) bool {
		var class [16]uint16
		n, _, _ := procGetClassName.Call(h, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
		if n == 0 || windows.UTF16ToString(class[:n]) != popupMenuClass {
			return true
		}
		if windowPID(h) != int(pid) {
			return true
		}
		ownMenus.mu.Lock()
		moved := ownMenus.moved[h]
		ownMenus.mu.Unlock()
		if moved {
			return true
		}
		r, has := windowRect(h)
		if !has || !overlaps(r, panel) {
			return true
		}
		ownMenus.mu.Lock()
		if len(ownMenus.moved) > 32 {
			ownMenus.moved = map[uintptr]bool{}
		}
		ownMenus.moved[h] = true
		ownMenus.mu.Unlock()
		x, y := besidePanel(r, panel, monitors)
		procSetWindowPos.Call(h, 0, uintptr(x), uintptr(y), 0, 0,
			swpNoSize|swpNoZOrder|swpNoActivate)
		return true
	})
}

// uiThreadInMenu reports whether the overlay's thread runs a popup menu
// loop right now: the cheap answer that keeps the pass from enumerating
// the desktop fifty times a second for nothing.
func uiThreadInMenu() bool {
	ownMenus.mu.Lock()
	window, tid := ownMenus.window, ownMenus.thread
	ownMenus.mu.Unlock()
	if window == 0 {
		return false
	}
	if tid == 0 {
		if tid = uint32(windowThread(window)); tid == 0 {
			return false
		}
		ownMenus.mu.Lock()
		ownMenus.thread = tid
		ownMenus.mu.Unlock()
	}
	var info guiThreadInfo
	info.cbSize = uint32(unsafe.Sizeof(info))
	if ok, _, _ := procGetGUIThreadInfo.Call(uintptr(tid), uintptr(unsafe.Pointer(&info))); ok == 0 {
		return false
	}
	return info.flags&(guiInMenuMode|guiSystemMenuMode|guiPopupMenuMode) != 0
}

func overlaps(a, b winRect) bool {
	return a.Left < b.Right && a.Right > b.Left && a.Top < b.Bottom && a.Bottom > b.Top
}

// monitorWorkAt answers the work area of the monitor holding the point,
// and whether a monitor holds it at all.
func monitorWorkAt(monitors []screenMonitor, x, y int32) (winRect, bool) {
	for _, m := range monitors {
		if x >= m.bounds.Left && x < m.bounds.Right && y >= m.bounds.Top && y < m.bounds.Bottom {
			return m.work, true
		}
	}
	return winRect{}, false
}

// besidePanel places a menu just right of the panel, at the height it was
// opened at, or left of it when the screen has no room on the right. The
// height is kept inside the work area of the monitor the menu opened on —
// a monitor above the primary one has a negative top, so nothing here
// assumes zero — and a menu the watch cannot place on any monitor is left
// where the system put it.
func besidePanel(menu, panel winRect, monitors []screenMonitor) (int32, int32) {
	y := menu.Top
	if work, ok := monitorWorkAt(monitors, menu.Left, menu.Top); ok {
		if height := menu.height(); y+height > work.Bottom-8 {
			y = work.Bottom - 8 - height
		}
		if y < work.Top+8 {
			y = work.Top + 8
		}
		width := menu.width()
		if room := work.Right - (panel.Right + 8); room >= width {
			return panel.Right + 8, y
		}
		if room := (panel.Left - 8) - work.Left; room >= width {
			return panel.Left - 8 - width, y
		}
	}
	return menu.Left, y
}
