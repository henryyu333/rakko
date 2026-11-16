package main

import (
	"math"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/locales"
)

// TestUnifiedSettingsIslandAndLayout verifies that macOS and Windows share
// the exact 640pt v6 "小岛" layout: #0B0B0D window background, centered top
// navigation island clearing Windows' 138 DIP caption buttons on every page,
// and single-column scrollable content below the 64pt top bar.
func TestUnifiedSettingsIslandAndLayout(t *testing.T) {
	origWin := forWindows
	t.Cleanup(func() {
		forWindows = origWin
	})

	for _, tc := range []struct {
		name string
		win  bool
	}{
		{"macOS", false},
		{"Windows", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forWindows = tc.win
			for _, lang := range []string{"zh-Hans", "en"} {
				t.Run(lang, func(t *testing.T) {
					l := locales.In(lang)
					tabs := []struct {
						id    settingsPageID
						label string
					}{
						{settingsPageGeneral, l.General()},
						{settingsPageNotch, l.SettingsNotchNav()},
						{settingsPageTray, l.SettingsTraySection()},
						{settingsPageClipboard, l.SettingsClipboardSection()},
						{settingsPageAIUsage, l.SettingsAISection()},
						{settingsPageShortcuts, l.SettingsShortcutsSection()},
					}
					if permissionsSupported {
						tabs = append(tabs, struct {
							id    settingsPageID
							label string
						}{settingsPagePermissions, l.PermTitle()})
					}
					tabs = append(tabs, struct {
						id    settingsPageID
						label string
					}{settingsPageAbout, l.SettingsAboutPage()})

					a := &shelf{
						dataDir: t.TempDir(), settings: settings.Default(), locale: lang,
						thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
						appNames: map[string]string{},
					}
					v := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
					if tc.win {
						v.SetTitleBar(ui.TitleBar{Height: 32, Right: 138})
					}
					v.SetLocale(lang)
					v.Frame()

					img := v.Image()
					if c := img.RGBAAt(2, 2); c.R != 11 || c.G != 11 || c.B != 13 {
						t.Fatalf("background at (2,2) = %v, want #0B0B0D", c)
					}

					for _, tab := range tabs {
						if err := v.Click(tab.label); err != nil {
							t.Fatalf("click tab %q: %v", tab.label, err)
						}
						v.Frame()
						v.Frame()
						if a.settingsPage != tab.id {
							t.Fatalf("clicking %q left page=%v, want %v", tab.label, a.settingsPage, tab.id)
						}

						first, ok := v.Find(tabs[0].label)
						if !ok {
							t.Fatalf("first tab %q missing", tabs[0].label)
						}
						last, ok := v.Find(tabs[len(tabs)-1].label)
						if !ok {
							t.Fatalf("last tab %q missing", tabs[len(tabs)-1].label)
						}
						islandLeft := first.X - settingsIslandPad
						islandRight := last.X + last.W + settingsIslandPad
						center := (islandLeft + islandRight) / 2
						if diff := math.Abs(float64(center - float32(settingsWidth)/2)); diff > 1.0 {
							t.Errorf("page %v: island center=%.1f, want %.1f (diff=%.1f)",
								tab.id, center, float32(settingsWidth)/2, diff)
						}
						if tc.win && islandRight > float32(settingsWidth-138) {
							t.Errorf("page %v (%s): island right %.1f overlaps 138pt caption buttons (max %.1f)",
								tab.id, lang, islandRight, float32(settingsWidth-138))
						}
					}
				})
			}
		})
	}
}

func TestSettingsThemeAndSegmentedParity(t *testing.T) {
	if got := settingsThemeFor(false).Font; got != "" {
		t.Fatalf("macOS settingsTheme.Font = %q, want empty", got)
	}
	if got := settingsThemeFor(true).Font; got != "Segoe UI, Microsoft YaHei UI" {
		t.Fatalf("Windows settingsTheme.Font = %q", got)
	}

	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	selected := 0
	var segChanged bool
	v := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(settingsThemeFor(true))
		sg := settingsSegmented(c, "lang", "Language", &selected, "跟随系统", "简体中文", "English")
		if sg.Changed() {
			segChanged = true
		}
	}, 400, 80)
	v.Frame()
	if _, ok := v.Find("English"); !ok {
		t.Fatal("English segment missing")
	}
	v.Click("English")
	if !segChanged || selected != 2 {
		t.Fatalf("clicking English segment gave changed=%v selected=%d, want true / 2", segChanged, selected)
	}
}

