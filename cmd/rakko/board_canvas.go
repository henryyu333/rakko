package main

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
)

const boardAtlasBudget = 6_000_000.0

type boardScene struct {
	b        *boardUI
	p        *ui.Painter
	pal      *editor.Palette
	cam      editor.Camera
	origin   geo.Vec
	view     geo.Box
	lightInk bool
	path     ui.Path
}

func (s *boardScene) camMat() geo.Mat {
	return geo.Translate(s.origin.X, s.origin.Y).
		Mul(geo.Scale(s.cam.Z, s.cam.Z)).
		Mul(geo.Translate(s.cam.X, s.cam.Y))
}

func (s *boardScene) buildPath(g *geo.Path, m geo.Mat) *ui.Path {
	p := &s.path
	p.Reset()
	for _, seg := range g.Segs {
		switch seg.Op {
		case geo.OpMove:
			v := m.Apply(seg.P[0])
			p.MoveTo(float32(v.X), float32(v.Y))
		case geo.OpLine:
			v := m.Apply(seg.P[0])
			p.LineTo(float32(v.X), float32(v.Y))
		case geo.OpQuad:
			a, b := m.Apply(seg.P[0]), m.Apply(seg.P[1])
			p.QuadTo(float32(a.X), float32(a.Y), float32(b.X), float32(b.Y))
		case geo.OpCube:
			a, b, c := m.Apply(seg.P[0]), m.Apply(seg.P[1]), m.Apply(seg.P[2])
			p.CubeTo(float32(a.X), float32(a.Y), float32(b.X), float32(b.Y), float32(c.X), float32(c.Y))
		case geo.OpClose:
			p.Close()
		}
	}
	return p
}

func (b *boardUI) paintStage(p *ui.Painter, r ui.Rect) {
	b.ed.SetViewport(float64(r.W), float64(r.H))
	pal := b.ed.Palette()
	cam := b.ed.Cam()
	p.Fill(r, boardColor(pal.Background), 0)

	s := &boardScene{
		b:      b,
		p:      p,
		pal:    pal,
		cam:    cam,
		origin: geo.V(float64(r.X), float64(r.Y)),
		view:   geo.Box{X: -cam.X, Y: -cam.Y, W: float64(r.W) / cam.Z, H: float64(r.H) / cam.Z},
	}

	p.Clip(r, 0, func() {
		b.paintPaper(p, r, pal)
		s.lightInk = s.estimateMaskArea(b.ed.Page) > boardAtlasBudget
		b.lastLightInk = s.lightInk
		s.renderChildren(b.ed.Page)
		s.renderOverlays()
	})
}

func (s *boardScene) estimateMaskArea(parent doc.ID) float64 {
	scale := float64(s.p.Scale())
	if scale <= 0 {
		scale = 1
	}
	k := s.cam.Z * scale
	var area float64
	for _, id := range s.b.ed.Children(parent) {
		sh := s.b.ed.Doc.Shapes[id]
		if sh == nil {
			continue
		}
		if sh.Type == doc.TypeGroup || sh.Type == doc.TypeFrame {
			area += s.estimateMaskArea(id)
			if sh.Type == doc.TypeGroup {
				continue
			}
		}
		box := s.b.ed.PageBounds(id)
		if s.view.Overlaps(box) {
			area += (box.W*k + 8) * (box.H*k + 8)
		}
	}
	return area
}

func (s *boardScene) renderChildren(parent doc.ID) {
	// Highlights always render beneath regular shapes and frames.
	s.renderLayer(parent, true)
	s.renderLayer(parent, false)
}

func (s *boardScene) renderLayer(parent doc.ID, highlights bool) {
	for _, id := range s.b.ed.Children(parent) {
		sh := s.b.ed.Doc.Shapes[id]
		if sh == nil {
			continue
		}
		switch sh.Type {
		case doc.TypeGroup:
			s.renderLayer(id, highlights)
		case doc.TypeHighlight:
			if highlights {
				s.renderShape(sh)
			}
		case doc.TypeFrame:
			if !highlights {
				s.renderFrame(sh)
			}
		default:
			if !highlights {
				s.renderShape(sh)
			}
		}
	}
}

