//go:build darwin

package mac

// Session and idle probes for the 每周简报's Mac use sampling. All three are
// read-only queries that need no permission: CoreGraphics reports how long
// the machine has been without input, CGSession reports whether the login
// session is on the console and its screen unlocked, and CGDisplayIsAsleep
// reports a dark display. Nothing here observes keystrokes or pointers —
// only the elapsed time since the last event.
//
// Main thread only, like the other ObjC-backed calls.

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

// kCGAnyInputEventType asks for the newest of every hardware input event;
// the combined session state is what the user's own devices drive.
const kCGAnyInputEventType = ^uint32(0)

var (
	sessionOnce sync.Once
	sessionErr  error

	cgEventSourceSecondsSinceLastEventType func(state int32, event uint32) float64
	cgSessionCopyCurrentDictionary         func() uintptr
	cgMainDisplayID                        func() uint32
	cfDictionaryGetValue                   func(dict, key uintptr) uintptr
	cfBooleanGetValue                      func(b uintptr) uint8
	cfGetTypeID                            func(ref uintptr) uintptr
	cfBooleanGetTypeID                     func() uintptr
	cfNumberGetTypeID                      func() uintptr
	cfReleaseSession                       func(ref uintptr)
)

// loadSession resolves the CoreGraphics entry points once. Init() has
// already opened the frameworks; this only names the symbols the sampling
// uses. The CF conversions live in their own vars so this file never races
// another loader's registration.
func loadSession() error {
	sessionOnce.Do(func() {
		if sessionErr = Init(); sessionErr != nil {
			return
		}
		resolve := func(handle uintptr, name string, dst any) {
			if sessionErr != nil {
				return
			}
			defer func() {
				if r := recover(); r != nil {
					sessionErr = errors.New("mac: " + name + ": " + fmt.Sprint(r))
				}
			}()
			purego.RegisterLibFunc(dst, handle, name)
		}
		resolve(libCG, "CGEventSourceSecondsSinceLastEventType", &cgEventSourceSecondsSinceLastEventType)
		resolve(libCG, "CGSessionCopyCurrentDictionary", &cgSessionCopyCurrentDictionary)
		resolve(libCG, "CGMainDisplayID", &cgMainDisplayID)
		// CoreFoundation's dictionary and boolean readers live in
		// CoreFoundation, which the Objective-C runtime already links.
		resolve(purego.RTLD_DEFAULT, "CFDictionaryGetValue", &cfDictionaryGetValue)
		resolve(purego.RTLD_DEFAULT, "CFBooleanGetValue", &cfBooleanGetValue)
		resolve(purego.RTLD_DEFAULT, "CFGetTypeID", &cfGetTypeID)
		resolve(purego.RTLD_DEFAULT, "CFBooleanGetTypeID", &cfBooleanGetTypeID)
		resolve(purego.RTLD_DEFAULT, "CFNumberGetTypeID", &cfNumberGetTypeID)
		resolve(purego.RTLD_DEFAULT, "CFRelease", &cfReleaseSession)
	})
	return sessionErr
}

// IdleSeconds is how long the machine has gone without a hardware input
// event, in seconds. It reads the combined session state; no permission is
// involved and no event content is available. An error means the query
// itself failed, not that the user is active.
func IdleSeconds() (float64, error) {
	if err := loadSession(); err != nil {
		return 0, err
	}
	s := cgEventSourceSecondsSinceLastEventType(kCGEventSourceStateCombinedSessionState, kCGAnyInputEventType)
	if s < 0 {
		return 0, errors.New("mac: idle time unavailable")
	}
	return s, nil
}

// sessionEvidence is what the session dictionary said about one sample.
// present/valid distinguish an absent key (normal while unlocked) from a
// key that is there but not a boolean (unknown).
type sessionEvidence struct {
	onConsole, onConsoleValid bool
	locked, lockPresent, lockValid bool
}

// usable reports whether a console session may be sampled.
//
// CGSession omits CGSSessionScreenIsLocked while the screen is unlocked and
// carries it as true while locked; absence is therefore the unlocked shape,
// not a positive "unlocked" value. Treating absence as locked would stop
// every normal session, so it counts only when the documented console key
// is present and true. A lock key that is present but not a boolean is
// unknown, and unknown locks stop the count.
func (e sessionEvidence) usable() bool {
	if !e.onConsoleValid || !e.onConsole {
		return false
	}
	if !e.lockPresent {
		return true
	}
	if !e.lockValid {
		return false
	}
	return !e.locked
}

// SessionActive reports whether this process runs in a console login
// session whose screen is not locked. The sampling must not count a locked
// screen or a switched-away user as time in an app. A missing session
// dictionary (no GUI session, a failed query) answers false so nothing is
// counted without evidence.
func SessionActive() bool {
	if err := loadSession(); err != nil {
		return false
	}
	dict := cgSessionCopyCurrentDictionary()
	if dict == 0 {
		return false
	}
	defer cfReleaseSession(dict)
	var e sessionEvidence
	withPool(func() {
		e.onConsole, _, e.onConsoleValid = sessionBool(dict, "kCGSSessionOnConsoleKey")
		e.locked, e.lockPresent, e.lockValid = sessionBool(dict, "CGSSessionScreenIsLocked")
	})
	return e.usable()
}

// sessionBool reads one boolean key from the session dictionary. present
// says the key was there; valid says it was a CFBoolean or a CFNumber.
// macOS has carried CGSSessionScreenIsLocked as both across releases; an
// NSNumber is toll-free bridged and answers boolValue. A null value is
// treated as absent.
func sessionBool(dict uintptr, key string) (value, present, valid bool) {
	v := cfDictionaryGetValue(dict, uintptr(nsString(key)))
	if v == 0 {
		return false, false, false
	}
	switch cfGetTypeID(v) {
	case cfBooleanGetTypeID():
		return cfBooleanGetValue(v) != 0, true, true
	case cfNumberGetTypeID():
		return sendInt(id(v), "boolValue") != 0, true, true
	default:
		return false, true, false
	}
}

// MainDisplayAsleep reports whether the main display is dark: the lid
// closed, the display slept, or the Mac asleep. It is named apart from
// DisplayAsleep(display), which asks about one known display.
func MainDisplayAsleep() bool {
	if err := loadSession(); err != nil {
		return false
	}
	id := cgMainDisplayID()
	if id == 0 {
		// No main display is not "awake": unknown time must not count.
		return true
	}
	return cgDisplayIsAsleep(id) != 0
}
