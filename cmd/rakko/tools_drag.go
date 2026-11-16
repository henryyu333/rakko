package main

import (
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
)

const (
	durLift     = 120 * time.Millisecond
	durPinMorph = 180 * time.Millisecond

	dragPreviewPadL = 32
	dragPreviewPadR = 32
	dragPreviewPadT = 20
	dragPreviewPadB = 48
)

var (
	liftedCardFill       = ui.Hex("#3A3A3D")
	allowHeadlessPreview bool
)

func (p *toolsPage) dragSpec(id string) layout.Card {
	if p.press != nil && p.press.fresh && p.press.id == id {
		return p.press.spec
	}
	for _, spec := range p.host.toolsLayout().Cards {
		if spec.ID == id {
			return spec
		}
	}
	spec := layout.Card{ID: id, Span: 1}
	if def := toolCardDefFor(id); def != nil {
		spec.Span = def.span
	}
	return spec
}

// dragPreview is the transient window a dragged card follows the pointer
// in once the pointer leaves the panel's drawn shape (with hysteresis).
// The preview is not a pin and never enters layout.json; a drop outside the
// panel hands its window to the committed pin instead of opening another.
//
// armed is the same window at zero opacity while the pointer is still
// inside the shape. It follows the pointer invisibly, so crossing the edge
// only raises its opacity: creating and compositing a native window took
// macOS four frames, which left the card missing (a 67 ms hole at the
// handoff) before this. The panel still owns the visible card inside; the
// armed window is never hit-tested or counted as "the preview is up".
type dragPreview struct {
	it    *pinItem
	armed *pinItem
}

// window is the preview's pin item, nil while no preview is up.
func (p *dragPreview) window() *pinItem {
	if p == nil {
		return nil
	}
	return p.it
}

// promote turns the armed window into the visible preview.
func (p *dragPreview) promote() {
	if p == nil || p.armed == nil {
		return
	}
	p.it, p.armed = p.armed, nil
	if p.it.win != nil {
		p.it.win.SetOpacity(1)
	}
}

// demote hides an active preview again without destroying it: the window
// renders at zero opacity and is ready for the next crossing.
func (p *dragPreview) demote() {
	if p == nil || p.it == nil {
		return
	}
	p.it, p.armed = nil, p.it
	if p.armed.win != nil {
		p.armed.win.SetOpacity(0)
	}
}

// dropArmed destroys a prepared window that is no longer near the edge.
func (p *dragPreview) dropArmed() {
	if p == nil || p.armed == nil {
		return
	}
	if w := p.armed.win; w != nil {
		w.Hide()
		w.Destroy()
	}
	p.armed = nil
}

// close destroys the preview window now: a drag that returns to the page,
// or one that is cancelled, must not leave the copy on the desktop.
func (p *dragPreview) close() {
	if it := p.window(); it != nil {
		p.it = nil
		if it.win != nil {
			it.win.Hide() // remove immediately; native close can animate out
			it.win.Destroy()
			it.win = nil
		}
	}
}

// closeAll ends a drag: the active preview and the armed window both go.
func (p *dragPreview) closeAll() {
	p.close()
	p.dropArmed()
}

// take hands the window over to a committed pin.
func (p *dragPreview) take() *pinItem {
	it := p.window()
	p.it = nil
	if it != nil {
		// The transparent preview becomes a committed pin: its speed tilt,
		// morph padding, and preview-only look must end with the gesture.
		it.preview, it.tilt = false, 0
		it.padL, it.padT, it.subX, it.subY = 0, 0, 0, 0
	}
	return it
}

func lerpF(a, b, t float32) float32 {
	return a + (b-a)*t
}

func lerpColor(a, b ui.Color, t float32) ui.Color {
	u := func(x, y uint8) uint8 {
		v := float32(x) + (float32(y)-float32(x))*t
		if v <= 0 {
			return 0
		}
		if v >= 255 {
			return 255
		}
		return uint8(math.Round(float64(v)))
	}
	return ui.RGBA(u(a.R, b.R), u(a.G, b.G), u(a.B, b.B), lerpF(float32(a.A)/255, float32(b.A)/255, t))
}

func dragMorphValue(from, to float32, at, now time.Time, reduce bool) float32 {
	if reduce || at.IsZero() || from == to {
		return to
	}
	p := float32(now.Sub(at)) / float32(durPinMorph)
	if p <= 0 {
		return from
	}
	if p >= 1 {
		return to
	}
	// Smoothstep: the shape and colour leave the handoff pose with zero
	// velocity, so the first frame after the window switch does not grow the
	// card abruptly. (An ease-out grew a 100pt card 8.6pt in its first
	// frame, which read as a jump at the edge.)
	e := p * p * (3 - 2*p)
	return lerpF(from, to, e)
}

