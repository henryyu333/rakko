//go:build darwin

package main

// How the screenshot overlay paints itself: the frozen picture, the veil,
// the selection, the marks, the toolbar and the magnifier. Everything is
// drawn in the display's local points; the session's geometry stays global.

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// draw paints one display: the frozen picture, the veil, the selection,
// the marks, the toolbar and the magnifier.
func (s *shotSession) draw(p *ui.Painter, r ui.Rect, di int) {
	d := s.displays[di]
	p.ImageRegion(d.bitmap, ui.Rect{W: float32(d.pixW), H: float32(d.pixH)}, r, ui.ImageOptions{})
	if s.hasSel {
		if di != s.owner {
			p.Fill(r, shotDim, 0)
		} else {
			s.drawVeil(p, d)
			s.drawSelection(p, d)
			s.drawAnnotations(p, d)
			s.drawBar(p, d)
		}
	} else if s.hoverOK && di == s.cursorDpy && shotRectsIntersect(s.hover, d.Bounds) {
		s.drawWindowHint(p, d)
	}
	if di == s.cursorDpy {
		s.drawMagnifier(p, d)
	}
}

// drawVeil darkens everything outside the selection on its display.
func (s *shotSession) drawVeil(p *ui.Painter, d *shotDisplay) {
	local := shotLocalRect(s.sel, d.shotTarget)
	if local.X > 0 {
		p.Fill(ui.Rect{Y: 0, W: float32(local.X), H: float32(d.Bounds.Height)}, shotDim, 0)
	}
	if x := local.X + local.Width; x < d.Bounds.Width {
		p.Fill(ui.Rect{X: float32(x), Y: 0, W: float32(d.Bounds.Width - x), H: float32(d.Bounds.Height)}, shotDim, 0)
	}
	if local.Y > 0 {
		p.Fill(ui.Rect{X: float32(local.X), Y: 0, W: float32(local.Width), H: float32(local.Y)}, shotDim, 0)
	}
	if y := local.Y + local.Height; y < d.Bounds.Height {
		p.Fill(ui.Rect{X: float32(local.X), Y: float32(y), W: float32(local.Width), H: float32(d.Bounds.Height - y)}, shotDim, 0)
	}
}

// drawSelection draws the accent edge and the eight handles.
func (s *shotSession) drawSelection(p *ui.Painter, d *shotDisplay) {
	local := shotLocalRect(s.sel, d.shotTarget)
	r := ui.Rect{X: float32(local.X), Y: float32(local.Y), W: float32(local.Width), H: float32(local.Height)}
	p.Stroke(r, accent, 0, 1.5)
	const hs = 4
	for _, c := range [][2]float32{
		{r.X, r.Y}, {r.X + r.W/2, r.Y}, {r.X + r.W, r.Y},
		{r.X, r.Y + r.H/2}, {r.X + r.W, r.Y + r.H/2},
		{r.X, r.Y + r.H}, {r.X + r.W/2, r.Y + r.H}, {r.X + r.W, r.Y + r.H},
	} {
		box := ui.Rect{X: c[0] - hs, Y: c[1] - hs, W: 2 * hs, H: 2 * hs}
		p.Fill(box, ui.Hex("#FFFFFF"), 2)
		p.Stroke(box, accent, 2, 1)
	}
}

// drawWindowHint outlines the window under the pointer.
func (s *shotSession) drawWindowHint(p *ui.Painter, d *shotDisplay) {
	local := shotLocalRect(s.hover, d.shotTarget)
	r := ui.Rect{X: float32(local.X), Y: float32(local.Y), W: float32(local.Width), H: float32(local.Height)}
	p.Fill(r, accent.Alpha(0.06), 6)
	p.Stroke(r, accent, 6, 2)
}

// drawAnnotations paints every committed mark, the live one and the text
// line being typed.
func (s *shotSession) drawAnnotations(p *ui.Painter, d *shotDisplay) {
	origin := mygo.Point{X: d.Bounds.X, Y: d.Bounds.Y}
	for _, an := range s.hist.annots {
		shotDrawAnnotation(p, an, origin, 1)
	}
	if s.pre.on {
		shotDrawAnnotation(p, s.pre.ann, origin, 1)
	}
	if s.text != nil {
		an := shotAnnotation{tool: shotToolText, color: s.color, a: s.text.at,
			text: s.text.text + s.text.compose}
		shotDrawAnnotation(p, an, origin, 1)
		x, y := float32(s.text.at.X-d.Bounds.X), float32(s.text.at.Y-d.Bounds.Y)
		tw, th := p.MeasureText(0, ui.Span{Text: s.text.text + s.text.compose, Size: shotTextSize})
		if tw == 0 {
			tw = 1
		}
		p.Line(x+tw, y, x+tw, y+max(th, shotTextSize), 1.5, accent)
	}
}

