//go:build darwin

package platform

import "github.com/ebitengine/purego/objc"

// ObserveMouseDown observes all mouse buttons without consuming events.
// clicked(true, button) is a press in window; clicked(false, button) a
// press in another app or on the desktop (button is the NSEvent
// buttonNumber: 0 is left). Presses in this app's other windows (the
// Quick Look panel) are neither and are not reported. Global mouse-only
// monitors require no Accessibility permission. Local events are returned
// unchanged. Install and stop on the AppKit main thread.
func ObserveMouseDown(window uintptr, clicked func(inside bool, button int)) (stop func()) {
	const mask = 1<<1 | 1<<3 | 1<<25
	globalBlock := objc.NewBlock(func(_ objc.Block, event id) {
		clicked(false, sendInt(event, "buttonNumber"))
	})
	defer globalBlock.Release()
	localBlock := objc.NewBlock(func(_ objc.Block, event id) id {
		if uintptr(send(event, "window")) == window {
			clicked(true, sendInt(event, "buttonNumber"))
		}
		return event
	})
	defer localBlock.Release()
	global := send(send(class("NSEvent"), "addGlobalMonitorForEventsMatchingMask:handler:", mask, uintptr(globalBlock)), "retain")
	local := send(send(class("NSEvent"), "addLocalMonitorForEventsMatchingMask:handler:", mask, uintptr(localBlock)), "retain")
	return func() {
		if global != 0 {
			send(class("NSEvent"), "removeMonitor:", uintptr(global))
			send(global, "release")
			global = 0
		}
		if local != 0 {
			send(class("NSEvent"), "removeMonitor:", uintptr(local))
			send(local, "release")
			local = 0
		}
	}
}

// ObserveLeftMouseDragged sees file drags in other apps (Finder included)
// without Accessibility. The first dragged event is when the overlay must
// become a drop target — polling 30 ms later is enough for Spaces to win
// the top edge. Install and stop on the AppKit main thread.
func ObserveLeftMouseDragged(moved func()) (stop func()) {
	const mask = 1 << 6 // NSEventTypeLeftMouseDragged
	globalBlock := objc.NewBlock(func(_ objc.Block, _ id) { moved() })
	defer globalBlock.Release()
	localBlock := objc.NewBlock(func(_ objc.Block, event id) id {
		moved()
		return event
	})
	defer localBlock.Release()
	global := send(send(class("NSEvent"), "addGlobalMonitorForEventsMatchingMask:handler:", mask, uintptr(globalBlock)), "retain")
	local := send(send(class("NSEvent"), "addLocalMonitorForEventsMatchingMask:handler:", mask, uintptr(localBlock)), "retain")
	return func() {
		if global != 0 {
			send(class("NSEvent"), "removeMonitor:", uintptr(global))
			send(global, "release")
			global = 0
		}
		if local != 0 {
			send(class("NSEvent"), "removeMonitor:", uintptr(local))
			send(local, "release")
			local = 0
		}
	}
}
