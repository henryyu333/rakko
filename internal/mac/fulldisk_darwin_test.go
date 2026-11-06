//go:build darwin

package mac

// Tests for the Full Disk Access probe and request. The probe is stubbed to
// temp files, and the request's two effects — opening System Settings and
// revealing the bundle in Finder — are seams, so no test touches a real TCC
// record, System Settings or Finder.

import (
	"os"
	"path/filepath"
	"testing"
)

// stubFullDisk replaces the probe's paths, the executable and both request
// effects, restoring them when the test ends.
func stubFullDisk(t *testing.T, paths ...string) (opens *[]string, reveals *[]string) {
	t.Helper()
	oldPaths, oldExe := fullDiskAccessPaths, fullDiskAccessExe
	oldReveal, oldOpen := revealBundleInFinder, openURL
	var o, r []string
	fullDiskAccessPaths = func() []string { return paths }
	fullDiskAccessExe = func() (string, error) { return "/tmp/ShelfU", nil }
	revealBundleInFinder = func(bundle string) { r = append(r, bundle) }
	openURL = func(url string) { o = append(o, url) }
	t.Cleanup(func() {
		fullDiskAccessPaths, fullDiskAccessExe = oldPaths, oldExe
		revealBundleInFinder, openURL = oldReveal, oldOpen
	})
	return &o, &r
}

func TestFullDiskAccessProbeReadsTheTCCDatabase(t *testing.T) {
	dir := t.TempDir()
	readable := filepath.Join(dir, "TCC.db")
	if err := os.WriteFile(readable, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "absent", "TCC.db")

	// An openable database is the grant.
	_, _ = stubFullDisk(t, readable)
	if !FullDiskAccessGranted() {
		t.Fatal("an openable TCC database must read as granted")
	}

	// A missing one is not, and the probe reports it instead of failing.
	_, _ = stubFullDisk(t, missing)
	if FullDiskAccessGranted() {
		t.Fatal("a missing TCC database must read as not granted")
	}

	// The list is tried in order: the first miss does not stop the probe.
	_, _ = stubFullDisk(t, missing, readable)
	if !FullDiskAccessGranted() {
		t.Fatal("a later TCC database was not tried")
	}

	// A denied open (EPERM from TCC) is not granted either.
	_, _ = stubFullDisk(t, filepath.Join(dir, "denied"))
	oldOpen := fullDiskAccessOpenFile
	fullDiskAccessOpenFile = func(string) (*os.File, error) {
		return nil, &os.PathError{Op: "open", Path: "denied", Err: os.ErrPermission}
	}
	t.Cleanup(func() { fullDiskAccessOpenFile = oldOpen })
	if FullDiskAccessGranted() {
		t.Fatal("a denied TCC database must read as not granted")
	}
}

func TestFullDiskAccessDefaultPaths(t *testing.T) {
	paths := defaultFullDiskAccessPaths()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	// The per-user database (macOS 25 and older) comes first, the system one
	// last: macOS 26 dropped the user database, so the system one must
	// always be probed.
	if len(paths) < 2 {
		t.Fatalf("expected both TCC databases, got %v", paths)
	}
	if want := filepath.Join(home, "Library", "Application Support", "com.apple.TCC", "TCC.db"); paths[0] != want {
		t.Fatalf("first probe path = %q, want %q", paths[0], want)
	}
	if last := paths[len(paths)-1]; last != "/Library/Application Support/com.apple.TCC/TCC.db" {
		t.Fatalf("last probe path = %q, want the system TCC database", last)
	}
}

func TestRequestFullDiskAccessOpensPaneAndRevealsBundle(t *testing.T) {
	opens, reveals := stubFullDisk(t, "/nonexistent/TCC.db")
	if RequestFullDiskAccess() {
		t.Fatal("an ungranted request must not report success")
	}
	if len(*opens) != 1 || (*opens)[0] != "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles" {
		t.Fatalf("the 完整磁盘访问 pane was not opened: %v", *opens)
	}
	// A bare executable has no bundle to reveal: the pane alone is the offer.
	if len(*reveals) != 0 {
		t.Fatalf("a non-bundled run revealed something: %v", *reveals)
	}
}

func TestRequestFullDiskAccessRevealsTheRunningBundle(t *testing.T) {
	opens, reveals := stubFullDisk(t, "/nonexistent/TCC.db")
	fullDiskAccessExe = func() (string, error) { return "/Applications/ShelfU.app/Contents/MacOS/ShelfU", nil }
	if RequestFullDiskAccess() {
		t.Fatal("an ungranted request must not report success")
	}
	if len(*reveals) != 1 || (*reveals)[0] != "/Applications/ShelfU.app" {
		t.Fatalf("the running bundle was not revealed: %v", *reveals)
	}
	if len(*opens) != 1 {
		t.Fatalf("the pane was not opened next to the reveal: %v", *opens)
	}
}

func TestRequestFullDiskAccessLeavesGrantedAlone(t *testing.T) {
	dir := t.TempDir()
	readable := filepath.Join(dir, "TCC.db")
	if err := os.WriteFile(readable, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	opens, reveals := stubFullDisk(t, readable)
	if !RequestFullDiskAccess() {
		t.Fatal("an already granted request must report success")
	}
	if len(*opens) != 0 || len(*reveals) != 0 {
		t.Fatalf("a granted request touched System Settings or Finder: opens=%v reveals=%v", *opens, *reveals)
	}
}

func TestOpenPrivacySecurityPane(t *testing.T) {
	oldOpen := openURL
	var got string
	openURL = func(url string) { got = url }
	t.Cleanup(func() { openURL = oldOpen })
	OpenPrivacySecurityPane()
	if got != "x-apple.systempreferences:com.apple.preference.security" {
		t.Fatalf("opened %q, want the 隐私与安全性 pane", got)
	}
}

func TestAppBundleForExecutableInMac(t *testing.T) {
	for _, tt := range []struct {
		exe  string
		want string
		ok   bool
	}{
		{"/Applications/ShelfU.app/Contents/MacOS/ShelfU", "/Applications/ShelfU.app", true},
		{"/tmp/a b/My App.app/Contents/MacOS/My App", "/tmp/a b/My App.app", true},
		{"/tmp/ShelfU", "", false},
		{"/tmp/Not.app/tool", "", false},
		{"", "", false},
	} {
		got, ok := AppBundleForExecutable(tt.exe)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("AppBundleForExecutable(%q) = %q,%v; want %q,%v", tt.exe, got, ok, tt.want, tt.ok)
		}
	}
}
