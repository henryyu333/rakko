package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/internal/store"
)

// TestPanelSettingsGearHidesThePanel: the page capsule's gear closes the
// panel before opening Settings on every platform, matching search's own
// Settings row and avoiding the panel lingering behind or over Settings.
func TestPanelSettingsGearHidesThePanel(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	oldOpen := openSettingsWindow
	opened := 0
	openSettingsWindow = func(*shelf) { opened++ }
	t.Cleanup(func() { openSettingsWindow = oldOpen })

	a := &shelf{
		store: s, dataDir: dir, visible: true, focusMode: true,
		level: 1, locale: "en", settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{},
	}
	v := ui.NewTester(a.view, sheetW, 400)
	v.SetLocale("en")
	v.Frame()
	v.Frame()

	if err := v.Click(a.appLoc().PanelSettings()); err != nil {
		t.Fatalf("click the settings gear: %v", err)
	}
	if opened != 1 {
		t.Fatalf("settings opened %d times, want 1", opened)
	}
	if a.visible {
		t.Fatal("panel stayed visible after clicking the settings gear")
	}
}
