//go:build windows

package platform

// The global mouse observer. The hook proc runs on the system's hook timeout
// and only copies events; the business callbacks run later on the main
// thread; the hook itself lives on a thread of its own with its own message
// loop, because the system drops a hook whose thread stops pumping.

import (
	"fmt"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func drainMouseQueue() { mouseHookQueue.drain() }

// takeMouseEvent answers the one event the last hook call queued.
func takeMouseEvent(t *testing.T) mouseHookEvent {
	t.Helper()
	queued := mouseHookQueue.drain()
	if len(queued) == 0 {
		t.Fatal("the hook proc queued no event")
	}
	return queued[0]
}

// TestMouseHookProcCopiesAndReturns checks that the hook proc copies the
// event and returns without running the app's callback: a slow callback in a
// hook proc makes the system unhook it, which is what used to break hover,
// click-through switching and drag watching.
func TestMouseHookProcCopiesAndReturns(t *testing.T) {
	drainMouseQueue()
	o := &mouseObservers.mouseObserverSet
	ran := make(chan struct{}, 1)
	o.mu.Lock()
	o.downs[9001] = func(bool, int) {
		time.Sleep(2 * time.Second)
		ran <- struct{}{}
	}
	o.mu.Unlock()
	t.Cleanup(func() {
		o.mu.Lock()
		delete(o.downs, 9001)
		o.mu.Unlock()
	})

	ev := msllHookStruct{pt: winPoint{X: 640, Y: 480}}
	start := time.Now()
	mouseHookProc(hcAction, wmLButtonDown, uintptr(unsafe.Pointer(&ev)))
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Fatalf("the hook proc took %v: it ran a business callback", took)
	}
	got := takeMouseEvent(t)
	if !got.press || got.button != 0 || got.pt != (winPoint{X: 640, Y: 480}) {
		t.Fatalf("queued %+v, want a left press at 640,480", got)
	}
	select {
	case <-ran:
		t.Fatal("the hook proc ran the business callback instead of queueing")
	default:
	}
}

// TestMouseHookProcButtons checks the button numbers the hook reports for
// every button-down message and that events the app does not watch are left
// alone.
func TestMouseHookProcButtons(t *testing.T) {
	drainMouseQueue()
	ev := msllHookStruct{pt: winPoint{X: 1, Y: 2}}
	for _, tt := range []struct {
		msg  uintptr
		want int
	}{
		{wmLButtonDown, 0},
		{wmRButtonDown, 1},
		{wmMButtonDown, 2},
		{wmXButtonDown, 3},
	} {
		mouseHookProc(hcAction, tt.msg, uintptr(unsafe.Pointer(&ev)))
		got := takeMouseEvent(t)
		if !got.press || got.button != tt.want || got.pt != ev.pt {
			t.Fatalf("wParam %#x queued %+v, want button %d at %+v", tt.msg, got, tt.want, ev.pt)
		}
	}
	if n := mouseHookQueue.len(); n != 0 {
		t.Fatalf("%d events queued for messages the app does not watch", n)
	}
	// 0xFFFFFFFF is -1 as the hook sees it: below HC_ACTION, so the event
	// must be passed down the chain untouched.
	mouseHookProc(uintptr(0xFFFFFFFF), wmLButtonDown, uintptr(unsafe.Pointer(&ev)))
	if n := mouseHookQueue.len(); n != 0 {
		t.Fatal("a code outside HC_ACTION queued an event")
	}
}

// TestMouseMoveThrottle checks that pointer moves are sampled at the throttle
// interval rather than queued at the mouse's report rate.
func TestMouseMoveThrottle(t *testing.T) {
	drainMouseQueue()
	hookLastMove.Store(0)
	ev := msllHookStruct{pt: winPoint{X: 10, Y: 20}}
	mouseHookProc(hcAction, wmMouseMove, uintptr(unsafe.Pointer(&ev)))
	if got := takeMouseEvent(t); got.press || got.pt != ev.pt {
		t.Fatalf("queued %+v, want a move to %+v", got, ev.pt)
	}
	mouseHookProc(hcAction, wmMouseMove, uintptr(unsafe.Pointer(&ev)))
	if n := mouseHookQueue.len(); n != 0 {
		t.Fatalf("%d moves queued inside the throttle window, want none", n)
	}
	time.Sleep(hookMoveInterval + 2*time.Millisecond)
	mouseHookProc(hcAction, wmMouseMove, uintptr(unsafe.Pointer(&ev)))
	if n := mouseHookQueue.len(); n != 1 {
		t.Fatalf("%d moves queued after the throttle window, want 1", n)
	}
	drainMouseQueue()
}

