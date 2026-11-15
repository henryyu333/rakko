//go:build windows

package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A fresh Windows install used to open as the floating 灵动岛; the simulated
// notch (模拟小刘海) is the Mac look this port starts with now. The island
// stays selectable, and a settings.json that names it keeps it.
func TestFreshInstallDefaultsToSimulatedNotch(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.ShellStyle != ShellStyleNotch {
		t.Fatalf("fresh install shellStyle = %q, want %q", s.ShellStyle, ShellStyleNotch)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"shellStyle": "notch"`) {
		t.Fatalf("settings.json recorded no notch style: %s", data)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"shellStyle": "island"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	back, err := Load(dir)
	if err != nil || back.ShellStyle != ShellStyleIsland {
		t.Fatalf("a saved island loaded %q, %v", back.ShellStyle, err)
	}
}
