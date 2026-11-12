//go:build darwin

package main

// Stage 3: the editing gestures. The page is the real registered tools
// page; the tester drives the same pointer handlers the window does.

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/settings"
)

func editPage(t *testing.T, cards []layout.Card) (*shelf, *toolsPage, *ui.Tester) {
	t.Helper()
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(), tab: pageTools, level: 2, visible: true,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.ensurePages()
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: cards}
	p := a.toolsPage()
	p.editing = true
	view := toolsTester(a)
	view.Frame()
	view.Frame()
	return a, p, view
}

// TestToolsEditingAddsTheDrawer: the editing shell is 80pt taller and
// shows the card library; leaving editing takes it back.
func TestToolsEditingAddsTheDrawer(t *testing.T) {
	a := &shelf{dataDir: t.TempDir(), settings: settings.Default(), tab: pageTools, level: 2, visible: true}
	a.ensurePages()
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: layout.DefaultWith(true).Cards}
	p := a.toolsPage()
	if got := a.pageShellH(); got != 250 {
		t.Fatalf("resting height = %v, want 250", got)
	}
	p.editing = true
	if got := a.pageShellH(); got != 330 {
		t.Fatalf("editing height = %v, want 250 + the 80pt drawer", got)
	}
	p.editing = false
	if got := a.pageShellH(); got != 250 {
		t.Fatalf("height after editing = %v, want 250", got)
	}
}

