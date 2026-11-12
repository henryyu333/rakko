//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/mac"
	"rakko/internal/store"
)

var pinTestArea = mygo.Rectangle{X: 0, Y: 25, Width: 1512, Height: 920}

// withPinDisplays feeds the geometry a fixed display set.
func withPinDisplays(t *testing.T, ds []mygo.Display) {
	t.Helper()
	old := pinDisplays
	pinDisplays = func() []mygo.Display { return ds }
	t.Cleanup(func() { pinDisplays = old })
}

var pinTestDisplays = []mygo.Display{
	{ID: 1, Primary: true, Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1512, Height: 982}, WorkArea: pinTestArea},
}

// pinRegister adds a windowless pin: everything the state machine owns,
// nothing native. The window opens in addPin/show on the real path.
func (a *shelf) pinRegister(it *pinItem) *pinItem {
	it.owner = a
	a.pins = append(a.pins, it)
	return it
}

func TestPinReleaseEdgeBoundary(t *testing.T) {
	for _, c := range []struct {
		x    int
		want pinEdge
	}{
		{0, pinLeft}, {pinSnapDist, pinLeft}, {pinSnapDist + 1, pinFree},
		{-pinSnapDist, pinLeft}, // a hair past the edge still docks
		{pinTestArea.Width - pinSnapDist, pinRight},
		{pinTestArea.Width - pinSnapDist - 1, pinFree},
		{pinTestArea.Width + pinSnapDist, pinRight},
		{pinTestArea.Width / 2, pinFree},
	} {
		if got := pinReleaseEdge(mygo.Point{X: c.x, Y: 400}, pinTestArea); got != c.want {
			t.Errorf("x=%d: got edge %v, want %v", c.x, got, c.want)
		}
	}
}

func TestPinExpandAndStripFrames(t *testing.T) {
	r := pinExpandFrame(pinLeft, pinTestArea, pinCardW, 500)
	if r.X != 0 || r.Width != pinCardW || r.Height != pinCardH || r.Y != 500-pinCardH/2 {
		t.Fatalf("left expand frame wrong: %+v", r)
	}
	r = pinExpandFrame(pinRight, pinTestArea, pinCardW, 500)
	if r.X != pinTestArea.Width-pinCardW || r.Y != 500-pinCardH/2 {
		t.Fatalf("right expand frame wrong: %+v", r)
	}
	s := pinStripFrame(pinLeft, pinTestArea, 500)
	if s.X != 0 || s.Width != pinStripW || s.Height != pinStripH || s.Y != 500-pinStripH/2 {
		t.Fatalf("left strip frame wrong: %+v", s)
	}
	s = pinStripFrame(pinRight, pinTestArea, 500)
	if s.X != pinTestArea.Width-pinStripW || s.Y != 500-pinStripH/2 {
		t.Fatalf("right strip frame wrong: %+v", s)
	}
	// Centres above the menu bar / below the work area clamp back inside.
	if s := pinStripFrame(pinLeft, pinTestArea, 0); s.Y != pinTestArea.Y {
		t.Fatalf("strip at the top edge should clamp to %d, got %+v", pinTestArea.Y, s)
	}
	if s := pinStripFrame(pinLeft, pinTestArea, 2000); s.Y+pinStripH != pinTestArea.Y+pinTestArea.Height {
		t.Fatalf("strip at the bottom edge should end at %d, got %+v", pinTestArea.Y+pinTestArea.Height, s)
	}
}

func TestPinExpandCollapseTiming(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := &shelf{}
	it := a.pinRegister(&pinItem{
		cardID: "zip", cardW: pinCardW, display: 1, edge: pinLeft,
		frame: pinStripFrame(pinLeft, pinTestArea, 500),
	})
	t0 := time.Now()
	over := mygo.Point{X: 4, Y: 500}
	// Hover the strip: it expands flush to the edge on the next tick.
	a.pinTick(t0, over, false, false)
	if !it.expanded || it.frame.X != 0 || it.frame.Width != pinCardW || it.frame.Y != 500-pinCardH/2 {
		t.Fatalf("hover expand: expanded=%v frame=%+v", it.expanded, it.frame)
	}
	// Pointer leaves: the collapse waits pinHideDelay, not less.
	out := mygo.Point{X: 500, Y: 500}
	t1 := t0.Add(300 * time.Millisecond)
	a.pinTick(t1, out, false, false) // the leave itself starts the countdown
	a.pinTick(t1.Add(pinHideDelay-50*time.Millisecond), out, false, false)
	if !it.expanded {
		t.Fatal("collapsed before pinHideDelay")
	}
	a.pinTick(t1.Add(pinHideDelay+50*time.Millisecond), out, false, false)
	if it.expanded || it.frame != pinStripFrame(pinLeft, pinTestArea, 500) {
		t.Fatalf("collapse after delay: expanded=%v frame=%+v", it.expanded, it.frame)
	}
	// Re-hover mid-countdown cancels it; a fresh leave starts it over.
	it.expanded = true
	it.frame = pinExpandFrame(pinLeft, pinTestArea, it.cardW, 500)
	a.pinTick(t0.Add(2*time.Second), over, false, false)
	a.pinTick(t0.Add(2*time.Second+pinHideDelay), out, false, false)
	if !it.expanded {
		t.Fatal("collapsing on the leave tick alone — the countdown is off")
	}
	a.pinTick(t0.Add(2*time.Second+2*pinHideDelay), out, false, false)
	if it.expanded {
		t.Fatal("second collapse did not happen on time")
	}
}

func TestPinFileDragWakesAndHoldsStrip(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := &shelf{}
	it := a.pinRegister(&pinItem{
		cardID: "zip", cardW: pinCardW, display: 1, edge: pinRight,
		frame: pinStripFrame(pinRight, pinTestArea, 400),
	})
	t0 := time.Now()
	// A file drag pinWakeDist from the strip opens it for the drop; an
	// empty-handed pointer at the same distance does not.
	near := mygo.Point{X: pinTestArea.Width - pinStripW - pinWakeDist + 1, Y: 400}
	a.pinTick(t0, near, false, true)
	if !it.expanded || it.frame.X != pinTestArea.Width-pinCardW {
		t.Fatalf("file drag near strip: expanded=%v frame=%+v", it.expanded, it.frame)
	}
	a.pinTick(t0.Add(pinHideDelay*2), near, false, false)
	if !it.expanded {
		t.Fatal("an empty pointer merely near should not hold the card open")
	}
	// A drag that stays near keeps it open past the hide delay.
	a.pinTick(t0.Add(3*time.Second), near, false, true)
	if !it.expanded {
		t.Fatal("a file drag near the card let it collapse mid-drag")
	}
}

