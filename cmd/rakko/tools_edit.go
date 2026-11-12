//go:build darwin

package main

// The tools page's editing gestures (v3 §1, §3, §4): the card library,
// the corner resize handle, the card context menu, and the fresh drag a
// library tile starts. The cards themselves are drawn by tools_cards.go;
// this file owns the transient state and the pointer bookkeeping.

import (
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
)

// toolLibraryH is the drawer's height while editing (v3 .drawer).
const toolLibraryH = 80

// toolResize is one handle drag: the pointer's window origin, the card's
// original rectangle, and the size it has snapped to. Nothing is written
// until the release.
type toolResize struct {
	id           string
	orig         layout.Card
	startX       float32 // pointer window x at the down
	startY       float32
	w0, h0       float32 // original card box
	cur          [2]int  // snapped return size (cols, rows)
	cellX, cellY float32 // the card's cell, for the rubber band
	bandW        float32 // the pointer's free size
	bandH        float32
}

// toolLibPress is the library tile's pointer session: it lives on the
// page, not in the build closure, because the tile is rebuilt every frame.
type toolLibPress struct {
	id             string
	startX, startY float32
	started, down  bool
}

// toolMenu is the card context menu, at stage coordinates.
type toolMenu struct {
	id string
	x  float32
	y  float32
}

// gridOrigin is the grid content's origin in window coordinates.
func (p *toolsPage) gridOrigin() (x, y float32) {
	return p.viewport.X, p.viewport.Y
}

// windowToContent maps a window point to the grid's content coordinates.
func (p *toolsPage) windowToContent(wx, wy float32) (x, y float32) {
	ox, oy := p.gridOrigin()
	return wx - ox, wy - oy + p.scroll.Y
}

// toolLibrary is the editing drawer under the grid: every catalogue card
// not on the page or the desktop, sized to its default rectangle. Clicking
// appends it; dragging it into the grid places it at the slot. A grid card
// held over the drawer turns it red.
func (p *toolsPage) toolLibrary(c *ui.Context, live bool) {
	defs := p.libraryCards()
	d := p.press
	catch := d != nil && d.dragging && !d.fresh && p.overLibrary()
	bg := ui.RGBA(0, 0, 0, 0.24)
	if catch {
		bg = ui.RGBA(255, 69, 58, 0.14)
	}
	box := ui.Column(c.Key("tool-drawer")).FillWidth().Height(toolLibraryH).
		Background(bg).Padding(12, 12, 12, 12)
	box.Children(func() {
		sc := ui.ScrollBoth(c.Key("lib-scroll")).FillWidth().Height(56).TrackScroll(&p.libScroll)
		sc.Children(func() {
			row := ui.Row(c.Key("lib-strip")).Height(56).Gap(6).AlignItems(ui.Center)
			row.Children(func() {
				for _, def := range defs {
					p.libTile(c, def, live)
				}
			})
		})
	})
}

