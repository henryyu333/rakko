package main

// The overlay asks for its native mouse gate and window band on every frame.
// On Windows both calls re-order the window to the front of the topmost band,
// so an answer that did not change still raised the panel — over the copy a
// tools drag follows the pointer in a preview window, which then never showed
// while the pointer was over the panel.

import (
	"testing"

	"github.com/egoist/mygo"

	"rakko/internal/platform"
)

// TestNativeWindowStateOnlyTouchesOnChange counts the native calls a settled
// shell makes: one each the first time, and nothing while the answer stands.
// A changed band or gate reaches the window exactly once.
func TestNativeWindowStateOnlyTouchesOnChange(t *testing.T) {
	a := parityWindow()
	oldFrame, oldAccept, oldLayer, oldCursor := setNativeWindowFrame, setOverlayAcceptsMouse, setWindowLayer, cursorPoint
	defer func() {
		setNativeWindowFrame, setOverlayAcceptsMouse, setWindowLayer, cursorPoint = oldFrame, oldAccept, oldLayer, oldCursor
	}()
	setNativeWindowFrame = func(uintptr, int, int, int, int) {}
	overlayCalls, layerCalls := 0, 0
	setOverlayAcceptsMouse = func(uintptr, bool) { overlayCalls++ }
	setWindowLayer = func(uintptr, platform.Layer) { layerCalls++ }
	far := mygo.Point{X: a.dispBounds.Width - 5, Y: a.dispBounds.Height - 5}
	cursorPoint = func() mygo.Point { return far }

	a.syncMousePassThrough()
	if overlayCalls != 1 || layerCalls != 1 {
		t.Fatalf("the first frame applied %d gate / %d band calls, want 1 and 1", overlayCalls, layerCalls)
	}
	a.syncMousePassThrough()
	if overlayCalls != 1 || layerCalls != 1 {
		t.Fatalf("an unchanged state touched the window again: %d gate / %d band calls", overlayCalls, layerCalls)
	}

	// The gate follows the pointer onto the drawn shell, once: an open
	// panel wants the mouse, and the anchor's box is where it lands.
	a.visible = true
	a.fx.w, a.fx.h = a.anchorSize()
	a.fx.openK = 0
	cursorPoint = func() mygo.Point {
		return mygo.Point{X: a.notchX + a.notchW/2, Y: a.notchY + a.notchH/2}
	}
	a.syncMousePassThrough()
	if overlayCalls != 2 {
		t.Fatalf("the gate onto the shell applied %d calls, want 2", overlayCalls)
	}
	a.syncMousePassThrough()
	if overlayCalls != 2 || layerCalls != 1 {
		t.Fatalf("a still pointer re-touched the window: %d gate / %d band calls", overlayCalls, layerCalls)
	}

	// A drag moves the overlay into the pop-up band, once; the gate already
	// takes the mouse, so it is not asked again.
	a.dragging = true
	a.syncMousePassThrough()
	if overlayCalls != 2 || layerCalls != 2 {
		t.Fatalf("the band change applied %d gate / %d band calls, want 2 band", overlayCalls, layerCalls)
	}

	// Back at rest: both answers change once.
	a.dragging, a.visible = false, false
	a.syncMousePassThrough()
	if overlayCalls != 3 || layerCalls != 3 {
		t.Fatalf("the resting state applied %d gate / %d band calls, want 3 and 3", overlayCalls, layerCalls)
	}
}
