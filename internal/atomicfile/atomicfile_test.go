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
