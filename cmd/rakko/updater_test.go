//go:build darwin

package main

// Tests for updater.go: the daily schedule (first check after launch,
// 24 h on success, sooner on failure, none while a check or install is in
// flight), the found-version state machine (announce only, install on
// choice, restart entry once swapped) and the tray menu labels. The app
// seams (updateCheck/runInstall/dispatchMain/relaunchApp) are stubbed so
// nothing touches the network, the app bundle or the UI loop.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/settings"
)

// updateProbe replaces the updater seams: checks answer what checkFn says,
// installs run runFn, background work runs inline through goWork, and
// main-thread dispatches are queued on drain for the test to run itself
// (the state they mutate is main-thread owned, so settle() runs it on the
// test goroutine and keeps happens-before).
type updateProbe struct {
	mu       sync.Mutex
	checks   int
	runs     int
	relaunch int
	checkFn  func(context.Context) (*mygo.Update, error)
	runFn    func(context.Context, *mygo.Update, func(int64, int64)) error
	drain    chan func()
	enabled  bool
}

func stubUpdater(t *testing.T) *updateProbe {
	t.Helper()
	p := &updateProbe{drain: make(chan func(), 64), enabled: true}
	origEnabled, origCheck, origRun := updateEnabled, updateCheck, runInstall
	origDispatch, origRelaunch, origWork := dispatchMain, relaunchApp, goWork
	origCheckTO, origInstallTO := updateCheckTimeout, updateInstallTimeout
	updateEnabled = func() bool { return p.enabled }
	updateCheck = func(ctx context.Context) (*mygo.Update, error) {
		p.mu.Lock()
		p.checks++
		p.mu.Unlock()
		if p.checkFn == nil {
			return nil, nil
		}
		return p.checkFn(ctx)
	}
	runInstall = func(ctx context.Context, up *mygo.Update, progress func(int64, int64)) error {
		p.mu.Lock()
		p.runs++
		p.mu.Unlock()
		if p.runFn == nil {
			return nil
		}
		return p.runFn(ctx, up, progress)
	}
	dispatchMain = func(fn func()) { p.drain <- fn }
	relaunchApp = func() { p.mu.Lock(); p.relaunch++; p.mu.Unlock() }
	goWork = func(fn func()) { fn() }
	t.Cleanup(func() {
		updateEnabled, updateCheck, runInstall = origEnabled, origCheck, origRun
		dispatchMain, relaunchApp, goWork = origDispatch, origRelaunch, origWork
		updateCheckTimeout, updateInstallTimeout = origCheckTO, origInstallTO
	})
	return p
}

func (p *updateProbe) checkCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.checks
}

func (p *updateProbe) runCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runs
}

func (p *updateProbe) relaunches() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.relaunch
}

// settle runs every queued main-thread callback, the state transitions a
// finished check or install posts.
func (p *updateProbe) settle() {
	for {
		select {
		case fn := <-p.drain:
			fn()
		default:
			return
		}
	}
}

func updateShelf() *shelf {
	return &shelf{settings: settings.Default()}
}

func TestNextAutoCheck(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.Local)
	if got := nextAutoCheck(now, true); !got.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("successful check rescheduled to %v, want +24h", got)
	}
	if got := nextAutoCheck(now, false); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("failed check rescheduled to %v, want +1h", got)
	}
}

func TestTickUpdatesSchedulesFirstCheck(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.Local)
	// The first tick only arms the launch check; nothing runs yet.
	a.tickUpdates(now)
	if got := a.updateNext; !got.Equal(now.Add(updateFirstDelay)) {
		t.Fatalf("first tick set updateNext=%v, want +%v", got, updateFirstDelay)
	}
	// Before it is due nothing runs.
	a.tickUpdates(now.Add(updateFirstDelay - time.Second))
	if p.checkCount() != 0 {
		t.Fatal("checked before the launch delay")
	}
	// Once due, the check runs (goWork is inline under tests).
	a.tickUpdates(now.Add(updateFirstDelay))
	if p.checkCount() != 1 {
		t.Fatalf("due check ran %d times", p.checkCount())
	}
	p.settle()
	if !a.updateNext.After(now) {
		t.Fatal("finished check did not reschedule")
	}
}

func TestTickUpdatesRespectsSettingAndBusy(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.Local)
	// Setting off: a due slot never checks and never arms updateNext.
	a.settings.AutoCheckUpdates = false
	a.tickUpdates(now)
	a.tickUpdates(now.Add(updateInterval))
	if p.checkCount() != 0 {
		t.Fatal("checked with autoCheckUpdates off")
	}
	// Setting back on: the tick arms the launch delay again.
	a.settings.AutoCheckUpdates = true
	a.tickUpdates(now.Add(updateInterval))
	if a.updateNext.IsZero() {
		t.Fatal("re-enabled but no check scheduled")
	}
	// A check or install in flight blocks the auto check.
	a.updateNext = now.Add(-time.Second)
	a.updateChecking = true
	a.tickUpdates(now)
	a.updateChecking = false
	a.updateInstalling = true
	a.tickUpdates(now)
	a.updateInstalling = false
	if p.checkCount() != 0 {
		t.Fatal("checked while another check or install was in flight")
	}
	// Builds that cannot update never reach the network.
	p.enabled = false
	a.updateNext = now.Add(-time.Second)
	a.tickUpdates(now)
	if p.checkCount() != 0 {
		t.Fatal("checked while updates are disabled for this build")
	}
	if !a.updateNext.After(now) {
		t.Fatal("disabled build did not reschedule")
	}
}

