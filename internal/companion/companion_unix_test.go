//go:build !windows

package companion

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreClickWriteFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := s.MarkClicked(); err == nil {
		t.Skip("this user can write to a read-only folder")
	}
	if s.Clicked() {
		t.Fatal("a failed save must not claim the click")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkClicked(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Clicked() {
		t.Fatal("the retried click was not saved")
	}
}

// TestStoreRequiresVersionAndStart treats a file that exists but lacks
// either its version or a usable first day as damaged, not as a fresh
// install: seeding it would replace whatever it once said.
func TestStoreSeedWriteFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := s.Seed(start); err == nil {
		t.Skip("this user can write to a read-only folder")
	}
	if s.Seeded() {
		t.Fatal("a failed save must not keep the first day")
	}
	if _, ok := s.Day(time.Now()); ok {
		t.Fatal("a failed save must not show a day")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(start); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if day, ok := again.Day(start); !ok || day != 1 {
		t.Fatalf("the retried seed was not saved: day %d, ok %v", day, ok)
	}
}

// TestStoreClickBeforeSeed writes nothing while no first day is known: a
// click after a failed seed must not leave a startless file that the next
// launch would read as damaged. Once the seed lands, the click is saved.
func TestStoreClickBeforeSeed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := s.Seed(start); err == nil {
		t.Skip("this user can write to a read-only folder")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	if err := s.MarkClicked(); err != nil {
		t.Fatalf("a click without a first day returned %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a click without a first day wrote %s (stat error %v)", FileName, err)
	}
	if s.Clicked() {
		t.Fatal("a click without a first day was kept in memory")
	}
	if err := s.Seed(start); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkClicked(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatalf("the file cannot be reopened: %v", err)
	}
	if again.Corrupt() || !again.Seeded() || !again.Clicked() {
		t.Fatalf("reopen corrupt %v seeded %v clicked %v", again.Corrupt(), again.Seeded(), again.Clicked())
	}
	if day, ok := again.Day(start); !ok || day != 1 {
		t.Fatalf("reopen day %d, ok %v; want 1", day, ok)
	}
}

// TestStoreMemoryOnly covers the empty data directory the headless
// fixtures use: state works, nothing is written.
