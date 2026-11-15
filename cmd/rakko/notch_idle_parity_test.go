package main

// The closed notch's resting frame across platforms: neither macOS nor
// Windows paints a black housing while the shell is idle.

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestIdleNotchIsBlankOnAllPlatforms verifies that an idle notch shell leaves
// its canvas blank on both macOS (hardware notch) and Windows (no persistent
// black housing on screen until hover or a live activity appears), while
// keeping the 180 x 32 anchor box that the next state grows from.
func TestIdleNotchIsBlankOnAllPlatforms(t *testing.T) {
	a := parityWindow()
	seen := false
	blank := false
	blankFrameTrace = func(b bool) { seen, blank = true, b }
	defer func() { blankFrameTrace = nil }()
	view := ui.NewTester(a.view, 400, 120)
	view.Frame()
	if !seen {
		t.Fatal("the idle frame was not traced")
	}
	w, h := a.anchorSize()
	if a.fx.w != w || a.fx.h != h {
		t.Fatalf("the resting shell is %.0fx%.0f, want the anchor's %.0fx%.0f", a.fx.w, a.fx.h, w, h)
	}
	if !blank {
		t.Fatalf("the idle notch blank=%v, want true", blank)
	}
}
