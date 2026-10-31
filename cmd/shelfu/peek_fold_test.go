//go:build darwin

package main

// The fold after a cancelled drag is subtract-only: the shell keeps the
// shape and content it was showing and just shrinks back into the notch.
// What it must never do is re-aim at the live peekSize()/peekView answer —
// a loaded track would morph the folding shell toward the wings and flash
// their label mid-fold (the user's frame-235 glitch).

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// dragPeekShelf builds the pre-fold state: an incoming system drag inside
// the notch band has the drop pill up on the notch while a track is loaded.
func dragPeekShelf() *shelf {
	a := motionWindow()
	fakeTrack(a, true)
	a.settings.IslandNowPlaying = true
	a.dragging, a.dropHot = true, true
	a.peeking = true
	a.peekProgress, a.peekFrom = 1, 1
	return a
}

// foldFrames steps the view while the peek folds and checks the shell only
// ever shrinks and keeps the drop pill's words — inside the clipping shell,
// not merely present in the layout tree: the text's own rect must overlap
// the drawn shell rect.
func foldFrames(t *testing.T, a *shelf, view *ui.Tester, keepText string) {
	t.Helper()
	pw, ph := a.fx.w, a.fx.h
	for i := range 30 {
		view.Frame()
		if a.fx.w > pw+0.5 || a.fx.h > ph+0.5 {
			t.Fatalf("frame %d: folding shell grew to %.1fx%.1f (was %.1fx%.1f)", i, a.fx.w, a.fx.h, pw, ph)
		}
		pw, ph = a.fx.w, a.fx.h
		if view.HasText("Fashion Killa") {
			t.Fatalf("frame %d: the wings appeared mid-fold", i)
		}
		if keepText != "" && a.fx.peekK > 0.02 {
			tr, ok := view.Find(keepText)
			if !ok {
				t.Fatalf("frame %d: %q vanished while the shell is still up (peekK %.2f)", i, keepText, a.fx.peekK)
			}
			// The shell draws centered at the top of the surface.
			sx0 := (sheetW - a.fx.w) / 2
			ix0 := max(tr.X, sx0)
			iy0 := max(tr.Y, float32(0))
			ix1 := min(tr.X+tr.W, sx0+a.fx.w)
			iy1 := min(tr.Y+tr.H, a.fx.h)
			if ix1 <= ix0 || iy1 <= iy0 {
				t.Fatalf("frame %d: %q's rect %+v does not overlap the shell %.1fx%.1f at %.1f,0", i, keepText, tr, a.fx.w, a.fx.h, sx0)
			}
		}
		view.Advance(16 * time.Millisecond)
	}
}

// TestPeekFoldKeepsShownShape: a cancelled drag folds the drop pill straight
// back — with a track loaded, the shell may only shrink and the wings must
// not appear.
func TestPeekFoldKeepsShownShape(t *testing.T) {
	a := dragPeekShelf()
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	if _, ok := view.Find("松开放入"); !ok {
		t.Fatal("the drop pill is not up before the fold")
	}
	// Drag ends, pointer not over the notch: the fold begins.
	a.dragging = false
	a.peeking = false
	foldFrames(t, a, view, "松开放入")
}

// TestPeekFoldMidMorphOnlyShrinks: the pointer leaves the notch while the
// pill is still morphing toward the wings — the fold must not grow past
// the size it froze at (the second grok finding: AnimateTo kept outward
// velocity and grew the shell one more step).
func TestPeekFoldMidMorphOnlyShrinks(t *testing.T) {
	a := dragPeekShelf()
	view := ui.NewTester(a.view, sheetW, sheetH)
	// Drag ends over the notch: the shell starts morphing toward the wings.
	a.dragging = false
	for range 2 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.w <= float32(180+dropPillExtraW) {
		t.Fatalf("the wings morph never grew the shell: %.1f", a.fx.w)
	}
	// The pointer leaves the notch mid-morph: fold from the drawn size.
	a.peeking = false
	pw, ph := a.fx.w, a.fx.h
	for i := range 30 {
		view.Frame()
		if a.fx.w > pw+0.5 || a.fx.h > ph+0.5 {
			t.Fatalf("frame %d: mid-morph fold grew to %.1fx%.1f (was %.1fx%.1f)", i, a.fx.w, a.fx.h, pw, ph)
		}
		pw, ph = a.fx.w, a.fx.h
		view.Advance(16 * time.Millisecond)
	}
}

// TestPeekFoldWithoutTrackAlsoPlain: the same fold with no track loaded
// keeps the drop pill's shape and words.
func TestPeekFoldWithoutTrackAlsoPlain(t *testing.T) {
	a := dragPeekShelf()
	a.np = nil
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	a.dragging = false
	a.peeking = false
	foldFrames(t, a, view, "松开放入")
}

// TestPeekFoldHoverStillGetsTheWings: if the pointer rests on the notch
// when the drag ends, the pill may morph into the hover wings — the fold
// freeze only applies while the peek is going away.
func TestPeekFoldHoverStillGetsTheWings(t *testing.T) {
	a := dragPeekShelf()
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	// Drag ends while hovering the notch: peeking stays true.
	a.dragging = false
	var grew bool
	for range 40 {
		view.Frame()
		if a.fx.w > float32(180+dropPillExtraW) {
			grew = true
		}
		view.Advance(16 * time.Millisecond)
	}
	if !grew {
		t.Fatal("a hover after the drag never morphed toward the wings' size")
	}
	if _, ok := view.Find("正在播放：Fashion Killa · 展开 Rakko"); !ok {
		t.Fatal("the hover wings never appeared over the notch")
	}
}

// TestPeekFoldInterruptedByNewDrag: a drag that starts while the pill is
// folding must grow it back out as the drop pill — no empty shell, no
// wings flash.
func TestPeekFoldInterruptedByNewDrag(t *testing.T) {
	a := dragPeekShelf()
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	a.dragging, a.dropHot = false, false
	a.peeking = false
	for range 8 { // mid-fold
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	mid := a.fx.w
	a.dragging, a.dropHot = true, true // a new drag re-enters the band
	a.peeking = true
	var back bool
	for i := range 30 {
		view.Frame()
		if view.HasText("Fashion Killa") {
			t.Fatalf("frame %d: the wings appeared while a drag re-raised the pill", i)
		}
		if a.fx.w > mid+40 {
			back = true
		}
		view.Advance(16 * time.Millisecond)
	}
	if !back {
		t.Fatal("the re-raised drag never grew the shell back")
	}
	if _, ok := view.Find("松开放入"); !ok {
		t.Fatal("the re-raised drag lost the drop pill's words")
	}
}
