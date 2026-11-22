package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/locales"
)

func stubFriendlyNames(t *testing.T, names map[int64]string) {
	t.Helper()
	orig := displayFriendlyNames
	displayFriendlyNames = func() map[int64]string { return names }
	t.Cleanup(func() { displayFriendlyNames = orig })
}

func TestDisplayModesFollowMainAndPinnedOnStackedLayout(t *testing.T) {
	rig := newStackedDualDisplayRig(t, true)
	stubFriendlyNames(t, nil)

	t.Run("follow-mouse", func(t *testing.T) {
		a := &shelf{dataDir: t.TempDir(), settings: settings.Default()}
		rig.setCursor(mygo.Point{X: 735, Y: 400})
		a.placeAt(cursorPoint())
		if a.dispID != 1 {
			t.Fatalf("start dispID = %d, want 1", a.dispID)
		}
		rig.setCursor(mygo.Point{X: 735, Y: -20})
		a.pointerMoved()
		if a.dispID != 2 || a.dispBounds.Y != -1080 {
			t.Fatalf("follow mode did not move to upper display: dispID=%d bounds=%+v", a.dispID, a.dispBounds)
		}
		// Idle capsules still appear on the other display in follow-mouse mode.
		ds := []islandDisplay{
			{ID: 1, Bounds: rig.displays[0].Bounds, HasNotch: true, NotchH: 32},
			{ID: 2, Bounds: rig.displays[1].Bounds},
		}
		pls := idleIslandPlacementsForMode(ds, a.dispID, settings.ShellStyleIsland, nil, a.settings.Display)
		if len(pls) != 1 || pls[0].DisplayID != 1 {
			t.Fatalf("follow mode idle islands = %+v, want [ID=1]", pls)
		}
	})

	t.Run("primary-display", func(t *testing.T) {
		s := settings.Default()
		s.Display = settings.DisplayMain
		s.ShellStyle = settings.ShellStyleIsland
		a := &shelf{dataDir: t.TempDir(), settings: s}

		// Even with pointer on upper display, startup/placeAt/show land on primary (1).
		rig.setCursor(mygo.Point{X: 735, Y: -300})
		a.placeAt(cursorPoint())
		if a.dispID != 1 || a.dispBounds.Y != 0 {
			t.Fatalf("main mode initial dispID=%d bounds=%+v, want ID=1", a.dispID, a.dispBounds)
		}
		// Moving pointer/dragging on upper screen does not move Rakko there.
		a.pointerMoved()
		if a.dispID != 1 {
			t.Fatalf("main mode moved on pointerMoved: dispID=%d", a.dispID)
		}
		a.monitorDrag(time.Now())
		if a.dispID != 1 {
			t.Fatalf("main mode moved on monitorDrag: dispID=%d", a.dispID)
		}
		// No extra idle capsules on other screens when pinned to main.
		ds := []islandDisplay{
			{ID: 1, Bounds: rig.displays[0].Bounds, HasNotch: true, NotchH: 32},
			{ID: 2, Bounds: rig.displays[1].Bounds},
		}
		if pls := idleIslandPlacementsForMode(ds, a.dispID, settings.ShellStyleIsland, nil, a.settings.Display); len(pls) != 0 {
			t.Fatalf("main mode drew extra idle islands: %+v", pls)
		}
	})

	t.Run("specific-display-unplug-and-replug", func(t *testing.T) {
		s := settings.Default()
		s.Display = "2"
		s.ShellStyle = settings.ShellStyleIsland
		a := &shelf{dataDir: t.TempDir(), settings: s}

		// Pointer sits on lower primary, yet pinned "2" places Rakko on upper display 2.
		rig.setCursor(mygo.Point{X: 735, Y: 500})
		a.placeAt(cursorPoint())
		if a.dispID != 2 || a.dispBounds.Y != -1080 {
			t.Fatalf("pinned mode initial dispID=%d bounds=%+v, want ID=2", a.dispID, a.dispBounds)
		}
		// Moving cursor across lower primary does not pull Rakko off display 2.
		rig.setCursor(mygo.Point{X: 300, Y: 200})
		a.pointerMoved()
		if a.dispID != 2 {
			t.Fatalf("pinned mode moved on pointerMoved: dispID=%d", a.dispID)
		}
		// No extra idle capsules on display 1 when pinned to display 2.
		ds := []islandDisplay{
			{ID: 1, Bounds: rig.displays[0].Bounds, HasNotch: true, NotchH: 32},
			{ID: 2, Bounds: rig.displays[1].Bounds},
		}
		if pls := idleIslandPlacementsForMode(ds, a.dispID, settings.ShellStyleIsland, nil, a.settings.Display); len(pls) != 0 {
			t.Fatalf("pinned mode drew extra idle islands: %+v", pls)
		}

		// Unplug display 2 -> falls back to primary (1), while keeping settings.Display == "2".
		upper := rig.displays[1]
		rig.unplug(2)
		a.displaysChanged()
		if a.dispID != 1 || a.dispBounds.Y != 0 {
			t.Fatalf("after unplug: dispID=%d bounds=%+v, want primary ID=1", a.dispID, a.dispBounds)
		}
		if a.settings.Display != "2" {
			t.Fatalf("after unplug: settings.Display changed to %q, want preserved %q", a.settings.Display, "2")
		}

		// Replug display 2 -> displaysChanged automatically returns Rakko to display 2.
		rig.plug(upper)
		a.displaysChanged()
		if a.dispID != 2 || a.dispBounds.Y != -1080 {
			t.Fatalf("after replug: dispID=%d bounds=%+v, want restored ID=2", a.dispID, a.dispBounds)
		}
	})
}

