//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// CGPoint uses global display coordinates, with Y increasing downwards.
type CGPoint struct{ X, Y float64 }

const (
	kCGHIDEventTap                = 0
	kCGSessionEventTap            = 1
	kCGHeadInsertEventTap         = 0
	kCGEventTapOptionDefault      = 0
	kCGEventLeftMouseDown         = 1
	kCGEventLeftMouseUp           = 2
	kCGEventMouseMoved            = 5
	kCGEventLeftMouseDragged      = 6
	kCGEventOtherMouseDragged     = 27
	cgEventTapDisabledByTimeout   = 0xFFFFFFFE
	cgEventTapDisabledByUserInput = 0xFFFFFFFF

	kPasteboardModified = 1
	cfUTF8Encoding      = 0x08000100

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
	pbCreate                      func(name uintptr, out *uintptr) int32
	pbSync                        func(pb uintptr) uint32
	pbGetItemCount                func(pb uintptr, count *uintptr) int32
	pbGetItemIdentifier           func(pb uintptr, index int, item *uintptr) int32
	pbCopyItemFlavors             func(pb uintptr, item uintptr, out *uintptr) int32
	cfArrayGetCount               func(arr uintptr) int
	cfArrayGetValueAtIndex        func(arr uintptr, idx int) uintptr
	cfStringGetCString            func(s uintptr, buf *byte, maxLen int, enc uint32) bool
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
	// dragGuardParked makes the armed filter pass events through untouched.
	dragGuardParked       atomic.Bool
	dragGuardCallbackOnce sync.Once
	dragGuardCallback     uintptr

	macEdgeMu    sync.Mutex
	macEdgeState EdgeGuardState

	// Tap-thread-only drag pasteboard state; accessed solely inside the serial
	// CGEventTap callback.
	tapDragPB      uintptr
	tapDragPBInited bool
	tapDragFile    bool
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
		purego.RegisterLibFunc(&pbCreate, services, "PasteboardCreate")
		purego.RegisterLibFunc(&pbSync, services, "PasteboardSynchronize")
		purego.RegisterLibFunc(&pbGetItemCount, services, "PasteboardGetItemCount")
		purego.RegisterLibFunc(&pbGetItemIdentifier, services, "PasteboardGetItemIdentifier")
		purego.RegisterLibFunc(&pbCopyItemFlavors, services, "PasteboardCopyItemFlavors")
		purego.RegisterLibFunc(&cgEventTapCreate, libCG, "CGEventTapCreate")
		purego.RegisterLibFunc(&cgEventTapEnable, libCG, "CGEventTapEnable")
		purego.RegisterLibFunc(&cgEventCreate, libCG, "CGEventCreate")
		purego.RegisterLibFunc(&cgEventGetLocation, libCG, "CGEventGetLocation")
		purego.RegisterLibFunc(&cgEventSetLocation, libCG, "CGEventSetLocation")
		purego.RegisterLibFunc(&cgWarpMouseCursorPosition, libCG, "CGWarpMouseCursorPosition")
		purego.RegisterLibFunc(&cgAssociateMouseAndCursor, libCG, "CGAssociateMouseAndMouseCursorPosition")
		purego.RegisterLibFunc(&cfArrayGetCount, libCoreFoundation, "CFArrayGetCount")
		purego.RegisterLibFunc(&cfArrayGetValueAtIndex, libCoreFoundation, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&cfStringGetCString, libCoreFoundation, "CFStringGetCString")
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

// CanSendInput reports whether this process may install an active
// event tap. It never asks the user.
func CanSendInput() bool {
	return loadDragGuard() == nil && axIsProcessTrusted()
}

// RequestAccessibility is the user-initiated path to grant the permission.
func RequestAccessibility() bool {
	if err := loadDragGuard(); err != nil {
		return false
	}
	if axIsProcessTrusted() {
		return true
	}
	if id := bundleIdentifier(); id != "" {
		_ = exec.Command("/usr/bin/tccutil", "reset", "Accessibility", id).Run()
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

func bundleIdentifier() string {
	var out string
	withPool(func() {
		out = goString(send(send(class("NSBundle"), "mainBundle"), "bundleIdentifier"))
	})
	return out
}

func clampDragPoint(p CGPoint, r DragGuardRegion) CGPoint {
	nx, ny, changed := clampCorridorPoint(p.X, p.Y, r)
	if !changed {
		return p
	}
	return CGPoint{X: nx, Y: ny}
}

func evalMacEdgeGuard(p CGPoint, r DragGuardRegion, parked, leftDown, fileDrag bool) (CGPoint, bool) {
	macEdgeMu.Lock()
	nx, ny, changed := macEdgeState.Eval(p.X, p.Y, r, parked, leftDown, fileDrag, true)
	macEdgeMu.Unlock()
	if !changed {
		return p, false
	}
	return CGPoint{X: nx, Y: ny}, true
}

// warpCursor moves the hardware cursor and immediately re-associates the
// mouse with it; without that, macOS freezes pointer input for ~250 ms after
// every warp and the drag visibly stalls.
func warpCursor(p CGPoint) {
	cgWarpMouseCursorPosition(p)
	cgAssociateMouseAndCursor(true)
}

func ensureTapDragPB() uintptr {
	if !tapDragPBInited {
		tapDragPBInited = true
		if dragBoardName != 0 && pbCreate != nil {
			var pb uintptr
			if pbCreate(uintptr(dragBoardName), &pb) == 0 && pb != 0 {
				pbSync(pb)
				tapDragPB = pb
			}
		}
	}
	return tapDragPB
}

func tapDragBoardCarriesFiles(pb uintptr) bool {
	if pb == 0 {
		return false
	}
	var count uintptr
	if pbGetItemCount(pb, &count) != 0 || count == 0 {
		return false
	}
	if count > 8 {
		count = 8
	}
	var flavors []string
	var buf [128]byte
	for i := 1; i <= int(count); i++ {
		var itemID uintptr
		if pbGetItemIdentifier(pb, i, &itemID) != 0 {
			continue
		}
		var arr uintptr
		if pbCopyItemFlavors(pb, itemID, &arr) != 0 || arr == 0 {
			continue
		}
		n := cfArrayGetCount(arr)
		for j := 0; j < n; j++ {
			cfStr := cfArrayGetValueAtIndex(arr, j)
			if cfStr != 0 && cfStringGetCString(cfStr, &buf[0], len(buf), cfUTF8Encoding) {
				for k, b := range buf {
					if b == 0 {
						flavors = append(flavors, string(buf[:k]))
						break
					}
				}
			}
		}
		cfRelease(arr)
	}
	return DragMayCarryFiles(flavors)
}

func tapUpdateFileDrag(leftDown bool) bool {
	pb := ensureTapDragPB()
	if !leftDown {
		if tapDragFile && pb != 0 {
			pbSync(pb)
		}
		tapDragFile = false
		return false
	}
	if pb != 0 && pbSync(pb)&kPasteboardModified != 0 {
		tapDragFile = tapDragBoardCarriesFiles(pb)
	}
	return tapDragFile
}

// A single trampoline serves every event; purego callbacks cannot be freed.
func dragGuardCallbackAddr() uintptr {
	dragGuardCallbackOnce.Do(func() {
		dragGuardCallback = purego.NewCallback(func(_ uintptr, typ uint32, event uintptr, _ uintptr) uintptr {
			s := dragGuardArmed.Load()
			if s == nil {
				return event
			}
			parked := dragGuardParked.Load()
			switch typ {
			case kCGEventLeftMouseDown:
				if pb := ensureTapDragPB(); pb != 0 {
					pbSync(pb)
				}
				tapDragFile = false
				if event != 0 && !parked {
					p := cgEventGetLocation(event)
					evalMacEdgeGuard(p, s.region, false, true, false)
				}
			case kCGEventLeftMouseUp:
				if pb := ensureTapDragPB(); pb != 0 {
					pbSync(pb)
				}
				tapDragFile = false
				if event != 0 && !parked {
					p := cgEventGetLocation(event)
					evalMacEdgeGuard(p, s.region, false, false, false)
				}
			case kCGEventMouseMoved:
				leftDown := cgEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonLeft)
				fileDrag := tapUpdateFileDrag(leftDown)
				if event != 0 {
					p := cgEventGetLocation(event)
					if q, changed := evalMacEdgeGuard(p, s.region, parked, leftDown, fileDrag); changed {
						cgEventSetLocation(event, q)
						warpCursor(q)
					}
				}
			case kCGEventLeftMouseDragged, kCGEventOtherMouseDragged:
				fileDrag := tapUpdateFileDrag(true)
				if event != 0 {
					p := cgEventGetLocation(event)
					if q, changed := evalMacEdgeGuard(p, s.region, parked, true, fileDrag); changed {
						cgEventSetLocation(event, q)
						warpCursor(q)
					}
				}
			case cgEventTapDisabledByTimeout, cgEventTapDisabledByUserInput:
				cgEventTapEnable(s.port, true)
			}
			return event
		})
	})
	return dragGuardCallback
}

// StartDragGuard installs an active mouse filter on a dedicated thread.
// Missing Accessibility trust or setup failure returns an error without
// prompting the user. Main thread only.
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
	if ev := cgEventCreate(0); ev != 0 {
		p := cgEventGetLocation(ev)
		cfRelease(ev)
		macEdgeMu.Lock()
		macEdgeState.Seed(p.Y)
		macEdgeMu.Unlock()
	}
	var (
		stopping atomic.Bool
		ready    = make(chan uintptr, 1)
		done     = make(chan struct{})
		failure  error
	)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		const dragMask = 1<<kCGEventLeftMouseDown |
			1<<kCGEventLeftMouseUp |
			1<<kCGEventMouseMoved |
			1<<kCGEventLeftMouseDragged |
			1<<kCGEventOtherMouseDragged
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
			macEdgeMu.Lock()
			macEdgeState.Reset()
			macEdgeMu.Unlock()
		})
	}, nil
}

