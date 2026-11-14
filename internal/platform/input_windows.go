//go:build windows

package platform

// Global mouse observation. Windows hands a process every mouse event only
// through a low-level hook (WH_MOUSE_LL). The hook is installed on the main
// thread, whose mygo message loop then invokes it, so the callbacks already
// run where the rest of the package expects them; each callback is kept
// short enough for the hook timeout (a dispatch, no drawing).

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type msllHookStruct struct {
	pt          winPoint
	mouseData   uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

type mouseObserverSet struct {
	mu       sync.Mutex
	nextID   uint32
	downs    map[uint32]func(inside bool, button int)
	moves    map[uint32]func()
	drags    map[uint32]func()
	hook     uintptr
	proc     uintptr
	lastMove time.Time
	lastDrag time.Time
}

var mouseObservers = struct {
	mouseObserverSet
	window uintptr
}{mouseObserverSet: mouseObserverSet{
	downs: map[uint32]func(bool, int){},
	moves: map[uint32]func(){},
	drags: map[uint32]func(){},
}}

// observeHookProc dispatches one WH_MOUSE_LL event and always continues the
// hook chain. Only the events the app's pointer logic needs are looked at.
func observeHookProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) != hcAction {
		return callNextHook(nCode, wParam, lParam)
	}
	o := &mouseObservers.mouseObserverSet
	switch wParam {
	case wmLButtonDown, wmRButtonDown, wmMButtonDown, wmXButtonDown:
		button := 0
		switch wParam {
		case wmRButtonDown:
			button = 1
		case wmMButtonDown:
			button = 2
		case wmXButtonDown:
			button = 3
		}
		inside, report := pressInside(lParam, mouseObservers.window)
		if report {
			o.mu.Lock()
			fns := make([]func(bool, int), 0, len(o.downs))
			for _, fn := range o.downs {
				fns = append(fns, fn)
			}
			o.mu.Unlock()
			for _, fn := range fns {
				fn(inside, button)
			}
		}
	case wmMouseMove:
		now := time.Now()
		o.mu.Lock()
		moves := make([]func(), 0, len(o.moves))
		if len(o.moves) > 0 && now.Sub(o.lastMove) >= 8*time.Millisecond {
			o.lastMove = now
			for _, fn := range o.moves {
				moves = append(moves, fn)
			}
		}
		drags := make([]func(), 0, len(o.drags))
		if len(o.drags) > 0 && now.Sub(o.lastDrag) >= 8*time.Millisecond && leftHeld() {
			o.lastDrag = now
			for _, fn := range o.drags {
				drags = append(drags, fn)
			}
		}
		o.mu.Unlock()
		for _, fn := range moves {
			fn()
		}
		for _, fn := range drags {
			fn()
		}
	}
	return callNextHook(nCode, wParam, lParam)
}

func callNextHook(nCode, wParam, lParam uintptr) uintptr {
	r, _, _ := procCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

// hookStruct reads the event the hook was handed. The value comes from the
// system's own stack frame, so the uintptr-to-pointer conversion is safe
// for the callback's duration; spelling it through a pointer typed value
// keeps vet's unsafeptr check quiet.
func hookStruct(lParam uintptr) *msllHookStruct {
	var p unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&p)) = lParam
	return (*msllHookStruct)(p)
}

// pressInside classifies a press: true when it landed on the watched
// window, false when it landed in another app or on the desktop. Presses in
// other Rakko windows report nothing, matching the Mac local monitor.
func pressInside(lParam, watched uintptr) (inside, report bool) {
	if lParam == 0 {
		return false, true
	}
	ev := hookStruct(lParam)
	return pressAt(ev.pt, watched)
}

// pressAt classifies a press at a screen point against the watched window:
// a press inside a window that takes the mouse belongs to it even when
// WindowFromPoint answers the desktop (a DirectComposition surface carries
// no hit-test alpha there).
func pressAt(pt winPoint, watched uintptr) (inside, report bool) {
	h, _, _ := procWindowFromPoint.Call(uintptr(unsafe.Pointer(&pt)))
	root := h
	if h != 0 {
		if r, _, _ := procGetAncestor.Call(h, gaRoot); r != 0 {
			root = r
		}
	}
	if root == watched {
		return true, true
	}
	if root != 0 && windowPID(root) == ownProcessID() {
		return false, false
	}
	if watched != 0 && windowExStyle(watched)&wsExTransparent == 0 {
		if wr, ok := windowRect(watched); ok {
			if pt.X >= wr.Left && pt.X < wr.Right && pt.Y >= wr.Top && pt.Y < wr.Bottom {
				return true, true
			}
		}
	}
	return false, true
}

func leftHeld() bool {
	r, _, _ := procGetAsyncKeyState.Call(vkLeftButton)
	return r&0x8000 != 0
}

// installHookLocked arms the hook once any observer is registered. The
// caller holds the set's lock.
func (o *mouseObserverSet) installHookLocked() {
	if o.hook != 0 {
		return
	}
	if o.proc == 0 {
		o.proc = windows.NewCallback(observeHookProc)
	}
	h, _, _ := procSetWindowsHookEx.Call(whMouseLL, o.proc, 0, 0)
	o.hook = h
}

func (o *mouseObserverSet) removeHookLocked() {
	if o.hook == 0 {
		return
	}
	if len(o.downs) == 0 && len(o.moves) == 0 && len(o.drags) == 0 {
		procUnhookWindowsHookEx.Call(o.hook)
		o.hook = 0
	}
}

// ObserveMouseDown observes every mouse button press without consuming it.
// clicked(true, button) is a press in window; clicked(false, button) a
// press in another app or on the desktop. Presses in this app's other
// windows are not reported.
func ObserveMouseDown(window uintptr, clicked func(inside bool, button int)) (stop func()) {
	if clicked == nil {
		return func() {}
	}
	o := &mouseObservers.mouseObserverSet
	o.mu.Lock()
	mouseObservers.window = window
	o.nextID++
	id := o.nextID
	o.downs[id] = clicked
	o.installHookLocked()
	o.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			delete(o.downs, id)
			o.removeHookLocked()
			o.mu.Unlock()
		})
	}
}

// ObserveMouseMoved reports pointer movement anywhere. The callbacks are
// throttled to roughly one frame's worth of events, which is all the
// pointer gate needs to sample the cursor.
func ObserveMouseMoved(moved func()) {
	if moved == nil {
		return
	}
	o := &mouseObservers.mouseObserverSet
	o.mu.Lock()
	o.nextID++
	o.moves[o.nextID] = moved
	o.installHookLocked()
	o.mu.Unlock()
}

// ObserveLeftMouseDragged reports a drag of another app's window (the
// Explorer file drag included): a pointer move with the left button held.
// The overlay turns into a drop target on the first of them.
func ObserveLeftMouseDragged(moved func()) (stop func()) {
	if moved == nil {
		return func() {}
	}
	o := &mouseObservers.mouseObserverSet
	o.mu.Lock()
	o.nextID++
	id := o.nextID
	o.drags[id] = moved
	o.installHookLocked()
	o.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			delete(o.drags, id)
			o.removeHookLocked()
			o.mu.Unlock()
		})
	}
}

// LeftMouseDown reports whether the primary mouse button is physically held
// right now, whichever window has it.
func LeftMouseDown() bool { return leftHeld() }

// NoteLeftPress tells the drag tracker a real left press arrived. The
// Windows tracker reads the capture window OLE holds for the whole drag,
// so it needs no press edge; the call stays for the shared gesture path.
func NoteLeftPress() {}
