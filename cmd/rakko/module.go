//go:build darwin

package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// A module is one optional feature hosted by the notch shell. The shell owns
// the window, the three states and incoming drags; a module owns only its own
// state. Modules run on the main thread from the shared monitoring tick and
// throttle themselves, so a slow poll never needs a goroutine of its own.
type module interface {
	tick(now time.Time)
}

// A liveActivity is a module with something under way (now playing, a
// conversion, a keep-awake hold) for the sheet's left ear. At most two
// chips fit; each is a 22pt capsule that must keep to the panel palette.
type liveActivity interface {
	live() bool
	// chip draws the activity as one 22pt capsule. The module keeps its
	// own actions (play/pause, 取消, stopping the hold) inside it.
	chip(c *ui.Context) ui.Element
}
