//go:build darwin

package platform

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Power assertions (IOPMAssertionCreateWithName / IOPMAssertionRelease,
// IOKit) keep the Mac from falling idle-asleep. The type selects what is
// prevented: the system's idle sleep leaves the display free to dim and
// turn off; the display's idle sleep implies it. Releasing the id (or the
// process dying) ends the hold. Main thread only.

// PowerAssertionKind names the behaviour an assertion holds.
type PowerAssertionKind int

const (
	// PowerAssertSystemSleep (kIOPMAssertionTypeNoIdleSleep) blocks system
	// idle sleep; the display still sleeps.
	PowerAssertSystemSleep PowerAssertionKind = iota
	// PowerAssertDisplaySleep (kIOPMAssertionTypeNoDisplaySleep) also keeps
	// the screen awake.
	PowerAssertDisplaySleep
)

// PowerAssertionID is the handle an assertion creation writes.
type PowerAssertionID = uint32

// The assertion-type constants are CFSTR literals, not exported symbols:
// dlsym cannot find them, so the strings are passed by name.
var powerAssertionTypes = map[PowerAssertionKind]string{
	PowerAssertSystemSleep:  "PreventUserIdleSystemSleep",
	PowerAssertDisplaySleep: "PreventUserIdleDisplaySleep",
}

const (
	kIOReturnSuccess      = 0
	powerAssertionLevelOn = 255 // kIOPMAssertionLevelOn
)

var (
	powerOnce    sync.Once
	powerLoadErr error
	// Resolved addresses; the calls go through SyscallN like send() —
	// RegisterFunc mis-marshals this signature on arm64.
	powerCreateAddr, powerFreeAddr, powerPropsAddr uintptr
	powerCFRelease                                 func(obj uintptr)
)

// loadPower opens IOKit once and resolves the assertion calls.
func loadPower() error {
	powerOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				powerLoadErr = fmt.Errorf("mac: IOKit setup failed: %v", r)
			}
		}()
		libIOKit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			powerLoadErr = fmt.Errorf("mac: cannot load IOKit: %w", err)
			return
		}
		for name, dst := range map[string]*uintptr{
			"IOPMAssertionCreateWithName": &powerCreateAddr,
			"IOPMAssertionRelease":        &powerFreeAddr,
			"IOPMAssertionCopyProperties": &powerPropsAddr,
		} {
			if *dst, err = purego.Dlsym(libIOKit, name); err != nil {
				powerLoadErr = fmt.Errorf("mac: %w", err)
				return
			}
		}
		purego.RegisterLibFunc(&powerCFRelease, purego.RTLD_DEFAULT, "CFRelease")
	})
	return powerLoadErr
}

// NewPowerAssertion creates a named IOKit power assertion of kind and
// returns its id. name becomes the assertion's recorded name (AssertName,
// what `pmset -g assertions` prints as `named:` when the string is ASCII).
// The CFString arguments are passed through the toll-free NSString that
// the current autorelease pool keeps; IOPMAssertionCreateWithName copies
// them.
//
//go:uintptrescapes
func NewPowerAssertion(kind PowerAssertionKind, name string) (PowerAssertionID, error) {
	typ, ok := powerAssertionTypes[kind]
	if !ok {
		return 0, fmt.Errorf("mac: unknown power assertion kind %d", int(kind))
	}
	if err := loadPower(); err != nil {
		return 0, err
	}
	var out PowerAssertionID
	var ret uintptr
	withPool(func() {
		ret, _, _ = purego.SyscallN(powerCreateAddr,
			uintptr(nsString(typ)), powerAssertionLevelOn, uintptr(nsString(name)),
			uintptr(unsafe.Pointer(&out)))
	})
	if ret != kIOReturnSuccess {
		return 0, fmt.Errorf("IOPMAssertionCreateWithName: IOReturn %#x", uint32(ret))
	}
	return out, nil
}

// PowerAssertionName returns the AssertName powerd recorded for
// assertionID — the string `pmset -g assertions` prints after `named:`.
// pmset itself cannot render CJK there, so tests read the property back.
func PowerAssertionName(assertionID PowerAssertionID) string {
	if assertionID == 0 || loadPower() != nil {
		return ""
	}
	var name string
	withPool(func() {
		dict, _, _ := purego.SyscallN(powerPropsAddr, uintptr(assertionID))
		if dict == 0 {
			return
		}
		defer func() { powerCFRelease(dict) }()
		name = goString(send(id(dict), "objectForKey:", uintptr(nsString("AssertName"))))
	})
	return name
}

// ReleasePowerAssertion ends the hold. An id of 0 (a failed create) is a
// no-op.
func ReleasePowerAssertion(assertionID PowerAssertionID) {
	if assertionID == 0 {
		return
	}
	if err := loadPower(); err != nil {
		return // nothing to release through; the id dies with the process
	}
	_, _, _ = purego.SyscallN(powerFreeAddr, uintptr(assertionID))
}
