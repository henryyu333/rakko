package editor

import (
	"math"
	"slices"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

// HandleKind identifies an interactive selection handle.
type HandleKind uint8

const (
	HandleNone HandleKind = iota
	HandleResize
	HandleRotate
	HandleArrowEnd
	HandleArrowBend
	HandleLinePoint
	HandleGeoVertex
	HandleCrop
)

// Handle describes a draggable handle on the active selection.
type Handle struct {
	Kind    HandleKind
	Which   geo.Vec // normalized coordinates in [0, 1]^2 on the selection box
	Point   geo.Vec // page coordinates
	Term    string  // "start" or "end" for HandleArrowEnd
	Index   int     // vertex index for HandleLinePoint / HandleGeoVertex / elbow
	Virtual bool    // true for midpoint insertion handles
}

func (e *Editor) topSelected() []doc.ID {
	if len(e.sel) == 0 {
		return nil
	}
	selSet := make(map[doc.ID]bool, len(e.sel))
	for _, id := range e.sel {
		selSet[id] = true
	}
	var out []doc.ID
	for _, id := range e.sel {
		s := e.Doc.Shapes[id]
		if s == nil {
			continue
		}
		ancestorSelected := false
		for cur := s.Parent; cur != "" && !doc.IsPageID(cur); {
			if selSet[cur] {
				ancestorSelected = true
				break
			}
			p := e.Doc.Shapes[cur]
			if p == nil {
				break
			}
			cur = p.Parent
		}
		if !ancestorSelected {
			out = append(out, id)
		}
	}
	return out
}

func (e *Editor) singleLinear() *doc.Shape {
	if len(e.sel) != 1 {
		return nil
	}
	s := e.Doc.Shapes[e.sel[0]]
	if s != nil && (s.Type == doc.TypeArrow || s.Type == doc.TypeLine) {
		return s
	}
	return nil
}

func (e *Editor) allLocked() bool {
	for _, id := range e.sel {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			return false
		}
	}
	return true
}

// SelectionFrame returns the selection's bounding box in its shared-rotation frame
// and that frame's rotation in radians (0 for mixed rotations).
func (e *Editor) SelectionFrame() (box geo.Box, rot float64, ok bool) {
	ids := e.topSelected()
	if len(ids) == 0 {
		return geo.Box{}, 0, false
	}
	rot = e.PageMat(ids[0]).Rotation()
	for _, id := range ids[1:] {
		if math.Abs(geo.ShortAngle(rot, e.PageMat(id).Rotation())) > 1e-3 {
			rot = 0
			break
		}
	}
	inv := geo.Rotate(-rot)
	first := true
	for _, id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil {
			continue
		}
		b := inv.Mul(e.PageMat(id)).BoundsOf(e.Geometry(s).Bounds)
		if first {
			box, first = b, false
		} else {
			box = box.Union(b)
		}
	}
	return box, rot, !first
}

// SelectionCorners returns the four corners of the selection frame in page space.
func (e *Editor) SelectionCorners() ([4]geo.Vec, bool) {
	box, rot, ok := e.SelectionFrame()
	if !ok {
		return [4]geo.Vec{}, false
	}
	return geo.Rotate(rot).ApplyBox(box), true
}

// SelectionSize returns the width and height of the selection in its frame.
func (e *Editor) SelectionSize() (w, h float64, ok bool) {
	box, _, ok := e.SelectionFrame()
	if !ok {
		return 0, 0, false
	}
	return box.W, box.H, true
}

// ShowsBox reports whether the selection displays a bounding box and 8 handles.
func (e *Editor) ShowsBox() bool {
	return len(e.sel) > 0 && e.singleLinear() == nil && e.Editing == "" && e.EditingVertices == ""
}

// IsResizing reports whether a resize drag is currently active.
func (e *Editor) IsResizing() bool {
	_, ok := e.gesture.(*resizeGesture)
	return ok
}

// RotateAngle returns the active selection's rotation angle in degrees during a rotate gesture.
func (e *Editor) RotateAngle() (float64, bool) {
	g, ok := e.gesture.(*rotateGesture)
	if !ok {
		return 0, false
	}
	deg := math.Mod(g.current*180/math.Pi, 360)
	if deg < 0 {
		deg += 360
	}
	return deg, true
}

// Handles returns the interactive handles for the selection.
func (e *Editor) Handles() []Handle {
	if len(e.sel) == 0 || e.Editing != "" || e.allLocked() {
		return nil
	}
	if e.EditingVertices != "" {
		if s := e.Doc.Shapes[e.EditingVertices]; s != nil && !s.Locked {
			return e.geoVertexHandles(s)
		}
	}
	if e.Cropping != "" {
		if s := e.Doc.Shapes[e.Cropping]; s != nil && s.Type == doc.TypeImage && !s.Locked {
			return e.cropHandles(s)
		}
	}
	if s := e.singleLinear(); s != nil {
		if s.Locked {
			return nil
		}
		return e.linearHandles(s)
	}
	if !e.ShowsBox() {
		return nil
	}
	box, r, ok := e.SelectionFrame()
	if !ok {
		return nil
	}
	m := geo.Rotate(r)
	at := func(fx, fy float64) geo.Vec {
		return m.Apply(geo.V(box.X+box.W*fx, box.Y+box.H*fy))
	}
	z := e.Cam().Z
	var hs []Handle
	// Corner resize handles take priority over side midpoints when overlapping.
	for _, c := range [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
		hs = append(hs, Handle{Kind: HandleResize, Which: geo.V(c[0], c[1]), Point: at(c[0], c[1])})
	}
	if box.W*z > 6 && box.H*z > 6 {
		for _, c := range [][2]float64{{0.5, 0}, {1, 0.5}, {0.5, 1}, {0, 0.5}} {
			hs = append(hs, Handle{Kind: HandleResize, Which: geo.V(c[0], c[1]), Point: at(c[0], c[1])})
		}
	}
	for _, c := range [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
		hs = append(hs, Handle{Kind: HandleRotate, Which: geo.V(c[0], c[1]), Point: at(c[0], c[1])})
	}
	return hs
}

