//go:build darwin

package main

// The one list of system permissions ShelfU needs and the one entry point
// that walks the user through granting them. The tray menu, the Settings
// window's 系统权限 group and the guided flow all read systemPermissions;
// none of them duplicates a title, an order or a probe.
//
// Nothing here changes a permission by itself: the enabled probes are
// read-only (a TCC.db open for Full Disk Access, AXIsProcessTrusted,
// CGPreflightScreenCaptureAccess), and only user-initiated paths open
// System Settings or ask macOS to prompt. No path runs tccutil: clearing a
// TCC record is the user's decision in System Settings, never a side effect
// of asking. Each row shows the real app icon as a drag source for the
// pane's list and its own direct Settings entry.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/locales"
)

const (
	fullDiskPermissionID      = "fulldisk"
	accessibilityPermissionID = "accessibility"
	screenCapturePermissionID = "screencapture"

	// permissionPollInterval is how often the flow re-reads the current
	// item's probe (Full Disk Access, Accessibility) while the user is in
	// System Settings.
	permissionPollInterval = time.Second
	// permissionScreenHandoff is how long the screen-recording step keeps
	// its 「第 n 项」 caption before it hands over to the reopen step. The
	// request opens the pane first; the pause lets the user see which step
	// they are on while the pane comes forward.
	permissionScreenHandoff = 1500 * time.Millisecond
)

// Seams for the headless tests. The probes never prompt; the request
// functions are the user-initiated paths.
var (
	// Full Disk Access has no read-only query API: the probe opens the
	// TCC database, which only a granted process may read.
	fullDiskAccessProbe   = mac.FullDiskAccessGranted
	fullDiskAccessRequest = mac.RequestFullDiskAccess
	accessibilityProbe    = mac.AccessibilityTrusted
	// The preflight is read-only and never prompts. Its answer is cached
	// for the process's lifetime, so showing state uses it alone; the
	// uncached grant proof below covers a grant made after that answer.
	screenCaptureProbe   = mac.ScreenCapturePreflight
	screenCaptureRequest = mac.RequestScreenCapture
	// openPrivacyPane is all a switched-off permission can do: macOS gives
	// no app the power to revoke a grant, only the user in System Settings.
	openPrivacyPane = mac.OpenPrivacySecurityPane
	// One direct entry per row: the pane that holds that permission. The
	// rows also offer the real app bundle as a drag source, so the user can
	// drag the icon into the pane's list instead of pressing +.
	openFullDiskAccessPane = mac.OpenFullDiskAccessPane
	openAccessibilityPane  = mac.OpenAccessibilityPane
	openScreenCapturePane  = mac.OpenScreenCapturePane
	// Screen recording's preflight answer is cached for the process's
	// lifetime. Window titles are the read-only evidence that the grant
	// exists; false only means "not proven" (a screen with no other titled
	// windows looks the same). The status display never treats the cached
	// false as a denial. The evidence is memoized for a moment: the Settings
	// window repaints many times a second and every answer enumerates the
	// on-screen windows. A second of lag cannot show a state the system does
	// not hold, and a grant made now stays "unproven" rather than claimed.
	screenCaptureGrantProbe = memoizedProbe(mac.ScreenCaptureGrantedByWindowTitles, time.Second)

	permissionAfterFunc = func(d time.Duration, fn func()) permissionTimer {
		return time.AfterFunc(d, fn)
	}
	permissionDispatch = mygo.RunOnMain
)

// The real app bundle and its icon are what the user drags into System
// Settings. Both are seams: a headless test must not touch AppKit, and a
// development run outside a bundle has neither.
var (
	permissionBundlePath = defaultPermissionBundlePath
	permissionIcon       = permissionIconFor
)

// defaultPermissionBundlePath is the running app's .app, when it has one.
func defaultPermissionBundlePath() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	return appBundleForExecutable(exe)
}

// permissionIcons caches each bundle's icon for the window's lifetime.
var permissionIcons = map[string]*ui.Bitmap{}