// TestToolsLibraryDragIntoGrid: a tile dragged from the drawer into the
// grid inserts its card there instead of appending it.
func TestToolsLibraryDragIntoGrid(t *testing.T) {
	a, p, view := editPage(t, []layout.Card{{ID: "ocr"}, {ID: "zip"}})
	tile, ok := view.Find("截图")
	if !ok {
		t.Fatalf("the drawer tile is missing; texts %v", view.Texts())
	}
	target, ok := view.Find("框选文字")
	if !ok {
		t.Fatal("the first card is missing")
	}
	view.Press(tile.X+tile.W/2, tile.Y+tile.H/2)
	view.Move(target.X+target.W/2, target.Y+target.H/2)
	view.Frame()
	if p.press == nil || !p.press.fresh || p.press.index < 0 {
		t.Logf("viewport=%+v scroll=%v tile=%+v target=%+v", p.viewport, p.scroll, tile, target)
		t.Fatalf("the tile did not start a fresh drag: %+v", p.press)
	}
	view.Release(target.X+target.W/2, target.Y+target.H/2)
	view.Advance(500 * time.Millisecond)
	view.Frame()
	cards := a.toolsLayout().Cards
	at := -1
	for i, c := range cards {
		if c.ID == "shot" {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("the dragged card was not added: %v", idsOf(cards))
	}
	if at == len(cards)-1 && len(cards) > 1 {
		t.Fatalf("the dragged card was appended, not placed at the slot: %v", idsOf(cards))
	}
}

// TestToolsDragToLibraryRemoves: a grid card released over the drawer
// leaves the page.
func TestToolsDragToLibraryRemoves(t *testing.T) {
	a, p, view := editPage(t, []layout.Card{{ID: "ocr"}, {ID: "zip"}})
	card, ok := view.Find("打成 zip")
	if !ok {
		t.Fatal("the zip card is missing")
	}
	tile, ok := view.Find("截图")
	if !ok {
		t.Fatal("the drawer tile is missing")
	}
	view.Press(card.X+card.W/2, card.Y+card.H/2)
	view.Move(tile.X+tile.W/2, tile.Y+tile.H/2)
	view.Frame()
	if !p.overLibrary() {
		t.Fatalf("the pointer is not over the drawer: press=%+v viewport=%+v", p.press, p.viewport)
	}
	view.Release(tile.X+tile.W/2, tile.Y+tile.H/2)
	view.Advance(300 * time.Millisecond)
	view.Frame()
	for _, c := range a.toolsLayout().Cards {
		if c.ID == "zip" {
			t.Fatalf("the card was not removed by the drawer drop: %v", idsOf(a.toolsLayout().Cards))
		}
	}
	if len(a.pins) != 0 {
		t.Fatalf("the drawer drop pinned the card: %d pins", len(a.pins))
	}
}

// TestToolsResizeHandleSnapsAndSaves: dragging the arc grows the card to
// the nearest size, the page follows live, and layout.json is written only
// on the release.
func TestToolsResizeHandleSnapsAndSaves(t *testing.T) {
	a, p, view := editPage(t, []layout.Card{
		{ID: "awake", Span: 1, Rows: 1}, {ID: "ocr"}, {ID: "zip"},
	})
	view.Advance(300 * time.Millisecond)
	view.Frame()
	handle, ok := view.Find(a.appLoc().ToolsResize())
	if !ok {
		t.Fatalf("the resize handle is missing; texts %v", view.Texts())
	}
	view.Press(handle.X+handle.W/2, handle.Y+handle.H/2)
	view.Move(handle.X+handle.W/2+90, handle.Y+handle.H/2)
	view.Frame()
	if p.rs == nil || p.rs.cur != [2]int{2, 1} {
		t.Fatalf("the drag did not snap to 2×1: %+v", p.rs)
	}
	if got := a.pageShellW(); got != 440 {
		t.Fatalf("the panel did not follow the transient size: %v", got)
	}
	for _, c := range a.toolsLayout().Cards {
		if c.ID == "awake" && c.Cols() != 1 {
			t.Fatal("the in-memory layout changed before the release")
		}
	}
	view.Release(handle.X+handle.W/2+90, handle.Y+handle.H/2)
	view.Frame()
	for _, c := range a.toolsLayout().Cards {
		if c.ID == "awake" && c.Cols() != 2 {
			t.Fatalf("the release did not save 2×1: %+v", c)
		}
	}
}

// TestToolsContextMenuSetsSizeAndRemoves: a right-click opens the card
// menu; the size shapes set the rectangle and the red item removes the
// card.
func TestToolsContextMenuSetsSizeAndRemoves(t *testing.T) {
	a, p, view := editPage(t, []layout.Card{{ID: "awake", Span: 2, Rows: 1}, {ID: "ocr"}})
	card, ok := view.Find("保持唤醒")
	if !ok {
		t.Fatal("the awake card is missing")
	}
	view.RightClickAt(card.X+card.W/2, card.Y+card.H/2)
	view.Frame()
	l := a.appLoc()
	shape, ok := view.Find(l.CellSize(1, 1))
	if !ok {
		t.Fatalf("the menu has no 1×1 shape; texts %v", view.Texts())
	}
	view.ClickAt(shape.X+shape.W/2, shape.Y+shape.H/2)
	view.Frame()
	for _, c := range a.toolsLayout().Cards {
		if c.ID == "awake" && c.Cols() != 1 {
			t.Fatalf("the size shape did not set 1×1: %+v", c)
		}
	}
	// Reopen and remove.
	card, ok = view.Find("保持唤醒")
	if !ok {
		t.Fatal("the awake card left the page")
	}
	view.RightClickAt(card.X+card.W/2, card.Y+card.H/2)
	view.Frame()
	remove, ok := view.Find(l.ToolsMenuRemove())
	if !ok {
		t.Fatalf("the menu has no remove item; texts %v", view.Texts())
	}
	view.ClickAt(remove.X+remove.W/2, remove.Y+remove.H/2)
	view.Frame()
	for _, c := range a.toolsLayout().Cards {
		if c.ID == "awake" {
			t.Fatal("the remove item left the card")
		}
	}
	if p.menu != nil {
		t.Fatal("the menu stayed open after the action")
	}
}

// TestToolsMenuPinsCard: the menu's pin item commits the same free pin as
// a drag-out and takes the card off the page.
func TestToolsMenuPinsCard(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	oldOpen := pinOpenWindow
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow = oldOpen })
	a := &shelf{dataDir: t.TempDir(), settings: settings.Default(), tab: pageTools, level: 2, visible: true,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{}}
	a.ensurePages()
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "ocr"}, {ID: "zip"}}}
	a.dispID = 1
	p := a.toolsPage()
	view := toolsTester(a)
	view.Frame()
	view.Frame()
	card, ok := view.Find("框选文字")
	if !ok {
		t.Fatal("the ocr card is missing")
	}
	view.RightClickAt(card.X+card.W/2, card.Y+card.H/2)
	view.Frame()
	pin, ok := view.Find(a.appLoc().ToolsMenuPin())
	if !ok {
		t.Fatalf("the menu has no pin item; texts %v", view.Texts())
	}
	// The menu is drawn over the shell, not clipped by the 200pt page
	// content: every item must sit inside the shell.
	if pin.Y+pin.H > a.pageShellH() {
		t.Fatalf("the menu is clipped by the short page: item %+v in a %vpt shell", pin, a.pageShellH())
	}
	view.ClickAt(pin.X+pin.W/2, pin.Y+pin.H/2)
	view.Frame()
	if len(a.pins) != 1 || a.pins[0].cardID != "ocr" {
		t.Fatalf("the menu did not pin ocr: %d pins", len(a.pins))
	}
	for _, c := range a.toolsLayout().Cards {
		if c.ID == "ocr" {
			t.Fatal("the pinned card stayed in the grid")
		}
	}
	if p.menu != nil {
		t.Fatal("the pin item left the menu open")
	}
}

