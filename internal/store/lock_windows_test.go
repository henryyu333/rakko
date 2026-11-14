//go:build windows

package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A .lock that is a junction is refused instead of being followed: the
// directory it points at is never truncated, and the store does not open.
func TestLockFileJunctionNotFollowed(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	lock := filepath.Join(root, lockFileName)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", lock, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v: %s", err, out)
	}
	if _, err := Open(root); err == nil || errors.Is(err, ErrLocked) {
		t.Fatalf("Open over a junction .lock = %v, want a symlink refusal", err)
	}
	// The junction and its target are untouched: .lock still resolves to
	// the directory, and nothing was written into it.
	if fi, err := os.Stat(lock); err != nil || !fi.IsDir() {
		t.Fatalf("the .lock junction was disturbed: %v", err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("the junction target was written to: %v, %v", entries, err)
	}
}
