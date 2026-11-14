//go:build darwin

package platform

// The power-source observer: IOKit's IOPSNotificationCreateRunLoopSource
// posts on the main run loop whenever the system's power sources change —
// adapter attach or detach, charge state, percentage. The battery reader in
// battery_darwin.go stays the source of truth; this only wakes whoever
// polls it, so a plug-in notice no longer waits for the next poll. The
// callbacks arrive far more often than the two transitions matter (every
// percent step while charging), so the woken poller keeps its own edge
// logic and posts nothing for a state it already reported.
//
// Main thread only, like the other run-loop and ObjC-backed calls.

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	powerObserverOnce sync.Once
	powerObserverErr  error
	// powerObserverMu guards the wake function the trampoline calls; the
	// trampoline runs on the main run loop, the same thread that installs
	// it.
	powerObserverMu  sync.Mutex
	powerObserverFn  func()
	powerObserverCb  uintptr
	powerObserverSrc uintptr

	iopsNotificationSource func(callback, context uintptr) uintptr
	powerRunLoopGetMain    func() uintptr
	powerRunLoopAddSource  func(runLoop, source, mode uintptr)
	powerCommonModes       uintptr
)

// loadPowerObserver resolves IOKit and CoreFoundation once.
func loadPowerObserver() error {
	powerObserverOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				powerObserverErr = fmt.Errorf("mac: power-source observer setup failed: %v", r)
			}
		}()
		ioKit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit",
			purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			powerObserverErr = fmt.Errorf("mac: cannot load IOKit: %w", err)
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			powerObserverErr = fmt.Errorf("mac: cannot load CoreFoundation: %w", err)
			return
		}
		purego.RegisterLibFunc(&iopsNotificationSource, ioKit, "IOPSNotificationCreateRunLoopSource")
		purego.RegisterLibFunc(&powerRunLoopGetMain, cf, "CFRunLoopGetMain")
		purego.RegisterLibFunc(&powerRunLoopAddSource, cf, "CFRunLoopAddSource")
		// kCFRunLoopCommonModes is a constant CFStringRef: copy the pointer
		// out of the symbol's storage, as the drag guard does for its modes.
		var copySymbol func(dst *uintptr, src uintptr, size uintptr) uintptr
		purego.RegisterLibFunc(&copySymbol, purego.RTLD_DEFAULT, "memcpy")
		addr, err := purego.Dlsym(cf, "kCFRunLoopCommonModes")
		if err != nil {
			powerObserverErr = err
			return
		}
		copySymbol(&powerCommonModes, addr, unsafe.Sizeof(powerCommonModes))
	})
	return powerObserverErr
}

// StartPowerObserver installs IOKit's power-source notification source on
// the main run loop in the common modes and calls wake on the main thread
// for every change. The source stays installed for the rest of the
// process; a later call only replaces wake. It returns the created source,
// or 0 with an error when IOKit or CoreFoundation cannot be reached — the
// caller then keeps its poll as the only path.
func StartPowerObserver(wake func()) (uintptr, error) {
	powerObserverMu.Lock()
	powerObserverFn = wake
	powerObserverMu.Unlock()
	if err := loadPowerObserver(); err != nil {
		return 0, err
	}
	if powerObserverSrc != 0 {
		return powerObserverSrc, nil
	}
	src := iopsNotificationSource(powerObserverCallbackAddr(), 0)
	if src == 0 {
		return 0, fmt.Errorf("mac: IOKit returned no power-source run-loop source")
	}
	// The created source carries a +1 reference that is never released, so
	// the run loop keeps a live source for the life of the app.
	powerRunLoopAddSource(powerRunLoopGetMain(), src, powerCommonModes)
	powerObserverSrc = src
	return src, nil
}

// The trampoline is one per process: purego callbacks cannot be freed. It
// runs on the main run loop and must stay trivial — only the poller's wake
// hook runs here, never IOKit work of its own.
func powerObserverCallbackAddr() uintptr {
	if powerObserverCb == 0 {
		powerObserverCb = purego.NewCallback(func(_ uintptr) uintptr {
			powerObserverMu.Lock()
			fn := powerObserverFn
			powerObserverMu.Unlock()
			if fn != nil {
				fn()
			}
			return 0
		})
	}
	return powerObserverCb
}
