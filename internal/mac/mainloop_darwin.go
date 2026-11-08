//go:build darwin

package mac

import (
	"sync"
	"time"

	"github.com/ebitengine/purego"
)

// Main-loop scheduling. Two native primitives cover the app: a run-loop
// source carrying a FIFO of cross-thread wakeups (nothing may overwrite a
// queued wakeup), and repeating CFRunLoopTimers for the active 30 ms
// cadence, added to the default and event-tracking modes. All callbacks run
// on the AppKit main thread.

type cfRunLoopTimerContext struct {
	version         int
	info            uintptr
	retain          uintptr
	release         uintptr
	copyDescription uintptr
}

type cfRunLoopSourceContext struct {
	version         int
	info            uintptr
	retain          uintptr
	release         uintptr
	copyDescription uintptr
	equal           uintptr
	hash            uintptr
	schedule        uintptr
	cancel          uintptr
	perform         uintptr
}

// mainWakeQueue is the FIFO the run-loop source drains. A single slot is
// wrong: a timer wake and an event wake can both arrive before the main
// thread runs, and overwriting one would strand monitorWakeQueued at true.
type mainWakeQueue struct {
	mu   sync.Mutex
	jobs []func()
}

func (q *mainWakeQueue) push(fn func()) {
	if fn == nil {
		return
	}
	q.mu.Lock()
	q.jobs = append(q.jobs, fn)
	q.mu.Unlock()
}

func (q *mainWakeQueue) drain() []func() {
	q.mu.Lock()
	jobs := q.jobs
	q.jobs = nil
	q.mu.Unlock()
	return jobs
}

func (q *mainWakeQueue) empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs) == 0
}

var (
	mainLoopOnce sync.Once
	mainLoopErr  error

	mlGetMain          func() uintptr
	mlAddTimer         func(runLoop, timer, mode uintptr)
	mlTimerCreate      func(allocator uintptr, fireDate float64, interval float64, flags uintptr, order int, callout uintptr, context *cfRunLoopTimerContext) uintptr
	mlTimerInvalidate  func(timer uintptr)
	mlTime             func() float64
	mlAddSource        func(runLoop, source, mode uintptr)
	mlSourceCreate     func(allocator uintptr, order int, context *cfRunLoopSourceContext) uintptr
	mlSourceSignal     func(source uintptr)
	mlSourceInvalidate func(source uintptr)
	mlWakeUp           func(runLoop uintptr)

	mainRunLoop uintptr
	mainSource  uintptr
	modes       []uintptr
	wakeQueue   mainWakeQueue

	tickerMu  sync.Mutex
	tickerCbs = map[uintptr]uintptr{}
)

func loadMainLoop() error {
	mainLoopOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
		if err != nil {
			mainLoopErr = err
			return
		}
		bind := func(fn any, name string) bool {
			addr, e := purego.Dlsym(lib, name)
			if e != nil {
				mainLoopErr = e
				return false
			}
			purego.RegisterFunc(fn, addr)
			return true
		}
		if !bind(&mlGetMain, "CFRunLoopGetMain") ||
			!bind(&mlAddTimer, "CFRunLoopAddTimer") ||
			!bind(&mlTimerCreate, "CFRunLoopTimerCreate") ||
			!bind(&mlTimerInvalidate, "CFRunLoopTimerInvalidate") ||
			!bind(&mlTime, "CFAbsoluteTimeGetCurrent") ||
			!bind(&mlAddSource, "CFRunLoopAddSource") ||
			!bind(&mlSourceCreate, "CFRunLoopSourceCreate") ||
			!bind(&mlSourceSignal, "CFRunLoopSourceSignal") ||
			!bind(&mlSourceInvalidate, "CFRunLoopSourceInvalidate") ||
			!bind(&mlWakeUp, "CFRunLoopWakeUp") {
			return
		}
		// CFSTR("kCFRunLoopCommonModes") built as an NSString is not the
		// run loop's own constant. Add the two modes the app actually runs
		// in: resting and tracking a mouse drag.
		withPool(func() {
			for _, name := range []string{"kCFRunLoopDefaultMode", "kCFRunLoopEventTracking"} {
				modes = append(modes, uintptr(send(nsString(name), "retain")))
			}
		})
		mainRunLoop = mlGetMain()

		sourceCb := purego.NewCallback(func(uintptr) {
			for {
				jobs := wakeQueue.drain()
				if len(jobs) == 0 {
					return
				}
				for _, fn := range jobs {
					fn()
				}
			}
		})
		sc := cfRunLoopSourceContext{perform: sourceCb}
		mainSource = mlSourceCreate(0, 0, &sc)
		for _, mode := range modes {
			mlAddSource(mainRunLoop, mainSource, mode)
		}
	})
	return mainLoopErr
}

// WakeMain makes fn run on the main run loop as soon as possible. Safe from
// any goroutine. Every queued function runs; a second wakeup never replaces
// the first.
func WakeMain(fn func()) {
	if fn == nil || loadMainLoop() != nil {
		return
	}
	wakeQueue.push(fn)
	mlSourceSignal(mainSource)
	mlWakeUp(mainRunLoop)
}

// StartMainTicker calls fn on the main run loop every interval until stop is
// called. It uses a repeating CFRunLoopTimer in the default and
// event-tracking modes, so an active shell is driven without a Go timer and
// a cross-thread wakeup on every frame.
func StartMainTicker(interval time.Duration, fn func()) (stop func()) {
	if fn == nil || interval <= 0 || loadMainLoop() != nil {
		return func() {}
	}
	var tc cfRunLoopTimerContext
	var timer, cb uintptr
	cb = purego.NewCallback(func(uintptr, uintptr) { fn() })
	timer = mlTimerCreate(0, mlTime()+interval.Seconds(), interval.Seconds(), 0, 0, cb, &tc)
	if timer == 0 {
		return func() {}
	}
	tickerMu.Lock()
	tickerCbs[timer] = cb
	tickerMu.Unlock()
	for _, mode := range modes {
		mlAddTimer(mainRunLoop, timer, mode)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			mlTimerInvalidate(timer)
			releaseCF(timer)
			tickerMu.Lock()
			delete(tickerCbs, timer)
			tickerMu.Unlock()
		})
	}
}

// MainLoopAvailable reports whether the run-loop bindings loaded.
func MainLoopAvailable() bool { return loadMainLoop() == nil }
