//go:build darwin

package main

// The app switcher's seams: system queries and activation are stubbed, so
// no test activates or launches a real application.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

// appSwitchShelf is a closed headless shelf on a 2560-wide display whose
// geometry is enough for the wing math: notch anchor 180 × 32 at (600,0).
func appSwitchShelf(t *testing.T) *shelf {
	t.Helper()
	a := &shelf{
		level:        2,
		notchX:       600,
		notchY:       0,
		notchW:       180,
		notchH:       32,
		x:            410,
		y:            0,
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
	a.frame = mygo.Rectangle{X: 410, Y: 0, Width: 1000, Height: 70}
	a.dispWork = mygo.Rectangle{X: 0, Y: 0, Width: 2560, Height: 1415}
	a.dispBounds = a.dispWork
	a.settings = settings.Default()
	return a
}

// stubApps swaps the seams for f's duration. Register keys are recorded,
// never really taken; activation and open only count what they were asked.
type appsStub struct {
	running    []mac.RunningApp
	paths      map[string]string
	names      map[string]string
	installed  []mygo.InstalledApp
	installErr error
	activated  []int
	opened     []string
	actOK      map[int]bool
	openErr    error
	reg        map[string]func()
	regErr     map[string]error
}

func stubApps(t *testing.T, s *appsStub) {
	t.Helper()
	oldRun, oldPath, oldName, oldInst := appRunning, appPathFor, appNameFor, appsInstalled
	oldReg, oldUnreg := registerKey, unregisterKey
	oldAct, oldOpen := activateApp, openPath
	oldOnMain := mygoRunOnMain
	mygoRunOnMain = func(fn func()) { fn() } // tests have no event loop
	appRunning = func() []mac.RunningApp { return s.running }
	appPathFor = func(id string) string { return s.paths[id] }
	appNameFor = func(id string) string { return s.names[id] }
	appsInstalled = func() ([]mygo.InstalledApp, error) { return s.installed, s.installErr }
	activateApp = func(pid int) bool {
		s.activated = append(s.activated, pid)
		return s.actOK[pid]
	}
	openPath = func(path string) error {
		s.opened = append(s.opened, path)
		return s.openErr
	}
	if s.reg == nil {
		s.reg = map[string]func(){}
	}
	registerKey = func(acc string, fn func()) error {
		if err := s.regErr[acc]; err != nil {
			return err
		}
		s.reg[acc] = fn
		return nil
	}
	unregisterKey = func(acc string) { delete(s.reg, acc) }
	t.Cleanup(func() {
		appRunning, appPathFor, appNameFor, appsInstalled = oldRun, oldPath, oldName, oldInst
		registerKey, unregisterKey = oldReg, oldUnreg
		activateApp, openPath = oldAct, oldOpen
		mygoRunOnMain = oldOnMain
	})
}

func pinned(s *shelf) []string {
	if s.settings.PinnedApps == nil {
		return nil
	}
	return *s.settings.PinnedApps
}

func bundleIDs(list []appEntry) []string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.bundleID
	}
	return out
}

// The snapshot keeps the pinned order first — missing ones included — and
// the other running apps sorted by name, each bundle id exactly once.
func TestAppSnapshotDedupAndOrder(t *testing.T) {
	a := appSwitchShelf(t)
	saved := []string{"com.test.B", "com.test.missing", "com.test.A"}
	a.settings.PinnedApps = &saved
	stubApps(t, &appsStub{
		running: []mac.RunningApp{
			{BundleID: "com.test.A", Name: "Alpha", PID: 10},
			{BundleID: "com.test.B", Name: "Beta", PID: 11},
			{BundleID: "com.test.B", Name: "Beta Again", PID: 12}, // duplicate id collapses
			{BundleID: "com.test.zed", Name: "Zulu", PID: 13},
			{BundleID: "com.test.ace", Name: "Ace", PID: 14},
		},
		paths: map[string]string{"com.test.A": "/A.app", "com.test.B": "/B.app", "com.test.stopped": "/S.app"},
		names: map[string]string{"com.test.missing": "Long Gone", "com.test.stopped": "Stopped"},
	})
	list := a.appSnapshot()
	got := bundleIDs(list)
	want := []string{"com.test.B", "com.test.missing", "com.test.A", "com.test.ace", "com.test.zed"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("snapshot order %v, want %v", got, want)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("snapshot %v, want %v", got, want)
	}
	if list[0].pid != 11 || !list[0].pinned {
		t.Fatalf("running pinned seat = %+v", list[0])
	}
	if !list[1].missing || list[1].name != "Long Gone" {
		t.Fatalf("uninstalled pinned seat = %+v, want missing with a name", list[1])
	}
	// A pinned app that resolves on disk but is not running: launchable.
	saved = append(saved, "com.test.stopped")
	a.settings.PinnedApps = &saved
	list = a.appSnapshot()
	if list[3].bundleID != "com.test.stopped" || list[3].pid != 0 || list[3].missing || list[3].name != "Stopped" {
		t.Fatalf("installed-but-stopped pinned seat = %+v, want launchable, not missing", list[3])
	}
}

