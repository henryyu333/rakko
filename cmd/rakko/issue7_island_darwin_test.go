package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/platform"
	"rakko/internal/settings"
)

// islandBlankShelf is an island-style shelf with a native handle, an open
// panel to grow into, and the native seams recorded.
func islandBlankShelf(t *testing.T, occluded bool) (*shelf, *[]mygo.Rectangle) {
	t.Helper()
	stubKeyboard(t)
	a, _ := levelShelf(t)
	a.win = &mygo.Window{}
	a.winH = 1
	a.dispBounds = mygo.Rectangle{X: 0, Y: 0, Width: 1512, Height: 982}
	a.dispWork = mygo.Rectangle{X: 0, Y: 25, Width: 1512, Height: 957}
	a.hasNotch, a.notchX, a.notchY, a.notchW, a.notchH = false, 696, 0, 120, 30
	a.island, a.islandTop = true, islandTopGap
	a.settings.ShellStyle = settings.ShellStyleIsland
	a.idleIslandDrawn = true
	a.level = 1
	a.visible = true // the target frame is the panel's: bigger than the capsule
	// A stand-in for the resting capsule window: already shown and on its
	// box, so no native window is created or touched.
	a.mainIdle = &mainIdleCapsule{win: &mygo.Window{}, frame: a.islandRect(), shown: true}

	origOcc, origSet, origCursor := windowOccluded, setNativeWindowFrame, cursorPoint
	t.Cleanup(func() { windowOccluded, setNativeWindowFrame, cursorPoint = origOcc, origSet, origCursor })
	cursorPoint = func() mygo.Point { return mygo.Point{X: 756, Y: 10} }
	origAccept, origLayer := setOverlayAcceptsMouse, setWindowLayer
	t.Cleanup(func() { setOverlayAcceptsMouse, setWindowLayer = origAccept, origLayer })
	setOverlayAcceptsMouse = func(uintptr, bool) {}
	setWindowLayer = func(uintptr, platform.Layer) {}
	windowOccluded = func(uintptr) bool { return occluded }
	var sets []mygo.Rectangle
	setNativeWindowFrame = func(_ uintptr, x, y, w, h int) {
		sets = append(sets, mygo.Rectangle{X: x, Y: y, Width: w, Height: h})
	}
	return a, &sets
}

func TestIslandDisplayDisconnectOrOccludedDoesNotDeadlockBlankFrame(t *testing.T) {
	capsule := mygo.Rectangle{X: 696, Y: 7, Width: 120, Height: 30}

	t.Run("occluded window resizes at once", func(t *testing.T) {
		a, sets := islandBlankShelf(t, true)
		a.frame = capsule
		a.blankQueued, a.blankFrame, a.blankQueuedAt = true, true, time.Now()
		a.syncWindowFrame()
		if len(*sets) != 1 || a.frame != (*sets)[0] || a.frame == capsule {
			t.Fatalf("occluded window did not resize: sets=%v frame=%v", *sets, a.frame)
		}
		if a.blankFrame || a.blankQueued || a.blankReady || !a.blankQueuedAt.IsZero() {
			t.Fatalf("blank handshake left behind: %+v", a)
		}

		a, _ = islandBlankShelf(t, true)
		a.visible = false
		a.frame = capsule
		a.beginClosedPeekBlank()
		if a.blankQueued || a.blankFrame {
			t.Fatal("beginClosedPeekBlank queued a blank frame for an occluded window")
		}
	})

	t.Run("window left on the unplugged display moves at once", func(t *testing.T) {
		a, sets := islandBlankShelf(t, false)
		a.frame = mygo.Rectangle{X: 3140, Y: 7, Width: 120, Height: 30} // on the gone 1920,0 2560x1440 display
		a.syncWindowFrame()
		if len(*sets) != 1 {
			t.Fatalf("native frame not set (waiting for a blank frame?): %v", *sets)
		}
		if !a.frameOnCurrentDisplay() || a.blankQueued || a.blankFrame {
			t.Fatalf("window still off the built-in display: frame=%v queued=%v", a.frame, a.blankQueued)
		}
	})

	t.Run("a healthy window still waits for the blank frame", func(t *testing.T) {
		a, sets := islandBlankShelf(t, false)
		a.frame = capsule
		a.syncWindowFrame()
		if len(*sets) != 0 || !a.blankQueued || !a.blankFrame || a.blankQueuedAt.IsZero() {
			t.Fatalf("expected a queued blank frame and no resize: sets=%v queued=%v", *sets, a.blankQueued)
		}
	})

	t.Run("a stuck wait times out", func(t *testing.T) {
		a, sets := islandBlankShelf(t, false)
		a.frame = capsule
		a.blankQueued, a.blankFrame = true, true
		a.blankQueuedAt = time.Now().Add(-2 * blankQueueTimeout)
		a.syncWindowFrame()
		if len(*sets) != 1 || a.blankQueued || a.blankFrame {
			t.Fatalf("timed-out wait did not release the resize: sets=%v queued=%v", *sets, a.blankQueued)
		}
	})

	t.Run("system wake clears the handshake and the cached frame", func(t *testing.T) {
		// Without a native window object only the bookkeeping runs: the
		// stale handshake and the cached frame (so the next sync places the
		// window again rather than trusting where it was before the sleep).
		a, _ := islandBlankShelf(t, false)
		a.win, a.mainIdle = nil, nil
		a.frame = capsule
		a.blankQueued, a.blankFrame, a.blankReady, a.blankQueuedAt = true, true, true, time.Now()
		origNearest := displayNearest
		t.Cleanup(func() { displayNearest = origNearest })
		displayNearest = func(mygo.Point) mygo.Display {
			b := mygo.Rectangle{X: 0, Y: 0, Width: 1512, Height: 982}
			return mygo.Display{ID: 1, Bounds: b, WorkArea: mygo.Rectangle{Y: 25, Width: 1512, Height: 957}, ScaleFactor: 2, Primary: true}
		}
		a.handleSystemWake()
		if a.blankFrame || a.blankQueued || a.blankReady || !a.blankQueuedAt.IsZero() {
			t.Fatalf("wake left blank state behind: %+v", a)
		}
		if a.frame != (mygo.Rectangle{}) {
			t.Fatalf("wake kept the cached frame %v", a.frame)
		}
	})
}
