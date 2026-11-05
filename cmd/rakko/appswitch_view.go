//go:build darwin

package main

// 应用切换的绘制：两翼是收起态内容（peek 家族的一员），壳按 appsSize
// 长成；「更多 / 编辑常用应用」浮层是挂在壳下的玻璃窗格，与搜索结果
// 面板、小岛导航同一套材质。几何规则：每翼的位宽由锚点该侧到工作区
// 边缘的真实距离决定——灵动岛靠边时拥挤侧自然让位，壳整体平移夹进
// 屏幕（锚点不动，壳不对称伸展），而不是对称加宽冲出屏幕。屏幕放不下
// 的座位只进「更多」浮层，壳本身永远不截断按钮、不长出屏幕。
//
// 行集派生一次、按代重建：快照（appSnapshot）或固定列表变化时
// rowsDirty/manageDirty 置位，appShownRows/moreRows/manageRows 各取各的
// 缓存——视图里没有任何逐帧的排序、map 或系统查询。

import (
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
	appEntryW  = 30  // 收起态应用入口按钮的宽（音乐翼 / 空闲小岛）
	// appEntryBand 收起态 chevron 带里的入口: 带只有 peekBody 高、圆角
	// 从两下角收进去，按钮必须整颗落在有像素的带队里，不能压硬件刘海
	// 或伸出圆角。
	appEntryBand = 16

	// switchIconFloor is the icon cache's floor: wings + overflow + a
	// working set of manage rows. The real cap grows with the installed
	// catalogue this mode scanned, so scrolling the full list never
	// re-decodes icons that fell out of a too-small cache.
	switchIconFloor = 48
)

var (
	// appsSymbol 是应用入口的 2×2 方格，和工具页的三块不对称格区分开。
	appsSymbol = symbol(`<rect x="4" y="4" width="7" height="7" rx="2"/><rect x="13" y="4" width="7" height="7" rx="2"/><rect x="4" y="13" width="7" height="7" rx="2"/><rect x="13" y="13" width="7" height="7" rx="2"/>`)
	moreSymbol = fillSymbol(`<circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/>`)
)

// appGeom is the wings' placement for the current display: how many seats
// each wing can draw beside the anchor, their widths, and how far the drawn
// shell's centre sits off the anchor's axis (sideR−sideL)/2 — an island at
// the screen edge gets a wider wing on the roomy side while the anchor keeps
// its saved spot. Everything that reads the shell's X — drawing, hit test,
// frame, popover — shares this one value.
type appGeom struct {
	ls, rs       int     // seats per wing (0 allowed: that side draws no buttons)
	sideL, sideR float32 // drawn wing widths
	off          float32 // shell centre offset from stripCenter
}

// appGeom resolves the wings' geometry from the anchor's real position
// against the work area. Each side fits what the distance from the
// anchor's flank to the edge allows: a cornered island keeps full seats
// on the roomy side and few or none on the pinned side — the shell leans
// into the room it has instead of symmetric overflow past the edge.
func (a *shelf) appGeom() appGeom {
	baseW, _ := a.anchorSize()
	work := a.dispWork
	if work.Width <= 0 {
		work = a.dispBounds
	}
	cx := float32(a.stripCenter())
	// room on each side of the anchor: flank → work-area edge, minus the
	// screen margin the shell keeps.
	roomL := cx - baseW/2 - float32(work.X) - 8
	roomR := float32(work.X+work.Width) - (cx + baseW/2) - 8
	fit := func(room float32) int {
		if room < 2*appPadX+appBtn {
			return 0
		}
		return min(int((room-2*appPadX+appGap)/(appBtn+appGap)), appsSlots)
	}
	g := appGeom{ls: fit(roomL), rs: fit(roomR)}
	g.sideL, g.sideR = appsWingW(g.ls), appsWingW(g.rs)
	g.off = (g.sideR - g.sideL) / 2
	return g
}

