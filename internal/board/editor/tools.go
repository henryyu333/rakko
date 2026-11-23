package editor

import (
	"math"
	"slices"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

// Tool identifies the active canvas tool.
type Tool string

const (
	ToolSelect    Tool = "select"
	ToolLasso     Tool = "lasso"
	ToolHand      Tool = "hand"
	ToolDraw      Tool = "draw"
	ToolHighlight Tool = "highlight"
	ToolEraser    Tool = "eraser"
	ToolGeo       Tool = "geo"
	ToolFrame     Tool = "frame"
	ToolText      Tool = "text"
	ToolNote      Tool = "note"
	ToolArrow     Tool = "arrow"
	ToolLine      Tool = "line"
	ToolLaser     Tool = "laser"
)

// EraserMode selects whether the eraser removes whole objects or clips ink locally.
type EraserMode string

const (
	EraserObject  EraserMode = "object"
	EraserPartial EraserMode = "partial"
)

// Cursor indicates the pointer cursor requested by the editor.
type Cursor int

const (
	CursorDefault Cursor = iota
	CursorCross
	CursorText
	CursorGrab
	CursorGrabbing
	CursorPointer
	CursorMove
	CursorEW
	CursorNS
	CursorNESW
	CursorNWSE
	CursorRotate
)

// Mods holds keyboard modifier flags for pointer/tool interactions.
type Mods struct {
	Shift, Alt, Cmd, Ctrl bool
}

// Pointer records the latest pointer state in screen and page space.
type Pointer struct {
	Screen, Page         geo.Vec
	DownScreen, DownPage geo.Vec
	Down                 bool
	Button               int
	Pressure             float64
	IsPen                bool
	Mods                 Mods
	Time                 float64
}

// Style holds the defaults applied to newly drawn shapes.
type Style struct {
	Stroke     string
	FillColor  string
	Fill       string
	Size       string
	Dash       string
	Opacity    float64
	Font       string
	FontSize   int
	Align      string
	VAlign     string
	TextAlign  string
	Bold       bool
	Italic     bool
	Underline  bool
	Geo        string
	PenKind    string
	PenWidth   float64
	EraserMode EraserMode
}

// DefaultStyle returns the default drawing style.
func DefaultStyle() Style {
	return Style{
		Stroke:     "#E8E8EA",
		FillColor:  "#60A5FA",
		Fill:       "none",
		Size:       "m",
		Dash:       "solid",
		Opacity:    1,
		Font:       "sketch",
		FontSize:   18,
		Align:      "center",
		VAlign:     "middle",
		TextAlign:  "left",
		Geo:        "rectangle",
		PenKind:    "normal",
		PenWidth:   2.5,
		EraserMode: EraserObject,
	}
}

// TrailPoint is a timestamped page-space point for eraser/laser trails.
type TrailPoint struct {
	P geo.Vec
	T float64
}

// Trail is a transient overlay stroke (eraser sweep or laser pointer).
type Trail struct {
	Points []TrailPoint
	Laser  bool
	Live   bool
}

type gesture interface {
	move(e *Editor)
	up(e *Editor)
	cancel(e *Editor)
}

const dragThresholdDIP = 4.0

func (e *Editor) dragged() bool {
	return e.pointer.Screen.Dist(e.pointer.DownScreen) > dragThresholdDIP
}

func (e *Editor) margin() float64 {
	return 8.0 / e.Cam().Z
}

// Selection returns the currently selected shape IDs.
func (e *Editor) Selection() []doc.ID {
	return slices.Clone(e.sel)
}

// IsSelected reports whether id is selected.
func (e *Editor) IsSelected(id doc.ID) bool {
	return slices.Contains(e.sel, id)
}

// Select replaces the selection with ids (filtering missing shapes).
func (e *Editor) Select(ids ...doc.ID) {
	e.sel = e.sel[:0]
	for _, id := range ids {
		if s := e.Doc.Shapes[id]; s != nil && !slices.Contains(e.sel, id) {
			e.sel = append(e.sel, id)
		}
	}
}

// SelectNone clears the current selection.
func (e *Editor) SelectNone() {
	e.sel = nil
}

// SelectAll selects all unlocked shapes on the active page.
func (e *Editor) SelectAll() {
	var all []doc.ID
	for _, id := range e.Children(e.Page) {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			all = append(all, id)
		}
	}
	e.Select(all...)
}

