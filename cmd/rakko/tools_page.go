//go:build darwin

package main

// The 工具 page: one grid holding the action cards and the widgets. Cards
// come from layout.json (internal/layout); drawing and per-card behaviour
// live in tools_cards.go. This file owns the adaptive packing (three to
// six columns, one to three drawn rows), the
// long-press / drag-to-reorder gestures, the editing chrome (⊖ badge, size
// handle, wobble, the + tile), the card catalogue, and the page's own
// actions (the pencil, 完成).

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
)

const (
	pageTools pageID = "tools" // 工具：用户排版的卡片页

	toolPickRowH  = 30 // 目录行高（design §3 .pi 30px）
	toolDragSlop  = 6  // 普通态拖动超过此距离作废点击
	toolJiggle    = 300 * time.Millisecond
	toolWobbleDeg = 0.7 // 编辑摆动 ±0.7°
	// toolLibraryH reserves the card library the editing pass adds under
	// the grid; the window envelope already holds it.
	toolLibraryH = 80
)

// toolMoveSpring moves cards to their new cells after the panel's packing
// changes: 300 ms, Bounce 0.15 (design §2).
var toolMoveSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.15}}

// toolCell is one card's position in content coordinates.
type toolCell struct {
	id   string
	x, y float32
	w, h float32
	plus bool // the dashed + slot (edit mode only)
}

// toolPress is the pointer session on one card: from the down, through a
// possible long-press or reorder drag, to the settle spring after the
// release. All coordinates are stage content space.
type toolPress struct {
	id         string
	downAt     time.Time
	downX      float32 // pointer position at the down (content space)
	downY      float32
	px, py     float32 // latest pointer position
	grabDX     float32 // pointer offset inside the card at the down
	grabDY     float32
	cardX      float32 // the card element's rendered left/top this frame
	cardY      float32 //   (the animation's answer, so moves stay aligned)
	dragging   bool    // slop passed: the lifted copy follows the pointer
	suppress   bool    // moved without edit: the release is not a click
	settling   bool    // released: flying to the committed cell or back
	settleJump bool    // the settle channels were Jump'ed to the start
	settleX    float32
	settleY    float32
	index      int // current insertion index while dragging
}

// toolsPage is the card grid page.
type toolsPage struct {
	host     *shelf
	scroll   ui.ScrollState
	viewport ui.Rect     // previous frame's scroll viewport, in window coordinates
	preview  dragPreview // transient drag window; absent from layout and live pins

	editing   bool
	picking   bool
	editAt    time.Time // wobble phase origin
	press     *toolPress
	skipClick bool   // the last release was a suppressed press, not a tap
	jiggleID  string // card shaking for 无可执行项
	jiggleAt  time.Time

	// A reorder drag pins the grid's columns where it began and may only
	// add rows: the panel must not follow the pointer sideways.
	dragCols int
	dragRows int
}

func (p *toolsPage) id() pageID    { return pageTools }
func (p *toolsPage) label() string { return p.host.appLoc().ToolsTitle() }
func (p *toolsPage) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, toolsNavSymbol)
}
func (p *toolsPage) enabled() bool { return true }

// toolsPageOf is the shelf's tools page, or nil for test shelves that
// never registered one.
func (a *shelf) toolsPage() *toolsPage {
	if p, ok := a.page(pageTools).(*toolsPage); ok {
		return p
	}
	return nil
}

// gridCards is the card list the grid packs now: the stored order, or the
// order a reorder drag previews. It records the preview's insertion index
// so the release commits the cell the pointer last meant.
func (p *toolsPage) gridCards() ([]layout.Card, int) {
	l := p.host.toolsLayoutNow()
	d := p.press
	if d == nil || !p.editing || !d.dragging {
		return l.Cards, 0
	}
	cols := p.dragCols
	if cols <= 0 {
		cols = toolMinCols
	}
	if ins := toolInsertIndex(l.Cards, cols, d.id, d.index, d.px, d.py); ins >= 0 {
		d.index = ins
		return toolVirtualCards(l.Cards, d.id, ins), cols
	}
	return l.Cards, cols
}

// grid is the packing the page shows: the narrowest ladder rung that holds
// the cards, widened for the band's ears. A reorder drag pins its starting
// columns and may only grow rows.
func (p *toolsPage) grid() toolGrid {
	cards, locked := p.gridCards()
	if locked > 0 {
		g := toolGridAt(cards, locked, p.host.inactiveWidget, p.editing)
		if p.dragRows > g.rows {
			g.rows, g.vis = p.dragRows, min(p.dragRows, toolMaxRows)
		}
		return g
	}
	return toolGridFor(cards, p.host.inactiveWidget, p.editing, p.bandMinCols())
}

// width is the shell width the page needs now (pageWidth).
func (p *toolsPage) width() float32 { return toolPanelW(p.grid().cols) }