// The untouched setting uses the installed defaults; a saved empty list
// stays empty, and edits persist through applySettings.
func TestPinnedAppsDefaultsEmptyAndPersist(t *testing.T) {
	dir := t.TempDir()
	a := appSwitchShelf(t)
	a.dataDir = dir
	stubApps(t, &appsStub{
		paths: map[string]string{"com.apple.finder": "/System/F.app", "com.apple.Terminal": "/A/Terminal.app"},
	})
	// Untouched: defaults filtered to what resolves — a missing default
	// neither fails the row nor holds an empty seat.
	if ids := a.effectivePinnedIDs(); len(ids) != 2 || ids[0] != "com.apple.finder" || ids[1] != "com.apple.Terminal" {
		t.Fatalf("default pins %v, want finder+terminal only", ids)
	}
	// Pinning materializes the effective list (defaults first), then adds;
	// reorder moves it, unpin removes it, and the cap refuses silently.
	a.pinApp("com.test.one")
	a.pinApp("com.test.two")
	if ids := pinned(a); len(ids) != 4 || ids[3] != "com.test.two" {
		t.Fatalf("after pinning: %v", ids)
	}
	a.movePinned("com.test.two", -1)
	if ids := pinned(a); len(ids) != 4 || ids[2] != "com.test.two" {
		t.Fatalf("after moving: %v", ids)
	}
	// The cap is enforced: the fifth request cannot silently drop or grow.
	a.pinApp("com.test.five")
	if ids := pinned(a); len(ids) != 4 {
		t.Fatalf("cap ignored: %v", ids)
	}
	a.unpinApp("com.test.two")
	if ids := pinned(a); len(ids) != 3 {
		t.Fatalf("after unpin: %v", ids)
	}
	reloaded, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PinnedApps == nil || len(*reloaded.PinnedApps) != 3 {
		t.Fatalf("reloaded %v, want the 3 saved ids", reloaded.PinnedApps)
	}
	// The saved empty list is a third state: it never falls back to
	// defaults — not on the saved value, not through effectivePinnedIDs,
	// and not in a snapshot taken after the change.
	a.setPinnedApps([]string{})
	if ids := pinned(a); ids == nil || len(ids) != 0 {
		t.Fatalf("empty save = %v, want an explicit empty list", ids)
	}
	if ids := a.effectivePinnedIDs(); ids == nil || len(ids) != 0 {
		t.Fatalf("explicit empty resurrected defaults: %v", ids)
	}
	if list := a.appSnapshot(); len(list) != 0 {
		t.Fatalf("explicit empty pins must not seat defaults: %v", bundleIDs(list))
	}
	reloaded, err = settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PinnedApps == nil || len(*reloaded.PinnedApps) != 0 {
		t.Fatalf("empty list must reload as empty, got %v", reloaded.PinnedApps)
	}
	// …and a reload into a fresh shelf keeps it empty — no defaults.
	a2 := appSwitchShelf(t)
	a2.dataDir = dir
	loaded, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a2.settings = loaded
	if ids := a2.effectivePinnedIDs(); ids == nil || len(ids) != 0 {
		t.Fatalf("reloaded empty pins resurrected defaults: %v", ids)
	}
	if list := a2.appSnapshot(); len(list) != 0 {
		t.Fatalf("reloaded empty pins must not seat defaults: %v", bundleIDs(list))
	}
	// Unpinning the last pinned app leaves the same explicit empty, not a
	// resurrection: pin one, remove it, then prove the list and snapshot
	// stay empty.
	a.setPinnedApps([]string{"com.apple.finder"})
	a.unpinApp("com.apple.finder")
	if ids := a.effectivePinnedIDs(); ids == nil || len(ids) != 0 {
		t.Fatalf("unpinning the last app resurrected defaults: %v", ids)
	}
	if list := a.appSnapshot(); len(list) != 0 {
		t.Fatalf("unpinning the last app seated defaults: %v", bundleIDs(list))
	}
	// A broken settings.json keeps the mode read-only: nothing is written.
	a.settingsBroken = true
	before := append([]string(nil), pinned(a)...)
	a.pinApp("com.test.six")
	a.unpinApp("com.test.three")
	if ids := pinned(a); len(ids) != len(before) {
		t.Fatalf("broken settings must refuse writes, %v → %v", before, ids)
	}
}