// SelectionBox returns the combined bounding box of the current selection in page space.
func (e *Editor) SelectionBox() (geo.Box, bool) {
	if len(e.sel) == 0 {
		return geo.Box{}, false
	}
	var box geo.Box
	first := true
	for _, id := range e.sel {
		if e.Doc.Shapes[id] == nil {
			continue
		}
		b := e.PageBounds(id)
		if first {
			box, first = b, false
		} else {
			box = box.Union(b)
		}
	}
	return box, !first
}

// SetTool activates tool t, canceling any active gesture or edit.
func (e *Editor) SetTool(t Tool) {
	e.StopEditing()
	e.cancelGesture()
	e.Tool = t
	if t != ToolSelect {
		e.Hovered = ""
		if t != ToolHand && t != ToolLaser && t != ToolLasso {
			e.SelectNone()
		}
	}
}

// SetGeoTool activates ToolGeo with shape kind.
func (e *Editor) SetGeoTool(kind string) {
	norm := normalizeGeoKind(kind)
	if IsValidGeoKind(norm) {
		e.Next.Geo = norm
	}
	e.SetTool(ToolGeo)
}

// SetEraserMode sets Object or Partial eraser mode.
func (e *Editor) SetEraserMode(mode EraserMode) {
	if mode == EraserObject || mode == EraserPartial {
		e.Next.EraserMode = mode
	}
}

// SetPenKind sets the freehand brush style ("normal", "pen", "marker").
func (e *Editor) SetPenKind(kind string) {
	switch kind {
	case "normal", "pen", "marker":
		e.Next.PenKind = kind
	}
}

func (e *Editor) finishTool() {
	if !e.ToolLocked {
		e.Tool = ToolSelect
	}
}

// SetSpace updates whether Space is held for temporary panning.
func (e *Editor) SetSpace(down bool) { e.spaceHeld = down }

// SpaceHeld reports whether Space is held.
func (e *Editor) SpaceHeld() bool { return e.spaceHeld }

// InGesture reports whether a pointer gesture is currently active.
func (e *Editor) InGesture() bool { return e.gesture != nil }

// CursorNow returns the semantic cursor for the current tool/gesture.
func (e *Editor) CursorNow() Cursor {
	switch g := e.gesture.(type) {
	case *panGesture:
		return CursorGrabbing
	case *resizeGesture:
		return resizeCursor(g.handle.Which, g.rotation)
	case *rotateGesture:
		return CursorRotate
	}
	if e.spaceHeld || e.Tool == ToolHand {
		return CursorGrab
	}
	switch e.Tool {
	case ToolText:
		return CursorText
	case ToolLasso, ToolDraw, ToolHighlight, ToolEraser, ToolGeo, ToolFrame, ToolNote:
		return CursorCross
	}
	h := e.HandleAt(e.pointer.Screen)
	switch h.Kind {
	case HandleResize:
		_, r, _ := e.SelectionFrame()
		return resizeCursor(h.Which, r)
	case HandleRotate:
		return CursorRotate
	}
	return CursorDefault
}

// Cancel handles Escape, backing out one layer at a time.
func (e *Editor) Cancel() {
	switch {
	case e.gesture != nil:
		e.cancelGesture()
	case e.Editing != "":
		e.StopEditing()
	case e.FocusedGroup != "":
		id := e.FocusedGroup
		e.FocusedGroup = ""
		e.Select(id)
	case len(e.sel) > 0:
		e.SelectNone()
	case e.Tool != ToolSelect:
		e.SetTool(ToolSelect)
	}
}