func dragMorphActive(from, to float32, at, now time.Time, reduce bool) bool {
	if reduce || at.IsZero() || from == to {
		return false
	}
	return now.Sub(at) < durPinMorph
}

type dragCardVisual struct {
	srcW, srcH float32
	w, h       float32
	radius     float32
	scale      float32
	curGrabX   float32
	curGrabY   float32
	shadowA    float32
	borderA    float32
	bg         ui.Color
}

func computeDragCardVisual(spec layout.Card, cardW int, grabDX, grabDY, liftK, morph float32, reduceMotion bool) dragCardVisual {
	srcW, srcH := toolColW(spec.Cols()), toolRowH(spec.Height())
	gx, gy := pinGrabOffset(grabDX, grabDY, srcW, srcH, cardW)
	liftedScale := float32(1)
	if !reduceMotion {
		liftedScale = 1 + 0.04*liftK
	}
	return dragCardVisual{
		srcW:     srcW,
		srcH:     srcH,
		w:        lerpF(srcW, float32(cardW), morph),
		h:        lerpF(srcH, float32(pinCardH), morph),
		radius:   lerpF(rCard, 18, morph),
		scale:    lerpF(liftedScale, 1, morph),
		curGrabX: lerpF(grabDX, gx, morph),
		curGrabY: lerpF(grabDY, gy, morph),
		shadowA:  0.42 * liftK * (1 - morph),
		borderA:  0.14 * morph,
		bg:       lerpColor(liftedCardFill, surface, morph),
	}
}

func drawDragCard(c *ui.Context, a *shelf, spec layout.Card, key string, cardW int, left, top float32, v dragCardVisual, morph, tilt float32) ui.Element {
	card := ui.Column(c.Key(key)).Absolute().Left(left).Top(top).
		Width(v.w).Height(v.h).Radius(v.radius).Background(v.bg).Clip().IgnoreInput(true)
	if v.borderA > 0.001 {
		card.Border(0.5, ui.RGBA(255, 255, 255, v.borderA))
	}
	if v.shadowA > 0.001 {
		card.Shadow(0, 14*(1-morph), 28*(1-morph), 0, ui.RGBA(0, 0, 0, v.shadowA))
	}
	if v.scale != 1 || tilt != 0 {
		card.Scale(v.scale, v.scale).Rotate(tilt).TransformOrigin(0.5, 0.5)
	}
	card.Children(func() {
		if morph < 0.999 {
			ex := (v.w - v.srcW) / 2
			ey := (v.h - v.srcH) / 2
			editWrap := ui.Column(c.Key("drag-edit")).Absolute().Left(ex).Top(ey).
				Width(v.srcW).Height(v.srcH).Opacity(1 - morph)
			a.toolCardDragBody(c, editWrap, spec, cardEdit)
		}
		if morph > 0.001 {
			dx := (v.w - float32(cardW)) / 2
			dy := (v.h - float32(pinCardH)) / 2
			deskWrap := ui.Column(c.Key("drag-desk")).Absolute().Left(dx).Top(dy).
				Width(float32(cardW)).Height(pinCardH).Background(fill1).Opacity(morph)
			a.toolCardDragBody(c, deskWrap, spec, cardDesktop)
		}
	})
	return card
}

func (it *pinItem) morphVal(now time.Time) float32 {
	if it.srcW <= 0 {
		return 1
	}
	return dragMorphValue(it.morphFrom, it.morphTo, it.morphAt, now, it.reduceMotion)
}

func (it *pinItem) morphActive(now time.Time) bool {
	if it.srcW <= 0 {
		return false
	}
	return dragMorphActive(it.morphFrom, it.morphTo, it.morphAt, now, it.reduceMotion)
}

func (it *pinItem) dropPreviewPaddingNow() {
	if it.padL == 0 && it.padT == 0 {
		return
	}
	area := pinArea(it.display)
	cardRect := mygo.Rectangle{
		X:      it.frame.X + it.padL,
		Y:      it.frame.Y + it.padT,
		Width:  it.cardW,
		Height: pinCardH,
	}
	it.padL, it.padT, it.subX, it.subY = 0, 0, 0, 0
	it.frame = pinClamp(cardRect, area)
	it.applyFrame()
	it.invalidate()
}

// syncDragPreview shows, moves, or closes the preview window for a card of
// span columns and cardW pixels grabbed at grabDX/grabDY. inside means the
// pointer is still inside the panel shape (with hysteresis), where the panel
// itself draws the card and no desktop preview window opens.
func (a *shelf) syncDragPreview(pv *dragPreview, id string, span, cardW int, grabDX, grabDY, srcW, srcH float32, cursor mygo.Point, inside bool, tilt float32) {
	a.syncDragPreviewMorph(pv, layout.Card{ID: id, Span: span, Rows: 1}, cardW, grabDX, grabDY, srcW, srcH, cursor, inside, tilt, 1, 1, time.Time{}, 1, false)
}

