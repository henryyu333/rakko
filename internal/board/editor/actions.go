package editor

import (
	"math"
	"slices"
	"sort"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

type boardClipItem struct {
	shape *doc.Shape
	isRoot bool
}

type boardClip struct {
	items    []boardClipItem
	bindings []*doc.Binding
}

type styleSnapshot struct {
	Color     string
	FillColor string
	Fill      string
	Size      string
	Dash      string
	Opacity   float64
	Font      string
	FontSize  int
	Align     string
	VAlign    string
	TextAlign string
	Bold      bool
	Italic    bool
	Underline bool
	PenKind   string
	PenWidth  float64
	ArrowKind string
	HeadStart string
	HeadEnd   string
	Spline    string
}

func cloneShapeTreeData(s *doc.Shape) *doc.Shape {
	c := s.Clone()
	if len(s.Segments) > 0 {
		segs := make([]doc.Segment, len(s.Segments))
		for i, seg := range s.Segments {
			seg.Points = slices.Clone(seg.Points)
			segs[i] = seg
		}
		c.Segments = segs
	}
	if len(s.Points) > 0 {
		c.Points = slices.Clone(s.Points)
	}
	if s.Crop != nil {
		cp := *s.Crop
		c.Crop = &cp
	}
	return c
}

// --- Navigation, Fit & Minimap ---

// PageContentBox returns the union page-space bounding box of all shapes on pageID.
func (e *Editor) PageContentBox(pageID doc.ID) (geo.Box, bool) {
	var box geo.Box
	first := true
	var walk func(parent doc.ID)
	walk = func(parent doc.ID) {
		for _, id := range e.Children(parent) {
			s := e.Doc.Shapes[id]
			if s == nil {
				continue
			}
			if s.Type == doc.TypeGroup || s.Type == doc.TypeFrame {
				walk(id)
				if s.Type == doc.TypeGroup {
					continue
				}
			}
			b := e.PageBounds(id)
			if first {
				box, first = b, false
			} else {
				box = box.Union(b)
			}
		}
	}
	walk(pageID)
	return box, !first
}

// SelectionContentBox returns the union page-space bounding box of currently selected shapes.
func (e *Editor) SelectionContentBox() (geo.Box, bool) {
	return e.SelectionBox()
}

// FitBox zooms and centers the camera onto box inside the viewport.
func (e *Editor) FitBox(box geo.Box, padX, padY float64) {
	vw := math.Max(e.VW, 200)
	vh := math.Max(e.VH, 200)
	availW := math.Max(vw-padX, 60)
	availH := math.Max(vh-padY, 60)
	z := math.Min(2.0, math.Min(availW/math.Max(box.W, 1), availH/math.Max(box.H, 1)))
	z = geo.Clamp(z, MinZoom, MaxZoom)
	c := box.Center()
	e.SetCam(Camera{
		X: vw/(2*z) - c.X,
		Y: vh/(2*z) - c.Y,
		Z: z,
	})
}

// ZoomToFit fits all shapes on the active page into the viewport.
func (e *Editor) ZoomToFit() {
	box, ok := e.PageContentBox(e.Page)
	if !ok {
		e.SetCam(Camera{Z: 1})
		return
	}
	e.FitBox(box, 140, 180)
}

// ZoomToSelection fits all selected shapes into the viewport.
func (e *Editor) ZoomToSelection() {
	box, ok := e.SelectionBox()
	if !ok {
		return
	}
	e.FitBox(box, 160, 200)
}

// ContentVisible reports whether the active page is empty or has at least one
// shape overlapping the current camera viewport.
func (e *Editor) ContentVisible() bool {
	hasAny := false
	vb := e.ViewBox()
	var walk func(parent doc.ID) bool
	walk = func(parent doc.ID) bool {
		for _, id := range e.Children(parent) {
			s := e.Doc.Shapes[id]
			if s == nil {
				continue
			}
			hasAny = true
			if s.Type == doc.TypeGroup {
				if walk(id) {
					return true
				}
				continue
			}
			if vb.Overlaps(e.PageBounds(id)) {
				return true
			}
			if s.Type == doc.TypeFrame && walk(id) {
				return true
			}
		}
		return false
	}
	vis := walk(e.Page)
	return !hasAny || vis
}

// CenterOn pans the camera so pagePt sits at the viewport center while keeping current zoom.
func (e *Editor) CenterOn(pagePt geo.Vec) {
	c := e.Cam()
	vw := math.Max(e.VW, 200)
	vh := math.Max(e.VH, 200)
	e.SetCam(Camera{
		X: vw/(2*c.Z) - pagePt.X,
		Y: vh/(2*c.Z) - pagePt.Y,
		Z: c.Z,
	})
}

// ToggleToolLock flips ToolLocked (shortcut Q) so creation tools remain active after drawing.
func (e *Editor) ToggleToolLock() bool {
	e.ToolLocked = !e.ToolLocked
	return e.ToolLocked
}

// RightClickAt updates selection cleanly before opening a context menu at screenPt.
func (e *Editor) RightClickAt(screenPt geo.Vec) {
	e.StopEditing()
	e.cancelGesture()
	p := e.ToPage(screenPt)
	h := e.Hit(p, e.margin(), true)
	if h.ID != "" {
		if !e.IsSelected(h.ID) {
			e.Select(h.ID)
		}
		return
	}
	e.SelectNone()
}

// --- Clipboard, Duplicate & Style Clipboard ---

// HasClipboard reports whether shapes have been copied to the board clipboard.
func (e *Editor) HasClipboard() bool {
	return e.clip != nil && len(e.clip.items) > 0
}

// HasStyleClip reports whether a style snapshot has been copied.
func (e *Editor) HasStyleClip() bool {
	return e.styleClip != nil
}

// Copy saves currently selected shapes (and internal bindings) to the board clipboard.
func (e *Editor) Copy() bool {
	roots := e.topSelected()
	if len(roots) == 0 {
		return false
	}
	cl := &boardClip{}
	seen := map[doc.ID]bool{}
	var collect func(id doc.ID, isRoot bool)
	collect = func(id doc.ID, isRoot bool) {
		if seen[id] {
			return
		}
		s := e.Doc.Shapes[id]
		if s == nil {
			return
		}
		seen[id] = true
		cl.items = append(cl.items, boardClipItem{
			shape:  cloneShapeTreeData(s),
			isRoot: isRoot,
		})
		for _, kid := range e.Children(id) {
			collect(kid, false)
		}
	}
	for _, id := range roots {
		collect(id, true)
	}
	for id := range seen {
		for _, term := range []string{"start", "end"} {
			if b := e.bindingOf(id, term); b != nil && seen[b.To] {
				cp := *b
				cl.bindings = append(cl.bindings, &cp)
			}
		}
	}
	e.clip = cl
	return len(cl.items) > 0
}

// Cut copies selected shapes and deletes unlocked ones in one undo step.
func (e *Editor) Cut() bool {
	if !e.Copy() {
		return false
	}
	e.DeleteSelected()
	return true
}

// Paste inserts copied shapes onto the active page offset by (24, 24).
func (e *Editor) Paste() bool {
	if !e.HasClipboard() {
		return false
	}
	e.begin()
	idMap := make(map[doc.ID]doc.ID, len(e.clip.items))
	for _, item := range e.clip.items {
		idMap[item.shape.ID] = doc.NewRecordID("shape")
	}
	var newRoots []doc.ID
	for _, item := range e.clip.items {
		c := cloneShapeTreeData(item.shape)
		oldID := item.shape.ID
		nid := idMap[oldID]
		c.ID = nid
		c.Seed = geo.Hash(string(nid))
		if item.isRoot {
			targetParent := item.shape.Parent
			if doc.IsPageID(targetParent) || e.Doc.Shapes[targetParent] == nil {
				targetParent = e.Page
			}
			c.Parent = targetParent
			c.Index = e.topIndex(targetParent)
			local := e.parentMat(targetParent).Invert().ApplyDir(geo.V(24, 24))
			c.X += local.X
			c.Y += local.Y
			newRoots = append(newRoots, nid)
		} else if np, ok := idMap[item.shape.Parent]; ok {
			c.Parent = np
		}
		e.put(c)
	}
	for _, b := range e.clip.bindings {
		nb := *b
		nb.ID = doc.NewRecordID("binding")
		nb.From = idMap[b.From]
		nb.To = idMap[b.To]
		if nb.From != "" && nb.To != "" {
			e.putBinding(&nb)
		}
	}
	e.Select(newRoots...)
	e.commit()
	return len(newRoots) > 0
}

// Duplicate clones selected shapes offset by (24, 24) in one undoable step.
func (e *Editor) Duplicate() bool {
	roots := e.topSelected()
	if len(roots) == 0 {
		return false
	}
	e.begin()
	cloned := e.cloneShapes(roots, geo.V(24, 24))
	if len(cloned) == 0 {
		e.rollback()
		return false
	}
	e.Select(cloned...)
	e.commit()
	return true
}

// CopyStyle copies the visual styling of the first selected shape.
func (e *Editor) CopyStyle() bool {
	targets := e.styledTargets()
	if len(targets) == 0 {
		return false
	}
	s := targets[0]
	e.styleClip = &styleSnapshot{
		Color:     s.Color,
		FillColor: s.FillColor,
		Fill:      s.Fill,
		Size:      s.Size,
		Dash:      s.Dash,
		Opacity:   s.Opacity,
		Font:      s.Font,
		FontSize:  s.FontSize,
		Align:     s.Align,
		VAlign:    s.VAlign,
		TextAlign: s.TextAlign,
		Bold:      s.Bold,
		Italic:    s.Italic,
		Underline: s.Underline,
		PenKind:   s.PenKind,
		PenWidth:  s.PenWidth,
		ArrowKind: s.Kind,
		HeadStart: s.HeadStart,
		HeadEnd:   s.HeadEnd,
		Spline:    s.Spline,
	}
	return true
}

// PasteStyle applies the copied style snapshot to selected unlocked shapes.
func (e *Editor) PasteStyle() bool {
	if e.styleClip == nil {
		return false
	}
	st := e.styleClip
	e.mutateStyled(func(c *doc.Shape) {
		if st.Color != "" {
			c.Color = st.Color
		}
		if st.FillColor != "" {
			c.FillColor = st.FillColor
		}
		if st.Fill != "" {
			c.Fill = st.Fill
		}
		if st.Size != "" {
			c.Size = st.Size
		}
		if st.Dash != "" {
			c.Dash = st.Dash
		}
		if st.Opacity > 0 {
			c.Opacity = st.Opacity
		}
		if st.Font != "" {
			c.Font = st.Font
		}
		if st.FontSize > 0 {
			c.FontSize = st.FontSize
		}
		if st.Align != "" {
			c.Align = st.Align
		}
		if st.VAlign != "" {
			c.VAlign = st.VAlign
		}
		if st.TextAlign != "" {
			c.TextAlign = st.TextAlign
		}
		c.Bold = st.Bold
		c.Italic = st.Italic
		c.Underline = st.Underline
		if c.Type == doc.TypeDraw {
			if st.PenKind != "" {
				c.PenKind = st.PenKind
				c.IsPen = st.PenKind == "pen"
			}
			if st.PenWidth > 0 {
				c.PenWidth = st.PenWidth
			}
		}
		if c.Type == doc.TypeArrow {
			if st.ArrowKind != "" {
				c.Kind = st.ArrowKind
			}
			if st.HeadStart != "" {
				c.HeadStart = st.HeadStart
			}
			if st.HeadEnd != "" {
				c.HeadEnd = st.HeadEnd
			}
		}
		if c.Type == doc.TypeLine && st.Spline != "" {
			c.Spline = st.Spline
		}
		e.fitLabel(c)
	})
	return len(e.styledTargets()) > 0
}

// --- Grouping & Locking ---

// HasGroupedSelected reports whether any top-selected shape is a group.
func (e *Editor) HasGroupedSelected() bool {
	for _, id := range e.topSelected() {
		if s := e.Doc.Shapes[id]; s != nil && s.Type == doc.TypeGroup {
			return true
		}
	}
	return false
}

// GroupSelected wraps >= 2 top-selected unlocked shapes into a new TypeGroup.
func (e *Editor) GroupSelected() bool {
	ids := e.topSelected()
	var valid []doc.ID
	for _, id := range ids {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			valid = append(valid, id)
		}
	}
	if len(valid) < 2 {
		return false
	}
	parent := e.Doc.Shapes[valid[0]].Parent
	for _, id := range valid[1:] {
		if e.Doc.Shapes[id].Parent != parent {
			parent = e.Page
			break
		}
	}
	if parent == "" {
		parent = e.Page
	}
	e.begin()
	gid := doc.NewRecordID("shape")
	g := doc.NewShape(gid, doc.TypeGroup)
	g.Parent = parent
	g.Index = e.topIndex(parent)
	e.put(g)

	// Keep children ordered stably and preserve their page transforms when moving into group.
	siblings := e.Children(parent)
	orderMap := make(map[doc.ID]int, len(siblings))
	for i, sid := range siblings {
		orderMap[sid] = i
	}
	sort.SliceStable(valid, func(i, j int) bool {
		return orderMap[valid[i]] < orderMap[valid[j]]
	})
	idx := ""
	pInv := e.parentMat(parent).Invert()
	for _, id := range valid {
		idx = doc.IndexBetween(idx, "")
		m := pInv.Mul(e.PageMat(id))
		o := m.Apply(geo.Vec{})
		rot := m.Rotation()
		c := e.Doc.Shapes[id].Moved()
		c.Parent = gid
		c.Index = idx
		c.X, c.Y, c.Rotation = o.X, o.Y, rot
		e.put(c)
	}
	e.Select(gid)
	e.commit()
	return true
}

