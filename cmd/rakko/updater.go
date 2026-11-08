//go:build darwin

package main

// Signed auto-updates through mygo.Updater. `mygo build` bakes the update
// feed (the latest release of updates.github in mygo.json) and the public
// key into the app; the release itself holds the signed archive and the
// update-darwin-arm64.json manifest. This file owns the scheduling and the
// non-intrusive notice: checks never prompt, a found version waits as a
// panel-footer line and a tray item until the user installs it.
//
// The check and the install run on their own goroutines (network and disk
// must not stall the UI); their results come back through dispatchMain onto
// the main thread, which owns every a.update* field.

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

const (
	// updateFirstDelay is when the first automatic check runs after launch.
	updateFirstDelay = 10 * time.Second
	// updateInterval is how often automatic checks run.
	updateInterval = 24 * time.Hour
	// updateRetry is the delay after a failed automatic check.
	updateRetry = time.Hour
)

// Seams around the app-level calls so the scheduling and state transitions
// run in headless tests without a packaged, updater-enabled build.
var (
	updateEnabled = func() bool { return mygo.Updater.Enabled() }
	updateCheck   = func(ctx context.Context) (*mygo.Update, error) { return mygo.Updater.Check(ctx) }
	runInstall    = func(ctx context.Context, up *mygo.Update, progress func(downloaded, total int64)) error {
		return up.Install(ctx, progress)
	}
	dispatchMain = mygo.RunOnMain
	// goWork runs the slow part of a check or install off the main thread.
	goWork      = func(fn func()) { go fn() }
	relaunchApp = func() { mygo.App.Relaunch() }
	// Timeouts bound the network work: go's HTTP client has none, so a
	// stalled GitHub request would otherwise leave updateChecking or
	// updateInstalling set forever and silence every later check. Variables
	// so tests can shrink them.
	updateCheckTimeout   = 30 * time.Second
	updateInstallTimeout = 10 * time.Minute
)

// updateCheckResult is how the last update check went, as the Settings
// window's 检查更新 row reports it.
type updateCheckResult int

const (
	updateResultNone updateCheckResult = iota
	updateResultLatest
	updateResultAvailable
	updateResultFailed
	updateResultDisabled
)

// nextAutoCheck returns when the next automatic check is due after one ran
// at now: a day after a successful check, sooner after a failure so a
// transient network problem does not mute updates for a day. Pure;
// unit-tested.
func nextAutoCheck(now time.Time, ok bool) time.Time {
	if ok {
		return now.Add(updateInterval)
	}
	return now.Add(updateRetry)
}

// tickUpdates drives the automatic checks from the monitoring loop (main
// thread): the first tick arms the launch check, later ticks fire when
// updateNext is due. The autoCheckUpdates setting is read on every tick, so
// turning it off stops future checks without a restart.
func (a *shelf) tickUpdates(now time.Time) {
	// A deferred automatic prompt goes once the busy gesture is over and
	// its quiet second has passed.
	if a.updatePromptPending != updateStageChecking {
		if a.updateBusyPrompt() {
			a.updatePromptAt = time.Time{}
		} else {
			if a.updatePromptAt.IsZero() {
				a.updatePromptAt = now.Add(time.Second)
			}
			if !now.Before(a.updatePromptAt) {
				a.updateFirePrompt()
			}
		}
	}
	if !a.settings.AutoCheckUpdates || a.updateChecking || a.updateInstalling {
		return
	}
	if a.updateNext.IsZero() {
		a.updateNext = now.Add(updateFirstDelay)
		return
	}
	if now.Before(a.updateNext) {
		return
	}
	if !updateEnabled() {
		// Development and unsigned builds cannot update: check the setting
		// again tomorrow instead of every tick.
		a.updateNext = nextAutoCheck(now, true)
		return
	}
	a.checkUpdates(false)
}