func (s *boardScene) opacityOf(sh *doc.Shape) float64 {
	o := sh.Opacity
	if o <= 0 {
		o = 1
	}
	if s.b.ed.Erasing[sh.ID] {
		o *= 0.25
	}
	cur := sh
	for !doc.IsPageID(cur.Parent) {
		p := s.b.ed.Doc.Shapes[cur.Parent]
		if p == nil {
			break
		}
		if p.Opacity > 0 {
			o *= p.Opacity
		}
		if s.b.ed.Erasing[p.ID] {
			o *= 0.25
		}
		cur = p
	}
	return o
}

func (s *boardScene) culled(sh *doc.Shape) bool {
	b := s.b.ed.PageBounds(sh.ID)
	pad := 48.0 / s.cam.Z
	if sh.Type == doc.TypeDraw || sh.Type == doc.TypeHighlight {
		pad += 60
	}
	return !s.view.Expand(pad).Overlaps(b)
}

func (s *boardScene) renderFrame(sh *doc.Shape) {
	if s.culled(sh) {
		return
	}
	m := s.camMat().Mul(s.b.ed.PageMat(sh.ID))
	o := s.opacityOf(sh)
	prims := s.b.ed.Display(sh, s.pal)
	if len(prims) > 0 && prims[0].Kind == editor.PrimFill {
		s.renderPrim(prims[0], m, o, sh)
	}

	// Clip children strictly to the Frame's interior rectangle (rotated if needed).
	tl := m.Apply(geo.V(0, 0))
	clipRect := ui.Rect{
		X: float32(tl.X),
		Y: float32(tl.Y),
		W: float32(sh.W * s.cam.Z),
		H: float32(sh.H * s.cam.Z),
	}
	rot := m.Rotation()
	if math.Abs(rot) < 1e-4 {
		s.p.Clip(clipRect, 0, func() {
			s.renderChildren(sh.ID)
		})
	} else {
		deg := float32(rot * 180 / math.Pi)
		s.p.Transform(ui.Rotation(deg), clipRect.X, clipRect.Y, func() {
			s.p.Clip(clipRect, 0, func() {
				s.p.Transform(ui.Rotation(-deg), clipRect.X, clipRect.Y, func() {
					s.renderChildren(sh.ID)
				})
			})
		})
	}

	for i := 1; i < len(prims); i++ {
		s.renderPrim(prims[i], m, o, sh)
	}

	if sh.ID != s.b.ed.Editing {
		// Frame title tab above the top-left corner.
		title := strings.TrimSpace(sh.Name)
		if title == "" {
			title = "Frame"
		}
		labelSize := float32(11.5)
		drawTitle := func() {
			s.p.RichText(float32(tl.X+2), float32(tl.Y)-labelSize-5, float32(max(sh.W*s.cam.Z, 80)), ui.Span{
				Text: title, Size: labelSize, Color: boardColor(s.pal.Ink2.Alpha(o)),
			})
		}
		if math.Abs(rot) < 1e-4 {
			drawTitle()
		} else {
			s.p.Transform(ui.Rotation(float32(rot*180/math.Pi)), float32(tl.X), float32(tl.Y), drawTitle)
		}
	}

	if s.b.ed.DropOn == sh.ID {
		s.outlineShape(sh.ID, 2.5, boardColor(s.pal.Accent))
	}
}

func (s *boardScene) renderShape(sh *doc.Shape) {
	if s.culled(sh) {
		return
	}
	m := s.camMat().Mul(s.b.ed.PageMat(sh.ID))
	o := s.opacityOf(sh)
	for _, pr := range s.b.ed.Display(sh, s.pal) {
		if pr.Label && sh.ID == s.b.ed.Editing {
			continue
		}
		s.renderPrim(pr, m, o, sh)
	}
	if pieces, ok := s.b.ed.PartialPreview[sh.ID]; ok {
		s.renderPartialPreview(sh, pieces, m)
	}
}

