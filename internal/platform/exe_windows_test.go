//go:build windows

package platform

// Where the installed executable is. A configuration written with a
// development build's path stops working at once, and a configuration
// written while the updater has renamed the running executable must still
// name the installed one; these cases pin both.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withInstallDir pins the install directory for one test.
func withInstallDir(t *testing.T, dir string) {
	t.Helper()
	old := installDir
	installDir = func() (string, bool) { return dir, true }
	t.Cleanup(func() { installDir = old })
}

func TestStableExePathAcceptsTheInstalledBinary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Programs", "Rakko")
	withInstallDir(t, dir)
	exe := filepath.Join(dir, "Rakko.exe")
	if got, ok := StableExePath(exe); !ok || got != exe {
		t.Fatalf("StableExePath(%q) = %q, %v; want %q, true", exe, got, ok, exe)
	}
}

// The updater renames a running executable to .Rakko.exe.old-<pid> in the
// same directory. A tool entry written then must name Rakko.exe beside it,
// which is where the new version lands before the next start.
func TestStableExePathResolvesTheUpdaterName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Programs", "Rakko")
	withInstallDir(t, dir)
	for _, name := range []string{".Rakko.exe.old-1234", ".Rakko.exe.old-1"} {
		exe := filepath.Join(dir, name)
		want := filepath.Join(dir, "Rakko.exe")
		got, ok := StableExePath(exe)
		if !ok || got != want {
			t.Fatalf("StableExePath(%q) = %q, %v; want %q, true", exe, got, ok, want)
		}
	}
}

func TestStableExePathRejectsEverythingElse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Programs", "Rakko")
	withInstallDir(t, dir)
	cases := []string{
		"",
		filepath.Join(dir, "other.exe"),
		filepath.Join(dir, "Rakko.exe.old-12"),
		filepath.Join(dir, "Rakko.exe.bak"),
		filepath.Join(dir, ".Rakko.exe.old-"),
		filepath.Join(dir, ".Rakko.exe.old-x1"),
		filepath.Join(t.TempDir(), "go-build123", "Rakko.exe"),
		filepath.Join(t.TempDir(), "Rakko.exe"),
		`C:\repo\Rakko-winagent\Rakko.exe`,
	}
	for _, exe := range cases {
		if got, ok := StableExePath(exe); ok {
			t.Errorf("StableExePath(%q) = %q, true; want false", exe, got)
		}
	}
}

// The path comes back with a volume prefix stripped and in the install
// directory's spelling, whatever case the caller used.
func TestStableExePathNormalizes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Programs", "Rakko")
	withInstallDir(t, dir)
	exe := `\\?\` + strings.ToUpper(dir) + `\RAKKO.EXE`
	if got, ok := StableExePath(exe); !ok || got != filepath.Join(dir, "Rakko.exe") {
		t.Fatalf("StableExePath(%q) = %q, %v", exe, got, ok)
	}
}

func TestDefaultInstallDir(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	got, ok := defaultInstallDir()
	if !ok || got != filepath.Join(local, "Programs", "Rakko") {
		t.Fatalf("defaultInstallDir() = %q, %v", got, ok)
	}
	t.Setenv("LOCALAPPDATA", "")
	if got, ok := defaultInstallDir(); ok {
		t.Fatalf("defaultInstallDir() without LOCALAPPDATA = %q, true", got)
	}
}

// A user-chosen install directory is read from Rakko's own uninstall entry.
// The test machine may already have Rakko installed, in which case the entry
// is exactly what the running install uses and only its shape can be
// checked.
func TestRegistryInstallDir(t *testing.T) {
	dir, ok := registryInstallDir()
	if !ok {
		return // Rakko is not installed on this machine; nothing to read.
	}
	if dir == "" || !filepath.IsAbs(dir) {
		t.Fatalf("registryInstallDir() = %q, true; want an absolute directory", dir)
	}
}

// The package's own install directory must not depend on the caller's
// working directory.
func TestStableExePathKeepsTheInstallSpelling(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my Programs", "Rakko")
	withInstallDir(t, dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "Rakko.exe")
	if got, ok := StableExePath(exe); !ok || got != exe {
		t.Fatalf("StableExePath(%q) = %q, %v", exe, got, ok)
	}
}