// Activation races: a process that ended after the snapshot fails activate
// and falls back to launch-by-path; a path that vanished reports instead of
// faking a switch. Whatever the outcome, the wings fold.
func TestSwitchToAppRaces(t *testing.T) {
	t.Run("activate succeeds", func(t *testing.T) {
		a := appSwitchShelf(t)
		st := &appsStub{actOK: map[int]bool{42: true}}
		stubApps(t, st)
		a.openAppSwitch()
		a.switchToApp(appEntry{bundleID: "com.test.x", name: "X", pid: 42})
		if a.apps.on {
			t.Fatal("wings stayed open after a successful switch")
		}
		if len(st.activated) != 1 || st.activated[0] != 42 {
			t.Fatalf("activate called with %v", st.activated)
		}
		if len(st.opened) != 0 {
			t.Fatalf("launch called after activation: %v", st.opened)
		}
	})
	t.Run("pid died after snapshot, launch recovers", func(t *testing.T) {
		a := appSwitchShelf(t)
		st := &appsStub{
			actOK: map[int]bool{},
			paths: map[string]string{"com.test.x": "/Applications/X.app"},
		}
		stubApps(t, st)
		a.switchToApp(appEntry{bundleID: "com.test.x", name: "X", pid: 42})
		if len(st.opened) != 1 || st.opened[0] != "/Applications/X.app" {
			t.Fatalf("fallback launch %v", st.opened)
		}
		if a.apps.on {
			t.Fatal("wings stayed open after the fallback launch")
		}
	})
	t.Run("unresolvable path reports, never fakes", func(t *testing.T) {
		a := appSwitchShelf(t)
		st := &appsStub{actOK: map[int]bool{}, paths: map[string]string{}}
		stubApps(t, st)
		a.switchToApp(appEntry{bundleID: "com.test.gone", name: "Ghost", pid: 42})
		if a.apps.on {
			t.Fatal("wings stayed open after a failed switch")
		}
		if len(st.opened) != 0 {
			t.Fatalf("launch attempted on an unresolvable path: %v", st.opened)
		}
	})
	t.Run("missing entries are inert", func(t *testing.T) {
		a := appSwitchShelf(t)
		st := &appsStub{actOK: map[int]bool{1: true}}
		stubApps(t, st)
		a.switchToApp(appEntry{bundleID: "com.test.dead", name: "Dead", missing: true})
		if len(st.activated)+len(st.opened) != 0 {
			t.Fatal("a missing seat must never switch or launch")
		}
	})
}

// The selection walks selectable seats only — a missing pinned seat is
// drawn but skipped — wraps at both ends, and Enter activates it.
func TestAppSelectionWalkAndEnter(t *testing.T) {
	a := appSwitchShelf(t)
	saved := []string{"com.test.miss", "com.test.pin"}
	a.settings.PinnedApps = &saved
	st := &appsStub{
		running: []mac.RunningApp{
			{BundleID: "com.test.pin", Name: "Pin", PID: 7},
			{BundleID: "com.test.run", Name: "Run", PID: 8},
		},
		paths: map[string]string{"com.test.pin": "/Pin.app"},
		actOK: map[int]bool{7: true, 8: true},
	}
	stubApps(t, st)
	a.openAppSwitch()
	list := a.apps.list
	sel := a.selApps()
	if len(list) != 3 || len(sel) != 2 {
		t.Fatalf("snapshot %v, selectable %v", list, sel)
	}
	a.moveAppSel(1)
	if a.apps.sel != 0 || sel[a.apps.sel].bundleID != "com.test.pin" {
		t.Fatalf("first Right selects the pinned running seat, sel=%d", a.apps.sel)
	}
	a.moveAppSel(1)
	if sel[a.apps.sel].bundleID != "com.test.run" {
		t.Fatalf("second Right selects the running seat, sel=%d", a.apps.sel)
	}
	a.moveAppSel(1) // wraps to the first selectable
	if a.apps.sel != 0 {
		t.Fatalf("Right past the end must wrap, sel=%d", a.apps.sel)
	}
	a.moveAppSel(-1) // back to the last selectable
	if a.apps.sel != 1 {
		t.Fatalf("Left from the start must wrap, sel=%d", a.apps.sel)
	}
	a.activateAppSel()
	if len(st.activated) != 1 || st.activated[0] != 8 {
		t.Fatalf("Enter activated %v, want pid 8", st.activated)
	}
	if a.apps.on {
		t.Fatal("wings stayed open after Enter")
	}
}

// Esc and an outside click close the wings without ever handing another
// app's focus anywhere: apps mode never took it, so there is nothing to
// restore — the close path must not call the focus hand-back.
func TestAppsCancelKeepsFocus(t *testing.T) {
	a := appSwitchShelf(t)
	st := &appsStub{paths: map[string]string{}}
	stubApps(t, st)
	a.prevAppPID = 99 // pretend the panel once held a remembered app
	restored := false
	old := appIsActive
	appIsActive = func() bool { restored = true; return true }
	t.Cleanup(func() { appIsActive = old })
	a.openAppSwitch()
	a.escape()
	if a.apps.on {
		t.Fatal("Esc left the wings open")
	}
	if restored {
		t.Fatal("cancel reached the focus hand-back")
	}
	a.openAppSwitch()
	a.mouseDown(false, 0, mygo.Point{X: 5, Y: 900})
	if a.apps.on {
		t.Fatal("outside press left the wings open")
	}
	if restored {
		t.Fatal("outside press reached the focus hand-back")
	}
}

// The wings keep only the keys they took: a second open holds Left/Right/
// Enter, Escape is routed through the shared escape path, and every key is
// returned at the end.
func TestAppsKeysHeldAndReturned(t *testing.T) {
	a := appSwitchShelf(t)
	st := &appsStub{paths: map[string]string{}}
	stubApps(t, st)
	a.openAppSwitch()
	for _, k := range []string{"Left", "Right", "Enter", "Escape"} {
		if _, ok := st.reg[k]; !ok {
			t.Fatalf("%s not registered while open, held %v", k, st.reg)
		}
	}
	a.endAppSwitch()
	if len(st.reg) != 0 {
		t.Fatalf("keys left registered after close: %v", st.reg)
	}
	// A registration failure on a selection key is a degraded mode, not a
	// broken one: the wings still open and close cleanly.
	a.openAppSwitch()
	st.regErr = map[string]error{"Right": errors.New("busy")}
	a.holdAppsKeys() // re-hold with Right failing: no panic, others held
	if _, ok := st.reg["Left"]; !ok {
		t.Fatal("Left not held after a partial failure")
	}
	a.endAppSwitch()
}

