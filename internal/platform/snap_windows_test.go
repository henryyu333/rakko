//go:build windows

package platform

// Snap calls against a real window this process creates: the headless
// Windows session can still make one, so the hit test, the frame read and
// the arrangement are exercised without a desktop.

import (
	"errors"
	"math"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// testSnapWindow creates the visible STATIC window the snap calls act on.
// Creating, moving and destroying it must all happen on one thread: a move
// or a destroy posted to another thread is asynchronous by design, and the
// tests read the immediate result. The lock lives until the test ends.
func testSnapWindow(t *testing.T) uintptr {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
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

// TestArrangementErrorClassifiesRefusals pins the verdict the async snap
// reports at the end of its confirmation window: a size-only mismatch is a
// resize refusal, a position mismatch a failed move.
func TestArrangementErrorClassifiesRefusals(t *testing.T) {
	want := winRect{Left: 100, Top: 80, Right: 700, Bottom: 580}
	if err := arrangementError(want, want, true); err != nil {
		t.Fatalf("a matching frame reported %v", err)
	}
	sizeOnly := winRect{Left: 100, Top: 80, Right: 420, Bottom: 320}
	if err := arrangementError(sizeOnly, want, true); !errors.Is(err, ErrAppWindowResize) {
		t.Fatalf("a refused size reported %v, want ErrAppWindowResize", err)
	}
	moved := winRect{Left: 160, Top: 140, Right: 760, Bottom: 640}
	if err := arrangementError(moved, want, true); err == nil || errors.Is(err, ErrAppWindowResize) {
		t.Fatalf("a refused move reported %v, want a move error", err)
	}
	// A move-only arrangement never blames the size.
	rightSize := winRect{Left: 160, Top: 140, Right: 760, Bottom: 640}
	if err := arrangementError(rightSize, want, false); err == nil || errors.Is(err, ErrAppWindowResize) {
		t.Fatalf("a move-only mismatch reported %v, want a move error", err)
	}
}

// testConstrainedWindow creates a foreign window that enforces a fixed
// track size through WM_GETMINMAXINFO, on a thread that pumps messages.
// It returns the handle and the window's size.
func testConstrainedWindow(t *testing.T) (uintptr, int, int) {
	t.Helper()
	const (
		wmGetMinMaxInfo = 0x0024
		wmClose         = 0x0010
		wmDestroy       = 0x0002
		minW, minH      = 320, 240
	)
	user32 := windows.NewLazySystemDLL("user32.dll")
	procRegisterClassEx := user32.NewProc("RegisterClassExW")
	procDefWindowProc := user32.NewProc("DefWindowProcW")
	procGetMessage := user32.NewProc("GetMessageW")
	procTranslateMessage := user32.NewProc("TranslateMessage")
	procDispatchMessage := user32.NewProc("DispatchMessageW")
	procPostQuitMessage := user32.NewProc("PostQuitMessage")

	type minMaxInfo struct {
		ptReserved     [2]int32
		ptMaxSize      [2]int32
		ptMaxPosition  [2]int32
		ptMinTrackSize [2]int32
		ptMaxTrackSize [2]int32
	}
	ready := make(chan uintptr, 1)
	done := make(chan struct{})
	className, _ := windows.UTF16PtrFromString("rakko-snap-constrained")
	title, _ := windows.UTF16PtrFromString("rakko-snap-constrained")
	wndProc := windows.NewCallback(func(h, msg, wparam, lparam uintptr) uintptr {
		switch msg {
		case wmGetMinMaxInfo:
			mmi := (*minMaxInfo)(wiPtr(lparam))
			mmi.ptMinTrackSize = [2]int32{minW, minH}
			mmi.ptMaxTrackSize = [2]int32{minW, minH}
			return 0
		case wmClose:
			procDestroyWindow.Call(h)
			return 0
		case wmDestroy:
			procPostQuitMessage.Call(0)
			return 0
		}
		r, _, _ := procDefWindowProc.Call(h, msg, wparam, lparam)
		return r
	})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		var wc struct {
			cbSize        uint32
			style         uint32
			lpfnWndProc   uintptr
			cbClsExtra    int32
			cbWndExtra    int32
			hInstance     uintptr
			hIcon         uintptr
			hCursor       uintptr
			hbrBackground uintptr
			lpszMenuName  *uint16
			lpszClassName *uint16
			hIconSm       uintptr
		}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		wc.lpfnWndProc = wndProc
		wc.lpszClassName = className
		if r, _, _ := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			ready <- 0
			return
		}
		const wsOverlappedWindow = 0x00CF0000
		h, _, _ := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
			wsOverlappedWindow, 200, 200, 400, 300, 0, 0, 0, 0)
		if h == 0 {
			ready <- 0
			return
		}
		t.Cleanup(func() {})
		procShowWindow.Call(h, 4 /* SW_SHOWNOACTIVATE */)
		ready <- h
		var msg struct {
			hwnd    uintptr
			message uint32
			wParam  uintptr
			lParam  uintptr
			time    uint32
			pt      winPoint
		}
		for {
			r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()
	h := <-ready
	if h == 0 {
		<-done
		t.Skip("this session cannot create a constrained window")
	}
	t.Cleanup(func() {
		// Closing posts WM_CLOSE and the owner's loop ends; a stuck loop
		// must not hang the suite.
		user32.NewProc("PostMessageW").Call(h, 0x0010, 0, 0)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Log("the constrained window's thread did not exit")
		}
	})
	return h, minW, minH
}

// TestArrangementErrorReportsAForeignResizeRefusal drives the asynchronous
// snap against a foreign window that fixes its track size: MoveResize
// returns before the owner answers, and the verdict at the end of the
// confirmation window is the resize refusal the synchronous path used to
// report.
func TestArrangementErrorReportsAForeignResizeRefusal(t *testing.T) {
	h, minW, minH := testConstrainedWindow(t)
	w := AppWindow{handle: h}
	frame, ok := w.Frame()
	if !ok {
		t.Fatal("the constrained window has no frame")
	}
	target := Rect{X: frame.X + 40, Y: frame.Y + 30, Width: float64(minW) + 180, Height: float64(minH) + 160}
	if err := w.MoveResize(target, true); err != nil {
		t.Fatalf("MoveResize on a foreign window: %v", err)
	}
	// The posted move is processed by the owner's message loop, so the
	// position lands first; the refusal is about the size alone. Read the
	// frame once and judge that same frame: a second read can catch the
	// owner between two messages and see the old position, which is not a
	// verdict about the frame the loop just accepted.
	want := dipToPixels(target)
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, ok := windowRect(h)
		if !ok {
			t.Fatal("the constrained window went away")
		}
		if abs32(got.Left-want.Left) <= 2 && abs32(got.Top-want.Top) <= 2 {
			if err := arrangementError(got, want, true); !errors.Is(err, ErrAppWindowResize) {
				t.Fatalf("after the move landed the verdict is %v, want ErrAppWindowResize", err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the posted move never landed (frame %+v, want %+v)", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