// UngroupSelected dissolves selected TypeGroup containers and promotes children to group's parent.
func (e *Editor) UngroupSelected() bool {
	ids := e.topSelected()
	var groups []doc.ID
	for _, id := range ids {
		if s := e.Doc.Shapes[id]; s != nil && s.Type == doc.TypeGroup && !s.Locked {
			groups = append(groups, id)
		}
	}
	if len(groups) == 0 {
		return false
	}
	e.begin()
	var promoted []doc.ID
	for _, gid := range groups {
		g := e.Doc.Shapes[gid]
		if g == nil {
			continue
		}
		targetParent := g.Parent
		if targetParent == "" {
			targetParent = e.Page
		}
		pInv := e.parentMat(targetParent).Invert()
		kids := slices.Clone(e.Children(gid))
		siblings := e.Children(targetParent)
		pos := slices.Index(siblings, gid)
		upper := ""
		if pos >= 0 && pos+1 < len(siblings) {
			upper = e.Doc.Shapes[siblings[pos+1]].Index
		}
		prev := g.Index
		for _, kid := range kids {
			ks := e.Doc.Shapes[kid]
			if ks == nil {
				continue
			}
			m := pInv.Mul(e.PageMat(kid))
			o := m.Apply(geo.Vec{})
			prev = doc.IndexBetween(prev, upper)
			c := ks.Moved()
			c.Parent = targetParent
			c.Index = prev
			c.X, c.Y, c.Rotation = o.X, o.Y, m.Rotation()
			e.put(c)
			promoted = append(promoted, kid)
		}
		e.removeShape(gid)
	}
	e.Select(promoted...)
	e.commit()
	return true
}