func TestWindowsSettingsScrolledShortcutsTopRowStartsRecording(t *testing.T) {
	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	const caption = 32
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(), locale: "zh-Hans",
		settingsPage: settingsPageShortcuts,
		thumbnails:   map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{},
	}
	v := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	v.SetTitleBar(ui.TitleBar{Height: caption, Right: 138})
	v.SetLocale("zh-Hans")
	v.Frame()

	for range 30 {
		v.Scroll(settingsWidth/2, float32(settingsHeight)/2, 0, 400)
		v.Frame()
	}

	l := a.appLoc()
	editable := []struct {
		kind  shortcutKind
		label string
	}{
		{shortcutToggle, l.SettingsShortcutTray()},
		{shortcutClipboard, l.SettingsShortcutClipboard()},
		{shortcutOpenTray, l.SettingsShortcutOpenTray()},
		{shortcutPlayPause, l.SettingsShortcutPlayPause()},
		{shortcutPrevious, l.SettingsShortcutPrevious()},
		{shortcutNext, l.SettingsShortcutNext()},
		{shortcutCapture, l.SettingsShortcutOCR()},
		{shortcutScreenshot, l.SettingsShortcutScreenshot()},
		{shortcutTools, l.SettingsShortcutTools()},
		{shortcutUsage, l.SettingsShortcutUsage()},
		{shortcutAwake, l.SettingsShortcutAwake()},
		{shortcutNote, l.SettingsShortcutNewNote()},
		{shortcutPet, l.SettingsShortcutPet()},
		{shortcutAppSwitch, l.SettingsShortcutAppSwitch()},
	}

	var topKind shortcutKind
	var topLabel string
	var topRect ui.Rect
	found := false
	for _, row := range editable {
		r, ok := v.Find(row.label)
		if !ok {
			continue
		}
		if r.Y+r.H <= settingsTopH {
			continue
		}
		if !found || r.Y < topRect.Y {
			found = true
			topKind = row.kind
			topLabel = row.label
			topRect = r
		}
	}
	if !found {
		t.Fatal("no editable shortcut row found after scrolling to bottom")
	}
	if topRect.Y < settingsTopH {
		t.Fatalf("top visible shortcut row %q starts at y=%.1f, inside top DragWindow [0,%d)", topLabel, topRect.Y, settingsTopH)
	}

	v.ClickAt(topRect.X+topRect.W/2, topRect.Y+topRect.H/2)
	v.Frame()
	if a.settingsRecording != topKind {
		t.Fatalf("clicking top visible scrolled row %q (rect=%+v) recorded %v, want %v",
			topLabel, topRect, a.settingsRecording, topKind)
	}
}