// TestToolsLegacySizeKeepsItsShape: a layout written before the size list
// changed (awake 1 × 2) still shows its current rectangle in the menu, so
// the accent marks a size the user can see and keep.
func TestToolsLegacySizeKeepsItsShape(t *testing.T) {
	a, p, view := editPage(t, []layout.Card{{ID: "awake", Span: 1, Rows: 2}, {ID: "ocr"}})
	card, ok := view.Find("保持唤醒")
	if !ok {
		t.Fatal("the awake card is missing")
	}
	view.RightClickAt(card.X+card.W/2, card.Y+card.H/2)
	view.Frame()
	if !view.HasText(a.appLoc().CellSize(1, 2)) {
		t.Fatalf("the menu dropped the card's current 1 × 2 shape; texts %v", view.Texts())
	}
	if p.menu == nil {
		t.Fatal("the menu did not open")
	}
}

// TestToolsEditChromeClearsTheGlyph: the ⊖ badge sits at the card's
// corner, clear of the editing glyph, and its 18pt square stays inside
// the shell even in the first row and the first column.
func TestToolsEditChromeClearsTheGlyph(t *testing.T) {
	// The music 2 × 2 keeps its live player in editing (v2 §6); its cover
	// sits at 20pt, clear of the badge like every edit glyph.
	for _, id := range []string{"awake", "ocr", "zip", "fit1m", "note", "recent", "clip", "timer", "stopwatch"} {
		t.Run(id, func(t *testing.T) {
			a, _, view := editPage(t, []layout.Card{{ID: id}})
			view.Advance(300 * time.Millisecond) // the chrome's 220ms pop
			view.Frame()
			minus, ok := view.Find(a.appLoc().Remove())
			if !ok {
				t.Fatalf("the ⊖ badge is missing; texts %v", view.Texts())
			}
			glyph, ok := view.Find("edit-glyph:" + id)
			if !ok {
				t.Fatalf("the %s edit glyph is missing", id)
			}
			if minus.X < glyph.X+glyph.W && glyph.X < minus.X+minus.W &&
				minus.Y < glyph.Y+glyph.H && glyph.Y < minus.Y+minus.H {
				t.Fatalf("the ⊖ badge %+v covers the glyph %+v", minus, glyph)
			}
			// The 220ms pop's spring overshoots a hair past 1 at rest;
			// the badge is 18pt before its scale.
			if math.Abs(float64(minus.W-18)) > 0.1 || math.Abs(float64(minus.H-18)) > 0.1 {
				t.Fatalf("the ⊖ badge = %v×%v, want 18×18", minus.W, minus.H)
			}
			if minus.X < earR || minus.Y < 0 ||
				minus.X+minus.W > earR+a.pageShellW() || minus.Y+minus.H > a.pageShellH() {
				t.Fatalf("the ⊖ badge %+v is clipped by the shell", minus)
			}
		})
	}
}

