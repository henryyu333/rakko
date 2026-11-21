//go:build windows

package platform

import (
	"testing"
	"unsafe"
)

// TestDragGuardDoesNotMoveCursor pins the Windows corridor to a no-op: the
// calls are accepted, the stop is idempotent, and the pointer stays where
// the user left it. Only the cursor is observed, so a busy machine cannot
// turn scheduling into a failure. A session without a desktop (the SSH one
// the tests usually run in) has no cursor to read and skips.
func TestDragGuardDoesNotMoveCursor(t *testing.T) {
	var before winPoint
	if res, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&before))); res == 0 {
		t.Skip("this session has no desktop cursor to observe")
	}

	r := DragGuardRegion{X: 0, Y: 0, Width: 2000, Height: 2000}
	stop, err := StartDragGuard(r)
	if err != nil {
		t.Fatalf("StartDragGuard: %v", err)
	}
	if stop == nil {
		t.Fatal("StartDragGuard returned nil stop")
	}
	SetDragGuardRegion(DragGuardRegion{X: 100, Y: 0, Width: 500, Height: 48})
	ParkDragGuard(false)
	SetDragPin(r, true)
	SetDragPin(DragGuardRegion{}, false)
	stop()
	stop() // idempotent

	var after winPoint
	if res, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&after))); res == 0 {
		t.Fatal("the drag guard left GetCursorPos unreadable")
	}
	if after != before {
		t.Fatalf("drag guard moved cursor from %+v to %+v", before, after)
	}
}

// A Windows drag declares no formats before it reaches one of our windows,
// so shaking must not summon the basket for it: only a caller that already
// knows the drag carries files or images does.
func TestDragMayCarryFilesNeedsDeclaredFiles(t *testing.T) {
	for _, types := range [][]string{nil, {"public.utf8-plain-text"}, {"text/plain"}} {
		if DragMayCarryFiles(types) {
			t.Fatalf("%v must not wake the basket", types)
		}
	}
	for _, types := range [][]string{{"CF_HDROP"}, {"image/png"}} {
		if !DragMayCarryFiles(types) {
			t.Fatalf("%v must wake the basket", types)
		}
	}
}