// SetDragGuardRegion updates the armed region without tearing the tap down.
func SetDragGuardRegion(region DragGuardRegion) {
	s := dragGuardArmed.Load()
	if s == nil || region.Width <= 0 || region.Height <= 0 {
		return
	}
	if s.region != region {
		macEdgeMu.Lock()
		macEdgeState.Reset()
		macEdgeMu.Unlock()
	}
	dragGuardArmed.Store(&dragGuardState{region: region, port: s.port})
}

// ParkDragGuard parks or unparks the armed filter.
func ParkDragGuard(parked bool) {
	dragGuardParked.Store(parked)
}

var pin struct {
	region atomic.Pointer[DragGuardRegion]
	armed  atomic.Bool
	stop   chan struct{}
	done   chan struct{}
}

// SetDragPin keeps the cursor out of the legacy drop corridor during drags when
// no Accessibility event tap is handling upper-display barriers. Main thread only.
func SetDragPin(region DragGuardRegion, active bool) {
	if s := dragGuardArmed.Load(); s != nil && s.region.BarrierEnabled {
		active = false
	}
	// Untrusted processes never block on upper displays; keep single-screen
	// corridor pin behavior identical to before.
	region.BarrierEnabled = false
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
			if q, changed := evalMacEdgeGuard(p, *pin.region.Load(), dragGuardParked.Load(), true, true); changed {
				warpCursor(q)
			}
		}
	}()
}
