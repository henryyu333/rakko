package main

// The screenshot markup refinements: badge colour inversion and tiers, the
// step number surviving undo and redo, and the selection's W × H pill
// placement. The shots under RAKKO_SHOTS show the badges on light and dark
// picture areas, the pill, the round handles and the floating toolbar.

import (
	"image"
	"os"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestShotNumberBadgeStyle(t *testing.T) {
	for _, tc := range []struct {
		width        int
		radius, size float32
	}{
		{2, 10, 11.5},
		{4, 12, 13},
		{7, 15, 15.5},
	} {
		if got := shotNumberRadiusFor(tc.width); got != tc.radius {
			t.Errorf("width %d radius = %v, want %v", tc.width, got, tc.radius)
		}
		if got := shotNumberTypeFor(tc.width); got != tc.size {
			t.Errorf("width %d type = %v, want %v", tc.width, got, tc.size)
		}
	}
	dark := ui.Hex("#111214")
	light := ui.Hex("#FFFFFF")
	for _, tc := range []struct {
		name  string
		color ui.Color
		want  ui.Color
	}{
		{"white", ui.Hex("#FFFFFF"), dark},
		{"yellow", ui.Hex("#FFD60A"), dark},
		{"red", ui.Hex("#FF453A"), light},
		{"green", ui.Hex("#30D158"), light},
		{"blue", ui.Hex("#0A84FF"), light},
	} {
		if got := shotNumberInk(tc.color); got != tc.want {
			t.Errorf("%s badge ink = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestShotNextNumber(t *testing.T) {
	if got := shotNextNumber(nil); got != 1 {
		t.Fatalf("empty history next = %d, want 1", got)
	}
	annots := []shotAnnotation{
		{tool: shotToolRect},
		{tool: shotToolNumber, number: 1},
		{tool: shotToolNumber, number: 2},
		{tool: shotToolText},
	}
	if got := shotNextNumber(annots); got != 3 {
		t.Fatalf("next = %d, want 3", got)
	}
	annots = append(annots, shotAnnotation{tool: shotToolNumber, number: 10})
	if got := shotNextNumber(annots); got != 11 {
		t.Fatalf("after a two-digit badge next = %d, want 11", got)
	}
}

// After undoing the last badge the next one continues the sequence; a redo
// moves the counter on again.
func TestShotUndoRedoResumesNumbering(t *testing.T) {
	s := &shotSession{nextNum: 1}
	add := func(n int) {
		s.hist.snap()
		s.hist.push(shotAnnotation{tool: shotToolNumber, number: n})
		s.nextNum++
	}
	add(1)
	add(2)
	add(3)
	if !s.undoMark() {
		t.Fatal("undo of a placed badge reported nothing")
	}
	if s.nextNum != 3 {
		t.Fatalf("after undo next = %d, want 3", s.nextNum)
	}
	if !s.undoMark() || s.nextNum != 2 {
		t.Fatalf("second undo next = %d, want 2", s.nextNum)
	}
	if !s.redoMark() || s.nextNum != 3 {
		t.Fatalf("redo next = %d, want 3", s.nextNum)
	}
	if !s.redoMark() || s.nextNum != 4 {
		t.Fatalf("second redo next = %d, want 4", s.nextNum)
	}
}

func TestShotSizeBadgeRect(t *testing.T) {
	display := mygo.Rectangle{Width: 1200, Height: 800}
	sel := mygo.Rectangle{X: 100, Y: 100, Width: 300, Height: 200}
	const w, h = 66, 22

	above := shotSizeBadgeRect(sel, display, nil, w, h)
	if above.X != sel.X || above.Y != sel.Y-shotSizeBadgeGap-h {
		t.Fatalf("badge above = %+v, want the selection's top-left above it", above)
	}

	// A selection at the display's top edge puts the pill inside.
	top := shotSizeBadgeRect(mygo.Rectangle{X: 100, Y: 0, Width: 300, Height: 200}, display, nil, w, h)
	if top.Y != shotSizeBadgeGap {
		t.Fatalf("badge bogus on the top edge: %+v", top)
	}

	// The toolbar wins: the pill steps inside rather than covering it.
	bar := mygo.Rectangle{X: 100, Y: sel.Y - shotSizeBadgeGap - h, Width: 120, Height: 44}
	avoid := shotSizeBadgeRect(sel, display, []mygo.Rectangle{bar}, w, h)
	if shotRectsIntersect(avoid, bar) {
		t.Fatalf("badge overlaps the toolbar: %+v", avoid)
	}
	if avoid.Y < sel.Y+shotSizeBadgeGap {
		t.Fatalf("badge did not step inside the selection: %+v", avoid)
	}

	// A selection at the right edge keeps the pill on the display.
	right := shotSizeBadgeRect(mygo.Rectangle{X: 1180, Y: 100, Width: 20, Height: 200}, display, nil, w, h)
	if right.X+w > display.Width {
		t.Fatalf("badge left the display: %+v", right)
	}

	// A secondary display with a non-zero origin: placement is global, and
	// only the painted rectangle is converted to the display's local points.
	secondary := mygo.Rectangle{X: 1920, Y: -200, Width: 1200, Height: 800}
	secSel := mygo.Rectangle{X: 2000, Y: -150, Width: 300, Height: 200}
	got := shotSizeBadgeRect(secSel, secondary, nil, w, h)
	if got.X != secSel.X || got.Y != secSel.Y-shotSizeBadgeGap-h {
		t.Fatalf("badge on a secondary display = %+v, want above its selection", got)
	}
	local := shotLocalRect(got, shotTarget{Bounds: secondary, Scale: 2})
	if local.X < 0 || local.Y < 0 || local.X+local.Width > secondary.Width || local.Y+local.Height > secondary.Height {
		t.Fatalf("badge painted outside the secondary display: %+v", local)
	}
	if want := secSel.X - secondary.X; local.X != want {
		t.Fatalf("badge local x = %d, want %d", local.X, want)
	}
	// The toolbar bars stay global too: a bar under the above slot moves the
	// pill inside the selection, still within the display.
	secBar := mygo.Rectangle{X: got.X, Y: got.Y, Width: 120, Height: 44}
	avoidSec := shotSizeBadgeRect(secSel, secondary, []mygo.Rectangle{secBar}, w, h)
	if shotRectsIntersect(avoidSec, secBar) {
		t.Fatalf("secondary badge overlaps the toolbar: %+v", avoidSec)
	}
	local = shotLocalRect(avoidSec, shotTarget{Bounds: secondary, Scale: 2})
	if local.X < 0 || local.Y < 0 || local.X+local.Width > secondary.Width || local.Y+local.Height > secondary.Height {
		t.Fatalf("secondary badge left the display after avoiding the toolbar: %+v", local)
	}
}

func TestPolish1189ScreenshotShots(t *testing.T) {
	if os.Getenv("RAKKO_SHOTS") == "" {
		t.Skip("set RAKKO_SHOTS to write the screenshot-markup shot")
	}
	const w, h = 900, 520
	img := image.NewRGBA(image.Rect(0, 0, w*2, h*2))
	for y := range h * 2 {
		for x := range w * 2 {
			i := y*img.Stride + x*4
			if y < h {
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 245, 246, 248, 0xFF
			} else {
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 28, 29, 33, 0xFF
			}
		}
	}
	// A couple of card blocks so the badges sit on something that reads as
	// a picture rather than a flat test pattern.
	for _, r := range []image.Rectangle{
		image.Rect(40, 30, 300, 120), image.Rect(540, 60, 860, 170),
		image.Rect(60, 320, 280, 470), image.Rect(520, 300, 860, 460),
	} {
		for y := r.Min.Y * 2; y < r.Max.Y*2; y++ {
			for x := r.Min.X * 2; x < r.Max.X*2; x++ {
				i := y*img.Stride + x*4
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 224, 226, 230, 0xFF
			}
		}
	}
	d := &shotDisplay{
		shotTarget: shotTarget{ID: 1, Bounds: mygo.Rectangle{Width: w, Height: h}, Scale: 2},
		img:        img, pixW: w * 2, pixH: h * 2, bitmap: ui.NewBitmap(img),
	}
	s := &shotSession{
		a: &shelf{}, displays: []*shotDisplay{d}, owner: 0,
		color: shotAnnotationColors[0], width: shotAnnotationWidths[1], nextNum: 1,
	}
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 30, Y: 30, Width: 840, Height: 460}
	s.tool = shotToolNumber
	s.layoutBar()
	s.hist.annots = []shotAnnotation{
		{tool: shotToolNumber, color: shotAnnotationColors[0], width: 2, a: mygo.Point{X: 140, Y: 80}, number: 1},
		{tool: shotToolNumber, color: shotAnnotationColors[1], width: 4, a: mygo.Point{X: 210, Y: 300}, number: 2},
		{tool: shotToolNumber, color: shotAnnotationColors[4], width: 4, a: mygo.Point{X: 420, Y: 110}, number: 3},
		{tool: shotToolNumber, color: shotAnnotationColors[3], width: 7, a: mygo.Point{X: 340, Y: 420}, number: 10},
	}
	out := ui.Render(func(c *ui.Context) { s.overlayView(c, 0) }, w, h, 2)
	saveTestShot(t, "polish1189", "screenshot-annotations.png", out)
}