func (e *Editor) linearHandles(s *doc.Shape) []Handle {
	m := e.PageMat(s.ID)
	var hs []Handle
	if s.Type == doc.TypeArrow {
		info := e.Arrow(s)
		hs = append(hs,
			Handle{Kind: HandleArrowEnd, Term: "start", Point: m.Apply(info.Start)},
			Handle{Kind: HandleArrowEnd, Term: "end", Point: m.Apply(info.End)},
		)
		if s.Kind == "elbow" {
			if len(s.Points) > 0 {
				for i, wp := range s.Points {
					hs = append(hs, Handle{Kind: HandleArrowBend, Index: i, Point: m.Apply(geo.V(wp.X, wp.Y))})
				}
			} else if len(info.Elbow) == 4 {
				hs = append(hs,
					Handle{Kind: HandleArrowBend, Index: 0, Point: m.Apply(geo.Med(info.Elbow[1], info.Elbow[2]))},
					Handle{Kind: HandleArrowBend, Index: 0, Virtual: true, Point: m.Apply(geo.Med(info.Elbow[0], info.Elbow[1]))},
					Handle{Kind: HandleArrowBend, Index: 1, Virtual: true, Point: m.Apply(geo.Med(info.Elbow[2], info.Elbow[3]))},
				)
			}
		} else {
			hs = append(hs, Handle{Kind: HandleArrowBend, Point: m.Apply(info.Mid)})
		}
		return hs
	}
	pts := e.linePoints(s)
	for i, p := range pts {
		hs = append(hs, Handle{Kind: HandleLinePoint, Index: i, Point: m.Apply(p)})
	}
	for i := 1; i < len(pts); i++ {
		hs = append(hs, Handle{
			Kind: HandleLinePoint, Index: i, Virtual: true,
			Point: m.Apply(geo.Med(pts[i-1], pts[i])),
		})
	}
	return hs
}

func (e *Editor) geoVertexHandles(s *doc.Shape) []Handle {
	pts := materializeGeoPoints(s)
	if len(pts) < 2 {
		return nil
	}
	m := e.PageMat(s.ID)
	w, h := math.Max(s.W, 1), math.Max(s.H, 1)
	localOf := func(lp doc.LinePoint) geo.Vec {
		lx, ly := lp.X*w, lp.Y*h
		if s.FlipX {
			lx = w - lx
		}
		if s.FlipY {
			ly = h - ly
		}
		return m.Apply(geo.V(lx, ly))
	}
	var hs []Handle
	pagePts := make([]geo.Vec, len(pts))
	for i, lp := range pts {
		pagePts[i] = localOf(lp)
		hs = append(hs, Handle{Kind: HandleGeoVertex, Index: i, Point: pagePts[i]})
	}
	edgeCount := len(pagePts) - 1
	if normalizeGeoKind(s.Geo) == "polygon" {
		edgeCount = len(pagePts)
	}
	for i := 0; i < edgeCount; i++ {
		a := pagePts[i]
		b := pagePts[(i+1)%len(pagePts)]
		hs = append(hs, Handle{
			Kind: HandleGeoVertex, Index: i + 1, Virtual: true,
			Point: geo.Med(a, b),
		})
	}
	return hs
}

// HandleAt returns the selection handle under screen coordinates, if any.
func (e *Editor) HandleAt(screen geo.Vec) Handle {
	hs := e.Handles()
	if len(hs) == 0 {
		return Handle{}
	}
	if e.EditingVertices != "" || e.singleLinear() != nil {
		// Prefer real vertices/endpoints over virtual midpoints.
		for _, h := range hs {
			if !h.Virtual && e.ToScreen(h.Point).Dist(screen) <= 10 {
				return h
			}
		}
		for _, h := range hs {
			if h.Virtual && e.ToScreen(h.Point).Dist(screen) <= 10 {
				return h
			}
		}
		return Handle{}
	}
	box, r, ok := e.SelectionFrame()
	if !ok {
		return Handle{}
	}
	z := e.Cam().Z
	q := geo.Rotate(-r).Apply(e.ToPage(screen))
	dx := func(at float64) float64 { return (q.X - at) * z }
	dy := func(at float64) float64 { return (q.Y - at) * z }

	const near = 7.0
	for _, h := range hs {
		if h.Kind != HandleResize && h.Kind != HandleCrop {
			continue
		}
		cx, cy := box.X+box.W*h.Which.X, box.Y+box.H*h.Which.Y
		switch {
		case h.Which.X != 0.5 && h.Which.Y != 0.5:
			if math.Abs(dx(cx)) <= near && math.Abs(dy(cy)) <= near {
				return h
			}
		case h.Which.X == 0.5:
			if math.Abs(dy(cy)) <= near-1 && q.X > box.X && q.X < box.MaxX() {
				return h
			}
		default:
			if math.Abs(dx(cx)) <= near-1 && q.Y > box.Y && q.Y < box.MaxY() {
				return h
			}
		}
	}
	// Outer corner rotation zones lie just beyond each corner.
	for _, h := range hs {
		if h.Kind != HandleRotate {
			continue
		}
		cx, cy := box.X+box.W*h.Which.X, box.Y+box.H*h.Which.Y
		sx, sy := h.Which.X*2-1, h.Which.Y*2-1
		ox := dx(cx) * sx
		oy := dy(cy) * sy
		if ox >= -near && oy >= -near && ox <= 22 && oy <= 22 && (ox > near || oy > near) {
			return h
		}
	}
	return Handle{}
}

