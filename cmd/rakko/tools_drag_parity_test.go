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
	"time"

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

func TestSyncDragPreviewSkipsRedundantFrameUpdates(t *testing.T) {
	a := parityWindow()
	a.winH = 1
	oldDisplays := pinDisplays
	oldFrame := setNativeWindowFrame
	defer func() {
		pinDisplays = oldDisplays
		setNativeWindowFrame = oldFrame
	}()
	pinDisplays = func() []mygo.Display {
		return []mygo.Display{{ID: 1, Primary: true, Bounds: a.dispBounds, WorkArea: a.dispBounds}}
	}
	var frameCalls int
	setNativeWindowFrame = func(uintptr, int, int, int, int) { frameCalls++ }

	pv := &dragPreview{it: &pinItem{owner: a, cardID: "zip", span: 1, cardW: pinCardW, frame: mygo.Rectangle{X: 100, Y: 100, Width: pinCardW, Height: pinCardH}}}
	cursor := mygo.Point{X: 300, Y: 300}
	a.syncDragPreviewMorph(pv, layout.Card{ID: "zip", Span: 1, Rows: 1}, pinCardW, 20, 20, 100, 84, cursor, false, 2.31, 1, 1, time.Time{}, 1, false, time.Now())
	if pv.it.tilt != 2.31 {
		t.Fatalf("syncDragPreviewMorph tilt=%v, want 2.31", pv.it.tilt)
	}
	callsAfterFirst := frameCalls
	// Calling again with identical cursor and tilt must not issue another SetWindowFrame.
	a.syncDragPreviewMorph(pv, layout.Card{ID: "zip", Span: 1, Rows: 1}, pinCardW, 20, 20, 100, 84, cursor, false, 2.31, 1, 1, time.Time{}, 1, false, time.Now())
	if frameCalls != callsAfterFirst {
		t.Fatalf("redundant syncDragPreviewMorph called SetWindowFrame %d -> %d times", callsAfterFirst, frameCalls)
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

func TestToolsFlushDoesNotDeadlockWhenDeferredWriteFails(t *testing.T) {
	mainExec := make(chan func(), 4)
	oldRunOnMain := toolsLayoutRunOnMain
	toolsLayoutRunOnMain = func(fn func()) {
		done := make(chan struct{})
		mainExec <- func() {
			defer close(done)
			fn()
		}
		<-done
	}
	t.Cleanup(func() { toolsLayoutRunOnMain = oldRunOnMain })

	started := make(chan struct{})
	release := make(chan struct{})
	a := parityWindow()
	a.setTab(pageTools)
	a.dataDir = t.TempDir()
	a.toolsSave = &layoutSaveQueue{
		write: func(string, layout.Layout) error {
			close(started)
			<-release
			return errors.New("disk full")
		},
	}

	cards := a.toolsLayout().Cards
	if len(cards) < 2 {
		t.Fatalf("need at least 2 default cards, got %d", len(cards))
	}
	a.mutateToolsWrite(func(l *layout.Layout) bool {
		l.MoveCard(cards[len(cards)-1].ID, 0)
		return true
	}, true)
	<-started

	flushed := make(chan struct{})
	go func() {
		a.flushToolsLayout()
		close(flushed)
	}()
	close(release)

	select {
	case <-flushed:
	case <-time.After(500 * time.Millisecond):
		// Drain mainExec so the test goroutine exits cleanly after failing.
		select {
		case fn := <-mainExec:
			fn()
		default:
		}
		t.Fatal("flushToolsLayout deadlocked while deferred write reported an error to main thread")
	}

	// Now that flushToolsLayout returned, the main loop is free to run posted callbacks.
	select {
	case fn := <-mainExec:
		fn()
	case <-time.After(500 * time.Millisecond):
		t.Fatal("deferred write error was never posted to main thread")
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

func setupToolsDragTester(t *testing.T, cards []layout.Card) (*shelf, *toolsPage, *ui.Tester) {
	t.Helper()
	oldDisplays := pinDisplays
	oldPreview := pinOpenPreview
	oldInvisible := pinOpenPreviewInvisible
	oldAllow := allowHeadlessPreview
	oldCursor := cursorPoint
	oldDown := dragLeftDown
	allowHeadlessPreview = true
	pinDisplays = func() []mygo.Display {
		return []mygo.Display{{
			ID:       1,
			Primary:  true,
			Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080},
			WorkArea: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040},
		}}
	}
	pinOpenPreview = func(it *pinItem) {}
	pinOpenPreviewInvisible = func(it *pinItem) {}
	var a *shelf
	cursorPoint = func() mygo.Point {
		if a == nil {
			return mygo.Point{X: 960, Y: 80}
		}
		return mygo.Point{X: a.stripCenter(), Y: 80}
	}
	dragLeftDown = func() bool { return false }
	t.Cleanup(func() {
		pinDisplays = oldDisplays
		pinOpenPreview = oldPreview
		pinOpenPreviewInvisible = oldInvisible
		allowHeadlessPreview = oldAllow
		cursorPoint = oldCursor
		dragLeftDown = oldDown
	})

	a = parityShelf()
	a.dataDir = t.TempDir()
	flushToolsLayoutOnCleanup(t, a)
	a.settings = settings.Default()
	a.tab = pageTools
	a.level = 2
	a.visible = true
	a.progress = 1
	a.fx.openK = 1
	a.fx.levelK = 1
	a.fx.w, a.fx.h = a.pageShellW(), a.pageShellH()
	a.winH = 1
	a.ensurePages()
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: cards}
	p := a.toolsPage()
	p.editing = true

	w := int(math.Ceil(float64(a.pageShellW()))) + 2*earR
	h := int(math.Ceil(float64(a.pageShellH()))) + navGap + navH + 2
	a.frame = mygo.Rectangle{X: a.stripCenter() - w/2, Y: 0, Width: w, Height: h}
	view := ui.NewTester(a.view, w, h)
	view.Frame()
	view.Frame()
	return a, p, view
}

func TestToolsDragInsidePanelShapeKeepsNoPreviewWindow(t *testing.T) {
	cards := []layout.Card{
		{ID: "playing", Span: 2, Rows: 2},
		{ID: "ocr", Span: 1, Rows: 1},
		{ID: "zip", Span: 1, Rows: 1},
		{ID: "stopwatch", Span: 1, Rows: 1},
		{ID: "timer", Span: 2, Rows: 1},
	}
	a, p, view := setupToolsDragTester(t, cards)

	// 1. Drag first-row card ("ocr") upward into the top bandRow (y=12), still inside panel shape.
	ocr, ok := view.Find("框选文字")
	if !ok {
		t.Fatal("ocr card missing")
	}
	cx, cy := ocr.X+ocr.W/2, ocr.Y+ocr.H/2
	view.Press(cx, cy)
	view.Move(cx, 12)
	view.Frame()
	if p.preview.window() != nil {
		t.Fatal("preview window opened while dragging first-row card up inside panel")
	}
	if !p.slotShows() {
		t.Fatal("landing slot hid while dragging inside panel")
	}
	if p.press == nil || !p.press.visReady || p.press.dragMorphAt(time.Now()) != 0 {
		t.Fatalf("unexpected lifted card state inside panel: %+v", p.press)
	}
	view.Release(cx, cy)
	view.Advance(400 * time.Millisecond)
	view.Frame()

	// 2. Drag rightmost-column card ("stopwatch") right to 2pt inside the panel's right edge.
	sw, ok := view.Find("秒表")
	if !ok {
		t.Fatal("stopwatch card missing")
	}
	sx, sy := sw.X+sw.W/2, sw.Y+sw.H/2
	shellRight := float32(a.stripCenter()-a.frame.X) + a.fx.w/2
	view.Press(sx, sy)
	view.Move(shellRight-2, sy)
	view.Frame()
	if p.preview.window() != nil {
		t.Fatal("preview window opened while pointer was still inside panel right edge")
	}
	if !p.slotShows() {
		t.Fatal("landing slot hid near panel right edge")
	}
	view.Release(sx, sy)
}

func TestToolsDragHandoffAndReverseMorph(t *testing.T) {
	cards := []layout.Card{
		{ID: "playing", Span: 2, Rows: 2},
		{ID: "ocr", Span: 1, Rows: 1},
		{ID: "zip", Span: 1, Rows: 1},
	}
	a, p, view := setupToolsDragTester(t, cards)
	ocr, ok := view.Find("框选文字")
	if !ok {
		t.Fatal("ocr card missing")
	}
	sx, sy := ocr.X+ocr.W/2, ocr.Y+ocr.H/2
	shellRight := float32(a.stripCenter()-a.frame.X) + a.fx.w/2

	view.Press(sx, sy)
	// Inside the shell but near its edge: the window is armed invisibly.
	view.Move(shellRight-10, sy)
	view.Frame()
	if p.preview.window() != nil || p.preview.armed == nil {
		t.Fatal("preview was not armed invisibly near the panel edge")
	}
	// Within the 6pt hysteresis band outside the panel: still no visible preview.
	view.Move(shellRight+3, sy)
	view.Frame()
	if p.preview.window() != nil {
		t.Fatal("preview window opened inside 6pt hysteresis band")
	}
	if p.preview.armed == nil {
		t.Fatal("no preview window was armed before the handoff (creation latency would show as a gap)")
	}

	// Cross past hysteresis: preview window opens at morph=0, matching panel lift size/color.
	view.Move(shellRight+14, sy)
	view.Frame()
	pv := p.preview.window()
	if pv == nil {
		t.Fatal("preview window did not open outside panel + hysteresis")
	}
	if p.slotShows() {
		t.Fatal("landing slot remained visible after preview window opened")
	}
	if p.preview.armed != nil {
		t.Fatal("the armed window stayed armed after promotion")
	}
	if pv.padL == 0 || pv.morphFrom != 0 || pv.morphTo != 1 {
		t.Fatalf("handoff morph state mismatch: padL=%d from=%v to=%v", pv.padL, pv.morphFrom, pv.morphTo)
	}
	// Right at handoff, morphVal is near 0 so rendered box matches srcW×srcH before 180ms morph completes.
	m0 := pv.morphVal(pv.morphAt)
	if m0 != 0 {
		t.Fatalf("at exact handoff frame morphVal=%v, want 0", m0)
	}
	// After durPinMorph (180ms), morphVal reaches 1 and dropPreviewPaddingNow strips shadow padding.
	m1 := pv.morphVal(pv.morphAt.Add(durPinMorph + 10*time.Millisecond))
	if m1 != 1 {
		t.Fatalf("after 180ms morphVal=%v, want 1", m1)
	}
	pv.dropPreviewPaddingNow()
	if pv.padL != 0 || pv.frame.Width != pinCardW || pv.frame.Height != pinCardH {
		t.Fatalf("post-morph preview frame = %+v (padL=%d), want %dx%d", pv.frame, pv.padL, pinCardW, pinCardH)
	}

	// Drag back inside the panel shape: preview closes immediately and reverse morph starts (1 -> 0).
	view.Move(shellRight-20, sy)
	view.Frame()
	if p.preview.window() != nil {
		t.Fatal("preview window stayed open after returning inside panel")
	}
	if !p.slotShows() {
		t.Fatal("landing slot did not return after dragging back inside panel")
	}
	if p.press.morphTo != 0 {
		t.Fatalf("reverse morph target = %v, want 0", p.press.morphTo)
	}
	if p.preview.armed == nil {
		t.Fatal("the preview window was not demoted to an invisible armed window on return")
	}
	view.Release(sx, sy)
}

func TestToolsDropDoesNotWriteSynchronouslyAndFlushesOnQuit(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	var writes int
	a, _, view := setupToolsDragTester(t, []layout.Card{{ID: "ocr"}, {ID: "zip"}, {ID: "shot"}})
	a.dataDir = dir
	a.toolsSave = &layoutSaveQueue{
		write: func(d string, l layout.Layout) error {
			writes++
			close(started)
			<-release
			return layout.Write(d, l)
		},
	}

	ocr, _ := view.Find("框选文字")
	shot, _ := view.Find("截图")
	view.Press(ocr.X+ocr.W/2, ocr.Y+ocr.H/2)
	view.Move(shot.X+shot.W/2, shot.Y+shot.H/2)
	view.Frame()

	done := make(chan struct{})
	go func() {
		view.Release(shot.X+shot.W/2, shot.Y+shot.H/2)
		close(done)
	}()

	// Drop must return immediately while the disk write is still blocked.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("toolCardDrop blocked on synchronous disk write")
	}
	if a.toolsLayout().Cards[0].ID == "ocr" {
		t.Fatalf("in-memory layout did not update on drop: %v", a.toolsLayout().Cards)
	}
	<-started
	if _, err := os.Stat(filepath.Join(dir, layout.FileName)); !os.IsNotExist(err) {
		t.Fatalf("layout.json already exists before background write finished: %v", err)
	}
	close(release)
	a.flushToolsLayout()
	if writes != 1 {
		t.Fatalf("write count = %d, want 1", writes)
	}
	disk, err := layout.Load(dir)
	if err != nil {
		t.Fatalf("layout.Load after flush: %v", err)
	}
	if disk.Cards[0].ID == "ocr" {
		t.Fatalf("persisted layout did not reflect drop: %v", disk.Cards)
	}
}

