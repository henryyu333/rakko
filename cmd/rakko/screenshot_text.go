//go:build darwin

package main

// The screenshot's text tool: a box drawn on the overlay whose text is
// edited by mygo's own TextArea, so input methods compose (Pinyin and its
// candidates), Enter breaks lines and ⌘A/C/V/X/Z act on the text. The box
// stores only what an export needs — text, top-left, content width, type
// size and colour — and the same wrapping width paints the preview and
// the crop.

import (
	"math"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// shotTextDragMode is which part of a text box a pointer grabs.
type shotTextDragMode int

const (
	shotTextDragMove shotTextDragMode = iota
	shotTextDragLeft
	shotTextDragRight
)

// shotTextDrag is one drag of a text box: moving it, or pulling its left or
// right edge to change the width. index is the annotation being moved, or
// -1 while the box is the one being edited.
type shotTextDrag struct {
	mode    shotTextDragMode
	press   mygo.Point
	at      mygo.Point
	width   int
	index   int
	snapped bool
}

// size is the box's type size, from its stroke preset.
func (te *shotTextEdit) size() float32 { return shotTextSizeFor(te.stroke) }

// shotTextMinWidth is the narrowest content width, in points: two ems.
func shotTextMinWidth(size float32) int {
	return max(24, int(math.Round(float64(size)*2)))
}

// shotTextDefaultWidth is 8 characters wide, where a click creates a box.
func shotTextDefaultWidth(size float32) int {
	return int(math.Round(float64(size) * 8))
}

// The editing frame's inner padding: text never touches the frame, and
// the grips sit on the frame's outside, clear of the text.
const (
	shotTextBoxPadX = 6
	shotTextBoxPadY = 4
)

// shotTextFrameRect is the editing frame around a text's content: the
// content top-left, width and height grown by the padding. The annotation
// stores the content, so an export (which has no frame) lands exactly where
// the editor showed the text.
func shotTextFrameRect(at mygo.Point, width, height int) mygo.Rectangle {
	return mygo.Rectangle{
		X: at.X - shotTextBoxPadX, Y: at.Y - shotTextBoxPadY,
		Width: width + 2*shotTextBoxPadX, Height: height + 2*shotTextBoxPadY,
	}
}

// shotTextGrips are the three width grips: the left and right edges'
// midpoints and the bottom-right corner, centred on the frame's border so
// half of each sits outside it.
func shotTextGrips(frame mygo.Rectangle) []mygo.Rectangle {
	const gw, gh, corner = 8, 20, 8
	midY := frame.Y + frame.Height/2
	right := frame.X + frame.Width
	bottom := frame.Y + frame.Height
	return []mygo.Rectangle{
		{X: frame.X - gw/2, Y: midY - gh/2, Width: gw, Height: gh},
		{X: right - gw/2, Y: midY - gh/2, Width: gw, Height: gh},
		{X: right - corner/2, Y: bottom - corner/2, Width: corner, Height: corner},
	}
}

// textWidthMax is the widest content that still fits right of frameX.
func textWidthMax(sel mygo.Rectangle, frameX int) int {
	return max(1, sel.X+sel.Width-frameX-2*shotTextBoxPadX)
}

// startTextDraft begins a press with the text tool: a click makes the
// default box, a drag sets its width.
func (s *shotSession) startTextDraft(p mygo.Point) {
	s.mode = shotModeDraw
	s.press = p
	s.textDraft = &shotTextDraft{at: p, cur: p}
	s.invalidate()
}

// createTextFromDraft turns the finished press into an editing box.
func (s *shotSession) createTextFromDraft() {
	draft := s.textDraft
	if draft == nil || !s.hasSel {
		return
	}
	size := shotTextSizeFor(s.width)
	frameX := draft.at.X
	frameW := shotTextDefaultWidth(size) + 2*shotTextBoxPadX
	if dx := draft.cur.X - draft.at.X; abs(dx) >= 3 {
		frameW = abs(dx)
		if dx < 0 {
			frameX = draft.cur.X
		}
	}
	maxW := textWidthMax(s.sel, frameX) + 2*shotTextBoxPadX
	frameW = clampInt(frameW, shotTextMinWidth(size)+2*shotTextBoxPadX, maxW)
	frameX = clampInt(frameX, s.sel.X, max(s.sel.X, s.sel.X+s.sel.Width-frameW))
	frameY := clampInt(draft.at.Y, s.sel.Y, max(s.sel.Y, s.sel.Y+s.sel.Height-1))
	s.text = &shotTextEdit{
		at:    mygo.Point{X: frameX + shotTextBoxPadX, Y: frameY + shotTextBoxPadY},
		width: frameW - 2*shotTextBoxPadX, color: s.color, stroke: s.width, focus: true,
	}
	s.lowerForText()
	s.textBoxH = 0
}

// editText opens a finished annotation for editing with its own width,
// style and text; the caret goes to the end.
func (s *shotSession) editText(index int) {
	if index < 0 || index >= len(s.hist.annots) {
		return
	}
	an := s.hist.annots[index]
	if an.tool != shotToolText {
		return
	}
	width := an.boxW
	if width <= 0 {
		width = shotTextDefaultWidth(shotAnnotationTextSize(an))
	}
	s.text = &shotTextEdit{
		at: an.a, width: width, text: an.text, color: an.color,
		stroke: an.width, index: index + 1, orig: an, focus: true,
	}
	s.lowerForText()
	s.textBoxH = 0
	s.color, s.width = an.color, an.width
	s.textMoveReset()
	s.invalidate()
}

// finishText completes the box: empty new text disappears without a history
// step, an edited box replaces its annotation, and an unchanged one leaves
// the history alone.
func (s *shotSession) finishText() {
	te := s.text
	if te == nil {
		return
	}
	s.text = nil
	s.textBoxH = 0
	s.textDrag = nil
	s.textDraft = nil
	s.restoreTextLevel()
	text := te.text
	nonEmpty := strings.TrimSpace(text) != ""
	switch {
	case te.index > 0:
		idx := te.index - 1
		if idx >= len(s.hist.annots) {
			break
		}
		if !nonEmpty {
			s.hist.snap()
			s.hist.annots = append(s.hist.annots[:idx], s.hist.annots[idx+1:]...)
		} else {
			an := shotAnnotation{tool: shotToolText, color: te.color, width: te.stroke,
				a: te.at, text: text, boxW: te.width}
			if !shotSameText(an, te.orig) {
				s.hist.snap()
				s.hist.annots[idx] = an
			}
		}
	case nonEmpty:
		s.hist.snap()
		s.hist.push(shotAnnotation{tool: shotToolText, color: te.color, width: te.stroke,
			a: te.at, text: text, boxW: te.width})
	}
	s.focusRoot = true
	s.layoutBar()
	s.invalidate()
}

// shotSameText reports whether two text annotations carry the same content
// and style, so leaving an editor without a change makes no undo step.
func shotSameText(a, b shotAnnotation) bool {
	return a.tool == b.tool && a.color == b.color && a.width == b.width &&
		a.a == b.a && a.text == b.text && a.boxW == b.boxW
}

// measureTexts caches every finished text's box for hit testing. The
// editing box is measured separately as it is built.
func (s *shotSession) measureTexts(c *ui.Context) {
	s.textRects = s.textRects[:0]
	for i, an := range s.hist.annots {
		if an.tool != shotToolText || an.text == "" {
			continue
		}
		if s.text != nil && s.text.index == i+1 {
			continue
		}
		w := float32(an.boxW)
		tw, th := c.MeasureText(w, ui.Span{Text: an.text, Size: shotAnnotationTextSize(an)})
		if an.boxW <= 0 {
			w = tw
		}
		s.textRects = append(s.textRects, shotTextRect{index: i,
			rect: mygo.Rectangle{X: an.a.X, Y: an.a.Y, Width: int(math.Ceil(float64(w))),
				Height: int(math.Ceil(float64(th)))}})
	}
}

// textAt returns the topmost finished text under p.
func (s *shotSession) textAt(p mygo.Point) (int, bool) {
	for i := len(s.textRects) - 1; i >= 0; i-- {
		if shotPointInRect(p, s.textRects[i].rect) {
			return s.textRects[i].index, true
		}
	}
	return 0, false
}

// textHeight is the editing box's height: measured from its wrapped text.
func (s *shotSession) textHeight(te *shotTextEdit) int {
	if s.textBoxH > 0 {
		return s.textBoxH
	}
	return int(math.Ceil(float64(te.size()) * 1.4))
}

// startTextMove grabs a finished text for moving; the drag snaps the
// history only on its first actual movement.
func (s *shotSession) startTextMove(index int, p mygo.Point) {
	an := s.hist.annots[index]
	s.textDrag = &shotTextDrag{
		mode: shotTextDragMove, press: p, at: an.a, width: an.boxW, index: index,
	}
	s.mode = shotModeMove
}

// textMoveReset clears any text drag.
func (s *shotSession) textMoveReset() {
	s.textDrag = nil
}

// beginTextDrag starts moving an editing box or pulling one of its width
// edges.
func (s *shotSession) beginTextDrag(mode shotTextDragMode, p mygo.Point) {
	te := s.text
	if te == nil {
		return
	}
	s.textDrag = &shotTextDrag{mode: mode, press: p, at: te.at, width: te.width, index: -1}
}

// textDragTo applies the current drag at p. Editing boxes stay inside the
// selection; a moved finished annotation records one history step.
func (s *shotSession) textDragTo(p mygo.Point, area mygo.Rectangle) {
	drag := s.textDrag
	if drag == nil {
		return
	}
	dx, dy := p.X-drag.press.X, p.Y-drag.press.Y
	te := s.text
	if drag.index >= 0 && (dx != 0 || dy != 0) && !drag.snapped {
		s.hist.snap()
		drag.snapped = true
	}
	switch drag.mode {
	case shotTextDragMove:
		at := mygo.Point{X: drag.at.X + dx, Y: drag.at.Y + dy}
		if drag.index >= 0 && drag.index < len(s.hist.annots) {
			an := s.hist.annots[drag.index]
			w, h := an.boxW, shotTextDefaultWidth(shotAnnotationTextSize(an))/2
			if w <= 0 {
				w = shotTextDefaultWidth(shotAnnotationTextSize(an))
			}
			if r, ok := s.textRectOf(drag.index); ok {
				h = r.rect.Height
			}
			at.X = clampInt(at.X, s.sel.X+shotTextBoxPadX, max(s.sel.X+shotTextBoxPadX, s.sel.X+s.sel.Width-shotTextBoxPadX-w))
			at.Y = clampInt(at.Y, s.sel.Y+shotTextBoxPadY, max(s.sel.Y+shotTextBoxPadY, s.sel.Y+s.sel.Height-shotTextBoxPadY-h))
			if drag.snapped {
				s.hist.annots[drag.index].a = at
			}
			break
		}
		h := s.textHeight(te)
		te.at.X = clampInt(at.X, s.sel.X+shotTextBoxPadX, max(s.sel.X+shotTextBoxPadX, s.sel.X+s.sel.Width-shotTextBoxPadX-te.width))
		te.at.Y = clampInt(at.Y, s.sel.Y+shotTextBoxPadY, max(s.sel.Y+shotTextBoxPadY, s.sel.Y+s.sel.Height-shotTextBoxPadY-h))
	case shotTextDragLeft:
		right := drag.at.X + drag.width + shotTextBoxPadX
		minFrame := shotTextMinWidth(te.size()) + 2*shotTextBoxPadX
		left := clampInt(p.X, s.sel.X, max(s.sel.X, right-minFrame))
		te.at.X = left + shotTextBoxPadX
		te.width = right - left - 2*shotTextBoxPadX
	case shotTextDragRight:
		minFrame := shotTextMinWidth(te.size()) + 2*shotTextBoxPadX
		frameX := te.at.X - shotTextBoxPadX
		right := clampInt(p.X, frameX+minFrame, s.sel.X+s.sel.Width)
		te.width = right - frameX - 2*shotTextBoxPadX
	}
	s.invalidate()
}

// textRectOf returns the cached box of annotation index.
func (s *shotSession) textRectOf(index int) (shotTextRect, bool) {
	for _, tr := range s.textRects {
		if tr.index == index {
			return tr, true
		}
	}
	return shotTextRect{}, false
}

// buildTextEditor puts the real text control over the frozen picture at
// the box's place, then the invisible strips that move or resize the box.
func (s *shotSession) buildTextEditor(c *ui.Context, d *shotDisplay) {
	te := s.text
	size := te.size()
	w := float32(te.width)
	_, h := c.MeasureText(w, ui.Span{Text: te.text, Size: size})
	s.textBoxH = max(int(math.Ceil(float64(h))), int(math.Ceil(float64(size)*1.2)))
	area := ui.TextAreaBase(c.Key("shot-text-area"), &te.text).
		Absolute().
		Left(float32(te.at.X - d.Bounds.X)).Top(float32(te.at.Y - d.Bounds.Y)).
		Width(w).Height(float32(s.textBoxH)).
		FontSize(size).TextColor(te.color).Padding(0).
		Background(ui.Transparent).Cursor(ui.CursorText).
		Label("")
	if te.focus {
		// A nonactivating panel must be made key explicitly before the
		// input method takes over.
		if d.win != nil {
			shotActivateSelf()
			d.win.Focus()
		}
		n := len([]rune(te.text))
		area.Focus().SetTextSelection(n, n)
		te.focus = false
	}
	area.OnTextKey(func(k ui.TextKey) bool {
		if k.Key == ui.KeyEscape || k.Key == ui.KeyEnter && k.Mods&ui.Cmd != 0 {
			te.text = k.Text()
			s.finishText()
			return true
		}
		// Enter, Space and the arrows belong to the input method while
		// it composes Pinyin.
		return false
	})
	s.buildTextChrome(c, d)
}

// buildTextChrome builds the invisible edge strips and the visible grips'
// hit targets: dragging an edge moves the box, a grip changes its width.
func (s *shotSession) buildTextChrome(c *ui.Context, d *shotDisplay) {
	te := s.text
	r := shotTextFrameRect(te.at, te.width, s.textHeight(te))
	const edge = 4
	grips := shotTextGrips(r)
	type strip struct {
		key  string
		rect mygo.Rectangle
		cur  ui.Cursor
		mode shotTextDragMode
	}
	strips := []strip{
		{"move-n", mygo.Rectangle{X: r.X, Y: r.Y - edge/2, Width: r.Width, Height: edge}, ui.CursorMove, shotTextDragMove},
		{"move-s", mygo.Rectangle{X: r.X, Y: r.Y + r.Height - edge/2, Width: r.Width, Height: edge}, ui.CursorMove, shotTextDragMove},
		{"move-w", mygo.Rectangle{X: r.X - edge/2, Y: r.Y, Width: edge, Height: r.Height}, ui.CursorMove, shotTextDragMove},
		{"move-e", mygo.Rectangle{X: r.X + r.Width - edge/2, Y: r.Y, Width: edge, Height: r.Height}, ui.CursorMove, shotTextDragMove},
		{"grip-w", grips[0], ui.CursorResizeEW, shotTextDragLeft},
		{"grip-e", grips[1], ui.CursorResizeEW, shotTextDragRight},
		{"grip-se", grips[2], ui.CursorResizeNWSE, shotTextDragRight},
	}
	for _, st := range strips {
		local := st.rect
		b := ui.Box(c.Key("shot-text-"+st.key)).Absolute().
			Left(float32(local.X-d.Bounds.X)).Top(float32(local.Y-d.Bounds.Y)).
			Size(float32(local.Width), float32(local.Height)).
			Background(ui.Transparent).Cursor(st.cur).KeepFocus()
		if st.mode != shotTextDragMove {
			// Grips paint over the control so its scrollbar or glyphs
			// never hide them.
			b.Draw(func(p *ui.Painter, r ui.Rect) {
				p.Fill(r, ui.Hex("#FFFFFF"), 2)
				p.Stroke(r, accent, 2, 1)
			})
		}
		mode, origin := st.mode, local
		b.HandleInput(func(ev ui.InputEvent) bool {
			p := mygo.Point{X: origin.X + int(ev.X+0.5), Y: origin.Y + int(ev.Y+0.5)}
			switch ev.Kind {
			case ui.InputPointerDown:
				if ev.Button != 0 {
					return false
				}
				s.beginTextDrag(mode, p)
				return true
			case ui.InputPointerMove:
				if s.textDrag != nil {
					s.textDragTo(p, d.Bounds)
					return true
				}
			case ui.InputPointerUp:
				if s.textDrag != nil {
					s.textDrag = nil
					return true
				}
			}
			return false
		})
	}
}

// drawTextFrame paints the editing box: a faint dark fill, a hairline
// border and the three width grips (left, right and bottom-right). The
// frame is overlay chrome only; an export carries no box.
func (s *shotSession) drawTextFrame(p *ui.Painter, d *shotDisplay) {
	if s.textDraft != nil {
		draft := s.textDraft
		size := shotTextSizeFor(s.width)
		width := shotTextDefaultWidth(size)
		x := draft.at.X
		if dx := draft.cur.X - draft.at.X; abs(dx) >= 3 {
			width = abs(dx)
			x = min(draft.at.X, draft.cur.X)
		}
		r := ui.Rect{
			X: float32(x - d.Bounds.X), Y: float32(draft.at.Y - d.Bounds.Y),
			W: float32(width), H: float32(max(size*1.4, 12)),
		}
		p.Fill(r, ui.RGBA(0, 0, 0, 0.10), 2)
		p.StrokeDashed(r, accent, 2, 1)
		return
	}
	te := s.text
	if te == nil {
		return
	}
	frame := shotTextFrameRect(te.at, te.width, s.textHeight(te))
	r := ui.Rect{
		X: float32(frame.X - d.Bounds.X), Y: float32(frame.Y - d.Bounds.Y),
		W: float32(frame.Width), H: float32(frame.Height),
	}
	p.Fill(r, ui.RGBA(0, 0, 0, 0.10), 2)
	p.Stroke(r, ui.RGBA(0, 0, 0, 0.35), 2, 1)
	p.Stroke(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}, ui.RGBA(255, 255, 255, 0.75), 2, 1)
}