func resizeCursor(which geo.Vec, rotation float64) Cursor {
	d := which.Sub(geo.V(0.5, 0.5))
	a := geo.NormAngle(math.Atan2(d.Y, d.X) + rotation)
	k := int(math.Round(a/(math.Pi/4))) % 4
	switch k {
	case 0:
		return CursorEW
	case 1:
		return CursorNWSE
	case 2:
		return CursorNS
	}
	return CursorNESW
}

// --- select tool & double click ---

func (e *Editor) selectDown(clicks int) {
	if clicks >= 2 && e.doubleClick() {
		return
	}
	if h := e.HandleAt(e.pointer.Screen); h.Kind != HandleNone {
		switch h.Kind {
		case HandleResize:
			e.gesture = newResizeGesture(e, h)
		case HandleRotate:
			e.gesture = newRotateGesture(e)
		case HandleArrowEnd:
			e.gesture = newArrowEndDragGesture(e, h.Term)
		case HandleArrowBend:
			e.gesture = newArrowBendDragGesture(e, h)
		case HandleLinePoint:
			e.gesture = newLinePointDragGesture(e, h)
		case HandleGeoVertex:
			e.gesture = newGeoVertexDragGesture(e, h)
		case HandleCrop:
			e.gesture = newCropGesture(e, h)
		}
		return
	}
	e.EditingVertices = ""
	e.Cropping = ""

	p := e.pointer.Page
	r := e.Hit(p, e.margin(), false)
	var target doc.ID
	if r.ID != "" {
		target = e.outermostSelectable(r.ID)
	}
	inBox := false
	if e.ShowsBox() {
		if box, rot, ok := e.SelectionFrame(); ok {
			inBox = box.Contains(geo.Rotate(-rot).Apply(p))
		}
	}

	switch {
	case target != "" && !r.Hollow:
		wasSel := e.IsSelected(target)
		if !wasSel && !e.pointer.Mods.Shift {
			if !(inBox && len(e.sel) > 1) {
				e.Select(target)
			}
		} else if !wasSel {
			e.sel = append(e.sel, target)
		}
		e.gesture = &pressShapeGesture{target: target, wasSelected: wasSel}
	case inBox:
		e.gesture = &pressShapeGesture{target: target, wasSelected: true, inBoxOnly: true, hollow: r.Hollow}
	default:
		e.gesture = &pressCanvasGesture{hollow: target, additive: e.pointer.Mods.Shift, before: slices.Clone(e.sel)}
	}
}

func (e *Editor) doubleClick() bool {
	p := e.pointer.Page
	r := e.Hit(p, e.margin(), false)
	if r.ID == "" {
		e.createTextAndEdit(p)
		return true
	}
	target := e.outermostSelectable(r.ID)
	s := e.Doc.Shapes[target]
	if s == nil {
		return false
	}
	if s.Type == doc.TypeFrame && r.Hollow {
		e.createTextAndEdit(p)
		return true
	}
	if s.Type == doc.TypeGroup {
		e.FocusedGroup = target
		inner := e.outermostSelectable(r.ID)
		e.Select(inner)
		if ts := e.Doc.Shapes[inner]; ts != nil && textEditable(ts) {
			e.StartEditing(inner)
		}
		return true
	}
	if s.Type == doc.TypeGeo && normalizeGeoKind(s.Geo) == "curve" && !s.Locked {
		e.EditVertices(target)
		return true
	}
	if s.Type == doc.TypeGeo && normalizeGeoKind(s.Geo) == "polygon" && !s.Locked {
		q := e.PageMat(target).Invert().Apply(p)
		if geo.DistToPolyline(q, e.Geometry(s).Outline, true) <= e.margin()*1.5 {
			e.EditVertices(target)
			return true
		}
	}
	if s.Type == doc.TypeImage && !s.Locked {
		return e.EnterCrop(target)
	}
	if textEditable(s) {
		e.Select(target)
		e.StartEditing(target)
		return true
	}
	return false
}

type pressShapeGesture struct {
	target      doc.ID
	wasSelected bool
	inBoxOnly   bool
	hollow      bool
}

func (g *pressShapeGesture) move(e *Editor) {
	if !e.dragged() || e.allLocked() {
		return
	}
	e.gesture = newTranslateGesture(e)
	e.gesture.move(e)
}

func (g *pressShapeGesture) up(e *Editor) {
	switch {
	case g.inBoxOnly:
		if g.target != "" && g.hollow && !e.pointer.Mods.Shift {
			e.Select(g.target)
		} else if !e.pointer.Mods.Shift {
			e.SelectNone()
		}
	case e.pointer.Mods.Shift && g.wasSelected:
		e.sel = slices.DeleteFunc(e.sel, func(id doc.ID) bool { return id == g.target })
	case !e.pointer.Mods.Shift:
		e.Select(g.target)
	}
}

func (g *pressShapeGesture) cancel(*Editor) {}

type pressCanvasGesture struct {
	hollow   doc.ID
	additive bool
	before   []doc.ID
}

func (g *pressCanvasGesture) move(e *Editor) {
	if !e.dragged() {
		return
	}
	e.gesture = &marqueeGesture{additive: g.additive, before: g.before}
	e.gesture.move(e)
}

