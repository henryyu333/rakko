package editor

import (
	"math"
	"slices"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

// ArrowHeads lists the 8 supported arrowhead styles for both start and end terminals.
var ArrowHeads = []string{
	"none",
	"arrow",
	"triangle",
	"square",
	"dot",
	"diamond",
	"inverted",
	"bar",
}

// IsValidArrowHead reports whether head is one of the 8 supported arrowhead styles.
func IsValidArrowHead(head string) bool {
	return slices.Contains(ArrowHeads, head)
}

// ArrowInfo describes the computed route and clipped terminals of an arrow in its local space.
type ArrowInfo struct {
	Start, End, RawStart, RawEnd geo.Vec
	Mid                          geo.Vec
	Arc                          bool
	Center                       geo.Vec
	Radius                       float64
	AngleA, Sweep                float64
	Elbow                        []geo.Vec
	Line                         []geo.Vec
}

type arrowMemo struct {
	s      *doc.Shape
	starts [2]*doc.Shape
	binds  [2]*doc.Binding
	mats   [3]geo.Mat
	info   *ArrowInfo
}

func (e *Editor) indexBindings() {
	if e.bindFrom != nil {
		return
	}
	e.bindFrom = make(map[doc.ID][]*doc.Binding, len(e.Doc.Bindings))
	e.bindTo = make(map[doc.ID][]*doc.Binding, len(e.Doc.Bindings))
	for _, b := range e.Doc.Bindings {
		e.bindFrom[b.From] = append(e.bindFrom[b.From], b)
		e.bindTo[b.To] = append(e.bindTo[b.To], b)
	}
}

func (e *Editor) bindingOf(shapeID doc.ID, terminal string) *doc.Binding {
	e.indexBindings()
	for _, b := range e.bindFrom[shapeID] {
		if b.Terminal == terminal {
			return b
		}
	}
	return nil
}

// BindingsTo returns all bindings targeting shape id.
func (e *Editor) BindingsTo(id doc.ID) []*doc.Binding {
	e.indexBindings()
	return slices.Clone(e.bindTo[id])
}

// Arrow computes (with memoization) the clipped route and endpoints of arrow s.
func (e *Editor) Arrow(s *doc.Shape) *ArrowInfo {
	if e.arrows == nil {
		e.arrows = map[doc.ID]*arrowMemo{}
	}
	bs, be := e.bindingOf(s.ID, "start"), e.bindingOf(s.ID, "end")
	var ts, te *doc.Shape
	if bs != nil {
		ts = e.Doc.Shapes[bs.To]
	}
	if be != nil {
		te = e.Doc.Shapes[be.To]
	}
	am := e.PageMat(s.ID)
	mats := [3]geo.Mat{am}
	if ts != nil {
		mats[1] = e.PageMat(ts.ID)
	}
	if te != nil {
		mats[2] = e.PageMat(te.ID)
	}
	if m := e.arrows[s.ID]; m != nil && m.s == s && m.starts == [2]*doc.Shape{ts, te} &&
		m.binds == [2]*doc.Binding{bs, be} && m.mats == mats {
		return m.info
	}
	info := e.computeArrow(s, bs, be, ts, te, am)
	e.arrows[s.ID] = &arrowMemo{
		s: s, starts: [2]*doc.Shape{ts, te}, binds: [2]*doc.Binding{bs, be}, mats: mats, info: info,
	}
	return info
}

func (e *Editor) anchorOf(b *doc.Binding, target *doc.Shape) geo.Vec {
	box := e.Geometry(target).Bounds
	a := geo.V(0.5, 0.5)
	if b.IsPrecise {
		a = b.Anchor
	}
	return e.PageMat(target.ID).Apply(geo.V(box.X+a.X*box.W, box.Y+a.Y*box.H))
}

func (e *Editor) outlineInSpace(t *doc.Shape, inv geo.Mat) []geo.Vec {
	g := e.Geometry(t)
	m := inv.Mul(e.PageMat(t.ID))
	out := make([]geo.Vec, len(g.Outline))
	for i, pt := range g.Outline {
		out[i] = m.Apply(pt)
	}
	return out
}

func (e *Editor) computeArrow(s *doc.Shape, bs, be *doc.Binding, ts, te *doc.Shape, am geo.Mat) *ArrowInfo {
	inv := am.Invert()
	rawEnd := s.End
	if s.Start == rawEnd && bs == nil && be == nil {
		rawEnd = geo.V(s.Start.X+120, s.Start.Y)
	}
	info := &ArrowInfo{RawStart: s.Start, RawEnd: rawEnd}
	if bs != nil && ts != nil {
		info.RawStart = inv.Apply(e.anchorOf(bs, ts))
	}
	if be != nil && te != nil {
		info.RawEnd = inv.Apply(e.anchorOf(be, te))
	}
	a, b := info.RawStart, info.RawEnd

	sw := strokeWidthOf(s)
	headOffset := func(head string) float64 {
		if head == "" || head == "none" {
			return sw*0.5 + 2
		}
		return sw*1.5 + 4
	}

	if s.Kind == "elbow" {
		return e.computeElbow(s, info, ts, te, inv, headOffset)
	}

	dist := a.Dist(b)
	if math.Abs(s.Bend) < 1 || dist < 1 {
		// Straight route.
		info.Start, info.End = a, b
		if ts != nil {
			if p, ok := lastCross(a, b, e.outlineInSpace(ts, inv)); ok {
				info.Start = p.Add(b.Sub(a).Unit().Mul(headOffset(s.HeadStart)))
			}
		}
		if te != nil {
			if p, ok := lastCross(b, a, e.outlineInSpace(te, inv)); ok {
				info.End = p.Add(a.Sub(b).Unit().Mul(headOffset(s.HeadEnd)))
			}
		}
		if b.Sub(a).Dot(info.End.Sub(info.Start)) < 0 {
			m := geo.Med(info.Start, info.End)
			info.Start, info.End = m, m
		}
		info.Mid = geo.Med(info.Start, info.End)
		info.Line = []geo.Vec{info.Start, info.End}
		return info
	}

	// Circular arc through a, bent midpoint, and b.
	u := b.Sub(a).Unit()
	mid := geo.Med(a, b).Add(u.Perp().Mul(-s.Bend))
	center, radius, ok := geo.CircleFrom3Points(a, mid, b)
	if !ok {
		info.Start, info.End, info.Mid = a, b, geo.Med(a, b)
		info.Line = []geo.Vec{a, b}
		return info
	}
	aa, am2, ab := center.Angle(a), center.Angle(mid), center.Angle(b)
	sweep := sweepThrough(aa, am2, ab)

	at := func(th float64) geo.Vec {
		sn, cs := math.Sincos(th)
		return center.Add(geo.V(radius*cs, radius*sn))
	}
	sample := func(from, turn float64) []geo.Vec {
		n := int(math.Max(10, math.Min(96, math.Abs(turn)*radius/6)))
		pts := make([]geo.Vec, n+1)
		for i := range pts {
			pts[i] = at(from + turn*float64(i)/float64(n))
		}
		return pts
	}

	startTurn, endTurn := 0.0, sweep
	if ts != nil {
		if t, ok := crossAlong(sample(aa, sweep), e.outlineInSpace(ts, inv), false); ok {
			startTurn = sweep*t + signPositive(sweep)*headOffset(s.HeadStart)/radius
		}
	}
	if te != nil {
		if t, ok := crossAlong(sample(aa, sweep), e.outlineInSpace(te, inv), true); ok {
			endTurn = sweep*t - signPositive(sweep)*headOffset(s.HeadEnd)/radius
		}
	}
	if signPositive(sweep)*(endTurn-startTurn) < 0 {
		m := (startTurn + endTurn) / 2
		startTurn, endTurn = m, m
	}
	info.Arc = true
	info.Center, info.Radius = center, radius
	info.AngleA, info.Sweep = aa+startTurn, endTurn-startTurn
	info.Start, info.End = at(info.AngleA), at(info.AngleA+info.Sweep)
	info.Mid = at(info.AngleA + info.Sweep/2)
	info.Line = sample(info.AngleA, info.Sweep)
	return info
}

func sweepThrough(a, m, b float64) float64 {
	cw := geo.NormAngle(b - a)
	if geo.NormAngle(m-a) <= cw {
		return cw
	}
	return cw - 2*math.Pi
}

func lastCross(a, b geo.Vec, outline []geo.Vec) (geo.Vec, bool) {
	cross := geo.SegmentPolyline(a, b, outline, true)
	if len(cross) == 0 {
		return geo.Vec{}, false
	}
	best, bestD := cross[0], cross[0].Dist(a)
	for _, p := range cross[1:] {
		if d := p.Dist(a); d > bestD {
			best, bestD = p, d
		}
	}
	return best, true
}

func crossAlong(line, outline []geo.Vec, atEnd bool) (float64, bool) {
	n := len(line) - 1
	if n < 1 {
		return 0, false
	}
	if atEnd {
		for i := n; i > 0; i-- {
			if c := geo.SegmentPolyline(line[i-1], line[i], outline, true); len(c) > 0 {
				best := c[0]
				for _, p := range c[1:] {
					if p.Dist(line[i-1]) < best.Dist(line[i-1]) {
						best = p
					}
				}
				f := best.Dist(line[i-1]) / math.Max(line[i-1].Dist(line[i]), 1e-9)
				return (float64(i-1) + f) / float64(n), true
			}
		}
		return 0, false
	}
	for i := 1; i <= n; i++ {
		if c := geo.SegmentPolyline(line[i-1], line[i], outline, true); len(c) > 0 {
			best := c[0]
			for _, p := range c[1:] {
				if p.Dist(line[i]) < best.Dist(line[i]) {
					best = p
				}
			}
			f := best.Dist(line[i-1]) / math.Max(line[i-1].Dist(line[i]), 1e-9)
			return (float64(i-1) + f) / float64(n), true
		}
	}
	return 0, false
}

func (e *Editor) computeElbow(s *doc.Shape, info *ArrowInfo, ts, te *doc.Shape, inv geo.Mat, headOffset func(string) float64) *ArrowInfo {
	a, b := info.RawStart, info.RawEnd
	var pts []geo.Vec
	if len(s.Points) > 0 {
		// Custom elbow waypoints inserted via elbow midpoint handles.
		pts = make([]geo.Vec, 0, len(s.Points)+4)
		pts = append(pts, a)
		horiz := math.Abs(b.X-a.X) >= math.Abs(b.Y-a.Y)
		cur := a
		for i, wp := range s.Points {
			p := geo.V(wp.X, wp.Y)
			if i == 0 {
				if horiz {
					pts = append(pts, geo.V(p.X, cur.Y))
				} else {
					pts = append(pts, geo.V(cur.X, p.Y))
				}
			} else {
				if horiz {
					pts = append(pts, geo.V(p.X, cur.Y))
				} else {
					pts = append(pts, geo.V(cur.X, p.Y))
				}
			}
			pts = append(pts, p)
			cur = p
			horiz = !horiz
		}
		if horiz {
			pts = append(pts, geo.V(b.X, cur.Y))
		} else {
			pts = append(pts, geo.V(cur.X, b.Y))
		}
		pts = append(pts, b)
	} else {
		mid := s.ElbowMidPoint
		if mid == 0 {
			mid = 0.5
		}
		d := b.Sub(a)
		if math.Abs(d.X) >= math.Abs(d.Y) {
			x := a.X + d.X*mid
			pts = []geo.Vec{a, geo.V(x, a.Y), geo.V(x, b.Y), b}
		} else {
			y := a.Y + d.Y*mid
			pts = []geo.Vec{a, geo.V(a.X, y), geo.V(b.X, y), b}
		}
	}

	if ts != nil && len(pts) >= 2 {
		if p, ok := lastCross(pts[0], pts[1], e.outlineInSpace(ts, inv)); ok {
			pts[0] = p.Add(pts[1].Sub(pts[0]).Unit().Mul(headOffset(s.HeadStart)))
		}
	}
	if te != nil && len(pts) >= 2 {
		last := len(pts) - 1
		if p, ok := lastCross(pts[last], pts[last-1], e.outlineInSpace(te, inv)); ok {
			pts[last] = p.Add(pts[last-1].Sub(pts[last]).Unit().Mul(headOffset(s.HeadEnd)))
		}
	}
	info.Elbow, info.Line = pts, pts
	info.Start, info.End = pts[0], pts[len(pts)-1]
	midIdx := len(pts) / 2
	info.Mid = geo.Med(pts[midIdx-1], pts[midIdx])
	return info
}

func (i *ArrowInfo) at(t float64) geo.Vec {
	if i.Arc {
		sn, cs := math.Sincos(i.AngleA + i.Sweep*t)
		return i.Center.Add(geo.V(i.Radius*cs, i.Radius*sn))
	}
	if len(i.Line) < 2 {
		return i.Start
	}
	total := geo.PolyLength(i.Line)
	d := total * geo.Clamp(t, 0, 1)
	for k := 1; k < len(i.Line); k++ {
		l := i.Line[k-1].Dist(i.Line[k])
		if d <= l && l > 0 {
			return i.Line[k-1].Lerp(i.Line[k], d/l)
		}
		d -= l
	}
	return i.Line[len(i.Line)-1]
}

func (e *Editor) arrowLabelBox(s *doc.Shape) *geo.Box {
	info := e.Arrow(s)
	text := s.Text
	if text == "" {
		text = " "
	}
	size := labelFontSize(s)
	b := e.measure(text, s.Font, size, 0, measureWeight(s), s.Italic)
	if b.W > 280 {
		b = e.measure(text, s.Font, size, 280, measureWeight(s), s.Italic)
	}
	pos := s.LabelPosition
	if pos <= 0 || pos >= 1 {
		pos = 0.5
	}
	c := info.at(pos)
	const pad = 4.25
	return &geo.Box{
		X: c.X - b.W/2 - pad,
		Y: c.Y - b.H/2 - pad,
		W: b.W + 2*pad,
		H: b.H + 2*pad,
	}
}

func (e *Editor) arrowGeometry(s *doc.Shape) *Geometry {
	info := e.Arrow(s)
	pts := info.Line
	box := geo.BoxOf(pts)
	g := &Geometry{Bounds: box, Outline: pts}
	if s.Text != "" {
		if lb := e.arrowLabelBox(s); lb != nil {
			g.Bounds = box.Union(*lb)
			g.Label = lb
		}
	}
	return g
}

func (e *Editor) arrowPrims(s *doc.Shape, pal *Palette) []Prim {
	info := e.Arrow(s)
	if len(info.Line) < 2 || info.Start.Dist(info.End) < 0.5 {
		return nil
	}
	c := ResolveColor(s.Color, pal.Dark, e.Look())
	w := strokeWidthOf(s)
	body := (&geo.Path{}).Poly(info.Line, false)
	out := outlinePrims(e.Look(), s.Dash, body, info.Line, false, w, c, string(s.ID))
	out = append(out, arrowheadPrims(s.HeadEnd, info.Line, false, w, c)...)
	out = append(out, arrowheadPrims(s.HeadStart, info.Line, true, w, c)...)

	if s.Text != "" {
		if lb := e.arrowLabelBox(s); lb != nil {
			cs := lb.Corners()
			out = append(out, fillWith(geo.RoundedPoly(cs[:], 4), pal.Background))
			inner := geo.Box{X: lb.X + 4.25, Y: lb.Y + 4.25, W: math.Max(lb.W-8.5, 8), H: math.Max(lb.H-8.5, 8)}
			out = append(out, e.labelPrim(s, &inner, labelFontSize(s), c))
		}
	}
	return out
}

func arrowheadPrims(kind string, line []geo.Vec, atStart bool, w float64, c Color) []Prim {
	if kind == "" || kind == "none" || len(line) < 2 {
		return nil
	}
	tip := line[len(line)-1]
	back := line[len(line)-2]
	if atStart {
		tip = line[0]
		back = line[1]
	}
	reach := 0.0
	for i := 1; i < len(line) && reach < w*4; i++ {
		var a, b geo.Vec
		if atStart {
			a, b = line[i-1], line[i]
		} else {
			a, b = line[len(line)-i], line[len(line)-1-i]
		}
		reach += a.Dist(b)
		back = b
	}
	total := geo.PolyLength(line)
	u := tip.Sub(back).Unit()
	if u.LenSq() == 0 {
		return nil
	}
	size := math.Min(w*3+6, math.Max(total/3, w+4))
	sideAt := func(ang, d float64) geo.Vec {
		return tip.Add(u.Rot(math.Pi + ang).Mul(d))
	}
	switch kind {
	case "triangle":
		a, b := sideAt(0.52, size), sideAt(-0.52, size)
		path := (&geo.Path{}).Poly([]geo.Vec{tip, a, b}, true)
		return []Prim{fillWith(path, c), strokeWith(path, w*0.6, c)}
	case "diamond":
		a, b := sideAt(0.6, size*0.6), sideAt(-0.6, size*0.6)
		far := tip.Sub(u.Mul(size))
		path := (&geo.Path{}).Poly([]geo.Vec{tip, a, far, b}, true)
		return []Prim{fillWith(path, c), strokeWith(path, w*0.6, c)}
	case "dot":
		r := math.Max(w*1.3, size*0.3)
		cen := tip.Sub(u.Mul(r * 0.5))
		return []Prim{fillWith(geo.EllipsePath(geo.Box{X: cen.X - r, Y: cen.Y - r, W: 2 * r, H: 2 * r}), c)}
	case "square":
		r := math.Max(w*1.3, size*0.3)
		cen := tip.Sub(u.Mul(r * 0.5))
		n := u.Perp().Mul(r)
		t := u.Mul(r)
		path := (&geo.Path{}).Poly([]geo.Vec{
			cen.Add(t).Add(n), cen.Add(t).Sub(n), cen.Sub(t).Sub(n), cen.Sub(t).Add(n),
		}, true)
		return []Prim{fillWith(path, c)}
	case "inverted":
		a, b := tip.Add(u.Perp().Mul(size*0.55)), tip.Sub(u.Perp().Mul(size*0.55))
		far := tip.Sub(u.Mul(size))
		path := (&geo.Path{}).Poly([]geo.Vec{far, a, b}, true)
		return []Prim{fillWith(path, c), strokeWith(path, w*0.6, c)}
	case "bar":
		n := u.Perp().Mul(size * 0.55)
		path := (&geo.Path{}).Move(tip.Add(n)).Line(tip.Sub(n))
		return []Prim{strokeWith(path, w, c)}
	default: // "arrow"
		path := (&geo.Path{}).Move(sideAt(0.5, size)).Line(tip).Line(sideAt(-0.5, size))
		return []Prim{strokeWith(path, w, c)}
	}
}

func bendFromPoint(a, b, p geo.Vec) float64 {
	u := b.Sub(a).Unit()
	return p.Sub(geo.Med(a, b)).Dot(u.Perp().Mul(-1))
}

// --- line shapes (straight / Catmull-Rom cubic spline + terminal bindings) ---

func (e *Editor) linePoints(s *doc.Shape) []geo.Vec {
	pts := make([]geo.Vec, len(s.Points))
	for i, lp := range s.Points {
		pts[i] = geo.V(lp.X, lp.Y)
	}
	if len(pts) == 0 {
		return nil
	}
	inv := e.PageMat(s.ID).Invert()
	if bs := e.bindingOf(s.ID, "start"); bs != nil {
		if ts := e.Doc.Shapes[bs.To]; ts != nil {
			raw := inv.Apply(e.anchorOf(bs, ts))
			pts[0] = raw
			if len(pts) >= 2 {
				if p, ok := lastCross(raw, pts[1], e.outlineInSpace(ts, inv)); ok {
					pts[0] = p
				}
			}
		}
	}
	if be := e.bindingOf(s.ID, "end"); be != nil {
		if ts := e.Doc.Shapes[be.To]; ts != nil {
			raw := inv.Apply(e.anchorOf(be, ts))
			last := len(pts) - 1
			pts[last] = raw
			if len(pts) >= 2 {
				if p, ok := lastCross(raw, pts[last-1], e.outlineInSpace(ts, inv)); ok {
					pts[last] = p
				}
			}
		}
	}
	return pts
}

func (e *Editor) linePath(s *doc.Shape) (*geo.Path, []geo.Vec) {
	pts := e.linePoints(s)
	p := &geo.Path{}
	if len(pts) < 2 {
		return p, pts
	}
	if s.Spline == "cubic" && len(pts) > 2 {
		p.Move(pts[0])
		for i := 0; i < len(pts)-1; i++ {
			p0 := pts[max(0, i-1)]
			p1 := pts[i]
			p2 := pts[i+1]
			p3 := pts[min(len(pts)-1, i+2)]
			c1 := p1.Add(p2.Sub(p0).Div(6))
			c2 := p2.Sub(p3.Sub(p1).Div(6))
			p.Cube(c1, c2, p2)
		}
		flat, _ := p.Points(16)
		return p, flat
	}
	p.Poly(pts, false)
	return p, pts
}

func (e *Editor) lineGeometry(s *doc.Shape) *Geometry {
	_, flat := e.linePath(s)
	if len(flat) == 0 {
		return &Geometry{Bounds: geo.Box{}}
	}
	return &Geometry{
		Bounds:  geo.BoxOf(flat),
		Outline: flat,
		Closed:  false,
	}
}

func (e *Editor) linePrims(s *doc.Shape, pal *Palette) []Prim {
	path, flat := e.linePath(s)
	if len(flat) < 2 {
		return nil
	}
	c := ResolveColor(s.Color, pal.Dark, e.Look())
	return outlinePrims(e.Look(), s.Dash, path, flat, false, strokeWidthOf(s), c, string(s.ID))
}

// --- binding helpers ---

func (e *Editor) bindTargetAt(p geo.Vec, skip doc.ID) doc.ID {
	var walk func(parent doc.ID) doc.ID
	walk = func(parent doc.ID) doc.ID {
		kids := e.Children(parent)
		for i := len(kids) - 1; i >= 0; i-- {
			id := kids[i]
			if id == skip {
				continue
			}
			s := e.Doc.Shapes[id]
			if s == nil || s.Locked || s.Type == doc.TypeHighlight || s.Type == doc.TypeArrow || s.Type == doc.TypeLine {
				continue
			}
			if s.Type == doc.TypeGroup {
				if k := walk(id); k != "" {
					return k
				}
				continue
			}
			q := e.PageMat(id).Invert().Apply(p)
			g := e.Geometry(s)
			if s.Type == doc.TypeFrame {
				if !g.Bounds.Contains(q) {
					continue
				}
				if k := walk(id); k != "" {
					return k
				}
				return id
			}
			if s.Type == doc.TypeDraw {
				if !g.Closed && geo.DistToPolyline(q, g.Outline, false) > e.margin() {
					continue
				}
				if g.Closed && !geo.PointInPolygon(q, g.Outline) && geo.DistToPolyline(q, g.Outline, true) > e.margin() {
					continue
				}
				return id
			}
			if g.Bounds.Expand(e.margin()).Contains(q) && (geo.PointInPolygon(q, g.Outline) || geo.DistToPolyline(q, g.Outline, true) <= e.margin()) {
				return id
			}
		}
		return ""
	}
	return walk(e.Page)
}

func (e *Editor) bindTerminal(shapeID doc.ID, term string, target doc.ID, p geo.Vec, exact bool) {
	t := e.Doc.Shapes[target]
	if t == nil {
		return
	}
	box := e.Geometry(t).Bounds
	q := e.PageMat(target).Invert().Apply(p)
	a := geo.V(
		geo.Clamp((q.X-box.X)/math.Max(box.W, 1e-6), 0, 1),
		geo.Clamp((q.Y-box.Y)/math.Max(box.H, 1e-6), 0, 1),
	)
	precise := exact || math.Abs(a.X-0.5) > 0.25 || math.Abs(a.Y-0.5) > 0.25
	b := e.bindingOf(shapeID, term)
	nb := &doc.Binding{
		ID:        doc.NewRecordID("binding"),
		From:      shapeID,
		To:        target,
		Terminal:  term,
		Anchor:    a,
		IsExact:   exact,
		IsPrecise: precise,
	}
	if b != nil {
		nb.ID = b.ID
	}
	e.putBinding(nb)
}

func (e *Editor) unbind(shapeID doc.ID, term string) {
	s := e.Doc.Shapes[shapeID]
	b := e.bindingOf(shapeID, term)
	if s == nil || b == nil {
		return
	}
	switch s.Type {
	case doc.TypeArrow:
		info := e.Arrow(s)
		e.update(shapeID, func(c *doc.Shape) {
			if term == "start" {
				c.Start = info.Start
			} else {
				c.End = info.End
			}
		})
	case doc.TypeLine:
		pts := e.linePoints(s)
		e.update(shapeID, func(c *doc.Shape) {
			if len(c.Points) == 0 || len(pts) != len(c.Points) {
				return
			}
			cloned := slices.Clone(c.Points)
			if term == "start" {
				cloned[0].X, cloned[0].Y = pts[0].X, pts[0].Y
			} else {
				last := len(cloned) - 1
				cloned[last].X, cloned[last].Y = pts[last].X, pts[last].Y
			}
			c.Points = cloned
		})
	}
	e.removeBinding(b.ID)
}

// --- arrow & line gestures ---

type arrowCreateGesture struct {
	id   doc.ID
	drag *arrowEndDragGesture
}

func newArrowCreateGesture(e *Editor) *arrowCreateGesture {
	e.begin()
	id := doc.NewRecordID("shape")
	s := doc.NewShape(id, doc.TypeArrow)
	e.applyNextStyle(s)
	p := e.snapPoint(e.pointer.DownPage)
	s.Parent, s.Index = e.Page, e.topIndex(e.Page)
	s.X, s.Y = p.X, p.Y
	s.Start, s.End = geo.Vec{}, geo.V(0.1, 0.1)
	s.Seed = geo.Hash(string(id))
	e.put(s)
	g := &arrowCreateGesture{id: id}
	if !(e.pointer.Mods.Cmd || e.pointer.Mods.Ctrl) {
		if target := e.bindTargetAt(p, id); target != "" {
			e.bindTerminal(id, "start", target, p, e.pointer.Mods.Alt)
		}
	}
	g.drag = &arrowEndDragGesture{id: id, term: "end"}
	return g
}

func (g *arrowCreateGesture) move(e *Editor) { g.drag.move(e) }

func (g *arrowCreateGesture) up(e *Editor) {
	e.Hint = nil
	if !e.dragged() {
		e.update(g.id, func(c *doc.Shape) {
			c.End = geo.V(120, 0)
		})
	}
	e.Select(g.id)
	e.commit()
	e.finishTool()
}

func (g *arrowCreateGesture) cancel(e *Editor) {
	e.Hint = nil
	e.rollback()
}

type arrowEndDragGesture struct {
	id   doc.ID
	term string
}

func newArrowEndDragGesture(e *Editor, term string) *arrowEndDragGesture {
	e.begin()
	return &arrowEndDragGesture{id: e.sel[0], term: term}
}

func (g *arrowEndDragGesture) move(e *Editor) {
	p := e.snapPoint(e.pointer.Page)
	s := e.Doc.Shapes[g.id]
	if s == nil {
		return
	}
	var target doc.ID
	if !(e.pointer.Mods.Cmd || e.pointer.Mods.Ctrl) {
		target = e.bindTargetAt(p, g.id)
	}
	e.Hint = nil
	if target != "" {
		e.Hint = map[doc.ID]bool{target: true}
		e.bindTerminal(g.id, g.term, target, p, e.pointer.Mods.Alt)
		return
	}
	if b := e.bindingOf(g.id, g.term); b != nil {
		e.removeBinding(b.ID)
	}
	local := e.PageMat(g.id).Invert().Apply(p)
	e.update(g.id, func(c *doc.Shape) {
		if g.term == "start" {
			c.Start = local
		} else {
			c.End = local
		}
	})
}

func (g *arrowEndDragGesture) up(e *Editor) {
	e.Hint = nil
	e.commit()
}

func (g *arrowEndDragGesture) cancel(e *Editor) {
	e.Hint = nil
	e.rollback()
}

type arrowBendDragGesture struct {
	id       doc.ID
	elbowIdx int
	virtual  bool
}

func newArrowBendDragGesture(e *Editor, h Handle) *arrowBendDragGesture {
	e.begin()
	id := e.sel[0]
	idx := h.Index
	if h.Virtual {
		// Insert a custom elbow waypoint when dragging an elbow leg's virtual midpoint.
		s := e.Doc.Shapes[id]
		if s != nil && s.Kind == "elbow" {
			info := e.Arrow(s)
			e.update(id, func(c *doc.Shape) {
				if len(c.Points) == 0 && len(info.Elbow) == 4 {
					mid := geo.Med(info.Elbow[1], info.Elbow[2])
					c.Points = []doc.LinePoint{{ID: string(doc.NewRecordID("p")), Index: doc.FirstIndex, X: mid.X, Y: mid.Y}}
				}
				pt := h.Point
				local := e.PageMat(id).Invert().Apply(pt)
				ins := min(max(idx, 0), len(c.Points))
				lp := doc.LinePoint{ID: string(doc.NewRecordID("p")), Index: doc.FirstIndex, X: local.X, Y: local.Y}
				c.Points = slices.Insert(slices.Clone(c.Points), ins, lp)
				idx = ins
			})
		}
	}
	return &arrowBendDragGesture{id: id, elbowIdx: idx, virtual: h.Virtual}
}

func (g *arrowBendDragGesture) move(e *Editor) {
	s := e.Doc.Shapes[g.id]
	if s == nil {
		return
	}
	q := e.PageMat(g.id).Invert().Apply(e.pointer.Page)
	if s.Kind == "elbow" {
		if len(s.Points) > 0 && g.elbowIdx >= 0 && g.elbowIdx < len(s.Points) {
			e.update(g.id, func(c *doc.Shape) {
				pts := slices.Clone(c.Points)
				pts[g.elbowIdx].X, pts[g.elbowIdx].Y = q.X, q.Y
				c.Points = pts
			})
			return
		}
		info := e.Arrow(s)
		d := info.RawEnd.Sub(info.RawStart)
		mid := 0.5
		if math.Abs(d.X) >= math.Abs(d.Y) && math.Abs(d.X) > 1e-6 {
			mid = (q.X - info.RawStart.X) / d.X
		} else if math.Abs(d.Y) > 1e-6 {
			mid = (q.Y - info.RawStart.Y) / d.Y
		}
		if math.Abs(mid-0.5) < 0.03 {
			mid = 0.5
		}
		e.update(g.id, func(c *doc.Shape) {
			c.ElbowMidPoint = geo.Clamp(mid, -1.0, 2.0)
		})
		return
	}
	info := e.Arrow(s)
	bend := bendFromPoint(info.RawStart, info.RawEnd, q)
	if math.Abs(bend) < 8/e.Cam().Z {
		bend = 0
	}
	e.update(g.id, func(c *doc.Shape) {
		c.Bend = bend
	})
}

func (g *arrowBendDragGesture) up(e *Editor)     { e.commit() }
func (g *arrowBendDragGesture) cancel(e *Editor) { e.rollback() }

type lineCreateGesture struct {
	id doc.ID
}

func newLineCreateGesture(e *Editor) *lineCreateGesture {
	e.begin()
	id := doc.NewRecordID("shape")
	s := doc.NewShape(id, doc.TypeLine)
	e.applyNextStyle(s)
	p := e.snapPoint(e.pointer.DownPage)
	s.Parent, s.Index = e.Page, e.topIndex(e.Page)
	s.X, s.Y = p.X, p.Y
	s.Seed = geo.Hash(string(id))
	s.Points = []doc.LinePoint{
		{ID: string(doc.NewRecordID("p")), Index: doc.FirstIndex, X: 0, Y: 0},
		{ID: string(doc.NewRecordID("p")), Index: doc.IndexBetween(doc.FirstIndex, ""), X: 0.1, Y: 0.1},
	}
	e.put(s)
	if !(e.pointer.Mods.Cmd || e.pointer.Mods.Ctrl) {
		if target := e.bindTargetAt(p, id); target != "" {
			e.bindTerminal(id, "start", target, p, e.pointer.Mods.Alt)
		}
	}
	return &lineCreateGesture{id: id}
}

func (g *lineCreateGesture) move(e *Editor) {
	s := e.Doc.Shapes[g.id]
	if s == nil {
		return
	}
	p := e.snapPoint(e.pointer.Page)
	q := e.PageMat(g.id).Invert().Apply(p)
	if e.pointer.Mods.Shift {
		a := math.Round(math.Atan2(q.Y, q.X)/(math.Pi/4)) * (math.Pi / 4)
		q = geo.V(q.Len(), 0).Rot(a)
		p = e.PageMat(g.id).Apply(q)
	}
	var target doc.ID
	if !(e.pointer.Mods.Cmd || e.pointer.Mods.Ctrl) {
		target = e.bindTargetAt(p, g.id)
	}
	e.Hint = nil
	if target != "" {
		e.Hint = map[doc.ID]bool{target: true}
		e.bindTerminal(g.id, "end", target, p, e.pointer.Mods.Alt)
	} else if b := e.bindingOf(g.id, "end"); b != nil {
		e.removeBinding(b.ID)
	}
	e.update(g.id, func(c *doc.Shape) {
		pts := slices.Clone(c.Points)
		pts[1].X, pts[1].Y = q.X, q.Y
		c.Points = pts
	})
}

func (g *lineCreateGesture) up(e *Editor) {
	e.Hint = nil
	if !e.dragged() {
		e.update(g.id, func(c *doc.Shape) {
			pts := slices.Clone(c.Points)
			pts[1].X, pts[1].Y = 100, 0
			c.Points = pts
		})
	}
	e.Select(g.id)
	e.commit()
	e.finishTool()
}

func (g *lineCreateGesture) cancel(e *Editor) {
	e.Hint = nil
	e.rollback()
}

type linePointDragGesture struct {
	id    doc.ID
	index int
}

func newLinePointDragGesture(e *Editor, h Handle) *linePointDragGesture {
	e.begin()
	id := e.sel[0]
	idx := h.Index
	if h.Virtual {
		e.update(id, func(c *doc.Shape) {
			pts := slices.Clone(c.Points)
			a, b := pts[idx-1], pts[idx]
			n := doc.LinePoint{
				ID:    string(doc.NewRecordID("p")),
				Index: doc.IndexBetween(a.Index, b.Index),
				X:     (a.X + b.X) / 2,
				Y:     (a.Y + b.Y) / 2,
			}
			c.Points = slices.Insert(pts, idx, n)
		})
	}
	return &linePointDragGesture{id: id, index: idx}
}

func (g *linePointDragGesture) move(e *Editor) {
	s := e.Doc.Shapes[g.id]
	if s == nil {
		return
	}
	p := e.snapPoint(e.pointer.Page)
	q := e.PageMat(g.id).Invert().Apply(p)
	term := ""
	if g.index == 0 {
		term = "start"
	} else if g.index == len(s.Points)-1 {
		term = "end"
	}
	e.Hint = nil
	if term != "" {
		var target doc.ID
		if !(e.pointer.Mods.Cmd || e.pointer.Mods.Ctrl) {
			target = e.bindTargetAt(p, g.id)
		}
		if target != "" {
			e.Hint = map[doc.ID]bool{target: true}
			e.bindTerminal(g.id, term, target, p, e.pointer.Mods.Alt)
		} else if b := e.bindingOf(g.id, term); b != nil {
			e.removeBinding(b.ID)
		}
	}
	e.update(g.id, func(c *doc.Shape) {
		pts := slices.Clone(c.Points)
		if g.index >= 0 && g.index < len(pts) {
			pts[g.index].X, pts[g.index].Y = q.X, q.Y
			c.Points = pts
		}
	})
}

func (g *linePointDragGesture) up(e *Editor) {
	e.Hint = nil
	e.commit()
}

func (g *linePointDragGesture) cancel(e *Editor) {
	e.Hint = nil
	e.rollback()
}

// --- polygon / curve vertex editing ---

// EditVertices enters or exits vertex-editing mode for polygon or curve geo shape id.
func (e *Editor) EditVertices(id doc.ID) bool {
	s := e.Doc.Shapes[id]
	if s == nil || s.Locked || s.Type != doc.TypeGeo {
		return false
	}
	kind := normalizeGeoKind(s.Geo)
	if kind != "polygon" && kind != "curve" {
		return false
	}
	e.SetTool(ToolSelect)
	e.Select(id)
	e.EditingVertices = id
	return true
}

func materializeGeoPoints(s *doc.Shape) []doc.LinePoint {
	if len(s.Points) >= 2 {
		return slices.Clone(s.Points)
	}
	kind := normalizeGeoKind(s.Geo)
	var pts []geo.Vec
	if kind == "curve" {
		pts = []geo.Vec{{X: 0, Y: 1}, {X: 0.33, Y: 0.2}, {X: 0.66, Y: 0.8}, {X: 1, Y: 0}}
	} else {
		pts, _ = geoOutline(s, "polygon", 1, 1)
	}
	out := make([]doc.LinePoint, len(pts))
	prev := ""
	for i, pt := range pts {
		idx := doc.IndexBetween(prev, "")
		prev = idx
		out[i] = doc.LinePoint{ID: string(doc.NewRecordID("p")), Index: idx, X: pt.X, Y: pt.Y}
	}
	return out
}

type geoVertexDragGesture struct {
	id    doc.ID
	index int
}

func newGeoVertexDragGesture(e *Editor, h Handle) *geoVertexDragGesture {
	e.begin()
	id := e.sel[0]
	idx := h.Index
	e.update(id, func(c *doc.Shape) {
		pts := materializeGeoPoints(c)
		if h.Virtual && len(pts) >= 2 {
			a := pts[idx-1]
			b := pts[idx%len(pts)]
			n := doc.LinePoint{
				ID:    string(doc.NewRecordID("p")),
				Index: doc.IndexBetween(a.Index, b.Index),
				X:     (a.X + b.X) / 2,
				Y:     (a.Y + b.Y) / 2,
			}
			pts = slices.Insert(pts, idx, n)
		}
		c.Points = pts
	})
	return &geoVertexDragGesture{id: id, index: idx}
}

func (g *geoVertexDragGesture) move(e *Editor) {
	s := e.Doc.Shapes[g.id]
	if s == nil {
		return
	}
	q := e.PageMat(g.id).Invert().Apply(e.snapPoint(e.pointer.Page))
	e.update(g.id, func(c *doc.Shape) {
		pts := materializeGeoPoints(c)
		if g.index < 0 || g.index >= len(pts) {
			return
		}
		w, h := math.Max(c.W, 1), math.Max(c.H, 1)
		// Denormalize all vertices to local coordinates, update dragged vertex, then re-normalize tight box.
		localPts := make([]geo.Vec, len(pts))
		for i, lp := range pts {
			lx, ly := lp.X*w, lp.Y*h
			if c.FlipX {
				lx = w - lx
			}
			if c.FlipY {
				ly = h - ly
			}
			localPts[i] = geo.V(lx, ly)
		}
		localPts[g.index] = q
		box := geo.BoxOf(localPts)
		nw, nh := math.Max(box.W, 8), math.Max(box.H, 8)
		deltaPage := geo.Rotate(e.PageMat(g.id).Rotation()).ApplyDir(geo.V(box.X, box.Y))
		inParent := e.parentMat(c.Parent).Invert().ApplyDir(deltaPage)
		c.X += inParent.X
		c.Y += inParent.Y
		c.W, c.H = nw, nh
		c.FlipX, c.FlipY = false, false
		for i, p := range localPts {
			pts[i].X = (p.X - box.X) / nw
			pts[i].Y = (p.Y - box.Y) / nh
		}
		c.Points = pts
	})
}

func (g *geoVertexDragGesture) up(e *Editor)     { e.commit() }
func (g *geoVertexDragGesture) cancel(e *Editor) { e.rollback() }
