package editor

import (
	"encoding/xml"
	"math"
	"strings"
	"testing"

	"rakko/internal/board/doc"
)

func TestSolidSameColorLabelContrastAndLabelColor(t *testing.T) {
	e := newTestEditor()

	// 1. Solid rectangle with default FillColor (same as stroke Color).
	e.SetGeoTool("rectangle")
	e.Next.Stroke = "#38BDF8"
	e.Next.Fill = "solid"
	e.Next.FillColor = ""
	e.PointerDown(40, 40, 0, 1, Mods{}, 0.1)
	e.PointerMove(220, 140, Mods{}, 0.2)
	e.PointerUp(220, 140, Mods{}, 0.3)
	rectID := e.Selection()[0]
	e.StartEditing(rectID)
	e.EditText = "Solid Rect"
	e.EditChanged()
	e.StopEditing()

	// 2. Solid ellipse with explicit FillColor equal to Color.
	e.SetGeoTool("ellipse")
	e.Next.Stroke = "#E8E8EA"
	e.Next.Fill = "solid"
	e.Next.FillColor = "#E8E8EA"
	e.PointerDown(260, 40, 0, 1, Mods{}, 0.4)
	e.PointerMove(440, 140, Mods{}, 0.5)
	e.PointerUp(440, 140, Mods{}, 0.6)
	ellID := e.Selection()[0]
	e.StartEditing(ellID)
	e.EditText = "Solid Ellipse"
	e.EditChanged()
	e.StopEditing()

	// 3. Arrow with label (sits on pal.Background pill) and explicit LabelColor override.
	e.SetTool(ToolArrow)
	e.Next.Stroke = "#38BDF8"
	e.PointerDown(60, 220, 0, 1, Mods{}, 0.7)
	e.PointerMove(320, 220, Mods{}, 0.8)
	e.PointerUp(320, 220, Mods{}, 0.9)
	arrowID := e.Selection()[0]
	e.StartEditing(arrowID)
	e.EditText = "Arrow Label"
	e.EditChanged()
	e.StopEditing()

	// 4. Open brace shape: Fill="solid" does not fill an open shape, so text stays strokeCol.
	e.SetGeoTool("brace")
	e.Next.Stroke = "#38BDF8"
	e.Next.Fill = "solid"
	e.Next.FillColor = ""
	e.PointerDown(480, 40, 0, 1, Mods{}, 1.0)
	e.PointerMove(540, 140, Mods{}, 1.1)
	e.PointerUp(540, 140, Mods{}, 1.2)
	braceID := e.Selection()[0]
	e.StartEditing(braceID)
	e.EditText = "Open Brace"
	e.EditChanged()
	e.StopEditing()

	findLabelColor := func(id doc.ID, pal *Palette) Color {
		t.Helper()
		for _, pr := range e.Display(e.Doc.Shapes[id], pal) {
			if pr.Kind == PrimText {
				return pr.Color
			}
		}
		t.Fatalf("shape %s missing PrimText", id)
		return Color{}
	}

	for _, pal := range []*Palette{DarkPalette, LightPalette} {
		e.SetDarkMode(pal.Dark)

		// Solid same-color rect & ellipse must invert label color to pal.Background.
		for _, id := range []doc.ID{rectID, ellID} {
			got := findLabelColor(id, pal)
			if got != pal.Background {
				t.Fatalf("dark=%v shape %s label color = %+v, want pal.Background %+v", pal.Dark, id, got, pal.Background)
			}
			e.StartEditing(id)
			_, _, _, _, _, _, _, editCol, ok := e.EditBox()
			e.StopEditing()
			if !ok || editCol != pal.Background {
				t.Fatalf("dark=%v shape %s EditBox col = %+v (ok=%v), want pal.Background %+v", pal.Dark, id, editCol, ok, pal.Background)
			}
		}

		// Open brace keeps stroke color.
		wantBrace := ResolveColor("#38BDF8", pal.Dark, e.Look())
		if got := findLabelColor(braceID, pal); got != wantBrace {
			t.Fatalf("dark=%v open brace label color = %+v, want %+v", pal.Dark, got, wantBrace)
		}

		// SVG export uses the same inverted label color for solid rect.
		svg, err := e.ExportSVG([]doc.ID{rectID}, pal, nil)
		if err != nil {
			t.Fatalf("ExportSVG: %v", err)
		}
		if err := xml.Unmarshal([]byte(svg), new(any)); err != nil {
			t.Fatalf("invalid SVG: %v", err)
		}
		wantHex := strings.ToUpper(pal.Background.CSS())
		if !strings.Contains(svg, `fill="`+wantHex+`"`) {
			t.Fatalf("dark=%v SVG missing inverted label fill %s:\n%s", pal.Dark, wantHex, svg)
		}
	}

	// Explicit LabelColor overrides default contrast color on both geo and arrow.
	e.SetDarkMode(true)
	const customHex = "#F472B6"
	e.transact(func() {
		e.update(rectID, func(c *doc.Shape) { c.LabelColor = customHex })
		e.update(arrowID, func(c *doc.Shape) { c.LabelColor = customHex })
	})
	wantCustom := Hex(customHex)
	for _, id := range []doc.ID{rectID, arrowID} {
		if got := findLabelColor(id, DarkPalette); got != wantCustom {
			t.Fatalf("shape %s with LabelColor=%s got %+v, want %+v", id, customHex, got, wantCustom)
		}
		e.StartEditing(id)
		_, _, _, _, _, _, _, editCol, ok := e.EditBox()
		e.StopEditing()
		if !ok || editCol != wantCustom {
			t.Fatalf("shape %s EditBox with LabelColor=%s got %+v (ok=%v), want %+v", id, customHex, editCol, ok, wantCustom)
		}
	}
	svg, err := e.ExportSVG([]doc.ID{rectID, arrowID}, DarkPalette, nil)
	if err != nil {
		t.Fatalf("ExportSVG custom LabelColor: %v", err)
	}
	if strings.Count(svg, `fill="`+customHex+`"`) < 2 {
		t.Fatalf("SVG missing custom LabelColor %s on rect and arrow:\n%s", customHex, svg)
	}
}

