package main

import (
	"reflect"
	"testing"
)

// osText is the seam every view uses to pick its wording: macOS keeps the
// original strings and Windows reads the Win keys of the same catalog.
func TestOSWordingFollowsThePlatform(t *testing.T) {
	mac, win := "在 Finder 中显示", "在文件资源管理器中显示"
	if got := osTextFor(false, mac, win); got != mac {
		t.Errorf("macOS wording = %q, want %q", got, mac)
	}
	if got := osTextFor(true, mac, win); got != win {
		t.Errorf("Windows wording = %q, want %q", got, win)
	}
}

// Windows users read shortcuts as the keys they print; macOS keeps its
// glyphs. The same accelerator must give both spellings.
func TestShortcutSpellingFollowsThePlatform(t *testing.T) {
	cases := []struct {
		acc          string
		mac, windows string
	}{
		{"Ctrl+Alt+Space", "⌃⌥Space", "Ctrl+Alt+Space"},
		{"Ctrl+Alt+V", "⌃⌥V", "Ctrl+Alt+V"},
		{"Cmd+Shift+Z", "⌘⇧Z", "Ctrl+Shift+Z"},
		{"CmdOrCtrl+Return", "⌘Return", "Ctrl+Return"},
		{"Super+Alt+2", "⌘⌥2", "Win+Alt+2"},
	}
	for _, tc := range cases {
		if got := shortcutLabelFor(false, tc.acc); got != tc.mac {
			t.Errorf("%s on macOS = %q, want %q", tc.acc, got, tc.mac)
		}
		if got := shortcutLabelFor(true, tc.acc); got != tc.windows {
			t.Errorf("%s on Windows = %q, want %q", tc.acc, got, tc.windows)
		}
	}

	if got, ok := shortcutTokensFor(true, "Ctrl+Alt+Space"); !ok ||
		!reflect.DeepEqual(got, []string{"Ctrl", "Alt", "Space"}) {
		t.Errorf("Windows keycaps = %v (ok=%v), want Ctrl Alt Space", got, ok)
	}
	if got, ok := shortcutTokensFor(false, "Ctrl+Alt+Space"); !ok ||
		!reflect.DeepEqual(got, []string{"⌃", "⌥", "Space"}) {
		t.Errorf("macOS keycaps = %v (ok=%v), want ⌃ ⌥ Space", got, ok)
	}
	if got := shortcutLabelFor(true, "Bogus+X"); got != "Bogus+X" {
		t.Errorf("malformed accelerator = %q, want it unchanged", got)
	}
}

// The tools catalog offers AirDrop on macOS and omits nearby sharing on Windows.
func TestToolCatalogAirDropFollowsPlatform(t *testing.T) {
	found := false
	for _, def := range toolCatalog() {
		if def.id == "airdrop" {
			found = true
			if def.title != "隔空投送" {
				t.Errorf("airdrop card title = %q, want 隔空投送", def.title)
			}
		}
	}
	if found != airDropSupported {
		t.Errorf("toolCatalog contains airdrop = %v, want %v", found, airDropSupported)
	}
}