// height is the shell height the page needs now (pageHeight). The card
// library of the editing pass is not drawn yet; the window envelope
// reserves it through maxSize.
func (p *toolsPage) height() float32 { return toolPanelH(p.grid().vis) }

// maxSize is the largest shell the page can ask for, for the session
// frame: six columns, three rows, and the 80pt card library under them.
func (p *toolsPage) maxSize() (float32, float32) {
	return toolPanelW(toolMaxCols), toolPanelH(toolMaxRows) + toolLibraryH
}

// bandMinCols is the narrowest panel that still leaves the band wide
// enough for its live chips. The band has priority over the cards' own
// width: a countdown or stopwatch that would ellipsize widens the panel,
// and the extra columns stay empty.
func (p *toolsPage) bandMinCols() int {
	need := p.bandEarNeed()
	for cols := toolMinCols; cols <= toolMaxCols; cols++ {
		if p.host.bandEarW(toolPanelW(cols)) >= need {
			return cols
		}
	}
	return toolMaxCols
}

// bandEarNeed is the wider of the band's two ears: the chips it must hold
// and the gaps between them, as the band splits them now.
func (p *toolsPage) bandEarNeed() float32 {
	a := p.host
	acts := a.liveActivities()
	leftIdx, _ := a.clockBandSplit(acts)
	var leftW, rightW float32
	var leftN, rightN int
	for i, act := range acts {
		w := a.toolChipW(act)
		if w <= 0 {
			continue
		}
		if i == leftIdx {
			leftW, leftN = leftW+w, leftN+1
		} else {
			rightW, rightN = rightW+w, rightN+1
		}
	}
	if leftN > 1 {
		leftW += float32(leftN - 1)
	}
	if rightN > 1 {
		rightW += float32(rightN - 1)
	}
	return max(leftW, rightW)
}

// bandEarW is the width one ear of the band has inside a panel that wide:
// the shell minus its padding, split by the camera housing when the style
// has one. The island's continuous band has no housing.
func (a *shelf) bandEarW(panelW float32) float32 {
	inner := panelW - 2*sheetPadX
	if a.notchW > 0 && !a.island {
		inner -= float32(a.notchW + 2*6)
	}
	return max(inner/2, 0)
}

// toolChipW estimates a live chip's drawn width. The band is laid out
// before the font is measured; this estimate leaves 3pt of slack per chip
// so the width the panel reserves never ellipsizes the real text.
func (a *shelf) toolChipW(act liveActivity) float32 {
	l := a.appLoc()
	switch t := act.(type) {
	case timerChip:
		m := t.m
		if m == nil {
			return 0
		}
		if m.phase == timerDone {
			return 9 + 9 + 13 + 5 + liveTextWidth(l.TimerStop(), 11.5) + 3
		}
		text := clockTextCeil(m.timerRemainAt(m.clock()))
		return 9 + 6 + 13 + 5 + liveTextWidth(text, 11.5) + 3
	case stopwatchChip:
		m := t.m
		if m == nil {
			return 0
		}
		text := clockText(m.watchElapsedAt(m.clock()))
		return 8 + 5 + 13 + 5 + liveTextWidth(text, 11.5) + 3
	case *converter:
		if t.cancelOpen {
			return 2 + 2 + liveTextWidth(l.ConvCancel(), 9.5) + 3
		}
		return 2 + 2 + 16 + 3
	case *downloadsModule:
		f := t.progress().file
		text := downloadPercent(f)
		if f.total <= 0 {
			text = downloadMegabytes(f.done)
		}
		return 3 + 7 + 16 + 4 + liveTextWidth(text, 9.5) + 3
	case *nowPlayingModule:
		return 2 + 2 + 16 + 3 + 10 + 3 + 18 + 3
	case *awakeModule:
		return chipH
	}
	// An unknown live module draws whatever it wants; reserve a full ear
	// for it rather than clipping it.
	return 112
}

// toolsLayout is the persisted arrangement. It loads lazily from
// layout.json, defaults first so the page never waits on the file. The
// copy carries the runtime answer for which widgets are switched off, so
// their entries take no cell without the file losing them.
func (a *shelf) toolsLayout() layout.Layout {
	if a.toolsLay == nil {
		l := layout.DefaultWith(a.settings.AIUsage.Enabled)
		if a.dataDir != "" {
			got, err := layout.LoadWith(a.dataDir, a.settings.AIUsage.Enabled)
			if err != nil {
				// Like settingsBroken: the page still draws, but the file
				// the user could repair stays untouched until restart.
				a.toolsLayBroken = true
				a.report(err)
			}
			l = got
		}
		a.toolsLay = &l
	}
	a.toolsLay.Inactive = a.inactiveWidget
	return *a.toolsLay
}

