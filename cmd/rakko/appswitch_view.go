//go:build darwin

package main

// 应用切换的绘制：两翼是收起态内容（peek 家族的一员），壳按 appsSize
// 长成；「更多 / 编辑常用应用」浮层是挂在壳下的玻璃窗格，与搜索结果
// 面板、小岛导航同一套材质。几何规则：左翼最多 4 个固定位，右翼运行
// 中位 + 固定保留的「更多」位；屏幕放不下时按位收窄，溢出的应用只进
// 「更多」浮层，壳本身永远不截断按钮、不长出屏幕。

import (
	"fmt"
	"sort"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/settings"
	"rakko/locales"
)

const (
	appBtn    = 46 // 两翼按钮
	appIcon   = 32 // 按钮里的应用图标
	appGap    = 12 // 按钮间距
	appPadX   = 18 // 翼外侧留白
	appsBody  = 28 // 两翼在锚点（刘海 / 灵动岛）下面占的带
	appsSlots = 4  // 单翼最多按钮数

	appPopW    = 280 // 浮层宽
	appPopMaxH = 380 // 浮层最大高
	appPopRowH = 38  // 浮层行高
	appPopHead = 34  // 浮层头行
	appPopFoot = 40  // 浮层尾行
	appPopGap  = 10  // 壳 → 浮层间距
	appPopR    = 16  // 浮层圆角
	appEntryW  = 30  // 收起态应用入口按钮的宽
)

var (
	// appsSymbol 是应用入口的 2×2 方格，和工具页的三块不对称格区分开。
	appsSymbol = symbol(`<rect x="4" y="4" width="7" height="7" rx="2"/><rect x="13" y="4" width="7" height="7" rx="2"/><rect x="4" y="13" width="7" height="7" rx="2"/><rect x="13" y="13" width="7" height="7" rx="2"/>`)
	moreSymbol = fillSymbol(`<circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/>`)
)

// appsSlots is how many buttons each wing can hold on this display: the
// distance from the anchor's flank to the screen's edge, minus margins and
// the wing's own padding, divided by the button pitch. Both wings always
// keep at least one seat — the rest overflows into 更多, never clipped.
// Pure apart from dispWork/anchorSize reads; unit-tested.
func (a *shelf) appsSlots() (left, right int) {
	baseW, _ := a.anchorSize()
	work := a.dispWork
	if work.Width <= 0 {
		work = a.dispBounds
	}
	half := float64(work.Width)/2 - float64(baseW)/2 - appPadX - 8
	fit := int((half + appGap) / (appBtn + appGap))
	left, right = min(fit, appsSlots), min(fit, appsSlots)
	if left < 1 {
		left = 1
	}
	if right < 2 {
		right = 2 // 「更多」永远在末位之外至少留一个应用位
	}
	return left, right
}

// appsWingW is one wing's share of the shell width for n button seats.
func appsWingW(n int) float32 {
	return float32(appPadX + n*appBtn + (n-1)*appGap + appPadX)
}

// appsSize is the switcher's peek shape: the anchor plus one wing per side,
// the band below it tall enough for the buttons and the collapse affordance.
func (a *shelf) appsSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	l, r := a.appsSlots()
	side := appsWingW(max(l, r))
	return baseW + 2*side, baseH + appsBody
}

// appsSplit splits the snapshot into the pinned zone and the running zone.
func (a *shelf) appsSplit() (pinned, running []appEntry) {
	for _, e := range a.apps.list {
		if e.pinned {
			pinned = append(pinned, e)
		} else {
			running = append(running, e)
		}
	}
	return pinned, running
}

// appsShown are the buttons the wings draw now: up to the left wing's seats
// of pinned, then up to the right wing's seats minus the 「更多」 seat of
// other running apps. Overflow is only ever in 更多 / manage, never dropped.
func (a *shelf) appsShown() (left, right []appEntry) {
	pinned, running := a.appsSplit()
	ls, rs := a.appsSlots()
	if len(pinned) > ls {
		pinned = pinned[:ls]
	}
	seats := rs - 1
	if len(running) > seats {
		running = running[:seats]
	}
	return pinned, running
}