// libTile is one 56pt library tile: the card's default width (66 or 138),
// its glyph and name. A drag starts the fresh-card preview; a click adds
// the card at the end.
func (p *toolsPage) libTile(c *ui.Context, def toolCardDef, live bool) {
	w := float32(66)
	if def.span >= 2 {
		w = 138
	}
	l := loc(c)
	name := def.name(l)
	tile := ui.Column(c.Key("lib-"+def.id)).Width(w).Height(56).Radius(11).
		Padding(8, 9, 8, 9).Background(fill1).Label(name).Tooltip(name)
	if p.press != nil && p.press.fresh && p.press.id == def.id {
		// Keep the pressed tile in the tree (the engine ends a press whose
		// element vanishes) but let the lifted copy be the visible card.
		tile.Opacity(0)
	} else if tile.Hovered() {
		tile.Background(fill2)
	}
	tile.Children(func() {
		ui.Icon(c, def.icon).Size(16, 16).TextColor(ink2)
		ui.Box(c).Grow(1)
		// The English names outgrow the 66pt tile at one line; two lines
		// keep the whole name instead of an ellipsis.
		ui.Text(c, name).FontSize(10.5).FontWeight(600).LineHeight(1.15).
			MaxLines(2).Ellipsis("…").TextColor(ink2).Label(name)
	})
	if !live {
		return
	}
	tile.PointerPosition()
	originX, originY := tile.Bounds().X, tile.Bounds().Y
	tile.HandleInput(func(ev ui.InputEvent) bool {
		wx, wy := float32(originX)+ev.X, float32(originY)+ev.Y
		switch ev.Kind {
		case ui.InputPointerDown:
			if ev.Button != 0 {
				return false
			}
			p.closeMenu()
			p.lp = &toolLibPress{id: def.id, startX: wx, startY: wy, down: true}
			return true
		case ui.InputPointerMove:
			lp := p.lp
			if lp == nil || !lp.down || lp.id != def.id {
				return false
			}
			if !lp.started && hypot(wx-lp.startX, wy-lp.startY) > toolDragSlop {
				lp.started = true
				p.beginFreshDrag(def, wx, wy)
			}
			if lp.started {
				p.moveFreshDrag(wx, wy)
			}
			return true
		case ui.InputPointerUp:
			lp := p.lp
			if lp == nil || !lp.down || lp.id != def.id {
				return false
			}
			p.lp = nil
			if lp.started {
				p.moveFreshDrag(wx, wy)
				p.endFreshDrag()
			} else {
				p.addFromLibrary(def)
			}
			return true
		}
		return false
	})
}

// addFromLibrary is the tile click: append the card and let the packing
// put it at the first free rectangle.
func (p *toolsPage) addFromLibrary(def toolCardDef) {
	p.closeMenu()
	p.host.mutateToolsChanged(func(l *layout.Layout) bool {
		l.AddCardSize(def.id, def.span, max(1, def.rows))
		return true
	})
}

// beginFreshDrag starts the lifted preview of a card that is not in the
// grid yet. The grab point is the tile's centre.
func (p *toolsPage) beginFreshDrag(def toolCardDef, wx, wy float32) {
	p.closeMenu()
	p.closeDragPreview()
	spec := layout.Card{ID: def.id, Span: def.span, Rows: max(1, def.rows)}
	p.press = &toolPress{
		id: def.id, fresh: true, spec: spec,
		downAt: time.Now(), dragging: true,
		grabDX: toolColW(spec.Cols()) / 2, grabDY: toolRowH(spec.Height()) / 2,
		index: len(p.host.toolsLayoutNow().Cards),
	}
	g := p.grid()
	p.dragCols, p.dragRows = g.cols, g.rows
	p.moveFreshDrag(wx, wy)
}

// moveFreshDrag maps the pointer into the grid and updates the slot the
// fresh card would take. Over the drawer the slot disappears.
func (p *toolsPage) moveFreshDrag(wx, wy float32) {
	d := p.press
	if d == nil || !d.fresh {
		return
	}
	d.px, d.py = p.windowToContent(wx, wy)
	d.cardX, d.cardY = d.px-d.grabDX, d.py-d.grabDY
	dt := d.lastMove
	if !dt.IsZero() {
		ms := max(float32(time.Since(dt).Milliseconds()), 8)
		p.updateDragVelocity(d, (d.px-d.lastX)/ms*16)
	}
	d.lastX = d.px
	d.lastMove = time.Now()
	if p.overLibrary() {
		d.index = -1
	} else if i := toolInsertIndexFresh(p.host.toolsLayoutNow().Cards, p.dragCols, d.spec, d.index, d.px, d.py); i >= 0 {
		d.index = i
	}
	if p.host.win != nil {
		p.host.win.Invalidate()
	}
}

// endFreshDrag commits the fresh card at the slot, or cancels it over the
// drawer, and lets the card settle into its cell.
func (p *toolsPage) endFreshDrag() {
	d := p.press
	if d == nil || !d.fresh {
		return
	}
	if d.index < 0 || p.overLibrary() {
		p.press = nil
		p.dragCols, p.dragRows = 0, 0
		if p.host.win != nil {
			p.host.win.Invalidate()
		}
		return
	}
	at, spec := d.index, d.spec
	if !p.host.mutateToolsChanged(func(l *layout.Layout) bool {
		l.InsertCardSize(spec.ID, spec.Span, spec.Rows, at)
		return true
	}) {
		p.press = nil
		p.dragCols, p.dragRows = 0, 0
		return
	}
	d.fresh, d.dragging, d.settling, d.settleJump = false, false, true, false
	p.dragCols, p.dragRows = 0, 0
	if g := p.grid(); true {
		for _, cell := range g.cells {
			if cell.id == d.id {
				d.settleX, d.settleY = cell.x, cell.y
				break
			}
		}
	}
	if p.host.win != nil {
		p.host.win.Invalidate()
	}
}

