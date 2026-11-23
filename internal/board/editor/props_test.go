package editor

import (
	"testing"

	"rakko/internal/board/doc"
)

func TestPropertyPanelAdaptiveStateAndMutations(t *testing.T) {
	e := newTestEditor()

	// Empty selection + non-styled tools hide property panel.
	e.SetTool(ToolSelect)
	if st := e.PanelState(); st.Visible {
		t.Fatalf("expected panel hidden for ToolSelect with empty selection")
	}
	e.SetTool(ToolHand)
	if st := e.PanelState(); st.Visible {
		t.Fatalf("expected panel hidden for ToolHand")
	}
	e.SetTool(ToolEraser)
	if st := e.PanelState(); !st.Visible || !st.ShowEraserProps {
		t.Fatalf("expected eraser property controls, got %+v", st)
	}

	// Create a geo shape and test adaptive sections + transactional mutations.
	e.SetGeoTool("rectangle")
	e.PointerDown(100, 100, 0, 1, Mods{}, 0.1)
	e.PointerMove(260, 200, Mods{}, 0.2)
	e.PointerUp(260, 200, Mods{}, 0.3)
	geoID := e.Selection()[0]

	ps := e.PanelState()
	if !ps.Visible || !ps.ShowFillTab || !ps.ShowGeoShape || !ps.ShowStrokeSize || !ps.ShowFont {
		t.Fatalf("unexpected geo PanelState: %+v", ps)
	}

	e.SetPropStroke("#38BDF8")
	e.SetPropFillColor("#F472B6") // Auto-switches Fill from none -> semi.
	e.SetPropFill("pattern")
	e.SetPropSize("l")
	e.SetPropDash("dash")
	e.SetPropOpacity(0.75)
	e.SetPropGeo("diamond")
	e.SetPropFont("serif")
	e.SetPropFontSize(32)
	e.TogglePropBold()
	e.TogglePropItalic()
	e.TogglePropUnderline()
	e.SetPropAlign("right")
	e.SetPropVAlign("bottom")

	s := e.Doc.Shapes[geoID]
	if s.Color != "#38BDF8" || s.FillColor != "#F472B6" || s.Fill != "pattern" || s.Size != "l" ||
		s.Dash != "dash" || s.Opacity != 0.75 || s.Geo != "diamond" || s.Font != "serif" || s.FontSize != 32 ||
		!s.Bold || !s.Italic || !s.Underline || s.Align != "right" || s.VAlign != "bottom" {
		t.Fatalf("geo properties did not apply cleanly: %+v", s)
	}

	// Undo restores prior VAlign, Redo restores bottom.
	e.Undo()
	if e.Doc.Shapes[geoID].VAlign == "bottom" {
		t.Fatalf("Undo did not revert VAlign")
	}
	e.Redo()
	if e.Doc.Shapes[geoID].VAlign != "bottom" {
		t.Fatalf("Redo did not restore VAlign")
	}

	// Arrow properties: route (straight/curve/elbow) + 8 heads.
	e.SetTool(ToolArrow)
	e.PointerDown(300, 100, 0, 1, Mods{}, 0.4)
	e.PointerMove(460, 180, Mods{}, 0.5)
	e.PointerUp(460, 180, Mods{}, 0.6)
	arrowID := e.Selection()[0]
	if aps := e.PanelState(); !aps.ShowArrowProps || aps.ArrowRoute != "straight" {
		t.Fatalf("unexpected arrow PanelState: %+v", aps)
	}
	e.SetPropArrowRoute("curve")
	if e.Doc.Shapes[arrowID].Bend == 0 {
		t.Fatalf("expected non-zero Bend for curve route")
	}
	e.SetPropArrowRoute("elbow")
	if e.Doc.Shapes[arrowID].Kind != "elbow" {
		t.Fatalf("expected elbow kind, got %s", e.Doc.Shapes[arrowID].Kind)
	}
	e.SetPropArrowHeads("diamond", "bar")
	if e.Doc.Shapes[arrowID].HeadStart != "diamond" || e.Doc.Shapes[arrowID].HeadEnd != "bar" {
		t.Fatalf("arrow heads not updated: %+v", e.Doc.Shapes[arrowID])
	}

	// Locked shape skips style mutations while updating e.Next.
	e.update(geoID, func(c *doc.Shape) { c.Locked = true })
	e.commit()
	e.Select(geoID)
	e.SetPropStroke("#4ADE80")
	if e.Doc.Shapes[geoID].Color != "#38BDF8" {
		t.Fatalf("locked shape color mutated unexpectedly to %s", e.Doc.Shapes[geoID].Color)
	}
	if e.Next.Stroke != "#4ADE80" {
		t.Fatalf("Next.Stroke did not update to #4ADE80")
	}
}

func TestLooksAndDeterministicSketchRibbon(t *testing.T) {
	e := newTestEditor()
	e.SetGeoTool("rounded-rect")
	e.PointerDown(40, 40, 0, 1, Mods{}, 0.1)
	e.PointerMove(220, 150, Mods{}, 0.2)
	e.PointerUp(220, 150, Mods{}, 0.3)
	id := e.Selection()[0]
	e.SetPropStroke("#F87171")
	s := e.Doc.Shapes[id]
	pal := DarkPalette

	// Sketch produces deterministic variable-width ribbon fill with Ink centerline.
	e.SetLook(doc.LookSketch)
	p1 := e.Display(s, pal)
	p2 := e.Display(s, pal)
	if len(p1) < 2 || p1[0].Kind != PrimFill || p1[0].Ink == nil || len(p1[0].Ink.Pts) < 4 {
		t.Fatalf("expected sketch PrimFill ribbon with Ink, got %+v", p1)
	}
	if len(p1[0].Path.Segs) != len(p2[0].Path.Segs) || p1[0].Path.Segs[0] != p2[0].Path.Segs[0] {
		t.Fatalf("sketch ribbon not deterministic across calls")
	}

	// Clean produces single crisp stroke without ribbon fill.
	e.SetLook(doc.LookClean)
	clean := e.Display(s, pal)
	if len(clean) != 1 || clean[0].Kind != PrimStroke || clean[0].Color != Hex("#F87171") {
		t.Fatalf("unexpected clean prims: %+v", clean)
	}

	// Mono maps color to monochrome MonoInk.
	e.SetLook(doc.LookMono)
	mono := e.Display(s, pal)
	if len(mono) != 1 || mono[0].Kind != PrimStroke || mono[0].Color != pal.MonoInk {
		t.Fatalf("expected MonoInk %+v, got %+v", pal.MonoInk, mono)
	}
}
