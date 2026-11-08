//go:build darwin

package main

// Charging, full and low-battery notices. The state comes from IOKit's
// power-source API on the shared 30 ms tick, gated to one dictionary copy
// every two seconds. IOKit's own run-loop source (mac.StartPowerObserver)
// wakes that gate, so a plug-in posts on the next tick instead of at the
// end of the poll interval; the poll stays as the fallback when the
// observer cannot be installed and covers a missed notification. The
// transitions are the events: plugging in, reaching 100% on the adapter,
// and each of the low thresholds once per discharge. A Mac without a
// battery never posts anything.

import (
	"sync/atomic"
	"time"

	"rakko/internal/mac"
)

const powerPollInterval = 2 * time.Second

// powerObserver installs the system's power-source watcher; tests replace
// it so the wake path runs without IOKit.
var powerObserver = mac.StartPowerObserver

type powerModule struct {
	host *shelf
	// probe is the seam: tests feed battery states instead of the machine.
	probe func() (mac.BatteryStatus, bool)
	now   func() time.Time
	next  time.Time
	// wake is set by the system's observer (from the main run loop) and
	// opened by the next tick, so a change probes without waiting for next.
	wake atomic.Bool

	seeded bool
	last   mac.BatteryStatus
	low20  bool
	low10  bool
	full   bool
}

func newPowerModule(a *shelf) *powerModule {
	return &powerModule{host: a, probe: mac.Battery, now: time.Now}
}

// watchPower asks the system to wake the poll on every power-source
// change. reports whether the observer was installed; when it was not, the
// two-second poll is the whole path, exactly as before.
func (m *powerModule) watchPower() bool {
	if powerObserver == nil {
		return false
	}
	src, err := powerObserver(func() {
		m.wake.Store(true)
		m.host.wakeMonitor()
	})
	return err == nil && src != 0
}

func (m *powerModule) tick(now time.Time) {
	// A woken tick skips the gate: the system just said something changed,
	// so the state is read now rather than at the end of the interval.
	if !m.wake.Swap(false) && now.Before(m.next) {
		return
	}
	m.next = now.Add(powerPollInterval)
	if m.probe == nil {
		return
	}
	st, ok := m.probe()
	if !ok {
		m.seeded = false
		return
	}
	if !m.seeded {
		m.adopt(st)
		return
	}
	prev := m.last
	m.last = st
	switch {
	case st.External && !prev.External:
		// Plugged in: a new charge cycle. Right at 100% the words are
		// 已充满 instead of 正在充电.
		m.resetCycle()
		if st.Percent >= 100 {
			m.fullNotice(st, now)
		} else {
			m.chargeNotice(st, now)
		}
	case st.External && st.Percent >= 100 && prev.Percent < 100 && !m.full:
		m.fullNotice(st, now)
	case !st.External:
		if prev.External {
			// A new discharge cycle starts where the battery is now.
			m.low20 = st.Percent <= 20
			m.low10 = st.Percent <= 10
			m.full = false
		}
		if st.Percent <= 20 && !m.low20 {
			m.low20 = true
			m.lowNotice(st, now)
		}
		if st.Percent <= 10 && !m.low10 {
			m.low10 = true
			m.lowNotice(st, now)
		}
	}
}

// adopt records the state the app started on without announcing it: an
// already-full battery or a low battery at launch is not a transition.
func (m *powerModule) adopt(st mac.BatteryStatus) {
	m.seeded = true
	m.last = st
	m.low20 = st.Percent <= 20
	m.low10 = st.Percent <= 10
	m.full = st.External && st.Percent >= 100
}

func (m *powerModule) resetCycle() {
	m.low20, m.low10, m.full = false, false, false
}

func (m *powerModule) chargeNotice(st mac.BatteryStatus, now time.Time) {
	m.host.pushLive(liveEvent{kind: liveCharging, label: m.host.appLoc().LiveCharging(), pct: st.Percent}, liveHoldCharge, now)
}

func (m *powerModule) fullNotice(st mac.BatteryStatus, now time.Time) {
	m.full = true
	m.host.pushLive(liveEvent{kind: liveCharged, label: m.host.appLoc().LiveCharged(), pct: st.Percent}, liveHoldFull, now)
}

func (m *powerModule) lowNotice(st mac.BatteryStatus, now time.Time) {
	m.host.pushLive(liveEvent{kind: liveLow, label: m.host.appLoc().LiveLow(), pct: st.Percent}, liveHoldLow, now)
}

// nextTick is the next battery probe; the IOKit observer wakes it earlier.
func (m *powerModule) nextTick(now time.Time) time.Time {
	if m.next.IsZero() {
		return now
	}
	return m.next
}
