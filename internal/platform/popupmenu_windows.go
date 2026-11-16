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

// gwOwner is GW_OWNER of GetWindow: the window a popup or a dialog belongs
// to, which a menu's window names as the window it was opened from.
const gwOwner = 4

var procGetWindow = user32.NewProc("GetWindow")

// ownMenus is the watch's state: the overlay the menus belong to, its UI
// thread, and the menus already raised. The overlay's handle comes from the
// app once the window exists; nothing here guesses which window it is.
var ownMenus struct {
	once   sync.Once
	mu     sync.Mutex
	raised map[uintptr]bool
	window uintptr // the overlay window, once the app has created it
	thread uint32  // that window's UI thread
}

const (
	guiInMenuMode     = 0x00000004
	guiSystemMenuMode = 0x00000008
	guiPopupMenuMode  = 0x00000010
)

// WatchOwnMenus keeps the system menus this process shows above the overlay
// window. The menu keeps the place the system gave it — at the pointer — and
// the watch only moves it into the topmost band, above the panel's content.
// Menus of other windows, and of this process's other windows, are left as
// the system drew them.
//
// window is the overlay's own handle: the watch asks that window's UI
// thread whether a menu loop is running and follows the menu's owner chain
// back to that window, so an idle overlay costs a thread query every tick
// and a menu of the Settings window is not touched. A zero handle (or none)
// does nothing, so the call is a no-op until the app has its window.
func WatchOwnMenus(window uintptr) {
	ownMenus.mu.Lock()
	ownMenus.window = window
	if window != 0 {
		ownMenus.thread = uint32(windowThread(window))
	}
	if ownMenus.raised == nil {
		ownMenus.raised = map[uintptr]bool{}
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

// overlayWindow is the overlay's window handle, zero until the app hands
// the watch one.
func overlayWindow() uintptr {
	ownMenus.mu.Lock()
	defer ownMenus.mu.Unlock()
	return ownMenus.window
}

// raiseOwnMenus lifts the process's open popup menu above the overlay. The
// menu is topmost for as long as it shows: a menu is dismissed by its loop,
// which destroys the window, and a submenu is a menu window of its own that
// the watch raises when it appears.
func raiseOwnMenus() {
	if !uiThreadInMenu() {
		return
	}
	overlay := overlayWindow()
	if overlay == 0 {
		return
	}
	pid := windows.GetCurrentProcessId()
	enumTopLevel(func(h uintptr) bool {
		if !isPopupMenu(h) || windowPID(h) != int(pid) {
			return true
		}
		if !belongsTo(h, overlay) || alreadyRaised(h) {
			return true
		}
		procSetWindowPos.Call(h, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		return true
	})
}

// isPopupMenu reports whether h is one of the system's menu windows.
func isPopupMenu(h uintptr) bool {
	var class [16]uint16
	n, _, _ := procGetClassName.Call(h, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	return n != 0 && windows.UTF16ToString(class[:n]) == popupMenuClass
}

// belongsTo walks the owner chain of h — a menu and its submenus name the
// window they were opened from — and reports whether owner is on it.
func belongsTo(h, owner uintptr) bool {
	for depth := 0; depth < 8 && h != 0; depth++ {
		if h == owner {
			return true
		}
		h, _, _ = procGetWindow.Call(h, gwOwner)
	}
	return false
}

// alreadyRaised marks h as lifted and reports whether it was marked before:
// one SetWindowPos per menu window, however long the menu stays open.
func alreadyRaised(h uintptr) bool {
	ownMenus.mu.Lock()
	defer ownMenus.mu.Unlock()
	if ownMenus.raised[h] {
		return true
	}
	if len(ownMenus.raised) > 32 {
		ownMenus.raised = map[uintptr]bool{}
	}
	ownMenus.raised[h] = true
	return false
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
