package main

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/layout"
	"rakko/internal/settings"
)

func TestPinRestoreSkipsUnsupportedCatalogCardsAndPreservesRecord(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}
	oldDisplays := pinDisplays
	pinDisplays = func() []mygo.Display {
		return []mygo.Display{
			{ID: 1, Primary: true, Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080}, WorkArea: area},
		}
	}
	t.Cleanup(func() { pinDisplays = oldDisplays })

	var opened []string
	oldOpen := pinOpenWindow
	pinOpenWindow = func(it *pinItem) {
		opened = append(opened, it.cardID)
	}
	t.Cleanup(func() { pinOpenWindow = oldOpen })

	dir := t.TempDir()
	airdropPin := layout.Pin{
		ID: "airdrop", Span: 1, Rows: 1,
		Display: "1", Edge: layout.EdgeRight, X: area.Width - pinStripW, Y: 300, AlwaysOpen: true,
	}
	zipPin := layout.Pin{
		ID: "zip", Span: 1, Rows: 1,
		Display: "999", Edge: layout.EdgeLeft, X: 0, Y: 200,
	}
	seed := layout.Layout{
		Version: layout.Version,
		Cards: []layout.Card{
			{ID: "playing", Span: 2, Rows: 2},
			{ID: "ocr", Span: 1, Rows: 1},
		},
		Pins: []layout.Pin{airdropPin, zipPin},
	}
	if err := layout.Write(dir, seed); err != nil {
		t.Fatal(err)
	}

	a := &shelf{dataDir: dir, settings: settings.Default()}
	a.pinRestore()

	if !skipUnknownPins {
		if !slices.Equal(opened, []string{"airdrop", "zip"}) || len(a.pins) != 2 {
			t.Fatalf("macOS pinRestore changed: opened=%v pins=%d", opened, len(a.pins))
		}
		return
	}

	if !slices.Equal(opened, []string{"zip"}) {
		t.Fatalf("opened = %v, want [zip]", opened)
	}
	if len(a.pins) != 1 || a.pins[0].cardID != "zip" {
		t.Fatalf("restored pins = %+v, want only zip", a.pins)
	}

	// Re-homing zip from display 999 -> 1 called savePins(); airdrop must stay intact.
	disk, err := layout.LoadWith(dir, a.settings.AIUsage.Enabled)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := findPin(disk.Pins, "airdrop"); !ok || got != airdropPin {
		t.Fatalf("after display re-home, airdrop pin = %+v (ok=%v), want %+v", got, ok, airdropPin)
	}
	if got, ok := findPin(disk.Pins, "zip"); !ok || got.Display != "1" {
		t.Fatalf("zip pin not re-homed to display 1: %+v (ok=%v)", got, ok)
	}

	// Exercise strip wake/tick/collapse, moving zip free, pinning ocr, and returning zip.
	zipItem := a.pins[0]
	a.pinWakeDrag(mygo.Point{X: 10, Y: zipItem.frame.Y})
	if !zipItem.expanded {
		t.Fatal("pinWakeDrag did not expand zip strip")
	}
	t0 := time.Now()
	a.pinTick(t0, mygo.Point{X: 500, Y: 500}, false, false)
	a.pinTick(t0.Add(pinHideDelay+time.Millisecond), mygo.Point{X: 500, Y: 500}, false, false)
	if zipItem.expanded {
		t.Fatal("pinTick did not collapse zip strip after delay")
	}

	// Move zip to a free position and verify savePins preserves airdrop.
	a.pinRelease(zipItem, mygo.Point{X: 600, Y: 400})
	// Pin a new card (ocr) and verify both live pins and preserved airdrop are on disk.
	if got := a.addPin("ocr", 1, 1, pinFree, false, mygo.Rectangle{X: 800, Y: 400, Width: pinCardW, Height: pinCardH}); got == nil {
		t.Fatal("addPin(ocr) failed")
	}
	disk, err = layout.LoadWith(dir, a.settings.AIUsage.Enabled)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := findPin(disk.Pins, "airdrop"); !ok || got != airdropPin {
		t.Fatalf("after addPin, airdrop pin = %+v (ok=%v), want %+v", got, ok, airdropPin)
	}
	if _, ok := findPin(disk.Pins, "ocr"); !ok {
		t.Fatalf("ocr pin missing from layout.json: %+v", disk.Pins)
	}

	// Return zip back to the tools grid; ocr and preserved airdrop stay in layout.json.
	if !a.unpin(zipItem, true) {
		t.Fatal("unpin(zip, true) failed")
	}
	disk, err = layout.LoadWith(dir, a.settings.AIUsage.Enabled)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := findPin(disk.Pins, "airdrop"); !ok || got != airdropPin {
		t.Fatalf("after unpin, airdrop pin = %+v (ok=%v), want %+v", got, ok, airdropPin)
	}
	if _, ok := findPin(disk.Pins, "zip"); ok {
		t.Fatalf("unpinned zip still present in Pins: %+v", disk.Pins)
	}
	if !slices.ContainsFunc(disk.Cards, func(c layout.Card) bool { return c.ID == "zip" }) {
		t.Fatalf("returned zip missing from Cards: %+v", disk.Cards)
	}
}

func TestPinRestoreUnknownIDByPlatform(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}
	oldDisplays := pinDisplays
	pinDisplays = func() []mygo.Display {
		return []mygo.Display{
			{ID: 1, Primary: true, Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080}, WorkArea: area},
		}
	}
	t.Cleanup(func() { pinDisplays = oldDisplays })

	var opened []string
	oldOpen := pinOpenWindow
	pinOpenWindow = func(it *pinItem) {
		opened = append(opened, it.cardID)
	}
	t.Cleanup(func() { pinOpenWindow = oldOpen })

	dir := t.TempDir()
	unknown := layout.Pin{ID: "futurecard", Span: 1, Rows: 1, Display: "1", X: 400, Y: 300}
	if err := layout.Write(dir, layout.Layout{
		Version: layout.Version,
		Cards:   []layout.Card{{ID: "zip", Span: 1, Rows: 1}},
		Pins:    []layout.Pin{unknown},
	}); err != nil {
		t.Fatal(err)
	}

	a := &shelf{dataDir: dir, settings: settings.Default()}
	a.pinRestore()

	if skipUnknownPins {
		if len(opened) != 0 || len(a.pins) != 0 {
			t.Fatalf("Windows restored unknown pin: opened=%v pins=%+v", opened, a.pins)
		}
		a.savePins()
		disk, err := layout.LoadWith(dir, a.settings.AIUsage.Enabled)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := findPin(disk.Pins, "futurecard"); !ok || got != unknown {
			t.Fatalf("savePins dropped unknown pin: %+v (ok=%v)", got, ok)
		}
	} else {
		if !slices.Equal(opened, []string{"futurecard"}) || len(a.pins) != 1 {
			t.Fatalf("macOS did not keep unknown pin restore: opened=%v pins=%+v", opened, a.pins)
		}
	}
}

func findPin(pins []layout.Pin, id string) (layout.Pin, bool) {
	for _, p := range pins {
		if p.ID == id {
			return p, true
		}
	}
	return layout.Pin{}, false
}