func (s *boardScene) renderPartialPreview(sh *doc.Shape, pieces [][]geo.Vec, m geo.Mat) {
	col := editor.ResolveColor(sh.Color, s.pal.Dark, s.b.ed.Look())
	w := float32(math.Max(sh.PenWidth, 2.5) * s.cam.Z)
	if sh.Type == doc.TypeHighlight {
		col = s.pal.HighlightColor(sh.Color).Alpha(0.42)
		w = float32(18 * s.cam.Z)
	}
	for _, poly := range pieces {
		if len(poly) < 2 {
			continue
		}
		pts := make([][2]float32, len(poly))
		for i, pt := range poly {
			v := m.Apply(pt)
			pts[i] = [2]float32{float32(v.X), float32(v.Y)}
		}
		s.p.StrokePolyline(pts, max(w, 1), boardColor(col))
	}
}

func (s *boardScene) renderPrim(pr editor.Prim, m geo.Mat, o float64, sh *doc.Shape) {
	switch pr.Kind {
	case editor.PrimFill:
		if s.lightInk && pr.Ink != nil {
			s.renderInkFallback(pr, m, o)
			return
		}
		s.p.FillPath(s.buildPath(pr.Path, m), boardColor(pr.Color.Alpha(o)))
	case editor.PrimStroke:
		if s.lightInk && len(pr.Dash) == 0 && len(pr.Path.Segs) > 0 {
			flat, closed := pr.Path.Points(4)
			if len(flat) >= 2 {
				pts := make([][2]float32, 0, len(flat)+1)
				for _, pt := range flat {
					v := m.Apply(pt)
					pts = append(pts, [2]float32{float32(v.X), float32(v.Y)})
				}
				if closed {
					pts = append(pts, pts[0])
				}
				s.p.StrokePolyline(pts, float32(math.Max(pr.Width*s.cam.Z, 0.75)), boardColor(pr.Color.Alpha(o)))
				return
			}
		}
		if pr.Butt && len(pr.Dash) == 0 && len(pr.Path.Segs) > 0 && isLineSegmentsOnly(pr.Path) {
			// Hatching and straight wireframe segments can draw without mask atlas pressure.
			s.strokeLineSegments(pr.Path, m, float32(math.Max(pr.Width*s.cam.Z, 1)), boardColor(pr.Color.Alpha(o)))
			return
		}
		path := s.buildPath(pr.Path, m)
		if pr.Butt {
			path.Cap(ui.ButtCap).Join(ui.MiterJoin)
		}
		if len(pr.Dash) >= 2 {
			path.Dash(0, float32(pr.Dash[0]*s.cam.Z), float32(pr.Dash[1]*s.cam.Z))
		}
		w := float32(pr.Width * s.cam.Z)
		if w < 0.75 {
			w = 0.75
		}
		s.p.StrokePath(path, w, boardColor(pr.Color.Alpha(o)))
	case editor.PrimText:
		s.renderTextPrim(pr, m, o)
	case editor.PrimImage:
		s.renderImagePrim(pr, m, o)
	}
}

func isLineSegmentsOnly(p *geo.Path) bool {
	for _, seg := range p.Segs {
		if seg.Op != geo.OpMove && seg.Op != geo.OpLine {
			return false
		}
	}
	return true
}

func (s *boardScene) strokeLineSegments(p *geo.Path, m geo.Mat, w float32, c ui.Color) {
	var cur geo.Vec
	for _, seg := range p.Segs {
		switch seg.Op {
		case geo.OpMove:
			cur = m.Apply(seg.P[0])
		case geo.OpLine:
			nxt := m.Apply(seg.P[0])
			s.p.StrokePolyline([][2]float32{
				{float32(cur.X), float32(cur.Y)},
				{float32(nxt.X), float32(nxt.Y)},
			}, w, c)
			cur = nxt
		}
	}
}

