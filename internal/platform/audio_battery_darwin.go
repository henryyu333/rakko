//go:build darwin

package platform

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// BatteryLevel is one charge percentage a headset reports.
type BatteryLevel struct {
	Part HeadphonePart
	Pct  int
}

// HeadphoneBatteries asks the system's own Bluetooth report for the
// connected device whose name matches name, and returns the percentages it
// names in 左 / 右 / 盒 order. An over-ear headset often reports one main
// level; that arrives as HeadphoneWhole. A missing device, or a report the
// system will not produce, returns nil.
func HeadphoneBatteries(name string) []BatteryLevel {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/system_profiler", "SPBluetoothDataType", "-json").Output()
	if err != nil {
		return nil
	}
	return parseBluetoothLevels(out, name)
}

// parseBluetoothLevels reads the connected devices out of the report.
func parseBluetoothLevels(out []byte, name string) []BatteryLevel {
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
				return bluetoothLevels(props)
			}
		}
	}
	return nil
}

// sameDevice compares the system audio name with the report's name, which
// can differ in spacing and case.
func sameDevice(a, b string) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.Join(strings.Fields(s), ""))
	}
	return norm(a) == norm(b)
}

func bluetoothLevels(props map[string]any) []BatteryLevel {
	var levels []BatteryLevel
	for _, f := range []struct {
		key  string
		part HeadphonePart
	}{
		{"device_batteryLevelLeft", HeadphoneLeft},
		{"device_batteryLevelRight", HeadphoneRight},
		{"device_batteryLevelCase", HeadphoneCase},
		{"device_batteryLevelMain", HeadphoneWhole},
		{"device_batteryLevel", HeadphoneWhole},
	} {
		if v, ok := props[f.key]; ok {
			if pct, ok := percentOf(v); ok {
				levels = append(levels, BatteryLevel{Part: f.part, Pct: pct})
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