func TestPinAlwaysExpanded(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := &shelf{}
	it := a.pinRegister(&pinItem{
		cardID: "zip", cardW: pinCardW, display: 1, edge: pinLeft, always: true,
		frame: pinStripFrame(pinLeft, pinTestArea, 300),
	})
	a.pinTick(time.Now(), mygo.Point{X: 900, Y: 900}, false, false)
	if !it.expanded || it.frame.Width != pinCardW {
		t.Fatalf("always-expanded pin stayed a strip: %+v", it.frame)
	}
	// …and the hide delay never collapses it.
	it.leftAt = time.Now().Add(-time.Hour)
	a.pinTick(time.Now(), mygo.Point{X: 900, Y: 900}, false, false)
	if !it.expanded {
		t.Fatal("always-expanded pin collapsed")
	}
}

func TestPinStagger(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	pins := []*pinItem{
		{display: 1, edge: pinLeft, frame: pinStripFrame(pinLeft, pinTestArea, 300)},
		{display: 1, edge: pinLeft, frame: pinStripFrame(pinLeft, pinTestArea, 320)}, // overlaps the first
		{display: 1, edge: pinRight, frame: pinStripFrame(pinRight, pinTestArea, 300)},
	}
	pinStagger(pins)
	if pins[1].frame.Y != pins[0].frame.Y+pinStripH+pinStripGap {
		t.Fatalf("same-edge strips did not stagger: %+v %+v", pins[0].frame, pins[1].frame)
	}
	if pins[2].frame.Y != 300-pinStripH/2 {
		t.Fatalf("other-edge strip moved: %+v", pins[2].frame)
	}
}

func TestPinMoveSnapAndReturn(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := &shelf{dataDir: t.TempDir()}
	it := a.pinRegister(&pinItem{
		cardID: "zip", cardW: pinCardW, display: 1,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH},
	})
	tr := ui.NewTester(it.view, pinCardW, pinCardH)
	tr.Press(0, 0)
	tr.Move(20, 20)
	t0 := time.Now()
	// Press goes far → the window follows the cursor.
	a.pinTick(t0, mygo.Point{X: 620, Y: 420}, true, false)
	if !it.moving {
		t.Fatal("a 20-pt press move did not start a window move")
	}
	a.pinTick(t0, mygo.Point{X: 30, Y: 500}, true, false)
	if it.frame.X != 30 || it.frame.Y != 500 {
		t.Fatalf("card did not follow the cursor: %+v", it.frame)
	}
	// Release near the left edge: docked as a strip.
	tr.Release(float32(10-it.frame.X), float32(500-it.frame.Y))
	if it.edge != pinLeft || it.expanded || it.frame != pinStripFrame(pinLeft, pinTestArea, 500) {
		t.Fatalf("release at the edge: edge=%v frame=%+v", it.edge, it.frame)
	}
}

// setTools seeds the shared layout the tools page accessor serves.
func (a *shelf) setTools(l layout.Layout) { a.toolsLay = &l }

func TestPinReturnToNotch(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := &shelf{dataDir: t.TempDir(), notchX: pinTestArea.Width/2 - 100, notchY: 0, notchW: 200, notchH: 32}
	a.setTools(layout.Layout{})
	it := a.pinRegister(&pinItem{
		cardID: "zip", cardW: pinCardW, display: 1, homeIndex: 0,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH},
	})
	a.mutateTools(func(l *layout.Layout) { l.Pins = []layout.Pin{{ID: "zip", Span: 1}} })
	tr := ui.NewTester(it.view, pinCardW, pinCardH)
	tr.Press(0, 0)
	tr.Move(100, -340)
	t0 := time.Now()
	a.pinTick(t0, mygo.Point{X: 700, Y: 60}, true, false)
	if !it.moving {
		t.Fatal("drag did not start")
	}
	// The pin's centre crosses the return zone → the bulge is up.
	a.pinTick(t0, mygo.Point{X: pinTestArea.Width / 2, Y: 80}, true, false)
	if !a.pinReturning {
		t.Fatal("a dragged pin over the notch did not set the return bulge")
	}
	tr.Release(float32(pinTestArea.Width/2-40-it.frame.X), float32(30-it.frame.Y))
	a.pinTick(t0, mygo.Point{X: pinTestArea.Width/2 - 40, Y: 30}, false, false)
	if len(a.pins) != 0 {
		t.Fatalf("release on the notch kept the pin: %+v", it.frame)
	}
	if a.pinReturning {
		t.Fatal("the bulge flag did not clear on release")
	}
	// putBack: the card went back to the tools page at its home slot.
	if l := a.toolsLayout(); len(l.Cards) != 1 || l.Cards[0].ID != "zip" {
		t.Fatalf("the card did not return to the grid: %+v", l.Cards)
	}
}

func TestPinUnpinSemantics(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := &shelf{dataDir: t.TempDir()}
	// 放回刘海: the card rejoins the grid at its home slot.
	a.setTools(layout.Layout{Cards: []layout.Card{{ID: "awake", Span: 1}}})
	it := a.pinRegister(&pinItem{
		cardID: "topdf", cardW: pinCardW, display: 1, homeIndex: 1,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH},
	})
	a.mutateTools(func(l *layout.Layout) { l.Pins = []layout.Pin{{ID: "topdf", Span: 1}} })
	a.unpin(it, true)
	if l := a.toolsLayout(); len(l.Cards) != 2 || l.Cards[0].ID != "awake" || l.Cards[1].ID != "topdf" {
		t.Fatalf("放回刘海 did not restore the home slot: %+v", l.Cards)
	}
	// 从桌面移除: the pin goes, the card stays off the page.
	it2 := a.pinRegister(&pinItem{
		cardID: "awake", cardW: pinCardW, display: 1, homeIndex: 0,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH},
	})
	a.mutateTools(func(l *layout.Layout) {
		l.Pins = []layout.Pin{{ID: "awake", Span: 1}}
		l.Cards = l.Cards[1:] // awake was pinned out of the grid; topdf stays
	})
	a.unpin(it2, false)
	if l := a.toolsLayout(); len(l.Cards) != 1 || l.Cards[0].ID != "topdf" {
		t.Fatalf("从桌面移除 put the card back: %+v", l.Cards)
	}
	// A home slot inserts at that index (design §4 原位/被占放末尾由
	// RemovePin 的 putBack clamp 处理).
	it3 := a.pinRegister(&pinItem{
		cardID: "zip", cardW: pinCardW, display: 1, homeIndex: 0,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH},
	})
	a.mutateTools(func(l *layout.Layout) { l.Pins = []layout.Pin{{ID: "zip", Span: 1}} })
	a.unpin(it3, true)
	if l := a.toolsLayout(); len(l.Cards) != 2 || l.Cards[0].ID != "zip" {
		t.Fatalf("home slot insert wrong: %+v", l.Cards)
	}
	// A pin restored from layout.json has no remembered slot: 放回刘海
	// appends it, it does not jump to the front.
	a.setTools(layout.Layout{Cards: []layout.Card{{ID: "awake", Span: 1}}})
	it4 := a.pinRegister(&pinItem{
		cardID: "resize", cardW: pinCardW, display: 1, homeIndex: -1,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH},
	})
	a.mutateTools(func(l *layout.Layout) { l.Pins = []layout.Pin{{ID: "resize", Span: 1}} })
	a.unpin(it4, true)
	if l := a.toolsLayout(); len(l.Cards) != 2 || l.Cards[1].ID != "resize" {
		t.Fatalf("a restored pin did not append at the end: %+v", l.Cards)
	}
}

