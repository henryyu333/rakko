//go:build darwin

package main

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// The island's search drop. While the window holds the keyboard the panel
// sits below the menu bar's level, so the shell has to clear the bar; the
// row it clears is measured on screen from the shell's own top edge. It used
// to be measured from the window's top row instead, so an island dragged
// anywhere on the screen still dropped 33pt (Issue #6), and the drop switched
// in a single frame at every open and fold.

func TestFocusDropByGeometry(t *testing.T) {
	for _, tt := range []struct {
		name                      string
		dispTop, topInset, anchor int
		want                      int
	}{
		// A bare screen: the menu bar's 24 plus the 8pt header gap are the
		// line, and the resting island's row is 7.
		{"bare screen, default row", 0, 32, islandTopGap, 33},
		{"bare screen, one point higher", 0, 32, islandTopGap - 1, 34},
		{"on the line, no drop left", 0, 32, menubarLine(), 0},
		{"one point below the line", 0, 32, menubarLine() + 1, 0},
		{"dragged far below the menu bar", 0, 32, 190, 0},
		// A display that does not start at y=0: the line is measured from its
		// own top edge, not from the main display's.
		{"second display below the first", 956, 32, 956 + islandTopGap, 33},
		// A notched screen in island style rests 6pt under the housing; the
		// line is 8pt under it, so 2pt remain.
		{"notched screen, island row", 0, 32, 32 + islandNotchGap, 2},
		{"taller housing moves the line down", 0, 38, 38 + islandNotchGap, 2},
		// A display with no measured housing at all: the 24pt bar plus 8.
		{"unmeasured housing", 0, 0, islandTopGap, 25},
	} {
		if got := focusDrop(tt.dispTop, tt.topInset, tt.anchor); got != tt.want {
			t.Errorf("%s: focusDrop(%d, %d, %d) = %d, want %d",
				tt.name, tt.dispTop, tt.topInset, tt.anchor, got, tt.want)
		}
	}
}

// menubarLine is the screen row the search panel's top has to clear on this
// machine's synthetic geometry: the housing's 32 plus the 8pt header gap.
func menubarLine() int { return max(32, menuBarMinH) + headerTopGap }

// bareIslandShelf is a headless island on a screen without a camera housing:
// the resting row is 7 and the search drop is the full 33pt.
func bareIslandShelf() *shelf {
	return &shelf{
		island:     true,
		islandTop:  islandTopGap,
		topInset:   32,
		dispBounds: mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
	}
}

func TestFocusDropFollowsTheKeyboard(t *testing.T) {
	a := bareIslandShelf()
	a.focusMode = true
	if got := a.focusDropSettled(); got != 33 {
		t.Fatalf("settled drop while searching = %d, want 33", got)
	}
	if got := a.sheetTopSettled(); got != islandTopGap+33 {
		t.Fatalf("settled shell row = %d, want %d", got, islandTopGap+33)
	}
	// The drawn row follows the ease, not the state: the first frame of an
	// opening is still on the island's own row and the shell has to travel.
	for _, tt := range []struct {
		k    float32
		want int
	}{{0, islandTopGap}, {0.25, islandTopGap + 8}, {0.5, islandTopGap + 17}, {1, islandTopGap + 33}} {
		a.fx.focusK = tt.k
		if got := a.sheetTop(); got != tt.want {
			t.Errorf("focusK %.2f: shell row %d, want %d", tt.k, got, tt.want)
		}
	}
	// Handing the keyboard back (leaveFocusMode) settles on the island's own
	// row; the drawn shell comes up on the ease, never in one frame.
	a.focusMode = false
	if got := a.sheetTopSettled(); got != islandTopGap {
		t.Fatalf("settled row after the keyboard went back = %d, want %d", got, islandTopGap)
	}
	if got := a.sheetTop(); got != islandTopGap+33 {
		t.Fatalf("the drawn shell left its row before the ease ran: %d", got)
	}
	a.fx.focusK = 0
	if got := a.sheetTop(); got != islandTopGap {
		t.Fatalf("the drawn shell did not come back to its row: %d", got)
	}
}

