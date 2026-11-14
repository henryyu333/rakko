//go:build windows

package platform

// Power: the battery, the wake assertions and the power notifications.

import (
	"sync"
	"unsafe"

	"github.com/egoist/mygo"
)

// BatteryStatus is what the system's battery reports now.
type BatteryStatus struct {
	// Percent is 0–100.
	Percent int
	// Charging is true while the battery charges.
	Charging bool
	// External is true while the machine runs from the adapter.
	External bool
}

// Battery returns the system battery's state. ok is false on a desktop
// without a battery.
func Battery() (BatteryStatus, bool) {
	type systemPowerStatus struct {
		ACLineStatus        byte
		BatteryFlag         byte
		BatteryLifePercent  byte
		SystemStatusFlag    byte
		BatteryLifeTime     uint32
		BatteryFullLifeTime uint32
	}
	var s systemPowerStatus
	if r, _, _ := procGetSystemPowerStat.Call(uintptr(unsafe.Pointer(&s))); r == 0 {
		return BatteryStatus{}, false
	}
	// BatteryFlag 128 means "no system battery".
	if s.BatteryFlag == 128 || s.BatteryLifePercent == 255 {
		return BatteryStatus{}, false
	}
	return BatteryStatus{
		Percent:  int(s.BatteryLifePercent),
		Charging: s.BatteryFlag&statusCharging != 0,
		External: s.ACLineStatus == 1,
	}, true
}

// PowerAssertionKind names the behaviour an assertion holds.
type PowerAssertionKind int

const (
	// PowerAssertSystemSleep blocks system idle sleep; the display may
	// still turn off.
	PowerAssertSystemSleep PowerAssertionKind = iota
	// PowerAssertDisplaySleep also keeps the display on.
	PowerAssertDisplaySleep
)

// PowerAssertionID is the handle an assertion creation returns; zero means
// no assertion.
type PowerAssertionID uintptr

var (
	assertMu    sync.Mutex
	assertNext  uintptr
	assertNames = map[PowerAssertionID]string{}
	assertKinds = map[PowerAssertionID]PowerAssertionKind{}
)

// NewPowerAssertion creates a named execution-state request. names become
// the assertion's recorded name, readable through PowerAssertionName.
func NewPowerAssertion(kind PowerAssertionKind, name string) (PowerAssertionID, error) {
	assertMu.Lock()
	defer assertMu.Unlock()
	assertNext++
	id := PowerAssertionID(assertNext)
	assertNames[id] = name
	assertKinds[id] = kind
	applyAssertionsLocked()
	return id, nil
}

// ReleasePowerAssertion ends the hold. An id of 0 is a no-op.
func ReleasePowerAssertion(assertionID PowerAssertionID) {
	if assertionID == 0 {
		return
	}
	assertMu.Lock()
	defer assertMu.Unlock()
	delete(assertNames, assertionID)
	delete(assertKinds, assertionID)
	applyAssertionsLocked()
}

// PowerAssertionName returns the name recorded for assertionID.
func PowerAssertionName(assertionID PowerAssertionID) string {
	assertMu.Lock()
	defer assertMu.Unlock()
	return assertNames[assertionID]
}

func applyAssertionsLocked() {
	flags := uintptr(esContinuous)
	for _, kind := range assertKinds {
		flags |= esSystemRequired
		if kind == PowerAssertDisplaySleep {
			flags |= esDisplayRequired
		}
	}
	procSetThreadExecState.Call(flags)
}

// StartPowerObserver subscribes the wake callback to the power and session
// notifications mygo delivers: resume, suspend, lock and unlock all refresh
// what the module polls. The subscription stays for the process; a later
// call adds another listener. It returns a non-zero token when the app is
// running.
func StartPowerObserver(wake func()) (uintptr, error) {
	if wake == nil {
		return 0, nil
	}
	wrapped := func() { WakeMain(wake) }
	mygo.Power.OnResume(wrapped)
	mygo.Power.OnSuspend(wrapped)
	mygo.Power.OnLockScreen(wrapped)
	mygo.Power.OnUnlockScreen(wrapped)
	return 1, nil
}
