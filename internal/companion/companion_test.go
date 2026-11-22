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
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	requireOwnerOnlyFile(t, path)
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

// TestStoreDayNeverGoesBackwards: the day count only ever grows. A trip
// west across the date line, or a clock set back, makes the calendar
// difference smaller; the day already shown must survive it, in memory
// and on the next launch.
func TestStoreDayNeverGoesBackwards(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 6, 1, 9, 0, 0, 0, time.Local)
	if err := s.Seed(start); err != nil {
		t.Fatal(err)
	}

	east := time.FixedZone("east", 14*60*60)
	west := time.FixedZone("west", -11*60*60)
	// The same instant is July 1 east of the line and June 30 west of it.
	first := time.Date(2026, 7, 1, 12, 0, 0, 0, east)
	if day, ok := s.Day(first); !ok || day != 31 {
		t.Fatalf("day %d, ok %v; want 31", day, ok)
	}
	if day, ok := s.Day(first.In(west)); !ok || day != 31 {
		t.Fatalf("crossing west went back to day %d; want 31", day)
	}

	// A clock set back a day reads the same way.
	back := time.Date(2026, 6, 30, 9, 0, 0, 0, time.Local)
	if day, _ := s.Day(back); day != 31 {
		t.Fatalf("a clock set back went to day %d; want 31", day)
	}

	// The highest day is on disk, so a relaunch cannot step back either.
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if day, ok := again.Day(back); !ok || day != 31 {
		t.Fatalf("relaunch on the old clock reads day %d; want 31", day)
	}

	// With the clock right again the count moves on as it always did.
	if day, _ := again.Day(start.AddDate(0, 0, 40)); day != 41 {
		t.Fatalf("the count did not resume: day %d; want 41", day)
	}
}

// 同一次运行里把时钟拨到未来不会买来陪伴天数：日历多出的天数远超单调
// 时钟实际经过的时间时，停在已确认的那天，不落盘；时钟回来后照常继续。
func TestStoreDayIgnoresFutureClockJump(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 6, 1, 9, 0, 0, 0, time.Local)
	if err := s.Seed(start); err != nil {
		t.Fatal(err)
	}
	orig := elapsedSince
	t.Cleanup(func() { elapsedSince = orig })
	if day, ok := s.Day(start.AddDate(0, 0, 9)); !ok || day != 10 {
		t.Fatalf("day %d, ok %v; want 10", day, ok)
	}

	// A dead RTC battery says 2036 while the monotonic clock says minutes
	// passed: the count stays where the real calendar left it, and the jump
	// is never written to disk.
	elapsedSince = func(from, to time.Time) time.Duration { return 10 * time.Minute }
	huge := start.AddDate(10, 0, 0)
	if day, ok := s.Day(huge); !ok || day != 10 {
		t.Fatalf("future clock read day %d, ok %v; want 10", day, ok)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if day, ok := again.Day(start); !ok || day != 10 {
		t.Fatalf("relaunch after the jump reads day %d, ok %v; want 10", day, ok)
	}

	// The clock comes back for a normal next day: the count moves on.
	if day, ok := s.Day(start.AddDate(0, 0, 10)); !ok || day != 11 {
		t.Fatalf("the count did not resume: day %d", day)
	}
}

// A real absence across launches is believed whole: a new process has no
// earlier monotonic reading to compare with, so the calendar's leap is the
// truth — the same way the first count of a start seeded long ago (an
// existing install's folder birth) is believed whole.
func TestStoreDayBelievesAGapAcrossLaunches(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	away := 409
	start := time.Now().AddDate(0, 0, -(away + 9))
	if err := s.Seed(start); err != nil {
		t.Fatal(err)
	}
	past := time.Now().AddDate(0, 0, -away)
	if day, ok := s.Day(past); !ok || day != 10 {
		t.Fatalf("last day before the absence: day %d, ok %v; want 10", day, ok)
	}
	// The app was closed for `away` days; reopening reads the whole gap.
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if day, ok := again.Day(now); !ok || day != Day(start, now) {
		t.Fatalf("a gap across launches read day %d, ok %v; want %d", day, ok, Day(start, now))
	}

	// The first count of a start seeded long ago is the calendar's, whole.
	fresh := t.TempDir()
	s2, err := Open(fresh)
	if err != nil {
		t.Fatal(err)
	}
	seedStart := now.AddDate(-2, 0, 0)
	if err := s2.Seed(seedStart); err != nil {
		t.Fatal(err)
	}
	if day, ok := s2.Day(now); !ok || day != Day(seedStart, now) {
		t.Fatalf("a start two years ago read day %d, ok %v; want %d", day, ok, Day(seedStart, now))
	}
}

