//go:build windows

package platform

// Snap calls against a real window this process creates: the headless
// Windows session can still make one, so the hit test, the frame read and
// the arrangement are exercised without a desktop.

import (
	"math"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// testSnapWindow creates the visible STATIC window the snap calls act on.
func testSnapWindow(t *testing.T) uintptr {
	t.Helper()
	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("rakko-snap-test")
	// WS_OVERLAPPEDWINDOW: caption, thick frame, system menu.
	const wsOverlappedWindow = 0x00CF0000
	h, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow, 200, 180, 420, 260, 0, 0, 0, 0)
	if h == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(h) })
	procShowWindow.Call(h, 4 /* SW_SHOWNOACTIVATE */)
	if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
		// A logon session with no attached desktop (an SSH-only test run)
		// cannot show or hit-test a window: the same test runs through
		// rw desk, where the desktop is real.
		t.Skip("no interactive desktop: the test window cannot be shown")
	}
	return h
}

func TestAppWindowFrameAndMove(t *testing.T) {
	// The window must belong to the thread that moves it: a move posted to
	// another thread is asynchronous by design, and this test checks the
	// immediate result.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	h := testSnapWindow(t)
	defer func() { procDestroyWindow.Call(h) }()
	// The calls under test take the handle as an AppWindow: the hit test is
	// separately covered, and must not decide this test's fate.
	w := AppWindow{handle: h}
	if !w.Valid() {
		t.Fatal("AppWindow.Valid on a live window is false")
	}
	frame, ok := w.Frame()
	if !ok {
		t.Fatal("AppWindow.Frame failed")
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

// TestAppWindowAtFindsOurWindow checks the hit test on a real desktop. The
// machine is shared with other tasks whose topmost tools may hold the
// point, so a few fresh positions are tried and an occupied one is a skip,
// not a failure — the drag scenario in the evidence run covers the call
// itself.
func TestAppWindowAtFindsOurWindow(t *testing.T) {
	h := testSnapWindow(t)
	frame, ok := WindowScreenRect(WindowNumber(h))
	if !ok {
		t.Fatal("WindowScreenRect of the test window failed")
	}
	var lastPID int
	for attempt := range 8 {
		// Move to a fresh spot instead of raising above other topmost
		// tools: SetWindowPos on a shared desktop can wait on a hung shell
		// window, and an occupied point is not this test's subject.
		procSetWindowPos.Call(h, hwndTop, uintptr(120+attempt*37), uintptr(160+attempt*29), 0, 0,
			swpNoSize|swpNoActivate)
		if f, ok := WindowScreenRect(WindowNumber(h)); ok {
			frame = f
		}
		cx, cy := frame.X+frame.Width/2, frame.Y+frame.Height/2
		w, pid, found := AppWindowAt(cx, cy)
		if found && pid == OwnPID() {
			w.Release()
			return
		}
		if w.Valid() {
			w.Release()
		}
		lastPID = pid
	}
	t.Skipf("the shared desktop kept another window at every tried point (last pid %d)", lastPID)
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

// TestMoveResizeDoesNotWaitOnAHungWindow moves a window owned by a thread
// that never pumps messages — an unresponsive app. SetWindowPos and
// ShowWindow would both wait on that thread, which on the UI thread is
// Rakko freezing with the app; the calls must be posted instead.
func TestMoveResizeDoesNotWaitOnAHungWindow(t *testing.T) {
	created := make(chan uintptr, 1)
	release := make(chan struct{})
	ownerDone := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(ownerDone)
		class, _ := windows.UTF16PtrFromString("STATIC")
		title, _ := windows.UTF16PtrFromString("rakko-hung-window-test")
		// WS_OVERLAPPEDWINDOW, never shown: the test needs no desktop.
		const wsOverlappedWindow = 0x00CF0000
		h, _, _ := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
			wsOverlappedWindow, 120, 120, 240, 160, 0, 0, 0, 0)
		created <- h
		// Hold the thread without a message loop: a synchronous
		// SetWindowPos from another thread waits here forever.
		<-release
		if h != 0 {
			procDestroyWindow.Call(h)
		}
	}()
	h := <-created
	if h == 0 {
		close(release)
		<-ownerDone
		t.Skip("this session cannot create a window")
	}
	t.Cleanup(func() {
		close(release)
		<-ownerDone
	})
	w := AppWindow{handle: h}
	moved := make(chan error, 1)
	go func() {
		moved <- w.MoveResize(Rect{X: 40, Y: 40, Width: 200, Height: 120}, true)
	}()
	select {
	case err := <-moved:
		if err != nil {
			t.Fatalf("MoveResize on a hung window: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MoveResize waited on a window whose thread does not pump messages")
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