// drawBar paints the toolbar in the owner display's local points.
func (s *shotSession) drawBar(p *ui.Painter, d *shotDisplay) {
	tb := s.bar
	if tb.rect.Width == 0 {
		return
	}
	ox, oy := d.Bounds.X, d.Bounds.Y
	local := func(r mygo.Rectangle) ui.Rect {
		return ui.Rect{X: float32(r.X - ox), Y: float32(r.Y - oy), W: float32(r.Width), H: float32(r.Height)}
	}
	r := local(tb.rect)
	p.Fill(r, shotBarFill, 10)
	p.Stroke(r, shotBarEdge, 10, 0.5)
	for _, it := range tb.items {
		box := local(it.rect)
		switch it.kind {
		case shotBarTool:
			if s.tool == it.tool {
				p.Fill(box, accent.Alpha(0.22), 6)
			}
			p.Icon(shotToolIcon(it.tool), ui.Rect{X: box.X + 4, Y: box.Y + 4, W: box.W - 8, H: box.H - 8}, ink)
		case shotBarColor:
			c := shotAnnotationColors[it.index]
			p.Fill(ui.Rect{X: box.X + 1, Y: box.Y + 1, W: box.W - 2, H: box.H - 2}, c, (box.W-2)/2)
			if s.color == c {
				p.Stroke(ui.Rect{X: box.X - 1, Y: box.Y - 1, W: box.W + 2, H: box.H + 2}, ink, (box.W+2)/2, 1.5)
			}
		case shotBarWidth:
			if s.width == shotAnnotationWidths[it.index] {
				p.Fill(box, shotBarHot, 6)
			}
			d := float32(shotAnnotationWidths[it.index])
			p.Fill(ui.Rect{X: box.X + box.W/2 - d/2, Y: box.Y + box.H/2 - d/2, W: d, H: d}, ink, d/2)
		case shotBarUndo:
			c := ink
			if !s.hist.canUndo() {
				c = ink3
			}
			p.Icon(shotUndoSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, c)
		case shotBarRedo:
			c := ink
			if !s.hist.canRedo() {
				c = ink3
			}
			p.Icon(shotRedoSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, c)
		case shotBarOCR:
			p.Icon(ocrSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, ink)
		case shotBarPin:
			p.Icon(shotPinSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, ink)
		case shotBarSave:
			p.Icon(shotSaveSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, ink)
		case shotBarCancel:
			p.Icon(shotCloseSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, ink)
		case shotBarCopy:
			p.Fill(box, accent, 6)
			label := s.a.appLoc().ShotCopyButton()
			span := ui.Span{Text: label, Size: 12, Weight: 600, Color: ui.Hex("#FFFFFF")}
			tw, th := p.MeasureText(0, span)
			p.RichText(box.X+(box.W-tw)/2, box.Y+(box.H-th)/2, 0, span)
		}
	}
}

// shotMagnifierAt places the magnifier beside the cursor and keeps it off
// the toolbar: below-right, then above, then left, then right, always
// clamped onto the display.
func shotMagnifierAt(cx, cy, dw, dh, bw, bh int, avoid mygo.Rectangle) (int, int) {
	const gap = 18
	try := func(x, y int) (int, int, bool) {
		x = clampInt(x, 6, max(6, dw-bw-6))
		y = clampInt(y, 6, max(6, dh-bh-6))
		r := mygo.Rectangle{X: x, Y: y, Width: bw, Height: bh}
		return x, y, !shotRectsIntersect(r, avoid)
	}
	for _, p := range [][2]int{
		{cx + gap, cy + gap},
		{cx + gap, cy - gap - bh},
		{cx - gap - bw, cy + gap},
		{cx - gap - bw, cy - gap - bh},
	} {
		if x, y, ok := try(p[0], p[1]); ok {
			return x, y
		}
	}
	x, y, _ := try(cx+gap, cy+gap)
	return x, y
}