// TestMouseHookQueueKeepsPresses checks the hand-off under overflow: a full
// queue sheds moves, never the press, because a dropped press is a dropped
// click (the panel would stay open after a click outside).
func TestMouseHookQueueKeepsPresses(t *testing.T) {
	q := newMouseEventQueue(4)
	for i := range 10 {
		q.push(mouseHookEvent{pt: winPoint{X: int32(i)}})
	}
	if n := q.len(); n != 4 {
		t.Fatalf("%d moves queued, want the 4 the queue holds", n)
	}
	press := mouseHookEvent{press: true, button: 0, pt: winPoint{X: -1, Y: -1}}
	q.push(press)
	queued := q.drain()
	if len(queued) != 4 {
		t.Fatalf("%d events queued after a press, want 4", len(queued))
	}
	found := false
	for _, ev := range queued {
		if ev.press && ev.pt == press.pt {
			found = true
		}
	}
	if !found {
		t.Fatalf("the press was dropped: queued %+v", queued)
	}
	// The oldest move made room for it.
	if queued[0].pt.X != 1 {
		t.Fatalf("oldest queued move is %+v, want the second one after eviction", queued[0])
	}

	// A queue that holds nothing but presses keeps the newest one: it is the
	// click the user just made.
	presses := newMouseEventQueue(2)
	for i := range 3 {
		presses.push(mouseHookEvent{press: true, pt: winPoint{X: int32(i)}})
	}
	queued = presses.drain()
	if len(queued) != 2 || queued[0].pt.X != 1 || queued[1].pt.X != 2 {
		t.Fatalf("presses queued = %+v, want the two newest", queued)
	}
}

// TestMouseEventDispatch checks that a queued event reaches the registered
// callbacks with the press classified against the watched window, and that a
// drag reports both the move and the drag.
func TestMouseEventDispatch(t *testing.T) {
	h := testHitWindow(t)
	o := &mouseObservers.mouseObserverSet
	var mu sync.Mutex
	var got []string
	record := func(s string) {
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
	}
	o.mu.Lock()
	mouseObservers.window = h
	o.downs[8101] = func(inside bool, button int) { record(fmt.Sprintf("down %v %d", inside, button)) }
	o.moves[8102] = func() { record("move") }
	o.drags[8103] = func() { record("drag") }
	o.mu.Unlock()
	t.Cleanup(func() {
		o.mu.Lock()
		delete(o.downs, 8101)
		delete(o.moves, 8102)
		delete(o.drags, 8103)
		mouseObservers.window = 0
		o.mu.Unlock()
	})

	r, ok := windowRect(h)
	if !ok {
		t.Fatal("GetWindowRect failed")
	}
	center := winPoint{X: (r.Left + r.Right) / 2, Y: (r.Top + r.Bottom) / 2}
	away := winPoint{X: r.Left - 2000, Y: r.Top - 2000}
	dispatchMouseEvent(mouseHookEvent{press: true, pt: center})
	dispatchMouseEvent(mouseHookEvent{press: true, pt: away})
	dispatchMouseEvent(mouseHookEvent{pt: center, drag: true})
	dispatchMouseEvent(mouseHookEvent{pt: center})

	mu.Lock()
	defer mu.Unlock()
	want := []string{"down true 0", "down false 0", "move", "drag", "move"}
	if len(got) != len(want) {
		t.Fatalf("callbacks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("callbacks = %v, want %v", got, want)
		}
	}
}

// TestMouseHookRunsOnItsOwnThread checks that the hook installs and that the
// thread the system drives it on is not the main thread: the main thread's
// message loop must stay free for window work.
func TestMouseHookRunsOnItsOwnThread(t *testing.T) {
	if err := ensureMouseHook(); err != nil {
		t.Fatalf("the global mouse hook did not install: %v", err)
	}
	if mouseHook.hook == 0 {
		t.Fatal("the hook reports an install without a handle")
	}
	recordMainThread()
	if mouseHook.threadID == 0 {
		t.Fatal("the hook thread recorded no thread id")
	}
	if mouseHook.threadID == mainThreadID {
		t.Fatalf("the hook runs on the main thread %d", mainThreadID)
	}
}
