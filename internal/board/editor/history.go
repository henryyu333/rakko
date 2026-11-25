package editor

import (
	"slices"
	"strings"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

type change[T any] struct {
	before, after *T
}

type historyEntry struct {
	shapes                map[doc.ID]change[doc.Shape]
	bindings              map[doc.ID]change[doc.Binding]
	assets                map[doc.ID]change[doc.Asset]
	pages                 *change[[]*doc.Page]
	name                  *change[string]
	selBefore             []doc.ID
	selAfter              []doc.ID
	pageBefore, pageAfter doc.ID
}

func (x *historyEntry) empty() bool {
	return len(x.shapes) == 0 && len(x.bindings) == 0 && len(x.assets) == 0 && x.pages == nil && x.name == nil
}

func (e *Editor) begin() {
	if e.tx != nil {
		return
	}
	e.tx = &historyEntry{
		shapes:     map[doc.ID]change[doc.Shape]{},
		bindings:   map[doc.ID]change[doc.Binding]{},
		assets:     map[doc.ID]change[doc.Asset]{},
		selBefore:  slices.Clone(e.sel),
		pageBefore: e.Page,
	}
}

func (e *Editor) commit() {
	x := e.tx
	if x == nil {
		return
	}
	e.tx = nil
	for id, c := range x.shapes {
		if c.before == c.after {
			delete(x.shapes, id)
		}
	}
	for id, c := range x.bindings {
		if c.before == c.after {
			delete(x.bindings, id)
		}
	}
	for id, c := range x.assets {
		if c.before == c.after {
			delete(x.assets, id)
		}
	}
	if x.empty() {
		return
	}
	x.selAfter = slices.Clone(e.sel)
	x.pageAfter = e.Page
	e.undo = append(e.undo, x)
	const maxUndo = 200
	if len(e.undo) > maxUndo {
		e.undo = e.undo[len(e.undo)-maxUndo:]
	}
	e.redo = nil
	e.emitChange()
}

func (e *Editor) rollback() {
	x := e.tx
	if x == nil {
		return
	}
	e.tx = nil
	e.applyHistory(x, true)
}

func (e *Editor) transact(fn func()) {
	if e.tx != nil {
		fn()
		return
	}
	e.begin()
	fn()
	e.commit()
}

func (e *Editor) applyHistory(x *historyEntry, back bool) {
	for id, c := range x.shapes {
		v := c.after
		if back {
			v = c.before
		}
		if v == nil {
			delete(e.Doc.Shapes, id)
		} else {
			e.Doc.Shapes[id] = v
		}
	}
	for id, c := range x.bindings {
		v := c.after
		if back {
			v = c.before
		}
		if v == nil {
			delete(e.Doc.Bindings, id)
		} else {
			e.Doc.Bindings[id] = v
		}
	}
	for id, c := range x.assets {
		v := c.after
		if back {
			v = c.before
		}
		if v == nil {
			delete(e.Doc.Assets, id)
		} else {
			e.Doc.Assets[id] = v
		}
	}
	if x.pages != nil {
		if back {
			e.Doc.Pages = slices.Clone(*x.pages.before)
		} else {
			e.Doc.Pages = slices.Clone(*x.pages.after)
		}
	}
	if x.name != nil {
		if back {
			e.Doc.Name = *x.name.before
		} else {
			e.Doc.Name = *x.name.after
		}
	}
	e.kids = nil
	e.bindFrom, e.bindTo, e.arrows = nil, nil, nil
	if back {
		e.sel = slices.Clone(x.selBefore)
		e.Page = x.pageBefore
	} else {
		e.sel = slices.Clone(x.selAfter)
		e.Page = x.pageAfter
	}
	if e.Doc.Page(e.Page) == nil && len(e.Doc.Pages) > 0 {
		e.Page = e.Doc.Pages[0].ID
	}
	e.Doc.ActivePage = e.Page
	e.Doc.Normalize(e.Doc.ID, e.Doc.Name)
	e.Doc.ActivePage = e.Page
	e.sel = slices.DeleteFunc(e.sel, func(id doc.ID) bool { return e.Doc.Shapes[id] == nil })
	if e.EditingVertices != "" && e.Doc.Shapes[e.EditingVertices] == nil {
		e.EditingVertices = ""
	}
}

// CanUndo reports whether there is a committed edit to undo.
func (e *Editor) CanUndo() bool { return len(e.undo) > 0 }

// CanRedo reports whether there is an undone edit to redo.
func (e *Editor) CanRedo() bool { return len(e.redo) > 0 }

// Undo reverts the last committed edit transaction.
func (e *Editor) Undo() {
	e.cancelGesture()
	e.StopEditing()
	e.commit()
	if len(e.undo) == 0 {
		return
	}
	x := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.redo = append(e.redo, x)
	e.applyHistory(x, true)
	e.emitChange()
}

// Redo reapplies the most recently undone edit transaction.
func (e *Editor) Redo() {
	e.cancelGesture()
	e.StopEditing()
	e.commit()
	if len(e.redo) == 0 {
		return
	}
	x := e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.undo = append(e.undo, x)
	e.applyHistory(x, false)
	e.emitChange()
}

func (e *Editor) put(s *doc.Shape) {
	e.begin()
	before := e.Doc.Shapes[s.ID]
	if c, ok := e.tx.shapes[s.ID]; ok {
		e.tx.shapes[s.ID] = change[doc.Shape]{before: c.before, after: s}
	} else {
		e.tx.shapes[s.ID] = change[doc.Shape]{before: before, after: s}
	}
	e.Doc.Shapes[s.ID] = s
	if before == nil || before.Parent != s.Parent || before.Index != s.Index {
		e.kids = nil
	}
}

func (e *Editor) update(id doc.ID, fn func(*doc.Shape)) {
	s := e.Doc.Shapes[id]
	if s == nil {
		return
	}
	c := s.Clone()
	fn(c)
	e.put(c)
}

func (e *Editor) removeShape(id doc.ID) {
	e.begin()
	before := e.Doc.Shapes[id]
	if before == nil {
		return
	}
	if c, ok := e.tx.shapes[id]; ok {
		e.tx.shapes[id] = change[doc.Shape]{before: c.before, after: nil}
	} else {
		e.tx.shapes[id] = change[doc.Shape]{before: before, after: nil}
	}
	delete(e.Doc.Shapes, id)
	e.kids = nil
}

func (e *Editor) putAsset(a *doc.Asset) {
	e.begin()
	before := e.Doc.Assets[a.ID]
	if c, ok := e.tx.assets[a.ID]; ok {
		e.tx.assets[a.ID] = change[doc.Asset]{before: c.before, after: a}
	} else {
		e.tx.assets[a.ID] = change[doc.Asset]{before: before, after: a}
	}
	e.Doc.Assets[a.ID] = a
}

func (e *Editor) putBinding(b *doc.Binding) {
	e.begin()
	before := e.Doc.Bindings[b.ID]
	if c, ok := e.tx.bindings[b.ID]; ok {
		e.tx.bindings[b.ID] = change[doc.Binding]{before: c.before, after: b}
	} else {
		e.tx.bindings[b.ID] = change[doc.Binding]{before: before, after: b}
	}
	e.Doc.Bindings[b.ID] = b
	e.bindFrom, e.bindTo = nil, nil
}

func (e *Editor) removeBinding(id doc.ID) {
	e.begin()
	before := e.Doc.Bindings[id]
	if before == nil {
		return
	}
	if c, ok := e.tx.bindings[id]; ok {
		e.tx.bindings[id] = change[doc.Binding]{before: c.before, after: nil}
	} else {
		e.tx.bindings[id] = change[doc.Binding]{before: before, after: nil}
	}
	delete(e.Doc.Bindings, id)
	e.bindFrom, e.bindTo = nil, nil
}

func (e *Editor) deleteTree(id doc.ID) {
	for _, kid := range slices.Clone(e.Children(id)) {
		e.deleteTree(kid)
	}
	// Collect bindings referencing id first to avoid mutating map during iteration.
	var fromIDs []doc.ID
	var toBindings []*doc.Binding
	for bid, b := range e.Doc.Bindings {
		if b.From == id {
			fromIDs = append(fromIDs, bid)
		} else if b.To == id {
			toBindings = append(toBindings, b)
		}
	}
	for _, b := range toBindings {
		e.unbind(b.From, b.Terminal)
	}
	for _, bid := range fromIDs {
		e.removeBinding(bid)
	}
	e.removeShape(id)
}

func (e *Editor) deleteShapes(ids []doc.ID) {
	parents := map[doc.ID]bool{}
	for _, id := range ids {
		if s := e.Doc.Shapes[id]; s != nil && !doc.IsPageID(s.Parent) {
			parents[s.Parent] = true
		}
		e.deleteTree(id)
	}
	// Collapse empty groups.
	for pid := range parents {
		if p := e.Doc.Shapes[pid]; p != nil && p.Type == doc.TypeGroup && len(e.Children(pid)) == 0 {
			e.deleteTree(pid)
		}
	}
	idSet := make(map[doc.ID]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	e.sel = slices.DeleteFunc(e.sel, func(id doc.ID) bool { return idSet[id] || e.Doc.Shapes[id] == nil })
}

// --- hierarchy & matrices ---

func (e *Editor) childrenMap() map[doc.ID][]doc.ID {
	if e.kids != nil {
		return e.kids
	}
	m := make(map[doc.ID][]doc.ID, 8)
	for id, s := range e.Doc.Shapes {
		m[s.Parent] = append(m[s.Parent], id)
	}
	for _, list := range m {
		slices.SortFunc(list, func(a, b doc.ID) int {
			if c := strings.Compare(e.Doc.Shapes[a].Index, e.Doc.Shapes[b].Index); c != 0 {
				return c
			}
			return strings.Compare(string(a), string(b))
		})
	}
	e.kids = m
	return m
}

// Children returns the ordered child shape IDs of parent (bottom to top).
func (e *Editor) Children(parent doc.ID) []doc.ID {
	return e.childrenMap()[parent]
}

// Descendants returns all descendant shape IDs of id depth-first.
func (e *Editor) Descendants(id doc.ID) []doc.ID {
	var out []doc.ID
	var walk func(doc.ID)
	walk = func(cur doc.ID) {
		for _, k := range e.Children(cur) {
			out = append(out, k)
			walk(k)
		}
	}
	walk(id)
	return out
}

// PageOf returns the page ID containing shape id.
func (e *Editor) PageOf(id doc.ID) doc.ID {
	for !doc.IsPageID(id) {
		s := e.Doc.Shapes[id]
		if s == nil {
			return ""
		}
		id = s.Parent
	}
	return id
}

// PageMat returns the affine transform from shape id's local space to page space.
func (e *Editor) PageMat(id doc.ID) geo.Mat {
	s := e.Doc.Shapes[id]
	if s == nil {
		return geo.Identity
	}
	m := geo.Compose(s.X, s.Y, s.Rotation)
	if doc.IsPageID(s.Parent) {
		return m
	}
	return e.PageMat(s.Parent).Mul(m)
}

func (e *Editor) parentMat(parent doc.ID) geo.Mat {
	if doc.IsPageID(parent) {
		return geo.Identity
	}
	return e.PageMat(parent)
}

func (e *Editor) topIndex(parent doc.ID) string {
	kids := e.Children(parent)
	if len(kids) == 0 {
		return doc.FirstIndex
	}
	return doc.IndexBetween(e.Doc.Shapes[kids[len(kids)-1]].Index, "")
}

func (e *Editor) outermostSelectable(id doc.ID) doc.ID {
	best := id
	cur := id
	for {
		s := e.Doc.Shapes[cur]
		if s == nil || doc.IsPageID(s.Parent) || s.Parent == e.FocusedGroup {
			return best
		}
		p := e.Doc.Shapes[s.Parent]
		if p == nil {
			return best
		}
		if p.Type == doc.TypeGroup {
			best = p.ID
		}
		cur = p.ID
	}
}
