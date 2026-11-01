//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOtterImageDecodesOnce: the same name is decoded once and cached.
func TestOtterImageDecodesOnce(t *testing.T) {
	oldDir := otterDir
	t.Cleanup(func() { otterDir = oldDir; resetOtters() })
	dir := t.TempDir()
	otterDir = func() string { return dir }
	// Write one real PNG under the expected name.
	if err := os.WriteFile(filepath.Join(dir, "shelfu-otter_ui_happy_v001.png"), testPNGBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	resetOtters()
	first := otterImage("happy")
	if first == nil {
		t.Fatal("the picture did not decode")
	}
	if second := otterImage("happy"); second != first {
		t.Fatal("the picture was decoded twice")
	}
	if otterImage("not-a-state") != nil {
		t.Fatal("an unknown state answered a picture")
	}
}
