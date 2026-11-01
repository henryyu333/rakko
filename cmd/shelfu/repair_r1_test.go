//go:build darwin

package main

// Repairs from the stage review (2026-10-29): the tools grid's widget
// taps, its drag-out bounds and preview height, the catalogue's scrolling,
// the pinned window's width, the legacy pin rectangles, the light
// appearance's material sync and corner, the first-run test, the card
// marks and the overview's own deadlines. Each test fails on the build
// before its repair.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
	"shelf/internal/mac"
	"shelf/internal/store"
)

// toolsFixture is a headless shelf showing the tools page.
func toolsFixture(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{store: s, dataDir: t.TempDir(), tab: pageTools, level: 2, visible: true}
	return a, ui.NewTester(a.view, sheetW, panelTestH)
}

// TestWidgetCardTapRunsItsAction: a whole-card tap on a widget does what
// that widget always did — keep-awake toggles, the clipboard card copies
// its newest capture, AI 用量 opens its page — while the music card, whose
// transport buttons own every press, stays inert to a tap beside them.
func TestWidgetCardTapRunsItsAction(t *testing.T) {
	a, _ := toolsFixture(t)
	p := a.toolsPage()

	// 保持唤醒: the tap starts and stops the hold.
	power := newFakePower()
	a.awake = newAwakeStub(power)
	a.awake.host = a
	p.toolCardTapped("awake")
	if !a.awake.active() || power.held() != 1 {
		t.Fatalf("a tap on 保持唤醒 did not start it: active=%v held=%d", a.awake.active(), power.held())
	}
	p.toolCardTapped("awake")
	if a.awake.active() {
		t.Fatal("a second tap did not stop 保持唤醒")
	}

	// 剪贴板最新: the newest capture goes to the clipboard.
	if err := a.store.AddClipText("最新的剪贴板内容", "com.example.App"); err != nil {
		t.Fatal(err)
	}
	rec := &recordClip{}
	a.write = rec
	p.toolCardTapped("clip")
	if rec.text != "最新的剪贴板内容" {
		t.Fatalf("a tap on 剪贴板最新 copied %q", rec.text)
	}

	// AI 用量: the page opens.
	usage := newUsageFixture(t, true)
	a.registerPage(usage.f)
	a.usage = usage.f
	p.toolCardTapped("usage")
	if a.tab != pageUsage || a.currentLevel() != 2 {
		t.Fatalf("a tap on AI 用量 left tab=%s level=%d", a.tab, a.currentLevel())
	}

	// 音乐: a tap must not toggle playback; its own buttons do.
	before := a.status
	p.toolCardTapped("playing")
	if a.status != before {
		t.Fatalf("a tap on the music card acted: %q", a.status)
	}

	// 最近文件 has nothing to act on and must not jiggle as a failed action.
	p.toolCardTapped("recent")
	if p.jiggleID == "recent" {
		t.Fatal("a tap on 最近文件 reported a failed action")
	}
}

// TestToolsDropBelowTheViewportPins: a card released below the rows the
// scroll shows is dragged out of the panel, not reordered into a row the
// user cannot see.
func TestToolsDropBelowTheViewportPins(t *testing.T) {
	pinCanvasDrag(t)
	a, _ := toolsFixture(t)
	a.settings = settingsForTest()
	a.x, a.y = 400, 0
	a.frame = mygo.Rectangle{X: a.x, Y: a.y, Width: sheetW, Height: sheetH}
	p := a.toolsPage()
	p.editing = true
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	view.Frame()
	if p.viewport.H <= 0 || p.scroll.MaxY <= 0 {
		t.Fatalf("fixture is not scrollable: viewport=%+v scroll=%+v", p.viewport, p.scroll)
	}
	// Below the visible viewport, still inside the grid's total height.
	py := p.scroll.Y + p.viewport.H + 40
	before := a.toolsLayout().Cards
	last := before[len(before)-1].ID
	d := &toolPress{id: last, dragging: true, px: 60, py: py, index: 0, cardX: 0, cardY: 0}
	p.toolCardDrop(d, mygo.Point{X: 60, Y: 2000})
	if len(a.pins) != 1 || a.pins[0].cardID != last {
		t.Fatalf("a release below the viewport did not pin: %d pins", len(a.pins))
	}
	if got := a.toolsLayout().Cards; len(got) != len(before)-1 {
		t.Fatalf("the release also reordered the grid: %d -> %d cards", len(before), len(got))
	}
}

