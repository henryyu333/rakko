//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Accessibility (AX) window access for 拖窗口到刘海分屏: find the window
// under a press, follow it while it is dragged, and set its frame on
// release. Every function here needs the process to be trusted for
// Accessibility; callers check CanSendInput first, because a
// denied AX call returns an error but cannot prompt. Main thread only.

// AppWindow owns one AXUIElementRef. Release it with Release;
// a zero ref is inert.
type AppWindow struct{ el uintptr }

// Valid reports whether the ref holds an element.
func (w AppWindow) Valid() bool { return w.el != 0 }

// ErrAppWindowResize is returned when the window accepted the move but refused
// the size change (or its size is not settable at all).
var ErrAppWindowResize = errors.New("mac: the window cannot be resized")

const (
	kAXValueCGPointType = 1
	kAXValueCGSizeType  = 2
)

var (
	axOnce sync.Once
	axErr  error

	axCopyElementAtPosition func(system uintptr, x, y float32, out *uintptr) int32
	axCopyAttributeValue    func(el, attr uintptr, out *uintptr) int32
	axSetAttributeValue     func(el, attr, value uintptr) int32
	axIsAttributeSettable   func(el, attr uintptr, out *byte) int32
	axGetPid                func(el uintptr, pid *int32) int32
	axValueCreate           func(typ int32, value unsafe.Pointer) uintptr
	axValueGetValue         func(value uintptr, typ int32, out unsafe.Pointer) bool
	axSystemWide            uintptr
	axCFRelease             func(cf uintptr)
)

// loadAX resolves the ApplicationServices entry points once. Init must
// already have run (every caller is past app startup).
func loadAX() error {
	axOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				axErr = fmt.Errorf("mac: accessibility setup: %v", r)
			}
		}()
		lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices",
			purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			axErr = err
			return
		}
		purego.RegisterLibFunc(&axCopyElementAtPosition, lib, "AXUIElementCopyElementAtPosition")
		purego.RegisterLibFunc(&axCopyAttributeValue, lib, "AXUIElementCopyAttributeValue")
		purego.RegisterLibFunc(&axSetAttributeValue, lib, "AXUIElementSetAttributeValue")
		purego.RegisterLibFunc(&axIsAttributeSettable, lib, "AXUIElementIsAttributeSettable")
		purego.RegisterLibFunc(&axGetPid, lib, "AXUIElementGetPid")
		purego.RegisterLibFunc(&axValueCreate, lib, "AXValueCreate")
		purego.RegisterLibFunc(&axValueGetValue, lib, "AXValueGetValue")
		purego.RegisterLibFunc(&axSystemWideCreate, lib, "AXUIElementCreateSystemWide")
		cfLib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			axErr = err
			return
		}
		purego.RegisterLibFunc(&axCFRelease, cfLib, "CFRelease")
		axSystemWide = axSystemWideCreate()
		if axSystemWide == 0 {
			axErr = errors.New("mac: no system-wide accessibility element")
		}
	})
	return axErr
}

var axSystemWideCreate func() uintptr

// axAttribute copies the named attribute of el into out. The caller owns
// the returned reference. Must run inside withPool: the attribute name is
// an autoreleased NSString bridged to CFString.
func axAttribute(el uintptr, name string, out *uintptr) int32 {
	return axCopyAttributeValue(el, uintptr(nsString(name)), out)
}

// axString reads a string attribute.
func axString(el uintptr, name string) string {
	var v uintptr
	if axAttribute(el, name, &v) != 0 || v == 0 {
		return ""
	}
	s := goString(id(v))
	axCFRelease(v)
	return s
}

