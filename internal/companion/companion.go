// Package companion keeps the one small fact Rakko's support line needs: the
// local calendar day the app was first started, and whether the user has
// opened the support page from inside the app.
//
// It is a file of its own (companion.json) so the formats of state.json,
// settings.json and layout.json stay exactly as they were. Nothing else is
// recorded: no account, no payment, no use of the page.
package companion

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the companion file inside the data directory.
const FileName = "companion.json"

// ErrCorrupt says the file exists but could not be decoded. The store keeps
// the file exactly as it found it and refuses every write: the user may
// still be able to repair it.
var ErrCorrupt = errors.New("companion: state file is not valid")

// DateLayout is the calendar date the file stores, "2006-01-02".
const DateLayout = "2006-01-02"

const formatVersion = 1

// document is the on-disk file.
type document struct {
	Version int    `json:"version"`
	Start   string `json:"start"`
	Clicked bool   `json:"clicked,omitempty"`
}

// Store is the companion state for one data directory. It is safe for
// concurrent use.
type Store struct {
	mu      sync.Mutex
	dir     string
	start   string // a local calendar date, DateLayout
	clicked bool
	corrupt bool
	saveErr error
}

// Open loads dir/companion.json. A missing file starts an unseeded store
// (Seeded reports false) so the caller can pick the first day; a file that
// cannot be decoded, or that does not name both a known version and a
// readable first day, returns a store that refuses every write together
// with ErrCorrupt. dir == "" keeps the state in memory only.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir}
	if dir == "" {
		return s, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		s.corrupt = true
		return s, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	var d document
	if err := json.Unmarshal(data, &d); err != nil {
		s.corrupt = true
		return s, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if d.Version < 1 || d.Version > formatVersion {
		s.corrupt = true
		return s, fmt.Errorf("%w: version %d is not 1..%d", ErrCorrupt, d.Version, formatVersion)
	}
	if _, err := time.Parse(DateLayout, d.Start); err != nil {
		s.corrupt = true
		return s, fmt.Errorf("%w: start %q", ErrCorrupt, d.Start)
	}
	s.start = d.Start
	s.clicked = d.Clicked
	return s, nil
}

// Corrupt reports whether the file was unreadable and is protected from
// being overwritten.
func (s *Store) Corrupt() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrupt
}

// Seeded reports whether a first day is known.
func (s *Store) Seeded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.start != ""
}

// Clicked reports whether the user has opened the support page from inside
// the app.
func (s *Store) Clicked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clicked
}

// SaveErr is the last failed save, nil after a successful one.
func (s *Store) SaveErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveErr
}

// Day is which day of companionship now is, the first day counting as 1.
// It reports false when the start is unknown or the file is unreadable.
func (s *Store) Day(now time.Time) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.corrupt || s.start == "" {
		return 0, false
	}
	start, err := time.Parse(DateLayout, s.start)
	if err != nil {
		return 0, false
	}
	return Day(start, now), true
}

// Seed stores the first day, unless a day is already known (or the file is
// unreadable). The date is start's local calendar date. A save that fails
// keeps no first day, so the caller sees the same answer as the disk.
func (s *Store) Seed(start time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.corrupt || s.start != "" || start.IsZero() {
		return nil
	}
	s.start = start.Format(DateLayout)
	if err := s.save(); err != nil {
		s.start = ""
		return err
	}
	return nil
}

// MarkClicked records that the support page was opened from inside the app.
// The flag only ever goes from false to true.
func (s *Store) MarkClicked() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.corrupt || s.clicked {
		return nil
	}
	s.clicked = true
	if err := s.save(); err != nil {
		// A click that did not reach the disk must keep the ask: the next
		// click tries again instead of the app forgetting for good.
		s.clicked = false
		return err
	}
	return nil
}

// save writes the current state atomically. Callers hold mu.
func (s *Store) save() error {
	if s.dir == "" {
		return nil
	}
	data, err := json.MarshalIndent(document{
		Version: formatVersion,
		Start:   s.start,
		Clicked: s.clicked,
	}, "", "  ")
	if err != nil {
		s.saveErr = err
		return err
	}
	if err := writeAtomic(s.dir, append(data, '\n')); err != nil {
		s.saveErr = err
		return err
	}
	s.saveErr = nil
	return nil
}

// writeAtomic replaces companion.json in one rename, so a crash leaves
// either the old file or the new one, never half of either.
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

// Day counts calendar days from start to now, the start day itself being
// day 1. Only dates matter: 23:59 to 00:01 is two days, and a daylight
// saving change cannot make a day longer, shorter or repeated. A clock set
// before the start still reads as day 1.
func Day(start, now time.Time) int {
	d := int(dateOnly(now).Sub(dateOnly(start)) / (24 * time.Hour))
	if d < 0 {
		d = 0
	}
	return d + 1
}

// dateOnly is the calendar date of t at noon UTC: noon so no zone's
// daylight change can land on a nonexistent time, UTC so date arithmetic
// is exact days.
func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

// Milestone reports the anniversary whose seven-day window contains day:
// 30, 100, and every full year from 365 (730, 1095, …). The home line
// shows on the milestone and the six days after it, then goes away.
func Milestone(day int) (int, bool) {
	for _, m := range []int{30, 100} {
		if day >= m && day <= m+6 {
			return m, true
		}
	}
	if day < 365 {
		return 0, false
	}
	m := day / 365 * 365
	if day <= m+6 {
		return m, true
	}
	return 0, false
}
