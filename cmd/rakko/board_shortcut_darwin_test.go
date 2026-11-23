//go:build darwin

package main

import (
	"testing"

	"rakko/internal/settings"
)

func TestBoardShortcutRegistersAndFires(t *testing.T) {
	f := installFakeKeys(t)
	a := &shelf{
		dataDir:  t.TempDir(),
		settings: settings.Default(),
	}
	a.settings.Shortcuts.Board = "Ctrl+Alt+B"
	a.registerShortcuts()
	if !f.has("Ctrl+Alt+B") || a.shortcutHeld(shortcutBoard) != "Ctrl+Alt+B" {
		t.Fatalf("board shortcut not registered: %v", f.regs)
	}
}
