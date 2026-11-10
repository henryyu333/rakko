//go:build darwin

package main

// The invariant behind issue #5, narrowed to the one canvas mygo actually
// stretches visibly: the island style's resting capsule. mygo presents a
// resized surface's previous canvas stretched into the new frame for one
// display frame, so an opening out of that capsule flashes it enlarged.
// The blank frame exists only for that opening: resizing over the notch's
// empty strip, a peek pill, the open panel or the fold's last frame must
// land directly, or every animation shows one blank frame and every resize
// arrives a frame late. Run with RAKKO_BASKET_SMOKE=1 through the native
// gate.

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

type nativeResize struct {
	frame           mygo.Rectangle
	afterBlankFrame bool
	covered         bool
}

// frameNote is what one built frame left on the canvas. blank is the
// view's own flag: the notch's resting strip is legitimately blank. move
// marks the transparent frame a native resize asked for (the blankFrame
// request), which is the frame that must not appear before a resize.
type frameNote struct {
	blank bool
	move  bool
}

// blankTrace records every built frame and every native resize the overlay
// asks for, plus whether the resting capsule's picture window was up when
// the resize landed.
type blankTrace struct {
	mu      sync.Mutex
	frames  []frameNote
	resizes []nativeResize
}

func traceBlankFrames(t *testing.T, a *shelf) *blankTrace {
	t.Helper()
	tr := &blankTrace{}
	onMain(func() {
		frameTrace = func(_, _ float32, _ mygo.Rectangle, blank bool) {
			tr.mu.Lock()
			// A blank frame with a queued blank frame request is the
			// transparent frame drawn for a resize; an idle notch strip
			// marks itself blank without one.
			tr.frames = append(tr.frames, frameNote{blank: blank, move: blank && a.blankQueued})
			tr.mu.Unlock()
		}
		setNativeWindowFrame = func(h uintptr, x, y, w, hgt int) {
			tr.mu.Lock()
			last := len(tr.frames) > 0 && tr.frames[len(tr.frames)-1].move
			tr.resizes = append(tr.resizes, nativeResize{
				frame:           mygo.Rectangle{X: x, Y: y, Width: w, Height: hgt},
				afterBlankFrame: last,
				covered:         a.mainIdle != nil && a.mainIdle.shown,
			})
			tr.mu.Unlock()
			mac.SetWindowFrame(h, x, y, w, hgt)
		}
	})
	t.Cleanup(func() {
		onMain(func() {
			frameTrace = nil
			setNativeWindowFrame = mac.SetWindowFrame
		})
	})
	return tr
}

// reset starts a fresh observation window.
func (tr *blankTrace) reset() {
	onMain(func() {
		tr.mu.Lock()
		tr.frames, tr.resizes = nil, nil
		tr.mu.Unlock()
	})
}

// take copies what was observed so far.
func (tr *blankTrace) take() ([]frameNote, []nativeResize) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]frameNote(nil), tr.frames...), append([]nativeResize(nil), tr.resizes...)
}

// moveBlankCount counts the transparent frames requested for a resize. The
// notch's empty idle strip is not one of them.
func moveBlankCount(frames []frameNote) int {
	n := 0
	for _, f := range frames {
		if f.move {
			n++
		}
	}
	return n
}

// waitRestingFold waits until the panel folded away, its envelope is gone
// and the close fade retired.
func waitRestingFold(t *testing.T, a *shelf) {
	t.Helper()
	if !waitMain(4*time.Second, func() bool {
		return !a.visible && a.frameHold == (mygo.Rectangle{}) && a.closeAt.IsZero() && !a.fx.openMoving
	}) {
		t.Fatal("the panel never folded back")
	}
	// Let the resize the fold asked for (and the capsule frame after it)
	// land before the observation window closes.
	time.Sleep(400 * time.Millisecond)
	onMain(a.syncWindowFrame)
}

func runOpenCloseCycle(t *testing.T, a *shelf) {
	t.Helper()
	onMain(func() { a.show(false) })
	if !waitMain(3*time.Second, func() bool {
		return a.progress >= 0.999 && !a.fx.openMoving
	}) {
		t.Fatal("the panel never settled open")
	}
	onMain(func() { a.hide(hidePassive) })
	waitRestingFold(t, a)
}