func TestToolsSlotHysteresisStopsMixedGridPingPong(t *testing.T) {
	cards := []layout.Card{
		{ID: "playing", Span: 2, Rows: 2},
		{ID: "ocr", Span: 1, Rows: 1},
		{ID: "zip", Span: 1, Rows: 1},
	}
	pitch := toolCellW + toolCellGap
	// Boundary between slot 1 and slot 2 for "ocr" is at x = 2.5*pitch + colW/2 ≈ 313.
	// Find the midpoint between slot 1 and slot 2 centers.
	c1 := packToolCards(toolVirtualCards(cards, "ocr", 1), 4, nil)
	c2 := packToolCards(toolVirtualCards(cards, "ocr", 2), 4, nil)
	var x1, y1, x2, y2 float32
	for _, c := range c1 {
		if c.id == "ocr" {
			x1, y1 = c.x+c.w/2, c.y+c.h/2
		}
	}
	for _, c := range c2 {
		if c.id == "ocr" {
			x2, y2 = c.x+c.w/2, c.y+c.h/2
		}
	}
	midX, midY := (x1+x2)/2, (y1+y2)/2
	var candIdx int = -1
	var candSince time.Time
	t0 := time.Now()

	// Oscillating ±4pt around the midpoint must NOT ping-pong away from current=1.
	got := toolInsertIndexStable(cards, 4, "ocr", 1, midX+4, midY, nil, &candIdx, &candSince, t0)
	if got != 1 {
		t.Fatalf("small 4pt step past midpoint switched prematurely: got %d, want 1", got)
	}
	got = toolInsertIndexStable(cards, 4, "ocr", 1, midX-4, midY, nil, &candIdx, &candSince, t0.Add(20*time.Millisecond))
	if got != 1 {
		t.Fatalf("oscillation switched: got %d, want 1", got)
	}
	// Dwelling >= 100ms just past midpoint switches cleanly.
	_ = toolInsertIndexStable(cards, 4, "ocr", 1, midX+4, midY, nil, &candIdx, &candSince, t0.Add(40*time.Millisecond))
	got = toolInsertIndexStable(cards, 4, "ocr", 1, midX+4, midY, nil, &candIdx, &candSince, t0.Add(150*time.Millisecond))
	if got != 2 {
		t.Fatalf("dwell past midpoint did not switch: got %d, want 2", got)
	}
	// Moving well past margin (16pt advantage -> >8pt past midpoint) switches immediately.
	candIdx, candSince = -1, time.Time{}
	got = toolInsertIndexStable(cards, 4, "ocr", 1, midX+float32(pitch)*0.25, midY, nil, &candIdx, &candSince, t0.Add(200*time.Millisecond))
	if got != 2 {
		t.Fatalf("clear move past margin did not switch immediately: got %d, want 2", got)
	}
}

