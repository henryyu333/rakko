package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
)

func boardColorRGBA(c editor.Color) color.RGBA {
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: uint8(c.A * 255)}
}

func countMatchingPixels(img *image.RGBA, box ui.Rect, want color.RGBA, tol int) int {
	b := img.Bounds()
	x0 := max(b.Min.X, int(box.X))
	y0 := max(b.Min.Y, int(box.Y))
	x1 := min(b.Max.X, int(box.X+box.W))
	y1 := min(b.Max.Y, int(box.Y+box.H))
	n := 0
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			got := img.RGBAAt(x, y)
			if abs(int(got.R)-int(want.R)) <= tol &&
				abs(int(got.G)-int(want.G)) <= tol &&
				abs(int(got.B)-int(want.B)) <= tol {
				n++
			}
		}
	}
	return n
}

func TestBoard1187SolidLabelContrastAndScreenshots(t *testing.T) {
	a, b := boardTestShelf(t)
	view := ui.NewTester(a.boardView, 900, 520)
	view.SetLocale("zh-Hans")

	// Solid same-color rectangle with text.
	b.ed.SetGeoTool("rectangle")
	b.ed.Next.Stroke = "#38BDF8"
	b.ed.Next.Fill = "solid"
	b.ed.Next.FillColor = ""
	b.ed.PointerDown(80, 90, 0, 1, editor.Mods{}, 0.1)
	b.ed.PointerMove(320, 210, editor.Mods{}, 0.2)
	b.ed.PointerUp(320, 210, editor.Mods{}, 0.3)
	rectID := b.ed.Selection()[0]
	b.ed.StartEditing(rectID)
	b.ed.EditText = "Solid Rect 文字"
	b.ed.EditChanged()
	b.ed.StopEditing()

	// Solid same-color ellipse with text.
	b.ed.SetGeoTool("ellipse")
	b.ed.Next.Stroke = "#4ADE80"
	b.ed.Next.Fill = "solid"
	b.ed.Next.FillColor = ""
	b.ed.PointerDown(380, 90, 0, 1, editor.Mods{}, 0.4)
	b.ed.PointerMove(620, 210, editor.Mods{}, 0.5)
	b.ed.PointerUp(620, 210, editor.Mods{}, 0.6)
	ellID := b.ed.Selection()[0]
	b.ed.StartEditing(ellID)
	b.ed.EditText = "Solid Ellipse 椭圆"
	b.ed.EditChanged()
	b.ed.StopEditing()

	// Arrow with label.
	b.ed.SetTool(editor.ToolArrow)
	b.ed.Next.Stroke = "#F87171"
	b.ed.PointerDown(120, 310, 0, 1, editor.Mods{}, 0.7)
	b.ed.PointerMove(580, 310, editor.Mods{}, 0.8)
	b.ed.PointerUp(580, 310, editor.Mods{}, 0.9)
	arrowID := b.ed.Selection()[0]
	b.ed.StartEditing(arrowID)
	b.ed.EditText = "Arrow 箭头标签"
	b.ed.EditChanged()
	b.ed.StopEditing()

	b.ed.SelectNone()

	for _, tc := range []struct {
		name string
		dark bool
	}{
		{"dark", true},
		{"light", false},
	} {
		b.setDarkMode(tc.dark)
		view.Frame()
		view.Frame()
		img := view.Image()
		saveTestShot(t, "board1187-seg1", "solid-labels-"+tc.name+".png", img)

		pal := b.ed.Palette()
		wantText := boardColorRGBA(pal.Background)
		// Check inside the center of the solid rectangle (stage starts below boardBarH).
		rb := b.ed.PageBounds(rectID)
		checkRect := ui.Rect{
			X: float32(rb.X + 20),
			Y: float32(rb.Y+20) + boardBarH,
			W: float32(rb.W - 40),
			H: float32(rb.H - 40),
		}
		if n := countMatchingPixels(img, checkRect, wantText, 24); n < 30 {
			t.Fatalf("%s theme: solid rect interior has only %d label pixels matching pal.Background %v; text is invisible", tc.name, n, wantText)
		}

		// Also verify SVG export contains the inverted label color and write SVG if RAKKO_SHOTS is set.
		svg, err := b.ed.ExportSVG(nil, pal, nil)
		if err != nil {
			t.Fatalf("ExportSVG %s: %v", tc.name, err)
		}
		if root := os.Getenv("RAKKO_SHOTS"); root != "" {
			dir := filepath.Join(root, "board1187-seg1")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "solid-labels-"+tc.name+".svg"), []byte(svg), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestBoardConfirmModalEscapeCancelsWithoutLeakingToCanvas(t *testing.T) {
	a, b := boardTestShelf(t)
	if err := b.newBoardDoc(); err != nil {
		t.Fatal(err)
	}
	b.ed.AddPage("Page 2")
	b.ed.SwitchPage(b.ed.Pages()[0].ID)

	rectID := b.ed.CreateNote(geo.V(120, 120), "Keep Selected")
	b.ed.SetTool(editor.ToolLasso)
	b.ed.Select(rectID)
	b.focusMode = true

	view := ui.NewTester(a.boardView, 900, 560)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	// 1. Board delete modal: Esc cancels without deleting board or leaking to canvas.
	boardsBefore := len(b.boards)
	b.confirmDelete = true
	view.Frame()
	view.Frame()

	view.Key(0, ui.KeyEscape)
	view.Frame()

	if b.confirmDelete {
		t.Fatal("Escape did not close board delete confirmation modal")
	}
	if len(b.boards) != boardsBefore {
		t.Fatalf("boards len = %d, want %d", len(b.boards), boardsBefore)
	}
	if got := b.ed.Selection(); len(got) != 1 || got[0] != rectID {
		t.Fatalf("Escape leaked to canvas and changed selection: %v", got)
	}
	if b.ed.Tool != editor.ToolLasso {
		t.Fatalf("Escape leaked to canvas and changed tool to %s", b.ed.Tool)
	}
	if !b.focusMode {
		t.Fatal("Escape leaked to canvas and exited focus mode")
	}

	// 2. Page delete modal: Enter does not confirm or edit canvas; Esc cancels cleanly.
	pagesBefore := len(b.ed.Pages())
	b.confirmPageDelete = true
	view.Frame()
	view.Frame()

	view.Key(0, ui.KeyEnter)
	view.Frame()
	if !b.confirmPageDelete || len(b.ed.Pages()) != pagesBefore {
		t.Fatalf("Enter unexpectedly closed or confirmed page delete modal: open=%v pages=%d", b.confirmPageDelete, len(b.ed.Pages()))
	}
	if b.ed.Editing != "" {
		t.Fatalf("Enter leaked to canvas and started editing %q", b.ed.Editing)
	}

	view.Key(0, ui.KeyEscape)
	view.Frame()
	if b.confirmPageDelete {
		t.Fatal("Escape did not close page delete confirmation modal")
	}
	if len(b.ed.Pages()) != pagesBefore {
		t.Fatalf("pages len = %d, want %d", len(b.ed.Pages()), pagesBefore)
	}
	if got := b.ed.Selection(); len(got) != 1 || got[0] != rectID {
		t.Fatalf("Escape leaked to canvas on page delete modal: sel=%v", got)
	}
	if !b.focusMode {
		t.Fatal("Escape leaked to canvas and exited focus mode on page delete modal")
	}

	// 3. With no selection and ToolDraw active, Esc on confirm modal must not reset tool to ToolSelect.
	b.ed.SelectNone()
	b.ed.SetTool(editor.ToolDraw)
	b.confirmDelete = true
	view.Frame()
	view.Frame()
	view.Key(0, ui.KeyEscape)
	view.Frame()
	if b.confirmDelete || b.ed.Tool != editor.ToolDraw {
		t.Fatalf("after Esc: confirmDelete=%v tool=%s, want false / ToolDraw", b.confirmDelete, b.ed.Tool)
	}
}

func TestBoard1187HollowCirclesScenario(t *testing.T) {
	a, b := boardTestShelf(t)
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")

	// 40 overlapping hollow circles (Fill="none") in the center of the stage.
	b.ed.ToolLocked = true
	for i := 0; i < 40; i++ {
		b.ed.SetGeoTool("ellipse")
		b.ed.Next.Stroke = editor.Swatches[i%len(editor.Swatches)]
		b.ed.Next.Fill = "none"
		x := 160.0 + float64(i%8)*12
		y := 70.0 + float64(i/8)*16
		ts := float64(i) * 0.02
		b.ed.PointerDown(x, y, 0, 1, editor.Mods{}, ts)
		b.ed.PointerMove(x+440, y+340, editor.Mods{}, ts+0.005)
		b.ed.PointerUp(x+440, y+340, editor.Mods{}, ts+0.01)
	}

	// Plus 4 prominent fountain-pen strokes beside/across the circles so calligraphic
	// taper vs flat polyline degradation is clearly visible.
	b.ed.SetTool(editor.ToolDraw)
	b.ed.Next.PenKind = "pen"
	b.ed.Next.PenWidth = 8
	var penIDs []doc.ID
	for i := 0; i < 4; i++ {
		b.ed.Next.Stroke = editor.Swatches[(i+1)%len(editor.Swatches)]
		baseY := 470.0 + float64(i)*34
		ts := 2.0 + float64(i)*0.1
		b.ed.PointerDown(120, baseY, 0, 1, editor.Mods{}, ts)
		for step := 1; step <= 14; step++ {
			f := float64(step) / 14.0
			px := 120 + f*760
			py := baseY + 18*math.Sin(f*math.Pi*3)
			b.ed.PointerMove(px, py, editor.Mods{}, ts+f*0.05)
		}
		b.ed.PointerUp(880, baseY, editor.Mods{}, ts+0.06)
		kids := b.ed.Children(b.ed.Page)
		penIDs = append(penIDs, kids[len(kids)-1])
	}
	b.ed.ToolLocked = false
	b.ed.SetTool(editor.ToolSelect)
	b.ed.SelectNone()

	view.Frame()
	view.Frame()
	var durs [5]time.Duration
	for i := range durs {
		t0 := time.Now()
		b.invalidate()
		view.Frame()
		durs[i] = time.Since(t0)
	}
	var sum time.Duration
	for _, d := range durs {
		sum += d
	}
	meanMs := float64(sum.Microseconds()) / float64(len(durs)) / 1000.0

	shotName := os.Getenv("RAKKO_HOLLOW_SHOT")
	if shotName == "" {
		shotName = "hollow-circles-after.png"
	}
	saveTestShot(t, "board1187-seg2", shotName, view.Image())

	t.Logf("HOLLOW_CIRCLES items=%d lightInk=%v degraded=%d estMaskArea=%.0f meanFrameMs=%.2f",
		b.ed.ItemCount(), b.lastLightInk, b.lastDegradedCount, b.lastMaskArea, meanMs)
	if os.Getenv("RAKKO_HOLLOW_SHOT") != "hollow-circles-before.png" {
		cam := b.ed.Cam()
		sc := &boardScene{
			b:    b,
			pal:  b.ed.Palette(),
			cam:  cam,
			view: geo.Box{X: -cam.X, Y: -cam.Y, W: 1040 / cam.Z, H: 680 / cam.Z},
		}
		sc.planMaskBudgetWithScale(b.ed.Page, nil, 1.0)
		for _, pid := range penIDs {
			if sc.degraded[pid] {
				t.Fatalf("fountain-pen stroke %s was degraded despite Tier 1 hollow circles available to shed", pid)
			}
		}
		if b.lastDegradedCount >= b.ed.ItemCount() {
			t.Fatalf("expected only Tier 1 excess circles degraded, got %d/%d", b.lastDegradedCount, b.ed.ItemCount())
		}
	}
}

func TestBoard1187SelectiveDegradationLargestFirst(t *testing.T) {
	a, b := boardTestShelf(t)
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")

	// Add 10 normal short fountain-pen strokes on the left (small ink cost).
	b.ed.ToolLocked = true
	b.ed.SetTool(editor.ToolDraw)
	b.ed.Next.PenKind = "pen"
	b.ed.Next.PenWidth = 4
	smallIDs := make([]doc.ID, 0, 10)
	for i := 0; i < 10; i++ {
		ts := float64(i) * 0.05
		y := 80.0 + float64(i)*22
		b.ed.PointerDown(40, y, 0, 1, editor.Mods{}, ts)
		b.ed.PointerMove(140, y+8, editor.Mods{}, ts+0.01)
		b.ed.PointerUp(140, y+8, editor.Mods{}, ts+0.02)
	}
	for _, id := range b.ed.Children(b.ed.Page) {
		smallIDs = append(smallIDs, id)
	}

	// Add 18 giant wide-spanning smooth multi-loop strokes on the right
	// (no sharp reversals so scribble-erase is not triggered; each ~22k px long
	// inside ~780x540 bounds -> ~425k px each, pushing total > 6M budget).
	b.ed.Next.PenWidth = 12
	var giantIDs []doc.ID
	for g := 0; g < 24; g++ {
		ts := 2.0 + float64(g)*0.2
		cx := 580.0 + float64(g%3)*8
		cy := 310.0 + float64(g/3)*4
		b.ed.PointerDown(cx+360, cy, 0, 1, editor.Mods{}, ts)
		tt := ts
		const turns = 12
		const steps = 240
		for step := 1; step <= steps; step++ {
			tt += 0.001
			f := float64(step) / float64(steps)
			ang := f * math.Pi * 2 * turns
			rx := 120.0 + 260.0*f
			ry := 80.0 + 180.0*f
			b.ed.PointerMove(cx+rx*math.Cos(ang), cy+ry*math.Sin(ang), editor.Mods{}, tt)
		}
		b.ed.PointerUp(cx+380, cy, editor.Mods{}, tt+0.001)
	}
	for _, id := range b.ed.Children(b.ed.Page) {
		isSmall := false
		for _, sid := range smallIDs {
			if id == sid {
				isSmall = true
				break
			}
		}
		if !isSmall {
			giantIDs = append(giantIDs, id)
		}
	}
	b.ed.ToolLocked = false
	b.ed.SetTool(editor.ToolSelect)
	b.ed.SelectNone()

	view.Frame()
	view.Frame()

	pal := b.ed.Palette()
	cam := b.ed.Cam()
	if !b.lastLightInk || b.lastDegradedCount == 0 || b.lastDegradedCount >= b.ed.ItemCount() {
		t.Fatalf("expected selective degradation of largest shapes only: lightInk=%v degraded=%d total=%d estArea=%.0f",
			b.lastLightInk, b.lastDegradedCount, b.ed.ItemCount(), b.lastMaskArea)
	}

	// Verify via boardScene.planMaskBudgetWithScale that small strokes stay un-degraded
	// while giant strokes are degraded.
	sc2 := &boardScene{
		b:    b,
		pal:  pal,
		cam:  cam,
		view: geo.Box{X: -cam.X, Y: -cam.Y, W: 1040 / cam.Z, H: 680 / cam.Z},
	}
	sc2.planMaskBudgetWithScale(b.ed.Page, nil, 1.0)
	for _, sid := range smallIDs {
		if sc2.degraded[sid] {
			t.Fatalf("small stroke %s was degraded despite larger shapes existing", sid)
		}
	}
	degradedGiants := 0
	for _, gid := range giantIDs {
		if sc2.degraded[gid] {
			degradedGiants++
		}
	}
	if degradedGiants == 0 || degradedGiants >= len(giantIDs)+len(smallIDs) {
		t.Fatalf("expected only giant strokes degraded, got %d/%d", degradedGiants, len(giantIDs))
	}
	t.Logf("SELECTIVE_DEGRADE small=%d (0 degraded) giant=%d (%d degraded) estMaskArea=%.0f budget=%.0f",
		len(smallIDs), len(giantIDs), degradedGiants, b.lastMaskArea, boardAtlasBudget)
}

func TestBoardDeleteImageUndoSurvivesSaveAndExportOmitsIt(t *testing.T) {
	_, b := boardTestShelf(t)
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 120, G: 200, B: 80, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	asset, err := b.store.SaveAsset(buf.Bytes(), "png", "clip.png", "image/png", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	b.ed.Doc.Assets[asset.ID] = asset
	imgID := doc.NewRecordID("shape")
	s := doc.NewShape(imgID, doc.TypeImage)
	s.Parent, s.AssetID = b.ed.Page, asset.ID
	s.W, s.H = 64, 64
	s.Index = doc.FirstIndex
	b.ed.Doc.Shapes[imgID] = s

	b.ed.Select(imgID)
	b.ed.DeleteSelected()
	b.dirtyFlag = true
	if err := b.flush(); err != nil {
		t.Fatal(err)
	}

	rawPkg, err := doc.ExportPackage(b.ed.Doc, b.store)
	if err != nil {
		t.Fatal(err)
	}
	var pkg doc.BoardPackage
	if err := json.Unmarshal(rawPkg, &pkg); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Assets) != 0 || len(pkg.Drawing.Assets) != 0 {
		t.Fatalf("ExportPackage included deleted image asset: pkg=%d doc=%d", len(pkg.Assets), len(pkg.Drawing.Assets))
	}

	b.ed.Undo()
	if b.ed.Doc.Shapes[imgID] == nil || b.ed.Doc.Assets[asset.ID] == nil {
		t.Fatalf("in-session Undo failed to keep deleted image usable: shape=%v asset=%v",
			b.ed.Doc.Shapes[imgID] != nil, b.ed.Doc.Assets[asset.ID] != nil)
	}

	b.ed.Select(imgID)
	b.ed.DeleteSelected()
	b.dirtyFlag = true
	if err := b.flush(); err != nil {
		t.Fatal(err)
	}
	reopened, err := b.store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Assets) != 0 {
		t.Fatalf("reopened board retained unreferenced assets after save: %+v", reopened.Assets)
	}
}

func TestBoardExportUsesAtomicWriteOnFailure(t *testing.T) {
	_, b := boardTestShelf(t)
	s := doc.NewShape(doc.NewRecordID("shape"), doc.TypeGeo)
	s.Parent, s.W, s.H = b.ed.Page, 80, 60
	s.Index = doc.FirstIndex
	b.ed.Doc.Shapes[s.ID] = s

	orig := boardSaveFileDialog
	t.Cleanup(func() { boardSaveFileDialog = orig })

	// First verify normal overwrite succeeds for PNG, SVG, and .rakkoboard.json with no temp leftovers.
	okDir := t.TempDir()
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"out.png", func() { b.exportPNGFile(nil) }},
		{"out.svg", func() { b.exportSVGFile(nil) }},
		{"out.rakkoboard.json", func() { b.exportBoardFile() }},
	} {
		target := filepath.Join(okDir, tc.name)
		if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		boardSaveFileDialog = func(*mygo.Window, string, string, []string) (string, error) { return target, nil }
		tc.run()
		got, err := os.ReadFile(target)
		if err != nil || string(got) == "old" || len(got) == 0 {
			t.Fatalf("export %s did not replace target: len=%d err=%v", tc.name, len(got), err)
		}
	}
	ents, err := os.ReadDir(okDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 3 {
		t.Fatalf("expected only 3 export files without temp leftovers, got %d", len(ents))
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	// On Unix non-root, making parent dir read-only forces temp-file creation in
	// dir to fail while bare os.WriteFile would overwrite the existing 0o644 file.
	roDir := t.TempDir()
	targets := []struct {
		name string
		run  func()
	}{
		{"keep.png", func() { b.exportPNGFile(nil) }},
		{"keep.svg", func() { b.exportSVGFile(nil) }},
		{"keep.rakkoboard.json", func() { b.exportBoardFile() }},
	}
	for _, tc := range targets {
		if err := os.WriteFile(filepath.Join(roDir, tc.name), []byte("keep-me"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	for _, tc := range targets {
		target := filepath.Join(roDir, tc.name)
		boardSaveFileDialog = func(*mygo.Window, string, string, []string) (string, error) { return target, nil }
		tc.run()
		got, err := os.ReadFile(target)
		if err != nil || string(got) != "keep-me" {
			t.Fatalf("export %s modified existing file in 0o555 dir instead of failing atomically: got=%q err=%v", tc.name, got, err)
		}
	}
}
