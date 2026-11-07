//go:build darwin

package main

// Real-window proof for the screenshot overlay: a synthetic freeze (the
// test never needs screen-recording permission for its own pictures), the
// per-display overlay windows, a real HID drag that selects a region, a
// window screenshot, and a real Esc that tears the session down. Also
// measures the real `screencapture -D` freeze so the report can state what
// the user waits for. Run with RAKKO_BASKET_SMOKE=1 through native-gate.sh.

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"

	"rakko/internal/mac"
)

const screenshotShotDir = "/tmp/rakko-shot/smoke"

// waitShot polls the session state on the main thread.
func waitShot(t *testing.T, a *shelf, timeout time.Duration, done func(*shotSession) bool) *shotSession {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var s *shotSession
		var ok bool
		onMain(func() {
			s = a.shot
			ok = s != nil && done(s)
		})
		if ok {
			return s
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("screenshot session did not reach the expected state in %s", timeout)
	return nil
}

func TestScreenshotSmokeOverlay(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := redesignShelf(t)
	d := mygo.Screen.PrimaryDisplay()
	w, h := d.Bounds.Width, d.Bounds.Height
	scale := d.ScaleFactor
	if scale <= 0 {
		scale = 1
	}
	pixW := int(float64(w) * scale)
	pixH := int(float64(h) * scale)

	// A synthetic freeze: a wallpaper and two windows, captured in-process.
	// The capture seam replaces only the subprocess, so the rest of the
	// session (decode, bitmap, window, crop) is the production path.
	oldCapture, oldPre := shotCaptureDisplay, shotPreflight
	t.Cleanup(func() { shotCaptureDisplay, shotPreflight = oldCapture, oldPre })
	shotPreflight = func() bool { return true }
	shotCaptureDisplay = func(index int, path string) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		writeShotTestPNG(t, path, pixW, pixH, color.RGBA{42, 54, 66, 255})
		return nil
	}
	if err := os.MkdirAll(screenshotShotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	onMain(func() { a.startScreenshotOnMain() })
	s := waitShot(t, a, 5*time.Second, func(s *shotSession) bool { return s.installed })
	if len(s.displays) == 0 {
		t.Fatal("no frozen display")
	}
	od := s.displays[0]
	if od.win == nil {
		t.Fatal("overlay window missing")
	}
	var num int
	onMain(func() { num = mac.WindowNumber(od.win.NativeHandle()) })
	if num == 0 {
		t.Fatal("overlay window has no CGWindowID")
	}
	for _, w := range ownSmokeWindows(t) {
		if w.ID == num {
			t.Logf("overlay in WindowServer: %+v", w)
		}
	}
	t.Logf("overlay display %d: bounds=%+v pix=%dx%d window=%d capture=%dms", od.ID, od.Bounds, od.pixW, od.pixH, num, s.captureMS)

	// Real HID drag inside the display: move, press, drag, release.
	target := mygo.Rectangle{X: d.Bounds.X + w/4, Y: d.Bounds.Y + h/4, Width: w / 3, Height: h / 3}
	postShotMouse(t, float64(target.X), float64(target.Y), num, 5)
	time.Sleep(150 * time.Millisecond)
	postShotMouse(t, float64(target.X), float64(target.Y), num, 1)
	for i := 1; i <= 8; i++ {
		postShotMouse(t,
			float64(target.X)+float64(target.Width)*float64(i)/8,
			float64(target.Y)+float64(target.Height)*float64(i)/8, num, 6)
		time.Sleep(30 * time.Millisecond)
	}
	postShotMouse(t, float64(target.X+target.Width), float64(target.Y+target.Height), num, 2)
	time.Sleep(300 * time.Millisecond)

	var hasSel bool
	var sel mygo.Rectangle
	onMain(func() {
		hasSel, sel = s.hasSel, s.sel
	})
	if !hasSel {
		t.Fatal("the real drag did not make a selection")
	}
	if sel.Width < target.Width/2 || sel.Height < target.Height/2 {
		t.Fatalf("selection = %+v, drag target = %+v", sel, target)
	}

	// The output crop is the production path, at the selection's pixels.
	var pngLen int
	onMain(func() {
		png, err := s.outputPNG(true)
		if err != nil {
			t.Errorf("output: %v", err)
			return
		}
		pngLen = len(png)
	})
	if pngLen == 0 {
		t.Fatal("no PNG produced")
	}
	shotWindow(t, od.win, "overlay-selection")
	t.Logf("selection %+v, annotated PNG %d bytes", sel, pngLen)

	// A real Esc while the overlay is key: the last step of the ladder
	// leaves the selection, then closes.
	postEscape(t)
	postEscape(t)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var live bool
		onMain(func() { live = a.shot != nil })
		if !live {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("the session survived Esc")
}

// TestScreenshotRealCaptureTiming measures the actual system freeze, the
// only part of the flow that waits on a subprocess. Without the
// screen-recording grant the test says so and leaves the timing unproven.
func TestScreenshotRealCaptureTiming(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native capture timing")
	}
	if !mac.ScreenCapturePreflight() {
		t.Skip("no screen-recording grant: real capture timing unproven")
	}
	dir := t.TempDir()
	var times []time.Duration
	for i := 0; i < 3; i++ {
		path := filepath.Join(dir, fmt.Sprintf("cap-%d.png", i))
		start := time.Now()
		if err := captureShotDisplayFile(1, path); err != nil {
			t.Fatalf("screencapture: %v", err)
		}
		times = append(times, time.Since(start))
		img, err := decodeShotPNG(path)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if img.Bounds().Empty() {
			t.Fatal("empty capture")
		}
	}
	t.Logf("screencapture -D 1: %v, %v, %v", times[0], times[1], times[2])
}

// postShotMouse posts one real HID mouse event per type at a screen point,
// proving first that WindowServer routes the point to this capture's own
// overlay window. The screenshot overlay sits above the menu bar and the
// Dock, so (unlike postIslandMouse) it covers any point of the display,
// including the ones the notch shell and the Dock would otherwise take.
func postShotMouse(t *testing.T, x, y float64, overlay int, events ...uint32) {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreGraphics: %v", err)
	}
	var (
		create  func(source uintptr, typ uint32, p mac.CGPoint, button uint32) uintptr
		post    func(tap uint32, ev uintptr)
		release func(cf uintptr)
	)
	purego.RegisterLibFunc(&create, lib, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreFoundation: %v", err)
	}
	purego.RegisterLibFunc(&release, libCF, "CFRelease")
	button := uint32(0)
	for _, typ := range events {
		if typ == 3 || typ == 4 || typ == 27 {
			button = 1
		}
		var hit int
		onMain(func() {
			primary := mygo.Screen.PrimaryDisplay()
			hit = mac.WindowAtPoint(x, y, float64(primary.Bounds.Y+primary.Bounds.Height))
		})
		if hit != overlay {
			t.Fatalf("refusing mouse event at %v,%v: WindowServer routes it to window %d, not the overlay %d", x, y, hit, overlay)
		}
		ev := create(0, typ, mac.CGPoint{X: x, Y: y}, button)
		if ev == 0 {
			t.Fatal("CGEventCreateMouseEvent returned nil")
		}
		post(0, ev)
		release(ev)
		time.Sleep(20 * time.Millisecond)
	}
}
