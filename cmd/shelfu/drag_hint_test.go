//go:build darwin

package main

// Tests for the drop hint during ShelfU's own drag-out: the panel toast and
// the notch's 「松开放入」 pill must only answer incoming system drags.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
)

func TestDropHintHiddenDuringOwnDrag(t *testing.T) {
	a := &shelf{
		settings: settings.Default(), visible: true,
		dragging: true, ownDrag: true, notchW: 180, notchH: 32,
	}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	if _, ok := view.Find("松开放入"); ok {
		t.Fatal("the panel showed the drop hint during ShelfU's own drag-out")
	}
	a.ownDrag = false
	view = ui.NewTester(a.view, sheetW, panelTestH)
	if _, ok := view.Find("松开放入"); !ok {
		t.Fatal("an incoming drag lost the panel's drop hint")
	}
}

func TestPeekDropHintHiddenDuringOwnDrag(t *testing.T) {
	a := &shelf{
		settings: settings.Default(), notchW: 180, notchH: 32,
		peeking: true, peekProgress: 1,
		dragging: true, ownDrag: true,
	}
	if w, h := a.peekSize(); w == 180+dropPillExtraW || h == 32+dropPillBody {
		t.Fatalf("own drag-out kept the drop pill shape %.0fx%.0f", w, h)
	}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	if _, ok := view.Find("松开放入"); ok {
		t.Fatal("the notch showed 松开放入 during ShelfU's own drag-out")
	}
	a.ownDrag = false
	// An incoming drag outside the notch band only widens the shell at the
	// notch's own height: the hint, never the pill or a glyph.
	if w, h := a.peekSize(); w != 180+dropHintExtraW || h != 32 {
		t.Fatalf("an incoming drag outside the band got peek %.0fx%.0f, want the band hint", w, h)
	}
	view = ui.NewTester(a.view, sheetW, panelTestH)
	if _, ok := view.Find("松开放入"); ok {
		t.Fatal("the drag hint outside the band showed the release words")
	}
	// Entering the band grows the real pill.
	a.dropHot = true
	if w, h := a.peekSize(); w != 180+dropPillExtraW || h != 32+dropPillBody {
		t.Fatalf("a drag inside the band got peek %.0fx%.0f, want the drop pill", w, h)
	}
	view = ui.NewTester(a.view, sheetW, panelTestH)
	if _, ok := view.Find("松开放入"); !ok {
		t.Fatal("an incoming drag inside the band lost 松开放入")
	}
}

// An incoming drag must never outgrow the drag hint's own box while the
// cursor is outside the drop band. A peek folding mid-morph pins its
// half-grown shape (e.g. the hover wings) in fx.peekW/H; when the drag
// re-raises the shell, that remnant must not ride the shell back past
// the menu bar — the "notch flashes at drag start" bug.
func TestDragStartNeverCarriesFoldRemnant(t *testing.T) {
	a := motionWindow()
	a.settings = settings.Default()
	fakeTrack(a, true)
	a.dispBounds = mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	// Hover the notch: the shell morphs toward the 284×36 wings.
	a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
	view.Frame()
	for range 10 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	// The pointer leaves mid-morph: the fold pins a large remnant shape.
	a.peeking = false
	for range 6 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	// The remnant must still be bigger than the hint on at least one
	// axis, or there is no flash to catch.
	hintShapeW := float32(max(180, a.notchW) + dropHintExtraW)
	hintShapeH := float32(max(32, a.notchH))
	if a.fx.peekW <= hintShapeW && a.fx.peekH <= hintShapeH {
		t.Skipf("remnant %gx%g too small to exercise the flash", a.fx.peekW, a.fx.peekH)
	}
	// A drag begins far from the notch: every tick's window must stay
	// inside the hint's frame, never the remnant's.
	old := cursorPoint
	oldS, oldT, oldD := dragState, dragTypes, dragLeftDown
	cursorPoint = func() mygo.Point { return mygo.Point{X: 689, Y: 422} }
	dragState = func() (bool, int64) { return true, 11 }
	dragTypes = func() []string { return []string{"public.utf8-plain-text"} }
	dragLeftDown = func() bool { return true }
	defer func() { cursorPoint, dragState, dragTypes, dragLeftDown = old, oldS, oldT, oldD }()
	a.pointerDragged()
	hintW := int(float32(max(180, a.notchW))+dropHintExtraW+0.5) + 2*earR
	hintH := max(32, a.notchH) + 1
	for i := range 14 {
		a.monitorDrag(time.Now())
		view.Frame()
		view.Advance(16 * time.Millisecond)
		f := a.desiredFrame()
		if f.Width > hintW+1 || f.Height > hintH+1 {
			t.Fatalf("tick %d: drag-start frame %dx%d escaped the hint %dx%d (remnant %.0fx%.0f, drawn %.1fx%.1f)",
				i, f.Width, f.Height, hintW, hintH, a.fx.peekW, a.fx.peekH, a.fx.w, a.fx.h)
		}
	}
}