// AllSelectionLocked reports whether every selected shape is locked.
func (e *Editor) AllSelectionLocked() bool {
	return len(e.sel) > 0 && e.allLocked()
}

// ToggleLockSelected locks or unlocks the active selection. Locking clears selection.
func (e *Editor) ToggleLockSelected() bool {
	ids := e.topSelected()
	if len(ids) == 0 {
		return false
	}
	toLock := !e.allLocked()
	e.begin()
	for _, id := range ids {
		var walk func(cur doc.ID)
		walk = func(cur doc.ID) {
			s := e.Doc.Shapes[cur]
			if s == nil {
				return
			}
			c := s.Moved()
			c.Locked = toLock
			e.put(c)
			for _, kid := range e.Children(cur) {
				walk(kid)
			}
		}
		walk(id)
	}
	if toLock {
		e.SelectNone()
	}
	e.commit()
	return true
}

// HasLockedOnPage reports whether any shape on the active page is locked.
func (e *Editor) HasLockedOnPage() bool {
	var walk func(parent doc.ID) bool
	walk = func(parent doc.ID) bool {
		for _, id := range e.Children(parent) {
			s := e.Doc.Shapes[id]
			if s == nil {
				continue
			}
			if s.Locked || walk(id) {
				return true
			}
		}
		return false
	}
	return walk(e.Page)
}