func (e *Editor) cancelGesture() {
	if g := e.gesture; g != nil {
		e.gesture = nil
		g.cancel(e)
		e.Brush = nil
		e.Lasso = nil
		e.Snaps = nil
		e.DropOn = ""
		e.Erasing = nil
		e.PartialPreview = nil
	}
}

// DeleteSelected removes all currently selected unlocked shapes in one undo step.
func (e *Editor) DeleteSelected() {
	if len(e.sel) == 0 {
		return
	}
	var toDel []doc.ID
	for _, id := range e.sel {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			toDel = append(toDel, id)
		}
	}
	if len(toDel) == 0 {
		return
	}
	e.transact(func() {
		e.deleteShapes(toDel)
	})
}

// PointerDown handles pointer press in stage DIPs.
func (e *Editor) PointerDown(x, y float64, button, clicks int, mods Mods, t float64) {
	e.PointerDownPressure(x, y, button, clicks, 0, false, mods, t)
}

// PointerDownPressure handles pointer press with optional pen pressure.
func (e *Editor) PointerDownPressure(x, y float64, button, clicks int, pressure float64, isPen bool, mods Mods, t float64) {
	if e.Editing != "" {
		e.StopEditing()
	}
	screen := geo.V(x, y)
	page := e.ToPage(screen)
	e.pointer = Pointer{
		Screen: screen, Page: page, DownScreen: screen, DownPage: page,
		Down: true, Button: button, Pressure: pressure, IsPen: isPen, Mods: mods, Time: t,
	}
	e.Now = t
	e.cancelGesture()

	if button == 2 || e.spaceHeld || (button == 0 && e.Tool == ToolHand) {
		e.gesture = &panGesture{startCam: e.Cam()}
		return
	}
	if button != 0 {
		return
	}
	switch e.Tool {
	case ToolSelect:
		e.selectDown(clicks)
	case ToolLasso:
		e.gesture = newLassoGesture(e)
	case ToolDraw, ToolHighlight:
		e.gesture = newFreehandGesture(e)
	case ToolEraser:
		e.gesture = newEraserGesture(e)
	case ToolGeo, ToolFrame:
		e.gesture = &createBoxGesture{}
	case ToolText:
		e.gesture = &clickCreateGesture{kind: doc.TypeText}
	case ToolNote:
		e.gesture = &clickCreateGesture{kind: doc.TypeNote}
	}
}

// PointerMove handles pointer motion in stage DIPs.
func (e *Editor) PointerMove(x, y float64, mods Mods, t float64) {
	e.PointerMovePressure(x, y, e.pointer.Pressure, e.pointer.IsPen, mods, t)
}

// PointerMovePressure handles pointer motion with optional pen pressure.
func (e *Editor) PointerMovePressure(x, y float64, pressure float64, isPen bool, mods Mods, t float64) {
	screen := geo.V(x, y)
	e.pointer.Screen = screen
	e.pointer.Page = e.ToPage(screen)
	e.pointer.Pressure = pressure
	e.pointer.IsPen = isPen
	e.pointer.Mods = mods
	e.pointer.Time = t
	e.Now = t
	if e.gesture != nil {
		e.gesture.move(e)
		return
	}
	e.updateHover()
}

// PointerUp handles pointer release in stage DIPs.
func (e *Editor) PointerUp(x, y float64, mods Mods, t float64) {
	screen := geo.V(x, y)
	e.pointer.Screen = screen
	e.pointer.Page = e.ToPage(screen)
	e.pointer.Down = false
	e.pointer.Mods = mods
	e.pointer.Time = t
	e.Now = t
	if g := e.gesture; g != nil {
		e.gesture = nil
		g.up(e)
	}
	e.updateHover()
}

