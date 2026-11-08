//go:build darwin

package mac

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Window list introspection for tests and diagnostics: CGWindowList is how
// WindowServer itself sees our overlay — a shown basket that cannot be
// found here is invisible no matter what AppKit believes.

var (
	cfLibrary           uintptr
	cfDictGetValue      func(dict, key id) id
	cfArrayCount        func(arr id) int
	cfArrayValueAtIndex func(arr id, idx int) id
	cfReleaseWL         func(cf uintptr)
	windowListCopy      func(option uint32, winID uint32) id
	rectFromDict        func(dict id, rect *NSRect) bool
	windowBoundsKey     id
	windowOwnerPIDKey   id
	windowLayerKey      id
	windowNameKey       id
	windowNumberKey     id
	windowListOnce      sync.Once
	windowListErr       error
)

// kCGWindowListOptionIncludingWindow asks the list for one window only.
const (
	kCGWindowListOptionIncludingWindow  = 1 << 3
	kCGWindowListOptionOnScreenOnly     = 1 << 0
	kCGWindowListExcludeDesktopElements = 1 << 4
	kCGWindowListNormalWindowLayer      = 0
)

// loadWindowList resolves the CoreFoundation/CoreGraphics entry points once.
// Called lazily by WindowScreenRect; Init must already have run (it has —
// callers reach us only after the app is up).
func loadWindowList() error {
	windowListOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				windowListErr = fmt.Errorf("mac: window list setup: %v", r)
			}
		}()
		var err error
		cfLibrary, err = purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			windowListErr = err
			return
		}
		purego.RegisterLibFunc(&cfDictGetValue, cfLibrary, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&cfArrayCount, cfLibrary, "CFArrayGetCount")
		purego.RegisterLibFunc(&cfArrayValueAtIndex, cfLibrary, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&cfReleaseWL, cfLibrary, "CFRelease")
		purego.RegisterLibFunc(&windowListCopy, libCG, "CGWindowListCopyWindowInfo")
		purego.RegisterLibFunc(&rectFromDict, libCG, "CGRectMakeWithDictionaryRepresentation")
		var copySymbol func(dst *id, src uintptr, size uintptr) uintptr
		purego.RegisterLibFunc(&copySymbol, purego.RTLD_DEFAULT, "memcpy")
		for name, dst := range map[string]*id{
			"kCGWindowBounds":   &windowBoundsKey,
			"kCGWindowOwnerPID": &windowOwnerPIDKey,
			"kCGWindowLayer":    &windowLayerKey,
			"kCGWindowName":     &windowNameKey,
			"kCGWindowNumber":   &windowNumberKey,
		} {
			keyAddr, err := purego.Dlsym(libCG, name)
			if err != nil {
				windowListErr = err
				return
			}
			copySymbol(dst, keyAddr, unsafe.Sizeof(*dst))
		}
	})
	return windowListErr
}

// WindowNumber returns the NSWindow's CGWindowID — the number
// CGWindowListCopyWindowInfo and `screencapture -l` both use.
// Main thread only.
func WindowNumber(window uintptr) int {
	var n int
	withPool(func() {
		n = sendInt(id(window), "windowNumber")
	})
	return n
}

// WindowScreenRect returns the on-screen rect WindowServer reports for the
// window with CGWindowID num, in MyGo top-left screen coordinates. ok is
// false while the window is not ordered in. Main thread only.
func WindowScreenRect(num int) (r NSRect, ok bool) {
	if err := Init(); err != nil || num == 0 {
		return r, false
	}
	if err := loadWindowList(); err != nil {
		return r, false
	}
	withPool(func() {
		list := windowListCopy(kCGWindowListOptionIncludingWindow, uint32(num))
		if list == 0 {
			return
		}
		defer cfReleaseWL(uintptr(list))
		if cfArrayCount(list) < 1 {
			return
		}
		dict := cfArrayValueAtIndex(list, 0)
		bounds := cfDictGetValue(dict, windowBoundsKey)
		if bounds == 0 {
			return
		}
		var out NSRect
		if rectFromDict(bounds, &out) {
			r, ok = out, true
		}
	})
	return r, ok
}

// ScreenWindow is one ordinary on-screen window as WindowServer reports
// it: the window's CGWindowID, its owner's process, and its bounds in MyGo
// top-left screen coordinates. The list NormalWindows returns is in the
// front-to-back order WindowServer hit-tests in.
type ScreenWindow struct {
	Number int
	PID    int
	Bounds NSRect
}