// TestMilestoneWindowDoesNotReturnAfterClockRollback: the home line shows
// in one seven-day window per milestone. Setting the clock back into a
// window that has already passed must not show it for a second week.
func TestMilestoneWindowDoesNotReturnAfterClockRollback(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 6, 1, 9, 0, 0, 0, time.Local)
	if err := s.Seed(start); err != nil {
		t.Fatal(err)
	}
	// Day 37 is the first day after the 30-day window.
	day, ok := s.Day(start.AddDate(0, 0, 36))
	if !ok || day != 37 {
		t.Fatalf("day %d, ok %v; want 37", day, ok)
	}
	if _, show := Milestone(day); show {
		t.Fatalf("day %d showed the 30-day window", day)
	}
	// A week back lands on the window's first day again.
	back := start.AddDate(0, 0, 29)
	day, ok = s.Day(back)
	if !ok || day != 37 {
		t.Fatalf("a week back reads day %d; want 37", day)
	}
	if _, show := Milestone(day); show {
		t.Fatalf("day %d showed the 30-day milestone for a second week", day)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if day, _ := again.Day(back); day != 37 {
		t.Fatalf("relaunch on the old clock reads day %d; want 37", day)
	}
}

// TestStoreFileWithoutDaysCountsFromTheStart: a file written by an older
// Rakko has no highest day, so it reads exactly as before — the calendar
// count — and starts recording one from there.
func TestStoreFileWithoutDaysCountsFromTheStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(`{"version": 1, "start": "2026-06-01"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if day, ok := s.Day(time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)); !ok || day != 7 {
		t.Fatalf("old file reads day %d, ok %v; want 7", day, ok)
	}
	if day, _ := s.Day(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)); day != 8 {
		t.Fatalf("the next day is %d; want 8", day)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if day, _ := again.Day(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)); day != 8 {
		t.Fatalf("after the upgrade a relaunch reads day %d; want 8", day)
	}
}

// 睡眠唤醒后不再拿同一只单调钟比对：合盖期间日历照走而单调钟可能停着，
// 唤醒后日历的天数就是真相，长期不关机的笔记本不会把陪伴天数冻住。
func TestStoreDayBelievesAGapAcrossAWake(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 6, 1, 9, 0, 0, 0, time.Local)
	if err := s.Seed(start); err != nil {
		t.Fatal(err)
	}
	orig := elapsedSince
	t.Cleanup(func() { elapsedSince = orig })
	elapsedSince = func(from, to time.Time) time.Duration { return time.Minute }
	if day, ok := s.Day(start.AddDate(0, 0, 9)); !ok || day != 10 {
		t.Fatalf("day %d, ok %v; want 10", day, ok)
	}
	// Three days with the lid shut: the wake releases the reference and
	// the calendar's leap is believed.
	s.Wake()
	if day, ok := s.Day(start.AddDate(0, 0, 12)); !ok || day != 13 {
		t.Fatalf("the count after a wake read day %d, ok %v; want 13", day, ok)
	}
}

func TestOpenStripsUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"version":1,"start":"2026-11-20","days":3}`)...)
	if err := os.WriteFile(filepath.Join(dir, FileName), body, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open with UTF-8 BOM: %v", err)
	}
	if st.start != "2026-11-20" || st.days != 3 {
		t.Fatalf("companion after BOM open = (%q, %d), want (2026-11-20, 3)", st.start, st.days)
	}
}
