//go:build darwin

package mac

import (
	"sync"

	"github.com/ebitengine/purego"
)

// kV is the ANSI V key code, the one ⌘V stands for.
const (
	keyCodeV            = 9
	kCGEventFlagMaskCmd = 0x00100000
)

var (
	pasteOnce    sync.Once
	pasteLoadErr error

	cgKeyEvent   func(source uintptr, keycode uint16, keydown bool) uintptr
	cgSetFlags   func(event uintptr, flags uint64)
	cgPostToPid  func(pid int32, event uintptr)
	cfReleaseKey func(cf uintptr)
)

// loadPaste resolves CoreGraphics' keyboard event entry points once.
func loadPaste() error {
	pasteOnce.Do(func() {
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			pasteLoadErr = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			pasteLoadErr = err
			return
		}
		purego.RegisterLibFunc(&cgKeyEvent, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgPostToPid, cg, "CGEventPostToPid")
		purego.RegisterLibFunc(&cfReleaseKey, cf, "CFRelease")
	})
	return pasteLoadErr
}

// PostCommandV sends ⌘V to the process pid — to that process, not to the
// session, so the keystroke cannot land in whichever app happens to be
// frontmost. macOS delivers a synthetic keystroke only from a process trusted
// for Accessibility, so a caller must check AccessibilityTrusted first and
// report a copy, never a paste, when it is false. It reports whether the
// keystroke was posted. Safe on any goroutine; it reads no AppKit state.
func PostCommandV(pid int) bool {
	if pid <= 0 || loadPaste() != nil {
		return false
	}
	down := cgKeyEvent(0, keyCodeV, true)
	up := cgKeyEvent(0, keyCodeV, false)
	if down == 0 || up == 0 {
		if down != 0 {
			cfReleaseKey(down)
		}
		if up != 0 {
			cfReleaseKey(up)
		}
		return false
	}
	defer cfReleaseKey(down)
	defer cfReleaseKey(up)
	cgSetFlags(down, kCGEventFlagMaskCmd)
	cgSetFlags(up, kCGEventFlagMaskCmd)
	cgPostToPid(int32(pid), down)
	cgPostToPid(int32(pid), up)
	return true
}