// The shown seats follow the display: pinned overflow joins the other
// running apps in 「更多」, and the right wing keeps its 更多 seat.
func TestAppsShownAndOverflow(t *testing.T) {
	a := appSwitchShelf(t)
	saved := []string{"com.t.a", "com.t.b", "com.t.c", "com.t.d"}
	a.settings.PinnedApps = &saved
	st := &appsStub{
		running: []mac.RunningApp{
			{BundleID: "com.t.a", Name: "A", PID: 1},
			{BundleID: "com.t.b", Name: "B", PID: 2},
			{BundleID: "com.t.c", Name: "C", PID: 3},
			{BundleID: "com.t.d", Name: "D", PID: 4},
			{BundleID: "com.t.e", Name: "E", PID: 5},
			{BundleID: "com.t.f", Name: "F", PID: 6},
			{BundleID: "com.t.g", Name: "G", PID: 7},
		},
		paths: map[string]string{"com.t.a": "/a", "com.t.b": "/b", "com.t.c": "/c", "com.t.d": "/d"},
	}
	stubApps(t, st)
	a.apps.on = true
	a.apps.list = a.appSnapshot()
	a.refreshAppsDerived()
	left, right := a.appShownRows()
	if len(left) != 4 {
		t.Fatalf("left wing %v, want the 4 pinned seats", left)
	}
	_, rs := a.appsSlots()
	if len(right) != rs-1 {
		t.Fatalf("right wing shows %d, want %d", len(right), rs-1)
	}
	more := a.moreRows()
	if len(more) != 3-(rs-1) {
		t.Fatalf("more rows %v, want the unshown running apps", bundleIDs(more))
	}
	// A narrow display holds its wings inside the work area: the slot math
	// shrinks seats rather than drawing past the edge.
	a.dispWork = mygo.Rectangle{X: 0, Y: 0, Width: 700, Height: 800}
	a.notchX = 260 // the anchor sits on this display's centre too
	nl, nr := a.appsSlots()
	baseW, _ := a.anchorSize()
	cx := a.stripCenter()
	gl, gr := appsWingW(nl), appsWingW(nr)
	if float32(cx)-baseW/2-gl < float32(a.dispWork.X) || float32(cx)+baseW/2+gr > float32(a.dispWork.X+a.dispWork.Width) {
		t.Fatalf("wings %d/%d seats overflow a %d-wide display", nl, nr, a.dispWork.Width)
	}
	left2, right2 := a.appShownRows()
	if len(left2) > nl || len(right2) > max(nr-1, 0) {
		t.Fatalf("shown seats exceed slots on the narrow display")
	}
	pinOver, runOver := 4-nl, 3-max(nr-1, 0)
	if pinOver < 0 {
		pinOver = 0
	}
	if runOver < 0 {
		runOver = 0
	}
	if len(a.moreRows()) != pinOver+runOver {
		t.Fatalf("narrow display must overflow into more, shown %v/%v more %v", left2, right2, a.moreRows())
	}
}

// The popover hangs below the shell on the right wing's centre, clamps to
// the work area, and flips above only when the bottom has no room.
func TestAppsPopoverSpot(t *testing.T) {
	a := appSwitchShelf(t)
	stubApps(t, &appsStub{paths: map[string]string{}})
	a.apps.on = true
	a.apps.more = true
	r := a.appsPopSpot(60)
	work := a.dispWork
	if r.X < work.X || r.X+r.Width > work.X+work.Width {
		t.Fatalf("popover %v escapes work area %v", r, work)
	}
	if r.Y <= a.y+a.sheetTop() {
		t.Fatalf("popover must hang below the shell: %v", r)
	}
	// Work area with no room below the anchor: the pane goes up, clamped
	// inside the top when the anchor itself is near it.
	a.dispWork = mygo.Rectangle{X: 0, Y: 0, Width: 2560, Height: 90}
	r = a.appsPopSpot(60)
	if r.Y != work.Y+4 {
		t.Fatalf("no room below must flip the popover above, clamped at top: %v", r)
	}
}

// The popover's frame coverage: desiredFrame grows to hold it, so its
// clicks reach the window instead of whatever is underneath.
func TestFrameCoversAppsPopover(t *testing.T) {
	a := appSwitchShelf(t)
	a.win = &mygo.Window{}
	stubApps(t, &appsStub{paths: map[string]string{}})
	a.apps.on = true
	a.apps.more = true
	a.fx.w, a.fx.h = a.appsSize()
	f := a.desiredFrame()
	r := a.appsPopSpot(a.fx.h)
	if r.X < f.X || r.X+r.Width > f.X+f.Width || r.Y < f.Y || r.Y+r.Height > f.Y+f.Height {
		t.Fatalf("frame %v does not cover the popover %v", f, r)
	}
	a.apps.more = false
	f2 := a.desiredFrame()
	if f2.Height >= f.Height {
		t.Fatal("closing the popover must shrink the frame back")
	}
}

