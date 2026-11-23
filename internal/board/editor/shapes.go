package editor

import (
	"math"
	"slices"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

// GeoKinds lists all 27 geometry shapes (merging Tern's 14 and godraw's 20).
var GeoKinds = []string{
	"rectangle",
	"rounded-rect",
	"ellipse",
	"diamond",
	"triangle",
	"pentagon",
	"hexagon",
	"octagon",
	"parallelogram",
	"parallelogram-2",
	"trapezoid",
	"star",
	"cylinder",
	"pill",
	"bracket",
	"brace",
	"arrow-right",
	"arrow-left",
	"arrow-up",
	"arrow-down",
	"x-box",
	"check-box",
	"cloud",
	"heart",
	"callout",
	"polygon",
	"curve",
}

// IsValidGeoKind reports whether kind is one of the 27 supported geometry shapes.
func IsValidGeoKind(kind string) bool {
	return slices.Contains(GeoKinds, normalizeGeoKind(kind))
}

func normalizeGeoKind(kind string) string {
	switch kind {
	case "rect":
		return "rectangle"
	case "oval":
		return "pill"
	case "rhombus":
		return "parallelogram"
	case "rhombus-2":
		return "parallelogram-2"
	}
	return kind
}

// Geometry describes a shape's bounds and hit-test outline in its local space.
type Geometry struct {
	Bounds  geo.Box
	Outline []geo.Vec
	Closed  bool
	Filled  bool
	Label   *geo.Box
}

type shapeCache struct {
	geom    *Geometry
	display [2][3][]Prim
	ok      [2][3]bool
}

func cacheOf(s *doc.Shape) *shapeCache {
	if c, ok := s.Cache.(*shapeCache); ok {
		return c
	}
	c := &shapeCache{}
	s.Cache = c
	return c
}

// Geometry returns the cached local-space geometry for s.
func (e *Editor) Geometry(s *doc.Shape) *Geometry {
	if s.Type == doc.TypeGroup {
		return e.groupGeometry(s)
	}
	c := cacheOf(s)
	if c.geom == nil {
		c.geom = e.computeGeometry(s)
	}
	return c.geom
}

func (e *Editor) groupGeometry(s *doc.Shape) *Geometry {
	inv := e.PageMat(s.ID).Invert()
	var box geo.Box
	first := true
	for _, id := range e.Children(s.ID) {
		k := e.Doc.Shapes[id]
		if k == nil {
			continue
		}
		b := inv.Mul(e.PageMat(id)).BoundsOf(e.Geometry(k).Bounds)
		if first {
			box, first = b, false
		} else {
			box = box.Union(b)
		}
	}
	cs := box.Corners()
	return &Geometry{Bounds: box, Outline: cs[:], Closed: true}
}

func (e *Editor) computeGeometry(s *doc.Shape) *Geometry {
	switch s.Type {
	case doc.TypeGeo:
		w, h := math.Max(s.W, 1), math.Max(s.H+s.GrowY, 1)
		kind := normalizeGeoKind(s.Geo)
		pts, closed := geoOutline(s, kind, w, h)
		open := kind == "bracket" || kind == "brace" || kind == "curve"
		filled := !open && s.Fill != "none" && s.Fill != ""
		g := &Geometry{
			Bounds:  geo.Box{W: w, H: h},
			Outline: pts,
			Closed:  closed,
			Filled:  filled,
		}
		if s.Text != "" {
			g.Label = e.labelBox(s, w, h, labelFontSize(s))
		}
		return g

	case doc.TypeDraw, doc.TypeHighlight:
		pts := strokePoints(s)
		if len(pts) == 0 {
			return &Geometry{Bounds: geo.Box{}}
		}
		if len(pts) == 1 {
			r := math.Max(strokeWidthOf(s), 2)
			pts = []geo.Vec{pts[0], pts[0].Add(geo.V(r, 0))}
		}
		box := geo.BoxOf(pts)
		filled := s.Type == doc.TypeDraw && s.IsClosed && s.Fill != "none" && s.Fill != ""
		return &Geometry{
			Bounds:  box,
			Outline: geo.SimplifyRDP(pts, 0.5),
			Closed:  s.IsClosed,
			Filled:  filled,
		}

	case doc.TypeText:
		size := textFontSize(s)
		text := s.Text
		if text == "" {
			text = " "
		}
		var b TextBlock
		if s.AutoSize || s.W <= 0 {
			b = e.measure(text, s.Font, size, 0)
		} else {
			b = e.measure(text, s.Font, size, s.W)
			b.W = s.W
		}
		box := geo.Box{W: math.Max(b.W, 12), H: math.Max(b.H, size*1.3)}
		cs := box.Corners()
		return &Geometry{Bounds: box, Outline: cs[:], Closed: true, Filled: true, Label: &box}

	case doc.TypeNote:
		w, h := math.Max(s.W, 180), math.Max(s.H+s.GrowY, 140)
		box := geo.Box{W: w, H: h}
		cs := box.Corners()
		g := &Geometry{Bounds: box, Outline: cs[:], Closed: true, Filled: true}
		g.Label = e.labelBox(s, w, h, labelFontSize(s))
		return g

	case doc.TypeFrame:
		w, h := math.Max(s.W, 1), math.Max(s.H, 1)
		box := geo.Box{W: w, H: h}
		cs := box.Corners()
		return &Geometry{Bounds: box, Outline: cs[:], Closed: true}

	case doc.TypeImage:
		w, h := math.Max(s.W, 1), math.Max(s.H, 1)
		box := geo.Box{W: w, H: h}
		cs := box.Corners()
		return &Geometry{Bounds: box, Outline: cs[:], Closed: true, Filled: true}
	}

	w, h := math.Max(s.W, 40), math.Max(s.H, 40)
	box := geo.Box{W: w, H: h}
	cs := box.Corners()
	return &Geometry{Bounds: box, Outline: cs[:], Closed: true, Filled: true}
}

func (e *Editor) measure(text, font string, size, width float64) TextBlock {
	if e.Measure != nil {
		return e.Measure(text, font, size, width)
	}
	return fallbackMeasure(text, size, width)
}

func fallbackMeasure(text string, size, width float64) TextBlock {
	lineH := size * 1.3
	cw := size * 0.56
	if text == "" {
		return TextBlock{Lines: []TextLine{{Text: "", W: 0}}, W: 0, H: lineH, Line: lineH}
	}
	var lines []TextLine
	maxW := 0.0
	for _, para := range splitLines(text) {
		if width <= 0 {
			w := float64(len([]rune(para))) * cw
			lines = append(lines, TextLine{Text: para, Y: float64(len(lines)) * lineH, W: w})
			if w > maxW {
				maxW = w
			}
			continue
		}
		var cur []rune
		curW := 0.0
		lastSpace := -1
		for _, r := range para {
			rw := cw
			if r > 0x7f {
				rw = size
			}
			if r == ' ' {
				lastSpace = len(cur)
			}
			if curW+rw > width && len(cur) > 0 {
				if lastSpace > 0 {
					lineStr := string(cur[:lastSpace])
					lw := measureLineFallback(cur[:lastSpace], size, cw)
					lines = append(lines, TextLine{Text: lineStr, Y: float64(len(lines)) * lineH, W: lw})
					if lw > maxW {
						maxW = lw
					}
					rest := append([]rune(nil), cur[lastSpace+1:]...)
					cur = rest
					curW = measureLineFallback(cur, size, cw)
					lastSpace = -1
				} else {
					lines = append(lines, TextLine{Text: string(cur), Y: float64(len(lines)) * lineH, W: curW})
					if curW > maxW {
						maxW = curW
					}
					cur = cur[:0]
					curW = 0
					lastSpace = -1
				}
			}
			cur = append(cur, r)
			curW += rw
		}
		lines = append(lines, TextLine{Text: string(cur), Y: float64(len(lines)) * lineH, W: curW})
		if curW > maxW {
			maxW = curW
		}
	}
	return TextBlock{Lines: lines, W: maxW, H: float64(len(lines)) * lineH, Line: lineH}
}

func measureLineFallback(runes []rune, size, cw float64) float64 {
	w := 0.0
	for _, r := range runes {
		if r > 0x7f {
			w += size
		} else {
			w += cw
		}
	}
	return w
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func (e *Editor) labelBox(s *doc.Shape, w, h, size float64) *geo.Box {
	const pad = 10.0
	text := s.Text
	if text == "" {
		text = " "
	}
	availW := math.Max(w-2*pad, 8)
	b := e.measure(text, s.Font, size, availW)
	box := geo.Box{X: pad, W: availW, H: b.H}
	switch s.VAlign {
	case "top", "start":
		box.Y = pad
	case "bottom", "end":
		box.Y = h - pad - b.H
	default:
		box.Y = (h - b.H) / 2
	}
	return &box
}

func strokePoints(s *doc.Shape) []geo.Vec {
	n := 0
	for _, seg := range s.Segments {
		n += len(seg.Points)
	}
	pts := make([]geo.Vec, 0, n)
	for _, seg := range s.Segments {
		for _, p := range seg.Points {
			pts = append(pts, geo.V(p.X, p.Y))
		}
	}
	return pts
}

func strokeWidthOf(s *doc.Shape) float64 {
	if s.PenWidth > 0 {
		return s.PenWidth
	}
	switch s.Size {
	case "s":
		return 1.5
	case "l":
		return 4.0
	case "xl":
		return 7.0
	}
	return 2.5
}

func textFontSize(s *doc.Shape) float64 {
	if s.FontSize > 0 {
		return float64(s.FontSize)
	}
	return 18
}

func labelFontSize(s *doc.Shape) float64 {
	if s.FontSize > 0 {
		return float64(s.FontSize)
	}
	return 16
}

// PageBounds returns the axis-aligned bounding box of shape id in page coordinates.
func (e *Editor) PageBounds(id doc.ID) geo.Box {
	s := e.Doc.Shapes[id]
	if s == nil {
		return geo.Box{}
	}
	return e.PageMat(id).BoundsOf(e.Geometry(s).Bounds)
}

// PageOutline returns the shape's hit-test outline transformed into page coordinates.
func (e *Editor) PageOutline(id doc.ID) []geo.Vec {
	s := e.Doc.Shapes[id]
	if s == nil {
		return nil
	}
	g := e.Geometry(s)
	m := e.PageMat(id)
	out := make([]geo.Vec, len(g.Outline))
	for i, p := range g.Outline {
		out[i] = m.Apply(p)
	}
	return out
}

// GeoOutlineOf returns the flattened outline of geometry kind in a w x h box.
func GeoOutlineOf(kind string, w, h float64) ([]geo.Vec, bool) {
	return geoOutline(&doc.Shape{ID: doc.ID(kind)}, normalizeGeoKind(kind), w, h)
}

func geoOutline(s *doc.Shape, kind string, w, h float64) ([]geo.Vec, bool) {
	switch kind {
	case "ellipse":
		pts := make([]geo.Vec, 48)
		for i := range pts {
			sn, cs := math.Sincos(2 * math.Pi * float64(i) / float64(len(pts)))
			pts[i] = geo.V(w/2+w/2*cs, h/2+h/2*sn)
		}
		return pts, true
	case "rounded-rect":
		r := math.Min(12, math.Min(w, h)*0.25)
		pts, _ := geo.RoundedRectPath(geo.Box{W: w, H: h}, r).Points(8)
		return pts, true
	case "diamond":
		return []geo.Vec{{X: w / 2, Y: 0}, {X: w, Y: h / 2}, {X: w / 2, Y: h}, {X: 0, Y: h / 2}}, true
	case "triangle":
		return []geo.Vec{{X: w / 2, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}}, true
	case "pentagon":
		return regularPoly(5, w, h, -math.Pi/2), true
	case "hexagon":
		return regularPoly(6, w, h, 0), true
	case "octagon":
		return regularPoly(8, w, h, math.Pi/8), true
	case "parallelogram":
		o := math.Min(w*0.25, h*0.4)
		return []geo.Vec{{X: o, Y: 0}, {X: w, Y: 0}, {X: w - o, Y: h}, {X: 0, Y: h}}, true
	case "parallelogram-2":
		o := math.Min(w*0.25, h*0.4)
		return []geo.Vec{{X: 0, Y: 0}, {X: w - o, Y: 0}, {X: w, Y: h}, {X: o, Y: h}}, true
	case "trapezoid":
		o := math.Min(w*0.24, h*0.4)
		return []geo.Vec{{X: o, Y: 0}, {X: w - o, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}}, true
	case "star":
		return starPoly(w, h), true
	case "cylinder":
		pts, _ := cylinderPath(w, h).Points(12)
		return pts, true
	case "pill":
		pts, _ := pillPath(w, h).Points(12)
		return pts, true
	case "bracket":
		return []geo.Vec{{X: w * 0.7, Y: 0}, {X: 0, Y: 0}, {X: 0, Y: h}, {X: w * 0.7, Y: h}}, false
	case "brace":
		pts, _ := bracePath(w, h).Points(10)
		return pts, false
	case "arrow-right":
		ox, oy := math.Min(w, h)*0.38, h*0.22
		return []geo.Vec{{X: 0, Y: oy}, {X: w - ox, Y: oy}, {X: w - ox, Y: 0}, {X: w, Y: h / 2}, {X: w - ox, Y: h}, {X: w - ox, Y: h - oy}, {X: 0, Y: h - oy}}, true
	case "arrow-left":
		ox, oy := math.Min(w, h)*0.38, h*0.22
		return []geo.Vec{{X: ox, Y: 0}, {X: ox, Y: oy}, {X: w, Y: oy}, {X: w, Y: h - oy}, {X: ox, Y: h - oy}, {X: ox, Y: h}, {X: 0, Y: h / 2}}, true
	case "arrow-up":
		ox, oy := w*0.22, math.Min(w, h)*0.38
		return []geo.Vec{{X: w / 2, Y: 0}, {X: w, Y: oy}, {X: w - ox, Y: oy}, {X: w - ox, Y: h}, {X: ox, Y: h}, {X: ox, Y: oy}, {X: 0, Y: oy}}, true
	case "arrow-down":
		ox, oy := w*0.22, math.Min(w, h)*0.38
		return []geo.Vec{{X: ox, Y: 0}, {X: w - ox, Y: 0}, {X: w - ox, Y: h - oy}, {X: w, Y: h - oy}, {X: w / 2, Y: h}, {X: 0, Y: h - oy}, {X: ox, Y: h - oy}}, true
	case "cloud":
		pts, _ := cloudPath(w, h, string(s.ID)).Points(10)
		return pts, true
	case "heart":
		pts, _ := heartPath(w, h).Points(12)
		return pts, true
	case "callout":
		return calloutPoly(w, h), true
	case "polygon":
		if len(s.Points) >= 3 {
			pts := make([]geo.Vec, len(s.Points))
			for i, lp := range s.Points {
				pts[i] = geo.V(lp.X*w, lp.Y*h)
			}
			return pts, true
		}
		return regularPoly(7, w, h, -math.Pi/2), true
	case "curve":
		pts, _ := curveShapePath(s, w, h).Points(12)
		return pts, false
	default: // rectangle, x-box, check-box
		return []geo.Vec{{X: 0, Y: 0}, {X: w, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}}, true
	}
}

func regularPoly(n int, w, h, start float64) []geo.Vec {
	pts := make([]geo.Vec, n)
	for i := range pts {
		sn, cs := math.Sincos(start + 2*math.Pi*float64(i)/float64(n))
		pts[i] = geo.V(cs, sn)
	}
	b := geo.BoxOf(pts)
	for i, p := range pts {
		pts[i] = geo.V((p.X-b.X)/math.Max(b.W, 1e-6)*w, (p.Y-b.Y)/math.Max(b.H, 1e-6)*h)
	}
	return pts
}

func starPoly(w, h float64) []geo.Vec {
	pts := make([]geo.Vec, 10)
	for i := range pts {
		r := 1.0
		if i%2 == 1 {
			r = 0.42
		}
		sn, cs := math.Sincos(-math.Pi/2 + math.Pi*float64(i)/5)
		pts[i] = geo.V(cs*r, sn*r)
	}
	b := geo.BoxOf(pts)
	for i, p := range pts {
		pts[i] = geo.V((p.X-b.X)/math.Max(b.W, 1e-6)*w, (p.Y-b.Y)/math.Max(b.H, 1e-6)*h)
	}
	return pts
}

func pillPath(w, h float64) *geo.Path {
	p := &geo.Path{}
	const k = 0.5522847498
	if w >= h {
		r := h / 2
		p.Move(geo.V(r, 0)).Line(geo.V(w-r, 0))
		p.Cube(geo.V(w-r+k*r, 0), geo.V(w, r-k*r), geo.V(w, r))
		p.Cube(geo.V(w, r+k*r), geo.V(w-r+k*r, h), geo.V(w-r, h))
		p.Line(geo.V(r, h))
		p.Cube(geo.V(r-k*r, h), geo.V(0, r+k*r), geo.V(0, r))
		p.Cube(geo.V(0, r-k*r), geo.V(r-k*r, 0), geo.V(r, 0))
		return p.Close()
	}
	r := w / 2
	p.Move(geo.V(r, 0))
	p.Cube(geo.V(r+k*r, 0), geo.V(w, r-k*r), geo.V(w, r))
	p.Line(geo.V(w, h-r))
	p.Cube(geo.V(w, h-r+k*r), geo.V(r+k*r, h), geo.V(r, h))
	p.Cube(geo.V(r-k*r, h), geo.V(0, h-r+k*r), geo.V(0, h-r))
	p.Line(geo.V(0, r))
	p.Cube(geo.V(0, r-k*r), geo.V(r-k*r, 0), geo.V(r, 0))
	return p.Close()
}

func cylinderPath(w, h float64) *geo.Path {
	const k = 0.5522847498
	ry := math.Min(h*0.18, w*0.35)
	rx := w / 2
	p := &geo.Path{}
	p.Move(geo.V(0, ry))
	p.Cube(geo.V(0, ry-k*ry), geo.V(rx-k*rx, 0), geo.V(rx, 0))
	p.Cube(geo.V(rx+k*rx, 0), geo.V(w, ry-k*ry), geo.V(w, ry))
	p.Line(geo.V(w, h-ry))
	p.Cube(geo.V(w, h-ry+k*ry), geo.V(rx+k*rx, h), geo.V(rx, h))
	p.Cube(geo.V(rx-k*rx, h), geo.V(0, h-ry+k*ry), geo.V(0, h-ry))
	return p.Close()
}

func bracePath(w, h float64) *geo.Path {
	p := &geo.Path{}
	mid := w * 0.48
	p.Move(geo.V(w, 0))
	p.Quad(geo.V(mid, 0), geo.V(mid, h*0.25))
	p.Quad(geo.V(mid, h*0.5), geo.V(0, h*0.5))
	p.Quad(geo.V(mid, h*0.5), geo.V(mid, h*0.75))
	p.Quad(geo.V(mid, h), geo.V(w, h))
	return p
}

func heartPath(w, h float64) *geo.Path {
	p := &geo.Path{}
	p.Move(geo.V(w/2, h*0.28))
	p.Cube(geo.V(w*0.42, h*0.08), geo.V(w*0.22, 0), geo.V(w*0.14, h*0.04))
	p.Cube(geo.V(-w*0.02, h*0.12), geo.V(-w*0.04, h*0.42), geo.V(w*0.12, h*0.58))
	p.Cube(geo.V(w*0.26, h*0.74), geo.V(w*0.42, h*0.88), geo.V(w/2, h))
	p.Cube(geo.V(w*0.58, h*0.88), geo.V(w*0.74, h*0.74), geo.V(w*0.88, h*0.58))
	p.Cube(geo.V(w*1.04, h*0.42), geo.V(w*1.02, h*0.12), geo.V(w*0.86, h*0.04))
	p.Cube(geo.V(w*0.78, 0), geo.V(w*0.58, h*0.08), geo.V(w/2, h*0.28))
	return p.Close()
}

func cloudPath(w, h float64, seed string) *geo.Path {
	r := geo.NewRng(seed)
	count := int(math.Max(7, math.Min(14, (w+h)/44)))
	pts := make([]geo.Vec, count)
	for i := range pts {
		a := 2*math.Pi*float64(i)/float64(count) + r.Next()*0.08
		sn, cs := math.Sincos(a)
		pts[i] = geo.V(w/2+w/2*cs, h/2+h/2*sn)
	}
	p := &geo.Path{}
	center := geo.V(w/2, h/2)
	for i := range pts {
		a, b := pts[i], pts[(i+1)%count]
		if i == 0 {
			p.Move(a)
		}
		m := geo.Med(a, b)
		out := m.Sub(center).Unit()
		bulge := a.Dist(b) * (0.78 + 0.18*r.Next())
		p.Quad(m.Add(out.Mul(bulge)), b)
	}
	p.Close()
	flat, _ := p.Points(8)
	box := geo.BoxOf(flat)
	if box.W > 0 && box.H > 0 {
		m := geo.Scale(w/box.W, h/box.H).Mul(geo.Translate(-box.X, -box.Y))
		for i := range p.Segs {
			for k := range p.Segs[i].P {
				p.Segs[i].P[k] = m.Apply(p.Segs[i].P[k])
			}
		}
	}
	return p
}

func calloutPoly(w, h float64) []geo.Vec {
	bodyH := h * 0.78
	return []geo.Vec{
		{X: 0, Y: 0},
		{X: w, Y: 0},
		{X: w, Y: bodyH},
		{X: w * 0.36, Y: bodyH},
		{X: w * 0.18, Y: h},
		{X: w * 0.20, Y: bodyH},
		{X: 0, Y: bodyH},
	}
}

func curveShapePath(s *doc.Shape, w, h float64) *geo.Path {
	p := &geo.Path{}
	if len(s.Points) >= 2 {
		pts := make([]geo.Vec, len(s.Points))
		for i, lp := range s.Points {
			pts[i] = geo.V(lp.X*w, lp.Y*h)
		}
		return p.Smooth(pts, false)
	}
	p.Move(geo.V(0, h))
	p.Cube(geo.V(w*0.33, 0), geo.V(w*0.66, h), geo.V(w, 0))
	return p
}

// --- Hit testing ---

type hitKind int

const (
	hitNone hitKind = iota
	hitEdge
	hitInside
	hitHollow
)

// HitResult identifies the topmost shape under a page-space point.
type HitResult struct {
	ID     doc.ID
	Hollow bool
}

// Hit returns the topmost selectable shape under p within margin.
func (e *Editor) Hit(p geo.Vec, margin float64, allowLocked bool) HitResult {
	var hollow doc.ID
	var hollowArea float64
	var walk func(parent doc.ID) doc.ID
	walk = func(parent doc.ID) doc.ID {
		kids := e.Children(parent)
		for i := len(kids) - 1; i >= 0; i-- {
			id := kids[i]
			s := e.Doc.Shapes[id]
			if s == nil || (s.Locked && !allowLocked) {
				continue
			}
			if s.Type == doc.TypeFrame {
				q := e.PageMat(id).Invert().Apply(p)
				box := geo.Box{W: s.W, H: s.H}
				if box.Contains(q) {
					if k := walk(id); k != "" {
						return k
					}
				}
				if geo.DistToPolyline(q, e.Geometry(s).Outline, true) <= margin || e.hitFrameTitle(s, q) {
					return id
				}
				if box.Contains(q) {
					area := s.W * s.H
					if hollow == "" || area < hollowArea {
						hollow, hollowArea = id, area
					}
				}
				continue
			}
			if s.Type == doc.TypeGroup {
				if k := walk(id); k != "" {
					return k
				}
				continue
			}
			switch e.hitShape(s, p, margin) {
			case hitEdge, hitInside:
				return id
			case hitHollow:
				b := e.Geometry(s).Bounds
				area := b.W * b.H
				if hollow == "" || area < hollowArea {
					hollow, hollowArea = id, area
				}
			}
		}
		return ""
	}
	if id := walk(e.Page); id != "" {
		return HitResult{ID: id}
	}
	if hollow != "" {
		return HitResult{ID: hollow, Hollow: true}
	}
	return HitResult{}
}

func (e *Editor) hitShape(s *doc.Shape, p geo.Vec, margin float64) hitKind {
	g := e.Geometry(s)
	q := e.PageMat(s.ID).Invert().Apply(p)
	if !g.Bounds.Expand(margin + 16).Contains(q) {
		return hitNone
	}
	if s.Type == doc.TypeHighlight {
		margin += 10
	} else if s.Type == doc.TypeDraw {
		margin += strokeWidthOf(s)
	}
	if g.Closed && geo.PointInPolygon(q, g.Outline) {
		if g.Filled || s.Type == doc.TypeGeo || (g.Label != nil && s.Text != "" && g.Label.Contains(q)) {
			return hitInside
		}
		if geo.DistToPolyline(q, g.Outline, g.Closed) <= margin {
			return hitEdge
		}
		return hitHollow
	}
	if geo.DistToPolyline(q, g.Outline, g.Closed) <= margin {
		return hitEdge
	}
	if g.Label != nil && g.Label.Contains(q) && s.Text != "" {
		return hitInside
	}
	return hitNone
}

func (e *Editor) hitFrameTitle(s *doc.Shape, q geo.Vec) bool {
	z := e.Cam().Z
	h := 24 / z
	return q.Y < 0 && q.Y >= -h && q.X >= 0 && q.X <= math.Min(s.W, 180/z)
}

func (e *Editor) frameAt(p geo.Vec, skip map[doc.ID]bool) doc.ID {
	var walk func(parent doc.ID) doc.ID
	walk = func(parent doc.ID) doc.ID {
		kids := e.Children(parent)
		for i := len(kids) - 1; i >= 0; i-- {
			id := kids[i]
			if skip[id] {
				continue
			}
			s := e.Doc.Shapes[id]
			if s == nil {
				continue
			}
			if s.Type == doc.TypeGroup {
				if k := walk(id); k != "" {
					return k
				}
				continue
			}
			if s.Type == doc.TypeFrame {
				q := e.PageMat(id).Invert().Apply(p)
				if (geo.Box{W: s.W, H: s.H}).Contains(q) {
					if k := walk(id); k != "" {
						return k
					}
					return id
				}
			}
		}
		return ""
	}
	return walk(e.Page)
}
