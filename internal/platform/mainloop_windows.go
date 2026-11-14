//go:build windows

package platform

// Main-loop scheduling on Windows. mygo owns the message loop, so a
// cross-thread wakeup is mygo.RunOnMain's posted message, and the timers
// are Go timers that hand their callback to the same queue. The visible
// behaviour matches the Mac implementation: callbacks run on the main
// thread, WakeMain never drops a queued function, and AfterMainOnce
// replaces the earlier pending deadline.

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

var (
	mainThreadID   uint32
	mainThreadOnce sync.Once

	deadlineMu  sync.Mutex
	deadlineGen atomic.Uint64
	deadlineTmr *time.Timer
)

func recordMainThread() {
	mainThreadOnce.Do(func() {
		r, _, _ := procGetCurrentThreadID.Call()
		mainThreadID = uint32(r)
	})
}

// OnMainThread reports whether the caller is on the loop that owns the
// windows.
func OnMainThread() bool {
	recordMainThread()
	r, _, _ := procGetCurrentThreadID.Call()
	return uint32(r) == mainThreadID
}

// WakeMain makes fn run on the main thread as soon as possible. Safe from
// any goroutine; every queued function runs.
func WakeMain(fn func()) {
	if fn == nil {
		return
	}
	mygo.RunOnMain(fn)
}

// AfterMainOnce runs fn on the main thread after d, replacing any earlier
// pending deadline.
func AfterMainOnce(d time.Duration, fn func()) {
	if fn == nil {
		return
	}
	gen := deadlineGen.Add(1)
	deadlineMu.Lock()
	if deadlineTmr != nil {
		deadlineTmr.Stop()
	}
	deadlineTmr = time.AfterFunc(d, func() {
		if deadlineGen.Load() != gen {
			return
		}
		mygo.RunOnMain(fn)
	})
	deadlineMu.Unlock()
}

// StartMainTicker calls fn on the main thread every interval until stop is
// called.
func StartMainTicker(interval time.Duration, fn func()) (stop func()) {
	if fn == nil || interval <= 0 {
		return func() {}
	}
	t := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-t.C:
				mygo.RunOnMain(fn)
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.Stop()
			close(done)
		})
	}
}
