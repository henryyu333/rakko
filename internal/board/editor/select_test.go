package editor

import (
	"math"
	"testing"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

func createRect(t *testing.T, e *Editor, x, y, w, h float64) doc.ID {
	t.Helper()
	e.SetGeoTool("rectangle")
	e.PointerDown(x, y, 0, 1, Mods{}, 1)
	e.PointerMove(x+w, y+h, Mods{}, 1.1)
	e.PointerUp(x+w, y+h, Mods{}, 1.2)
	sel := e.Selection()
	if len(sel) != 1 {
		t.Fatalf("createRect sel = %v", sel)
	}
	return sel[0]
}

func TestResize8HandlesShiftAltAndNegativeFlip(t *testing.T) {
	e := newTestEditor()
	id := createRect(t, e, 100, 100, 120, 80)

	hs := e.Handles()
	var resizeCount, rotateCount int
	for _, h := range hs {
		switch h.Kind {
		case HandleResize:
			resizeCount++
		case HandleRotate:
			rotateCount++
		}
	}
	if resizeCount != 8 || rotateCount != 4 {
		t.Fatalf("Handles = %d resize, %d rotate; want 8 resize, 4 rotate", resizeCount, rotateCount)
	}

	// 1. Shift uniform resize from SE corner (220, 180) -> (340, 200): aspect 1.5 preserved -> 240x160.
	e.PointerDown(220, 180, 0, 1, Mods{}, 2)
	e.PointerMove(340, 200, Mods{Shift: true}, 2.1)
	e.PointerUp(340, 200, Mods{Shift: true}, 2.2)
	s := e.Doc.Shapes[id]
	if math.Abs(s.W-240) > 1e-6 || math.Abs(s.H-160) > 1e-6 || math.Abs(s.X-100) > 1e-6 || math.Abs(s.Y-100) > 1e-6 {
		t.Fatalf("Shift SE resize = (%v,%v) %vx%v, want (100,100) 240x160", s.X, s.Y, s.W, s.H)
	}

	// 2. Alt center-anchored resize on E side handle (340, 180) -> (380, 180): center stays at (220, 180), W grows by 80 -> 320.
	e.PointerDown(340, 180, 0, 1, Mods{}, 3)
	e.PointerMove(380, 180, Mods{Alt: true}, 3.1)
	e.PointerUp(380, 180, Mods{Alt: true}, 3.2)
	s = e.Doc.Shapes[id]
	if math.Abs(s.X-60) > 1e-6 || math.Abs(s.W-320) > 1e-6 || math.Abs(s.H-160) > 1e-6 {
		t.Fatalf("Alt E resize = (%v,%v) %vx%v, want (60,100) 320x160", s.X, s.Y, s.W, s.H)
	}

	// 3. Negative scaling flip: drag SE corner (380, 260) past NW anchor (60, 100) to (0, 20).
	e.PointerDown(380, 260, 0, 1, Mods{}, 4)
	e.PointerMove(0, 20, Mods{}, 4.1)
	e.PointerUp(0, 20, Mods{}, 4.2)
	s = e.Doc.Shapes[id]
	if !s.FlipX || !s.FlipY {
		t.Fatalf("negative resize did not set FlipX=%v FlipY=%v", s.FlipX, s.FlipY)
	}
	if math.Abs(s.X-0) > 1e-6 || math.Abs(s.Y-20) > 1e-6 || math.Abs(s.W-60) > 1e-6 || math.Abs(s.H-80) > 1e-6 {
		t.Fatalf("negative flip box = (%v,%v) %vx%v, want (0,20) 60x80", s.X, s.Y, s.W, s.H)
	}
	// Hit testing on flipped box still works and undo restores pre-flip state.
	e.Undo()
	s = e.Doc.Shapes[id]
	if s.FlipX || s.FlipY || math.Abs(s.W-320) > 1e-6 {
		t.Fatalf("Undo after flip resize got (%v,%v) %vx%v flip=(%v,%v)", s.X, s.Y, s.W, s.H, s.FlipX, s.FlipY)
	}
}

func TestRotateOuterCornerAndShift15Deg(t *testing.T) {
	e := newTestEditor()
	id := createRect(t, e, 200, 200, 100, 100) // center (250, 250), SE corner (300, 300)

	// Hit outer rotate zone just outside SE corner: (312, 312).
	e.PointerMove(312, 312, Mods{}, 1.5)
	h := e.HandleAt(geo.V(312, 312))
	if h.Kind != HandleRotate {
		t.Fatalf("HandleAt(312,312) = %v, want HandleRotate", h.Kind)
	}
	if e.CursorNow() != CursorRotate {
		t.Fatalf("CursorNow at outer corner = %v, want CursorRotate", e.CursorNow())
	}

	// Drag from (312, 312) (45 deg from center) to ~68 deg with Shift -> snaps to 15 deg delta (15° or 30°).
	e.PointerDown(312, 312, 0, 1, Mods{}, 2)
	// Angle to (250 + 80*cos(66°), 250 + 80*sin(66°)) is 21° delta -> Shift snaps to 15° (pi/12).
	ang := math.Pi/4 + 21*math.Pi/180
	e.PointerMove(250+80*math.Cos(ang), 250+80*math.Sin(ang), Mods{Shift: true}, 2.1)
	if deg, ok := e.RotateAngle(); !ok || math.Abs(deg-15) > 0.1 {
		t.Fatalf("RotateAngle = %v ok=%v, want 15", deg, ok)
	}
	e.PointerUp(250+80*math.Cos(ang), 250+80*math.Sin(ang), Mods{Shift: true}, 2.2)

	s := e.Doc.Shapes[id]
	if math.Abs(s.Rotation-math.Pi/12) > 1e-6 {
		t.Fatalf("Rotation = %v, want pi/12 (%v)", s.Rotation, math.Pi/12)
	}
	// Center stays invariant at (250, 250).
	c := e.PageBounds(id).Center()
	if math.Abs(c.X-250) > 1e-6 || math.Abs(c.Y-250) > 1e-6 {
		t.Fatalf("rotated center = %v, want (250,250)", c)
	}
}

func TestLassoAndMarqueeEncloseSelection(t *testing.T) {
	e := newTestEditor()
	r1 := createRect(t, e, 100, 100, 60, 60) // center (130, 130)
	r2 := createRect(t, e, 300, 100, 60, 60) // center (330, 130)
	r3 := createRect(t, e, 500, 100, 60, 60) // center (530, 130)
	e.SelectNone()

	// 1. Marquee with Cmd (enclose-only): overlaps r1 partially, encloses r2 completely.
	e.PointerDown(140, 80, 0, 1, Mods{Cmd: true}, 2)
	e.PointerMove(380, 180, Mods{Cmd: true}, 2.1)
	e.PointerUp(380, 180, Mods{Cmd: true}, 2.2)
	if len(e.Selection()) != 1 || e.Selection()[0] != r2 {
		t.Fatalf("Cmd enclose marquee sel = %v, want [%s]", e.Selection(), r2)
	}

	// 2. Lasso tool around r1 and r3 (using Shift additive after selecting r1 first).
	e.SetTool(ToolLasso)
	e.PointerDown(80, 80, 0, 1, Mods{}, 3)
	e.PointerMove(180, 80, Mods{}, 3.1)
	e.PointerMove(180, 180, Mods{}, 3.2)
	e.PointerMove(80, 180, Mods{}, 3.3)
	e.PointerUp(80, 180, Mods{}, 3.4)
	if e.Tool != ToolSelect {
		t.Fatalf("Tool after lasso up = %s, want select", e.Tool)
	}
	if len(e.Selection()) != 1 || e.Selection()[0] != r1 {
		t.Fatalf("Lasso sel = %v, want [%s]", e.Selection(), r1)
	}

	// Shift+Lasso adds r3 without losing r1.
	e.SetTool(ToolLasso)
	e.PointerDown(480, 80, 0, 1, Mods{Shift: true}, 4)
	e.PointerMove(580, 80, Mods{Shift: true}, 4.1)
	e.PointerMove(580, 180, Mods{Shift: true}, 4.2)
	e.PointerMove(480, 180, Mods{Shift: true}, 4.3)
	e.PointerUp(480, 180, Mods{Shift: true}, 4.4)
	if !e.IsSelected(r1) || !e.IsSelected(r3) || e.IsSelected(r2) {
		t.Fatalf("Shift+Lasso sel = %v, want [%s, %s]", e.Selection(), r1, r3)
	}
}

func TestAltDragRealTimeDuplicate(t *testing.T) {
	e := newTestEditor()
	origID := createRect(t, e, 100, 100, 80, 60)

	// Start dragging, toggle Alt on mid-drag, toggle Alt off, toggle Alt back on, release.
	e.PointerDown(140, 130, 0, 1, Mods{}, 2)
	e.PointerMove(180, 150, Mods{}, 2.1)
	e.ModsChanged(Mods{Alt: true})
	if e.ItemCount() != 2 {
		t.Fatalf("ItemCount with Alt held = %d, want 2", e.ItemCount())
	}
	// Original returned to (100, 100).
	if e.Doc.Shapes[origID].X != 100 || e.Doc.Shapes[origID].Y != 100 {
		t.Fatalf("original moved while Alt cloning: (%v,%v)", e.Doc.Shapes[origID].X, e.Doc.Shapes[origID].Y)
	}

	// Release Alt mid-drag -> clone disappears.
	e.ModsChanged(Mods{})
	if e.ItemCount() != 1 || !e.IsSelected(origID) {
		t.Fatalf("after releasing Alt mid-drag count=%d sel=%v", e.ItemCount(), e.Selection())
	}

	// Hold Alt again and drop at +100, +50.
	e.ModsChanged(Mods{Alt: true})
	e.PointerMove(240, 180, Mods{Alt: true}, 2.2)
	e.PointerUp(240, 180, Mods{Alt: true}, 2.3)

	if e.ItemCount() != 2 {
		t.Fatalf("final ItemCount = %d, want 2", e.ItemCount())
	}
	sel := e.Selection()
	if len(sel) != 1 || sel[0] == origID {
		t.Fatalf("expected clone to be selected, got %v", sel)
	}
	clone := e.Doc.Shapes[sel[0]]
	if math.Abs(clone.X-200) > 1e-6 || math.Abs(clone.Y-150) > 1e-6 {
		t.Fatalf("clone pos = (%v,%v), want (200,150)", clone.X, clone.Y)
	}
	if e.Doc.Shapes[origID].X != 100 || e.Doc.Shapes[origID].Y != 100 {
		t.Fatalf("original moved after commit: (%v,%v)", e.Doc.Shapes[origID].X, e.Doc.Shapes[origID].Y)
	}

	// Single undo deletes the clone.
	e.Undo()
	if e.ItemCount() != 1 || e.Doc.Shapes[origID] == nil {
		t.Fatalf("Undo after Alt-drag did not remove clone cleanly: count=%d", e.ItemCount())
	}
}

func TestSmartGuidesAndGridSnap(t *testing.T) {
	e := newTestEditor()
	_ = createRect(t, e, 100, 100, 100, 80)       // X in [100..200], Y in [100..180]
	mover := createRect(t, e, 300, 300, 100, 80)  // moving shape

	// 1. Drag mover near X=103 (within 8px of target left X=100) with Cmd held (temp smart snap).
	e.Select(mover)
	e.PointerDown(350, 340, 0, 1, Mods{}, 2)
	e.PointerMove(153, 340, Mods{Cmd: true}, 2.1)
	if len(e.Snaps) == 0 {
		t.Fatal("expected smart alignment guide Snaps when Cmd is held")
	}
	if math.Abs(e.Doc.Shapes[mover].X-100) > 1e-6 {
		t.Fatalf("smart snap X = %v, want 100", e.Doc.Shapes[mover].X)
	}
	e.PointerUp(153, 340, Mods{Cmd: true}, 2.2)
	if len(e.Snaps) != 0 {
		t.Fatal("expected Snaps cleared after pointer up")
	}

	// 2. Always Snap (e.Snap = true), holding Cmd temporarily suppresses snapping.
	e.ToggleSnap()
	e.PointerDown(150, 340, 0, 1, Mods{}, 3)
	e.PointerMove(156, 340, Mods{Cmd: true}, 3.1)
	if len(e.Snaps) != 0 || math.Abs(e.Doc.Shapes[mover].X-106) > 1e-6 {
		t.Fatalf("Cmd should suppress Snap when Snap=true: X=%v snaps=%d", e.Doc.Shapes[mover].X, len(e.Snaps))
	}
	e.PointerUp(156, 340, Mods{Cmd: true}, 3.2)
	e.ToggleSnap() // back to false

	// 3. Grid snap (step = 40): moving (106, 300) by (+12, +14) -> (118, 314) snaps to (120, 320).
	e.ToggleGrid()
	e.PointerDown(150, 340, 0, 1, Mods{}, 4)
	e.PointerMove(162, 354, Mods{}, 4.1)
	e.PointerUp(162, 354, Mods{}, 4.2)
	s := e.Doc.Shapes[mover]
	if math.Abs(s.X-120) > 1e-6 || math.Abs(s.Y-320) > 1e-6 {
		t.Fatalf("Grid snap pos = (%v,%v), want (120,320)", s.X, s.Y)
	}
}

func TestInlineEditingGrowYNudgeAndEscapeLayers(t *testing.T) {
	e := newTestEditor()

	// 1. Double-click geo shape edits label & auto-grows GrowY when text wraps.
	rectID := createRect(t, e, 100, 100, 100, 50)
	e.SelectNone()
	e.PointerDown(150, 125, 0, 2, Mods{}, 2)
	e.PointerUp(150, 125, Mods{}, 2.1)
	if e.Editing != rectID {
		t.Fatalf("double-click rect Editing = %s, want %s", e.Editing, rectID)
	}
	e.EditText = "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
	e.EditChanged()
	if e.Doc.Shapes[rectID].GrowY <= 0 {
		t.Fatalf("expected GrowY > 0 for tall label, got %v", e.Doc.Shapes[rectID].GrowY)
	}
	e.StopEditing()
	if e.Editing != "" || e.Doc.Shapes[rectID].Text == "" {
		t.Fatalf("StopEditing state invalid: Editing=%q Text=%q", e.Editing, e.Doc.Shapes[rectID].Text)
	}

	// Clearing text resets GrowY back to 0.
	e.Select(rectID)
	if !e.EnterEdit() {
		t.Fatal("EnterEdit failed on single selected rect")
	}
	e.EditText = ""
	e.EditChanged()
	e.StopEditing()
	if e.Doc.Shapes[rectID].GrowY != 0 {
		t.Fatalf("expected GrowY=0 after clearing text, got %v", e.Doc.Shapes[rectID].GrowY)
	}

	// 2. Sticky note auto-grows GrowY.
	noteID := e.CreateNote(geo.V(400, 200), "")
	e.StartEditing(noteID)
	e.EditText = "1\n2\n3\n4\n5\n6\n7\n8\n9\n10"
	e.EditChanged()
	e.StopEditing()
	if e.Doc.Shapes[noteID].GrowY <= 0 {
		t.Fatalf("expected note GrowY > 0, got %v", e.Doc.Shapes[noteID].GrowY)
	}

	// 3. Double-click Frame title edits Frame.Name; double-click Frame interior creates Text inside Frame.
	e.SetTool(ToolFrame)
	e.PointerDown(500, 100, 0, 1, Mods{}, 3)
	e.PointerMove(820, 280, Mods{}, 3.1)
	e.PointerUp(820, 280, Mods{}, 3.2)
	frameID := e.Selection()[0]

	e.PointerDown(520, 90, 0, 2, Mods{}, 4) // title tab above top-left
	e.PointerUp(520, 90, Mods{}, 4.1)
	if e.Editing != frameID {
		t.Fatalf("double-click frame title Editing=%s, want %s", e.Editing, frameID)
	}
	e.EditText = "Design System"
	e.EditChanged()
	e.StopEditing()
	if e.Doc.Shapes[frameID].Name != "Design System" {
		t.Fatalf("Frame.Name = %q, want Design System", e.Doc.Shapes[frameID].Name)
	}

	// Double-click inside hollow frame body creates TypeText child; empty cancels on Esc.
	e.PointerDown(650, 180, 0, 2, Mods{}, 5)
	e.PointerUp(650, 180, Mods{}, 5.1)
	if e.Editing == "" || e.Editing == frameID {
		t.Fatalf("double-click frame interior did not create child text: Editing=%s", e.Editing)
	}
	childTextID := e.Editing
	if e.Doc.Shapes[childTextID].Parent != frameID {
		t.Fatalf("child text parent = %s, want %s", e.Doc.Shapes[childTextID].Parent, frameID)
	}
	// Esc layer 1: exits editing (and deletes empty TypeText).
	e.Cancel()
	if e.Editing != "" || e.Doc.Shapes[childTextID] != nil {
		t.Fatalf("Esc should exit editing and delete empty text: Editing=%q exists=%v", e.Editing, e.Doc.Shapes[childTextID] != nil)
	}

	// 4. Double-click Arrow edits arrow label.
	arrowID := doc.NewRecordID("shape")
	ar := doc.NewShape(arrowID, doc.TypeArrow)
	ar.Parent, ar.Index = e.Page, e.topIndex(e.Page)
	ar.X, ar.Y = 100, 400
	ar.Start, ar.End = geo.V(0, 0), geo.V(160, 0)
	e.transact(func() { e.put(ar) })
	e.PointerDown(180, 400, 0, 2, Mods{}, 6)
	e.PointerUp(180, 400, Mods{}, 6.1)
	if e.Editing != arrowID {
		t.Fatalf("double-click arrow Editing=%s, want %s", e.Editing, arrowID)
	}
	e.EditText = "Flow"
	e.EditChanged()
	e.StopEditing()
	if e.Doc.Shapes[arrowID].Text != "Flow" {
		t.Fatalf("Arrow text = %q, want Flow", e.Doc.Shapes[arrowID].Text)
	}

	// 5. Arrow key nudge (1px / 10px) and Esc layered exit (selection -> tool).
	e.Select(rectID)
	x0, y0 := e.Doc.Shapes[rectID].X, e.Doc.Shapes[rectID].Y
	e.Nudge(10, -1)
	if e.Doc.Shapes[rectID].X != x0+10 || e.Doc.Shapes[rectID].Y != y0-1 {
		t.Fatalf("Nudge pos = (%v,%v), want (%v,%v)", e.Doc.Shapes[rectID].X, e.Doc.Shapes[rectID].Y, x0+10, y0-1)
	}
	e.Undo()
	if e.Doc.Shapes[rectID].X != x0 || e.Doc.Shapes[rectID].Y != y0 {
		t.Fatalf("Undo Nudge failed: (%v,%v)", e.Doc.Shapes[rectID].X, e.Doc.Shapes[rectID].Y)
	}

	e.SetTool(ToolDraw)
	e.Select(rectID)
	e.Cancel() // clears selection
	if len(e.Selection()) != 0 || e.Tool != ToolDraw {
		t.Fatalf("first Cancel should clear selection only: sel=%v tool=%s", e.Selection(), e.Tool)
	}
	e.Cancel() // resets tool to select
	if e.Tool != ToolSelect {
		t.Fatalf("second Cancel should reset tool to select: tool=%s", e.Tool)
	}
}

func TestDrawFlipTextScaleAndMultiRotate(t *testing.T) {
	e := newTestEditor()

	// 1. Freehand draw stroke scaled negatively flips its page bounds and points.
	e.SetTool(ToolDraw)
	e.PointerDown(100, 100, 0, 1, Mods{}, 1)
	e.PointerMove(160, 120, Mods{}, 1.1)
	e.PointerMove(200, 160, Mods{}, 1.2)
	e.PointerUp(200, 160, Mods{}, 1.3)
	drawID := e.Children(e.Page)[0]
	e.SetTool(ToolSelect)
	e.Select(drawID)
	b0 := e.PageBounds(drawID)

	// Drag SE corner (200, 160) past NW (100, 100) to (50, 70).
	e.PointerDown(b0.MaxX(), b0.MaxY(), 0, 1, Mods{}, 2)
	e.PointerMove(50, 70, Mods{}, 2.1)
	e.PointerUp(50, 70, Mods{}, 2.2)
	ds := e.Doc.Shapes[drawID]
	if ds.ScaleX >= 0 || ds.ScaleY >= 0 {
		t.Fatalf("expected negative ScaleX/ScaleY on draw flip, got (%v,%v)", ds.ScaleX, ds.ScaleY)
	}
	b1 := e.PageBounds(drawID)
	if math.Abs(b1.MaxX()-b0.X) > 2 || math.Abs(b1.MaxY()-b0.Y) > 2 {
		t.Fatalf("flipped draw bounds = %+v, anchor was (%v,%v)", b1, b0.X, b0.Y)
	}

	// 2. Text shape: corner resize scales Scale; side resize disables AutoSize and wraps width.
	txtID := e.CreateText(geo.V(300, 100), "Hello World")
	e.Select(txtID)
	tb0 := e.PageBounds(txtID)
	e.PointerDown(tb0.MaxX(), tb0.MaxY(), 0, 1, Mods{}, 3)
	e.PointerMove(tb0.X+tb0.W*2, tb0.Y+tb0.H*2, Mods{}, 3.1)
	e.PointerUp(tb0.X+tb0.W*2, tb0.Y+tb0.H*2, Mods{}, 3.2)
	ts := e.Doc.Shapes[txtID]
	if math.Abs(ts.Scale-2.0) > 0.1 || !ts.AutoSize {
		t.Fatalf("corner resize text Scale=%v AutoSize=%v, want ~2, true", ts.Scale, ts.AutoSize)
	}
	tb1 := e.PageBounds(txtID)
	e.PointerDown(tb1.MaxX(), tb1.Center().Y, 0, 1, Mods{}, 4)
	e.PointerMove(tb1.X+40, tb1.Center().Y, Mods{}, 4.1)
	e.PointerUp(tb1.X+40, tb1.Center().Y, Mods{}, 4.2)
	ts = e.Doc.Shapes[txtID]
	if ts.AutoSize || ts.W >= tb1.W {
		t.Fatalf("side resize text AutoSize=%v W=%v", ts.AutoSize, ts.W)
	}

	// 3. Multi-selection rotation orbits shapes around shared center.
	r1 := createRect(t, e, 400, 400, 40, 40) // center (420, 420)
	r2 := createRect(t, e, 500, 400, 40, 40) // center (520, 420); shared center (470, 420)
	e.Select(r1, r2)
	box, _, ok := e.SelectionFrame()
	if !ok {
		t.Fatal("SelectionFrame failed for multi-selection")
	}
	// Rotate from NE corner (540, 400) -> SE corner region using Shift 90° step.
	e.PointerDown(box.MaxX()+10, box.Y-10, 0, 1, Mods{}, 5)
	// Move directly below shared center (470, 500) -> angle from center (470,420) is +90°, start was ~-25° -> snaps to 90°/105°/120°.
	e.PointerMove(470, 500, Mods{Shift: true}, 5.1)
	e.PointerUp(470, 500, Mods{Shift: true}, 5.2)
	if math.Abs(e.Doc.Shapes[r1].Rotation) < 0.1 || math.Abs(e.Doc.Shapes[r1].Rotation-e.Doc.Shapes[r2].Rotation) > 1e-6 {
		t.Fatalf("multi-rotate rotations r1=%v r2=%v", e.Doc.Shapes[r1].Rotation, e.Doc.Shapes[r2].Rotation)
	}
}