func permissionIconFor(bundle string) *ui.Bitmap {
	if bundle == "" {
		return nil
	}
	if icon, ok := permissionIcons[bundle]; ok {
		return icon
	}
	var icon *ui.Bitmap
	if png := mac.FileIcon(bundle, 32); len(png) > 0 {
		if decoded, err := ui.DecodeBitmap(png); err == nil {
			icon = decoded
		}
	}
	permissionIcons[bundle] = icon
	return icon
}

// screenGrantObserved is a grant that CGRequestScreenCaptureAccess itself
// reported in this process. It is direct evidence that outlives the cached
// preflight and needs no other app's titled window; only a request that
// returned true sets it, never an inference.
var screenGrantObserved atomic.Bool

// screenCaptureGranted is the one evidence chain for "the system's list
// holds the screen-recording grant": the live preflight first, then a grant
// a request reported, then the WindowServer proof. False only means "not
// proven"; callers must not read it as a denial.
func screenCaptureGranted() bool {
	return screenCaptureProbe() || screenGrantObserved.Load() || screenCaptureGrantProbe()
}

// memoizedProbe repeats probe's answer for ttl: a probe that walks every
// on-screen window is too heavy to run on each repaint of a window that
// shows the same state. Callers still read a real answer within ttl.
func memoizedProbe(probe func() bool, ttl time.Duration) func() bool {
	var mu sync.Mutex
	var at time.Time
	var answer bool
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && time.Since(at) < ttl {
			return answer
		}
		answer = probe()
		at = time.Now()
		return answer
	}
}

type permissionTimer interface {
	Stop() bool
}

// systemPermission is one item of the unified entry: the probe that reports
// the live state and the user-initiated request for that one permission.
// short names it inside a sentence (the row's caption and the group's note),
// gloss is the one-line reason that note gives for it. l is the language the
// copy is in, so a sentence built from a list of them (the group's note, the
// row's caption) uses that language's separators without asking again.
type systemPermission struct {
	id      string
	title   string
	short   string
	gloss   string
	detail  string
	l       locales.Messages
	enabled func() bool
	// granted reports that the system's list already holds the grant, even
	// when this process cannot use it until it is reopened. nil means the
	// live probe is also the grant's answer (Full Disk Access, Accessibility).
	granted func() bool
	request func() bool
	pane    func()
}

// isGranted is the state the system's list shows: the live probe, or the
// uncached evidence of a grant this process is too old to use.
func (p systemPermission) isGranted() bool {
	if p.granted != nil {
		return p.granted()
	}
	return p.enabled()
}

// needsRelaunch is true for a grant that exists but that this process still
// answers no to (screen recording's cached preflight).
func (p systemPermission) needsRelaunch() bool {
	return p.isGranted() && !p.enabled()
}

// systemPermissions is the single source of the list, in the order the
// guided flow walks it: Full Disk Access first, because it is the one the
// user notices (every drag out of Downloads/Desktop/Documents asks until it
// is on). It says the source language; systemPermissionSet says it in the
// language a window or a menu shows.
func systemPermissions() []systemPermission {
	return systemPermissionSet(locales.In(""))
}