// NormalWindows lists the ordinary on-screen windows (layer 0) of other
// processes, frontmost first. The screenshot overlay uses it to highlight
// and pick the window under the pointer. Read-only; needs no permission
// beyond what listing windows already needs, and returns nothing useful
// when WindowServer withholds other apps' windows. Main thread only.
func NormalWindows() []ScreenWindow {
	if err := Init(); err != nil {
		return nil
	}
	if err := loadWindowList(); err != nil {
		return nil
	}
	self := os.Getpid()
	var out []ScreenWindow
	withPool(func() {
		list := windowListCopy(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements, 0)
		if list == 0 {
			return
		}
		defer cfReleaseWL(uintptr(list))
		for i, n := 0, cfArrayCount(list); i < n; i++ {
			dict := cfArrayValueAtIndex(list, i)
			if dict == 0 {
				continue
			}
			pid := sendInt(cfDictGetValue(dict, windowOwnerPIDKey), "intValue")
			if pid == self {
				continue
			}
			if sendInt(cfDictGetValue(dict, windowLayerKey), "intValue") != kCGWindowListNormalWindowLayer {
				continue
			}
			bounds := cfDictGetValue(dict, windowBoundsKey)
			if bounds == 0 {
				continue
			}
			var r NSRect
			if !rectFromDict(bounds, &r) || r.Size.Width <= 0 || r.Size.Height <= 0 {
				continue
			}
			number := 0
			if num := cfDictGetValue(dict, windowNumberKey); num != 0 {
				number = sendInt(num, "intValue")
			}
			out = append(out, ScreenWindow{Number: number, PID: pid, Bounds: r})
		}
	})
	return out
}

// WindowsOfPID lists every on-screen window owned by pid, at any layer.
// The notch's panel sits above the menu bar, so the ordinary layer-0 list
// would miss it. Frontmost first. Main thread only.
func WindowsOfPID(pid int) []ScreenWindow {
	if err := Init(); err != nil {
		return nil
	}
	if err := loadWindowList(); err != nil {
		return nil
	}
	var out []ScreenWindow
	withPool(func() {
		list := windowListCopy(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements, 0)
		if list == 0 {
			return
		}
		defer cfReleaseWL(uintptr(list))
		for i, n := 0, cfArrayCount(list); i < n; i++ {
			dict := cfArrayValueAtIndex(list, i)
			if dict == 0 {
				continue
			}
			if sendInt(cfDictGetValue(dict, windowOwnerPIDKey), "intValue") != pid {
				continue
			}
			bounds := cfDictGetValue(dict, windowBoundsKey)
			if bounds == 0 {
				continue
			}
			var r NSRect
			if !rectFromDict(bounds, &r) || r.Size.Width <= 0 || r.Size.Height <= 0 {
				continue
			}
			number := 0
			if num := cfDictGetValue(dict, windowNumberKey); num != 0 {
				number = sendInt(num, "intValue")
			}
			out = append(out, ScreenWindow{Number: number, PID: pid, Bounds: r})
		}
	})
	return out
}

// ScreenCaptureGrantedByWindowTitles reports whether WindowServer returns
// the titles of other apps' ordinary windows. Without the screen-recording
// grant CGWindowListCopyWindowInfo still lists those windows, but
// kCGWindowName is nil (system chrome such as the menu bar keeps its name,
// so only layer-0 windows owned by another process count). The call never
// prompts and changes nothing. A true answer proves the grant is active
// even while this process's cached preflight answer is still false; a
// false answer only means the grant is not proven — a screen with no other
// titled window looks the same. Main thread only.
func ScreenCaptureGrantedByWindowTitles() bool {
	if err := Init(); err != nil {
		return false
	}
	if err := loadWindowList(); err != nil {
		return false
	}
	granted := false
	self := os.Getpid()
	withPool(func() {
		list := windowListCopy(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements, 0)
		if list == 0 {
			return
		}
		defer cfReleaseWL(uintptr(list))
		for i, n := 0, cfArrayCount(list); i < n; i++ {
			dict := cfArrayValueAtIndex(list, i)
			if dict == 0 {
				continue
			}
			pid := sendInt(cfDictGetValue(dict, windowOwnerPIDKey), "intValue")
			if pid == self {
				continue
			}
			if sendInt(cfDictGetValue(dict, windowLayerKey), "intValue") != kCGWindowListNormalWindowLayer {
				continue
			}
			if name := cfDictGetValue(dict, windowNameKey); name != 0 && goString(name) != "" {
				granted = true
				return
			}
		}
	})
	return granted
}