func TestFocusDropLeavesTheNotchStyleAlone(t *testing.T) {
	// The hardware notch path draws the sheet out of the housing: its band
	// row is the empty strip the menu bar draws over, and it never dropped.
	a := &shelf{topInset: 32, dispBounds: mygo.Rectangle{Width: 1470, Height: 956}}
	a.focusMode = true
	a.fx.focusK = 1
	if got := a.focusDropOnScreen(); got != 0 {
		t.Fatalf("notch style drop = %d, want 0", got)
	}
	if got := a.sheetTop(); got != 0 {
		t.Fatalf("notch style shell row = %d, want 0", got)
	}
}

// A dragged island takes no drop at all: the panel's top edge is the
// capsule's own row on every frame, which is what the issue's recording
// showed jumping by 33pt.
func TestFocusDropVanishesOnADraggedIsland(t *testing.T) {
	a := bareIslandShelf()
	a.focusMode = true
	a.fx.focusK = 1
	a.y = 183 // the user's island, dragged below the menu bar
	if got := a.focusDropOnScreen(); got != 0 {
		t.Fatalf("drop on a dragged island = %d, want 0", got)
	}
	if got := a.sheetTop(); got != islandTopGap {
		t.Fatalf("shell row on a dragged island = %d, want its own %d", got, islandTopGap)
	}
	if got, want := a.y+a.sheetTop(), a.anchorScreenTop(); got != want {
		t.Fatalf("panel top %d is not the capsule's top %d", got, want)
	}
}

// The window envelope is measured at the settled row: a frame measured with
// the row the drop passes through would clip the panel it ends up holding.
func TestOpenFrameHoldsTheSettledSearchRow(t *testing.T) {
	a := bareIslandShelf()
	a.focusMode = true
	for _, k := range []float32{0, 0.4, 1} {
		a.fx.focusK = k
		f := a.openFrame(sheetW, sheetH, 0)
		need := a.sheetTopSettled() + sheetH
		if f.Y+f.Height < need {
			t.Errorf("focusK %.1f: frame %+v ends at %d, the settled panel needs %d",
				k, f, f.Y+f.Height, need)
		}
	}
}

// focusDropEase is the drop's travel: monotone, symmetric, and slow enough
// that no 60Hz frame moves the shell's top more than 3pt over the full 33.
func TestFocusDropEaseMovesLessThanThreePointsAFrame(t *testing.T) {
	const drop = 33.0
	const frames = 60                                     // the machine's display is 60Hz
	dur := float64(focusDropAnim.Duration) / float64(1e9) // seconds
	prev := 0.0
	maxStep := 0.0
	for i := 1; i <= frames; i++ {
		tm := dur * float64(i) / frames
		v := float64(focusDropEase(float32(tm / dur)))
		if v < prev-1e-6 || v > 1+1e-6 {
			t.Fatalf("ease at %.0fms is %.4f, after %.4f: not monotone", tm*1000, v, prev)
		}
		maxStep = math.Max(maxStep, drop*(v-prev))
		prev = v
	}
	if maxStep > 3 {
		t.Errorf("fastest frame moves the shell's top %.2fpt over a 33pt drop, want at most 3", maxStep)
	}
	// The whole drop is travelled, and a mid-frame pair mirrors around 0.5.
	if got := float64(focusDropEase(1)); got != 1 {
		t.Errorf("ease(1) = %v, want 1", got)
	}
	for _, tt := range []float32{0.1, 0.3, 0.5} {
		if a, b := focusDropEase(tt), focusDropEase(1-tt); math.Abs(float64(a+b-1)) > 1e-5 {
			t.Errorf("ease(%.2f)+ease(%.2f) = %v, want 1", tt, 1-tt, a+b)
		}
	}
	// It rests before the fold does: the close spring's rest is ~320ms past
	// the hide, so the last frame of the drop is already the settled one.
	if focusDropAnim.Duration >= 320*time.Millisecond {
		t.Errorf("the drop takes %v, longer than the fold's rest", focusDropAnim.Duration)
	}
}
