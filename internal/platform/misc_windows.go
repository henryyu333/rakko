//go:build windows

package platform

// Input side effects. Haptics do not exist on this platform (accepted
// difference), and the paste shortcut belongs to the clipboard batch.

import (
	"errors"
)

// HapticTick is a no-op: Windows has no trackpad haptics that a desktop
// app can play, and the accepted Windows behaviour is to skip the nudge.
func HapticTick() {}

// SendPasteShortcut sends Ctrl+V to the process pid.
// WINDOWS-NEXT(clipboard): SendInput targeting a specific process is part
// of the paste batch; until then the caller reports a copy, never a paste.
func SendPasteShortcut(pid int) bool { return false }

// errUnsupported keeps the platform's unsupported-call error available to
// the files that need a plain failure.
var errUnsupported = errors.New("platform: unsupported on Windows")