func TestPinDisplaysChanged(t *testing.T) {
	ext := mygo.Display{ID: 2, Bounds: mygo.Rectangle{X: 1512, Y: 0, Width: 1920, Height: 1080},
		WorkArea: mygo.Rectangle{X: 1512, Y: 25, Width: 1920, Height: 1055}}
	withPinDisplays(t, append([]mygo.Display{pinTestDisplays[0]}, ext))
	a := &shelf{dataDir: t.TempDir()}
	it := a.pinRegister(&pinItem{
		cardID: "zip", cardW: pinCardW, display: 2, edge: pinRight,
		frame: mygo.Rectangle{X: 1512 + 1920 - pinStripW, Y: 400, Width: pinStripW, Height: pinStripH},
	})
	// The external display goes away: the pin keeps its edge on the primary.
	withPinDisplays(t, pinTestDisplays)
	a.pinDisplaysChanged()
	if it.display != 1 || it.edge != pinRight ||
		it.frame.X != pinTestArea.Width-pinStripW {
		t.Fatalf("unplugged display: display=%d frame=%+v", it.display, it.frame)
	}
}

func TestPinLayoutRoundTrip(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	dir := t.TempDir()
	a := &shelf{dataDir: dir}
	a.setTools(layout.Layout{Version: layout.Version})
	a.pinRegister(&pinItem{
		cardID: "awake", span: 1, cardW: pinCardW, display: 1, edge: pinRight, always: true,
		frame: mygo.Rectangle{X: 1512 - 8, Y: 300, Width: pinStripW, Height: pinStripH},
	})
	a.savePins()
	// layout.json carries the pin; restore rebuilds it (positions are
	// offsets inside the display's work area).
	data, err := os.ReadFile(filepath.Join(dir, layout.FileName))
	if err != nil {
		t.Fatal(err)
	}
	var f layout.Layout
	if err := json.Unmarshal(data, &f); err != nil || len(f.Pins) != 1 ||
		f.Pins[0].ID != "awake" || f.Pins[0].Edge != layout.EdgeRight || !f.Pins[0].AlwaysOpen {
		t.Fatalf("layout.json wrong: %v %+v", err, f.Pins)
	}
	b := &shelf{dataDir: dir}
	if l := b.toolsLayout(); len(l.Pins) != 1 || l.Pins[0].ID != "awake" {
		t.Fatalf("restored pins wrong: %+v", l.Pins)
	}
}

// pinViewShelf is a headless shelf whose pin view builds in a Tester.
func pinViewShelf(t *testing.T, cardID string, expanded bool) (*shelf, *pinItem, *ui.Tester) {
	t.Helper()
	a := &shelf{dataDir: t.TempDir()}
	it := a.pinRegister(&pinItem{
		cardID: cardID, cardW: pinCardW, display: 1, edge: pinLeft, expanded: expanded,
		frame: mygo.Rectangle{X: 0, Y: 300, Width: pinCardW, Height: pinCardH},
	})
	return a, it, ui.NewTester(func(c *ui.Context) { it.view(c) }, pinCardW, pinCardH)
}

func TestPinViewMenuItems(t *testing.T) {
	_, it, tr := pinViewShelf(t, "zip", true)
	tr.RightClickAt(40, 40)
	menu := tr.Menu()
	want := []string{"始终展开", "-", "放回刘海", "从桌面移除"}
	if len(menu) != len(want) {
		t.Fatalf("menu = %v, want %v", menu, want)
	}
	for i := range want {
		if menu[i] != want[i] {
			t.Fatalf("menu = %v, want %v", menu, want)
		}
	}
	// 从桌面移除 marks the pin for the tick's destroy without putBack.
	if err := tr.ChooseMenuItem("从桌面移除"); err != nil {
		t.Fatal(err)
	}
	if !it.wantKill || it.putBack {
		t.Fatalf("从桌面移除: wantKill=%v putBack=%v", it.wantKill, it.putBack)
	}
}

func TestPinReturnPeekDrawsBulge(t *testing.T) {
	// The 松开放回 pill is the existing peek-shape mechanism, one new
	// kind: the drop pill's size, the put-back's words.
	a := motionWindow()
	a.pinReturning, a.peeking = true, true
	a.peekProgress, a.peekFrom = 1, 1
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	if _, ok := view.Find("松开放回"); !ok {
		t.Fatal("the return pill is not up while a pin drag holds the notch")
	}
	if w, _ := a.peekSize(); w < float32(max(180, a.notchW))+dropPillExtraW {
		t.Fatalf("return peek narrower than the drop pill: %.1f", w)
	}
	// The drag leaves the zone: the kind is re-derived and folds away.
	a.pinReturning, a.peeking = false, false
	foldFrames(t, a, view, "松开放回")
}

