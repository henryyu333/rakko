package editor

import (
	"encoding/json"
	"math"
	"sync"
	"testing"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

// addTestShape inserts a bare record under parent without gestures.
func addTestShape(e *Editor, typ string, parent doc.ID, x, y, w, h float64) doc.ID {
	id := doc.NewRecordID("shape")
	s := doc.NewShape(id, typ)
	s.Parent, s.X, s.Y, s.W, s.H = parent, x, y, w, h
	s.Index = e.topIndex(parent)
	e.transact(func() { e.put(s) })
	return id
}

func TestEditChangedNotifiesDuringOpenTransaction(t *testing.T) {
	e := newTestEditor()
	changes := 0
	e.OnChange = func(*doc.Drawing) { changes++ }
	id := e.CreateNote(geo.V(100, 100), "")
	before := changes
	e.StartEditing(id)
	e.EditText = "重要的会议纪要"
	e.EditChanged()
	if changes == before {
		t.Fatal("EditChanged did not notify OnChange while the edit transaction is open")
	}
	if got := e.Doc.Shapes[id].Text; got != "重要的会议纪要" {
		t.Fatalf("document text = %q", got)
	}
	e.StopEditing()
	if !e.CanUndo() {
		t.Fatal("StopEditing did not commit the edit transaction")
	}
}

func TestReplaceCommitsPendingInlineEdit(t *testing.T) {
	e := newTestEditor()
	id := e.CreateNote(geo.V(100, 100), "")
	e.StartEditing(id)
	e.EditText = "别丢这一行"
	e.EditChanged()
	d2 := doc.NewDrawing("b2", "B2")
	// The old document must have been committed before the swap; simulate the
	// save path reading the old Doc through the callback.
	var saved *doc.Drawing
	e.OnChange = func(d *doc.Drawing) { saved = d }
	e.Replace(d2)
	if e.Doc != d2 {
		t.Fatal("Replace did not swap the drawing")
	}
	if e.HasClipboard() {
		t.Fatal("Replace kept a clipboard that may reference the old document")
	}
	_ = saved
}

func TestDeletePageUndoRedoKeepsHistoryConsistent(t *testing.T) {
	e := newTestEditor()
	e.CreateText(geo.V(10, 10), "page1")
	p2 := e.AddPage("")
	note := e.CreateNote(geo.V(100, 100), "第二页内容")

	if err := e.DeletePage(p2.ID); err != nil {
		t.Fatal(err)
	}
	if e.Doc.Page(p2.ID) != nil || e.Doc.Shapes[note] != nil {
		t.Fatal("DeletePage did not remove the page and its shapes")
	}
	if !e.CanUndo() {
		t.Fatal("DeletePage did not create an undo entry")
	}

	e.Undo()
	if e.Doc.Page(p2.ID) == nil {
		t.Fatal("Undo did not restore the deleted page")
	}
	if e.Doc.Shapes[note] == nil || e.Doc.Shapes[note].Parent != p2.ID {
		t.Fatal("Undo did not restore the page's shapes")
	}
	if e.Page != p2.ID {
		t.Fatalf("Undo active page = %q, want deleted page %q", e.Page, p2.ID)
	}

	e.Redo()
	e.Redo() // second redo is a no-op; must not resurrect orphans.
	if e.Doc.Page(p2.ID) != nil || e.Doc.Shapes[note] != nil {
		t.Fatal("Redo did not remove the page again")
	}
	for id, s := range e.Doc.Shapes {
		if !doc.IsPageID(s.Parent) && e.Doc.Shapes[s.Parent] == nil {
			t.Fatalf("orphan shape %s left after redo (parent %q)", id, s.Parent)
		}
		if e.Doc.Page(e.PageOf(id)) == nil {
			t.Fatalf("shape %s has no page after redo", id)
		}
	}
}

func TestPageAddAndRenameAreUndoable(t *testing.T) {
	e := newTestEditor()
	first := e.Page
	p := e.AddPage("")
	if !e.CanUndo() {
		t.Fatal("AddPage did not create an undo entry")
	}
	e.Undo()
	if e.Doc.Page(p.ID) != nil || e.Page != first {
		t.Fatal("Undo did not remove the added page")
	}
	e.Redo()
	if e.Doc.Page(p.ID) == nil {
		t.Fatal("Redo did not restore the added page")
	}
	if !e.RenamePage(p.ID, "想法") {
		t.Fatal("RenamePage failed")
	}
	e.Undo()
	if got := e.Doc.Page(p.ID).Name; got == "想法" {
		t.Fatalf("Undo did not revert page rename, name=%q", got)
	}
	e.Redo()
	if got := e.Doc.Page(p.ID).Name; got != "想法" {
		t.Fatalf("Redo page name = %q, want 想法", got)
	}
}

func TestScribbleHatchDoesNotEraseFrameOrClosedShapes(t *testing.T) {
	e := newTestEditor()
	fr := addTestShape(e, doc.TypeFrame, e.Page, 100, 100, 600, 400)
	rect := addTestShape(e, doc.TypeGeo, fr, 20, 20, 120, 80)

	e.SetTool(ToolDraw)
	// Hatching in the empty area of the frame (the audit probe's 8-pass zig-zag).
	e.PointerDown(400, 350, 0, 1, Mods{}, 1)
	tt := 1.0
	for i := 0; i < 8; i++ {
		tt += 0.03
		if i%2 == 0 {
			e.PointerMove(480, 350+float64(i)*6, Mods{}, tt)
		} else {
			e.PointerMove(400, 350+float64(i)*6, Mods{}, tt)
		}
	}
	e.PointerUp(400, 400, Mods{}, tt+0.03)

	if e.Doc.Shapes[fr] == nil {
		t.Fatal("scribble hatch inside a frame erased the frame")
	}
	if e.Doc.Shapes[rect] == nil {
		t.Fatal("scribble hatch inside a frame erased its child shape")
	}
}

func TestScribbleInsideClosedInkKeepsTheInk(t *testing.T) {
	e := newTestEditor()
	e.SetTool(ToolDraw)
	// Closed freehand loop around (300,300).
	e.PointerDown(500, 300, 0, 1, Mods{}, 1)
	for i := 1; i <= 16; i++ {
		a := 2 * math.Pi * float64(i) / 16
		e.PointerMove(400+100*math.Cos(a), 300+80*math.Sin(a), Mods{}, 1+float64(i)*0.02)
	}
	e.PointerUp(500, 300, Mods{}, 1.4)
	loop := e.Children(e.Page)[0]
	if !e.Doc.Shapes[loop].IsClosed {
		t.Log("loop was not auto-closed; hatching inside still must not delete it")
	}
	// Hatch entirely inside the loop without touching its outline.
	e.PointerDown(380, 290, 0, 1, Mods{}, 2)
	tt := 2.0
	for i := 0; i < 8; i++ {
		tt += 0.03
		if i%2 == 0 {
			e.PointerMove(420, 290+float64(i)*3, Mods{}, tt)
		} else {
			e.PointerMove(380, 290+float64(i)*3, Mods{}, tt)
		}
	}
	e.PointerUp(380, 311, Mods{}, tt+0.03)
	if e.Doc.Shapes[loop] == nil {
		t.Fatal("scribble inside closed freehand ink erased the ink without crossing its outline")
	}
}

func TestPasteBoundArrowBakesVisibleEndpoint(t *testing.T) {
	e := newTestEditor()
	target := addTestShape(e, doc.TypeGeo, e.Page, 300, 300, 120, 80)
	aid := doc.NewRecordID("shape")
	a := doc.NewShape(aid, doc.TypeArrow)
	a.Parent = e.Page
	a.Index = e.topIndex(e.Page)
	a.Start, a.End = geo.V(0, 0), geo.V(320, 320)
	e.transact(func() {
		e.put(a)
		e.bindTerminal(aid, "end", target, geo.V(360, 340), false)
	})
	e.transact(func() { e.update(target, func(c *doc.Shape) { c.X, c.Y = 900, 50 }) })
	before := e.Arrow(e.Doc.Shapes[aid]).End

	e.Select(aid)
	if !e.Copy() || !e.Paste() {
		t.Fatal("copy/paste failed")
	}
	pasted := e.Doc.Shapes[e.Selection()[0]]
	after := e.Arrow(pasted).End
	if math.Abs(after.X-before.X) > 1e-6 || math.Abs(after.Y-before.Y) > 1e-6 {
		t.Fatalf("pasted arrow endpoint = %v, want baked visible endpoint %v", after, before)
	}
}

func TestPasteChildOfFrameOnOtherPageLandsOnActivePage(t *testing.T) {
	e := newTestEditor()
	p1 := e.Page
	fr := addTestShape(e, doc.TypeFrame, p1, 0, 0, 320, 180)
	child := addTestShape(e, doc.TypeGeo, fr, 20, 20, 120, 80)
	e.Select(child)
	if !e.Copy() {
		t.Fatal("Copy failed")
	}
	p2 := e.AddPage("")
	if !e.Paste() {
		t.Fatal("Paste failed")
	}
	pasted := e.Selection()[0]
	if got := e.Doc.Shapes[pasted].Parent; got != p2.ID {
		t.Fatalf("pasted child parent = %q, want active page %q", got, p2.ID)
	}
	if e.PageOf(pasted) != p2.ID {
		t.Fatalf("pasted child lives on %q", e.PageOf(pasted))
	}
	if len(e.Children(p2.ID)) != 1 {
		t.Fatalf("active page children = %d, want 1", len(e.Children(p2.ID)))
	}
}

func TestPasteAcrossBoardsCarriesImageAsset(t *testing.T) {
	e := newTestEditor()
	asset := &doc.Asset{ID: "asset:abc", Type: "image", W: 50, H: 50, Path: "assets/abc.png"}
	id := e.InsertImage(asset, geo.V(10, 10))
	e.Select(id)
	if !e.Copy() {
		t.Fatal("Copy failed")
	}
	d2 := doc.NewDrawing("b2", "B2")
	e.Replace(d2)
	if !e.Paste() {
		t.Fatal("Paste after Replace failed")
	}
	pasted := d2.Shapes[e.Selection()[0]]
	if pasted == nil {
		t.Fatal("pasted shape missing")
	}
	if d2.Assets[pasted.AssetID] == nil {
		t.Fatalf("asset %q was not carried into the target board", pasted.AssetID)
	}
}

func TestMoveSelectedToPageUnbindsArrowAtVisibleEndpoint(t *testing.T) {
	e := newTestEditor()
	target := addTestShape(e, doc.TypeGeo, e.Page, 300, 300, 120, 80)
	aid := doc.NewRecordID("shape")
	a := doc.NewShape(aid, doc.TypeArrow)
	a.Parent = e.Page
	a.Index = e.topIndex(e.Page)
	a.Start, a.End = geo.V(0, 0), geo.V(320, 320)
	e.transact(func() {
		e.put(a)
		e.bindTerminal(aid, "end", target, geo.V(360, 340), false)
	})
	e.transact(func() { e.update(target, func(c *doc.Shape) { c.X, c.Y = 900, 50 }) })
	before := e.Arrow(e.Doc.Shapes[aid]).End

	p2 := e.AddPage("")
	e.SwitchPage(e.Doc.Pages[0].ID)
	e.Select(target)
	if n := e.MoveSelectedToPage(p2.ID); n != 1 {
		t.Fatalf("moved %d shapes, want 1", n)
	}
	if b := e.bindingOf(aid, "end"); b != nil {
		t.Fatal("binding to a shape on another page was kept")
	}
	after := e.Arrow(e.Doc.Shapes[aid]).End
	if math.Abs(after.X-before.X) > 1e-6 || math.Abs(after.Y-before.Y) > 1e-6 {
		t.Fatalf("arrow endpoint after move = %v, want visible endpoint %v", after, before)
	}
}

func TestMoveSelectedToPagePreservesZOrder(t *testing.T) {
	e := newTestEditor()
	p1 := e.Page
	a := addTestShape(e, doc.TypeGeo, p1, 0, 0, 50, 50)
	b := addTestShape(e, doc.TypeGeo, p1, 60, 0, 50, 50)
	c := addTestShape(e, doc.TypeGeo, p1, 120, 0, 50, 50)
	p2 := e.AddPage("")
	e.SwitchPage(p1)
	// Selection order reversed relative to z-order.
	e.Select(c, a)
	if n := e.MoveSelectedToPage(p2.ID); n != 2 {
		t.Fatalf("moved %d shapes, want 2", n)
	}
	kids := e.Children(p2.ID)
	if len(kids) != 2 || kids[0] != a || kids[1] != c {
		t.Fatalf("moved children order = %v, want [%s %s] (a below c)", kids, a, c)
	}
	if e.Doc.Shapes[b].Parent != p1 {
		t.Fatal("unselected shape moved pages")
	}
}

func TestPartialEraseKeepsPressureAndClearsSelection(t *testing.T) {
	e := newTestEditor()
	e.SetTool(ToolDraw)
	e.SetPenKind("pen")
	zs := []float64{0.2, 0.4, 0.6, 0.8, 0.9}
	e.PointerDownPressure(100, 100, 0, 1, zs[0], true, Mods{}, 1)
	for i, z := range zs[1:] {
		e.PointerMovePressure(150+float64(i)*50, 100, z, true, Mods{}, 1.1+float64(i)*0.02)
	}
	e.PointerUp(300, 100, Mods{}, 1.3)
	stroke := e.Children(e.Page)[0]
	e.Select(stroke)

	e.SetTool(ToolEraser)
	e.SetEraserMode(EraserPartial)
	e.PointerDown(200, 60, 0, 1, Mods{}, 2)
	e.PointerMove(200, 140, Mods{}, 2.1)
	e.PointerUp(200, 140, Mods{}, 2.2)

	if e.Doc.Shapes[stroke] != nil {
		t.Fatal("partial eraser kept the original stroke")
	}
	for _, id := range e.Selection() {
		if id == stroke {
			t.Fatal("deleted stroke ID remained selected after split")
		}
	}
	kids := e.Children(e.Page)
	if len(kids) < 2 {
		t.Fatalf("partial erase produced %d pieces, want >= 2", len(kids))
	}
	var sawLow, sawHigh, sawNonHalf bool
	for _, id := range kids {
		s := e.Doc.Shapes[id]
		if s == nil || s.Type != doc.TypeDraw {
			continue
		}
		for _, seg := range s.Segments {
			for _, p := range seg.Points {
				if p.Z < 0.3 {
					sawLow = true
				}
				if p.Z > 0.75 {
					sawHigh = true
				}
				if math.Abs(p.Z-0.5) > 1e-9 {
					sawNonHalf = true
				}
			}
		}
	}
	if !sawNonHalf || !sawLow || !sawHigh {
		t.Fatalf("pressure not preserved after split: low=%v high=%v nonHalf=%v", sawLow, sawHigh, sawNonHalf)
	}
}

func TestScribbleLikeLetterDoesNotEraseFrameContent(t *testing.T) {
	e := newTestEditor()
	fr := addTestShape(e, doc.TypeFrame, e.Page, 100, 100, 600, 400)
	rect := addTestShape(e, doc.TypeGeo, fr, 40, 40, 120, 80)
	e.SetTool(ToolDraw)
	// A hand-written "M"/"W" over the frame body: many direction reversals but
	// no intent to scrub anything out.
	for _, letter := range [][]geo.Vec{
		{{X: 300, Y: 400}, {X: 310, Y: 340}, {X: 320, Y: 400}, {X: 330, Y: 340}, {X: 340, Y: 400}},
		{{X: 360, Y: 340}, {X: 370, Y: 400}, {X: 380, Y: 340}, {X: 390, Y: 400}, {X: 400, Y: 340}},
	} {
		e.PointerDown(letter[0].X, letter[0].Y, 0, 1, Mods{}, 1)
		tt := 1.0
		for _, pt := range letter[1:] {
			tt += 0.03
			e.PointerMove(pt.X, pt.Y, Mods{}, tt)
		}
		e.PointerUp(letter[len(letter)-1].X, letter[len(letter)-1].Y, Mods{}, tt+0.03)
	}
	if e.Doc.Shapes[fr] == nil || e.Doc.Shapes[rect] == nil {
		t.Fatal("writing letters inside a frame erased its content")
	}
}

func TestSetCamAndSnapshotMarshalNoDataRace(t *testing.T) {
	e := newTestEditor()
	addTestShape(e, doc.TypeGeo, e.Page, 10, 10, 120, 80)

	jobs := make(chan *doc.Drawing, 4)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for snap := range jobs {
			if _, err := json.Marshal(snap); err != nil {
				t.Errorf("marshal snapshot: %v", err)
				return
			}
		}
	}()

	for i := range 300 {
		// Main thread takes Snapshot() and hands it to the background encoder,
		// then immediately mutates the viewport via Pan / ZoomAt.
		select {
		case jobs <- e.Doc.Snapshot():
		default:
		}
		e.Pan(float64(i%5)+1, -float64(i%3)-1)
		e.ZoomAt(geo.V(120, 90), 1.002)
	}
	close(jobs)
	wg.Wait()
}

