//go:build darwin

package mac

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// CGPoint uses global display coordinates, with Y increasing downwards.
type CGPoint struct{ X, Y float64 }

// DragGuardRegion is the notch's protected approach corridor, in screen points.
// Drag events inside it stop at its lower edge, which remains inside the shelf's
// drop target. Other displays and the rest of the menu bar are unaffected.
type DragGuardRegion struct{ X, Y, Width, Height int }

const (
	kCGHIDEventTap                = 0
	kCGSessionEventTap            = 1
	kCGHeadInsertEventTap         = 0
	kCGEventTapOptionDefault      = 0
	kCGEventMouseMoved            = 5
	kCGEventLeftMouseDragged      = 6
	kCGEventOtherMouseDragged     = 27
	cgEventTapDisabledByTimeout   = 0xFFFFFFFE
	cgEventTapDisabledByUserInput = 0xFFFFFFFF

	// pinInterval is how often the pin re-checks the cursor during a file
	// drag. WindowServer reacts to the cursor sitting at the top edge, so a
	// 30 ms UI poll is too slow; 4 ms stays well under one display frame.
	pinInterval = 4 * time.Millisecond
	// tapSlice bounds how long the filter thread sleeps in its run loop
	// before re-checking whether it was asked to stop.
	tapSlice = 0.25
)

var (
	libCoreFoundation             uintptr
	cgEventTapCreate              func(tap, place, options uint32, mask uint64, callback, userInfo uintptr) uintptr
	cgEventTapEnable              func(tap uintptr, enable bool)
	cgEventCreate                 func(source uintptr) uintptr
	cgEventGetLocation            func(event uintptr) CGPoint
	cgEventSetLocation            func(event uintptr, point CGPoint)
	cgWarpMouseCursorPosition     func(point CGPoint) int32
	cgAssociateMouseAndCursor     func(connected bool) int32
	axIsProcessTrusted            func() bool
	axIsProcessTrustedWithOptions func(options uintptr) bool
	cfRunLoopGetCurrent           func() uintptr
	cfRunLoopRunInMode            func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32
	cfRunLoopStop                 func(runLoop uintptr)
	cfMachPortCreateRunLoopSource func(allocator, port uintptr, order int) uintptr
	cfRunLoopAddSource            func(runLoop, source, mode uintptr)
	cfMachPortInvalidate          func(port uintptr)
	cfRunLoopSourceInvalidate     func(source uintptr)
	cfRelease                     func(cf uintptr)
	cfRunLoopCommonModes          uintptr
	cfRunLoopDefaultMode          uintptr
	dragGuardOnce                 sync.Once
	dragGuardErr                  error
	dragGuardArmed                atomic.Pointer[dragGuardState]
	// dragGuardParked makes the armed filter pass drag events through
	// untouched. The app's own island gesture parks it: the corridor's clamp
	// would push that swipe — and the capsule under the pointer — down onto
	// its landing row, which the user never asked for.
	dragGuardParked       atomic.Bool
	dragGuardCallbackOnce sync.Once
	dragGuardCallback     uintptr
)

type dragGuardState struct {
	region DragGuardRegion
	port   uintptr
}

func loadDragGuard() error {
	dragGuardOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				dragGuardErr = fmt.Errorf("mac: drag guard setup failed: %v", r)
			}
		}()
		if err := Init(); err != nil {
			dragGuardErr = err
			return
		}
		var err error
		libCoreFoundation, err = purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			dragGuardErr = err
			return
		}
		services, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			dragGuardErr = err
			return
		}
		purego.RegisterLibFunc(&axIsProcessTrusted, services, "AXIsProcessTrusted")
		purego.RegisterLibFunc(&axIsProcessTrustedWithOptions, services, "AXIsProcessTrustedWithOptions")
		purego.RegisterLibFunc(&cgEventTapCreate, libCG, "CGEventTapCreate")
		purego.RegisterLibFunc(&cgEventTapEnable, libCG, "CGEventTapEnable")
		purego.RegisterLibFunc(&cgEventCreate, libCG, "CGEventCreate")
		purego.RegisterLibFunc(&cgEventGetLocation, libCG, "CGEventGetLocation")
		purego.RegisterLibFunc(&cgEventSetLocation, libCG, "CGEventSetLocation")
		purego.RegisterLibFunc(&cgWarpMouseCursorPosition, libCG, "CGWarpMouseCursorPosition")
		purego.RegisterLibFunc(&cgAssociateMouseAndCursor, libCG, "CGAssociateMouseAndMouseCursorPosition")
		purego.RegisterLibFunc(&cfRunLoopGetCurrent, libCoreFoundation, "CFRunLoopGetCurrent")
		purego.RegisterLibFunc(&cfRunLoopRunInMode, libCoreFoundation, "CFRunLoopRunInMode")
		purego.RegisterLibFunc(&cfRunLoopStop, libCoreFoundation, "CFRunLoopStop")
		purego.RegisterLibFunc(&cfMachPortCreateRunLoopSource, libCoreFoundation, "CFMachPortCreateRunLoopSource")
		purego.RegisterLibFunc(&cfRunLoopAddSource, libCoreFoundation, "CFRunLoopAddSource")
		purego.RegisterLibFunc(&cfMachPortInvalidate, libCoreFoundation, "CFMachPortInvalidate")
		purego.RegisterLibFunc(&cfRunLoopSourceInvalidate, libCoreFoundation, "CFRunLoopSourceInvalidate")
		purego.RegisterLibFunc(&cfRelease, libCoreFoundation, "CFRelease")
		var copySymbol func(dst *uintptr, src uintptr, size uintptr) uintptr
		purego.RegisterLibFunc(&copySymbol, purego.RTLD_DEFAULT, "memcpy")
		for name, dst := range map[string]*uintptr{
			"kCFRunLoopCommonModes": &cfRunLoopCommonModes,
			"kCFRunLoopDefaultMode": &cfRunLoopDefaultMode,
		} {
			addr, err := purego.Dlsym(libCoreFoundation, name)
			if err != nil {
				dragGuardErr = err
				return
			}
			copySymbol(dst, addr, unsafe.Sizeof(*dst))
		}
	})
	return dragGuardErr
}

