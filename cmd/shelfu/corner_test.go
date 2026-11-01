//go:build darwin

package main

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestShellCornerHasNoDarkFringe: the black backdrop must stop inside the
// glass pane's bottom corners. When it reached the pane's edge, the pane's
// antialiased rim covered it only partly, and the outermost pixel — black
// against the desktop — read as a second, misaligned edge behind the lit
// rim. Both bottom corners of both levels must be lit, not black.
func TestShellCornerHasNoDarkFringe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		level  int
		shell  float32
		corner int
	}{
		{"search bar", 1, homeShellH, homeRadius},
		{"page", 2, sheetH, sheetRadius},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := levelShelf(t)
			a.visible, a.level = true, tc.level
			a.progress, a.animationFrom = 1, 1
			a.animationStart = time.Time{}
			f := a.desiredFrame()
			view := ui.NewTester(a.view, f.Width, f.Height)
			view.SetScale(2)
			view.Frame()
			img := view.Image()

			// Window pixels at scale 2: a corner of R = 2·corner px has its
			// 45° point R/√2 left of and below its centre.
			top := int(a.shellTopLocal(tc.shell, 1) * 2)
			shellW := homeShellW
			if tc.level == 2 {
				shellW = sheetW
			}
			left := (f.Width - shellW) / 2 * 2
			bottom := top + int(tc.shell)*2
			r := float64(tc.corner) * 2
			off := int(math.Round(r / math.Sqrt2))
			for _, cx := range []int{left + int(r), left + shellW*2 - int(r)} {
				cy := bottom - int(r)
				px, py := cx-off, cy+off
				for y := py - 3; y <= py+3; y++ {
					for x := px - 3; x <= px+3; x++ {
						red, green, blue, alpha := img.At(x, y).RGBA()
						if alpha>>8 > 200 && red>>8 < 20 && green>>8 < 20 && blue>>8 < 20 {
							t.Fatalf("dark fringe at the (%d,%d) corner, pixel (%d,%d): %v",
								cx, cy, x, y, img.At(x, y))
						}
					}
				}
			}
		})
	}
}
