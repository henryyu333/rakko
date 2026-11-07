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
	clean := shotCleanTemp
	settle := shotPanelSettle
	after := shotAfter
	openPin := shotOpenPin
	t.Cleanup(func() {
		shotDisplays, shotCursorPoint, shotCaptureDisplay, shotNormalWindow = displays, cursor, capture, windows
		shotDispatch, shotWork, shotClock = dispatch, work, clock
		shotFrontPID, shotActivateSelf, shotPreflight = front, activate, preflight
		shotClipboard, shotReadSaveDir, shotWriteFile, shotOpenWindow = clipboard, readDir, writeFile, openWin
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
	tb := shotToolbarLayout(sel, display)
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
	tb = shotToolbarLayout(low, display)
	if tb.rect.Y < low.Y || tb.rect.Y+tb.rect.Height > low.Y+low.Height {
		t.Fatalf("toolbar did not fold inside the selection: %+v sel %+v", tb.rect, low)
	}

	// A selection at the left edge still clamps onto the display.
	left := mygo.Rectangle{X: 0, Y: 100, Width: 80, Height: 80}
	tb = shotToolbarLayout(left, display)
	if tb.rect.X < 8 {
		t.Fatalf("toolbar clamped off the left edge: %+v", tb.rect)
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

func TestShotPixelateAveragesBlocks(t *testing.T) {
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
	out := shotPixelate(src, src.Bounds(), 2)
	if out.Bounds().Dx() != 2 || out.Bounds().Dy() != 2 {
		t.Fatalf("grid = %v", out.Bounds())
	}
	if got := out.RGBAAt(0, 0); got.R != 10 || got.G != 20 || got.B != 30 || got.A != 255 {
		t.Fatalf("top-left cell = %v", got)
	}
	if got := out.RGBAAt(1, 1); got.R != 200 || got.G != 210 || got.B != 220 {
		t.Fatalf("bottom-right cell = %v", got)
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
	s.text = &shotTextEdit{at: mygo.Point{X: 20, Y: 20}, text: "hi"}
	s.escape()
	if s.text != nil || len(s.hist.annots) != 1 {
		t.Fatalf("Esc did not commit the line: text=%v annots=%d", s.text, len(s.hist.annots))
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
