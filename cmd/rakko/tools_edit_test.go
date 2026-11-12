//go:build darwin

package main

// Stage 3: the editing gestures. The page is the real registered tools
// page; the tester drives the same pointer handlers the window does.

import (
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