// AccessibilityTrusted reports whether this process may install an active
// event tap. It never asks the user.
func AccessibilityTrusted() bool {
	return loadDragGuard() == nil && axIsProcessTrusted()
}

// RequestAccessibility is the user-initiated path to grant the permission.
// It asks macOS to show its own prompt; the Settings UI also offers the
// 辅助功能 pane and the app icon as a drag source, so a stale entry can be
// replaced by the user. It never resets a TCC record: a reset could delete
// a grant the user just made, and clearing records is the user's decision
// in System Settings, not a side effect of asking.
func RequestAccessibility() bool {
	if err := loadDragGuard(); err != nil {
		return false
	}
	if axIsProcessTrusted() {
		return true
	}
	trusted := false
	withPool(func() {
		key := nsString("AXTrustedCheckOptionPrompt")
		num := send(class("NSNumber"), "numberWithBool:", 1)
		dict := send(class("NSDictionary"), "dictionaryWithObject:forKey:", uintptr(num), uintptr(key))
		trusted = axIsProcessTrustedWithOptions(uintptr(dict))
	})
	return trusted
}

// OpenAccessibilityPane opens 系统设置 → 隐私与安全性 → 辅助功能, the list
// the user drags the app icon into or uses + in.
func OpenAccessibilityPane() {
	openURL(accessibilityPane)
}

const accessibilityPane = "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"

func clampDragPoint(p CGPoint, r DragGuardRegion) CGPoint {
	if r.Width <= 0 || r.Height <= 0 || p.X < float64(r.X) || p.X >= float64(r.X+r.Width) ||
		p.Y < float64(r.Y) || p.Y >= float64(r.Y+r.Height) {
		return p
	}
	p.Y = float64(r.Y + r.Height)
	return p
}

// warpCursor moves the hardware cursor and immediately re-associates the
// mouse with it; without that, macOS freezes pointer input for ~250 ms after
// every warp and the drag visibly stalls.
func warpCursor(p CGPoint) {
	cgWarpMouseCursorPosition(p)
	cgAssociateMouseAndCursor(true)
}

// A single trampoline serves every drag; purego callbacks cannot be freed.
// It runs on the filter's own thread and only compares and rewrites points.
func dragGuardCallbackAddr() uintptr {
	dragGuardCallbackOnce.Do(func() {
		dragGuardCallback = purego.NewCallback(func(_ uintptr, typ uint32, event uintptr, _ uintptr) uintptr {
			s := dragGuardArmed.Load()
			if s == nil || dragGuardParked.Load() {
				return event
			}
			switch typ {
			case kCGEventMouseMoved:
				if !cgEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonLeft) {
					return event
				}
				fallthrough
			case kCGEventLeftMouseDragged, kCGEventOtherMouseDragged:
				if event != 0 {
					p := cgEventGetLocation(event)
					if q := clampDragPoint(p, s.region); q != p {
						// Rewrite the event and the hardware cursor:
						// WindowServer's Spaces edge reads the cursor, not
						// only the dragged-event location.
						cgEventSetLocation(event, q)
						warpCursor(q)
					}
				}
			case cgEventTapDisabledByTimeout, cgEventTapDisabledByUserInput:
				// The event argument is NOT the tap's mach port.
				cgEventTapEnable(s.port, true)
			}
			return event
		})
	})
	return dragGuardCallback
}

