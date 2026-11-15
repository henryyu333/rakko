package main

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/settings"
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
	a.mutateToolsWrite(func(l *layout.Layout) bool {
		l.MoveCard(firstMove, 0)
		return true
	}, true)
	<-started

	a.mutateToolsWrite(func(l *layout.Layout) bool {
		l.MoveCard(secondMove, 0)
		return true
	}, true)
	a.mutateToolsWrite(func(l *layout.Layout) bool {
		l.MoveCard(lastMove, 0)
		return true
	}, true)
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

func TestToolsDeferredWriteReportsErrorOnMain(t *testing.T) {
	oldDispatch := toolsLayoutDispatch
	var dispatched bool
	toolsLayoutDispatch = func(fn func()) {
		dispatched = true
		fn()
	}
	t.Cleanup(func() { toolsLayoutDispatch = oldDispatch })

	a := parityWindow()
	a.setTab(pageTools)
	a.dataDir = t.TempDir()
	wantErr := errors.New("disk full")
	a.toolsSave = &layoutSaveQueue{
		write: func(string, layout.Layout) error { return wantErr },
	}

	cards := a.toolsLayout().Cards
	if len(cards) < 2 {
		t.Fatalf("need at least 2 default cards, got %d", len(cards))
	}
	a.mutateToolsWrite(func(l *layout.Layout) bool {
		l.MoveCard(cards[len(cards)-1].ID, 0)
		return true
	}, true)
	a.flushToolsLayout()

	if !dispatched {
		t.Fatal("failed deferred write did not dispatch to main thread")
	}
	if a.statusKind != statusErr || !strings.Contains(a.status, "tools layout: disk full") {
		t.Fatalf("a.report status = (kind=%v, msg=%q), want statusErr containing %q", a.statusKind, a.status, "tools layout: disk full")
	}
}

func TestToolsLibraryDragCancelClearsPointerSessionAndOverlayMouse(t *testing.T) {
	oldCursor := cursorPoint
	defer func() { cursorPoint = oldCursor }()

	setup := func(t *testing.T) (*shelf, *toolsPage, *ui.Tester, ui.Rect) {
		t.Helper()
		a := parityShelf()
		a.dataDir = t.TempDir()
		a.settings = settings.Default()
		a.tab = pageTools
		a.level = 2
		a.visible = true
		a.progress = 1
		a.fx.openK = 1
		a.winH = 1
		a.ensurePages()
		a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "ocr"}, {ID: "zip"}}}
		p := a.toolsPage()
		p.editing = true

		far := mygo.Point{X: a.dispBounds.Width - 5, Y: a.dispBounds.Height - 5}
		cursorPoint = func() mygo.Point { return far }

		w := int(math.Ceil(float64(a.pageShellW()))) + 2*earR
		h := int(math.Ceil(float64(a.pageShellH()))) + navGap + navH + 2
		view := ui.NewTester(a.view, w, h)
		view.Frame()
		view.Frame()
		a.fx.w, a.fx.h = a.shellSize(1, 0, 0, 0, 0)

		shotName := toolCardDefFor("shot").name(a.appLoc())
		tile, ok := view.Find(shotName)
		if !ok {
			t.Fatalf("library tile %q not found; texts=%v", shotName, view.Texts())
		}
		view.Press(tile.X+tile.W/2, tile.Y+tile.H/2)
		view.Move(tile.X+tile.W/2, tile.Y+tile.H/2-30)
		view.Frame()
		if p.lp == nil || !p.lp.started || p.press == nil || !p.press.fresh {
			t.Fatalf("library drag did not start: lp=%+v press=%+v", p.lp, p.press)
		}
		if !a.toolsDragActive() {
			t.Fatal("toolsDragActive() = false during active library drag")
		}
		if got := a.overlayAcceptsMouse(); got != toolsDragKeepsMouse {
			t.Fatalf("during library drag outside shell: overlayAcceptsMouse=%v, want %v", got, toolsDragKeepsMouse)
		}
		return a, p, view, tile
	}

	assertCleared := func(t *testing.T, a *shelf, p *toolsPage) {
		t.Helper()
		if p.lp != nil {
			t.Fatalf("p.lp stayed non-nil: %+v", p.lp)
		}
		if p.press != nil {
			t.Fatalf("p.press stayed non-nil: %+v", p.press)
		}
		if p.rs != nil {
			t.Fatalf("p.rs stayed non-nil: %+v", p.rs)
		}
		if a.toolsDragActive() {
			t.Fatal("toolsDragActive() stayed true")
		}
		if a.overlayAcceptsMouse() {
			t.Fatal("overlayAcceptsMouse() stayed true outside shell")
		}
	}

	t.Run("escape-then-release-and-hide", func(t *testing.T) {
		a, p, view, tile := setup(t)
		a.escape()
		// Checked before the next frame: the engine also ends the press of
		// a tile that leaves the tree (a canceled release), but the page
		// must not depend on that frame being built — a folded or idle
		// overlay may never build it.
		assertCleared(t, a, p)
		view.Frame()
		assertCleared(t, a, p)
		view.Release(tile.X+tile.W/2, tile.Y+tile.H/2-30)
		view.Frame()
		a.hide(hideExplicit)
		a.fx.openK = 0
		a.progress = 0
		a.finishClose()
		assertCleared(t, a, p)
	})

	t.Run("leave-via-setTab", func(t *testing.T) {
		a, p, view, tile := setup(t)
		a.setTab(pageHome)
		view.Frame()
		assertCleared(t, a, p)
		view.Release(tile.X+tile.W/2, tile.Y+tile.H/2-30)
		a.hide(hideExplicit)
		a.fx.openK = 0
		a.progress = 0
		a.finishClose()
		assertCleared(t, a, p)
	})

	t.Run("leave-via-switchTab", func(t *testing.T) {
		a, p, view, tile := setup(t)
		a.switchTab(pageClipboard)
		view.Frame()
		assertCleared(t, a, p)
		view.Release(tile.X+tile.W/2, tile.Y+tile.H/2-30)
		assertCleared(t, a, p)
	})

	t.Run("hide-directly", func(t *testing.T) {
		a, p, view, tile := setup(t)
		a.hide(hideExplicit)
		a.fx.openK = 0
		a.progress = 0
		assertCleared(t, a, p)
		view.Release(tile.X+tile.W/2, tile.Y+tile.H/2-30)
		a.finishClose()
		assertCleared(t, a, p)
	})

	t.Run("done-button", func(t *testing.T) {
		a, p, view, tile := setup(t)
		done, ok := view.Find(a.appLoc().Done())
		if !ok {
			t.Fatalf("done button %q not found", a.appLoc().Done())
		}
		view.Release(tile.X+tile.W/2, tile.Y+tile.H/2-30)
		// Re-start library drag and click Done without releasing first.
		view.Press(tile.X+tile.W/2, tile.Y+tile.H/2)
		view.Move(tile.X+tile.W/2, tile.Y+tile.H/2-30)
		view.Frame()
		if p.lp == nil || !p.lp.started {
			t.Fatalf("re-started library drag failed: lp=%+v", p.lp)
		}
		view.ClickAt(done.X+done.W/2, done.Y+done.H/2)
		view.Frame()
		assertCleared(t, a, p)
	})
}
