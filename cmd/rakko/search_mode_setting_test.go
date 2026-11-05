//go:build darwin

package main

// The default search mode setting: the bar opens on the saved mode, Tab and
// the capsule change only the search on screen, the settings row saves the
// next opening's mode, and a broken settings.json locks the row.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/settings"
	"rakko/internal/store"
)

// modeSettingShelf is a home shelf whose default mode is saved in a fresh
// data directory.
func modeSettingShelf(t *testing.T, mode settings.SearchMode) *shelf {
	t.Helper()
	a := &shelf{
		store: &store.Store{}, dataDir: t.TempDir(), settings: settings.Default(),
		appNames: map[string]string{},
	}
	a.settings.SearchMode = mode
	return a
}

// TestDefaultSearchModeOpensTheBar: every opening starts on the saved mode,
// and 应用 / 文件 show their capsule before anything is typed.
func TestDefaultSearchModeOpensTheBar(t *testing.T) {
	for _, c := range []struct {
		mode  settings.SearchMode
		scope mac.SearchScope
	}{
		{settings.SearchModeAll, mac.ScopeAll},
		{settings.SearchModeApps, mac.ScopeApps},
		{settings.SearchModeFiles, mac.ScopeFiles},
	} {
		a := modeSettingShelf(t, c.mode)
		a.show(false)
		if a.homeScope != c.scope {
			t.Fatalf("default %q opened on scope %d, want %d", c.mode, a.homeScope, c.scope)
		}
		if got := a.defaultHomeScope(); got != c.scope {
			t.Fatalf("defaultHomeScope(%q) = %d, want %d", c.mode, got, c.scope)
		}

		// An empty bar already says which mode it is in, except 全部.
		a.visible, a.focusMode, a.level = true, true, 1
		v := ui.NewTester(a.view, homeW, int(bandH+homeBarH)+8)
		v.SetLocale("zh-Hans")
		v.Frame()
		l := a.appLoc()
		name := l.SearchModeFiles()
		if c.scope == mac.ScopeApps {
			name = l.SearchModeApps()
		}
		_, drawn := v.Find(name)
		if drawn != (c.scope != mac.ScopeAll) {
			t.Fatalf("default %q: capsule %q drawn = %v", c.mode, name, drawn)
		}
		if _, all := v.Find(l.SearchModeAll()); all {
			t.Fatalf("default %q drew a 全部 capsule", c.mode)
		}
	}
}

// TestTabChangesOneSearchOnly: a default of 文件 survives a Tab to 全部 and
// a close; the next opening comes back on 文件, never on the last search.
func TestTabChangesOneSearchOnly(t *testing.T) {
	a := modeSettingShelf(t, settings.SearchModeFiles)
	a.show(false)
	if a.homeScope != mac.ScopeFiles {
		t.Fatalf("the bar opened on scope %d, want 文件", a.homeScope)
	}

	a.visible, a.focusMode, a.level = true, true, 1
	v := ui.NewTester(a.view, homeW, int(bandH+homeBarH)+8)
	v.SetLocale("zh-Hans")
	v.Frame()
	v.Frame()
	v.Key(0, ui.KeyTab)
	v.Frame()
	if a.homeScope != mac.ScopeAll {
		t.Fatalf("Tab from 文件 left scope %d, want 全部", a.homeScope)
	}
	if a.settings.SearchMode != settings.SearchModeFiles {
		t.Fatalf("Tab saved %q as the default", a.settings.SearchMode)
	}

	a.hide(hideExplicit)
	a.finishClose()
	a.show(false)
	if a.homeScope != mac.ScopeFiles {
		t.Fatalf("the reopened bar started on scope %d, want the saved 文件", a.homeScope)
	}
}

// TestGoingHomeUsesTheDefaultMode: the page → bar path is an opening too.
func TestGoingHomeUsesTheDefaultMode(t *testing.T) {
	a := modeSettingShelf(t, settings.SearchModeApps)
	a.visible, a.level, a.focusMode = true, 2, true
	a.homeScope = mac.ScopeAll
	a.goHome()
	if a.homeScope != mac.ScopeApps {
		t.Fatalf("going home left scope %d, want the saved 应用", a.homeScope)
	}
}

// TestSetSearchModeSavesForTheNextOpening: the settings row writes
// settings.json and leaves the search on screen in its own mode.
func TestSetSearchModeSavesForTheNextOpening(t *testing.T) {
	a := modeSettingShelf(t, settings.SearchModeAll)
	a.homeScope = mac.ScopeFiles
	a.homeQuery = "rakko"

	a.setSearchMode(settings.SearchModeApps)
	if a.settings.SearchMode != settings.SearchModeApps {
		t.Fatalf("the shelf kept %q", a.settings.SearchMode)
	}
	if a.homeScope != mac.ScopeFiles {
		t.Fatalf("the visible search moved to scope %d", a.homeScope)
	}
	back, err := settings.Load(a.dataDir)
	if err != nil || back.SearchMode != settings.SearchModeApps {
		t.Fatalf("settings.json kept %q, %v", back.SearchMode, err)
	}

	// The next opening uses it.
	a.visible = false
	a.show(false)
	if a.homeScope != mac.ScopeApps {
		t.Fatalf("the next opening used scope %d, want 应用", a.homeScope)
	}
}

// TestBrokenSettingsDisableTheSearchModeRow: a settings.json that could not
// be read leaves the row disabled, and a click on it changes neither the
// shelf nor the file.
func TestBrokenSettingsDisableTheSearchModeRow(t *testing.T) {
	a := modeSettingShelf(t, settings.SearchModeAll)
	a.settingsBroken = true
	path := filepath.Join(a.dataDir, settings.FileName)
	body := []byte("{ not json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	v := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	v.SetLocale("zh-Hans")
	v.Frame()
	v.Frame()
	l := a.appLoc()
	for _, label := range []string{l.SearchModeAll(), l.SearchModeApps(), l.SearchModeFiles()} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("the search mode row lost %q: %v", label, v.Texts())
		}
	}
	if err := v.Click(l.SearchModeApps()); err != nil {
		t.Fatalf("clicking the row failed: %v", err)
	}
	v.Frame()
	if a.settings.SearchMode != settings.SearchModeAll {
		t.Fatalf("a broken settings.json took %q", a.settings.SearchMode)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(body) {
		t.Fatalf("the broken file was rewritten: %q, %v", got, err)
	}

	// With a readable file the same click saves the choice.
	a.settingsBroken = false
	os.Remove(path)
	v.Frame()
	if err := v.Click(l.SearchModeFiles()); err != nil {
		t.Fatalf("clicking 文件 failed: %v", err)
	}
	v.Frame()
	if a.settings.SearchMode != settings.SearchModeFiles {
		t.Fatalf("clicking 文件 saved %q", a.settings.SearchMode)
	}
	back, err := settings.Load(a.dataDir)
	if err != nil || back.SearchMode != settings.SearchModeFiles {
		t.Fatalf("settings.json kept %q, %v", back.SearchMode, err)
	}
}
