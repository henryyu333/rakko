//go:build darwin

package main

// The adaptive panel (design/explore/tools-v3.html §2): the shell follows
// the card grid's packing, the band's chips get priority over a narrow
// panel, and a reorder drag pins the columns it started on.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/settings"
)

var (
	_ pageWidth    = (*toolsPage)(nil)
	_ pageHeight   = (*toolsPage)(nil)
	_ pageEnvelope = (*toolsPage)(nil)
)

// toolsPanelShelf is a registered tools page with a placeholder native
// window, so view() takes its production spring path.
func toolsPanelShelf(t *testing.T, cards []layout.Card) *shelf {
	t.Helper()
	a := &shelf{
		store: mustStore(t), dataDir: t.TempDir(),
		tab: pageTools, level: 2, visible: true, progress: 1,
		notchX: 600, notchY: 0, notchW: 180, notchH: 32, x: 410, y: 0,
		settings:   settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{},
	}
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: cards}
	a.ensurePages()
	a.win = &mygo.Window{}
	return a
}

func settleToolPanel(view *ui.Tester) {
	for range 40 {
		view.Advance(16 * time.Millisecond)
		view.Frame()
	}
}

// TestToolsPanelFollowsCards: the page's shell is the packing's box, and a
// card added through the regular mutation path moves it on the next frame.
func TestToolsPanelFollowsCards(t *testing.T) {
	a := toolsPanelShelf(t, []layout.Card{{ID: "ocr"}, {ID: "zip"}})
	p := a.toolsPage()
	view := ui.NewTester(a.view, int(toolPanelW(toolMaxCols))+2*earR,
		int(toolPanelH(toolMaxRows))+toolLibraryH+navGap+navH+2)
	settleToolPanel(view)
	if got := a.pageShellW(); got != 334.5 {
		t.Fatalf("two action cards ask for %vpt, want 334.5", got)
	}
	if got := a.pageShellH(); got != 160 {
		t.Fatalf("one row asks for %vpt, want 160", got)
	}

	// A third row of cards: 5 × 1 grows the panel to 4 × 2.
	a.mutateTools(func(l *layout.Layout) {
		l.AddCardSize("awake", 1, 1)
		l.AddCardSize("fit1m", 1, 1)
		l.AddCardSize("airdrop", 1, 1)
	})
	settleToolPanel(view)
	if got := a.pageShellW(); got != 440 {
		t.Fatalf("five cards ask for %vpt, want 440", got)
	}
	if got := a.pageShellH(); got != 250 {
		t.Fatalf("two rows ask for %vpt, want 250", got)
	}
	// The cards took the new columns: the last one is on the second row.
	last := a.toolsLayout().Cards[len(a.toolsLayout().Cards)-1].ID
	g := p.grid()
	found := false
	for _, cell := range g.cells {
		if cell.id == last {
			found = true
			if cell.y == 0 {
				t.Fatalf("%s stayed on the first row after the repack", last)
			}
		}
	}
	if !found {
		t.Fatalf("%s has no cell", last)
	}
}

// widthOutsideTest keeps gofmt honest about the interface assertion; it
// is a compile-time claim the page implements pageWidth, not a runtime flag.
func (p *toolsPage) widthOutsideTest() bool {
	var _ pageWidth = p
	var _ pageHeight = p
	var _ pageEnvelope = p
	return true
}