// checkUpdates starts one update check on a background goroutine; user is
// true when the user asked from the tray menu, Settings or the window's
// 重试. A manual check shows its answer in the update window, which opens
// on the 正在检查更新 face right away; a check or install already under
// way is never doubled.
func (a *shelf) checkUpdates(user bool) {
	if a.updateChecking || a.updateInstalling {
		return
	}
	a.updateChecking = true
	a.updateCheckDead = false
	if user {
		// The window is the progress note: it replaces the old toast and
		// switches to the answer face when the check lands.
		a.updateSetStage(updateStageChecking)
		a.openUpdateWindow()
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	a.updateCheckCancel = cancel
	goWork(func() {
		defer cancel()
		up, err := updateCheck(ctx)
		dispatchMain(func() { a.checkedUpdate(user, up, err) })
	})
}

// cancelUpdateCheck is the 正在检查更新 face's 取消: the request is let
// die, its late result is buried (a canceled check asks for nothing
// further — not even the automatic prompt for a version it happens to
// find), and the window closes.
func (a *shelf) cancelUpdateCheck() {
	a.updateCheckDead = true
	if a.updateCheckCancel != nil {
		a.updateCheckCancel()
	}
	a.closeUpdateWindow()
}

// checkedUpdate records the outcome of a check on the main thread. user
// says who answers: a manual check's window shows the result, an
// automatic check's found version only prompts when it is neither
// blocked nor meant for a silent install. A check the user canceled
// (updateCheckDead) records its result and reschedules, but the outcome
// changes nothing the user sees: no window, no prompt, no silent
// install — the version stays as the ordinary offer it announced.
func (a *shelf) checkedUpdate(user bool, up *mygo.Update, err error) {
	a.updateChecking = false
	a.updateCheckCancel = nil
	dead := a.updateCheckDead
	a.updateCheckDead = false
	user = user && !dead
	// A check is a check: a manual one counts toward the daily schedule too.
	a.updateNext = nextAutoCheck(time.Now(), err == nil)
	// The 通用 page's 检查更新 row says when the last check ran and how it
	// went; the state lives for this launch, like the schedule does.
	a.updateLastCheck = time.Now()
	a.updateLastResult = updateResultLatest
	a.updateLastVersion = ""
	defer a.repaintSettings()
	if err != nil {
		a.updateLastResult = updateResultFailed
		if errors.Is(err, mygo.ErrUpdatesDisabled) {
			a.updateLastResult = updateResultDisabled
		}
		log.Printf("Rakko: 检查更新失败：%v", err)
		if user {
			a.updateFailDetail = a.updateFailureDetail(err)
			a.updateSetStage(updateStageFailed)
			a.openUpdateWindow()
		}
		return
	}
	if up == nil {
		a.update = nil
		if user {
			a.updateSetStage(updateStageLatest)
			a.openUpdateWindow()
		} else if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	a.updateLastResult = updateResultAvailable
	a.updateLastVersion = up.Version
	// An install in flight or a version already swapped in keeps its state.
	if a.updateInstalling || (a.updateInstalled && a.update != nil && a.update.Version == up.Version) {
		return
	}
	a.update, a.updateInstalled, a.updateProgress = up, false, 0
	log.Printf("Rakko: 发现新版本 v%s", up.Version)
	if a.win != nil {
		a.win.Invalidate()
	}
	if dead {
		return
	}
	if os.Getenv("RAKKO_UPDATE_SELFTEST") == "1" {
		// End-to-end test hook: the gated update smoke test uses it to take
		// the same install path the menu item takes. Once installed, it
		// also takes the 重新启动 ShelfU 完成更新 path: mygo stages the new
		// bundle and installs it when the app exits, so the test lets that
		// exit happen instead of replacing a running bundle. The window
		// never joins it.
		a.installUpdate(true)
		return
	}
	if user {
		// Asking again for a skipped version lifts the skip.
		if a.settings.UpdateSkippedVersion == up.Version {
			next := a.settings
			next.UpdateSkippedVersion = ""
			a.applySettings(next)
		}
		a.updateSetStage(updateStageFound)
		a.openUpdateWindow()
		return
	}
	// The automatic path: skipped and deferred versions stay silent,
	// UpdateAutoInstall downloads without asking, everything else prompts
	// once — now, or after an in-flight gesture ends (tickUpdates re-arms
	// the deferral).
	if updateBlocked(up, a.settings, time.Now()) {
		return
	}
	if a.settings.UpdateAutoInstall {
		a.installUpdate(true)
		return
	}
	if a.updatePrompted != up.Version {
		a.updateArmPrompt(updateStageFound)
	}
}

// updateBlocked says whether a found version may not make itself known:
// the user skipped it, or its 稍后提醒 quiet period is still on.
func updateBlocked(up *mygo.Update, s settings.Settings, now time.Time) bool {
	if s.UpdateSkippedVersion == up.Version {
		return true
	}
	return s.UpdateRemindVersion == up.Version && s.UpdateRemindUntil > now.Unix()
}

// updateShouldPrompt says whether an automatically found version may open
// the found window: a skipped version never does, one inside the 稍后提醒
// quiet period does not, and silent installs take their own path. Pure;
// unit-tested.
func updateShouldPrompt(up *mygo.Update, s settings.Settings, now time.Time) bool {
	if up == nil || updateBlocked(up, s, now) {
		return false
	}
	return !s.UpdateAutoInstall
}

// updateBusyPrompt reports whether an automatic prompt would interrupt a
// gesture the user is already in: a file drag (the monitor's flag; the
// drop guard itself is armed for the session, so it cannot speak for
// this), a shortcut recording, the permission walk or the rename window.
// The panel holding the keyboard does not count — the window takes it
// over by the Settings window's rules.
func (a *shelf) updateBusyPrompt() bool {
	return a.dragging || a.settingsRecording != shortcutNone ||
		a.perm.stage != permissionIdle || a.rn != nil
}

// updateArmPrompt schedules the automatic prompt: a second after the last
// busy gesture ends (tickUpdates drives the clock), or right away when
// nothing is in the way.
func (a *shelf) updateArmPrompt(stage updateStage) {
	if a.updateBusyPrompt() {
		a.updatePromptAt = time.Time{}
		a.updatePromptPending = stage
		return
	}
	if a.updatePromptAt.IsZero() {
		a.updatePromptAt = time.Now().Add(time.Second)
	}
	a.updatePromptPending = stage
}

// updateFirePrompt opens the window the deferral armed, once: the found
// face still asks updateShouldPrompt (settings may have changed while it
// waited), the ready face of a silent install only needs the update to
// still be staged and unprompted.
func (a *shelf) updateFirePrompt() {
	stage := a.updatePromptPending
	a.updatePromptPending, a.updatePromptAt = updateStageChecking, time.Time{}
	if a.update == nil || a.updateWinOpen() || a.updateBusyPrompt() {
		return
	}
	if a.updatePrompted == a.update.Version {
		return
	}
	if stage == updateStageFound && !updateShouldPrompt(a.update, a.settings, time.Now()) {
		return
	}
	if stage == updateStageReady && !a.updateInstalled {
		return
	}
	a.updatePrompted = a.update.Version
	a.updateSetStage(stage)
	a.openUpdateWindow()
}

// installUpdate starts the download of the offered version: the signed
// archive (or delta) replaces the bundle in the background while the
// window's downloading face — or, for a silent install, the footer —
// shows progress. quiet is the auto-install path, which never opens the
// downloading face and prompts once when the update is staged.
func (a *shelf) installUpdate(quiet bool) {
	up := a.update
	if up == nil || a.updateInstalling || a.updateInstalled {
		return
	}
	a.updateInstalling, a.updateProgress = true, 0
	a.updateBytes, a.updateTotal = 0, 0
	a.updateInstallQuiet = quiet
	if !quiet {
		a.updateSetStage(updateStageDownloading)
		a.openUpdateWindow()
	}
	if !a.updateWinOpen() {
		a.toastProgress(a.appLoc().UpdateDownloading(up.Version), time.Now().Add(updateInstallTimeout))
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateInstallTimeout)
	a.updateInstallCancel = cancel
	goWork(func() {
		defer cancel()
		err := runInstall(ctx, up, func(n, total int64) {
			if total <= 0 {
				return
			}
			dispatchMain(func() {
				if a.updateInstalling {
					a.updateBytes, a.updateTotal = n, total
					a.updateProgress = float64(n) / float64(total)
					if a.win != nil {
						a.win.Invalidate()
					}
					a.repaintUpdateWindow()
				}
			})
		})
		dispatchMain(func() { a.installedUpdate(up, err) })
	})
}

// installFromWindow is the found face's 安装更新: the window stays open
// and shrinks into the downloading face.
func (a *shelf) installFromWindow() {
	a.installUpdate(false)
}

// cancelUpdateInstall is the downloading face's 取消: the download's
// context dies, the staged-work cleanup mygo runs makes it safe, and the
// late result is recorded as a cancel rather than a failure.
func (a *shelf) cancelUpdateInstall() {
	a.updateCanceled = true
	if a.updateInstallCancel != nil {
		a.updateInstallCancel()
	}
	a.closeUpdateWindow()
}

// installedUpdate records an install's outcome on the main thread.
func (a *shelf) installedUpdate(up *mygo.Update, err error) {
	a.updateInstalling, a.updateProgress = false, 0
	a.updateInstallCancel = nil
	canceled := a.updateCanceled || errors.Is(err, context.Canceled)
	a.updateCanceled = false
	quiet := a.updateInstallQuiet
	a.updateInstallQuiet = false
	defer a.repaintSettings()
	if err != nil {
		if canceled {
			log.Printf("Rakko: 取消更新 v%s 的下载", up.Version)
			return
		}
		log.Printf("Rakko: 安装更新失败：%v", err)
		a.updateLastResult = updateResultFailed
		if !quiet {
			// A quiet install reports in Settings' last-result row only:
			// a network wobble must not interrupt the user.
			a.toastStatus(a.appLoc().UpdateInstallFailed(err.Error()), time.Now().Add(8*time.Second), statusErr)
		}
		a.closeUpdateWindow()
		return
	}
	a.updateInstalled = true
	log.Printf("Rakko: 更新 v%s 已安装，重启后生效", up.Version)
	a.toastStatus(a.appLoc().UpdateInstalled(up.Version), time.Now().Add(8*time.Second), statusOK)
	if os.Getenv("RAKKO_UPDATE_SELFTEST") == "1" {
		// End-to-end test hook: click 重新启动 ShelfU 完成更新 for the
		// test. mygo installs the staged bundle as this instance exits
		// and starts it again; nothing is replaced while it runs.
		relaunchApp()
		return
	}
	if a.updateWinOpen() {
		a.updateSetStage(updateStageReady)
		return
	}
	if quiet && a.updatePrompted != up.Version {
		a.updateArmPrompt(updateStageReady)
	}
}

// updateMenu returns the update entry of the tray menu: 检查更新… normally,
// 安装更新（vX）… while a version waits, a progress line while it installs
// and 重新启动 once it is in place.
func (a *shelf) updateMenu() *mygo.MenuItem {
	l := a.appLoc()
	switch {
	case a.updateInstalled:
		return &mygo.MenuItem{Label: l.UpdateRestartToFinish(), Click: func(*mygo.MenuItem, *mygo.Window) {
			relaunchApp()
		}}
	case a.updateInstalling:
		label := l.UpdateInstalling(a.update.Version)
		if a.updateProgress > 0 {
			label = l.UpdateInstallingPercent(a.update.Version, int(a.updateProgress*100))
		}
		return &mygo.MenuItem{Label: label, Disabled: true}
	case a.update != nil && a.update.Version == a.settings.UpdateSkippedVersion:
		return &mygo.MenuItem{Label: l.UpdateCheck(), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.checkUpdates(true)
		}}
	case a.update != nil:
		return &mygo.MenuItem{Label: l.UpdateInstall(a.update.Version), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.installUpdate(false)
		}}
	case a.updateChecking:
		return &mygo.MenuItem{Label: l.UpdateChecking(), Disabled: true}
	default:
		return &mygo.MenuItem{Label: l.UpdateCheck(), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.checkUpdates(true)
		}}
	}
}

