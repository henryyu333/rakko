package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/platform"
	"rakko/internal/settings"
)

func TestUpperDisplayTouchesSegmentLayouts(t *testing.T) {
	lower := mygo.Display{ID: 1, Primary: true, Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956}}
	if _, _, _, ok := upperDisplayTouchesSegment([]mygo.Display{lower}, lower, 645, 0, 180); ok {
		t.Fatal("single display enabled top-edge barrier")
	}

	sideBySide := mygo.Display{ID: 2, Bounds: mygo.Rectangle{X: 1470, Y: 0, Width: 1920, Height: 1080}}
	if _, _, _, ok := upperDisplayTouchesSegment([]mygo.Display{lower, sideBySide}, lower, 645, 0, 180); ok {
		t.Fatal("side-by-side display enabled top-edge barrier")
	}

	offsetNoOverlap := mygo.Display{ID: 3, Bounds: mygo.Rectangle{X: 0, Y: -1080, Width: 500, Height: 1080}}
	if _, _, _, ok := upperDisplayTouchesSegment([]mygo.Display{lower, offsetNoOverlap}, lower, 645, 0, 180); ok {
		t.Fatal("upper display outside notch horizontal span enabled barrier")
	}

	centeredAbove := mygo.Display{ID: 4, Bounds: mygo.Rectangle{X: -225, Y: -1080, Width: 1920, Height: 1080}}
	bx, by, bw, ok := upperDisplayTouchesSegment([]mygo.Display{lower, centeredAbove}, lower, 645, 0, 180)
	if !ok || bx != 645 || by != 0 || bw != 180 {
		t.Fatalf("stacked centered upper display got (%d,%d,%d,%v), want (645,0,180,true)", bx, by, bw, ok)
	}

	// Partial overlap clips precisely to the touching sub-segment, including
	// mixed-DPI ±1 DIP boundary rounding.
	partialMixedDPI := mygo.Display{ID: 5, Bounds: mygo.Rectangle{X: 700, Y: -719, Width: 1280, Height: 720}}
	bx, by, bw, ok = upperDisplayTouchesSegment([]mygo.Display{lower, partialMixedDPI}, lower, 645, 0, 180)
	if !ok || bx != 700 || by != 0 || bw != 125 {
		t.Fatalf("partial overlap got (%d,%d,%d,%v), want (700,0,125,true)", bx, by, bw, ok)
	}
}

func TestShelfBarrierRegionFollowMainPinnedAndStyles(t *testing.T) {
	rig := newStackedDualDisplayRig(t, true)
	for _, mode := range []string{"", settings.DisplayMain, "1"} {
		t.Run("mode="+mode, func(t *testing.T) {
			a := &shelf{settings: settings.Settings{Display: mode, ShellStyle: settings.ShellStyleNotch}}
			a.placeAt(mygo.Point{X: 735, Y: 400})
			g := a.dragGuardRegion()
			if !g.BarrierEnabled || g.BarrierX != 645 || g.BarrierY != 0 || g.BarrierWidth != 180 || g.BarrierPushPt != platform.EdgeBarrierPushPt {
				t.Fatalf("notch mode %q barrier = %+v", mode, g)
			}

			// Switch to Island at default row: anchor narrows to the 120pt capsule.
			a.settings.ShellStyle = settings.ShellStyleIsland
			a.placeAt(mygo.Point{X: 735, Y: 400})
			g = a.dragGuardRegion()
			r := a.islandRect()
			if !g.BarrierEnabled || g.BarrierX != r.X || g.BarrierY != 0 || g.BarrierWidth != r.Width {
				t.Fatalf("island default row barrier = %+v, capsule=%+v", g, r)
			}

			// Hover/drop stopped at the top edge must wake the default-row island.
			if !a.overHoverAnchor(mygo.Point{X: r.X + r.Width/2, Y: 0}, time.Now()) {
				t.Fatal("default-row island did not wake on pointer stopped at display top edge")
			}

			// Dragged island off default row disables barrier.
			a.islandDY = 40
			if g := a.dragGuardRegion(); g.BarrierEnabled {
				t.Fatalf("moved island still armed barrier: %+v", g)
			}
		})
	}

	// Follow onto the upper screen: no screen above upper -> barrier off.
	a := &shelf{settings: settings.Settings{Display: "", ShellStyle: settings.ShellStyleNotch}}
	a.placeAt(mygo.Point{X: 735, Y: -200})
	if g := a.dragGuardRegion(); g.BarrierEnabled {
		t.Fatalf("upper display enabled top-edge barrier with nothing above it: %+v", g)
	}

	// Unplugging upper display disables barrier.
	a.placeAt(mygo.Point{X: 735, Y: 400})
	if !a.dragGuardRegion().BarrierEnabled {
		t.Fatal("expected barrier while upper display attached")
	}
	rig.unplug(2)
	a.placeAt(mygo.Point{X: 735, Y: 400})
	if g := a.dragGuardRegion(); g.BarrierEnabled {
		t.Fatalf("unplugged upper display kept barrier armed: %+v", g)
	}
}