func addTestFreehandLine(e *Editor, x0, y0, x1, y1 float64, tStart float64) doc.ID {
	e.SetTool(ToolDraw)
	e.PointerDown(x0, y0, 0, 1, Mods{}, tStart)
	for i := 1; i <= 5; i++ {
		f := float64(i) / 5.0
		e.PointerMove(x0+(x1-x0)*f, y0+(y1-y0)*f, Mods{}, tStart+float64(i)*0.02)
	}
	e.PointerUp(x1, y1, Mods{}, tStart+0.12)
	kids := e.Children(e.Page)
	return kids[len(kids)-1]
}

func doScribbleGesture(e *Editor, x0, y0, x1, y1 float64, tStart float64) {
	e.SetTool(ToolDraw)
	e.PointerDown(x0, y0, 0, 1, Mods{}, tStart)
	tt := tStart
	for i := 0; i < 6; i++ {
		tt += 0.02
		y := y0 + (y1-y0)*float64(i)/5.0
		if i%2 == 0 {
			e.PointerMove(x1, y, Mods{}, tt)
		} else {
			e.PointerMove(x0, y, Mods{}, tt)
		}
	}
	e.PointerUp(x0, y1, Mods{}, tt+0.02)
}

func TestScribbleEraseDeletesOnlyHitShapesInGroupAndUndoRestores(t *testing.T) {
	// Case 1: Group of [rectA, strokeB, strokeC]. Scribble over strokeB -> only strokeB deleted,
	// rectA and strokeC remain grouped, 1 Undo restores strokeB into the group.
	t.Run("three_item_group_keeps_siblings", func(t *testing.T) {
		e := newTestEditor()
		rectA := addTestRect(e, 100, 100, 120, 80, 0.1)
		strokeB := addTestFreehandLine(e, 100, 220, 240, 220, 0.3)
		strokeC := addTestFreehandLine(e, 100, 320, 240, 320, 0.5)
		e.Select(rectA, strokeB, strokeC)
		if !e.GroupSelected() {
			t.Fatal("GroupSelected failed")
		}
		gid := e.Selection()[0]

		doScribbleGesture(e, 120, 195, 220, 245, 1.0)

		if e.Doc.Shapes[strokeB] != nil {
			t.Fatal("expected hit strokeB to be scribble-erased")
		}
		if e.Doc.Shapes[rectA] == nil || e.Doc.Shapes[strokeC] == nil {
			t.Fatalf("scribble-erase deleted unhit siblings in group: rectA=%v strokeC=%v",
				e.Doc.Shapes[rectA] != nil, e.Doc.Shapes[strokeC] != nil)
		}
		if e.Doc.Shapes[gid] == nil || len(e.Children(gid)) != 2 {
			t.Fatalf("expected group %s to remain with 2 children, got shape=%v kids=%d",
				gid, e.Doc.Shapes[gid] != nil, len(e.Children(gid)))
		}

		e.Undo()
		if e.Doc.Shapes[strokeB] == nil || len(e.Children(gid)) != 3 {
			t.Fatalf("Undo did not restore strokeB into group: strokeB=%v kids=%d",
				e.Doc.Shapes[strokeB] != nil, len(e.Children(gid)))
		}
	})

	// Case 2: Group of [rectA, strokeB]. Scribble over strokeB -> strokeB deleted, group dissolved,
	// rectA promoted to page preserving its page bounds, 1 Undo restores group + strokeB.
	t.Run("two_item_group_dissolves_to_remaining_shape", func(t *testing.T) {
		e := newTestEditor()
		rectA := addTestRect(e, 100, 100, 120, 80, 0.1)
		strokeB := addTestFreehandLine(e, 100, 220, 240, 220, 0.3)
		e.Select(rectA, strokeB)
		if !e.GroupSelected() {
			t.Fatal("GroupSelected failed")
		}
		gid := e.Selection()[0]
		rectBoundsBefore := e.PageBounds(rectA)

		doScribbleGesture(e, 120, 195, 220, 245, 1.0)

		if e.Doc.Shapes[strokeB] != nil {
			t.Fatal("expected strokeB to be deleted")
		}
		if e.Doc.Shapes[rectA] == nil {
			t.Fatal("scribble-erase deleted unhit rectA in 2-item group")
		}
		if e.Doc.Shapes[gid] != nil {
			t.Fatalf("expected 1-child group %s to be dissolved", gid)
		}
		if e.Doc.Shapes[rectA].Parent != e.Page {
			t.Fatalf("rectA parent = %q, want page %q", e.Doc.Shapes[rectA].Parent, e.Page)
		}
		rectBoundsAfter := e.PageBounds(rectA)
		if math.Abs(rectBoundsAfter.X-rectBoundsBefore.X) > 1e-6 || math.Abs(rectBoundsAfter.Y-rectBoundsBefore.Y) > 1e-6 {
			t.Fatalf("rectA moved on dissolve: before=%+v after=%+v", rectBoundsBefore, rectBoundsAfter)
		}

		e.Undo()
		if e.Doc.Shapes[gid] == nil || e.Doc.Shapes[strokeB] == nil || e.Doc.Shapes[rectA].Parent != gid {
			t.Fatalf("Undo did not restore 2-item group: gid=%v strokeB=%v rectParent=%q",
				e.Doc.Shapes[gid] != nil, e.Doc.Shapes[strokeB] != nil, e.Doc.Shapes[rectA].Parent)
		}
	})

	// Case 3: Group of [strokeA, strokeB]. Scribble erases both -> empty group deleted; 1 Undo restores all.
	t.Run("all_children_erased_deletes_empty_group", func(t *testing.T) {
		e := newTestEditor()
		strokeA := addTestFreehandLine(e, 100, 200, 240, 200, 0.1)
		strokeB := addTestFreehandLine(e, 100, 230, 240, 230, 0.3)
		e.Select(strokeA, strokeB)
		if !e.GroupSelected() {
			t.Fatal("GroupSelected failed")
		}
		gid := e.Selection()[0]

		doScribbleGesture(e, 110, 185, 230, 245, 1.0)

		if e.Doc.Shapes[strokeA] != nil || e.Doc.Shapes[strokeB] != nil || e.Doc.Shapes[gid] != nil {
			t.Fatalf("expected strokes and empty group deleted: a=%v b=%v g=%v",
				e.Doc.Shapes[strokeA] != nil, e.Doc.Shapes[strokeB] != nil, e.Doc.Shapes[gid] != nil)
		}
		if e.ItemCount() != 0 {
			t.Fatalf("ItemCount = %d, want 0", e.ItemCount())
		}

		e.Undo()
		if e.Doc.Shapes[gid] == nil || e.Doc.Shapes[strokeA] == nil || e.Doc.Shapes[strokeB] == nil {
			t.Fatalf("Undo did not restore group and both strokes")
		}
	})

	// Case 4: Nested groups G1 -> [G2(strokeA, strokeB), rectC]. Scribble erases both strokes ->
	// G2 deleted, G1 dissolved, rectC promoted to page; 1 Undo restores full hierarchy.
	t.Run("nested_groups_collapse_and_undo", func(t *testing.T) {
		e := newTestEditor()
		strokeA := addTestFreehandLine(e, 100, 200, 240, 200, 0.1)
		strokeB := addTestFreehandLine(e, 100, 230, 240, 230, 0.3)
		e.Select(strokeA, strokeB)
		if !e.GroupSelected() {
			t.Fatal("inner GroupSelected failed")
		}
		g2 := e.Selection()[0]

		rectC := addTestRect(e, 320, 100, 120, 80, 0.5)
		e.Select(g2, rectC)
		if !e.GroupSelected() {
			t.Fatal("outer GroupSelected failed")
		}
		g1 := e.Selection()[0]
		rectBoundsBefore := e.PageBounds(rectC)

		doScribbleGesture(e, 110, 185, 230, 245, 1.0)

		if e.Doc.Shapes[g2] != nil || e.Doc.Shapes[g1] != nil {
			t.Fatalf("expected nested degenerate groups removed: g1=%v g2=%v",
				e.Doc.Shapes[g1] != nil, e.Doc.Shapes[g2] != nil)
		}
		if e.Doc.Shapes[rectC] == nil || e.Doc.Shapes[rectC].Parent != e.Page {
			t.Fatalf("rectC missing or wrong parent: %+v", e.Doc.Shapes[rectC])
		}
		rectBoundsAfter := e.PageBounds(rectC)
		if math.Abs(rectBoundsAfter.X-rectBoundsBefore.X) > 1e-6 || math.Abs(rectBoundsAfter.Y-rectBoundsBefore.Y) > 1e-6 {
			t.Fatalf("rectC moved on nested collapse: before=%+v after=%+v", rectBoundsBefore, rectBoundsAfter)
		}

		e.Undo()
		if e.Doc.Shapes[g1] == nil || e.Doc.Shapes[g2] == nil || e.Doc.Shapes[strokeA] == nil || e.Doc.Shapes[strokeB] == nil || e.Doc.Shapes[rectC].Parent != g1 {
			t.Fatalf("Undo did not restore nested groups")
		}
	})
}