// UnlockAll unlocks all locked shapes on the active page and returns how many were unlocked.
func (e *Editor) UnlockAll() int {
	var ids []doc.ID
	var walk func(parent doc.ID)
	walk = func(parent doc.ID) {
		for _, id := range e.Children(parent) {
			s := e.Doc.Shapes[id]
			if s == nil {
				continue
			}
			if s.Locked {
				ids = append(ids, id)
			}
			walk(id)
		}
	}
	walk(e.Page)
	if len(ids) == 0 {
		return 0
	}
	e.begin()
	for _, id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil {
			continue
		}
		c := s.Moved()
		c.Locked = false
		e.put(c)
	}
	e.commit()
	return len(ids)
}

// --- Z-Order ---

func (e *Editor) reorderSelected(kind string) bool {
	sel := e.topSelected()
	if len(sel) == 0 {
		return false
	}
	selSet := make(map[doc.ID]bool, len(sel))
	parents := map[doc.ID]bool{}
	for _, id := range sel {
		if s := e.Doc.Shapes[id]; s != nil {
			selSet[id] = true
			parents[s.Parent] = true
		}
	}
	changed := false
	for parent := range parents {
		kids := slices.Clone(e.Children(parent))
		if len(kids) < 2 {
			continue
		}
		nextOrder := slices.Clone(kids)
		switch kind {
		case "front":
			var rest, picked []doc.ID
			for _, id := range kids {
				if selSet[id] {
					picked = append(picked, id)
				} else {
					rest = append(rest, id)
				}
			}
			nextOrder = append(rest, picked...)
		case "back":
			var rest, picked []doc.ID
			for _, id := range kids {
				if selSet[id] {
					picked = append(picked, id)
				} else {
					rest = append(rest, id)
				}
			}
			nextOrder = append(picked, rest...)
		case "fwd":
			for i := len(nextOrder) - 2; i >= 0; i-- {
				if selSet[nextOrder[i]] && !selSet[nextOrder[i+1]] {
					nextOrder[i], nextOrder[i+1] = nextOrder[i+1], nextOrder[i]
				}
			}
		case "bwd":
			for i := 1; i < len(nextOrder); i++ {
				if selSet[nextOrder[i]] && !selSet[nextOrder[i-1]] {
					nextOrder[i], nextOrder[i-1] = nextOrder[i-1], nextOrder[i]
				}
			}
		}
		if slices.Equal(kids, nextOrder) {
			continue
		}
		if !changed {
			e.begin()
			changed = true
		}
		// Re-assign clean strictly-increasing fractional indices across changed positions.
		idx := ""
		for _, id := range nextOrder {
			idx = doc.IndexBetween(idx, "")
			s := e.Doc.Shapes[id]
			if s == nil || s.Index == idx {
				continue
			}
			c := s.Moved()
			c.Index = idx
			e.put(c)
		}
	}
	if changed {
		e.commit()
	}
	return changed
}