func TestHoverAndDropBandUseCachedBarrier(t *testing.T) {
	newStackedDualDisplayRig(t, true)

	var displayCalls, resolverCalls int
	origDisplays := screenDisplays
	origResolver := resolveUpperSegment
	t.Cleanup(func() {
		screenDisplays = origDisplays
		resolveUpperSegment = origResolver
	})
	screenDisplays = func() []mygo.Display {
		displayCalls++
		return origDisplays()
	}
	resolveUpperSegment = func(ds []mygo.Display, target mygo.Display, segX, segY, segW int) (int, int, int, bool) {
		resolverCalls++
		return origResolver(ds, target, segX, segY, segW)
	}

	a := &shelf{
		dataDir:  t.TempDir(),
		settings: settings.Settings{Display: settings.DisplayFollow, ShellStyle: settings.ShellStyleIsland},
	}
	a.placeAt(mygo.Point{X: 735, Y: 400})
	if displayCalls != 1 || resolverCalls != 1 {
		t.Fatalf("placeAt called display/resolver (%d, %d) times; want (1, 1)", displayCalls, resolverCalls)
	}

	r := a.islandRect()
	mid := mygo.Point{X: r.X + r.Width/2, Y: 0}
	now := time.Now()
	for i := 0; i < 50; i++ {
		if !a.overHoverAnchor(mid, now) {
			t.Fatalf("hover %d missed top-edge island barrier", i)
		}
		if !a.dropBandX(mid) {
			t.Fatalf("dropBandX %d missed top-edge island barrier", i)
		}
		if !a.dropBand(mid) {
			t.Fatalf("dropBand %d missed top-edge island barrier", i)
		}
		g := a.dragGuardRegion()
		if !g.BarrierEnabled || g.BarrierX != r.X || g.BarrierWidth != r.Width {
			t.Fatalf("cached dragGuardRegion %d mismatch: %+v", i, g)
		}
	}
	if displayCalls != 1 || resolverCalls != 1 {
		t.Fatalf("hover/drop-band/dragGuardRegion re-queried displays: displayCalls=%d resolverCalls=%d, want (1, 1)", displayCalls, resolverCalls)
	}

	// Headless style change refreshes cached segment once.
	a.setShellStyle(settings.ShellStyleNotch)
	if resolverCalls != 2 || displayCalls != 1 {
		t.Fatalf("setShellStyle(Notch) expected 1 resolver refresh and 0 extra display calls; got (%d, %d)", resolverCalls, displayCalls)
	}
	if !a.barrier.enabled || a.barrier.width != 180 {
		t.Fatalf("after style switch barrier = %+v, want width 180", a.barrier)
	}

	// Switch back to Island, then move off default row: BarrierEnabled clears without extra resolver calls.
	a.setShellStyle(settings.ShellStyleIsland)
	if resolverCalls != 3 || !a.barrier.enabled {
		t.Fatalf("setShellStyle(Island) expected resolverCalls=3 enabled=true, got (%d, %v)", resolverCalls, a.barrier.enabled)
	}
	a.islandDY = 32
	g := a.dragGuardRegion()
	if g.BarrierEnabled || resolverCalls != 3 {
		t.Fatalf("moved island expected BarrierEnabled=false and resolverCalls=3, got enabled=%v resolverCalls=%d", g.BarrierEnabled, resolverCalls)
	}
}
