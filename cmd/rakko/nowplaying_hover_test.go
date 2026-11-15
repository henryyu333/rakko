package main

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/platform"
	"rakko/internal/settings"
)

func hoverTestShelf(t *testing.T, tab pageID, playingH int) (*shelf, *ui.Tester) {
	t.Helper()
	st := platform.NowPlayingState{
		Title:    "夜曲",
		Artist:   "周杰伦",
		Album:    "十一月的萧邦",
		Duration: 227,
		Elapsed:  56,
		Rate:     1,
		Playing:  true,
		Received: time.Now(),
	}
	a := &shelf{
		dataDir:      t.TempDir(),
		settings:     settings.Default(),
		level:        2,
		visible:      true,
		dispBounds:   mygo.Rectangle{Width: 1470, Height: 956},
		dispWork:     mygo.Rectangle{Width: 1470, Height: 956},
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
	a.np = &nowPlayingModule{host: a, have: true, last: st}
	if playingH > 0 {
		a.mutateTools(func(l *layout.Layout) {
			l.Cards = []layout.Card{{ID: "playing", Span: 2, Rows: playingH}}
		})
	}
	a.setTab(tab)
	a.ensurePages()
	w := int(a.pageShellW()) + 2*earR + 2
	h := int(a.pageShellH()) + navGap + navH + 4
	return a, ui.NewTester(a.view, w, h)
}

func circleLumSum(img *image.RGBA, rect ui.Rect) int64 {
	cx := float64(rect.X) + float64(rect.W)/2
	cy := float64(rect.Y) + float64(rect.H)/2
	r := float64(rect.W)/2 - 1.5
	if h := float64(rect.H)/2 - 1.5; h < r {
		r = h
	}
	r2 := r * r
	var sum int64
	b := img.Bounds()
	x0, x1 := int(rect.X), int(rect.X+rect.W)+1
	y0, y1 := int(rect.Y), int(rect.Y+rect.H)+1
	if x0 < b.Min.X {
		x0 = b.Min.X
	}
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y0 < b.Min.Y {
		y0 = b.Min.Y
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			if dx*dx+dy*dy <= r2 {
				c := img.RGBAAt(x, y)
				sum += int64(c.R) + int64(c.G) + int64(c.B)
			}
		}
	}
	return sum
}

func assertControlHoverAndPress(t *testing.T, view *ui.Tester, name string, labels []string) {
	t.Helper()
	view.Frame()
	view.Frame()

	for _, label := range labels {
		rect, ok := view.Find(label)
		if !ok {
			t.Fatalf("%s: button %q not found", name, label)
		}
		cx, cy := rect.X+rect.W/2, rect.Y+rect.H/2

		view.Move(1, 1)
		view.Frame()
		idleImg := view.Image()
		idle := circleLumSum(idleImg, rect)

		view.Move(cx, cy)
		view.Frame()
		hoveredImg := view.Image()
		hovered := circleLumSum(hoveredImg, rect)
		if hovered <= idle {
			t.Fatalf("%s %q: hover lum %d did not brighten over idle %d (rect=%+v)", name, label, hovered, idle, rect)
		}

		// Corner at 45deg inside circle perimeter must also brighten (circular fill around glyph, not just center).
		rx := int(rect.X + rect.W/2 + (rect.W/2-2.5)*0.7)
		ry := int(rect.Y + rect.H/2 - (rect.H/2-2.5)*0.7)
		if c := hoveredImg.RGBAAt(rx, ry); c.R == 0 && c.G == 0 && c.B == 0 {
			t.Fatalf("%s %q: corner inside hover circle (%d,%d) remained black %+v", name, label, rx, ry, c)
		}
		if rect.W < 23 {
			// Verify the out-of-layout 23x23 hover halo extends cleanly outside the 15x15 button bounds.
			hx, hy := int(cx+9), int(cy)
			hc, ic := hoveredImg.RGBAAt(hx, hy), idleImg.RGBAAt(hx, hy)
			if int(hc.R)+int(hc.G)+int(hc.B) <= int(ic.R)+int(ic.G)+int(ic.B) {
				t.Fatalf("%s %q: outer hover halo (%d,%d) rgb=%+v did not brighten over idle %+v", name, label, hx, hy, hc, ic)
			}
		}

		view.Press(cx, cy)
		view.Frame()
		pressed := circleLumSum(view.Image(), rect)
		if pressed <= hovered {
			t.Fatalf("%s %q: press lum %d did not brighten over hovered %d", name, label, pressed, hovered)
		}

		view.Release(cx, cy)
		view.Move(1, 1)
		view.Frame()
		after := circleLumSum(view.Image(), rect)
		if after >= hovered {
			t.Fatalf("%s %q: release/leave lum %d stayed >= hovered %d", name, label, after, hovered)
		}
	}
}

func TestControlButtonHoverAndPress(t *testing.T) {
	l := loc(nil)
	labels := []string{l.PreviousTrack(), playPauseLabel(true), l.NextTrack()}

	_, homeView := hoverTestShelf(t, pageHome, 0)
	assertControlHoverAndPress(t, homeView, "home", labels)

	_, tools2x2 := hoverTestShelf(t, pageTools, 2)
	assertControlHoverAndPress(t, tools2x2, "tools-2x2", labels)

	_, tools2x1 := hoverTestShelf(t, pageTools, 1)
	assertControlHoverAndPress(t, tools2x1, "tools-2x1", labels)
}
