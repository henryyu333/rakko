package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/settings"
)

func TestScreenshotTimeoutRestoresMousePassThrough(t *testing.T) {
	displays, capture, dispatch, work, preflight := shotDisplays, shotCaptureDisplay, shotDispatch, shotWork, shotPreflight
	front, cursor, clean, actApp := shotFrontPID, shotCursorPoint, shotCleanTemp, shotActivateApp
	t.Cleanup(func() {
		shotDisplays, shotCaptureDisplay, shotDispatch, shotWork, shotPreflight = displays, capture, dispatch, work, preflight
		shotFrontPID, shotCursorPoint, shotCleanTemp, shotActivateApp = front, cursor, clean, actApp
	})

	a := &shelf{
		settings:     settings.Default(),
		shortcutReg:  map[shortcutKind]string{},
		shortcutBusy: map[shortcutKind]bool{},
	}
	a.syncMousePassThrough()
	if !a.ignoringMouse {
		t.Fatal("precondition: idle shelf should ignore mouse")
	}

	shotPreflight = func() bool { return true }
	shotFrontPID = func() int { return 0 }
	shotCursorPoint = func() mygo.Point { return mygo.Point{} }
	shotCleanTemp = func() {}
	shotActivateApp = func(int) bool { return true }
	shotDisplays = func() []mygo.Display {
		return []mygo.Display{{ID: 1, Bounds: mygo.Rectangle{Width: 800, Height: 600}, WorkArea: mygo.Rectangle{Width: 800, Height: 600}, ScaleFactor: 1, Primary: true}}
	}
	shotCaptureDisplay = func(_ int, _ string) error {
		return context.DeadlineExceeded
	}
	shotDispatch = func(fn func()) { fn() }
	shotWork = func(fn func()) { fn() }

	a.startScreenshotOnMain()
	if a.shot != nil {
		t.Fatalf("a.shot != nil after timed-out capture")
	}
	if !a.ignoringMouse {
		t.Fatalf("a.ignoringMouse = false after failed capture, want true")
	}
}

func TestWriteDiagnosticsIncludesRequiredStateAndStacks(t *testing.T) {
	origDispatch := diagDispatch
	t.Cleanup(func() { diagDispatch = origDispatch })
	diagDispatch = func(fn func()) { fn() }

	a := &shelf{
		visible:       true,
		peeking:       false,
		ignoringMouse: false,
		shortcutReg:   map[shortcutKind]string{shortcutToggle: "Option+Space"},
		shortcutBusy:  map[shortcutKind]bool{shortcutToggle: false},
	}

	dir := t.TempDir()
	path, err := writeDiagnostics(a, dir)
	if err != nil {
		t.Fatalf("writeDiagnostics: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	text := string(data)
	for _, want := range []string{
		"MainThreadResponsive: true",
		"IgnoringMouse (a.ignoringMouse): false",
		"NativeIgnoresMouse (ignoresMouseEvents): false",
		"Visible (a.visible): true",
		"Peeking (a.peeking): false",
		"ShotActive (a.shot != nil): false",
		`accel="Option+Space"`,
		"--- Goroutine Stacks ---",
		"TestWriteDiagnosticsIncludesRequiredStateAndStacks",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("diagnostics output missing %q:\n%s", want, text)
		}
	}

	// Also verify timeout path when main thread is unresponsive.
	diagDispatch = func(func()) {} // drops callback
	start := time.Now()
	path2, err := writeDiagnostics(a, dir)
	if err != nil {
		t.Fatalf("writeDiagnostics unresponsive: %v", err)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("expected ~1s timeout when main thread unresponsive")
	}
	data2, _ := os.ReadFile(path2)
	if !strings.Contains(string(data2), "MainThreadResponsive: false") {
		t.Fatalf("expected MainThreadResponsive: false, got:\n%s", string(data2))
	}
}
