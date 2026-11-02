//go:build darwin

package main

// The realtime activities' rules: one notice at a time in arrival order,
// the panel cancels what is queued, charging posts each threshold once per
// cycle, headphone and download text, and the switches in settings.json
// silence their activity.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
	"shelf/internal/store"
)

func liveShelf() *shelf {
	a := motionShelf()
	a.settings = settings.Default()
	return a
}

func TestLiveQueueIsOneAtATime(t *testing.T) {
	a := liveShelf()
	now := time.Now()
	a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 80}, time.Second, now)
	a.pushLive(liveEvent{kind: liveHeadset, label: "AirPods Pro"}, 2*time.Second, now.Add(500*time.Millisecond))

	ev, ok := a.liveNow(now, true)
	if !ok || ev.label != "正在充电" {
		t.Fatalf("first event = %+v ok=%v, want 正在充电", ev, ok)
	}
	if ev, ok = a.liveNow(now.Add(900*time.Millisecond), true); !ok || ev.label != "正在充电" {
		t.Fatalf("the first hold ended early: %+v ok=%v", ev, ok)
	}
	if ev, ok = a.liveNow(now.Add(time.Second), true); !ok || ev.label != "AirPods Pro" {
		t.Fatalf("second event = %+v ok=%v, want AirPods Pro", ev, ok)
	}
	if ev, ok = a.liveNow(now.Add(2500*time.Millisecond), true); !ok || ev.label != "AirPods Pro" {
		t.Fatalf("the second hold was cut short: %+v ok=%v", ev, ok)
	}
	if ev, ok = a.liveNow(now.Add(3*time.Second), true); ok {
		t.Fatalf("second event outlived its hold: %+v", ev)
	}
}

// A notice queued behind another keeps its whole hold when its turn comes:
// the reviewer's example is a 5s 电量低 notice followed by a 2s 已断开, which
// used to expire unseen while it waited.
func TestLiveQueuedNoticeKeepsItsFullHold(t *testing.T) {
	a := liveShelf()
	now := time.Now()
	a.pushLive(liveEvent{kind: liveLow, label: "电量低", pct: 20}, liveHoldLow, now)
	a.pushLive(liveEvent{kind: liveHeadsetOff, label: "AirPods Pro"}, liveHoldUnplug, now.Add(500*time.Millisecond))
	if ev, ok := a.liveNow(now, true); !ok || ev.kind != liveLow {
		t.Fatalf("first event = %+v ok=%v", ev, ok)
	}
	// At the end of the first hold the queued disconnect still shows, and
	// its own two seconds start now rather than at arrival.
	ev, ok := a.liveNow(now.Add(liveHoldLow), true)
	if !ok || ev.kind != liveHeadsetOff {
		t.Fatalf("queued disconnect = %+v ok=%v", ev, ok)
	}
	if got, want := a.live.until, now.Add(liveHoldLow+liveHoldUnplug); !got.Equal(want) {
		t.Fatalf("disconnect runs until %v, want %v", got, want)
	}
	if _, ok := a.liveNow(now.Add(liveHoldLow+liveHoldUnplug-time.Millisecond), true); !ok {
		t.Fatal("the disconnect ended before its own hold")
	}
	if ev, ok := a.liveNow(now.Add(liveHoldLow+liveHoldUnplug), true); ok {
		t.Fatalf("the disconnect outlived its hold: %+v", ev)
	}
}

// The backlog has a bound for system notices: a charging or headphone
// notice that waited longer than liveQueueMaxWait is stale and never
// reaches the notch. Download results are exempt — the file finished, and
// the click is how it reaches the tray.
func TestLiveQueueDropsStaleBacklog(t *testing.T) {
	a := liveShelf()
	now := time.Now()
	// The documented bound is 15s.
	a.pushLive(liveEvent{kind: liveHeadset, label: "AirPods"}, liveHoldHeadset, now.
		Add(-15*time.Second-time.Second))
	if ev, ok := a.liveNow(now, true); ok {
		t.Fatalf("a stale notice was shown: %+v", ev)
	}
	a.pushLive(liveEvent{kind: liveHeadset, label: "Sony"}, liveHoldHeadset, now.Add(-15*time.Second))
	if _, ok := a.liveNow(now, true); !ok {
		t.Fatal("a notice exactly at the wait bound was dropped")
	}
	a.clearLive()
	a.pushLive(liveEvent{kind: liveDownloadDone, label: "a.zip", paths: []string{"/tmp/a.zip"}},
		liveHoldDownload, now.Add(-30*time.Second))
	if ev, ok := a.liveNow(now, true); !ok || ev.kind != liveDownloadDone {
		t.Fatalf("a waiting download result was dropped: %+v ok=%v", ev, ok)
	}
	if got, want := a.live.until, now.Add(liveHoldDownload); !got.Equal(want) {
		t.Fatalf("the result's hold ends at %v, want its full %v", got, want)
	}
}

// A status capsule (已复制 and friends) owns the shape while it is up: a
// live notice queued behind it must not start its hold unseen under the
// capsule's words. Its clock starts when the capsule ends.
func TestLiveWaitsForTheStatusCapsule(t *testing.T) {
	a := liveShelf()
	now := time.Now()
	a.toastStatus("已复制", now.Add(2*time.Second), statusOK)
	a.fb.arm(now)
	if !a.capsuleUp(now) {
		t.Fatal("the status capsule is not up")
	}
	a.pushLive(liveEvent{kind: liveHeadset, label: "AirPods Pro"}, liveHoldHeadset, now)
	if ev, ok := a.liveNow(now.Add(100*time.Millisecond), true); ok {
		t.Fatalf("the notice took the shape from the capsule: %+v", ev)
	}
	if a.live.shown {
		t.Fatal("the notice's hold started behind the capsule")
	}
	end := now.Add(2 * time.Second)
	ev, ok := a.liveNow(end, true)
	if !ok || ev.kind != liveHeadset {
		t.Fatalf("the notice did not start when the capsule ended: %+v ok=%v", ev, ok)
	}
	if got, want := a.live.until, end.Add(liveHoldHeadset); !got.Equal(want) {
		t.Fatalf("hold ends at %v, want the full %v from the capsule's end", got, want)
	}
}

