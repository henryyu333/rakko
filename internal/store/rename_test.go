package store

import (
	"os"
	"path/filepath"
	"testing"
)

// UpdatePaths rewrites the kept file references after the user renamed
// the real files: paths change on disk and in memory, order and pinning
// stay, and the new state survives a reopen.
func TestUpdatePathsRenamesAndPersists(t *testing.T) {
	s, _ := newStore(t)
	dir := t.TempDir()
	one := filepath.Join(dir, "one.txt")
	two := filepath.Join(dir, "two.txt")
	for _, p := range []string{one, two} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddFiles([]string{two, one}); err != nil { // newest first: one, two
		t.Fatal(err)
	}
	if err := s.SetPinned(s.Items[1].ID, true); err != nil { // pin "two"
		t.Fatal(err)
	}
	newOne := filepath.Join(dir, "一.txt")
	newTwo := filepath.Join(dir, "二.txt")
	n, err := s.UpdatePaths(map[string]string{
		s.Items[0].ID: newOne,
		s.Items[1].ID: newTwo,
	})
	if err != nil || n != 2 {
		t.Fatalf("UpdatePaths = %d, %v; want 2", n, err)
	}
	if s.Items[0].Path != newOne || s.Items[1].Path != newTwo {
		t.Fatalf("paths = %q, %q", s.Items[0].Path, s.Items[1].Path)
	}
	if !s.Items[1].Pinned {
		t.Fatal("pin lost on rename")
	}
	// Reopen with the renamed names on disk: state.json must hold them.
	for _, p := range []string{newOne, newTwo} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.Items[0].Path != newOne || s2.Items[1].Path != newTwo {
		t.Fatalf("persisted paths = %q, %q", s2.Items[0].Path, s2.Items[1].Path)
	}
}

// Unknown ids, text items and managed files are left alone; identical
// paths are a no-op and write nothing.
func TestUpdatePathsSkipsNonFiles(t *testing.T) {
	s, _ := newStore(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{f}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("hello"); err != nil {
		t.Fatal(err)
	}
	fileID, textID := s.Items[1].ID, s.Items[0].ID
	n, err := s.UpdatePaths(map[string]string{
		fileID:   f, // unchanged path
		textID:   filepath.Join(dir, "other.txt"),
		"ghost":  filepath.Join(dir, "ghost.txt"),
		"ghost2": filepath.Join(dir, "ghost2.txt"),
	})
	if err != nil || n != 0 {
		t.Fatalf("UpdatePaths = %d, %v; want 0", n, err)
	}
	if s.Items[0].Text != "hello" || s.Items[1].Path != f {
		t.Fatalf("non-file or unchanged rows were touched: %+v", s.Items)
	}
}
