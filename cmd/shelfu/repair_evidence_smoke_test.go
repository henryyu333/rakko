//go:build darwin

package main

// Evidence for the stage-review repairs that only a real window can show:
// a 1 × 1 music widget pinned to the desktop at the wide card size.
// Run with SHELF_BASKET_SMOKE=1; everything lands in /tmp/shelf-stagef-A.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"shelf/internal/layout"
	"shelf/internal/mac"
)

// TestSmokePinnedOneColumnMusic: a music widget shrunk to 1 × 1 pins as a
// 278pt desktop card (its player needs the width) while the grid keeps the
// 1 × 1 tile for 放回刘海.
func TestSmokePinnedOneColumnMusic(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to capture the pinned card")
	}
	withPinDisplays(t, pinTestDisplays)
	oldOpen, oldCursor := pinOpenWindow, cursorPoint
	t.Cleanup(func() { pinOpenWindow = oldOpen; cursorPoint = oldCursor })

	a := stageAShelf(t)
	fillShelf(t, a)
	a.x, a.y = 400, 0
	a.mutateTools(func(l *layout.Layout) { l.SetCardSize("playing", 1, 1) })
	onMain(func() {
		a.frame = mygo.Rectangle{X: a.x, Y: a.y, Width: sheetW, Height: sheetH}
		a.openPage(pageTools)
		if p := a.toolsPage(); p != nil {
			p.editing = true
		}
		a.win.Invalidate()
	})
	time.Sleep(300 * time.Millisecond)
	onMain(func() {
		p := a.toolsPage()
		p.toolCardDrop(&toolPress{id: "playing", dragging: true, px: 60, py: 2000, grabDX: 10, grabDY: 10},
			mygo.Point{X: 60, Y: 2000})
	})
	time.Sleep(400 * time.Millisecond)
	var num int
	var frame mygo.Rectangle
	onMain(func() {
		if len(a.pins) != 1 {
			t.Fatalf("pins = %+v", a.pins)
		}
		frame = a.pins[0].frame
		num = mac.WindowNumber(a.pins[0].win.NativeHandle())
	})
	if frame.Width != pinCardW2 {
		t.Fatalf("the pinned 1 × 1 music card is %d wide, want %d", frame.Width, pinCardW2)
	}
	time.Sleep(300 * time.Millisecond)
	out := filepath.Join(stageADir, "pin-music-1x1.png")
	shot := fmt.Sprintf("-l%d", num)
	if num == 0 {
		t.Fatal("the pin window has no number")
	}
	if b, err := exec.Command("screencapture", "-x", "-o", shot, out).CombinedOutput(); err != nil {
		t.Fatalf("screencapture: %v: %s", err, b)
	}
	t.Logf("pinned card -> %s", out)
	onMain(func() {
		for _, it := range a.pins {
			if it.win != nil {
				it.win.Destroy()
			}
		}
	})
}