func (g *pressCanvasGesture) up(e *Editor) {
	switch {
	case g.hollow != "":
		if g.additive {
			if !e.IsSelected(g.hollow) {
				e.sel = append(e.sel, g.hollow)
			}
		} else {
			e.Select(g.hollow)
		}
	case !g.additive:
		e.SelectNone()
	}
}

func (g *pressCanvasGesture) cancel(*Editor) {}

// --- marquee box selection ---

type marqueeGesture struct {
	additive bool
	before   []doc.ID
}

func (g *marqueeGesture) move(e *Editor) {
	box := geo.BoxFrom(e.pointer.DownPage, e.pointer.Page)
	e.Brush = &box
	encloseOnly := e.pointer.Mods.Cmd || e.pointer.Mods.Ctrl
	parent := e.Page
	if e.FocusedGroup != "" {
		parent = e.FocusedGroup
	}
	var hits []doc.ID
	var test func(id doc.ID) bool
	test = func(id doc.ID) bool {
		s := e.Doc.Shapes[id]
		if s == nil || s.Locked {
			return false
		}
		if s.Type == doc.TypeGroup {
			for _, k := range e.Children(id) {
				if test(k) {
					return true
				}
			}
			return false
		}
		bounds := e.PageBounds(id)
		if !box.Overlaps(bounds) {
			return false
		}
		if box.ContainsBox(bounds) {
			return true
		}
		if encloseOnly {
			return false
		}
		if s.Type == doc.TypeFrame {
			return !bounds.ContainsBox(box)
		}
		return geo.BoxHitsPolyline(box, e.PageOutline(id), e.Geometry(s).Closed)
	}
	for _, id := range e.Children(parent) {
		if test(id) {
			hits = append(hits, id)
			continue
		}
		if s := e.Doc.Shapes[id]; s != nil && s.Type == doc.TypeFrame {
			for _, k := range e.Children(id) {
				if test(k) {
					hits = append(hits, k)
				}
			}
		}
	}
	if g.additive {
		for _, id := range g.before {
			if !slices.Contains(hits, id) {
				hits = append(hits, id)
			}
		}
	}
	e.Select(hits...)
}

func (g *marqueeGesture) up(e *Editor) {
	e.Brush = nil
}

func (g *marqueeGesture) cancel(e *Editor) {
	e.Brush = nil
	e.Select(g.before...)
}

// --- lasso selection ---

type lassoGesture struct {
	additive bool
	before   []doc.ID
	pts      []geo.Vec
}

func newLassoGesture(e *Editor) *lassoGesture {
	pts := []geo.Vec{e.pointer.Page}
	e.Lasso = pts
	return &lassoGesture{
		additive: e.pointer.Mods.Shift,
		before:   slices.Clone(e.sel),
		pts:      pts,
	}
}

func (g *lassoGesture) move(e *Editor) {
	p := e.pointer.Page
	if len(g.pts) == 0 || p.Dist(g.pts[len(g.pts)-1]) >= 2/e.Cam().Z {
		g.pts = append(g.pts, p)
		e.Lasso = g.pts
	}
	if len(g.pts) < 3 {
		return
	}
	polyBox := geo.BoxOf(g.pts)
	parent := e.Page
	if e.FocusedGroup != "" {
		parent = e.FocusedGroup
	}
	var hits []doc.ID
	var test func(id doc.ID) bool
	test = func(id doc.ID) bool {
		s := e.Doc.Shapes[id]
		if s == nil || s.Locked {
			return false
		}
		if s.Type == doc.TypeGroup {
			for _, k := range e.Children(id) {
				if test(k) {
					return true
				}
			}
			return false
		}
		bounds := e.PageBounds(id)
		if !polyBox.Overlaps(bounds) {
			return false
		}
		if s.Type == doc.TypeFrame {
			// Frame selects if its border crosses the lasso or its corners fall inside.
			return geo.PolygonHitsPolyline(g.pts, e.PageOutline(id), false)
		}
		return geo.PointInPolygon(bounds.Center(), g.pts) ||
			geo.PolygonHitsPolyline(g.pts, e.PageOutline(id), e.Geometry(s).Closed)
	}
	for _, id := range e.Children(parent) {
		if test(id) {
			hits = append(hits, id)
			continue
		}
		if s := e.Doc.Shapes[id]; s != nil && s.Type == doc.TypeFrame {
			for _, k := range e.Children(id) {
				if test(k) {
					hits = append(hits, k)
				}
			}
		}
	}
	if g.additive {
		for _, id := range g.before {
			if !slices.Contains(hits, id) {
				hits = append(hits, id)
			}
		}
	}
	e.Select(hits...)
}

func (g *lassoGesture) up(e *Editor) {
	e.Lasso = nil
	e.finishTool()
}

func (g *lassoGesture) cancel(e *Editor) {
	e.Lasso = nil
	e.Select(g.before...)
}

// --- translate with Alt-duplicate, smart alignment guides, and grid snap ---

type translateGesture struct {
	ids          []doc.ID
	initial      map[doc.ID]*doc.Shape
	origShapes   map[doc.ID]*doc.Shape
	origBindings []*doc.Binding
	cloned       []doc.ID
	wasAlt       bool
	selBefore    []doc.ID
	box          geo.Box
	targets      snapTargets
}

