package main

import (
	"testing"

	"rakko/internal/settings"
)

// The shortcut hints are rendered from the accelerators settings.json holds,
// so a hint can never disagree with the shortcut the app registers. macOS
// draws glyphs; Windows spells the modifiers.
func TestShortcutLabelGlyphs(t *testing.T) {
	cases := []struct{ in, mac, windows string }{
		{"Ctrl+Alt+Space", "⌃⌥Space", "Ctrl+Alt+Space"},
		{"Ctrl+Alt+S", "⌃⌥S", "Ctrl+Alt+S"},
		{"Cmd+Shift+Z", "⌘⇧Z", "Ctrl+Shift+Z"},
		{"CmdOrCtrl+Shift+Space", "⌘⇧Space", "Ctrl+Shift+Space"},
		{"Super+Alt+2", "⌘⌥2", "Win+Alt+2"},
		{"Shift+F5", "⇧F5", "Shift+F5"},
		// 未知写法原样显示，不虚构按键。
		{"Bogus+Key", "Bogus+Key", "Bogus+Key"},
		{"", "", ""},
	}
	for _, tc := range cases {
		want := tc.mac
		if forWindows {
			want = tc.windows
		}
		if got := shortcutLabel(tc.in); got != want {
			t.Fatalf("shortcutLabel(%q) = %q, want %q", tc.in, got, want)
		}
	}
}

// A fresh install's three shortcuts still render, and the fourth is unset.
func TestDefaultShortcutsRender(t *testing.T) {
	def := settings.Default().Shortcuts
	toggle, clipboard, capture := "⌃⌥Space", "⌃⌥V", "⌃⌥O"
	if forWindows {
		toggle, clipboard, capture = "Ctrl+Alt+Space", "Ctrl+Alt+V", "Ctrl+Alt+O"
	}
	for acc, want := range map[string]string{
		def.Toggle:    toggle,
		def.Clipboard: clipboard,
		def.Capture:   capture,
	} {
		if got := shortcutLabel(acc); got != want {
			t.Fatalf("shortcutLabel(%q) = %q, want %q", acc, got, want)
		}
	}
	if def.Tray != "" {
		t.Fatalf("the tray shortcut must default to unset, got %q", def.Tray)
	}
}