func (s *boardScene) renderInkFallback(pr editor.Prim, m geo.Mat, o float64) {
	if pr.Ink == nil || len(pr.Ink.Pts) < 2 {
		return
	}
	pts := make([][2]float32, len(pr.Ink.Pts))
	for i, pt := range pr.Ink.Pts {
		v := m.Apply(pt)
		pts[i] = [2]float32{float32(v.X), float32(v.Y)}
	}
	baseW := 2.5
	if len(pr.Ink.W) > 0 {
		sum := 0.0
		for _, wi := range pr.Ink.W {
			sum += wi
		}
		baseW = sum / float64(len(pr.Ink.W))
	}
	w := float32(baseW * s.cam.Z)
	s.p.StrokePolyline(pts, max(w, 0.75), boardColor(pr.Color.Alpha(o)))
}

var boardNatHeights sync.Map

func boardNaturalHeight(font string) float32 {
	if v, ok := boardNatHeights.Load(font); ok {
		return v.(float32)
	}
	const probe = 100
	l := ui.ShapeText("Hg", ui.Font{Family: boardFontFamily(font), Size: probe}, 0)
	_, h := l.Size()
	v := h / probe
	if v <= 0 {
		v = 1.2
	}
	boardNatHeights.Store(font, v)
	return v
}

func (s *boardScene) renderTextPrim(pr editor.Prim, m geo.Mat, o float64) {
	if pr.Block == nil {
		return
	}
	col := boardColor(pr.Color.Alpha(o))
	size := float32(pr.Size * s.cam.Z)
	if size < 2 {
		return
	}
	family := boardFontFamily(pr.Font)
	weight := 400
	if pr.Bold {
		weight = 700
	}
	nat := boardNaturalHeight(pr.Font) * size
	drop := (float32(pr.Block.Line*s.cam.Z) - nat) / 2
	drawLine := func(pos geo.Vec, text string) {
		s.p.RichText(float32(pos.X), float32(pos.Y)+drop, 0, ui.Span{
			Text:      text,
			Font:      family,
			Size:      size,
			Weight:    weight,
			Italic:    pr.Italic,
			Underline: pr.Underline,
			Color:     col,
		})
	}
	rot := m.Rotation()
	if math.Abs(rot) < 1e-4 {
		for _, line := range pr.Block.Lines {
			if line.Text == "" {
				continue
			}
			drawLine(m.Apply(geo.V(pr.X+line.X, pr.Y+line.Y)), line.Text)
		}
		return
	}
	origin := m.Apply(geo.V(pr.X, pr.Y))
	deg := float32(rot * 180 / math.Pi)
	z := s.cam.Z
	s.p.Transform(ui.Rotation(deg), float32(origin.X), float32(origin.Y), func() {
		for _, line := range pr.Block.Lines {
			if line.Text == "" {
				continue
			}
			drawLine(origin.Add(geo.V(line.X*z, line.Y*z)), line.Text)
		}
	})
}