// selApps overrides the definition in appswitch.go only conceptually: it is
// the same shown order the wings draw — selectable entries in left-then-
// right order, missing pinned entries excluded.
func (a *shelf) shownSelectable() []appEntry {
	left, right := a.appsShown()
	out := make([]appEntry, 0, len(left)+len(right))
	for _, e := range left {
		if !e.missing {
			out = append(out, e)
		}
	}
	return append(out, right...)
}

// moreRows is what the 更多 popover lists: pinned seats that did not fit
// the left wing (still switchable — missing ones are manage-only), then
// every other running app past the right wing's seats.
func (a *shelf) moreRows() []appEntry {
	pinned, running := a.appsSplit()
	ls, rs := a.appsSlots()
	var out []appEntry
	if len(pinned) > ls {
		for _, e := range pinned[ls:] {
			if !e.missing {
				out = append(out, e)
			}
		}
	}
	seats := rs - 1
	if len(running) > seats {
		out = append(out, running[seats:]...)
	}
	return out
}

// manageRow is one row of the 编辑常用应用 popover: a pinned entry first
// (ordered, removable, movable), then every other installed or running app
// the user can pin.
type manageRow struct {
	appEntry
	order int  // position inside the pinned list; -1 for unpinned
	full  bool // the pinned list is full: a pin click would be dropped
}

// manageRows builds the manage list: pinned in saved order, then unpinned
// candidates (installed ∪ running by bundle id) sorted by name.
func (a *shelf) manageRows() []manageRow {
	pinnedIDs := a.effectivePinnedIDs()
	pinSet := make(map[string]bool, len(pinnedIDs))
	byID := make(map[string]appEntry, len(a.apps.list))
	for _, e := range a.apps.list {
		byID[e.bundleID] = e
	}
	var out []manageRow
	for i, id := range pinnedIDs {
		pinSet[id] = true
		e := byID[id]
		e.pinned, e.bundleID = true, id
		if e.name == "" {
			e.name = a.switchAppName(id)
		}
		if !e.missing && appPathFor(id) == "" && e.pid == 0 {
			e.missing = true
		}
		out = append(out, manageRow{appEntry: e, order: i, full: len(pinnedIDs) >= settings.PinnedAppsMax})
	}
	type cand struct {
		id   string
		name string
	}
	seen := map[string]bool{}
	var cands []cand
	for _, app := range a.apps.installed {
		if app.ID == "" || pinSet[app.ID] || seen[app.ID] {
			continue
		}
		seen[app.ID] = true
		name := app.Name
		if name == "" {
			name = a.switchAppName(app.ID)
		}
		cands = append(cands, cand{id: app.ID, name: name})
	}
	for _, e := range a.apps.list {
		if e.pinned || seen[e.bundleID] {
			continue
		}
		seen[e.bundleID] = true
		cands = append(cands, cand{id: e.bundleID, name: e.name})
	}
	// 已安装清单还没回来时（后台在途或取数失败）候选人至少包含运行中应用，
	// 界面永远不假装「没有可固定的应用」。
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].name == cands[j].name {
			return cands[i].id < cands[j].id
		}
		return cands[i].name < cands[j].name
	})
	full := len(pinnedIDs) >= settings.PinnedAppsMax
	for _, cd := range cands {
		e := byID[cd.id]
		e.bundleID, e.name = cd.id, cd.name
		out = append(out, manageRow{appEntry: e, order: -1, full: full})
	}
	return out
}

// switchIcon returns the 64pt icon for a bundle id, cached across opens of
// the mode. A nil bitmap means the app has no resolvable icon — the caller
// falls back to the app grid glyph.
func (a *shelf) switchIcon(bundleID string) *ui.Bitmap {
	if a.apps.icons == nil {
		a.apps.icons = map[string]*ui.Bitmap{}
	}
	if b, ok := a.apps.icons[bundleID]; ok {
		return b
	}
	if len(a.apps.icons) >= appIconCacheLimit {
		a.apps.icons = map[string]*ui.Bitmap{}
	}
	var b *ui.Bitmap
	if png := mac.AppIcon(bundleID, 64); len(png) > 0 {
		if decoded, err := ui.DecodeBitmap(png); err == nil {
			b = decoded
		}
	}
	a.apps.icons[bundleID] = b
	return b
}

