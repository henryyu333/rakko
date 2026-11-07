//go:build darwin

package main

// The screenshot session's headless tests: geometry, the annotation
// history, the output crop and the entry/permission path. Nothing here
// opens a window, captures a display, writes the pasteboard or touches the
// user's Desktop.

import (
	"bytes"
	"image"
	"image/color"
	stdpng "image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
)

// shotSeams replaces every screenshot seam and restores them after the
// test. The default stubs run work inline and do nothing native.
func shotSeams(t *testing.T) {
	t.Helper()
	displays, cursor, capture, windows := shotDisplays, shotCursorPoint, shotCaptureDisplay, shotNormalWindow
	dispatch, work, clock := shotDispatch, shotWork, shotClock
	front, activate, preflight := shotFrontPID, shotActivateSelf, shotPreflight
	clipboard, readDir, writeFile, openWin := shotClipboard, shotReadSaveDir, shotWriteFile, shotOpenWindow
	keyWindow := shotKeyWindow
	clean := shotCleanTemp
	settle := shotPanelSettle
	after := shotAfter
	openPin := shotOpenPin
	t.Cleanup(func() {
		shotDisplays, shotCursorPoint, shotCaptureDisplay, shotNormalWindow = displays, cursor, capture, windows
		shotDispatch, shotWork, shotClock = dispatch, work, clock
		shotFrontPID, shotActivateSelf, shotPreflight = front, activate, preflight
		shotClipboard, shotReadSaveDir, shotWriteFile, shotOpenWindow = clipboard, readDir, writeFile, openWin
		shotKeyWindow = keyWindow
		shotCleanTemp = clean
		shotPanelSettle = settle
		shotAfter, shotOpenPin = after, openPin
	})
	shotDispatch = func(fn func()) { fn() }
	shotWork = func(fn func()) { fn() }
	shotDisplays = func() []mygo.Display {
		return []mygo.Display{{ID: 1, Bounds: mygo.Rectangle{Width: 1000, Height: 800},
			WorkArea: mygo.Rectangle{Width: 1000, Height: 800}, ScaleFactor: 1, Primary: true}}
	}
	shotFrontPID = func() int { return 0 }
	shotActivateSelf = func() {}
	shotKeyWindow = func(uintptr) bool { return true }
	shotNormalWindow = func() []mac.ScreenWindow { return nil }
	shotOpenWindow = func(*shotSession, int) {}
	shotCleanTemp = func() { os.MkdirAll(shotTempDir(), 0o700) }
	shotPanelSettle = 0
	shotAfter = func(time.Duration, func()) *time.Timer { return time.NewTimer(time.Hour) }
	shotOpenPin = func(*shelf, []byte, mygo.Rectangle, float64) {}
}

// writeShotTestPNG writes a w×h opaque PNG for the capture stub.
func writeShotTestPNG(t *testing.T, path string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	if err := stdpng.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// shotTestSession builds a session over one synthetic display.
func shotTestSession(t *testing.T, bounds mygo.Rectangle, pixW, pixH int) *shotSession {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, pixW, pixH))
	for y := 0; y < pixH; y++ {
		for x := 0; x < pixW; x++ {
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 40, 60, 80, 0xFF
		}
	}
	d := &shotDisplay{
		shotTarget: shotTarget{ID: 1, Bounds: bounds, Scale: 1},
		img:        img, pixW: pixW, pixH: pixH,
		bitmap: ui.NewBitmap(img),
	}
	return &shotSession{a: &shelf{}, displays: []*shotDisplay{d}, owner: 0, color: shotAnnotationColors[0],
		width: shotAnnotationWidths[1], nextNum: 1}
}

func TestShotCropRectUsesDisplayLocalPixels(t *testing.T) {
	primary := shotTarget{ID: 1, Bounds: mygo.Rectangle{Width: 1440, Height: 900}, Scale: 2}
	sel := mygo.Rectangle{X: 100, Y: 50, Width: 200, Height: 100}
	if got, want := shotCropRect(sel, primary, 2880, 1800), image.Rect(200, 100, 600, 300); got != want {
		t.Fatalf("primary crop = %v, want %v", got, want)
	}
	secondary := shotTarget{ID: 2, Bounds: mygo.Rectangle{X: -1920, Y: -200, Width: 1920, Height: 1080}, Scale: 1}
	sel = mygo.Rectangle{X: -1820, Y: -150, Width: 100, Height: 50}
	if got, want := shotCropRect(sel, secondary, 1920, 1080), image.Rect(100, 50, 200, 100); got != want {
		t.Fatalf("secondary crop = %v, want %v", got, want)
	}
}

func TestShotToolbarStaysOnDisplay(t *testing.T) {
	display := mygo.Rectangle{X: 0, Y: 0, Width: 1200, Height: 800}
	sel := mygo.Rectangle{X: 500, Y: 300, Width: 300, Height: 200}
	tb := shotToolbarLayout(sel, display, shotToolNone)
	if tb.rect.Y != sel.Y+sel.Height+8 {
		t.Fatalf("toolbar did not sit below the selection: %+v", tb.rect)
	}
	if tb.rect.X+tb.rect.Width != sel.X+sel.Width {
		t.Fatalf("toolbar is not right-aligned: %+v", tb.rect)
	}
	if !shotPointInRect(mygo.Point{X: tb.rect.X + tb.rect.Width/2, Y: tb.rect.Y + tb.rect.Height/2}, display) {
		t.Fatal("toolbar left the display")
	}

	// Below and above both fail for a full-screen selection: it goes
	// inside the selection's bottom edge.
	low := mygo.Rectangle{X: 0, Y: 0, Width: 1200, Height: 800}
	tb = shotToolbarLayout(low, display, shotToolNone)
	if tb.rect.Y < low.Y || tb.rect.Y+tb.rect.Height > low.Y+low.Height {
		t.Fatalf("toolbar did not fold inside the selection: %+v sel %+v", tb.rect, low)
	}

	// A selection at the left edge still clamps onto the display.
	left := mygo.Rectangle{X: 0, Y: 100, Width: 80, Height: 80}
	tb = shotToolbarLayout(left, display, shotToolNone)
	if tb.rect.X < 8 {
		t.Fatalf("toolbar clamped off the left edge: %+v", tb.rect)
	}
}

