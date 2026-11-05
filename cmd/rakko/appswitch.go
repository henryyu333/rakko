//go:build darwin

package main

// 应用切换：刘海两翼从刘海两侧展开，左翼最多 4 个固定常用应用，右翼 3
// 个其他运行中应用加「更多」。它是收起态的一种内容（peek 家族），不是
// 展开的页面：面板保持收起，不拿键盘，Esc / ← / → / ⏎ 由全局注册临时
// 持有。固定集合按 bundle id 持久化在 settings.PinnedApps；nil 表示用户
// 没编辑过，按默认常用（访达 / Safari / 终端 / 备忘录）过滤已安装来显示；
// 显式保存的列表（含空）原样保留——默认与空是两个状态。

import (
	"errors"
	"fmt"
	"sort"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

// defaultPinnedApps 是首次使用前从未编辑过固定列表时的候选：按顺序尝试，
// 只加入系统能解析出应用的那几个；某个默认应用缺失不会让整组失败。
var defaultPinnedApps = []string{
	"com.apple.finder",
	"com.apple.Safari",
	"com.apple.Terminal",
	"com.apple.Notes",
}

// appEntry 是两翼里的一个应用：固定位或运行中位。PID 只对本次快照有
// 效；missing 为真时它是已卸载或解析不到的固定应用，只给移除入口。
type appEntry struct {
	bundleID string
	name     string
	pid      int  // >0 表示正在运行
	missing  bool // 固定但已卸载或解析不到：占位，只给移除入口
	pinned   bool
}

// appSwitcher 是切换模式自己的状态：开关、本次快照、键盘选择、「更多 /
// 管理」浮层和已安装应用缓存。模式打开时 list 是打开时那一帧的快照，
// 之后每次重开或刷新浮层才重取——不做后台轮询。
type appSwitcher struct {
	on           bool              // 模式打开：壳是应用两翼，透明区收自己的点击
	list         []appEntry        // 快照：固定在前（含 missing）、其他运行中在后
	sel          int               // 键盘选择（只数可激活的应用，selApps 的下标）；<0 = 无
	more         bool              // 「更多」浮层开着
	manage       bool              // 「编辑常用应用」浮层开着（比 more 深一层）
	appsScroll   ui.ScrollState    // 浮层列表的滚动
	missingNames map[string]string // 已卸载固定应用的展示名缓存（bundle → 名字）

	installed     []mygo.InstalledApp   // 已安装应用缓存（管理浮层懒加载）
	installedLoad bool                  // 后台取数在途
	icons         map[string]*ui.Bitmap // bundle → 64pt 图标缓存（跨开关复用）
	keys          map[string]bool       // 模式持有的全局键（Left/Right/Enter，可能含 Escape）
	popRect       mygo.Rectangle        // 浮层在屏幕上的盒子（命中测试用）
	entryRect     mygo.Rectangle        // 收起态应用入口按钮的盒子（命中测试用）
}

// appsModeOn reports whether the switcher owns the closed shell right now.
func (a *shelf) appsModeOn() bool { return a.apps.on }

// appSeam 是测试替身挂口：系统查询与激活都走这几条，行为测试不用真的
// 切到用户的应用。
var (
	appRunning    = mac.RunningApps
	appPathFor    = mac.AppPathForBundleID
	appNameFor    = mac.AppNameForBundleID
	appsInstalled = func() ([]mygo.InstalledApp, error) { return mygo.Shell.InstalledApps() }
)

// pinnedIDs returns the saved pinned list, or nil when the user never
// edited it — the caller falls back to the filtered defaults. A copy: the
// mutators walk off it and a refused applySettings must leave the saved
// list untouched.
func (a *shelf) pinnedIDs() []string {
	if a.settings.PinnedApps == nil {
		return nil
	}
	return append([]string(nil), (*a.settings.PinnedApps)...)
}

// effectivePinnedIDs is what the wings pin: the saved list, or the
// defaults minus whatever is not installed — a missing default must not
// hold an empty seat or fail the whole row.
func (a *shelf) effectivePinnedIDs() []string {
	if saved := a.pinnedIDs(); saved != nil {
		return saved
	}
	var ids []string
	for _, id := range defaultPinnedApps {
		if appPathFor(id) != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// appSnapshot rebuilds the switch list: pinned order first (missing ones
// kept, so they can be removed), then the other running GUI apps sorted by
// name. Pinned apps already running appear once, in the pinned zone.
func (a *shelf) appSnapshot() []appEntry {
	running := appRunning()
	byID := make(map[string]mac.RunningApp, len(running))
	for _, r := range running {
		if _, dup := byID[r.BundleID]; !dup {
			byID[r.BundleID] = r
		}
	}
	pinned := a.effectivePinnedIDs()
	list := make([]appEntry, 0, len(pinned)+len(running))
	for _, id := range pinned {
		e := appEntry{bundleID: id, pinned: true}
		if r, ok := byID[id]; ok {
			e.name, e.pid = r.Name, r.PID
		} else if appPathFor(id) != "" {
			e.name = a.switchAppName(id)
		} else {
			e.missing = true
			e.name = a.switchAppName(id)
		}
		list = append(list, e)
	}
	rest := make([]mac.RunningApp, 0, len(running))
	pinSet := make(map[string]bool, len(pinned))
	for _, id := range pinned {
		pinSet[id] = true
	}
	for _, r := range running {
		if !pinSet[r.BundleID] {
			rest = append(rest, r)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool {
		li, lj := rest[i].Name, rest[j].Name
		if li == lj {
			return rest[i].BundleID < rest[j].BundleID
		}
		return li < lj
	})
	for _, r := range rest {
		list = append(list, appEntry{bundleID: r.BundleID, name: r.Name, pid: r.PID})
	}
	return list
}

// switchAppName resolves a display name for a bundle id, cached: running
// app names come with the snapshot, installed ones ask LaunchServices once.
func (a *shelf) switchAppName(bundleID string) string {
	if a.apps.missingNames == nil {
		a.apps.missingNames = map[string]string{}
	}
	if n, ok := a.apps.missingNames[bundleID]; ok {
		return n
	}
	n := appNameFor(bundleID)
	if n == "" {
		n = bundleID // 找不到名字时显示 bundle id，用户仍能认出并移除
	}
	a.apps.missingNames[bundleID] = n
	return n
}

// openAppSwitch expands the app wings on the closed shell. Any open panel
// folds first — the modes are exclusive — and the wings then grow on the
// same peek shape the hover pill uses. A second call while open is the
// toggle: it closes.
func (a *shelf) openAppSwitch() {
	if a.apps.on {
		a.endAppSwitch()
		return
	}
	if a.visible {
		// 切换模式与搜索 / 页面 / 投放互斥：面板先走自己的收起路径
		// （含焦点归还），两翼随后从收起态长出。
		a.hide(hideExplicit)
	}
	a.endAppSwitch() // 保险：清空残留（理论上已不在模式里）
	a.apps = appSwitcher{on: true, sel: -1, icons: a.apps.icons, missingNames: a.apps.missingNames, installed: a.apps.installed}
	a.apps.list = a.appSnapshot()
	a.holdAppsKeys()
	a.setPeek(true)
	a.syncMousePassThrough()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// endAppSwitch closes the wings: keys return, the popover dies with the
// mode, and the shell folds back to whatever peek content the closed shelf
// draws next — a track's wings, a queued notice or the chevron.
func (a *shelf) endAppSwitch() {
	if !a.apps.on {
		return
	}
	a.apps.on = false
	a.apps.more, a.apps.manage = false, false
	a.apps.popRect, a.apps.entryRect = mygo.Rectangle{}, mygo.Rectangle{}
	a.releaseAppsKeys()
	a.setPeek(false)
	a.syncMousePassThrough()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// appSwitchKeys are the global keys the mode holds while open: Esc cancels,
// ←/→ move the selection, ⏎ switches. Escape is shared with the panel's own
// hold (escHeld): if the panel already took it, this mode rides the same
// registration through a.escape's routing instead of grabbing a second one.
var appSwitchKeys = []string{"Left", "Right", "Enter"}

// holdAppsKeys registers the selection keys for as long as the mode is
// open; failures just leave that key to the mouse — the wings still work.
func (a *shelf) holdAppsKeys() {
	if a.apps.keys == nil {
		a.apps.keys = map[string]bool{}
	}
	for _, k := range appSwitchKeys {
		cb := func() { a.appsKey(k) }
		key := k
		if err := registerKey(key, cb); err == nil {
			a.apps.keys[key] = true
		}
	}
	if !a.escHeld {
		if err := registerKey("Escape", a.escape); err == nil {
			a.apps.keys["Escape"] = true
		}
	}
}

// releaseAppsKeys returns every key the mode holds. The shared Escape hold
// is released only when this mode registered it.
func (a *shelf) releaseAppsKeys() {
	for k := range a.apps.keys {
		unregisterKey(k)
	}
	a.apps.keys = nil
}

// appsKey routes a global selection key while the mode is open.
func (a *shelf) appsKey(key string) {
	if !a.apps.on {
		return
	}
	switch key {
	case "Left":
		a.moveAppSel(-1)
	case "Right":
		a.moveAppSel(1)
	case "Enter":
		a.activateAppSel()
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// selApps are the selectable entries in shown order: pinned seats that can
// actually launch (missing ones are占位 but not selectable), then the other
// running apps the wings draw. 「更多」 is not in this list — the design's
// left/right walk only visits app buttons. The list is the same shown set
// the view draws, so a seat hidden by a narrow screen is never selected.
func (a *shelf) selApps() []appEntry { return a.shownSelectable() }

// moveAppSel walks the selection across the selectable buttons, left wing
// first then right, wrapping at both ends.
func (a *shelf) moveAppSel(d int) {
	n := len(a.selApps())
	if n == 0 {
		a.apps.sel = -1
		return
	}
	a.apps.sel = (a.apps.sel + d + n) % n
}

// activateAppSel runs the selected app, like a click on its button.
func (a *shelf) activateAppSel() {
	apps := a.selApps()
	if a.apps.sel < 0 || a.apps.sel >= len(apps) {
		return
	}
	a.switchToApp(apps[a.apps.sel])
}

// switchToApp activates a running app or launches a pinned-but-not-running
// one. A missing entry can only be removed (the view never calls this for
// it). On any failure the error is surfaced and the wings stay closed —
// the user can reopen and retry; a fake "switched" state is never shown.
func (a *shelf) switchToApp(e appEntry) {
	if e.missing {
		return
	}
	// A process that ended after the snapshot fails activate and falls to
	// launch-by-bundle; a path that vanished fails the launch and reports.
	if e.pid > 0 && activateApp(e.pid) {
		a.endAppSwitch()
		return
	}
	path := appPathFor(e.bundleID)
	if path == "" {
		a.endAppSwitch()
		a.report(errors.New(e.name + ": " + a.appLoc().AppsUnavailable()))
		return
	}
	if err := openPath(path); err != nil {
		a.endAppSwitch()
		a.report(fmt.Errorf("%s: %w", e.name, err))
		return
	}
	a.endAppSwitch()
}

// setPinnedApps writes the pinned list through applySettings, which refuses
// a broken settings.json; the wings re-read a.settings next frame.
func (a *shelf) setPinnedApps(ids []string) {
	next := a.settings
	cp := make([]string, len(ids))
	copy(cp, ids)
	next.PinnedApps = &cp
	a.applySettings(next)
}

// pinApp adds a bundle id to the pinned list when there is a seat left;
// the cap is enforced here too, so no caller can silently drop a request.
func (a *shelf) pinApp(bundleID string) {
	ids := a.effectivePinnedIDs()
	for _, id := range ids {
		if id == bundleID {
			return
		}
	}
	if len(ids) >= settings.PinnedAppsMax {
		return
	}
	a.setPinnedApps(append(ids, bundleID))
	a.apps.list = a.appSnapshot()
}

// unpinApp removes a bundle id from the pinned list.
func (a *shelf) unpinApp(bundleID string) {
	ids := a.effectivePinnedIDs()
	out := ids[:0]
	removed := false
	for _, id := range ids {
		if id == bundleID {
			removed = true
			continue
		}
		out = append(out, id)
	}
	if !removed {
		return
	}
	a.setPinnedApps(out)
	a.apps.list = a.appSnapshot()
}

// movePinned shifts a pinned entry by d seats; out-of-range requests are a
// no-op, so the row buttons can always fire.
func (a *shelf) movePinned(bundleID string, d int) {
	ids := append([]string(nil), a.effectivePinnedIDs()...)
	i := -1
	for j, id := range ids {
		if id == bundleID {
			i = j
			break
		}
	}
	to := i + d
	if i < 0 || to < 0 || to >= len(ids) {
		return
	}
	ids[i], ids[to] = ids[to], ids[i]
	a.setPinnedApps(ids)
	a.apps.list = a.appSnapshot()
}

// ensureInstalled fills the installed-apps cache for the manage popover,
// once per open of the mode: the disk scan runs off the main thread.
func (a *shelf) ensureInstalled() {
	if a.apps.installed != nil || a.apps.installedLoad {
		return
	}
	a.apps.installedLoad = true
	go func() {
		list, err := appsInstalled()
		mygo.RunOnMain(func() {
			a.apps.installedLoad = false
			if err != nil {
				// 取不到已安装清单时管理浮层退化为只列固定项 + 运行中应用，
				// 仍能取消固定和排序，不假装安装列表为空。
				a.apps.installed = nil
			} else {
				a.apps.installed = list
			}
			if a.win != nil {
				a.win.Invalidate()
			}
		})
	}()
}

// toggleAppSwitch is the App Switcher global shortcut: the same path the
// entry click walks — a second press folds the wings back.
func (a *shelf) toggleAppSwitch() {
	if a.apps.on {
		a.endAppSwitch()
		return
	}
	a.openAppSwitch()
}