// appsView draws the switcher inside the peek shell: the left wing's pinned
// buttons, the right wing's running buttons and the 「更多」 seat, and the
// collapse chevron under the anchor. shellW/shellH are the drawn shell this
// frame; k is the peek reveal. Layout is at the final wing size inside a
// clip, the same contract the hover wings use.
func (a *shelf) appsView(c *ui.Context, shellW, shellH, k float32) {
	l := loc(c)
	baseW, _ := a.anchorSize()
	left, right := a.appsShown()
	ls, rs := a.appsSlots()
	side := appsWingW(max(ls, rs))
	sel := a.selApps()
	body := ui.Row(c.Key("apps-wings")).Width(shellW).Height(shellH).
		Opacity(min(max(k*k, 0), 1)).AlignItems(ui.Center)
	selIx := make(map[string]int, len(sel))
	for i, e := range sel {
		selIx[e.bundleID] = i
	}
	picked := func(e appEntry) bool {
		i, ok := selIx[e.bundleID]
		return ok && a.apps.sel == i
	}
	body.Children(func() {
		// 左翼：固定位，左对齐贴翼外缘。
		lw := ui.Row(c.Key("apps-left")).Width(side).Height(shellH).
			Padding(0, appPadX, 0, appPadX).Gap(appGap).AlignItems(ui.Center).Justify(ui.Start)
		lw.Children(func() {
			for _, e := range left {
				a.appButton(c, e, picked(e))
			}
			for n := len(left); n < ls; n++ {
				a.appsAddSeat(c, n)
			}
		})
		// 中央锚带：刘海或灵动岛本身，底部一个收起箭头。
		mid := ui.Column(c.Key("apps-mid")).Width(baseW).Height(shellH).
			Justify(ui.End).AlignItems(ui.Center)
		mid.Children(func() {
			b := ui.ButtonBase(c.Key("apps-close")).Size(38, 22).Radius(7).Center().
				Label(l.AppsCollapse()).Tooltip(l.AppsCollapse())
			if b.Hovered() {
				b.Background(fill2)
			}
			b.Children(func() { ui.Icon(c, chevronUpSymbol).Size(12, 12).TextColor(ink2) })
			if b.Clicked() {
				a.endAppSwitch()
			}
		})
		// 右翼：其他运行中应用右对齐，末位固定是「更多」。
		rw := ui.Row(c.Key("apps-right")).Width(side).Height(shellH).
			Padding(0, appPadX, 0, appPadX).Gap(appGap).AlignItems(ui.Center).Justify(ui.End)
		rw.Children(func() {
			for _, e := range right {
				a.appButton(c, e, picked(e))
			}
			a.appsMoreButton(c)
		})
	})
	// 选中态：selApps 与两翼同一顺序，sel 下标直接对上按钮位置。
	// 浮层在壳外单独画（见 appsPopView），这里只管翼内。
}