func (e *Editor) BringToFront() bool { return e.reorderSelected("front") }
func (e *Editor) BringForward() bool { return e.reorderSelected("fwd") }
func (e *Editor) SendBackward() bool { return e.reorderSelected("bwd") }
func (e *Editor) SendToBack() bool   { return e.reorderSelected("back") }

// --- Align, Distribute, Stack, Pack, Stretch, Rotate 90, Flip ---

func (e *Editor) unlockedTopSelected() []doc.ID {
	var out []doc.ID
	for _, id := range e.topSelected() {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			out = append(out, id)
		}
	}
	return out
}

func (e *Editor) shiftShapePage(id doc.ID, dx, dy float64) {
	s := e.Doc.Shapes[id]
	if s == nil {
		return
	}
	local := e.parentMat(s.Parent).Invert().ApplyDir(geo.V(dx, dy))
	c := s.Moved()
	c.X += local.X
	c.Y += local.Y
	e.put(c)
}

type arrangeEntry struct {
	id  doc.ID
	box geo.Box
}

func (e *Editor) arrangeEntries(ids []doc.ID) ([]arrangeEntry, geo.Box) {
	out := make([]arrangeEntry, 0, len(ids))
	var union geo.Box
	first := true
	for _, id := range ids {
		b := e.PageBounds(id)
		out = append(out, arrangeEntry{id: id, box: b})
		if first {
			union, first = b, false
		} else {
			union = union.Union(b)
		}
	}
	return out, union
}

