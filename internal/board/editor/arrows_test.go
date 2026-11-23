package editor

import (
	"math"
	"testing"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

func TestArrowAndLineBindingFollowCmdAltAndDeleteUnbind(t *testing.T) {
	e := newTestEditor()
	boxA := createRect(t, e, 100, 100, 100, 80) // center (150, 140)
	boxB := createRect(t, e, 400, 100, 100, 80) // center (450, 140)

	// 1. Draw an arrow from center of boxA to an Alt-exact off-center anchor on boxB.
	e.SetTool(ToolArrow)
	e.PointerDown(150, 140, 0, 1, Mods{}, 1)
	e.PointerMove(420, 170, Mods{Alt: true}, 1.1)
	if !e.Hint[boxB] {
		t.Fatalf("expected Hint highlight on boxB while dragging arrow end")
	}
	e.PointerUp(420, 170, Mods{Alt: true}, 1.2)
	arrowID := e.Selection()[0]

	bs := e.bindingOf(arrowID, "start")
	be := e.bindingOf(arrowID, "end")
	if bs == nil || bs.To != boxA || bs.IsPrecise {
		t.Fatalf("start binding = %+v, want center-bound to %s", bs, boxA)
	}
	if be == nil || be.To != boxB || !be.IsExact || !be.IsPrecise {
		t.Fatalf("end binding = %+v, want Alt-exact bound to %s", be, boxB)
	}

	// Arrow endpoints are clipped to boxA/boxB edges, not raw centers.
	ar := e.Doc.Shapes[arrowID]
	am := e.PageMat(arrowID)
	info0 := e.Arrow(ar)
	pStart := am.Apply(info0.Start)
	pEnd := am.Apply(info0.End)
	if pStart.X < 195 || pEnd.X > 405 {
		t.Fatalf("expected clipped arrow endpoints in page space, got Start=%v End=%v", pStart, pEnd)
	}

	// Move boxB down by +120px -> arrow follows automatically!
	e.Select(boxB)
	e.Nudge(0, 120)
	info1 := e.Arrow(ar)
	if math.Abs(info1.RawEnd.Y-info0.RawEnd.Y-120) > 1e-6 || info1.End.Y <= info0.End.Y+100 {
		t.Fatalf("arrow did not follow boxB move: before=%+v after=%+v", info0, info1)
	}

	// Delete boxB -> arrow unbinds cleanly, keeping its visual End at info1.End!
	e.Select(boxB)
	e.DeleteSelected()
	if e.bindingOf(arrowID, "end") != nil {
		t.Fatal("expected end binding removed after deleting boxB")
	}
	arAfterDel := e.Doc.Shapes[arrowID]
	if arAfterDel == nil || math.Abs(arAfterDel.End.Y-info1.End.Y) > 1 {
		t.Fatalf("arrow End jumped after target deletion: got %+v, want Y~%v", arAfterDel, info1.End.Y)
	}
	// Undo restores boxB and the binding.
	e.Undo()
	if e.Doc.Shapes[boxB] == nil || e.bindingOf(arrowID, "end") == nil {
		t.Fatal("Undo did not restore deleted target and binding")
	}

	// 2. Cmd held prevents snapping/binding during arrow creation.
	e.SetTool(ToolArrow)
	e.PointerDown(150, 140, 0, 1, Mods{Cmd: true}, 2)
	e.PointerMove(450, 260, Mods{Cmd: true}, 2.1)
	e.PointerUp(450, 260, Mods{Cmd: true}, 2.2)
	freeArrow := e.Selection()[0]
	if e.bindingOf(freeArrow, "start") != nil || e.bindingOf(freeArrow, "end") != nil {
		t.Fatalf("Cmd held should suppress arrow binding, got start=%v end=%v",
			e.bindingOf(freeArrow, "start"), e.bindingOf(freeArrow, "end"))
	}

	// 3. ToolLine also binds to shapes and follows movement.
	e.SetTool(ToolLine)
	e.PointerDown(150, 140, 0, 1, Mods{}, 3)
	e.PointerMove(450, 260, Mods{}, 3.1)
	e.PointerUp(450, 260, Mods{}, 3.2)
	lineID := e.Selection()[0]
	if e.bindingOf(lineID, "start") == nil || e.bindingOf(lineID, "end") == nil {
		t.Fatalf("expected ToolLine to bind to boxA and boxB")
	}
	pts0 := e.linePoints(e.Doc.Shapes[lineID])
	e.Select(boxA)
	e.Nudge(50, 0)
	pts1 := e.linePoints(e.Doc.Shapes[lineID])
	if math.Abs(pts1[0].X-pts0[0].X-50) > 2 {
		t.Fatalf("line did not follow boxA move: before=%v after=%v", pts0[0], pts1[0])
	}
}

func TestArrow3RoutesAnd8Heads(t *testing.T) {
	if len(ArrowHeads) != 8 {
		t.Fatalf("ArrowHeads len = %d, want 8", len(ArrowHeads))
	}
	e := newTestEditor()
	pal := e.Palette()

	// 1. Straight -> Curved via bend drag -> Elbow via Kind="elbow" and midpoint drag/insert.
	e.SetTool(ToolArrow)
	e.PointerDown(100, 200, 0, 1, Mods{}, 1)
	e.PointerMove(300, 200, Mods{}, 1.1)
	e.PointerUp(300, 200, Mods{}, 1.2)
	id := e.Selection()[0]

	info := e.Arrow(e.Doc.Shapes[id])
	if info.Arc || len(info.Line) != 2 {
		t.Fatalf("expected straight arrow initially, got %+v", info)
	}

	// Drag midpoint (200, 200) up to (200, 140) -> curved arc!
	e.PointerDown(200, 200, 0, 1, Mods{}, 2)
	e.PointerMove(200, 140, Mods{}, 2.1)
	e.PointerUp(200, 140, Mods{}, 2.2)
	info = e.Arrow(e.Doc.Shapes[id])
	if !info.Arc || math.Abs(e.Doc.Shapes[id].Bend) < 40 {
		t.Fatalf("expected curved arc after bend drag, got Arc=%v Bend=%v", info.Arc, e.Doc.Shapes[id].Bend)
	}

	// Switch to elbow and drag elbow midpoint + insert virtual leg point.
	e.update(id, func(c *doc.Shape) { c.Kind = "elbow" })
	info = e.Arrow(e.Doc.Shapes[id])
	if len(info.Elbow) != 4 {
		t.Fatalf("expected 4-point elbow initially, got %d", len(info.Elbow))
	}
	// Drag elbow middle handle (200, 200) to (260, 200) -> ElbowMidPoint shifts toward 0.8.
	e.PointerDown(200, 200, 0, 1, Mods{}, 3)
	e.PointerMove(260, 200, Mods{}, 3.1)
	e.PointerUp(260, 200, Mods{}, 3.2)
	if math.Abs(e.Doc.Shapes[id].ElbowMidPoint-0.8) > 0.05 {
		t.Fatalf("ElbowMidPoint = %v, want ~0.8", e.Doc.Shapes[id].ElbowMidPoint)
	}
	// Drag virtual midpoint on first elbow leg to insert custom waypoint.
	var virt Handle
	for _, h := range e.Handles() {
		if h.Kind == HandleArrowBend && h.Virtual {
			virt = h
			break
		}
	}
	if !virt.Virtual {
		t.Fatal("expected virtual leg midpoint handle on elbow arrow")
	}
	e.PointerDown(virt.Point.X, virt.Point.Y, 0, 1, Mods{}, 4)
	e.PointerMove(virt.Point.X, virt.Point.Y-40, Mods{}, 4.1)
	e.PointerUp(virt.Point.X, virt.Point.Y-40, Mods{}, 4.2)
	if len(e.Doc.Shapes[id].Points) == 0 || len(e.Arrow(e.Doc.Shapes[id]).Elbow) <= 4 {
		t.Fatalf("expected custom elbow waypoint insertion, got Points=%d Elbow=%d",
			len(e.Doc.Shapes[id].Points), len(e.Arrow(e.Doc.Shapes[id]).Elbow))
	}

	// 2. All 8 arrowheads render cleanly on both start and end.
	for _, head := range ArrowHeads {
		e.update(id, func(c *doc.Shape) {
			c.Kind = "arc"
			c.Bend = 0
			c.Points = nil
			c.HeadStart = head
			c.HeadEnd = head
		})
		prims := e.Display(e.Doc.Shapes[id], pal)
		if head == "none" && len(prims) < 1 {
			t.Fatalf("none/none heads prims = %d, want >= 1", len(prims))
		}
		if head != "none" && len(prims) < 4 {
			t.Fatalf("head %q prims = %d, want >= 4 (sketch body + start + end)", head, len(prims))
		}
	}
}

func TestLineMidpointInsertSplineAndGeoVertexEditing(t *testing.T) {
	e := newTestEditor()

	// 1. Create Line -> drag virtual midpoint to insert vertex -> switch to cubic spline.
	e.SetTool(ToolLine)
	e.PointerDown(100, 100, 0, 1, Mods{}, 1)
	e.PointerMove(300, 100, Mods{}, 1.1)
	e.PointerUp(300, 100, Mods{}, 1.2)
	lineID := e.Selection()[0]

	e.PointerDown(200, 100, 0, 1, Mods{}, 2) // virtual midpoint handle
	e.PointerMove(200, 40, Mods{}, 2.1)
	e.PointerUp(200, 40, Mods{}, 2.2)
	ls := e.Doc.Shapes[lineID]
	if len(ls.Points) != 3 {
		t.Fatalf("line points after midpoint insert = %d, want 3", len(ls.Points))
	}
	e.update(lineID, func(c *doc.Shape) { c.Spline = "cubic" })
	if len(e.Display(e.Doc.Shapes[lineID], e.Palette())) == 0 || len(e.Geometry(e.Doc.Shapes[lineID]).Outline) <= 3 {
		t.Fatal("cubic spline did not tessellate smoothly")
	}

	// 2. Curve vertex editing (double-click enters EditingVertices, drag vertex, insert midpoint, Esc exits).
	e.SetGeoTool("curve")
	e.PointerDown(100, 200, 0, 1, Mods{}, 3)
	e.PointerMove(300, 320, Mods{}, 3.1)
	e.PointerUp(300, 320, Mods{}, 3.2)
	curveID := e.Selection()[0]

	if !e.EditVertices(curveID) {
		t.Fatal("EditVertices failed on curve")
	}
	hs := e.Handles()
	if len(hs) != 7 { // 4 vertices + 3 virtual midpoints
		t.Fatalf("curve vertex handles = %d, want 7", len(hs))
	}
	// Drag a virtual midpoint to insert a 5th vertex on the curve.
	var vtxVirtual Handle
	for _, h := range hs {
		if h.Virtual {
			vtxVirtual = h
			break
		}
	}
	e.PointerDown(vtxVirtual.Point.X, vtxVirtual.Point.Y, 0, 1, Mods{}, 4)
	e.PointerMove(vtxVirtual.Point.X, vtxVirtual.Point.Y-30, Mods{}, 4.1)
	e.PointerUp(vtxVirtual.Point.X, vtxVirtual.Point.Y-30, Mods{}, 4.2)
	if len(e.Doc.Shapes[curveID].Points) != 5 {
		t.Fatalf("curve points after midpoint insert = %d, want 5", len(e.Doc.Shapes[curveID].Points))
	}

	// Esc exits vertex editing without deselecting curve.
	e.Cancel()
	if e.EditingVertices != "" || !e.IsSelected(curveID) {
		t.Fatalf("Cancel should exit EditingVertices while keeping curve selected: ev=%q sel=%v", e.EditingVertices, e.Selection())
	}

	// 3. Polygon vertex editing.
	e.SetGeoTool("polygon")
	e.PointerDown(400, 200, 0, 1, Mods{}, 5)
	e.PointerMove(560, 360, Mods{}, 5.1)
	e.PointerUp(560, 360, Mods{}, 5.2)
	polyID := e.Selection()[0]
	if !e.EditVertices(polyID) {
		t.Fatal("EditVertices failed on polygon")
	}
	// 7 default vertices + 7 virtual edge midpoints = 14 handles.
	if got := len(e.Handles()); got != 14 {
		t.Fatalf("polygon vertex handles = %d, want 14", got)
	}
}

func TestSmartShapeRecognitionAndScribbleErase(t *testing.T) {
	e := newTestEditor()

	// 1. Draw a rough rectangle with PenWidth=6 and dwell at the end -> auto-snaps to rectangle keeping PenWidth=6!
	e.SetTool(ToolDraw)
	e.Next.PenWidth = 6
	e.Next.Stroke = "#38BDF8"
	rectPts := []geo.Vec{
		{X: 100, Y: 100}, {X: 160, Y: 102}, {X: 220, Y: 99},
		{X: 221, Y: 140}, {X: 219, Y: 180},
		{X: 160, Y: 181}, {X: 101, Y: 179},
		{X: 99, Y: 140}, {X: 100, Y: 102},
	}
	e.PointerDown(rectPts[0].X, rectPts[0].Y, 0, 1, Mods{}, 1.0)
	for i, pt := range rectPts[1:] {
		e.PointerMove(pt.X, pt.Y, Mods{}, 1.0+float64(i+1)*0.03)
	}
	// Dwell with Shift held for >= 0.45s -> regularizes to square!
	if !e.CheckDwell(2.0) {
		// Force move with Shift + dwell time if CheckDwell didn't have Shift mod yet.
		t.Fatal("CheckDwell did not trigger snap after 0.45s pause")
	}
	e.PointerUp(rectPts[len(rectPts)-1].X, rectPts[len(rectPts)-1].Y, Mods{}, 2.05)

	if e.ItemCount() != 1 {
		t.Fatalf("ItemCount = %d, want 1", e.ItemCount())
	}
	snappedRect := e.Doc.Shapes[e.Children(e.Page)[0]]
	if snappedRect.Type != doc.TypeGeo || snappedRect.Geo != "rectangle" {
		t.Fatalf("expected recognized rectangle, got %+v", snappedRect)
	}
	if snappedRect.PenWidth != 6 || snappedRect.Color != "#38BDF8" {
		t.Fatalf("recognized shape lost PenWidth/Color: %+v", snappedRect)
	}

	// 2. Circle recognition with Shift regularization -> exact square bounds (W == H).
	e.SetTool(ToolDraw)
	e.Next.PenWidth = 4
	e.PointerDown(480, 200, 0, 1, Mods{}, 3.0)
	for i := 1; i <= 24; i++ {
		a := 2 * math.Pi * float64(i) / 24
		e.PointerMove(400+80*math.Cos(a), 200+50*math.Sin(a), Mods{}, 3.0+float64(i)*0.02)
	}
	e.PointerMove(480, 200, Mods{Shift: true}, 4.2) // dwell with Shift
	e.PointerUp(480, 200, Mods{Shift: true}, 4.25)
	circle := e.Doc.Shapes[e.Children(e.Page)[1]]
	if circle.Type != doc.TypeGeo || circle.Geo != "ellipse" || math.Abs(circle.W-circle.H) > 1e-6 {
		t.Fatalf("Shift-regularized circle = %+v", circle)
	}

	// 3. Straight line recognition with Shift -> 45-degree angle.
	e.SetTool(ToolDraw)
	e.PointerDown(100, 350, 0, 1, Mods{}, 5.0)
	for i := 1; i <= 10; i++ {
		e.PointerMove(100+float64(i)*12, 350+float64(i)*2, Mods{}, 5.0+float64(i)*0.02)
	}
	// Dwell with Shift at end -> snaps line to 0 degrees!
	e.PointerMove(220, 370, Mods{Shift: true}, 5.8)
	e.PointerUp(220, 370, Mods{Shift: true}, 5.85)
	ln := e.Doc.Shapes[e.Children(e.Page)[2]]
	if ln.Type != doc.TypeLine || math.Abs(ln.Points[1].Y) > 1e-6 {
		t.Fatalf("expected Shift-regularized horizontal line, got %+v", ln)
	}

	// 4. Triangle recognition.
	e.SetTool(ToolDraw)
	triPts := []geo.Vec{{X: 600, Y: 100}, {X: 680, Y: 220}, {X: 520, Y: 220}, {X: 600, Y: 102}}
	e.PointerDown(triPts[0].X, triPts[0].Y, 0, 1, Mods{}, 6.0)
	for i, pt := range triPts[1:] {
		e.PointerMove(pt.X, pt.Y, Mods{}, 6.0+float64(i+1)*0.03)
	}
	e.PointerMove(600, 102, Mods{}, 7.0)
	e.PointerUp(600, 102, Mods{}, 7.05)
	tri := e.Doc.Shapes[e.Children(e.Page)[3]]
	if tri.Type != doc.TypeGeo || tri.Geo != "triangle" {
		t.Fatalf("expected recognized triangle, got %+v", tri)
	}

	// 5. Scribble back-and-forth over snappedRect erases snappedRect + the scribble itself in 1 undo step.
	beforeCount := e.ItemCount()
	e.SetTool(ToolDraw)
	scribble := []geo.Vec{
		{X: 90, Y: 110}, {X: 230, Y: 115},
		{X: 90, Y: 135}, {X: 230, Y: 140},
		{X: 90, Y: 160}, {X: 230, Y: 165},
		{X: 90, Y: 178},
	}
	e.PointerDown(scribble[0].X, scribble[0].Y, 0, 1, Mods{}, 8.0)
	for i, pt := range scribble[1:] {
		e.PointerMove(pt.X, pt.Y, Mods{}, 8.0+float64(i+1)*0.02)
	}
	e.PointerUp(scribble[len(scribble)-1].X, scribble[len(scribble)-1].Y, Mods{}, 8.2)
	if e.ItemCount() != beforeCount-1 || e.Doc.Shapes[snappedRect.ID] != nil {
		t.Fatalf("scribble did not erase snappedRect: count=%d (want %d), exists=%v",
			e.ItemCount(), beforeCount-1, e.Doc.Shapes[snappedRect.ID] != nil)
	}
	// Single Undo brings snappedRect back cleanly.
	e.Undo()
	if e.ItemCount() != beforeCount || e.Doc.Shapes[snappedRect.ID] == nil {
		t.Fatalf("Undo after scribble erase failed: count=%d", e.ItemCount())
	}
}
