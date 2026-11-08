//go:build darwin

package mac

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
)

// AfterMain runs fn on the main queue after d. The monitoring loop uses it
// as its only clock: libdispatch owns the deadline, so the main thread is
// woken once instead of a Go ticker waking the scheduler to bounce a
// closure onto it. Safe from any goroutine; fn always runs on the main
// thread. A call scheduled for d <= 0 runs on the next queue pass.
var (
	afterMainOnce sync.Once
	afterMainErr  error
	dispatchAfter func(when uint64, queue, context, work uintptr)
	dispatchAt    func(when uint64, delta int64) uint64
	dispatchQueue func() uintptr

	afterMainWork uintptr
	afterMainSeq  atomic.Uint64
	afterMainJobs sync.Map // uint64 → func()
)

func loadAfterMain() error {
	afterMainOnce.Do(func() {
		var (
			afterAddr, timeAddr, queueAddr uintptr
			err                            error
		)
		if afterAddr, err = purego.Dlsym(purego.RTLD_DEFAULT, "dispatch_after_f"); err != nil {
			afterMainErr = err
			return
		}
		if timeAddr, err = purego.Dlsym(purego.RTLD_DEFAULT, "dispatch_time"); err != nil {
			afterMainErr = err
			return
		}
		// dispatch_get_main_queue() is a macro for &_dispatch_main_q, and
		// the function itself is not exported. The symbol is the queue
		// object; its address is the queue pointer.
		if queueAddr, err = purego.Dlsym(purego.RTLD_DEFAULT, "_dispatch_main_q"); err != nil {
			afterMainErr = err
			return
		}
		purego.RegisterFunc(&dispatchAfter, afterAddr)
		purego.RegisterFunc(&dispatchAt, timeAddr)
		dispatchQueue = func() uintptr { return queueAddr }
		afterMainWork = purego.NewCallback(func(context uintptr) {
			if fn, ok := afterMainJobs.LoadAndDelete(context); ok {
				fn.(func())()
			}
		})
	})
	return afterMainErr
}

// AfterMain posts fn to the main queue after d. When the dispatch symbols
// are unavailable it falls back to a Go timer that hands the callback to
// mygo's main thread; the callback must never run on a worker goroutine,
// because it owns UI state.
func AfterMain(d time.Duration, fn func()) {
	if fn == nil {
		return
	}
	if err := loadAfterMain(); err != nil {
		go func() {
			time.Sleep(d)
			mygo.RunOnMain(fn)
		}()
		return
	}
	id := afterMainSeq.Add(1)
	afterMainJobs.Store(id, fn)
	dispatchAfter(dispatchAt(0, int64(d)), dispatchQueue(), uintptr(id), afterMainWork)
}
