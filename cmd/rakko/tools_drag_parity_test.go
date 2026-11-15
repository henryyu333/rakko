package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

func TestToolsDeferredWriteCoalescesAndFlushesLatest(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	var writes []string
	var q layoutSaveQueue
	q.write = func(d string, l layout.Layout) error {
		writes = append(writes, l.Cards[0].ID)
		if len(writes) == 1 {
			close(started)
			<-release
		}
		return layout.Write(d, l)
	}

	a := parityWindow()
	a.dataDir = dir
	a.toolsSave = &q

	cards := slices.Clone(a.toolsLayout().Cards)
	if len(cards) < 3 {
		t.Fatalf("need at least 3 default cards, got %d", len(cards))
	}
	firstMove := cards[len(cards)-1].ID
	secondMove := cards[1].ID
	lastMove := cards[2].ID

	// Start write 1 and block inside it while two rapid reorders follow.
	a.mutateToolsDeferred(func(l *layout.Layout) bool {
		l.MoveCard(firstMove, 0)
		return true
	})
	<-started

	a.mutateToolsDeferred(func(l *layout.Layout) bool {
		l.MoveCard(secondMove, 0)
		return true
	})
	a.mutateToolsDeferred(func(l *layout.Layout) bool {
		l.MoveCard(lastMove, 0)
		return true
	})
	close(release)
	a.flushToolsLayout()

	// Write 2 must have been coalesced into write 3, and layout.json on disk must match the final order.
	if len(writes) != 2 || writes[0] != firstMove || writes[1] != lastMove {
		t.Fatalf("coalesced write sequence = %v, want [%s %s]", writes, firstMove, lastMove)
	}
	disk, err := layout.Load(dir)
	if err != nil {
		t.Fatalf("layout.Load after flush: %v", err)
	}
	if len(disk.Cards) == 0 || disk.Cards[0].ID != lastMove {
		t.Fatalf("persisted first card = %+v, want %q", disk.Cards, lastMove)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, layout.FileName))
		if err != nil {
			t.Fatalf("stat layout.json: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("layout.json perm = %o, want 0600", mode)
		}
	}
}