// toolsLayoutNow is the arrangement the page packs for sizing. The session
// frame asks every page for its box on every frame, long before the tools
// page is drawn, so this must not create or repair layout.json: it peeks
// at the loaded copy (or the defaults) and lets the page body do the one
// real load when the page is on screen.
func (a *shelf) toolsLayoutNow() layout.Layout {
	if a.toolsLay != nil {
		return *a.toolsLay
	}
	on := a.settings.AIUsage.Enabled
	if a.toolsLayPeek == nil || a.toolsLayPeekOn != on {
		l := layout.DefaultWith(on)
		a.toolsLayPeek, a.toolsLayPeekOn = &l, on
	}
	return *a.toolsLayPeek
}

// inactiveWidget reports widgets that are in layout.json but take no cell
// right now: a widget whose feature is off (AI 用量), or an id this build
// cannot draw.
func (a *shelf) inactiveWidget(id string) bool {
	def := toolCardDefFor(id)
	return def == nil || (def.kind == cardWidget && !def.available(a))
}

// mutateTools applies fn to a copy of the layout and saves it, reporting
// whether the change took. The in-memory copy is replaced only after the
// write succeeds, so a failed Write leaves both the file and the shown
// arrangement as they were — and the caller learns the card never moved;
// a load that failed (toolsLayBroken) latches writes off entirely — the
// page mutates for the session, the recoverable file stays put, which
// still counts as taken.
func (a *shelf) mutateTools(fn func(*layout.Layout)) bool {
	return a.mutateToolsChanged(func(l *layout.Layout) bool {
		fn(l)
		return true
	})
}

// mutateToolsChanged is mutateTools for an operation that knows whether it
// changed anything: fn reports it, and a false answer writes nothing — a
// refused gesture (a size that does not fit, a drop on an occupied cell)
// leaves the file untouched instead of rewriting the same layout.
func (a *shelf) mutateToolsChanged(fn func(*layout.Layout) bool) bool {
	l := a.toolsLayout()
	l.Cards = slices.Clone(l.Cards)
	l.Pins = slices.Clone(l.Pins)
	if !fn(&l) {
		return false
	}
	if a.dataDir != "" && !a.toolsLayBroken {
		if err := layout.Write(a.dataDir, l); err != nil {
			a.report(fmt.Errorf("tools layout: %w", err))
			return false // keep the pre-change layout and the file as they were
		}
	}
	a.toolsLay = &l
	if a.win != nil {
		a.win.Invalidate()
	}
	return true
}

