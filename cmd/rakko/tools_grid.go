//go:build darwin

package main

// Pure grid math for the tools page: pack the card list into a variable
// number of columns, choose the panel size from the packing, find the
// insertion index a dragged card's pointer means, and build the virtual
// list a drop previews. Split from tools_page.go so tests can exercise it
// without a view.
//
// Cards carry a column span and a row span: action cards take one cell,
// the widgets keep the rectangles they had on the old home canvas (the
// music player is 2 × 2). Packing is row-major first-fit over a grid that
// grows downward.
//
// The panel hugs its cards (design/explore/tools-v3.html §2): the first
// rung of the ladder below the cards fit in wins, then the packing is
// trimmed to the columns it used, never under three. Past three rows the
// panel keeps three and the page scrolls.

import (
	"math"

	"rakko/internal/layout"
)

const (
	toolCellW   = 99.5 // one cell, the design's CELL_W
	toolCellH   = 84
	toolCellGap = 6

	toolMinCols = 3 // the band keeps room for chips on both sides
	toolMaxCols = 6
	toolMaxRows = 3 // rows drawn; more scroll
)

// toolLadder is the order widths are tried in: grow sideways first, then
// down. Each rung carries the widest row count it may use.
var toolLadder = [][2]int{
	{3, 1}, {4, 1}, {4, 2}, {5, 2}, {6, 2}, {6, 3},
}

// toolColW is a card's pixel width for its column span.
func toolColW(span int) float32 {
	span = max(1, min(span, toolMaxCols))
	return float32(span)*toolCellW + float32(span-1)*toolCellGap
}

// toolRowH is a card's pixel height for its row span.
func toolRowH(rows int) float32 {
	rows = max(1, min(rows, 2))
	return float32(rows)*toolCellH + float32(rows-1)*toolCellGap
}

// toolPanelW is the shell width a grid of cols columns needs: the 12pt
// padding on each side plus the cells and their gaps.
func toolPanelW(cols int) float32 {
	cols = max(toolMinCols, min(cols, toolMaxCols))
	return float32(2*sheetPadX) + float32(cols)*toolCellW + float32(cols-1)*toolCellGap
}

// toolPanelH is the shell height a page showing rows rows needs: the band,
// the gap, the action row, its gap, the cells and the 12pt bottom pad.
func toolPanelH(rows int) float32 {
	rows = max(1, min(rows, toolMaxRows))
	return float32(bandH+bodyGap+pageActionH+pageActionsGap+padBottom) +
		float32(rows)*toolCellH + float32(rows-1)*toolCellGap
}

// toolGrid is one packing: the cards' cells, the columns the panel shows,
// and the rows it is sized for.
type toolGrid struct {
	cells []toolCell
	cols  int
	rows  int // rows the cards need, at least 1
	vis   int // rows drawn: rows, capped at three
}

// packToolCards lays cards into cols columns, first fit in row-major
// order: each card takes the first w × h block of free cells, scanning
// rows top to bottom. skip names cards that take no space right now (a
// widget whose feature is off): their cells read as free, their entries
// stay. editing appends the dashed + slot.
func packToolCards(cards []layout.Card, cols int, skip func(string) bool) []toolCell {
	cols = max(toolMinCols, min(cols, toolMaxCols))
	taken := map[[2]int]bool{}
	var cells []toolCell
	place := func(id string, span, rows int) {
		span, rows = max(1, min(span, cols)), max(1, min(rows, 2))
		for y := 0; ; y++ {
			for x := 0; x+span <= cols; x++ {
				free := true
				for dy := range rows {
					for dx := range span {
						if taken[[2]int{x + dx, y + dy}] {
							free = false
						}
					}
				}
				if !free {
					continue
				}
				for dy := range rows {
					for dx := range span {
						taken[[2]int{x + dx, y + dy}] = true
					}
				}
				cells = append(cells, toolCell{
					id: id,
					x:  float32(x) * (toolCellW + toolCellGap),
					y:  float32(y) * (toolCellH + toolCellGap),
					w:  toolColW(span), h: toolRowH(rows),
				})
				return
			}
		}
	}
	for _, card := range cards {
		if skip != nil && skip(card.ID) {
			continue
		}
		place(card.ID, card.Cols(), card.Height())
	}
	return cells
}

// toolGridAt packs cards into exactly cols columns and reports the rows
// the packing used and the rows actually drawn. The caller decides when
// to trim the shell to the columns that packing touched (toolGridFor); a
// locked reorder drag keeps cols as given.
func toolGridAt(cards []layout.Card, cols int, skip func(string) bool) toolGrid {
	cols = max(toolMinCols, min(cols, toolMaxCols))
	cells := packToolCards(cards, cols, skip)
	rows := 1
	for _, cell := range cells {
		rows = max(rows, int(math.Round(float64(cell.y+cell.h+toolCellGap)/float64(toolCellH+toolCellGap))))
	}
	return toolGrid{cells: cells, cols: cols, rows: rows, vis: min(rows, toolMaxRows)}
}

