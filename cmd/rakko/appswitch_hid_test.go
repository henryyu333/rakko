//go:build darwin

package main

// 实机点击链路（RakkoSwitchTarget 受控夹具）：生产监控 ticker（animate +
// monitorDrag，与 startMonitoring 同序同参）、真实指针悬停在刘海上、真实
// HID 点击两翼或收起态入口的按钮，最终断言被点应用成为前台
// (FrontmostPID == 夹具 PID)、两翼收起、且无事后切回。快照、图标、激活、
// 按路径启动全部走真实现（mac 包与 LaunchServices），不用 stub；点击沿
// postRealClick 的 warp+严格 own-window 校验。夹具是本仓库
// dist.noindex/switch-fixtures 构建的空白目标应用，结束时按 bundle 退出，
// 不触碰用户任何窗口或应用。
//
// 用 RAKKO_HID_SMOKE=1 门控：拖动真实指针、打断菜单栏热区属于机器级动作，
// 只在 gate runner 准备好的环境跑。

import (
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"rakko/internal/mac"
)

const (
	// hidTargetA/B 是夹具的 bundle id。A 从未运行（点它 = 按路径启动），
	// B 提前启动（点它 = 激活运行中应用），一次用例把两条路径都走到。
	hidTargetA = "local.y3.rakko.switchtest.a"
	hidTargetB = "local.y3.rakko.switchtest.b"
	// 夹具的 quit 哨兵与 pid 文件所在：Swift 夹具 250ms 轮询该哨兵。
	hidFixtures     = "/tmp/rakko-appswitch/fixtures"
	hidQuitSentinel = hidFixtures + "/quit"
	// 冒烟装配由 RAKKO_BASKET_SMOKE=1 建（smokeShelf/真主循环/真窗口），
	// 本用例依赖它，再用自己的门收真实指针动作。父方两个门同时开。
	hidEnv      = "RAKKO_HID_SMOKE"
	hidHarness  = "RAKKO_BASKET_SMOKE"
	hidSettle   = 600 * time.Millisecond
	hidLaunch   = 20 * time.Second
	hidFront    = 12 * time.Second
	hidQuitWait = 8 * time.Second
	hidTick     = 30 * time.Millisecond
)

// hidSkip 把机器级的两道门一起核对：装配门在前，指针门在后。
func hidSkip(t *testing.T) {
	t.Helper()
	if os.Getenv(hidHarness) != "1" || os.Getenv(hidEnv) != "1" {
		t.Skipf("real-pointer smoke skipped: set %s=1 with the smoke harness (%s=1)", hidEnv, hidHarness)
	}
}

// hidFrontPID/hidRunningPID 都在主线程读（mac 包的约定）。回调里不断言。
func hidFrontPID() int {
	if p := mainValue(func() any { return mac.FrontmostPID() }); p != nil {
		return p.(int)
	}
	return 0
}

func hidRunningPID(bundleID string) int {
	if v := mainValue(func() any {
		for _, r := range mac.RunningApps() {
			if r.BundleID == bundleID {
				return r.PID
			}
		}
		return 0
	}); v != nil {
		return v.(int)
	}
	return 0
}

// mainValue ships a read onto the main thread and brings the value back.
func mainValue(fn func() any) any {
	var out any
	onMain(func() { out = fn() })
	return out
}

// hidWaitFrontPID polls until the frontmost PID is pid or the budget ends.
func hidWaitFrontPID(t *testing.T, pid int, what string) {
	t.Helper()
	deadline := time.Now().Add(hidFront)
	for time.Now().Before(deadline) {
		if hidFrontPID() == pid {
			return
		}
		time.Sleep(120 * time.Millisecond)
	}
	a := hidFrontPID()
	b := hidRunningPID(hidTargetA)
	c := hidRunningPID(hidTargetB)
	t.Fatalf("%s: frontmost PID is %d, want %d (A=%d B=%d)", what, a, pid, b, c)
}