// resetToolsEdit ends editing and the picker; called on hide and on
// leaving the page so re-entry starts clean.
func (a *shelf) resetToolsEdit() {
	if a.clock != nil && a.clock.editing() {
		a.clock.endEdit()
		if a.visible && a.focusMode {
			a.leaveFocusMode()
		}
	}
	if p := a.toolsPage(); p != nil {
		p.closeDragPreview()
		p.editing, p.picking, p.press = false, false, nil
		p.jiggleID = ""
		p.dragCols, p.dragRows = 0, 0
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

func (p *toolsPage) title() string {
	if p.picking {
		return p.host.appLoc().ToolsCardCatalog()
	}
	return p.host.appLoc().ToolsTitle()
}

// actions is the page's own row: the pencil, or 完成 while editing or in
// the catalogue. When every card is placed the dashed + tile is the way
// into the catalogue; with no free cell it backs up with a + button here.
func (p *toolsPage) actions(c *ui.Context) []ui.Element {
	a := p.host
	live := a.tab == pageTools
	switch {
	case p.picking:
		return []ui.Element{a.toolDoneButton(c, live, "pick-done")}
	case p.editing:
		var out []ui.Element
		if p.pickable() {
			if !p.hasFreeCell() {
				b := a.iconButton(c, "tools-add", plusSymbol, loc(c).ToolsAddCard())
				if live && b.Clicked() {
					p.picking, p.press = true, nil
				}
				out = append(out, b)
			}
		}
		return append(out, a.toolDoneButton(c, live, "edit-done"))
	default:
		b := a.iconButton(c, "edit", pencilSymbol, loc(c).ToolsEdit())
		if live && b.Clicked() {
			p.editing, p.editAt = true, time.Now()
		}
		return []ui.Element{b}
	}
}

// toolDoneButton is the 完成 pill at the page's action row.
func (a *shelf) toolDoneButton(c *ui.Context, live bool, key string) ui.Element {
	b := ui.Row(c.Key(key)).Height(pageActionH).Padding(0, 10, 0, 10).Radius(pageActionH / 2).
		Background(accent).Center().Label(loc(c).Done())
	if live && b.Clicked() {
		if p := a.toolsPage(); p != nil {
			if p.picking {
				p.picking = false
			} else {
				p.closeDragPreview()
				p.editing, p.press = false, nil
			}
		}
	}
	b.Children(func() {
		panelLabel(c, nil, loc(c).Done(), 12, 600).TextColor(ink)
	})
	return b
}

// pickerGroups is the catalogue: action cards, then widgets, minus the
// ones already on the page or on the desktop.
func (p *toolsPage) pickerGroups() (groups [][]toolCardDef) {
	var actions, widgets []toolCardDef
	for _, def := range toolCatalog() {
		// The catalogue shows the rectangle a pick adds, so a def that
		// leaves rows at zero (the action cards' one cell) reads 1 × 1
		// rather than “N × 0”.
		if def.rows < 1 {
			def.rows = 1
		}
		if !def.available(p.host) || p.placed(def.id) {
			continue
		}
		if def.kind == cardWidget {
			widgets = append(widgets, def)
		} else {
			actions = append(actions, def)
		}
	}
	if len(actions) > 0 {
		groups = append(groups, actions)
	}
	if len(widgets) > 0 {
		groups = append(groups, widgets)
	}
	return
}

// placed reports whether a card is in the grid or pinned to the desktop.
func (p *toolsPage) placed(id string) bool {
	for _, c := range p.host.toolsLayout().Cards {
		if c.ID == id && !p.host.inactiveWidget(id) {
			return true
		}
	}
	for _, pin := range p.host.pins {
		if pin.cardID == id {
			return true
		}
	}
	return false
}

// pickable reports whether anything is left to add.
func (p *toolsPage) pickable() bool {
	for _, g := range p.pickerGroups() {
		if len(g) > 0 {
			return true
		}
	}
	return false
}

// hasFreeCell reports whether the dashed + tile has a cell on the grid.
func (p *toolsPage) hasFreeCell() bool {
	cards := p.host.toolsLayout().Cards
	cols := p.bandMinCols()
	return len(packToolCards(cards, cols, p.host.inactiveWidget, true)) >
		len(packToolCards(cards, cols, p.host.inactiveWidget, false))
}

// body is the page's content box: the catalogue while picking, else the
// card grid at the width its packing needs.
func (p *toolsPage) body(c *ui.Context, _ bool, live bool) ui.Element {
	if p.picking {
		return p.pickerBody(c, live)
	}
	g := p.grid()
	editing := p.editing
	d := p.press
	contentH := float32(0)
	for _, cell := range g.cells {
		contentH = max(contentH, cell.y+cell.h)
	}
	stageW := float32(g.cols)*toolCellW + float32(g.cols-1)*toolCellGap
	return ui.Column(c.Key("tools-stage")).Grow(1).MinHeight(0).Children(func() {
		scroll := ui.Scroll(c.Key("cards")).Grow(1).MinHeight(0).TrackScroll(&p.scroll)
		p.viewport = scroll.Bounds()
		// Pointer tracking makes the stage interactive and reports the
		// viewport-local pointer for the press bookkeeping.
		vpx, vpy, _ := scroll.PointerPosition()
		if d != nil && !d.settling {
			d.px, d.py = vpx, vpy+p.scroll.Y
		}
		scroll.Children(func() {
			// A box in normal flow carries the grid's real height: an
			// absolute child of the scroll itself counts toward neither
			// the scrollable content nor the offset, so a grid past the
			// visible rows could neither scroll nor be reached.
			ui.Box(c.Key("cards-stack")).Width(stageW).Height(contentH).Children(func() {
				for _, cell := range g.cells {
					// The grid never blurs: the cards are the content, and
					// a row cut by the viewport stays as sharp as the rest
					// (the tray and clipboard pages keep the edge melt).
					p.toolCell(c, cell, editing, live)
				}
				if d != nil && d.dragging && p.preview.window() == nil {
					p.toolLiftedCard(c, *d)
				}
			})
		})
	})
}

// toolCell draws one packed cell: the + tile, a parked card (in-flow,
// possibly mid-reorder), or the card itself.
func (p *toolsPage) toolCell(c *ui.Context, cell toolCell, editing, live bool) {
	a := p.host
	if cell.plus {
		plus := ui.Column(c.Key("tc-plus")).Absolute().Left(cell.x).Top(cell.y).
			Width(cell.w).Height(cell.h).Radius(rCard).Center().
			BorderWidth(1.5).BorderColor(ui.RGBA(255, 255, 255, 0.25)).
			BorderStyle(ui.BorderDashed).Label(loc(c).ToolsAddCard())
		if live {
			plus.HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind == ui.InputPointerUp && p.editing {
					p.picking = true
					return true
				}
				return false
			})
			plus.PointerPosition()
		}
		plus.Children(func() {
			ui.Icon(c, plusSymbol).Size(22, 22).TextColor(ink3)
		})
		return
	}
	spec := layout.Card{ID: cell.id, Span: 1, Rows: 1}
	if def := toolCardDefFor(cell.id); def != nil {
		spec.Span, spec.Rows = def.span, def.rows
	}
	for _, cc := range a.toolsLayout().Cards {
		if cc.ID == cell.id {
			spec.Span, spec.Rows = cc.Cols(), cc.Height()
		}
	}
	d := p.press
	parked := d != nil && d.id == cell.id && (d.dragging || d.settling)
	card := a.toolCard(c, spec, surfFor(editing))
	cx, cy := cell.x, cell.y
	switch {
	case parked && d.settling:
		p.toolSettle(card, d)
		card.Absolute().Width(cell.w).Height(cell.h)
		cx, cy = d.cardX, d.cardY
	case parked:
		// The parked element stays the pointer's press target; its
		// animated slot position converts card-local events to stage
		// coordinates.
		cx = card.AnimateTo("cx", cell.x, ui.Snappy)
		cy = card.AnimateTo("cy", cell.y, ui.Snappy)
		d.cardX, d.cardY = cx, cy
		card.Absolute().Left(cx).Top(cy).Width(cell.w).Height(cell.h).Opacity(0)
	default:
		// A card follows a re-packed grid on the design's 300 ms spring;
		// Reduce Motion puts it in its new cell at once.
		if reduceMotion(c) {
			card.Jump("cx", cell.x)
			card.Jump("cy", cell.y)
		} else {
			cx = card.AnimateTo("cx", cell.x, toolMoveSpring)
			cy = card.AnimateTo("cy", cell.y, toolMoveSpring)
		}
		card.Absolute().Left(cx).Top(cy).Width(cell.w).Height(cell.h)
		if editing {
			p.toolEditChrome(c, card, cell, spec, live)
		} else if live {
			p.clockCardBadges(c, card, spec)
		}
	}
	if live {
		p.toolCardInput(c, card, cell, cx, cy, editing, live)
	}
	if !parked {
		p.toolCardWobble(c, card, cell, editing)
	}
}

