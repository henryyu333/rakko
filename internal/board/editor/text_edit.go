package editor

import (
	"math"
	"slices"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

const labelPadding = 10.0

func textEditable(s *doc.Shape) bool {
	if s == nil || s.Locked {
		return false
	}
	switch s.Type {
	case doc.TypeGeo, doc.TypeText, doc.TypeNote, doc.TypeArrow, doc.TypeFrame:
		return true
	}
	return false
}

func (e *Editor) createTextAndEdit(pagePt geo.Vec) doc.ID {
	e.begin()
	id := doc.NewRecordID("shape")
	s := doc.NewShape(id, doc.TypeText)
	e.applyNextStyle(s)
	s.Text = ""
	s.AutoSize = true
	s.Seed = geo.Hash(string(id))
	h := textFontSize(s) * 1.3
	e.placeAt(s, geo.V(pagePt.X, pagePt.Y-h/2))
	e.put(s)
	e.Select(id)
	e.StartEditing(id)
	return id
}

func (e *Editor) createNoteAndEdit(pagePt geo.Vec) doc.ID {
	e.begin()
	id := doc.NewRecordID("shape")
	s := doc.NewShape(id, doc.TypeNote)
	e.applyNextStyle(s)
	s.Text = ""
	s.Seed = geo.Hash(string(id))
	e.placeAt(s, geo.V(pagePt.X-s.W/2, pagePt.Y-s.H/2))
	e.put(s)
	e.Select(id)
	e.StartEditing(id)
	return id
}

// EnterEdit starts inline editing for the single selected editable shape.
func (e *Editor) EnterEdit() bool {
	if len(e.sel) != 1 || e.Editing != "" {
		return false
	}
	s := e.Doc.Shapes[e.sel[0]]
	if !textEditable(s) {
		return false
	}
	e.StartEditing(s.ID)
	return true
}

// StartEditing opens inline text/label editing on shape id.
func (e *Editor) StartEditing(id doc.ID) {
	s := e.Doc.Shapes[id]
	if !textEditable(s) {
		return
	}
	e.begin()
	e.Editing = id
	if s.Type == doc.TypeFrame {
		e.EditText = s.Name
	} else {
		e.EditText = s.Text
	}
	e.Hovered = ""
}

// EditChanged applies the live EditText buffer to the shape being edited and updates GrowY.
func (e *Editor) EditChanged() {
	id := e.Editing
	if id == "" {
		return
	}
	s := e.Doc.Shapes[id]
	if s == nil {
		return
	}
	text := e.EditText
	e.update(id, func(c *doc.Shape) {
		if c.Type == doc.TypeFrame {
			c.Name = text
			return
		}
		c.Text = text
		e.fitLabel(c)
	})
	// Inline edits stay in one open transaction, but the document now holds the
	// text: notify so debounced autosave and flush see it before Esc is pressed.
	e.emitChange()
}

func (e *Editor) fitLabel(s *doc.Shape) {
	switch s.Type {
	case doc.TypeGeo:
		if s.Text == "" {
			s.GrowY = 0
			return
		}
		size := labelFontSize(s)
		availW := math.Max(s.W-2*labelPadding, 8)
		b := e.measure(s.Text, s.Font, size, availW)
		s.GrowY = math.Max(0, b.H+2*labelPadding-s.H)
	case doc.TypeNote:
		if s.Text == "" {
			s.GrowY = 0
			return
		}
		size := labelFontSize(s)
		w := math.Max(s.W, 80)
		h := math.Max(s.H, 60)
		availW := math.Max(w-2*labelPadding, 8)
		b := e.measure(s.Text, s.Font, size, availW)
		s.GrowY = math.Max(0, b.H+2*labelPadding-h)
	}
}

// StopEditing commits the active inline edit (or deletes an empty TypeText shape).
func (e *Editor) StopEditing() {
	id := e.Editing
	if id == "" {
		return
	}
	e.Editing = ""
	s := e.Doc.Shapes[id]
	if s != nil && s.Type == doc.TypeText && len(s.Text) == 0 {
		createdNow := e.tx != nil && e.tx.shapes[id].before == nil
		if createdNow {
			e.rollback()
			e.sel = slices.DeleteFunc(e.sel, func(x doc.ID) bool { return x == id })
			return
		}
		e.deleteTree(id)
		e.sel = slices.DeleteFunc(e.sel, func(x doc.ID) bool { return x == id })
	}
	e.commit()
}

// EditBox returns the local-space box, font styling, and foreground color for the
// shape currently being edited.
func (e *Editor) EditBox() (box geo.Box, size float64, font, align string, bold, italic, underline bool, col Color, ok bool) {
	s := e.Doc.Shapes[e.Editing]
	if s == nil {
		return
	}
	pal := e.Palette()
	font = s.Font
	bold, italic, underline = s.Bold, s.Italic, s.Underline
	switch s.Type {
	case doc.TypeText:
		size = textFontSize(s)
		box = e.Geometry(s).Bounds
		box.H = math.Max(box.H, size*1.3)
		align = s.TextAlign
		if align == "" {
			align = s.Align
		}
		col = ResolveColor(s.Color, pal.Dark, e.Look())
		ok = true
	case doc.TypeNote:
		size = labelFontSize(s)
		g := e.Geometry(s)
		if g.Label != nil {
			box = *g.Label
		}
		box.H = math.Max(box.H, size*1.3)
		align = s.Align
		col = pal.NoteText
		ok = true
	case doc.TypeGeo:
		size = labelFontSize(s)
		w, h := math.Max(s.W, 1), math.Max(s.H+s.GrowY, 1)
		box = *e.labelBox(s, w, h, size)
		box.H = math.Max(box.H, size*1.3)
		align = s.Align
		col = ResolveColor(s.Color, pal.Dark, e.Look())
		ok = true
	case doc.TypeArrow:
		size = labelFontSize(s)
		box = *e.arrowLabelBox(s)
		box.W = math.Max(box.W, 120)
		box.X = box.Center().X - box.W/2
		box.H = math.Max(box.H, size*1.3)
		align = "center"
		col = ResolveColor(s.Color, pal.Dark, e.Look())
		ok = true
	case doc.TypeFrame:
		z := e.Cam().Z
		size = 11.5 / z
		font = "sans"
		bold = false
		italic = false
		underline = false
		box = geo.Box{X: 0, Y: -22 / z, W: math.Max(s.W, 160/z), H: 20 / z}
		align = "left"
		col = pal.Ink
		ok = true
	}
	return
}