func (s *boardScene) renderImagePrim(pr editor.Prim, m geo.Mat, o float64) {
	bmp := s.b.bitmapForAsset(pr.Asset)
	origin := m.Apply(geo.V(pr.Box.X, pr.Box.Y))
	dst := ui.Rect{
		X: float32(origin.X),
		Y: float32(origin.Y),
		W: float32(pr.Box.W * s.cam.Z),
		H: float32(pr.Box.H * s.cam.Z),
	}
	draw := func() {
		if bmp == nil {
			s.p.Fill(dst, boardColor(s.pal.BarActive.Alpha(o)), 4)
			return
		}
		var src ui.Rect
		if pr.Crop != nil {
			iw, ih := bmp.Size()
			src = ui.Rect{
				X: float32(pr.Crop.TopLeft.X) * float32(iw),
				Y: float32(pr.Crop.TopLeft.Y) * float32(ih),
				W: float32(pr.Crop.BottomRight.X-pr.Crop.TopLeft.X) * float32(iw),
				H: float32(pr.Crop.BottomRight.Y-pr.Crop.TopLeft.Y) * float32(ih),
			}
		}
		tint := ui.RGBA(255, 255, 255, float32(o))
		s.p.ImageRegion(bmp, src, dst, ui.ImageOptions{
			FlipX: pr.FlipX,
			FlipY: pr.FlipY,
			Tint:  tint,
		})
	}
	rot := m.Rotation()
	if math.Abs(rot) < 1e-4 {
		draw()
		return
	}
	s.p.Transform(ui.Rotation(float32(rot*180/math.Pi)), dst.X, dst.Y, draw)
}

func (s *boardScene) outlineShape(id doc.ID, width float32, c ui.Color) {
	sh := s.b.ed.Doc.Shapes[id]
	if sh == nil {
		return
	}
	if sh.Type == doc.TypeGroup {
		for _, k := range s.b.ed.Children(id) {
			s.outlineShape(k, width, c)
		}
		return
	}
	g := s.b.ed.Geometry(sh)
	m := s.camMat().Mul(s.b.ed.PageMat(id))
	var pts []geo.Vec
	if sh.Type == doc.TypeArrow || sh.Type == doc.TypeLine || sh.Type == doc.TypeDraw || sh.Type == doc.TypeHighlight {
		pts = g.Outline
	} else {
		cs := g.Bounds.Corners()
		pts = cs[:]
	}
	s.p.StrokePath(s.buildPath((&geo.Path{}).Poly(pts, sh.Type != doc.TypeArrow), m), width, c)
}

func (s *boardScene) drawHandleSquare(p geo.Vec, rot float64, fill, line ui.Color) {
	const d = 4.5
	cs := [4]geo.Vec{{X: -d, Y: -d}, {X: d, Y: -d}, {X: d, Y: d}, {X: -d, Y: d}}
	m := geo.Translate(p.X, p.Y).Mul(geo.Rotate(rot))
	pts := make([]geo.Vec, 4)
	for i := range cs {
		pts[i] = m.Apply(cs[i])
	}
	path := s.buildPath((&geo.Path{}).Poly(pts, true), geo.Identity)
	s.p.FillPath(path, fill)
	s.p.StrokePath(path.Join(ui.MiterJoin), 1.5, line)
}

func (s *boardScene) renderSelectionBadge(corners []geo.Vec, c ui.Color) {
	w, h, ok := s.b.ed.SelectionSize()
	if !ok {
		return
	}
	label := fmt.Sprintf("%.0f × %.0f", w, h)
	if deg, rotating := s.b.ed.RotateAngle(); rotating {
		label = fmt.Sprintf("%.0f°", deg)
	}
	bottom := corners[0]
	for _, p := range corners {
		if p.Y > bottom.Y {
			bottom = p
		}
	}
	mid := geo.Med(corners[2], corners[3])
	if mid.Y < bottom.Y {
		mid.Y = bottom.Y
	}
	span := ui.Span{Text: label, Size: 11, Weight: 500, Color: ui.RGBA(255, 255, 255, 1)}
	tw, th := s.p.MeasureText(0, span)
	r := ui.Rect{X: float32(mid.X) - tw/2 - 6, Y: float32(mid.Y) + 10, W: tw + 12, H: th + 4}
	s.p.Fill(r, c, 4)
	s.p.RichText(r.X+6, r.Y+2, 0, span)
}