// systemPermissionSet is the one list said in the language of l.
func systemPermissionSet(l locales.Messages) []systemPermission {
	return []systemPermission{
		{
			id:     fullDiskPermissionID,
			title:  l.PermFullDiskTitle(),
			short:  l.PermFullDiskShort(),
			gloss:  l.PermFullDiskGloss(),
			detail: l.PermFullDiskDetail(),
			l:      l,
			enabled: func() bool {
				return fullDiskAccessProbe()
			},
			request: func() bool {
				return fullDiskAccessRequest()
			},
			pane: func() { openFullDiskAccessPane() },
		},
		{
			id:     accessibilityPermissionID,
			title:  l.PermAccessibilityTitle(),
			short:  l.PermAccessibilityShort(),
			gloss:  l.PermAccessibilityGloss(),
			detail: l.PermAccessibilityDetail(),
			l:      l,
			enabled: func() bool {
				return accessibilityProbe()
			},
			request: func() bool {
				return accessibilityRequest()
			},
			pane: func() { openAccessibilityPane() },
		},
		{
			id:     screenCapturePermissionID,
			title:  l.PermScreenTitle(),
			short:  l.PermScreenShort(),
			gloss:  l.PermScreenGloss(),
			detail: l.PermScreenDetail(),
			l:      l,
			enabled: func() bool {
				return screenCaptureProbe()
			},
			granted: func() bool {
				return screenCaptureGranted()
			},
			request: func() bool {
				return screenCaptureRequest()
			},
			pane: func() { openScreenCapturePane() },
		},
	}
}

func permissionByID(id string) (systemPermission, bool) {
	return permissionIn(systemPermissions(), id)
}

// permissionIn finds the item of list that has id.
func permissionIn(list []systemPermission, id string) (systemPermission, bool) {
	for _, p := range list {
		if p.id == id {
			return p, true
		}
	}
	return systemPermission{}, false
}

func missingSystemPermissions() []systemPermission {
	return missingPermissions(systemPermissions())
}

// missingPermissions filters list to the permissions the system's list does
// not hold: a grant proven but not yet usable by this process is not
// missing, it only needs a relaunch.
func missingPermissions(list []systemPermission) []systemPermission {
	var missing []systemPermission
	for _, p := range list {
		if !p.isGranted() {
			missing = append(missing, p)
		}
	}
	return missing
}

// restartPendingPermissions filters list to the grants that exist in
// System Settings but that this process still answers no to.
func restartPendingPermissions(list []systemPermission) []systemPermission {
	var pending []systemPermission
	for _, p := range list {
		if p.needsRelaunch() {
			pending = append(pending, p)
		}
	}
	return pending
}

func restartPendingSystemPermissions() []systemPermission {
	return restartPendingPermissions(systemPermissions())
}

// permissionShortNames joins the given permissions' short names the way a
// sentence in their language lists them: 完整磁盘访问、辅助功能、屏幕录制,
// or Full Disk Access, Accessibility, Screen Recording.
func permissionShortNames(ps []systemPermission) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.short
	}
	l := locales.In("")
	if len(ps) > 0 {
		l = ps[0].l
	}
	return strings.Join(names, l.PermListSeparator())
}

// permissionMenuItem is the tray item that replaces the old single
// permission entry. It counts what is missing at menu-build time, because
// the menu is rebuilt on every right-click.
func (a *shelf) permissionMenuItem() *mygo.MenuItem {
	l := a.appLoc()
	missing := missingSystemPermissions()
	if len(missing) == 0 {
		if pending := restartPendingSystemPermissions(); len(pending) > 0 {
			return &mygo.MenuItem{
				Label: l.PermMenuRelaunch(),
				Click: func(*mygo.MenuItem, *mygo.Window) { a.showPermissions() },
			}
		}
		return &mygo.MenuItem{Label: l.PermMenuAllOn(), Disabled: true}
	}
	return &mygo.MenuItem{
		Label: l.PermMenuMissing(len(missing)),
		Click: func(*mygo.MenuItem, *mygo.Window) { a.showPermissions() },
	}
}

// showPermissions opens (or focuses) the Settings window and immediately
// starts the guided flow.
func (a *shelf) showPermissions() {
	openSettingsWindow(a)
	// The guided walk lives on its own page: switching first means the
	// flow's captions are visible on an already-open window too.
	a.setSettingsPage(settingsPagePermissions)
	a.startPermissionFlow()
}

// openSettingsWindow is a seam: opening the window needs a running app loop,
// so the headless tests stub it and still exercise the click path.
var openSettingsWindow = func(a *shelf) { a.openSettings() }

// permissionStage is the guided flow's state. The zero value is idle.
type permissionStage int

