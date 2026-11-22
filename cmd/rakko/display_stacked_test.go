package main

import (
	"testing"

	"github.com/egoist/mygo"

	"rakko/internal/settings"
)

// syntheticDisplayRig replaces displayNearest, screenDisplays, notchRect and
// cursorPoint with an editable multi-display layout for headless tests, and
// restores the real lookups on cleanup.
type syntheticDisplayRig struct {
	displays []mygo.Display
	notches  map[int64]mygo.Rectangle
	cursor   mygo.Point
}

func newStackedDualDisplayRig(t *testing.T, lowerNotch bool) *syntheticDisplayRig {
	t.Helper()
	lower := mygo.Display{
		ID:       1,
		Primary:  true,
		Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
		WorkArea: mygo.Rectangle{X: 0, Y: 25, Width: 1470, Height: 931},
	}
	upper := mygo.Display{
		ID:       2,
		Primary:  false,
		Bounds:   mygo.Rectangle{X: -225, Y: -1080, Width: 1920, Height: 1080},
		WorkArea: mygo.Rectangle{X: -225, Y: -1055, Width: 1920, Height: 1055},
	}
	notches := map[int64]mygo.Rectangle{}
	if lowerNotch {
		notches[1] = mygo.Rectangle{X: 645, Y: 0, Width: 180, Height: 32}
	}
	rig := &syntheticDisplayRig{
		displays: []mygo.Display{lower, upper},
		notches:  notches,
		cursor:   mygo.Point{X: 735, Y: 400},
	}

	oldNearest := displayNearest
	oldDisplays := screenDisplays
	oldNotch := notchRect
	oldCursor := cursorPoint
	t.Cleanup(func() {
		displayNearest = oldNearest
		screenDisplays = oldDisplays
		notchRect = oldNotch
		cursorPoint = oldCursor
	})

	displayNearest = func(p mygo.Point) mygo.Display {
		return nearestDisplayIn(rig.displays, p)
	}
	screenDisplays = func() []mygo.Display {
		out := make([]mygo.Display, len(rig.displays))
		copy(out, rig.displays)
		return out
	}
	notchRect = func(id int64) (int, int, int, int, bool) {
		r, ok := rig.notches[id]
		if !ok || r.Width <= 0 || r.Height <= 0 {
			return 0, 0, 0, 0, false
		}
		return r.X, r.Y, r.Width, r.Height, true
	}
	cursorPoint = func() mygo.Point {
		return rig.cursor
	}
	return rig
}

func (r *syntheticDisplayRig) setCursor(p mygo.Point) {
	r.cursor = p
}

func (r *syntheticDisplayRig) unplug(id int64) {
	kept := make([]mygo.Display, 0, len(r.displays))
	for _, d := range r.displays {
		if d.ID != id {
			kept = append(kept, d)
		}
	}
	r.displays = kept
}

func (r *syntheticDisplayRig) plug(d mygo.Display) {
	for i := range r.displays {
		if r.displays[i].ID == d.ID {
			r.displays[i] = d
			return
		}
	}
	r.displays = append(r.displays, d)
}

func nearestDisplayIn(ds []mygo.Display, p mygo.Point) mygo.Display {
	if len(ds) == 0 {
		return mygo.Display{}
	}
	for _, d := range ds {
		if insideRect(p, d.Bounds) {
			return d
		}
	}
	best := ds[0]
	bestDist := rectDistSq(p, ds[0].Bounds)
	for _, d := range ds[1:] {
		if dist := rectDistSq(p, d.Bounds); dist < bestDist {
			best = d
			bestDist = dist
		}
	}
	return best
}

func rectDistSq(p mygo.Point, r mygo.Rectangle) int {
	dx, dy := 0, 0
	if p.X < r.X {
		dx = r.X - p.X
	} else if p.X >= r.X+r.Width {
		dx = p.X - (r.X + r.Width - 1)
	}
	if p.Y < r.Y {
		dy = r.Y - p.Y
	} else if p.Y >= r.Y+r.Height {
		dy = p.Y - (r.Y + r.Height - 1)
	}
	return dx*dx + dy*dy
}

func TestSyntheticStackedDualDisplayLayout(t *testing.T) {
	rig := newStackedDualDisplayRig(t, true)
	if ds := screenDisplays(); len(ds) != 2 {
		t.Fatalf("screenDisplays len = %d, want 2", len(ds))
	}
	if d := displayNearest(mygo.Point{X: 735, Y: 0}); d.ID != 1 || !d.Primary {
		t.Fatalf("boundary Y=0 resolved to %+v, want lower primary ID=1", d)
	}
	if d := displayNearest(mygo.Point{X: 735, Y: -1}); d.ID != 2 || d.Primary {
		t.Fatalf("boundary Y=-1 resolved to %+v, want upper ID=2", d)
	}
	if x, y, w, h, has := notchRect(1); !has || x != 645 || y != 0 || w != 180 || h != 32 {
		t.Fatalf("lower notchRect = (%d,%d,%d,%d,%v), want (645,0,180,32,true)", x, y, w, h, has)
	}
	if _, _, _, _, has := notchRect(2); has {
		t.Fatal("upper display unexpectedly reported a notch")
	}

	rig.unplug(2)
	if ds := screenDisplays(); len(ds) != 1 || ds[0].ID != 1 {
		t.Fatalf("after unplug: screenDisplays = %+v, want [ID=1]", ds)
	}
	if d := displayNearest(mygo.Point{X: 735, Y: -50}); d.ID != 1 {
		t.Fatalf("after unplug: nearest = %+v, want ID=1", d)
	}
}

func TestSyntheticStackedDualDisplayShelfPlaceAndUnplug(t *testing.T) {
	rig := newStackedDualDisplayRig(t, true)
	a := &shelf{settings: settings.Default()}

	rig.setCursor(mygo.Point{X: 735, Y: 400})
	a.placeAt(cursorPoint())
	if a.dispID != 1 || a.dispBounds.Y != 0 {
		t.Fatalf("initial place on lower primary: dispID=%d bounds=%+v", a.dispID, a.dispBounds)
	}

	// Crossing upward onto the upper display moves the closed shelf there.
	rig.setCursor(mygo.Point{X: 735, Y: -20})
	a.pointerMoved()
	if a.dispID != 2 || a.dispBounds.Y != -1080 {
		t.Fatalf("crossed onto upper display: dispID=%d bounds=%+v", a.dispID, a.dispBounds)
	}

	// Unplugging the upper display falls back to the lower primary.
	upper := rig.displays[1]
	rig.unplug(2)
	a.placeAt(cursorPoint())
	if a.dispID != 1 || a.dispBounds.Y != 0 {
		t.Fatalf("after upper unplug: dispID=%d bounds=%+v", a.dispID, a.dispBounds)
	}

	// Replugging restores the upper display when queried there.
	rig.plug(upper)
	a.placeAt(mygo.Point{X: 735, Y: -20})
	if a.dispID != 2 || a.dispBounds.Y != -1080 {
		t.Fatalf("after upper replug: dispID=%d bounds=%+v", a.dispID, a.dispBounds)
	}
}