// AlignSelected aligns >= 2 unlocked selected shapes ("left", "center", "right", "top", "middle", "bottom").
func (e *Editor) AlignSelected(dir string) bool {
	ids := e.unlockedTopSelected()
	if len(ids) < 2 {
		return false
	}
	entries, u := e.arrangeEntries(ids)
	e.begin()
	for _, it := range entries {
		var dx, dy float64
		switch dir {
		case "left":
			dx = u.X - it.box.X
		case "center":
			dx = (u.X + u.W/2) - (it.box.X + it.box.W/2)
		case "right":
			dx = (u.X + u.W) - (it.box.X + it.box.W)
		case "top":
			dy = u.Y - it.box.Y
		case "middle":
			dy = (u.Y + u.H/2) - (it.box.Y + it.box.H/2)
		case "bottom":
			dy = (u.Y + u.H) - (it.box.Y + it.box.H)
		default:
			e.rollback()
			return false
		}
		if dx != 0 || dy != 0 {
			e.shiftShapePage(it.id, dx, dy)
		}
	}
	e.commit()
	return true
}

// DistributeSelected evenly spaces >= 3 selected shapes along axis ("x" or "y").
func (e *Editor) DistributeSelected(axis string) bool {
	ids := e.unlockedTopSelected()
	if len(ids) < 3 {
		return false
	}
	entries, _ := e.arrangeEntries(ids)
	if axis == "x" {
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].box.X < entries[j].box.X })
		first, last := entries[0].box.X, entries[len(entries)-1].box.MaxX()
		sum := 0.0
		for _, it := range entries {
			sum += it.box.W
		}
		gap := (last - first - sum) / float64(len(entries)-1)
		e.begin()
		pos := first
		for _, it := range entries {
			e.shiftShapePage(it.id, pos-it.box.X, 0)
			pos += it.box.W + gap
		}
		e.commit()
		return true
	}
	if axis == "y" {
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].box.Y < entries[j].box.Y })
		first, last := entries[0].box.Y, entries[len(entries)-1].box.MaxY()
		sum := 0.0
		for _, it := range entries {
			sum += it.box.H
		}
		gap := (last - first - sum) / float64(len(entries)-1)
		e.begin()
		pos := first
		for _, it := range entries {
			e.shiftShapePage(it.id, 0, pos-it.box.Y)
			pos += it.box.H + gap
		}
		e.commit()
		return true
	}
	return false
}

// StackSelected stacks >= 2 shapes with 16px spacing along axis ("x" or "y").
func (e *Editor) StackSelected(axis string) bool {
	ids := e.unlockedTopSelected()
	if len(ids) < 2 {
		return false
	}
	const gap = 16.0
	entries, _ := e.arrangeEntries(ids)
	if axis == "x" {
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].box.X < entries[j].box.X })
		e.begin()
		pos := entries[0].box.X
		for _, it := range entries {
			e.shiftShapePage(it.id, pos-it.box.X, 0)
			pos += it.box.W + gap
		}
		e.commit()
		return true
	}
	if axis == "y" {
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].box.Y < entries[j].box.Y })
		e.begin()
		pos := entries[0].box.Y
		for _, it := range entries {
			e.shiftShapePage(it.id, 0, pos-it.box.Y)
			pos += it.box.H + gap
		}
		e.commit()
		return true
	}
	return false
}

// PackSelected arranges >= 2 shapes into a neat compact grid starting at their union top-left.
func (e *Editor) PackSelected() bool {
	ids := e.unlockedTopSelected()
	if len(ids) < 2 {
		return false
	}
	const gap = 16.0
	entries, u := e.arrangeEntries(ids)
	cols := int(math.Ceil(math.Sqrt(float64(len(entries)))))
	e.begin()
	x, y, rowH := u.X, u.Y, 0.0
	for i, it := range entries {
		if i > 0 && i%cols == 0 {
			x = u.X
			y += rowH + gap
			rowH = 0
		}
		e.shiftShapePage(it.id, x-it.box.X, y-it.box.Y)
		x += it.box.W + gap
		if it.box.H > rowH {
			rowH = it.box.H
		}
	}
	e.commit()
	return true
}

