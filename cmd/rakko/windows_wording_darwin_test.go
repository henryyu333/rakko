//go:build darwin

package main

// The Windows wording as the Settings window actually draws it: with the
// platform switched, both languages show Windows names and no macOS glyphs
// or Mac-only labels survive on any page that Windows opens.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/locales"
)

func TestWindowsSettingsPagesSayWindowsWords(t *testing.T) {
	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	macOnly := []string{
		"⌘", "⌃", "⌥", "⇧", "↩",
		"访达", "Finder", "隔空投送", "AirDrop",
		"菜单栏", "快速查看", "Quick Look",
	}
	for _, lang := range []string{"zh-Hans", "en"} {
		a, view := newSettingsShelf(t)
		a.locale = lang
		view.SetLocale(lang)
		l := locales.In(lang)
		pages := []string{
			l.General(), l.SettingsNotchNav(),
			l.SettingsShortcutsSection(), l.SettingsAISection(),
		}
		for _, nav := range pages {
			openSettingsPage(t, view, nav)
			view.Frame()
			joined := strings.Join(view.Texts(), "\n")
			for _, bad := range macOnly {
				if strings.Contains(joined, bad) {
					t.Errorf("%s/%s still says %q: %q", lang, nav, bad, joined)
				}
			}
		}

		openSettingsPage(t, view, l.General())
		view.Frame()
		want := osTextFor(true, l.SettingsLoginRow(), l.SettingsLoginRowWin())
		if !strings.Contains(strings.Join(view.Texts(), "\n"), want) {
			t.Errorf("%s general page missing %q", lang, want)
		}

		openSettingsPage(t, view, l.SettingsShortcutsSection())
		view.Frame()
		for _, cap := range []string{"Ctrl", "Alt", "Space"} {
			if !view.HasText(cap) {
				t.Errorf("%s shortcuts page does not show the %q keycap", lang, cap)
			}
		}

		openSettingsPage(t, view, l.SettingsNotchNav())
		view.Frame()
		joined := strings.Join(view.Texts(), "\n")
		want = osTextFor(true, l.SettingsGroupNotch(), l.SettingsGroupNotchWin())
		if !strings.Contains(joined, want) {
			t.Errorf("%s appearance page missing %q", lang, want)
		}
	}
}

// The screenshot toolbar's tooltips teach the keys Windows users press.
func TestScreenshotTipsSpellWindowsKeys(t *testing.T) {
	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	got := map[shotBarKind]string{}
	view := ui.NewTester(func(c *ui.Context) {
		for _, it := range []shotBarItem{
			{kind: shotBarUndo}, {kind: shotBarRedo},
			{kind: shotBarSave}, {kind: shotBarCopy},
		} {
			_, keys := shotBarTip(c, it)
			got[it.kind] = keys
		}
	}, 10, 10)
	view.SetLocale("zh-Hans")
	view.Frame()

	for kind, want := range map[shotBarKind]string{
		shotBarUndo: "Ctrl+Z", shotBarRedo: "Ctrl+Shift+Z",
		shotBarSave: "Ctrl+S", shotBarCopy: "Ctrl+C",
	} {
		if got[kind] != want {
			t.Errorf("shot tooltip %d = %q, want %q", kind, got[kind], want)
		}
	}
}
