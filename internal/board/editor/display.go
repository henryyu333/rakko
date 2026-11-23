package editor

import (
	"math"
	"slices"
	"sort"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

// PrimKind identifies a renderer-independent drawing primitive.
type PrimKind uint8

const (
	PrimFill PrimKind = iota
	PrimStroke
	PrimText
	PrimImage
)

// Ink stores the centerline points and widths of a hand-drawn stroke ribbon so
// high-density scenes can degrade smoothly to polyline strokes if mask budget overflows.
type Ink struct {
	Pts []geo.Vec
	W   []float64
}

// Prim is one vector, text, or image primitive in a shape's local coordinate space.
type Prim struct {
	Kind  PrimKind
	Path  *geo.Path
	Color Color
	Width float64
	Dash  []float64
	Butt  bool

	Ink *Ink

	// Text fields.
	Block     *TextBlock
	X, Y      float64
	Size      float64
	Font      string
	Bold      bool
	Italic    bool
	Underline bool
	Label     bool

	// Image fields.
	Asset doc.ID
	Box   geo.Box
	FlipX bool
	FlipY bool
	Crop  *doc.Crop
}

func lookIndex(look string) int {
	switch look {
	case doc.LookClean:
		return 1
	case doc.LookMono:
		return 2
	default:
		return 0
	}
}

// Display returns the cached display primitives for s in the current look and theme.
func (e *Editor) Display(s *doc.Shape, pal *Palette) []Prim {
	if s == nil {
		return nil
	}
	di := 0
	if !pal.Dark {
		di = 1
	}
	li := lookIndex(e.Look())
	c := cacheOf(s)
	switch s.Type {
	case doc.TypeArrow:
		info := e.Arrow(s)
		if c.arrowInfo != info {
			c.arrowInfo = info
			c.geom = nil
			c.ok = [2][3]bool{}
		}
	case doc.TypeLine:
		pts := e.linePoints(s)
		if !slices.Equal(c.linePts, pts) {
			c.linePts = pts
			c.geom = nil
			c.ok = [2][3]bool{}
		}
	}
	if !c.ok[di][li] {
		c.display[di][li] = e.buildPrims(s, pal)
		c.ok[di][li] = true
	}
	return c.display[di][li]
}

func (e *Editor) buildPrims(s *doc.Shape, pal *Palette) []Prim {
	switch s.Type {
	case doc.TypeGeo:
		return e.geoPrims(s, pal)
	case doc.TypeDraw:
		return e.drawPrims(s, pal)
	case doc.TypeHighlight:
		return e.highlightPrims(s, pal)
	case doc.TypeArrow:
		return e.arrowPrims(s, pal)
	case doc.TypeLine:
		return e.linePrims(s, pal)
	case doc.TypeText:
		return e.textPrims(s, pal)
	case doc.TypeNote:
		return e.notePrims(s, pal)
	case doc.TypeFrame:
		return e.framePrims(s, pal)
	case doc.TypeImage:
		return e.imagePrims(s, pal)
	case doc.TypeGroup:
		return nil
	}
	return nil
}

func strokeWith(path *geo.Path, width float64, c Color) Prim {
	return Prim{Kind: PrimStroke, Path: path, Width: width, Color: c}
}

func fillWith(path *geo.Path, c Color) Prim {
	return Prim{Kind: PrimFill, Path: path, Color: c}
}

func dashPattern(dash string, w float64) []float64 {
	switch dash {
	case "dash", "dashed":
		return []float64{w * 3.5, w * 3.0}
	case "dot", "dotted":
		return []float64{0.1, w * 2.5}
	}
	return nil
}

func (e *Editor) geoBasePath(s *doc.Shape, w, h float64) (*geo.Path, bool) {
	kind := normalizeGeoKind(s.Geo)
	switch kind {
	case "ellipse":
		return geo.EllipsePath(geo.Box{W: w, H: h}), true
	case "rounded-rect":
		r := math.Min(12, math.Min(w, h)*0.25)
		return geo.RoundedRectPath(geo.Box{W: w, H: h}, r), true
	case "pill":
		return pillPath(w, h), true
	case "cylinder":
		return cylinderPath(w, h), true
	case "brace":
		return bracePath(w, h), false
	case "heart":
		return heartPath(w, h), true
	case "cloud":
		return cloudPath(w, h, string(s.ID)), true
	case "curve":
		return curveShapePath(s, w, h), false
	}
	pts, closed := geoOutline(s, kind, w, h)
	if e.Look() == doc.LookSketch && closed && kind != "rectangle" && kind != "x-box" && kind != "check-box" {
		return geo.RoundedPoly(pts, math.Min(strokeWidthOf(s)*2, 8)), true
	}
	return (&geo.Path{}).Poly(pts, closed), closed
}

func (e *Editor) geoPrims(s *doc.Shape, pal *Palette) []Prim {
	w, h := math.Max(s.W, 1), math.Max(s.H+s.GrowY, 1)
	kind := normalizeGeoKind(s.Geo)
	strokeCol := ResolveColor(s.Color, pal.Dark, e.Look())
	fillHex := s.FillColor
	if fillHex == "" {
		fillHex = s.Color
	}
	fillCol := ResolveColor(fillHex, pal.Dark, e.Look())
	wStroke := strokeWidthOf(s)

	base, closed := e.geoBasePath(s, w, h)
	if s.FlipX || s.FlipY {
		base = flipPath(base, w, h, s.FlipX, s.FlipY)
	}
	flat, _ := base.Points(10)

	var out []Prim
	if closed && s.Fill != "none" && s.Fill != "" {
		out = append(out, fillPrims(s.Fill, base, flat, fillCol)...)
	}

	out = append(out, outlinePrims(e.Look(), s.Dash, base, flat, closed, wStroke, strokeCol, string(s.ID))...)

	// Extra interior strokes for cylinder top cap, x-box, check-box.
	inner := &geo.Path{}
	switch kind {
	case "cylinder":
		ry := math.Min(h*0.18, w*0.35)
		inner.Move(geo.V(0, ry)).Quad(geo.V(w/2, ry*2), geo.V(w, ry))
	case "x-box":
		inner.Move(geo.V(0, 0)).Line(geo.V(w, h)).Move(geo.V(w, 0)).Line(geo.V(0, h))
	case "check-box":
		k := math.Min(w, h)
		inner.Move(geo.V(w/2-k*0.26, h/2)).
			Line(geo.V(w/2-k*0.06, h/2+k*0.20)).
			Line(geo.V(w/2+k*0.26, h/2-k*0.20))
	}
	if len(inner.Segs) > 0 {
		if s.FlipX || s.FlipY {
			inner = flipPath(inner, w, h, s.FlipX, s.FlipY)
		}
		innerCol := strokeCol
		if s.Fill == "solid" && fillCol == strokeCol {
			innerCol = pal.Background
		}
		out = append(out, strokeWith(inner, wStroke, innerCol))
	}

	if s.Text != "" {
		g := e.Geometry(s)
		if g.Label != nil {
			out = append(out, e.labelPrim(s, g.Label, labelFontSize(s), strokeCol))
		}
	}
	return out
}

func flipPath(p *geo.Path, w, h float64, fx, fy bool) *geo.Path {
	out := &geo.Path{Segs: make([]geo.Seg, len(p.Segs))}
	tf := func(v geo.Vec) geo.Vec {
		if fx {
			v.X = w - v.X
		}
		if fy {
			v.Y = h - v.Y
		}
		return v
	}
	for i, seg := range p.Segs {
		seg.P[0] = tf(seg.P[0])
		seg.P[1] = tf(seg.P[1])
		seg.P[2] = tf(seg.P[2])
		out.Segs[i] = seg
	}
	return out
}

func fillPrims(fillStyle string, base *geo.Path, poly []geo.Vec, c Color) []Prim {
	switch fillStyle {
	case "semi":
		return []Prim{fillWith(base, c.Alpha(0.28))}
	case "solid":
		return []Prim{fillWith(base, c)}
	case "pattern":
		p := fillWith(base, c.Alpha(0.12))
		h := strokeWith(hatchPath(poly, 8), 1.4, c.Alpha(0.55))
		h.Butt = true
		return []Prim{p, h}
	}
	return nil
}

func hatchPath(poly []geo.Vec, spacing float64) *geo.Path {
	box := geo.BoxOf(poly)
	p := &geo.Path{}
	if box.W <= 0 || box.H <= 0 || len(poly) < 3 {
		return p
	}
	step := spacing * math.Sqrt2
	start := math.Floor((box.X+box.Y)/step) * step
	end := box.MaxX() + box.MaxY()
	far := box.W + box.H + 10
	for k := start; k <= end; k += step {
		a := geo.V(k-box.Y+far, box.Y-far)
		b := geo.V(k-box.Y-far, box.Y+far)
		cross := geo.SegmentPolyline(a, b, poly, true)
		sort.Slice(cross, func(i, j int) bool {
			return cross[i].Dist(a) < cross[j].Dist(a)
		})
		for i := 0; i+1 < len(cross); i += 2 {
			p.Move(cross[i]).Line(cross[i+1])
		}
	}
	return p
}

func outlinePrims(look, dash string, base *geo.Path, flat []geo.Vec, closed bool, w float64, c Color, seed string) []Prim {
	if dash == "none" {
		return nil
	}
	if d := dashPattern(dash, w); len(d) > 0 {
		pr := strokeWith(base, w, c)
		pr.Dash = d
		return []Prim{pr}
	}
	if look == doc.LookSketch && len(flat) >= 2 {
		// Deterministic double-pass subtle hand-drawn wobble.
		r := geo.NewRng(seed)
		amp := math.Min(1.8, 0.4+w*0.25)
		var out []Prim
		for pass := 0; pass < 2; pass++ {
			sub := resamplePolyline(flat, closed, 32)
			jittered := make([]geo.Vec, len(sub))
			for i, pt := range sub {
				jittered[i] = geo.V(pt.X+r.Next()*amp, pt.Y+r.Next()*amp)
			}
			p := (&geo.Path{}).Smooth(jittered, closed)
			alpha := 1.0
			if pass == 1 {
				alpha = 0.55
			}
			out = append(out, strokeWith(p, w, c.Alpha(alpha)))
		}
		return out
	}
	return []Prim{strokeWith(base, w, c)}
}

func resamplePolyline(pts []geo.Vec, closed bool, step float64) []geo.Vec {
	if len(pts) < 2 {
		return pts
	}
	count := len(pts) - 1
	if closed {
		count = len(pts)
	}
	out := make([]geo.Vec, 0, len(pts)*2)
	for i := 0; i < count; i++ {
		a, b := pts[i], pts[(i+1)%len(pts)]
		d := a.Dist(b)
		n := max(1, int(math.Ceil(d/step)))
		for k := 0; k < n; k++ {
			out = append(out, a.Lerp(b, float64(k)/float64(n)))
		}
	}
	if !closed {
		out = append(out, pts[len(pts)-1])
	}
	return out
}

// --- freehand draw & highlight ---

func smoothStroke(pts []geo.Vec, amount float64) []geo.Vec {
	if len(pts) < 3 {
		return pts
	}
	out := make([]geo.Vec, 0, len(pts))
	out = append(out, pts[0])
	cur := pts[0]
	for _, p := range pts[1:] {
		cur = cur.Lerp(p, 1-amount)
		out = append(out, cur)
	}
	out = append(out, pts[len(pts)-1])
	return out
}

func (e *Editor) drawPrims(s *doc.Shape, pal *Palette) []Prim {
	c := ResolveColor(s.Color, pal.Dark, e.Look())
	baseW := strokeWidthOf(s)
	if len(s.Segments) == 0 {
		return nil
	}

	var out []Prim
	// Closed fillable outline when stroke formed a closed loop.
	allPts := strokePoints(s)
	if len(allPts) == 0 {
		return nil
	}
	if s.IsClosed && s.Fill != "none" && s.Fill != "" {
		fillHex := s.FillColor
		if fillHex == "" {
			fillHex = s.Color
		}
		fc := ResolveColor(fillHex, pal.Dark, e.Look())
		smooth := (&geo.Path{}).Smooth(allPts, true)
		out = append(out, fillPrims(s.Fill, smooth, allPts, fc)...)
	}

	// Render each segment (freehand smoothed ribbon vs Shift straight segment).
	sx, sy := orOne(s.ScaleX), orOne(s.ScaleY)
	for _, seg := range s.Segments {
		if len(seg.Points) == 0 {
			continue
		}
		if seg.Type == "straight" {
			pts := make([]geo.Vec, len(seg.Points))
			for i, p := range seg.Points {
				pts[i] = geo.V(p.X*sx, p.Y*sy)
			}
			p := (&geo.Path{}).Poly(pts, false)
			out = append(out, strokeWith(p, baseW, c))
			continue
		}
		out = append(out, freehandSegmentPrims(s, seg, baseW, c, e.Look())...)
	}
	return out
}

func freehandSegmentPrims(s *doc.Shape, seg doc.Segment, baseW float64, c Color, look string) []Prim {
	sx, sy := orOne(s.ScaleX), orOne(s.ScaleY)
	pts := make([]geo.Vec, len(seg.Points))
	press := make([]float64, len(seg.Points))
	for i, p := range seg.Points {
		pts[i] = geo.V(p.X*sx, p.Y*sy)
		press[i] = p.Z
	}
	if len(pts) == 1 {
		r := math.Max(baseW*0.5, 1.0)
		return []Prim{fillWith(geo.EllipsePath(geo.Box{X: pts[0].X - r, Y: pts[0].Y - r, W: 2 * r, H: 2 * r}), c)}
	}

	smoothAmt := 0.42
	wMul := 1.15
	switch s.PenKind {
	case "pen", "p1":
		smoothAmt = 0.25
		wMul = 1.45
	case "marker", "p2":
		smoothAmt = 0.52
		wMul = 2.8
	}
	sm := smoothStroke(pts, smoothAmt)

	if s.PenKind == "marker" || s.PenKind == "p2" {
		p := (&geo.Path{}).Smooth(sm, false)
		return []Prim{strokeWith(p, baseW*wMul, c.Alpha(0.78))}
	}

	if s.PenKind != "pen" && s.PenKind != "p1" || look == doc.LookClean || s.Dash == "dash" || s.Dash == "dot" {
		p := (&geo.Path{}).Smooth(sm, false)
		pr := strokeWith(p, baseW*wMul, c)
		pr.Dash = dashPattern(s.Dash, baseW*wMul)
		return []Prim{pr}
	}

	// Build variable-width tapered ribbon for fountain pen feel.
	sp := make([]geo.StrokePoint, len(sm))
	for i, pt := range sm {
		z := 0.5
		if i < len(press) && press[i] > 0 {
			z = press[i]
		}
		sp[i] = geo.StrokePoint{X: pt.X, Y: pt.Y, Z: z}
	}
	simulate := !s.IsPen
	ribPts := geo.RibbonOutline(sp, baseW*wMul*1.35, simulate)
	if len(ribPts) < 3 {
		return []Prim{strokeWith((&geo.Path{}).Poly(sm, false), baseW*wMul, c)}
	}
	ws := make([]float64, len(sm))
	for i := range ws {
		ws[i] = baseW * wMul
	}
	path := (&geo.Path{}).Smooth(ribPts, true)
	return []Prim{{
		Kind:  PrimFill,
		Path:  path,
		Color: c,
		Ink:   &Ink{Pts: sm, W: ws},
	}}
}

func (e *Editor) highlightPrims(s *doc.Shape, pal *Palette) []Prim {
	pts := strokePoints(s)
	if len(pts) == 0 {
		return nil
	}
	w := math.Max(strokeWidthOf(s)*5.5, 16)
	c := pal.HighlightColor(s.Color).Alpha(0.42)
	if len(pts) == 1 {
		r := w / 2
		return []Prim{fillWith(geo.EllipsePath(geo.Box{X: pts[0].X - r, Y: pts[0].Y - r, W: w, H: w}), c)}
	}
	sm := smoothStroke(pts, 0.5)
	return []Prim{strokeWith((&geo.Path{}).Smooth(sm, false), w, c)}
}

// --- text, note, frame, image ---

func (e *Editor) labelPrim(s *doc.Shape, box *geo.Box, size float64, c Color) Prim {
	b := e.measure(s.Text, s.Font, size, box.W)
	b = alignBlock(b, box.W, s.Align)
	return Prim{
		Kind: PrimText, Block: &b, X: box.X, Y: box.Y,
		Size: size, Font: s.Font, Color: c,
		Bold: s.Bold, Italic: s.Italic, Underline: s.Underline, Label: true,
	}
}

func alignBlock(b TextBlock, width float64, align string) TextBlock {
	lines := make([]TextLine, len(b.Lines))
	for i, l := range b.Lines {
		switch align {
		case "center", "middle":
			l.X = (width - l.W) / 2
		case "right", "end":
			l.X = width - l.W
		default:
			l.X = 0
		}
		lines[i] = l
	}
	b.Lines = lines
	return b
}

func (e *Editor) textPrims(s *doc.Shape, pal *Palette) []Prim {
	size := textFontSize(s)
	g := e.Geometry(s)
	scale := orOne(s.Scale)
	var b TextBlock
	if s.AutoSize || s.W <= 0 {
		b = e.measure(s.Text, s.Font, size, 0)
	} else {
		b = e.measure(s.Text, s.Font, size, math.Max(s.W*scale, 8))
	}
	align := s.TextAlign
	if align == "" {
		align = s.Align
	}
	b = alignBlock(b, g.Bounds.W, align)
	c := ResolveColor(s.Color, pal.Dark, e.Look())
	return []Prim{{
		Kind: PrimText, Block: &b, Size: size, Font: s.Font,
		Color: c, Bold: s.Bold, Italic: s.Italic, Underline: s.Underline, Label: true,
	}}
}

func (e *Editor) notePrims(s *doc.Shape, pal *Palette) []Prim {
	g := e.Geometry(s)
	box := g.Bounds
	cs := box.Corners()
	fillCol := pal.NoteFillColor(s.Color)
	out := []Prim{
		fillWith(geo.RoundedPoly(cs[:], 8), fillCol),
		strokeWith(geo.RoundedPoly(cs[:], 8), 1, Color{R: 0, G: 0, B: 0, A: 0.10}),
	}
	if s.Text != "" && g.Label != nil {
		out = append(out, e.labelPrim(s, g.Label, labelFontSize(s), pal.NoteText))
	}
	return out
}

func (e *Editor) framePrims(s *doc.Shape, pal *Palette) []Prim {
	box := geo.Box{W: math.Max(s.W, 1), H: math.Max(s.H, 1)}
	cs := box.Corners()
	path := (&geo.Path{}).Poly(cs[:], true)
	stroke := strokeWith(path, 1, pal.FrameStroke)
	stroke.Butt = true
	return []Prim{fillWith(path, pal.FrameFill), stroke}
}

func (e *Editor) imagePrims(s *doc.Shape, pal *Palette) []Prim {
	box := geo.Box{W: math.Max(s.W, 1), H: math.Max(s.H, 1)}
	if s.AssetID == "" || e.Doc.Assets[s.AssetID] == nil {
		cs := box.Corners()
		return []Prim{fillWith((&geo.Path{}).Poly(cs[:], true), pal.BarActive)}
	}
	return []Prim{{
		Kind:  PrimImage,
		Asset: s.AssetID,
		Box:   box,
		FlipX: s.FlipX,
		FlipY: s.FlipY,
		Crop:  s.Crop,
	}}
}