func TestDisplayOptionsAndRelativeSide(t *testing.T) {
	lzh := locales.In("zh-Hans")
	locEN := locales.In("en")

	single := []mygo.Display{{
		ID: 1, Primary: true, Label: "Built-in Retina Display",
		Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
	}}
	if opts := displayOptions(single, nil, "", lzh); len(opts) != 0 {
		t.Fatalf("single display returned %d options, want 0", len(opts))
	}

	// Ultrawide display stacked directly above a narrower primary must still be "上方".
	primary := mygo.Display{
		ID: 1, Primary: true, Label: "内建视网膜显示器",
		Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
	}
	ultrawideAbove := mygo.Display{
		ID: 2, Label: "DELL U2723QE",
		Bounds: mygo.Rectangle{X: 0, Y: -1080, Width: 3840, Height: 1080},
	}
	if got := displayPosRelative(primary, ultrawideAbove); got != displayPosAbove {
		t.Fatalf("ultrawide above classified as %v, want displayPosAbove", got)
	}

	// Identical names on different sides remain distinguishable via suffixes,
	// and Windows friendlyNames override Display.Label.
	left := mygo.Display{
		ID: 10, Label: `\\.\DISPLAY2`,
		Bounds: mygo.Rectangle{X: -1920, Y: 0, Width: 1920, Height: 1080},
	}
	right := mygo.Display{
		ID: 20, Label: `\\.\DISPLAY3`,
		Bounds: mygo.Rectangle{X: 1470, Y: 0, Width: 1920, Height: 1080},
	}
	friendly := map[int64]string{
		1:  "MacBook Pro",
		10: "DELL U2723QE",
		20: "DELL U2723QE",
	}
	optsZH := displayOptions([]mygo.Display{primary, left, right}, friendly, "20", lzh)
	if len(optsZH) != 5 {
		t.Fatalf("3 displays yielded %d rows, want 5", len(optsZH))
	}
	if optsZH[2].name != "MacBook Pro" || optsZH[2].suffix != lzh.SettingsDisplayPosPrimary() || optsZH[2].selected {
		t.Fatalf("primary row = %+v", optsZH[2])
	}
	if optsZH[3].name != "DELL U2723QE" || optsZH[3].suffix != lzh.SettingsDisplayPosLeft() || optsZH[3].selected {
		t.Fatalf("left dup row = %+v", optsZH[3])
	}
	if optsZH[4].name != "DELL U2723QE" || optsZH[4].suffix != lzh.SettingsDisplayPosRight() || !optsZH[4].selected {
		t.Fatalf("right dup row = %+v", optsZH[4])
	}

	optsEN := displayOptions([]mygo.Display{primary, left, right}, friendly, settings.DisplayMain, locEN)
	if !optsEN[1].selected || optsEN[3].suffix != locEN.SettingsDisplayPosLeft() || optsEN[4].suffix != locEN.SettingsDisplayPosRight() {
		t.Fatalf("en options = %+v", optsEN)
	}
}