func TestToolsDropTargetMatchesShownSlot(t *testing.T) {
	// Start in a 5-column packing: [timer(2x1), ocr, playing(2x2), zip].
	// Moving "ocr" to the end makes unlocked toolGridFor shrink to 4 columns
	// where "ocr" wraps to row 1 col 0, whereas during the 5-col drag the dashed
	// slot sits at row 0 col 4. Releasing inside the panel must settle to the
	// shown 5-col slot before unlocking columns.
	cards := []layout.Card{
		{ID: "timer", Span: 2, Rows: 1},
		{ID: "ocr", Span: 1, Rows: 1},
		{ID: "playing", Span: 2, Rows: 2},
		{ID: "zip", Span: 1, Rows: 1},
	}
	a, p, view := setupToolsDragTester(t, cards)
	if g := p.grid(); g.cols != 5 {
		t.Fatalf("expected 5-col starting grid, got %d", g.cols)
	}
	ocr, ok := view.Find("框选文字")
	if !ok {
		t.Fatal("ocr missing")
	}
	// Drag "ocr" to the top-right slot (row 0, col 4 in 5 cols).
	targetX := p.viewport.X + 4*(toolCellW+toolCellGap) + toolColW(1)/2
	targetY := p.viewport.Y + toolRowH(1)/2
	view.Press(ocr.X+ocr.W/2, ocr.Y+ocr.H/2)
	view.Move(targetX+18, targetY)
	view.Advance(130 * time.Millisecond)
	view.Frame()
	var shown toolCell
	for _, cell := range p.grid().cells {
		if cell.id == "ocr" {
			shown = cell
		}
	}
	view.Release(targetX+18, targetY)
	if p.press == nil || !p.press.settling {
		t.Fatalf("expected active settle after drop, got %+v", p.press)
	}
	if p.press.settleX != shown.x || p.press.settleY != shown.y {
		t.Fatalf("drop settle target (%v,%v) != shown drag slot (%v,%v)",
			p.press.settleX, p.press.settleY, shown.x, shown.y)
	}
	if p.dragCols != 5 {
		t.Fatalf("dragCols unlocked prematurely during settle: got %d, want 5", p.dragCols)
	}
	for range 70 {
		view.Advance(16 * time.Millisecond)
		view.Frame()
		if p.dragCols == 0 {
			break
		}
	}
	if p.dragCols != 0 {
		t.Fatalf("dragCols did not unlock after settle: got %d", p.dragCols)
	}
	_ = a
}