func TestShotPropertyBarFollowsTheTool(t *testing.T) {
	display := mygo.Rectangle{X: 0, Y: 0, Width: 1200, Height: 800}
	sel := mygo.Rectangle{X: 500, Y: 300, Width: 300, Height: 200}
	tb := shotToolbarLayout(sel, display, shotToolRect)
	if !tb.hasProps {
		t.Fatal("a selected tool has no property bar")
	}
	if tb.props.rect.Y != tb.rect.Y+shotToolbarH+shotPropsGap {
		t.Fatalf("property bar does not hang under the main bar: %+v", tb.props.rect)
	}
	if tb.props.rect.X+tb.props.rect.Width != tb.rect.X+tb.rect.Width {
		t.Fatalf("property bar is not right-aligned with the main bar: %+v", tb.props.rect)
	}
	if !shotPointInRect(mygo.Point{X: tb.props.rect.X + 1, Y: tb.props.rect.Y + 1}, display) ||
		tb.props.rect.Y+tb.props.rect.Height > display.Height-8 {
		t.Fatalf("property bar left the display: %+v", tb.props.rect)
	}
	if got := len(tb.props.items); got != len(shotAnnotationColors)+len(shotAnnotationWidths) {
		t.Fatalf("property controls = %d", got)
	}
	// The mosaic has widths only.
	mosaic := shotToolbarLayout(sel, display, shotToolMosaic)
	if got := len(mosaic.props.items); got != len(shotAnnotationWidths) {
		t.Fatalf("mosaic property controls = %d", got)
	}
	for _, it := range mosaic.props.items {
		if it.kind != shotBarWidth {
			t.Fatalf("mosaic property bar shows a %v", it.kind)
		}
	}
	// No tool, no property bar.
	if none := shotToolbarLayout(sel, display, shotToolNone); none.hasProps {
		t.Fatal("the property bar showed with no tool selected")
	}
	// A press in the property bar belongs to the bar, not the selection.
	pt := mygo.Point{X: tb.props.items[0].rect.X + 2, Y: tb.props.items[0].rect.Y + 2}
	if !tb.contains(pt) {
		t.Fatal("the property bar does not absorb presses")
	}
	if it, ok := tb.shotBarAt(pt); !ok || it.kind != shotBarColor {
		t.Fatalf("property hit = %+v, %v", it, ok)
	}
}

func TestShotHandleHitAndResize(t *testing.T) {
	sel := mygo.Rectangle{X: 100, Y: 100, Width: 200, Height: 100}
	if got := shotHandleAt(sel, mygo.Point{X: 100, Y: 100}, 6); got != shotHandleNW {
		t.Fatalf("corner hit = %v, want NW", got)
	}
	if got := shotHandleAt(sel, mygo.Point{X: 200, Y: 100}, 6); got != shotHandleN {
		t.Fatalf("edge hit = %v, want N", got)
	}
	if got := shotHandleAt(sel, mygo.Point{X: 180, Y: 150}, 6); got != shotHandleNone {
		t.Fatalf("inside hit = %v, want none", got)
	}
	got := shotResize(sel, shotHandleSE, mygo.Point{X: 500, Y: 500}, mygo.Rectangle{Width: 400, Height: 300})
	if got.X != 100 || got.Y != 100 || got.Width != 300 || got.Height != 200 {
		t.Fatalf("resize = %+v", got)
	}
	// Dragging west past east flips instead of collapsing.
	got = shotResize(sel, shotHandleW, mygo.Point{X: 350, Y: 150}, mygo.Rectangle{Width: 400, Height: 300})
	if got.X != 300 || got.Width != 50 {
		t.Fatalf("flipped resize = %+v", got)
	}
}

func TestShotWindowAtPicksFrontmost(t *testing.T) {
	display := shotTarget{ID: 1, Bounds: mygo.Rectangle{Width: 1000, Height: 700}}
	windows := []mac.ScreenWindow{
		{Bounds: mac.NSRect{Origin: mac.NSPoint{X: 20, Y: 20}, Size: mac.NSSize{Width: 200, Height: 100}}},
		{Bounds: mac.NSRect{Origin: mac.NSPoint{X: 100, Y: 50}, Size: mac.NSSize{Width: 300, Height: 300}}},
	}
	r, ok := shotWindowAt(windows, mygo.Point{X: 150, Y: 80}, display)
	if !ok || r.X != 20 || r.Y != 20 || r.Width != 200 {
		t.Fatalf("frontmost window = %+v, %v", r, ok)
	}
	if _, ok := shotWindowAt(windows, mygo.Point{X: 900, Y: 600}, display); ok {
		t.Fatal("empty desktop picked a window")
	}
	// A window hanging off the display is clipped to it.
	wide := []mac.ScreenWindow{{Bounds: mac.NSRect{Origin: mac.NSPoint{X: -100, Y: 0}, Size: mac.NSSize{Width: 300, Height: 100}}}}
	r, ok = shotWindowAt(wide, mygo.Point{X: 10, Y: 10}, display)
	if !ok || r.X != 0 || r.Width != 200 {
		t.Fatalf("clipped window = %+v, %v", r, ok)
	}
}

func TestShotMosaicCellAverage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			i := y*src.Stride + x*4
			if x < 2 && y < 2 {
				src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 10, 20, 30, 255
			} else {
				src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 200, 210, 220, 255
			}
		}
	}
	top := shotAverageColor(src, image.Rect(0, 0, 2, 2))
	if top.R != 10 || top.G != 20 || top.B != 30 {
		t.Fatalf("top-left cell = %+v", top)
	}
	bottom := shotAverageColor(src, image.Rect(2, 2, 4, 4))
	if bottom.R != 200 || bottom.G != 210 || bottom.B != 220 {
		t.Fatalf("bottom-right cell = %+v", bottom)
	}
}

// A fast drag must not leave holes: every point of the stroke lies in some
// cell the brush added.
func TestShotMosaicStrokeCoversFastDrag(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 90, 90, 90, 255
	}
	target := shotTarget{ID: 1, Bounds: mygo.Rectangle{Width: 200, Height: 200}, Scale: 1}
	cells := map[[2]int]shotMosaicCell{}
	shotMosaicCells(img, target, 10, 10, mygo.Point{X: 10, Y: 10}, mygo.Point{X: 150, Y: 150},
		func(col, row int, cell shotMosaicCell) { cells[[2]int{col, row}] = cell })
	if len(cells) == 0 {
		t.Fatal("the stroke painted no cells")
	}
	for i := 0; i <= 140; i++ {
		p := mygo.Point{X: 10 + i, Y: 10 + i}
		covered := false
		for _, cell := range cells {
			if shotPointInRect(p, cell.rect) {
				covered = true
				break
			}
		}
		if !covered {
			t.Fatalf("the stroke left a hole at %+v", p)
		}
	}
}

