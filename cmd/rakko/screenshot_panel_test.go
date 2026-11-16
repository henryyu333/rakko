package main

import (
	"image"
	"image/color"
	stdpng "image/png"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/platform"
)

func TestPanelFullyOpenForCapture(t *testing.T) {
	a := &shelf{visible: true}
	a.fx.openK = 1
	if !a.panelFullyOpenForCapture() {
		t.Fatal("fully open stable panel should stay open")
	}
	for name, mutate := range map[string]func(*shelf){
		"hidden":  func(s *shelf) { s.visible = false },
		"peeking": func(s *shelf) { s.peeking = true },
		"closing": func(s *shelf) { s.closeAt = time.Now().Add(time.Second) },
		"opening": func(s *shelf) { s.fx.openK = 0.8 },
	} {
		s := &shelf{visible: true}
		s.fx.openK = 1
		mutate(s)
		if s.panelFullyOpenForCapture() {
			t.Fatalf("%s panel must not count as fully open", name)
		}
	}
}

func TestScreenshotKeepsFullyOpenPanelAndBlocksOverlayReorder(t *testing.T) {
	displays, capture, dispatch, work := shotDisplays, shotCaptureDisplay, shotDispatch, shotWork
	front, preflight, openWin, clean := shotFrontPID, shotPreflight, shotOpenWindow, shotCleanTemp
	cursor, normalWin, keyWin, actSelf := shotCursorPoint, shotNormalWindow, shotKeyWindow, shotActivateSelf
	setLevel, orderOverlay, after := shotSetWindowLayer, shotOrderOverlay, shotAfter
	settle := shotPanelSettle
	oldAccept, oldLayer := setOverlayAcceptsMouse, setWindowLayer
	oldActivate, oldActive, oldReg, oldUnreg := activateApp, appIsActive, registerKey, unregisterKey
	t.Cleanup(func() {
		shotDisplays, shotCaptureDisplay, shotDispatch, shotWork = displays, capture, dispatch, work
		shotFrontPID, shotPreflight, shotOpenWindow, shotCleanTemp = front, preflight, openWin, clean
		shotCursorPoint, shotNormalWindow, shotKeyWindow, shotActivateSelf = cursor, normalWin, keyWin, actSelf
		shotSetWindowLayer, shotOrderOverlay, shotAfter = setLevel, orderOverlay, after
		shotPanelSettle = settle
		setOverlayAcceptsMouse, setWindowLayer = oldAccept, oldLayer
		activateApp, appIsActive, registerKey, unregisterKey = oldActivate, oldActive, oldReg, oldUnreg
	})

	shotDispatch = func(fn func()) { fn() }
	shotWork = func(fn func()) { fn() }
	shotDisplays = func() []mygo.Display {
		return []mygo.Display{{ID: 1, Bounds: mygo.Rectangle{Width: 1000, Height: 800}, WorkArea: mygo.Rectangle{Width: 1000, Height: 800}, ScaleFactor: 1, Primary: true}}
	}
	shotCursorPoint = func() mygo.Point { return mygo.Point{X: 100, Y: 100} }
	shotNormalWindow = func() []platform.ScreenWindow { return nil }
	shotKeyWindow = func(uintptr) bool { return false }
	shotActivateSelf = func() {}
	shotSetWindowLayer = func(uintptr, platform.Layer) {}
	shotOrderOverlay = func(uintptr) {}
	shotAfter = func(time.Duration, func()) *time.Timer { return time.NewTimer(time.Hour) }
	shotPreflight = func() bool { return true }
	shotFrontPID = func() int { return 4242 }
	shotOpenWindow = func(*shotSession, int) {}
	shotCleanTemp = func() { _ = os.MkdirAll(shotTempDir(), 0o700) }
	shotPanelSettle = 5 * time.Second // would fail fast if a fully open panel tried to sleep
	shotCaptureDisplay = func(_ int, path string) error {
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		defer f.Close()
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		img.Set(0, 0, color.RGBA{A: 255})
		return stdpng.Encode(f, img)
	}

	var acceptCalls, layerCalls, handedBack int
	setOverlayAcceptsMouse = func(uintptr, bool) { acceptCalls++ }
	setWindowLayer = func(uintptr, platform.Layer) { layerCalls++ }
	appIsActive = func() bool { return true }
	activateApp = func(int) bool { handedBack++; return true }
	registerKey = func(string, func()) error { return nil }
	unregisterKey = func(string) {}

	a := &shelf{visible: true, escHeld: true, spaceHeld: true}
	a.fx.openK = 1
	a.startScreenshotOnMain()
	if !a.visible {
		t.Fatal("fully open panel was hidden by screenshot start")
	}
	if a.escHeld || a.spaceHeld {
		t.Fatal("screenshot start must release global Esc/Space while panel stays open")
	}
	if a.shot == nil {
		t.Fatal("screenshot session was not installed")
	}

	// While a.shot != nil, pointer gate / layer sync must not touch native window order.
	a.syncMousePassThrough()
	a.syncPointerGate()
	if acceptCalls != 0 || layerCalls != 0 {
		t.Fatalf("gate/layer changed during screenshot: accept=%d layer=%d", acceptCalls, layerCalls)
	}

	// Closing via Esc must keep panel open, restore keys, and not hand back to prevPID.
	a.shot.close(true)
	if !a.visible {
		t.Fatal("panel closed after screenshot close")
	}
	if !a.escHeld || !a.spaceHeld {
		t.Fatal("panel keys were not restored after screenshot close")
	}
	if handedBack != 0 {
		t.Fatalf("handBackApp called %d times while panel stayed open", handedBack)
	}
}