func TestToolsLibraryDragFollowsOutsideAndFliesBackOnCancel(t *testing.T) {
	a, p, view := setupToolsDragTester(t, []layout.Card{{ID: "ocr"}, {ID: "zip"}})
	tile, ok := view.Find("截图")
	if !ok {
		t.Fatal("library tile missing")
	}
	shellRight := float32(a.stripCenter()-a.frame.X) + a.fx.w/2
	view.Press(tile.X+tile.W/2, tile.Y+tile.H/2)
	view.Move(shellRight+24, tile.Y+tile.H/2-40)
	view.Frame()
	if p.preview.window() == nil {
		t.Fatal("library drag outside panel did not open preview window")
	}
	view.Release(shellRight+24, tile.Y+tile.H/2-40)
	if p.preview.window() != nil {
		t.Fatal("preview window stayed open after cancelled library drop")
	}
	if p.press == nil || !p.press.freshCancel || !p.press.settling {
		t.Fatalf("cancelled library drag did not start fly-back animation: %+v", p.press)
	}
	if slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == "shot" }) {
		t.Fatal("cancelled library drag added card to layout")
	}
	for range 70 {
		view.Advance(16 * time.Millisecond)
		view.Frame()
		if p.press == nil {
			break
		}
	}
	if p.press != nil {
		t.Fatalf("fly-back animation did not clear press after settling: %+v", p.press)
	}
}

