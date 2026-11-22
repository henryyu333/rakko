//go:build windows

package platform

import (
	"testing"
	"unsafe"
)

// TestDragGuardDoesNotMoveCursor pins the Windows corridor to a no-op when no
// upper display barrier is armed: calls are accepted, stop is idempotent, and
// the pointer stays where the user left it.
func TestDragGuardDoesNotMoveCursor(t *testing.T) {
	var before winPoint
	if res, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&before))); res == 0 {
		t.Skip("this session has no desktop cursor to observe")
	}

	r := DragGuardRegion{X: 0, Y: 0, Width: 2000, Height: 2000}
	stop, err := StartDragGuard(r)
	if err != nil {
		t.Fatalf("StartDragGuard: %v", err)
	}
	if stop == nil {
		t.Fatal("StartDragGuard returned nil stop")
	}
	SetDragGuardRegion(DragGuardRegion{X: 100, Y: 0, Width: 500, Height: 48})
	ParkDragGuard(false)
	SetDragPin(r, true)
	SetDragPin(DragGuardRegion{}, false)
	stop()
	stop() // idempotent

	var after winPoint
	if res, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&after))); res == 0 {
		t.Fatal("the drag guard left GetCursorPos unreadable")
	}
	if after != before {
		t.Fatalf("drag guard moved cursor from %+v to %+v", before, after)
	}
}

func TestWinBarrierRegionMixedDPI(t *testing.T) {
	// Lower monitor at 150% DPI (1920x1080 physical -> 1280x720 DIP, origin 0,0)
	// with an upper monitor at 100% DPI (1920x1080 physical above it).
	ms := []screenMonitor{
		{
			bounds: winRect{Left: 0, Top: 0, Right: 1920, Bottom: 1080},
			dpi:    144,
		},
		{
			bounds: winRect{Left: 0, Top: -1080, Right: 1920, Bottom: 0},
			dpi:    96,
		},
	}
	reg := DragGuardRegion{
		BarrierEnabled: true,
		BarrierX:       550,
		BarrierY:       0,
		BarrierWidth:   180,
		BarrierPushPt:  80,
	}
	px := winBarrierRegionIn(ms, reg)
	if !px.BarrierEnabled || px.BarrierX != 825 || px.BarrierY != 0 || px.BarrierWidth != 270 || px.BarrierPushPt != 120 || px.BarrierResetMargin != 30 {
		t.Fatalf("150%% lower barrier scaled to %+v, want X=825 Y=0 W=270 Push=120 Margin=30", px)
	}

	// Lower monitor at 100% DPI below a 150% upper monitor.
	ms2 := []screenMonitor{
		{
			bounds: winRect{Left: 0, Top: 0, Right: 1920, Bottom: 1080},
			dpi:    96,
		},
		{
			bounds: winRect{Left: 0, Top: -1080, Right: 1920, Bottom: 0},
			dpi:    144,
		},
	}
	px2 := winBarrierRegionIn(ms2, DragGuardRegion{
		BarrierEnabled: true,
		BarrierX:       870,
		BarrierY:       0,
		BarrierWidth:   180,
		BarrierPushPt:  80,
	})
	if !px2.BarrierEnabled || px2.BarrierX != 870 || px2.BarrierY != 0 || px2.BarrierWidth != 180 || px2.BarrierPushPt != 80 || px2.BarrierResetMargin != 20 {
		t.Fatalf("100%% lower barrier scaled to %+v, want X=870 Y=0 W=180 Push=80 Margin=20", px2)
	}

	// Physical adjacency in mixed DPI (100% lower + 150% upper leaves a 360-DIP gap
	// in mygo's DIP space, yet touches flush at physical Y=0).
	bx, by, bw, ok := physicalUpperTouchesSegmentIn(ms2, 870, 0, 180)
	if !ok || bx != 870 || by != 0 || bw != 180 {
		t.Fatalf("100%% lower + 150%% upper physical adjacency = (%d,%d,%d,%v), want (870,0,180,true)", bx, by, bw, ok)
	}
	bx, by, bw, ok = physicalUpperTouchesSegmentIn(ms, 550, 0, 180)
	if !ok || bx != 550 || by != 0 || bw != 180 {
		t.Fatalf("150%% lower + 100%% upper physical adjacency = (%d,%d,%d,%v), want (550,0,180,true)", bx, by, bw, ok)
	}
}

