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
		}
	} else if s.hoverOK && di == s.cursorDpy && shotRectsIntersect(s.hover, d.Bounds) {
		s.drawWindowHint(p, d)
	}
	if di == s.cursorDpy && s.magnifierVisible() {
		s.drawMagnifier(p, d)
	}
}

// magnifierVisible reports whether the pixel magnifier belongs on screen:
// only while choosing (no selection yet, dragging a new one, or dragging a
// handle). Once the selection is settled, while it is moved whole, and
// while an annotation is being drawn it would cover the very pixels the
// user is working on.
func (s *shotSession) magnifierVisible() bool {
	switch s.mode {
	case shotModeSelect, shotModeResize:
		return true
	case shotModeMove, shotModeDraw:
		return false
	}
	return !s.hasSel
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
		size := shotTextSizeFor(s.width)
		an := shotAnnotation{tool: shotToolText, color: s.color, a: s.text.at, width: s.width,
			text: s.text.text + s.text.compose}
		shotDrawAnnotation(p, an, origin, 1)
		x, y := float32(s.text.at.X-d.Bounds.X), float32(s.text.at.Y-d.Bounds.Y)
		tw, th := p.MeasureText(0, ui.Span{Text: s.text.text + s.text.compose, Size: size})
		if tw == 0 {
			tw = 1
		}
		p.Line(x+tw, y, x+tw, y+max(th, size), 1.5, accent)
	}
}

// buildBar builds the selection's toolbar as real elements over the frozen
// canvas, so hover fills, tooltips and clicks are the app's own controls.
func (s *shotSession) buildBar(c *ui.Context, d *shotDisplay) {
	tb := s.bar
	if tb.rect.Width == 0 {
		return
	}
	s.buildBarStrip(c, "main", tb.shotBarStrip, d)
	if tb.hasProps {
		s.buildBarStrip(c, "props", tb.props, d)
	}
}

// buildBarStrip builds one row's frame, its group dividers and its buttons.
func (s *shotSession) buildBarStrip(c *ui.Context, group string, strip shotBarStrip, d *shotDisplay) {
	ox, oy := d.Bounds.X, d.Bounds.Y
	bg := ui.Box(c.Key("shotbar-"+group)).Absolute().
		Left(float32(strip.rect.X-ox)).Top(float32(strip.rect.Y-oy)).
		Width(float32(strip.rect.Width)).Height(float32(strip.rect.Height)).
		Radius(10).Background(shotBarFill).Border(0.5, shotBarEdge)
	// A Draw callback paints in window coordinates: r is where the bar is.
	bg.Draw(func(p *ui.Painter, r ui.Rect) {
		for _, gx := range strip.seps {
			p.Fill(ui.Rect{X: r.X + float32(gx-strip.rect.X), Y: r.Y + 10, W: 1, H: r.H - 20}, shotBarEdge, 0)
		}
	})
	for _, it := range strip.items {
		s.buildBarButton(c, group, it, d)
	}
}