func TestCheckedUpdateFoundWaitsForUser(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	up := &mygo.Update{Version: "1.2.3"}
	a.checkedUpdate(false, up, nil)
	if a.update != up || a.updateInstalled || a.updateInstalling {
		t.Fatal("a found update must only be announced")
	}
	if p.runCount() != 0 {
		t.Fatal("a background check installed on its own")
	}
	if got := a.updateStatus(); !strings.Contains(got, "1.2.3") {
		t.Fatalf("updateStatus = %q, want the version named", got)
	}
	if item := a.updateMenu(); !strings.Contains(item.Label, "安装更新（v1.2.3）") {
		t.Fatalf("menu label = %q", item.Label)
	}
}

func TestCheckedUpdateKeepsInFlightInstall(t *testing.T) {
	stubUpdater(t)
	a := updateShelf()
	old := &mygo.Update{Version: "1.2.3"}
	a.update, a.updateInstalling = old, true
	a.checkedUpdate(false, &mygo.Update{Version: "1.2.4"}, nil)
	if a.update != old {
		t.Fatal("a check replaced the update being installed")
	}
	// An installed version is not re-offered.
	a.updateInstalling, a.updateInstalled = false, true
	a.checkedUpdate(false, &mygo.Update{Version: "1.2.3"}, nil)
	if a.update != old || !a.updateInstalled {
		t.Fatal("a check re-offered the installed version")
	}
}

func TestCheckedUpdateNilAndError(t *testing.T) {
	stubUpdater(t)
	a := updateShelf()
	// Up to date: a stale offer clears; the manual check says so.
	a.update = &mygo.Update{Version: "9.9.9"}
	a.checkedUpdate(true, nil, nil)
	if a.update != nil {
		t.Fatal("a stale offer survived an up-to-date check")
	}
	if !strings.Contains(a.status, "最新") {
		t.Fatalf("manual up-to-date status = %q", a.status)
	}
	// Errors: user checks show the reason, disabled builds explain instead.
	a.checkedUpdate(true, nil, errors.New("offline"))
	if !strings.Contains(a.status, "检查更新失败") {
		t.Fatalf("manual failure status = %q", a.status)
	}
	a.checkedUpdate(true, nil, mygo.ErrUpdatesDisabled)
	if !strings.Contains(a.status, "未启用自动更新") {
		t.Fatalf("disabled-build status = %q", a.status)
	}
	// A background failure stays silent apart from rescheduling.
	a.status = ""
	a.checkedUpdate(false, nil, errors.New("offline"))
	if a.status != "" {
		t.Fatalf("background failure wrote status %q", a.status)
	}
	if !a.updateNext.After(time.Now()) {
		t.Fatal("check did not reschedule")
	}
}