// AppWindowAt returns the window containing the screen point, with its
// owner pid. The caller must release the ref. ok is false when the point
// is over no window (the desktop background still answers Finder's
// desktop window; that one is not movable and callers reject it).
func AppWindowAt(x, y float64) (w AppWindow, pid int, ok bool) {
	if loadAX() != nil {
		return AppWindow{}, 0, false
	}
	var found uintptr
	var foundPID int32
	withPool(func() {
		var el uintptr
		// The C declaration takes float, not CGFloat: a float64 here would
		// pass the low half of its mantissa (0.0 for whole points).
		if axCopyElementAtPosition(axSystemWide, float32(x), float32(y), &el) != 0 || el == 0 {
			return
		}
		for range 16 {
			if el == 0 {
				return
			}
			if axString(el, "AXRole") == "AXWindow" {
				found = el
				axGetPid(el, &foundPID)
				return
			}
			var parent uintptr
			bad := axAttribute(el, "AXParent", &parent) != 0 || parent == 0
			axCFRelease(el)
			if bad {
				return
			}
			el = parent
		}
		if el != 0 {
			axCFRelease(el)
		}
	})
	if found == 0 {
		return AppWindow{}, 0, false
	}
	return AppWindow{el: found}, int(foundPID), true
}

// Release releases the element reference.
func (w AppWindow) Release() {
	if w.el == 0 || loadAX() != nil {
		return
	}
	withPool(func() { axCFRelease(w.el) })
}

// Frame reads the window's position and size.
func (w AppWindow) Frame() (Rect, bool) {
	if w.el == 0 || loadAX() != nil {
		return Rect{}, false
	}
	var r Rect
	var okP, okS bool
	withPool(func() {
		var pv, sv uintptr
		if axAttribute(w.el, "AXPosition", &pv) == 0 && pv != 0 {
			var p [2]float64
			okP = axValueGetValue(pv, kAXValueCGPointType, unsafe.Pointer(&p))
			axCFRelease(pv)
			r.X, r.Y = p[0], p[1]
		}
		if axAttribute(w.el, "AXSize", &sv) == 0 && sv != 0 {
			var s [2]float64
			okS = axValueGetValue(sv, kAXValueCGSizeType, unsafe.Pointer(&s))
			axCFRelease(sv)
			r.Width, r.Height = s[0], s[1]
		}
	})
	return r, okP && okS
}

// Settable reports whether the window's position (move) and size (resize)
// can be set. A movable window answers true for position.
func (w AppWindow) Settable() (move, resize bool) {
	if w.el == 0 || loadAX() != nil {
		return false, false
	}
	withPool(func() {
		var m, s byte
		if axAttributeSettable(w.el, "AXPosition", &m) == 0 {
			move = m != 0
		}
		if axAttributeSettable(w.el, "AXSize", &s) == 0 {
			resize = s != 0
		}
	})
	return move, resize
}

func axAttributeSettable(el uintptr, name string, out *byte) int32 {
	return axIsAttributeSettable(el, uintptr(nsString(name)), out)
}

// MoveResize sets the window's position, then its size when resize is
// true. Position first: a window resized at its old origin may push the
// title bar under the menu bar before the move lands. ErrAppWindowResize means
// the move may already have been applied.
func (w AppWindow) MoveResize(r Rect, resize bool) error {
	if w.el == 0 || loadAX() != nil {
		return errors.New("mac: accessibility window unavailable")
	}
	var moveErr, sizeErr int32
	withPool(func() {
		var p [2]float64 = [2]float64{r.X, r.Y}
		if pv := axValueCreate(kAXValueCGPointType, unsafe.Pointer(&p)); pv != 0 {
			moveErr = axSetAttributeValue(w.el, uintptr(nsString("AXPosition")), pv)
			axCFRelease(pv)
		} else {
			moveErr = -1
		}
		if moveErr != 0 || !resize {
			return
		}
		var s [2]float64 = [2]float64{r.Width, r.Height}
		if sv := axValueCreate(kAXValueCGSizeType, unsafe.Pointer(&s)); sv != 0 {
			sizeErr = axSetAttributeValue(w.el, uintptr(nsString("AXSize")), sv)
			axCFRelease(sv)
		} else {
			sizeErr = -1
		}
	})
	if moveErr != 0 {
		return fmt.Errorf("mac: setting the window position failed (%d)", moveErr)
	}
	if sizeErr != 0 {
		return ErrAppWindowResize
	}
	return nil
}