// TestToolsDropBelowThePanelPins: only the library's own rectangle is a
// removal target. A card released below the panel or beside the library
// is still a drag-out and pins; it must not be deleted from layout.json.
func TestToolsDropBelowThePanelPins(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	oldOpen := pinOpenWindow
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow = oldOpen })

	for _, tc := range []struct {
		name string
		at   func(a *shelf, ear float32) (x, y int)
	}{
		{"below", func(a *shelf, ear float32) (int, int) { return int(ear + 100), int(a.pageShellH()) + 8 }},
		{"beside", func(a *shelf, ear float32) (int, int) { return int(ear) - 6, int(a.pageShellH()) - 40 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, view := editPage(t, []layout.Card{{ID: "ocr"}, {ID: "zip"}})
			card, ok := view.Find("框选文字")
			if !ok {
				t.Fatal("the ocr card is missing")
			}
			x, y := tc.at(a, earR)
			view.Press(card.X+card.W/2, card.Y+card.H/2)
			view.Move(float32(x), float32(y))
			view.Frame()
			view.Release(float32(x), float32(y))
			view.Frame()
			if len(a.pins) != 1 || a.pins[0].cardID != "ocr" {
				t.Fatalf("the drop deleted ocr without pinning it: %d pins, cards %v", len(a.pins), idsOf(a.toolsLayout().Cards))
			}
		})
	}
}

// TestToolsResizeHandleSurvivesReflow: a corner drag that re-packs its card
// out of the visible rows must still receive the release and write the
// chosen size. The handle cannot disappear mid-gesture.
func TestToolsResizeHandleSurvivesReflow(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	oldOpen := pinOpenWindow
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow = oldOpen })

	cards := []layout.Card{
		{ID: "ocr"}, {ID: "zip"}, {ID: "topdf"}, {ID: "tojpeg"}, {ID: "toword"},
		{ID: "extract"}, {ID: "resize"}, {ID: "airdrop"}, {ID: "shot"}, {ID: "fit1m"},
		{ID: "note"}, {ID: "awake"},
	}
	a, p, view := editPage(t, cards)
	view.Advance(300 * time.Millisecond) // the chrome's 220ms pop
	view.Frame()
	var awakeCell toolCell
	for _, cell := range p.grid().cells {
		if cell.id == "awake" {
			awakeCell = cell
		}
	}
	// The awake is the last cell; its arc handle's centre, from the same
	// shell/content geometry the chrome uses.
	hx := earR + sheetPadX + awakeCell.x + awakeCell.w - 14
	hy := toolsContentTop() + awakeCell.y - p.scroll.Y + awakeCell.h - 14
	view.Press(hx, hy)
	view.Move(hx+200, hy+180)
	view.Frame()
	rs := p.rs
	var movedCell toolCell
	for _, cell := range p.grid().cells {
		if cell.id == "awake" {
			movedCell = cell
		}
	}
	t.Logf("during: rs=%+v viewport=%+v awakeCell=%+v movedCell=%+v shell=%v", rs, p.viewport, awakeCell, movedCell, a.pageShellH())
	if rs == nil {
		t.Fatal("the handle press did not start a resize")
	}
	view.Release(hx+200, hy+180)
	view.Frame()
	if p.rs != nil {
		t.Fatalf("the resize never ended: %+v", p.rs)
	}
	var got layout.Card
	for _, c := range a.toolsLayout().Cards {
		if c.ID == "awake" {
			got = c
		}
	}
	if got.Cols() != 2 || got.Height() != 2 {
		t.Fatalf("the release did not write 2×2: %+v", got)
	}
}

