//go:build darwin

package main

import (
	"image"
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestShellBottomEdgeShowsNoRim: the glass material lights a hairline on
// the pane's own edge, and on a dark desktop that hairline read as a
// second, bright line along the shell's bottom edge, over a second
// contour at its corners. The glass now stops at the shell's own edge, so
// the outermost pixel of the bottom edge and of both bottom corners is
// never brighter than the body behind it.
func TestShellBottomEdgeShowsNoRim(t *testing.T) {
	lum := func(c color.Color) int {
		r, g, b, _ := c.RGBA()
		return int((299*(r>>8) + 587*(g>>8) + 114*(b>>8)) / 1000)
	}
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
			opaque := func(x, y int) bool {
				if !image.Pt(x, y).In(img.Bounds()) {
					return false
				}
				_, _, _, alpha := img.At(x, y).RGBA()
				return alpha>>8 > 200
			}
			shellW := homeShellW
			if tc.level == 2 {
				shellW = sheetW
			}
			top := int(a.shellTopLocal(tc.shell, 1) * 2)
			left := (f.Width - shellW) / 2 * 2
			bottom := top + int(tc.shell)*2

			// The straight bottom edge: the outermost painted pixel of a
			// column, against the body four pixels inside it.
			for x := left + 32; x <= left+shellW*2-32; x += 24 {
				y := bottom + 2
				for y > top && !opaque(x, y) {
					y--
				}
				if y <= top {
					continue
				}
				if edge, body := lum(img.At(x, y)), lum(img.At(x, y-4)); edge > body+10 {
					t.Fatalf("a bright rim at the bottom edge, column %d: edge %d over body %d", x, edge, body)
				}
			}
			// Both bottom corners: the same walk along each corner's 45° ray.
			r := float64(tc.corner) * 2
			for _, corner := range []struct {
				cx, sx int
				cy     int
			}{
				{left + int(r), -1, bottom - int(r)},
				{left + shellW*2 - int(r), 1, bottom - int(r)},
			} {
				at := func(t float64) (int, int) {
					dx := float64(corner.sx) * t / math.Sqrt2
					return corner.cx + int(math.Round(dx)), corner.cy + int(math.Round(t/math.Sqrt2))
				}
				t0 := r + 8
				for ; t0 > r-8; t0-- {
					if x, y := at(t0); opaque(x, y) {
						break
					}
				}
				ex, ey := at(t0)
				bx, by := at(t0 - 4)
				if edge, body := lum(img.At(ex, ey)), lum(img.At(bx, by)); edge > body+10 {
					t.Fatalf("a bright rim at the (%d,%d) corner: edge %d over body %d",
						corner.cx, corner.cy, edge, body)
				}
			}
		})
	}
}
