package editor

import (
	"math"
	"testing"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

func TestCameraPanZoomAndBounds(t *testing.T) {
	d := doc.NewDrawing("b1", "Board 1")
	e := New(d, nil)
	e.SetViewport(1000, 600)

	// Initial roundtrip.
	pt := geo.V(320, 240)
	if rt := e.ToScreen(e.ToPage(pt)); rt.Dist(pt) > 1e-9 {
		t.Fatalf("ToScreen(ToPage) = %+v, want %+v", rt, pt)
	}

	// Panning by screen delta moves ViewBox by dx/z, dy/z.
	e.Pan(100, -50)
	vb := e.ViewBox()
	if math.Abs(vb.X-100) > 1e-9 || math.Abs(vb.Y-(-50)) > 1e-9 {
		t.Fatalf("ViewBox after Pan = %+v", vb)
	}

	// ZoomAt keeps the page coordinate under the cursor fixed.
	cursor := geo.V(420, 310)
	beforePage := e.ToPage(cursor)
	e.ZoomAt(cursor, 2.0)
	afterPage := e.ToPage(cursor)
	if beforePage.Dist(afterPage) > 1e-9 {
		t.Fatalf("ZoomAt shifted cursor page point: %+v -> %+v", beforePage, afterPage)
	}
	if e.Cam().Z != 2.0 {
		t.Fatalf("Zoom = %v, want 2.0", e.Cam().Z)
	}

	// Clamps to [MinZoom, MaxZoom] (5%..800%).
	e.ZoomAt(cursor, 100)
	if e.Cam().Z != MaxZoom {
		t.Fatalf("max zoom = %v, want %v", e.Cam().Z, MaxZoom)
	}
	e.ZoomAt(cursor, 0.0001)
	if e.Cam().Z != MinZoom {
		t.Fatalf("min zoom = %v, want %v", e.Cam().Z, MinZoom)
	}

	// ZoomIn / ZoomOut / ResetZoom stops.
	e.ResetZoom()
	if e.Cam().Z != 1.0 {
		t.Fatalf("ResetZoom = %v", e.Cam().Z)
	}
	e.ZoomIn()
	if e.Cam().Z != 2.0 {
		t.Fatalf("ZoomIn from 1.0 = %v, want 2.0", e.Cam().Z)
	}
	e.ZoomOut()
	if e.Cam().Z != 1.0 {
		t.Fatalf("ZoomOut from 2.0 = %v, want 1.0", e.Cam().Z)
	}
}

func TestPerPageCameraAndPreferences(t *testing.T) {
	d := doc.NewDrawing("b1", "Board 1")
	p2 := d.AddPage("Page 2")
	changes := 0
	e := New(d, nil)
	e.OnChange = func(*doc.Drawing) { changes++ }

	e.Pan(120, 80)
	e.ZoomTo(geo.V(100, 100), 2.5)
	cam1 := e.Cam()

	if !e.SwitchPage(p2.ID) {
		t.Fatal("SwitchPage failed")
	}
	if e.Cam() != (Camera{Z: 1}) {
		t.Fatalf("page 2 initial cam = %+v, want default", e.Cam())
	}
	e.Pan(-40, 20)
	cam2 := e.Cam()

	e.SwitchPage(d.Pages[0].ID)
	if e.Cam() != cam1 {
		t.Fatalf("page 1 cam lost: %+v != %+v", e.Cam(), cam1)
	}
	e.SwitchPage(p2.ID)
	if e.Cam() != cam2 {
		t.Fatalf("page 2 cam lost: %+v != %+v", e.Cam(), cam2)
	}

	e.SetPaper(doc.PaperGrid)
	if e.Paper() != doc.PaperGrid {
		t.Fatalf("Paper = %q", e.Paper())
	}
	e.SetDarkMode(false)
	if e.DarkMode() || e.Palette() != LightPalette {
		t.Fatal("SetDarkMode(false) did not switch to LightPalette")
	}
	e.SetLook(doc.LookClean)
	if e.Look() != doc.LookClean {
		t.Fatalf("Look = %q", e.Look())
	}
	if changes == 0 {
		t.Fatal("expected OnChange callbacks")
	}
}
