//go:build windows

package platform

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The system's popup menus are windows of this class, owned by the window
// they were opened from. A menu opened over the topmost overlay is owned by
// it but is not itself topmost, so the overlay's content is composited
// above it and hides the menu's first items. The watch lifts the menu into
// the topmost band instead of moving it: the system placed it at the
// pointer, where the user expects it, and a mouse or a finger is already
// there.
const popupMenuClass = "#32768"

var procGetWindow = user32.NewProc("GetWindow")

// ownMenus is the watch's state: the overlay, whose UI thread runs every
// menu loop this app opens (mygo keeps all its windows on one thread). The
// handle comes from the app once the window exists; nothing here guesses
// which window it is.
var ownMenus struct {
	once   sync.Once
	mu     sync.Mutex
	window uintptr // the overlay window, once the app has created it
	thread uint32  // that window's UI thread
}

const (
	guiInMenuMode     = 0x00000004
	guiSystemMenuMode = 0x00000008
	guiPopupMenuMode  = 0x00000010
)

// WatchOwnMenus keeps the system menus this process shows above its topmost
// windows — the panel, pins, the note, a screenshot pin. The menu keeps the
// place the system gave it (at the pointer) and the watch only moves it into
// the topmost band, above the surface it was opened from. Menus of other
// processes are left as the system drew them.
//
// window is the overlay's own handle: the watch asks its UI thread whether a
// menu loop is running, so an idle app costs one thread query every tick.
// A zero handle (or none) does nothing, so the call is a no-op until the app
// has its window.
func WatchOwnMenus(window uintptr) {
	ownMenus.mu.Lock()
	ownMenus.window = window
	if window != 0 {
		ownMenus.thread = uint32(windowThread(window))
	}
	ownMenus.mu.Unlock()
	if window == 0 {
		return
	}
	ownMenus.once.Do(func() {
		go func() {
			for {
				time.Sleep(20 * time.Millisecond)
				raiseOwnMenus()
			}
		}()
	})
}

// raiseOwnMenus lifts this process's open popup menus into the topmost band.
// A menu is topmost for as long as it shows: its loop destroys the window
// when it is dismissed, and a submenu is a menu window of its own that the
// watch raises when it appears. Every surface of this app is topmost, so
// the menu's owner chain is not what decides: the menu of a pin or a note
// window needs the same lift as the panel's.
func raiseOwnMenus() {
	if !uiThreadInMenu() {
		return
	}
	pid := windows.GetCurrentProcessId()
	enumTopLevel(func(h uintptr) bool {
		if !isPopupMenu(h) || windowPID(h) != int(pid) {
			return true
		}
		raiseMenu(h)
		return true
	})
}

// raiseMenu moves one menu window into the topmost band and reports whether
// it had to. The window's own style is the record, not a cache of handles:
// a menu is destroyed after it closes, and Windows reuses window handles, so
// a remembered handle could name a later menu and leave it under the panel.
func raiseMenu(h uintptr) bool {
	if h == 0 || windowExStyle(h)&wsExTopmost != 0 {
		return false
	}
	procSetWindowPos.Call(h, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	return true
}

// isPopupMenu reports whether h is one of the system's menu windows.
func isPopupMenu(h uintptr) bool {
	var class [16]uint16
	n, _, _ := procGetClassName.Call(h, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	return n != 0 && windows.UTF16ToString(class[:n]) == popupMenuClass
}

// MenuOpen reports whether window's UI thread is currently running a system
// popup menu loop (TrackPopupMenuEx).
func MenuOpen(window uintptr) bool {
	if window == 0 {
		return uiThreadInMenu()
	}
	tid := uint32(windowThread(window))
	if tid == 0 {
		return false
	}
	var info guiThreadInfo
	info.cbSize = uint32(unsafe.Sizeof(info))
	if ok, _, _ := procGetGUIThreadInfo.Call(uintptr(tid), uintptr(unsafe.Pointer(&info))); ok == 0 {
		return false
	}
	return info.flags&(guiInMenuMode|guiSystemMenuMode|guiPopupMenuMode) != 0
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
