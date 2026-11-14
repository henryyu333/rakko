//go:build darwin

package platform

import "testing"

// step(count, typed, down) mirrors DragState's per-tick observations.

func TestDragTrackerFirstSamplePrimes(t *testing.T) {
	var tr dragTracker
	// App launched while a button is already held and the board carries a
	// finished drag's leftovers: must not fire.
	tr.step(7, true, true)
	if tr.active || tr.serial != 0 {
		t.Fatalf("first sample must only baseline, got active=%v serial=%d", tr.active, tr.serial)
	}
}

func TestDragTrackerStaleBoardOnFreshPress(t *testing.T) {
	var tr dragTracker
	tr.step(3, true, false) // idle baseline after a completed drag
	tr.step(3, true, true)  // user presses: stale typed board, no new count
	if tr.active || tr.serial != 0 {
		t.Fatalf("click over a stale drag board must not activate, got active=%v serial=%d", tr.active, tr.serial)
	}
	tr.step(3, true, true) // still holding
	if tr.active || tr.serial != 0 {
		t.Fatalf("ordinary hold must stay inactive, got active=%v serial=%d", tr.active, tr.serial)
	}
}

func TestDragTrackerLatchesOneSerialPerHold(t *testing.T) {
	var tr dragTracker
	tr.step(3, true, false) // baseline
	tr.step(3, true, true)  // press
	tr.step(4, true, true)  // drag source declares items → armed
	if !tr.active || tr.serial != 1 {
		t.Fatalf("expected active serial 1, got active=%v serial=%d", tr.active, tr.serial)
	}
	tr.step(4, true, true) // same drag, next tick
	if !tr.active || tr.serial != 1 {
		t.Fatalf("serial must stay latched, got active=%v serial=%d", tr.active, tr.serial)
	}
	tr.step(5, true, true) // source rewrites board mid-drag
	if !tr.active || tr.serial != 1 {
		t.Fatalf("mid-drag rewrite must not mint a new serial, got active=%v serial=%d", tr.active, tr.serial)
	}
}

func TestDragTrackerRearmAcrossHolds(t *testing.T) {
	var tr dragTracker
	tr.step(3, true, false)
	tr.step(3, true, true)  // press 1
	tr.step(4, true, true)  // drag 1 begins
	tr.step(4, true, false) // drop: released, rebaseline
	if tr.active {
		t.Fatal("release must deactivate")
	}
	tr.step(4, true, true) // press 2, board unchanged → click, not a drag
	if tr.active || tr.serial != 1 {
		t.Fatalf("re-press on unchanged board must not activate, got active=%v serial=%d", tr.active, tr.serial)
	}
	tr.step(5, true, true) // second drag during hold 2
	if !tr.active || tr.serial != 2 {
		t.Fatalf("expected second generation serial 2, got active=%v serial=%d", tr.active, tr.serial)
	}
}

func TestDragTrackerEmptyBumpIsNotDrag(t *testing.T) {
	var tr dragTracker
	tr.step(3, true, false)
	tr.step(3, true, true)  // press
	tr.step(4, false, true) // board bumped but declares no items (cleared)
	if tr.active || tr.serial != 0 {
		t.Fatalf("untyped bump must not activate, got active=%v serial=%d", tr.active, tr.serial)
	}
	tr.step(5, true, true) // real content arrives in same hold → drags
	if !tr.active || tr.serial != 1 {
		t.Fatalf("typed bump in same hold must arm serial 1, got active=%v serial=%d", tr.active, tr.serial)
	}
}

func TestDragTrackerPreExistingDragInvisible(t *testing.T) {
	var tr dragTracker
	// Drag already in flight when polling starts: count never changes, so
	// it is (documented) invisible rather than a false positive later.
	tr.step(9, true, true)
	for range 10 {
		tr.step(9, true, true)
	}
	if tr.active || tr.serial != 0 {
		t.Fatalf("constant board must never activate, got active=%v serial=%d", tr.active, tr.serial)
	}
}

func TestDragTrackerReleaseBetweenPolls(t *testing.T) {
	var tr dragTracker
	tr.step(3, true, false)
	tr.step(3, true, true)
	tr.step(4, true, true)  // drag begins
	tr.step(5, true, false) // released with one final board write (drop)
	tr.step(5, true, true)  // next press consumes that write as baseline
	tr.step(5, true, true)
	if tr.active || tr.serial != 1 {
		t.Fatalf("drop-time write must not leak into the next hold, got active=%v serial=%d", tr.active, tr.serial)
	}
}

func TestDragTrackerPressAndDragSameTick(t *testing.T) {
	var tr dragTracker
	tr.step(3, true, false) // baseline
	// Press and the session's first board write land in one poll window.
	tr.step(4, true, true)
	if !tr.active || tr.serial != 1 {
		t.Fatalf("bump inside the press tick must arm, got active=%v serial=%d", tr.active, tr.serial)
	}
}

func TestDragTrackerPressWithUntypedBump(t *testing.T) {
	var tr dragTracker
	tr.step(3, true, false)
	// A write that left no declared items between samples, then press:
	// treated as a clear, not a drag.
	tr.step(4, false, true)
	if tr.active || tr.serial != 0 {
		t.Fatalf("untyped bump on press must not arm, got active=%v serial=%d", tr.active, tr.serial)
	}
}

// TestDragTrackerPressEventArmsAMissedRelease: polling the button state at
// 30 ms can miss a release entirely (a fast drop-and-regrab, or a tap-drag
// the event source reports late). The tracker then never sees down=false,
// so a quick third drag's board write is consumed as the previous
// session's rewrite: active stays on the stale serial and the shelf's
// suppressed flag from the interrupted drop never clears — the drop pill
// never returns. The left-press event (ObserveMouseDown, always
// installed) is the edge polling misses: it must re-arm the hold.
func TestDragTrackerPressEventArmsAMissedRelease(t *testing.T) {
	var tr dragTracker
	tr.step(3, true, false) // idle
	tr.step(3, true, true)  // press 1
	tr.step(4, true, true)  // drag 1 begins: serial 1
	tr.step(4, true, true)
	// Drag 1 ends and drag 2's press plus its first board write all land
	// inside one poll window: down never goes false in a sample. Without
	// the press edge, step() sees an unchanged held button and folds the
	// write into serial 1.
	tr.press()
	tr.step(5, true, true)
	if !tr.active || tr.serial != 2 {
		t.Fatalf("a press observed between samples must re-arm: active=%v serial=%d, want serial 2", tr.active, tr.serial)
	}
	tr.press()
	tr.step(6, true, true) // drag 3 the same way
	if !tr.active || tr.serial != 3 {
		t.Fatalf("third drag must arm serial 3, got active=%v serial=%d", tr.active, tr.serial)
	}
}

// TestDragTrackerPressEventIgnoresStaleBoard: a press edge alone cannot
// resurrect the board — press on an unchanged board is still a click.
func TestDragTrackerPressEventIgnoresStaleBoard(t *testing.T) {
	var tr dragTracker
	tr.step(3, true, false)
	tr.step(3, true, true)
	tr.step(4, true, true) // drag 1
	tr.step(4, true, true)
	tr.press()
	tr.step(4, true, true) // missed release, but no new write: press ends it
	if tr.active || tr.serial != 1 {
		t.Fatalf("press on an unchanged board must end the hold, got active=%v serial=%d", tr.active, tr.serial)
	}
}