// assertNoResizeBlanks fails if any native resize in the window followed a
// transparent frame drawn for a resize, or if nothing resized at all.
func assertNoResizeBlanks(t *testing.T, what string, frames []frameNote, resizes []nativeResize) {
	t.Helper()
	if n := moveBlankCount(frames); n != 0 {
		t.Fatalf("%s built %d resize blank frames (frames %v, resizes %+v)", what, n, frames, resizes)
	}
	if len(resizes) == 0 {
		t.Fatalf("%s never resized the native frame", what)
	}
	for i, r := range resizes {
		if r.afterBlankFrame {
			t.Fatalf("%s: resize %d to %+v followed a resize blank frame", what, i, r.frame)
		}
	}
	t.Logf("%s: %d native resizes, 0 resize blank frames", what, len(resizes))
}

func TestSmokeOpeningResizesOnABlankFrame(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}

	// (a) Island style: the opening out of the resting capsule is the one
	// resize that must land after a transparent frame, with the capsule's
	// picture covering it (issue #5).
	t.Run("island idle capsule", func(t *testing.T) {
		a := redesignShelf(t)
		islandStyle(t, a, settings.ShellStyleIsland)
		motionClosed(a)
		time.Sleep(400 * time.Millisecond)
		var idle mygo.Rectangle
		onMain(func() { idle = a.win.Bounds() })
		if idle.Width <= 0 || idle.Height <= 0 {
			t.Fatalf("the idle island has no native frame: %+v", idle)
		}
		tr := traceBlankFrames(t, a)
		tr.reset()
		stop := startMotionTicker(a)
		defer stop()
		onMain(func() { a.show(false) })
		if !waitMain(3*time.Second, func() bool {
			return a.progress >= 0.999 && !a.fx.openMoving
		}) {
			t.Fatal("the panel never settled open")
		}
		onMain(func() { a.hide(hidePassive) })
		waitRestingFold(t, a)

		frames, resizes := tr.take()
		if len(resizes) == 0 {
			t.Fatalf("the opening never changed the native frame from %+v", idle)
		}
		if resizes[0].frame == idle {
			t.Fatalf("the first resize kept the idle frame %+v", idle)
		}
		if !resizes[0].afterBlankFrame {
			t.Fatalf("the opening resize to %+v landed on the idle capsule: no blank frame preceded it (frames %v)", resizes[0].frame, frames)
		}
		if !resizes[0].covered {
			t.Fatalf("the opening resize to %+v had no resting capsule over its blank frame", resizes[0].frame)
		}
		t.Logf("island open/close: %d native resizes, the opening after a blank frame covered by the capsule", len(resizes))
	})

	// (b) Notch style: the resting strip is empty but every other canvas
	// is drawn content, so no resize may wait for a transparent frame.
	// Both routes into the panel are covered: a click on the empty strip
	// and a click while the peek pill is up (the pill was one of the
	// frames the released regression blanked). The notch's empty idle
	// strip is content, not a resize blank; moveBlankCount excludes it.
	t.Run("notch open close", func(t *testing.T) {
		a := redesignShelf(t)
		islandStyle(t, a, settings.ShellStyleNotch)
		motionClosed(a)
		time.Sleep(400 * time.Millisecond)
		tr := traceBlankFrames(t, a)
		stop := startMotionTicker(a)
		defer stop()

		// From the resting strip.
		tr.reset()
		runOpenCloseCycle(t, a)
		frames, resizes := tr.take()
		assertNoResizeBlanks(t, "notch open/close from rest", frames, resizes)

		// From the peek pill, as a hover followed by a click opens it.
		peekShelf(a)
		time.Sleep(300 * time.Millisecond)
		tr.reset()
		runOpenCloseCycle(t, a)
		frames, resizes = tr.take()
		assertNoResizeBlanks(t, "notch open/close from peek", frames, resizes)
	})

	// (c) Island style after the panel is open: the fold-end shrink is not
	// the resting capsule's canvas, so it must land directly too.
	t.Run("island open non-idle resize", func(t *testing.T) {
		a := redesignShelf(t)
		islandStyle(t, a, settings.ShellStyleIsland)
		motionClosed(a)
		time.Sleep(400 * time.Millisecond)
		tr := traceBlankFrames(t, a)
		stop := startMotionTicker(a)
		defer stop()
		onMain(func() { a.show(false) })
		if !waitMain(3*time.Second, func() bool {
			return a.progress >= 0.999 && !a.fx.openMoving
		}) {
			t.Fatal("the panel never settled open")
		}
		tr.reset()
		onMain(func() { a.hide(hidePassive) })
		waitRestingFold(t, a)

		frames, resizes := tr.take()
		assertNoResizeBlanks(t, "island fold from open", frames, resizes)
	})
}