// TestToolsPanelSpringsToTheNewBox drives the frame clock: a packing
// change moves the shell on the 300 ms level curve, never in one frame,
// while the native window's envelope stays put.
func TestToolsPanelSpringsToTheNewBox(t *testing.T) {
	a := toolsPanelShelf(t, []layout.Card{{ID: "ocr"}, {ID: "zip"}})
	view := ui.NewTester(a.view, int(toolPanelW(toolMaxCols))+2*earR,
		int(toolPanelH(toolMaxRows))+toolLibraryH+navGap+navH+2)
	settleToolPanel(view)
	if got := a.fx.w; got != 334.5 {
		t.Fatalf("settled shell is %vpt wide, want 334.5", got)
	}
	env := a.sessionFrame()
	if env.Width < int(toolPanelW(toolMaxCols))+2*earR {
		t.Fatalf("session frame %+v cannot hold the tools page's widest box", env)
	}
	frame := a.sessionFrame()

	a.mutateTools(func(l *layout.Layout) {
		for _, id := range []string{"awake", "fit1m", "airdrop"} {
			l.AddCardSize(id, 1, 1)
		}
	})
	// The first frame after the change still draws the old box: springs
	// do not jump.
	view.Frame()
	if got := a.fx.pageW; got >= 440 {
		t.Fatalf("the shell jumped to %vpt instead of springing", got)
	}
	view.Advance(100 * time.Millisecond)
	view.Frame()
	mid := a.fx.pageW
	if mid <= 334.5 || mid >= 440 {
		t.Fatalf("mid-flight width = %v, want between the two boxes", mid)
	}
	if got := a.fx.pageH; got <= 160 || got >= 250 {
		t.Fatalf("mid-flight height = %v, want between 160 and 250", got)
	}
	// The native window keeps the envelope it took before the change; the
	// drawn shell's frame must stay inside it for the whole flight.
	if d := a.desiredFrame(); d.Width > frame.Width || d.Height > frame.Height {
		t.Fatalf("the drawn frame %+v escapes the held envelope %+v", d, frame)
	}
	settleToolPanel(view)
	if a.fx.pageW != 440 || a.fx.pageH != 250 {
		t.Fatalf("settled page box = %vx%v, want 440x250", a.fx.pageW, a.fx.pageH)
	}
	if a.fx.w != 440 || a.fx.h != 250 {
		t.Fatalf("settled shell = %vx%v, want 440x250", a.fx.w, a.fx.h)
	}

	// Reduce Motion: the same change lands at once.
	view.SetPreferences(ui.Preferences{ReduceMotion: true})
	a.mutateTools(func(l *layout.Layout) { l.RemoveCard("awake") })
	a.mutateTools(func(l *layout.Layout) { l.RemoveCard("fit1m") })
	a.mutateTools(func(l *layout.Layout) { l.RemoveCard("airdrop") })
	view.Frame()
	if a.fx.pageW != 334.5 || a.fx.pageH != 160 {
		t.Fatalf("Reduce Motion left the shell at %vx%v, want 334.5x160", a.fx.pageW, a.fx.pageH)
	}
}

// TestToolsBandPriorityWidensPanel: a running countdown's long reading does
// not fit the notch style's 3-column ears, so the page takes a fourth
// column and leaves it empty. The island's continuous band needs no extra
// column.
func TestToolsBandPriorityWidensPanel(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	newShelf := func(island bool) *shelf {
		a := toolsPanelShelf(t, []layout.Card{{ID: "ocr"}})
		a.island = island
		a.notchW = 180
		a.registerClockModules()
		m := a.clock
		m.now = func() time.Time { return now }
		m.playSound = func() {}
		m.stopSound = func() {}
		m.phase, m.dur = timerRunning, 24*time.Hour
		m.end = now.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
		return a
	}

	notch := newShelf(false)
	p := notch.toolsPage()
	if got := p.width(); got != 440 {
		t.Fatalf("notch: a 23:59:59 chip asks for %vpt, want the 4-column 440", got)
	}
	// The extra column is empty: the one card stays in the first column.
	g := p.grid()
	if g.cols != 4 {
		t.Fatalf("notch: grid is %d columns, want 4", g.cols)
	}
	for _, cell := range g.cells {
		if cell.x != 0 {
			t.Fatalf("notch: %s moved into the reserved column: %+v", cell.id, cell)
		}
	}

	island := newShelf(true)
	if got := island.toolsPage().width(); got != 334.5 {
		t.Fatalf("island: a 23:59:59 chip asks for %vpt, want the 3-column 334.5", got)
	}
}

// TestToolsReorderDragPinsColumns: while a reorder drag is in flight the
// panel keeps the columns it began with (rows may grow). The drop releases
// the pin and the panel takes the committed packing.
func TestToolsReorderDragPinsColumns(t *testing.T) {
	a := toolsPanelShelf(t, []layout.Card{
		{ID: "ocr"}, {ID: "zip"}, {ID: "airdrop"}, {ID: "fit1m"}, {ID: "topdf"},
	})
	p := a.toolsPage()
	p.editing = true
	if g := p.grid(); g.cols != 4 {
		t.Fatalf("five cards pack into %d columns, want 4", g.cols)
	}
	p.dragCols, p.dragRows = 3, 1
	p.press = &toolPress{id: "ocr", dragging: true, index: 0, px: 2 * (toolCellW + toolCellGap), py: 0}
	g := p.grid()
	if g.cols != 3 {
		t.Fatalf("a dragging panel widened to %d columns", g.cols)
	}
	if got := p.width(); got != 334.5 {
		t.Fatalf("a dragging panel is %vpt wide, want the pinned 334.5", got)
	}
	if g.rows < 2 {
		t.Fatalf("the pinned packing lost its rows: %+v", g)
	}
	p.press, p.dragCols, p.dragRows = nil, 0, 0
	if got := p.width(); got != 440 {
		t.Fatalf("after the drop the panel stayed at %vpt, want 440", got)
	}
}
