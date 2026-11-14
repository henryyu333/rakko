//go:build windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// openWithoutShareDelete opens path the way an editor or a scanner may:
// read access only, and no FILE_SHARE_DELETE, so a rename over it is
// refused while the handle lives.
func openWithoutShareDelete(t *testing.T, path string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	return h
}

// A target another process holds open without FILE_SHARE_DELETE cannot be
// replaced: the save reports the error and the old file stays whole.
func TestWriteFileFailsSafelyWhileTargetIsOpen(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := openWithoutShareDelete(t, target)
	if err := WriteFile(dir, "state.json", []byte("new"), 0o600); err == nil {
		windows.CloseHandle(h)
		t.Fatal("replacing a target held without FILE_SHARE_DELETE reported success")
	}
	windows.CloseHandle(h)

	got, err := os.ReadFile(target)
	if err != nil || string(got) != "old" {
		t.Fatalf("old file damaged by a failed save: %q, %v", got, err)
	}
	assertNoTempFiles(t, dir)

	// Once the handle is gone the same save succeeds.
	if err := WriteFile(dir, "state.json", []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile after close: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "new" {
		t.Fatalf("target after successful save = %q, %v", got, err)
	}
}

// A target held open for a moment is replaced once the holder lets go:
// the save waits through the sharing violation instead of failing.
func TestWriteFileRetriesWhileTargetIsBrieflyOpen(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := openWithoutShareDelete(t, target)
	go func() {
		time.Sleep(100 * time.Millisecond)
		windows.CloseHandle(h)
	}()
	if err := WriteFile(dir, "state.json", []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile while the holder lets go: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "new" {
		t.Fatalf("target = %q, %v", got, err)
	}
	assertNoTempFiles(t, dir)
}
