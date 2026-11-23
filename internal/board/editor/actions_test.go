package editor

import (
	"math"
	"slices"
	"testing"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

func addTestRect(e *Editor, x, y, w, h float64, t float64) doc.ID {
	e.SetGeoTool("rectangle")
	e.PointerDown(x, y, 0, 1, Mods{}, t)
	e.PointerMove(x+w, y+h, Mods{}, t+0.02)
	e.PointerUp(x+w, y+h, Mods{}, t+0.04)
	return e.Selection()[0]
}

func TestClipboardDuplicateAndStyleClipboard(t *testing.T) {
	e := newTestEditor()
	a := addTestRect(e, 40, 40, 80, 60, 0.1)
	b := addTestRect(e, 180, 40, 100, 60, 0.2)

	e.Select(a)
	e.SetPropStroke("#F87171")
	e.SetPropFillColor("#38BDF8")
	e.SetPropFill("semi")
	e.SetPropDash("dash")
	if !e.CopyStyle() || !e.HasStyleClip() {
		t.Fatal("CopyStyle failed")
	}

	e.Select(b)
	if !e.PasteStyle() {
		t.Fatal("PasteStyle failed")
	}
	sb := e.Doc.Shapes[b]
	if sb.Color != "#F87171" || sb.FillColor != "#38BDF8" || sb.Fill != "semi" || sb.Dash != "dash" {
		t.Fatalf("PasteStyle did not copy properties: %+v", sb)
	}

	// Copy + Paste + Duplicate + Cut + Undo.
	e.Select(a, b)
	if !e.Copy() || !e.HasClipboard() {
		t.Fatal("Copy failed")
	}
	if !e.Paste() || len(e.Children(e.Page)) != 4 {
		t.Fatalf("Paste expected 4 items, got %d", len(e.Children(e.Page)))
	}
	if !e.Duplicate() || len(e.Children(e.Page)) != 6 {
		t.Fatalf("Duplicate expected 6 items, got %d", len(e.Children(e.Page)))
	}
	if !e.Cut() || len(e.Children(e.Page)) != 4 {
		t.Fatalf("Cut expected 4 remaining items, got %d", len(e.Children(e.Page)))
	}
	e.Undo()
	if len(e.Children(e.Page)) != 6 {
		t.Fatalf("Undo after Cut expected 6 items, got %d", len(e.Children(e.Page)))
	}
}

func TestArrangeOrderTransformGroupAndLock(t *testing.T) {
	e := newTestEditor()
	id1 := addTestRect(e, 20, 40, 60, 40, 0.1)
	id2 := addTestRect(e, 140, 90, 100, 80, 0.2)
	id3 := addTestRect(e, 340, 60, 80, 50, 0.3)

	// Z-Order: BringToFront / SendBackward / BringForward / SendToBack.
	e.Select(id1)
	if !e.BringToFront() {
		t.Fatal("BringToFront failed")
	}
	kids := e.Children(e.Page)
	if kids[len(kids)-1] != id1 {
		t.Fatalf("BringToFront order = %v, want %s last", kids, id1)
	}
	if !e.SendBackward() {
		t.Fatal("SendBackward failed")
	}
	kids = e.Children(e.Page)
	if kids[len(kids)-2] != id1 {
		t.Fatalf("SendBackward order = %v, want %s second-to-last", kids, id1)
	}
	if !e.BringForward() {
		t.Fatal("BringForward failed")
	}
	if !e.SendToBack() || e.Children(e.Page)[0] != id1 {
		t.Fatalf("SendToBack failed: %v", e.Children(e.Page))
	}

	// Align (6 directions) & Distribute (2 axes).
	e.Select(id1, id2, id3)
	if !e.AlignSelected("top") {
		t.Fatal("AlignSelected top failed")
	}
	for _, id := range []doc.ID{id1, id2, id3} {
		if math.Abs(e.PageBounds(id).Y-40) > 1e-3 {
			t.Fatalf("Align top mismatch on %s: y=%v", id, e.PageBounds(id).Y)
		}
	}
	if !e.AlignSelected("left") {
		t.Fatal("AlignSelected left failed")
	}
	for _, id := range []doc.ID{id1, id2, id3} {
		if math.Abs(e.PageBounds(id).X-20) > 1e-3 {
			t.Fatalf("Align left mismatch on %s: x=%v", id, e.PageBounds(id).X)
		}
	}

	// Spread horizontally and distribute evenly.
	e.Select(id1)
	e.Nudge(0, 0) // baseline
	e.update(id2, func(c *doc.Shape) { c.X = 110 })
	e.update(id3, func(c *doc.Shape) { c.X = 340 })
	e.commit()
	e.Select(id1, id2, id3)
	if !e.DistributeSelected("x") {
		t.Fatal("DistributeSelected x failed")
	}
	b1, b2, b3 := e.PageBounds(id1), e.PageBounds(id2), e.PageBounds(id3)
	gap1 := b2.X - b1.MaxX()
	gap2 := b3.X - b2.MaxX()
	if math.Abs(gap1-gap2) > 0.5 {
		t.Fatalf("Distribute x gaps unequal: gap1=%v gap2=%v", gap1, gap2)
	}

	// Stack, Pack, Stretch W/H.
	if !e.StackSelected("y") {
		t.Fatal("StackSelected y failed")
	}
	if !e.PackSelected() {
		t.Fatal("PackSelected failed")
	}
	if !e.StretchSelected("w") {
		t.Fatal("StretchSelected w failed")
	}
	for _, id := range []doc.ID{id1, id2, id3} {
		if e.Doc.Shapes[id].W != 100 {
			t.Fatalf("Stretch w mismatch on %s: W=%v", id, e.Doc.Shapes[id].W)
		}
	}
	if !e.StretchSelected("h") {
		t.Fatal("StretchSelected h failed")
	}
	for _, id := range []doc.ID{id1, id2, id3} {
		if e.Doc.Shapes[id].H != 80 {
			t.Fatalf("Stretch h mismatch on %s: H=%v", id, e.Doc.Shapes[id].H)
		}
	}

	// Rotate 90 & Flip H/V.
	e.Select(id1)
	if !e.RotateSelected90(1) || math.Abs(e.Doc.Shapes[id1].Rotation-math.Pi/2) > 1e-6 {
		t.Fatalf("Rotate 90 CW failed: rot=%v", e.Doc.Shapes[id1].Rotation)
	}
	if !e.RotateSelected90(-1) || math.Abs(e.Doc.Shapes[id1].Rotation) > 1e-6 {
		t.Fatalf("Rotate 90 CCW failed: rot=%v", e.Doc.Shapes[id1].Rotation)
	}
	if !e.FlipSelected("x") || !e.Doc.Shapes[id1].FlipX {
		t.Fatalf("Flip x failed")
	}
	if !e.FlipSelected("y") || !e.Doc.Shapes[id1].FlipY {
		t.Fatalf("Flip y failed")
	}

	// Group & Ungroup.
	e.Select(id1, id2)
	if !e.GroupSelected() || !e.HasGroupedSelected() {
		t.Fatal("GroupSelected failed")
	}
	gid := e.Selection()[0]
	if len(e.Children(gid)) != 2 {
		t.Fatalf("group child count = %d, want 2", len(e.Children(gid)))
	}
	if !e.UngroupSelected() || e.HasGroupedSelected() {
		t.Fatal("UngroupSelected failed")
	}
	if e.Doc.Shapes[gid] != nil || !slices.Contains(e.Selection(), id1) {
		t.Fatalf("Ungroup did not restore children to selection: %v", e.Selection())
	}

	// Lock & UnlockAll.
	e.Select(id1, id2)
	if !e.ToggleLockSelected() || len(e.Selection()) != 0 || !e.HasLockedOnPage() {
		t.Fatal("ToggleLockSelected failed to lock and clear selection")
	}
	if unlocked := e.UnlockAll(); unlocked != 2 || e.HasLockedOnPage() {
		t.Fatalf("UnlockAll = %d, HasLocked = %v", unlocked, e.HasLockedOnPage())
	}
}

func TestNavigationFitContentVisibleAndMultiPage(t *testing.T) {
	e := newTestEditor()
	e.SetViewport(1000, 640)
	id1 := addTestRect(e, 100, 100, 160, 100, 0.1)
	id2 := addTestRect(e, 1800, 1200, 120, 90, 0.2)

	// Pan far away -> ContentVisible becomes false.
	e.SetCam(Camera{X: 5000, Y: 5000, Z: 1})
	if e.ContentVisible() {
		t.Fatal("expected ContentVisible=false when panned far away")
	}

	// ZoomToFit brings all shapes back into view.
	e.ZoomToFit()
	if !e.ContentVisible() {
		t.Fatal("expected ContentVisible=true after ZoomToFit")
	}

	// ZoomToSelection centers selection closely.
	e.Select(id2)
	e.ZoomToSelection()
	if !e.ViewBox().Overlaps(e.PageBounds(id2)) {
		t.Fatal("expected selected item inside viewport after ZoomToSelection")
	}

	// CenterOn moves camera center onto target point.
	target := geo.V(120, 140)
	e.CenterOn(target)
	if dist := e.ViewBox().Center().Dist(target); dist > 1.0 {
		t.Fatalf("CenterOn center = %v, want %v", e.ViewBox().Center(), target)
	}

	// Multi-page management: Add, Rename, MoveSelectedToPage, Delete + last-page protection.
	page1 := e.Page
	if err := e.DeletePage(page1); err == nil {
		t.Fatal("expected error deleting last remaining page")
	}
	page2 := e.AddPage("")
	if e.Page != page2.ID || len(e.Pages()) != 2 {
		t.Fatalf("AddPage did not switch to new page")
	}
	if !e.RenamePage(page2.ID, "Flowchart") || e.ActivePageInfo().Name != "Flowchart" {
		t.Fatal("RenamePage failed")
	}
	e.SwitchPage(page1)
	e.Select(id1)
	if moved := e.MoveSelectedToPage(page2.ID); moved != 1 {
		t.Fatalf("MoveSelectedToPage = %d, want 1", moved)
	}
	if e.PageItemCount(page2.ID) != 1 || e.PageItemCount(page1) != 1 {
		t.Fatalf("page item counts mismatch: p1=%d p2=%d", e.PageItemCount(page1), e.PageItemCount(page2.ID))
	}
	if err := e.DeletePage(page2.ID); err != nil {
		t.Fatalf("DeletePage page2: %v", err)
	}
	if len(e.Pages()) != 1 || e.Doc.Shapes[id1] != nil {
		t.Fatalf("expected page2 and its moved shape cleaned up cleanly")
	}

	// ToolLock Q keeps creation tool active after finishing gesture.
	if !e.ToggleToolLock() || !e.ToolLocked {
		t.Fatal("ToggleToolLock failed")
	}
	e.SetGeoTool("diamond")
	e.PointerDown(50, 50, 0, 1, Mods{}, 1.0)
	e.PointerMove(130, 120, Mods{}, 1.02)
	e.PointerUp(130, 120, Mods{}, 1.04)
	if e.Tool != ToolGeo {
		t.Fatalf("expected ToolGeo preserved when ToolLocked=true, got %s", e.Tool)
	}
}
