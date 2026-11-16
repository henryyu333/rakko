//go:build windows

package main

// Both platforms share the box-based petReturnTarget rule: a carried otter
// returns to the island capsule when its own box overlaps the capsule's zone,
// even when parked at the top of a Windows work area, and an otter released
// below the capsule stays on the desktop.

import (
	"testing"

	"github.com/egoist/mygo"
)

func TestPetReturnUsesBoxOnWindows(t *testing.T) {
	a := &shelf{}
	a.island = true
	capsule := a.islandRect()

	// Box parked on the capsule at the top of the work area: stows.
	onCapsule := mygo.Rectangle{X: capsule.X + 20, Y: 0, Width: 74, Height: 96}
	if !petReturnTarget(a, onCapsule) {
		t.Fatalf("otter box %+v overlapping capsule %+v did not count as a return", onCapsule, capsule)
	}

	// Box released below the capsule: stays on desktop.
	belowCapsule := mygo.Rectangle{X: capsule.X + 20, Y: capsule.Y + capsule.Height + notchHoverPadY, Width: 74, Height: 96}
	if petReturnTarget(a, belowCapsule) {
		t.Fatalf("otter box %+v below capsule %+v counted as a return", belowCapsule, capsule)
	}
}
