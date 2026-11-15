package main

// The page capsule hangs under the shell, in the overlay window's transparent
// margin: the shell-shaped mouse gate has to name it or the press falls
// through to the desktop and the global observer folds the panel.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
)

// TestPageCapsuleRowTakesThePress: the page capsule hangs under the shell, in
// the window's transparent margin. The shell-shaped gate (overlayAcceptsMouse)
// must accept a press on the buttons it draws there — the window is topmost
// and takes nothing outside the shapes the gate names, so a miss sends the
// press to the desktop and the global press observer folds the panel.
func TestPageCapsuleRowTakesThePress(t *testing.T) {
	a := parityWindow()
	a.settings = settings.Default()
	a.visible, a.level, a.progress = true, 1, 1
	f := a.desiredFrame()
	view := ui.NewTester(a.view, f.Width, f.Height)
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	// The canvas is the native window's box; the screen box adds its origin.
	a.frame = mygo.Rectangle{X: a.stripCenter() - f.Width/2, Y: a.y, Width: f.Width, Height: f.Height}
	r, ok := view.Find("剪贴板")
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("the page capsule's 剪贴板 seat is not drawn: %v", view.Texts())
	}
	if r.Y < a.fx.h {
		t.Fatalf("the seat at y=%.0f is inside the shell (%.0f tall), not hung below it", r.Y, a.fx.h)
	}
	orig := cursorPoint
	defer func() { cursorPoint = orig }()
	seat := mygo.Point{X: a.frame.X + int(r.X+r.W/2), Y: a.frame.Y + int(r.Y+r.H/2)}
	a.winH = 1
	cursorPoint = func() mygo.Point { return seat }
	if !a.overlayAcceptsMouse() {
		t.Fatalf("a press on the drawn seat %+v passes through the window", seat)
	}
	// The transparent margin beside the capsule stays click-through.
	cursorPoint = func() mygo.Point {
		return mygo.Point{X: seat.X + int(a.fx.w/2) + 40, Y: seat.Y}
	}
	if a.overlayAcceptsMouse() {
		t.Fatal("the frame's transparent margin beside the capsule takes the press")
	}
}