// ModsChanged propagates modifier updates mid-gesture.
func (e *Editor) ModsChanged(mods Mods) {
	if mods == e.pointer.Mods {
		return
	}
	e.pointer.Mods = mods
	if e.gesture != nil {
		e.gesture.move(e)
	}
}

func (e *Editor) updateHover() {
	if e.Tool != ToolSelect || e.Editing != "" {
		e.Hovered = ""
		return
	}
	if h := e.HandleAt(e.pointer.Screen); h.Kind != HandleNone {
		e.Hovered = ""
		return
	}
	r := e.Hit(e.pointer.Page, e.margin(), false)
	if r.ID == "" || r.Hollow {
		e.Hovered = ""
		return
	}
	e.Hovered = e.outermostSelectable(r.ID)
}

// --- pan gesture ---

type panGesture struct {
	startCam Camera
}

func (g *panGesture) move(e *Editor) {
	d := e.pointer.Screen.Sub(e.pointer.DownScreen)
	e.SetCam(Camera{
		X: g.startCam.X + d.X/g.startCam.Z,
		Y: g.startCam.Y + d.Y/g.startCam.Z,
		Z: g.startCam.Z,
	})
}

func (g *panGesture) up(*Editor)     {}
func (g *panGesture) cancel(*Editor) {}

// --- creating geo shapes & frames ---

type createBoxGesture struct {
	id     doc.ID
	origin geo.Vec
}

func (g *createBoxGesture) move(e *Editor) {
	if g.id == "" {
		if !e.dragged() {
			return
		}
		e.begin()
		g.origin = e.snapPoint(e.pointer.DownPage)
		g.id = e.newBoxShape(g.origin, 1, 1)
	}
	p := e.snapPoint(e.pointer.Page)
	a, b := g.origin, p
	if e.pointer.Mods.Alt {
		a = g.origin.Sub(p.Sub(g.origin))
	}
	box := geo.BoxFrom(a, b)
	if e.pointer.Mods.Shift {
		k := math.Max(box.W, box.H)
		if p.X < g.origin.X && !e.pointer.Mods.Alt {
			box.X = g.origin.X - k
		}
		if p.Y < g.origin.Y && !e.pointer.Mods.Alt {
			box.Y = g.origin.Y - k
		}
		box.W, box.H = k, k
	}
	e.update(g.id, func(s *doc.Shape) {
		o := e.parentMat(s.Parent).Invert().Apply(geo.V(box.X, box.Y))
		s.X = o.X
		s.Y = o.Y
		s.W = math.Max(box.W, 1)
		s.H = math.Max(box.H, 1)
	})
}

func (g *createBoxGesture) up(e *Editor) {
	if g.id == "" {
		w, h := 120.0, 90.0
		if e.Tool == ToolFrame {
			w, h = 320, 180
		} else if normalizeGeoKind(e.Next.Geo) == "pill" {
			w, h = 140, 60
		}
		e.begin()
		p := e.pointer.Page
		g.id = e.newBoxShape(geo.V(p.X-w/2, p.Y-h/2), w, h)
	}
	if e.Tool == ToolFrame {
		e.adoptIntoFrame(g.id)
	}
	e.Select(g.id)
	e.commit()
	e.finishTool()
}

func (g *createBoxGesture) cancel(e *Editor) {
	if g.id != "" {
		e.rollback()
	}
}

func (e *Editor) applyNextStyle(s *doc.Shape) {
	n := e.Next
	s.Color = n.Stroke
	s.FillColor = n.FillColor
	s.Fill = n.Fill
	s.Dash = n.Dash
	s.Size = n.Size
	s.Font = n.Font
	s.FontSize = n.FontSize
	s.Align = n.Align
	s.VAlign = n.VAlign
	s.TextAlign = n.TextAlign
	s.Bold = n.Bold
	s.Italic = n.Italic
	s.Underline = n.Underline
	s.Opacity = n.Opacity
	s.PenKind = n.PenKind
	s.PenWidth = n.PenWidth
}