// appButton is one wing seat: the 46pt hover target with the 32pt app
// icon, a running dot under it, and the app's name as tooltip and
// accessible label. A missing pinned app draws dimmed and does nothing —
// it can only be removed from the manage list.
func (a *shelf) appButton(c *ui.Context, e appEntry, selected bool) {
	l := loc(c)
	b := ui.ButtonBase(c.Key("app-"+e.bundleID)).Size(appBtn, appBtn).Radius(10).Center()
	label := l.AppsSwitchTo(e.name)
	if e.pid == 0 && !e.missing {
		label = l.AppsLaunch(e.name)
	}
	if e.missing {
		label = l.AppsUninstalledLabel(e.name)
	}
	b.Label(label).Tooltip(label)
	switch {
	case selected:
		b.Background(fill3)
	case b.Hovered() && !e.missing:
		b.Background(fill2)
	}
	b.Children(func() {
		ui.Column(c).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if icon := a.switchIcon(e.bundleID); icon != nil {
				im := ui.Image(c.Key("ic-"+e.bundleID), icon).Size(appIcon, appIcon)
				if e.missing {
					im.Opacity(0.35)
				}
			} else {
				g := ui.Icon(c, appsSymbol).Size(24, 24).TextColor(ink2)
				if e.missing {
					g.Opacity(0.35)
				}
			}
		})
		if e.pid > 0 {
			ui.Box(c.Key("dot-"+e.bundleID)).Absolute().Bottom(4).Left(appBtn/2-2).Size(4, 4).Radius(2).Background(ink2)
		}
	})
	if !e.missing && b.Clicked() {
		a.switchToApp(e)
	}
}

// appsAddSeat is the left wing's 添加入口: a plus seat that opens the
// manage popover. It only appears while a pinned seat is unused.
func (a *shelf) appsAddSeat(c *ui.Context, seat int) ui.Element {
	l := loc(c)
	b := ui.ButtonBase(c.Key(fmt.Sprintf("apps-add-%d", seat))).Size(appBtn, appBtn).Radius(10).Center().
		Label(l.AppsEdit()).Tooltip(l.AppsEdit())
	if b.Hovered() {
		b.Background(fill2)
	}
	b.Children(func() { ui.Icon(c, plusSymbol).Size(18, 18).TextColor(ink2) })
	if b.Clicked() {
		a.appsManageOpen()
	}
	return b
}

// appsMoreButton is the right wing's last seat: it opens the popover with
// the overflow running apps and the manage entry.
func (a *shelf) appsMoreButton(c *ui.Context) {
	l := loc(c)
	b := ui.ButtonBase(c.Key("apps-more")).Size(appBtn, appBtn).Radius(10).Center().
		Label(l.AppsMore()).Tooltip(l.AppsMore())
	switch {
	case a.apps.more:
		b.Background(fill3)
	case b.Hovered():
		b.Background(fill2)
	}
	b.Children(func() { ui.Icon(c, moreSymbol).Size(18, 18).TextColor(ink2) })
	if b.Clicked() {
		a.toggleAppsMore()
	}
}

// toggleAppsMore flips the 更多 popover; opening it refreshes the snapshot
// — launches and quits since the wings opened are then visible — and
// re-lays out the seats so the counts stay true.
func (a *shelf) toggleAppsMore() {
	a.apps.more = !a.apps.more
	if a.apps.more {
		a.apps.list = a.appSnapshot()
		a.apps.manage = false
		a.apps.appsScroll = ui.ScrollState{}
	}
	a.winInvalidate()
}

// appsManageOpen swaps 更多 for the manage list and starts the installed
// scan. The pinned list is persisted already; the manage popover writes it
// through setPinnedApps so a broken settings.json keeps it read-only.
func (a *shelf) appsManageOpen() {
	a.apps.more, a.apps.manage = true, true
	a.apps.appsScroll = ui.ScrollState{}
	a.ensureInstalled()
	a.winInvalidate()
}

// appsPopSpot is the popover's box on screen, computed from the shell's
// drawn top and the wings' width. It prefers hanging under the shell on the
// right wing's centre; without room below (an island near the bottom, a
// Dock that lifted the work area) it hangs above instead. Horizontally it
// clamps inside the work area. Pure apart from geometry reads; unit-tested.
func (a *shelf) appsPopSpot(shellH float32) mygo.Rectangle {
	baseW, _ := a.anchorSize()
	ls, rs := a.appsSlots()
	side := appsWingW(max(ls, rs))
	rows := len(a.moreRows())
	if a.apps.manage {
		rows = len(a.manageRows())
	}
	h := float32(appPopHead+appPopFoot) + float32(rows)*appPopRowH
	h = min(max(h, appPopHead+appPopFoot+appPopRowH), appPopMaxH)
	w := float32(appPopW)
	work := a.dispWork
	if work.Width <= 0 {
		work = a.dispBounds
	}
	cx := float32(a.stripCenter()) + baseW/2 + side/2 // 右翼中心的屏幕 X
	x := cx - w/2
	x = min(max(x, float32(work.X)+8), float32(work.X+work.Width)-8-w)
	top := float32(a.frameTop() + a.sheetTop())
	y := top + shellH + appPopGap
	if y+h > float32(work.Y+work.Height)-4 {
		y = top - appPopGap - h // 下方放不下就挂到壳上沿（灵动岛在屏幕底部时）
	}
	if y < float32(work.Y)+4 {
		y = float32(work.Y) + 4
	}
	return mygo.Rectangle{X: int(x + 0.5), Y: int(y + 0.5), Width: int(w), Height: int(h)}
}