func TestDuplicateAltDragMoveAndNudgeBoundArrow(t *testing.T) {
	setupBound := func() (*Editor, doc.ID, doc.ID, doc.ID, *ArrowInfo) {
		e := newTestEditor()
		r1 := addTestShape(e, doc.TypeGeo, e.Page, 0, 100, 80, 60)
		r2 := addTestShape(e, doc.TypeGeo, e.Page, 200, 100, 80, 60)
		ar := addTestShape(e, doc.TypeArrow, e.Page, 0, 0, 0, 0)
		e.transact(func() {
			e.update(ar, func(c *doc.Shape) {
				c.Start = geo.V(80, 130)
				c.End = geo.V(200, 130)
			})
			e.putBinding(&doc.Binding{
				ID: doc.NewRecordID("binding"), From: ar, To: r1,
				Terminal: "start", Anchor: geo.V(1, 0.5),
			})
			e.putBinding(&doc.Binding{
				ID: doc.NewRecordID("binding"), From: ar, To: r2,
				Terminal: "end", Anchor: geo.V(0, 0.5),
			})
		})
		// Move both bound targets so stored Start/End are stale relative to screen.
		e.Select(r1, r2)
		e.Nudge(200, 50)
		e.SelectNone()
		return e, r1, r2, ar, e.Arrow(e.Doc.Shapes[ar])
	}

	t.Run("duplicate", func(t *testing.T) {
		e, _, _, ar, origInfo := setupBound()
		origPos := e.PageMat(ar).Apply(origInfo.Start)
		origEnd := e.PageMat(ar).Apply(origInfo.End)
		e.Select(ar)
		if !e.Duplicate() {
			t.Fatal("Duplicate failed")
		}
		dupID := e.Selection()[0]
		if dupID == ar {
			t.Fatal("Duplicate kept original ID")
		}
		dupInfo := e.Arrow(e.Doc.Shapes[dupID])
		gotStart := e.PageMat(dupID).Apply(dupInfo.Start)
		gotEnd := e.PageMat(dupID).Apply(dupInfo.End)
		wantStart := origPos.Add(geo.V(24, 24))
		wantEnd := origEnd.Add(geo.V(24, 24))
		if gotStart.Dist(wantStart) > 1e-3 || gotEnd.Dist(wantEnd) > 1e-3 {
			t.Fatalf("duplicated bound arrow = (%v -> %v), want (%v -> %v)", gotStart, gotEnd, wantStart, wantEnd)
		}
	})

	t.Run("alt_drag", func(t *testing.T) {
		e, _, _, ar, origInfo := setupBound()
		origStart := e.PageMat(ar).Apply(origInfo.Start)
		origEnd := e.PageMat(ar).Apply(origInfo.End)
		grab := origStart.Add(origEnd.Sub(origStart).Mul(0.25))
		e.Select(ar)
		e.PointerDown(grab.X, grab.Y, 0, 1, Mods{Alt: true}, 1)
		e.PointerMove(grab.X+40, grab.Y-30, Mods{Alt: true}, 1.1)
		e.PointerUp(grab.X+40, grab.Y-30, Mods{Alt: true}, 1.2)
		cloneID := e.Selection()[0]
		if cloneID == ar {
			t.Fatal("Alt-drag did not create a clone")
		}
		cloneInfo := e.Arrow(e.Doc.Shapes[cloneID])
		gotStart := e.PageMat(cloneID).Apply(cloneInfo.Start)
		gotEnd := e.PageMat(cloneID).Apply(cloneInfo.End)
		if gotStart.Dist(origStart.Add(geo.V(40, -30))) > 1e-3 || gotEnd.Dist(origEnd.Add(geo.V(40, -30))) > 1e-3 {
			t.Fatalf("alt-dragged bound arrow = (%v -> %v), want shifted by (40,-30) from (%v -> %v)", gotStart, gotEnd, origStart, origEnd)
		}
		if e.bindingOf(ar, "start") == nil || e.bindingOf(ar, "end") == nil {
			t.Fatal("alt-drag stripped bindings from the original arrow")
		}
	})

	t.Run("drag_bound_arrow_alone", func(t *testing.T) {
		e, _, _, ar, origInfo := setupBound()
		origStart := e.PageMat(ar).Apply(origInfo.Start)
		origEnd := e.PageMat(ar).Apply(origInfo.End)
		grab := origStart.Add(origEnd.Sub(origStart).Mul(0.25))
		e.Select(ar)
		e.PointerDown(grab.X, grab.Y, 0, 1, Mods{}, 1)
		e.PointerMove(grab.X, grab.Y+150, Mods{}, 1.1)
		midInfo := e.Arrow(e.Doc.Shapes[ar])
		duringStart := e.PageMat(ar).Apply(midInfo.Start)
		if duringStart.Dist(origStart.Add(geo.V(0, 150))) > 1e-3 {
			t.Fatalf("bound arrow did not follow during drag: start=%v, want %v", duringStart, origStart.Add(geo.V(0, 150)))
		}
		e.PointerUp(grab.X, grab.Y+150, Mods{}, 1.2)
		afterInfo := e.Arrow(e.Doc.Shapes[ar])
		afterStart := e.PageMat(ar).Apply(afterInfo.Start)
		afterEnd := e.PageMat(ar).Apply(afterInfo.End)
		if afterStart.Dist(origStart.Add(geo.V(0, 150))) > 1e-3 || afterEnd.Dist(origEnd.Add(geo.V(0, 150))) > 1e-3 {
			t.Fatalf("bound arrow snapped back after drop: (%v -> %v), want (%v -> %v)",
				afterStart, afterEnd, origStart.Add(geo.V(0, 150)), origEnd.Add(geo.V(0, 150)))
		}
		if e.bindingOf(ar, "start") != nil || e.bindingOf(ar, "end") != nil {
			t.Fatal("dragged-away arrow remained bound")
		}
	})

	t.Run("nudge_bound_arrow", func(t *testing.T) {
		e, _, _, ar, origInfo := setupBound()
		origStart := e.PageMat(ar).Apply(origInfo.Start)
		origEnd := e.PageMat(ar).Apply(origInfo.End)
		e.Select(ar)
		e.Nudge(15, -10)
		afterInfo := e.Arrow(e.Doc.Shapes[ar])
		afterStart := e.PageMat(ar).Apply(afterInfo.Start)
		afterEnd := e.PageMat(ar).Apply(afterInfo.End)
		if afterStart.Dist(origStart.Add(geo.V(15, -10))) > 1e-3 || afterEnd.Dist(origEnd.Add(geo.V(15, -10))) > 1e-3 {
			t.Fatalf("nudged bound arrow = (%v -> %v), want shifted by (15,-10)", afterStart, afterEnd)
		}
	})
}

