package editor

import (
	"math"
	"slices"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

const shapeDwellSeconds = 0.45

// CheckDwell checks whether an active freehand stroke has paused long enough to
// auto-snap into a recognized standard shape.
func (e *Editor) CheckDwell(now float64) bool {
	g, ok := e.gesture.(*freehandGesture)
	if !ok || g.snapped != "" || g.typ != doc.TypeDraw {
		return false
	}
	e.Now = now
	if now-g.lastMoveTime < shapeDwellSeconds {
		return false
	}
	if nid, ok := e.RecognizeStroke(g.id, e.pointer.Mods.Shift); ok {
		g.snapped = nid
		return true
	}
	return false
}

// RecognizeStroke analyzes a freehand draw stroke and replaces it with a
// matching standard shape (line, ellipse/circle, triangle, diamond, rectangle/square),
// preserving stroke width and styling. If regularize is true (Shift), the
// resulting geometry is snapped to regular proportions or 45-degree angles.
func (e *Editor) RecognizeStroke(id doc.ID, regularize bool) (doc.ID, bool) {
	s := e.Doc.Shapes[id]
	if s == nil || s.Type != doc.TypeDraw {
		return "", false
	}
	localPts := strokePoints(s)
	if len(localPts) < 4 {
		return "", false
	}
	m := e.PageMat(id)
	pts := make([]geo.Vec, len(localPts))
	for i, pt := range localPts {
		pts[i] = m.Apply(pt)
	}
	box := geo.BoxOf(pts)
	diag := math.Hypot(box.W, box.H)
	totalLen := geo.PolyLength(pts)
	if totalLen < 24 || diag < 16 {
		return "", false
	}

	kind, lineA, lineB := classifyStroke(pts, box, diag, totalLen, regularize)
	if kind == "" {
		return "", false
	}

	var newID doc.ID
	e.transact(func() {
		newID = doc.NewRecordID("shape")
		if kind == "line" {
			ns := doc.NewShape(newID, doc.TypeLine)
			ns.Parent = s.Parent
			ns.Index = s.Index
			ns.Color = s.Color
			ns.Dash = s.Dash
			ns.Size = s.Size
			ns.Opacity = s.Opacity
			ns.PenWidth = s.PenWidth
			ns.Seed = geo.Hash(string(newID))
			pMat := e.parentMat(s.Parent).Invert()
			aInP := pMat.Apply(lineA)
			bInP := pMat.Apply(lineB)
			ns.X, ns.Y = aInP.X, aInP.Y
			ns.Points = []doc.LinePoint{
				{ID: string(doc.NewRecordID("p")), Index: doc.FirstIndex, X: 0, Y: 0},
				{ID: string(doc.NewRecordID("p")), Index: doc.IndexBetween(doc.FirstIndex, ""), X: bInP.X - aInP.X, Y: bInP.Y - aInP.Y},
			}
			e.removeShape(id)
			e.put(ns)
			return
		}

		ns := doc.NewShape(newID, doc.TypeGeo)
		ns.Geo = kind
		ns.Parent = s.Parent
		ns.Index = s.Index
		ns.Color = s.Color
		ns.Fill = s.Fill
		ns.FillColor = s.FillColor
		ns.Dash = s.Dash
		ns.Size = s.Size
		ns.Opacity = s.Opacity
		ns.PenWidth = s.PenWidth
		ns.Seed = geo.Hash(string(newID))

		w, h := math.Max(box.W, 12), math.Max(box.H, 12)
		c := box.Center()
		if regularize || (kind == "ellipse" && w/h >= 0.85 && w/h <= 1.18) {
			side := math.Max(w, h)
			if !regularize && kind == "ellipse" {
				side = (w + h) / 2
			}
			w, h = side, side
		}
		tl := e.parentMat(s.Parent).Invert().Apply(geo.V(c.X-w/2, c.Y-h/2))
		ns.X, ns.Y, ns.W, ns.H = tl.X, tl.Y, w, h
		e.removeShape(id)
		e.put(ns)
	})
	return newID, true
}

func classifyStroke(pts []geo.Vec, box geo.Box, diag, totalLen float64, regularize bool) (kind string, a, b geo.Vec) {
	p0, p1 := pts[0], pts[len(pts)-1]
	endDist := p0.Dist(p1)

	// 1. Straight line check.
	if endDist >= 20 && endDist/totalLen >= 0.90 {
		maxDev := 0.0
		for _, pt := range pts {
			if d := geo.DistToSegment(pt, p0, p1); d > maxDev {
				maxDev = d
			}
		}
		if maxDev <= math.Max(14, endDist*0.09) {
			if regularize {
				ang := math.Round(p0.Angle(p1)/(math.Pi/4)) * (math.Pi / 4)
				p1 = p0.Add(geo.V(endDist, 0).Rot(ang))
			}
			return "line", p0, p1
		}
	}

	// 2. Closed shape check.
	if endDist > math.Max(32, diag*0.32) || totalLen < 60 {
		return "", geo.Vec{}, geo.Vec{}
	}

	// Check ellipse radial consistency in normalized bounding box coordinates.
	c := box.Center()
	rx, ry := math.Max(box.W/2, 1), math.Max(box.H/2, 1)
	radii := make([]float64, len(pts))
	var sum float64
	for i, pt := range pts {
		r := math.Hypot((pt.X-c.X)/rx, (pt.Y-c.Y)/ry)
		radii[i] = r
		sum += r
	}
	mean := sum / float64(len(radii))
	var varSum float64
	for _, r := range radii {
		d := r - mean
		varSum += d * d
	}
	stdDev := math.Sqrt(varSum / float64(len(radii)))

	simp := geo.SimplifyRDP(pts, math.Max(6, diag*0.05))
	if len(simp) > 1 && simp[0].Dist(simp[len(simp)-1]) <= math.Max(20, diag*0.20) {
		simp = simp[:len(simp)-1]
	}

	if math.Abs(mean-1.0) < 0.25 && stdDev < 0.17 && (len(simp) >= 5 || stdDev < 0.11) {
		return "ellipse", geo.Vec{}, geo.Vec{}
	}

	if len(simp) == 3 {
		return "triangle", geo.Vec{}, geo.Vec{}
	}

	if len(simp) == 4 {
		corners := box.Corners()
		mids := [4]geo.Vec{
			{X: box.X + box.W/2, Y: box.Y},
			{X: box.MaxX(), Y: box.Y + box.H/2},
			{X: box.X + box.W/2, Y: box.MaxY()},
			{X: box.X, Y: box.Y + box.H/2},
		}
		var cornerErr, midErr float64
		for _, v := range simp {
			bc := math.Inf(1)
			for _, cn := range corners {
				bc = math.Min(bc, v.Dist(cn))
			}
			bm := math.Inf(1)
			for _, md := range mids {
				bm = math.Min(bm, v.Dist(md))
			}
			cornerErr += bc
			midErr += bm
		}
		cornerErr /= 4
		midErr /= 4
		if midErr < cornerErr*0.82 && midErr <= diag*0.20 {
			return "diamond", geo.Vec{}, geo.Vec{}
		}
		if cornerErr <= diag*0.24 {
			return "rectangle", geo.Vec{}, geo.Vec{}
		}
	}

	if math.Abs(mean-1.0) < 0.28 && stdDev < 0.20 {
		return "ellipse", geo.Vec{}, geo.Vec{}
	}
	return "", geo.Vec{}, geo.Vec{}
}

// tryScribbleErase deletes overlapped unlocked shapes when the drawn stroke is
// a dense back-and-forth scribble gesture.
func (e *Editor) tryScribbleErase(scribbleID doc.ID) bool {
	s := e.Doc.Shapes[scribbleID]
	if s == nil {
		return false
	}
	localPts := strokePoints(s)
	if len(localPts) < 6 {
		return false
	}
	m := e.PageMat(scribbleID)
	pts := make([]geo.Vec, len(localPts))
	for i, pt := range localPts {
		pts[i] = m.Apply(pt)
	}
	simp := geo.SimplifyRDP(pts, 3.0)
	if len(simp) < 5 {
		return false
	}
	box := geo.BoxOf(pts)
	diag := math.Hypot(box.W, box.H)
	totalLen := geo.PolyLength(pts)
	if totalLen < 100 || diag < 14 || totalLen/math.Max(diag, 1) < 2.4 {
		return false
	}

	reversals := 0
	var prevDir geo.Vec
	hasPrev := false
	for i := 1; i < len(simp); i++ {
		d := simp[i].Sub(simp[i-1])
		if d.Len() < 6 {
			continue
		}
		u := d.Unit()
		if hasPrev && u.Dot(prevDir) < -0.25 {
			reversals++
		}
		prevDir = u
		hasPrev = true
	}
	if reversals < 4 {
		return false
	}

	var hits []doc.ID
	var walk func(parent doc.ID)
	walk = func(parent doc.ID) {
		for _, id := range e.Children(parent) {
			if id == scribbleID {
				continue
			}
			sh := e.Doc.Shapes[id]
			if sh == nil || sh.Locked {
				continue
			}
			// Containers are only traversed: a frame's own outline must never be
			// erased by hatching in its empty area.
			if sh.Type == doc.TypeGroup || sh.Type == doc.TypeFrame {
				walk(id)
				continue
			}
			// Only freehand ink and thin connectors are scribble-erasable. Closed
			// shapes keep their outline "through-crossing" semantics out of it too.
			switch sh.Type {
			case doc.TypeDraw, doc.TypeHighlight, doc.TypeLine, doc.TypeArrow:
			default:
				continue
			}
			if !e.PageBounds(id).Expand(6).Overlaps(box) {
				continue
			}
			outline := e.PageOutline(id)
			if len(outline) == 0 {
				continue
			}
			hit := false
			for i := 1; i < len(pts); i++ {
				if geo.SegmentHitsPolyline(pts[i-1], pts[i], outline, false, 4) {
					hit = true
					break
				}
			}
			if hit {
				top := e.outermostSelectable(id)
				if !slices.Contains(hits, top) {
					hits = append(hits, top)
				}
			}
		}
	}
	walk(e.Page)
	if len(hits) == 0 {
		return false
	}
	e.removeShape(scribbleID)
	e.deleteShapes(hits)
	return true
}
