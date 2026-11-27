package main

// The island's stretched hover and drop zones while an upper display keeps
// the capsule on its default row (notch.go's overHoverAnchor, drag.go's
// dropBandX): the stretch reaches the screen's top edge on the Mac, whose
// menu bar rows belong to the shell's reach — but on Windows a taskbar
// docked at the top owns the rows above the island, so the zones stop at
// the work area's top instead of eating the taskbar's clicks and drags.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// upperBarrierShelf is an island-style shelf on a 1920 × 1080 display with
// an upper display active (the barrier is on), resting on its default row.
func upperBarrierShelf(dispWork mygo.Rectangle) *shelf {
	return &shelf{
		island:    true,
		islandTop: islandTopGap,
		y:         dispWork.Y, // the row placeIslandAxis rests the capsule on
		notchX:    870, notchW: 180, notchH: 32,
		dispBounds: mygo.Rectangle{Width: 1920, Height: 1080},
		dispWork:   dispWork,
		barrier:    shelfBarrier{valid: true, enabled: true},
	}
}

func TestUpperBarrierZonesKeepOffATopTaskbar(t *testing.T) {
	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	// A top taskbar: the work area starts 32pt below the screen's top edge
	// and the island rests below it.
	a := upperBarrierShelf(mygo.Rectangle{Y: 32, Width: 1920, Height: 1048})
	island := a.islandRect()
	if island.Y != 32+islandTopGap {
		t.Fatalf("island row %d, want %d below the taskbar", island.Y, 32+islandTopGap)
	}
	now := time.Now()
	for _, p := range []mygo.Point{
		{X: 960, Y: 5}, {X: 960, Y: 20}, {X: 960, Y: 31},
	} {
		if a.overHoverAnchor(p, now) {
			t.Errorf("hover at %+v (taskbar row) triggered", p)
		}
		if a.dropBandX(p) {
			t.Errorf("drop band at %+v (taskbar row) triggered", p)
		}
	}
	for _, p := range []mygo.Point{
		{X: 960, Y: 32}, {X: 960, Y: 38}, {X: 960, Y: 50},
	} {
		if !a.overHoverAnchor(p, now) {
			t.Errorf("hover at %+v (safe row) did not trigger", p)
		}
		if !a.dropBandX(p) {
			t.Errorf("drop band at %+v (safe row) did not trigger", p)
		}
	}
	// Off the island's horizontal reach nothing triggers, taskbar or not.
	if a.overHoverAnchor(mygo.Point{X: 400, Y: 36}, now) || a.dropBandX(mygo.Point{X: 400, Y: 36}) {
		t.Error("a point beside the island triggered the zones")
	}
}

func TestUpperBarrierZonesKeepTheTopEdgeWithoutATaskbar(t *testing.T) {
	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	// No top taskbar: the work area starts at the screen's top edge, and
	// the stretch keeps today's top-of-screen behaviour.
	a := upperBarrierShelf(mygo.Rectangle{Width: 1920, Height: 1080})
	now := time.Now()
	for _, p := range []mygo.Point{
		{X: 960, Y: 0}, {X: 960, Y: 5}, {X: 960, Y: 38},
	} {
		if !a.overHoverAnchor(p, now) {
			t.Errorf("hover at %+v lost the top-edge stretch", p)
		}
		if !a.dropBandX(p) {
			t.Errorf("drop band at %+v lost the top-edge stretch", p)
		}
	}
}

func TestUpperBarrierZonesKeepTheMenuBarOnTheMac(t *testing.T) {
	// The Mac build must be untouched: a menu bar above the island (the
	// work area starts 25pt down) still stretches the zones to the
	// screen's top edge, where the shell is reachable.
	a := upperBarrierShelf(mygo.Rectangle{Y: 25, Width: 1920, Height: 1055})
	now := time.Now()
	for _, p := range []mygo.Point{
		{X: 960, Y: 0}, {X: 960, Y: 5}, {X: 960, Y: 24},
	} {
		if !a.overHoverAnchor(p, now) {
			t.Errorf("Mac hover at %+v lost the menu-bar rows", p)
		}
		if !a.dropBandX(p) {
			t.Errorf("Mac drop band at %+v lost the menu-bar rows", p)
		}
	}
}