// appsSlots is how many button seats each wing draws on this display.
// Either side may be 0 on a pinned-corner island; the rest overflow into
// 更多, never clipped. Kept as the seats accessor for tests.
func (a *shelf) appsSlots() (left, right int) {
	g := a.appGeom()
	return g.ls, g.rs
}

// appsDrawOff is the drawn shell's offset from the anchor axis while the
// wings are the peek content: (sideR−sideL)/2, folded to 0 in every other
// state so only the wings' own frames — open or mid-fold — carry it.
func (a *shelf) appsDrawOff() float32 {
	if a.fx.peekKind == peekApps {
		return a.appGeom().off
	}
	return 0
}

// appsWingW is one wing's share of the shell width for n button seats.
func appsWingW(n int) float32 {
	if n <= 0 {
		return 0
	}
	return float32(appPadX + n*appBtn + (n-1)*appGap + appPadX)
}

// appsSize is the switcher's peek shape: the anchor plus the two wings,
// the band below it tall enough for the buttons and the collapse affordance.
func (a *shelf) appsSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	g := a.appGeom()
	return baseW + g.sideL + g.sideR, baseH + appsBody
}

// appsSplit splits the snapshot into the pinned prefix and the running
// tail — zero-copy: appSnapshot keeps all pinned entries first.
func (a *shelf) appsSplit() (pinned, running []appEntry) {
	list := a.apps.list
	i := 0
	for i < len(list) && list[i].pinned {
		i++
	}
	return list[:i], list[i:]
}

// rebuildAppRows recomputes the derived row sets from the snapshot and the
// current slot counts: the seats each wing draws (left pinned, right
// running minus the 更多 seat), the selectable subset (missing pinned
// entries drawn but never selected), and the 更多 popover's rows (pinned
// overflow then running overflow). All slices share the snapshot's backing
// array — nothing is copied per frame.
func (a *shelf) rebuildAppRows() {
	as := &a.apps
	pinned, running := a.appsSplit()
	ls, rs := a.appsSlots()
	as.shownL = pinned[:min(len(pinned), ls)]
	seats := max(rs-1, 0) // 右翼末位永远是「更多」
	as.shownR = running[:min(len(running), seats)]
	as.selV = as.selV[:0]
	for _, e := range as.shownL {
		if !e.missing {
			as.selV = append(as.selV, e)
		}
	}
	as.selV = append(as.selV, as.shownR...)
	as.moreV = as.moreV[:0]
	for _, e := range pinned[min(len(pinned), ls):] {
		if !e.missing {
			as.moreV = append(as.moreV, e)
		}
	}
	as.moreV = append(as.moreV, running[min(len(running), seats):]...)
	as.shownSlots = [2]int{ls, rs}
	as.rowsDirty = false
}

// appShownRows are the buttons the wings draw now. Rebuilt only when the
// snapshot changed or the slot counts moved (display/geometry change), so
// a frame never re-splits or re-sorts.
func (a *shelf) appShownRows() (left, right []appEntry) {
	ls, rs := a.appsSlots()
	if a.apps.rowsDirty || a.apps.shownSlots != [2]int{ls, rs} {
		a.rebuildAppRows()
	}
	return a.apps.shownL, a.apps.shownR
}

// appSelRows are the selectable seats in shown order — missing pinned
// entries skipped. Cached with the shown rows.
func (a *shelf) appSelRows() []appEntry {
	a.appShownRows()
	return a.apps.selV
}

// moreRows is what the 更多 popover lists: pinned seats that did not fit
// the left wing (still switchable — missing ones are manage-only), then
// every other running app past the right wing's seats. Cached.
func (a *shelf) moreRows() []appEntry {
	a.appShownRows()
	return a.apps.moreV
}