func (e *Editor) placeAt(s *doc.Shape, pagePt geo.Vec) {
	parent := e.frameAt(pagePt, nil)
	if parent == "" {
		parent = e.Page
	}
	s.Parent = parent
	s.Index = e.topIndex(parent)
	local := e.parentMat(parent).Invert().Apply(pagePt)
	s.X = local.X
	s.Y = local.Y
	s.Rotation = -e.parentMat(parent).Rotation()
}

func (e *Editor) newBoxShape(at geo.Vec, w, h float64) doc.ID {
	id := doc.NewRecordID("shape")
	if e.Tool == ToolFrame {
		s := doc.NewShape(id, doc.TypeFrame)
		s.W, s.H = w, h
		s.X, s.Y = at.X, at.Y
		s.Parent = e.Page
		s.Index = e.topIndex(e.Page)
		s.Opacity = e.Next.Opacity
		s.Seed = geo.Hash(string(id))
		e.put(s)
		return id
	}
	s := doc.NewShape(id, doc.TypeGeo)
	s.Geo = normalizeGeoKind(e.Next.Geo)
	s.W, s.H = w, h
	s.Seed = geo.Hash(string(id))
	e.applyNextStyle(s)
	e.placeAt(s, at)
	e.put(s)
	return id
}

func (e *Editor) adoptIntoFrame(frame doc.ID) {
	box := e.PageBounds(frame)
	for _, id := range slices.Clone(e.Children(e.Page)) {
		if id == frame {
			continue
		}
		if box.ContainsBox(e.PageBounds(id)) {
			e.reparent(id, frame)
		}
	}
}

// --- text & note creation ---

type clickCreateGesture struct{ kind string }

func (g *clickCreateGesture) move(*Editor) {}
func (g *clickCreateGesture) up(e *Editor) {
	if g.kind == doc.TypeText {
		e.createTextAndEdit(e.pointer.Page)
	} else {
		e.createNoteAndEdit(e.pointer.Page)
	}
	e.finishTool()
}
func (g *clickCreateGesture) cancel(*Editor) {}

// CreateText adds a text shape at pagePt in a single undo transaction.
func (e *Editor) CreateText(pagePt geo.Vec, text string) doc.ID {
	var id doc.ID
	e.transact(func() {
		id = doc.NewRecordID("shape")
		s := doc.NewShape(id, doc.TypeText)
		e.applyNextStyle(s)
		s.Text = text
		s.AutoSize = true
		s.Seed = geo.Hash(string(id))
		h := textFontSize(s) * 1.3
		e.placeAt(s, geo.V(pagePt.X, pagePt.Y-h/2))
		e.put(s)
		e.Select(id)
	})
	return id
}

// CreateNote adds a sticky note shape centered at pagePt in a single undo transaction.
func (e *Editor) CreateNote(pagePt geo.Vec, text string) doc.ID {
	var id doc.ID
	e.transact(func() {
		id = doc.NewRecordID("shape")
		s := doc.NewShape(id, doc.TypeNote)
		e.applyNextStyle(s)
		s.Text = text
		s.Seed = geo.Hash(string(id))
		e.placeAt(s, geo.V(pagePt.X-s.W/2, pagePt.Y-s.H/2))
		e.put(s)
		e.Select(id)
	})
	return id
}

// InsertImage inserts an image asset shape centered at pagePt (scaled to max 480px wide).
func (e *Editor) InsertImage(asset *doc.Asset, pagePt geo.Vec) doc.ID {
	if asset == nil {
		return ""
	}
	var id doc.ID
	e.transact(func() {
		e.putAsset(asset)
		id = doc.NewRecordID("shape")
		s := doc.NewShape(id, doc.TypeImage)
		w, h := asset.W, asset.H
		if w <= 0 {
			w = 240
		}
		if h <= 0 {
			h = 180
		}
		if w > 480 {
			scale := 480.0 / w
			w *= scale
			h *= scale
		}
		s.AssetID = asset.ID
		s.W, s.H = w, h
		s.Opacity = 1
		s.Seed = geo.Hash(string(id))
		e.placeAt(s, geo.V(pagePt.X-w/2, pagePt.Y-h/2))
		e.put(s)
		e.Select(id)
	})
	return id
}

