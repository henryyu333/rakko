package weekly

import (
	"strings"
	"time"
)

// Sampling cadence and the edges the two measures use.
const (
	// SampleInterval is how often the front app is read. The monitor tick
	// runs far faster; this keeps the Objective-C queries at one per
	// second while bounding the attribution error to about a second.
	SampleInterval = time.Second
	// IdleLimit is the inactivity above which a second no longer counts
	// toward MetricActive. Resident time keeps counting.
	IdleLimit = 60 * time.Second
	// MaxGap is the longest interval a sample may attribute. A larger gap
	// means the monitor missed its cadence (sleep, a stall, a clock jump),
	// and the app seen at both ends does not prove what was front during
	// the gap: that time is unknown and dropped rather than guessed.
	MaxGap = 2 * SampleInterval
)

// Probe is everything the sampler asks the system. A test replaces it to
// describe a machine without touching AppKit.
type Probe struct {
	// Front is the frontmost application's bundle identifier and display
	// name, both "" when the system reports none.
	Front func() (bundleID, name string)
	// IdleSeconds is the time since the last hardware input; err says the
	// query failed.
	IdleSeconds func() (float64, error)
	// SessionActive is false while the screen is locked, the user is
	// switched away or no console session exists.
	SessionActive func() bool
	// DisplayAsleep is true while the main display is dark.
	DisplayAsleep func() bool
}

// Sampler turns the probe into the two measures the report shows. It is
// driven by the panel's monitor tick and keeps one sample of state between
// ticks; all methods run on the main thread.
type Sampler struct {
	store *Store
	probe Probe

	// excluded bundle ids never count as Mac use: Rakko itself (its pages
	// are the report's own Rakko section) and the system's login surfaces.
	excluded map[string]bool

	last     time.Time
	lastID   string
	lastName string
	lastOK   bool
}

// NewSampler builds the sampler over store. probe entries left nil answer
// through the caller's defaults; Tick with enabled false never calls them.
func NewSampler(store *Store, probe Probe) *Sampler {
	return &Sampler{
		store: store,
		probe: probe,
		excluded: map[string]bool{
			"local.y3.rakko":                true,
			"com.apple.loginwindow":         true,
			"com.apple.ScreenSaver.Engine":  true,
			"com.apple.SecurityAgent":       true,
			"com.apple.screensaver":         true,
		},
	}
}

// Reset drops the baseline without recording anything: the next Tick
// starts a new measurement. Called when collection stops.
func (s *Sampler) Reset() {
	s.last = time.Time{}
	s.lastID, s.lastName = "", ""
	s.lastOK = false
}

// Tick samples the machine. enabled is the user's explicit consent; while
// it is false nothing is read and the baseline resets, so turning
// collection off never keeps counting. The interval since the previous
// sample is attributed to the app observed then, using the state observed
// now: a second that may have begun before the user went idle or locked
// the screen is dropped instead of counted.
func (s *Sampler) Tick(now time.Time, enabled bool) {
	if !enabled {
		s.Reset()
		return
	}
	if s.probe.SessionActive == nil || s.probe.DisplayAsleep == nil || s.probe.Front == nil || s.probe.IdleSeconds == nil {
		return
	}
	sessionOK := s.probe.SessionActive() && !s.probe.DisplayAsleep()
	id, name := "", ""
	if sessionOK {
		id, name = s.probe.Front()
		id = strings.TrimSpace(id)
		if s.excluded[id] {
			id, name = "", ""
		}
	}
	if s.last.IsZero() {
		s.remember(now, id, name, sessionOK)
		return
	}
	delta := now.Sub(s.last)
	prevAt := s.last
	prevID, prevName, prevOK := s.lastID, s.lastName, s.lastOK
	s.remember(now, id, name, sessionOK)

	if !prevOK || id == "" || !sessionOK {
		// The previous sample or the current one has no usable state:
		// the interval is unknown on at least one end.
		return
	}
	if prevID != id {
		// The front app changed somewhere inside the interval. Polling
		// cannot say when, so neither app gets the time; the new baseline
		// starts from this sample.
		return
	}
	if delta <= 0 || delta > MaxGap {
		return // sleep, a stalled tick or a clock jump: not measured time
	}
	idle, err := s.probe.IdleSeconds()
	active := err == nil && idle >= 0 && idle < IdleLimit.Seconds()
	s.addInterval(prevAt, now, prevID, prevName, delta, active)
}

// addInterval records one measured interval for the app observed at both
// ends. A crossing of local midnight is split at midnight so each day gets
// its own part; the whole interval uses the activity observed at its end.
func (s *Sampler) addInterval(from, to time.Time, id, name string, d time.Duration, active bool) {
	if d <= 0 || id == "" {
		return
	}
	add := func(at time.Time, part time.Duration) {
		if part <= 0 {
			return
		}
		if active {
			s.store.AddApp(at, id, name, part, part)
			return
		}
		// Resident time still counts; active time for this interval is
		// unknown, so it is left out.
		s.store.AddApp(at, id, name, 0, part)
	}
	if dayKey(from) == dayKey(to) {
		add(to, d)
		return
	}
	midnight := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
	before, after := midnight.Sub(from), to.Sub(midnight)
	if before > 0 && after > 0 && before+after == d {
		add(from, before)
		add(to, after)
		return
	}
	// A DST shift at midnight can make the split fail arithmetic; one
	// sample's interval is not enough evidence to invent a boundary.
}

// remember stores the observation the next interval attributes to.
func (s *Sampler) remember(now time.Time, id, name string, ok bool) {
	s.last = now
	s.lastID, s.lastName = id, name
	s.lastOK = ok && id != ""
}