func TestShotMosaicBrushDragUndoAndExport(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 200, Height: 200}, 200, 200)
	// A picture whose halves have different colours, so the export proves
	// each cell carries its own block's average.
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			i := y*img.Stride + x*4
			if x < 100 {
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 10, 20, 30, 255
			} else {
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 200, 210, 220, 255
			}
		}
	}
	s.displays[0].img = img
	s.displays[0].bitmap = ui.NewBitmap(img)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 0, Y: 0, Width: 200, Height: 200}
	s.tool = shotToolMosaic
	s.layoutBar()
	down := ui.InputEvent{Kind: ui.InputPointerDown}
	move := ui.InputEvent{Kind: ui.InputPointerMove}
	up := ui.InputEvent{Kind: ui.InputPointerUp}
	s.pointerDown(mygo.Point{X: 20, Y: 20}, down, 0)
	s.pointerMove(mygo.Point{X: 180, Y: 180}, move, 0)
	s.pointerUp(mygo.Point{X: 180, Y: 180}, up, 0)
	if len(s.hist.annots) != 1 || len(s.hist.annots[0].cells) == 0 {
		t.Fatalf("brush annots = %+v", s.hist.annots)
	}
	png, err := s.outputPNG(true)
	if err != nil {
		t.Fatal(err)
	}
	out, err := stdpng.Decode(bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	if px := color.NRGBAModel.Convert(out.At(5, 5)).(color.NRGBA); px.R != 10 || px.G != 20 || px.B != 30 {
		t.Fatalf("left cell exported %v, want its block's average", px)
	}
	if px := color.NRGBAModel.Convert(out.At(195, 195)).(color.NRGBA); px.R != 200 || px.G != 210 || px.B != 220 {
		t.Fatalf("right cell exported %v, want its block's average", px)
	}
	// One drag is one history step.
	if !s.hist.undoLast() || len(s.hist.annots) != 0 {
		t.Fatalf("undo left %+v", s.hist.annots)
	}
}

func TestShotFileNameStyles(t *testing.T) {
	at := time.Date(2026, 11, 7, 15, 4, 5, 0, time.UTC)
	if got, want := shotFileName(at, "截屏", " "), "截屏 2026-11-07 15.04.05.png"; got != want {
		t.Fatalf("Chinese name = %q, want %q", got, want)
	}
	if got, want := shotFileName(at, "Screenshot", " at "), "Screenshot 2026-11-07 at 15.04.05.png"; got != want {
		t.Fatalf("English name = %q, want %q", got, want)
	}
	taken := map[string]bool{"a.png": true, "a (2).png": true}
	if got := shotUniqueName("a.png", func(n string) bool { return taken[n] }); got != "a (3).png" {
		t.Fatalf("unique name = %q", got)
	}
}

func TestShotSaveDirFallsBackToDesktop(t *testing.T) {
	home := "/Users/example"
	if got := shotSaveDir("/Users/example/Pictures", home); got != "/Users/example/Pictures" {
		t.Fatalf("configured dir = %q", got)
	}
	if got := shotSaveDir(" ~/Shots ", home); got != "/Users/example/Shots" {
		t.Fatalf("tilde dir = %q", got)
	}
	if got := shotSaveDir("", home); got != "/Users/example/Desktop" {
		t.Fatalf("empty dir = %q", got)
	}
}

func TestShotHistoryUndoRedo(t *testing.T) {
	var h shotHistory
	h.snap()
	h.push(shotAnnotation{tool: shotToolRect, text: "one"})
	h.snap()
	h.push(shotAnnotation{tool: shotToolRect, text: "two"})
	if len(h.annots) != 2 {
		t.Fatalf("annots = %d", len(h.annots))
	}
	if !h.undoLast() || len(h.annots) != 1 || h.annots[0].text != "one" {
		t.Fatalf("undo = %+v", h.annots)
	}
	if !h.redoLast() || len(h.annots) != 2 || h.annots[1].text != "two" {
		t.Fatalf("redo = %+v", h.annots)
	}
	h.snap()
	h.push(shotAnnotation{tool: shotToolRect, text: "three"})
	if h.canRedo() {
		t.Fatal("a new change kept the redo stack")
	}
}

func TestShotShiftConstraints(t *testing.T) {
	r := shotSquareRect(mygo.Point{X: 10, Y: 10}, mygo.Point{X: 70, Y: 30})
	if r.Width != 60 || r.Height != 60 {
		t.Fatalf("square = %+v", r)
	}
	p := shotSnapPoint(mygo.Point{}, mygo.Point{X: 100, Y: 10})
	if p.X != 100 || p.Y != 0 {
		t.Fatalf("45° snap = %+v", p)
	}
	an := shotDragAnnotation(shotToolArrow, shotAnnotationColors[0], 2, mygo.Point{}, mygo.Point{X: 100, Y: 20}, true)
	if an.b.Y != 0 {
		t.Fatalf("arrow did not snap: %+v", an.b)
	}
}

func TestShotOutputCropsAndComposites(t *testing.T) {
	s := shotTestSession(t, mygo.Rectangle{Width: 40, Height: 30}, 40, 30)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 5, Y: 5, Width: 20, Height: 15}
	s.hist.annots = []shotAnnotation{{
		tool: shotToolRect, color: ui.Hex("#FF0000"), width: 2,
		a: mygo.Point{X: 5, Y: 5}, b: mygo.Point{X: 25, Y: 20},
	}}
	png, err := s.outputPNG(true)
	if err != nil {
		t.Fatal(err)
	}
	img, err := stdpng.Decode(bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 20 || img.Bounds().Dy() != 15 {
		t.Fatalf("crop size = %v", img.Bounds())
	}
	if !hasReddishPixel(img) {
		t.Fatal("annotation was not composited into the crop")
	}
	plain, err := s.outputPNG(false)
	if err != nil {
		t.Fatal(err)
	}
	img2, err := stdpng.Decode(bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	if hasReddishPixel(img2) {
		t.Fatal("unannotated crop carried the annotation")
	}
	if px := color.NRGBAModel.Convert(img2.At(2, 2)).(color.NRGBA); px.R != 40 || px.G != 60 || px.B != 80 || px.A != 255 {
		t.Fatalf("crop pixel = %v, want the frozen 40,60,80", px)
	}
}

func hasReddishPixel(img image.Image) bool {
	r := img.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			px := img.At(x, y).(color.RGBA)
			if px.R > 180 && px.G < 100 && px.B < 100 {
				return true
			}
		}
	}
	return false
}

func TestShotEntryNeedsPreflightOnly(t *testing.T) {
	shotSeams(t)
	shotPreflight = func() bool { return false }
	captured := false
	shotCaptureDisplay = func(int, string) error { captured = true; return nil }
	a := &shelf{}
	a.startScreenshotOnMain()
	if !a.ocrSheet || !a.ocrSheetShot {
		t.Fatalf("permission sheet not armed for the screenshot: sheet=%v shot=%v", a.ocrSheet, a.ocrSheetShot)
	}
	if captured {
		t.Fatal("capture ran without the screen-recording grant")
	}
}

