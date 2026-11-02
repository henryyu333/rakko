//go:build darwin

package main

// Regression tests for the shell motion fixes: the ear corners' room in
// the window, monotonic mid-open closes, the drawn-shell-driven window and
// click pass-through on close, the invisible rest snap, and the
// drawn-shell height freeze. Plus the drop-zone isolation fixes: headless
// board reads, per-drag caches, headless thumbnails and the same-frame
// window sync on height changes.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
)

// #13: the shell's top edge reaches earR past its flanks on each side; the
// window must hold the ears on every frame of the open flight and at rest.
func TestFrameAlwaysHoldsTheEars(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	a.visible = true
	for i := range 120 {
		view.Frame()
		f := a.desiredFrame()
		if float64(f.Width) < float64(a.fx.w)+2*earR-0.5 {
			t.Fatalf("frame %d: window %d wide clips the ears of a %.1fpt shell", i, f.Width, a.fx.w)
		}
		view.Advance(16 * time.Millisecond)
	}
	sw, _ := sheetFrame()
	if got, want := a.desiredFrame().Width, sw; got != want {
		t.Fatalf("the settled frame is %d wide, want the panel plus both ears (%d)", got, want)
	}
	// The idle strip holds the peek shape's ears too.
	b := motionShelf()
	w, _ := b.peekSize()
	if f := b.desiredFrame(); f.Width < int(w+0.5)+2*earR {
		t.Fatalf("the idle strip %d wide clips the peek shape's ears", f.Width)
	}
}

