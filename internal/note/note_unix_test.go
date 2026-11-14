//go:build !windows

package note

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteReplacesTheFile proves the write is a rename, not a truncate:
// a reader holding the old file keeps the old bytes, and no temporary
// file is left behind.
func TestWriteReplacesTheFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write("old\n"); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := s.Write("new content\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := reader.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "old\n" {
		t.Fatalf("the reader of the old file saw %q, so the write was not a rename", got)
	}
	text, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if text != "new content\n" {
		t.Fatalf("read = %q", text)
	}
	entries, err := os.ReadDir(filepath.Join(dir, DirName))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}