// TestToolsFreshDragKeepsTheGridWidth: starting a library drag must use
// the grid's current columns, not the minimum 3 the drag lock would fall
// back to before it is recorded.
func TestToolsFreshDragKeepsTheGridWidth(t *testing.T) {
	nine := []layout.Card{
		{ID: "playing", Span: 2, Rows: 2}, {ID: "awake"}, {ID: "usage"}, {ID: "ocr"}, {ID: "zip"},
		{ID: "timer", Span: 2, Rows: 1}, {ID: "stopwatch"}, {ID: "clip", Span: 2, Rows: 1}, {ID: "airdrop"},
	}
	a, p, view := editPage(t, nine)
	wantCols := p.grid().cols
	wantW := a.pageShellW()
	if wantCols < 6 {
		t.Fatalf("the fixture is not a 6-column page: %d columns", wantCols)
	}
	tile, ok := view.Find("截图")
	if !ok {
		t.Fatalf("the shot tile is missing: %v", view.Texts())
	}
	view.Press(tile.X+tile.W/2, tile.Y+tile.H/2)
	view.Move(tile.X+tile.W/2+30, tile.Y+tile.H/2)
	view.Frame()
	if got := p.dragCols; got != wantCols {
		t.Fatalf("the fresh drag locked %d columns, want %d", got, wantCols)
	}
	if got := a.pageShellW(); got != wantW {
		t.Fatalf("the fresh drag shrank the shell to %v, want %v", got, wantW)
	}
}

// TestToolsFreshDropOutsideTheGridCancels: a library tile released outside
// the visible grid is a cancelled drag, not a card appended at the nearest
// cell. Only a release over the grid adds.
func TestToolsFreshDropOutsideTheGridCancels(t *testing.T) {
	a, _, view := editPage(t, []layout.Card{{ID: "ocr"}, {ID: "zip"}})
	tile, ok := view.Find("截图")
	if !ok {
		t.Fatalf("the shot tile is missing: %v", view.Texts())
	}
	// Above the grid, in the action row: inside the panel, outside the
	// scroll viewport.
	view.Press(tile.X+tile.W/2, tile.Y+tile.H/2)
	view.Move(tile.X+tile.W/2, 10)
	view.Release(tile.X+tile.W/2, 10)
	view.Frame()
	if slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == "shot" }) {
		t.Fatalf("a drop outside the grid added shot: %v", idsOf(a.toolsLayout().Cards))
	}
}

// TestToolsDragKeepsItsTallestRows: once a drag needs three rows, moving
// the card back to a two-row spot during the same gesture must not shrink
// the panel and the drawer under the pointer.
func TestToolsDragKeepsItsTallestRows(t *testing.T) {
	a, p, _ := editPage(t, []layout.Card{
		{ID: "awake", Span: 2, Rows: 2}, {ID: "ocr"}, {ID: "zip"}, {ID: "topdf"},
		{ID: "tojpeg"}, {ID: "toword"}, {ID: "extract"}, {ID: "resize"},
	})
	g0 := p.grid()
	if g0.cols != 6 || g0.rows != 2 {
		t.Fatalf("the fixture is %d×%d, want 6 columns and 2 rows", g0.cols, g0.rows)
	}
	var front toolCell
	for _, cell := range g0.cells {
		if cell.id == "awake" {
			front = cell
		}
	}
	// The end position after the seven singles: the first free 2×2 is at
	// row 1, columns 1–2.
	p.press = &toolPress{id: "awake", dragging: true, px: 207, py: 177}
	p.dragCols, p.dragRows = g0.cols, g0.rows
	gEnd := p.grid()
	t.Logf("end: idx=%d grid=%+v", p.press.index, gEnd)
	if got := gEnd.rows; got != 3 {
		t.Fatalf("the back position needs 3 rows, got %d", got)
	}
	p.press.px, p.press.py = front.x+front.w/2, front.y+front.h/2
	if got := p.grid().rows; got != 3 {
		t.Fatalf("the panel shrank to %d rows while the pointer returned to the front", got)
	}
	if got := a.pageShellH(); got != 420 {
		t.Fatalf("the shell shrank to %v during the drag, want 420", got)
	}
}