func TestLivePanelCancelsQueue(t *testing.T) {
	a := liveShelf()
	a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 50}, 3*time.Second, time.Now())
	if _, ok := a.liveNow(time.Now(), true); !ok {
		t.Fatal("the event was not shown")
	}
	a.show(false) // opening clears the queue
	if len(a.live.queued) != 0 || a.live.shown {
		t.Fatalf("the queue survived the panel opening: %+v", a.live)
	}
	// Nothing posted while the panel is open pops out after closing.
	a.pushLive(liveEvent{kind: liveHeadset, label: "AirPods"}, liveHoldHeadset, time.Now())
	if len(a.live.queued) != 0 {
		t.Fatalf("an event was queued behind the open panel")
	}
}

func TestLiveCoversNowPlayingAndReturns(t *testing.T) {
	a := liveShelf()
	fakeTrack(a, true)
	now := time.Now()
	a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 80}, liveHoldCharge, now)

	w, h := a.peekSize()
	if w <= a.wingsSizeW() || int(h) != max(32, a.notchH)+npWingsBody {
		t.Fatalf("charging shape = %gx%g, want wider than the wings", w, h)
	}
	// End the notice: the track's own wings come back.
	a.live.until = now.Add(-time.Second)
	w, h = a.peekSize()
	if w != a.wingsSizeW() {
		t.Fatalf("wings came back as %gx%g, want width %g", w, h, a.wingsSizeW())
	}
}

func (a *shelf) wingsSizeW() float32 {
	w, _ := a.wingsSize()
	return w
}

func TestLiveSwitchSilencesActivity(t *testing.T) {
	a := liveShelf()
	a.settings.LiveActivities.Charging = false
	a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 20}, liveHoldCharge, time.Now())
	if len(a.live.queued) != 0 {
		t.Fatal("a switched-off charging notice was queued")
	}
	// A notice already up disappears when its switch turns off.
	a.settings.LiveActivities.Charging = true
	a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 20}, liveHoldCharge, time.Now())
	a.settings.LiveActivities.Charging = false
	if _, ok := a.liveNow(time.Now(), true); ok {
		t.Fatal("the switch did not hide the notice")
	}
}

func TestPowerEventsOncePerThreshold(t *testing.T) {
	a := liveShelf()
	m := &powerModule{host: a, now: time.Now}
	var st mac.BatteryStatus
	ok := true
	m.probe = func() (mac.BatteryStatus, bool) { return st, ok }
	step := func(s mac.BatteryStatus, at time.Time) {
		st = s
		m.next = time.Time{}
		m.tick(at)
	}
	now := time.Now()
	step(mac.BatteryStatus{Percent: 50}, now) // seed on battery
	if len(a.live.queued) != 0 {
		t.Fatalf("the launch state posted %d notices", len(a.live.queued))
	}

	step(mac.BatteryStatus{Percent: 80, External: true}, now.Add(2*time.Second))
	if len(a.live.queued) != 1 || a.live.queued[0].kind != liveCharging {
		t.Fatalf("plugging in queued %+v", a.live.queued)
	}
	step(mac.BatteryStatus{Percent: 80, External: true, Charging: true}, now.Add(4*time.Second))
	if len(a.live.queued) != 1 {
		t.Fatalf("a steady charge posted again: %+v", a.live.queued)
	}
	step(mac.BatteryStatus{Percent: 100, External: true}, now.Add(6*time.Second))
	if len(a.live.queued) != 2 || a.live.queued[1].kind != liveCharged {
		t.Fatalf("full battery queued %+v", a.live.queued)
	}
	step(mac.BatteryStatus{Percent: 100, External: true}, now.Add(8*time.Second))
	if len(a.live.queued) != 2 {
		t.Fatalf("full battery repeated: %+v", a.live.queued)
	}

	// Discharge: each threshold once, then a new cycle brings them back.
	a.clearLive()
	step(mac.BatteryStatus{Percent: 30}, now.Add(10*time.Second))
	if len(a.live.queued) != 0 {
		t.Fatalf("unplugging above the threshold posted %+v", a.live.queued)
	}
	step(mac.BatteryStatus{Percent: 20}, now.Add(12*time.Second))
	step(mac.BatteryStatus{Percent: 19}, now.Add(14*time.Second))
	step(mac.BatteryStatus{Percent: 10}, now.Add(16*time.Second))
	step(mac.BatteryStatus{Percent: 9}, now.Add(18*time.Second))
	if len(a.live.queued) != 2 {
		t.Fatalf("low battery queued %d notices, want the 20%% and 10%% thresholds", len(a.live.queued))
	}
	if a.live.queued[0].pct != 20 || a.live.queued[1].pct != 10 {
		t.Fatalf("low notices %+v, want 20 then 10", a.live.queued)
	}
	a.clearLive()
	step(mac.BatteryStatus{Percent: 40, External: true}, now.Add(20*time.Second))
	a.clearLive() // drop the charging notice
	step(mac.BatteryStatus{Percent: 40}, now.Add(22*time.Second))
	step(mac.BatteryStatus{Percent: 20}, now.Add(24*time.Second))
	if len(a.live.queued) != 1 {
		t.Fatalf("the new discharge cycle did not repeat 20%%: %+v", a.live.queued)
	}

	// A Mac without a battery never speaks.
	a.clearLive()
	ok = false
	step(mac.BatteryStatus{Percent: 5}, now.Add(26*time.Second))
	if len(a.live.queued) != 0 {
		t.Fatalf("a battery-less Mac posted %+v", a.live.queued)
	}
}

