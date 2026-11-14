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

func drainMouseQueue() {
	for {
		select {
		case <-mouseHookQueue:
		default:
			return
		}
	}
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
	select {
	case got := <-mouseHookQueue:
		if !got.press || got.button != 0 || got.pt != (winPoint{X: 640, Y: 480}) {
			t.Fatalf("queued %+v, want a left press at 640,480", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the hook proc queued no event")
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
		select {
		case got := <-mouseHookQueue:
			if !got.press || got.button != tt.want || got.pt != ev.pt {
				t.Fatalf("wParam %#x queued %+v, want button %d at %+v", tt.msg, got, tt.want, ev.pt)
			}
		case <-time.After(time.Second):
			t.Fatalf("wParam %#x queued nothing", tt.msg)
		}
	}
	if n := len(mouseHookQueue); n != 0 {
		t.Fatalf("%d events queued for messages the app does not watch", n)
	}
	// 0xFFFFFFFF is -1 as the hook sees it: below HC_ACTION, so the event
	// must be passed down the chain untouched.
	mouseHookProc(uintptr(0xFFFFFFFF), wmLButtonDown, uintptr(unsafe.Pointer(&ev)))
	if n := len(mouseHookQueue); n != 0 {
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
	select {
	case got := <-mouseHookQueue:
		if got.press || got.pt != ev.pt {
			t.Fatalf("queued %+v, want a move to %+v", got, ev.pt)
		}
	case <-time.After(time.Second):
		t.Fatal("the first move was not queued")
	}
	mouseHookProc(hcAction, wmMouseMove, uintptr(unsafe.Pointer(&ev)))
	if n := len(mouseHookQueue); n != 0 {
		t.Fatalf("%d moves queued inside the throttle window, want none", n)
	}
	time.Sleep(hookMoveInterval + 2*time.Millisecond)
	mouseHookProc(hcAction, wmMouseMove, uintptr(unsafe.Pointer(&ev)))
	if n := len(mouseHookQueue); n != 1 {
		t.Fatalf("%d moves queued after the throttle window, want 1", n)
	}
	drainMouseQueue()
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
