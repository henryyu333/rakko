package editor

import (
	"math"
	"testing"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

func fakeMeasure(text, font string, size, width float64) TextBlock {
	return fallbackMeasure(text, size, width)
}

func newTestEditor() *Editor {
	d := doc.NewDrawing("b1", "Test Board")
	e := New(d, fakeMeasure)
	e.SetViewport(1000, 800)
	return e
}

func TestAll27GeoShapesCreateAndDisplay(t *testing.T) {
	if len(GeoKinds) != 27 {
		t.Fatalf("GeoKinds len = %d, want 27", len(GeoKinds))
	}
	e := newTestEditor()
	e.ToolLocked = true
	pal := e.Palette()
	seen := map[string]bool{}
	for i, kind := range GeoKinds {
		if seen[kind] {
			t.Fatalf("duplicate geo kind %q", kind)
		}
		seen[kind] = true
		e.SetGeoTool(kind)
		x0, y0 := float64((i%6)*150+40), float64((i/6)*120+40)
		e.PointerDown(x0, y0, 0, 1, Mods{}, 1)
		e.PointerMove(x0+100, y0+80, Mods{}, 1.1)
		e.PointerUp(x0+100, y0+80, Mods{}, 1.2)
		sel := e.Selection()
		if len(sel) != 1 {
			t.Fatalf("%s: expected 1 selected shape, got %d", kind, len(sel))
		}
		s := e.Doc.Shapes[sel[0]]
		if s.Geo != kind {
			t.Fatalf("shape Geo = %q, want %q", s.Geo, kind)
		}
		g := e.Geometry(s)
		if g.Bounds.W <= 0 || g.Bounds.H <= 0 || len(g.Outline) < 2 {
			t.Fatalf("%s: invalid geometry %+v", kind, g)
		}
		prims := e.Display(s, pal)
		if len(prims) == 0 {
			t.Fatalf("%s: Display returned 0 prims", kind)
		}
	}

	// Shift square constraint & Alt center constraint.
	e.SetGeoTool("rectangle")
	e.PointerDown(500, 400, 0, 1, Mods{}, 2)
	e.PointerMove(560, 430, Mods{Shift: true, Alt: true}, 2.1)
	e.PointerUp(560, 430, Mods{Shift: true, Alt: true}, 2.2)
	s := e.Doc.Shapes[e.Selection()[0]]
	if math.Abs(s.W-s.H) > 1e-6 || math.Abs(s.W-120) > 1e-6 {
		t.Fatalf("Shift+Alt box = (%v,%v) %vx%v, want 120x120", s.X, s.Y, s.W, s.H)
	}
}

func TestDrawModesHighlightAndUndoRedo(t *testing.T) {
	e := newTestEditor()

	// 1. Freehand draw with Shift straight segment.
	e.SetTool(ToolDraw)
	e.SetPenKind("pen")
	e.Next.PenWidth = 4
	e.PointerDownPressure(100, 100, 0, 1, 0.8, true, Mods{}, 1)
	e.PointerMovePressure(130, 110, 0.9, true, Mods{}, 1.1)
	e.PointerMovePressure(200, 110, 0.9, true, Mods{Shift: true}, 1.2)
	e.PointerUp(200, 110, Mods{Shift: true}, 1.3)

	if len(e.Children(e.Page)) != 1 {
		t.Fatalf("expected 1 draw shape, got %d", len(e.Children(e.Page)))
	}
	ds := e.Doc.Shapes[e.Children(e.Page)[0]]
	if !ds.IsPen || ds.PenKind != "pen" || len(ds.Segments) != 2 || ds.Segments[1].Type != "straight" {
		t.Fatalf("unexpected draw shape segments/props: %+v", ds)
	}

	// 2. Near-circle closed stroke sets IsClosed.
	e.PointerDown(300, 300, 0, 1, Mods{}, 2)
	for i := 1; i <= 20; i++ {
		a := 2 * math.Pi * float64(i) / 20
		x := 300 + (1-math.Cos(a))*40
		y := 300 + math.Sin(a)*40
		e.PointerMove(x, y, Mods{}, 2+float64(i)*0.01)
	}
	e.PointerUp(300, 300, Mods{}, 2.3)
	closedID := e.Children(e.Page)[1]
	if !e.Doc.Shapes[closedID].IsClosed {
		t.Fatal("expected near-circle stroke to auto-close (IsClosed=true)")
	}

	// 3. Highlight tool creates highlight shape.
	e.SetTool(ToolHighlight)
	e.PointerDown(100, 200, 0, 1, Mods{}, 3)
	e.PointerMove(220, 200, Mods{}, 3.1)
	e.PointerUp(220, 200, Mods{}, 3.2)
	hlID := e.Children(e.Page)[2]
	if e.Doc.Shapes[hlID].Type != doc.TypeHighlight {
		t.Fatalf("expected TypeHighlight, got %q", e.Doc.Shapes[hlID].Type)
	}
	if len(e.Display(e.Doc.Shapes[hlID], e.Palette())) == 0 {
		t.Fatal("highlight Display returned 0 prims")
	}

	// Undo/Redo restores cleanly.
	if !e.CanUndo() {
		t.Fatal("expected CanUndo")
	}
	e.Undo()
	if len(e.Children(e.Page)) != 2 {
		t.Fatalf("after Undo count = %d, want 2", len(e.Children(e.Page)))
	}
	e.Redo()
	if len(e.Children(e.Page)) != 3 {
		t.Fatalf("after Redo count = %d, want 3", len(e.Children(e.Page)))
	}
}

func TestEraserObjectVsPartial(t *testing.T) {
	e := newTestEditor()

	// Draw a horizontal stroke from (100, 100) to (300, 100).
	e.SetTool(ToolDraw)
	e.PointerDown(100, 100, 0, 1, Mods{}, 1)
	for x := 120.0; x <= 300.0; x += 20 {
		e.PointerMove(x, 100, Mods{}, 1+x/1000)
	}
	e.PointerUp(300, 100, Mods{}, 1.5)

	// Draw a rectangle at (100, 200) 100x80.
	e.SetGeoTool("rectangle")
	e.PointerDown(100, 200, 0, 1, Mods{}, 2)
	e.PointerMove(200, 280, Mods{}, 2.1)
	e.PointerUp(200, 280, Mods{}, 2.2)

	kids := e.Children(e.Page)
	if len(kids) != 2 {
		t.Fatalf("setup shapes = %d, want 2", len(kids))
	}
	strokeID := kids[0]
	rectID := kids[1]

	// 1. Partial eraser across the middle of the stroke AND across the rectangle:
	// stroke splits into 2 pieces, rectangle stays intact!
	e.SetTool(ToolEraser)
	e.SetEraserMode(EraserPartial)
	e.PointerDown(200, 60, 0, 1, Mods{}, 3)
	e.PointerMove(200, 140, Mods{}, 3.1)
	if len(e.PartialPreview[strokeID]) != 2 {
		t.Fatalf("PartialPreview pieces = %d, want 2", len(e.PartialPreview[strokeID]))
	}
	e.PointerMove(150, 240, Mods{}, 3.2)
	e.PointerUp(150, 240, Mods{}, 3.3)

	if e.Doc.Shapes[rectID] == nil {
		t.Fatal("Partial eraser deleted non-ink rectangle")
	}
	if e.Doc.Shapes[strokeID] != nil {
		t.Fatal("Partial eraser kept original unsplit stroke")
	}
	afterPartial := e.Children(e.Page)
	if len(afterPartial) != 3 {
		t.Fatalf("after partial erase total shapes = %d, want 3 (2 split strokes + 1 rect)", len(afterPartial))
	}

	// Single Undo restores original unsplit stroke.
	e.Undo()
	if e.Doc.Shapes[strokeID] == nil || len(e.Children(e.Page)) != 2 {
		t.Fatalf("Undo after partial erase did not restore single stroke")
	}

	// 2. Object eraser sweeping across the rectangle's border deletes the whole rectangle in 1 step.
	e.SetEraserMode(EraserObject)
	e.PointerDown(95, 240, 0, 1, Mods{}, 4)
	e.PointerMove(115, 240, Mods{}, 4.1)
	if !e.Erasing[rectID] {
		t.Fatal("Object eraser did not mark rectangle in Erasing preview")
	}
	e.PointerUp(115, 240, Mods{}, 4.2)
	if e.Doc.Shapes[rectID] != nil {
		t.Fatal("Object eraser did not delete rectangle")
	}
	e.Undo()
	if e.Doc.Shapes[rectID] == nil {
		t.Fatal("Undo after object erase did not restore rectangle")
	}
}

func TestFrameAdoptReleaseAndClipping(t *testing.T) {
	e := newTestEditor()

	// Create two rectangles: one inside future frame, one outside.
	e.SetGeoTool("rectangle")
	e.PointerDown(150, 150, 0, 1, Mods{}, 1)
	e.PointerMove(210, 200, Mods{}, 1.1)
	e.PointerUp(210, 200, Mods{}, 1.2)
	insideID := e.Selection()[0]

	e.SetGeoTool("rectangle")
	e.PointerDown(600, 150, 0, 1, Mods{}, 2)
	e.PointerMove(660, 200, Mods{}, 2.1)
	e.PointerUp(660, 200, Mods{}, 2.2)
	outsideID := e.Selection()[0]

	// Create Frame covering (100,100)..(420,280): adopts insideID, leaves outsideID.
	e.SetTool(ToolFrame)
	e.PointerDown(100, 100, 0, 1, Mods{}, 3)
	e.PointerMove(420, 280, Mods{}, 3.1)
	e.PointerUp(420, 280, Mods{}, 3.2)
	frameID := e.Selection()[0]

	if e.Doc.Shapes[insideID].Parent != frameID {
		t.Fatalf("inside shape parent = %s, want frame %s", e.Doc.Shapes[insideID].Parent, frameID)
	}
	if e.Doc.Shapes[outsideID].Parent != e.Page {
		t.Fatalf("outside shape parent = %s, want page %s", e.Doc.Shapes[outsideID].Parent, e.Page)
	}

	// Dragging outsideID into Frame reparents it into Frame.
	e.SetTool(ToolSelect)
	e.PointerDown(630, 175, 0, 1, Mods{}, 4)
	e.PointerMove(320, 175, Mods{}, 4.1)
	if e.DropOn != frameID {
		t.Fatalf("DropOn = %s, want frame %s", e.DropOn, frameID)
	}
	e.PointerUp(320, 175, Mods{}, 4.2)
	if e.Doc.Shapes[outsideID].Parent != frameID {
		t.Fatalf("dragged-in shape parent = %s, want frame %s", e.Doc.Shapes[outsideID].Parent, frameID)
	}

	// Dragging insideID outside Frame releases it back to the Page.
	e.PointerDown(180, 175, 0, 1, Mods{}, 5)
	e.PointerMove(700, 175, Mods{}, 5.1)
	e.PointerUp(700, 175, Mods{}, 5.2)
	if e.Doc.Shapes[insideID].Parent != e.Page {
		t.Fatalf("dragged-out shape parent = %s, want page %s", e.Doc.Shapes[insideID].Parent, e.Page)
	}
}

func TestTextNoteImageAndHandTools(t *testing.T) {
	e := newTestEditor()

	// Text tool.
	e.SetTool(ToolText)
	e.PointerDown(120, 120, 0, 1, Mods{}, 1)
	e.PointerUp(120, 120, Mods{}, 1.1)
	if len(e.Selection()) != 1 || e.Doc.Shapes[e.Selection()[0]].Type != doc.TypeText {
		t.Fatalf("Text tool failed: sel=%v", e.Selection())
	}
	if e.Editing != e.Selection()[0] {
		t.Fatalf("Text tool did not enter inline editing: Editing=%s", e.Editing)
	}
	e.EditText = "Hello"
	e.EditChanged()
	e.StopEditing()

	// Note tool.
	e.SetTool(ToolNote)
	e.PointerDown(300, 300, 0, 1, Mods{}, 2)
	e.PointerUp(300, 300, Mods{}, 2.1)
	if len(e.Selection()) != 1 || e.Doc.Shapes[e.Selection()[0]].Type != doc.TypeNote {
		t.Fatalf("Note tool failed: sel=%v", e.Selection())
	}
	e.EditText = "Sticky"
	e.EditChanged()
	e.StopEditing()

	// InsertImage scales wide images to <= 480px.
	imgID := e.InsertImage(&doc.Asset{
		ID: "asset:img1", Type: "image", W: 960, H: 480, Path: "assets/img1.png",
	}, geo.V(500, 400))
	imgShape := e.Doc.Shapes[imgID]
	if imgShape == nil || imgShape.W != 480 || imgShape.H != 240 {
		t.Fatalf("InsertImage shape = %+v", imgShape)
	}

	// Hand tool pans camera.
	e.SetTool(ToolHand)
	e.PointerDown(200, 200, 0, 1, Mods{}, 3)
	e.PointerMove(260, 230, Mods{}, 3.1)
	e.PointerUp(260, 230, Mods{}, 3.2)
	if e.Cam().X != 60 || e.Cam().Y != 30 {
		t.Fatalf("ToolHand pan cam = %+v, want (60,30,1)", e.Cam())
	}

	// Marquee selects shapes and DeleteSelected removes them (undoable).
	e.SetTool(ToolSelect)
	e.PointerDown(0, 0, 0, 1, Mods{}, 4)
	e.PointerMove(900, 700, Mods{}, 4.1)
	e.PointerUp(900, 700, Mods{}, 4.2)
	if len(e.Selection()) != 3 {
		t.Fatalf("marquee selected %d shapes, want 3", len(e.Selection()))
	}
	e.DeleteSelected()
	if e.ItemCount() != 0 {
		t.Fatalf("DeleteSelected left %d items", e.ItemCount())
	}
	e.Undo()
	if e.ItemCount() != 3 {
		t.Fatalf("Undo DeleteSelected left %d items, want 3", e.ItemCount())
	}
}