// hidWaitRunning polls until the bundle's PID appears in the snapshot.
func hidWaitRunning(t *testing.T, bundleID string) int {
	t.Helper()
	deadline := time.Now().Add(hidLaunch)
	for time.Now().Before(deadline) {
		if pid := hidRunningPID(bundleID); pid != 0 {
			return pid
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s never showed up in the running snapshot", bundleID)
	return 0
}

// hidLaunchFixture opens the fixture by path through the real Shell and
// returns the PID once the snapshot shows it running.
func hidLaunchFixture(t *testing.T, bundleID, path string) int {
	t.Helper()
	if err := mygo.Shell.OpenPath(path); err != nil {
		t.Fatalf("opening %s: %v", bundleID, err)
	}
	return hidWaitRunning(t, bundleID)
}

// hidClearSentinel先删掉本 stash 的 quit 哨兵：上一轮遗留的文件会让夹具
// 一启动就自杀。只碰本哨兵一个文件。
func hidClearSentinel(t *testing.T) {
	t.Helper()
	if err := os.Remove(hidQuitSentinel); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clearing the quit sentinel: %v", err)
	}
}

// hidWriteSentinel 回写哨兵并结合快照等待；这是本轮唯一会结束夹具的
// 通道（不枚举/杀进程），错杀不到别人的同 id。
func hidWriteSentinel(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(hidQuitSentinel, nil, 0o644); err != nil {
		t.Fatalf("writing the quit sentinel: %v", err)
	}
	deadline := time.Now().Add(hidQuitWait)
	for time.Now().Before(deadline) {
		if hidRunningPID(hidTargetA) == 0 && hidRunningPID(hidTargetB) == 0 {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("fixture of this stash outlived the case: A=%d B=%d",
		hidRunningPID(hidTargetA), hidRunningPID(hidTargetB))
}

// hidFixture resolves the fixture's bundle path through the real
// LaunchServices — the same seam a pinned-but-not-running seat uses to
// launch — and fails loudly when fixtures were not built/registered.
func hidFixture(t *testing.T, bundleID string) string {
	t.Helper()
	p := mainValue(func() any { return mac.AppPathForBundleID(bundleID) }).(string)
	if p == "" {
		t.Fatalf("%s not resolvable through LaunchServices — build the switch fixtures first (dist.noindex/switch-fixtures)", bundleID)
	}
	return p
}

// hidTickLoop runs the production monitor on its own goroutine exactly the
// way shelf.startMonitoring does — animate then monitorDrag on the main
// thread, with the real cursor under monitorDrag's own logic. No hand-set
// state; stop closes the loop and waits for the tick that is in flight.
func hidTickLoop(t *testing.T, a *shelf) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		tk := time.NewTicker(hidTick)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-tk.C:
				mygo.RunOnMain(func() {
					a.animate(now)
					a.monitorDrag(now)
				})
			}
		}
	}()
	return func() {
		close(done)
		<-finished // join: the last tick is in or past RunOnMain before we return
	}
}

// hidFrontPID stays the same helper as above; the pointer helpers use the
// shared warpPointerTo (tools_drag_smoke) and mygo's screen API.

// hidPointNow reads the real cursor position through mygo's Screen API.
func hidPointNow() mygo.Point { return mygo.Screen.CursorScreenPoint() }

// hidWindowVisible reports whether the fixture process pid has any on-screen
// window: the Dock-style reopen draws windows even when the app activation
// alone would leave a hidden or minimized process frontmost-none. Normal
// layer only; no screenshot needed. The pid is passed down so the window
// probe keeps only this pid besides our own — no other user windows enter.
func hidWindowVisible(t *testing.T, pid int) bool {
	if pid <= 0 {
		return false
	}
	var ok bool
	onMain(func() { ok = hidEntryVisible(t, pid) })
	return ok
}

// hidEntryVisible runs the CGWindowList probe on the main thread: one
// layer-0 on-screen window of the fixture's pid, at meaningful pixel size.
func hidEntryVisible(t *testing.T, pid int) bool {
	for _, w := range probeWindows(t, pid) {
		if w.OwnerPID == pid && w.Layer == 0 && w.Onscreen {
			return w.Frame.Width > 40 && w.Frame.Height > 40
		}
	}
	return false
}

// probeWindows lists this process's windows plus exactly the controlled
// fixture pids the case is watching — owner/bounds/onscreen only for the
// fixtures, titles only for our own. Run on the main thread only.
func probeWindows(t *testing.T, fixturePIDs ...int) []smokeWindowInfo {
	return smokeWindowList(t, fixturePIDs)
}