// updateStatus is the persistent update notice of the panel footer, ""
// while there is nothing to say. It is a line of its own so it never
// overwrites status messages.
func (a *shelf) updateStatus() string {
	l := a.appLoc()
	switch {
	case a.updateInstalling && a.update != nil:
		if a.updateProgress > 0 {
			return l.UpdateDownloadingPercent(a.update.Version, int(a.updateProgress*100))
		}
		return l.UpdateDownloading(a.update.Version)
	case a.updateInstalled && a.update != nil:
		return l.UpdateInstalled(a.update.Version)
	case a.update != nil && a.update.Version != a.settings.UpdateSkippedVersion:
		return l.UpdateAvailable(a.update.Version)
	}
	return ""
}

// setAutoCheckUpdates saves the autoCheckUpdates preference through the
// single applySettings path and, when it was just turned on, checks at the
// next monitor tick instead of waiting a day.
func (a *shelf) setAutoCheckUpdates(on bool) {
	if a.settings.AutoCheckUpdates == on {
		return
	}
	next := a.settings
	next.AutoCheckUpdates = on
	if !a.applySettings(next) {
		return
	}
	if on {
		a.updateNext = time.Now()
	}
}

// setUpdateAutoInstall saves the 以后自动下载并安装更新 preference through
// the single applySettings path, from the found window's checkbox and the
// Settings row alike.
func (a *shelf) setUpdateAutoInstall(on bool) {
	if a.settings.UpdateAutoInstall == on {
		return
	}
	next := a.settings
	next.UpdateAutoInstall = on
	a.applySettings(next)
}

