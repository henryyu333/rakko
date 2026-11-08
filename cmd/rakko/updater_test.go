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
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/locales"
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
	origDispatch, origRelaunch, origWork, origMake := dispatchMain, relaunchApp, goWork, updateWinMake
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
	updateWinMake = func(*shelf) *mygo.Window { return nil }
	t.Cleanup(func() {
		updateEnabled, updateCheck, runInstall = origEnabled, origCheck, origRun
		dispatchMain, relaunchApp, goWork = origDispatch, origRelaunch, origWork
		updateCheckTimeout, updateInstallTimeout = origCheckTO, origInstallTO
		updateWinMake = origMake
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

// waitCallback runs the next queued main-thread callback, waiting for a
// worker to post one when nothing is queued yet.
func (p *updateProbe) waitCallback(t *testing.T) {
	t.Helper()
	select {
	case fn := <-p.drain:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("no main-thread callback arrived")
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
	// The automatic prompt is deferred by a quiet second and then opens
	// the found window once.
	a.tickUpdates(time.Now().Add(2 * time.Second))
	if !a.updateWinOpen() || a.updateWinStage != updateStageFound {
		t.Fatalf("auto-found stage = %v, open = %v", a.updateWinStage, a.updateWinOpen())
	}
	a.closeUpdateWindow()
	a.updatePromptPending, a.updatePromptAt = updateStageChecking, time.Time{}
	a.updateArmPrompt(updateStageFound)
	a.tickUpdates(time.Now().Add(2 * time.Second))
	if a.updateWinOpen() {
		t.Fatal("the window prompted twice for the same version")
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
	// Up to date: a stale offer clears; the manual check's window says so.
	a.update = &mygo.Update{Version: "9.9.9"}
	a.checkedUpdate(true, nil, nil)
	if a.update != nil {
		t.Fatal("a stale offer survived an up-to-date check")
	}
	if !a.updateWinOpen() || a.updateWinStage != updateStageLatest {
		t.Fatalf("manual up-to-date stage = %v, open = %v", a.updateWinStage, a.updateWinOpen())
	}
	a.updateWindowClosed()
	// Errors: the failed face shows the reason — the specific one when
	// this copy cannot update.
	a.checkedUpdate(true, nil, errors.New("offline"))
	if a.updateWinStage != updateStageFailed {
		t.Fatalf("manual failure stage = %v", a.updateWinStage)
	}
	a.updateWindowClosed()
	a.checkedUpdate(true, nil, mygo.ErrUpdatesDisabled)
	if a.updateWinStage != updateStageFailed || a.updateFailDetail == "" {
		t.Fatalf("disabled-build stage = %v, detail = %q", a.updateWinStage, a.updateFailDetail)
	}
	a.updateWindowClosed()
	// A background failure stays silent apart from rescheduling.
	a.checkedUpdate(false, nil, errors.New("offline"))
	if a.updateWinOpen() || a.status != "" {
		t.Fatalf("background failure wrote a window or status %q", a.status)
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
	if a.updateWinStage != updateStageFailed {
		t.Fatalf("timeout left stage %v, want failed", a.updateWinStage)
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
	a.installUpdate(true)
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
	a.installUpdate(true)
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
	a.installUpdate(true)
	p.settle()
	if a.updateInstalling || a.updateInstalled {
		t.Fatal("timed-out install left the installing/installed state set")
	}
	// A quiet install reports in Settings, not as an error toast: the
	// progress note that was already up is not a failure.
	if strings.Contains(a.status, "失败") {
		t.Fatalf("quiet timeout reported a failure toast: %q", a.status)
	}
	if a.updateLastResult != updateResultFailed {
		t.Fatalf("quiet timeout last result = %v", a.updateLastResult)
	}
	if item := a.updateMenu(); !strings.Contains(item.Label, "安装更新（v1.2.3）") {
		t.Fatalf("post-timeout menu = %q", item.Label)
	}
	// Retrying the same offer works.
	p.runFn = nil
	a.installUpdate(true)
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
	// A quiet install (the auto path) fails silently apart from Settings.
	a.installUpdate(true)
	p.settle()
	if a.updateInstalled || a.updateInstalling {
		t.Fatal("failed install marked installed")
	}
	if strings.Contains(a.status, "失败") {
		t.Fatalf("quiet failure reported a toast: %q", a.status)
	}
	if a.updateLastResult != updateResultFailed {
		t.Fatalf("quiet failure last result = %v", a.updateLastResult)
	}
	// The user's own install says so.
	a.installUpdate(false)
	p.settle()
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

// A version the user skipped stays quiet: the automatic path never
// prompts and never installs it, its footer and menu entries hide, and
// asking again (a manual check) lifts the skip and opens the found face.
func TestSkipUpdateVersion(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	a.dataDir = t.TempDir()
	a.update = &mygo.Update{Version: "1.2.3"}
	a.updateWinStage = updateStageFound
	a.updateWinMarked = true
	a.skipUpdateVersion()
	if a.settings.UpdateSkippedVersion != "1.2.3" {
		t.Fatal("skip did not persist the version")
	}
	loaded, err := settings.Load(a.dataDir)
	if err != nil || loaded.UpdateSkippedVersion != "1.2.3" {
		t.Fatalf("settings.json not saved: %+v, %v", loaded, err)
	}
	if a.updateWinOpen() {
		t.Fatal("skip left the window open")
	}
	if got := a.updateStatus(); got != "" {
		t.Fatalf("skipped version still shows the footer: %q", got)
	}
	if item := a.updateMenu(); item.Label != "检查更新…" {
		t.Fatalf("skipped version's menu = %q", item.Label)
	}
	// An automatic re-find of the same version prompts nothing and
	// installs nothing — even with UpdateAutoInstall on.
	a.settings.UpdateAutoInstall = true
	a.checkedUpdate(false, &mygo.Update{Version: "1.2.3"}, nil)
	if a.updateWinOpen() || p.runCount() != 0 {
		t.Fatalf("skipped version opened the window or installed: open=%v runs=%d",
			a.updateWinOpen(), p.runCount())
	}
	// A newer version is a different story — it installs quietly.
	a.checkedUpdate(false, &mygo.Update{Version: "1.2.4"}, nil)
	p.settle()
	if p.runCount() != 1 || !a.updateInstalled {
		t.Fatal("auto-install ignored a version newer than the skip")
	}
}

// 稍后提醒我 writes the version and its deadline and silences it for the
// quiet period; once that lapses an automatic check prompts again.
func TestRemindUpdateLater(t *testing.T) {
	stubUpdater(t)
	a := updateShelf()
	a.dataDir = t.TempDir()
	a.update = &mygo.Update{Version: "1.2.3"}
	a.updateWinStage = updateStageFound
	a.updateWinMarked = true
	before := time.Now()
	a.remindUpdateLater()
	if a.settings.UpdateRemindVersion != "1.2.3" ||
		a.settings.UpdateRemindUntil <= before.Unix() {
		t.Fatalf("remind persisted %q until %d",
			a.settings.UpdateRemindVersion, a.settings.UpdateRemindUntil)
	}
	loaded, err := settings.Load(a.dataDir)
	if err != nil || loaded.UpdateRemindVersion != "1.2.3" {
		t.Fatalf("settings.json not saved: %+v, %v", loaded, err)
	}
	if a.updateWinOpen() {
		t.Fatal("remind left the window open")
	}
	// Re-found inside the quiet period: nothing.
	a.checkedUpdate(false, &mygo.Update{Version: "1.2.3"}, nil)
	a.tickUpdates(time.Now().Add(2 * time.Second))
	if a.updateWinOpen() {
		t.Fatal("deferred version prompted inside its quiet period")
	}
	// Past the deadline the same version may prompt again.
	a.settings.UpdateRemindUntil = time.Now().Add(-time.Minute).Unix()
	if !updateShouldPrompt(&mygo.Update{Version: "1.2.3"}, a.settings, time.Now()) {
		t.Fatal("lapsed remind window still blocks the prompt")
	}
}

// 以后自动下载并安装更新: an automatic check installs silently — no
// downloading face — and arms the ready prompt for the next tick.
func TestAutoInstallRunsQuiet(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	a.settings.UpdateAutoInstall = true
	a.checkedUpdate(false, &mygo.Update{Version: "1.2.3"}, nil)
	if !a.updateInstalling {
		t.Fatal("auto-install did not start the download")
	}
	if a.updateWinOpen() {
		t.Fatal("quiet install opened the window")
	}
	p.settle()
	if !a.updateInstalled {
		t.Fatal("auto-install did not finish")
	}
	// The ready face is armed and fires once, on the next tick.
	a.tickUpdates(time.Now().Add(2 * time.Second))
	if !a.updateWinOpen() || a.updateWinStage != updateStageReady {
		t.Fatalf("ready prompt never opened: open=%v stage=%v",
			a.updateWinOpen(), a.updateWinStage)
	}
	a.closeUpdateWindow()
	a.tickUpdates(time.Now().Add(3 * time.Second))
	if a.updateWinOpen() {
		t.Fatal("the ready window prompted twice for the same version")
	}
}

// The found window's 自动下载并安装 toggle writes the setting through the
// single applySettings path, and the default is off.
func TestUpdateAutoInstallSetting(t *testing.T) {
	a := updateShelf()
	a.dataDir = t.TempDir()
	if a.settings.UpdateAutoInstall {
		t.Fatal("UpdateAutoInstall defaults on")
	}
	a.setUpdateAutoInstall(true)
	if !a.settings.UpdateAutoInstall {
		t.Fatal("setUpdateAutoInstall did not apply")
	}
	loaded, err := settings.Load(a.dataDir)
	if err != nil || !loaded.UpdateAutoInstall {
		t.Fatalf("settings.json not saved: %+v, %v", loaded, err)
	}
}

// 取消 on the checking face buries the late result: it is still recorded
// and rescheduled, but no face opens and a found version neither prompts
// nor installs on its own — the user dismissed this check, the ordinary
// offer keeps it reachable.
func TestCancelCheckBuriesResult(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	p.checkFn = func(context.Context) (*mygo.Update, error) {
		return &mygo.Update{Version: "1.2.3"}, nil
	}
	a.checkUpdates(true)
	if a.updateWinStage != updateStageChecking || !a.updateWinOpen() {
		t.Fatal("manual check did not open the checking face")
	}
	a.cancelUpdateCheck()
	p.settle()
	if a.updateChecking {
		t.Fatal("canceled check left updateChecking set")
	}
	if a.updateWinOpen() {
		t.Fatal("canceled check's result opened a face")
	}
	if a.update == nil || a.update.Version != "1.2.3" {
		t.Fatal("canceled check's found version was dropped")
	}
	if p.runCount() != 0 {
		t.Fatal("canceled check's result installed")
	}
	if !a.updateNext.After(time.Now()) {
		t.Fatal("canceled check did not reschedule")
	}
	a.tickUpdates(time.Now().Add(2 * time.Second))
	if a.updateWinOpen() {
		t.Fatal("canceled check's found version prompted")
	}
	// The offer stands on the tray menu.
	if item := a.updateMenu(); !strings.Contains(item.Label, "安装更新（v1.2.3）") {
		t.Fatalf("post-cancel menu = %q", item.Label)
	}
}

// The downloading face's 取消 kills the request and the late result
// counts as a cancel, not a failure: no error toast, the offer stays.
func TestCancelInstallKeepsOffer(t *testing.T) {
	p := stubUpdater(t)
	a := updateShelf()
	a.update = &mygo.Update{Version: "1.2.3"}
	started := make(chan struct{})
	p.runFn = func(ctx context.Context, _ *mygo.Update, _ func(int64, int64)) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	// The real install runs on the worker, not inline: it must be able to
	// block until the cancel arrives.
	origWork := goWork
	goWork = func(fn func()) { go fn() }
	t.Cleanup(func() { goWork = origWork })
	a.installUpdate(false)
	<-started
	if a.updateWinStage != updateStageDownloading || !a.updateWinOpen() {
		t.Fatal("install did not open the downloading face")
	}
	a.cancelUpdateInstall()
	p.waitCallback(t)
	if a.updateInstalling || a.updateInstalled {
		t.Fatal("canceled install left installing/installed set")
	}
	if strings.Contains(a.status, "失败") {
		t.Fatalf("cancel reported as a failure: %q", a.status)
	}
	if a.update == nil || a.updateInstalling {
		t.Fatal("cancel lost the offer")
	}
	if item := a.updateMenu(); !strings.Contains(item.Label, "安装更新（v1.2.3）") {
		t.Fatalf("post-cancel menu = %q", item.Label)
	}
}

// A found version announces on the tray menu and the footer pill whether
// it was prompted or not.
func TestFoundAnnouncesMenuAndStatus(t *testing.T) {
	a := updateShelf()
	a.checkedUpdate(false, &mygo.Update{Version: "1.2.3"}, nil)
	if item := a.updateMenu(); !strings.Contains(item.Label, "安装更新（v1.2.3）") {
		t.Fatalf("menu = %q", item.Label)
	}
	if got := a.updateStatus(); !strings.Contains(got, "1.2.3") {
		t.Fatalf("footer = %q", got)
	}
}

// The window's faces are sized per face: found shrinks without notes,
// every other face is the small card.
func TestUpdateWinSize(t *testing.T) {
	a := updateShelf()
	a.update = &mygo.Update{Version: "1.2.3", Notes: "### What's new"}
	if w, h := a.updateWinSize(updateStageFound); w != 520 || h != 420 {
		t.Fatalf("found-with-notes size = %dx%d", w, h)
	}
	a.update.Notes = "   "
	if w, h := a.updateWinSize(updateStageFound); w != 520 || h != updateFoundMin {
		t.Fatalf("found-without-notes size = %dx%d", w, h)
	}
	for _, stage := range []updateStage{
		updateStageChecking, updateStageDownloading, updateStageReady,
		updateStageLatest, updateStageFailed,
	} {
		if w, h := a.updateWinSize(stage); w != 400 || h != 146 {
			t.Fatalf("stage %d size = %dx%d", stage, w, h)
		}
	}
}

// The update window is key: the shelf must not take the global Esc/Space
// back while it is up, or a panel click would hide it.
func TestUpdateWindowKeepsPanelKeys(t *testing.T) {
	a := &shelf{}
	if !a.keysAreOurs() {
		t.Fatal("a shelf with no windows may hold the keys")
	}
	a.updateWin = &mygo.Window{}
	if a.keysAreOurs() {
		t.Fatal("the update window must keep Esc/Space to itself")
	}
	a.updateWin = nil
	if !a.keysAreOurs() {
		t.Fatal("closing the update window gives the keys back")
	}
}

// The found face's checkbox writes the setting through applySettings: a
// click persists it, it is off by default, and a second click turns it
// back off.
func TestUpdateAutoRowPersistsClick(t *testing.T) {
	a := updateShelf()
	a.dataDir = t.TempDir()
	a.update = &mygo.Update{Version: "1.2.3"}
	a.updateWinStage = updateStageFound
	l := locales.In("zh-Hans")
	v := ui.NewTester(a.updateView, updateFoundW, updateFoundMin)
	v.SetLocale("zh-Hans")
	v.Frame()
	if err := v.Click(l.UpdateAutoInstallRow()); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	if !a.settings.UpdateAutoInstall {
		t.Fatal("the click did not turn the setting on")
	}
	loaded, err := settings.Load(a.dataDir)
	if err != nil || !loaded.UpdateAutoInstall {
		t.Fatalf("settings.json not saved: %+v, %v", loaded, err)
	}
	if err := v.Click(l.UpdateAutoInstallRow()); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	if a.settings.UpdateAutoInstall {
		t.Fatal("the second click did not turn it off")
	}
	loaded, err = settings.Load(a.dataDir)
	if err != nil || loaded.UpdateAutoInstall {
		t.Fatalf("settings.json kept the old value: %+v, %v", loaded, err)
	}
}
