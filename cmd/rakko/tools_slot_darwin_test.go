//go:build darwin

package main

// The Mac's landing slot. Once the card's lift is the desktop preview
// window, the Mac has never drawn the dashed landing slot: the preview is
// the only copy on screen. Judging by the pointer alone put the slot back
// whenever the card crossed the viewport's edge while the pointer was still
// inside the page.

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestLandingSlotStaysHiddenBehindThePreview: the page draws the slot under
// its own lift, and drops it with that lift; the preview window takes over
// along with the card.
func TestLandingSlotStaysHiddenBehindThePreview(t *testing.T) {
	a := parityShelf()
	a.frame = mygo.Rectangle{X: 700, Y: 0, Width: 700, Height: 400}
	p := &toolsPage{host: a, viewport: ui.Rect{X: 10, Y: 58, W: 500, H: 174}}
	if !p.slotShows() {
		t.Fatal("the slot is hidden while the page draws the lift itself")
	}
	orig := cursorPoint
	defer func() { cursorPoint = orig }()
	cursorPoint = func() mygo.Point { return mygo.Point{X: 900, Y: 150} } // window 200,150
	p.preview.it = &pinItem{}
	if p.slotShows() {
		t.Fatal("the slot showed over the Mac's preview window while the pointer sat inside the page")
	}
}