func (s *boardScene) renderOverlays() {
	cm := s.camMat()
	selCol := boardColor(s.pal.Selection)
	gesturing := s.b.ed.InGesture()

	// Hovered shape outline.
	if hov := s.b.ed.Hovered; hov != "" && !s.b.ed.IsSelected(hov) && !gesturing {
		s.outlineShape(hov, 1.5, boardColor(s.pal.Selection.Alpha(0.45)))
	}

	// Target bind highlight (arrows/lines) and Frame drop highlight.
	for id := range s.b.ed.Hint {
		s.outlineShape(id, 2.5, selCol)
	}

	// Selection bounding box, 8 rotated handles, linear/vertex handles, and size badge.
	ids := s.b.ed.Selection()
	if len(ids) > 0 && s.b.ed.Editing == "" {
		if len(ids) > 1 && !gesturing {
			for _, id := range ids {
				if sh := s.b.ed.Doc.Shapes[id]; sh != nil && !s.culled(sh) {
					s.outlineShape(id, 1, boardColor(s.pal.Selection.Alpha(0.55)))
				}
			}
		}
		if s.b.ed.ShowsBox() {
			if corners, ok := s.b.ed.SelectionCorners(); ok {
				if s.b.ed.Cropping != "" {
					if cs := s.b.ed.Doc.Shapes[s.b.ed.Cropping]; cs != nil {
						if full, ok := editor.UncroppedFrame(cs); ok {
							fm := cm.Mul(s.b.ed.PageMat(cs.ID))
							fcs := full.Corners()
							sc := make([]geo.Vec, 4)
							for i, pt := range fcs {
								sc[i] = fm.Apply(pt)
							}
							fp := s.buildPath((&geo.Path{}).Poly(sc, true), geo.Identity)
							fp.Dash(0, 4, 4)
							s.p.StrokePath(fp, 1, boardColor(s.pal.Ink3.Alpha(0.45)))
						}
					}
				}
				screen := make([]geo.Vec, 4)
				for i, pt := range corners {
					screen[i] = cm.Apply(pt)
				}
				path := s.buildPath((&geo.Path{}).Poly(screen, true), geo.Identity)
				s.p.StrokePath(path.Cap(ui.ButtCap).Join(ui.MiterJoin), 1.5, selCol)

				_, rot, _ := s.b.ed.SelectionFrame()
				if !gesturing || s.b.ed.IsResizing() || s.b.ed.Cropping != "" {
					fill := boardColor(s.pal.Background)
					for _, h := range s.b.ed.Handles() {
						switch h.Kind {
						case editor.HandleResize:
							s.drawHandleSquare(cm.Apply(h.Point), rot, fill, selCol)
						case editor.HandleCrop:
							s.drawHandleSquare(cm.Apply(h.Point), rot, selCol, fill)
						}
					}
				}
				if !gesturing || s.b.ed.IsResizing() {
					s.renderSelectionBadge(screen, selCol)
				} else if _, rotating := s.b.ed.RotateAngle(); rotating {
					s.renderSelectionBadge(screen, selCol)
				}
			}
		} else {
			if len(ids) == 1 {
				s.outlineShape(ids[0], 1.5, selCol)
			}
			fill := boardColor(s.pal.Background)
			for _, h := range s.b.ed.Handles() {
				switch h.Kind {
				case editor.HandleArrowEnd, editor.HandleArrowBend, editor.HandleLinePoint, editor.HandleGeoVertex:
					pt := cm.Apply(h.Point)
					r := float32(4.5)
					alpha := float32(1)
					if h.Virtual || h.Kind == editor.HandleArrowBend {
						r = 3.5
						alpha = 0.78
					}
					cPath := (&ui.Path{}).Circle(float32(pt.X), float32(pt.Y), r)
					s.p.FillPath(cPath, fill.Alpha(alpha))
					s.p.StrokePath(cPath, 1.5, selCol.Alpha(alpha))
				}
			}
		}
	}

	// Marquee selection box.
	if s.b.ed.Brush != nil {
		b := *s.b.ed.Brush
		tl, br := cm.Apply(geo.V(b.X, b.Y)), cm.Apply(geo.V(b.MaxX(), b.MaxY()))
		r := ui.Rect{
			X: float32(tl.X), Y: float32(tl.Y),
			W: float32(br.X - tl.X), H: float32(br.Y - tl.Y),
		}
		s.p.Fill(r, boardColor(s.pal.BrushFill), 0)
		s.p.Stroke(r, boardColor(s.pal.BrushLine), 0, 1)
	}

	// Freehand lasso polygon.
	if len(s.b.ed.Lasso) >= 2 {
		pts := make([]geo.Vec, len(s.b.ed.Lasso))
		for i, pt := range s.b.ed.Lasso {
			pts[i] = cm.Apply(pt)
		}
		if len(pts) >= 3 {
			s.p.FillPath(s.buildPath((&geo.Path{}).Poly(pts, true), geo.Identity), boardColor(s.pal.BrushFill))
		}
		s.p.StrokePath(s.buildPath((&geo.Path{}).Poly(pts, len(pts) >= 3), geo.Identity), 1.5, boardColor(s.pal.BrushLine))
	}

	// Smart alignment guide lines.
	if len(s.b.ed.Snaps) > 0 {
		snapCol := boardColor(s.pal.Snap)
		for _, ln := range s.b.ed.Snaps {
			a, b := cm.Apply(ln.A), cm.Apply(ln.B)
			s.p.Line(float32(a.X), float32(a.Y), float32(b.X), float32(b.Y), 1, snapCol)
		}
	}

	// Active eraser / fading laser trails.
	now := s.b.ed.Now
	for _, tr := range s.b.ed.Trails {
		if len(tr.Points) < 2 {
			continue
		}
		if tr.Laser {
			for i := 1; i < len(tr.Points); i++ {
				a := cm.Apply(tr.Points[i-1].P)
				b := cm.Apply(tr.Points[i].P)
				age := math.Max(0, now-tr.Points[i].T)
				fade := geo.Clamp(1.0-age/1.0, 0, 1)
				if fade <= 0.02 {
					continue
				}
				w := float32(1.8 + 3.4*fade)
				s.p.StrokePolyline([][2]float32{
					{float32(a.X), float32(a.Y)},
					{float32(b.X), float32(b.Y)},
				}, w, boardColor(s.pal.Laser.Alpha(fade)))
			}
			last := tr.Points[len(tr.Points)-1]
			if now-last.T < 1.0 {
				tip := cm.Apply(last.P)
				s.p.FillPath((&ui.Path{}).Circle(float32(tip.X), float32(tip.Y), 5), boardColor(s.pal.Laser.Alpha(0.35)))
				s.p.FillPath((&ui.Path{}).Circle(float32(tip.X), float32(tip.Y), 2.5), boardColor(s.pal.Laser))
			}
			continue
		}
		pts := make([][2]float32, len(tr.Points))
		for i, tp := range tr.Points {
			v := cm.Apply(tp.P)
			pts[i] = [2]float32{float32(v.X), float32(v.Y)}
		}
		s.p.StrokePolyline(pts, 3, boardColor(s.pal.Ink3.Alpha(0.5)))
	}
}

func (b *boardUI) bitmapForAsset(id doc.ID) *ui.Bitmap {
	if id == "" || b.ed == nil {
		return nil
	}
	if b.bitmaps == nil {
		b.bitmaps = map[doc.ID]*ui.Bitmap{}
	}
	if bmp, ok := b.bitmaps[id]; ok {
		return bmp
	}
	asset := b.ed.Doc.Assets[id]
	if asset == nil || asset.Path == "" || b.store == nil {
		return nil
	}
	full, err := b.store.AssetPath(asset.Path)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil
	}
	img, _, err := image.Decode(strings.NewReader(string(data)))
	if err != nil {
		return nil
	}
	bmp := ui.NewBitmap(img)
	b.bitmaps[id] = bmp
	return bmp
}