func TestShotEntryFreezesAndToggles(t *testing.T) {
	shotSeams(t)
	shotPreflight = func() bool { return true }
	shotDisplays = func() []mygo.Display {
		return []mygo.Display{{ID: 1, Bounds: mygo.Rectangle{Width: 100, Height: 80}, ScaleFactor: 1, Primary: true}}
	}
	shotCursorPoint = func() mygo.Point { return mygo.Point{X: 10, Y: 10} }
	captures := 0
	shotCaptureDisplay = func(index int, path string) error {
		captures++
		writeShotTestPNG(t, path, 100, 80, color.RGBA{1, 2, 3, 255})
		return nil
	}
	a := &shelf{}
	a.startScreenshotOnMain()
	s := a.shot
	if s == nil || !s.installed || len(s.displays) != 1 || captures != 1 {
		t.Fatalf("session = %+v, captures=%d", s, captures)
	}
	if _, err := s.outputPNG(false); err == nil {
		t.Fatal("a capture with no selection produced an image")
	}
	// The same shortcut while the overlay is up cancels it.
	a.startScreenshotOnMain()
	if a.shot != nil || !s.closed {
		t.Fatal("second press did not cancel the session")
	}
}

func TestShotPointerSelectsWindowOrDisplay(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 100, Height: 80}, 100, 80)
	shotNormalWindow = func() []mac.ScreenWindow {
		return []mac.ScreenWindow{{Bounds: mac.NSRect{Origin: mac.NSPoint{X: 20, Y: 10}, Size: mac.NSSize{Width: 40, Height: 30}}}}
	}
	down := ui.InputEvent{Kind: ui.InputPointerDown}
	up := ui.InputEvent{Kind: ui.InputPointerUp}
	s.pointerDown(mygo.Point{X: 30, Y: 20}, down, 0)
	s.pointerUp(mygo.Point{X: 30, Y: 20}, up, 0)
	if !s.hasSel || s.sel != (mygo.Rectangle{X: 20, Y: 10, Width: 40, Height: 30}) {
		t.Fatalf("window click = %+v", s.sel)
	}
	// A click on the desktop selects the whole display.
	s.pointerDown(mygo.Point{X: 90, Y: 70}, down, 0)
	s.pointerUp(mygo.Point{X: 90, Y: 70}, up, 0)
	if s.sel != (mygo.Rectangle{Width: 100, Height: 80}) {
		t.Fatalf("blank click = %+v", s.sel)
	}
}

func TestShotPointerDragMoveResize(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	s.pointerDown(mygo.Point{X: 10, Y: 10}, ui.InputEvent{Kind: ui.InputPointerDown}, 0)
	s.pointerMove(mygo.Point{X: 60, Y: 50}, ui.InputEvent{Kind: ui.InputPointerMove}, 0)
	s.pointerUp(mygo.Point{X: 60, Y: 50}, ui.InputEvent{Kind: ui.InputPointerUp}, 0)
	if s.sel != (mygo.Rectangle{X: 10, Y: 10, Width: 50, Height: 40}) {
		t.Fatalf("drag = %+v", s.sel)
	}
	// Press inside moves the whole selection.
	s.pointerDown(mygo.Point{X: 30, Y: 30}, ui.InputEvent{Kind: ui.InputPointerDown}, 0)
	s.pointerMove(mygo.Point{X: 40, Y: 35}, ui.InputEvent{Kind: ui.InputPointerMove}, 0)
	s.pointerUp(mygo.Point{X: 40, Y: 35}, ui.InputEvent{Kind: ui.InputPointerUp}, 0)
	if s.sel != (mygo.Rectangle{X: 20, Y: 15, Width: 50, Height: 40}) {
		t.Fatalf("move = %+v", s.sel)
	}
	// The SE handle resizes it, clamped to the display.
	s.pointerDown(mygo.Point{X: 70, Y: 55}, ui.InputEvent{Kind: ui.InputPointerDown}, 0)
	s.pointerMove(mygo.Point{X: 500, Y: 500}, ui.InputEvent{Kind: ui.InputPointerMove}, 0)
	s.pointerUp(mygo.Point{X: 500, Y: 500}, ui.InputEvent{Kind: ui.InputPointerUp}, 0)
	if s.sel.X != 20 || s.sel.Y != 15 || s.sel.X+s.sel.Width != 400 || s.sel.Y+s.sel.Height != 300 {
		t.Fatalf("resize = %+v", s.sel)
	}
}

func TestShotMagnifierAvoidsToolbar(t *testing.T) {
	bar := mygo.Rectangle{X: 100, Y: 200, Width: 200, Height: 42}
	x, y := shotMagnifierAt(110, 90, 400, 300, 156, 108, bar)
	if shotRectsIntersect(mygo.Rectangle{X: x, Y: y, Width: 156, Height: 108}, bar) {
		t.Fatalf("magnifier at %d,%d overlaps the toolbar", x, y)
	}
}

func TestShotEscapeLadder(t *testing.T) {
	s := shotTestSession(t, mygo.Rectangle{Width: 100, Height: 80}, 100, 80)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 40, Height: 30}
	s.tool = shotToolRect
	s.text = &shotTextEdit{at: mygo.Point{X: 20, Y: 20}, width: 60, text: "hi"}
	s.escape()
	if s.text != nil || len(s.hist.annots) != 1 || s.hist.annots[0].text != "hi" {
		t.Fatalf("Esc did not finish and keep the text: text=%v annots=%+v", s.text, s.hist.annots)
	}
	s.escape()
	if s.tool != shotToolNone {
		t.Fatal("Esc did not drop the tool")
	}
	s.escape()
	if s.hasSel {
		t.Fatal("Esc did not clear the selection")
	}
	s.escape()
	if !s.closed {
		t.Fatal("the last Esc did not close the overlay")
	}
}

func TestShotArrowDrawsDiagonalAndSolidHead(t *testing.T) {
	img := renderShotMarks(t, 64, 64, shotAnnotation{
		tool: shotToolArrow, color: ui.Hex("#FF0000"), width: 4,
		a: mygo.Point{X: 6, Y: 6}, b: mygo.Point{X: 58, Y: 58},
	})
	red := func(x, y int) bool {
		px := img.RGBAAt(x, y)
		return px.R > 180 && px.G < 100 && px.B < 100 && px.A > 180
	}
	if !red(30, 30) {
		t.Fatal("the diagonal shaft painted nothing on the diagonal")
	}
	// The old painter line filled an axis-aligned band at the start's row.
	if red(30, 6) {
		t.Fatal("the arrow still painted an axis-aligned bar off the diagonal")
	}
	if !red(53, 49) {
		t.Fatal("the arrow head is not solid beside the shaft")
	}
}

