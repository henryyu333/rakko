//go:build darwin

package main

// The 工具 page: one grid holding the action cards and the widgets. Cards
// come from layout.json (internal/layout); drawing and per-card behaviour
// live in tools_cards.go. This file owns the adaptive packing (three to
// six columns, one to three drawn rows), the long-press /
// drag-to-reorder gestures, the editing chrome (⊖ badge, arc handle), and
// the page's own actions (the pencil, 完成). The editing drawer, the card
// menu and the corner resize live in tools_edit.go.

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

	toolPickRowH = 30 // 目录行高（design §3 .pi 30px）
	toolDragSlop = 6  // 普通态拖动超过此距离作废点击
	toolJiggle   = 300 * time.Millisecond
)

// toolMoveSpring moves cards to their new cells after the panel's packing
// changes: 300 ms, Bounce 0.15 (design §2).
var toolMoveSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.15}}

// toolCell is one card's position in content coordinates.
type toolCell struct {
	id   string
	x, y float32
	w, h float32
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

	// A card dragged in from the library has no parked cell: fresh marks
	// it, spec is its default rectangle.
	fresh    bool
	spec     layout.Card
	vx       float32   // horizontal pointer velocity, for the lift's tilt
	lastX    float32   // pointer x at the last move
	lastMove time.Time // time of the last move (tilt relax after 60 ms)
}