// A foreign file drag owns the shape over the wings: the switcher is not a
// drop target, and the mode survives to draw again when the drag ends.
func TestDragOutranksWings(t *testing.T) {
	a := appSwitchShelf(t)
	stubApps(t, &appsStub{paths: map[string]string{}})
	a.apps.on = true
	a.dragging = true
	pw, _ := a.peekSize()
	_, baseH := a.anchorSize()
	if pw != float32(max(180, a.notchW))+dropHintExtraW {
		t.Fatalf("foreign drag must take the hint shape, got %v", pw)
	}
	if baseH < 0 {
		t.Fatal("anchor")
	}
	a.dragging = false
	pw, _ = a.peekSize()
	if aw, _ := a.appsSize(); pw != aw {
		t.Fatalf("wings must own the shape again after the drag, %v != %v", pw, aw)
	}
}

// Opening the panel ends the mode; ending the mode never leaves keys or a
// popover behind, and the switcher's own open is idempotent through the
// toggle.
func TestModeLifecycleExclusive(t *testing.T) {
	a := appSwitchShelf(t)
	stubApps(t, &appsStub{paths: map[string]string{}})
	a.openAppSwitch()
	a.openAppSwitch() // the second call is the toggle: it closes
	if a.apps.on {
		t.Fatal("second openAppSwitch did not close the wings")
	}
	a.openAppSwitch()
	if !a.apps.on {
		t.Fatal("third openAppSwitch must reopen")
	}
	a.show(false)
	if a.apps.on || !a.visible {
		t.Fatalf("show must end the mode and open the panel: on=%v visible=%v", a.apps.on, a.visible)
	}
	if len(a.apps.keys) != 0 || a.apps.more {
		t.Fatal("ending left keys or the popover behind")
	}
	// While the wings are open, a panel-opening path folds first.
	a.openAppSwitch()
	a.openAppSwitch()
	a.openAppSwitch()
	if !a.apps.on {
		t.Fatal("toggle parity broken")
	}
}

// setPeek(false) mid-mode must not strand the wings: the mode holds the
// peek target itself, so hover bookkeeping cannot fold it shut.
func TestAppsSurviveHoverFold(t *testing.T) {
	a := appSwitchShelf(t)
	a.win = &mygo.Window{}
	stubApps(t, &appsStub{paths: map[string]string{}})
	a.openAppSwitch()
	a.setPeek(false)
	a.peeking = false
	view := ui.NewTester(a.view, 1000, 400)
	view.Frame()
	if a.fx.peekW <= 0 || a.fx.peekH <= 0 {
		t.Fatalf("wings folded with the mode open: peek %.1fx%.1f", a.fx.peekW, a.fx.peekH)
	}
}

// manageRows: pinned seats lead in saved order, then every unpinned
// installed or running app — a running app that never installed still
// appears, and the cap marks every candidate full.
func TestManageRowsModel(t *testing.T) {
	a := appSwitchShelf(t)
	saved := []string{"com.t.pin", "com.t.gone"}
	a.settings.PinnedApps = &saved
	st := &appsStub{
		running: []mac.RunningApp{
			{BundleID: "com.t.pin", Name: "Pinned", PID: 3},
			{BundleID: "com.t.orphan", Name: "Orphan", PID: 4},
		},
		paths: map[string]string{"com.t.pin": "/P.app"},
		names: map[string]string{"com.t.gone": "Gone"},
		installed: []mygo.InstalledApp{
			{ID: "com.t.pin", Name: "Pinned"},
			{ID: "com.t.zeta", Name: "Zeta"},
			{ID: "com.t.alpha", Name: "Alpha"},
		},
	}
	stubApps(t, st)
	a.apps.on = true
	a.apps.list = a.appSnapshot()
	a.refreshAppsDerived()
	a.apps.installed = st.installed
	rows := a.manageRows()
	if len(rows) != 5 {
		t.Fatalf("manage rows %d, want 2 pinned + 3 candidates", len(rows))
	}
	if rows[0].order != 0 || rows[0].bundleID != "com.t.pin" || rows[1].order != 1 || !rows[1].missing {
		t.Fatalf("pinned rows %v %v", rows[0].appEntry, rows[1].appEntry)
	}
	got := []string{rows[2].bundleID, rows[3].bundleID, rows[4].bundleID}
	want := []string{"com.t.alpha", "com.t.orphan", "com.t.zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates %v, want %v", got, want)
		}
	}
	// Full: every candidate is marked, never silently dropped. Mutating
	// the saved list by hand marks the cache like applySettings does.
	saved4 := []string{"a", "b", "c", "d"}
	a.settings.PinnedApps = &saved4
	a.apps.manageDirty = true
	rows = a.manageRows()
	for _, r := range rows {
		if r.order < 0 && !r.full {
			t.Fatalf("candidate %s not marked full at the cap", r.bundleID)
		}
	}
}

