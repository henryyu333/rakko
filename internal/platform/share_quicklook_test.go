//go:build darwin

package platform

import (
	"path/filepath"
	"testing"
)

// The error paths run before any AppKit object is touched, so they are
// safe on the test goroutine and on headless machines.

func TestAirDropEmptyItems(t *testing.T) {
	if err := AirDrop(nil, nil); err == nil {
		t.Fatal("AirDrop(nil, nil) = nil, want error")
	}
}

func TestQuickLookMissingFiles(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.png")
	if err := QuickLook([]string{missing, ""}); err == nil {
		t.Fatal("QuickLook(missing) = nil, want error")
	}
	if err := QuickLook(nil); err == nil {
		t.Fatal("QuickLook(nil) = nil, want error")
	}
}
