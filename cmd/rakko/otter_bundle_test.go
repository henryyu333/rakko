package main

// The bust artwork exists under both build prefixes, byte for byte, and
// mygo.json ships one copy of it (resourcesExclude). The loader tries the
// prefixes in order, so every bust must still resolve from a prefix the
// bundle keeps: a future drop that renames the kept prefix would empty the
// panel's pictures, and this test says so before a build does.

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestOtterBustsSurviveTheBundleExclusions(t *testing.T) {
	root := filepath.Join("..", "..")
	art := filepath.Join(root, "resources", "otter")
	if _, err := os.Stat(art); err != nil {
		t.Skipf("the brand artwork is not in this checkout: %v", err)
	}
	var cfg struct {
		ResourcesExclude []string `json:"resourcesExclude"`
	}
	data, err := os.ReadFile(filepath.Join(root, "mygo.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	// mygo matches each pattern's segments against the resource's path;
	// every pattern this project writes is flat, so path.Match over the
	// whole path says the same thing.
	shipped := func(rel string) bool {
		for _, p := range cfg.ResourcesExclude {
			if strings.Contains(p, "**") {
				t.Fatalf("this test does not evaluate the ** pattern %q", p)
			}
			ok, err := path.Match(p, rel)
			if err != nil {
				t.Fatalf("mygo.json pattern %q: %v", p, err)
			}
			if ok {
				return false
			}
		}
		return true
	}
	for name, stem := range otterFiles {
		if !strings.HasPrefix(stem, "ui_") {
			continue // the desktop poses exist under one prefix only
		}
		found := ""
		for _, prefix := range otterPrefixes {
			rel := "otter/" + prefix + "otter_" + stem + "_v001.png"
			if !fileExists(filepath.Join(root, "resources", rel)) || !shipped(rel) {
				continue
			}
			found = rel
			break
		}
		if found == "" {
			t.Errorf("no picture of %s (%s) survives resourcesExclude", name, stem)
		}
	}
}
