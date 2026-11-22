package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
)

func TestUpdateAutoInstallCheckRenderDarkAndLight(t *testing.T) {
	shotDir := os.Getenv("RAKKO_UPDATE_SHOT_DIR")
	cases := []struct {
		name string
		dark bool
	}{
		{"dark", true},
		{"light", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &shelf{
				settings:       settings.Settings{Language: "zh-Hans", UpdateAutoInstall: true},
				locale:         "zh-Hans",
				updateWinStage: updateStageFound,
				update: &mygo.Update{
					Version: "1.18.4",
					Notes:   "### 新增\n- 支持自定义活跃显示器与顶边阻挡。\n- 修复若干界面细节。",
				},
			}
			w, h := a.updateWinSize(updateStageFound)
			tester := ui.NewTester(a.updateView, w, h)
			tester.SetLocale("zh-Hans")
			tester.SetScale(2)
			tester.SetDark(tc.dark)
			tester.Frame()
			tester.Frame()

			img := tester.Image()
			if img == nil {
				t.Fatal("rendered image is nil")
			}

			// Verify white checkmark pixels span the 16x16 blue box cleanly
			// in both dark and light appearances (not cropped to top-left corner).
			box, ok := tester.Find(a.appLoc().UpdateAutoInstallRow())
			if !ok {
				t.Fatal("auto-install checkbox element not found")
			}
			whiteCount := 0
			minX, maxX := img.Bounds().Dx(), 0
			minY, maxY := img.Bounds().Dy(), 0
			x0 := max(0, int(box.X*2))
			y0 := max(0, int(box.Y*2))
			x1 := min(img.Bounds().Dx(), int((box.X+20)*2))
			y1 := min(img.Bounds().Dy(), int((box.Y+box.H)*2))
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					c := img.RGBAAt(x, y)
					if c.R > 240 && c.G > 240 && c.B > 240 {
						whiteCount++
						if x < minX {
							minX = x
						}
						if x > maxX {
							maxX = x
						}
						if y < minY {
							minY = y
						}
						if y > maxY {
							maxY = y
						}
					}
				}
			}
			if whiteCount < 40 || (maxX-minX) < 12 || (maxY-minY) < 10 {
				t.Fatalf("%s white checkmark pixels=%d span=(%d x %d); want full 16x16 checkmark", tc.name, whiteCount, maxX-minX, maxY-minY)
			}

			if shotDir != "" {
				if err := os.MkdirAll(shotDir, 0o755); err != nil {
					t.Fatal(err)
				}
				out := filepath.Join(shotDir, "update-check-"+tc.name+".png")
				f, err := os.Create(out)
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, img); err != nil {
					_ = f.Close()
					t.Fatal(err)
				}
				_ = f.Close()
			}
		})
	}
}