// --- freehand draw & highlight ---

type freehandGesture struct {
	id   doc.ID
	last geo.Vec
}

func newFreehandGesture(e *Editor) *freehandGesture {
	e.begin()
	typ := doc.TypeDraw
	if e.Tool == ToolHighlight {
		typ = doc.TypeHighlight
	}
	id := doc.NewRecordID("shape")
	s := doc.NewShape(id, typ)
	e.applyNextStyle(s)
	if typ == doc.TypeHighlight {
		s.Dash = "solid"
		s.Fill = "none"
	}
	s.IsPen = e.pointer.IsPen
	z := 0.5
	if e.pointer.IsPen && e.pointer.Pressure > 0 {
		z = e.pointer.Pressure
	}
	p := e.pointer.Page
	s.Segments = []doc.Segment{{Type: "free", Points: []doc.PointZ{{X: 0, Y: 0, Z: z}}}}
	s.Seed = geo.Hash(string(id))
	e.placeAt(s, p)
	e.put(s)
	return &freehandGesture{id: id, last: p}
}

func (g *freehandGesture) move(e *Editor) {
	p := e.pointer.Page
	zCam := e.Cam().Z
	if p.Dist(g.last) < 0.8/zCam && !e.pointer.Mods.Shift {
		return
	}
	s := e.Doc.Shapes[g.id]
	if s == nil {
		return
	}
	q := e.PageMat(g.id).Invert().Apply(p)
	pz := 0.5
	if e.pointer.IsPen && e.pointer.Pressure > 0 {
		pz = e.pointer.Pressure
	}
	pt := doc.PointZ{X: q.X, Y: q.Y, Z: pz}

	e.update(g.id, func(c *doc.Shape) {
		segs := slices.Clone(s.Segments)
		last := &segs[len(segs)-1]
		if e.pointer.Mods.Shift {
			if last.Type == "free" {
				from := last.Points[len(last.Points)-1]
				segs = append(segs, doc.Segment{Type: "straight", Points: []doc.PointZ{from, pt}})
			} else {
				last.Points = []doc.PointZ{last.Points[0], pt}
			}
		} else if last.Type == "straight" {
			from := last.Points[len(last.Points)-1]
			segs = append(segs, doc.Segment{Type: "free", Points: []doc.PointZ{from, pt}})
		} else {
			pts := make([]doc.PointZ, len(last.Points)+1)
			copy(pts, last.Points)
			pts[len(pts)-1] = pt
			last.Points = pts
		}
		c.Segments = segs
	})
	g.last = p
}

func (g *freehandGesture) up(e *Editor) {
	s := e.Doc.Shapes[g.id]
	if s == nil {
		e.rollback()
		return
	}
	e.update(g.id, func(c *doc.Shape) {
		c.IsComplete = true
		if c.Type == doc.TypeDraw {
			pts := strokePoints(c)
			thresh := math.Max(strokeWidthOf(c)*3, 14)
			if len(pts) > 8 && pts[0].Dist(pts[len(pts)-1]) < thresh && geo.PolyLength(pts) > 60 {
				c.IsClosed = true
			}
		}
	})
	e.commit()
}

func (g *freehandGesture) cancel(e *Editor) {
	e.rollback()
}

// --- eraser (Object & Partial modes) ---

type eraserGesture struct {
	mode   EraserMode
	last   geo.Vec
	points []geo.Vec
	radius float64
	trail  *Trail
}

