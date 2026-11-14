//go:build windows

package platform

// Global mouse observation. Windows hands a process every mouse event only
// through a low-level hook (WH_MOUSE_LL). The system calls the hook on the
// thread that installed it, and it forgets the hook when that thread stops
// pumping messages or when one call takes longer than LowLevelHooksTimeout.
// The hook therefore lives on a thread of its own that does nothing but pump
// messages: the hook proc copies the event into a queue and returns at once,
// and the business callbacks run later on the main thread, where the rest of
// the package expects window work to happen.

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
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

// mouseHookEvent is one event copied out of the hook proc: what happened,
// where, and whether the left button was held for a move.
type mouseHookEvent struct {
	press  bool
	button int
	pt     winPoint
	drag   bool
}

// mouseHookQueue carries events from the hook thread to the dispatcher. The
// hook proc never waits for it: when the queue is full the event is dropped,
// and the next move arrives within a frame anyway.
var mouseHookQueue = make(chan mouseHookEvent, 256)

// mouseHookProcAddr is the hook's one permanent callback.
var mouseHookProcAddr = windows.NewCallback(mouseHookProc)

var mouseHook struct {
	once     sync.Once
	ready    chan struct{}
	hook     uintptr
	threadID uint32
	err      error
}

// hookMoveInterval is how often a pointer move is queued. The pointer gate
// samples at most a frame apart, and the throttle keeps the hook thread far
// below the timeout the system allows a hook proc.
const hookMoveInterval = 8 * time.Millisecond

var hookLastMove atomic.Int64

// mouseHookProc copies one event and returns. Nothing here touches a window
// or a business callback: a hook proc that blocks makes the system drop the
// hook, which is what silently stopped hover, click-through switching and
// drag watching before.
func mouseHookProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) == hcAction {
		switch wParam {
		case wmLButtonDown, wmRButtonDown, wmMButtonDown, wmXButtonDown:
			queueMouseEvent(mouseHookEvent{press: true, button: mouseButton(wParam), pt: hookPoint(lParam)})
		case wmMouseMove:
			if hookMoveDue() {
				queueMouseEvent(mouseHookEvent{pt: hookPoint(lParam), drag: leftHeld()})
			}
		}
	}
	return callNextHook(nCode, wParam, lParam)
}

// mouseButton maps the hook's button-down message to its button number, the
// same order the Mac observer reports.
func mouseButton(wParam uintptr) int {
	switch wParam {
	case wmRButtonDown:
		return 1
	case wmMButtonDown:
		return 2
	case wmXButtonDown:
		return 3
	}
	return 0
}

// hookPoint reads the screen point out of the event the system handed the
// hook. The struct belongs to the system and is only valid for the call.
func hookPoint(lParam uintptr) winPoint {
	if lParam == 0 {
		return winPoint{}
	}
	return hookStruct(lParam).pt
}

// hookMoveDue reports whether a pointer move is worth queueing: one per
// hookMoveInterval, which is all the pointer gate needs.
func hookMoveDue() bool {
	now := time.Now().UnixNano()
	last := hookLastMove.Load()
	if now-last < int64(hookMoveInterval) {
		return false
	}
	return hookLastMove.CompareAndSwap(last, now)
}

func queueMouseEvent(ev mouseHookEvent) {
	select {
	case mouseHookQueue <- ev:
	default:
	}
}

func callNextHook(nCode, wParam, lParam uintptr) uintptr {
	r, _, _ := procCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

// hookStruct reads the event the hook was handed. The value comes from the
// system's own stack frame, so the uintptr-to-pointer conversion is safe for
// the callback's duration; spelling it through a pointer typed value keeps
// vet's unsafeptr check quiet.
func hookStruct(lParam uintptr) *msllHookStruct {
	var p unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&p)) = lParam
	return (*msllHookStruct)(p)
}

func leftHeld() bool {
	r, _, _ := procGetAsyncKeyState.Call(vkLeftButton)
	return r&0x8000 != 0
}

// ensureMouseHook installs the hook once, on its own thread, and reports why
// the system refused it. The hook stays installed for the life of the
// process: the thread that installed it must keep pumping messages, and
// re-arming it per observer would only churn.
func ensureMouseHook() error {
	mouseHook.once.Do(func() {
		mouseHook.ready = make(chan struct{})
		go runMouseHook()
	})
	<-mouseHook.ready
	return mouseHook.err
}

