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
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"rakko/internal/mac"
)

const audioPollInterval = time.Second

type audioModule struct {
	host *shelf
	// list and battery are seams: tests never touch CoreAudio and never run
	// system_profiler.
	list    func() []mac.AudioOutput
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
		host: a, list: mac.OutputDevices, battery: readBluetoothLevels,
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

// readBluetoothLevels asks system_profiler for the connected devices'
// battery percentages and returns the ones it names, in 左 / 右 / 盒 order.
// An over-ear headset often reports one main level; that is shown as 电量.
func readBluetoothLevels(name string) []batteryLevel {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/system_profiler", "SPBluetoothDataType", "-json").Output()
	if err != nil {
		return nil
	}
	return parseBluetoothLevels(out, name)
}

// parseBluetoothLevels reads the connected devices out of the report.
func parseBluetoothLevels(out []byte, name string) []batteryLevel {
	var doc struct {
		Data []map[string]json.RawMessage `json:"SPBluetoothDataType"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return nil
	}
	for _, section := range doc.Data {
		raw, ok := section["device_connected"]
		if !ok {
			continue
		}
		var devices []map[string]map[string]any
		if json.Unmarshal(raw, &devices) != nil {
			continue
		}
		for _, entry := range devices {
			for devName, props := range entry {
				if !sameDevice(devName, name) {
					continue
				}
				return batteryLevels(props)
			}
		}
	}
	return nil
}

// sameDevice compares the CoreAudio name with the report's name, which can
// differ in spacing and case.
func sameDevice(a, b string) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.Join(strings.Fields(s), ""))
	}
	return norm(a) == norm(b)
}

func batteryLevels(props map[string]any) []batteryLevel {
	var levels []batteryLevel
	for _, f := range []struct {
		key  string
		part batteryPart
	}{
		{"device_batteryLevelLeft", batteryLeft},
		{"device_batteryLevelRight", batteryRight},
		{"device_batteryLevelCase", batteryCase},
		{"device_batteryLevelMain", batteryWhole},
		{"device_batteryLevel", batteryWhole},
	} {
		if v, ok := props[f.key]; ok {
			if pct, ok := percentOf(v); ok {
				levels = append(levels, batteryLevel{part: f.part, pct: pct})
			}
		}
	}
	return levels
}

// percentOf reads "80%", " 80 %" or 80 from a report value.
func percentOf(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case string:
		s := strings.TrimSpace(t)
		i := 0
		for i < len(s) && (s[i] >= '0' && s[i] <= '9') {
			i++
		}
		if i == 0 {
			return 0, false
		}
		n, err := strconv.Atoi(s[:i])
		return n, err == nil
	}
	return 0, false
}

// nextTick is the next audio-device scan.
func (m *audioModule) nextTick(now time.Time) time.Time {
	if m.next.IsZero() {
		return now
	}
	return m.next
}
