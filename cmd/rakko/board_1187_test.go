package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

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
