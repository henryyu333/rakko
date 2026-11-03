//go:build darwin

package main

// Real-window evidence for the realtime activities: charging's three
// states, the headphone capsule's three, a download with and without a
// known total, and the completion capsule. Run with RAKKO_BASKET_SMOKE=1;
// the PNGs land in /tmp/shelf-live/. The charging events are injected
// through the same queue the power module posts to, because the test
// machine's adapter cannot be unplugged from a test.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

const liveShotDir = "/tmp/shelf-live"

func liveShot(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(120 * time.Millisecond)
	onMain(a.syncWindowFrame)
	time.Sleep(80 * time.Millisecond)
	if err := os.MkdirAll(liveShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(liveShotDir, name+".png")
	if out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput(); err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, path)
	return path
}

// liveSmokeShelf prepares the notch window with default live settings and
// no other modules, so every shot starts from a clean closed shell.
func liveSmokeShelf(t *testing.T) *shelf {
	t.Helper()
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := smokeShelf
	if a.win == nil {
		onMain(func() { a.newNotchWindow() })
	}
	onMain(func() {
		a.modules, a.np, a.dl = nil, nil, nil
		a.settings = settings.Default()
		a.clearLive()
		a.fb = feedback{}
		a.visible, a.peeking = false, false
		a.progress, a.animationFrom = 0, 0
		a.animationStart = time.Time{}
		a.peekProgress, a.peekFrom = 0, 0
		a.peekAnimStart = time.Time{}
		a.place(mygo.Screen.CursorScreenPoint())
		a.win.SetOpacity(1)
		a.win.ShowInactive()
		a.win.Invalidate()
	})
	time.Sleep(700 * time.Millisecond) // let any previous shell fold away
	return a
}

// TestLiveSmokeStates photographs the nine states. The charging, headphone
// and download-done notices are posted by the real modules through their
// seams (battery probe, device list, temporary folder), not by calling the
// queue directly, so the whole path is what the pictures show. Run with
// RAKKO_BASKET_SMOKE=1.
func TestLiveSmokeStates(t *testing.T) {
	a := liveSmokeShelf(t)
	defer onMain(func() {
		a.clearLive()
		a.dl = nil
		a.win.Invalidate()
	})
	shoot := func(name string) {
		time.Sleep(650 * time.Millisecond) // the grow-out spring
		liveShot(t, a, name)
		onMain(func() {
			a.clearLive()
			a.win.Invalidate()
		})
		time.Sleep(450 * time.Millisecond) // fold back before the next one
	}

	// Charging through the power module: launch on battery, plug in at 80%,
	// charge to 100%, unplug at 100% and drain to 20%.
	var batt mac.BatteryStatus
	pm := &powerModule{host: a, now: time.Now}
	pm.probe = func() (mac.BatteryStatus, bool) { return batt, true }
	stepPower := func(st mac.BatteryStatus) {
		batt = st
		pm.next = time.Time{}
		pm.tick(time.Now())
	}
	onMain(func() { stepPower(mac.BatteryStatus{Percent: 50}) })
	onMain(func() { stepPower(mac.BatteryStatus{Percent: 80, External: true}) })
	shoot("01-charging")
	onMain(func() { stepPower(mac.BatteryStatus{Percent: 100, External: true}) })
	shoot("02-charged")
	onMain(func() {
		stepPower(mac.BatteryStatus{Percent: 100})
		stepPower(mac.BatteryStatus{Percent: 20})
	})
	shoot("03-low")

	// Headphones through the audio module: devices come and go in its list;
	// the AirPods' battery arrives from its lookup seam.
	var devs []mac.AudioOutput
	am := &audioModule{
		host: a, list: func() []mac.AudioOutput { return devs }, now: time.Now,
		known: map[string]string{}, dispatch: onMain,
	}
	am.battery = func(name string) []batteryLevel {
		if name == "AirPods Pro" {
			return []batteryLevel{{part: batteryLeft, pct: 80}, {part: batteryRight, pct: 78}, {part: batteryCase, pct: 50}}
		}
		return nil
	}
	onMain(func() {
		// Seed with the built-in output only: a nil list means "CoreAudio
		// unavailable", which never starts the baseline.
		devs = []mac.AudioOutput{{Name: "MacBook Air的揚聲器", UID: "built-in"}}
		am.tick(time.Now())
	})
	onMain(func() {
		devs = append(devs, mac.AudioOutput{Name: "AirPods Pro", UID: "airpods", Bluetooth: true})
		am.next = time.Time{}
		am.tick(time.Now())
	})
	time.Sleep(250 * time.Millisecond) // the battery lookup's dispatch
	shoot("04-pods")
	onMain(func() {
		devs = []mac.AudioOutput{{Name: "MacBook Air的揚聲器", UID: "built-in"}}
		am.next = time.Time{}
		am.tick(time.Now())
	})
	shoot("06-disconnected")
	onMain(func() {
		devs = append(devs, mac.AudioOutput{Name: "Sony WH-1000XM5", UID: "sony", Bluetooth: true})
		am.next = time.Time{}
		am.tick(time.Now())
	})
	shoot("05-connected")

	// A finished download through its module: the temporary file becomes
	// the real one between two scans.
	dir := t.TempDir()
	temp := filepath.Join(dir, "设计稿-v3.zip.crdownload")
	target := filepath.Join(dir, "设计稿-v3.zip")
	writeLiveFile(t, temp, make([]byte, 4096))
	dm := &downloadsModule{
		host: a, dir: func() (string, error) { return dir, nil }, scan: scanDownloadDir,
		allowed: func() bool { return true }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	onMain(func() { dm.tick(time.Now()) })
	onMain(func() {
		if err := os.Remove(temp); err != nil {
			t.Fatal(err)
		}
		writeLiveFile(t, target, make([]byte, 4096))
		dm.next = time.Time{}
		dm.tick(time.Now())
	})
	shoot("07-download-done")

	// The persistent download owns the wings.
	onMain(func() {
		a.dl = &downloadsModule{host: a, items: []downloadFile{{
			temp: "/tmp/dl-1", name: "设计稿-v3.zip", done: 4_200_000, total: 10_000_000,
			mod: time.Now(),
		}}}
		a.win.Invalidate()
	})
	time.Sleep(700 * time.Millisecond)
	liveShot(t, a, "08-download-progress")
	onMain(func() {
		a.dl = &downloadsModule{host: a, items: []downloadFile{{
			temp: "/tmp/dl-2", name: "Xcode_16.xip", done: 2_200_000, total: 0,
			mod: time.Now(),
		}}}
		a.win.Invalidate()
	})
	time.Sleep(500 * time.Millisecond)
	liveShot(t, a, "09-download-unknown")

	// Back to a quiet notch for the next tests in this process.
	onMain(func() {
		a.dl = nil
		a.clearLive()
		a.win.Invalidate()
	})
	time.Sleep(500 * time.Millisecond)
}

// TestLiveMachineProbe records what the machine itself reports: the data
// the modules poll. It runs no timers and takes no screenshots.
func TestLiveMachineProbe(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	var batt mac.BatteryStatus
	var battOK bool
	var devices []mac.AudioOutput
	onMain(func() {
		batt, battOK = mac.Battery()
		devices = mac.OutputDevices()
	})
	t.Logf("battery=%+v ok=%v", batt, battOK)
	for _, d := range devices {
		t.Logf("output device %+v", d)
	}
}