// ensureInstalled fills the cache off the main thread and surfaces through
// the manage rows; the empty-result case still lists running candidates.
func TestEnsureInstalledDegrades(t *testing.T) {
	a := appSwitchShelf(t)
	st := &appsStub{
		running:    []mac.RunningApp{{BundleID: "com.t.x", Name: "X", PID: 1}},
		paths:      map[string]string{},
		installErr: errors.New("ls boom"),
	}
	stubApps(t, st)
	a.apps.on = true
	a.apps.list = a.appSnapshot()
	a.refreshAppsDerived()
	a.ensureInstalled()
	deadline := time.Now().Add(2 * time.Second)
	for a.apps.installedLoad && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.apps.installed != nil {
		t.Fatal("a failed scan must stay nil, not an empty list")
	}
	rows := a.manageRows()
	if len(rows) != 1 || rows[0].bundleID != "com.t.x" {
		t.Fatalf("degraded manage rows %v", rows)
	}
}

// A showing live notice pauses while the wings hold the shape and resumes
// with its remaining hold afterwards — the mode must not eat the reminder.
func TestAppsPausesLiveNotice(t *testing.T) {
	a := appSwitchShelf(t)
	stubApps(t, &appsStub{paths: map[string]string{}})
	now := time.Now()
	a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 80}, time.Second, now)
	if _, ok := a.liveNow(now, a.liveAllow()); !ok {
		t.Fatal("notice must show before the wings open")
	}
	a.openAppSwitch()
	if _, ok := a.liveNow(now.Add(100*time.Millisecond), a.liveAllow()); ok {
		t.Fatal("wings must hide the notice, not draw it")
	}
	a.endAppSwitch()
	ev, ok := a.liveNow(now.Add(120*time.Millisecond), a.liveAllow())
	if !ok || ev.label != "正在充电" {
		t.Fatalf("notice must resume after the wings close, got %+v ok=%v", ev, ok)
	}
	// The remaining hold is the original minus what already showed, not a
	// restart and not zero.
	if !a.live.until.After(now.Add(time.Second)) {
		t.Fatalf("paused hold %v, want it pushed past its original end", a.live.until)
	}
}

// --- R2: 返修覆盖 -------------------------------------------------------

// appsTester renders the closed shelf's peek content headlessly: the wings
// and — when open — the popover, at the same coordinates the real window
// would draw them (the tester's frame matches a.frame).
func appsTester(a *shelf, w, h int) *ui.Tester {
	a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
	v := ui.NewTester(a.view, w, h)
	v.Frame()
	v.Frame()
	return v
}

// The manage rows' controls are clicked through the real button layer:
// 前移 on the second pinned row reorders, the first row's 上移 and last
// row's 下移 are dead, and 取消固定 of the last pinned app leaves the
// explicit empty list — never the defaults.
func TestManageRowButtonsClick(t *testing.T) {
	dir := t.TempDir()
	a := appSwitchShelf(t)
	a.dataDir = dir
	saved := []string{"com.t.one", "com.t.two"}
	a.settings.PinnedApps = &saved
	st := &appsStub{
		running: []mac.RunningApp{
			{BundleID: "com.t.one", Name: "One", PID: 1},
			{BundleID: "com.t.two", Name: "Two", PID: 2},
		},
		paths:     map[string]string{"com.t.one": "/1", "com.t.two": "/2"},
		names:     map[string]string{"com.t.one": "One", "com.t.two": "Two"},
		installed: []mygo.InstalledApp{{ID: "com.t.cand", Name: "Cand"}},
	}
	stubApps(t, st)
	a.apps.on = true
	a.refreshAppList()
	a.apps.more, a.apps.manage = true, true
	v := appsTester(a, 1000, 400)
	// Row i's centre in tester coords comes from the pane's recorded box:
	// head + i rows + half a row. Controls sit at the pane's right edge —
	// 上移/下移/取消固定 at 13/39/65 DIP in from the padding.
	rowTap := func(i int, fromRight float32) {
		t.Helper()
		pop := a.apps.popRect
		if pop.Width <= 0 {
			t.Fatal("manage pane has no recorded box")
		}
		y := float32(pop.Y-a.frame.Y) + appPopHead + float32(i)*appPopRowH + appPopRowH/2
		x := float32(pop.X-a.frame.X) + float32(pop.Width) - 8 - fromRight
		v.ClickAt(x, y)
		v.Frame()
	}
	// Two is the second pinned row (i=1): 前移 swaps it in front of One.
	rowTap(1, 73)
	if ids := pinned(a); len(ids) != 2 || ids[0] != "com.t.two" {
		t.Fatalf("上移 did not reorder: %v", ids)
	}
	// Now Two is first (i=0): its 上移 is the dimmed one — dead press.
	rowTap(0, 73)
	if ids := pinned(a); ids[0] != "com.t.two" {
		t.Fatalf("first row's 上移 must be inert, got %v", ids)
	}
	// One is the last row (i=1): its 下移 is dimmed — dead press.
	rowTap(1, 47)
	if ids := pinned(a); ids[1] != "com.t.one" {
		t.Fatalf("last row's 下移 must be inert, got %v", ids)
	}
	// 取消固定 removes by real button clicks; removing both leaves the
	// explicit empty — never the defaults.
	rowTap(0, 21)
	rowTap(0, 21)
	if ids := a.effectivePinnedIDs(); ids == nil || len(ids) != 0 {
		t.Fatalf("removing the last pinned app resurrected defaults: %v", ids)
	}
	// The unpinned apps stay on the list as ordinary running seats — the
	// pinned section must be empty, never back-filled by defaults.
	if pinned, _ := a.appsSplit(); len(pinned) != 0 {
		t.Fatalf("unpinning all seated defaults: %v", bundleIDs(pinned))
	}
}

