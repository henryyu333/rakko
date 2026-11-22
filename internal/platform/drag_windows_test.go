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