// skipUpdateVersion is the found window's 跳过这个版本: the version is
// remembered, its pill and install item disappear, and no automatic check
// prompts for it again.
func (a *shelf) skipUpdateVersion() {
	if a.update == nil {
		return
	}
	next := a.settings
	next.UpdateSkippedVersion = a.update.Version
	a.applySettings(next)
	a.closeUpdateWindow()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// remindUpdateLater is the found window's 稍后提醒我: this version stays
// quiet for one automatic-check round, then may prompt again.
func (a *shelf) remindUpdateLater() {
	if a.update == nil {
		return
	}
	next := a.settings
	next.UpdateRemindVersion = a.update.Version
	next.UpdateRemindUntil = nextRemindUntil(time.Now()).Unix()
	a.applySettings(next)
	a.closeUpdateWindow()
}

// --- the window itself -------------------------------------------------

// updateWinMake is the window factory, a seam so tests can run the open
// and stage logic without a window server. Assigned in init: a package
// initializer that mentions updateView would close an initialization
// cycle through the panel's view methods, the loop settingsOpen's init
// already breaks for the Settings window.
var updateWinMake func(*shelf) *mygo.Window

func init() {
	updateWinMake = func(a *shelf) *mygo.Window {
		w0, h0 := a.updateWinSize(a.updateWinStage)
		work := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea
		return mygo.NewWindow(mygo.WindowOptions{
			Title: a.updateWindowTitle(), Width: w0, Height: h0,
			X: work.X + (work.Width-w0)/2, Y: work.Y + (work.Height-h0)*2/5,
			Hidden:          true,
			BackgroundColor: "light-dark(#FFFFFF, #0E0F11)",
			// The title bar is hidden but its lights stay, over the strip the
			// view draws: 28pt tall, the first light's corner 20pt in and 8pt
			// down, like the design's strip. The window follows the system
			// appearance: no appearance is pinned, and the view picks its
			// palette from c.Theme().Dark each frame.
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 20, Y: 8},
			DisableMaximize:      true, DisableFullScreen: true,
			Content: ui.View(a.updateView),
		})
	}
}