func newEraserGesture(e *Editor) *eraserGesture {
	mode := e.Next.EraserMode
	if mode == "" {
		mode = EraserObject
	}
	r := math.Max(12/e.Cam().Z, e.Next.PenWidth*1.8)
	g := &eraserGesture{
		mode:   mode,
		last:   e.pointer.Page,
		points: []geo.Vec{e.pointer.Page},
		radius: r,
		trail:  &Trail{Live: true, Points: []TrailPoint{{P: e.pointer.Page, T: e.Now}}},
	}
	e.Trails = append(e.Trails, g.trail)
	e.Erasing = map[doc.ID]bool{}
	if mode == EraserPartial {
		e.PartialPreview = map[doc.ID][][]geo.Vec{}
	}
	g.sweep(e, e.pointer.Page, e.pointer.Page)
	return g
}

func (g *eraserGesture) sweep(e *Editor, a, b geo.Vec) {
	if g.mode == EraserPartial {
		g.sweepPartial(e)
		return
	}
	margin := 6 / e.Cam().Z
	var walk func(parent doc.ID)
	walk = func(parent doc.ID) {
		for _, id := range e.Children(parent) {
			s := e.Doc.Shapes[id]
			if s == nil || s.Locked {
				continue
			}
			if s.Type == doc.TypeGroup {
				walk(id)
				continue
			}
			if s.Type == doc.TypeFrame {
				walk(id)
			}
			bounds := e.PageBounds(id).Expand(margin + 14)
			if !bounds.Contains(a) && !bounds.Contains(b) {
				continue
			}
			geom := e.Geometry(s)
			pts := e.PageOutline(id)
			hit := geo.SegmentHitsPolyline(a, b, pts, geom.Closed, margin+strokeWidthOf(s)/2)
			if !hit && geom.Filled && geom.Closed && geo.PointInPolygon(b, pts) {
				hit = true
			}
			if hit {
				e.Erasing[e.outermostSelectable(id)] = true
			}
		}
	}
	walk(e.Page)
}

func (g *eraserGesture) sweepPartial(e *Editor) {
	erBox := geo.BoxOf(g.points).Expand(g.radius + 8)
	var walk func(parent doc.ID)
	walk = func(parent doc.ID) {
		for _, id := range e.Children(parent) {
			s := e.Doc.Shapes[id]
			if s == nil || s.Locked {
				continue
			}
			if s.Type == doc.TypeGroup || s.Type == doc.TypeFrame {
				walk(id)
				continue
			}
			if s.Type != doc.TypeDraw && s.Type != doc.TypeHighlight {
				continue
			}
			if !e.PageBounds(id).Expand(g.radius + strokeWidthOf(s)).Overlaps(erBox) {
				continue
			}
			m := e.PageMat(id)
			inv := m.Invert()
			localPts := strokePoints(s)
			if len(localPts) == 0 {
				continue
			}
			pagePts := make([]geo.Vec, len(localPts))
			for i, pt := range localPts {
				pagePts[i] = m.Apply(pt)
			}
			r := g.radius + strokeWidthOf(s)*0.5
			if s.Type == doc.TypeHighlight {
				r += 6
			}
			if !polylineNearEraser(pagePts, g.points, r) {
				delete(e.Erasing, id)
				delete(e.PartialPreview, id)
				continue
			}
			survived := geo.ClipPolylineOutsideCapsules(pagePts, g.points, r)
			e.Erasing[id] = true
			// Keep surviving pieces in shape's local space for live preview & commit.
			localSurvived := make([][]geo.Vec, len(survived))
			for i, poly := range survived {
				lp := make([]geo.Vec, len(poly))
				for j, v := range poly {
					lp[j] = inv.Apply(v)
				}
				localSurvived[i] = lp
			}
			e.PartialPreview[id] = localSurvived
		}
	}
	walk(e.Page)
}

