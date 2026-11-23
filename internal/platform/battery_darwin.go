//go:build darwin

package platform

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

// Battery state through IOKit's public power-source API
// (IOPSCopyPowerSourcesInfo / IOPSCopyPowerSourcesList /
// IOPSGetPowerSourceDescription). The dictionary is toll-free bridged to
// NSDictionary, so the values are read with the same Objective-C messages
// as the rest of this package. Reading it is cheap and stays a poll: the
// caller decides when to look, and platform.StartPowerObserver
// (power_observer_darwin.go) wakes it on IOKit's change notifications. The
// poll is the fallback for a missing observer, not the only path.
//
// Main thread only, like the other ObjC-backed calls.

var (
	batteryOnce    sync.Once
	batteryLoadErr error
	iopsCopyInfo   func() uintptr
	iopsCopyList   func(uintptr) uintptr
	iopsGetDesc    func(uintptr, uintptr) uintptr
)

// loadBattery resolves the IOPS calls once.
func loadBattery() error {
	batteryOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				batteryLoadErr = fmt.Errorf("mac: IOKit power sources setup failed: %v", r)
			}
		}()
		lib, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit",
			purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			batteryLoadErr = fmt.Errorf("mac: cannot load IOKit: %w", err)
			return
		}
		purego.RegisterLibFunc(&iopsCopyInfo, lib, "IOPSCopyPowerSourcesInfo")
		purego.RegisterLibFunc(&iopsCopyList, lib, "IOPSCopyPowerSourcesList")
		purego.RegisterLibFunc(&iopsGetDesc, lib, "IOPSGetPowerSourceDescription")
	})
	return batteryLoadErr
}

// The power-source dictionary keys (IOPSKeys.h), passed as NSStrings.
const (
	iopsTypeKey          = "Type"
	iopsInternalBattery  = "InternalBattery"
	iopsCurrentCapacity  = "Current Capacity"
	iopsMaxCapacity      = "Max Capacity"
	iopsIsCharging       = "Is Charging"
	iopsPowerSourceState = "Power Source State"
	iopsACPower          = "AC Power"
)

// Battery returns the first internal battery's state. ok is false on a Mac
// without a battery (Mac mini, iMac) or when IOKit cannot be reached.
func Battery() (BatteryStatus, bool) {
	if err := loadBattery(); err != nil || iopsCopyInfo == nil {
		return BatteryStatus{}, false
	}
	var out BatteryStatus
	found := false
	withPool(func() {
		info := iopsCopyInfo()
		if info == 0 {
			return
		}
		defer releaseCF(info)
		list := iopsCopyList(info)
		if list == 0 {
			return
		}
		defer releaseCF(list)
		n := sendInt(id(list), "count")
		for i := range n {
			src := send(id(list), "objectAtIndex:", uintptr(i))
			desc := iopsGetDesc(info, uintptr(src))
			if desc == 0 {
				continue
			}
			d := id(desc)
			typ := goString(send(d, "objectForKey:", uintptr(nsString(iopsTypeKey))))
			if typ != iopsInternalBattery {
				continue
			}
			cur := sendInt(send(d, "objectForKey:", uintptr(nsString(iopsCurrentCapacity))), "intValue")
			maxCap := sendInt(send(d, "objectForKey:", uintptr(nsString(iopsMaxCapacity))), "intValue")
			charging := sendInt(send(d, "objectForKey:", uintptr(nsString(iopsIsCharging))), "boolValue") != 0
			state := goString(send(d, "objectForKey:", uintptr(nsString(iopsPowerSourceState))))
			pct := 0
			if cur > 0 && maxCap > 0 {
				pct = int(float64(cur)*100/float64(maxCap) + 0.5)
			}
			out = BatteryStatus{Percent: pct, Charging: charging, External: state == iopsACPower}
			found = true
			break
		}
	})
	return out, found
}