// toolGridUsedCols is the number of columns the packed cells touch.
func toolGridUsedCols(cells []toolCell) int {
	used := 0
	pitch := toolCellW + toolCellGap
	for _, cell := range cells {
		used = max(used, int(math.Round(float64(cell.x+cell.w+toolCellGap)/float64(pitch))))
	}
	return used
}

// toolGridFor picks the panel's box for cards: the first ladder rung that
// holds every card and is at least minCols wide, packed at that rung and
// then trimmed to the columns the cells touch (never under three). minCols
// is the band's requirement and always wins the trim. With nothing left to
// pack the smallest panel stays. Past the last rung the six-column grid
// shows three rows and scrolls.
func toolGridFor(cards []layout.Card, skip func(string) bool, minCols int) toolGrid {
	minCols = max(toolMinCols, min(minCols, toolMaxCols))
	finish := func(g toolGrid) toolGrid {
		g.cols = max(minCols, max(toolMinCols, toolGridUsedCols(g.cells)))
		return g
	}
	for _, rung := range toolLadder {
		if rung[0] < minCols {
			continue
		}
		g := toolGridAt(cards, rung[0], skip)
		if g.rows <= rung[1] {
			return finish(g)
		}
	}
	return finish(toolGridAt(cards, toolMaxCols, skip))
}

// toolVirtualCards is the list the grid would have if the dragged card
// dropped at index now: cards minus it, with it re-inserted there. The
// parked copy keeps its own size.
func toolVirtualCards(cards []layout.Card, dragID string, index int) []layout.Card {
	var (
		rest  []layout.Card
		drag  layout.Card
		found bool
	)
	for _, card := range cards {
		if card.ID == dragID {
			drag, found = card, true
			continue
		}
		rest = append(rest, card)
	}
	if !found {
		return cards
	}
	index = max(0, min(index, len(rest)))
	out := make([]layout.Card, 0, len(rest)+1)
	out = append(out, rest[:index]...)
	out = append(out, drag)
	return append(out, rest[index:]...)
}

// toolInsertIndex is where the pointer means to drop the dragged card:
// the index inside cards (without it) whose packed cell's centre is
// nearest, in row-major scan order, packed at cols columns. It returns -1
// when the drag id is unknown. px, py are stage content coordinates.
//
// current is the index last reported; it wins ties so the target does
// not flicker between two cells while the pointer hovers a boundary.
func toolInsertIndex(cards []layout.Card, cols int, dragID string, current int, px, py float32) int {
	drag := layout.Card{ID: dragID}
	for _, card := range cards {
		if card.ID == dragID {
			drag = card
		}
	}
	if drag.ID == "" {
		return -1
	}
	cols = max(toolMinCols, min(cols, toolMaxCols))
	best, bestD := -1, float64(math.MaxFloat64)
	for i := 0; i <= len(cards)-1; i++ {
		virtual := toolVirtualCards(cards, dragID, i)
		for _, cell := range packToolCards(virtual, cols, nil) {
			if cell.id != dragID {
				continue
			}
			dx := px - (cell.x + cell.w/2)
			dy := py - (cell.y + cell.h/2)
			d := float64(dx*dx + dy*dy)
			if d < bestD || (d == bestD && i == current) {
				best, bestD = i, d
			}
		}
	}
	return best
}

// toolInsertIndexFresh is toolInsertIndex for a card that is not in the
// list yet (dragged in from the library): the candidate list is the cards
// with id inserted at each index.
func toolInsertIndexFresh(cards []layout.Card, cols int, spec layout.Card, current int, px, py float32) int {
	if spec.ID == "" {
		return -1
	}
	cols = max(toolMinCols, min(cols, toolMaxCols))
	best, bestD := -1, float64(math.MaxFloat64)
	for i := 0; i <= len(cards); i++ {
		v := make([]layout.Card, 0, len(cards)+1)
		v = append(v, cards[:i]...)
		v = append(v, spec)
		v = append(v, cards[i:]...)
		for _, cell := range packToolCards(v, cols, nil) {
			if cell.id != spec.ID {
				continue
			}
			dx := px - (cell.x + cell.w/2)
			dy := py - (cell.y + cell.h/2)
			d := float64(dx*dx + dy*dy)
			if d < bestD || (d == bestD && i == current) {
				best, bestD = i, d
			}
		}
	}
	return best
}