// TestToolsDragOutPins is the phase-3 hook end to end: an edit-mode drag
// released outside the panel's drawn body pins the card at the cursor and
// takes it out of the tools page grid; a release back on another pane of
// the panel still flies back.
func TestToolsDragOutPins(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	oldOpen := pinOpenWindow
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow = oldOpen })
	oldCursor := cursorPoint
	t.Cleanup(func() { cursorPoint = oldCursor })

	a := &shelf{dataDir: t.TempDir()}
	a.setTools(layout.Default())
	p := &toolsPage{host: a, editing: true}

	// Panel at 400..760 x 0..600 on the test display.
	placePanel := func() {
		a.x, a.y = 400, 0
	}
	placePanel()
	cursorPoint = func() mygo.Point { return mygo.Point{X: 1400, Y: 500} } // far right of the panel

	first := a.toolsLayout().Cards[0]
	d := &toolPress{id: first.ID, dragging: true, grabDX: 10, grabDY: 10, px: 200, py: 1200}
	p.toolCardDrop(d, cursorPoint())

	if len(a.pins) != 1 || a.pins[0].cardID != first.ID {
		t.Fatalf("drag-out did not pin the card: %+v", a.pins)
	}
	if a.pins[0].edge != pinFree {
		t.Fatalf("a release away from an edge should float, got edge %v", a.pins[0].edge)
	}
	if slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == first.ID }) {
		t.Fatalf("the pinned card stayed in the grid: %+v", a.toolsLayout().Cards)
	}
	if len(a.toolsLayout().Pins) != 1 || a.toolsLayout().Pins[0].ID != first.ID {
		t.Fatalf("layout.json's pin list missed the drag-out: %+v", a.toolsLayout().Pins)
	}
	if !d.settling && p.press != nil {
		t.Fatal("a pinned drag should not leave a settle press behind")
	}

	// A release over the panel but outside the stage is not a pin: it
	// flies back to its cell.
	second := a.toolsLayout().Cards[0]
	cursorPoint = func() mygo.Point { return mygo.Point{X: 700, Y: 100} } // inside the panel body
	d2 := &toolPress{id: second.ID, dragging: true, grabDX: 10, grabDY: 10, px: 200, py: 1200}
	p.toolCardDrop(d2, cursorPoint())
	if len(a.pins) != 1 {
		t.Fatalf("a release inside the panel pinned the card: %d pins", len(a.pins))
	}
	if !d2.settling {
		t.Fatal("a release inside the panel should settle back")
	}
}

// pinDiskPins reads the pins that layout.json actually holds for dir —
// the next launch's truth, not the in-memory copy.
func pinDiskPins(t *testing.T, dir string) []layout.Pin {
	t.Helper()
	l, err := layout.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return l.Pins
}

// mustStore opens a throwaway tray store for the drop-fallback tests.
func mustStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestPinQuitCloseKeepsPin is review P1: a window close while quitting is
// the app closing, not a put-back — the live pin and its layout.json
// entry survive, so the next launch restores it.
func TestPinQuitCloseKeepsPin(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	dir := t.TempDir()
	a := &shelf{dataDir: dir}
	a.mutateTools(func(l *layout.Layout) {
		l.AddPin(layout.Pin{ID: "zip", Span: 1, Display: "1", X: 600, Y: 375})
	})
	a.quitting = true
	it := a.pinRegister(&pinItem{cardID: "zip", span: 1, cardW: pinCardW, display: 1, edge: pinFree,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
	it.onClose(nil)
	if len(a.pins) != 1 {
		t.Fatal("the quit-time close removed the live pin")
	}
	disk := pinDiskPins(t, dir)
	if len(disk) != 1 || disk[0].ID != "zip" {
		t.Fatalf("the quit-time close rewrote layout.json: %+v", disk)
	}
	if slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == "zip" }) {
		t.Fatal("the quit-time close put the card back on the tools page")
	}
	// A close while not quitting still returns the card, as before.
	a.quitting = false
	it.onClose(nil)
	if len(a.pins) != 0 {
		t.Fatal("a real close did not unpin")
	}
	if !slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == "zip" }) {
		t.Fatal("a real close did not return the card to the grid")
	}
}

