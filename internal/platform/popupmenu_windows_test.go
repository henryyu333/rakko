//go:build windows

package platform

// The menu watch lifts a menu into the topmost band through the menu's own
// style bit, never a cache of handles: a menu is destroyed when it closes
// and Windows reuses window handles, so a remembered handle could name the
// next menu and leave it hidden under the panel.

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestMenuWatchRaisesByTopmostBit(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("rakko-menu-test")
	h, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		0x80000000 /* WS_POPUP */, 0, 0, 120, 40, 0, 0, 0, 0)
	if h == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	defer procDestroyWindow.Call(h)

	if windowExStyle(h)&wsExTopmost != 0 {
		t.Fatal("a fresh window must not be topmost")
	}
	if !raiseMenu(h) {
		t.Fatal("a menu outside the topmost band was not lifted")
	}
	if windowExStyle(h)&wsExTopmost == 0 {
		t.Fatal("the lifted menu is not topmost")
	}
	if raiseMenu(h) {
		t.Fatal("a menu already in the topmost band was re-ordered")
	}
	if raiseMenu(0) {
		t.Fatal("a zero handle reported a raise")
	}
}
