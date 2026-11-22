package main

import (
	"github.com/egoist/mygo"

	"rakko/internal/platform"
)

// upperDisplayTouchesSegment reports whether another display's bottom edge
// sits flush against top-edge segment [segX, segX+segW) along segY on target,
// returning the horizontally clipped touching interval [bx, bx+bw) at by=segY.
func upperDisplayTouchesSegment(ds []mygo.Display, target mygo.Display, segX, segY, segW int) (bx, by, bw int, ok bool) {
	if segW <= 0 || len(ds) < 2 {
		return 0, 0, 0, false
	}
	segRight := segX + segW
	var hit bool
	minX, maxX := segRight, segX
	for _, d := range ds {
		if target.ID != 0 && d.ID == target.ID {
			continue
		}
		if d.Bounds == target.Bounds {
			continue
		}
		// Allow ±1 DIP rounding when mixed-DPI scaling maps physical edges.
		diff := (d.Bounds.Y + d.Bounds.Height) - segY
		if diff < -1 || diff > 1 {
			continue
		}
		x0 := max(d.Bounds.X, segX)
		x1 := min(d.Bounds.X+d.Bounds.Width, segRight)
		if x1 <= x0 {
			continue
		}
		hit = true
		if x0 < minX {
			minX = x0
		}
		if x1 > maxX {
			maxX = x1
		}
	}
	if !hit || maxX <= minX {
		return 0, 0, 0, false
	}
	return minX, segY, maxX - minX, true
}

// resolveUpperSegment finds whether an upper display touches [segX, segX+segW)
// at segY on target. Overridden on Windows to test physical pixel adjacency for
// mixed-DPI monitors, and replaced by upperDisplayTouchesSegment in headless rigs.
var resolveUpperSegment = platformUpperSegment

func (a *shelf) barrierSegment() (bx, by, bw int, ok bool) {
	if !a.notchGuardOn() || a.notchW <= 0 || a.notchH <= 0 || len(a.displays) < 2 {
		return 0, 0, 0, false
	}
	segX, segW := a.notchX, a.notchW
	if a.island {
		r := a.islandRect()
		segX, segW = r.X, r.Width
	}
	target := mygo.Display{ID: a.dispID, Bounds: a.dispBounds}
	return resolveUpperSegment(a.displays, target, segX, a.dispBounds.Y, segW)
}

func (a *shelf) upperBarrierActive() bool {
	_, _, _, ok := a.barrierSegment()
	return ok
}

func applyBarrierSegment(reg platform.DragGuardRegion, a *shelf) platform.DragGuardRegion {
	if bx, by, bw, ok := a.barrierSegment(); ok {
		reg.BarrierEnabled = true
		reg.BarrierX = bx
		reg.BarrierY = by
		reg.BarrierWidth = bw
		reg.BarrierPushPt = platform.EdgeBarrierPushPt
	}
	return reg
}