// surfFor picks the card surface: glyph-only while editing.
func surfFor(editing bool) toolCardSurface {
	if editing {
		return cardEdit
	}
	return cardPanel
}

// clockCardBadges adds the 1 × 1 clocks' corner control after the card has
// its place. An absolute child built before positioning resolves against
// the stage instead of the card, which put a second card's badge over the
// first; the desktop pin fills its own window, so its badge stays in the
// card body.
func (p *toolsPage) clockCardBadges(c *ui.Context, card ui.Element, spec layout.Card) {
	a := p.host
	if a.clock == nil || spec.Cols() != 1 || spec.Height() != 1 {
		return
	}
	switch spec.ID {
	case "timer":
		m := a.clock
		if m.phase != timerRunning && m.phase != timerPaused {
			return
		}
		if !card.Hovered() {
			return
		}
		card.Children(func() { a.clockCancelBadge(c, m).Absolute().Top(6).Right(6) })
	case "stopwatch":
		m := a.clock
		if m.watchRun || m.watchElapsed == 0 {
			return
		}
		card.Children(func() { a.clockResetBadge(c, m).Absolute().Top(6).Right(6) })
	}
}

// toolLiftedCard draws the dragged copy above the grid at the pointer.
// It takes no input: the parked card keeps the press so release always
// lands on the same element the down did.
func (p *toolsPage) toolLiftedCard(c *ui.Context, d toolPress) {
	a := p.host
	spec := p.dragSpec(d.id)
	card := a.toolCard(c, spec, cardEdit, "lift")
	card.Absolute().Left(d.px-d.grabDX).Top(d.py-d.grabDY).
		Width(toolColW(spec.Cols())).Height(toolRowH(spec.Height())).
		Scale(1.06, 1.06).TransformOrigin(0.5, 0.5).IgnoreInput(true)
}

