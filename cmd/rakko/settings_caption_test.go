package main

import (
	"math"
	"testing"

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
					h := a.measureSettingsHeight(settingsPageGeneral)
					v := ui.NewTester(a.settingsView, settingsWidth, h)
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
						v.SetSize(settingsWidth, a.measureSettingsHeight(tab.id))
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
	h := a.measureSettingsHeight(settingsPageShortcuts)
	v := ui.NewTester(a.settingsView, settingsWidth, h)
	v.SetTitleBar(ui.TitleBar{Height: caption, Right: 138})
	v.SetLocale("zh-Hans")
	v.Frame()

	for range 30 {
		v.Scroll(settingsWidth/2, float32(h)/2, 0, 400)
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
