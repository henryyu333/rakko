package main

// The closed notch's resting frame on a screen with no camera housing.

import (
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestIdleNotchIsPaintedWhereNoHousingExists: a Mac's camera housing is black
// on screen whether the shell draws anything or not, so the closed notch
// blanked its canvas. A Windows display has no housing: there the resting
// shell must paint the anchor itself, and the frame the next state grows from
// stays the 180 × 32 box.
func TestIdleNotchIsPaintedWhereNoHousingExists(t *testing.T) {
	a := parityWindow()
	seen := false
	blank := true
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
	// Only Windows has no camera housing to draw the black; a Mac's shell
	// must keep its idle canvas empty or it paints a second notch.
	wantBlank := runtime.GOOS != "windows"
	if simulatedNotchHousing == wantBlank {
		t.Fatalf("simulatedNotchHousing=%v on %s, want %v", simulatedNotchHousing, runtime.GOOS, !wantBlank)
	}
	if blank != wantBlank {
		t.Fatalf("the idle notch blank=%v on %s, want %v", blank, runtime.GOOS, wantBlank)
	}
}