// updateDragVelocity is the horizontal velocity the lift's tilt follows,
// smoothed like the design's moveDrag.
func (p *toolsPage) updateDragVelocity(d *toolPress, v float32) {
	d.vx = d.vx*0.75 + v*0.25
	d.lastMove = time.Now()
}

// dragTilt is the lifted card's rotation: ±5°, relaxing after 60 ms of a
// still pointer (v2 §7 拿起).
func (p *toolsPage) dragTilt(now time.Time) float32 {
	d := p.press
	if d == nil || !d.dragging {
		return 0
	}
	if !d.lastMove.IsZero() && now.Sub(d.lastMove) > 60*time.Millisecond {
		d.vx *= 0.6
	}
	return min(max(d.vx*0.55, -5), 5)
}

// toolResizeBand is the rubber band that follows the pointer while the
// corner handle drags: accent 75% stroke on a 10% fill, at the card's
// original cell.
func (p *toolsPage) toolResizeBand(c *ui.Context) {
	rs := p.rs
	if rs == nil {
		return
	}
	ui.Box(c.Key("resize-band")).Absolute().Left(rs.cellX).Top(rs.cellY).
		Width(rs.bandW).Height(rs.bandH).Radius(rCard).BorderWidth(1.5).
		BorderColor(accent.Alpha(0.75)).Background(accent.Alpha(0.10)).IgnoreInput(true)
}

// beginResize starts a corner drag. The pointer's window position is all
// the gesture needs: the delta stays valid while the panel grows under it.
func (p *toolsPage) beginResize(id string, spec layout.Card, cell toolCell, wx, wy float32) {
	p.closeMenu()
	p.press = nil
	p.closeDragPreview()
	p.rs = &toolResize{
		id: id, orig: spec, startX: wx, startY: wy,
		w0: cell.w, h0: cell.h, cur: [2]int{spec.Cols(), spec.Height()},
		cellX: cell.x, cellY: cell.y, bandW: cell.w, bandH: cell.h,
	}
	if p.host.win != nil {
		p.host.win.Invalidate()
	}
}

// moveResize snaps the free rectangle to the nearest size the card has
// (the current one has 0.15 of stickiness). The packing and the shell
// follow the snapped rectangle; layout.json is untouched.
func (p *toolsPage) moveResize(wx, wy float32) {
	rs := p.rs
	if rs == nil {
		return
	}
	rw := max(float32(70), rs.w0+wx-rs.startX)
	rh := max(float32(60), rs.h0+wy-rs.startY)
	rs.bandW, rs.bandH = rw, rh
	ww := (rw + toolCellGap) / (toolCellW + toolCellGap)
	wh := (rh + toolCellGap) / (toolCellH + toolCellGap)
	best, bestD := rs.cur, math.MaxFloat64
	sizes := cardSizes(rs.id)
	if !slices.Contains(sizes, rs.cur) {
		// A legacy rectangle stays a resize target until the user picks
		// another shape.
		sizes = append(sizes, rs.cur)
	}
	for _, s := range sizes {
		d := math.Abs(float64(s[0])-float64(ww)) + math.Abs(float64(s[1])-float64(wh))
		if s[0] == rs.cur[0] && s[1] == rs.cur[1] {
			d -= 0.15
		}
		if d < bestD {
			best, bestD = [2]int{s[0], s[1]}, d
		}
	}
	if best != rs.cur {
		rs.cur = best
		if p.host.win != nil {
			p.host.win.Invalidate()
		}
	}
}