// appsPopView draws the 更多 / 编辑常用应用 pane hung under the shell, the
// same glass and rim the page capsule uses. The pane's box is recorded in
// screen coordinates so the outside-press test owns it.
func (a *shelf) appsPopView(c *ui.Context, shellH float32) {
	if !a.apps.more {
		a.apps.popRect = mygo.Rectangle{}
		return
	}
	r := a.appsPopSpot(shellH)
	a.apps.popRect = r
	l := loc(c)
	lr := ui.Rect{X: float32(r.X - a.frame.X), Y: float32(r.Y - a.frame.Y), W: float32(r.Width), H: float32(r.Height)}
	// window-local: the shell's top-left is (frameTop, stripCenter-shellW/2) at openK=0
	glassBase(c, "apps-pop-base", lr, appPopR, 1)
	pane := ui.Column(c.Key("apps-pop")).Absolute().Left(lr.X).Top(lr.Y).Width(lr.W).Height(lr.H).
		Radius(appPopR).Draw(glassPaneDraw(appPopR)).Border(0.5, paneRim).Clip()
	pane.Children(func() {
		if a.apps.manage {
			a.appsManagePane(c, l)
		} else {
			a.appsMorePane(c, l)
		}
	})
}

// popHead is the popover's title row: the name plus a dismiss button.
func popHead(c *ui.Context, key, title string, icon *ui.SVG, onTap func()) {
	ui.Row(c.Key(key+"-head")).Height(appPopHead).FillWidth().Padding(0, 8, 0, 12).
		AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).SingleLine().FontSize(11).TextColor(ink2).Grow(1)
		b := ui.ButtonBase(c.Key(key+"-headbtn")).Size(26, 26).Radius(7).Center().Label(title).Tooltip(title)
		if b.Hovered() {
			b.Background(fill1)
		}
		b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(ink2) })
		if b.Clicked() {
			onTap()
		}
	})
}

// popRow is one app line in a popover: icon, name, and trailing controls
// supplied by the caller. Returns the row so the caller can add a click.
func (a *shelf) appPopRow(c *ui.Context, e appEntry, controls func()) ui.Element {
	row := ui.Row(c.Key("row-"+e.bundleID)).Height(appPopRowH).FillWidth().
		Padding(0, 8, 0, 8).Gap(10).AlignItems(ui.Center)
	row.Children(func() {
		if icon := a.switchIcon(e.bundleID); icon != nil {
			im := ui.Image(c.Key("ic-"+e.bundleID), icon).Size(28, 28)
			if e.missing {
				im.Opacity(0.35)
			}
		} else {
			g := ui.Icon(c, appsSymbol).Size(20, 20).TextColor(ink2)
			if e.missing {
				g.Opacity(0.35)
			}
		}
		name := e.name
		if e.missing {
			name += " · " + loc(c).AppsUninstalled()
		}
		t := ui.Text(c, name).SingleLine().FontSize(12).TextColor(ink).Grow(1).MinWidth(0)
		if e.missing {
			t.TextColor(ink3)
		}
		if controls != nil {
			controls()
		}
	})
	return row
}