// manageRow is one row of the 编辑常用应用 popover: a pinned entry first
// (ordered, removable, movable), then every other installed or running app
// the user can pin.
type manageRow struct {
	appEntry
	order int  // position inside the pinned list; -1 for unpinned
	last  bool // last pinned row: 下移 is disabled
	full  bool // the pinned list is full: a pin click would be dropped
}

// manageRows builds the manage list once per rebuild: pinned in saved
// order, then unpinned candidates (installed ∪ running by bundle id)
// sorted by name. Rebuilt only when the snapshot, the pinned list or the
// installed catalogue changed — never per frame.
func (a *shelf) manageRows() []manageRow {
	if !a.apps.manageDirty {
		return a.apps.manageV
	}
	a.apps.manageV = a.buildManageRows()
	a.apps.manageDirty = false
	return a.apps.manageV
}

// buildManageRows does the actual manage-list construction: saved pinned
// order first, then installed ∪ running candidates sorted by name.
func (a *shelf) buildManageRows() []manageRow {
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
		out = append(out, manageRow{
			appEntry: e, order: i,
			last: i == len(pinnedIDs)-1,
			full: len(pinnedIDs) >= settings.PinnedAppsMax,
		})
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

// switchIcon returns the 64pt icon for a bundle id. The cache is bounded
// by this open's working set — the installed catalogue's size plus the
// wings' and overflow's seats — so it neither thrashes (a fixed 64 would
// evict-and-redecode forever while a 200-app manage list scrolls) nor
// grows without bound. Misses are cached as nil; over the cap an app
// falls back to the grid glyph instead of decoding every frame.
func (a *shelf) switchIcon(bundleID string) *ui.Bitmap {
	if a.apps.icons == nil {
		a.apps.icons = map[string]*ui.Bitmap{}
	}
	if b, ok := a.apps.icons[bundleID]; ok {
		return b
	}
	if cap := max(switchIconFloor, len(a.apps.installed)+16); len(a.apps.icons) >= cap {
		return nil
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
// frame; k is the peek reveal. The row lays out at the final wing sizes —
// possibly asymmetric on a cornered island — so the anchor column always
// lands on the strip's axis while the shell shifts around it.
func (a *shelf) appsView(c *ui.Context, shellW, shellH, k float32) {
	l := loc(c)
	baseW, _ := a.anchorSize()
	g := a.appGeom()
	left, right := a.appShownRows()
	sel := a.appSelRows()
	selIx := map[string]int{}
	for i, e := range sel {
		selIx[e.bundleID] = i
	}
	picked := func(e appEntry) bool {
		i, ok := selIx[e.bundleID]
		return ok && a.apps.sel == i
	}
	body := ui.Row(c.Key("apps-wings")).Width(shellW).Height(shellH).
		Opacity(min(max(k*k, 0), 1)).AlignItems(ui.Center)
	body.Children(func() {
		// 左翼：固定位，左对齐贴翼外缘；固定没占满时留一个添加入口。
		lw := ui.Row(c.Key("apps-left")).Width(g.sideL).Height(shellH).
			Padding(0, appPadX, 0, appPadX).Gap(appGap).AlignItems(ui.Center).Justify(ui.Start)
		lw.Children(func() {
			for _, e := range left {
				a.appButton(c, e, picked(e))
			}
			if len(left) < g.ls && len(left) < settings.PinnedAppsMax {
				a.appsAddSeat(c)
			}
		})
		// 中央锚带：刘海或灵动岛本身，底部一个收起箭头。锚带其余位置
		// 的点击仍是搜索——中央路径不变。
		mid := ui.Column(c.Key("apps-mid")).Width(baseW).Height(shellH).
			Justify(ui.End).AlignItems(ui.Center)
		mid.Children(func() {
			row := ui.Row(c.Key("apps-midrow")).Gap(4).AlignItems(ui.Center)
			row.Children(func() {
				b := ui.ButtonBase(c.Key("apps-close")).Size(38, 22).Radius(7).Center().
					Label(l.AppsCollapse()).Tooltip(l.AppsCollapse())
				if b.Hovered() {
					b.Background(fill2)
				}
				b.Children(func() { ui.Icon(c, chevronUpSymbol).Size(12, 12).TextColor(ink2) })
				if b.Clicked() {
					a.endAppSwitch()
				}
				if g.rs == 0 {
					// 屏幕挤掉右翼时「更多」退到锚点下沿、收起箭头旁——
					// 壳体带内、硬件刘海之下，溢出座位始终可达。
					a.appsMoreButton(c)
				}
			})
		})
		if mid.Clicked() {
			a.show(false) // 中央即搜索：收起两翼走原展开路径
		}
		// 右翼：其他运行中应用右对齐，末位固定是「更多」。
		rw := ui.Row(c.Key("apps-right")).Width(g.sideR).Height(shellH).
			Padding(0, appPadX, 0, appPadX).Gap(appGap).AlignItems(ui.Center).Justify(ui.End)
		rw.Children(func() {
			for _, e := range right {
				a.appButton(c, e, picked(e))
			}
			if g.rs > 0 {
				a.appsMoreButton(c)
			}
		})
	})
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
func (a *shelf) appsAddSeat(c *ui.Context) ui.Element {
	l := loc(c)
	b := ui.ButtonBase(c.Key("apps-add")).Size(appBtn, appBtn).Radius(10).Center().
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
		a.refreshAppList()
		a.apps.manage = false
		a.apps.appsScroll = ui.ScrollState{}
	}
	a.winInvalidate()
}

// appsManageOpen swaps 更多 for the manage list and starts a fresh
// installed scan — the catalogue may have changed since the mode opened.
// The pinned list is persisted already; the manage popover writes it
// through setPinnedApps so a broken settings.json keeps it read-only.
func (a *shelf) appsManageOpen() {
	a.apps.more, a.apps.manage = true, true
	a.apps.appsScroll = ui.ScrollState{}
	a.ensureInstalled()
	a.winInvalidate()
}

// appsPopSpot is the popover's box on screen, computed from the shell's
// drawn top and the wings' geometry. It prefers hanging under the shell on
// the right wing's centre; without room below (an island near the bottom, a
// Dock that lifted the work area) it hangs above instead. Horizontally it
// clamps inside the work area, and its height is capped by whichever side
// it lands on — the list scrolls inside the box it gets.
func (a *shelf) appsPopSpot(shellH float32) mygo.Rectangle {
	baseW, _ := a.anchorSize()
	g := a.appGeom()
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
	workBot := float32(work.Y + work.Height)
	// 右翼中心（壳内坐标）：锚点右缘 + 右翼一半。右翼为 0 时退到锚带右缘。
	cx := float32(a.stripCenter()) + baseW/2 + g.sideR/2
	x := cx - w/2
	x = min(max(x, float32(work.X)+8), float32(work.X+work.Width)-8-w)
	top := float32(a.frameTop() + a.sheetTop())
	y := top + shellH + appPopGap
	if y+h > workBot-4 {
		y = top - appPopGap - h // 下方放不下就挂到壳上沿（灵动岛在屏幕底部时）
	}
	if y < float32(work.Y)+4 {
		y = float32(work.Y) + 4
	}
	// 高度跟着落点收窄：浮层永远完整落在工作区内，内容靠滚动。
	if y+h > workBot-4 {
		h = max(workBot-4-y, appPopHead+appPopRowH)
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

// appPopRow is one app line in a popover: icon, name, and trailing controls
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
// with a pin toggle. The installed scan's state is a real status row —
// loading, a retryable failure, or nothing when the catalogue is in —
// while the pinned and running candidates stay usable underneath.
func (a *shelf) appsManagePane(c *ui.Context, l locales.Messages) {
	popHead(c, "apps-manage", l.AppsManageTitle(), chevronDownSym, func() {
		a.apps.manage = false
		a.apps.appsScroll = ui.ScrollState{}
		a.winInvalidate()
	})
	switch {
	case a.apps.installedLoad:
		ui.Text(c, l.AppsManageLoading()).FontSize(11).TextColor(ink3).Height(appPopRowH).Padding(0, 12, 0, 12)
	case a.apps.installedErr != nil:
		ui.Row(c.Key("apps-scan-err")).Height(appPopRowH).FillWidth().Padding(0, 8, 0, 12).Gap(8).
			AlignItems(ui.Center).Children(func() {
			ui.Text(c, l.AppsManageFailed()).SingleLine().FontSize(11).TextColor(ink3).Grow(1).MinWidth(0)
			b := ui.ButtonBase(c.Key("apps-scan-retry")).Size(26, 26).Radius(7).Center().
				Label(l.AppsRetry()).Tooltip(l.AppsRetry())
			if b.Hovered() {
				b.Background(fill1)
			}
			b.Children(func() { ui.Icon(c, refreshSymbol).Size(14, 14).TextColor(ink2) })
			if b.Clicked() {
				a.ensureInstalled()
			}
		})
	}
	rows := a.manageRows()
	if len(rows) == 0 {
		ui.Text(c, l.AppsManageEmpty()).FontSize(11).TextColor(ink3).Height(appPopRowH).Padding(0, 12, 0, 12)
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

// appManageRow draws one manage row: pinned rows get up/down/remove — the
// first row's 上移 and the last row's 下移 stay visible but dimmed — and
// other candidates get a pin toggle that's visibly full when the list is.
// A broken settings.json dims every control: the list stays read-only.
func (a *shelf) appManageRow(c *ui.Context, r manageRow) {
	l := loc(c)
	a.appPopRow(c, r.appEntry, func() {
		if r.order >= 0 {
			up := a.appRowBtn(c, "up-"+r.bundleID, chevronUpSymbol, l.AppsMoveUp(), r.order == 0)
			if up {
				a.movePinned(r.bundleID, -1)
			}
			down := a.appRowBtn(c, "down-"+r.bundleID, chevronDownSym, l.AppsMoveDown(), r.last)
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
		if r.full || a.settingsBroken {
			pin.Opacity(0.35) // 满额/设置损坏：按钮仍在但暗下去，点击不悄悄丢弃
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
// — first/last seats, or a broken settings.json — still draw dimmed and
// report clicked=false.
func (a *shelf) appRowBtn(c *ui.Context, key string, icon *ui.SVG, label string, dim bool) (clicked bool) {
	dim = dim || a.settingsBroken
	b := ui.ButtonBase(c.Key(key)).Size(26, 26).Radius(7).Center().Label(label).Tooltip(label)
	if dim {
		b.Opacity(0.35)
	} else if b.Hovered() {
		b.Background(fill1)
	}
	b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(ink2) })
	if !dim && b.Clicked() {
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
// button — the one drawn into the hover band, the music wings' right end,
// or the idle island capsule.
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
// state: it opens the wings directly instead of the search bar. size is
// the drawn box (and the hit box — noteAppEntry records the button's own
// bounds): 30 in the music wings and the idle island capsule, the compact
// 16 in the chevron band whose 18pt body sits mostly behind the bottom
// corner cuts. Same key kinds, hover fill and tap action at every size so
// every drawn entry stays the one button.
func (a *shelf) appsEntryButton(c *ui.Context, key string, size float32) ui.Element {
	l := loc(c)
	b := ui.ButtonBase(c.Key(key)).Size(size, size).Radius(min(7, size/2-1)).Center().
		Label(l.AppsSwitch()).Tooltip(l.AppsSwitch())
	if b.Hovered() {
		b.Background(fill2)
	}
	glyph := size * 0.53
	b.Children(func() { ui.Icon(c, appsSymbol).Size(glyph, glyph).TextColor(ink2) })
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