func TestSettingsResizeLayoutsAndBounds(t *testing.T) {
	origWin := forWindows
	origFit := settingsFit
	t.Cleanup(func() {
		forWindows = origWin
		settingsFit = origFit
	})

	sizes := []struct {
		name string
		w, h int
	}{
		{"min", settingsMinWidth, settingsMinHeight},
		{"default", settingsWidth, settingsHeight},
		{"wide", 1000, 800},
	}

	for _, tc := range []struct {
		name string
		win  bool
	}{
		{"macOS", false},
		{"Windows", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forWindows = tc.win
			for _, lang := range []string{"zh-Hans", "en"} {
				t.Run(lang, func(t *testing.T) {
					l := locales.In(lang)
					tabs := []struct {
						id    settingsPageID
						label string
					}{
						{settingsPageGeneral, l.General()},
						{settingsPageNotch, l.SettingsNotchNav()},
						{settingsPageTray, l.SettingsTraySection()},
						{settingsPageClipboard, l.SettingsClipboardSection()},
						{settingsPageAIUsage, l.SettingsAISection()},
						{settingsPageShortcuts, l.SettingsShortcutsSection()},
					}
					if permissionsSupported {
						tabs = append(tabs, struct {
							id    settingsPageID
							label string
						}{settingsPagePermissions, l.PermTitle()})
					}
					tabs = append(tabs, struct {
						id    settingsPageID
						label string
					}{settingsPageAbout, l.SettingsAboutPage()})

					for _, sz := range sizes {
						t.Run(sz.name, func(t *testing.T) {
							var fitIssues []string
							settingsFit = func(msg string) {
								fitIssues = append(fitIssues, msg)
							}
							a := &shelf{
								dataDir: t.TempDir(), settings: settings.Default(), locale: lang,
								thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
								appNames: map[string]string{},
							}
							v := ui.NewTester(a.settingsView, sz.w, sz.h)
							if tc.win {
								v.SetTitleBar(ui.TitleBar{Height: 32, Right: 138})
							}
							v.SetLocale(lang)
							v.Frame()

							for _, tab := range tabs {
								fitIssues = nil
								a.setSettingsPage(tab.id)
								v.Frame()
								v.Frame()
								if len(fitIssues) > 0 {
									t.Errorf("page %v at %dx%d clipped labels: %v", tab.id, sz.w, sz.h, fitIssues)
								}

								first, ok1 := v.Find(tabs[0].label)
								last, ok2 := v.Find(tabs[len(tabs)-1].label)
								if !ok1 || !ok2 {
									t.Fatalf("island tabs missing on page %v", tab.id)
								}
								islandLeft := first.X - settingsIslandPad
								islandRight := last.X + last.W + settingsIslandPad
								center := (islandLeft + islandRight) / 2
								if diff := math.Abs(float64(center - float32(sz.w)/2)); diff > 1.5 {
									t.Errorf("page %v at %dx%d: island center=%.1f, want %.1f",
										tab.id, sz.w, sz.h, center, float32(sz.w)/2)
								}
								if !tc.win && islandLeft < 78 {
									t.Errorf("page %v at %dx%d: island left=%.1f overlaps macOS traffic lights (<78)",
										tab.id, sz.w, sz.h, islandLeft)
								}
								if tc.win && !permissionsSupported && islandRight > float32(sz.w-138) {
									t.Errorf("page %v at %dx%d: island right=%.1f overlaps Windows caption buttons (>%.1f)",
										tab.id, sz.w, sz.h, islandRight, float32(sz.w-138))
								}
							}

							// At 1000px wide, content column must cap at 720pt centered (left edge at 140pt).
							if sz.w == 1000 {
								a.setSettingsPage(settingsPageGeneral)
								v.Frame()
								v.Frame()
								row, ok := v.Find(l.LanguageRow())
								if !ok {
									t.Fatal("LanguageRow missing")
								}
								// row is inside card with 14pt horizontal padding -> card left = row.X - 14.
								cardLeft := row.X - settingsRowPadX
								wantLeft := float32(sz.w-settingsMaxContentW) / 2
								if math.Abs(float64(cardLeft-wantLeft)) > 2.0 {
									t.Errorf("at width %d, card left=%.1f, want %.1f (content capped at %d)",
										sz.w, cardLeft, wantLeft, settingsMaxContentW)
								}
							}
						})
					}
				})
			}
		})
	}
}

func TestSettingsWindowMinSizeAddsBorderOnWindows(t *testing.T) {
	outer := mygo.Rectangle{X: 100, Y: 100, Width: 656, Height: 608}
	inner := mygo.Rectangle{X: 108, Y: 100, Width: 640, Height: 600}

	macW, macH := settingsWindowMinSize(false, outer, inner)
	if macW != settingsMinWidth || macH != settingsMinHeight {
		t.Fatalf("macOS min size = %dx%d, want %dx%d", macW, macH, settingsMinWidth, settingsMinHeight)
	}

	winW, winH := settingsWindowMinSize(true, outer, inner)
	if winW != settingsMinWidth+16 || winH != settingsMinHeight+8 {
		t.Fatalf("Windows min size = %dx%d, want %dx%d", winW, winH, settingsMinWidth+16, settingsMinHeight+8)
	}

	// Subtracting the window's non-client border leaves exact 600x420 content.
	if gotW, gotH := winW-(outer.Width-inner.Width), winH-(outer.Height-inner.Height); gotW != settingsMinWidth || gotH != settingsMinHeight {
		t.Fatalf("Windows min content = %dx%d, want %dx%d", gotW, gotH, settingsMinWidth, settingsMinHeight)
	}

	// Zero or negative frame delta never shrinks below settingsMinWidth x settingsMinHeight.
	clampedW, clampedH := settingsWindowMinSize(true, inner, outer)
	if clampedW != settingsMinWidth || clampedH != settingsMinHeight {
		t.Fatalf("clamped min size = %dx%d, want %dx%d", clampedW, clampedH, settingsMinWidth, settingsMinHeight)
	}
}
