//go:build darwin

package main

// The invariant behind issue #5: whenever the overlay changes the native
// frame, the canvas already presented must be blank. mygo presents a
// resized surface's previous canvas stretched into the new frame for one
// display frame, so resizing over the idle island capsule (or a drawn
// shell) flashes it enlarged. Run with RAKKO_BASKET_SMOKE=1 through the
// native gate.

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

func TestSmokeOpeningResizesOnABlankFrame(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := redesignShelf(t)
	islandStyle(t, a, settings.ShellStyleIsland)
	motionClosed(a)
	time.Sleep(400 * time.Millisecond)
	var idle mygo.Rectangle
	onMain(func() {
		a.syncWindowFrame()
		idle = a.win.Bounds()
	})
	if idle.Width <= 0 || idle.Height <= 0 {
		t.Fatalf("the idle island has no native frame: %+v", idle)
	}

	type resize struct {
		frame mygo.Rectangle
		blank bool
	}
	var mu sync.Mutex
	var frames []bool
	var resizes []resize
	onMain(func() {
		frameTrace = func(_, _ float32, _ mygo.Rectangle, blank bool) {
			mu.Lock()
			frames = append(frames, blank)
			mu.Unlock()
		}
		setNativeWindowFrame = func(h uintptr, x, y, w, hgt int) {
			mu.Lock()
			lastBlank := len(frames) > 0 && frames[len(frames)-1]
			resizes = append(resizes, resize{
				frame: mygo.Rectangle{X: x, Y: y, Width: w, Height: hgt},
				blank: lastBlank,
			})
			mu.Unlock()
			mac.SetWindowFrame(h, x, y, w, hgt)
		}
	})
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		onMain(func() {
			frameTrace = nil
			setNativeWindowFrame = mac.SetWindowFrame
		})
	}
	defer restore()

	stop := startMotionTicker(a)
	defer stop()
	onMain(func() { a.show(false) })
	if !waitMain(3*time.Second, func() bool {
		return a.progress >= 0.999 && !a.fx.openMoving
	}) {
		t.Fatal("the panel never settled open")
	}
	onMain(func() { a.hide(hidePassive) })
	if !waitMain(4*time.Second, func() bool {
		return !a.visible && a.frameHold == (mygo.Rectangle{}) && a.closeAt.IsZero() && !a.fx.openMoving
	}) {
		t.Fatal("the panel never folded back")
	}
	time.Sleep(400 * time.Millisecond)
	onMain(a.syncWindowFrame)
	restore()

	mu.Lock()
	defer mu.Unlock()
	if len(resizes) == 0 {
		t.Fatalf("the opening never changed the native frame from %+v", idle)
	}
	if resizes[0].frame == idle {
		t.Fatalf("the first resize kept the idle frame %+v", idle)
	}
	for i, r := range resizes {
		if !r.blank {
			t.Fatalf("resize %d to %+v landed on a drawn canvas: the previous frame was not blank (frames %v)", i, r.frame, frames)
		}
	}
	t.Logf("open/close cycle: %d native resizes, every one after a blank frame", len(resizes))
}