func TestUndoThenRedoEmptyNewTextLeavesNoPhantom(t *testing.T) {
	e := newTestEditor()
	r := addTestShape(e, doc.TypeGeo, e.Page, 20, 20, 80, 60)
	e.SetTool(ToolText)
	e.PointerDown(200, 200, 0, 1, Mods{}, 1)
	e.PointerUp(200, 200, Mods{}, 1.05)
	if e.Editing == "" {
		t.Fatal("clicking with ToolText did not enter inline editing")
	}

	e.Undo()
	e.Redo()

	for _, s := range e.Doc.Shapes {
		if s.Type == doc.TypeText {
			t.Fatalf("found phantom empty text shape %s after Undo/Redo", s.ID)
		}
	}
	if e.Doc.Shapes[r] == nil {
		t.Fatal("existing shape missing after Undo/Redo")
	}
}

func TestFlipSelectedGroupMirrorsChildren(t *testing.T) {
	e := newTestEditor()
	r1 := addTestShape(e, doc.TypeGeo, e.Page, 0, 0, 40, 20)
	r2 := addTestShape(e, doc.TypeGeo, e.Page, 100, 0, 60, 20)
	e.Select(r1, r2)
	if !e.GroupSelected() {
		t.Fatal("GroupSelected failed")
	}
	if !e.FlipSelected("x") {
		t.Fatal("FlipSelected failed")
	}
	// Group spanned [0..160] (center X=80). r1 center 20 -> 140, r2 center 130 -> 30.
	c1 := e.PageBounds(r1).Center()
	c2 := e.PageBounds(r2).Center()
	if math.Abs(c1.X-140) > 1e-3 || math.Abs(c2.X-30) > 1e-3 {
		t.Fatalf("flipped group child centers X = (%.1f, %.1f), want (140, 30)", c1.X, c2.X)
	}
	if !e.Doc.Shapes[r1].FlipX || !e.Doc.Shapes[r2].FlipX {
		t.Fatalf("child FlipX flags = (%v, %v), want both true", e.Doc.Shapes[r1].FlipX, e.Doc.Shapes[r2].FlipX)
	}
}
