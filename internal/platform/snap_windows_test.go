//go:build windows

package platform

// Snap calls against a real window this process creates: the headless
// Windows session can still make one, so the hit test, the frame read and
// the arrangement are exercised without a desktop.

import (
	"math"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestAppWindowAtAndMove(t *testing.T) {
	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("rakko-snap-test")
	// WS_OVERLAPPEDWINDOW: caption, thick frame, system menu.
	const wsOverlappedWindow = 0x00CF0000
	h, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow, 200, 180, 420, 260, 0, 0, 0, 0)
	if h == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	defer procDestroyWindow.Call(h)
	procShowWindow.Call(h, 4 /* SW_SHOWNOACTIVATE */)
	// The shared desktop may have topmost tools over the test spot: put
	// this window at the front of the topmost band, where it was created.
	procSetWindowPos.Call(h, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpShowWindow)
	if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
		// A logon session with no attached desktop (an SSH-only test run)
		// cannot show or hit-test a window: the call is exercised by the
		// same test through rw desk, where the desktop is real.
		t.Skip("no interactive desktop: the test window cannot be shown")
	}

	frame, ok := WindowScreenRect(WindowNumber(h))
	if !ok {
		t.Fatal("WindowScreenRect of the test window failed")
	}
	cx, cy := frame.X+frame.Width/2, frame.Y+frame.Height/2
	// Other topmost tools on the shared desktop may sit over the spot; keep
	// raising ours until the hit test lands on it, as a press would.
	var w AppWindow
	var pid int
	var found bool
	for attempt := range 30 {
		procSetWindowPos.Call(h, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpShowWindow)
		if w, pid, found = AppWindowAt(cx, cy); found && pid == OwnPID() {
			break
		}
		if w.Valid() {
			w.Release()
		}
		if attempt == 29 {
			t.Fatalf("AppWindowAt(%v, %v) kept hitting another app (pid %d, found=%v)", cx, cy, pid, found)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer w.Release()
	if got, ok := w.Frame(); !ok || math.Abs(got.X-frame.X) > 2 || math.Abs(got.Y-frame.Y) > 2 {
		t.Fatalf("AppWindow.Frame = %+v, %v; want %+v", got, ok, frame)
	}
	move, resize := w.Settable()
	if !move || !resize {
		t.Fatalf("Settable = %v, %v; want true true for an overlapped window", move, resize)
	}
	want := Rect{X: frame.X + 40, Y: frame.Y + 30, Width: 300, Height: 200}
	if err := w.MoveResize(want, true); err != nil {
		t.Fatalf("MoveResize: %v", err)
	}
	got, ok := w.Frame()
	if !ok || math.Abs(got.X-want.X) > 2 || math.Abs(got.Y-want.Y) > 2 ||
		math.Abs(got.Width-want.Width) > 4 || math.Abs(got.Height-want.Height) > 4 {
		t.Fatalf("after MoveResize the frame is %+v, %v; want %+v", got, ok, want)
	}
	// A move without a resize must leave the size alone.
	moved := Rect{X: want.X + 20, Y: want.Y + 10, Width: 90, Height: 90}
	if err := w.MoveResize(moved, false); err != nil {
		t.Fatalf("MoveResize without resize: %v", err)
	}
	got, ok = w.Frame()
	if !ok || math.Abs(got.X-moved.X) > 2 || math.Abs(got.Height-want.Height) > 4 {
		t.Fatalf("move-only frame is %+v, %v; want position %v and size %v", got, ok, moved, want)
	}
}

func TestAppWindowRejectsTheDesktop(t *testing.T) {
	// A corner of the screen is wallpaper or taskbar in every layout this
	// machine can have: the desktop is not a window drag.
	if w, _, ok := AppWindowAt(1, 1); ok {
		w.Release()
		// A maximized app covering the corner would be a valid answer; the
		// call must just not report the desktop itself.
		if class := windowClass(uintptr(w.handle)); shellWindows[class] {
			t.Fatalf("AppWindowAt(1,1) returned the desktop window %q", class)
		}
	}
}

func TestAppWindowZeroValueIsInert(t *testing.T) {
	var w AppWindow
	if w.Valid() {
		t.Fatal("the zero AppWindow is valid")
	}
	if _, ok := w.Frame(); ok {
		t.Fatal("the zero AppWindow has a frame")
	}
	if err := w.MoveResize(Rect{Width: 10, Height: 10}, true); err == nil {
		t.Fatal("the zero AppWindow accepted a move")
	}
	w.Release()
}
