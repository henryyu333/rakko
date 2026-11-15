//go:build windows

package platform

// The menu watch lifts a menu into the topmost band once, and only once per
// menu window: a menu stays up while the pointer moves over it, and a
// SetWindowPos every tick would ask DWM to re-order a window that already
// sits where it belongs.

import "testing"

func TestMenuWatchRaisesEachMenuOnce(t *testing.T) {
	ownMenus.mu.Lock()
	ownMenus.raised = map[uintptr]bool{}
	ownMenus.mu.Unlock()
	t.Cleanup(func() {
		ownMenus.mu.Lock()
		ownMenus.raised = nil
		ownMenus.mu.Unlock()
	})

	if alreadyRaised(0x1234) {
		t.Fatal("a menu watched for the first time reported itself raised")
	}
	if !alreadyRaised(0x1234) {
		t.Fatal("the same menu was raised twice")
	}
	if alreadyRaised(0x5678) {
		t.Fatal("a second menu was not raised")
	}
}

// The watch forgets the menus it raised once the map outgrows its bound, so
// a long session does not keep every menu window it ever showed.
func TestMenuWatchForgetsOldMenus(t *testing.T) {
	ownMenus.mu.Lock()
	ownMenus.raised = map[uintptr]bool{}
	for i := uintptr(1); i <= 40; i++ {
		ownMenus.raised[i] = true
	}
	ownMenus.mu.Unlock()
	t.Cleanup(func() {
		ownMenus.mu.Lock()
		ownMenus.raised = nil
		ownMenus.mu.Unlock()
	})

	if alreadyRaised(0x9001) {
		t.Fatal("the new menu was not raised")
	}
	ownMenus.mu.Lock()
	n := len(ownMenus.raised)
	ownMenus.mu.Unlock()
	if n != 1 {
		t.Fatalf("raised map kept %d menus, want 1 after the reset", n)
	}
}