// StartDragGuard installs an active mouse-drag filter on a dedicated thread,
// so a busy UI thread can neither delay drag events nor get the filter
// disabled by the system for being slow. It never requests a permission.
// Missing trust or native setup failure is returned to the caller.
// Only one guard may be active; start/stop are called on the AppKit main thread.
func StartDragGuard(region DragGuardRegion) (func(), error) {
	if region.Width <= 0 || region.Height <= 0 {
		return nil, errors.New("notch drag guard region is invalid")
	}
	if err := loadDragGuard(); err != nil {
		return nil, err
	}
	if !axIsProcessTrusted() {
		return nil, errors.New("Rakko has no Accessibility permission yet")
	}
	if dragGuardArmed.Load() != nil {
		return nil, errors.New("mac: a drag guard is already active")
	}
	var (
		stopping atomic.Bool
		ready    = make(chan uintptr, 1) // filter thread's run loop, 0 on failure
		done     = make(chan struct{})
		failure  error
	)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		// HID-level first: WindowServer's edge gesture reads the cursor
		// before a session tap can rewrite it. Fall back to a session tap
		// when the process is trusted but the HID tap is refused.
		const dragMask = 1<<kCGEventLeftMouseDragged | 1<<kCGEventMouseMoved | 1<<kCGEventOtherMouseDragged
		port := cgEventTapCreate(kCGHIDEventTap, kCGHeadInsertEventTap, kCGEventTapOptionDefault,
			dragMask, dragGuardCallbackAddr(), 0)
		if port == 0 {
			port = cgEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap, kCGEventTapOptionDefault,
				dragMask, dragGuardCallbackAddr(), 0)
		}
		if port == 0 {
			failure = errors.New("mac: unable to create active drag filter")
			ready <- 0
			return
		}
		source := cfMachPortCreateRunLoopSource(0, port, 0)
		if source == 0 {
			cfMachPortInvalidate(port)
			cfRelease(port)
			failure = errors.New("mac: unable to create drag filter run-loop source")
			ready <- 0
			return
		}
		loop := cfRunLoopGetCurrent()
		dragGuardArmed.Store(&dragGuardState{region: region, port: port})
		cfRunLoopAddSource(loop, source, cfRunLoopCommonModes)
		cgEventTapEnable(port, true)
		ready <- loop
		for !stopping.Load() {
			cfRunLoopRunInMode(cfRunLoopDefaultMode, tapSlice, false)
		}
		cgEventTapEnable(port, false)
		cfRunLoopSourceInvalidate(source)
		cfMachPortInvalidate(port)
		cfRelease(source)
		cfRelease(port)
	}()
	loop := <-ready
	if loop == 0 {
		<-done
		return nil, failure
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			dragGuardArmed.Store(nil)
			stopping.Store(true)
			cfRunLoopStop(loop)
			<-done
		})
	}, nil
}

// SetDragGuardRegion updates the armed corridor without tearing the tap
// down. No-op when no guard is running.
func SetDragGuardRegion(region DragGuardRegion) {
	s := dragGuardArmed.Load()
	if s == nil || region.Width <= 0 || region.Height <= 0 {
		return
	}
	dragGuardArmed.Store(&dragGuardState{region: region, port: s.port})
}

// ParkDragGuard parks or unparks the armed filter, if any: a parked filter
// clamps nothing and rewrites no event, and it keeps its corridor for the
// unpark. The tap deliberately stays installed — the app parks it for its own
// island gesture, which must never be pushed by the notch corridor, and a
// teardown there would cost a new tap (and a new permission check) on every
// click. Unlike the region, parking is independent of whether a tap exists,
// so the caller may set it at any time. Main thread only, like StartDragGuard.
func ParkDragGuard(parked bool) {
	dragGuardParked.Store(parked)
}

var pin struct {
	region atomic.Pointer[DragGuardRegion]
	// armed is set by each SetDragPin(active) call — made only while
	// DragState sees the button held — and cleared by the pin itself on
	// button release, so a click right after a drop is never moved.
	armed atomic.Bool
	stop  chan struct{} // non-nil while the pin goroutine runs; main thread only
	done  chan struct{}
}

// SetDragPin keeps the cursor out of region while a file drag is active,
// independently of the event filter: it needs no permission and catches the
// cursor even when no drag event reaches the filter. It polls the cursor on
// its own goroutine every pinInterval and only runs during drags.
// Main thread only.
func SetDragPin(region DragGuardRegion, active bool) {
	if !active || region.Width <= 0 || region.Height <= 0 || loadDragGuard() != nil {
		pin.armed.Store(false)
		if pin.stop != nil {
			close(pin.stop)
			<-pin.done
			pin.stop, pin.done = nil, nil
		}
		return
	}
	pin.region.Store(&region)
	pin.armed.Store(true)
	if pin.stop != nil {
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	pin.stop, pin.done = stop, done
	go func() {
		defer close(done)
		t := time.NewTicker(pinInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			if !pin.armed.Load() {
				continue
			}
			if !cgEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonLeft) {
				pin.armed.Store(false)
				continue
			}
			ev := cgEventCreate(0)
			if ev == 0 {
				continue
			}
			p := cgEventGetLocation(ev)
			cfRelease(ev)
			if q := clampDragPoint(p, *pin.region.Load()); q != p {
				warpCursor(q)
			}
		}
	}()
}
