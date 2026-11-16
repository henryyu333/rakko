package main

// Unified landing-slot rule across both platforms: while the pointer stays
// inside the panel's drawn shape (with hysteresis), the panel itself draws
// the lifted card, no desktop preview window opens, and the dashed landing
// slot stays visible. Once the desktop preview window takes over outside the
// panel, the landing slot hides.

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestLandingSlotFollowsUnifiedPreviewRule(t *testing.T) {
	a := parityShelf()
	a.frame = mygo.Rectangle{X: 700, Y: 0, Width: 700, Height: 400}
	p := &toolsPage{host: a, viewport: ui.Rect{X: 10, Y: 58, W: 500, H: 174}}
	if !p.slotShows() {
		t.Fatal("the slot is hidden while the page draws the lift itself")
	}
	p.preview.it = &pinItem{}
	if p.slotShows() {
		t.Fatal("the slot showed while the desktop preview window is open")
	}
}