// renderShotMarks paints annotations over a transparent canvas at 1× so a
// test can read exact pixels.
func renderShotMarks(t *testing.T, w, h int, annots ...shotAnnotation) *image.RGBA {
	t.Helper()
	return ui.Render(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		ui.Box(c.Key("marks")).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			for _, an := range annots {
				shotDrawAnnotation(p, an, mygo.Point{}, 1)
			}
		})
	}, w, h, 1)
}

// shotTextView renders an editing overlay at 1×; typing through it is the
// same TextCaret path the window uses.
func shotTextView(t *testing.T, s *shotSession) *ui.Tester {
	t.Helper()
	w, h := 300, 200
	if d := s.ownerDisplay(); d != nil {
		w, h = d.Bounds.Width, d.Bounds.Height
	}
	v := ui.NewTester(func(c *ui.Context) { s.overlayView(c, 0) }, w, h)
	v.SetScale(1)
	v.Frame()
	v.Frame()
	return v
}

// shotNewTextBox clicks at (x, y) with the text tool and returns the new
// editing box.
func shotNewTextBox(t *testing.T, s *shotSession, v *ui.Tester, x, y float32) *shotTextEdit {
	t.Helper()
	if s.tool != shotToolText {
		s.tool = shotToolText
		s.layoutBar()
	}
	v.ClickAtWith(0, x, y)
	v.Frame()
	if s.text == nil {
		t.Fatalf("a click with the text tool made no box at %v,%v", x, y)
	}
	return s.text
}

func TestShotTextClickCreatesDefaultBox(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 200, Height: 100}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	te := shotNewTextBox(t, s, v, 40, 40)
	size := shotTextSizeFor(s.width)
	want := shotTextDefaultWidth(size)
	if te.width != want {
		t.Fatalf("click box width = %d, want %d", te.width, want)
	}
	if te.at != (mygo.Point{X: 40, Y: 40}) {
		t.Fatalf("click box at %+v", te.at)
	}
	s.finishText()
}

func TestShotTextDragSetsBoxWidth(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 220, Height: 120}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	v.Move(40, 50)
	v.Press(40, 50)
	v.Move(120, 50)
	v.Release(120, 50)
	v.Frame()
	if s.text == nil {
		t.Fatal("a drag with the text tool made no box")
	}
	if got, want := s.text.width, 80; got != want {
		t.Fatalf("dragged box width = %d, want %d", got, want)
	}
	if got := s.text.at.X; got != 40 {
		t.Fatalf("dragged box left = %d, want 40", got)
	}
	// Dragging left anchors the box at the released edge.
	v.Move(160, 70)
	v.Press(160, 70)
	v.Move(100, 70)
	v.Release(100, 70)
	v.Frame()
	if s.text == nil || s.text.at.X != 100 || s.text.width != 60 {
		t.Fatalf("leftward drag = %+v", s.text)
	}
	s.finishText()
}

func TestShotTextEnterBreaksLinesAndCmdEnterFinishes(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 220, Height: 120}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	te := shotNewTextBox(t, s, v, 30, 30)
	v.Type("hello")
	v.Key(0, ui.KeyEnter)
	v.Type("world")
	v.Frame()
	if got, want := te.text, "hello\nworld"; got != want {
		t.Fatalf("typed text = %q, want %q", got, want)
	}
	v.Key(ui.Cmd, ui.KeyEnter)
	v.Frame()
	if s.text != nil {
		t.Fatal("⌘Enter did not leave the box")
	}
	if len(s.hist.annots) != 1 || s.hist.annots[0].text != "hello\nworld" {
		t.Fatalf("committed text = %+v", s.hist.annots)
	}
	if s.hist.annots[0].boxW == 0 {
		t.Fatal("the annotation lost its box width")
	}
	// The overlay takes the keyboard back after the box goes.
	v.Key(0, ui.KeyEscape)
	v.Frame()
	if s.tool != shotToolNone {
		t.Fatalf("Esc after finishing did not reach the overlay: tool=%v", s.tool)
	}
}

func TestShotTextEscKeepsTextAndEmptyDisappears(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 220, Height: 120}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	shotNewTextBox(t, s, v, 30, 30)
	v.Type("keep")
	v.Key(0, ui.KeyEscape)
	v.Frame()
	if s.text != nil {
		t.Fatal("Esc did not finish the box")
	}
	if len(s.hist.annots) != 1 || s.hist.annots[0].text != "keep" {
		t.Fatalf("Esc lost the text: %+v", s.hist.annots)
	}
	// An empty box leaves no annotation and no undo step.
	before := len(s.hist.undo)
	shotNewTextBox(t, s, v, 40, 60)
	v.Key(0, ui.KeyEscape)
	v.Frame()
	if s.text != nil || len(s.hist.annots) != 1 || len(s.hist.undo) != before {
		t.Fatalf("empty box left state: text=%v annots=%d undo=%d", s.text, len(s.hist.annots), len(s.hist.undo))
	}
}

func TestShotTextComposingEnterStaysInTheBox(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 220, Height: 120}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	shotNewTextBox(t, s, v, 30, 30)
	v.Compose("ni", 2)
	v.Frame()
	v.Key(0, ui.KeyEnter)
	v.Frame()
	if s.text == nil {
		t.Fatal("Enter while composing finished the box; it belongs to the input method")
	}
	v.Compose("", 0)
	v.Key(0, ui.KeyEscape)
	v.Frame()
}

func TestShotTextResizeReflows(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 300, Height: 220}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	te := shotNewTextBox(t, s, v, 30, 30)
	v.Type("one two three four five six seven eight nine ten")
	v.Frame()
	before := s.textHeight(te)
	gx := float32(te.at.X + te.width - 6 + 6) // grip-e's centre
	gy := float32(te.at.Y + s.textHeight(te)/2)
	v.Move(gx, gy)
	v.Press(gx, gy)
	v.Move(gx-70, gy)
	v.Release(gx-70, gy)
	v.Frame()
	if te.width > shotTextDefaultWidth(te.size())-60 {
		t.Fatalf("the grip did not narrow the box: width=%d", te.width)
	}
	if got := s.textHeight(te); got <= before {
		t.Fatalf("lines did not reflow: height %d -> %d", before, got)
	}
	s.finishText()
}