func TestToolsLiftTransitionAndVelocitySettle(t *testing.T) {
	cards := []layout.Card{{ID: "ocr"}, {ID: "zip"}, {ID: "shot"}}
	_, p, view := setupToolsDragTester(t, cards)
	ocr, _ := view.Find("框选文字")
	sx, sy := ocr.X+ocr.W/2, ocr.Y+ocr.H/2

	view.Press(sx, sy)
	view.Move(sx+20, sy)
	view.Frame()
	if p.press == nil || !p.press.dragging {
		t.Fatal("drag did not start")
	}
	// Right at lift start, liftProgress is 0 (scale=1.0), reaching 1 (scale=1.04) by 120ms.
	if got := p.press.liftProgress(p.press.liftAt, false); got != 0 {
		t.Fatalf("liftProgress at t=0 = %v, want 0", got)
	}
	if got := p.press.liftProgress(p.press.liftAt.Add(60*time.Millisecond), false); got <= 0 || got >= 1 {
		t.Fatalf("liftProgress at 60ms = %v, want (0,1)", got)
	}
	if got := p.press.liftProgress(p.press.liftAt.Add(durLift), false); got != 1 {
		t.Fatalf("liftProgress at 120ms = %v, want 1", got)
	}

	view.Advance(16 * time.Millisecond)
	view.Move(sx+60, sy+12)
	view.Frame()
	visX, visY := p.press.visWinX-p.viewport.X, p.press.visWinY-p.viewport.Y+p.scroll.Y
	view.Release(sx+60, sy+12)
	if p.press == nil || !p.press.settling {
		t.Fatal("expected settle after release")
	}
	if math.Abs(float64(p.press.settleStartX-visX)) > 0.5 || math.Abs(float64(p.press.settleStartY-visY)) > 0.5 {
		t.Fatalf("settle started at (%v,%v), want visual (%v,%v)",
			p.press.settleStartX, p.press.settleStartY, visX, visY)
	}
	if p.press.settleVX <= 0 || p.press.settleVY <= 0 {
		t.Fatalf("settle dropped pointer velocity: (%v,%v)", p.press.settleVX, p.press.settleVY)
	}
}

