//go:build windows

package platform

// Input side effects. Haptics do not exist on this platform (accepted
// difference); the paste shortcut lives with the clipboard.

import (
	"errors"
)

// HapticTick is a no-op: Windows has no trackpad haptics that a desktop
// app can play, and the accepted Windows behaviour is to skip the nudge.
func HapticTick() {}

// errUnsupported keeps the platform's unsupported-call error available to
// the files that need a plain failure.
var errUnsupported = errors.New("platform: unsupported on Windows")