const (
	permissionIdle permissionStage = iota
	// permissionWaiting: the current item's request has been made and the
	// flow is waiting for its grant. Accessibility is polled; the screen
	// recording step hands over to permissionRelaunch after a pause.
	permissionWaiting
	// permissionRelaunch: a grant that a running process may keep answering
	// stale for — Full Disk Access or screen recording — was made during the
	// walk, so ShelfU must be relaunched before it takes effect.
	permissionRelaunch
)

// permissionFlow is the state of the guided walk. ids is the snapshot of
// what was missing when the flow started, so a grant landing mid-flow
// cannot shift the step index. relaunch remembers that a permission whose
// answer this process keeps stale (Full Disk Access, screen recording)
// landed while the walk was running: the remaining items are still walked,
// and the walk then ends on the reopen stage instead of the ordinary
// all-granted one.
type permissionFlow struct {
	stage    permissionStage
	ids      []string
	step     int
	relaunch bool
	// unconfirmed marks the screen-recording handoff where no grant could
	// be proven before the relaunch: the walk must not claim the permission
	// is on. The relaunch is what re-reads the answer.
	unconfirmed bool
	timer       permissionTimer
}

// stopPermissionFlow ends the walk and leaves no timer behind. Closing the
// Settings window calls it too.
func (a *shelf) stopPermissionFlow() {
	if a.perm.timer != nil {
		a.perm.timer.Stop()
		a.perm.timer = nil
	}
	a.perm = permissionFlow{}
}

// startPermissionFlow begins (or restarts) the walk over the permissions
// that are missing right now. Already-granted permissions are skipped by
// beginPermissionStep and never requested, so the user is never asked for a
// permission that is already on; no path resets a TCC record.
func (a *shelf) startPermissionFlow() {
	a.stopPermissionFlow()
	missing := missingSystemPermissions()
	if len(missing) == 0 {
		// Nothing is left to ask for. A grant that exists but this process
		// cannot use yet still needs the reopen step, not silence.
		if len(restartPendingSystemPermissions()) > 0 {
			a.perm = permissionFlow{stage: permissionRelaunch}
		}
		a.repaintSettings()
		return
	}
	ids := make([]string, len(missing))
	for i, p := range missing {
		ids[i] = p.id
	}
	a.perm = permissionFlow{stage: permissionWaiting, ids: ids}
	a.repaintSettings()
	a.beginPermissionStep()
}

func (a *shelf) permissionFlowCurrent() (systemPermission, bool) {
	if a.perm.step < 0 || a.perm.step >= len(a.perm.ids) {
		return systemPermission{}, false
	}
	return permissionByID(a.perm.ids[a.perm.step])
}

// beginPermissionStep skips everything already granted, then issues the
// current item's request and arms its wait.
func (a *shelf) beginPermissionStep() {
	for a.perm.step < len(a.perm.ids) {
		p, ok := a.permissionFlowCurrent()
		if !ok {
			a.perm.step++
			continue
		}
		if p.isGranted() {
			if p.needsRelaunch() {
				a.perm.relaunch = true
			}
			a.perm.step++
			continue
		}
		granted := p.request()
		if p.id == screenCapturePermissionID && granted {
			screenGrantObserved.Store(true)
		}
		a.repaintSettings()
		if p.id == screenCapturePermissionID {
			a.armPermissionTimer(permissionScreenHandoff)
		} else {
			a.armPermissionTimer(permissionPollInterval)
		}
		return
	}
	// Nothing left: the flow ends in the ordinary all-granted state, or on
	// the relaunch stage when a Full Disk Access grant landed during it.
	if a.perm.relaunch {
		a.perm.stage = permissionRelaunch
		a.repaintSettings()
		return
	}
	a.stopPermissionFlow()
	a.repaintSettings()
}

func (a *shelf) armPermissionTimer(d time.Duration) {
	if a.perm.timer != nil {
		a.perm.timer.Stop()
	}
	a.perm.timer = permissionAfterFunc(d, func() {
		permissionDispatch(func() { a.permissionTimerFired() })
	})
}

