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

// TestOtterFindsBothInstalledPrefixes: the panel's busts are installed as
// shelfu-* and the otter's pictures kept from the earlier build as
// rakko-*; one loader answers both, so a local install never needs a
// rename to show either family.
func TestOtterFindsBothInstalledPrefixes(t *testing.T) {
	oldDir := otterDir
	t.Cleanup(func() { otterDir = oldDir; resetOtters() })
	dir := t.TempDir()
	otterDir = func() string { return dir }
	for _, name := range []string{"shelfu-otter_ui_neutral_v001.png", "rakko-otter_pet_idle_v001.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), testPNGBytes(t), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resetOtters()
	if otterImage(otterNeutral) == nil {
		t.Fatal("the shelfu- bust did not decode")
	}
	if petArt() == nil {
		t.Fatal("the rakko- pet pose did not decode")
	}
}