// A stalled feed must not leave updateChecking set forever: the check runs
// under a deadline, and its deadline cancels the request, resets the state
// and schedules the next attempt.
func TestCheckTimeoutResetsState(t *testing.T) {
	p := stubUpdater(t)
	updateCheckTimeout = 40 * time.Millisecond
	a := updateShelf()
	p.checkFn = func(ctx context.Context) (*mygo.Update, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("check ran without a deadline")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	a.checkUpdates(true)
	p.settle()
	if a.updateChecking {
		t.Fatal("timed-out check left updateChecking set")
	}
	if !a.updateNext.After(time.Now()) {
		t.Fatal("timed-out check did not reschedule")
	}
	if !strings.Contains(a.status, "检查更新失败") {
		t.Fatalf("timeout status = %q", a.status)
	}
	// The next check is not blocked.
	p.checkFn = nil
	a.checkUpdates(false)
	p.settle()
	if p.checkCount() != 2 {
		t.Fatalf("checks after timeout = %d, want 2", p.checkCount())
	}
}

func TestInstallUpdateFlow(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	a.update = &mygo.Update{Version: "1.2.3"}
	p.runFn = func(_ context.Context, up *mygo.Update, progress func(int64, int64)) error {
		progress(50, 100)
		progress(100, 100)
		return nil
	}
	a.installUpdate()
	if !a.updateInstalling {
		t.Fatal("install did not start")
	}
	p.settle()
	if !a.updateInstalled || a.updateInstalling {
		t.Fatal("install did not finish into installed state")
	}
	if p.runCount() != 1 {
		t.Fatalf("install ran %d times", p.runCount())
	}
	if item := a.updateMenu(); item.Label != "重新启动 Rakko 完成更新" {
		t.Fatalf("post-install menu = %q", item.Label)
	}
	if !strings.Contains(a.status, "已安装") {
		t.Fatalf("post-install status = %q", a.status)
	}
	// The restart entry relaunches the app.
	a.updateMenu().Click(nil, nil)
	if p.relaunches() != 1 {
		t.Fatal("restart menu item did not relaunch")
	}
	// installUpdate refuses to run twice over an installed update.
	a.installUpdate()
	if p.runCount() != 1 {
		t.Fatal("installed update installed again")
	}
}

// A stalled download must release updateInstalling, keep the offer for a
// retry, and let a later install succeed.
func TestInstallTimeoutResetsState(t *testing.T) {
	p := stubUpdater(t)
	updateInstallTimeout = 40 * time.Millisecond
	a := updateShelf()
	a.update = &mygo.Update{Version: "1.2.3"}
	p.runFn = func(ctx context.Context, _ *mygo.Update, _ func(int64, int64)) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("install ran without a deadline")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	a.installUpdate()
	p.settle()
	if a.updateInstalling || a.updateInstalled {
		t.Fatal("timed-out install left the installing/installed state set")
	}
	if !strings.Contains(a.status, "安装更新失败") {
		t.Fatalf("timeout status = %q", a.status)
	}
	if item := a.updateMenu(); !strings.Contains(item.Label, "安装更新（v1.2.3）") {
		t.Fatalf("post-timeout menu = %q", item.Label)
	}
	// Retrying the same offer works.
	p.runFn = nil
	a.installUpdate()
	p.settle()
	if !a.updateInstalled {
		t.Fatal("retry after the timeout did not install")
	}
}

func TestInstallUpdateFailure(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	a.update = &mygo.Update{Version: "1.2.3"}
	p.runFn = func(context.Context, *mygo.Update, func(int64, int64)) error { return errors.New("disk full") }
	a.installUpdate()
	p.settle()
	if a.updateInstalled || a.updateInstalling {
		t.Fatal("failed install marked installed")
	}
	if !strings.Contains(a.status, "安装更新失败") {
		t.Fatalf("failure status = %q", a.status)
	}
	// The offer stays: the user can retry the same version.
	if item := a.updateMenu(); !strings.Contains(item.Label, "安装更新（v1.2.3）") {
		t.Fatalf("post-failure menu = %q", item.Label)
	}
}

func TestUpdateStatusStates(t *testing.T) {
	a := updateShelf()
	if got := a.updateStatus(); got != "" {
		t.Fatalf("idle footer note = %q, want none", got)
	}
	a.update = &mygo.Update{Version: "1.2.3"}
	if got := a.updateStatus(); !strings.Contains(got, "发现新版本 v1.2.3") {
		t.Fatalf("available note = %q", got)
	}
	a.updateInstalling = true
	if got := a.updateStatus(); !strings.Contains(got, "正在下载更新 v1.2.3") {
		t.Fatalf("installing note = %q", got)
	}
	a.updateProgress = 0.5
	if got := a.updateStatus(); !strings.Contains(got, "50%") {
		t.Fatalf("progress note = %q", got)
	}
	a.updateInstalling, a.updateInstalled = false, true
	if got := a.updateStatus(); !strings.Contains(got, "已安装") || !strings.Contains(got, "重新启动") {
		t.Fatalf("installed note = %q", got)
	}
}

func TestUpdateMenuDefaultStates(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	if item := a.updateMenu(); item.Label != "检查更新…" {
		t.Fatalf("idle menu = %q", item.Label)
	}
	a.updateChecking = true
	if item := a.updateMenu(); item.Label != "正在检查更新…" || !item.Disabled {
		t.Fatalf("checking menu = %q disabled=%v", item.Label, item.Disabled)
	}
	a.updateChecking = false
	a.update, a.updateInstalling = &mygo.Update{Version: "1.2.3"}, true
	a.updateProgress = 0.42
	if item := a.updateMenu(); item.Label != "正在安装更新（v1.2.3 · 42%）" || !item.Disabled {
		t.Fatalf("installing menu = %q disabled=%v", item.Label, item.Disabled)
	}
	// A manual check must not start while checking or installing.
	a.checkUpdates(true)
	if p.checkCount() != 0 {
		t.Fatal("checkUpdates doubled an in-flight check")
	}
}

func TestSetAutoCheckUpdates(t *testing.T) {
	a := updateShelf()
	a.dataDir = t.TempDir()
	// The switch goes through applySettings, so settings.json must hold the
	// new value; turning it back on schedules a check right away.
	a.setAutoCheckUpdates(false)
	if a.settings.AutoCheckUpdates {
		t.Fatal("turning off did not apply")
	}
	loaded, err := settings.Load(a.dataDir)
	if err != nil || loaded.AutoCheckUpdates {
		t.Fatalf("settings.json not saved: %+v, %v", loaded, err)
	}
	a.setAutoCheckUpdates(true)
	if !a.settings.AutoCheckUpdates || a.updateNext.IsZero() {
		t.Fatal("turning on did not schedule a check")
	}
}
