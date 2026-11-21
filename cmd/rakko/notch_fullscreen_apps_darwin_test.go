//go:build darwin

package main

// The suppression path needs the keyboard seams, which only the darwin
// tests define.

import (
	"testing"

	"github.com/egoist/mygo"
)

// Fullscreen suppression folds the wings, so it must end the app switcher
// with them: otherwise ←/→/⏎ keep steering an invisible selection.
func TestFullscreenSuppressionEndsAppSwitch(t *testing.T) {
	origFS := foregroundFullscreen
	t.Cleanup(func() { foregroundFullscreen = origFS })
	foregroundFullscreen = func() bool { return true }
	stubKeyboard(t)
	a := fullscreenTestShelf()
	a.apps.on = true
	a.apps.keys = map[string]bool{"Left": true, "Right": true, "Enter": true}
	a.mouseDown(false, 0, mygo.Point{X: 100, Y: 500})
	if a.apps.on {
		t.Fatal("the app switcher survived the suppression that folded its wings")
	}
}