func setupWinBarrierTest(t *testing.T, leftDown, oleDrag bool) {
	t.Helper()
	origCursor := winCursorPos
	origLeft := winLeftHeld
	origOLE := winOLEDrag
	t.Cleanup(func() {
		winCursorPos = origCursor
		winLeftHeld = origLeft
		winOLEDrag = origOLE
		winBarrierArmed.Store(false)
		winBarrierParked.Store(false)
		winBarrierRegion.Store(nil)
		winEdgeMu.Lock()
		winEdgeState = EdgeGuardState{}
		winEdgeMu.Unlock()
	})
	winCursorPos = func() (winPoint, bool) { return winPoint{}, false }
	winLeftHeld = func() bool { return leftDown }
	winOLEDrag = func() bool { return oleDrag }
	winBarrierArmed.Store(true)
	winBarrierParked.Store(false)
	winEdgeMu.Lock()
	winEdgeState = EdgeGuardState{}
	winEdgeMu.Unlock()
	reg := DragGuardRegion{
		BarrierEnabled:     true,
		BarrierX:           645,
		BarrierY:           0,
		BarrierWidth:       180,
		BarrierPushPt:      80,
		BarrierResetMargin: 20,
	}
	winBarrierRegion.Store(&reg)
}

func TestWinEdgeBarrierSkipsDoNotLeaveStalePrevY(t *testing.T) {
	setupWinBarrierTest(t, false, false)

	// Start in anchor X range below the junction, move outside anchor X range,
	// cross onto upper display outside anchor X range, then slide horizontally
	// back into anchor X range on the upper display. Must NOT yank to BarrierY.
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 735, Y: 10}); blocked || got != (winPoint{X: 735, Y: 10}) {
		t.Fatalf("step 1 below anchor failed: got=%+v blocked=%v", got, blocked)
	}
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 300, Y: 10}); blocked || got != (winPoint{X: 300, Y: 10}) {
		t.Fatalf("step 2 outside anchor below failed: got=%+v blocked=%v", got, blocked)
	}
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 300, Y: -250}); blocked || got != (winPoint{X: 300, Y: -250}) {
		t.Fatalf("step 3 upper outside anchor failed: got=%+v blocked=%v", got, blocked)
	}
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 735, Y: -250}); blocked || got != (winPoint{X: 735, Y: -250}) {
		t.Fatalf("step 4 horizontal entry on upper display yanked cursor: got=%+v blocked=%v", got, blocked)
	}
}

func TestWinEdgeBarrierBlocksOLEDragAndPassesWindowDrag(t *testing.T) {
	// Any OLE drag (files, text, links) stops at BarrierY until 80px upward push.
	setupWinBarrierTest(t, true, true)
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 735, Y: 40}); blocked || got != (winPoint{X: 735, Y: 40}) {
		t.Fatalf("OLE drag below edge altered: got=%+v blocked=%v", got, blocked)
	}
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 735, Y: -20}); !blocked || got != (winPoint{X: 735, Y: 0}) {
		t.Fatalf("OLE drag first crossing did not stop at BarrierY: got=%+v blocked=%v", got, blocked)
	}
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 735, Y: -50}); !blocked || got != (winPoint{X: 735, Y: 0}) {
		t.Fatalf("OLE drag released early at 50px: got=%+v blocked=%v", got, blocked)
	}
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 735, Y: -30}); blocked || got != (winPoint{X: 735, Y: -30}) {
		t.Fatalf("OLE drag did not release after 80px push: got=%+v blocked=%v", got, blocked)
	}

	// Non-OLE held drag (window move) passes straight across without blocking.
	setupWinBarrierTest(t, true, false)
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 735, Y: 40}); blocked || got != (winPoint{X: 735, Y: 40}) {
		t.Fatalf("window drag below edge altered: got=%+v blocked=%v", got, blocked)
	}
	if got, blocked := evalWinEdgeBarrier(winPoint{X: 735, Y: -20}); blocked || got != (winPoint{X: 735, Y: -20}) {
		t.Fatalf("window drag across junction was blocked: got=%+v blocked=%v", got, blocked)
	}
}
