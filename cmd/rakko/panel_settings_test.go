package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/internal/store"
)

// TestPanelSettingsGearKeepsThePanelWhereThePlatformAllowsIt: the page
// capsule's gear opens Settings; on Windows the overlay closes first, since
// its content composites over the window stack, while on macOS the panel
// stays open behind the window as it always has.
func TestPanelSettingsGearKeepsThePanelWhereThePlatformAllowsIt(t *testing.T) {
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
	if want := !overlayHidesForSettings; a.visible != want {
		t.Fatalf("panel visible = %v after the gear, want %v on this platform", a.visible, want)
	}
}
