//go:build darwin

package main

// The island's edges: the hand-over between the resident display and the
// other screens' idle capsules across a style switch and a display change,
// the band the window sits in while the search drop carries the shell past
// the menu bar, the drop's per-frame travel at 120Hz, and the resting
// capsule's app entry.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/platform"
	"rakko/internal/settings"
)

// idleCapsuleDisplays is a notched built-in and a bare external to its
// right, the two screens this machine cannot show together.
func idleCapsuleDisplays() []islandDisplay {
	return []islandDisplay{
		{ID: 1, Bounds: mygo.Rectangle{Width: 1470, Height: 956}, HasNotch: true, NotchH: 32},
		{ID: 2, Bounds: mygo.Rectangle{X: 1470, Width: 2560, Height: 1440}},
	}
}

// A display's idle capsule is the shape the style draws on that screen: a
// notched screen shows none in the notch style (its hardware is the shell)
// but the same capsule as everywhere else in the island style, on the row
// below its housing.
func TestIdleCapsulesFollowTheStylePerDisplay(t *testing.T) {
	displays := idleCapsuleDisplays()

	// Resident on the external, 灵动岛: the built-in keeps its resting
	// capsule below the camera housing, 6pt under it.
	got := idleIslandPlacements(displays, 2, settings.ShellStyleIsland, nil)
	if len(got) != 1 || got[0].DisplayID != 1 {
		t.Fatalf("island style on display 2: placements = %+v, want the built-in's", got)
	}
	want := mygo.Rectangle{X: (1470 - islandIdleW) / 2, Y: 32 + islandNotchGap, Width: islandIdleW, Height: islandIdleH}
	if got[0].Frame != want {
		t.Fatalf("built-in capsule = %+v, want %+v (below the housing)", got[0].Frame, want)
	}

	// The same layout in 刘海: the built-in draws nothing at rest, so the
	// external display is the only one with a capsule and the built-in has
	// none.
	if got := idleIslandPlacements(displays, 2, settings.ShellStyleNotch, nil); len(got) != 0 {
		t.Fatalf("notch style on display 2: placements = %+v, want none", got)
	}

	// Resident on the built-in: the external keeps the bare screen's row.
	got = idleIslandPlacements(displays, 1, settings.ShellStyleIsland, nil)
	if len(got) != 1 || got[0].DisplayID != 2 {
		t.Fatalf("island style on display 1: placements = %+v, want the external's", got)
	}
	want = mygo.Rectangle{X: 1470 + (2560-islandIdleW)/2, Y: islandTopGap, Width: islandIdleW, Height: islandIdleH}
	if got[0].Frame != want {
		t.Fatalf("external capsule = %+v, want %+v", got[0].Frame, want)
	}

	// A spot saved on the built-in replays there, clamped to the row below
	// its housing rather than the bare screen's 7pt.
	saved := map[string]settings.IslandPos{
		islandSpotKey(1): {X: 300, Y: 0},
		islandSpotKey(2): {X: 4000, Y: 0},
	}
	got = idleIslandPlacements(displays, 2, settings.ShellStyleIsland, saved)
	if len(got) != 1 {
		t.Fatalf("saved placements = %+v, want the built-in's", got)
	}
	if f := got[0].Frame; f.Y != 32+islandNotchGap || f.X != 300-islandIdleW/2 {
		t.Fatalf("restored built-in capsule = %+v, want the housing row and the saved centre", f)
	}
}

