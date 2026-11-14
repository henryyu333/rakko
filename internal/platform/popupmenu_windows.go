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

var ownMenus struct {
	once   sync.Once
	mu     sync.Mutex
	moved  map[uintptr]bool
	thread uint32 // the overlay's UI thread, once found
}

const (
	guiInMenuMode     = 0x00000004
	guiSystemMenuMode = 0x00000008
	guiPopupMenuMode  = 0x00000010
)

// WatchOwnMenus keeps the system menus this process shows clear of the
// overlay. A menu opened over the panel is moved just outside the panel's
// window, at the row it was opened from, where nothing of the overlay
// paints over it. Menus of other windows are left where the system put
// them.
func WatchOwnMenus() {
	ownMenus.once.Do(func() {
		ownMenus.moved = map[uintptr]bool{}
		go func() {
			for {
				time.Sleep(20 * time.Millisecond)
				placeOwnMenus()
			}
		}()
	})
}

// overlayHandle is the panel's window handle: the only window of this
// process this tall.
func overlayHandle() (uintptr, bool) {
	pid := windows.GetCurrentProcessId()
	var found uintptr
	enumTopLevel(func(h uintptr) bool {
		var class [16]uint16
		n, _, _ := procGetClassName.Call(h, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
		if n == 0 || windows.UTF16ToString(class[:n]) != "MyGoWindow" {
			return true
		}
		if windowPID(h) != int(pid) {
			return true
		}
		r, has := windowRect(h)
		if !has || r.height() < 650 {
			return true
		}
		found = h
		return true
	})
	return found, found != 0
}

// overlayRect is the panel's window: the overlay keeps one size for the
// search bar, the pages and the closed capsule, and it is the only window
// this tall.
func overlayRect() (winRect, bool) {
	pid := windows.GetCurrentProcessId()
	var found winRect
	ok := false
	enumTopLevel(func(h uintptr) bool {
		var class [16]uint16
		n, _, _ := procGetClassName.Call(h, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
		if n == 0 || windows.UTF16ToString(class[:n]) != "MyGoWindow" {
			return true
		}
		if windowPID(h) != int(pid) {
			return true
		}
		r, has := windowRect(h)
		if !has || r.height() < 650 {
			return true
		}
		found, ok = r, true
		return true
	})
	return found, ok
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
		if !has || !hasPanel || !overlaps(r, panel) {
			return true
		}
		ownMenus.mu.Lock()
		if len(ownMenus.moved) > 32 {
			ownMenus.moved = map[uintptr]bool{}
		}
		ownMenus.moved[h] = true
		ownMenus.mu.Unlock()
		x, y := besidePanel(r, panel)
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
	tid := ownMenus.thread
	ownMenus.mu.Unlock()
	if tid == 0 {
		panel, ok := overlayHandle()
		if !ok {
			return false
		}
		tidRaw, _, _ := procGetWindowThreadPID.Call(panel, 0)
		tid = uint32(tidRaw)
		if tid == 0 {
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

// besidePanel places a menu just right of the panel, at the height it was
// opened at, or left of it when the screen has no room on the right.
func besidePanel(menu, panel winRect) (int32, int32) {
	height := menu.height()
	y := menu.Top
	if y < 8 {
		y = 8
	}
	var work winRect
	for _, m := range enumMonitors() {
		if menu.Left >= m.bounds.Left && menu.Left < m.bounds.Right &&
			menu.Top >= m.bounds.Top && menu.Top < m.bounds.Bottom {
			work = m.work
			break
		}
	}
	if work.Right > work.Left && y+height > work.Bottom-8 {
		y = work.Bottom - 8 - height
	}
	if y < work.Top+8 {
		y = work.Top + 8
	}
	if room := work.Right - (panel.Right + 8); room >= menu.Right-menu.Left {
		return panel.Right + 8, y
	}
	if room := (panel.Left - 8) - work.Left; room >= menu.Right-menu.Left {
		return panel.Left - 8 - (menu.width()), y
	}
	return menu.Left, y
}
