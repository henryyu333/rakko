//go:build darwin

package mac

import "github.com/ebitengine/purego/objc"

// The timer alert uses one of macOS's own sounds, so the app ships no
// audio asset: NSSound's named sounds live in /System/Library/Sounds.
// PlayAlertSound loops Glass until StopAlertSound; nothing is retained
// after the stop. Main thread only, like the rest of this package, and a
// missing sound or a headless test simply plays nothing.

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
		alertSound = 0
	})
}
