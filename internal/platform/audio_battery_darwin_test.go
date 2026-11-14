//go:build darwin

package platform

import (
	"fmt"
	"testing"
)

func TestBluetoothLevelParsing(t *testing.T) {
	report := []byte(`{"SPBluetoothDataType":[{"device_connected":[{"AirPods Pro":{"device_batteryLevelLeft":"80%","device_batteryLevelRight":"78 %","device_batteryLevelCase":"50%"}},{"Sony WH-1000XM5":{"device_batteryLevelMain":"65%"}}]}]}`)
	levels := parseBluetoothLevels(report, "AirPods Pro")
	want := []BatteryLevel{{Part: HeadphoneLeft, Pct: 80}, {Part: HeadphoneRight, Pct: 78}, {Part: HeadphoneCase, Pct: 50}}
	if fmt.Sprint(levels) != fmt.Sprint(want) {
		t.Fatalf("levels = %v, want %v", levels, want)
	}
	if got := parseBluetoothLevels(report, "Sony WH-1000XM5"); fmt.Sprint(got) != fmt.Sprint([]BatteryLevel{{Part: HeadphoneWhole, Pct: 65}}) {
		t.Fatalf("single level = %v", got)
	}
	if got := parseBluetoothLevels(report, "Unknown"); got != nil {
		t.Fatalf("unknown device = %v", got)
	}
}