// drawMagnifier paints the pixel magnifier and the coordinates under the
// pointer. The zoomed part is the frozen image itself, so it shows exactly
// what the crop will contain.
func (s *shotSession) drawMagnifier(p *ui.Painter, d *shotDisplay) {
	c := s.cursor
	if !shotPointInRect(c, d.Bounds) {
		return
	}
	const (
		boxW, boxH = 156, 108
		imgH       = 72
		pad        = 8
		zoom       = 8
	)
	cx0, cy0 := c.X-d.Bounds.X, c.Y-d.Bounds.Y
	avoid := mygo.Rectangle{}
	if s.bar.rect.Width > 0 {
		avoid = shotLocalRect(s.bar.rect, d.shotTarget)
	}
	x, y := shotMagnifierAt(cx0, cy0, d.Bounds.Width, d.Bounds.Height, boxW, boxH, avoid)
	r := ui.Rect{X: float32(x), Y: float32(y), W: boxW, H: boxH}
	p.Shadow(r, 10, 0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.45))
	p.Fill(r, ui.RGBA(16, 16, 18, 0.96), 10)
	p.Stroke(r, shotBarEdge, 10, 0.5)

	// The part of the frozen picture around the pointer, scaled up.
	cx, cy := c.X-d.Bounds.X, c.Y-d.Bounds.Y
	srcW, srcH := float32(boxW-2*pad)/zoom, float32(imgH-2*pad)/zoom
	src := ui.Rect{X: float32(cx) - srcW/2, Y: float32(cy) - srcH/2, W: srcW, H: srcH}
	dst := ui.Rect{X: r.X + pad, Y: r.Y + pad, W: boxW - 2*pad, H: imgH - 2*pad}
	p.Clip(dst, 0, func() {
		p.ImageRegion(d.bitmap, src, dst, ui.ImageOptions{})
	})
	p.Stroke(dst, ui.RGBA(255, 255, 255, 0.18), 0, 0.5)
	p.Line(dst.X+dst.W/2, dst.Y, dst.X+dst.W/2, dst.Y+dst.H, 1, accent)
	p.Line(dst.X, dst.Y+dst.H/2, dst.X+dst.W, dst.Y+dst.H/2, 1, accent)

	w, h := 0, 0
	if s.hasSel {
		if o := s.ownerDisplay(); o != nil {
			w, h = shotPixelSize(s.sel, o.shotTarget, o.pixW, o.pixH)
		}
	}
	label := fmt.Sprintf("%d, %d", c.X, c.Y)
	if s.hasSel {
		label = fmt.Sprintf("%d, %d · %d × %d", c.X, c.Y, w, h)
	}
	p.Text(r.X+pad, r.Y+imgH+4, label, 10.5, ink2)
}

// shotToolIcon is the toolbar's glyph for a tool.
func shotToolIcon(t shotTool) *ui.SVG {
	switch t {
	case shotToolRect:
		return shotRectSymbol
	case shotToolArrow:
		return shotArrowSymbol
	case shotToolPen:
		return pencilSymbol
	case shotToolMosaic:
		return shotMosaicSymbol
	case shotToolText:
		return shotTextSymbol
	case shotToolNumber:
		return shotNumberSymbol
	}
	return shotRectSymbol
}

// Toolbar glyphs and the screenshot card's own glyph.
var (
	shotSymbol       = symbol(`<path d="M4 8V5a1 1 0 0 1 1-1h3M16 4h3a1 1 0 0 1 1 1v3M20 16v3a1 1 0 0 1-1 1h-3M8 20H5a1 1 0 0 1-1-1v-3"/><rect x="7" y="9" width="10" height="6" rx="1"/>`)
	shotRectSymbol   = symbol(`<rect x="4" y="5" width="16" height="14" rx="2"/>`)
	shotArrowSymbol  = symbol(`<path d="M5 19L19 5M19 5h-6M19 5v6"/>`)
	shotMosaicSymbol = symbol(`<path d="M4 4h6v6H4zM14 4h6v6h-6zM9 9h6v6H9zM4 14h6v6H4zM14 14h6v6h-6z"/>`)
	shotTextSymbol   = symbol(`<path d="M5 6h14M12 6v13M8 19h8"/>`)
	shotNumberSymbol = symbol(`<circle cx="12" cy="12" r="8"/><path d="M10 10l2-1v7"/>`)
	shotUndoSymbol   = symbol(`<path d="M9 7L4 12l5 5"/><path d="M4 12h9a5 5 0 0 1 0 10h-2"/>`)
	shotRedoSymbol   = symbol(`<path d="M15 7l5 5-5 5"/><path d="M20 12h-9a5 5 0 0 0 0 10h2"/>`)
	shotPinSymbol    = symbol(`<path d="M9 3h6l-1 6 3 3v2H7v-2l3-3z"/><path d="M12 14v7"/>`)
	shotSaveSymbol   = symbol(`<path d="M5 4h11l3 3v13H5z"/><path d="M8 4v5h7V4M8 20v-6h8v6"/>`)
	shotCloseSymbol  = symbol(`<path d="M6 6l12 12M18 6L6 18"/>`)
)