// appsMorePane is the overflow list: the other running apps plus the
// 编辑常用应用 entry at the bottom.
func (a *shelf) appsMorePane(c *ui.Context, l locales.Messages) {
	popHead(c, "apps-more", l.AppsOtherRunning(), closeSymbol, func() { a.toggleAppsMore() })
	rows := a.moreRows()
	if len(rows) == 0 {
		ui.Text(c, l.AppsNoOtherRunning()).FontSize(11).TextColor(ink3).Height(appPopRowH).Padding(0, 12, 0, 12)
	} else {
		ui.Scroll(c.Key("apps-more-scroll")).Grow(1).MinHeight(0).MaxHeight(float32(len(rows)) * appPopRowH).
			TrackScroll(&a.apps.appsScroll).Children(func() {
			ui.Column(c.Key("apps-more-list")).Children(func() {
				for _, e := range rows {
					e := e
					row := a.appPopRow(c, e, nil)
					if row.Clicked() {
						a.switchToApp(e)
					}
					if row.Hovered() {
						row.Background(fill1)
					}
				}
			})
		})
	}
	ui.Column(c.Key("apps-more-foot")).Height(appPopFoot).Justify(ui.Center).Children(func() {
		ui.Box(c.Key("apps-more-sep")).Height(1).FillWidth().Margin(0, 0, 4, 0).Background(fill2)
		b := ui.ButtonBase(c.Key("apps-manage")).Height(30).FillWidth().Radius(8).Padding(0, 8, 0, 8).
			Label(l.AppsEdit()).Tooltip(l.AppsEdit())
		if b.Hovered() {
			b.Background(fill1)
		}
		b.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, pinSymbol).Size(15, 15).TextColor(ink2)
				ui.Text(c, l.AppsEdit()).SingleLine().FontSize(12).TextColor(ink2)
			})
		})
		if b.Clicked() {
			a.appsManageOpen()
		}
	})
}

// appsManagePane is the pinned-list editor: pinned entries in saved order
// with move/remove controls, then every other installed or running app
// with a pin toggle. A full list keeps its pin buttons visible but marked,
// never silently swallowing the tap.
func (a *shelf) appsManagePane(c *ui.Context, l locales.Messages) {
	popHead(c, "apps-manage", l.AppsManageTitle(), chevronDownSym, func() {
		a.apps.manage = false
		a.apps.appsScroll = ui.ScrollState{}
		a.winInvalidate()
	})
	rows := a.manageRows()
	if len(rows) == 0 {
		msg := l.AppsManageLoading()
		if a.apps.installed != nil {
			msg = l.AppsManageEmpty()
		}
		ui.Text(c, msg).FontSize(11).TextColor(ink3).Height(appPopRowH).Padding(0, 12, 0, 12)
		return
	}
	ui.Scroll(c.Key("apps-manage-scroll")).Grow(1).MinHeight(0).TrackScroll(&a.apps.appsScroll).Children(func() {
		ui.Column(c.Key("apps-manage-list")).Children(func() {
			for _, r := range rows {
				r := r
				a.appManageRow(c, r)
			}
		})
	})
	ui.Column(c.Key("apps-manage-foot")).Height(appPopFoot).Justify(ui.Center).Children(func() {
		ui.Box(c.Key("apps-manage-sep")).Height(1).FillWidth().Margin(0, 0, 4, 0).Background(fill2)
		b := ui.ButtonBase(c.Key("apps-manage-done")).Height(30).FillWidth().Radius(8).Padding(0, 8, 0, 8).
			Label(l.AppsDone()).Tooltip(l.AppsDone())
		if b.Hovered() {
			b.Background(fill1)
		}
		b.Children(func() {
			ui.Text(c, l.AppsDone()).SingleLine().FontSize(12).TextColor(ink2)
		})
		if b.Clicked() {
			a.apps.manage = false
			a.winInvalidate()
		}
	})
}

