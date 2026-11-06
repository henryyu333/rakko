// Package weekly keeps the 每周简报's local history: per-app foreground time
// in two measures (active and resident), and the Rakko completions that
// happened each day.
//
// Everything stays on this Mac. An app day holds only the bundle
// identifier, the display name and accumulated milliseconds — never window
// titles, URLs, file names or input content. Days with no samples stay
// absent: an hour Rakko did not run and measure is unknown, not zero.
package weekly

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileName is the report history file inside the data directory.
const FileName = "weekly.json"

// ErrCorrupt says the file exists but could not be decoded. The store keeps
// the file exactly as it found it and refuses to overwrite it: the original
// data is what the user may still be able to recover.
var ErrCorrupt = errors.New("weekly: history file is not valid")

// Metric names one of the two durations every sample carries.
type Metric int

const (
	// MetricActive counts a foreground app only while the user was not
	// idle: pointer or keyboard input within IdleLimit.
	MetricActive Metric = iota
	// MetricResident counts a foreground app regardless of idle, as long
	// as the screen was awake and the session unlocked.
	MetricResident
)

// EventKind is one kind of Rakko completion counted for the report.
type EventKind string

const (
	// EventConvert is one finished conversion job with at least one
	// successful output.
	EventConvert EventKind = "convert"
	// EventScreenText is one completed 框选文字 capture with text.
	EventScreenText EventKind = "ocr"
	// EventTrayTake is one drag that took a tray item out.
	EventTrayTake EventKind = "tray"
)

// AppTime is one app's accumulated time in one local day. Name is the
// display name observed last; ActiveMs and ResidentMs are wall-clock
// milliseconds.
type AppTime struct {
	Name       string `json:"name,omitempty"`
	ActiveMs   int64  `json:"activeMs,omitempty"`
	ResidentMs int64  `json:"residentMs,omitempty"`
}

// RakkoCounts are one day's completed Rakko actions.
type RakkoCounts struct {
	Convert int `json:"convert,omitempty"`
	OCR     int `json:"ocr,omitempty"`
	Tray    int `json:"tray,omitempty"`
}

// Day is one local calendar day. Apps is empty on a day with no samples.
type Day struct {
	Apps  map[string]AppTime `json:"apps,omitempty"`
	Rakko RakkoCounts        `json:"rakko"`
}

// history is the on-disk document. Version guards a future format.
type history struct {
	Version int             `json:"version"`
	Days    map[string]*Day `json:"days"`
}

const formatVersion = 1

// Store is the report history for one data directory. It is safe for
// concurrent use.
type Store struct {
	mu        sync.Mutex
	dir       string
	days      map[string]*Day
	corrupt   bool
	saveErr   error
	dirty     bool
	lastWrite time.Time
}

// Open loads dir/weekly.json. A missing file starts an empty history. A file
// that cannot be decoded returns a usable (in-memory) store together with
// ErrCorrupt; that store refuses every save so the unreadable original
// survives. dir == "" keeps the history in memory only.
func Open(dir string) (*Store, error) {
	st := &Store{dir: dir, days: map[string]*Day{}}
	if dir == "" {
		return st, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		st.corrupt = true
		return st, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	var h history
	if err := json.Unmarshal(data, &h); err != nil {
		st.corrupt = true
		return st, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if h.Version > formatVersion {
		st.corrupt = true
		return st, fmt.Errorf("%w: version %d is newer than %d", ErrCorrupt, h.Version, formatVersion)
	}
	if h.Days != nil {
		st.days = h.Days
	}
	for k, d := range st.days {
		if d == nil {
			st.days[k] = &Day{}
		}
	}
	return st, nil
}

// Corrupt reports whether the loaded file was unreadable and is protected
// from being overwritten.
func (s *Store) Corrupt() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrupt
}

// SaveErr is the last failed save, nil after a successful one.
func (s *Store) SaveErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveErr
}

// Dirty reports whether there is history not yet written.
func (s *Store) Dirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirty
}

// day returns the day entry for t's local calendar date, creating it.
// Callers hold mu.
func (s *Store) day(t time.Time) *Day {
	key := dayKey(t)
	d := s.days[key]
	if d == nil {
		d = &Day{}
		s.days[key] = d
	}
	return d
}

// AddApp adds one sample to the app's time on t's local day. A non-empty
// name replaces the stored one; non-positive durations are ignored. The
// history becomes dirty; callers flush on their own rhythm.
func (s *Store) AddApp(at time.Time, bundleID, name string, active, resident time.Duration) {
	if bundleID == "" || (active <= 0 && resident <= 0) {
		return
	}
	if active < 0 {
		active = 0
	}
	if resident < active {
		resident = active
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.day(at)
	if d.Apps == nil {
		d.Apps = map[string]AppTime{}
	}
	a := d.Apps[bundleID]
	if name != "" {
		a.Name = name
	}
	a.ActiveMs += active.Milliseconds()
	a.ResidentMs += resident.Milliseconds()
	d.Apps[bundleID] = a
	s.dirty = true
}

// AddEvent counts one completed Rakko action on t's local day. Callers only
// report a success: a failed conversion, a capture with no text or a drag
// that carried nothing must not reach here.
func (s *Store) AddEvent(kind EventKind, at time.Time) {
	switch kind {
	case EventConvert, EventScreenText, EventTrayTake:
	default:
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.day(at)
	switch kind {
	case EventConvert:
		d.Rakko.Convert++
	case EventScreenText:
		d.Rakko.OCR++
	case EventTrayTake:
		d.Rakko.Tray++
	}
	s.dirty = true
}

// Flush writes the history atomically. A corrupt source file is never
// overwritten (ErrCorrupt); any other failure keeps the in-memory data, so
// a later attempt can still succeed, and reports the error.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.corrupt {
		return ErrCorrupt
	}
	if !s.dirty || s.dir == "" {
		return nil
	}
	h := history{Version: formatVersion, Days: s.days}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		s.saveErr = err
		return err
	}
	data = append(data, '\n')
	if err := writeAtomic(s.dir, data); err != nil {
		s.saveErr = err
		return err
	}
	s.dirty = false
	s.saveErr = nil
	s.lastWrite = time.Now()
	return nil
}