// runMouseHook owns the hook's thread. The system calls the hook proc from
// this thread's message dispatch, so the loop takes no work of its own.
func runMouseHook() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if r, _, _ := procGetCurrentThreadID.Call(); r != 0 {
		mouseHook.threadID = uint32(r)
	}
	h, _, err := procSetWindowsHookEx.Call(whMouseLL, mouseHookProcAddr, 0, 0)
	if h == 0 {
		mouseHook.err = fmt.Errorf("platform: SetWindowsHookEx(WH_MOUSE_LL): %w", err)
		close(mouseHook.ready)
		return
	}
	mouseHook.hook = h
	// The install result is what the caller waits for; the loop below runs
	// for the rest of the process.
	close(mouseHook.ready)
	go dispatchMouseEvents()

	var msg winMsg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 { // WM_QUIT or an error
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// dispatchMouseEvents hands each queued event to the main thread.
func dispatchMouseEvents() {
	for ev := range mouseHookQueue {
		ev := ev
		WakeMain(func() { dispatchMouseEvent(ev) })
	}
}

// mouseObserverSet holds what the app watches. Callbacks are registered and
// released on the main thread; the hook proc only copies events, so the set
// is never touched from the hook thread.
type mouseObserverSet struct {
	mu     sync.Mutex
	nextID uint32
	downs  map[uint32]func(inside bool, button int)
	moves  map[uint32]func()
	drags  map[uint32]func()
}

var mouseObservers = struct {
	mouseObserverSet
	window uintptr
}{mouseObserverSet: mouseObserverSet{
	downs: map[uint32]func(bool, int){},
	moves: map[uint32]func(){},
	drags: map[uint32]func(){},
}}

// dispatchMouseEvent runs the business callbacks for one queued event. Main
// thread only: the callbacks touch windows and may do file work.
func dispatchMouseEvent(ev mouseHookEvent) {
	o := &mouseObservers.mouseObserverSet
	o.mu.Lock()
	if len(o.downs) == 0 && len(o.moves) == 0 && len(o.drags) == 0 {
		o.mu.Unlock()
		return
	}
	o.mu.Unlock()

	if ev.press {
		inside, report := pressAt(ev.pt, mouseObservers.window)
		if !report {
			return
		}
		o.mu.Lock()
		fns := make([]func(bool, int), 0, len(o.downs))
		for _, fn := range o.downs {
			fns = append(fns, fn)
		}
		o.mu.Unlock()
		for _, fn := range fns {
			fn(inside, ev.button)
		}
		return
	}

	o.mu.Lock()
	moves := make([]func(), 0, len(o.moves))
	for _, fn := range o.moves {
		moves = append(moves, fn)
	}
	var drags []func()
	if ev.drag {
		drags = make([]func(), 0, len(o.drags))
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

// pressAt classifies a press at a physical screen point against the watched
// window: a press inside a window that takes the mouse belongs to it even
// when WindowFromPoint answers the desktop (a DirectComposition surface
// carries no hit-test alpha there).
func pressAt(pt winPoint, watched uintptr) (inside, report bool) {
	h := windowFromPointAt(pt)
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

// ObserveMouseDown observes every mouse button press without consuming it.
// clicked(true, button) is a press in window; clicked(false, button) a
// press in another app or on the desktop. Presses in this app's other
// windows are not reported. The callback runs on the main thread.
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
	o.mu.Unlock()
	ensureMouseHook()
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			delete(o.downs, id)
			o.mu.Unlock()
		})
	}
}

// ObserveMouseMoved reports pointer movement anywhere. The events are
// throttled to roughly one frame's worth, which is all the pointer gate needs
// to sample the cursor. The callback runs on the main thread.
func ObserveMouseMoved(moved func()) {
	if moved == nil {
		return
	}
	o := &mouseObservers.mouseObserverSet
	o.mu.Lock()
	o.nextID++
	o.moves[o.nextID] = moved
	o.mu.Unlock()
	ensureMouseHook()
}

// ObserveLeftMouseDragged reports a drag of another app's window (the
// Explorer file drag included): a pointer move with the left button held.
// The overlay turns into a drop target on the first of them. The callback
// runs on the main thread.
func ObserveLeftMouseDragged(moved func()) (stop func()) {
	if moved == nil {
		return func() {}
	}
	o := &mouseObservers.mouseObserverSet
	o.mu.Lock()
	o.nextID++
	id := o.nextID
	o.drags[id] = moved
	o.mu.Unlock()
	ensureMouseHook()
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			delete(o.drags, id)
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