func TestDisplaySettingsPageInteraction(t *testing.T) {
	rig := newStackedDualDisplayRig(t, true)
	rig.displays[0].Label = "内建视网膜显示器"
	rig.displays[1].Label = "DELL U2723QE"
	stubFriendlyNames(t, nil)

	l := locales.In("zh-Hans")
	a := &shelf{
		dataDir:      t.TempDir(),
		settings:     settings.Default(),
		locale:       "zh-Hans",
		settingsPage: settingsPageNotch,
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
	rig.setCursor(mygo.Point{X: 735, Y: 400})
	a.placeAt(cursorPoint())

	var overflows []string
	origFit := settingsFit
	settingsFit = func(msg string) { overflows = append(overflows, msg) }
	t.Cleanup(func() { settingsFit = origFit })

	v := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	v.SetLocale("zh-Hans")
	v.Frame()
	v.Frame()

	if _, ok := v.Find(l.SettingsDisplayGroup()); !ok {
		t.Fatalf("display group %q missing on dual-display setup; texts=%v", l.SettingsDisplayGroup(), v.Texts())
	}
	if err := v.Click("DELL U2723QE · 上方"); err != nil {
		t.Fatalf("click DELL row: %v", err)
	}
	v.Frame()
	if a.settings.Display != "2" {
		t.Fatalf("after clicking DELL row: settings.Display = %q, want %q", a.settings.Display, "2")
	}
	if a.dispID != 2 {
		t.Fatalf("after clicking DELL row: dispID = %d, want 2", a.dispID)
	}
	loaded, err := settings.Load(a.dataDir)
	if err != nil || loaded.Display != "2" {
		t.Fatalf("persisted Display = %q, err=%v; want %q", loaded.Display, err, "2")
	}

	if err := v.Click(l.SettingsDisplayMain()); err != nil {
		t.Fatalf("click primary option: %v", err)
	}
	v.Frame()
	if a.settings.Display != settings.DisplayMain || a.dispID != 1 {
		t.Fatalf("after clicking main: Display=%q dispID=%d", a.settings.Display, a.dispID)
	}

	// Unplugging down to 1 display hides the entire group.
	rig.unplug(2)
	a.displaysChanged()
	v.Frame()
	if _, ok := v.Find(l.SettingsDisplayGroup()); ok {
		t.Fatal("display group still visible with only 1 display connected")
	}
	if len(overflows) > 0 {
		t.Fatalf("label overflows: %v", overflows)
	}
}

func TestDisplaySettingsCachesEnumeration(t *testing.T) {
	rig := newStackedDualDisplayRig(t, true)
	rig.displays[0].Label = "Built-in"
	rig.displays[1].Label = "External"

	var dispCalls, friendlyCalls int
	origDisp := screenDisplays
	origFriendly := displayFriendlyNames
	screenDisplays = func() []mygo.Display {
		dispCalls++
		return origDisp()
	}
	displayFriendlyNames = func() map[int64]string {
		friendlyCalls++
		return nil
	}
	t.Cleanup(func() {
		screenDisplays = origDisp
		displayFriendlyNames = origFriendly
	})

	a := &shelf{
		dataDir:      t.TempDir(),
		settings:     settings.Default(),
		locale:       "zh-Hans",
		settingsPage: settingsPageGeneral,
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
	v := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	v.SetLocale("zh-Hans")
	v.Frame()
	if dispCalls != 0 || friendlyCalls != 0 {
		t.Fatalf("General page enumerated displays: disp=%d friendly=%d", dispCalls, friendlyCalls)
	}

	// Entering settingsPageNotch populates the cache once; subsequent frames read cache only.
	a.setSettingsPage(settingsPageNotch)
	if dispCalls != 1 || friendlyCalls != 1 {
		t.Fatalf("entering page enumerated disp=%d friendly=%d, want 1, 1", dispCalls, friendlyCalls)
	}
	for range 10 {
		v.Frame()
	}
	if dispCalls != 1 || friendlyCalls != 1 {
		t.Fatalf("frames re-enumerated displays: disp=%d friendly=%d, want 1, 1", dispCalls, friendlyCalls)
	}

	// Clicking an option refreshes once (plus placeAt); further frames do not re-enumerate.
	if err := v.Click("External · 上方"); err != nil {
		t.Fatalf("click External: %v", err)
	}
	afterClickDisp, afterClickFriendly := dispCalls, friendlyCalls
	if afterClickFriendly != 2 {
		t.Fatalf("click friendlyCalls = %d, want 2", afterClickFriendly)
	}
	for range 5 {
		v.Frame()
	}
	if dispCalls != afterClickDisp || friendlyCalls != afterClickFriendly {
		t.Fatalf("frames after click re-enumerated displays: disp %d->%d friendly %d->%d",
			afterClickDisp, dispCalls, afterClickFriendly, friendlyCalls)
	}

	// Display unplug/change refreshes cache once (place + refreshDisplayOptions);
	// subsequent frames read cache only.
	rig.unplug(2)
	beforeUnplug := dispCalls
	a.displaysChanged()
	afterUnplug := dispCalls
	for range 5 {
		v.Frame()
	}
	if dispCalls != afterUnplug || friendlyCalls != 2 {
		t.Fatalf("after displaysChanged frames re-enumerated: disp %d->%d friendly=%d", beforeUnplug, dispCalls, friendlyCalls)
	}
	if _, ok := v.Find(locales.In("zh-Hans").SettingsDisplayGroup()); ok {
		t.Fatal("display group still shown after unplug + displaysChanged")
	}
}

func TestDisplaySettingsRenderScreenshots(t *testing.T) {
	outDir := os.Getenv("RAKKO_DISPLAY_GALLERY")
	if outDir == "" {
		t.Skip("set RAKKO_DISPLAY_GALLERY=<dir> to write headless display settings screenshots")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stubFriendlyNames(t, nil)

	origDisplays := screenDisplays
	origNearest := displayNearest
	origNotch := notchRect
	t.Cleanup(func() {
		screenDisplays = origDisplays
		displayNearest = origNearest
		notchRect = origNotch
	})

	renderCase := func(name, lang, chosen string, ds []mygo.Display) {
		screenDisplays = func() []mygo.Display { return ds }
		displayNearest = func(p mygo.Point) mygo.Display { return nearestDisplayIn(ds, p) }
		notchRect = func(id int64) (int, int, int, int, bool) {
			if id == 1 {
				return 645, 0, 180, 32, true
			}
			return 0, 0, 0, 0, false
		}
		s := settings.Default()
		s.Display = chosen
		a := &shelf{
			dataDir:      t.TempDir(),
			settings:     s,
			locale:       lang,
			settingsPage: settingsPageNotch,
			thumbnails:   map[string]*ui.Bitmap{},
			thumbPending: map[string]bool{},
			appNames:     map[string]string{},
		}
		// Tall enough to capture both 外形 and 显示在 without scrolling clip.
		v := ui.NewTester(a.settingsView, settingsWidth, 760)
		v.SetLocale(lang)
		v.SetScale(2)
		v.Frame()
		v.Frame()

		img := v.Image()
		if img == nil {
			t.Fatalf("%s: nil image", name)
		}
		f, err := os.Create(filepath.Join(outDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	for _, lang := range []string{"zh-Hans", "en"} {
		builtinName := "Built-in Retina Display"
		if lang == "zh-Hans" {
			builtinName = "内建视网膜显示器"
		}
		single := []mygo.Display{{
			ID: 1, Primary: true, Label: builtinName,
			Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
		}}
		dual := []mygo.Display{
			{ID: 2, Label: "DELL U2723QE", Bounds: mygo.Rectangle{X: -225, Y: -1080, Width: 1920, Height: 1080}},
			single[0],
		}
		tripleDup := []mygo.Display{
			{ID: 2, Label: "DELL U2723QE", Bounds: mygo.Rectangle{X: -1920, Y: 0, Width: 1920, Height: 1080}},
			single[0],
			{ID: 3, Label: "DELL U2723QE", Bounds: mygo.Rectangle{X: 1470, Y: 0, Width: 1920, Height: 1080}},
		}
		tag := "en"
		if lang == "zh-Hans" {
			tag = "zh"
		}
		renderCase("display-1screen-"+tag+".png", lang, "", single)
		renderCase("display-2screen-"+tag+".png", lang, "1", dual)
		renderCase("display-3screen-dup-"+tag+".png", lang, "3", tripleDup)
	}
}
