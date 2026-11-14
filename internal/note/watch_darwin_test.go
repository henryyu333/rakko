//go:build darwin

package note

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestWatchFileArmFailureClosesEveryFD: a kqueue registration that fails
// must not leak the folder, the stop pipe or the kqueue. The watch it
// would have returned never starts a goroutine, so WatchFile itself has to
// close them; a working kqueue will not fail, so the seam takes that path.
func TestWatchFileArmFailureClosesEveryFD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	old := watchArm
	var got *Watch
	watchArm = func(w *Watch, _ uint64, _ uint32) error {
		got = w
		return errors.New("arm failed")
	}
	t.Cleanup(func() { watchArm = old })
	if _, err := WatchFile(path, func() {}); err == nil {
		t.Fatal("a failed registration must be an error")
	}
	if got == nil {
		t.Fatal("the test never saw the watch")
	}
	if got.dir != nil || got.quit != nil || got.stop != nil || got.kq != -1 {
		t.Fatalf("fds left open: dir %v quit %v stop %v kq %d", got.dir, got.quit, got.stop, got.kq)
	}
	if !got.done {
		t.Fatal("the abandoned watch is not marked closed")
	}
}