func TestShotTextDoubleClickReeditsWithItsWidth(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 220, Height: 120}
	s.tool = shotToolNone
	s.hist.annots = []shotAnnotation{{tool: shotToolText, color: shotAnnotationColors[4], width: 4,
		a: mygo.Point{X: 30, Y: 30}, text: "hello", boxW: 120}}
	s.layoutBar()
	v := shotTextView(t, s)
	v.ClickAtWith(0, 40, 40)
	v.ClickAtWith(0, 40, 40)
	v.Frame()
	if s.text == nil {
		t.Fatal("double click did not reopen the text")
	}
	if s.text.width != 120 || s.text.text != "hello" || s.text.index != 1 {
		t.Fatalf("reopened box = %+v", s.text)
	}
	s.finishText()
	if len(s.hist.annots) != 1 || s.hist.annots[0].text != "hello" {
		t.Fatalf("re-edit changed the annotation: %+v", s.hist.annots)
	}
}

func TestShotTextPasteKeepsLinesAndUndoStaysInTheBox(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 220, Height: 120}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	te := shotNewTextBox(t, s, v, 30, 30)
	v.SetClipboard("a\nb")
	v.Key(ui.Cmd, ui.KeyV)
	v.Frame()
	if te.text != "a\nb" {
		t.Fatalf("pasted text = %q", te.text)
	}
	v.Type("c")
	v.Key(ui.Cmd, ui.KeyZ)
	v.Frame()
	if s.text == nil {
		t.Fatal("⌘Z in the box finished the screenshot edit")
	}
	if te.text == "a\nbc" {
		t.Fatal("⌘Z did not undo inside the text box")
	}
	s.finishText()
}

// shotTextRowBands counts the lines of white text in area: a glyph row
// may have a blank row inside it, so bands closer than four rows are one
// line.
func shotTextRowBands(img image.Image, area image.Rectangle) int {
	bands, last, gap := 0, -100, 0
	for y := area.Min.Y; y < area.Max.Y; y++ {
		row := false
		for x := area.Min.X; x < area.Max.X; x++ {
			px := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if px.R > 180 && px.G > 180 && px.B > 180 {
				row = true
				break
			}
		}
		if row {
			gap = y - last
			if gap > 4 {
				bands++
			}
			last = y
		}
	}
	return bands
}

func TestShotTextExportWrapsLikeThePreview(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 0, Y: 0, Width: 300, Height: 200}
	s.hist.annots = []shotAnnotation{{tool: shotToolText, color: ui.Hex("#FFFFFF"), width: 4,
		a: mygo.Point{X: 20, Y: 20}, boxW: 100, text: "one two three four five six seven eight"}}
	preview := ui.Render(func(c *ui.Context) { s.overlayView(c, 0) }, 400, 300, 1)
	previewBands := shotTextRowBands(preview, image.Rect(15, 15, 130, 190))
	png, err := s.outputPNG(true)
	if err != nil {
		t.Fatal(err)
	}
	out, err := stdpng.Decode(bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	exportBands := shotTextRowBands(out, image.Rect(0, 0, 300, 200))
	if previewBands < 2 {
		t.Fatalf("the test text did not wrap in the preview: %d bands", previewBands)
	}
	if exportBands != previewBands {
		t.Fatalf("export wrapped into %d lines, preview into %d", exportBands, previewBands)
	}
	// A narrower box breaks the same text into more lines.
	s.hist.annots[0].boxW = 50
	png, err = s.outputPNG(true)
	if err != nil {
		t.Fatal(err)
	}
	out, err = stdpng.Decode(bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	if narrow := shotTextRowBands(out, image.Rect(0, 0, 300, 200)); narrow <= exportBands {
		t.Fatalf("narrow box kept %d lines, wide had %d", narrow, exportBands)
	}
}

func TestShotTextMovesAfterChoosingIt(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 220, Height: 120}
	s.tool = shotToolNone
	s.hist.annots = []shotAnnotation{{tool: shotToolText, color: shotAnnotationColors[4], width: 4,
		a: mygo.Point{X: 30, Y: 30}, text: "move me", boxW: 100}}
	s.layoutBar()
	v := shotTextView(t, s)
	v.Move(40, 40)
	v.Press(40, 40)
	v.Move(60, 55)
	v.Release(60, 55)
	v.Frame()
	if got := s.hist.annots[0].a; got != (mygo.Point{X: 50, Y: 45}) {
		t.Fatalf("moved text at %+v", got)
	}
	if !s.hist.undoLast() {
		t.Fatal("moving a text made no undo step")
	}
	if got := s.hist.annots[0].a; got != (mygo.Point{X: 30, Y: 30}) {
		t.Fatalf("undo left the text at %+v", got)
	}
}

func TestShotTextToolSwitchFinishesTheBox(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 260, Height: 160}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	shotNewTextBox(t, s, v, 30, 30)
	v.Type("done")
	var arrow mygo.Rectangle
	for _, it := range s.bar.items {
		if it.kind == shotBarTool && it.tool == shotToolArrow {
			arrow = it.rect
		}
	}
	v.ClickAtWith(0, float32(arrow.X+arrow.Width/2), float32(arrow.Y+arrow.Height/2))
	v.Frame()
	if s.text != nil || s.tool != shotToolArrow {
		t.Fatalf("switching tools left the box open: text=%v tool=%v", s.text, s.tool)
	}
	if len(s.hist.annots) != 1 || s.hist.annots[0].text != "done" {
		t.Fatalf("the box text was lost: %+v", s.hist.annots)
	}
}

func TestShotTextColourChangesWhileEditing(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 260, Height: 160}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	shotNewTextBox(t, s, v, 30, 30)
	var swatch mygo.Rectangle
	for _, it := range s.bar.props.items {
		if it.kind == shotBarColor && it.index == 2 {
			swatch = it.rect
		}
	}
	v.ClickAtWith(0, float32(swatch.X+swatch.Width/2), float32(swatch.Y+swatch.Height/2))
	v.Frame()
	if s.text == nil {
		t.Fatal("choosing a colour finished the box")
	}
	if s.text.color != shotAnnotationColors[2] {
		t.Fatalf("editing colour = %+v, want %+v", s.text.color, shotAnnotationColors[2])
	}
	v.Type("green")
	s.finishText()
	if got := s.hist.annots[len(s.hist.annots)-1].color; got != shotAnnotationColors[2] {
		t.Fatalf("committed colour = %+v", got)
	}
}

// The live control and the export must break the same text at the same
// width; only the type size and the box width decide the lines.
func TestShotTextEditorWrapsLikeExport(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 0, Y: 0, Width: 300, Height: 200}
	s.tool = shotToolText
	s.color = ui.Hex("#FFFFFF")
	s.layoutBar()
	v := shotTextView(t, s)
	te := shotNewTextBox(t, s, v, 30, 30)
	v.Type("one two three four five six seven eight nine")
	v.Frame()
	te.width = 100
	v.Frame()
	preview := v.Image()
	previewBands := shotTextRowBands(preview, image.Rect(38, 30, 120, 136))
	if previewBands < 2 {
		t.Fatalf("the control did not wrap the text: %d bands", previewBands)
	}
	s.finishText()
	png, err := s.outputPNG(true)
	if err != nil {
		t.Fatal(err)
	}
	out, err := stdpng.Decode(bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	exportBands := shotTextRowBands(out, image.Rect(0, 0, 300, 200))
	if exportBands != previewBands {
		t.Fatalf("the control wrapped into %d lines, the export into %d", previewBands, exportBands)
	}
}

