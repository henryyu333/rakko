//go:build windows

package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
)

func windowsNotchShelf() *shelf {
	return &shelf{
		notchX:       870,
		notchY:       0,
		notchW:       180,
		notchH:       32,
		hasNotch:     true,
		x:            (1920 - sheetW) / 2,
		y:            0,
		dispBounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080},
		dispWork:     mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040},
		settings:     settings.Default(),
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
}

func driveWindowsMonitor(t *testing.T, a *shelf, now time.Time, p mygo.Point) {
	t.Helper()
	oldCursor, oldState, oldTypes, oldDown := cursorPoint, dragState, dragTypes, dragLeftDown
	t.Cleanup(func() {
		cursorPoint, dragState, dragTypes, dragLeftDown = oldCursor, oldState, oldTypes, oldDown
	})
	cursorPoint = func() mygo.Point { return p }
	dragState = func() (bool, int64) { return false, 0 }
	dragTypes = func() []string { return nil }
	dragLeftDown = func() bool { return false }
	a.monitorDrag(now)
}

func TestWindowsNotchTopEdgeHoverTrigger(t *testing.T) {
	now := time.Now()
	cx := 960

	// Idle shell: only y = notchY + 0..2 within anchor width triggers peek.
	for _, y := range []int{0, 1, 2} {
		a := windowsNotchShelf()
		p := mygo.Point{X: cx, Y: y}
		if !a.pointerNearZone(p) {
			t.Fatalf("y=%d at top edge: pointerNearZone = false, want true", y)
		}
		driveWindowsMonitor(t, a, now, p)
		if !a.peeking {
			t.Fatalf("y=%d at top edge did not open peek", y)
		}
	}
	for _, x := range []int{870 - notchHoverPadX, 870 + 180 + notchHoverPadX} {
		a := windowsNotchShelf()
		p := mygo.Point{X: x, Y: 1}
		driveWindowsMonitor(t, a, now, p)
		if !a.peeking {
			t.Fatalf("top-edge horizontal boundary x=%d did not open peek", x)
		}
	}

	// Below the 3px top edge: an invisible hot zone must not cover tabs/title bars.
	for _, y := range []int{3, 4, 20, 32, 36} {
		a := windowsNotchShelf()
		p := mygo.Point{X: cx, Y: y}
		if a.pointerNearZone(p) {
			t.Fatalf("y=%d below top edge: pointerNearZone = true, want false", y)
		}
		driveWindowsMonitor(t, a, now, p)
		if a.peeking {
			t.Fatalf("y=%d below top edge unexpectedly opened peek", y)
		}
	}

	// Outside horizontal bounds at the top edge: must not trigger.
	for _, x := range []int{400, 870 - notchHoverPadX - 1, 870 + 180 + notchHoverPadX + 1} {
		a := windowsNotchShelf()
		p := mygo.Point{X: x, Y: 1}
		if a.pointerNearZone(p) {
			t.Fatalf("x=%d outside horizontal reach: pointerNearZone = true, want false", x)
		}
		driveWindowsMonitor(t, a, now, p)
		if a.peeking {
			t.Fatalf("x=%d outside horizontal reach unexpectedly opened peek", x)
		}
	}

	// Once peeking, moving down into the peek pill holds it; leaving retracts it;
	// after retracting, y=20 no longer re-triggers.
	a := windowsNotchShelf()
	driveWindowsMonitor(t, a, now, mygo.Point{X: cx, Y: 1})
	if !a.peeking {
		t.Fatal("initial top-edge hover did not open peek")
	}
	for _, p := range []mygo.Point{{X: cx, Y: 20}, {X: cx, Y: 40}} {
		driveWindowsMonitor(t, a, now, p)
		if !a.peeking {
			t.Fatalf("pointer at %+v inside peek shape dropped peek", p)
		}
	}
	driveWindowsMonitor(t, a, now, mygo.Point{X: cx, Y: 60})
	if a.peeking {
		t.Fatal("moving below the peek shape did not close peek")
	}
	driveWindowsMonitor(t, a, now, mygo.Point{X: cx, Y: 20})
	if a.peeking {
		t.Fatal("moving back to y=20 on an idle closed notch re-triggered peek")
	}
}

func TestWindowsNotchHoverUsesFullAnchorWhenWingsVisible(t *testing.T) {
	now := time.Now()
	a := windowsNotchShelf()
	a.clock = newClockModule(a)
	a.clock.playSound = func() {}
	a.clock.startTimer(now, 5*time.Minute)

	p := mygo.Point{X: 960, Y: 20}
	if !a.pointerNearZone(p) {
		t.Fatal("countdown wings visible: pointerNearZone(960,20) = false, want true")
	}
	driveWindowsMonitor(t, a, now, p)
	if !a.peeking {
		t.Fatal("countdown wings visible: hover at y=20 did not hold peek")
	}
}

func TestWindowsNotchTopEdgeFollowsDisplayOriginAndIslandStyle(t *testing.T) {
	now := time.Now()

	// Secondary display above primary: notchY = -1080.
	sec := windowsNotchShelf()
	sec.notchY = -1080
	sec.y = -1080
	sec.dispBounds = mygo.Rectangle{X: 0, Y: -1080, Width: 1920, Height: 1080}
	driveWindowsMonitor(t, sec, now, mygo.Point{X: 960, Y: -1078})
	if !sec.peeking {
		t.Fatal("secondary display top edge y=-1078 did not open peek")
	}
	sec2 := windowsNotchShelf()
	sec2.notchY = -1080
	sec2.y = -1080
	sec2.dispBounds = mygo.Rectangle{X: 0, Y: -1080, Width: 1920, Height: 1080}
	driveWindowsMonitor(t, sec2, now, mygo.Point{X: 960, Y: -1077})
	if sec2.peeking {
		t.Fatal("secondary display y=-1077 (3px below top edge) unexpectedly opened peek")
	}

	// Top taskbar fallback / Island style keeps full island hover box.
	isl := windowsNotchShelf()
	isl.hasNotch = false
	isl.island = true
	isl.islandTop = islandTopGap
	driveWindowsMonitor(t, isl, now, mygo.Point{X: 960, Y: islandTopGap + 15})
	if !isl.peeking {
		t.Fatal("island style did not hover over its capsule")
	}
}
