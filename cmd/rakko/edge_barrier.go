package main

import (
	"github.com/egoist/mygo"

	"rakko/internal/platform"
)

type barrierCacheKey struct {
	dispID     int64
	dispBounds mygo.Rectangle
	notchX     int
	notchW     int
	notchH     int
	island     bool
	islandTop  int
	islandDX   int
	islandDY   int
	displayCnt int
}

type shelfBarrier struct {
	valid   bool
	key     barrierCacheKey
	enabled bool
	x       int
	y       int
	width   int
}

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

func (a *shelf) currentBarrierKey() barrierCacheKey {
	return barrierCacheKey{
		dispID:     a.dispID,
		dispBounds: a.dispBounds,
		notchX:     a.notchX,
		notchW:     a.notchW,
		notchH:     a.notchH,
		island:     a.island,
		islandTop:  a.islandTop,
		islandDX:   a.islandDX,
		islandDY:   a.islandDY,
		displayCnt: len(a.displays),
	}
}

// refreshBarrier recomputes and caches the top-edge anchor barrier on the shelf
// when placement, display layout, shell style, or island row changes. Hover and
// drop-band hit tests read the cached result without re-enumerating monitors.
func (a *shelf) refreshBarrier() {
	key := a.currentBarrierKey()
	if !a.notchGuardOn() || a.notchW <= 0 || a.notchH <= 0 || len(a.displays) < 2 {
		a.barrier = shelfBarrier{valid: true, key: key}
		return
	}
	segX, segW := a.notchX, a.notchW
	if a.island {
		r := a.islandRect()
		segX, segW = r.X, r.Width
	}
	target := mygo.Display{ID: a.dispID, Bounds: a.dispBounds}
	bx, by, bw, ok := resolveUpperSegment(a.displays, target, segX, a.dispBounds.Y, segW)
	a.barrier = shelfBarrier{
		valid:   true,
		key:     key,
		enabled: ok,
		x:       bx,
		y:       by,
		width:   bw,
	}
}

func (a *shelf) ensureBarrier() {
	if !a.barrier.valid || a.barrier.key != a.currentBarrierKey() {
		a.refreshBarrier()
	}
}

func (a *shelf) upperBarrierActive() bool {
	return a.barrier.enabled
}

func applyBarrierSegment(reg platform.DragGuardRegion, a *shelf) platform.DragGuardRegion {
	a.ensureBarrier()
	if a.barrier.enabled {
		reg.BarrierEnabled = true
		reg.BarrierX = a.barrier.x
		reg.BarrierY = a.barrier.y
		reg.BarrierWidth = a.barrier.width
		reg.BarrierPushPt = platform.EdgeBarrierPushPt
	}
	return reg
}
