//go:build darwin

package main

// Tests for the decision behind the bundle self-rename: only an executable
// inside Shelf.app moves, and the new bundle path is always a sibling of
// the old one. Nothing here touches a real installed app.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfRenameTarget(t *testing.T) {
	cases := []struct {
		exe     string
		old     string
		next    string
		wantHit bool
	}{
		// An in-app update of an earlier name leaves the new bundle at ShelfU.app.
		{"/Applications/ShelfU.app/Contents/MacOS/ShelfU", "/Applications/ShelfU.app", "/Applications/Rakko.app", true},
		// An update that ran while the app was still called Shelf.
		{"/Applications/Shelf.app/Contents/MacOS/Shelf", "/Applications/Shelf.app", "/Applications/Rakko.app", true},
		{"/Applications/Shelf.app/Contents/MacOS/ShelfU", "/Applications/Shelf.app", "/Applications/Rakko.app", true},
		{"/Users/me/Applications/ShelfU.app/Contents/MacOS/ShelfU", "/Users/me/Applications/ShelfU.app", "/Users/me/Applications/Rakko.app", true},
		// Already renamed: nothing to do.
		{"/Applications/Rakko.app/Contents/MacOS/ShelfU", "", "", false},
		// go run and go test build into a temporary directory.
		{"/var/folders/xy/T/go-build123/b001/exe/shelf", "", "", false},
		// A bare executable, and a directory that only looks like a bundle.
		{"/tmp/Shelf", "", "", false},
		{"/tmp/Rakko.app/Contents/ShelfU", "", "", false},
		{"/tmp/Rakko.app/MacOS/ShelfU", "", "", false},
	}
	for _, tc := range cases {
		old, next, ok := selfRenameTarget(tc.exe)
		if ok != tc.wantHit || old != tc.old || next != tc.next {
			t.Errorf("selfRenameTarget(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.exe, old, next, ok, tc.old, tc.next, tc.wantHit)
		}
	}
}

// TestSelfRenameTargetIsSiblingOfTheOldBundle pins the property the rename
// relies on: the new bundle is created in the same directory, so the rename
// stays on one volume.
func TestSelfRenameTargetIsSiblingOfTheOldBundle(t *testing.T) {
	old, next, ok := selfRenameTarget("/Applications/ShelfU.app/Contents/MacOS/ShelfU")
	if !ok {
		t.Fatal("an exe inside ShelfU.app was not recognised")
	}
	if filepath.Dir(old) != filepath.Dir(next) {
		t.Errorf("the new bundle %q is not next to %q", next, old)
	}
	if filepath.Base(next) != newBundleName || !isOldBundleName(filepath.Base(old)) {
		t.Errorf("bundle names = %q, %q; want one of %q, %q", filepath.Base(old), filepath.Base(next), oldBundleNames, newBundleName)
	}
}

// TestSelfRenameBundleIgnoresNonBundles checks the wrapper used at startup:
// under go test the executable is not in an .app, so the call is a no-op
// and leaves the process running.
func TestSelfRenameBundleIgnoresNonBundles(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable path:", err)
	}
	if _, _, ok := selfRenameTarget(exe); ok {
		t.Skipf("the test binary runs from %s, inside an old bundle", exe)
	}
	selfRenameBundle() // must return without exiting
}