// Retina only scales the same layout: the crop breaks lines where the
// overlay does.
func TestShotTextRetinaWrapsLikeThePreview(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 200, Height: 150}, 400, 300)
	s.displays[0].Scale = 2
	s.displays[0].shotTarget.Scale = 2
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 0, Y: 0, Width: 200, Height: 150}
	s.hist.annots = []shotAnnotation{{tool: shotToolText, color: ui.Hex("#FFFFFF"), width: 4,
		a: mygo.Point{X: 10, Y: 10}, boxW: 90, text: "one two three four five six seven eight"}}
	preview := ui.Render(func(c *ui.Context) { s.overlayView(c, 0) }, 200, 150, 1)
	previewBands := shotTextRowBands(preview, image.Rect(12, 10, 96, 140))
	png, err := s.outputPNG(true)
	if err != nil {
		t.Fatal(err)
	}
	out, err := stdpng.Decode(bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	exportBands := shotTextRowBands(out, image.Rect(0, 0, 400, 300))
	if previewBands < 2 || exportBands != previewBands {
		t.Fatalf("retina export has %d lines, preview %d", exportBands, previewBands)
	}
}

func TestShotTextSelectCopyCutStayInBox(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 220, Height: 120}
	s.tool = shotToolText
	s.layoutBar()
	v := shotTextView(t, s)
	te := shotNewTextBox(t, s, v, 30, 30)
	v.Type("abc")
	v.Key(ui.Cmd, ui.KeyA)
	v.Key(ui.Cmd, ui.KeyC)
	v.Frame()
	if v.Clipboard() != "abc" {
		t.Fatalf("⌘C in the box copied %q", v.Clipboard())
	}
	if s.text == nil {
		t.Fatal("⌘C closed the box")
	}
	v.Key(ui.Cmd, ui.KeyX)
	v.Frame()
	if te.text != "" || s.text == nil {
		t.Fatalf("⌘X left %q in the box (text nil: %v)", te.text, s.text == nil)
	}
	s.finishText()
}

func TestShotTextSizeFollowsWidth(t *testing.T) {
	if got := shotTextSizeFor(shotAnnotationWidths[0]); got != 14 {
		t.Fatalf("thin text = %v", got)
	}
	if got := shotTextSizeFor(shotAnnotationWidths[1]); got != 18 {
		t.Fatalf("medium text = %v", got)
	}
	if got := shotTextSizeFor(shotAnnotationWidths[2]); got != 24 {
		t.Fatalf("thick text = %v", got)
	}
}

func TestShotToolbarClicksSelectAndDeselect(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 200, Height: 100}
	s.tool = shotToolNone
	s.layoutBar()
	v := shotTextView(t, s)
	v.SetLocale("zh-Hans")
	var arrow mygo.Rectangle
	for _, it := range s.bar.items {
		if it.kind == shotBarTool && it.tool == shotToolArrow {
			arrow = it.rect
		}
	}
	if arrow.Width == 0 {
		t.Fatal("the toolbar has no arrow control")
	}
	cx, cy := float32(arrow.X+arrow.Width/2), float32(arrow.Y+arrow.Height/2)
	v.ClickAtWith(0, cx, cy)
	v.Frame()
	if s.tool != shotToolArrow {
		t.Fatalf("clicked tool = %v", s.tool)
	}
	if !s.bar.hasProps || len(s.bar.props.items) != len(shotAnnotationColors)+len(shotAnnotationWidths) {
		t.Fatalf("property bar after choosing a tool: %+v", s.bar.props)
	}
	// The same control drops the tool and with it the property bar.
	v.ClickAtWith(0, cx, cy)
	v.Frame()
	if s.tool != shotToolNone || s.bar.hasProps {
		t.Fatalf("second click: tool=%v props=%v", s.tool, s.bar.hasProps)
	}
}

func TestShotToolbarTooltipNamesTheControl(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 300, Height: 200}, 300, 200)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 10, Y: 10, Width: 200, Height: 100}
	s.layoutBar()
	v := shotTextView(t, s)
	v.SetLocale("zh-Hans")
	var mosaic mygo.Rectangle
	for _, it := range s.bar.items {
		if it.kind == shotBarTool && it.tool == shotToolMosaic {
			mosaic = it.rect
		}
	}
	v.Move(float32(mosaic.X+mosaic.Width/2), float32(mosaic.Y+mosaic.Height/2))
	v.Advance(700 * time.Millisecond)
	v.Frame()
	found := false
	for _, text := range v.Texts() {
		if text == "马赛克" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no tooltip after resting on the mosaic button: texts=%v", v.Texts())
	}
}

// Marks belong inside the selection: the preview clips them, so a mosaic
// block (or any other mark) at the edge cannot paint over the veil or break
// the selection border.
func TestShotPreviewClipsMarksToSelection(t *testing.T) {
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 100, Y: 80, Width: 200, Height: 120}
	s.tool = shotToolNone
	s.layoutBar()
	green := ui.RGBA(0, 255, 0, 1)
	inside := shotMosaicCell{rect: mygo.Rectangle{X: 150, Y: 120, Width: 20, Height: 20}, color: green}
	crossing := shotMosaicCell{rect: mygo.Rectangle{X: 180, Y: 190, Width: 20, Height: 20}, color: green}
	outside := shotMosaicCell{rect: mygo.Rectangle{X: 320, Y: 40, Width: 20, Height: 20}, color: green}
	s.hist.annots = []shotAnnotation{{tool: shotToolMosaic, color: green, width: 4,
		cells: []shotMosaicCell{inside, crossing, outside}}}
	img := ui.Render(func(c *ui.Context) { s.overlayView(c, 0) }, 400, 300, 1)
	isGreen := func(x, y int) bool {
		px := img.RGBAAt(x, y)
		return px.G > 180 && px.R < 80 && px.B < 80
	}
	if !isGreen(155, 125) {
		t.Fatal("the block inside the selection was not painted")
	}
	if isGreen(325, 45) {
		t.Fatal("a block outside the selection painted over the veil")
	}
	if isGreen(190, 205) {
		t.Fatal("a block's outside part painted below the selection")
	}
	// The border is complete across the bottom edge, including where the
	// crossing block meets it.
	accentish := func(x, y int) bool {
		px := img.RGBAAt(x, y)
		return px.B > 180 && px.R < 90 && px.G > 90 && px.G < 190
	}
	for x := 105; x <= 295; x += 5 {
		if abs(x-200) <= 8 {
			continue // the bottom-centre handle sits on this stretch
		}
		found := false
		for y := 197; y <= 202; y++ {
			if accentish(x, y) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("the selection border is broken at x=%d", x)
		}
	}
}

