package main

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/locales"
)

func newDropPillTestShelf(island bool) *shelf {
	a := &shelf{
		settings:   settings.Default(),
		island:     island,
		notchW:     180,
		notchH:     32,
		dispBounds: mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
		dispWork:   mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 920},
		dispID:     1,
	}
	if island {
		a.islandTop = islandTopGap
		a.settings.ShellStyle = settings.ShellStyleIsland
	}
	return a
}

// Reproduces the stuck 「松开放入」 pill when ui.DataDragOver remains latched
// (over == true) after a drag already ended (dragging == false, LeftMouseDown == false),
// as well as the half-clipped text inside a 36pt sysmon/hover shell.
func TestDropPillDoesNotStickOnStaleDataDragOver(t *testing.T) {
	a := newDropPillTestShelf(false)
	a.peeking = true
	a.peekProgress, a.peekFrom = 1, 1

	view := ui.NewTester(func(c *ui.Context) {
		shellW, shellH := a.peekSize()
		a.peekView(c, shellW, shellH, 0, 1, 0, true /* stale over */, false)
	}, sheetW, 120)
	view.Frame()
	if view.HasText("松开放入") {
		t.Fatal("peekView stayed on 松开放入 with stale DataDragOver after drag ended")
	}

	// Same stale over with settled sysmon wings (36pt tall): must show sysmon,
	// never a half-clipped drop pill.
	a.peeking = false
	a.sysmon = &sysmonModule{}
	a.settings.SysMon.WingsEnabled = true
	view = ui.NewTester(func(c *ui.Context) {
		shellW, shellH := a.peekSize()
		a.peekView(c, shellW, shellH, 0, 1, 0, true /* stale over */, false)
	}, sheetW, 120)
	view.Frame()
	if view.HasText("松开放入") {
		t.Fatal("sysmon wings hijacked by stale DataDragOver into 松开放入")
	}
}

// Reproduces stuck-drop-pill.png verbatim: an incoming drag outside the hot
// band (or suppressed) has peekSize() height == baseH (32pt, 0pt body below
// the hardware notch), yet over == true made peekView render 松开放入 whose
// bottom half is sliced off by Clip().
func TestDropPillNeverRendersInsideBandHeightHintShell(t *testing.T) {
	a := newDropPillTestShelf(false)
	a.dragging = true
	a.dropHot = false // outside hot band or suppressed -> dropHint height == notchH
	a.peeking = true
	a.peekProgress, a.peekFrom = 1, 1

	shellW, shellH := a.peekSize()
	view := ui.NewTester(func(c *ui.Context) {
		a.peekView(c, shellW, shellH, 0, 1, 0, true /* over */, false)
	}, sheetW, 120)
	view.Frame()

	if tr, ok := view.Find("松开放入"); ok {
		if tr.Y+tr.H > shellH+0.5 {
			t.Fatalf("松开放入 clipped by shell: text rect=%+v exceeds shellH=%.1f", tr, shellH)
		}
		t.Fatalf("off-band hint shell (%.0fx%.0f) should not display 松开放入", shellW, shellH)
	}
}

func TestDropPillRetractsAcrossAllDragEndings(t *testing.T) {
	origCursor := cursorPoint
	origDragState := dragState
	origDragLeft := dragLeftDown
	origDragTypes := dragTypes
	origDispNearest := displayNearest
	origScreens := screenDisplays
	origNotch := notchRect
	t.Cleanup(func() {
		cursorPoint = origCursor
		dragState = origDragState
		dragLeftDown = origDragLeft
		dragTypes = origDragTypes
		displayNearest = origDispNearest
		screenDisplays = origScreens
		notchRect = origNotch
	})

	d1 := mygo.Display{ID: 1, Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956}, WorkArea: mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 920}}
	d2 := mygo.Display{ID: 2, Bounds: mygo.Rectangle{X: 1695, Y: 0, Width: 1280, Height: 800}, WorkArea: mygo.Rectangle{X: 1695, Y: 0, Width: 1280, Height: 770}}
	displays := []mygo.Display{d1, d2}
	screenDisplays = func() []mygo.Display { return displays }
	displayNearest = func(p mygo.Point) mygo.Display {
		if p.X >= d2.Bounds.X {
			return d2
		}
		return d1
	}
	notchRect = func(id int64) (int, int, int, int, bool) {
		if id == 2 {
			return d2.Bounds.X + (d2.Bounds.Width-180)/2, 0, 180, 32, false
		}
		return (d1.Bounds.Width - 180) / 2, 0, 180, 32, true
	}

	var cur mygo.Point
	var active bool
	var serial int64 = 1
	var leftDown bool
	cursorPoint = func() mygo.Point { return cur }
	dragState = func() (bool, int64) { return active, serial }
	dragLeftDown = func() bool { return leftDown }
	dragTypes = func() []string { return []string{"public.file-url"} }

	a := newDropPillTestShelf(false)
	a.placeAt(mygo.Point{X: 735, Y: 400})

	// 1. Mid-drag display switch while in-band must clear old display's inBand/dropHot.
	cur = mygo.Point{X: 735, Y: 10}
	active, leftDown = true, true
	a.monitorDrag(a.lastAct)
	a.visible = false // keep closed peek state for testing pill retract directly
	a.suppressed = false
	a.inBand, a.dropHot = true, true

	a.placeAt(mygo.Point{X: 2335, Y: 400})
	if a.dispID != 2 {
		t.Fatalf("dispID = %d, want 2", a.dispID)
	}
	if a.inBand || a.dropHot {
		t.Fatalf("switching display mid-drag kept stale inBand=%v dropHot=%v", a.inBand, a.dropHot)
	}

	// 2. Drag moves off-screen / out of band then ends (mouse-up): must retract.
	cur = mygo.Point{X: 5000, Y: 5000}
	active, leftDown = false, false
	a.monitorDrag(a.lastAct)
	if a.dragging || a.dropHot || a.inBand || a.peeking {
		t.Fatalf("after drag end off-screen: dragging=%v dropHot=%v inBand=%v peeking=%v",
			a.dragging, a.dropHot, a.inBand, a.peeking)
	}
}

func TestDropPillTextAndIconUnclippedNotchAndIsland(t *testing.T) {
	for _, island := range []bool{false, true} {
		for _, lang := range []string{"zh-Hans", "en"} {
			a := newDropPillTestShelf(island)
			a.dragging, a.dropHot = true, true
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1

			w, h := a.peekSize()
			_, notchH := a.anchorSize()
			top := float32(a.sheetTop())
			view := ui.NewTester(a.view, sheetW, 120)
			view.SetLocale(lang)
			view.Frame()

			label := locales.In(lang).DropRelease()
			tr, ok := view.Find(label)
			if !ok {
				t.Fatalf("island=%v lang=%s: %q not rendered", island, lang, label)
			}
			sx0 := (float32(sheetW) - w) / 2
			if tr.X < sx0 || tr.X+tr.W > sx0+w {
				t.Fatalf("island=%v lang=%s: %q horiz clipped rect=%+v shell=[%.1f..%.1f]",
					island, lang, label, tr, sx0, sx0+w)
			}
			if tr.Y < top+notchH || tr.Y+tr.H > top+h {
				t.Fatalf("island=%v lang=%s: %q vert clipped rect=%+v band=[%.1f..%.1f]",
					island, lang, label, tr, top+notchH, top+h)
			}
		}
	}
}