func newTranslateGesture(e *Editor) *translateGesture {
	e.begin()
	g := &translateGesture{
		initial:    map[doc.ID]*doc.Shape{},
		origShapes: map[doc.ID]*doc.Shape{},
		selBefore:  slices.Clone(e.sel),
	}
	for _, id := range e.topSelected() {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			g.ids = append(g.ids, id)
		}
	}
	// Preserve pre-unbind shapes and bindings so Alt-drag can restore them onto
	// the originals while normal drag moves the unbound arrow in real time.
	for _, id := range g.ids {
		for _, mid := range append([]doc.ID{id}, e.Descendants(id)...) {
			if s := e.Doc.Shapes[mid]; s != nil {
				g.origShapes[mid] = s
			}
			for _, term := range []string{"start", "end"} {
				if b := e.bindingOf(mid, term); b != nil {
					cp := *b
					g.origBindings = append(g.origBindings, &cp)
				}
			}
		}
	}
	e.unbindExternal(g.ids)
	for _, id := range g.ids {
		if s := e.Doc.Shapes[id]; s != nil {
			g.initial[id] = s
		}
	}
	box, r, _ := e.SelectionFrame()
	g.box = geo.Rotate(r).BoundsOf(box)
	g.targets = e.collectSnapTargets(g.ids)
	return g
}

func (g *translateGesture) moving() []doc.ID {
	if len(g.cloned) > 0 {
		return g.cloned
	}
	return g.ids
}

func (g *translateGesture) setCloned(e *Editor, on bool) {
	if on {
		// Restore originals (and their bindings) before cloning so Alt-drag copies
		// keep visible endpoints without stripping bindings from the originals.
		for _, s := range g.origShapes {
			e.put(s.Moved())
		}
		for _, b := range g.origBindings {
			cp := *b
			e.putBinding(&cp)
		}
		clones := e.cloneShapes(g.ids, geo.Vec{})
		g.cloned = clones
		e.Select(clones...)
		return
	}
	for _, id := range g.cloned {
		e.deleteTree(id)
		delete(g.initial, id)
	}
	g.cloned = nil
	e.unbindExternal(g.ids)
	for _, id := range g.ids {
		if s := e.Doc.Shapes[id]; s != nil {
			g.initial[id] = s
		}
	}
	e.Select(g.selBefore...)
}

func (g *translateGesture) move(e *Editor) {
	p := e.pointer
	if p.Mods.Alt != g.wasAlt {
		g.wasAlt = p.Mods.Alt
		g.setCloned(e, p.Mods.Alt)
	}
	d := p.Page.Sub(p.DownPage)
	if p.Mods.Shift {
		if math.Abs(d.X) > math.Abs(d.Y) {
			d.Y = 0
		} else {
			d.X = 0
		}
	}
	e.Snaps = nil
	snapActive := e.Snap != (p.Mods.Cmd || p.Mods.Ctrl)
	if snapActive {
		var snappedX, snappedY bool
		d, snappedX, snappedY = e.snapTranslate(g.box, d, g.targets)
		if e.Grid {
			step := e.GridStep()
			if !snappedX {
				d.X = math.Round((g.box.X+d.X)/step)*step - g.box.X
			}
			if !snappedY {
				d.Y = math.Round((g.box.Y+d.Y)/step)*step - g.box.Y
			}
		}
	} else if e.Grid {
		step := e.GridStep()
		d.X = math.Round((g.box.X+d.X)/step)*step - g.box.X
		d.Y = math.Round((g.box.Y+d.Y)/step)*step - g.box.Y
	}

	for _, id := range g.moving() {
		init := g.initial[id]
		if init == nil {
			init = e.Doc.Shapes[id]
			g.initial[id] = init
		}
		if init == nil {
			continue
		}
		pm := e.parentMat(init.Parent).Invert()
		local := pm.ApplyDir(d)
		c := init.Moved()
		c.X = init.X + local.X
		c.Y = init.Y + local.Y
		e.put(c)
	}
	e.DropOn = g.dropTarget(e)
}

func (g *translateGesture) dropTarget(e *Editor) doc.ID {
	moving := g.moving()
	skip := make(map[doc.ID]bool, len(moving)*2)
	for _, id := range moving {
		skip[id] = true
		for _, k := range e.Descendants(id) {
			skip[k] = true
		}
	}
	return e.frameAt(e.pointer.Page, skip)
}

func (g *translateGesture) up(e *Editor) {
	target := g.dropTarget(e)
	e.DropOn = ""
	e.Snaps = nil
	moving := g.moving()
	e.reparentMoved(moving, target)
	e.unbindExternal(moving)
	e.commit()
}

// unbindExternal detaches arrow or line terminals bound to shapes outside ids,
// baking their current on-screen position before or after a move.
func (e *Editor) unbindExternal(ids []doc.ID) {
	moving := make(map[doc.ID]bool, len(ids)*2)
	for _, id := range ids {
		moving[id] = true
		for _, k := range e.Descendants(id) {
			moving[k] = true
		}
	}
	for id := range moving {
		s := e.Doc.Shapes[id]
		if s == nil || (s.Type != doc.TypeArrow && s.Type != doc.TypeLine) {
			continue
		}
		for _, term := range []string{"start", "end"} {
			if b := e.bindingOf(id, term); b != nil && !moving[b.To] {
				e.unbind(id, term)
			}
		}
	}
}

func (g *translateGesture) cancel(e *Editor) {
	e.DropOn = ""
	e.Snaps = nil
	e.rollback()
}

func (e *Editor) reparentMoved(ids []doc.ID, target doc.ID) {
	dest := target
	if dest == "" {
		dest = e.Page
	}
	for _, id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil {
			continue
		}
		if p := e.Doc.Shapes[s.Parent]; p != nil && p.Type == doc.TypeGroup {
			continue
		}
		if s.Parent == dest {
			continue
		}
		e.reparent(id, dest)
	}
}