// toolCardInput wires the pointer: down starts a press, move past the
// slop reorders in edit or kills the click outside it, up drops or lands,
// and the long-press deadline turns editing on. ox, oy are the card's
// drawn position: a card still springing into its cell must map the press
// from where it is painted, not from the cell it is heading for.
func (p *toolsPage) toolCardInput(c *ui.Context, card ui.Element, cell toolCell, ox, oy float32, editing, live bool) {
	a := p.host
	card.PointerPosition() // interactive: a hit-test and tracking target
	card.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			if p.press != nil {
				return false
			}
			d := &toolPress{
				id: cell.id, downAt: ev.Time,
				downX: ox + ev.X, downY: oy + ev.Y,
				px: ox + ev.X, py: oy + ev.Y,
				grabDX: ev.X, grabDY: ev.Y,
				cardX: ox, cardY: oy,
			}
			p.press = d
			c.AnimationFrame() // the long-press deadline needs frames
			return true
		case ui.InputPointerMove:
			defer p.syncDragPreview(p.toolPointer(ev))
			d := p.press
			if d == nil || d.id != cell.id || d.settling {
				return d != nil && d.id == cell.id && d.settling
			}
			d.px, d.py = d.cardX+ev.X, d.cardY+ev.Y
			if d.dragging {
				if ins := toolInsertIndex(a.toolsLayout().Cards, p.dragCols, d.id, d.index, d.px, d.py); ins >= 0 {
					d.index = ins
				}
				return true
			}
			if hypot(d.px-d.downX, d.py-d.downY) <= toolDragSlop {
				return true
			}
			if p.editing {
				// The grid's width is pinned where the drag began: only
				// rows may grow while the pointer moves.
				g := p.grid()
				p.dragCols, p.dragRows = g.cols, g.rows
				d.dragging = true
				d.index = toolInsertIndex(a.toolsLayout().Cards, p.dragCols, d.id, -1, d.px, d.py)
				if d.index < 0 {
					d.index = len(a.toolsLayout().Cards) - 1
				}
			} else {
				d.suppress = true
			}
			return true
		case ui.InputPointerUp:
			d := p.press
			if d == nil || d.id != cell.id {
				return false
			}
			if d.settling {
				return true
			}
			if d.dragging {
				d.px, d.py = d.cardX+ev.X, d.cardY+ev.Y
				p.toolCardDrop(d, p.toolPointer(ev))
				return true
			}
			p.skipClick = d.suppress // a moved press never becomes a tap
			p.press = nil
			return true
		}
		return false
	})
	// The long-press deadline is checked at build time: a still press asks
	// for every frame until it crosses the slop or editing starts, so the
	// deadline fires on the frame clock even when nothing else animates.
	if d := p.press; d != nil && d.id == cell.id && !d.dragging && !d.settling && !d.suppress && !p.editing {
		c.AnimationFrame()
		if c.Now().Sub(d.downAt) >= durLongPress {
			p.editing, p.editAt = true, c.Now()
			d.suppress = true // this press only entered edit; no click, no drag yet
		}
	}
	if !editing && live && card.Clicked() {
		if p.skipClick {
			p.skipClick = false
		} else {
			p.toolCardTapped(cell.id)
		}
	}
}

// toolCardTapped runs the card's own action: an action card works on the
// tray's selection (design §3: 对托盘页当前选中的项目执行), a widget card
// does what its whole-card tap has always meant (keep-awake toggles, the
// clipboard card copies its newest capture, AI 用量 opens its page) —
// the music card opens the home page: its transport buttons and rails own
// their presses, and a tap beside them is never a play toggle.
// A card with nothing to act on jiggles with the hint.
func (p *toolsPage) toolCardTapped(id string) {
	a := p.host
	if p.editing {
		return
	}
	if def := toolCardDefFor(id); def != nil && def.kind == cardWidget && def.id == "playing" {
		// The transport and the rails own their own presses; a tap on the
		// card itself opens the home page and its player.
		a.openHomePage()
		return
	}
	if a.runToolCard(id) {
		return
	}
	p.jiggleID, p.jiggleAt = id, time.Now()
	a.toolCardTapFeedback()
}

// toolCardTapFeedback is shared by tools-page and desktop-card taps.
func (a *shelf) toolCardTapFeedback() {
	if a.conv != nil && a.conv.busy() {
		a.toastStatus(a.appLoc().OneJobAtATime(), time.Now().Add(2*time.Second), statusInfo)
		return
	}
	a.toastStatus(a.appLoc().DropFilesHere(), time.Now().Add(2*time.Second), statusInfo)
}

func hypot(dx, dy float32) float64 {
	return math.Hypot(float64(dx), float64(dy))
}