func (a *shelf) syncDragPreviewMorph(pv *dragPreview, spec layout.Card, cardW int, grabDX, grabDY, srcW, srcH float32, cursor mygo.Point, inside bool, tilt, morphFrom, morphTo float32, morphAt time.Time, liftK float32, reduceMotion bool) {
	if inside || a.winH == 0 {
		pv.close()
		return
	}
	now := time.Now()
	m := dragMorphValue(morphFrom, morphTo, morphAt, now, reduceMotion)
	v := computeDragCardVisual(spec, cardW, grabDX, grabDY, liftK, m, reduceMotion)
	display := displayIDAt(cursor, 0)
	area := pinArea(display)
	cardLeft := float32(cursor.X) - v.curGrabX
	cardTop := float32(cursor.Y) - v.curGrabY
	clamped := pinClamp(mygo.Rectangle{
		X:      int(math.Round(float64(cardLeft))),
		Y:      int(math.Round(float64(cardTop))),
		Width:  int(math.Ceil(float64(v.w))),
		Height: int(math.Ceil(float64(v.h))),
	}, area)
	subX := cardLeft - float32(int(math.Round(float64(cardLeft))))
	subY := cardTop - float32(int(math.Round(float64(cardTop))))
	if clamped.X != int(math.Round(float64(cardLeft))) {
		subX = 0
	}
	if clamped.Y != int(math.Round(float64(cardTop))) {
		subY = 0
	}

	var frame mygo.Rectangle
	var padL, padT int
	if m < 0.999 {
		padL, padT = dragPreviewPadL, dragPreviewPadT
		winW := int(math.Ceil(float64(max(srcW, float32(cardW))))) + dragPreviewPadL + dragPreviewPadR
		winH := int(math.Ceil(float64(max(srcH, float32(pinCardH))))) + dragPreviewPadT + dragPreviewPadB
		frame = mygo.Rectangle{
			X:      clamped.X - padL,
			Y:      clamped.Y - padT,
			Width:  winW,
			Height: winH,
		}
	} else {
		frame = pinClamp(pinFrameUnder(cursor, grabDX, grabDY, srcW, srcH, cardW), area)
		subX, subY = 0, 0
	}

	if pv.it == nil {
		pv.it = &pinItem{
			owner: a, cardID: spec.ID, span: spec.Cols(), rowsSpan: spec.Height(), cardW: cardW,
			display: display, edge: pinFree, expanded: true, preview: true, tilt: tilt, frame: frame,
			srcW: srcW, srcH: srcH, grabDX: grabDX, grabDY: grabDY,
			morphFrom: morphFrom, morphTo: morphTo, morphAt: morphAt,
			liftK: liftK, reduceMotion: reduceMotion,
			padL: padL, padT: padT, subX: subX, subY: subY,
		}
		pinOpenPreview(pv.it)
		return
	}
	prevFrame, prevTilt := pv.it.frame, pv.it.tilt
	prevSubX, prevSubY := pv.it.subX, pv.it.subY
	pv.it.display, pv.it.frame, pv.it.tilt = display, frame, tilt
	pv.it.cardW, pv.it.span = cardW, spec.Cols()
	pv.it.rowsSpan = spec.Height()
	pv.it.srcW, pv.it.srcH = srcW, srcH
	pv.it.grabDX, pv.it.grabDY = grabDX, grabDY
	pv.it.morphFrom, pv.it.morphTo, pv.it.morphAt = morphFrom, morphTo, morphAt
	pv.it.liftK, pv.it.reduceMotion = liftK, reduceMotion
	pv.it.padL, pv.it.padT, pv.it.subX, pv.it.subY = padL, padT, subX, subY
	if pv.it.frame != prevFrame {
		pv.it.applyFrame()
	}
	if pv.it.tilt != prevTilt || pv.it.subX != prevSubX || pv.it.subY != prevSubY || pv.it.morphActive(now) {
		pv.it.invalidate()
	}
}

func (p *toolsPage) closeDragPreview() { p.preview.closeAll() }

// Events carry the pointer's actual location. A cursor poll can lag the
// event during fast direction changes, especially while a window is moving.
func (p *toolsPage) toolPointer(ev ui.InputEvent) mygo.Point {
	d := p.press
	if d == nil {
		return mygo.Point{}
	}
	sx, sy := d.cardLocalToStage(ev.X, ev.Y)
	x := p.viewport.X + sx
	y := p.viewport.Y + sy - p.scroll.Y
	return mygo.Point{
		X: p.host.frame.X + int(math.Round(float64(x))),
		Y: p.host.frame.Y + int(math.Round(float64(y))),
	}
}