// The system's power-source observer wakes the poll: the notice for an
// adapter attach starts on the next 30 ms tick, long before the two-second
// interval. Without an observer the poll alone still carries it.
func TestPowerObserverWakesThePoll(t *testing.T) {
	a := liveShelf()
	var wake func()
	old := powerObserver
	powerObserver = func(fn func()) (uintptr, error) { wake = fn; return 1, nil }
	t.Cleanup(func() { powerObserver = old })

	st := mac.BatteryStatus{Percent: 40}
	probes := 0
	probe := func() (mac.BatteryStatus, bool) { probes++; return st, true }
	m := &powerModule{host: a, now: time.Now, probe: probe}
	if !m.watchPower() || wake == nil {
		t.Fatal("the observer was not installed through the seam")
	}
	now := time.Now()
	m.tick(now) // seed on battery
	if probes != 1 {
		t.Fatalf("seeding probed %d times", probes)
	}
	// The gate holds for two seconds: a tick alone must not probe.
	m.tick(now.Add(10 * time.Millisecond))
	if probes != 1 {
		t.Fatal("the tick ignored the poll gate")
	}
	// IOKit's notification opens the gate for the next tick.
	st = mac.BatteryStatus{Percent: 40, External: true}
	wake()
	m.tick(now.Add(20 * time.Millisecond))
	if probes != 2 {
		t.Fatalf("the wake did not probe: %d probes", probes)
	}
	if len(a.live.queued) != 1 || a.live.queued[0].kind != liveCharging {
		t.Fatalf("plugging in queued %+v", a.live.queued)
	}

	// Fallback: a Mac where the observer cannot be installed keeps the
	// two-second poll as its only path.
	powerObserver = func(func()) (uintptr, error) { return 0, errors.New("no IOKit") }
	a.clearLive()
	st = mac.BatteryStatus{Percent: 40}
	fallback := &powerModule{host: a, now: time.Now, probe: probe}
	if fallback.watchPower() {
		t.Fatal("a failed observer reported success")
	}
	before := probes
	fallback.tick(now)                                           // its own gate is open: one probe
	fallback.tick(now.Add(powerPollInterval - time.Millisecond)) // the gate holds
	if probes != before+1 {
		t.Fatalf("the fallback probed %d times, want exactly the gated tick", probes-before)
	}
	st = mac.BatteryStatus{Percent: 40, External: true}
	fallback.tick(now.Add(powerPollInterval))
	if len(a.live.queued) != 1 {
		t.Fatalf("the poll missed the plug-in: %+v", a.live.queued)
	}
}

func TestAudioConnectAndDisconnect(t *testing.T) {
	a := liveShelf()
	devs := []mac.AudioOutput{
		{Name: "扬声器", UID: "built-in"},
		{Name: "AirPods Pro", UID: "airpods", Bluetooth: true},
	}
	m := &audioModule{host: a, list: func() []mac.AudioOutput { return devs }, now: time.Now, known: map[string]string{}}
	now := time.Now()
	m.tick(now) // seed with the AirPods already connected
	if len(a.live.queued) != 0 {
		t.Fatalf("the launch state announced %+v", a.live.queued)
	}
	devs = []mac.AudioOutput{{Name: "扬声器", UID: "built-in"}}
	m.next = time.Time{}
	m.tick(now.Add(time.Second))
	if len(a.live.queued) != 1 || a.live.queued[0].kind != liveHeadsetOff || a.live.queued[0].label != "AirPods Pro" {
		t.Fatalf("disconnect queued %+v", a.live.queued)
	}
	devs = append(devs, mac.AudioOutput{Name: "AirPods Pro", UID: "airpods", Bluetooth: true})
	m.next = time.Time{}
	m.tick(now.Add(2 * time.Second))
	if len(a.live.queued) != 2 || a.live.queued[1].kind != liveHeadset {
		t.Fatalf("reconnect queued %+v", a.live.queued)
	}
	// A keyboard is not an output and must never be announced.
	devs = append(devs, mac.AudioOutput{Name: "键盘", UID: "keys"})
	m.next = time.Time{}
	m.tick(now.Add(3 * time.Second))
	if len(a.live.queued) != 2 {
		t.Fatalf("a non-audio device was announced: %+v", a.live.queued)
	}
}

func TestBluetoothLevelParsing(t *testing.T) {
	report := []byte(`{"SPBluetoothDataType":[{"device_connected":[{"AirPods Pro":{"device_batteryLevelLeft":"80%","device_batteryLevelRight":"78 %","device_batteryLevelCase":"50%"}},{"Sony WH-1000XM5":{"device_batteryLevelMain":"65%"}}]}]}`)
	levels := parseBluetoothLevels(report, "AirPods Pro")
	want := []string{"左 80%", "右 78%", "盒 50%"}
	if fmt.Sprint(levels) != fmt.Sprint(want) {
		t.Fatalf("levels = %v, want %v", levels, want)
	}
	if got := parseBluetoothLevels(report, "Sony WH-1000XM5"); fmt.Sprint(got) != "[电量 65%]" {
		t.Fatalf("single level = %v", got)
	}
	if got := parseBluetoothLevels(report, "Unknown"); got != nil {
		t.Fatalf("unknown device = %v", got)
	}
}