// toolCardDrop ends a reorder: inside the stage it commits the index,
// outside the panel it pins the card to the desktop (design §2 把卡片拖出
// 面板边缘 = 固定到桌面), elsewhere it flies back.
func (p *toolsPage) toolCardDrop(d *toolPress, cursor mygo.Point) {
	defer p.closeDragPreview()
	a := p.host
	// The drop lands inside the stage only when the release is inside the
	// viewport the scroll shows right now: a card dragged below the visible
	// rows is on its way to the desktop, not to a slot further down the
	// grid it cannot see.
	viewH := p.viewport.H
	if viewH <= 0 {
		viewH = pageContentHeight(p)
	}
	inside := d.px >= 0 && d.px <= p.stageW() &&
		d.py >= p.scroll.Y-0.5 && d.py <= p.scroll.Y+viewH+0.5
	target := toolCell{id: d.id, x: d.cardX, y: d.cardY}
	if inside {
		if ins := d.index; ins >= 0 {
			a.mutateTools(func(l *layout.Layout) { l.MoveCard(d.id, ins) })
			// The drop ends the pinned width: the panel follows the
			// committed packing, and the settle flight aims at its cell.
			p.dragCols, p.dragRows = 0, 0
			for _, cell := range toolGridFor(a.toolsLayout().Cards, a.inactiveWidget, true, p.bandMinCols()).cells {
				if cell.id == d.id {
					target = cell
				}
			}
		}
	} else {
		// A release beyond the panel's drawn body pins the card at the
		// cursor (grab point preserved so the window lands under it);
		// a release on another pane of the panel still flies back.
		panel := a.panelDropRect()
		if !insideRect(cursor, panel) {
			spec := p.dragSpec(d.id)
			span, w := spec.Cols(), pinCardWidth(d.id, spec.Cols())
			disp := displayIDAt(cursor, 0)
			area := pinArea(disp)
			edge := pinReleaseEdge(cursor, area)
			frame := pinFrameUnder(cursor, d.grabDX, d.grabDY, toolColW(spec.Cols()), toolRowH(spec.Height()), w)
			if edge != pinFree {
				frame = pinStripFrame(edge, area, cursor.Y) // the 24 pt edge rule, same as pin drags
			}
			var committed bool
			if it := p.preview.take(); it != nil {
				it.display, it.edge, it.expanded, it.frame = disp, edge, edge == pinFree, frame
				committed = a.commitPin(it)
				if !committed {
					if it.win != nil {
						it.win.Hide()
						it.win.Destroy()
						it.win = nil
					}
				}
			} else {
				committed = a.addPin(d.id, span, disp, edge, false, frame) != nil
			}
			if committed {
				p.press = nil // the parked cell is gone: no settle back
				return
			}
			// The failed write kept the grid unchanged; fly back.
		}
	}
	d.settling, d.dragging = true, false
	p.dragCols, p.dragRows = 0, 0
	d.settleX, d.settleY = target.x, target.y
}

// stageW is the grid's content width in the packing it shows now.
func (p *toolsPage) stageW() float32 {
	g := p.grid()
	return float32(g.cols)*toolCellW + float32(g.cols-1)*toolCellGap
}

// toolSettle animates the released card from the pointer to its cell.
// The press clears once the flight rests: Settled, not Arrived — Arrived
// reports only the frame a moving value comes to rest, so a flight that
// starts at its goal (an edit drag released back where it began, index
// unchanged) never moves and never arrives, and the press would stick.
func (p *toolsPage) toolSettle(card ui.Element, d *toolPress) {
	if !d.settleJump {
		card.Jump("cx", d.px-d.grabDX)
		card.Jump("cy", d.py-d.grabDY)
		card.Jump("sc", 1.06)
		d.settleJump = true
	}
	cx := card.AnimateTo("cx", d.settleX, ui.Animation{Spring: ui.SpringOptions{Duration: durSettle, Bounce: 0.12}})
	cy := card.AnimateTo("cy", d.settleY, ui.Animation{Spring: ui.SpringOptions{Duration: durSettle, Bounce: 0.12}})
	sc := card.AnimateTo("sc", 1, ui.Animation{Spring: ui.SpringOptions{Duration: durSettle, Bounce: 0.12}})
	card.Left(cx).Top(cy).Scale(sc, sc).TransformOrigin(0.5, 0.5)
	d.cardX, d.cardY = cx, cy
	if card.Settled("cx") && card.Settled("cy") && card.Settled("sc") {
		p.press = nil
	}
}

// toolEditChrome is the editing overlay on one card: the ⊖ badge at the
// top left, and — for a card that has sizes to choose from — the handle at
// the bottom right.
func (p *toolsPage) toolEditChrome(c *ui.Context, card ui.Element, cell toolCell, spec layout.Card, live bool) {
	a := p.host
	card.Children(func() {
		minus := ui.ButtonBase(c.Key("minus-"+cell.id)).Absolute().Left(2).Top(2).Size(18, 18).
			Radius(9).Center().Background(surfaceBadge).Border(1.5, surface).
			Label(loc(c).Remove()).Tooltip(loc(c).Remove())
		minus.Children(func() {
			ui.Icon(c, minusSymbol).Size(8, 8).TextColor(ink)
		})
		if live {
			minus.HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind == ui.InputPointerUp {
					if a.mutateToolsChanged(func(l *layout.Layout) bool {
						l.RemoveCard(cell.id)
						return true
					}) {
						p.press = nil
					}
				}
				return true // swallow the down too, so no card press starts
			})
			minus.PointerPosition()
		}
		if len(cardSizes(cell.id)) > 1 {
			next := cardSizes(cell.id)[0]
			handle := ui.ButtonBase(c.Key("size-"+cell.id)).Absolute().Right(5).Bottom(5).Size(12, 12).
				Center().Label(loc(c).CellSize(next[0], next[1])).Tooltip(loc(c).CellSize(next[0], next[1]))
			handle.Children(func() {
				ui.Icon(c, spanSymbol).Size(10, 10).TextColor(ink2)
			})
			if live {
				handle.HandleInput(func(ev ui.InputEvent) bool {
					if ev.Kind == ui.InputPointerUp {
						p.cycleSize(spec)
					}
					return true
				})
				handle.PointerPosition()
			}
		}
	})
}