// permissionTimerFired advances the walk on the UI thread. A callback that
// lands after 停止 or after the window closed finds the flow idle and does
// nothing.
func (a *shelf) permissionTimerFired() {
	a.perm.timer = nil
	if a.perm.stage != permissionWaiting {
		return
	}
	p, ok := a.permissionFlowCurrent()
	if !ok {
		a.stopPermissionFlow()
		a.repaintSettings()
		return
	}
	if p.id == screenCapturePermissionID {
		// The pane is open, but this process's cached preflight cannot
		// prove a grant made after its first answer. Window titles are the
		// uncached evidence; when even they cannot prove it, the step ends
		// as unconfirmed rather than claiming success. Either way the next
		// launch re-reads the real answer.
		a.perm.relaunch = true
		a.perm.unconfirmed = !p.isGranted()
		a.perm.stage = permissionRelaunch
		a.repaintSettings()
		return
	}
	if !p.isGranted() {
		a.armPermissionTimer(permissionPollInterval)
		return
	}
	if p.id == fullDiskPermissionID {
		// The grant is live for new opens, but this process may keep its
		// old answer for file access; the walk goes on and ends on the
		// relaunch stage.
		a.perm.relaunch = true
	}
	a.perm.step++
	a.beginPermissionStep()
}

// relaunchPlan is how the Settings window restarts this exact app after the
// old process has released the data-directory lock.
type relaunchPlan struct {
	Exec string
	Args []string
}

// relaunchPlanFor builds a detached /bin/sh wait-then-launch: the new
// process starts only after pid exits, so the single-instance flock is free
// and the user never sees the 「ShelfU 已在运行」 dialog. Inside an app
// bundle the launch goes through LaunchServices (`open -n`), which is what
// gives the restarted process the app's registered identity and TCC
// responsibility — the screen-recording grant this relaunch exists for
// would not follow an exec of Contents/MacOS. Outside a bundle (go test, a
// development run) the executable is started directly. The original
// arguments are preserved, including --data-dir.
func relaunchPlanFor(exe string, args []string, pid int) relaunchPlan {
	var b strings.Builder
	fmt.Fprintf(&b, "while kill -0 %d 2>/dev/null; do sleep 0.1; done; sleep 0.3; ", pid)
	if bundle, ok := appBundleForExecutable(exe); ok {
		fmt.Fprintf(&b, "exec /usr/bin/open -n %s", shellQuote(bundle))
		if len(args) > 0 {
			b.WriteString(" --args")
		}
	} else {
		b.WriteString("exec ")
		b.WriteString(shellQuote(exe))
	}
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(shellQuote(arg))
	}
	return relaunchPlan{Exec: "/bin/sh", Args: []string{"-c", b.String()}}
}