func (e *Editor) reparent(id, parent doc.ID) {
	s := e.Doc.Shapes[id]
	if s == nil || id == parent || e.isAncestor(id, parent) {
		return
	}
	m := e.parentMat(parent).Invert().Mul(e.PageMat(id))
	c := s.Moved()
	c.Parent = parent
	c.Index = e.topIndex(parent)
	o := m.Apply(geo.Vec{})
	c.X, c.Y = o.X, o.Y
	c.Rotation = m.Rotation()
	e.put(c)
}

func (e *Editor) isAncestor(a, id doc.ID) bool {
	for cur := id; cur != "" && !doc.IsPageID(cur); {
		s := e.Doc.Shapes[cur]
		if s == nil {
			return false
		}
		if s.Parent == a {
			return true
		}
		cur = s.Parent
	}
	return false
}

// --- smart alignment guides & grid snap ---

type snapTargets struct {
	xs, ys []snapStop
}

type snapStop struct {
	at     float64
	lo, hi float64
}

func (e *Editor) snapPoint(p geo.Vec) geo.Vec {
	if !e.Grid {
		return p
	}
	step := e.GridStep()
	return geo.V(math.Round(p.X/step)*step, math.Round(p.Y/step)*step)
}

func (e *Editor) collectSnapTargets(skip []doc.ID) snapTargets {
	var t snapTargets
	view := e.ViewBox().Expand(200)
	skipping := make(map[doc.ID]bool, len(skip)*2)
	for _, id := range skip {
		skipping[id] = true
		for _, k := range e.Descendants(id) {
			skipping[k] = true
		}
	}
	var walk func(parent doc.ID)
	walk = func(parent doc.ID) {
		for _, id := range e.Children(parent) {
			if skipping[id] {
				continue
			}
			s := e.Doc.Shapes[id]
			if s == nil {
				continue
			}
			b := e.PageBounds(id)
			if !view.Overlaps(b) {
				continue
			}
			t.xs = append(t.xs,
				snapStop{b.X, b.Y, b.MaxY()},
				snapStop{b.X + b.W/2, b.Y, b.MaxY()},
				snapStop{b.MaxX(), b.Y, b.MaxY()},
			)
			t.ys = append(t.ys,
				snapStop{b.Y, b.X, b.MaxX()},
				snapStop{b.Y + b.H/2, b.X, b.MaxX()},
				snapStop{b.MaxY(), b.X, b.MaxX()},
			)
			if s.Type == doc.TypeFrame || s.Type == doc.TypeGroup {
				walk(id)
			}
		}
	}
	walk(e.Page)
	return t
}

func (e *Editor) snapTranslate(box geo.Box, d geo.Vec, t snapTargets) (geo.Vec, bool, bool) {
	moved := geo.Box{X: box.X + d.X, Y: box.Y + d.Y, W: box.W, H: box.H}
	th := 8 / e.Cam().Z
	bestX, atX := th, math.NaN()
	for _, x := range [3]float64{moved.X, moved.X + moved.W/2, moved.MaxX()} {
		for _, st := range t.xs {
			if dd := st.at - x; math.Abs(dd) < bestX {
				bestX, atX = math.Abs(dd), dd
			}
		}
	}
	bestY, atY := th, math.NaN()
	for _, y := range [3]float64{moved.Y, moved.Y + moved.H/2, moved.MaxY()} {
		for _, st := range t.ys {
			if dd := st.at - y; math.Abs(dd) < bestY {
				bestY, atY = math.Abs(dd), dd
			}
		}
	}
	snappedX := !math.IsNaN(atX)
	snappedY := !math.IsNaN(atY)
	if snappedX {
		d.X += atX
	}
	if snappedY {
		d.Y += atY
	}
	moved = geo.Box{X: box.X + d.X, Y: box.Y + d.Y, W: box.W, H: box.H}
	var lines []SnapLine
	for _, x := range [3]float64{moved.X, moved.X + moved.W/2, moved.MaxX()} {
		lo, hi := moved.Y, moved.MaxY()
		found := false
		for _, st := range t.xs {
			if math.Abs(st.at-x) < 0.01 {
				lo, hi, found = math.Min(lo, st.lo), math.Max(hi, st.hi), true
			}
		}
		if found {
			lines = append(lines, SnapLine{A: geo.V(x, lo), B: geo.V(x, hi)})
		}
	}
	for _, y := range [3]float64{moved.Y, moved.Y + moved.H/2, moved.MaxY()} {
		lo, hi := moved.X, moved.MaxX()
		found := false
		for _, st := range t.ys {
			if math.Abs(st.at-y) < 0.01 {
				lo, hi, found = math.Min(lo, st.lo), math.Max(hi, st.hi), true
			}
		}
		if found {
			lines = append(lines, SnapLine{A: geo.V(lo, y), B: geo.V(hi, y)})
		}
	}
	e.Snaps = lines
	return d, snappedX, snappedY
}

// --- rotate ---

type rotateGesture struct {
	ids      []doc.ID
	initial  map[doc.ID]*doc.Shape
	mats     map[doc.ID]geo.Mat
	center   geo.Vec
	start    float64
	rotation float64
	current  float64
}

func newRotateGesture(e *Editor) *rotateGesture {
	e.begin()
	g := &rotateGesture{
		initial: map[doc.ID]*doc.Shape{},
		mats:    map[doc.ID]geo.Mat{},
	}
	for _, id := range e.topSelected() {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			g.ids = append(g.ids, id)
			g.initial[id] = s
			g.mats[id] = e.PageMat(id)
		}
	}
	box, r, _ := e.SelectionFrame()
	g.center = geo.Rotate(r).Apply(box.Center())
	g.start = g.center.Angle(e.pointer.Page)
	g.rotation = r
	g.current = r
	return g
}

