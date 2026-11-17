//go:build darwin

package platform

import "github.com/ebitengine/purego/objc"

// The timer alert uses one of macOS's own sounds, so the app ships no
// audio asset: NSSound's named sounds live in /System/Library/Sounds.
// PlayAlertSound loops Glass until StopAlertSound. The sound is retained
// while it is held and released when it stops or is replaced: soundNamed:
// returns an autoreleased object that would otherwise die with the pool.
// Main thread only, like the rest of this package, and a missing sound or
// a headless test simply plays nothing.

// alertSound is the NSSound started by PlayAlertSound; zero while silent.
var alertSound id

// PlayAlertSound starts the system Glass sound looping. It reports
// whether a sound started; a Mac without the named sound stays silent.
func PlayAlertSound() bool {
	if err := Init(); err != nil {
		return false
	}
	played := false
	withPool(func() {
		cls := id(objc.GetClass("NSSound"))
		if cls == 0 {
			return
		}
		snd := send(cls, "soundNamed:", uintptr(nsString("Glass")))
		if snd == 0 {
			return
		}
		// A second start replaces the first: stop and release the old
		// loop, or it would play on under no owner.
		if alertSound != 0 {
			send(alertSound, "stop")
			send(alertSound, "release")
		}
		send(snd, "retain")
		alertSound = snd
		send(snd, "setLoops:", 1)
		send(snd, "play")
		played = true
	})
	return played
}

// StopAlertSound silences the loop PlayAlertSound started, if any.
func StopAlertSound() {
	if alertSound == 0 {
		return
	}
	withPool(func() {
		send(alertSound, "stop")
		send(alertSound, "release")
		alertSound = 0
	})
}