// #14: closing mid-open must never grow the shell again — the opening
// spring's outward velocity does not carry into the fold.
func TestMidOpenCloseNeverGrowsOutward(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	a.visible = true
	// Open partway: long enough to be fast-moving, not settled.
	for range 8 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	a.visible = false
	prevW, prevH := a.fx.w, a.fx.h
	for i := range 60 {
		view.Frame()
		if a.fx.h > prevH+2 || a.fx.w > prevW+2 {
			t.Fatalf("frame %d after the close: the shell grew to %.1fx%.1f from %.1fx%.1f",
				i, a.fx.w, a.fx.h, prevW, prevH)
		}
		prevW, prevH = a.fx.w, a.fx.h
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.openK > 0.01 {
		t.Fatalf("the close never folded: openK %.3f", a.fx.openK)
	}
}

// Reopening mid-close turns the shell outward from its last drawn size.
// The next display frame must grow, not keep folding or pause while the
// old spring's inward velocity wears off.
func TestMidCloseReopenIsContinuous(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	a.visible = true
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	a.visible = false
	for range 8 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	a.visible = true // reopen mid-fold
	reopenedH := a.fx.h
	prevH := reopenedH
	targetH := float32(a.panelH())
	for i := range 60 {
		view.Frame()
		if i == 1 && a.fx.h <= reopenedH {
			t.Fatalf("the first display frame after reopening did not grow: %.2f -> %.2f", reopenedH, a.fx.h)
		}
		if prevH < targetH && a.fx.h < prevH-0.01 {
			t.Fatalf("frame %d after the reopen: the shell kept folding from %.2f to %.2f", i, prevH, a.fx.h)
		}
		prevH = a.fx.h
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.openK < 0.99 {
		t.Fatalf("the reopen never finished: openK %.3f", a.fx.openK)
	}
}

// #15: once the drawn shell has folded back to the peek shape, the window
// shrinks to the strip and lets clicks through — even while the tick's
// eased progress still says "open".
func TestFoldedShellRestoresClicksAndTheStrip(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	a.visible = true
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	a.visible = false
	for range 60 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.openK > 0.01 {
		t.Fatalf("the fold never finished: openK %.3f", a.fx.openK)
	}
	a.progress = 0.2 // the 30ms tick's EaseOut still trails the drawn spring
	a.syncMousePassThrough()
	if !a.ignoringMouse {
		t.Fatal("the folded shell still eats clicks 110ms early")
	}
	if f := a.desiredFrame(); f.Height > 120 {
		t.Fatalf("the window still holds the panel's %dpt height after the fold", f.Height)
	}
}

// #16: the fold's last approach is smooth — the step that lands on the
// notch band is sub-point, not a visible snap.
func TestCloseTailHasNoVisibleSnap(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	a.visible = true
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	a.visible = false
	base := float32(max(32, a.notchH))
	prev := a.fx.h
	finalStep := float32(-1)
	for range 60 {
		view.Frame()
		if prev > base && a.fx.h <= base+0.001 {
			finalStep = prev - a.fx.h // the landing step
			break
		}
		prev = a.fx.h
		view.Advance(16 * time.Millisecond)
	}
	if finalStep < 0 {
		t.Fatal("the fold never reached the notch band")
	}
	if finalStep > 1 {
		t.Fatalf("the fold snapped the last %.1fpt into place", finalStep)
	}
}

// tallPage is a registered test page with content taller than the fixed
// content box, for the closing freeze test (#18).
type tallPage struct{ host *shelf }

func (p *tallPage) id() pageID    { return "tall" }
func (p *tallPage) label() string { return "高页" }
func (p *tallPage) enabled() bool { return true }
func (p *tallPage) icon(c *ui.Context, active bool) ui.Element {
	return ui.Box(c).Size(16, 16)
}
func (p *tallPage) body(c *ui.Context, over bool, live bool) ui.Element {
	return ui.Box(c).Grow(1)
}
func (p *tallPage) title() string { return "高页" }
func (p *tallPage) actions(c *ui.Context) []ui.Element {
	return nil
}

// #18: the closing frame follows the drawn shell while it folds, so the
// sheet's bottom is never cut off at the fold's tail.
func TestClosingFreezeFollowsTheDrawnShell(t *testing.T) {
	a := motionWindow()
	a.registerPage(&tallPage{host: a})
	a.tab, a.level, a.visible = "tall", 2, true
	view := ui.NewTester(a.view, sheetW, panelTestH)
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if got := a.desiredFrame().Height; got < sheetH {
		t.Fatalf("the settled sheet frame is %d tall, want at least %d", got, sheetH)
	}
	a.hide(hideExplicit) // the view state stays for the 120 ms fade
	a.progress = 0       // the tick's EaseOut already ended; the drawn spring has not
	for i := range 30 {
		view.Frame()
		if a.fx.openK <= 0.01 {
			break
		}
		if drawn := int(a.fx.h + 0.5); a.desiredFrame().Height+2 < drawn {
			t.Fatalf("frame %d cuts the shell drawn at %d (openK %.3f)",
				i, drawn, a.fx.openK)
		}
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.openK > 0.01 {
		t.Fatal("the fold never finished")
	}
}

// #19: a headless shelf never reads the real drag pasteboard — tests get
// files only from a substituted board.
func TestHeadlessSnapNeverReadsTheRealBoard(t *testing.T) {
	a := &shelf{dragging: true, visible: true, notchW: 180, notchH: 32,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	if got := a.dropZoneSnap(true); got != nil {
		t.Fatalf("a headless shelf read the real drag board: %v", got)
	}
}

// #20: a new drag session never inherits the previous one's files or
// classification, even when the board's changeCount coincides.
func TestNewDragDropsTheLastSessionsCache(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	zip1 := writeFile(t, dir, "2.zip", []byte("PK"))
	files := []string{img}
	reads := new(int)
	oldF, oldC := dragFiles, dragFilesChange
	dragFiles = func(*shelf) []string { *reads++; return files }
	dragFilesChange = func(*shelf) int64 { return 7 } // the board serial coincides
	t.Cleanup(func() { dragFiles, dragFilesChange = oldF, oldC })
	a := &shelf{dragging: true, visible: true, notchW: 180, notchH: 32,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	if got := a.dropZoneSnap(true); len(got) != 1 || a.drop.class != classImages {
		t.Fatalf("first drag: files %v class %v", got, a.drop.class)
	}
	a.dragSerial = 9 // the monitor saw a new drag session
	files = []string{zip1}
	got := a.dropZoneSnap(true)
	if *reads != 2 {
		t.Fatalf("a new drag reused the cached board read (reads=%d)", *reads)
	}
	if len(got) != 1 || got[0] != zip1 || a.drop.class != classArchives {
		t.Fatalf("the new drag inherited the old session: files %v class %v", got, a.drop.class)
	}
}

// A headless shelf does not start a decode with no window to receive it.
func TestHeadlessThumbnailNeverTouchesTheWindow(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "p.png", testPNGBytes(t))
	a := &shelf{thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	item := store.Item{ID: "x", Path: png, Kind: store.KindImage}
	if got := a.thumbnail(item); got != nil {
		t.Fatal("a headless thumbnail decoded synchronously")
	}
	if a.thumbActive != 0 || len(a.thumbPending) != 0 {
		t.Fatal("a headless shelf started a thumbnail decode")
	}
}

// #22: the drop zone changing panelH re-syncs the window on the same
// frame, not at the next 30ms tick.
// #22: the drop zone is content inside the fixed sheet, so the window's
// size must not change when a drag appears or leaves.
func TestDropZoneKeepsTheSheetSize(t *testing.T) {
	a := motionWindow()
	a.visible = true
	view := ui.NewTester(a.view, sheetW, panelTestH)
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	before := a.frame
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	t.Setenv("SHELF_DEBUG_DROP_FILES", img)
	view.Frame()
	if a.frame != before {
		t.Fatalf("the drop zone resized the window: %+v -> %+v", before, a.frame)
	}
	if !view.HasText("放入托盘") {
		t.Fatalf("the zone did not draw; texts %v", view.Texts())
	}
	t.Setenv("SHELF_DEBUG_DROP_FILES", "")
	view.Frame()
	view.Advance(300 * time.Millisecond)
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.frame != before {
		t.Fatalf("after the zone the window is %+v, want the original %+v", a.frame, before)
	}
	// The frame's width still holds the ears after every sync.
	if a.frame.Width < int(a.fx.w+0.5)+2*earR-1 {
		t.Fatalf("the synced frame %d clips the ears of a %.0fpt shell", a.frame.Width, a.fx.w)
	}
}

// Opening the shelf must size the window for the open flight from the
// first frame and keep its horizontal position fixed when the spring
// settles, while freezing any peek morph in flight so the shell grows
// monotonically without a secondary height or width bounce.
func TestOpeningFrameStaysAlignedAndFreezesPeekMorph(t *testing.T) {
	a := motionWindow()
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	t.Setenv("SHELF_DEBUG_DROP_FILES", img)
	view := ui.NewTester(a.view, sheetW, panelTestH)
	// Start a peek morph partway (as a drag or hover wings does), then open.
	a.dragging, a.peeking = true, true
	view.Frame()
	view.Advance(32 * time.Millisecond)
	view.Frame()
	a.visible, a.edgeOpened = true, true
	a.dropZoneSnap(true)
	a.fx.openMoving = true
	a.syncMousePassThrough()
	firstFrame := a.desiredFrame()
	prevW, prevH := a.fx.w, a.fx.h
	peaked := false
	for i := range 60 {
		view.Frame()
		f := a.desiredFrame()
		if f.X != firstFrame.X || f.Width != firstFrame.Width {
			t.Fatalf("frame %d: window X/Width shifted from %+v to %+v during open", i, firstFrame, f)
		}
		if f.Height < int(a.fx.h+0.5) {
			t.Fatalf("frame %d: window height %d clips shell %.1f", i, f.Height, a.fx.h)
		}
		if !peaked && a.fx.openK >= 1 && a.fx.h < prevH {
			peaked = true
		}
		if !peaked && (a.fx.w < prevW-0.05 || a.fx.h < prevH-0.05) {
			t.Fatalf("frame %d: shell reversed before peak (%.1fx%.1f -> %.1fx%.1f)", i, prevW, prevH, a.fx.w, a.fx.h)
		}
		if peaked && (a.fx.w > prevW+0.05 || a.fx.h > prevH+0.05) {
			t.Fatalf("frame %d: shell grew again after peak (%.1fx%.1f -> %.1fx%.1f)", i, prevW, prevH, a.fx.w, a.fx.h)
		}
		prevW, prevH = a.fx.w, a.fx.h
		view.Advance(16 * time.Millisecond)
	}
	settled := a.desiredFrame()
	if settled.X != firstFrame.X || settled.Width != firstFrame.Width {
		t.Fatalf("settled frame %+v shifted horizontally from flight frame %+v", settled, firstFrame)
	}
}

// A track/drag change while opening must affect the visible fold, not only
// the peek that appears after the panel has already disappeared.
func TestClosingFollowsCurrentPeekAfterOpeningStateChanges(t *testing.T) {
	oldFiles, oldCount, oldCursor := dragFiles, dragFilesChange, cursorPoint
	dragFiles = func(*shelf) []string { return nil }
	dragFilesChange = func(*shelf) int64 { return 0 }
	cursorPoint = func() mygo.Point { return mygo.Point{} }
	t.Cleanup(func() { dragFiles, dragFilesChange, cursorPoint = oldFiles, oldCount, oldCursor })
	for _, name := range []string{"track stops", "drag starts", "drag ends"} {
		t.Run(name, func(t *testing.T) {
			makePanel := func() (*shelf, *ui.Tester) {
				a := motionWindow()
				a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Title: "Track"}}
				a.modules = []module{a.np}
				a.dragging = name == "drag ends"
				v := ui.NewTester(a.view, sheetW, panelTestH)
				v.Frame()
				a.peeking = true
				for range 24 {
					v.Frame()
					v.Advance(16 * time.Millisecond)
				}
				a.visible = true
				v.Frame()
				v.Advance(96 * time.Millisecond)
				v.Frame()
				return a, v
			}
			changed, changedView := makePanel()
			control, controlView := makePanel()
			switch name {
			case "track stops":
				changed.np.have = false
			case "drag starts":
				changed.dragging = true
			case "drag ends":
				changed.dragging = false
			}
			changed.visible, changed.peeking = false, false
			control.visible, control.peeking = false, false
			for range 3 {
				changedView.Frame()
				controlView.Frame()
				changedView.Advance(16 * time.Millisecond)
				controlView.Advance(16 * time.Millisecond)
			}
			// Each axis must move toward the live peek shape: the changing
			// shelf follows its new target, the control keeps the old one.
			// The hover wings are narrower but shorter than the chevron,
			// so the two axes no longer share a direction.
			cw, ch := changed.peekSize()
			ow, oh := control.peekSize()
			check := func(axis string, got, want, target, other float32) {
				dir := float32(0)
				switch {
				case target > other:
					dir = 1
				case target < other:
					dir = -1
				default:
					t.Skipf("%s: both states share the peek target %.1f", axis, target)
				}
				if dir*(got-want) <= 0.1 {
					t.Fatalf("the fold still uses the old peek shape on %s: changed %.3f, unchanged %.3f (targets %.1f vs %.1f)",
						axis, got, want, target, other)
				}
			}
			check("width", changed.fx.w, control.fx.w, cw, ow)
			check("height", changed.fx.h, control.fx.h, ch, oh)
		})
	}
}