// TestPinDropUsesOnlyItsOwnFiles is review P2's class-match trap: the
// cached panel-drag class happens to suit the card, so the pre-fix code
// converted the previous drag's paths. The pin must act on the files its
// own transfer carried.
func TestPinDropUsesOnlyItsOwnFiles(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	dir := t.TempDir()
	stale := writeFile(t, dir, "stale.png", testPNGBytes(t))
	fresh := writeFile(t, dir, "fresh.png", testPNGBytes(t))
	a := &shelf{dataDir: dir}
	a.store = mustStore(t)
	a.conv = &converter{host: a}
	f := fakeConverters(t)
	// The leftover panel-drag snapshot: same class the card accepts.
	a.drop.files, a.drop.class = []string{stale}, classImages
	it := a.pinRegister(&pinItem{cardID: "topdf", cardW: pinCardW, display: 1, edge: pinFree,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
	a.pinReceive(it, fileDrop(t, fresh))
	if len(f.pdfSrcs) != 1 || len(f.pdfSrcs[0]) != 1 || f.pdfSrcs[0][0] != fresh {
		t.Fatalf("the pin's drop ran on %v, not the file it carried", f.pdfSrcs)
	}
	if slices.ContainsFunc(a.store.Items, func(it store.Item) bool { return it.Path == stale || it.Path == fresh }) {
		t.Fatal("the pin's drop shelved the source file")
	}
}

// TestPinDropUnsupportedShelves is review P2's other half: the card does
// not take this drop's class — even though the stale snapshot's class
// would match — so the files land in the tray instead of vanishing.
func TestPinDropUnsupportedShelves(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	dir := t.TempDir()
	stale := writeFile(t, dir, "stale.png", testPNGBytes(t))
	fresh := writeFile(t, dir, "fresh.zip", []byte("PK\x03\x04fake"))
	a := &shelf{dataDir: dir}
	a.store = mustStore(t)
	a.conv = &converter{host: a}
	f := fakeConverters(t)
	// The stale snapshot accepts what tojpeg wants; this drop does not.
	a.drop.files, a.drop.class = []string{stale}, classImages
	it := a.pinRegister(&pinItem{cardID: "tojpeg", cardW: pinCardW, display: 1, edge: pinFree,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
	a.pinReceive(it, fileDrop(t, fresh))
	if len(f.formats) != 0 {
		t.Fatalf("an unsupported drop still converted: %v", f.formats)
	}
	if len(a.store.Items) != 1 || a.store.Items[0].Path != fresh {
		t.Fatalf("the unsupported drop did not land in the tray: %+v", a.store.Items)
	}
}

// TestPinDropUnstartableShelves covers the rest of review P2's contract:
// when the action's pipeline cannot run — a job already in flight, or a
// start that fails — the files still land in the tray.
func TestPinDropUnstartableShelves(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	dir := t.TempDir()
	fresh := writeFile(t, dir, "fresh.png", testPNGBytes(t))
	mkPin := func(a *shelf) *pinItem {
		return a.pinRegister(&pinItem{cardID: "tojpeg", cardW: pinCardW, display: 1, edge: pinFree,
			frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
	}
	t.Run("busy", func(t *testing.T) {
		a := &shelf{dataDir: dir}
		a.store = mustStore(t)
		a.conv = &converter{host: a, job: &convertJob{target: toJPEG, started: time.Now()}}
		f := fakeConverters(t)
		a.pinReceive(mkPin(a), fileDrop(t, fresh))
		if len(f.formats) != 0 {
			t.Fatalf("a busy pipeline still converted: %v", f.formats)
		}
		if len(a.store.Items) != 1 || a.store.Items[0].Path != fresh {
			t.Fatalf("the busy drop did not land in the tray: %+v", a.store.Items)
		}
	})
	t.Run("start fails", func(t *testing.T) {
		a := &shelf{dataDir: dir}
		a.store = mustStore(t)
		a.conv = &converter{host: a}
		f := fakeConverters(t)
		if err := os.RemoveAll(a.store.ConvertedDir()); err != nil {
			t.Fatal(err)
		}
		a.pinReceive(mkPin(a), fileDrop(t, fresh))
		if len(f.formats) != 0 {
			t.Fatalf("a failed start still converted: %v", f.formats)
		}
		if len(a.store.Items) != 1 || a.store.Items[0].Path != fresh {
			t.Fatalf("the unstartable drop did not land in the tray: %+v", a.store.Items)
		}
	})
}

// TestPinDragReleaseIsNotATap is review P3: a release that ends a window
// move must not fire the card's tap, whether the release frame reaches
// the view before the poll (the usual order) or after the poll sampled
// the lifted button. The 保持唤醒 card's power assertion is the real tap
// effect the test watches — no execution seam.
func TestPinDragReleaseIsNotATap(t *testing.T) {
	for _, tickFirst := range []bool{false, true} {
		name := "release frame beats the poll"
		if tickFirst {
			name = "the poll samples release before the pointer event"
		}
		t.Run(name, func(t *testing.T) {
			withPinDisplays(t, pinTestDisplays)
			p := newFakePower()
			a := &shelf{dataDir: t.TempDir()}
			a.setTools(layout.Default())
			a.awake = &awakeModule{host: a, create: p.create, release: p.release}
			it := a.pinRegister(&pinItem{cardID: "awake", span: 1, cardW: pinCardW, display: 1,
				edge: pinFree, expanded: true,
				frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
			tr := ui.NewTester(func(c *ui.Context) { it.view(c) }, pinCardW, pinCardH)
			tr.Press(20, 20)
			if !it.pressed || it.didMove {
				t.Fatal("the press did not arm a tap")
			}
			tr.Move(40, 40)
			a.pinTick(time.Now(), mygo.Point{X: 640, Y: 440}, true, false) // the press went far: a move
			if !it.moving || !it.didMove {
				t.Fatal("the move did not mark didMove")
			}
			if tickFirst {
				// A sampled button-up cannot end the event-owned session.
				a.pinTick(time.Now(), mygo.Point{X: 640, Y: 440}, false, false)
				if !it.pressed || !it.moving {
					t.Fatal("the poll ended the session before its PointerUp")
				}
				tr.Release(20, 20)
			} else {
				tr.Release(20, 20)
				a.pinTick(time.Now(), mygo.Point{X: 640, Y: 440}, false, false)
			}
			if a.awake.active() {
				t.Fatal("a moved release ran the card's tap")
			}
			// A press that never moves still taps afterwards.
			tr.Press(20, 20)
			tr.Release(20, 20)
			if !a.awake.active() {
				t.Fatal("a clean tap after the drag did not run the card")
			}
		})
	}
}

// TestPinMoveFollowsAcrossDisplays is review P4: the dragged card tracks
// the cursor's display each tick, and a release — with or without a move
// tick on the far side — settles on the display the cursor reached.
func TestPinMoveFollowsAcrossDisplays(t *testing.T) {
	ext := mygo.Display{ID: 2, Bounds: mygo.Rectangle{X: 1512, Y: 0, Width: 1920, Height: 1080},
		WorkArea: mygo.Rectangle{X: 1512, Y: 25, Width: 1920, Height: 1055}}
	withPinDisplays(t, append([]mygo.Display{pinTestDisplays[0]}, ext))
	a := &shelf{dataDir: t.TempDir()}
	a.setTools(layout.Default())
	mk := func() *pinItem {
		return a.pinRegister(&pinItem{cardID: "zip", cardW: pinCardW, display: 1,
			frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
	}
	it := mk()
	tr := ui.NewTester(it.view, pinCardW, pinCardH)
	tr.Press(10, 10)
	tr.Move(40, 40)
	a.pinTick(time.Now(), mygo.Point{X: 640, Y: 440}, true, false) // moving
	// The cursor crosses the seam; the card clamps into display 2's area.
	a.pinTick(time.Now(), mygo.Point{X: 1600, Y: 500}, true, false)
	if it.display != 2 {
		t.Fatalf("the card stayed on display %d while the cursor crossed", it.display)
	}
	if it.frame.X != 1600 || it.frame.Y != 500 {
		t.Fatalf("the card did not follow onto the new display: %+v", it.frame)
	}
	// Releasing on display 2's right edge docks there, not back on 1.
	tr.Release(float32(3440-it.frame.X), float32(500-it.frame.Y))
	if it.display != 2 || it.edge != pinRight || it.expanded {
		t.Fatalf("the release did not dock on display 2: display=%d edge=%v expanded=%v", it.display, it.edge, it.expanded)
	}
	if it.frame.X != 1512+1920-pinStripW {
		t.Fatalf("the strip did not flush to display 2's right edge: %+v", it.frame)
	}
	// A fast release: the cursor crosses and the button lifts between two
	// move ticks — the release still lands on display 2.
	it = mk()
	tr = ui.NewTester(it.view, pinCardW, pinCardH)
	tr.Press(10, 10)
	tr.Move(40, 40)
	a.pinTick(time.Now(), mygo.Point{X: 640, Y: 440}, true, false)
	tr.Release(float32(3440-it.frame.X), float32(500-it.frame.Y))
	if it.display != 2 || it.edge != pinRight {
		t.Fatalf("the between-ticks release did not land on display 2: display=%d edge=%v", it.display, it.edge)
	}
}

// TestPinStaggerOverflowAndCollapse is review P5: a column that
// overhangs the work area's bottom shifts back up, one taller than the
// work area squeezes inside it, and a collapsing card re-spaces the
// strips still on the edge.
func TestPinStaggerOverflowAndCollapse(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	bottom := pinTestArea.Y + pinTestArea.Height
	mkCol := func(n int, ys ...int) []*pinItem {
		var col []*pinItem
		for i := 0; i < n; i++ {
			y := pinTestArea.Y + i*(pinStripH+pinStripGap)
			if i < len(ys) {
				y = ys[i]
			}
			col = append(col, &pinItem{display: 1, edge: pinLeft,
				frame: mygo.Rectangle{X: 0, Y: y, Width: pinStripW, Height: pinStripH}})
		}
		return col
	}
	assertInside := func(t *testing.T, col []*pinItem) {
		t.Helper()
		for i, it := range col {
			if it.frame.Y < pinTestArea.Y || it.frame.Y+pinStripH > bottom {
				t.Fatalf("strip %d sits off the work area: %+v", i, it.frame)
			}
		}
	}
	t.Run("overflow pulls back", func(t *testing.T) {
		col := mkCol(2, bottom-pinStripH-4, bottom-4)
		pinStagger(col)
		assertInside(t, col)
		if col[1].frame.Y-col[0].frame.Y != pinStripH+pinStripGap {
			t.Fatalf("the pair lost its spacing: %+v %+v", col[0].frame, col[1].frame)
		}
	})
	t.Run("fitting column keeps spacing", func(t *testing.T) {
		col := mkCol(0)
		for i := 0; i < 14; i++ { // 14 strips need 888pt, fitting the 920pt work area
			col = append(col, &pinItem{display: 1, edge: pinLeft,
				frame: mygo.Rectangle{X: 0, Y: pinTestArea.Y + i*30, Width: pinStripW, Height: pinStripH}})
		}
		pinStagger(col)
		assertInside(t, col)
		for i := 1; i < len(col); i++ {
			if col[i].frame.Y-col[i-1].frame.Y != pinStripH+pinStripGap {
				t.Fatalf("a fitting column squeezed: strips %d/%d", i-1, i)
			}
		}
	})
	t.Run("head above top reflows", func(t *testing.T) {
		col := mkCol(0)
		for _, y := range []int{-40, -10} { // a column saved before bounds tightened
			col = append(col, &pinItem{display: 1, edge: pinLeft,
				frame: mygo.Rectangle{X: 0, Y: y, Width: pinStripW, Height: pinStripH}})
		}
		pinStagger(col)
		assertInside(t, col)
		if col[1].frame.Y-col[0].frame.Y != pinStripH+pinStripGap {
			t.Fatalf("a fitting column squeezed: %+v %+v", col[0].frame, col[1].frame)
		}
	})
	t.Run("overfull squeezes inside", func(t *testing.T) {
		col := mkCol(20)
		pinStagger(col)
		assertInside(t, col)
		for i := 1; i < len(col); i++ {
			if col[i].frame.Y < col[i-1].frame.Y {
				t.Fatalf("the squeeze reordered strips: %d at %d after %d", i, col[i].frame.Y, col[i-1].frame.Y)
			}
		}
	})
	t.Run("collapse respaces the edge", func(t *testing.T) {
		a := &shelf{dataDir: t.TempDir()}
		lo := a.pinRegister(&pinItem{cardID: "zip", cardW: pinCardW, display: 1, edge: pinLeft,
			frame: mygo.Rectangle{X: 0, Y: 814, Width: pinStripW, Height: pinStripH}})
		hi := a.pinRegister(&pinItem{cardID: "awake", cardW: pinCardW, display: 1, edge: pinLeft, expanded: true,
			leftAt: time.Now().Add(-2 * pinHideDelay),
			frame:  mygo.Rectangle{X: 0, Y: 800, Width: pinCardW, Height: pinCardH}})
		a.pinTick(time.Now(), mygo.Point{X: 1200, Y: 200}, false, false)
		if hi.expanded {
			t.Fatal("the delayed collapse did not run")
		}
		// The pair ends up as the two stacked slots — which strip took
		// which slot does not matter (they collided at the same Y).
		slots := map[int]bool{hi.frame.Y: true, lo.frame.Y: true}
		if len(slots) != 2 || !slots[814] || !slots[814+pinStripH+pinStripGap] {
			t.Fatalf("the collapsing card did not re-space its edge: hi=%+v lo=%+v", hi.frame, lo.frame)
		}
	})
}

// TestPinSaveWritesStripFrame is review P6: an expanded docked pin saves
// its collapsed strip rect, not the open card.
func TestPinSaveWritesStripFrame(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := &shelf{dataDir: t.TempDir()}
	a.setTools(layout.Default())
	a.pinRegister(&pinItem{cardID: "zip", span: 1, cardW: pinCardW, display: 1,
		edge: pinRight, expanded: true,
		frame: mygo.Rectangle{X: pinTestArea.Width - pinCardW, Y: 400, Width: pinCardW, Height: pinCardH}})
	a.savePins()
	p := a.toolsLayout().Pins[0]
	if p.X != pinTestArea.Width-pinStripW {
		t.Fatalf("saved X=%d is the card's left edge, not the strip's", p.X)
	}
	wantY := 400 + pinCardH/2 - pinStripH/2 - pinTestArea.Y
	if p.Y != wantY {
		t.Fatalf("saved Y=%d want strip top %d", p.Y, wantY)
	}
}

// TestPinSaveRestoreStable is review P6's drift: an always-expanded
// docked pin is what gets saved while open, so save → restore → re-expand
// must converge immediately and repeat identically — including clamped
// at the work area's bottom edge, where the card's centre is not the
// strip's.
func TestPinSaveRestoreStable(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	dir := t.TempDir()
	old := pinOpenWindow
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow = old })
	a := &shelf{dataDir: dir}
	a.setTools(layout.Default())
	// The strip at the last legal slot: its expanded card clamps against
	// the work area's bottom, the boundary case for centre math.
	cy := pinTestArea.Y + pinTestArea.Height - pinStripH/2
	it := a.pinRegister(&pinItem{cardID: "zip", span: 1, cardW: pinCardW, display: 1,
		edge: pinRight, always: true,
		frame: pinStripFrame(pinRight, pinTestArea, cy)})
	a.pinTick(time.Now(), mygo.Point{X: 1200, Y: 200}, false, false) // 始终展开 re-opens it
	if !it.expanded {
		t.Fatal("the always-open pin did not expand")
	}
	var prev layout.Pin
	for cycle := 0; cycle < 3; cycle++ {
		a.savePins()
		got := a.toolsLayout().Pins[0]
		if cycle > 0 && got != prev {
			t.Fatalf("cycle %d drifted: %+v → %+v", cycle, prev, got)
		}
		prev = got
		a.pins = nil
		a.pinRestore()
		if len(a.pins) != 1 {
			t.Fatalf("restore lost the pin: %+v", a.pins)
		}
		it := a.pins[0]
		if it.expanded || it.frame.Width != pinStripW {
			t.Fatalf("a docked pin must restore as its strip: %+v", it.frame)
		}
		if it.frame.X != pinTestArea.X+pinTestArea.Width-pinStripW {
			t.Fatalf("the restored strip is not flush to the right edge: %+v", it.frame)
		}
		// The next tick re-expands the always-open pin; that card is what
		// the next quit's savePins would see.
		a.pinTick(time.Now(), mygo.Point{X: 1200, Y: 200}, false, false)
		if !it.expanded {
			t.Fatal("the always-open pin did not re-expand after restore")
		}
	}
}

// TestPinUnplugCollapsesExpanded is review P7: an expanded docked pin on
// a display that goes away lands as a strip on the primary — so the
// always-open one re-expands on the next tick instead of drawing a card
// into an 8×56 window — and the stored display ID is rewritten.
func TestPinUnplugCollapsesExpanded(t *testing.T) {
	ext := mygo.Display{ID: 2, Bounds: mygo.Rectangle{X: 1512, Y: 0, Width: 1920, Height: 1080},
		WorkArea: mygo.Rectangle{X: 1512, Y: 25, Width: 1920, Height: 1055}}
	withPinDisplays(t, append([]mygo.Display{pinTestDisplays[0]}, ext))
	dir := t.TempDir()
	a := &shelf{dataDir: dir}
	a.setTools(layout.Default())
	it := a.pinRegister(&pinItem{cardID: "zip", span: 1, cardW: pinCardW, display: 2, edge: pinRight,
		expanded: true, always: true,
		frame: mygo.Rectangle{X: 1512 + 1920 - pinCardW, Y: 400, Width: pinCardW, Height: pinCardH}})
	floater := a.pinRegister(&pinItem{cardID: "tojpeg", span: 1, cardW: pinCardW, display: 2,
		edge: pinFree, expanded: true,
		frame: mygo.Rectangle{X: 2000, Y: 500, Width: pinCardW, Height: pinCardH}})
	withPinDisplays(t, pinTestDisplays)
	a.pinDisplaysChanged()
	if it.display != 1 || it.expanded || it.frame.Width != pinStripW || it.frame.X != pinTestArea.Width-pinStripW {
		t.Fatalf("the unplugged pin did not re-dock as a strip: display=%d %+v", it.display, it.frame)
	}
	if floater.display != 1 || !insideRect(mygo.Point{X: floater.frame.X, Y: floater.frame.Y}, pinTestArea) {
		t.Fatalf("the floating pin did not land inside the primary: %+v", floater.frame)
	}
	if p := a.toolsLayout().Pins[0]; p.Display != "1" {
		t.Fatalf("the stored display ID was not rewritten: %+v", p)
	}
	// The re-docked strip keeps working: 始终展开 re-opens it on the
	// primary on the very next tick.
	a.pinTick(time.Now(), mygo.Point{X: 1200, Y: 200}, false, false)
	if !it.expanded || it.frame.Width != pinCardW || it.frame.X != pinTestArea.Width-pinCardW {
		t.Fatalf("the always-open pin did not re-expand on the primary: %+v", it.frame)
	}
}

// TestPinRestoreRehomesDisplay is review P7's second half: a pin stored
// on a display no longer plugged in re-homes on restore and the stored
// ID is rewritten, so layout.json stops pointing at a missing screen.
func TestPinRestoreRehomesDisplay(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	dir := t.TempDir()
	a := &shelf{dataDir: dir}
	a.mutateTools(func(l *layout.Layout) {
		l.AddPin(layout.Pin{ID: "zip", Span: 1, Display: "999", Edge: layout.EdgeRight, X: 0, Y: 300})
	})
	old := pinOpenWindow
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow = old })
	a.pinRestore()
	if len(a.pins) != 1 || a.pins[0].display != 1 {
		t.Fatalf("the orphaned pin did not re-home: %+v", a.pins)
	}
	if p := a.toolsLayout().Pins[0]; p.Display != "1" {
		t.Fatalf("the stored display ID was not rewritten: %+v", p)
	}
	if disk := pinDiskPins(t, dir); len(disk) != 1 || disk[0].Display != "1" {
		t.Fatalf("layout.json still names the missing display: %+v", disk)
	}
}

// The return-zone verdict reads the release point, not the frame the
// last 30 ms poll applied: a drag whose last sampled centre sat on the
// notch but whose release happens far from it stays a pin, landed where
// the release was.
func TestPinReturnZoneReadsFinalReleasePoint(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := &shelf{dataDir: t.TempDir(), notchX: 600, notchY: 0, notchW: 180, notchH: 32}
	a.setTools(layout.Default())
	it := a.pinRegister(&pinItem{cardID: "zip", span: 1, cardW: pinCardW, display: 1,
		edge: pinFree, expanded: true,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
	tr := ui.NewTester(it.view, pinCardW, pinCardH)
	tr.Press(10, 10)
	tr.Move(22, -350)
	// The poll samples the frame with its centre on the notch...
	a.pinTick(time.Now(), mygo.Point{X: 622, Y: 50}, true, false)
	if !it.moving || !a.pinReturning {
		t.Fatalf("fixture: the sampled centre was not on the notch: moving=%v returning=%v", it.moving, a.pinReturning)
	}
	// ...and the release happens far from it.
	tr.Release(float32(1100-it.frame.X), float32(500-it.frame.Y))
	if len(a.pins) != 1 || a.pins[0] != it {
		t.Fatal("a release far from the notch returned the pin on the previous poll's centre")
	}
	if it.frame.X != 1100 || it.frame.Y != 500 {
		t.Fatalf("the release kept the sampled frame: %+v", it.frame)
	}
}

// A free release lands where the finger let go — the release point's own
// frame, not the last one the poll applied.
func TestPinFreeReleaseLandsAtReleasePoint(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := &shelf{dataDir: t.TempDir()}
	a.setTools(layout.Default())
	it := a.pinRegister(&pinItem{cardID: "zip", span: 1, cardW: pinCardW, display: 1,
		edge: pinFree, expanded: true,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
	tr := ui.NewTester(it.view, pinCardW, pinCardH)
	tr.Press(10, 10)
	tr.Move(50, 50)
	a.pinTick(time.Now(), mygo.Point{X: 650, Y: 450}, true, false) // the sampled frame
	tr.Release(float32(900-it.frame.X), float32(650-it.frame.Y))
	if it.frame.X != 900 || it.frame.Y != 650 {
		t.Fatalf("the release kept the previous poll's frame: %+v", it.frame)
	}
}

// A drag faster than the 30 ms poll must still count as a drag: the
// press session follows the real pointer events, so a press that moves
// past pinDragStart and releases between two polls settles on the
// release point and never runs the card's tap.
func TestPinFastDragBetweenPollsMovesNotTaps(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	power := newFakePower()
	a := &shelf{dataDir: t.TempDir()}
	a.setTools(layout.Default())
	a.awake = &awakeModule{host: a, create: power.create, release: power.release}
	it := a.pinRegister(&pinItem{cardID: "awake", span: 1, cardW: pinCardW, display: 1,
		edge: pinFree, expanded: true,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
	tr := ui.NewTester(func(c *ui.Context) { it.view(c) }, pinCardW, pinCardH)
	start := it.frame
	tr.Press(20, 20)
	if !it.pressed || it.didMove {
		t.Fatalf("the press did not arm a fresh session: pressed=%v didMove=%v", it.pressed, it.didMove)
	}
	tr.Move(120, 70) // past pinDragStart; no poll samples any of it
	tr.Release(120, 70)
	if it.frame == start {
		t.Fatalf("a drag the poll never sampled left the window where it was: %+v", it.frame)
	}
	if a.awake.active() {
		t.Fatal("the release ran the card's tap")
	}
}

// A fresh press before the next poll is its own gesture: the previous
// drag's release ends its session at the pointer event, so the next
// press starts clean instead of inheriting the drag's move.
func TestPinPressBeforeReleasePollIsFresh(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	power := newFakePower()
	a := &shelf{dataDir: t.TempDir()}
	a.setTools(layout.Default())
	a.awake = &awakeModule{host: a, create: power.create, release: power.release}
	it := a.pinRegister(&pinItem{cardID: "awake", span: 1, cardW: pinCardW, display: 1,
		edge: pinFree, expanded: true,
		frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
	tr := ui.NewTester(func(c *ui.Context) { it.view(c) }, pinCardW, pinCardH)
	tr.Press(20, 20)
	tr.Move(80, 60)
	a.pinTick(time.Now(), mygo.Point{X: 680, Y: 460}, true, false)
	tr.Release(20, 20)
	if a.awake.active() {
		t.Fatal("a dragged release ran the card's tap")
	}
	// No down=false poll ran in between: the next press is a new gesture.
	tr.Press(20, 20)
	if !it.pressed || it.didMove {
		t.Fatalf("the new press inherited the previous drag: pressed=%v didMove=%v", it.pressed, it.didMove)
	}
	tr.Release(20, 20)
	if !a.awake.active() {
		t.Fatal("a fresh tap after a drag did not run")
	}
}

func TestPinFailedRemovalKeepsPlacement(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	for _, putBack := range []bool{false, true} {
		t.Run(fmt.Sprint(putBack), func(t *testing.T) {
			dir := t.TempDir()
			a := &shelf{dataDir: dir}
			l := layout.Default()
			l.AddPin(layout.Pin{ID: "zip", Span: 1, Display: "1", X: 600, Y: 375})
			a.setTools(l)
			if err := layout.Write(dir, l); err != nil {
				t.Fatal(err)
			}
			it := a.pinRegister(&pinItem{cardID: "zip", span: 1, cardW: pinCardW, display: 1,
				frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW, Height: pinCardH}})
			if err := os.Chmod(dir, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
			a.unpin(it, putBack)
			if len(a.pins) != 1 || a.pins[0] != it {
				t.Fatal("failed removal discarded the live pin")
			}
			if len(a.toolsLayout().Pins) != 1 ||
				slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == "zip" }) {
				t.Fatal("failed removal changed the layout")
			}
		})
	}
}

func TestPinChildControlsKeepContextMenu(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	for _, tc := range []struct{ id, label string }{
		{"awake", "30 分"}, {"playing", "暂停"}, {"recent", "menu-thumb.png"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			a := &shelf{store: mustStore(t), dataDir: t.TempDir()}
			power := newFakePower()
			a.awake = &awakeModule{host: a, create: power.create, release: power.release}
			a.np = &nowPlayingModule{host: a, np: &mac.NowPlayingHost{}, have: true,
				last: mac.NowPlayingState{Title: "Menu Track", Playing: true, Rate: 1}}
			path := writeFile(t, t.TempDir(), "menu-thumb.png", testPNGBytes(t))
			a.store.Items = append(a.store.Items, store.Item{ID: "menu-thumb", Kind: store.KindFile, Path: path})
			it := a.pinRegister(&pinItem{cardID: tc.id, span: 2, cardW: pinCardW2, display: 1,
				edge: pinFree, expanded: true,
				frame: mygo.Rectangle{X: 600, Y: 400, Width: pinCardW2, Height: pinCardH}})
			tr := ui.NewTester(it.view, pinCardW2, pinCardH)
			r, ok := tr.Find(tc.label)
			if !ok {
				t.Fatalf("missing child control %q: %v", tc.label, tr.Texts())
			}
			tr.RightClickAt(r.X+r.W/2, r.Y+r.H/2)
			if !slices.Contains(tr.Menu(), "放回刘海") || !slices.Contains(tr.Menu(), "从桌面移除") {
				t.Fatalf("right click on %s lost the pin menu: %v", tc.id, tr.Menu())
			}
			tr.CloseMenu()
			if it.pressed || a.awake.active() || !a.np.last.Playing || a.ownDrag {
				t.Fatal("right click started a primary-button action")
			}
			if tc.id != "recent" {
				tr.Press(r.X+r.W/2, r.Y+r.H/2)
				tr.Release(r.X+r.W/2, r.Y+r.H/2)
				if tc.id == "awake" && (!a.awake.active() || a.awake.duration != 30*time.Minute) {
					t.Fatal("primary click no longer selects the awake duration")
				}
				if tc.id == "playing" && a.np.last.Playing {
					t.Fatal("primary click no longer pauses playback")
				}
			}
		})
	}
}

func TestPinCloseWriteFailureKeepsWindow(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	dir := t.TempDir()
	a := &shelf{dataDir: dir}
	l := layout.Default()
	l.AddPin(layout.Pin{ID: "zip", Span: 1, Display: "1"})
	a.setTools(l)
	if err := layout.Write(dir, l); err != nil {
		t.Fatal(err)
	}
	w := &mygo.Window{}
	it := a.pinRegister(&pinItem{cardID: "zip", win: w})
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	ev := &mygo.CloseEvent{Window: w}
	it.onClose(ev)
	if !ev.DefaultPrevented() || it.win != w || len(a.pins) != 1 {
		t.Fatal("a failed put-back allowed the native close or lost its window")
	}
}
