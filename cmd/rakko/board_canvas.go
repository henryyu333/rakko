package main

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"strings"

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

	// Clip children strictly to the Frame's interior rectangle.
	tl := m.Apply(geo.V(0, 0))
	br := m.Apply(geo.V(sh.W, sh.H))
	clipRect := ui.Rect{
		X: float32(math.Min(tl.X, br.X)),
		Y: float32(math.Min(tl.Y, br.Y)),
		W: float32(math.Abs(br.X - tl.X)),
		H: float32(math.Abs(br.Y - tl.Y)),
	}
	s.p.Clip(clipRect, 0, func() {
		s.renderChildren(sh.ID)
	})

	for i := 1; i < len(prims); i++ {
		s.renderPrim(prims[i], m, o, sh)
	}

	// Frame title tab above the top-left corner.
	title := strings.TrimSpace(sh.Name)
	if title == "" {
		title = "Frame"
	}
	labelSize := float32(11.5)
	s.p.Text(float32(tl.X+2), float32(tl.Y)-labelSize-5, title, labelSize, boardColor(s.pal.Ink2.Alpha(o)))

	if s.b.ed.DropOn == sh.ID {
		s.p.Stroke(clipRect, boardColor(s.pal.Accent), 0, 2)
	}
}

func (s *boardScene) renderShape(sh *doc.Shape) {
	if s.culled(sh) {
		return
	}
	m := s.camMat().Mul(s.b.ed.PageMat(sh.ID))
	o := s.opacityOf(sh)
	for _, pr := range s.b.ed.Display(sh, s.pal) {
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
		if s.lightInk && pr.Ink != nil && pr.Color.A*o >= 0.99 {
			s.renderInkFallback(pr, m, o)
			return
		}
		s.p.FillPath(s.buildPath(pr.Path, m), boardColor(pr.Color.Alpha(o)))
	case editor.PrimStroke:
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
	w := float32(2.5 * s.cam.Z)
	if len(pr.Ink.W) > 0 {
		w = float32(pr.Ink.W[0] * s.cam.Z)
	}
	s.p.StrokePolyline(pts, max(w, 0.75), boardColor(pr.Color.Alpha(o)))
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
	for _, line := range pr.Block.Lines {
		if line.Text == "" {
			continue
		}
		pos := m.Apply(geo.V(pr.X+line.X, pr.Y+line.Y))
		s.p.Text(float32(pos.X), float32(pos.Y), line.Text, size, col)
		if pr.Underline {
			uw := float32(line.W * s.cam.Z)
			s.p.Fill(ui.Rect{X: float32(pos.X), Y: float32(pos.Y) + size*1.05, W: uw, H: max(1, size/14)}, col, 0)
		}
	}
}

func (s *boardScene) renderImagePrim(pr editor.Prim, m geo.Mat, o float64) {
	bmp := s.b.bitmapForAsset(pr.Asset)
	tl := m.Apply(geo.V(pr.Box.X, pr.Box.Y))
	br := m.Apply(geo.V(pr.Box.MaxX(), pr.Box.MaxY()))
	dst := ui.Rect{
		X: float32(math.Min(tl.X, br.X)),
		Y: float32(math.Min(tl.Y, br.Y)),
		W: float32(math.Abs(br.X - tl.X)),
		H: float32(math.Abs(br.Y - tl.Y)),
	}
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

func (s *boardScene) renderOverlays() {
	cm := s.camMat()
	// Hovered shape outline.
	if hov := s.b.ed.Hovered; hov != "" && !s.b.ed.IsSelected(hov) {
		box := s.b.ed.PageBounds(hov)
		tl, br := cm.Apply(geo.V(box.X, box.Y)), cm.Apply(geo.V(box.MaxX(), box.MaxY()))
		s.p.Stroke(ui.Rect{
			X: float32(tl.X), Y: float32(tl.Y),
			W: float32(br.X - tl.X), H: float32(br.Y - tl.Y),
		}, boardColor(s.pal.Selection.Alpha(0.45)), 0, 1.5)
	}

	// Selection bounding box.
	if box, ok := s.b.ed.SelectionBox(); ok {
		tl, br := cm.Apply(geo.V(box.X, box.Y)), cm.Apply(geo.V(box.MaxX(), box.MaxY()))
		s.p.Stroke(ui.Rect{
			X: float32(tl.X), Y: float32(tl.Y),
			W: float32(br.X - tl.X), H: float32(br.Y - tl.Y),
		}, boardColor(s.pal.Selection), 0, 1.5)
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

	// Active eraser trail.
	for _, tr := range s.b.ed.Trails {
		if len(tr.Points) < 2 {
			continue
		}
		pts := make([][2]float32, len(tr.Points))
		for i, tp := range tr.Points {
			v := cm.Apply(tp.P)
			pts[i] = [2]float32{float32(v.X), float32(v.Y)}
		}
		col := boardColor(s.pal.Ink3.Alpha(0.5))
		if tr.Laser {
			col = boardColor(s.pal.Laser)
		}
		s.p.StrokePolyline(pts, 3, col)
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
