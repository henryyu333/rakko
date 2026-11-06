package weekly

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustOpen(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func TestWeekStartIsLocalMonday(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 2026-11-05 is a Thursday.
	thu := time.Date(2026, 11, 5, 15, 30, 0, 0, ny)
	if got := WeekStart(thu); !got.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, ny)) {
		t.Fatalf("WeekStart(Thu) = %v, want Mon Nov 2 00:00", got)
	}
	// Sunday belongs to the week that started six days earlier.
	sun := time.Date(2026, 11, 8, 23, 0, 0, 0, ny)
	if got := WeekStart(sun); !got.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, ny)) {
		t.Fatalf("WeekStart(Sun) = %v", got)
	}
	// The DST week: Sunday 2026-03-08 loses an hour. The boundary is
	// still local midnight, and the days are civil days, not 24-hour
	// slices of the previous day.
	monday := time.Date(2026, 3, 9, 9, 0, 0, 0, ny)
	start := WeekStart(monday)
	if got, want := start.Format(time.RFC3339), "2026-03-09T00:00:00-04:00"; got != want {
		t.Fatalf("DST week start = %s, want %s", got, want)
	}
	if end := WeekEnd(start); !end.Equal(time.Date(2026, 3, 16, 0, 0, 0, 0, ny)) {
		t.Fatalf("WeekEnd = %v", end)
	}
	// A week that *contains* the spring-forward day still has seven days.
	prev := WeekStart(time.Date(2026, 3, 8, 12, 0, 0, 0, ny))
	if prev.Equal(start) {
		t.Fatal("Sunday before the change must be its own week")
	}
	if got := prev.AddDate(0, 0, 7); !got.Equal(start) {
		t.Fatalf("next Monday = %v, want %v", got, start)
	}
}

func TestWeekAggregationKeepsUnsampledDaysUnknown(t *testing.T) {
	ny := time.FixedZone("EST", -5*3600)
	start := time.Date(2026, 11, 2, 0, 0, 0, 0, ny)
	st := &Store{days: map[string]*Day{}, dir: ""}
	st.AddApp(start.Add(9*time.Hour), "com.apple.Safari", "Safari", 30*time.Minute, 45*time.Minute)
	st.AddApp(start.Add(2*24*time.Hour+10*time.Hour), "com.apple.Safari", "Safari", 10*time.Minute, 20*time.Minute)
	st.AddApp(start.Add(2*24*time.Hour+11*time.Hour), "com.apple.dt.Xcode", "Xcode", 5*time.Minute, 15*time.Minute)
	st.AddEvent(EventConvert, start.Add(3*time.Hour))
	st.AddEvent(EventTrayTake, start.Add(3*time.Hour))
	st.AddEvent(EventScreenText, start.Add(4*24*time.Hour))

	w := st.Week(start)
	if !w.Sampled {
		t.Fatal("week must be sampled")
	}
	if !w.Days[0].Sampled || w.Days[1].Sampled || !w.Days[2].Sampled {
		t.Fatalf("sampled days = %v %v %v, want true false true", w.Days[0].Sampled, w.Days[1].Sampled, w.Days[2].Sampled)
	}
	if w.Days[1].Active != 0 || w.Days[1].Resident != 0 {
		t.Fatalf("an unsampled day must stay unknown, got %v/%v", w.Days[1].Active, w.Days[1].Resident)
	}
	if got := w.Days[0].Active; got != 30*time.Minute {
		t.Fatalf("Monday active = %v", got)
	}
	if got := w.Days[2].Resident; got != 35*time.Minute {
		t.Fatalf("Wednesday resident = %v", got)
	}
	if w.Rakko.Convert != 1 || w.Rakko.Tray != 1 || w.Rakko.OCR != 1 {
		t.Fatalf("Rakko counts = %+v", w.Rakko)
	}
	if len(w.Apps) != 2 || w.Apps[0].BundleID != "com.apple.Safari" {
		t.Fatalf("apps = %+v", w.Apps)
	}
	if got := w.Apps[0].Active; got != 40*time.Minute {
		t.Fatalf("Safari active total = %v", got)
	}
}

func TestStorePersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st := mustOpen(t, dir)
	day := time.Date(2026, 11, 3, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	st.AddApp(day, "com.apple.Safari", "Safari", 2*time.Minute, 3*time.Minute)
	st.AddEvent(EventConvert, day)
	if err := st.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	st2 := mustOpen(t, dir)
	w := st2.Week(day)
	if !w.Sampled || w.Days[1].Active != 2*time.Minute || w.Rakko.Convert != 1 {
		t.Fatalf("reloaded week = %+v", w)
	}
}

func TestCorruptHistoryIsPreservedAndNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	raw := []byte("{this is not json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open err = %v, want ErrCorrupt", err)
	}
	if !st.Corrupt() {
		t.Fatal("store must report the corrupt source")
	}
	st.AddApp(time.Now(), "com.apple.Safari", "Safari", time.Second, time.Second)
	if err := st.Flush(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Flush err = %v, want ErrCorrupt", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatalf("corrupt file changed: %q", got)
	}
	// The in-memory week still shows what was collected this session.
	if w := st.Week(time.Now()); !w.Sampled {
		t.Fatal("in-memory samples must remain readable")
	}
}

func TestSaveFailureKeepsDataAndRetries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	st := mustOpen(t, dir)
	now := time.Now()
	st.AddApp(now, "com.apple.Safari", "Safari", time.Minute, time.Minute)
	if err := st.Flush(); err == nil {
		t.Fatal("Flush into a missing directory must fail")
	}
	if st.SaveErr() == nil {
		t.Fatal("SaveErr must report the failure")
	}
	if !st.Dirty() {
		t.Fatal("a failed flush must leave the history dirty")
	}
	if w := st.Week(now); !w.Sampled {
		t.Fatal("data must survive a failed flush")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.Flush(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if st.SaveErr() != nil || st.Dirty() {
		t.Fatal("a successful retry clears the error")
	}
}

func TestUnknownEventKindIsIgnored(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	now := time.Now()
	st.AddEvent(EventKind("bogus"), now)
	w := st.Week(now)
	if w.Rakko != (RakkoCounts{}) {
		t.Fatalf("counts = %+v", w.Rakko)
	}
}

func TestTotalsFollowTheSelectedMeasure(t *testing.T) {
	ny := time.FixedZone("EST", -5*3600)
	start := time.Date(2026, 11, 2, 0, 0, 0, 0, ny)
	st := &Store{days: map[string]*Day{}}
	st.AddApp(start.Add(9*time.Hour), "com.apple.Safari", "Safari", 30*time.Minute, 45*time.Minute)
	st.AddApp(start.Add(24*time.Hour+9*time.Hour), "com.apple.Safari", "Safari", 10*time.Minute, 20*time.Minute)
	w := st.Week(start)
	if got := WeekTotal(w, MetricActive); got != 40*time.Minute {
		t.Fatalf("active week = %v", got)
	}
	if got := WeekTotal(w, MetricResident); got != 65*time.Minute {
		t.Fatalf("resident week = %v", got)
	}
	if got := DayTotal(w.Days[0], MetricResident); got != 45*time.Minute {
		t.Fatalf("Monday resident = %v", got)
	}
	if len(w.Apps) != 1 {
		t.Fatalf("apps = %+v", w.Apps)
	}
	if got := AppTotal(w.Apps[0], MetricActive); got != 40*time.Minute {
		t.Fatalf("Safari active = %v", got)
	}
}

func TestEarliestDay(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	if _, ok := st.Earliest(); ok {
		t.Fatal("empty history has no earliest day")
	}
	now := time.Now()
	st.AddEvent(EventConvert, now.AddDate(0, 0, -9))
	st.AddEvent(EventConvert, now.AddDate(0, 0, -2))
	got, ok := st.Earliest()
	if !ok {
		t.Fatal("earliest missing")
	}
	want := time.Date(now.AddDate(0, 0, -9).Year(), now.AddDate(0, 0, -9).Month(), now.AddDate(0, 0, -9).Day(), 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("earliest = %v, want %v", got, want)
	}
}
