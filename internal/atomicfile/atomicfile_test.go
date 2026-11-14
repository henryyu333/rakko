package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteFile replaces the target with the new bytes and leaves no temporary
// file behind.
func TestWriteFileReplaces(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dir, "state.json", []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new" {
		t.Fatalf("target = %q, %v; want new", got, err)
	}
	assertNoTempFiles(t, dir)
}

// A failed write leaves the target's old bytes and no temporary file.
func TestWriteFileLeavesOldFileBehind(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFile(filepath.Join(dir, "missing-subdir"), "state.json", []byte("new"), 0o600); err == nil {
		t.Fatal("writing into a missing directory reported success")
	}
	assertNoTempFiles(t, dir)
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

// WriteFileNoSync replaces the target the same way, without the sync and
// without changing the temporary file's mode — the write a rebuildable
// cache has always done.
func TestWriteFileNoSyncReplaces(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "lyrics.json")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileNoSync(dir, "lyrics.json", []byte("new")); err != nil {
		t.Fatalf("WriteFileNoSync: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new" {
		t.Fatalf("target = %q, %v; want new", got, err)
	}
	assertNoTempFiles(t, dir)
}

// The file keeps the temporary file's mode, which is what CreateTemp and a
// rename gave it before the shared replace.
func TestWriteFileNoSyncKeepsTheTemporaryFileMode(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFileNoSync(dir, "usage-scan.json", []byte("new")); err != nil {
		t.Fatalf("WriteFileNoSync: %v", err)
	}
	ref, err := os.CreateTemp(dir, ".ref-*")
	if err != nil {
		t.Fatal(err)
	}
	refName := ref.Name()
	if err := ref.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(refName, filepath.Join(dir, "ref.json")); err != nil {
		t.Fatal(err)
	}
	got, err := os.Stat(filepath.Join(dir, "usage-scan.json"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(filepath.Join(dir, "ref.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode().Perm() != want.Mode().Perm() {
		t.Fatalf("mode = %v, want the temporary file's %v", got.Mode(), want.Mode())
	}
}
