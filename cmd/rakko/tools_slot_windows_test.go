//go:build windows

package main

// The Windows landing slot. The desktop preview window carries the card past
// the page's content box; the dashed slot stays as long as the pointer is
// over the page, exactly as the video reference shows the lift and the slot
// together.

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestLandingSlotShowsWhileThePointerIsOverThePage: the card's lift is the
// desktop preview window once the page's own copy would leave the content
// box, and the dashed landing slot must stay until the pointer really leaves
// the page. A drag out of the panel shows no slot at all.
func TestLandingSlotShowsWhileThePointerIsOverThePage(t *testing.T) {
	a := parityShelf()
	a.frame = mygo.Rectangle{X: 700, Y: 0, Width: 700, Height: 400}
	p := &toolsPage{host: a, viewport: ui.Rect{X: 10, Y: 58, W: 500, H: 174}}
	if !p.slotShows() {
		t.Fatal("the slot is hidden while the page draws the lift itself")
	}
	p.preview.it = &pinItem{}
	orig := cursorPoint
	defer func() { cursorPoint = orig }()
	cursorPoint = func() mygo.Point { return mygo.Point{X: 900, Y: 150} } // window 200,150
	if !p.slotShows() {
		t.Fatalf("the slot vanished while the pointer sits inside the page at %+v", p.viewport)
	}
	cursorPoint = func() mygo.Point { return mygo.Point{X: 1500, Y: 150} }
	if p.slotShows() {
		t.Fatal("the slot stays while the card is dragged out of the panel")
	}
}