func TestPreviewMorphKeepsGrabPointSteadyWhenPointerIdle(t *testing.T) {
	spec := layout.Card{ID: "playing", Span: 2, Rows: 2}
	srcW, srcH := toolColW(spec.Cols()), toolRowH(spec.Height())
	cardW := pinCardWidth(spec.ID, spec.Cols())
	grabDX, grabDY := float32(40), srcH-20
	cursor := mygo.Point{X: 400, Y: 500}
	area := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}

	for _, m := range []float32{0, 0.25, 0.5, 0.75, 1} {
		v := computeDragCardVisual(spec, cardW, grabDX, grabDY, 1, m, false)
		frame, padL, padT, subX, subY := previewFrameForVisual(cursor, area, v, srcW, srcH, cardW, m, grabDX, grabDY)
		cardScreenX := float32(frame.X+padL) + subX
		cardScreenY := float32(frame.Y+padT) + subY
		gotGrabX := float32(cursor.X) - cardScreenX
		gotGrabY := float32(cursor.Y) - cardScreenY
		if math.Abs(float64(gotGrabX-v.curGrabX)) > 0.6 || math.Abs(float64(gotGrabY-v.curGrabY)) > 0.6 {
			t.Fatalf("morph=%v: screen grab=(%.2f,%.2f), want curGrab=(%.2f,%.2f)",
				m, gotGrabX, gotGrabY, v.curGrabX, v.curGrabY)
		}
	}
}