// cardSizes are the rectangles a card cycles through with its handle, the
// card's current one first.
func cardSizes(id string) [][2]int {
	if def := toolCardDefFor(id); def != nil && def.kind == cardWidget {
		switch id {
		case "playing":
			return [][2]int{{2, 2}, {2, 1}, {1, 1}}
		case "awake":
			return [][2]int{{1, 1}, {2, 1}, {2, 2}}
		case "usage":
			return [][2]int{{1, 1}, {2, 1}}
		case "recent", "clip":
			return [][2]int{{2, 1}, {2, 2}, {1, 1}}
		case "note":
			return [][2]int{{1, 1}, {2, 1}}
		case "timer":
			return [][2]int{{2, 1}, {1, 1}}
		case "stopwatch":
			return [][2]int{{1, 1}, {2, 1}}
		}
	}
	return [][2]int{{1, 1}}
}

// cycleSize moves a card to the next rectangle after its current one.
func (p *toolsPage) cycleSize(spec layout.Card) {
	sizes := cardSizes(spec.ID)
	next := sizes[0]
	for i, s := range sizes {
		if s[0] == spec.Cols() && s[1] == spec.Height() {
			next = sizes[(i+1)%len(sizes)]
			break
		}
	}
	p.host.mutateToolsChanged(func(l *layout.Layout) bool {
		l.SetCardSize(spec.ID, next[0], next[1])
		return true
	})
}

// toolCardWobble is the editing wobble: ±0.7° on a 520ms period, odd
// columns in counter-phase; still under Reduce Motion.
func (p *toolsPage) toolCardWobble(c *ui.Context, card ui.Element, cell toolCell, editing bool) {
	if !editing || reduceMotion(c) {
		return
	}
	col := int(math.Round(float64(cell.x / (toolColW(1) + toolCellGap))))
	phase := 0.0
	if col%2 == 1 {
		phase = math.Pi
	}
	k := card.Loop("wobble", durWobble, ui.Linear)
	angle := toolWobbleDeg * math.Sin(2*math.Pi*float64(k)+phase)
	card.Rotate(float32(angle)).TransformOrigin(0.5, 0.5)
	c.AnimationFrame()
}

// pickerBody is the catalogue (design §3): a raised card holding two
// columns of rows — action cards, then widgets — with the default size on
// the right. Picking a row places it at the first free rectangle.
func (p *toolsPage) pickerBody(c *ui.Context, live bool) ui.Element {
	groups := p.pickerGroups()
	body := ui.Column(c.Key("picker")).FillWidth().FillHeight()
	body.Children(func() {
		// The list scrolls inside the card: with every card removed the
		// catalogue is taller than the 174pt box, and a clipped row could
		// never be picked.
		scroll := ui.Scroll(c.Key("picker-scroll")).FillWidth().Grow(1).MinHeight(0)
		scroll.Children(func() {
			card := ui.Column(c.Key("picker-card")).FillWidth().Background(surfaceRaise).
				Radius(rCard).Padding(10, 12, 12, 12).Gap(4)
			card.Children(func() {
				panelLabel(c, nil, loc(c).ToolsCardCatalog(), typeSub, 600).TextColor(ink3)
				if len(groups) == 0 {
					panelLabel(c, nil, loc(c).ToolsAllPlaced(), typeCallout, 0).TextColor(ink3)
					return
				}
				for i, group := range groups {
					if i > 0 {
						ui.Box(c).Height(4)
					}
					ui.Grid(c.Key(fmt.Sprintf("pick-group-%d", i))).
						ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(4).FillWidth().Children(func() {
						for _, def := range group {
							p.pickRow(c, def, live)
						}
					})
				}
			})
		})
	})
	return body
}

// pickRow is one catalogue row: glyph, name and the default size.
func (p *toolsPage) pickRow(c *ui.Context, def toolCardDef, live bool) ui.Element {
	a := p.host
	row := ui.Row(c.Key("pick-"+def.id)).Height(toolPickRowH).Gap(8).
		Padding(0, 6, 0, 6).Radius(rRow).AlignItems(ui.Center).Label(def.name(loc(c)))
	if row.Hovered() {
		row.Background(fill1)
	}
	row.Children(func() {
		ui.Icon(c, def.icon).Size(15, 15).TextColor(ink2)
		panelLabel(c, nil, def.name(loc(c)), typeCallout, 0).TextColor(ink).Grow(1).MinWidth(0)
		if def.span != 1 || def.rows != 1 {
			ui.Text(c, loc(c).CellSize(def.span, def.rows)).SingleLine().FontSize(typeMini).TextColor(ink3)
		}
	})
	if live && row.Clicked() {
		a.mutateToolsChanged(func(l *layout.Layout) bool {
			l.AddCardSize(def.id, def.span, def.rows)
			return true
		})
		p.picking = false
	}
	return row
}