// A display change replays each screen's own row and place: the island can
// never keep the previous display's housing offset, and the screen left
// behind keeps a capsule on the row its own hardware calls for.
func TestDisplayChangeReplaysEachScreensOwnRow(t *testing.T) {
	syntheticDisplays(t, true, true)
	a := islandShelf(true, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.settings.IslandPos = map[string]settings.IslandPos{
		islandSpotKey(1): {X: 700, Y: 38},
	}

	// The built-in: the island rests on the user's saved row below the
	// housing, and its search drop is the rest of the header gap.
	a.place(mygo.Point{X: 700, Y: 100})
	if a.dispID != 1 || !a.island || a.islandTop != 38 {
		t.Fatalf("built-in: display %d island %v top %d", a.dispID, a.island, a.islandTop)
	}
	if r := a.islandRect(); r.Y != 38 || r.X != 700-islandIdleW/2 {
		t.Fatalf("built-in island %+v, want the saved spot (700, 38)", r)
	}
	if got := a.focusDropOnScreen(); got != 2 {
		t.Fatalf("built-in search drop %d, want the 2pt header gap", got)
	}

	// The external: no housing, so the island's row is the bare screen's
	// 7pt, its own drop is the full header row, and the screen left behind
	// gets its capsule below the housing — not at the island's new row.
	a.place(mygo.Point{X: 2000, Y: 100})
	if a.dispID != 2 || a.islandTop != islandTopGap || a.islandRect().Y != islandTopGap {
		t.Fatalf("external: display %d top %d rect %+v", a.dispID, a.islandTop, a.islandRect())
	}
	if got := a.focusDropOnScreen(); got != 25 {
		t.Fatalf("external search drop %d, want 25", got)
	}
	placements := idleIslandPlacements([]islandDisplay{
		{ID: 1, Bounds: mygo.Rectangle{Width: 1470, Height: 956}, HasNotch: true, NotchH: 32},
		{ID: 2, Bounds: mygo.Rectangle{X: 1470, Width: 2560, Height: 1440}},
	}, a.dispID, a.settings.ShellStyle, a.settings.IslandPos)
	if len(placements) != 1 || placements[0].DisplayID != 1 {
		t.Fatalf("external resident: placements = %+v, want the built-in's capsule", placements)
	}
	if f := placements[0].Frame; f.Y != 32+islandNotchGap {
		t.Fatalf("built-in capsule rest %+v, want the row below the housing 38", f)
	}

	// Unplugging the external sends the island back to the built-in's saved
	// row instead of leaving it on the bare screen's.
	displayNearest = func(mygo.Point) mygo.Display {
		return mygo.Display{
			ID: 1, Primary: true,
			Bounds:   mygo.Rectangle{Width: 1470, Height: 956},
			WorkArea: mygo.Rectangle{Y: 25, Width: 1470, Height: 931},
		}
	}
	a.place(mygo.Point{X: 100, Y: 100})
	if a.dispID != 1 || a.islandTop != 38 || a.islandRect().Y != 38 {
		t.Fatalf("after unplug: display %d top %d rect %+v", a.dispID, a.islandTop, a.islandRect())
	}
	if got := a.focusDropOnScreen(); got != 2 {
		t.Fatalf("after unplug: search drop %d, want 2", got)
	}
}

// The keyboard band puts the menu bar over everything above its bottom, so
// a floating island may only enter it once the shell's own top has passed
// the bar: before that the rounded top would be cut off for the first
// frames of every opening.
func TestKeyboardBandWaitsForTheShellToClearTheBar(t *testing.T) {
	bare := islandShelf(false, settings.ShellStyleNotch)
	bare.focusMode = true
	bare.fx.focusK = 0
	if got := bare.windowLevel(); got != platform.LayerStatus {
		t.Fatalf("capsule row (top %d) took the keyboard band %v", bare.anchorScreenTop(), got)
	}
	// Halfway down the 25pt drop the top is still above the 24pt bar.
	bare.fx.focusK = 0.6
	if top := bare.anchorScreenTop() + bare.focusDropNow(); top >= 24 {
		t.Fatalf("halfway drop top %d already clears the bar", top)
	}
	if got := bare.windowLevel(); got != platform.LayerStatus {
		t.Fatalf("mid-drop took the keyboard band %v", got)
	}
	bare.fx.focusK = 1
	if got := bare.windowLevel(); got != platform.LayerSearchKeyboard {
		t.Fatalf("settled search bar (top %d) stayed out of the keyboard band %v",
			bare.anchorScreenTop()+bare.focusDropNow(), got)
	}
	// Without the keyboard the shell goes back above the bar at once.
	bare.focusMode = false
	if got := bare.windowLevel(); got != platform.LayerStatus {
		t.Fatalf("closed panel level %v, want the status band", got)
	}

	// The notch style's shell starts at the display's top edge on purpose:
	// the menu bar draws over its empty top band, so it takes the keyboard
	// band at once.
	notch := islandShelf(true, settings.ShellStyleNotch)
	notch.focusMode = true
	notch.fx.focusK = 0
	if got := notch.windowLevel(); got != platform.LayerSearchKeyboard {
		t.Fatalf("notch style level %v, want the keyboard band", got)
	}

	// An island on a notched screen rests below the housing, already under
	// the bar: it takes the keyboard band at once, as it always did.
	island := islandShelf(true, settings.ShellStyleIsland)
	island.focusMode = true
	island.fx.focusK = 0
	if got := island.windowLevel(); got != platform.LayerSearchKeyboard {
		t.Fatalf("notched island (top %d) level %v, want the keyboard band", island.anchorScreenTop(), got)
	}
}

// The drop's ease is time-based, so a 120Hz display draws it finer than a
// 60Hz one: the first drawn frame is still on the capsule's row and no
// frame moves the top more than two points.
func TestFocusDropStepsHoldAt120Hz(t *testing.T) {
	a := islandShelf(false, settings.ShellStyleNotch)
	drop := a.focusDropOnScreen()
	if drop <= 0 {
		t.Fatalf("this stand-in has no search drop (%d)", drop)
	}
	frame := 1000.0 / 120.0 // milliseconds per display frame
	last, maxStep, first := 0, 0, -1
	for ms := 0.0; ms <= float64(focusDropAnim.Duration/time.Millisecond)+frame; ms += frame {
		if ms > 0 {
			a.fx.focusK = focusDropEase(float32(ms / float64(focusDropAnim.Duration/time.Millisecond)))
		}
		now := a.focusDropNow()
		if now < 0 || now > drop {
			t.Fatalf("at %.1fms the drop = %d, outside 0..%d", ms, now, drop)
		}
		if first < 0 {
			first = now
		}
		if step := now - last; step > maxStep {
			maxStep = step
		}
		last = now
	}
	if first > 1 {
		t.Fatalf("the first 120Hz frame moved the top %dpt", first)
	}
	if maxStep > 2 {
		t.Fatalf("a 120Hz frame moved the top %dpt, want at most 2", maxStep)
	}
	if last != drop {
		t.Fatalf("the drop settled at %d, want %d", last, drop)
	}
}

// The resting capsule is one picture in two windows: the panel's idle frame
// and the picture that covers its blank resizes. The app entry has to look
// the same in both, hover fill included.
func TestIdleCapsuleEntryDrawsItsHover(t *testing.T) {
	a := islandShelf(false, settings.ShellStyleNotch)
	shot := func(hover bool) *ui.Tester {
		a.idleEntryHover = hover
		v := ui.NewTester(a.mainIdleCapsuleView, islandIdleW, islandIdleH)
		v.Frame()
		return v
	}
	off := shot(false).Image()
	on := shot(true).Image()

	// The entry is the 30pt box at the capsule's right end, inset 4pt; the
	// glyph sits in its middle, so a point 6pt from its left edge is fill
	// only.
	entryLeft := islandIdleW - 4 - appEntryW
	x, y := entryLeft+6, islandIdleH/2
	r0, g0, b0, _ := off.At(x, y).RGBA()
	r1, g1, b1, _ := on.At(x, y).RGBA()
	if r1>>8 <= r0>>8 || g1>>8 <= g0>>8 || b1>>8 <= b0>>8 {
		t.Fatalf("entry hover fill did not brighten (%d,%d,%d) -> (%d,%d,%d)",
			r0>>8, g0>>8, b0>>8, r1>>8, g1>>8, b1>>8)
	}
	// The capsule's body away from the entry is the same picture.
	if c0, c1 := off.At(10, islandIdleH/2), on.At(10, islandIdleH/2); c0 != c1 {
		t.Fatalf("the hover repainted the capsule body: %v -> %v", c0, c1)
	}
	// The entry's corners stay the capsule's black (the fill is rounded).
	corner := off.At(entryLeft, islandIdleH-1)
	if c := on.At(entryLeft, islandIdleH-1); c != corner {
		t.Fatalf("the hover fill leaked outside the rounded entry: %v -> %v", corner, c)
	}
}

// Hovering the app entry keeps the capsule and its entry on screen; moving
// off it and onto the capsule's body peeks as before.
func TestHoveringTheIdleEntryKeepsTheCapsule(t *testing.T) {
	a := islandShelf(false, settings.ShellStyleNotch)
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	r := a.islandRect()
	// The box the idle frame registers for the entry (noteAppEntry): the
	// capsule's right end, inset 4pt.
	a.apps.entryRect = mygo.Rectangle{
		X: r.X + r.Width - 4 - appEntryW, Y: r.Y, Width: appEntryW, Height: appEntryW,
	}
	entry := mygo.Point{X: a.apps.entryRect.X + appEntryW/2, Y: a.apps.entryRect.Y + appEntryW/2}

	// The entry keeps the capsule and shows its hover.
	driveMonitor(t, a, entry, false, 0, nil)
	if a.peeking {
		t.Fatal("hovering the app entry folded the capsule into the peek")
	}
	if !a.idleEntryHover {
		t.Fatal("hovering the app entry did not register the hover")
	}
	// The capsule's body beside it still peeks, and the entry's hover goes
	// with the capsule it belongs to.
	driveMonitor(t, a, mygo.Point{X: r.X + 20, Y: r.Y + 10}, false, 0, nil)
	if !a.peeking {
		t.Fatal("the capsule body did not peek")
	}
	if a.idleEntryHover {
		t.Fatal("the capsule body was taken for the entry")
	}
	// While the chevron peek is up it owns the capsule: its frame clears the
	// entry's box (nothing has drawn the entry), so the peek stays until the
	// pointer leaves.
	driveMonitor(t, a, entry, false, 0, nil)
	if !a.peeking {
		t.Fatal("the peeking capsule folded under a pointer over the peek")
	}
	// Leaving the capsule drops the peek.
	driveMonitor(t, a, mygo.Point{X: r.X + 20, Y: r.Y + 200}, false, 0, nil)
	if a.idleEntryHover || a.peeking {
		t.Fatalf("leaving the capsule left hover %v peek %v", a.idleEntryHover, a.peeking)
	}
}
