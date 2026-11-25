//go:build darwin

package main

// Headless evidence for the 快捷键 page's clear affordance: the page at
// rest, a set row hovered (its × appears), and the row recording (the × and
// the ⌫ hint). Written under RAKKO_SHOTS when a run asks for shots.

import (
	"os"
	"testing"

	"rakko/locales"
)

func TestPolish1189ShortcutShots(t *testing.T) {
	if os.Getenv("RAKKO_SHOTS") == "" {
		t.Skip("set RAKKO_SHOTS to write the shortcut-page shots")
	}
	_, view, _ := shortcutShelf(t)
	view.SetScale(2)
	view.SetDark(true)
	openSettingsPage(t, view, "快捷键")
	view.Frame()
	view.Frame()
	saveTestShot(t, "polish1189", "settings-shortcuts-normal.png", view.Image())

	row, ok := view.Find("剪贴板搜索")
	if !ok {
		t.Fatalf("the clipboard row did not draw: %v", view.Texts())
	}
	view.Move(row.X+row.W/2, row.Y+row.H/2)
	view.Frame()
	view.Frame()
	if _, ok := view.Find(locales.In("").SettingsShortcutClear()); !ok {
		t.Fatalf("hovering the row did not show ×: %v", view.Texts())
	}
	saveTestShot(t, "polish1189", "settings-shortcuts-hover.png", view.Image())

	if err := view.Click("剪贴板搜索"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Frame()
	if _, ok := view.Find(locales.In("").SettingsShortcutClear()); !ok {
		t.Fatalf("the recording row did not show ×: %v", view.Texts())
	}
	saveTestShot(t, "polish1189", "settings-shortcuts-recording.png", view.Image())
}