// hidWaitWindow spends the visibility budget waiting for a fixture window
// that is actually on screen, at reasonable pixel size.
func hidWaitWindow(t *testing.T, pid int, what string) {
	t.Helper()
	deadline := time.Now().Add(hidFront)
	for time.Now().Before(deadline) {
		if hidWindowVisible(t, pid) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s: no on-screen window of PID %d reached normal layer within the budget", what, pid)
}

// TestHIDAppsClickLaunchesTargets covers both branches of the device chain
// with only production state:
//
//	pointer hover on the notch → peeked chevron with the compact band entry
//	→ HID click on the entry (UI button) → wings open
//	→ HID click on wing seat 0 → fixture A (not running) launches; A lands
//	  at the front (FrontmostPID == A) and the wings fold
//	→ reopen through the band entry again (the pointer is parked there)
//	→ HID click on wing seat 1 → running fixture B activates; B is
//	  frontmost, the wings fold, and nothing hands the focus back.
func TestHIDAppsClickLaunchesTargets(t *testing.T) {
	hidSkip(t)
	// Clear this round's quit sentinel before anything else: a leftover
	// from an earlier round would self-terminate a fixture on launch.
	hidClearSentinel(t)
	// End-of-case: write the sentinel back and wait for both fixtures to
	// leave — no process enumeration or kill anywhere.
	t.Cleanup(func() { hidWriteSentinel(t) })

	// Both fixtures resolve through the real LaunchServices. A might still
	// be up from a previous round: writing the sentinel once, then
	// clearing it again, brings A down before the case starts.
	if pid := hidRunningPID(hidTargetA); pid != 0 {
		t.Logf("A already running (PID %d) from a previous round — dropping it first", pid)
		hidWriteSentinel(t)
		hidClearSentinel(t)
	}
	pathB := hidFixture(t, hidTargetB)
	pidB := hidLaunchFixture(t, hidTargetB, pathB)

	a := redesignShelf(t)
	// The notch window comes up Hidden; an overlay not on screen gates
	// nothing. ShowInactive is production's own这条路 (不夺焦)。
	onMain(func() {
		a.win.SetOpacity(1)
		a.win.ShowInactive()
		a.win.Invalidate()
	})
	// Pin the two fixtures in order from the settings DEFAULTS restored by
	// the harness reset: seat 0 (A, dark) is the launch branch, seat 1
	// (B, running) the activate branch. Only the two fixture ids are
	// written; nothing else in settings is touched by the case.
	onMain(func() {
		order := []string{hidTargetA, hidTargetB}
		a.settings.PinnedApps = &order
		a.refreshAppList()
	})
	// The full production monitor loop: animate + monitorDrag on the real
	// main thread. No stub anywhere from here on.
	stop := hidTickLoop(t, a)
	defer stop()
	onMain(func() { a.place(hidPointNow()) })

	// Park the real pointer on the anchor's hover pad and hold it there:
	// monitorDrag's overAnchor holds the peek on its own.
	old := hidPointNow()
	t.Cleanup(func() { warpPointerTo(t, float64(old.X), float64(old.Y)) })

	seat := hidAnchorPoint(t, a)
	// Rest on the anchor: overAnchor's pad is what holds the peek on a
	// visit, and the parked pointer must still be there for the entry.
	warpPointerTo(t, float64(seat.X), float64(seat.Y))
	waitApps(t, a, "the pointer to wake the peek", func() bool {
		return a.peeking && a.peekProgress > 0.5
	})
	// The collapsed band entry is drawn where the frame says.
	var er mygo.Rectangle
	waitApps(t, a, "the band entry", func() bool {
		er = a.apps.entryRect
		return er.Width > 0 && a.fx.peekKind == peekChevron
	})
	shotAppsWindow(t, a.win, "hid-chev-entry")

	// Real HID click on the entry: nothing in this path bypasses the gate.
	clickOverlayAt(t, a, float64(er.X+er.Width/2), float64(er.Y+er.Height/2))
	var on bool
	waitApps(t, a, "the wings after the entry click", func() bool {
		onMain(func() { on = a.apps.on })
		return on
	})
	waitApps(t, a, "the wings to settle", func() bool { return a.peekProgress >= 0.99 })
	// The production ticket draws two seats: A then B.
	x, y := appSeat(t, a, 0)
	clickOverlayAt(t, a, x, y)
	pidA := hidWaitRunning(t, hidTargetA)
	hidWaitFrontPID(t, pidA, "the launched fixture must be frontmost")
	hidWaitWindow(t, pidA, "the launched fixture must draw a window")
	var folded bool
	onMain(func() { folded = !a.apps.on })
	if !folded {
		t.Fatalf("the wings survived a successful launch (apps.on still true)")
	}
	// The pointer is parked over the shell; the app that just launched is
	// at the front. The case ends without stealing anything back: check
	// the PID is STILL A (no hand-back, no refocus churn).
	time.Sleep(hidSettle)
	if still := hidFrontPID(); still != pidA {
		t.Fatalf("the frontmost PID slipped back to %d after the switch", still)
	}

	// Reopen and take the activate branch. The pointer is still parked on
	// the anchor; the band entry draws again.
	waitApps(t, a, "the entry after fold", func() bool {
		return a.apps.entryRect.Width > 0 && a.fx.peekKind == peekChevron
	})
	var er2 mygo.Rectangle
	onMain(func() { er2 = a.apps.entryRect })
	clickOverlayAt(t, a, float64(er2.X+er2.Width/2), float64(er2.Y+er2.Height/2))
	waitApps(t, a, "the reopen wings", func() bool {
		onMain(func() { on = a.apps.on })
		return on
	})
	waitApps(t, a, "the reopened wings to settle", func() bool { return a.peekProgress >= 0.99 })
	// Seat 1 is B: activating a running fixture.
	x2, y2 := appSeat(t, a, 1)
	clickOverlayAt(t, a, x2, y2)
	hidWaitFrontPID(t, pidB, "the running fixture must be frontmost")
	hidWaitWindow(t, pidB, "the activated fixture must draw a window")
	onMain(func() { folded = !a.apps.on })
	if !folded {
		t.Fatalf("the wings survived a successful activation (apps.on still true)")
	}
	time.Sleep(hidSettle)
	if still := hidFrontPID(); still != pidB {
		t.Fatalf("the frontmost PID slipped back to %d after activation", still)
	}
}

// hidAnchorPoint is the screen point at the centre of the anchor's hover
// zone — the notch for the notched shelf (island smoke uses its own).
func hidAnchorPoint(t *testing.T, a *shelf) mygo.Point {
	t.Helper()
	var cx, cy float64
	onMain(func() {
		cx = float64(a.notchX + a.notchW/2)
		cy = float64(a.notchY + a.notchH + notchHoverPadY/2)
	})
	return mygo.Point{X: int(cx), Y: int(cy)}
}