// appBundleForExecutable reports the enclosing .app when exe lives at
// <Bundle>.app/Contents/MacOS/<binary>. It is a pure path check; callers
// only use it to choose the launch route. The Full Disk Access reveal needs
// the same answer, so the check itself lives in internal/mac.
func appBundleForExecutable(exe string) (string, bool) {
	return mac.AppBundleForExecutable(exe)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Seams for the relaunch tests: nothing in a test may start a process or
// quit the app.
var (
	relaunchExecutable = os.Executable
	relaunchStart      = func(p relaunchPlan) error {
		cmd := exec.Command(p.Exec, p.Args...)
		// Detach from this process so quitting the app does not take the
		// waiter down with it.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return cmd.Start()
	}
	relaunchQuit = mygo.App.Quit
)

// relaunchShelf restarts ShelfU with the same arguments so the cached
// screen-recording preflight and the process's Full Disk Access answer are
// re-read. It starts the waiter first and quits only once that succeeded; a
// failure stays visible in the window.
func (a *shelf) relaunchShelf() {
	exe, err := relaunchExecutable()
	if err != nil {
		a.report(err)
		return
	}
	plan := relaunchPlanFor(exe, os.Args[1:], os.Getpid())
	if err := relaunchStart(plan); err != nil {
		a.report(err)
		return
	}
	a.stopPermissionFlow()
	relaunchQuit()
}

// permissionCaption is the row's caption: what is left, or which step of the
// walk the user is on. Pure apart from the flow's own state. permissions
// carries the names in the language of l, so a name said here follows the
// window rather than the source language.
func (a *shelf) permissionCaption(l locales.Messages, permissions, missing []systemPermission) string {
	switch a.perm.stage {
	case permissionWaiting:
		if a.perm.step < 0 || a.perm.step >= len(a.perm.ids) {
			break
		}
		return l.PermStep(a.perm.step+1, len(a.perm.ids))
	case permissionRelaunch:
		if a.perm.unconfirmed {
			return l.PermRelaunchUnconfirmed()
		}
		return l.PermRelaunchGranted()
	}
	if len(missing) == 0 {
		if len(restartPendingPermissions(permissions)) > 0 {
			return l.PermRelaunchGranted()
		}
		return l.PermAllGranted(len(permissions), len(permissions))
	}
	return l.PermMissing(len(missing), permissionShortNames(missing))
}

// settingsPermissionSwitch is the group's one row: the title and the state
// caption on the left, one switch on the right. The switch is on only when
// every permission is granted; while the walk waits it shows on, because the
// walk is what is running, and a quiet 停止 sits next to it. When nothing is
// missing and a grant still needs a relaunch, the accent button replaces the
// switch: nothing is left to switch until the app has restarted.
func (a *shelf) settingsPermissionSwitch(c *ui.Context, permissions, missing []systemPermission) {
	l := loc(c)
	title := l.PermTitle()
	// The caption says what is missing, which step the walk is on, or what a
	// pending reopen affects. With everything on and no walk running, it
	// would only repeat the sidebar's status.
	caption := ""
	if a.perm.stage != permissionIdle || len(missing) > 0 || len(restartPendingPermissions(permissions)) > 0 {
		caption = a.permissionCaption(l, permissions, missing)
	}
	// A pending reopen replaces the switch only when nothing is left to ask
	// for: with a missing permission too, the switch must stay usable or the
	// walk could not be started from this row.
	relaunch := a.perm.stage == permissionRelaunch ||
		(a.perm.stage == permissionIdle && len(missing) == 0 &&
			len(restartPendingPermissions(permissions)) > 0)
	if relaunch {
		ui.Row(c.Key("perm-row")).AlignItems(ui.Center).Gap(10).MinHeight(48).Padding(settingsRowPadY, 12).FillWidth().Children(func() {
			permissionRowLabel(c, title, caption)
			settingsAccentButton(c, "relaunch", l.PermRelaunchButton(), func() { a.relaunchShelf() })
		})
		return
	}
	on := len(missing) == 0 || a.perm.stage == permissionWaiting
	row := ui.ToggleBase(c.Key("toggle-permissions"), &on).FillWidth().AlignItems(ui.Center).
		Gap(10).MinHeight(48).Padding(settingsRowPadY, 12).Label(title)
	var sw ui.Element
	row.Children(func() {
		permissionRowLabel(c, title, caption)
		ui.Row(c.Key("perm-ctl")).AlignItems(ui.Center).Gap(8).Children(func() {
			if a.perm.stage == permissionWaiting {
				settingsQuietButton(c, "stop", l.PermStop(), func() {
					a.stopPermissionFlow()
					a.repaintSettings()
				})
			}
			sw = ui.Switch(c, &on).Label(title)
		})
	})
	if row.Changed() || sw.Changed() {
		a.applyPermissionSwitch(on)
	}
}

// permissionAppDragData offers the running app bundle as a file URL. That
// is what System Settings accepts: dropped on a privacy list it adds Rakko
// to the list, so the user never has to find the app through +.
func permissionAppDragData() transfer.Data {
	bundle, ok := permissionBundlePath()
	if !ok {
		return transfer.Data{}
	}
	data, err := transfer.FileData(bundle)
	if err != nil {
		return transfer.Data{}
	}
	return data
}

// permissionAppIcon is one row's real app icon. When the app runs from a
// bundle the icon is also the drag source; a click always opens the row's
// pane, and outside a bundle (go run, go test) the generic mark stands in
// and the row still offers 「打开设置」 and the group's fallback note.
func (a *shelf) permissionAppIcon(c *ui.Context, l locales.Messages, p systemPermission, bundle string, draggable bool) {
	label := l.PermIconLabelFallback(p.short)
	if draggable {
		label = l.PermIconLabel(p.short)
	}
	btn := ui.ButtonBase(c.Key("perm-app-"+p.id)).Size(30, 30).Radius(7).
		AlignItems(ui.Center).Label(label)
	if draggable {
		btn.Tooltip(l.PermDragTooltip())
		btn.DragDataFrom(permissionAppDragData)
	} else {
		btn.Tooltip(l.PermNoBundleHint())
	}
	if btn.Hovered() {
		btn.Background(setCtlHover)
	}
	btn.Children(func() {
		if icon := permissionIcon(bundle); icon != nil {
			ui.Image(c.Key("img"), icon).Size(28, 28).Radius(7).Fit(ui.Contain)
			return
		}
		ui.Icon(c, appGlyph).Size(24, 24).TextColor(ink2)
	})
	if btn.Clicked() {
		p.pane()
	}
}

// permissionRowLabel is the switch row's left column.
func permissionRowLabel(c *ui.Context, title, caption string) {
	ui.Column(c.Key("perm-k")).Gap(2).Grow(1).MinWidth(0).Children(func() {
		settingsLabel(c, title, 13, 0).TextColor(ink)
		if caption != "" {
			ui.Text(c, caption).FontSize(11).TextColor(ink3)
		}
	})
}

// applyPermissionSwitch is what the one switch does. On starts the walk over
// whatever is missing. Off while the walk waits stops it. Off when everything
// is granted cannot revoke anything — macOS gives no app that power — so it
// opens 隐私与安全性 and the row's probe keeps the switch on.
func (a *shelf) applyPermissionSwitch(on bool) {
	switch {
	case on:
		a.startPermissionFlow()
	case a.perm.stage == permissionWaiting:
		a.stopPermissionFlow()
		a.repaintSettings()
	default:
		openPrivacyPane()
		a.repaintSettings()
	}
}

// settingsAccentButton is the mock's compact accent pill used by the
// permission header.
func settingsAccentButton(c *ui.Context, key, label string, fn func()) {
	b := ui.ButtonBase(c.Key("perm-"+key)).Height(24).Radius(12).Padding(0, 10, 0, 10).
		Background(settingsTheme.Accent).Center().Label(label)
	if b.Pressed() {
		b.Background(settingsTheme.AccentPressed)
	} else if b.Hovered() {
		b.Background(settingsTheme.AccentHover)
	}
	b.Children(func() {
		ui.Text(c, label).SingleLine().FontSize(12).FontWeight(600).TextColor(ui.RGB(255, 255, 255))
	})
	if b.Clicked() {
		fn()
	}
}

// settingsQuietButton is the same pill without the accent fill, for 停止.
func settingsQuietButton(c *ui.Context, key, label string, fn func()) {
	b := ui.ButtonBase(c.Key("perm-"+key)).Height(24).Radius(12).Padding(0, 10, 0, 10).
		Background(ui.RGBA(255, 255, 255, 0.10)).Center().Label(label)
	if b.Hovered() {
		b.Background(ui.RGBA(255, 255, 255, 0.16))
	}
	b.Children(func() {
		ui.Text(c, label).SingleLine().FontSize(12).FontWeight(600).TextColor(ink)
	})
	if b.Clicked() {
		fn()
	}
}