// TestToolsPickerScrolls: with every card removed the catalogue is taller
// than the page, and its rows scroll inside the card instead of being cut.
func TestToolsPickerScrolls(t *testing.T) {
	a, view := toolsFixture(t)
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "ocr"}}}
	p := a.toolsPage()
	p.editing, p.picking = true, true
	view.Frame()
	view.Frame()
	last, ok := view.Find("隔空投送")
	if !ok {
		t.Fatalf("the catalogue is missing its last row; texts %v", view.Texts())
	}
	bodyBottom := float32(a.sheetTop() + bandH + pageActionH + actionsGap + contentH)
	// With no scrolling the last row would be laid out below the page (the
	// rows add up to far more than 174pt) and cut off; inside a scroll it
	// starts inside the box.
	if last.Y >= bodyBottom {
		t.Fatalf("the last catalogue row at %+v is below the page (bottom %.0f): the list does not scroll",
			last, bodyBottom)
	}
	// The list really moves: a wheel over it pulls the later rows up.
	before := last.Y
	view.Scroll(last.X+last.W/2, last.Y+last.H/2, 0, 120)
	view.Frame()
	view.Frame()
	after, ok := view.Find("隔空投送")
	if !ok {
		t.Fatal("the last row vanished after scrolling")
	}
	if after.Y >= before {
		t.Fatalf("the wheel did not scroll the catalogue: %+v -> %+v", before, after.Y)
	}
}

// TestPinnedWideWidgetsKeepTwoColumns: the desktop form of the music,
// recent-file and clipboard widgets is 278pt even when the card sits in one
// grid column; a card that fits 136pt stays 136.
func TestPinnedWideWidgetsKeepTwoColumns(t *testing.T) {
	if got := pinCardWidth("playing", 1); got != pinCardW2 {
		t.Fatalf("a one-column music card pins %d wide, want %d", got, pinCardW2)
	}
	if got := pinCardWidth("recent", 1); got != pinCardW2 {
		t.Fatalf("a one-column recent card pins %d wide, want %d", got, pinCardW2)
	}
	if got := pinCardWidth("clip", 1); got != pinCardW2 {
		t.Fatalf("a one-column clipboard card pins %d wide, want %d", got, pinCardW2)
	}
	if got := pinCardWidth("awake", 1); got != pinCardW {
		t.Fatalf("保持唤醒 pins %d wide, want %d", got, pinCardW)
	}
	if got := pinCardWidth("awake", 2); got != pinCardW2 {
		t.Fatalf("a two-column 保持唤醒 pins %d wide, want %d", got, pinCardW2)
	}
	// The pin from a 1 × 1 grid card is wide on the desktop but returns to
	// its single column.
	pinCanvasDrag(t)
	a, _ := toolsFixture(t)
	a.settings = settingsForTest()
	a.mutateTools(func(l *layout.Layout) { l.SetCardSize("playing", 1, 1) })
	a.x, a.y = 400, 0
	a.frame = mygo.Rectangle{X: a.x, Y: a.y, Width: sheetW, Height: sheetH}
	p := a.toolsPage()
	p.editing = true
	p.toolCardDrop(&toolPress{id: "playing", dragging: true, px: 60, py: 2000, grabDX: 10, grabDY: 10}, mygo.Point{X: 60, Y: 2000})
	if len(a.pins) != 1 || a.pins[0].cardW != pinCardW2 {
		t.Fatalf("the pinned music card is %+v, want a %dpt window", a.pins, pinCardW2)
	}
	disk, err := layout.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(disk.Pins) != 1 || disk.Pins[0].Span != 1 || disk.Pins[0].Rows != 1 {
		t.Fatalf("the pin recorded %+v, want the card's 1 × 1 grid size", disk.Pins)
	}
}

// TestPreviewHeightIsTheCardHeight: the dragged copy leaves the page at the
// card's real height, so a 2 × 2 widget's preview opens where the drawn card
// reaches the edge, not 90pt later.
func TestPreviewHeightIsTheCardHeight(t *testing.T) {
	v := ui.Rect{W: 416, H: 174}
	// At 80pt down the box a one-row card (84pt) still fits (80 + 89 ≤ 174)
	// while a 2 × 2 widget (174pt) reaches past it — the case the review
	// found: the preview opened 90pt late for the tall card.
	top := float32(80)
	if previewLeaves(v, 0, top, toolColW(2), toolRowH(1)) {
		t.Fatal("the one-row copy left the box at 80pt")
	}
	if !previewLeaves(v, 0, top, toolColW(2), toolRowH(2)) {
		t.Fatal("the two-row copy does not leave the box at 80pt")
	}
}