// updateWinOpen reports whether the update window is logically open —
// tests mark it without a native window.
func (a *shelf) updateWinOpen() bool { return a.updateWin != nil || a.updateWinMarked }

// updateWinSize is the found face's height: the notes card leaves with
// empty release notes and the window shortens by its size plus gap.
func (a *shelf) updateWinSize(stage updateStage) (w, h int) {
	if stage != updateStageFound {
		return updateSmallW, updateSmallH
	}
	if a.update != nil && strings.TrimSpace(a.update.Notes) != "" {
		return updateFoundW, updateFoundH
	}
	return updateFoundW, updateFoundMin
}

// updateSetStage switches the window's face: the title and size follow
// (SetBounds moves it directly, no spring, so the content's repaint is
// the only motion), and the notes scroll resets when the found face
// arrives.
func (a *shelf) updateSetStage(stage updateStage) {
	if stage == updateStageFound && a.updateWinStage != updateStageFound {
		a.updateNotes = ui.ScrollState{}
	}
	a.updateWinStage = stage
	if a.updateWin == nil {
		return
	}
	a.updateWin.SetTitle(a.updateWindowTitle())
	w, h := a.updateWinSize(stage)
	b := a.updateWin.Bounds()
	if b.Width != w || b.Height != h {
		a.updateWin.SetBounds(mygo.Rectangle{
			X: b.X + (b.Width-w)/2, Y: b.Y, Width: w, Height: h,
		})
	}
	a.updateWin.Invalidate()
}

