//go:build darwin

package platform

import (
	"path/filepath"
	"testing"
)

// The error paths run before any AppKit object is touched, so they are
// safe on the test goroutine and on headless machines.

func TestAirDropEmptyItems(t *testing.T) {
	if err := ShareNearby(nil, nil); err == nil {
		t.Fatal("ShareNearby(nil, nil) = nil, want error")
	}
}

func TestQuickLookMissingFiles(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.png")
	if err := Preview([]string{missing, ""}); err == nil {
		t.Fatal("Preview(missing) = nil, want error")
	}
	if err := Preview(nil); err == nil {
		t.Fatal("Preview(nil) = nil, want error")
	}
}