// endResize writes the snapped size, if it changed, and drops the band.
func (p *toolsPage) endResize() {
	rs := p.rs
	p.rs = nil
	if rs == nil {
		return
	}
	if rs.cur[0] != rs.orig.Cols() || rs.cur[1] != rs.orig.Height() {
		p.host.mutateToolsChanged(func(l *layout.Layout) bool {
			l.SetCardSize(rs.id, rs.cur[0], rs.cur[1])
			return true
		})
	}
	if p.host.win != nil {
		p.host.win.Invalidate()
	}
}

// openMenu opens the card's context menu at stage coordinates.
func (p *toolsPage) openMenu(id string, x, y float32) {
	p.closeMenu()
	p.press, p.rs = nil, nil
	p.menu = &toolMenu{id: id, x: x, y: y}
	if p.host.win != nil {
		p.host.win.Invalidate()
	}
}

// closeMenu closes the card menu.
func (p *toolsPage) closeMenu() {
	if p.menu == nil {
		return
	}
	p.menu = nil
	if p.host.win != nil {
		p.host.win.Invalidate()
	}
}

// menuHeight is the menu's height for its card: the size row appears only
// when the card has more than one rectangle.
func menuHeight(sizes int) float32 {
	h := float32(10 + 24*3 + 11) // padding, three items, one separator
	if sizes > 1 {
		h += 38 + 11
	}
	return h
}

// overlay draws the card menu over the whole shell: the page content's
// clip is only as tall as the shell's content, too short for the menu.
func (p *toolsPage) overlay(c *ui.Context, live bool) {
	if live {
		p.toolMenuView(c, live)
	}
}

// toolMenuView draws the card menu: the size shapes (smallest area first,
// the current one accent), then the page's verbs. It is a custom menu
// rather than a native one: the shape row is the picker, and a native
// menu cannot hold it; the overlay window also keeps the menu inside the
// panel's glass.
func (p *toolsPage) toolMenuView(c *ui.Context, live bool) {
	m := p.menu
	if m == nil {
		return
	}
	a, l := p.host, loc(c)
	spec := layout.Card{ID: m.id, Span: 1, Rows: 1}
	if def := toolCardDefFor(m.id); def != nil {
		spec.Span, spec.Rows = def.span, def.rows
	}
	for _, cc := range a.toolsLayout().Cards {
		if cc.ID == m.id {
			spec.Span, spec.Rows = cc.Cols(), cc.Height()
		}
	}
	sizes := cardSizes(m.id)
	if !slices.Contains(sizes, [2]int{spec.Cols(), spec.Height()}) {
		// A layout from an older canvas can hold a rectangle the current
		// size list no longer offers; keep it visible and selectable.
		sizes = append(sizes, [2]int{spec.Cols(), spec.Height()})
	}
	// smallest area first; the array's order stays for cycling elsewhere
	order := slices.Clone(sizes)
	slices.SortStableFunc(order, func(x, y [2]int) int {
		return x[0]*x[1] - y[0]*y[1]
	})
	h := menuHeight(len(order))
	w := float32(188)
	// The menu lives in the shell's coordinates (the page overlay): the
	// page content's own clip is 200 pt tall, too short for the menu.
	x := m.x + sheetPadX
	y := m.y + bandH + bodyGap + pageActionH + actionsGap
	x = min(max(x, 4), max(a.pageShellW()-w-4, 4))
	y = min(max(y, 4), max(a.pageShellH()-h-4, 4))
	ui.Box(c.Key("menu-backdrop")).Absolute().Left(0).Top(0).Right(0).Bottom(0).
		HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputPointerDown {
				p.closeMenu()
			}
			return true
		})
	box := ui.Column(c.Key("tool-menu")).Absolute().Left(x).Top(y).Width(w).
		Radius(10).Background(ui.RGBA(42, 42, 46, 0.94)).Border(0.5, ui.RGBA(255, 255, 255, 0.16)).
		Padding(5, 5, 5, 5).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.4))
	box.Children(func() {
		if len(order) > 1 {
			row := ui.Row(c.Key("menu-sizes")).Height(38).Gap(4).Padding(2, 4, 2, 4)
			row.Children(func() {
				for _, s := range order {
					cur := s[0] == spec.Cols() && s[1] == spec.Height()
					btn := ui.Box(c.Key(fmtInts("menu-size", s))).Width(44).Height(38).Radius(7).Center().
						Label(l.CellSize(s[0], s[1])).Tooltip(l.CellSize(s[0], s[1]))
					if cur {
						btn.Background(accent)
					} else {
						btn.Background(ui.RGBA(255, 255, 255, 0.05))
					}
					if btn.Hovered() && !cur {
						btn.Background(ui.RGBA(255, 255, 255, 0.12))
					}
					btn.Children(func() {
						ui.Box(c).Size(float32(s[0]*11+(s[0]-1)*2), float32(s[1]*9+(s[1]-1)*2)).
							Radius(3).Background(ui.RGBA(255, 255, 255, 0.82))
					})
					if live && btn.Clicked() {
						a.mutateToolsChanged(func(layout *layout.Layout) bool {
							layout.SetCardSize(m.id, s[0], s[1])
							return true
						})
						p.menu = nil
					}
				}
			})
			menuSep(c, "menu-sep-1")
		}
		editLabel := l.ToolsMenuEdit()
		if p.editing {
			editLabel = l.ToolsMenuDone()
		}
		menuItem(c, "menu-edit", editLabel, false, live, func() {
			p.editing = !p.editing
			p.press, p.rs, p.menu = nil, nil, nil
		})
		menuItem(c, "menu-pin", l.ToolsMenuPin(), false, live, func() {
			p.pinFromMenu(m.id)
			p.menu = nil
		})
		menuSep(c, "menu-sep-2")
		menuItem(c, "menu-remove", l.ToolsMenuRemove(), true, live, func() {
			a.mutateToolsChanged(func(layout *layout.Layout) bool {
				layout.RemoveCard(m.id)
				return true
			})
			p.menu = nil
		})
	})
}