func (g *rotateGesture) move(e *Editor) {
	a := g.center.Angle(e.pointer.Page) - g.start
	if e.pointer.Mods.Shift {
		const step = math.Pi / 12 // 15 deg
		a = math.Round((g.rotation+a)/step)*step - g.rotation
	}
	g.current = g.rotation + a
	turn := geo.Translate(g.center.X, g.center.Y).Mul(geo.Rotate(a)).Mul(geo.Translate(-g.center.X, -g.center.Y))
	for _, id := range g.ids {
		init := g.initial[id]
		m := e.parentMat(init.Parent).Invert().Mul(turn).Mul(g.mats[id])
		c := init.Moved()
		o := m.Apply(geo.Vec{})
		c.X, c.Y = o.X, o.Y
		c.Rotation = init.Rotation + a
		e.put(c)
	}
}

func (g *rotateGesture) up(e *Editor)     { e.commit() }
func (g *rotateGesture) cancel(e *Editor) { e.rollback() }

// --- resize (8 handles, Shift uniform, Alt center, negative flip) ---

type resizeItem struct {
	id   doc.ID
	init *doc.Shape
	mat  geo.Mat
	rel  float64
}

type resizeGesture struct {
	handle   Handle
	box      geo.Box
	rotation float64
	items    []resizeItem
	uniform  bool
}

func newResizeGesture(e *Editor, h Handle) *resizeGesture {
	e.begin()
	box, r, _ := e.SelectionFrame()
	g := &resizeGesture{handle: h, box: box, rotation: r}
	var add func(id doc.ID)
	add = func(id doc.ID) {
		s := e.Doc.Shapes[id]
		if s == nil || s.Locked {
			return
		}
		if s.Type == doc.TypeGroup {
			for _, k := range e.Children(id) {
				add(k)
			}
			return
		}
		m := e.PageMat(id)
		rel := geo.ShortAngle(r, m.Rotation())
		g.items = append(g.items, resizeItem{id: id, init: s, mat: m, rel: rel})
		if math.Mod(math.Abs(rel), math.Pi/2) > 1e-3 && math.Abs(math.Mod(math.Abs(rel), math.Pi/2)-math.Pi/2) > 1e-3 {
			g.uniform = true
		}
	}
	for _, id := range e.topSelected() {
		add(id)
	}
	return g
}

func (g *resizeGesture) move(e *Editor) {
	p := e.pointer
	inv := geo.Rotate(-g.rotation)
	q := inv.Apply(p.Page)
	w := g.handle.Which

	anchor := geo.V(g.box.X+g.box.W*(1-w.X), g.box.Y+g.box.H*(1-w.Y))
	if p.Mods.Alt {
		anchor = g.box.Center()
	}
	moving := geo.V(g.box.X+g.box.W*w.X, g.box.Y+g.box.H*w.Y)
	dx, dy := moving.X-anchor.X, moving.Y-anchor.Y
	sx, sy := 1.0, 1.0
	if w.X != 0.5 && dx != 0 {
		sx = (q.X - anchor.X) / dx
	}
	if w.Y != 0.5 && dy != 0 {
		sy = (q.Y - anchor.Y) / dy
	}
	if g.uniform || p.Mods.Shift {
		switch {
		case w.X == 0.5:
			sx = math.Abs(sy) * signPositive(sx)
		case w.Y == 0.5:
			sy = math.Abs(sx) * signPositive(sy)
		default:
			k := math.Max(math.Abs(sx), math.Abs(sy))
			sx, sy = k*signPositive(sx), k*signPositive(sy)
		}
	}
	if math.Abs(sx) < 1e-4 {
		sx = signPositive(sx) * 1e-4
	}
	if math.Abs(sy) < 1e-4 {
		sy = signPositive(sy) * 1e-4
	}

	fwd := geo.Rotate(g.rotation)
	for _, it := range g.items {
		e.applyResizeItem(it, sx, sy, anchor, fwd, inv, w)
	}
}