// buildBarButton builds one toolbar control with its state: the selected
// tool's accent, hover's fill, a disabled action's dimmed glyph, and the
// tooltip with the control's name and shortcut.
func (s *shotSession) buildBarButton(c *ui.Context, group string, it shotBarItem, d *shotDisplay) {
	key := fmt.Sprintf("shotbar-%s-%d-%d-%d", group, it.kind, it.tool, it.index)
	b := ui.Box(c.Key(key)).Absolute().
		Left(float32(it.rect.X-d.Bounds.X)).Top(float32(it.rect.Y-d.Bounds.Y)).
		Size(float32(it.rect.Width), float32(it.rect.Height)).
		Radius(7).Center().Cursor(ui.CursorPointer)
	if name, keys := shotBarTip(c, it); name != "" {
		tip := name
		if keys != "" {
			tip += " · " + keys
		}
		b.Tooltip(tip)
	}
	hovered := b.Hovered()
	disabled := false
	switch it.kind {
	case shotBarTool:
		active := s.tool == it.tool
		switch {
		case active:
			b.Background(accent.Alpha(0.22))
		case hovered:
			b.Background(shotBarHot)
		}
		col := ink
		if active {
			col = accent
		}
		b.Children(func() { ui.Icon(c, shotToolIcon(it.tool)).Size(17, 17).TextColor(col) })
	case shotBarColor:
		col := shotAnnotationColors[it.index]
		b.Radius(shotButtonSize / 2)
		if s.color == col {
			b.Border(1.5, ink)
		}
		if hovered {
			b.Background(shotBarHot)
		}
		b.Children(func() {
			ui.Box(c.Key(key+"-swatch")).Size(shotSwatchSize, shotSwatchSize).
				Radius(shotSwatchSize / 2).Background(col)
		})
	case shotBarWidth:
		w := shotAnnotationWidths[it.index]
		switch {
		case s.width == w:
			b.Background(accent.Alpha(0.22))
		case hovered:
			b.Background(shotBarHot)
		}
		d := float32(w)
		b.Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + (r.W-d)/2, Y: r.Y + (r.H-d)/2, W: d, H: d}, ink, d/2)
		})
	case shotBarUndo, shotBarRedo:
		can := s.hist.canUndo()
		if it.kind == shotBarRedo {
			can = s.hist.canRedo()
		}
		disabled = !can
		b.Disabled(disabled)
		if can && hovered {
			b.Background(shotBarHot)
		}
		col := ink
		if !can {
			col = ink3
		}
		icon := shotUndoSymbol
		if it.kind == shotBarRedo {
			icon = shotRedoSymbol
		}
		b.Children(func() { ui.Icon(c, icon).Size(17, 17).TextColor(col) })
	case shotBarOCR, shotBarPin, shotBarSave, shotBarCancel:
		if hovered {
			b.Background(shotBarHot)
		}
		icon := map[shotBarKind]*ui.SVG{
			shotBarOCR: ocrSymbol, shotBarPin: shotPinSymbol,
			shotBarSave: shotSaveSymbol, shotBarCancel: shotCloseSymbol,
		}[it.kind]
		b.Children(func() { ui.Icon(c, icon).Size(17, 17).TextColor(ink) })
	case shotBarCopy:
		b.Radius(7).Background(accent)
		label := loc(c).ShotCopyButton()
		b.Children(func() {
			ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(ui.Hex("#FFFFFF")).SingleLine()
		})
	}
	if !disabled && b.Clicked() {
		s.actBar(it)
	}
}

// shotBarTip is a control's name and its shortcut, for the tooltip.
func shotBarTip(c *ui.Context, it shotBarItem) (name, keys string) {
	l := loc(c)
	switch it.kind {
	case shotBarTool:
		switch it.tool {
		case shotToolRect:
			return l.ShotToolRect(), ""
		case shotToolArrow:
			return l.ShotToolArrow(), ""
		case shotToolPen:
			return l.ShotToolPen(), ""
		case shotToolMosaic:
			return l.ShotToolMosaic(), ""
		case shotToolText:
			return l.ShotToolText(), ""
		case shotToolNumber:
			return l.ShotToolNumber(), ""
		}
	case shotBarUndo:
		return l.ShotActionUndo(), "⌘Z"
	case shotBarRedo:
		return l.ShotActionRedo(), "⇧⌘Z"
	case shotBarOCR:
		return l.ShotActionOCR(), ""
	case shotBarPin:
		return l.ShotActionPin(), ""
	case shotBarSave:
		return l.ShotActionSave(), "⌘S"
	case shotBarCancel:
		return l.ShotActionCancel(), ""
	case shotBarCopy:
		return l.ShotCopyButton(), "⌘C"
	}
	return "", ""
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
	shotTextSymbol   = symbol(`<path d="M5 6h14M12 6v13M8 19h8"/>`)
	shotNumberSymbol = symbol(`<circle cx="12" cy="12" r="8"/><path d="M10 10l2-1v7"/>`)
	shotUndoSymbol   = symbol(`<path d="M9 7L4 12l5 5"/><path d="M4 12h9a5 5 0 0 1 0 10h-2"/>`)
	shotRedoSymbol   = symbol(`<path d="M15 7l5 5-5 5"/><path d="M20 12h-9a5 5 0 0 0 0 10h2"/>`)
	shotPinSymbol    = symbol(`<path d="M9 3h6l-1 6 3 3v2H7v-2l3-3z"/><path d="M12 14v7"/>`)
	shotSaveSymbol   = symbol(`<path d="M5 4h11l3 3v13H5z"/><path d="M8 4v5h7V4M8 20v-6h8v6"/>`)
	shotCloseSymbol  = symbol(`<path d="M6 6l12 12M18 6L6 18"/>`)
)

// shotMosaicSymbol is a rounded frame holding a 2×2 checker, the same
// stroke weight as the other tools' glyphs.
var shotMosaicSymbol = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2.5"/><path d="M5 5h7v7H5zM12 12h7v7h-7z" fill="currentColor" stroke="none"/></svg>`))
