//go:build darwin

package platform

import "testing"

func TestClampDragPointProtectsNotchCentre(t *testing.T) {
	region := DragGuardRegion{X: 580, Y: 0, Width: 280, Height: 40}
	for _, tt := range []struct {
		name     string
		in, want CGPoint
	}{
		{"centre at top", CGPoint{720, 0}, CGPoint{720, 40}},
		{"left inside edge", CGPoint{580, 1}, CGPoint{580, 40}},
		{"last inside column", CGPoint{859.5, 0}, CGPoint{859.5, 40}},
		{"safe landing", CGPoint{720, 40}, CGPoint{720, 40}},
		{"below corridor", CGPoint{720, 300}, CGPoint{720, 300}},
		{"outside left", CGPoint{579.5, 0}, CGPoint{579.5, 0}},
		{"outside right", CGPoint{860, 0}, CGPoint{860, 0}},
		{"different screen above", CGPoint{720, -1}, CGPoint{720, -1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampDragPoint(tt.in, region); got != tt.want {
				t.Fatalf("drag point %+v became %+v; want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestClampDragPointOnOffsetDisplay(t *testing.T) {
	region := DragGuardRegion{X: -1000, Y: -1080, Width: 280, Height: 40}
	if got := clampDragPoint(CGPoint{-860, -1080}, region); got != (CGPoint{-860, -1040}) {
		t.Fatalf("secondary-screen centre not protected: %+v", got)
	}
	for _, p := range []CGPoint{{720, 0}, {-860, 0}, {-860, -1081}, {-1100, -1080}} {
		if got := clampDragPoint(p, region); got != p {
			t.Fatalf("point on another screen/region changed: %+v -> %+v", p, got)
		}
	}
}

func TestSetDragGuardRegionNoopWhenDisarmed(t *testing.T) {
	SetDragGuardRegion(DragGuardRegion{X: 580, Y: 0, Width: 280, Height: 40})
}

func TestInvalidDragGuardRegionDoesNotAlterInput(t *testing.T) {
	p := CGPoint{720, 0}
	for _, r := range []DragGuardRegion{{}, {X: 580, Width: 280}, {X: 580, Height: 40}, {Width: -1, Height: 40}} {
		if got := clampDragPoint(p, r); got != p {
			t.Fatalf("invalid region changed input: %+v -> %+v", p, got)
		}
		stop, err := StartDragGuard(r)
		if err == nil || stop != nil {
			t.Fatal("invalid geometry must return a setup failure, not claim protection")
		}
	}
}

func TestSingleScreenDragGuardSkipsPasteboard(t *testing.T) {
	origCreate := tapPBCreateFn
	origSync := tapPBSyncFn
	origGetLoc := tapGetLocationFn
	origSetLoc := tapSetLocationFn
	origWarp := tapWarpCursorFn
	origLeft := tapLeftButtonDownFn
	origPB := tapDragPB
	origInited := tapDragPBInited
	origFile := tapDragFile
	origDragBoard := dragBoardName
	origPBCreate := pbCreate
	t.Cleanup(func() {
		tapPBCreateFn = origCreate
		tapPBSyncFn = origSync
		tapGetLocationFn = origGetLoc
		tapSetLocationFn = origSetLoc
		tapWarpCursorFn = origWarp
		tapLeftButtonDownFn = origLeft
		tapDragPB = origPB
		tapDragPBInited = origInited
		tapDragFile = origFile
		dragBoardName = origDragBoard
		pbCreate = origPBCreate
		macEdgeMu.Lock()
		macEdgeState = EdgeGuardState{}
		macEdgeMu.Unlock()
	})

	pbCalls := 0
	var cur CGPoint
	var warped []CGPoint
	leftDown := false
	tapDragPB = 0
	tapDragPBInited = false
	tapDragFile = false
	dragBoardName = 1
	pbCreate = func(uintptr, *uintptr) int32 { return 0 }
	tapPBCreateFn = func(_ uintptr, out *uintptr) int32 {
		pbCalls++
		*out = 42
		return 0
	}
	tapPBSyncFn = func(uintptr) uint32 {
		pbCalls++
		return 0
	}
	tapGetLocationFn = func(uintptr) CGPoint { return cur }
	tapSetLocationFn = func(_ uintptr, p CGPoint) { cur = p }
	tapWarpCursorFn = func(p CGPoint) { warped = append(warped, p) }
	tapLeftButtonDownFn = func() bool { return leftDown }

	single := &dragGuardState{
		region: DragGuardRegion{X: 580, Y: 0, Width: 280, Height: 40, BarrierEnabled: false},
	}
	base := DragGuardPasteboardCalls()

	// Single-screen click, move, and drag into corridor: zero pasteboard calls,
	// while corridor clamping on drag still works.
	cur = CGPoint{720, 200}
	leftDown = true
	handleDragGuardEvent(single, false, kCGEventLeftMouseDown, 1)
	cur = CGPoint{720, 10}
	handleDragGuardEvent(single, false, kCGEventLeftMouseDragged, 1)
	leftDown = false
	handleDragGuardEvent(single, false, kCGEventLeftMouseUp, 1)
	cur = CGPoint{720, 100}
	handleDragGuardEvent(single, false, kCGEventMouseMoved, 1)

	if pbCalls != 0 || DragGuardPasteboardCalls() != base {
		t.Fatalf("single-screen guard touched pasteboard: pbCalls=%d delta=%d", pbCalls, DragGuardPasteboardCalls()-base)
	}
	if len(warped) != 1 || warped[0] != (CGPoint{720, 40}) {
		t.Fatalf("single-screen corridor warp = %+v, want [{720 40}]", warped)
	}

	// Stacked upper-display barrier DOES consult the drag pasteboard.
	stacked := &dragGuardState{
		region: DragGuardRegion{
			X: 580, Y: 0, Width: 280, Height: 40,
			BarrierEnabled: true, BarrierX: 580, BarrierY: 0, BarrierWidth: 280, BarrierPushPt: 80,
		},
	}
	leftDown = true
	handleDragGuardEvent(stacked, false, kCGEventLeftMouseDown, 1)
	handleDragGuardEvent(stacked, false, kCGEventLeftMouseDragged, 1)
	if pbCalls == 0 || DragGuardPasteboardCalls() == base {
		t.Fatal("upper-display barrier did not consult drag pasteboard")
	}
}