// TestLegacyPinKeepsItsReturnRect: a pin written by the build before the
// grid union stored the window's span (widened to two columns) and the
// canvas rectangle; the rectangle is the return size, and it survives the
// first save.
func TestLegacyPinKeepsItsReturnRect(t *testing.T) {
	dir := t.TempDir()
	old := `{
  "version": 2,
  "cards": [{"id": "ocr"}],
  "pins": [{"id": "playing", "span": 2, "display": "1", "x": 10, "y": 20,
            "homeX": 1, "homeY": 0, "homeW": 1, "homeH": 2}]
}`
	if err := os.WriteFile(filepath.Join(dir, layout.FileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := layout.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Pins) != 1 {
		t.Fatalf("pins = %+v", l.Pins)
	}
	pin := l.Pins[0]
	if pin.Span != 1 || pin.Rows != 2 {
		t.Fatalf("legacy pin return size = %d × %d, want the recorded 1 × 2", pin.Span, pin.Rows)
	}
	if pin.HomeW != 0 || pin.HomeH != 0 {
		t.Fatal("the legacy rectangle is still applied after the migration")
	}
	// The first save keeps it: 放回刘海 returns the music card 1 × 2.
	if err := layout.Write(dir, l); err != nil {
		t.Fatal(err)
	}
	again, err := layout.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Pins[0].Span != 1 || again.Pins[0].Rows != 2 {
		t.Fatalf("after a save the pin returns %d × %d", again.Pins[0].Span, again.Pins[0].Rows)
	}
}

// TestLeaveFocusModeHandsTheKeyboardBack: leaving the clipboard search's
// keyboard mode goes back to the app the user came from at once, while the
// panel stays open; the later close has nothing left to restore and must
// not lose the remembered app on the way out.
func TestLeaveFocusModeHandsTheKeyboardBack(t *testing.T) {
	oldActive, oldActivate, oldResign := appIsActive, activateApp, resignSelf
	var activated []int
	resigned := 0
	appIsActive = func() bool { return true }
	activateApp = func(pid int) bool { activated = append(activated, pid); return true }
	resignSelf = func() { resigned++ }
	t.Cleanup(func() { appIsActive, activateApp, resignSelf = oldActive, oldActivate, oldResign })

	a, view := levelShelf(t)
	a.visible = true
	// The ⌃⌥V state: the clipboard page, its search focused, the app the
	// user came from remembered.
	a.tab, a.tabFrom, a.level = pageClipboard, pageClipboard, 2
	a.focusMode = true
	a.prevAppPID = 4242
	a.search = "x"
	view.Frame()
	view.Key(0, ui.KeyEscape) // clears nothing, leaves the search mode
	view.Key(0, ui.KeyEscape) // backs out to the overview
	if a.focusMode {
		t.Fatal("Esc left the keyboard mode on")
	}
	if len(activated) != 1 || activated[0] != 4242 {
		t.Fatalf("the app the user came from was not reactivated: %v", activated)
	}
	if a.prevAppPID != 0 {
		t.Fatalf("the remembered app is still pending a restore: %d", a.prevAppPID)
	}
	if !a.visible || a.currentLevel() != 1 {
		t.Fatalf("the panel did not stay open on the overview: visible=%v level=%d", a.visible, a.currentLevel())
	}
	// The final close has nothing to hand back and must not activate again.
	a.hide(hideExplicit)
	view.Frame()
	if len(activated) != 1 {
		t.Fatalf("the close restored the app a second time: %v", activated)
	}
	if resigned != 0 {
		t.Fatalf("the panel resigned itself %d times", resigned)
	}
}

// TestLeaveFocusModeKeepsTheOpenPanel: leaving the clipboard search's
// keyboard mode hands the keyboard back without ever hiding this app. The
// remembered app may already be gone, and activating it then fails; the
// close path's own fallback resigns NSApp, which would take the panel the
// user is still looking at down with it. This path must only ask, and a
// live remembered app must still get the keyboard at once.
func TestLeaveFocusModeKeepsTheOpenPanel(t *testing.T) {
	oldActive, oldActivate, oldResign := appIsActive, activateApp, resignSelf
	var activated []int
	resigned := 0
	appIsActive = func() bool { return true }
	activateApp = func(pid int) bool { activated = append(activated, pid); return false }
	resignSelf = func() { resigned++ }
	t.Cleanup(func() { appIsActive, activateApp, resignSelf = oldActive, oldActivate, oldResign })

	a, view := levelShelf(t)
	a.visible = true
	searching := func() {
		// The ⌃⌥V state with the search empty, so one Esc leaves the mode
		// instead of first clearing the query.
		a.tab, a.tabFrom, a.level = pageClipboard, pageClipboard, 2
		a.search, a.focusMode, a.prevAppPID = "", true, 4242
	}
	searching()
	view.Frame()
	view.Key(0, ui.KeyEscape) // the remembered app is gone: activation fails
	if a.focusMode {
		t.Fatal("Esc left the keyboard mode on")
	}
	if len(activated) != 1 || activated[0] != 4242 {
		t.Fatalf("the hand-back did not try the remembered app: %v", activated)
	}
	if resigned != 0 {
		t.Fatalf("the hand-back resigned the app %d times and hid the open panel", resigned)
	}
	if !a.visible {
		t.Fatal("the panel was hidden while it was still open")
	}

	activateApp = func(pid int) bool { activated = append(activated, pid); return true }
	searching()
	view.Frame()
	view.Key(0, ui.KeyEscape)
	if len(activated) != 2 || activated[1] != 4242 {
		t.Fatalf("a live remembered app was not reactivated: %v", activated)
	}
	if resigned != 0 || !a.visible {
		t.Fatalf("the panel did not survive the hand-back: resigned=%d visible=%v", resigned, a.visible)
	}
}

// TestReduceMotionHoverShapeNeverSprings: with Reduce Motion on, the peek
// shape takes its new width and height on the frame the state changes, and
// the following frame does not move it again; with the preference off the
// same change keeps springing. (mygo's AnimateTo also snaps under Reduce
// Motion, so the shell's explicit jumps and the engine agree here: the
// test pins the behaviour either way.)
func TestReduceMotionHoverShapeNeverSprings(t *testing.T) {
	for _, reduce := range []bool{true, false} {
		name := "spring"
		if reduce {
			name = "reduce motion"
		}
		t.Run(name, func(t *testing.T) {
			a := motionWindow()
			a.settings.IslandNowPlaying = false
			a.np = nil
			a.visible, a.level = false, 1
			view := ui.NewTester(a.view, sheetW, panelTestH)
			if reduce {
				view.SetPreferences(ui.Preferences{ReduceMotion: true})
			}
			// The plain chevron bulge, settled.
			a.peeking = true
			for range 40 {
				view.Frame()
				view.Advance(16 * time.Millisecond)
			}
			// A track arrives: the wings replace the chevron's bulge, a
			// much wider shape.
			a.settings.IslandNowPlaying = true
			a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{
				Title: "夜曲", Playing: true, Duration: 200, Received: time.Now(),
			}}
			view.Frame() // the frame the state changes on
			w0, h0 := a.fx.peekW, a.fx.peekH
			view.Advance(16 * time.Millisecond)
			view.Frame()
			w1, h1 := a.fx.peekW, a.fx.peekH
			if reduce {
				if w1 != w0 || h1 != h0 {
					t.Fatalf("reduce motion moved the shape after its first frame: %.1f×%.1f -> %.1f×%.1f",
						w0, h0, w1, h1)
				}
				return
			}
			if w1 == w0 && h1 == h0 {
				t.Fatalf("the spring never moved the shape (%.1f×%.1f): the fixture does not exercise it", w0, h0)
			}
		})
	}
}

// TestReduceMotionJumpsTheShell: with Reduce Motion the shell takes its
// size at once (no spring, no overshoot) and only the content fades; the
// spring still animates when the preference is off.
func TestReduceMotionJumpsTheShell(t *testing.T) {
	a := motionWindow()
	a.level, a.visible, a.jumpLevel = 1, true, true
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.SetPreferences(ui.Preferences{ReduceMotion: true})
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.w != homeShellW || a.fx.h != homeShellH {
		t.Fatalf("reduced overview shell = %.1f×%.1f", a.fx.w, a.fx.h)
	}
	// Switching to a page: the first frame after the switch is already the
	// page's size, with no intermediate heights.
	a.setLevel(2)
	view.Frame()
	if a.fx.h != sheetH {
		t.Fatalf("reduced level change drew %.0f tall, want the page at once", a.fx.h)
	}
	view.Advance(16 * time.Millisecond)
	view.Frame()
	if a.fx.h != sheetH {
		t.Fatalf("reduced level change overshot to %.0f", a.fx.h)
	}
}