// toolsPage is the card grid page.
type toolsPage struct {
	host     *shelf
	scroll   ui.ScrollState
	viewport ui.Rect     // previous frame's scroll viewport, in window coordinates
	preview  dragPreview // transient drag window; absent from layout and live pins

	editing   bool
	press     *toolPress
	skipClick bool   // the last release was a suppressed press, not a tap
	jiggleID  string // card shaking for 无可执行项
	jiggleAt  time.Time
	tapX      float32 // the last card tap's press, in stage content space
	tapY      float32

	// A reorder drag pins the grid's columns where it began and may only
	// add rows: the panel must not follow the pointer sideways.
	dragCols int
	dragRows int

	// The resize gesture, the library's own scroll position and the card
	// context menu (nil while closed).
	rs         *toolResize
	libScroll  ui.ScrollState
	menu       *toolMenu
	lp         *toolLibPress
	chromeAt   time.Time // first frame in editing, for the chrome's pop
	libraryTop float32   // drawer's top in the grid's content coordinates
	stageWNow  float32   // the drawn grid width, for hit tests outside grid()
	// reduceMotion is the window's preference, cached for the input paths
	// that run outside a build.
	reduceMotion bool
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

// gridCards is the card list the grid packs now: the stored order, the
// order a reorder previews, or the order with a library card at the
// pointer's slot. A resize shows its transient rectangle before it is
// saved. It records the preview's insertion index so the release commits
// the cell the pointer last meant.
func (p *toolsPage) gridCards() ([]layout.Card, int) {
	l := p.host.toolsLayoutNow()
	d := p.press
	if d == nil || !p.editing || !d.dragging {
		return p.withResize(l.Cards), 0
	}
	cols := p.dragCols
	if cols <= 0 {
		cols = toolMinCols
	}
	if p.overLibrary() {
		d.index = -1
		return p.withResize(l.Cards), cols
	}
	if d.fresh {
		d.index = max(0, min(d.index, len(l.Cards)))
		out := make([]layout.Card, 0, len(l.Cards)+1)
		out = append(out, l.Cards[:d.index]...)
		out = append(out, d.spec)
		out = append(out, l.Cards[d.index:]...)
		return p.withResize(out), cols
	}
	if ins := toolInsertIndex(l.Cards, cols, d.id, d.index, d.px, d.py); ins >= 0 {
		d.index = ins
		return p.withResize(toolVirtualCards(l.Cards, d.id, ins)), cols
	}
	return p.withResize(l.Cards), cols
}

// withResize overlays the live resize rectangle on the stored list.
func (p *toolsPage) withResize(cards []layout.Card) []layout.Card {
	if p.rs == nil {
		return cards
	}
	out := slices.Clone(cards)
	for i := range out {
		if out[i].ID == p.rs.id {
			out[i].Span, out[i].Rows = p.rs.cur[0], p.rs.cur[1]
		}
	}
	return out
}

// grid is the packing the page shows: the narrowest ladder rung that holds
// the cards, widened for the band's ears. A reorder drag pins its starting
// columns and may only grow rows.
func (p *toolsPage) grid() toolGrid {
	cards, locked := p.gridCards()
	if locked > 0 {
		g := toolGridAt(cards, locked, p.host.inactiveWidget)
		// Rows are cumulative during one drag: once the preview has needed
		// a third row, moving the card back must not make the shell and
		// the drawer jump upward under the pointer.
		if g.rows > p.dragRows {
			p.dragRows = g.rows
		}
		if p.dragRows > g.rows {
			g.rows, g.vis = p.dragRows, min(p.dragRows, toolMaxRows)
		}
		return g
	}
	return toolGridFor(cards, p.host.inactiveWidget, p.bandMinCols())
}

// width is the shell width the page needs now (pageWidth).
func (p *toolsPage) width() float32 { return toolPanelW(p.grid().cols) }

// height is the shell height the page needs now (pageHeight): the grid's
// rows plus the 80pt card library while editing.
func (p *toolsPage) height() float32 {
	h := toolPanelH(p.grid().vis)
	if p.editing {
		h += toolLibraryH
	}
	return h
}

// maxSize is the largest shell the page can ask for, for the session
// frame: six columns, three rows, and the 80pt card library under them.
func (p *toolsPage) maxSize() (float32, float32) {
	return toolPanelW(toolMaxCols), toolPanelH(toolMaxRows) + toolLibraryH
}

// maxSizeWithin is the widest tools page whose tallest state still fits
// limit: the six-column width is kept, and the row count picks the largest
// grid (with or without the library) that fits the island side.
func (p *toolsPage) maxSizeWithin(limit float32) (float32, float32) {
	w := toolPanelW(toolMaxCols)
	best := float32(0)
	for rows := 1; rows <= toolMaxRows; rows++ {
		for _, editing := range []bool{false, true} {
			h := toolPanelH(rows)
			if editing {
				h += toolLibraryH
			}
			if h <= limit {
				best = max(best, h)
			}
		}
	}
	if best == 0 {
		return w, toolPanelH(1)
	}
	return w, best
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

// resetToolsEdit ends editing and any gesture; called on hide and on
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
		p.editing, p.press = false, nil
		p.jiggleID = ""
		p.dragCols, p.dragRows = 0, 0
		p.rs, p.menu = nil, nil
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

func (p *toolsPage) title() string { return p.host.appLoc().ToolsTitle() }

// actions is the page's own row: the pencil, or 完成 while editing.
func (p *toolsPage) actions(c *ui.Context) []ui.Element {
	a := p.host
	live := a.tab == pageTools
	if p.editing {
		return []ui.Element{a.toolDoneButton(c, live, "edit-done")}
	}
	b := a.iconButton(c, "edit", pencilSymbol, loc(c).ToolsEdit())
	if live && b.Clicked() {
		p.editing = true
		p.menu = nil
	}
	return []ui.Element{b}
}

// toolDoneButton is the 完成 pill at the page's action row.
func (a *shelf) toolDoneButton(c *ui.Context, live bool, key string) ui.Element {
	b := ui.Row(c.Key(key)).Height(pageActionH).Padding(0, 10, 0, 10).Radius(pageActionH / 2).
		Background(accent).Center().Label(loc(c).Done())
	if live && b.Clicked() {
		if p := a.toolsPage(); p != nil {
			p.closeDragPreview()
			p.editing, p.press = false, nil
			p.rs, p.menu, p.chromeAt = nil, nil, time.Time{}
		}
	}
	b.Children(func() {
		panelLabel(c, nil, loc(c).Done(), 12, 600).TextColor(ink)
	})
	return b
}

// libraryCards are the drawer's cards: everything the catalogue still
// offers that is neither in the grid nor on the desktop, in catalogue
// order. available keeps a feature that is off out (AI 用量).
func (p *toolsPage) libraryCards() []toolCardDef {
	var out []toolCardDef
	for _, def := range toolCatalog() {
		if def.rows < 1 {
			def.rows = 1
		}
		if !def.available(p.host) || p.placed(def.id) {
			continue
		}
		out = append(out, def)
	}
	return out
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

// body is the page's content box: the card grid at the width its packing
// needs, the card library under it while editing, and the lifted card on
// top of both.
func (p *toolsPage) body(c *ui.Context, _ bool, live bool) ui.Element {
	g := p.grid()
	editing := p.editing
	p.reduceMotion = c.Preferences().ReduceMotion
	if editing && p.chromeAt.IsZero() {
		p.chromeAt = c.Now()
	} else if !editing {
		p.chromeAt = time.Time{}
	}
	d := p.press
	contentH := float32(0)
	for _, cell := range g.cells {
		contentH = max(contentH, cell.y+cell.h)
	}
	stageW := float32(g.cols)*toolCellW + float32(g.cols-1)*toolCellGap
	p.stageWNow = stageW
	return ui.Column(c.Key("tools-stage")).Grow(1).MinHeight(0).Children(func() {
		scroll := ui.Scroll(c.Key("cards")).Grow(1).MinHeight(0).TrackScroll(&p.scroll)
		p.viewport = scroll.Bounds()
		// The visible drawer is drawn over the shell; the grid keeps this
		// spacer so a card can never sit under the drawer.
		p.libraryTop = p.host.pageShellH() - toolLibraryH - p.viewport.Y + p.scroll.Y
		scroll.PointerPosition() // pointer tracking: the wheel and the rails
		scroll.Children(func() {
			// A box in normal flow carries the grid's real height: an
			// absolute child of the scroll itself counts toward neither
			// the scrollable content nor the offset, so a grid past the
			// visible rows could neither scroll nor be reached.
			ui.Box(c.Key("cards-stack")).Width(stageW).Height(contentH).Children(func() {
				if d != nil && d.dragging && d.index >= 0 && !p.overLibrary() && p.preview.window() == nil {
					for _, cell := range g.cells {
						if cell.id == d.id {
							p.toolSlot(c, cell, d)
							break
						}
					}
				}
				for _, cell := range g.cells {
					// The grid never blurs: the cards are the content, and
					// a row cut by the viewport stays as sharp as the rest
					// (the tray and clipboard pages keep the edge melt).
					p.toolCell(c, cell, editing, live)
				}
				if p.rs != nil {
					p.toolResizeBand(c)
				}
			})
		})
		if editing {
			ui.Box(c.Key("library-space")).FillWidth().Height(toolLibraryH)
		}
		if d != nil && d.dragging && p.preview.window() == nil {
			layer := ui.Column(c.Key("tools-lift")).Absolute().Left(0).Top(0).Right(0).Bottom(0).IgnoreInput(true)
			layer.Children(func() { p.toolLiftedCard(c, *d) })
		}
	})
}

// overLibrary reports whether the pointer is below the grid's viewport,
// where the editing drawer sits: a reorder there means removal, and a
// fresh library drag there is cancelled on release.
func (p *toolsPage) overLibrary() bool {
	d := p.press
	if d == nil || p.viewport.H <= 0 {
		return false
	}
	// The drawer is a full-width band at the shell's bottom: the grid's
	// content x runs from -sheetPadX to stageW+sheetPadX there, and the
	// band is exactly toolLibraryH high. A point below it or beside it is
	// outside the panel, not a removal.
	x0 := float32(-sheetPadX)
	x1 := p.stageWNow + float32(sheetPadX)
	y0, y1 := p.libraryTop, p.libraryTop+toolLibraryH
	return d.px >= x0 && d.px <= x1 && d.py >= y0 && d.py <= y1
}

// toolSlot is the dashed placeholder at the cell the dragged card would
// take. It follows the same spring as the cards that make room for it.
func (p *toolsPage) toolSlot(c *ui.Context, cell toolCell, d *toolPress) {
	slot := ui.Box(c.Key("tool-slot")).Absolute().Width(cell.w).Height(cell.h).
		Radius(rCard).BorderWidth(1.5).BorderColor(ui.RGBA(255, 255, 255, 0.22)).
		BorderStyle(ui.BorderDashed).Background(ui.RGBA(255, 255, 255, 0.025)).IgnoreInput(true)
	var x, y float32
	if reduceMotion(c) {
		x, y = slot.Jump("sx", cell.x), slot.Jump("sy", cell.y)
	} else {
		x = slot.AnimateTo("sx", cell.x, toolMoveSpring)
		y = slot.AnimateTo("sy", cell.y, toolMoveSpring)
	}
	slot.Left(x).Top(y)
}

// toolCell draws one packed cell: a parked card (in-flow, possibly a
// reorder's invisible press target), or the card itself. A fresh library
// card has no cell of its own: the slot and the lifted copy show it.
func (p *toolsPage) toolCell(c *ui.Context, cell toolCell, editing, live bool) {
	a := p.host
	d := p.press
	if d != nil && d.fresh && d.id == cell.id {
		return
	}
	spec := p.cardSpec(cell)
	parked := d != nil && d.id == cell.id && (d.dragging || d.settling)
	card := a.toolCard(c, spec, surfFor(editing))
	cx, cy := cell.x, cell.y
	switch {
	case parked && d.settling:
		p.toolSettle(c, card, d)
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
	}
	if live {
		p.toolCardInput(c, card, cell, cx, cy, editing, live)
	}
}

// surfFor picks the card surface: glyph-only while editing.
func surfFor(editing bool) toolCardSurface {
	if editing {
		return cardEdit
	}
	return cardPanel
}

// toolLiftedCard draws the dragged copy above the grid at the pointer.
// It takes no input: the parked card keeps the press so release always
// lands on the same element the down did.
func (p *toolsPage) toolLiftedCard(c *ui.Context, d toolPress) {
	a := p.host
	spec := p.dragSpec(d.id)
	tilt := p.dragTilt(c.Now())
	if tilt != 0 {
		// Keep the frames coming so a stopped pointer relaxes the tilt
		// (dragTilt decays the velocity after 60 ms).
		c.AnimationFrame()
	}
	card := a.toolCard(c, spec, cardEdit, "lift")
	// The lifted copy is the design's `.lifted`: the darker fill, the
	// large shadow, 1.04 and a tilt following the horizontal velocity.
	// Reduce Motion keeps only the shadow.
	card.Background(ui.Hex("#3A3A3D"))
	card = card.Absolute().Left(d.px-d.grabDX).Top(d.py-d.grabDY-p.scroll.Y).
		Width(toolColW(spec.Cols())).Height(toolRowH(spec.Height())).
		Shadow(0, 14, 28, 0, ui.RGBA(0, 0, 0, 0.42)).IgnoreInput(true)
	if !p.reduceMotion {
		card.Scale(1.04, 1.04).Rotate(tilt).TransformOrigin(0.5, 0.5)
	}
	c.AnimationFrame()
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
			if ev.Button == 1 {
				p.openMenu(cell.id, ox+ev.X, oy+ev.Y-p.scroll.Y)
				return true
			}
			if ev.Button != 0 {
				return false
			}
			p.closeMenu()
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
				if !d.lastMove.IsZero() {
					ms := max(ev.Time.Sub(d.lastMove).Milliseconds(), 8)
					d.vx = d.vx*0.75 + (d.px-d.lastX)/float32(ms)*16*0.25
				}
				d.lastX, d.lastMove = d.px, ev.Time
				if p.overLibrary() {
					d.index = -1
				} else if ins := toolInsertIndex(a.toolsLayout().Cards, p.dragCols, d.id, d.index, d.px, d.py); ins >= 0 {
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
				d.lastX, d.lastMove = d.px, ev.Time
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
			p.tapX, p.tapY = d.downX, d.downY
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
			p.editing = true
			d.suppress = true // this press only entered edit; no click, no drag yet
		}
	}
	if !editing && live && card.Clicked() {
		if p.skipClick {
			p.skipClick = false
		} else {
			p.toolCardTappedAt(cell.id, cell, p.tapX, p.tapY)
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

// toolCardTappedAt is a click with its press position: the 2 × 1
// countdown's idle reading opens the custom length editor, while a press
// elsewhere on the card starts it (v3 §5). Pins keep the plain tap.
func (p *toolsPage) toolCardTappedAt(id string, cell toolCell, x, y float32) {
	if id == "timer" && p.host.clock != nil && p.host.clock.phase == timerIdle &&
		cell.w > toolColW(1)+1 && y <= cell.y+cardPad+cardRowH+4 && x >= cell.x+cell.w*0.45 {
		p.host.clock.beginEdit(p.host.clock.clock())
		return
	}
	p.toolCardTapped(id)
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
	if p.editing && p.overLibrary() {
		// The library is the removal target: a reorder dropped there
		// leaves the page; a library card released there is simply
		// cancelled.
		if !d.fresh {
			a.mutateToolsChanged(func(l *layout.Layout) bool {
				l.RemoveCard(d.id)
				return true
			})
		}
		p.press = nil
		p.dragCols, p.dragRows = 0, 0
		return
	}
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
			for _, cell := range toolGridFor(a.toolsLayout().Cards, a.inactiveWidget, p.bandMinCols()).cells {
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
func (p *toolsPage) toolSettle(c *ui.Context, card ui.Element, d *toolPress) {
	tilt := min(max(d.vx*0.55, -5), 5)
	if reduceMotion(c) {
		card.Jump("cx", d.settleX)
		card.Jump("cy", d.settleY)
		card.Jump("sc", 1)
		card.Jump("rot", 0)
		card.Left(d.settleX).Top(d.settleY)
		d.cardX, d.cardY = d.settleX, d.settleY
		p.press = nil
		return
	}
	if !d.settleJump {
		card.Jump("cx", d.px-d.grabDX)
		card.Jump("cy", d.py-d.grabDY)
		card.Jump("sc", 1.04)
		card.Jump("rot", tilt)
		d.settleJump = true
	}
	anim := ui.Animation{Spring: ui.SpringOptions{Duration: durSettle, Bounce: 0.15}}
	cx := card.AnimateTo("cx", d.settleX, anim)
	cy := card.AnimateTo("cy", d.settleY, anim)
	sc := card.AnimateTo("sc", 1, anim)
	rot := card.AnimateTo("rot", 0, anim)
	card.Left(cx).Top(cy).Scale(sc, sc).Rotate(rot).TransformOrigin(0.5, 0.5)
	d.cardX, d.cardY = cx, cy
	if card.Settled("cx") && card.Settled("cy") && card.Settled("sc") && card.Settled("rot") {
		p.press = nil
	}
}

// toolChrome draws the editing chrome over the whole shell: the ⊖ badge
// at each card's top-left, nine points outside it so it never covers the
// editing glyph, and the arc handle inside the bottom-right corner. The
// page content clips at the grid's first row and column, and a badge at
// the card's corner would be cut there; drawing the chrome over the shell
// keeps the 18pt square whole. The layer only shows cards whose whole
// rectangle is inside the scrolled viewport.
func (p *toolsPage) toolChrome(c *ui.Context, live bool) {
	if !p.editing || !live {
		return
	}
	a, l := p.host, loc(c)
	// A 1pt anchor carries the shared 220ms pop; a full-size layer would
	// sit between the cards and the pointer and swallow their presses.
	clock := ui.Box(c.Key("chrome-clock")).Absolute().Left(0).Top(0).Size(1, 1).IgnoreInput(true)
	pop := float32(1)
	if !reduceMotion(c) && !p.chromeAt.IsZero() {
		if c.Now().Equal(p.chromeAt) {
			clock.Jump("chrome", 0)
		}
		pop = clock.AnimateTo("chrome", 1, ui.Animation{Spring: ui.SpringOptions{Duration: 220 * time.Millisecond, Bounce: 0.15}})
		if pop < 1 {
			c.AnimationFrame()
		}
	}
	top := toolsContentTop()
	for _, cell := range p.grid().cells {
		if d := p.press; d != nil && d.id == cell.id && (d.dragging || d.settling) {
			continue // the lifted copy carries the card, not its chrome
		}
		y := cell.y - p.scroll.Y
		visible := !(p.viewport.H > 0 && (y < 0 || y+cell.h > p.viewport.H+0.5))
		activeResize := p.rs != nil && p.rs.id == cell.id
		if !visible && !activeResize {
			continue // never float the chrome over the action row or the drawer
		}
		spec := p.cardSpec(cell)
		x := sheetPadX + cell.x
		sy := top + y
		if !visible {
			// The resizing card's handle is the pointer's press target;
			// it must stay in the tree even after the packing moves the
			// card out of the visible rows, or the release is lost.
			if len(cardSizes(cell.id)) < 2 {
				continue
			}
			p.toolResizeHandle(c, cell, spec, x, sy, pop)
			continue
		}
		minus := ui.ButtonBase(c.Key("minus-"+cell.id)).Absolute().Left(x-9).Top(sy-9).Size(18, 18).
			Radius(9).Center().Background(surfaceBadge).Border(1.5, surface).
			Label(l.Remove()).Tooltip(l.Remove()).Scale(pop, pop).Opacity(pop)
		minus.Children(func() {
			ui.Icon(c, minusSymbol).Size(8, 8).TextColor(ink)
		})
		minus.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputPointerUp {
				if a.mutateToolsChanged(func(l *layout.Layout) bool {
					l.RemoveCard(cell.id)
					return true
				}) {
					p.press, p.menu, p.rs = nil, nil, nil
				}
			}
			return true // swallow the down too, so no card press starts
		})
		minus.PointerPosition()
		if len(cardSizes(cell.id)) < 2 {
			continue
		}
		p.toolResizeHandle(c, cell, spec, x, sy, pop)
	}
}

// toolResizeHandle builds the arc handle at the card's bottom-right. It is
// also called for a resizing card whose re-packed cell has left the visible
// rows: the pressed handle must stay in the tree to receive the release.
func (p *toolsPage) toolResizeHandle(c *ui.Context, cell toolCell, spec layout.Card, x, sy, pop float32) {
	l := loc(c)
	hx, hy := x+cell.w-5-18, sy+cell.h-5-18
	handle := ui.Box(c.Key("size-"+cell.id)).Absolute().Left(hx).Top(hy).Size(18, 18).
		Label(l.ToolsResize()).Tooltip(l.ToolsResize()).Scale(pop, pop).Opacity(pop)
	handle.Draw(func(pt *ui.Painter, r ui.Rect) {
		col := ui.RGBA(255, 255, 255, 0.62)
		if handle.Hovered() || (p.rs != nil && p.rs.id == cell.id) {
			col = ink
		}
		// the design's 18-unit arc: 3pt round stroke, white 62%
		var arc ui.Path
		arc.MoveTo(r.X+15, r.Y+4.5).LineTo(r.X+15, r.Y+6.5).
			QuadTo(r.X+15, r.Y+15, r.X+6.5, r.Y+15).LineTo(r.X+4.5, r.Y+15)
		pt.StrokePath(&arc, 3, col)
		var cap ui.Path
		cap.Circle(r.X+15, r.Y+4.5, 1.5)
		pt.FillPath(&cap, col)
		cap.Circle(r.X+4.5, r.Y+15, 1.5)
		pt.FillPath(&cap, col)
	})
	bx, by := handle.Bounds().X, handle.Bounds().Y
	handle.HandleInput(func(ev ui.InputEvent) bool {
		wx, wy := float32(bx)+ev.X, float32(by)+ev.Y
		switch ev.Kind {
		case ui.InputPointerDown:
			if ev.Button != 0 {
				return false
			}
			p.beginResize(cell.id, spec, cell, wx, wy)
			return true
		case ui.InputPointerMove:
			if p.rs != nil && p.rs.id == cell.id {
				p.moveResize(wx, wy)
				return true
			}
		case ui.InputPointerUp:
			if p.rs != nil && p.rs.id == cell.id {
				p.endResize()
				return true
			}
		}
		return false
	})
	handle.PointerPosition()
}

// cardSpec is the card's rectangle now: the layout's, or the transient
// resize rectangle while the handle drags.
func (p *toolsPage) cardSpec(cell toolCell) layout.Card {
	spec := layout.Card{ID: cell.id, Span: 1, Rows: 1}
	if def := toolCardDefFor(cell.id); def != nil {
		spec.Span, spec.Rows = def.span, def.rows
	}
	for _, cc := range p.host.toolsLayout().Cards {
		if cc.ID == cell.id {
			spec.Span, spec.Rows = cc.Cols(), cc.Height()
		}
	}
	if p.rs != nil && p.rs.id == cell.id {
		spec.Span, spec.Rows = p.rs.cur[0], p.rs.cur[1]
	}
	return spec
}

// toolsContentTop is the page content's y inside the panel shell: the
// band, the action row and the gap (the tab body has no top padding).
func toolsContentTop() float32 { return bandH + pageActionH + actionsGap }

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
