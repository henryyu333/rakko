//go:build darwin

package main

// Tests for the one-time move of the data directory from the pre-rename
// Support/Shelf to Support/ShelfU. Every root is a temporary directory, so
// no test touches the user's real data.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// fixtureRoot fills root with what the old app kept: state, layout,
// settings, a managed image, a converted file, and the store's lock file
// (with the pid it writes). The paths inside the JSON files are absolute
// and point into root, as the old app wrote them.
func fixtureRoot(t *testing.T, root string) {
	t.Helper()
	for _, dir := range []string{"images", "converted", "usage-cache"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	img := filepath.Join(root, "images", "shot.png")
	if err := os.WriteFile(img, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	conv := filepath.Join(root, "converted", "报告.pdf")
	if err := os.WriteFile(conv, []byte("pdf-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"version": 1,
		"items": []any{
			map[string]any{"id": "a", "kind": "image", "path": "shot.png", "createdAt": "2026-10-01T10:00:00Z"},
			// A file item keeps an absolute path; one of them lives inside
			// the data directory, which is what the migration must rewrite.
			map[string]any{"id": "b", "kind": "file", "path": conv, "createdAt": "2026-10-01T10:01:00Z"},
			map[string]any{"id": "c", "kind": "text", "text": "keep me", "createdAt": "2026-10-01T10:02:00Z"},
		},
	}
	writeJSON(t, filepath.Join(root, "state.json"), state, 0o600)
	layout := map[string]any{
		"version": 2,
		"pins":    []any{map[string]any{"id": "usage", "edge": "left", "file": filepath.Join(root, "converted", "报告.pdf")}},
	}
	writeJSON(t, filepath.Join(root, "layout.json"), layout, 0o600)
	settings := map[string]any{"clipboardLimit": 30, "islandNowPlaying": true, "cache": filepath.Join(root, "usage-cache")}
	writeJSON(t, filepath.Join(root, "settings.json"), settings, 0o600)
	if err := os.WriteFile(filepath.Join(root, ".lock"), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeJSON writes doc with the two-space indent the app uses.
func writeJSON(t *testing.T, path string, doc any, perm os.FileMode) {
	t.Helper()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), perm); err != nil {
		t.Fatal(err)
	}
}

// snapshotTree maps every entry below root to its content (or "" for a
// directory) and its permission bits, so a test can prove a tree did not
// change.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[rel] = "dir " + info.Mode().Perm().String()
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(data) + " " + info.Mode().Perm().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMigrateDataRootMovesOldData(t *testing.T) {
	base := t.TempDir()
	oldRoot := filepath.Join(base, "Shelf")
	fixtureRoot(t, oldRoot)
	newRoot := filepath.Join(base, "ShelfU")
	before := snapshotTree(t, oldRoot)

	migrated, err := migrateDataRoot(oldRoot, newRoot)
	if err != nil || !migrated {
		t.Fatalf("migrateDataRoot = (%v, %v), want (true, nil)", migrated, err)
	}

	// The content arrives: managed image, converted file, text item.
	for _, rel := range []string{"images/shot.png", "converted/报告.pdf", "state.json", "layout.json", "settings.json"} {
		if _, err := os.Stat(filepath.Join(newRoot, rel)); err != nil {
			t.Errorf("%s missing from the new root: %v", rel, err)
		}
	}
	// The new root takes its own lock and does not inherit the old pid file.
	if _, err := os.Stat(filepath.Join(newRoot, ".lock")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the copied root has a .lock: %v", err)
	}
	// Absolute paths inside the old root now point into the new one.
	state := readJSON(t, filepath.Join(newRoot, "state.json"))
	items, _ := state["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("state has %d items, want 3", len(items))
	}
	if got, want := itemField(items[1], "path"), filepath.Join(newRoot, "converted", "报告.pdf"); got != want {
		t.Errorf("converted file path = %q, want %q", got, want)
	}
	if got := itemField(items[0], "path"); got != "shot.png" {
		t.Errorf("managed image path = %q, want the relative name untouched", got)
	}
	if got := itemField(items[2], "text"); got != "keep me" {
		t.Errorf("text item = %q, want its content untouched", got)
	}
	layout := readJSON(t, filepath.Join(newRoot, "layout.json"))
	pins, _ := layout["pins"].([]any)
	if len(pins) != 1 {
		t.Fatalf("layout has %d pins, want 1", len(pins))
	}
	if got, want := itemField(pins[0], "file"), filepath.Join(newRoot, "converted", "报告.pdf"); got != want {
		t.Errorf("layout path = %q, want %q", got, want)
	}
	if got, want := readJSON(t, filepath.Join(newRoot, "settings.json"))["cache"], filepath.Join(newRoot, "usage-cache"); got != want {
		t.Errorf("settings path = %v, want %v", got, want)
	}
	// The old root is untouched: it stays as a complete backup.
	if after := snapshotTree(t, oldRoot); !sameTree(before, after) {
		t.Errorf("the old root changed:\nbefore %v\nafter  %v", before, after)
	}
}

func TestMigrateDataRootKeepsExistingNewRoot(t *testing.T) {
	base := t.TempDir()
	oldRoot := filepath.Join(base, "Shelf")
	fixtureRoot(t, oldRoot)
	newRoot := filepath.Join(base, "ShelfU")
	if err := os.MkdirAll(newRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(newRoot, "state.json")
	if err := os.WriteFile(marker, []byte(`{"version":1,"items":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, oldRoot)

	migrated, err := migrateDataRoot(oldRoot, newRoot)
	if err != nil || migrated {
		t.Fatalf("migrateDataRoot = (%v, %v), want (false, nil)", migrated, err)
	}
	if after := snapshotTree(t, oldRoot); !sameTree(before, after) {
		t.Errorf("the old root changed although the new one exists")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != `{"version":1,"items":[]}` {
		t.Errorf("the new root's state.json was replaced: %q, %v", data, err)
	}
}

func TestMigrateDataRootWithoutOldRoot(t *testing.T) {
	base := t.TempDir()
	newRoot := filepath.Join(base, "ShelfU")
	migrated, err := migrateDataRoot(filepath.Join(base, "Shelf"), newRoot)
	if err != nil || migrated {
		t.Fatalf("migrateDataRoot = (%v, %v), want (false, nil)", migrated, err)
	}
	if _, err := os.Stat(newRoot); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the migration created %s for a missing old root", newRoot)
	}
}

func TestMigrateDataRootRefusesARunningOldApp(t *testing.T) {
	base := t.TempDir()
	oldRoot := filepath.Join(base, "Shelf")
	fixtureRoot(t, oldRoot)
	// Hold the old root's lock the way a live old app does.
	holder, err := os.OpenFile(filepath.Join(oldRoot, ".lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("locking the fixture: %v", err)
	}
	before := snapshotTree(t, oldRoot)
	newRoot := filepath.Join(base, "ShelfU")

	migrated, err := migrateDataRoot(oldRoot, newRoot)
	if !errors.Is(err, errLegacyRootLocked) {
		t.Fatalf("migrateDataRoot = (%v, %v), want errLegacyRootLocked", migrated, err)
	}
	if migrated {
		t.Errorf("migrateDataRoot reported a migration while the old root is in use")
	}
	if _, err := os.Stat(newRoot); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the new root exists although the old app is running")
	}
	if after := snapshotTree(t, oldRoot); !sameTree(before, after) {
		t.Errorf("the old root changed while it was locked")
	}
	// A second run after the holder is gone migrates normally.
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	holder.Close()
	if migrated, err := migrateDataRoot(oldRoot, newRoot); err != nil || !migrated {
		t.Fatalf("migrateDataRoot after the lock was released = (%v, %v), want (true, nil)", migrated, err)
	}
}

func TestMigrateDataRootFailureLeavesBothRootsAlone(t *testing.T) {
	base := t.TempDir()
	oldRoot := filepath.Join(base, "Shelf")
	fixtureRoot(t, oldRoot)
	before := snapshotTree(t, oldRoot)
	// A read-only parent makes the temporary directory uncreatable, which
	// is the first step of the migration.
	parent := filepath.Join(base, "readonly")
	if err := os.MkdirAll(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	newRoot := filepath.Join(parent, "ShelfU")

	migrated, err := migrateDataRoot(oldRoot, newRoot)
	if err == nil || migrated {
		t.Fatalf("migrateDataRoot = (%v, %v), want a failure", migrated, err)
	}
	if _, err := os.Stat(newRoot); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a failed migration left %s behind", newRoot)
	}
	if after := snapshotTree(t, oldRoot); !sameTree(before, after) {
		t.Errorf("a failed migration changed the old root")
	}
	entries, rerr := os.ReadDir(parent)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Errorf("a failed migration left a temporary directory: %v", entries)
	}
}

// readJSON parses a data file into generic values, keeping numbers as-is.
func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, ok := decodeJSON(data)
	if !ok {
		t.Fatalf("%s is not JSON: %q", path, data)
	}
	m, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("%s is not a JSON object: %q", path, data)
	}
	return m
}

func itemField(v any, key string) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func sameTree(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// mygo runs the app binary in client-generation mode during every build.
// That run must not migrate, create or rename anything: an empty ShelfU
// root made by a build would block the migration when the user launches
// the app for real.
func TestGeneratingClientMode(t *testing.T) {
	t.Setenv("MYGO_GENERATE", "")
	if generatingClient() {
		t.Error("an absent MYGO_GENERATE reported client-generation mode")
	}
	t.Setenv("MYGO_GENERATE", filepath.Join(t.TempDir(), "mygo.ts"))
	if !generatingClient() {
		t.Error("MYGO_GENERATE did not report client-generation mode")
	}
}