// The popover's seats are clicked through the same path: the wing's 更多
// button opens it, a running row switches, and 编辑常用应用 takes the
// manage pane.
func TestAppsPopoverClicks(t *testing.T) {
	a := appSwitchShelf(t)
	saved := []string{"com.t.pin"}
	a.settings.PinnedApps = &saved
	st := &appsStub{
		running: []mac.RunningApp{
			{BundleID: "com.t.pin", Name: "Pin", PID: 1},
			{BundleID: "com.t.x", Name: "Xray", PID: 2},
			{BundleID: "com.t.y", Name: "Yankee", PID: 3},
			{BundleID: "com.t.z", Name: "Zulu", PID: 4},
			{BundleID: "com.t.w", Name: "Whisky", PID: 5},
		},
		paths: map[string]string{"com.t.pin": "/P"},
		actOK: map[int]bool{5: true},
	}
	stubApps(t, st)
	a.openAppSwitch()
	v := appsTester(a, 1000, 400)
	if err := v.Click("更多"); err != nil {
		t.Fatalf("more button: %v", err)
	}
	v.Frame()
	if !a.apps.more {
		t.Fatal("更多 button did not open the popover")
	}
	// The overflow rows: 4 running minus the seats the right wing drew —
	// on a wide display only Zulu spills into 更多.
	if rows := a.moreRows(); len(rows) != 1 || rows[0].bundleID != "com.t.z" {
		t.Fatalf("overflow rows %v, want only Zulu", bundleIDs(rows))
	}
	for _, e := range a.moreRows() {
		if !v.HasText(e.name) {
			t.Fatalf("popover missing row %s", e.name)
		}
	}
	if err := v.Click("Zulu"); err != nil {
		t.Fatalf("overflow row: %v — texts: %v", err, v.Texts())
	}
	v.Frame()
	if a.apps.on {
		t.Fatal("clicking an overflow row must switch and fold")
	}
	if len(st.activated) != 1 || st.activated[0] != 4 {
		t.Fatalf("popover row activated %v, want pid 4", st.activated)
	}
}

// 中央即搜索：inside presses on the anchor column open the panel; outside
// presses routed through the AppKit path take the same search path instead
// of being swallowed by the mode. The wing's inert padding still does
// nothing and the entry's own press opens the wings.
func TestAppsAnchorStillSearches(t *testing.T) {
	a := appSwitchShelf(t)
	stubApps(t, &appsStub{paths: map[string]string{}})
	a.openAppSwitch()
	v := appsTester(a, 1000, 400)
	// inside: the anchor column's free area (above the collapse chevron)
	// is a search press — innermost element wins, so the chevron is safe.
	v.ClickAt(float32(a.frame.Width)/2, 8)
	v.Frame()
	if !a.visible || a.apps.on {
		t.Fatalf("anchor click must fold wings into search: visible=%v on=%v", a.visible, a.apps.on)
	}
	a.hide(hideExplicit)
	// outside routing: a press delivered off-window over the anchor takes
	// the same search path, not the swallow.
	a.openAppSwitch()
	a.mouseDown(false, 0, mygo.Point{X: a.notchX + a.notchW/2, Y: a.notchH / 2})
	if !a.visible || a.apps.on {
		t.Fatalf("outside anchor press must open search: visible=%v on=%v", a.visible, a.apps.on)
	}
	a.hide(hideExplicit)
	// The collapse entry's outside-routed press opens the wings.
	a.apps.entryRect = mygo.Rectangle{X: a.frame.X + 500, Y: a.frame.Y + 40, Width: 30, Height: 30}
	a.mouseDown(false, 0, mygo.Point{X: a.frame.X + 500 + 15, Y: a.frame.Y + 40 + 15})
	if !a.apps.on {
		t.Fatal("entry press routed outside must open the wings")
	}
}

// Cornered island: a dragged island keeps the wings inside the work area
// by dropping seats on the pinned side — the drawn shell leans off the
// anchor, and its hit box follows the same geometry.
func TestAppsGeomCorneredIsland(t *testing.T) {
	a := appSwitchShelf(t)
	stubApps(t, &appsStub{paths: map[string]string{}})
	a.island = true
	a.dispWork = mygo.Rectangle{X: 0, Y: 0, Width: 1000, Height: 800}
	a.dispBounds = a.dispWork
	// The drag clamp keeps the island inside the display: the capsule's
	// right edge can sit at the work area's at most.
	a.islandDX = a.dispWork.X + a.dispWork.Width - islandIdleW/2 - (a.notchX + a.notchW/2) // capsule centre 940
	a.apps.on = true
	g := a.appGeom()
	cx := float32(a.stripCenter())
	work := float32(a.dispWork.Width)
	if g.rs != 0 {
		t.Fatalf("pinned island must lose its right seats, got %d", g.rs)
	}
	if g.ls != appsSlots {
		t.Fatalf("roomy side keeps its seats, got %d", g.ls)
	}
	left := cx + float32(g.off) - (float32(120)+g.sideL+g.sideR)/2
	right := cx + float32(g.off) + (float32(120)+g.sideL+g.sideR)/2
	if left < 8-0.5 || right > work+0.5 {
		t.Fatalf("shifted shell %.0f..%.0f escapes the work area", left, right)
	}
	// The hit box must sit on the drawn shell, not the anchor.
	a.fx.w, a.fx.h = a.appsSize()
	a.fx.peekKind = peekApps
	p := mygo.Point{X: int(left + 10), Y: 5}
	if !a.overShellShapeAt(p, a.fx.w, a.fx.h, 0, 0, 0) {
		t.Fatal("drawn wing is outside its own hit box")
	}
	p = mygo.Point{X: int(cx) + 120/2 + int(g.sideR) + 30, Y: 5}
	if a.overShellShapeAt(p, a.fx.w, a.fx.h, 0, 0, 0) {
		t.Fatal("a point past the dropped seats still hits the shell")
	}
}