func signPositive(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

func boxFlipOffset(w, h, lx, ly float64) geo.Vec {
	var o geo.Vec
	if lx < 0 {
		o.X = -w
	}
	if ly < 0 {
		o.Y = -h
	}
	return o
}

func (e *Editor) applyResizeItem(it resizeItem, sx, sy float64, anchor geo.Vec, fwd, inv geo.Mat, which geo.Vec) {
	lx, ly := sx, sy
	rel := geo.NormAngle(it.rel)
	if q := math.Mod(rel+math.Pi/4, math.Pi); q > math.Pi/2 {
		lx, ly = sy, sx
	}
	init := it.init
	o := inv.Apply(it.mat.Apply(geo.Vec{}))
	moved := geo.V(anchor.X+(o.X-anchor.X)*sx, anchor.Y+(o.Y-anchor.Y)*sy)
	c := init.Clone()
	turn := it.mat.Rotation()
	var offset geo.Vec

	switch init.Type {
	case doc.TypeGeo:
		c.W = math.Max(math.Abs(init.W*lx), 1)
		c.H = math.Max(math.Abs((init.H+init.GrowY)*ly), 1)
		c.GrowY = 0
		offset = boxFlipOffset(c.W, c.H, lx, ly)
		c.FlipX = init.FlipX != (lx < 0)
		c.FlipY = init.FlipY != (ly < 0)
		e.fitLabel(c)
	case doc.TypeFrame:
		c.W = math.Max(math.Abs(init.W*lx), 1)
		c.H = math.Max(math.Abs(init.H*ly), 1)
		offset = boxFlipOffset(c.W, c.H, lx, ly)
	case doc.TypeImage:
		c.W = math.Max(math.Abs(init.W*lx), 1)
		c.H = math.Max(math.Abs(init.H*ly), 1)
		offset = boxFlipOffset(c.W, c.H, lx, ly)
		c.FlipX = init.FlipX != (lx < 0)
		c.FlipY = init.FlipY != (ly < 0)
	case doc.TypeNote:
		c.W = math.Max(math.Abs(init.W*lx), 80)
		c.H = math.Max(math.Abs((init.H+init.GrowY)*ly), 60)
		c.GrowY = 0
		offset = boxFlipOffset(c.W, c.H, lx, ly)
		e.fitLabel(c)
	case doc.TypeText:
		baseScale := orOne(init.Scale)
		if init.AutoSize && which.X != 0.5 && which.Y != 0.5 {
			k := math.Abs(lx)
			c.Scale = math.Max(baseScale*k, 0.1)
			g0 := e.Geometry(init)
			offset = boxFlipOffset(g0.Bounds.W*k, g0.Bounds.H*k, lx, ly)
		} else {
			g0 := e.Geometry(init)
			c.AutoSize = false
			c.W = math.Max(math.Abs(g0.Bounds.W*lx)/baseScale, 12)
			offset = boxFlipOffset(c.W*baseScale, g0.Bounds.H, lx, 1)
			if ly < 0 {
				offset.Y = 0
			}
		}
	case doc.TypeDraw, doc.TypeHighlight:
		c.ScaleX = orOne(init.ScaleX) * lx
		c.ScaleY = orOne(init.ScaleY) * ly
	case doc.TypeLine:
		pts := make([]doc.LinePoint, len(init.Points))
		for i, lp := range init.Points {
			lp.X *= lx
			lp.Y *= ly
			pts[i] = lp
		}
		c.Points = pts
	case doc.TypeArrow:
		c.Start = geo.V(init.Start.X*lx, init.Start.Y*ly)
		c.End = geo.V(init.End.X*lx, init.End.Y*ly)
		c.Bend = init.Bend * math.Sqrt(math.Abs(lx*ly))
		if lx*ly < 0 {
			c.Bend = -c.Bend
		}
	default:
		c.W = math.Max(math.Abs(init.W*lx), 1)
		c.H = math.Max(math.Abs(init.H*ly), 1)
		offset = boxFlipOffset(c.W, c.H, lx, ly)
	}

	page := fwd.Apply(moved).Add(geo.Rotate(turn).Apply(offset))
	inP := e.parentMat(init.Parent).Invert().Apply(page)
	c.X, c.Y = inP.X, inP.Y
	e.put(c)
}

func (g *resizeGesture) up(e *Editor)     { e.commit() }
func (g *resizeGesture) cancel(e *Editor) { e.rollback() }

// --- cloning & nudging ---

func (e *Editor) cloneShapes(ids []doc.ID, offset geo.Vec) []doc.ID {
	idMap := map[doc.ID]doc.ID{}
	var roots []doc.ID
	var cloneOne func(id, newParent doc.ID, idx string) doc.ID
	cloneOne = func(id, newParent doc.ID, idx string) doc.ID {
		s := e.Doc.Shapes[id]
		if s == nil {
			return ""
		}
		nid := doc.NewRecordID("shape")
		idMap[id] = nid
		c := cloneShapeTreeData(s)
		c.ID = nid
		c.Parent = newParent
		c.Index = idx
		c.Seed = geo.Hash(string(nid))
		e.put(c)
		for _, kid := range e.Children(id) {
			ks := e.Doc.Shapes[kid]
			if ks == nil {
				continue
			}
			cloneOne(kid, nid, ks.Index)
		}
		return nid
	}

	for _, id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil {
			continue
		}
		nid := cloneOne(id, s.Parent, e.topIndex(s.Parent))
		if nid != "" {
			if offset.X != 0 || offset.Y != 0 {
				e.update(nid, func(c *doc.Shape) {
					local := e.parentMat(c.Parent).Invert().ApplyDir(offset)
					c.X += local.X
					c.Y += local.Y
				})
			}
			roots = append(roots, nid)
		}
	}
	// Remap bindings when both arrow/line and target were cloned together;
	// otherwise bake the visible endpoint onto the clone so it stays in place.
	for oldFrom, newFrom := range idMap {
		orig := e.Doc.Shapes[oldFrom]
		for _, term := range []string{"start", "end"} {
			b := e.bindingOf(oldFrom, term)
			if b == nil {
				continue
			}
			if newTo, ok := idMap[b.To]; ok {
				nb := *b
				nb.ID = doc.NewRecordID("binding")
				nb.From = newFrom
				nb.To = newTo
				e.putBinding(&nb)
				continue
			}
			if orig != nil {
				e.update(newFrom, func(c *doc.Shape) {
					e.bakeUnclonedTerminal(orig, c, term)
				})
			}
		}
	}
	return roots
}

// Nudge shifts the selected unlocked shapes by (dx, dy) in page coordinates in one undo step.
func (e *Editor) Nudge(dx, dy float64) {
	if len(e.sel) == 0 || (dx == 0 && dy == 0) {
		return
	}
	d := geo.V(dx, dy)
	e.transact(func() {
		var unlocked []doc.ID
		for _, id := range e.topSelected() {
			if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
				unlocked = append(unlocked, id)
			}
		}
		e.unbindExternal(unlocked)
		for _, id := range unlocked {
			s := e.Doc.Shapes[id]
			if s == nil {
				continue
			}
			local := e.parentMat(s.Parent).Invert().ApplyDir(d)
			c := s.Moved()
			c.X += local.X
			c.Y += local.Y
			e.put(c)
		}
	})
}
