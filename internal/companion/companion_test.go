package companion

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDayCountsCalendarDays pins the count to local dates rather than
// elapsed hours, including both daylight-saving changes.
func TestDayCountsCalendarDays(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		start time.Time
		now   time.Time
		want  int
	}{
		{"first day", time.Date(2026, 11, 13, 0, 0, 0, 0, ny), time.Date(2026, 11, 13, 23, 59, 0, 0, ny), 1},
		{"next day", time.Date(2026, 11, 13, 23, 59, 0, 0, ny), time.Date(2026, 11, 14, 0, 1, 0, 0, ny), 2},
		{"thirtieth", time.Date(2026, 10, 15, 8, 0, 0, 0, ny), time.Date(2026, 11, 13, 8, 0, 0, 0, ny), 30},
		{"spring forward", time.Date(2026, 3, 7, 23, 30, 0, 0, ny), time.Date(2026, 3, 9, 0, 10, 0, 0, ny), 3},
		{"fall back", time.Date(2026, 10, 31, 23, 50, 0, 0, ny), time.Date(2026, 11, 2, 0, 10, 0, 0, ny), 3},
	}
	for _, c := range cases {
		if got := Day(c.start, c.now); got != c.want {
			t.Errorf("%s: day %d, want %d", c.name, got, c.want)
		}
	}
	// The same local dates count as one day even when the two instants are
	// far apart: the count follows the calendar, not 24-hour spans.
	east := time.FixedZone("east", 14*60*60)
	west := time.FixedZone("west", -11*60*60)
	if got := Day(time.Date(2026, 6, 1, 0, 30, 0, 0, east), time.Date(2026, 6, 1, 23, 30, 0, 0, west)); got != 1 {
		t.Fatalf("one local date counted %d days; want 1", got)
	}
}

// TestMilestoneWindows pins the seven-day windows the home line appears in,
// on both sides of every boundary.
func TestMilestoneWindows(t *testing.T) {
	cases := []struct {
		day  int
		want int // 0 when the line is hidden
	}{
		{1, 0}, {29, 0}, {30, 30}, {36, 30}, {37, 0},
		{99, 0}, {100, 100}, {106, 100}, {107, 0},
		{364, 0}, {365, 365}, {371, 365}, {372, 0},
		{729, 0}, {730, 730}, {736, 730}, {737, 0},
		{1094, 0}, {1095, 1095}, {1101, 1095}, {1102, 0},
	}
	for _, c := range cases {
		got, ok := Milestone(c.day)
		if c.want == 0 {
			if ok {
				t.Errorf("day %d: shown as %d, want hidden", c.day, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("day %d: milestone %d, shown %v; want %d", c.day, got, ok, c.want)
		}
	}
}

// TestStoreSeedsAndPersists covers the file's whole life: missing, seeded,
// clicked, and read back by a later launch.
func TestStoreSeedsAndPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Seeded() || s.Clicked() {
		t.Fatal("a missing file must start unseeded and unclicked")
	}
	if _, ok := s.Day(time.Now()); ok {
		t.Fatal("an unseeded store has no day")
	}
	start := time.Date(2026, 2, 1, 15, 0, 0, 0, time.Local)
	if err := s.Seed(start); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("file mode %v, want 0600", mode)
	}
	if err := s.MarkClicked(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Seeded() || !again.Clicked() {
		t.Fatalf("reopen lost state: seeded %v clicked %v", again.Seeded(), again.Clicked())
	}
	if day, ok := again.Day(time.Date(2026, 3, 2, 9, 0, 0, 0, time.Local)); !ok || day != 30 {
		t.Fatalf("reopen day %d, ok %v; want 30", day, ok)
	}
	// Seeding a started store keeps the first day.
	if err := again.Seed(time.Now()); err != nil {
		t.Fatal(err)
	}
	if day, _ := again.Day(time.Date(2026, 3, 2, 9, 0, 0, 0, time.Local)); day != 30 {
		t.Fatalf("seed replaced the first day: now day %d", day)
	}
}

// TestStoreCorruptKeepsTheFile says an unreadable file is reported, shows
// nothing, and is never overwritten by a later write.
func TestStoreCorruptKeepsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	original := []byte("{ this is not json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("open error %v, want ErrCorrupt", err)
	}
	if !s.Corrupt() {
		t.Fatal("store does not report corruption")
	}
	if _, ok := s.Day(time.Now()); ok {
		t.Fatal("a corrupt store must not show a day")
	}
	if err := s.Seed(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkClicked(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("corrupt file was rewritten: %q", after)
	}
}

// TestStoreNewerVersionIsCorrupt guards a file written by a later Rakko.
func TestStoreNewerVersionIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	data := `{"version": 9, "start": "2026-01-01"}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("open error %v, want ErrCorrupt", err)
	}
}

// TestStoreClickWriteFailure keeps the ask when the click cannot be
// written, and lets the next click succeed once the folder is writable.
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
func TestStoreRequiresVersionAndStart(t *testing.T) {
	payloads := []string{
		`null`,
		`{}`,
		`{"version": 1}`,
		`{"start": "2026-01-01"}`,
		`{"version": 0, "start": "2026-01-01"}`,
	}
	for _, payload := range payloads {
		dir := t.TempDir()
		path := filepath.Join(dir, FileName)
		original := []byte(payload)
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(dir)
		if !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: open error %v, want ErrCorrupt", payload, err)
			continue
		}
		if !s.Corrupt() {
			t.Errorf("%s: the store does not report corruption", payload)
		}
		if _, ok := s.Day(time.Now()); ok {
			t.Errorf("%s: a corrupt store must not show a day", payload)
		}
		if err := s.Seed(time.Now()); err != nil {
			t.Errorf("%s: seed returned %v", payload, err)
		}
		if err := s.MarkClicked(); err != nil {
			t.Errorf("%s: click returned %v", payload, err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(original) {
			t.Errorf("%s: file was rewritten to %q", payload, after)
		}
	}
}

// TestStoreSeedWriteFailure keeps no first day when the file cannot be
// written, and lets a later seed write it once the folder is writable.
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

// TestStoreMemoryOnly covers the empty data directory the headless
// fixtures use: state works, nothing is written.
func TestStoreMemoryOnly(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkClicked(); err != nil {
		t.Fatal(err)
	}
	if !s.Seeded() || !s.Clicked() {
		t.Fatal("memory-only store lost its state")
	}
}

// TestDocumentShape keeps the file small and stable: a version, the start
// date and the one flag.
func TestDocumentShape(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(time.Date(2026, 11, 13, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"version": 1`, `"start": "2026-11-13"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("file lacks %s: %s", want, text)
		}
	}
	if strings.Contains(text, "clicked") {
		t.Fatalf("unclicked file should not carry the flag: %s", text)
	}
}
