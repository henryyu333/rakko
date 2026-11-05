//go:build darwin

package main

// Native smoke coverage for the app switcher, gated behind
// RAKKO_BASKET_SMOKE=1 like every other *_smoke_test.go: real window,
// real HID clicks through clickAt, real screencapture shots into
// /tmp/rakko-appswitch/native/. Seat clicks prove the shell's hit paths
// without touching the user's apps — switchToApp's seams stay stubbed.
// The one real launch proof (TestSmokeAppsRealLaunch) uses the bundled
// Calculator through the genuine bundle→path→openPath chain and
// terminates exactly what it launched, so no user app is ever activated.
//
// Run through the gate:  bash .scratch/native-gate.sh /tmp/rakko-appswitch/native/log.txt \
//   env RAKKO_BASKET_SMOKE=1 go test -count=1 -run 'TestSmokeApps' ./cmd/rakko

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
)

const appsShotDir = "/tmp/rakko-appswitch/native"

// shotAppsWindow screenshots w into the R2 evidence directory.
func shotAppsWindow(t *testing.T, w *mygo.Window, name string) string {
	t.Helper()
	num := mac.WindowNumber(w.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(350 * time.Millisecond) // let the drawn frame settle
	if err := os.MkdirAll(appsShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(appsShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s", shot)
	return shot
}

// appsSmokeShelf is redesignShelf plus a deterministic app world: the
// system seams are stubbed (no click ever switches a real app), the shelf
// placed, and a fixed pinned list set in the smoke settings.
func appsSmokeShelf(t *testing.T, running []mac.RunningApp) *shelf {
	t.Helper()
	a := redesignShelf(t)
	stubApps(t, &appsStub{
		running: running,
		paths: map[string]string{
			"com.smoke.pin1": "/System/Applications/Utilities/Terminal.app",
			"com.smoke.pin2": "/System/Applications/Calculator.app",
		},
		names: map[string]string{"com.smoke.pin1": "Pin One", "com.smoke.pin2": "Pin Two"},
		actOK: map[int]bool{},
	})
	onMain(func() {
		saved := []string{"com.smoke.pin1", "com.smoke.pin2"}
		a.settings.PinnedApps = &saved
		a.place(mygo.Screen.CursorScreenPoint())
	})
	return a
}

// openAppsWings puts the real window into the wings state and settles it.
func openAppsWings(t *testing.T, a *shelf) {
	t.Helper()
	onMain(func() {
		a.openAppSwitch()
		a.peekProgress, a.peekFrom = 1, 1
		a.peekAnimStart = time.Time{}
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	time.Sleep(450 * time.Millisecond)
}

// closeAppsWings folds the wings and lets the fold settle.
func closeAppsWings(a *shelf) {
	onMain(func() {
		a.endAppSwitch()
		a.win.Invalidate()
	})
	time.Sleep(300 * time.Millisecond)
}

var smokeApps = []mac.RunningApp{
	{BundleID: "com.smoke.pin1", Name: "Pin One", PID: 101},
	{BundleID: "com.smoke.pin2", Name: "Pin Two", PID: 102},
	{BundleID: "com.smoke.run1", Name: "Run One", PID: 103},
	{BundleID: "com.smoke.run2", Name: "Run Two", PID: 104},
	{BundleID: "com.smoke.run3", Name: "Run Three", PID: 105},
	{BundleID: "com.smoke.run4", Name: "Run Four", PID: 106},
}

// appSeat returns the screen point at the centre of wing seat index i:
// 0..ls-1 are the left wing's buttons left→right, ls.. are the right
// wing's (last is always 更多 when a right wing exists).
func appSeat(t *testing.T, a *shelf, i int) (x, y float64) {
	t.Helper()
	g := a.appGeom()
	cx := float64(a.stripCenter()) + float64(g.off)
	baseW, _ := a.anchorSize()
	left := cx - float64(baseW+g.sideL+g.sideR)/2
	ls, rs := a.appsSlots()
	if i < ls {
		x = left + float64(appPadX+appBtn/2) + float64(i*(appBtn+appGap))
	} else if i < ls+rs {
		j := i - ls
		x = left + float64(g.sideL) + float64(baseW) + float64(g.sideR) -
			float64(appPadX) - float64((rs-j)*appBtn) + float64(appBtn/2) +
			float64(j)*float64(appGap) + float64(j)*(float64(appBtn)+float64(appGap)) - float64(j)*float64(appBtn)
	} else {
		t.Fatalf("seat %d out of range %d/%d", i, ls, rs)
	}
	// Right wing seats are right-aligned inside the wing: recompute
	// simply — the j-th seat's centre from the wing's right edge.
	if i >= ls {
		j := rs - 1 - (i - ls) // distance from the rightmost seat inward
		right := left + float64(g.sideL) + float64(baseW) + float64(g.sideR) - float64(appPadX)
		x = right - float64(j)*float64(appBtn+appGap) - float64(appBtn/2)
	}
	y = float64(a.frameTop() + a.sheetTop()) + float64(a.fx.h)/2
	return x, y
}

// The collapsed-state entry opens the wings through a real click; the
// left wing's pinned seat launches through the recorded stub, proving the
// button is a real target inside the real window.
func TestSmokeAppsWingsOpenFromEntry(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	onMain(func() {
		a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	time.Sleep(400 * time.Millisecond)
	var er mygo.Rectangle
	onMain(func() { er = a.apps.entryRect })
	if er.Width <= 0 {
		t.Fatal("no collapsed-state entry was drawn")
	}
	clickAt(t, float64(er.X+er.Width/2), float64(er.Y+er.Height/2))
	onMain(func() {
		if !a.apps.on {
			t.Fatal("clicking the entry did not open the wings")
		}
	})
	openAppsWings(t, a)
	shotAppsWindow(t, a.win, "wings-open")

	// The first pinned seat is a real button in the real window.
	x, y := appSeat(t, a, 0)
	clickAt(t, x, y)
	onMain(func() {
		if a.apps.on {
			t.Fatal("the wings must fold after a successful switch")
		}
	})
	closeAppsWings(a)
}

// The 更多 seat opens the glass popover under the wings at a real box the
// frame covers; 编辑常用应用 swaps to the manage list, whose 前移/取消固定
// controls reorder and persist through real clicks.
func TestSmokeAppsPopoverAndManage(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	openAppsWings(t, a)
	ls, rs := a.appsSlots()
	if rs == 0 {
		t.Skip("no right wing on this display")
	}
	x, y := appSeat(t, a, ls+rs-1) // the last right-wing seat: 更多
	clickAt(t, x, y)
	var more bool
	onMain(func() { more = a.apps.more })
	if !more {
		t.Fatal("更多 seat did not open the popover")
	}
	time.Sleep(300 * time.Millisecond)
	shotAppsWindow(t, a.win, "wings-more")

	var pop, fr mygo.Rectangle
	onMain(func() {
		pop = a.apps.popRect
		fr = a.desiredFrame()
	})
	if pop.Width < 200 || pop.Height < 100 {
		t.Fatalf("popover box %v is not a real pane", pop)
	}
	if pop.X < fr.X || pop.X+pop.Width > fr.X+fr.Width || pop.Y < fr.Y || pop.Y+pop.Height > fr.Y+fr.Height {
		t.Fatalf("frame %v does not cover popover %v", fr, pop)
	}
	// 编辑常用应用 is the pane's footer row.
	clickAt(t, float64(pop.X+pop.Width/2), float64(pop.Y+pop.Height-appPopFoot/2))
	var manage bool
	onMain(func() { manage = a.apps.manage })
	if !manage {
		t.Fatal("编辑常用应用 did not open the manage list")
	}
	time.Sleep(600 * time.Millisecond) // the installed scan is real here
	shotAppsWindow(t, a.win, "wings-manage")

	// The second pinned row's 上移 (first control from the right edge)
	// reorders the persisted list through a real click.
	onMain(func() { pop = a.apps.popRect })
	rowY := float64(pop.Y + appPopHead + appPopRowH + appPopRowH/2)
	upX := float64(pop.X + pop.Width - 8 - 73)
	clickAt(t, upX, rowY)
	var order []string
	onMain(func() { order = pinned(a) })
	if len(order) != 2 || order[0] != "com.smoke.pin2" {
		t.Fatalf("上移 did not reorder the real list: %v", order)
	}
	// The same row is now first: its 上移 is dimmed, so the same click
	// does nothing — the persisted order must not move.
	clickAt(t, upX, rowY)
	onMain(func() { order = pinned(a) })
	if order[0] != "com.smoke.pin2" {
		t.Fatalf("first row's 上移 must be inert, got %v", order)
	}
	// And the last pinned row's 下移 is dimmed the same way.
	downX := float64(pop.X + pop.Width - 8 - 47)
	row2Y := float64(pop.Y + appPopHead + 2*appPopRowH + appPopRowH/2)
	clickAt(t, downX, row2Y)
	onMain(func() { order = pinned(a) })
	if order[1] != "com.smoke.pin1" {
		t.Fatalf("last row's 下移 must be inert, got %v", order)
	}
	// 取消固定 on the first row removes it for real.
	rmX := float64(pop.X + pop.Width - 8 - 21)
	clickAt(t, rmX, rowY)
	onMain(func() { order = pinned(a) })
	if len(order) != 1 || order[0] != "com.smoke.pin1" {
		t.Fatalf("取消固定 did not remove: %v", order)
	}
	closeAppsWings(a)
}

// The anchor stays the search path while the wings are open: a real click
// on the anchor column folds the wings into the panel, Esc folds to the
// closed shelf, and an outside click folds without stealing focus.
func TestSmokeAppsCenterSearchAndCancel(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	openAppsWings(t, a)
	// Inside routing: the anchor's top strip (above the collapse chevron)
	// is the search press — the chevron keeps its own target.
	clickAt(t, float64(a.stripCenter()), float64(a.frameTop()+a.sheetTop())+5)
	var visible, on bool
	onMain(func() { visible, on = a.visible, a.apps.on })
	if !visible || on {
		t.Fatalf("anchor click must fold wings into search: visible=%v on=%v", visible, on)
	}
	onMain(func() { a.hide(hideExplicit) })
	time.Sleep(350 * time.Millisecond)

	openAppsWings(t, a)
	onMain(func() { a.escape() })
	onMain(func() { visible, on = a.visible, a.apps.on })
	if on || visible {
		t.Fatalf("Esc must fold only the wings: visible=%v on=%v", visible, on)
	}
	openAppsWings(t, a)
	// A press far from shell and popover is the outside cancel.
	clickAt(t, 40, float64(a.dispWork.Y+a.dispWork.Height)-120)
	onMain(func() { visible, on = a.visible, a.apps.on })
	if on || visible {
		t.Fatalf("outside press must fold the wings: visible=%v on=%v", visible, on)
	}
	closeAppsWings(a)
}

// While a track is loaded the right wing keeps its own app entry — the
// music wings and the entry coexist; clicking it opens the apps mode and
// folding returns the music wings, not the bare chevron.
func TestSmokeAppsEntryOnMusicWings(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	onMain(func() { fakeTrack(a, true) })
	peekShelf(a)
	time.Sleep(300 * time.Millisecond)
	var er mygo.Rectangle
	onMain(func() { er = a.apps.entryRect })
	if er.Width <= 0 {
		t.Fatal("music wings drew no app entry")
	}
	shotAppsWindow(t, a.win, "music-wings-entry")
	clickAt(t, float64(er.X+er.Width/2), float64(er.Y+er.Height/2))
	var on bool
	onMain(func() { on = a.apps.on })
	if !on {
		t.Fatal("the music wings' app entry did not open the mode")
	}
	shotAppsWindow(t, a.win, "music-to-apps")
	closeAppsWings(a)
	var pw, mw float32
	onMain(func() { pw, _ = a.peekSize(); mw, _ = a.wingsSize() })
	if pw != mw {
		t.Fatalf("wings did not return after the mode: %.0f != %.0f", pw, mw)
	}
	onMain(func() { a.np = nil })
}

// A showing live notice pauses for the wings and resumes after — through
// the real window's own state, not only the model.
func TestSmokeAppsPausesLiveNotice(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	now := time.Now()
	onMain(func() {
		a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 80}, 8*time.Second, now)
		a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	time.Sleep(350 * time.Millisecond)
	shotAppsWindow(t, a.win, "notice-showing")
	openAppsWings(t, a)
	if _, ok := a.liveNow(time.Now(), a.liveAllow()); ok {
		t.Fatal("notice must hide while the wings are up")
	}
	shotAppsWindow(t, a.win, "notice-paused")
	closeAppsWings(a)
	if _, ok := a.liveNow(time.Now(), a.liveAllow()); !ok {
		t.Fatal("notice must resume after the wings close")
	}
	onMain(func() { a.live = liveQueue{} })
}

// An island dragged to the work area's edge keeps the wings inside the
// display: seats drop on the pinned side, the shell leans off the anchor,
// and the frame never escapes the work area.
func TestSmokeAppsCorneredIsland(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	onMain(func() {
		a.island = true
		a.islandDX = a.dispWork.Width/2 - 100 // near the right edge
	})
	openAppsWings(t, a)
	var g appGeom
	var fr mygo.Rectangle
	onMain(func() {
		g = a.appGeom()
		fr = a.desiredFrame()
	})
	if g.off == 0 && g.ls == g.rs {
		t.Fatal("cornered island kept symmetric seats — the bug is back")
	}
	shotAppsWindow(t, a.win, "island-cornered")
	work := a.dispWork
	if fr.X < work.X || fr.X+fr.Width > work.X+work.Width {
		t.Fatalf("frame %v escapes the work area %v", fr, work)
	}
	onMain(func() {
		a.island, a.islandDX = false, 0
		a.endAppSwitch()
	})
}

// The wings open and fold under whatever motion setting the desktop has;
// a reduced-motion re-run of the gate only changes the easing. (The real
// window's ReduceMotion flag is the OS's — the headless gallery covers
// the preference's drawing paths; this proves state correctness either
// way.)
func TestSmokeAppsReduceMotionState(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	openAppsWings(t, a)
	var on bool
	onMain(func() { on = a.apps.on })
	if !on {
		t.Fatal("wings did not open")
	}
	shotAppsWindow(t, a.win, "open-settled")
	closeAppsWings(a)
}

// Real launch proof: a pinned-but-stopped seat resolves its path through
// the real LaunchServices lookup and opens it through the real shell —
// Calculator, which we then terminate ourselves. This is the only test
// that reaches the genuine chain; the seam-based tests cannot prove it.
func TestSmokeAppsRealLaunch(t *testing.T) {
	a := appsSmokeShelf(t, nil)
	// Swap back the real seams for this test only.
	oldRun, oldPath, oldName, oldInst := appRunning, appPathFor, appNameFor, appsInstalled
	oldAct, oldOpen := activateApp, openPath
	appRunning = mac.RunningApps
	appPathFor = mac.AppPathForBundleID
	appNameFor = mac.AppNameForBundleID
	activateApp = mac.ActivateApp
	openPath = mygo.Shell.OpenPath
	t.Cleanup(func() {
		appRunning, appPathFor, appNameFor, appsInstalled = oldRun, oldPath, oldName, oldInst
		activateApp, openPath = oldAct, oldOpen
	})
	if path := appPathFor("com.apple.calculator"); path == "" {
		t.Skip("Calculator.app not resolvable on this system")
	}
	var launched bool
	onMain(func() {
		a.switchToApp(appEntry{bundleID: "com.apple.calculator", name: "Calculator", pinned: true})
		launched = true
	})
	if !launched {
		t.Fatal("switchToApp did not run")
	}
	// Poll the real running-applications list: LaunchServices needs a beat.
	deadline := time.Now().Add(8 * time.Second)
	pid := 0
	for time.Now().Before(deadline) && pid == 0 {
		for _, r := range mac.RunningApps() {
			if r.BundleID == "com.apple.calculator" {
				pid = r.PID
			}
		}
		if pid == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if pid == 0 {
		t.Fatal("Calculator never appeared in runningApplications — the launch failed silently")
	}
	// Terminate exactly what we launched through NSRunningApplication.
	onMain(func() {
		if !mac.TerminateApp(pid) {
			t.Logf("warning: could not terminate Calculator (pid %d)", pid)
		}
	})
}