// StretchSelected resizes all non-linear selected shapes to match the maximum width ("w") or height ("h").
func (e *Editor) StretchSelected(dim string) bool {
	if dim != "w" && dim != "h" {
		return false
	}
	ids := e.unlockedTopSelected()
	var boxes []*doc.Shape
	maxV := 0.0
	for _, id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil || s.Type == doc.TypeArrow || s.Type == doc.TypeLine || s.Type == doc.TypeGroup || s.Type == doc.TypeDraw || s.Type == doc.TypeHighlight {
			continue
		}
		boxes = append(boxes, s)
		if dim == "w" && s.W > maxV {
			maxV = s.W
		}
		if dim == "h" && s.H > maxV {
			maxV = s.H
		}
	}
	if len(boxes) < 2 || maxV <= 0 {
		return false
	}
	e.begin()
	for _, s := range boxes {
		c := s.Clone()
		if dim == "w" {
			c.W = maxV
		} else {
			c.H = maxV
		}
		e.fitLabel(c)
		e.put(c)
	}
	e.commit()
	return true
}

// HasTransformableSelected reports whether any unlocked top-selected shape supports Rotate/Flip.
func (e *Editor) HasTransformableSelected() bool {
	return len(e.unlockedTopSelected()) > 0
}

// RotateSelected90 rotates unlocked selected shapes by dir*90 degrees around the selection center.
func (e *Editor) RotateSelected90(dir int) bool {
	ids := e.unlockedTopSelected()
	if len(ids) == 0 {
		return false
	}
	box, rot, ok := e.SelectionFrame()
	if !ok {
		return false
	}
	center := geo.Rotate(rot).Apply(box.Center())
	angle := math.Pi / 2
	if dir < 0 {
		angle = -math.Pi / 2
	}
	turn := geo.Translate(center.X, center.Y).Mul(geo.Rotate(angle)).Mul(geo.Translate(-center.X, -center.Y))
	e.begin()
	for _, id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil {
			continue
		}
		pm := e.PageMat(id)
		m := e.parentMat(s.Parent).Invert().Mul(turn).Mul(pm)
		c := s.Moved()
		o := m.Apply(geo.Vec{})
		c.X, c.Y = o.X, o.Y
		c.Rotation = s.Rotation + angle
		e.put(c)
	}
	e.commit()
	return true
}

// FlipSelected mirrors unlocked selected shapes horizontally ("x") or vertically ("y").
func (e *Editor) FlipSelected(axis string) bool {
	if axis != "x" && axis != "y" {
		return false
	}
	ids := e.unlockedTopSelected()
	if len(ids) == 0 {
		return false
	}
	_, u := e.arrangeEntries(ids)
	cx, cy := u.X+u.W/2, u.Y+u.H/2
	e.begin()
	for _, id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil {
			continue
		}
		if s.Type == doc.TypeArrow || s.Type == doc.TypeLine {
			c := s.Clone()
			pMat := e.PageMat(id)
			pInv := pMat.Invert()
			mirrorPt := func(pt geo.Vec) geo.Vec {
				wp := pMat.Apply(pt)
				if axis == "x" {
					wp.X = 2*cx - wp.X
				} else {
					wp.Y = 2*cy - wp.Y
				}
				return pInv.Apply(wp)
			}
			if s.Type == doc.TypeArrow {
				c.Start = mirrorPt(c.Start)
				c.End = mirrorPt(c.End)
				c.Bend = -c.Bend
			}
			for i := range c.Points {
				mp := mirrorPt(geo.V(c.Points[i].X, c.Points[i].Y))
				c.Points[i].X, c.Points[i].Y = mp.X, mp.Y
			}
			e.put(c)
			continue
		}
		// Box-like shape: mirror its page center across axis and invert flip flag + rotation.
		b := e.PageBounds(id)
		bc := b.Center()
		var dx, dy float64
		if axis == "x" {
			dx = (2*cx - bc.X) - bc.X
		} else {
			dy = (2*cy - bc.Y) - bc.Y
		}
		pInv := e.parentMat(s.Parent).Invert()
		localShift := pInv.ApplyDir(geo.V(dx, dy))
		c := s.Clone()
		c.X += localShift.X
		c.Y += localShift.Y
		if axis == "x" {
			c.FlipX = !c.FlipX
		} else {
			c.FlipY = !c.FlipY
		}
		if s.Rotation != 0 {
			// Preserve visual center when negating rotation around shape center.
			beforeC := e.PageMat(id).Apply(e.Geometry(s).Bounds.Center())
			c.Rotation = -s.Rotation
			e.put(c)
			afterC := e.PageMat(id).Apply(e.Geometry(c).Bounds.Center())
			adj := pInv.ApplyDir(beforeC.Sub(afterC))
			c2 := c.Clone()
			c2.X += adj.X
			c2.Y += adj.Y
			e.put(c2)
			continue
		}
		e.put(c)
	}
	e.commit()
	return true
}