// syncDragPreview decides between the panel-drawn lifted card and the desktop
// preview window using the panel's drawn shell shape (with hysteresis on exit).
func (p *toolsPage) syncDragPreview(cursor mygo.Point) {
	p.syncDragPreviewAt(cursor, time.Now())
}

func (p *toolsPage) syncDragPreviewAt(cursor mygo.Point, now time.Time) {
	a, d := p.host, p.press
	if d == nil || !d.dragging || a.winH == 0 || (a.win == nil && !allowHeadlessPreview) {
		p.preview.closeAll()
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	if p.editing && p.overLibrary() {
		if it := p.preview.window(); it != nil {
			d.setMorph(it.morphVal(now), 0, now)
			p.preview.demote()
		} else if d.morphTo != 0 {
			d.setMorph(d.dragMorphAt(now), 0, now)
		}
		if a.nearShellEdge(cursor, dragPreviewWarmDist) || !a.pointerInPanelForDrag(cursor, 0) {
			p.armDragPreview(d, cursor, now)
		} else if p.preview.window() == nil {
			p.preview.dropArmed()
		}
		return
	}
	wasOutside := p.preview.window() != nil
	pad := dragPreviewOutHysteresis
	if wasOutside {
		pad = 0
	}
	inside := a.pointerInPanelForDrag(cursor, pad)
	if inside {
		if wasOutside {
			d.setMorph(p.preview.it.morphVal(now), 0, now)
			p.preview.close()
		} else if d.morphTo != 0 {
			d.setMorph(d.dragMorphAt(now), 0, now)
		}
		if a.nearShellEdge(cursor, dragPreviewWarmDist) || !a.pointerInPanelForDrag(cursor, 0) {
			p.armDragPreview(d, cursor, now)
		} else if !wasOutside {
			p.preview.dropArmed()
		}
		return
	}
	if !wasOutside {
		d.setMorph(d.dragMorphAt(now), 1, now)
		// Put the invisible window on this event's pointer before raising
		// its opacity, so the first composited frame is already here.
		p.armDragPreview(d, cursor, now)
		p.preview.promote()
	}
	spec := p.dragSpec(d.id)
	w, h := toolColW(spec.Cols()), toolRowH(spec.Height())
	cardW := pinCardWidth(d.id, spec.Cols())
	tilt := p.dragTilt(now)
	if p.reduceMotion {
		tilt = 0
	}
	liftK := d.liftProgress(now, p.reduceMotion)
	a.syncDragPreviewMorph(&p.preview, spec, cardW, d.grabDX, d.grabDY, w, h, cursor, false, tilt, d.morphFrom, d.morphTo, d.morphAt, liftK, p.reduceMotion)
	if pv := p.preview.window(); pv != nil {
		d.visReady = true
		d.visWinX = float32(pv.frame.X+pv.padL-a.frame.X) + pv.subX
		d.visWinY = float32(pv.frame.Y+pv.padT-a.frame.Y) + pv.subY
		v := computeDragCardVisual(spec, cardW, d.grabDX, d.grabDY, liftK, pv.morphVal(now), p.reduceMotion)
		d.visW, d.visH, d.visScale, d.visRot = v.w, v.h, v.scale, tilt
	}
}

// armDragPreview keeps the invisible preview window on the same card the
// panel is drawing, so crossing the edge only raises its opacity. The window
// renders at zero opacity throughout the panel phase and never takes input.
func (p *toolsPage) armDragPreview(d *toolPress, cursor mygo.Point, now time.Time) {
	a := p.host
	if p.preview.armed == nil {
		it := &pinItem{owner: a, preview: true, expanded: true, cardID: d.id, srcW: 1, srcH: 1,
			frame: mygo.Rectangle{Width: pinCardW, Height: pinCardH}}
		pinOpenPreviewInvisible(it)
		p.preview.armed = it
	}
	spec := p.dragSpec(d.id)
	w, h := toolColW(spec.Cols()), toolRowH(spec.Height())
	cardW := pinCardWidth(d.id, spec.Cols())
	tilt := p.dragTilt(now)
	if p.reduceMotion {
		tilt = 0
	}
	liftK := d.liftProgress(now, p.reduceMotion)
	// syncDragPreviewMorph works on the active slot; lend it the armed item
	// and take it back afterwards.
	it := p.preview.armed
	p.preview.it, p.preview.armed = it, nil
	a.syncDragPreviewMorph(&p.preview, spec, cardW, d.grabDX, d.grabDY, w, h, cursor, false, tilt, d.morphFrom, d.morphTo, d.morphAt, liftK, p.reduceMotion)
	p.preview.armed, p.preview.it = p.preview.it, nil
	if p.preview.armed == nil {
		p.preview.armed = it
	}
	if p.preview.armed.win != nil {
		p.preview.armed.win.SetOpacity(0)
	}
}
