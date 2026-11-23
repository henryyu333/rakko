package editor

import (
	"encoding/xml"
	"strings"
	"testing"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

func TestSVGExportAndPortableBoardRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := doc.Open(dir, "Board 1")
	if err != nil {
		t.Fatal(err)
	}
	d, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEditor()
	e.Replace(d)

	// Add rectangle with text, frame with clipped child, and cropped image.
	e.SetGeoTool("rounded-rect")
	e.Next.Stroke = "#38BDF8"
	e.Next.Fill = "semi"
	e.Next.FillColor = "#38BDF8"
	e.PointerDown(60, 60, 0, 1, Mods{}, 0.1)
	e.PointerMove(220, 150, Mods{}, 0.12)
	e.PointerUp(220, 150, Mods{}, 0.14)
	rid := e.Selection()[0]
	e.StartEditing(rid)
	e.EditText = "Canvas SVG & <XML>"
	e.EditChanged()
	e.StopEditing()

	asset, err := st.SaveAsset([]byte("fake-png-bytes"), "png", "photo.png", "image/png", 200, 100)
	if err != nil {
		t.Fatal(err)
	}
	imgID := e.InsertImage(asset, geo.V(340, 120))
	e.update(imgID, func(c *doc.Shape) {
		c.Crop = &doc.Crop{TopLeft: geo.V(0.1, 0.2), BottomRight: geo.V(0.9, 0.8)}
	})
	e.commit()

	svg, err := e.ExportSVG(nil, DarkPalette, func(id doc.ID) ([]byte, string, bool) {
		if id == asset.ID {
			return []byte("fake-png-bytes"), "image/png", true
		}
		return nil, "", false
	})
	if err != nil {
		t.Fatalf("ExportSVG: %v", err)
	}
	if !strings.HasPrefix(svg, "<svg ") || !strings.Contains(svg, "<path ") || !strings.Contains(svg, "<text ") || !strings.Contains(svg, "<image ") || !strings.Contains(svg, "<clipPath ") {
		t.Fatalf("SVG missing expected elements:\n%s", svg)
	}
	if err := xml.Unmarshal([]byte(svg), new(any)); err != nil {
		t.Fatalf("invalid SVG XML: %v\n%s", err, svg)
	}

	// .rakkoboard.json package export & import round-trip preserving assets and shapes.
	rawPkg, err := doc.ExportPackage(e.Doc, st)
	if err != nil {
		t.Fatalf("ExportPackage: %v", err)
	}
	imported, err := doc.ImportPackage(rawPkg, st, "Imported")
	if err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}
	if imported.ID == e.Doc.ID || len(imported.Shapes) != len(e.Doc.Shapes) || len(imported.Assets) != 1 {
		t.Fatalf("imported board mismatch: id=%q shapes=%d assets=%d", imported.ID, len(imported.Shapes), len(imported.Assets))
	}
}

func TestImageInteractiveCropResetAndLaserTrails(t *testing.T) {
	e := newTestEditor()
	asset := &doc.Asset{ID: doc.ID("asset:1"), Type: "image", W: 200, H: 100, Path: "assets/1.png"}
	e.Doc.Assets[asset.ID] = asset
	imgID := e.InsertImage(asset, geo.V(200, 150))
	orig := e.Doc.Shapes[imgID]
	origX, origW := orig.X, orig.W

	// Double-click image enters interactive crop mode.
	if !e.EnterCrop(imgID) || e.Cropping != imgID {
		t.Fatal("EnterCrop failed")
	}
	hs := e.Handles()
	var leftMid Handle
	found := false
	for _, h := range hs {
		if h.Kind == HandleCrop && h.Which.X == 0 && h.Which.Y == 0.5 {
			leftMid = h
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected left-middle HandleCrop in %+v", hs)
	}
	sp := e.ToScreen(leftMid.Point)
	e.PointerDown(sp.X, sp.Y, 0, 1, Mods{}, 0.2)
	e.PointerMove(sp.X+origW*0.25, sp.Y, Mods{}, 0.22)
	e.PointerUp(sp.X+origW*0.25, sp.Y, Mods{}, 0.24)

	cropped := e.Doc.Shapes[imgID]
	if cropped.Crop == nil || cropped.Crop.TopLeft.X < 0.2 || cropped.W >= origW*0.8 {
		t.Fatalf("expected ~25%% left crop: W=%v origW=%v crop=%+v", cropped.W, origW, cropped.Crop)
	}
	if cropped.X <= origX {
		t.Fatalf("expected X to shift right keeping visible pixels anchored: X=%v origX=%v", cropped.X, origX)
	}

	// ResetCrop restores full uncropped bounds and clears Crop cleanly in undoable tx.
	if !e.CanResetCrop() || !e.ResetCrop(imgID) {
		t.Fatal("ResetCrop failed")
	}
	restored := e.Doc.Shapes[imgID]
	if restored.Crop != nil || restored.W < origW-1 || restored.X > origX+1 {
		t.Fatalf("ResetCrop mismatch: %+v vs orig (%v, %v)", restored, origX, origW)
	}

	// Laser pointer creates temporary fading trail without writing undo stack.
	undoBefore := e.CanUndo()
	e.SetTool(ToolLaser)
	e.PointerDown(100, 100, 0, 1, Mods{}, 1.0)
	e.PointerMove(160, 120, Mods{}, 1.2)
	e.PointerUp(160, 120, Mods{}, 1.4)
	if len(e.Trails) != 1 || !e.Trails[0].Laser || len(e.Trails[0].Points) != 2 {
		t.Fatalf("unexpected laser trails: %+v", e.Trails)
	}
	if e.CanUndo() != undoBefore {
		t.Fatal("laser pointer unexpectedly dirtied undo history")
	}
	if !e.AgeTrails(1.8) || len(e.Trails) != 1 {
		t.Fatal("expected laser trail still alive at 1.8s")
	}
	if e.AgeTrails(2.6) || len(e.Trails) != 0 {
		t.Fatal("expected laser trail faded out after 1.0s lifetime")
	}
}