// --- Multi-page management ---

// Pages returns all pages in the active drawing in order.
func (e *Editor) Pages() []*doc.Page {
	return slices.Clone(e.Doc.Pages)
}

// ActivePageInfo returns the active Page record.
func (e *Editor) ActivePageInfo() *doc.Page {
	return e.Doc.Page(e.Page)
}

// PageItemCount returns the number of direct shapes on pageID.
func (e *Editor) PageItemCount(pageID doc.ID) int {
	return len(e.Children(pageID))
}

// AddPage appends a new page, switches to it, and notifies OnChange.
func (e *Editor) AddPage(name string) *doc.Page {
	p := e.Doc.AddPage(name)
	e.SwitchPage(p.ID)
	e.emitChange()
	return p
}

// RenamePage renames page id and notifies OnChange.
func (e *Editor) RenamePage(id doc.ID, name string) bool {
	if name == "" || !e.Doc.RenamePage(id, name) {
		return false
	}
	e.emitChange()
	return true
}

// DeletePage removes page id (unless it is the last page), cleaning its shapes and switching active page.
func (e *Editor) DeletePage(id doc.ID) error {
	if err := e.Doc.DeletePage(id); err != nil {
		return err
	}
	// Purge any shapes and bindings attached to deleted page id cleanly.
	toDrop := map[doc.ID]bool{}
	var collect func(parent doc.ID)
	collect = func(parent doc.ID) {
		for _, sid := range e.Children(parent) {
			toDrop[sid] = true
			collect(sid)
		}
	}
	collect(id)
	for sid := range toDrop {
		delete(e.Doc.Shapes, sid)
	}
	for bid, b := range e.Doc.Bindings {
		if toDrop[b.From] || toDrop[b.To] {
			delete(e.Doc.Bindings, bid)
		}
	}
	delete(e.cams, id)
	e.kids, e.bindFrom, e.bindTo, e.arrows = nil, nil, nil, nil
	if e.Page == id || e.Doc.Page(e.Page) == nil {
		e.Page = e.Doc.ActivePage
		e.SelectNone()
		e.Editing, e.EditingVertices, e.FocusedGroup = "", "", ""
	}
	e.emitChange()
	return nil
}

// MoveSelectedToPage moves top-selected shapes to targetPage in one undoable step.
func (e *Editor) MoveSelectedToPage(targetPage doc.ID) int {
	if targetPage == "" || targetPage == e.Page || e.Doc.Page(targetPage) == nil {
		return 0
	}
	ids := e.unlockedTopSelected()
	if len(ids) == 0 {
		return 0
	}
	movedSet := map[doc.ID]bool{}
	for _, id := range ids {
		movedSet[id] = true
		for _, d := range e.Descendants(id) {
			movedSet[d] = true
		}
	}
	e.begin()
	for _, id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil {
			continue
		}
		// Compute current page transform so nested-in-frame items keep their visual position.
		m := e.PageMat(id)
		o := m.Apply(geo.Vec{})
		c := s.Moved()
		c.Parent = targetPage
		c.Index = e.topIndex(targetPage)
		c.X, c.Y, c.Rotation = o.X, o.Y, m.Rotation()
		e.put(c)
	}
	// Remove bindings whose target stayed on the old page (or vice versa).
	for bid, b := range e.Doc.Bindings {
		if movedSet[b.From] != movedSet[b.To] {
			e.removeBinding(bid)
		}
	}
	e.SelectNone()
	e.commit()
	return len(ids)
}
