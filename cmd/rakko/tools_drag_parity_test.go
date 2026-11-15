package main

import (
	"testing"

	"github.com/egoist/mygo"

	"rakko/internal/layout"
)

func TestToolsDragActiveKeepsOverlayAcceptsMouse(t *testing.T) {
	a := parityWindow()
	oldCursor := cursorPoint
	defer func() { cursorPoint = oldCursor }()

	// Park pointer outside the panel shell.
	far := mygo.Point{X: a.dispBounds.Width - 5, Y: a.dispBounds.Height - 5}
	cursorPoint = func() mygo.Point { return far }

	a.setTab(pageTools)
	a.ensurePages()
	a.winH = 1
	a.visible = true
	a.fx.openK = 1
	a.fx.w, a.fx.h = a.shellSize(1, 0, 0, 0, 0)
	p := a.toolsPage()

	if a.overlayAcceptsMouse() {
		t.Fatal("idle panel outside shell unexpectedly accepts mouse")
	}

	p.press = &toolPress{id: "zip", dragging: true}
	if got := a.overlayAcceptsMouse(); got != toolsDragKeepsMouse {
		t.Fatalf("during active card drag outside panel: overlayAcceptsMouse=%v, want %v", got, toolsDragKeepsMouse)
	}

	p.press = nil
	p.rs = &toolResize{id: "playing"}
	if got := a.overlayAcceptsMouse(); got != toolsDragKeepsMouse {
		t.Fatalf("during active resize outside panel: overlayAcceptsMouse=%v, want %v", got, toolsDragKeepsMouse)
	}

	p.rs = nil
	if a.overlayAcceptsMouse() {
		t.Fatal("after drag ended outside panel: overlayAcceptsMouse stayed true")
	}
}

func TestSyncDragPreviewQuantizesTiltOnWindows(t *testing.T) {
	a := parityWindow()
	a.winH = 1
	oldDisplays := pinDisplays
	defer func() { pinDisplays = oldDisplays }()
	pinDisplays = func() []mygo.Display {
		return []mygo.Display{{ID: 1, Primary: true, Bounds: a.dispBounds, WorkArea: a.dispBounds}}
	}
	pv := &dragPreview{it: &pinItem{owner: a, cardID: "zip", span: 1, cardW: pinCardW, frame: mygo.Rectangle{X: 100, Y: 100, Width: pinCardW, Height: pinCardH}}}
	cursor := mygo.Point{X: 300, Y: 300}
	a.syncDragPreview(pv, "zip", 1, pinCardW, 20, 20, 100, 84, cursor, false, 2.31)
	wantTilt := float32(2.31)
	if dragPreviewSkipRedundantPaint {
		wantTilt = 2.25
	}
	if pv.it.tilt != wantTilt {
		t.Fatalf("syncDragPreview tilt=%v, want %v", pv.it.tilt, wantTilt)
	}
}

func TestLayoutWriteNoSyncRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := layout.Default()
	want.MoveCard(want.Cards[len(want.Cards)-1].ID, 0)
	if err := layout.WriteNoSync(dir, want); err != nil {
		t.Fatalf("WriteNoSync: %v", err)
	}
	got, err := layout.Load(dir)
	if err != nil {
		t.Fatalf("Load after WriteNoSync: %v", err)
	}
	if len(got.Cards) != len(want.Cards) || got.Cards[0].ID != want.Cards[0].ID {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got.Cards, want.Cards)
	}
}