// MaybeFlush writes a dirty history at most once per interval. It reports
// the save's error so the page can say the data is not on disk yet.
func (s *Store) MaybeFlush(now time.Time, interval time.Duration) error {
	s.mu.Lock()
	due := s.dirty && !s.corrupt && s.dir != ""
	if due && interval > 0 && !s.lastWrite.IsZero() && now.Sub(s.lastWrite) < interval {
		due = false
	}
	if due {
		s.lastWrite = now
	}
	s.mu.Unlock()
	if !due {
		return nil
	}
	return s.Flush()
}

// writeAtomic replaces dir/FileName with data through a temporary file in
// the same directory.
func writeAtomic(dir string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "."+FileName+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(dir, FileName)); err != nil {
		return err
	}
	// The rename itself must reach the disk too, or a crash can leave the
	// old file — acceptable — or, on some filesystems, neither name.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// dayKey is the local calendar date of t, the history's day identity.
func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// DayReport is one day's totals for both measures.
type DayReport struct {
	Date     time.Time
	Sampled  bool
	Active   time.Duration
	Resident time.Duration
}

// AppReport is one app's totals over a week.
type AppReport struct {
	BundleID string
	Name     string
	Active   time.Duration
	Resident time.Duration
}

// Week is a Monday-to-Monday report. Sampled is false when no app sample
// landed in the span; the page must say so instead of drawing zero hours.
type Week struct {
	Start   time.Time
	Sampled bool
	Days    [7]DayReport
	Apps    []AppReport
	Rakko   RakkoCounts
}

// WeekStart is the local Monday 00:00 of t's week. AddDate keeps the
// boundary at local midnight across DST changes.
func WeekStart(t time.Time) time.Time {
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	wd := int(midnight.Weekday()) // Sunday = 0
	if wd == 0 {
		wd = 7
	}
	return midnight.AddDate(0, 0, -(wd - 1))
}

// WeekEnd is the local Monday after start.
func WeekEnd(start time.Time) time.Time { return start.AddDate(0, 0, 7) }

// Week aggregates the history inside [start, start+7 days) in start's
// location. Days with no samples remain Sampled=false; apps are sorted by
// the larger of the two measures, then by name.
func (s *Store) Week(start time.Time) Week {
	start = WeekStart(start)
	w := Week{Start: start}
	apps := map[string]*AppReport{}
	s.mu.Lock()
	for i := range 7 {
		date := start.AddDate(0, 0, i)
		w.Days[i] = DayReport{Date: date}
		d := s.days[dayKey(date)]
		if d == nil {
			continue
		}
		w.Rakko.Convert += max(0, d.Rakko.Convert)
		w.Rakko.OCR += max(0, d.Rakko.OCR)
		w.Rakko.Tray += max(0, d.Rakko.Tray)
		if len(d.Apps) == 0 {
			continue
		}
		w.Days[i].Sampled = true
		w.Sampled = true
		for id, a := range d.Apps {
			active := time.Duration(max(int64(0), a.ActiveMs)) * time.Millisecond
			resident := time.Duration(max(int64(0), a.ResidentMs)) * time.Millisecond
			if resident < active {
				resident = active
			}
			w.Days[i].Active += active
			w.Days[i].Resident += resident
			e := apps[id]
			if e == nil {
				e = &AppReport{BundleID: id}
				apps[id] = e
			}
			if a.Name != "" {
				e.Name = a.Name
			}
			e.Active += active
			e.Resident += resident
		}
	}
	s.mu.Unlock()
	w.Apps = make([]AppReport, 0, len(apps))
	for _, e := range apps {
		w.Apps = append(w.Apps, *e)
	}
	sort.SliceStable(w.Apps, func(i, j int) bool {
		a, b := w.Apps[i], w.Apps[j]
		ai, bi := max(a.Active, a.Resident), max(b.Active, b.Resident)
		if ai != bi {
			return ai > bi
		}
		return a.Name < b.Name
	})
	return w
}

// Earliest is the first day the history holds, as local midnight, for the
// week picker's oldest step. ok is false on an empty history.
func (s *Store) Earliest() (t time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.days {
		day, err := time.ParseInLocation("2006-01-02", key, time.Local)
		if err != nil {
			continue
		}
		if !ok || day.Before(t) {
			t, ok = day, true
		}
	}
	return t, ok
}

// DayTotal is one day's duration in metric m. An unsampled day still
// answers zero; callers must check DayReport.Sampled before drawing it.
func DayTotal(d DayReport, m Metric) time.Duration {
	if m == MetricResident {
		return d.Resident
	}
	return d.Active
}

// WeekTotal is the sum of the week's daily totals in metric m.
func WeekTotal(w Week, m Metric) time.Duration {
	var total time.Duration
	for _, d := range w.Days {
		total += DayTotal(d, m)
	}
	return total
}

// AppTotal is one app's duration in metric m.
func AppTotal(a AppReport, m Metric) time.Duration {
	if m == MetricResident {
		return a.Resident
	}
	return a.Active
}