// openUpdateWindow shows the update window, creating it on the cursor's
// display, or retargets the open one to the current stage. Opening takes
// the keyboard by the Settings window's rules (release Esc/Space, remember
// the app to hand back to); closing gives it back.
func (a *shelf) openUpdateWindow() {
	if a.updateWin != nil || a.updateWinMarked {
		if a.updateWin != nil {
			mac.ActivateSelf()
			a.updateWin.Show()
			a.updateWin.Focus()
		}
		return
	}
	a.settingsTakeKeyboard()
	w := updateWinMake(a)
	if w == nil {
		// Headless: mark the window open so the state machine runs the
		// same transitions.
		a.updateWinMarked = true
		return
	}
	a.updateWinMarked = true
	w.OnClosed(a.updateWindowClosed)
	a.updateWin = w
	w.Show()
	mac.ActivateSelf()
	w.Focus()
}

// closeUpdateWindow closes the window if it is up, and unwinds the
// logical state either way (a headless mark, or a native close that
// already ran updateWindowClosed).
func (a *shelf) closeUpdateWindow() {
	if a.updateWin != nil {
		a.updateWin.Close()
		return
	}
	if a.updateWinMarked {
		a.updateWindowClosed()
	}
}

// updateWindowClosed runs when the window goes away: the state resets, an
// in-flight face's operation is canceled (a check dies, a download
// dies — a staged update stays, it cannot be revoked), and the keyboard
// returns by the Settings window's rules, preferring the Settings window
// itself when that is still open.
func (a *shelf) updateWindowClosed() {
	if !a.updateWinMarked && a.updateWin == nil {
		return
	}
	a.updateWin, a.updateWinMarked = nil, false
	switch a.updateWinStage {
	case updateStageChecking:
		// Closing the progress face is its 取消: the late result stays
		// silent instead of opening a result the user dismissed.
		a.updateCheckDead = true
		if a.updateCheckCancel != nil {
			a.updateCheckCancel()
		}
	case updateStageDownloading:
		a.updateCanceled = true
		if a.updateInstallCancel != nil {
			a.updateInstallCancel()
		}
	}
	if a.quitting {
		return
	}
	a.settingsReleaseKeyboard()
	if a.settingsWin != nil {
		// Settings gave the keyboard to this window; it is still there,
		// so it — not the previous app — gets it back.
		mac.ActivateSelf()
		a.settingsWin.Focus()
	}
}

// repaintUpdateWindow redraws the update window when it is open.
func (a *shelf) repaintUpdateWindow() {
	if a.updateWin != nil {
		a.updateWin.Invalidate()
	}
}

// updateUnavailableReason says why this copy cannot update, "" when it
// can. mygo's Enabled() is the gate (a development or unsigned build, or
// a bundle whose neighbour cannot be written — a mounted disk image;
// Gatekeeper's random translocation path lands there too — ~/Downloads is
// writable, so a copy run from it updates in place and the user owns the
// duplicate). A development build explains itself as a build, every other
// unavailable copy asks to be moved to 应用程序. The check path reports
// ErrUpdatesDisabled for the same situations, so they share one
// diagnosis.
func (a *shelf) updateUnavailableReason() string {
	if updateEnabled() {
		return ""
	}
	l := a.appLoc()
	if updateDevBuild() {
		return l.SettingsUpdateResultDisabled()
	}
	return l.UpdateReadOnlyDetail()
}

// updateDevBuild wraps mygo.IsDev behind a seam: it is true under go
// test, where a test standing in for a production build replaces it.
var updateDevBuild = mygo.IsDev

// updateFailureDetail picks the failed face's detail line: the specific
// reason when this copy cannot update, else the generic network one.
func (a *shelf) updateFailureDetail(err error) string {
	if errors.Is(err, mygo.ErrUpdatesDisabled) {
		if reason := a.updateUnavailableReason(); reason != "" {
			return reason
		}
	}
	return a.appLoc().UpdateFailedDetail()
}
