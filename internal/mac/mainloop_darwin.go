//go:build darwin

package mac

import (
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Main-loop scheduling. The monitoring loop needs wakeups at a deadline and
// immediate wakes from helper goroutines, both delivered on the AppKit main
// thread. libdispatch's main-queue symbols are not exported by name on this
// system, so this uses CoreFoundation directly: one run-loop timer for the
// single pending deadline and one run-loop source for cross-thread wakes.
// Both are added to kCFRunLoopCommonModes so they fire during tracking too.

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

var (
	mainLoopOnce sync.Once
	mainLoopErr  error

	mlGetMain          func() uintptr
	mlAddTimer         func(runLoop, timer, mode uintptr)
	mlTimerCreate      func(allocator uintptr, fireDate float64, interval float64, flags uintptr, order int, callout uintptr, context *cfRunLoopTimerContext) uintptr
	mlTimerSetNext     func(timer uintptr, at float64)
	mlTime             func() float64
	mlAddSource        func(runLoop, source, mode uintptr)
	mlSourceCreate     func(allocator uintptr, order int, context *cfRunLoopSourceContext) uintptr
	mlSourceSignal     func(source uintptr)
	mlSourceInvalidate func(source uintptr)
	mlWakeUp           func(runLoop uintptr)

	mainRunLoop uintptr
	mainTimer   uintptr
	mainSource  uintptr
	commonMode  uintptr

	mainLoopMu  sync.Mutex
	deadlineJob func()
	wakeJob     func()
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
			!bind(&mlTimerSetNext, "CFRunLoopTimerSetNextFireDate") ||
			!bind(&mlTime, "CFAbsoluteTimeGetCurrent") ||
			!bind(&mlAddSource, "CFRunLoopAddSource") ||
			!bind(&mlSourceCreate, "CFRunLoopSourceCreate") ||
			!bind(&mlSourceSignal, "CFRunLoopSourceSignal") ||
			!bind(&mlSourceInvalidate, "CFRunLoopSourceInvalidate") ||
			!bind(&mlWakeUp, "CFRunLoopWakeUp") {
			return
		}
		modeAddr, err := purego.Dlsym(lib, "kCFRunLoopCommonModes")
		if err != nil {
			mainLoopErr = err
			return
		}
		commonMode = *(*uintptr)(unsafe.Pointer(modeAddr))
		mainRunLoop = mlGetMain()

		timerCb := purego.NewCallback(func(uintptr, uintptr) {
			mainLoopMu.Lock()
			fn := deadlineJob
			deadlineJob = nil
			mainLoopMu.Unlock()
			if fn != nil {
				fn()
			}
		})
		var tc cfRunLoopTimerContext
		mainTimer = mlTimerCreate(0, mlTime()+3600, 3600, 0, 0, timerCb, &tc)
		mlAddTimer(mainRunLoop, mainTimer, commonMode)

		sourceCb := purego.NewCallback(func(uintptr) {
			mainLoopMu.Lock()
			fn := wakeJob
			wakeJob = nil
			mainLoopMu.Unlock()
			if fn != nil {
				fn()
			}
		})
		sc := cfRunLoopSourceContext{perform: sourceCb}
		mainSource = mlSourceCreate(0, 0, &sc)
		mlAddSource(mainRunLoop, mainSource, commonMode)
	})
	return mainLoopErr
}

// AfterMain runs fn on the main run loop after d, replacing any earlier
// deadline. Call on the main thread only (the monitoring loop does).
// A failure leaves the callback unrun rather than executing UI code on a
// worker: the caller can detect load failure through MainLoopAvailable.
func AfterMain(d time.Duration, fn func()) {
	if fn == nil || loadMainLoop() != nil {
		return
	}
	mainLoopMu.Lock()
	deadlineJob = fn
	mainLoopMu.Unlock()
	at := mlTime() + d.Seconds()
	mlTimerSetNext(mainTimer, at)
}

// WakeMain makes fn run on the main run loop as soon as possible. Safe from
// any goroutine.
func WakeMain(fn func()) {
	if fn == nil || loadMainLoop() != nil {
		return
	}
	mainLoopMu.Lock()
	wakeJob = fn
	mainLoopMu.Unlock()
	mlSourceSignal(mainSource)
	mlWakeUp(mainRunLoop)
}

// MainLoopAvailable reports whether the run-loop bindings loaded. The
// monitoring loop uses it to fail visibly instead of silently stopping.
func MainLoopAvailable() bool { return loadMainLoop() == nil }