func TestDownloadScanIdentifiesBrowsers(t *testing.T) {
	dir := t.TempDir()
	writeLiveFile(t, filepath.Join(dir, "movie.mp4.crdownload"), make([]byte, 700))
	writeLiveFile(t, filepath.Join(dir, "linux.iso.part"), make([]byte, 1300))
	bundle := filepath.Join(dir, "design.zip.download")
	if err := os.Mkdir(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "design.zip")
	writeLiveFile(t, filepath.Join(bundle, "Info.plist"), []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>DownloadEntryProgressBytesSoFar</key><integer>250</integer>
<key>DownloadEntryProgressTotalToLoad</key><integer>1000</integer>
<key>DownloadEntryPath</key><string>%s</string>
</dict></plist>`, target)))

	files, err := scanDownloadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("scan found %d downloads: %+v", len(files), files)
	}
	byName := map[string]downloadFile{}
	for _, f := range files {
		byName[f.name] = f
	}
	if f := byName["movie.mp4"]; f.done != 700 || f.total != 0 {
		t.Fatalf("Chrome download = %+v", f)
	}
	if f := byName["linux.iso"]; f.done != 1300 || f.total != 0 {
		t.Fatalf("Firefox download = %+v", f)
	}
	f := byName["design.zip"]
	if f.done != 250 || f.total != 1000 || f.target != target {
		t.Fatalf("Safari download = %+v", f)
	}
	if got := downloadPercent(f); got != "25%" {
		t.Fatalf("Safari percent = %q", got)
	}
	if got := downloadPercent(byName["movie.mp4"]); got != "" {
		t.Fatalf("unknown total reported %q", got)
	}
	if got := downloadMegabytes(700); got != "700 B" {
		t.Fatalf("bytes = %q", got)
	}
}

func TestDownloadCompletionPostsCapsule(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, "notes.txt.crdownload")
	target := filepath.Join(dir, "notes.txt")
	writeLiveFile(t, temp, make([]byte, 64))
	a := liveShelf()
	m := &downloadsModule{
		host: a, dir: func() (string, error) { return dir, nil }, scan: scanDownloadDir,
		allowed: func() bool { return true }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	now := time.Now()
	m.tick(now)
	if !m.active() {
		t.Fatal("the running download is not active")
	}
	// Chrome removes its temp file and renames it into place.
	if err := os.Remove(temp); err != nil {
		t.Fatal(err)
	}
	writeLiveFile(t, target, make([]byte, 64))
	m.next = time.Time{}
	m.tick(now.Add(time.Second))
	if m.active() {
		t.Fatal("the finished download still owns the wings")
	}
	ev, ok := a.liveNow(now.Add(time.Second), true)
	if !ok || ev.kind != liveDownloadDone || len(ev.paths) != 1 || ev.paths[0] != target || ev.label != "notes.txt" {
		t.Fatalf("completion = %+v ok=%v", ev, ok)
	}
}

func TestDownloadSwitchAndUnreadableFolder(t *testing.T) {
	dir := t.TempDir()
	writeLiveFile(t, filepath.Join(dir, "a.bin.part"), make([]byte, 10))
	a := liveShelf()
	a.settings.LiveActivities.Downloads = false
	m := &downloadsModule{
		host: a, dir: func() (string, error) { return dir, nil }, scan: scanDownloadDir,
		allowed: func() bool { return true }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	m.tick(time.Now())
	if m.active() || len(a.live.queued) != 0 {
		t.Fatal("downloads ran with their switch off")
	}
	a.settings.LiveActivities.Downloads = true
	m.next = time.Time{}
	m.tick(time.Now().Add(time.Second))
	if !m.active() {
		t.Fatal("re-enabling did not start the watcher")
	}
	// Full Disk Access denied: nothing may be read.
	m = &downloadsModule{
		host: a, dir: func() (string, error) { return dir, nil }, scan: scanDownloadDir,
		allowed: func() bool { return false }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	m.tick(time.Now())
	if m.active() {
		t.Fatal("downloads were read without Full Disk Access")
	}
}

func TestLiveViewsDrawTheirWords(t *testing.T) {
	a := liveShelf()
	a.peeking = true
	a.peekProgress, a.peekFrom = 1, 1
	view := ui.NewTester(a.view, sheetW, panelTestH)
	now := time.Now()
	a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 80}, liveHoldCharge, now)
	view.Frame()
	if !view.HasText("正在充电") || !view.HasText("80") {
		t.Fatalf("charging wings lost their words: %v", view.Texts())
	}

	a.clearLive()
	a.fx.peekKind = peekChevron
	a.pushLive(liveEvent{kind: liveHeadset, label: "AirPods Pro", levels: []string{"左 80%", "右 78%", "盒 50%"}}, liveHoldHeadset, now.Add(time.Second))
	view2 := ui.NewTester(a.view, sheetW, panelTestH)
	view2.Frame()
	for _, want := range []string{"AirPods Pro", "左 80%", "右 78%", "盒 50%"} {
		if !view2.HasText(want) {
			t.Fatalf("headphone capsule lost %q: %v", want, view2.Texts())
		}
	}

	a.clearLive()
	a.fx.peekKind = peekChevron
	a.dl = &downloadsModule{host: a, items: []downloadFile{{name: "movie.mp4", done: 420, total: 1000}}}
	a.settings.LiveActivities.Downloads = true
	view3 := ui.NewTester(a.view, sheetW, panelTestH)
	view3.Frame()
	if !view3.HasText("42%") {
		t.Fatalf("download wings lost the percentage: %v", view3.Texts())
	}
}

func TestDownloadCapsuleClickAddsToTray(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "report.pdf")
	writeLiveFile(t, target, []byte("%PDF"))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := liveShelf()
	a.store = st
	a.dataDir = t.TempDir()
	a.pushLive(liveEvent{kind: liveDownloadDone, label: "report.pdf", paths: []string{target}}, liveHoldDownload, time.Now())
	ev, ok := a.liveNow(time.Now(), true)
	if !ok {
		t.Fatal("no completion event")
	}
	a.openDownloaded(ev)
	if !a.visible || a.currentLevel() != 2 || a.tab != pageTray {
		t.Fatalf("the tray page did not open: visible=%v level=%d tab=%v", a.visible, a.currentLevel(), a.tab)
	}
	found := false
	for _, it := range st.Items {
		if it.Path == target {
			found = true
		}
	}
	if !found {
		t.Fatalf("the file is not in the tray: %+v", st.Items)
	}
}

func writeLiveFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The signal geometry: both wings keep the bar's whole group inside the
// visible band (the middle of the shell sits behind the camera and has no
// pixels), and the capsules use the completion capsule's 22pt body.
func TestLiveShapesFitTheNotchBand(t *testing.T) {
	a := liveShelf()
	// 26pt is a three-digit 13pt semibold number; keep at least 12pt clear
	// of the camera housing so the bar never slides under the notch.
	if need := liveWingPad + liveBarW + 10 + 26 + 12; need > liveWingW {
		t.Fatalf("the charge bar and a three-digit number need %dpt with margin, wing is %d",
			need, liveWingW)
	}
	if need := liveWingPad + liveBarW + 10 + 26; need > islandLiveWingW {
		t.Fatalf("the island's charge group needs %dpt, wing is %d", need, islandLiveWingW)
	}
	if liveWingPad+11+9+50 > liveWingW {
		t.Fatalf("the charging words need %gpt, wing is %d",
			float32(liveWingPad+11+9+50), liveWingW)
	}
	w, h := a.liveShape(liveEvent{kind: liveCharging, pct: 80})
	if w != float32(max(180, a.notchW))+2*liveWingW || h != float32(a.notchH)+npWingsBody {
		t.Fatalf("charging shape = %gx%g", w, h)
	}
	if dw, dh := a.downloadShape(); dw != w || dh != h {
		t.Fatalf("download shape = %gx%g, want the same wings %gx%g", dw, dh, w, h)
	}
	for _, ev := range []liveEvent{
		{kind: liveHeadset, label: "AirPods Pro", levels: []string{"左 80%", "右 78%", "盒 50%"}},
		{kind: liveHeadset, label: "Sony WH-1000XM5"},
		{kind: liveHeadsetOff, label: "AirPods Pro"},
		{kind: liveDownloadDone, label: "设计稿-v3.zip"},
	} {
		cw, ch := a.liveShape(ev)
		if ch != float32(a.notchH)+fbBody {
			t.Fatalf("%v: capsule height %g, want %d", ev.label, ch, a.notchH+fbBody)
		}
		if cw < float32(max(180, a.notchW))+60 || cw > fbMaxW {
			t.Fatalf("%v: capsule width %g outside the shell's range", ev.label, cw)
		}
	}
}

// A settings.json written before this feature keeps every activity on.
func TestLiveSettingsDefaultOnForOldFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, settings.FileName), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LiveActivities.Charging || !got.LiveActivities.Headphones || !got.LiveActivities.Downloads {
		t.Fatalf("old settings.json turned activities off: %+v", got.LiveActivities)
	}
}

// The headphone notice's glyph follows the device: AirPods keep the
// earbuds, any other headset gets the over-ear headphones.
func TestLiveHeadsetGlyph(t *testing.T) {
	if got, w := liveHeadsetIcon("AirPods Pro"); got != livePodsSym || w != 18 {
		t.Fatalf("AirPods glyph = %p w=%g, want the earbuds", got, w)
	}
	if got, _ := liveHeadsetIcon("Sony WH-1000XM5"); got != livePhonesSym {
		t.Fatal("a non-AirPods headset did not get the over-ear glyph")
	}
	if got, _ := liveHeadsetIcon("ALLENCHAN的AirPods Pro"); got != livePodsSym {
		t.Fatal("a named AirPods pair did not get the earbuds")
	}
}

// Two Bluetooth outputs appearing in one poll each keep their own battery
// answer: the lookup is judged against that device's connection, not a
// single module-wide generation.
func TestAudioBatteryPerDevice(t *testing.T) {
	a := liveShelf()
	devs := []mac.AudioOutput{{Name: "扬声器", UID: "built-in"}}
	m := &audioModule{host: a, list: func() []mac.AudioOutput { return devs }, now: time.Now, known: map[string]string{}}
	now := time.Now()
	m.tick(now) // seed

	dispatched := make(chan func(), 4)
	m.battery = func(name string) []string {
		switch name {
		case "AirPods Pro":
			return []string{"左 80%"}
		case "Sony WH-1000XM5":
			return []string{"电量 65%"}
		}
		return nil
	}
	m.dispatch = func(fn func()) { dispatched <- fn }
	devs = append(devs,
		mac.AudioOutput{Name: "AirPods Pro", UID: "airpods", Bluetooth: true},
		mac.AudioOutput{Name: "Sony WH-1000XM5", UID: "sony", Bluetooth: true})
	m.next = time.Time{}
	m.tick(now.Add(time.Second))
	if len(a.live.queued) != 2 {
		t.Fatalf("two connections queued %+v", a.live.queued)
	}
	var pending []func()
	deadline := time.After(3 * time.Second)
	for len(pending) < 2 {
		select {
		case fn := <-dispatched:
			pending = append(pending, fn)
		case <-deadline:
			t.Fatalf("only %d battery answers arrived", len(pending))
		}
	}
	for _, fn := range pending {
		fn()
	}
	if got := a.live.queued[0].levels; fmt.Sprint(got) != "[左 80%]" {
		t.Fatalf("AirPods levels = %v, want its own answer", got)
	}
	if got := a.live.queued[1].levels; fmt.Sprint(got) != "[电量 65%]" {
		t.Fatalf("Sony levels = %v, want its own answer", got)
	}
}

// A folder that stays unreadable must not leave a frozen download owning
// the wings: three consecutive unknown errors (or one missing folder)
// pause the watcher — the wings go, the baseline stays — and no 已下载 is
// invented from a failed scan.
func TestDownloadPersistentScanFailureClearsWings(t *testing.T) {
	a := liveShelf()
	now := time.Now()
	items := []downloadFile{{temp: "/tmp/a.zip.crdownload", name: "a.zip", target: "/tmp/a.zip", done: 10, mod: now}}
	var scanErr error
	m := &downloadsModule{
		host: a, dir: func() (string, error) { return "/tmp", nil },
		scan: func(string) ([]downloadFile, error) {
			if scanErr != nil {
				return nil, scanErr
			}
			return items, nil
		},
		allowed: func() bool { return true }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	m.tick(now)
	if !m.active() {
		t.Fatal("the running download is not active")
	}
	scanErr = errors.New("temporary I/O error")
	// Three consecutive unknown errors are the documented limit.
	for i := range 2 {
		m.next = time.Time{}
		m.tick(now.Add(time.Duration(i+1) * time.Second))
		if !m.active() {
			t.Fatalf("tick %d: a transient failure dropped the download", i+1)
		}
	}
	m.next = time.Time{}
	m.tick(now.Add(3 * time.Second))
	if m.active() || len(m.items) != 0 {
		t.Fatalf("persistent failures left the wings frozen: %+v", m.items)
	}
	if len(a.live.queued) != 0 {
		t.Fatalf("a failed scan invented notices: %+v", a.live.queued)
	}
}

// A missing folder stops the wings at once, without waiting for the
// failure cap, but it no longer forgets what was in flight: the baseline
// stays so the first readable scan can still name a file that finished
// while the folder was gone.
func TestDownloadMissingFolderPausesAtOnce(t *testing.T) {
	old := fileExists
	fileExists = func(string) bool { return true }
	t.Cleanup(func() { fileExists = old })
	a := liveShelf()
	now := time.Now()
	missing := &os.PathError{Op: "open", Path: "/tmp/nope", Err: syscall.ENOENT}
	inFlight := downloadFile{temp: "/tmp/x.crdownload", name: "x", target: "/tmp/x", done: 10, mod: now}
	items := []downloadFile{inFlight}
	var scanErr error = missing
	m := &downloadsModule{
		host: a, dir: func() (string, error) { return "/tmp/nope", nil },
		scan: func(string) ([]downloadFile, error) {
			if scanErr != nil {
				return nil, scanErr
			}
			return items, nil
		},
		allowed: func() bool { return true }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	m.items = []downloadFile{{temp: "/tmp/x", name: "x"}}
	m.prev = map[string]downloadFile{inFlight.temp: inFlight}
	m.seeded = true
	m.tick(now)
	if m.active() || len(m.items) != 0 {
		t.Fatalf("a missing folder kept its wings: items=%+v", m.items)
	}
	if !m.seeded || len(m.prev) != 1 || m.pausedAt.IsZero() {
		t.Fatalf("the missing folder forgot the baseline: seeded=%v prev=%+v pausedAt=%v",
			m.seeded, m.prev, m.pausedAt)
	}
	if len(a.live.queued) != 0 {
		t.Fatalf("the missing folder invented notices: %+v", a.live.queued)
	}
	// The folder is readable again within the pause window: the file that
	// finished meanwhile is still a result the user has not seen.
	items, scanErr = nil, nil
	m.next = time.Time{}
	m.tick(now.Add(5 * time.Second))
	if len(a.live.queued) != 1 || len(a.live.queued[0].paths) != 1 || a.live.queued[0].paths[0] != "/tmp/x" {
		t.Fatalf("the completion during the pause was lost: %+v", a.live.queued)
	}
}

// Several downloads finishing in one scan are one notice that names the
// count and holds long enough to read; two files used to be two capsules
// racing the 15s backlog bound.
func TestDownloadCompletionsOneNotice(t *testing.T) {
	dir := t.TempDir()
	writeLiveFile(t, filepath.Join(dir, "one.zip.crdownload"), []byte("1"))
	writeLiveFile(t, filepath.Join(dir, "two.zip.crdownload"), []byte("2"))
	a := liveShelf()
	now := time.Now()
	m := &downloadsModule{
		host: a, dir: func() (string, error) { return dir, nil }, scan: scanDownloadDir,
		allowed: func() bool { return true }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	m.tick(now)
	for _, name := range []string{"one.zip", "two.zip"} {
		if err := os.Remove(filepath.Join(dir, name+".crdownload")); err != nil {
			t.Fatal(err)
		}
		writeLiveFile(t, filepath.Join(dir, name), []byte("done"))
	}
	m.next = time.Time{}
	doneAt := now.Add(time.Second)
	m.tick(doneAt)
	if len(a.live.queued) != 1 {
		t.Fatalf("two completed downloads queued %d notices, want one", len(a.live.queued))
	}
	ev, ok := a.liveNow(doneAt, true)
	if !ok || ev.kind != liveDownloadDone || len(ev.paths) != 2 {
		t.Fatalf("completion = %+v ok=%v, want both paths", ev, ok)
	}
	if ev.label != "2 个文件" || ev.statusLine() != "已下载 2 个文件" {
		t.Fatalf("batch words = %q / %q", ev.label, ev.statusLine())
	}
	if got, want := a.live.until, doneAt.Add(liveHoldDownload+time.Second); !got.Equal(want) {
		t.Fatalf("batch hold ends at %v, want %v", got, want)
	}
	if ev, ok := a.liveNow(a.live.until, true); ok {
		t.Fatalf("the batch outlived its hold: %+v", ev)
	}
}

// A result pushed while another result still waits in the queue joins it:
// the second completion updates the queued words instead of adding another
// entry behind them.
func TestDownloadDoneMergesQueuedResult(t *testing.T) {
	a := liveShelf()
	now := time.Now()
	a.pushDownloaded([]string{"/tmp/one.zip"}, now)
	a.pushDownloaded([]string{"/tmp/two.zip"}, now.Add(200*time.Millisecond))
	if len(a.live.queued) != 1 {
		t.Fatalf("two pushes queued %d notices, want one merged", len(a.live.queued))
	}
	ev, ok := a.liveNow(now.Add(time.Second), true)
	if !ok || len(ev.paths) != 2 || ev.paths[0] != "/tmp/one.zip" || ev.paths[1] != "/tmp/two.zip" {
		t.Fatalf("merged result = %+v ok=%v", ev, ok)
	}
	if ev.label != "2 个文件" {
		t.Fatalf("merged label = %q", ev.label)
	}
	if got, want := a.live.until, now.Add(time.Second+liveHoldDownload+time.Second); !got.Equal(want) {
		t.Fatalf("merged hold ends at %v, want %v", got, want)
	}
}

// A whole scan's completions are one notice: the words name the count, the
// hold grows to its cap, and the click puts every file into the tray. Five
// files used to lose the fifth onward to the backlog bound — and that
// dropped capsule was the only way to reach those files.
func TestDownloadBatchNoticeAddsEveryFile(t *testing.T) {
	dir := t.TempDir()
	const batch = 6
	targets := make([]string, 0, batch)
	for i := range batch {
		name := fmt.Sprintf("f%d.zip", i)
		writeLiveFile(t, filepath.Join(dir, name+".crdownload"), []byte("x"))
		targets = append(targets, filepath.Join(dir, name))
	}
	a := liveShelf()
	now := time.Now()
	m := &downloadsModule{
		host: a, dir: func() (string, error) { return dir, nil }, scan: scanDownloadDir,
		allowed: func() bool { return true }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	m.tick(now)
	for _, target := range targets {
		if err := os.Remove(target + ".crdownload"); err != nil {
			t.Fatal(err)
		}
		writeLiveFile(t, target, []byte("done"))
	}
	m.next = time.Time{}
	doneAt := now.Add(time.Second)
	m.tick(doneAt)
	if len(a.live.queued) != 1 {
		t.Fatalf("six completions queued %d notices, want one", len(a.live.queued))
	}
	ev, ok := a.liveNow(doneAt, true)
	if !ok || len(ev.paths) != batch {
		t.Fatalf("batch = %+v ok=%v, want %d paths", ev, ok, batch)
	}
	if ev.statusLine() != "已下载 6 个文件" {
		t.Fatalf("batch words = %q", ev.statusLine())
	}
	if got, want := a.live.until, doneAt.Add(liveHoldDownloadMax); !got.Equal(want) {
		t.Fatalf("batch hold ends at %v, want %v", got, want)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.store = st
	a.dataDir = t.TempDir()
	a.openDownloaded(ev)
	found := map[string]bool{}
	for _, it := range st.Items {
		found[it.Path] = true
	}
	for _, target := range targets {
		if !found[target] {
			t.Fatalf("the click left %q out of the tray: %+v", target, st.Items)
		}
	}
}

// A pause keeps the baseline: a file that finished while the folder was
// unreadable still gets its notice on the first good scan.
func TestDownloadPauseKeepsCompletionDuringFailure(t *testing.T) {
	old := fileExists
	fileExists = func(string) bool { return true }
	t.Cleanup(func() { fileExists = old })
	a := liveShelf()
	now := time.Now()
	inFlight := downloadFile{temp: "/tmp/a.zip.crdownload", name: "a.zip", target: "/tmp/a.zip", done: 10, mod: now}
	items := []downloadFile{inFlight}
	var scanErr error
	m := &downloadsModule{
		host: a, dir: func() (string, error) { return "/tmp", nil },
		scan: func(string) ([]downloadFile, error) {
			if scanErr != nil {
				return nil, scanErr
			}
			return items, nil
		},
		allowed: func() bool { return true }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	m.tick(now)
	scanErr = errors.New("temporary I/O error")
	for i := range downloadScanFailureLimit {
		m.next = time.Time{}
		m.tick(now.Add(time.Duration(i+1) * time.Second))
	}
	if m.active() || len(m.items) != 0 {
		t.Fatalf("persistent failures left the wings up: %+v", m.items)
	}
	if !m.seeded || m.prev[inFlight.temp].target != "/tmp/a.zip" {
		t.Fatalf("the pause forgot the baseline: seeded=%v prev=%+v", m.seeded, m.prev)
	}
	if len(a.live.queued) != 0 {
		t.Fatalf("a failed scan invented notices: %+v", a.live.queued)
	}
	// The folder comes back well inside the window: the file finished
	// during the failure still gets its notice.
	items, scanErr = nil, nil
	m.next = time.Time{}
	m.tick(now.Add(4 * time.Second))
	if len(a.live.queued) != 1 {
		t.Fatalf("the completion during the pause was lost: %+v", a.live.queued)
	}
	ev := a.live.queued[0]
	if ev.kind != liveDownloadDone || len(ev.paths) != 1 || ev.paths[0] != "/tmp/a.zip" {
		t.Fatalf("completion = %+v", ev)
	}
}

// A pause longer than two minutes re-baselines in silence: the files that
// finished then are old news by the time the folder is readable again.
func TestDownloadLongPauseRebaselinesSilently(t *testing.T) {
	old := fileExists
	fileExists = func(string) bool { return true }
	t.Cleanup(func() { fileExists = old })
	a := liveShelf()
	now := time.Now()
	inFlight := downloadFile{temp: "/tmp/a.zip.crdownload", name: "a.zip", target: "/tmp/a.zip", done: 10, mod: now}
	items := []downloadFile{inFlight}
	var scanErr error
	m := &downloadsModule{
		host: a, dir: func() (string, error) { return "/tmp", nil },
		scan: func(string) ([]downloadFile, error) {
			if scanErr != nil {
				return nil, scanErr
			}
			return items, nil
		},
		allowed: func() bool { return true }, plist: readXMLPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
	m.tick(now)
	scanErr = errors.New("temporary I/O error")
	failedAt := now.Add(time.Second)
	for i := range downloadScanFailureLimit {
		m.next = time.Time{}
		m.tick(failedAt.Add(time.Duration(i) * time.Second))
	}
	if !m.seeded || len(m.prev) != 1 {
		t.Fatalf("the pause forgot the baseline it must re-baseline from: seeded=%v prev=%+v",
			m.seeded, m.prev)
	}
	items, scanErr = nil, nil
	m.next = time.Time{}
	m.tick(failedAt.Add(downloadPauseMax + time.Second))
	if len(a.live.queued) != 0 {
		t.Fatalf("a stale pause replayed its completions: %+v", a.live.queued)
	}
	if len(m.prev) != 0 || !m.seeded {
		t.Fatalf("the silent re-baseline kept old state: prev=%+v seeded=%v", m.prev, m.seeded)
	}
}

// A status capsule that comes up over a notice already on screen covers its
// words without eating its hold: the clock rests for as long as the capsule
// shows, so a 4s download result a 已复制 covers is still readable — and
// still clickable — when the capsule folds away. The capsule-first order
// keeps its own rule: a notice queued behind a capsule starts after it.
func TestLiveNoticePausesUnderTheStatusCapsule(t *testing.T) {
	a := liveShelf()
	now := time.Now()
	a.pushDownloaded([]string{"/tmp/probe.zip"}, now)
	ev, ok := a.liveNow(now, true)
	if !ok || ev.kind != liveDownloadDone {
		t.Fatalf("the download result did not show: %+v ok=%v", ev, ok)
	}
	holdEnd := a.live.until
	if want := now.Add(liveHoldDownload); !holdEnd.Equal(want) {
		t.Fatalf("the download's hold ends at %v, want %v", holdEnd, want)
	}
	// 已复制 arrives one second into the hold and stays for four seconds.
	// Its clock starts at that frame, so its span is exactly t=1..5.
	capsuleAt, capsuleEnd := now.Add(time.Second), now.Add(5*time.Second)
	a.fb.show("已复制", statusOK, capsuleEnd, capsuleAt)
	a.fb.arm(capsuleAt)
	if !a.capsuleUp(capsuleAt) {
		t.Fatal("the status capsule is not up")
	}
	if ev, ok := a.liveNow(capsuleAt, true); ok {
		t.Fatalf("the notice showed through the capsule: %+v", ev)
	}
	if ev, ok := a.liveNow(capsuleAt.Add(2*time.Second), true); ok {
		t.Fatalf("the notice ran on under the capsule: %+v", ev)
	}
	if a.live.cur.kind != liveDownloadDone {
		t.Fatalf("the covered notice was dropped: %+v", a.live.cur)
	}
	// The four seconds behind the capsule do not count against the hold.
	ev, ok = a.liveNow(capsuleEnd, true)
	if !ok || ev.kind != liveDownloadDone {
		t.Fatalf("the download result was lost behind the capsule: %+v ok=%v", ev, ok)
	}
	if want := holdEnd.Add(capsuleEnd.Sub(capsuleAt)); !a.live.until.Equal(want) {
		t.Fatalf("the resumed hold ends at %v, want %v", a.live.until, want)
	}
	if !a.liveClickable(capsuleEnd) {
		t.Fatal("the resumed download result is not clickable")
	}
	if _, ok := a.liveNow(a.live.until.Add(-time.Millisecond), true); !ok {
		t.Fatal("the resumed notice ended before its extended hold")
	}
	if ev, ok := a.liveNow(a.live.until, true); ok {
		t.Fatalf("the resumed notice outlived its hold: %+v", ev)
	}
}

// A status capsule can outlast the notice's own deadline: 已复制 covering
// t=1..5s over a 4s download result must keep the notice paused past its
// frozen t=4 deadline and give back the whole covered span, so the words
// resume at t=5 and stay up until t=8.
func TestLiveCoveredNoticeWaitsOutItsOriginalDeadline(t *testing.T) {
	a := liveShelf()
	now := time.Now()
	a.pushDownloaded([]string{"/tmp/probe.zip"}, now)
	ev, ok := a.liveNow(now, true)
	if !ok || ev.kind != liveDownloadDone {
		t.Fatalf("the download result did not show: %+v ok=%v", ev, ok)
	}
	// 已复制 covers t=1..5s, crossing the notice's t=4 deadline.
	a.fb.show("已复制", statusOK, now.Add(5*time.Second), now.Add(time.Second))
	a.fb.arm(now.Add(time.Second))
	if !a.capsuleUp(now.Add(4500*time.Millisecond)) || a.capsuleUp(now.Add(5*time.Second)) {
		t.Fatal("the fixture capsule must cover t=1..5s")
	}
	if ev, ok := a.liveNow(now.Add(time.Second), true); ok {
		t.Fatalf("the notice showed through the capsule: %+v", ev)
	}
	if ev, ok := a.liveNow(now.Add(4500*time.Millisecond), true); ok {
		t.Fatalf("the covered notice ran on past its frozen deadline: %+v", ev)
	}
	ev, ok = a.liveNow(now.Add(5*time.Second), true)
	if !ok || ev.kind != liveDownloadDone {
		t.Fatalf("the covered notice did not resume when the capsule ended: %+v ok=%v", ev, ok)
	}
	if got, want := a.live.until.Sub(now), 2*liveHoldDownload; got != want {
		t.Fatalf("the resumed hold ends at %v, want %v (the 4s hold plus the 4s cover)", got, want)
	}
}

// The frame loop must agree with the sparse-clock case: 30ms frames across
// the same capsule never show the notice under it and still end its hold at
// t=8, not at the 7.97 a leaked start would give.
func TestLiveCoversEveryFramePastTheOriginalDeadline(t *testing.T) {
	a := liveShelf()
	now := time.Now()
	a.pushDownloaded([]string{"/tmp/probe.zip"}, now)
	if _, ok := a.liveNow(now, true); !ok {
		t.Fatal("the download result did not show")
	}
	a.fb.show("已复制", statusOK, now.Add(5*time.Second), now.Add(time.Second))
	a.fb.arm(now.Add(time.Second))
	if !a.capsuleUp(now.Add(4500*time.Millisecond)) || a.capsuleUp(now.Add(5*time.Second)) {
		t.Fatal("the fixture capsule must cover t=1..5s")
	}
	leaks := 0
	for d := time.Second; d < 5*time.Second; d += 30 * time.Millisecond {
		if _, ok := a.liveNow(now.Add(d), true); ok {
			leaks++
		}
	}
	if leaks != 0 {
		t.Fatalf("the notice showed %d times under the capsule", leaks)
	}
	if ev, ok := a.liveNow(now.Add(5*time.Second), true); !ok || ev.kind != liveDownloadDone {
		t.Fatalf("the covered notice did not resume when the capsule ended: %+v ok=%v", ev, ok)
	}
	if got, want := a.live.until.Sub(now), 2*liveHoldDownload; got != want {
		t.Fatalf("30ms frames leave the hold ending at %v, want %v", got, want)
	}
}