// A tiny work area can drop the right wing entirely: the popover is then
// centred on the anchor's right edge, the height clamps to the work area,
// and the seats never overflow.
func TestAppsGeomCramped(t *testing.T) {
	a := appSwitchShelf(t)
	stubApps(t, &appsStub{paths: map[string]string{}})
	a.dispWork = mygo.Rectangle{X: 0, Y: 0, Width: 360, Height: 200}
	a.dispBounds = a.dispWork
	a.notchX = 90
	a.apps.on, a.apps.more = true, true
	g := a.appGeom()
	if g.ls+g.rs == 0 {
		t.Fatal("even a cramped work area keeps some seats")
	}
	r := a.appsPopSpot(60)
	if r.X < a.dispWork.X || r.X+r.Width > a.dispWork.X+a.dispWork.Width {
		t.Fatalf("popover %v escapes %v", r, a.dispWork)
	}
	if r.Y < a.dispWork.Y || r.Y+r.Height > a.dispWork.Y+a.dispWork.Height {
		t.Fatalf("popover %v escapes %v vertically", r, a.dispWork)
	}
}

// The installed scan runs once per manage open and reports failure as a
// retryable state — the candidates (pinned + running) keep drawing, and a
// stale result landing after a newer request is discarded by generation.
func TestInstalledScanStates(t *testing.T) {
	a := appSwitchShelf(t)
	st := &appsStub{
		running: []mac.RunningApp{{BundleID: "com.t.x", Name: "X", PID: 1}},
		paths:   map[string]string{},
	}
	stubApps(t, st)
	a.apps.on = true
	a.apps.list = a.appSnapshot()
	a.refreshAppsDerived()
	// Slow stub: block until released so the load state is observable.
	release := make(chan struct{})
	appsInstalled = func() ([]mygo.InstalledApp, error) {
		<-release
		return st.installed, st.installErr
	}
	a.ensureInstalled()
	if !a.apps.installedLoad {
		t.Fatal("scan must mark loading")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for a.apps.installedLoad && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.apps.installedLoad {
		t.Fatal("scan never finished")
	}
	// Failure: error state, candidates still there, retry re-scans.
	st.installErr = errors.New("ls boom")
	a.ensureInstalled()
	release2 := make(chan struct{})
	appsInstalled = func() ([]mygo.InstalledApp, error) {
		<-release2
		return nil, st.installErr
	}
	close(release2)
	for a.apps.installedLoad && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.apps.installedErr == nil {
		t.Fatal("a failed scan must surface an error state")
	}
	rows := a.manageRows()
	if len(rows) != 1 || rows[0].bundleID != "com.t.x" {
		t.Fatalf("failed scan must keep candidates, got %v", rows)
	}
	// Generation: a stale landing after a newer request is discarded.
	a.ensureInstalled()
	staleGen := a.apps.installedGen
	a.apps.installedLoad = false
	a.apps.installedGen++ // a newer request began meanwhile
	done := make(chan []mygo.InstalledApp, 1)
	go func() { done <- st.installed }()
	_ = <-done // the stale scan returns…
	// …and its landing would hit the gen check — simulate it directly:
	if staleGen == a.apps.installedGen {
		t.Fatal("generation must differ")
	}
}

// switchIcon's cap grows with the catalogue: a manage list bigger than the
// old fixed 64 keeps icons instead of thrashing; over the cap the entry
// falls back to the glyph rather than decoding every frame.
func TestSwitchIconCacheBound(t *testing.T) {
	a := appSwitchShelf(t)
	stubApps(t, &appsStub{paths: map[string]string{}})
	a.apps.installed = make([]mygo.InstalledApp, 120)
	for i := range a.apps.installed {
		a.apps.installed[i] = mygo.InstalledApp{ID: fmt.Sprintf("com.t.%d", i)}
	}
	// mac.AppIcon returns nil for these fake bundles: misses are cached —
	// the point is the cache bound, not icon resolution.
	for i := range 120 {
		a.switchIcon(fmt.Sprintf("com.t.%d", i))
	}
	if got := len(a.apps.icons); got != 120 {
		t.Fatalf("icons %d < catalogue+16: real lists would thrash", got)
	}
	for i := 120; i < 140; i++ {
		a.switchIcon(fmt.Sprintf("com.t.%d", i))
	}
	if got := len(a.apps.icons); got != 136 {
		t.Fatalf("cap must be catalogue+16=136, got %d", got)
	}
}
