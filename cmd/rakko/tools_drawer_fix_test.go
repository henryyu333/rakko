package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
)

func makeToolsEditDrawerTester(t *testing.T, island bool, scale float32) (*shelf, *toolsPage, *ui.Tester) {
	t.Helper()
	a, _, _ := makeTestSysMonShelf(t, "zh-Hans", []layout.Card{{ID: "ocr"}, {ID: "zip"}})
	a.hasNotch = !island
	a.applyShellStyle()
	a.visible = true
	a.level = 2
	a.progress = 1
	a.animationFrom = 1
	a.animationStart = time.Time{}
	a.contentFrom = 1
	a.tab = pageTools
	p := a.toolsPage()
	p.editing = true
	f := a.desiredFrame()
	view := ui.NewTester(a.view, f.Width, f.Height)
	if scale > 0 {
		view.SetScale(scale)
	}
	view.Frame()
	view.Frame()
	return a, p, view
}

func stepDrawerFrames(view *ui.Tester, total time.Duration) {
	const step = 16 * time.Millisecond
	for elapsed := time.Duration(0); elapsed < total; elapsed += step {
		view.Advance(step)
		view.Frame()
	}
	view.Frame()
}

func TestToolsLibraryWheelScrollsHorizontally(t *testing.T) {
	_, p, view := makeToolsEditDrawerTester(t, true, 1)
	if p.libScroll.MaxX <= 0 {
		t.Fatalf("library drawer did not overflow horizontally: %+v", p.libScroll)
	}
	cx := p.libraryRect.X + p.libraryRect.W/2
	cy := p.libraryRect.Y + p.libraryRect.H/2
	view.Move(cx, cy)
	view.Wheel(cx, cy, 0, 120)
	stepDrawerFrames(view, 240*time.Millisecond)
	first := p.libScroll.X
	if first <= 50 {
		t.Fatalf("vertical mouse wheel did not scroll library horizontally: X=%.1f MaxX=%.1f", first, p.libScroll.MaxX)
	}
	if p.scroll.Y != 0 {
		t.Fatalf("wheel over drawer unexpectedly scrolled grid: Y=%.1f", p.scroll.Y)
	}

	view.Wheel(cx, cy, 0, 120)
	stepDrawerFrames(view, 240*time.Millisecond)
	second := p.libScroll.X
	if second <= first && second < p.libScroll.MaxX {
		t.Fatalf("second wheel notch did not advance library scroll: first=%.1f second=%.1f max=%.1f", first, second, p.libScroll.MaxX)
	}

	view.Wheel(cx, cy, 0, -400)
	stepDrawerFrames(view, 280*time.Millisecond)
	if p.libScroll.X != 0 {
		t.Fatalf("reverse wheel did not return library scroll to 0: X=%.1f", p.libScroll.X)
	}
}

func TestToolsLibraryScrollbarClearsTiles(t *testing.T) {
	_, p, view := makeToolsEditDrawerTester(t, true, 1)
	tile, ok := view.Find("截图")
	if !ok {
		t.Fatalf("library tile missing; texts=%v", view.Texts())
	}
	view.Move(tile.X+tile.W/2, tile.Y+tile.H/2)
	view.Frame()
	if tile.Y < p.libraryRect.Y+6 {
		t.Fatalf("library tile touches drawer top edge: tile.Y=%.1f drawer.Y=%.1f", tile.Y, p.libraryRect.Y)
	}
	if tile.Y+tile.H > p.libraryRect.Y+65 {
		t.Fatalf("library tile bottom overlaps scrollbar gutter: tileBottom=%.1f drawerTop=%.1f", tile.Y+tile.H, p.libraryRect.Y)
	}
}

func TestToolsEditDrawerBottomCornersHaveNoGhost(t *testing.T) {
	shotDir := os.Getenv("RAKKO_DRAWER_SHOT_DIR")
	for _, tc := range []struct {
		name   string
		island bool
		shot   string
	}{
		{name: "notch", island: false, shot: "tools-edit-notch.png"},
		{name: "island", island: true, shot: "tools-edit-island.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const scale = 2
			_, p, view := makeToolsEditDrawerTester(t, tc.island, scale)
			cx := p.libraryRect.X + p.libraryRect.W/2
			cy := p.libraryRect.Y + p.libraryRect.H/2
			view.Move(cx, cy)
			view.Frame()
			img := view.Image()

			left := int(math.Round(float64(p.libraryRect.X) * scale))
			right := int(math.Round(float64(p.libraryRect.X+p.libraryRect.W) * scale))
			bottom := int(math.Round(float64(p.libraryRect.Y+p.libraryRect.H) * scale))

			for _, pt := range []struct {
				label string
				x, y  int
			}{
				{"bottom-left-1", left + 1, bottom - 2},
				{"bottom-left-2", left + 2, bottom - 2},
				{"bottom-left-3", left + 3, bottom - 4},
				{"bottom-right-1", right - 2, bottom - 2},
				{"bottom-right-2", right - 3, bottom - 2},
				{"bottom-right-3", right - 4, bottom - 4},
			} {
				if !image.Pt(pt.x, pt.y).In(img.Bounds()) {
					t.Fatalf("%s out of bounds %v: img=%v", pt.label, pt, img.Bounds())
				}
				_, _, _, a := img.At(pt.x, pt.y).RGBA()
				if a != 0 {
					t.Fatalf("%s outer corner painted ghost pixel (alpha=%d) at (%d,%d); drawer=%+v",
						pt.label, a>>8, pt.x, pt.y, p.libraryRect)
				}
			}

			if shotDir != "" {
				if err := os.MkdirAll(shotDir, 0o755); err != nil {
					t.Fatal(err)
				}
				bg := image.NewRGBA(img.Bounds())
				backdrop := color.NRGBA{R: 0xDC, G: 0xE3, B: 0xEE, A: 0xFF}
				draw.Draw(bg, bg.Bounds(), &image.Uniform{C: backdrop}, image.Point{}, draw.Src)
				draw.Draw(bg, bg.Bounds(), img, img.Bounds().Min, draw.Over)
				var buf bytes.Buffer
				if err := png.Encode(&buf, bg); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(shotDir, tc.shot), buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
