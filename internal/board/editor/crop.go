package editor

import (
	"math"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

const minCropFraction = 0.05

// EnterCrop enters interactive non-destructive crop mode for image shape id.
func (e *Editor) EnterCrop(id doc.ID) bool {
	s := e.Doc.Shapes[id]
	if s == nil || s.Type != doc.TypeImage || s.Locked {
		return false
	}
	e.StopEditing()
	e.EditingVertices = ""
	e.Select(id)
	e.Cropping = id
	return true
}

// ExitCrop leaves interactive crop mode.
func (e *Editor) ExitCrop() {
	e.Cropping = ""
}

// CanResetCrop reports whether any selected unlocked image currently has a crop applied.
func (e *Editor) CanResetCrop() bool {
	for _, id := range e.sel {
		if s := e.Doc.Shapes[id]; s != nil && s.Type == doc.TypeImage && !s.Locked && s.Crop != nil {
			return true
		}
	}
	return false
}

// ResetCrop removes the active crop on targeted (or selected) image shapes while
// expanding bounds back to the full uncropped bitmap scale cleanly.
func (e *Editor) ResetCrop(ids ...doc.ID) bool {
	targets := ids
	if len(targets) == 0 {
		targets = e.sel
	}
	changed := false
	for _, id := range targets {
		s := e.Doc.Shapes[id]
		if s == nil || s.Type != doc.TypeImage || s.Locked || s.Crop == nil {
			continue
		}
		if !changed {
			e.begin()
			changed = true
		}
		full, ok := UncroppedFrame(s)
		e.update(id, func(c *doc.Shape) {
			c.Crop = nil
			if ok {
				tl := geo.Rotate(c.Rotation).Apply(geo.V(full.X, full.Y))
				c.X += tl.X
				c.Y += tl.Y
				c.W = full.W
				c.H = full.H
			}
		})
	}
	if changed {
		e.commit()
	}
	return changed
}

// UncroppedFrame returns the full uncropped bitmap box in s's local coordinate space.
func UncroppedFrame(s *doc.Shape) (geo.Box, bool) {
	if s == nil || s.Type != doc.TypeImage || s.Crop == nil {
		return geo.Box{W: math.Max(s.W, 1), H: math.Max(s.H, 1)}, false
	}
	cw := math.Max(s.Crop.BottomRight.X-s.Crop.TopLeft.X, minCropFraction)
	ch := math.Max(s.Crop.BottomRight.Y-s.Crop.TopLeft.Y, minCropFraction)
	fullW := s.W / cw
	fullH := s.H / ch
	offX := s.Crop.TopLeft.X * fullW
	if s.FlipX {
		offX = (1 - s.Crop.BottomRight.X) * fullW
	}
	offY := s.Crop.TopLeft.Y * fullH
	if s.FlipY {
		offY = (1 - s.Crop.BottomRight.Y) * fullH
	}
	return geo.Box{
		X: -offX,
		Y: -offY,
		W: fullW,
		H: fullH,
	}, true
}

func (e *Editor) cropHandles(s *doc.Shape) []Handle {
	box, r, ok := e.SelectionFrame()
	if !ok {
		return nil
	}
	m := geo.Rotate(r)
	at := func(fx, fy float64) geo.Vec {
		return m.Apply(geo.V(box.X+box.W*fx, box.Y+box.H*fy))
	}
	coords := [][2]float64{
		{0, 0}, {1, 0}, {1, 1}, {0, 1},
		{0.5, 0}, {1, 0.5}, {0.5, 1}, {0, 0.5},
	}
	hs := make([]Handle, len(coords))
	for i, c := range coords {
		hs[i] = Handle{Kind: HandleCrop, Which: geo.V(c[0], c[1]), Point: at(c[0], c[1])}
	}
	return hs
}

type cropGesture struct {
	id         doc.ID
	init       *doc.Shape
	initMat    geo.Mat
	fullBoxLoc geo.Box
	l0, t0     float64
	r0, b0     float64
	which      geo.Vec
}

func newCropGesture(e *Editor, h Handle) *cropGesture {
	id := e.Cropping
	s := e.Doc.Shapes[id]
	if s == nil {
		return nil
	}
	l0, t0, r0, b0 := 0.0, 0.0, 1.0, 1.0
	if s.Crop != nil {
		l0, t0 = s.Crop.TopLeft.X, s.Crop.TopLeft.Y
		r0, b0 = s.Crop.BottomRight.X, s.Crop.BottomRight.Y
	}
	full, _ := UncroppedFrame(s)
	e.begin()
	return &cropGesture{
		id:         id,
		init:       s,
		initMat:    e.PageMat(id),
		fullBoxLoc: full,
		l0:         l0, t0: t0, r0: r0, b0: b0,
		which: h.Which,
	}
}

func (g *cropGesture) move(e *Editor) {
	if g == nil {
		return
	}
	q := g.initMat.Invert().Apply(e.pointer.Page)
	fw := math.Max(g.fullBoxLoc.W, 1)
	fh := math.Max(g.fullBoxLoc.H, 1)

	// Express drag handle in uncropped local fractions [0, 1]^2 (respecting flips).
	fx := geo.Clamp((q.X-g.fullBoxLoc.X)/fw, 0, 1)
	fy := geo.Clamp((q.Y-g.fullBoxLoc.Y)/fh, 0, 1)
	if g.init.FlipX {
		fx = 1 - fx
	}
	if g.init.FlipY {
		fy = 1 - fy
	}

	nl, nt, nr, nb := g.l0, g.t0, g.r0, g.b0
	switch {
	case g.which.X == 0:
		if g.init.FlipX {
			nr = geo.Clamp(fx, nl+minCropFraction, 1)
		} else {
			nl = geo.Clamp(fx, 0, nr-minCropFraction)
		}
	case g.which.X == 1:
		if g.init.FlipX {
			nl = geo.Clamp(fx, 0, nr-minCropFraction)
		} else {
			nr = geo.Clamp(fx, nl+minCropFraction, 1)
		}
	}
	switch {
	case g.which.Y == 0:
		if g.init.FlipY {
			nb = geo.Clamp(fy, nt+minCropFraction, 1)
		} else {
			nt = geo.Clamp(fy, 0, nb-minCropFraction)
		}
	case g.which.Y == 1:
		if g.init.FlipY {
			nt = geo.Clamp(fy, 0, nb-minCropFraction)
		} else {
			nb = geo.Clamp(fy, nt+minCropFraction, 1)
		}
	}

	visL, visT := nl, nt
	if g.init.FlipX {
		visL = 1 - nr
	}
	if g.init.FlipY {
		visT = 1 - nb
	}
	locTL := geo.V(g.fullBoxLoc.X+visL*fw, g.fullBoxLoc.Y+visT*fh)
	parentOrigin := e.parentMat(g.init.Parent).Invert().Apply(g.initMat.Apply(locTL))

	c := g.init.Clone()
	c.X = parentOrigin.X
	c.Y = parentOrigin.Y
	c.W = (nr - nl) * fw
	c.H = (nb - nt) * fh
	if nl <= 1e-4 && nt <= 1e-4 && nr >= 1-1e-4 && nb >= 1-1e-4 {
		c.Crop = nil
	} else {
		c.Crop = &doc.Crop{
			TopLeft:     geo.V(nl, nt),
			BottomRight: geo.V(nr, nb),
		}
	}
	e.put(c)
}

func (g *cropGesture) up(e *Editor)     { e.commit() }
func (g *cropGesture) cancel(e *Editor) { e.rollback() }
