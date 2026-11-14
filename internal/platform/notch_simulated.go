package platform

import "sync/atomic"

// simulatedNotch asks a platform with no camera housing to measure the
// notch geometry anyway, so the Windows build can draw the shelf as a
// screen-top notch instead of the floating island. A display with real
// housing ignores it: the hardware decides there.
var simulatedNotch atomic.Bool

// SetSimulatedNotch turns the simulated camera housing on or off. The card
// is a user preference (the 刘海样式 choice) and is re-applied before every
// measurement. Platforms whose displays carry a real housing ignore it.
func SetSimulatedNotch(on bool) { simulatedNotch.Store(on) }
