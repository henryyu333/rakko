//go:build darwin

package main

// Headphone notices: a Bluetooth audio device appearing in CoreAudio's
// output list says 已连接 (with battery levels when the system reports
// them), and its removal says 已断开. CoreAudio needs no permission and no
// Bluetooth prompt; the levels come from `system_profiler
// SPBluetoothDataType -json`, which reads the same public report System
// Information shows. The lookup is a subprocess, so it runs off the main
// thread and fills the notice in when it answers.

import (
	"fmt"
	"time"

	"rakko/internal/platform"
)

const audioPollInterval = time.Second

type audioModule struct {
	host *shelf
	// list and battery are seams: tests never touch CoreAudio and never run
	// system_profiler.
	list    func() []platform.AudioOutput
	battery func(name string) []batteryLevel
	// dispatch delivers the battery result to the main thread.
	dispatch func(func())
	now      func() time.Time
	next     time.Time

	seeded bool
	known  map[string]string // UID → name
	order  []string          // UIDs in the order they appeared
	// gen makes each connection's token unique; connGen is the same
	// counter kept per device UID, so one device's answer is judged
	// against its own connection and never against a sibling that
	// appeared in the same poll.
	gen     int
	connGen map[string]int
}

func newAudioModule(a *shelf) *audioModule {
	return &audioModule{
		host: a, list: platform.OutputDevices, battery: func(name string) []batteryLevel { return headphoneLevels(platform.HeadphoneBatteries(name)) },
		dispatch: mygoRunOnMain, now: time.Now, known: map[string]string{},
		connGen: map[string]int{},
	}
}

func (m *audioModule) tick(now time.Time) {
	if now.Before(m.next) {
		return
	}
	m.next = now.Add(audioPollInterval)
	if m.connGen == nil {
		m.connGen = map[string]int{}
	}
	if m.list == nil {
		return
	}
	devs := m.list()
	if devs == nil {
		return // CoreAudio unavailable: never treat that as "all gone"
	}
	cur := make(map[string]string)
	var order []string
	for _, d := range devs {
		if !d.Bluetooth {
			continue
		}
		key := d.UID
		if key == "" {
			key = d.Name
		}
		if _, seen := cur[key]; seen {
			continue
		}
		cur[key] = d.Name
		order = append(order, key)
	}
	if !m.seeded {
		m.seeded = true
		m.known, m.order = cur, order
		return
	}
	// Removals first, in the order the devices had: an AirPods pair that
	// reconnects under a new UID still reads as disconnect then connect.
	for _, key := range m.order {
		if _, ok := cur[key]; ok {
			continue
		}
		name := m.known[key]
		if name == "" {
			continue
		}
		delete(m.connGen, key) // a lookup still in flight can no longer land
		m.host.pushLive(liveEvent{kind: liveHeadsetOff, label: name}, liveHoldUnplug, now)
	}
	for _, key := range order {
		if _, ok := m.known[key]; ok {
			continue
		}
		name := cur[key]
		m.gen++
		gen := m.gen
		m.connGen[key] = gen
		token := fmt.Sprintf("%s#%d", key, gen)
		m.host.pushLive(liveEvent{kind: liveHeadset, label: name, token: token}, liveHoldHeadset, now)
		m.lookupBattery(key, name, token, gen)
	}
	m.known, m.order = cur, order
}

// lookupBattery fills the notice in when system_profiler answers. A late
// answer is judged against that device's own connection generation: two
// devices appearing in one poll each keep their answer, a reconnected
// device invalidates only its own earlier lookup.
func (m *audioModule) lookupBattery(uid, name, token string, gen int) {
	if m.battery == nil {
		return
	}
	list, dispatch := m.battery, m.dispatch
	if dispatch == nil {
		dispatch = func(fn func()) { fn() }
	}
	go func() {
		levels := list(name)
		if len(levels) == 0 {
			return
		}
		dispatch(func() {
			if m.host.quitting || m.connGen[uid] != gen {
				return
			}
			m.host.liveEnrich(token, levels)
		})
	}()
}

// headphoneLevels turns the platform's report into the module's parts.
func headphoneLevels(levels []platform.BatteryLevel) []batteryLevel {
	out := make([]batteryLevel, 0, len(levels))
	for _, l := range levels {
		out = append(out, batteryLevel{part: headphonePartOf(l.Part), pct: l.Pct})
	}
	return out
}

// headphonePartOf maps the platform's part onto the module's.
func headphonePartOf(part platform.HeadphonePart) batteryPart {
	switch part {
	case platform.HeadphoneLeft:
		return batteryLeft
	case platform.HeadphoneRight:
		return batteryRight
	case platform.HeadphoneCase:
		return batteryCase
	}
	return batteryWhole
}

// nextTick is the next audio-device scan.
func (m *audioModule) nextTick(now time.Time) time.Time {
	if m.next.IsZero() {
		return now
	}
	return m.next
}
