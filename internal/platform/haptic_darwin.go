//go:build darwin

package platform

import "github.com/ebitengine/purego/objc"

// NSHapticFeedbackManager taps the Force Touch trackpad. Macs without one
// (desktops, older models) make performFeedbackPattern a silent no-op.
// The class is guarded with GetClass rather than class(), which panics on
// a missing class — haptics are a nicety, never worth a crash.

// nsHapticFeedbackPatternGeneric is a soft tick.
const nsHapticFeedbackPatternGeneric = 0

// HapticTick performs a light haptic nudge on the Force Touch trackpad —
// the "something accepted your drag" tap other drag-and-drop utilities
// play. It is a cheap no-op on hardware without the trackpad and never
// activates the app or steals focus. Main thread only.
func HapticTick() {
	if err := Init(); err != nil {
		return
	}
	withPool(func() {
		cls := id(objc.GetClass("NSHapticFeedbackManager"))
		if cls == 0 {
			return
		}
		performer := send(cls, "defaultPerformer")
		if performer == 0 {
			return
		}
		// performanceTime 0 = NSHapticFeedbackPerformanceTimeNow.
		send(performer, "performFeedbackPattern:performanceTime:", nsHapticFeedbackPatternGeneric, 0)
	})
}