// appManageRow draws one manage row: pinned rows get up/down/remove; other
// candidates get a pin toggle that's visibly full when the list is.
func (a *shelf) appManageRow(c *ui.Context, r manageRow) {
	l := loc(c)
	a.appPopRow(c, r.appEntry, func() {
		if r.order >= 0 {
			up := a.appRowBtn(c, "up-"+r.bundleID, chevronUpSymbol, l.AppsMoveUp(), r.order > 0)
			if up && r.order > 0 {
				a.movePinned(r.bundleID, -1)
			}
			down := a.appRowBtn(c, "down-"+r.bundleID, chevronDownSym, l.AppsMoveDown(), false)
			if down {
				a.movePinned(r.bundleID, 1)
			}
			rm := a.appRowBtn(c, "rm-"+r.bundleID, minusSymbol, l.AppsUnpin(r.name), false)
			if rm {
				a.unpinApp(r.bundleID)
			}
			return
		}
		label := l.AppsPin(r.name)
		if r.full {
			label = l.AppsPinFull()
		}
		pin := ui.ButtonBase(c.Key("pin-"+r.bundleID)).Size(26, 26).Radius(7).Center().
			Label(label).Tooltip(label)
		if r.full {
			pin.Opacity(0.35) // 满额：按钮仍在但暗下去，点击不悄悄丢弃
		} else if pin.Hovered() {
			pin.Background(fill1)
		}
		pin.Children(func() { ui.Icon(c, pinSymbol).Size(15, 15).TextColor(ink2) })
		if !r.full && !a.settingsBroken && pin.Clicked() {
			a.pinApp(r.bundleID)
		}
	})
}

// appRowBtn is one 26pt control at a manage row's right edge; disabled rows
// still draw dimmed and report clicked=false.
func (a *shelf) appRowBtn(c *ui.Context, key string, icon *ui.SVG, label string, dim bool) (clicked bool) {
	b := ui.ButtonBase(c.Key(key)).Size(26, 26).Radius(7).Center().Label(label).Tooltip(label)
	if dim {
		b.Opacity(0.35)
	} else if b.Hovered() {
		b.Background(fill1)
	}
	b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(ink2) })
	if !dim && !a.settingsBroken && b.Clicked() {
		return true
	}
	return false
}

// overAppsPop reports whether p (screen) is inside the popover's drawn box.
func (a *shelf) overAppsPop(p mygo.Point) bool {
	r := a.apps.popRect
	return r.Width > 0 && insideRect(p, r)
}

// overAppEntry reports whether p is on the collapsed state's app-entry
// button — the one drawn into the hover band or the idle island capsule.
func (a *shelf) overAppEntry(p mygo.Point) bool {
	r := a.apps.entryRect
	return r.Width > 0 && insideRect(p, r)
}

// noteAppEntry records an element's drawn box in screen coordinates for the
// press test: the pointer gate works in screen space, the element's bounds
// in window space.
func (a *shelf) noteAppEntry(e ui.Element) {
	b := e.Bounds()
	if b.W <= 0 {
		return
	}
	a.apps.entryRect = mygo.Rectangle{
		X:      a.frame.X + int(b.X+0.5),
		Y:      a.frame.Y + int(b.Y+0.5),
		Width:  int(b.W + 0.5),
		Height: int(b.H + 0.5),
	}
}

// appsEntryButton is the small dedicated entry drawn in the collapsed
// state's hover band (and on the idle island capsule): it opens the wings
// directly instead of the search bar.
func (a *shelf) appsEntryButton(c *ui.Context, key string) ui.Element {
	l := loc(c)
	b := ui.ButtonBase(c.Key(key)).Size(appEntryW, appEntryW).Radius(7).Center().
		Label(l.AppsSwitch()).Tooltip(l.AppsSwitch())
	if b.Hovered() {
		b.Background(fill2)
	}
	b.Children(func() { ui.Icon(c, appsSymbol).Size(16, 16).TextColor(ink2) })
	if b.Clicked() {
		a.openAppSwitch()
	}
	a.noteAppEntry(b)
	return b
}

// winInvalidate repaints the resident window when there is one.
func (a *shelf) winInvalidate() {
	if a.win != nil {
		a.win.Invalidate()
	}
}