// TestToolsMenuPinUsesTheCardHeight: pinning a 2×2 card from the menu must
// create the same 84pt window as a drag-out and remember its row span for
// a return to the tools page.
func TestToolsMenuPinUsesTheCardHeight(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	oldOpen := pinOpenWindow
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow = oldOpen })

	a, p, view := editPage(t, []layout.Card{{ID: "playing", Span: 2, Rows: 2}, {ID: "ocr"}})
	view.Frame()
	p.pinFromMenu("playing")
	if len(a.pins) != 1 {
		t.Fatalf("the menu did not pin playing: %d pins", len(a.pins))
	}
	it := a.pins[0]
	if it.frame.Height != pinCardH {
		t.Fatalf("the menu pin is %dpt high, want %d", it.frame.Height, pinCardH)
	}
	if it.rows != 2 {
		t.Fatalf("the menu pin stored %d rows, want 2", it.rows)
	}
}

// TestToolsLibraryHitRectFollowsTheIslandTop: the drawer is drawn 38pt
// below the window's top on an island. Its hit rectangle must use that
// drawn position: a release on the grid's last row sorts, and a release on
// the drawer's bottom removes, instead of the two swapping places.
func TestToolsLibraryHitRectFollowsTheIslandTop(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	oldOpen := pinOpenWindow
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow = oldOpen })

	newPage := func(t *testing.T) (*shelf, *toolsPage, *ui.Tester) {
		a := islandShelf(true, settings.ShellStyleIsland)
		a.dataDir = t.TempDir()
		a.tab, a.level, a.visible = pageTools, 2, true
		a.ensurePages()
		a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "ocr"}, {ID: "zip"}}}
		p := a.toolsPage()
		p.editing = true
		view := toolsTester(a)
		view.Frame()
		view.Frame()
		view.Advance(300 * time.Millisecond)
		view.Frame()
		return a, p, view
	}
	toWindow := func(p *toolsPage, x, y float32) (float32, float32) {
		return p.viewport.X + x - p.scroll.X, p.viewport.Y + y - p.scroll.Y
	}
	drag := func(t *testing.T, view *ui.Tester, fromX, fromY, toX, toY float32) {
		view.Press(fromX, fromY)
		view.Move(toX, toY)
		view.Frame()
		view.Release(toX, toY)
		view.Frame()
	}

	t.Run("last row sorts", func(t *testing.T) {
		a, p, view := newPage(t)
		var cell toolCell
		for _, c := range p.grid().cells {
			if c.id == "zip" {
				cell = c
			}
		}
		zip, ok := view.Find("打成 zip")
		if !ok {
			t.Fatalf("zip missing: %v", view.Texts())
		}
		// The bottom of the last grid row: inside the grid, below the
		// drawer's real top, but above the wrongly-offset old top.
		_, ty := toWindow(p, cell.x+cell.w/2, cell.y+cell.h-6)
		drag(t, view, zip.X+zip.W/2, zip.Y+zip.H/2, p.viewport.X+cell.x+cell.w/2, ty)
		found := false
		for _, c := range a.toolsLayout().Cards {
			if c.ID == "zip" {
				found = true
			}
		}
		if !found {
			t.Fatalf("the last-row drop removed zip instead of sorting: %v", idsOf(a.toolsLayout().Cards))
		}
		if len(a.pins) != 0 {
			t.Fatalf("the last-row drop pinned zip: %d pins", len(a.pins))
		}
	})

	t.Run("drawer bottom removes", func(t *testing.T) {
		a, p, view := newPage(t)
		zip, ok := view.Find("打成 zip")
		if !ok {
			t.Fatalf("zip missing: %v", view.Texts())
		}
		// The drawer's drawn bottom, from the shell's own top offset.
		shellTop := float32(a.sheetTop())
		contentBottom := shellTop + a.pageShellH() - p.viewport.Y + p.scroll.Y
		x, ty := toWindow(p, toolColW(2)/2, contentBottom-10)
		drag(t, view, zip.X+zip.W/2, zip.Y+zip.H/2, x, ty)
		found := false
		for _, c := range a.toolsLayout().Cards {
			if c.ID == "zip" {
				found = true
			}
		}
		if found {
			t.Fatalf("the drawer-bottom drop did not remove zip: %v", idsOf(a.toolsLayout().Cards))
		}
		if len(a.pins) != 0 {
			t.Fatalf("the drawer-bottom drop pinned zip instead of removing it: %d pins", len(a.pins))
		}
	})
}