func TestPartialReverseMorphSettlesSmoothly(t *testing.T) {
	cards := []layout.Card{{ID: "ocr", Span: 2, Rows: 2}, {ID: "zip"}}
	_, p, view := setupToolsDragTester(t, cards)
	ocr, _ := view.Find("框选文字")
	sx, sy := ocr.X+ocr.W/2, ocr.Y+ocr.H/2

	view.Press(sx, sy)
	view.Move(sx+12, sy+12)
	view.Frame()
	if p.press == nil {
		t.Fatal("press not started")
	}
	// Simulate partial reverse morph: visW/visH between desktop pin (278x136) and 2x2 grid card.
	p.press.visReady = true
	p.press.visW = 240
	p.press.visH = 160
	view.Release(sx+12, sy+12)
	if p.press == nil || !p.press.settling {
		t.Fatal("expected settle after release")
	}
	if p.press.settleStartW != 240 || p.press.settleStartH != 160 {
		t.Fatalf("settle start size = %vx%v, want 240x160", p.press.settleStartW, p.press.settleStartH)
	}
}

func TestFreshDragZeroVelocityWhenPausedBeforeRelease(t *testing.T) {
	cards := []layout.Card{{ID: "ocr"}}
	_, p, view := setupToolsDragTester(t, cards)
	lib, ok := view.Find("截图")
	if !ok {
		t.Fatal("missing 截图 in library")
	}
	sx, sy := lib.X+lib.W/2, lib.Y+lib.H/2
	view.Press(sx, sy)
	view.Move(sx+40, sy-80)
	view.Advance(16 * time.Millisecond)
	view.Move(sx+80, sy-140)
	view.Frame()
	// Pause for >60ms before releasing at the same point.
	view.Advance(80 * time.Millisecond)
	view.Release(sx+80, sy-140)
	if p.press != nil && (p.press.settleVX != 0 || p.press.settleVY != 0) {
		t.Fatalf("paused fresh drag retained velocity on release: (%v,%v)", p.press.settleVX, p.press.settleVY)
	}
}

func TestFreshDragReleaseOverLibraryCancels(t *testing.T) {
	cards := []layout.Card{{ID: "ocr"}}
	a, p, view := setupToolsDragTester(t, cards)
	lib, ok := view.Find("截图")
	if !ok {
		t.Fatal("missing 截图 in library")
	}
	ocr, ok := view.Find("框选文字")
	if !ok {
		t.Fatal("missing 框选文字 in grid")
	}
	sx, sy := lib.X+lib.W/2, lib.Y+lib.H/2
	gx, gy := ocr.X+ocr.W/2, ocr.Y+ocr.H/2

	view.Press(sx, sy)
	view.Move(gx, gy)
	view.Frame()
	if p.press == nil || !p.press.fresh || p.press.index < 0 {
		t.Fatalf("expected fresh drag hovering grid, got press=%+v", p.press)
	}
	// Release directly over the library without an intermediate Move.
	view.Release(sx, sy)
	if got := len(a.toolsLayoutNow().Cards); got != 1 {
		t.Fatalf("cards count = %d, want 1 (release over library must cancel)", got)
	}
	if p.press != nil && !p.press.freshCancel {
		t.Fatalf("expected freshCancel on release over library, got press=%+v", p.press)
	}
}