// menuSep is a menu divider.
func menuSep(c *ui.Context, key string) {
	ui.Box(c.Key(key)).Height(1).Margin(5, 8, 5, 8).Background(ui.RGBA(255, 255, 255, 0.12))
}

// menuItem is one 24pt menu row; danger paints it red until hovered.
func menuItem(c *ui.Context, key, label string, danger, live bool, fn func()) {
	col := ink
	if danger {
		col = ui.Hex("#FF6961")
	}
	row := ui.Row(c.Key(key)).Height(24).Padding(0, 10, 0, 10).Radius(5).
		AlignItems(ui.Center).Label(label).Tooltip(label)
	if row.Hovered() {
		row.Background(accent)
		col = ink
	}
	row.Children(func() {
		panelLabel(c, nil, label, 13, 0).TextColor(col)
	})
	clockSwallow(row)
	if live && row.Clicked() {
		fn()
	}
}

// pinFromMenu pins the card at the screen spot of its current cell,
// clamped to the card's display: the menu's choice keeps the card where
// the user sees it, as the drag-out does.
func (p *toolsPage) pinFromMenu(id string) {
	a := p.host
	g := toolGridFor(a.toolsLayout().Cards, a.inactiveWidget, p.bandMinCols())
	var cell toolCell
	found := false
	for _, cc := range g.cells {
		if cc.id == id {
			cell, found = cc, true
			break
		}
	}
	if !found {
		return
	}
	spec := layout.Card{ID: id, Span: 1, Rows: 1}
	if def := toolCardDefFor(id); def != nil {
		spec.Span, spec.Rows = def.span, def.rows
	}
	for _, cc := range a.toolsLayout().Cards {
		if cc.ID == id {
			spec.Span, spec.Rows = cc.Cols(), cc.Height()
		}
	}
	disp := a.dispID
	x := int(float32(a.frame.X) + p.viewport.X + cell.x)
	y := int(float32(a.frame.Y) + p.viewport.Y + cell.y - p.scroll.Y)
	frame := mygo.Rectangle{X: x, Y: y, Width: pinCardWidth(id, spec.Cols()), Height: int(cell.h)}
	frame = pinClamp(frame, pinArea(disp))
	a.addPin(id, spec.Cols(), disp, pinFree, false, frame)
}

// fmtInts is a stable key for a menu size shape.
func fmtInts(prefix string, s [2]int) string {
	return prefix + "-" + strconv.Itoa(s[0]) + "x" + strconv.Itoa(s[1])
}
