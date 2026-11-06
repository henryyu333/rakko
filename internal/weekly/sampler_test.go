package weekly

import (
	"errors"
	"testing"
	"time"
)

// fakeMachine is a machine described frame by frame: the test sets the app,
// the idle clock and the session before each Tick.
type fakeMachine struct {
	now     time.Time
	bundle  string
	name    string
	idle    float64
	idleErr error
	session bool
	asleep  bool
}

func (m *fakeMachine) probe() Probe {
	return Probe{
		Front:         func() (string, string) { return m.bundle, m.name },
		IdleSeconds:  func() (float64, error) { return m.idle, m.idleErr },
		SessionActive: func() bool { return m.session },
		DisplayAsleep: func() bool { return m.asleep },
	}
}

func newFake() *fakeMachine {
	return &fakeMachine{
		now:     time.Date(2026, 11, 4, 10, 0, 0, 0, time.Local),
		bundle:  "com.apple.Safari",
		name:    "Safari",
		idle:    5,
		session: true,
	}
}

// run ticks the sampler at one-second steps, letting the caller change the
// machine before each step.
func run(t *testing.T, s *Sampler, m *fakeMachine, n int, before func(i int)) {
	t.Helper()
	for i := range n {
		m.now = m.now.Add(SampleInterval)
		if before != nil {
			before(i)
		}
		s.Tick(m.now, true)
	}
}

func TestSamplerActiveAndResident(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	m := newFake()
	s := NewSampler(st, m.probe())
	s.Tick(m.now, true) // baseline
	run(t, s, m, 10, nil)

	w := st.Week(m.now)
	if !w.Sampled {
		t.Fatal("week must be sampled")
	}
	if got := w.Days[2].Active; got != 10*time.Second {
		t.Fatalf("active = %v, want 10s", got)
	}
	if got := w.Days[2].Resident; got != 10*time.Second {
		t.Fatalf("resident = %v", got)
	}
}

func TestSamplerIdleCountsResidentOnly(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	m := newFake()
	m.idle = 120
	s := NewSampler(st, m.probe())
	s.Tick(m.now, true)
	run(t, s, m, 5, nil)

	w := st.Week(m.now)
	if got := w.Days[2].Active; got != 0 {
		t.Fatalf("idle time counted as active: %v", got)
	}
	if got := w.Days[2].Resident; got != 5*time.Second {
		t.Fatalf("resident = %v, want 5s", got)
	}
}

func TestSamplerFailedIdleQueryNeverCountsActive(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	m := newFake()
	m.idleErr = errors.New("no event source")
	s := NewSampler(st, m.probe())
	s.Tick(m.now, true)
	run(t, s, m, 3, nil)

	w := st.Week(m.now)
	if w.Days[2].Active != 0 {
		t.Fatalf("active = %v, want 0 without a usable idle answer", w.Days[2].Active)
	}
	if w.Days[2].Resident != 3*time.Second {
		t.Fatalf("resident = %v", w.Days[2].Resident)
	}
}

func TestSamplerLockedScreenDropsTheInterval(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	m := newFake()
	s := NewSampler(st, m.probe())
	s.Tick(m.now, true)
	run(t, s, m, 3, nil) // 3 seconds counted
	// Lock at the fourth second: that interval is unknown and dropped.
	run(t, s, m, 1, func(int) { m.session = false })
	// Stay locked for three more seconds: nothing counts, the baseline is
	// reset each time.
	run(t, s, m, 3, nil)
	// Unlock: the interval that straddles the unlock is dropped, then
	// counting resumes.
	m.session = true
	run(t, s, m, 1, nil)
	run(t, s, m, 2, nil)

	w := st.Week(m.now)
	if got := w.Days[2].Resident; got != 5*time.Second {
		t.Fatalf("resident = %v, want 3 locked-out seconds plus 2 after unlock", got)
	}
}

func TestSamplerSleepGapIsNotMeasuredTime(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	m := newFake()
	s := NewSampler(st, m.probe())
	s.Tick(m.now, true)
	// The Mac slept for an hour; the process resumes with one tick.
	m.now = m.now.Add(time.Hour)
	s.Tick(m.now, true)
	if w := st.Week(m.now); w.Sampled {
		t.Fatal("a sleep gap must not be attributed to the front app")
	}
	run(t, s, m, 2, nil)
	if got := st.Week(m.now).Days[2].Resident; got != 2*time.Second {
		t.Fatalf("resident after resume = %v, want 2s", got)
	}
}

func TestSamplerDisabledCountsNothing(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	m := newFake()
	s := NewSampler(st, m.probe())
	s.Tick(m.now, true)
	m.now = m.now.Add(SampleInterval)
	s.Tick(m.now, false)
	if w := st.Week(m.now); w.Sampled {
		t.Fatal("a disabled sampler must record nothing")
	}
	// Re-enabling starts a new baseline: the gap while off is never
	// backfilled.
	m.now = m.now.Add(30 * time.Second)
	s.Tick(m.now, true)
	run(t, s, m, 2, nil)
	if got := st.Week(m.now).Days[2].Resident; got != 2*time.Second {
		t.Fatalf("resident after re-enable = %v, want 2s", got)
	}
}

func TestSamplerSwitchesApps(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	m := newFake()
	s := NewSampler(st, m.probe())
	s.Tick(m.now, true)
	run(t, s, m, 3, nil) // Safari
	m.bundle, m.name = "com.apple.dt.Xcode", "Xcode"
	run(t, s, m, 4, nil) // Xcode

	w := st.Week(m.now)
	byID := map[string]AppReport{}
	for _, a := range w.Apps {
		byID[a.BundleID] = a
	}
	if got := byID["com.apple.Safari"].Resident; got != 4*time.Second {
		t.Fatalf("Safari = %v, want 4s (the switch second belongs to the app observed)", got)
	}
	if got := byID["com.apple.dt.Xcode"].Resident; got != 3*time.Second {
		t.Fatalf("Xcode = %v, want 3s", got)
	}
}

func TestSamplerSkipsRakkoItself(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	m := newFake()
	m.bundle, m.name = "local.y3.rakko", "Rakko"
	s := NewSampler(st, m.probe())
	s.Tick(m.now, true)
	run(t, s, m, 5, nil)
	if w := st.Week(m.now); w.Sampled {
		t.Fatalf("Rakko's own time must not count as Mac use: %+v", w.Apps)
	}
}

func TestSamplerClockJumpBackwardsDropsTheInterval(t *testing.T) {
	st := &Store{days: map[string]*Day{}}
	m := newFake()
	s := NewSampler(st, m.probe())
	s.Tick(m.now, true)
	m.now = m.now.Add(-5 * time.Second)
	s.Tick(m.now, true)
	if w := st.Week(m.now); w.Sampled {
		t.Fatal("a backwards clock must not create measured time")
	}
}