func polylineNearEraser(poly, eraser []geo.Vec, r float64) bool {
	if len(poly) == 1 {
		for _, c := range eraser {
			if poly[0].Dist(c) <= r {
				return true
			}
		}
		return false
	}
	for i := 1; i < len(eraser); i++ {
		if geo.SegmentHitsPolyline(eraser[i-1], eraser[i], poly, false, r) {
			return true
		}
	}
	if len(eraser) == 1 {
		return geo.DistToPolyline(eraser[0], poly, false) <= r
	}
	return false
}

func (g *eraserGesture) move(e *Editor) {
	p := e.pointer.Page
	g.trail.Points = append(g.trail.Points, TrailPoint{P: p, T: e.Now})
	g.points = append(g.points, p)
	g.sweep(e, g.last, p)
	g.last = p
}

func (g *eraserGesture) up(e *Editor) {
	g.trail.Live = false
	if g.mode == EraserPartial {
		g.commitPartial(e)
		return
	}
	var ids []doc.ID
	for id := range e.Erasing {
		ids = append(ids, id)
	}
	e.Erasing = nil
	e.PartialPreview = nil
	if len(ids) > 0 {
		e.transact(func() { e.deleteShapes(ids) })
	}
}

func (g *eraserGesture) commitPartial(e *Editor) {
	preview := e.PartialPreview
	e.Erasing = nil
	e.PartialPreview = nil
	if len(preview) == 0 {
		return
	}
	e.transact(func() {
		for id, pieces := range preview {
			orig := e.Doc.Shapes[id]
			if orig == nil {
				continue
			}
			// Find neighbor indices under orig.Parent so split pieces keep exact z-order.
			siblings := e.Children(orig.Parent)
			nextIdx := ""
			for i, sid := range siblings {
				if sid == id && i+1 < len(siblings) {
					nextIdx = e.Doc.Shapes[siblings[i+1]].Index
					break
				}
			}
			e.removeShape(id)
			if len(pieces) == 0 {
				e.sel = slices.DeleteFunc(e.sel, func(x doc.ID) bool { return x == id })
				continue
			}
			prevIdx := orig.Index
			indices := doc.IndicesBetween(prevIdx, nextIdx, len(pieces))
			for i, poly := range pieces {
				if len(poly) < 2 {
					continue
				}
				nid := doc.NewRecordID("shape")
				ns := orig.Clone()
				ns.ID = nid
				ns.Index = indices[i]
				ns.IsClosed = false
				ns.IsComplete = true
				ns.ScaleX, ns.ScaleY = 1, 1
				ns.Seed = geo.Hash(string(nid))
				// Re-anchor each split piece to its own starting point so (X, Y) is tight.
				originLocal := poly[0]
				originPage := e.PageMat(orig.Parent).Mul(geo.Compose(orig.X, orig.Y, orig.Rotation)).Apply(originLocal)
				inParent := e.parentMat(orig.Parent).Invert().Apply(originPage)
				ns.X, ns.Y = inParent.X, inParent.Y
				pts := make([]doc.PointZ, len(poly))
				for k, v := range poly {
					pts[k] = doc.PointZ{X: v.X - originLocal.X, Y: v.Y - originLocal.Y, Z: 0.5}
				}
				ns.Segments = []doc.Segment{{Type: "free", Points: pts}}
				e.put(ns)
			}
		}
	})
}

func (g *eraserGesture) cancel(e *Editor) {
	g.trail.Live = false
	e.Erasing = nil
	e.PartialPreview = nil
}

// AgeTrails removes expired trail points and reports whether any remain active.
func (e *Editor) AgeTrails(now float64) bool {
	e.Now = now
	out := e.Trails[:0]
	for _, tr := range e.Trails {
		life := 0.25
		if tr.Laser {
			life = 1.0
		}
		i := 0
		for i < len(tr.Points) && now-tr.Points[i].T > life {
			i++
		}
		tr.Points = tr.Points[i:]
		if tr.Live || len(tr.Points) > 0 {
			out = append(out, tr)
		}
	}
	e.Trails = out
	return len(out) > 0
}