// The brush collects no block wholly outside the selection.
func TestShotMosaicBrushKeepsInsideSelection(t *testing.T) {
	shotSeams(t)
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 100, Y: 80, Width: 200, Height: 120}
	s.tool = shotToolMosaic
	s.pre = pre{ann: shotAnnotation{tool: shotToolMosaic, width: shotAnnotationWidths[1]}, on: true}
	s.brushSeen = map[[2]int]bool{}
	s.paintMosaic(mygo.Point{X: 60, Y: 140}, mygo.Point{X: 140, Y: 140})
	if len(s.pre.ann.cells) == 0 {
		t.Fatal("the brush collected no cells at the selection's edge")
	}
	for _, cell := range s.pre.ann.cells {
		if !shotRectsIntersect(cell.rect, s.sel) {
			t.Fatalf("the brush collected a block outside the selection: %+v", cell.rect)
		}
	}
}

func TestShotTempClipboardPayload(t *testing.T) {
	shotSeams(t)
	var wrotePNG []byte
	var wrotePath string
	shotClipboard = func(png []byte, path string) error {
		wrotePNG, wrotePath = png, path
		return nil
	}
	a := &shelf{dataDir: t.TempDir()}
	png := []byte("not really png")
	if !a.copyShotPNG(png) {
		t.Fatal("copyShotPNG failed")
	}
	if !bytes.Equal(wrotePNG, png) {
		t.Fatal("clipboard got different bytes")
	}
	if filepath.Base(wrotePath) == "" {
		t.Fatal("clipboard file URL has no name")
	}
}

// The pasteboard must carry both a PNG and a file URL: the receiver gets a
// picture, Finder gets a file. Both live in one write.
func TestShotClipboardOffersPNGAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := shotClipboardData([]byte("png-bytes"), path)
	if err != nil {
		t.Fatal(err)
	}
	formats := data.Formats()
	for _, want := range []transfer.Format{transfer.PNG, transfer.FileList, transfer.URIList} {
		found := false
		for _, f := range formats {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("clipboard data lacks %s; formats=%v", want, formats)
		}
	}
}

func TestShotEnterCopiesAndCloses(t *testing.T) {
	shotSeams(t)
	var got []byte
	var path string
	shotClipboard = func(png []byte, p string) error {
		got, path = png, p
		return nil
	}
	a := &shelf{}
	s := shotTestSession(t, mygo.Rectangle{Width: 40, Height: 30}, 40, 30)
	s.a = a
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 5, Y: 5, Width: 20, Height: 15}
	if !s.keyDown(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter}) {
		t.Fatal("Enter was not handled")
	}
	if len(got) == 0 || path == "" {
		t.Fatalf("clipboard got png=%d path=%q", len(got), path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("clipboard file URL target missing: %v", err)
	}
	if !s.closed {
		t.Fatal("Enter did not close the session")
	}
}

func TestShotSaveWritesToConfiguredFolder(t *testing.T) {
	shotSeams(t)
	dir := t.TempDir()
	shotReadSaveDir = func() string { return dir }
	s := shotTestSession(t, mygo.Rectangle{Width: 40, Height: 30}, 40, 30)
	s.a = &shelf{}
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 5, Y: 5, Width: 20, Height: 15}
	s.save()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("saved files = %v, %v", entries, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil || len(b) < 8 {
		t.Fatalf("saved png = %d bytes, %v", len(b), err)
	}
}

func TestShotRecognizeCopiesFromUnannotatedCrop(t *testing.T) {
	ocrSeams(t)
	shotSeams(t)
	var sawFile int
	ocrRecognize = func(path string) ([]string, error) {
		if b, err := os.ReadFile(path); err == nil {
			sawFile = len(b)
		}
		return []string{"识别出的截图文字"}, nil
	}
	var copied string
	a := &shelf{}
	a.write = recordClipFunc(func(text string) error { copied = text; return nil })
	s := shotTestSession(t, mygo.Rectangle{Width: 40, Height: 30}, 40, 30)
	s.a = a
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 5, Y: 5, Width: 20, Height: 15}
	s.hist.annots = []shotAnnotation{{tool: shotToolRect, color: ui.Hex("#FF0000"), width: 2,
		a: mygo.Point{X: 5, Y: 5}, b: mygo.Point{X: 25, Y: 20}}}
	s.recognize()
	if sawFile == 0 {
		t.Fatal("the recognizer got no file")
	}
	if copied != "识别出的截图文字" {
		t.Fatalf("copied %q", copied)
	}
}

func TestShotPinOpensAnnotatedCrop(t *testing.T) {
	shotSeams(t)
	var pinned []byte
	var frame mygo.Rectangle
	var scale float64
	shotOpenPin = func(_ *shelf, png []byte, f mygo.Rectangle, sc float64) {
		pinned, frame, scale = png, f, sc
	}
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	s.a = &shelf{}
	s.hasSel = true
	s.sel = mygo.Rectangle{X: 40, Y: 30, Width: 120, Height: 90}
	s.pin()
	if len(pinned) == 0 {
		t.Fatal("no pinned picture")
	}
	if frame.Width != 120 || frame.Height != 90 {
		t.Fatalf("pin frame = %+v", frame)
	}
	if scale != 1 {
		t.Fatalf("pin scale = %v", scale)
	}
}

// The magnifier covers the pixels it magnifies: it belongs to choosing,
// not to working on a settled selection.
func TestShotMagnifierOnlyWhileSelecting(t *testing.T) {
	s := shotTestSession(t, mygo.Rectangle{Width: 400, Height: 300}, 400, 300)
	if !s.magnifierVisible() {
		t.Fatal("no magnifier before a selection")
	}
	s.mode = shotModeSelect
	if !s.magnifierVisible() {
		t.Fatal("no magnifier while dragging a new selection")
	}
	s.mode = shotModeResize
	if !s.magnifierVisible() {
		t.Fatal("no magnifier while resizing a handle")
	}
	s.hasSel = true
	s.mode = shotModeIdle
	if s.magnifierVisible() {
		t.Fatal("magnifier stayed over a settled selection")
	}
	s.mode = shotModeMove
	if s.magnifierVisible() {
		t.Fatal("magnifier showed while moving the selection")
	}
	s.mode = shotModeDraw
	if s.magnifierVisible() {
		t.Fatal("magnifier showed while annotating")
	}
}
