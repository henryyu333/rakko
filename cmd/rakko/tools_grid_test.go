//go:build darwin

package main

import (
	"testing"

	"rakko/internal/layout"
)

func TestPackToolCardsSpans(t *testing.T) {
	cards := []layout.Card{
		{ID: "a", Span: 1}, {ID: "b", Span: 2}, {ID: "c", Span: 1},
		{ID: "d", Span: 2}, {ID: "e", Span: 2},
	}
	cells := packToolCards(cards, 4, nil)
	if len(cells) != 5 {
		t.Fatalf("cells = %d", len(cells))
	}
	// a(1) b(2) c(1) fills row 0; d(2)+e(2) fills row 1.
	colW := toolColW(1)
	spanW := toolColW(2)
	rowH := toolRowH(1)
	want := []toolCell{
		{id: "a", x: 0, y: 0, w: colW, h: rowH},
		{id: "b", x: colW + toolCellGap, y: 0, w: spanW, h: rowH},
		{id: "c", x: 3*colW + 3*toolCellGap, y: 0, w: colW, h: rowH},
		{id: "d", x: 0, y: toolCellH + toolCellGap, w: spanW, h: rowH},
		{id: "e", x: 2*colW + 2*toolCellGap, y: toolCellH + toolCellGap, w: spanW, h: rowH},
	}
	for i, cell := range cells {
		if cell != want[i] {
			t.Fatalf("cell %d = %+v, want %+v", i, cell, want[i])
		}
	}
}

// TestToolLadderMatchesDesign pins the six rungs of tools-v3 §2: the
// first ladder step that holds every card wins, then the panel is trimmed
// to the columns used (never under three). The row count follows the
// design's own layoutOf/packC, which is the behaviour spec.
func TestToolLadderMatchesDesign(t *testing.T) {
	card := func(id string, span, rows int) layout.Card {
		return layout.Card{ID: id, Span: span, Rows: rows}
	}
	join := func(groups ...[]layout.Card) []layout.Card {
		var out []layout.Card
		for _, g := range groups {
			out = append(out, g...)
		}
		return out
	}
	def := []layout.Card{
		card("playing", 2, 2), card("awake", 1, 1), card("usage", 1, 1),
		card("ocr", 1, 1), card("zip", 1, 1),
	}
	plus := join(def, []layout.Card{card("timer", 2, 1)})
	six := join(plus, []layout.Card{card("watch", 1, 1), card("clip", 2, 1), card("airdrop", 1, 1)})
	full := join(six, []layout.Card{
		card("recent", 2, 1), card("note", 2, 1), card("topdf", 1, 1),
		card("fit1m", 1, 1), card("shot", 1, 1), card("extract", 1, 1), card("resize", 1, 1),
	})
	cases := []struct {
		name  string
		cards []layout.Card
		cols  int
		rows  int
		vis   int
		w, h  float32
	}{
		{"3×1 two actions", []layout.Card{card("ocr", 1, 1), card("zip", 1, 1)}, 3, 1, 1, 334.5, 160},
		{"4×1 four small cards", []layout.Card{
			card("ocr", 1, 1), card("zip", 1, 1), card("awake", 1, 1), card("airdrop", 1, 1),
		}, 4, 1, 1, 440, 160},
		{"4×2 default five", def, 4, 2, 2, 440, 250},
		{"5×2 plus a 2×1 timer", plus, 5, 2, 2, 545.5, 250},
		{"6×3 nine cards", six, 6, 3, 3, 651, 340},
		{"6×3 scrolls past three rows", full, 6, 4, 3, 651, 340},
		// The winning rung can be wider than the cells use: the panel is
		// trimmed to the used columns, not the rung's.
		{"4×2 rung trimmed to 3 columns", []layout.Card{
			card("playing", 2, 2), card("ocr", 1, 1),
		}, 3, 2, 2, 334.5, 250},
		{"4×2 rung trimmed to one 2×2 card", []layout.Card{
			card("playing", 2, 2),
		}, 3, 2, 2, 334.5, 250},
		{"2×1 timer beside the player uses all four", []layout.Card{
			card("timer", 2, 1), card("playing", 2, 2),
		}, 4, 2, 2, 440, 250},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := toolGridFor(tc.cards, nil, 0)
			if g.cols != tc.cols || g.rows != tc.rows || g.vis != tc.vis {
				t.Fatalf("grid = %d cols, %d rows, %d vis; want %d, %d, %d",
					g.cols, g.rows, g.vis, tc.cols, tc.rows, tc.vis)
			}
			if got := toolPanelW(g.cols); got != tc.w {
				t.Fatalf("panel width = %v, want %v", got, tc.w)
			}
			if got := toolPanelH(g.vis); got != tc.h {
				t.Fatalf("panel height = %v, want %v", got, tc.h)
			}
			// Every card took a cell, and nothing sits past the panel's
			// right edge.
			if len(g.cells) != len(tc.cards) {
				t.Fatalf("%d cells for %d cards", len(g.cells), len(tc.cards))
			}
			right := float32(g.cols)*toolCellW + float32(g.cols-1)*toolCellGap
			for _, cell := range g.cells {
				if cell.x+cell.w > right+0.01 {
					t.Fatalf("cell %+v overflows the %vpt stage", cell, right)
				}
			}
		})
	}
}

// TestToolGridMinCols: the band's reserved columns win over the cards'
// own width, and the extra columns stay empty.
func TestToolGridMinCols(t *testing.T) {
	cards := []layout.Card{{ID: "a"}, {ID: "b"}}
	g := toolGridFor(cards, nil, 4)
	if g.cols != 4 || g.rows != 1 || g.vis != 1 {
		t.Fatalf("minCols 4 grid = %+v, want 4 columns one row", g)
	}
	if got := toolPanelW(g.cols); got != 440 {
		t.Fatalf("panel width = %v, want 440", got)
	}
	// Trimming never goes under three even with a single card.
	g = toolGridFor(cards[:1], nil, 0)
	if g.cols != toolMinCols {
		t.Fatalf("single card grid = %d columns, want the 3-column floor", g.cols)
	}
}

func TestToolVirtualCards(t *testing.T) {
	cards := []layout.Card{{ID: "a"}, {ID: "b", Span: 2}, {ID: "c"}}
	got := toolVirtualCards(cards, "a", 2)
	ids := []string{got[0].ID, got[1].ID, got[2].ID}
	if ids[0] != "b" || ids[1] != "c" || ids[2] != "a" {
		t.Fatalf("virtual order = %v", ids)
	}
	if got[0].Span != 2 {
		t.Fatal("virtual move lost the span")
	}
	// Unknown id leaves the list alone.
	same := toolVirtualCards(cards, "zz", 0)
	if len(same) != len(cards) {
		t.Fatal("unknown id changed the list")
	}
}

func TestToolInsertIndex(t *testing.T) {
	cards := []layout.Card{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	colW, rowH := toolColW(1), float32(toolCellH)
	// Pointer over cell 2's centre while dragging "a": index 2 — the
	// virtual order is b c a d e, and a lands where c was.
	if got := toolInsertIndex(cards, 4, "a", -1, 2*colW+2*toolCellGap+colW/2, rowH/2); got != 2 {
		t.Fatalf("insert = %d, want 2", got)
	}
	// Over the first cell: 0.
	if got := toolInsertIndex(cards, 4, "a", -1, colW/2, rowH/2); got != 0 {
		t.Fatalf("insert = %d, want 0", got)
	}
	// Past the last cell of a full row: 4 (before e).
	if got := toolInsertIndex(cards, 4, "a", -1, 3*colW+3*toolCellGap+colW-1, rowH/2); got != 3 && got != 4 {
		t.Fatalf("row-end insert = %d", got)
	}
	// Unknown card: -1.
	if got := toolInsertIndex(cards, 4, "zz", -1, 0, 0); got != -1 {
		t.Fatalf("unknown card insert = %d", got)
	}
}

func TestToolInsertIndexSpans(t *testing.T) {
	// Dragging the 2-span "b" over the row's second half should land it
	// after "a" rather than overlapping columns.
	cards := []layout.Card{{ID: "a"}, {ID: "b", Span: 2}, {ID: "c"}, {ID: "d"}}
	colW := toolColW(1)
	if got := toolInsertIndex(cards, 4, "b", -1, 2*colW+2*toolCellGap+colW/2, toolCellH/2); got < 1 || got > 3 {
		t.Fatalf("span insert = %d", got)
	}
}

// TestDefaultLayoutFitsTwoRows: a fresh install's grid is exactly the
// page's two rows (4 × 2 = 174pt) with no scrolling, for either AI usage
// setting, and the right half holds 保持唤醒 over 框选文字 / 打成 zip.
func TestDefaultLayoutFitsTwoRows(t *testing.T) {
	viewport := float32(2*toolCellH + toolCellGap)
	for _, aiUsage := range []bool{true, false} {
		cells := packToolCards(layout.DefaultWith(aiUsage).Cards, 4, nil)
		if len(cells) != 5 {
			t.Fatalf("aiUsage %v: cells = %d, want 5", aiUsage, len(cells))
		}
		var bottom float32
		for _, cell := range cells {
			bottom = max(bottom, cell.y+cell.h)
		}
		if bottom != viewport {
			t.Fatalf("aiUsage %v: grid bottom = %v, want the %v viewport", aiUsage, bottom, viewport)
		}
		fifth := map[string]toolCell{}
		for _, cell := range cells {
			fifth[cell.id] = cell
		}
		corner, hasUsage, hasFit := fifth["usage"], fifth["usage"].id == "usage", fifth["fit1m"].id == "fit1m"
		if aiUsage && !hasUsage {
			t.Fatalf("aiUsage on: usage missing from the fresh grid: %+v", cells)
		}
		if !aiUsage {
			if hasUsage || !hasFit {
				t.Fatalf("aiUsage off: want fit1m in usage's cell, got %+v", cells)
			}
			corner = fifth["fit1m"]
		}
		if corner.y != 0 || corner.x < 2*(toolColW(1)+toolCellGap) {
			t.Fatalf("aiUsage %v: corner card at %+v, want the right half of row 0", aiUsage, corner)
		}
		for _, id := range []string{"ocr", "zip"} {
			if c := fifth[id]; c.y != toolCellH+toolCellGap || c.x < 2*(toolColW(1)+toolCellGap) {
				t.Fatalf("aiUsage %v: %s at %+v, want the right half of row 1", aiUsage, id, c)
			}
		}
	}
}

// TestToolsGridHoldsTallWidgets: the music card is 2 × 2 in the grid and
// the page scrolls past the two visible rows.
func TestToolsGridHoldsTallWidgets(t *testing.T) {
	l := mustLayout(t)
	withNil := func(string) bool { return false }
	cells := packToolCards(l.Cards, 4, withNil)
	var playing, awake toolCell
	for _, c := range cells {
		switch c.id {
		case "playing":
			playing = c
		case "awake":
			awake = c
		}
	}
	if playing.h != toolRowH(2) || playing.w != toolColW(2) {
		t.Fatalf("playing cell = %+v, want 2x2", playing)
	}
	if awake.x != 2*(toolColW(1)+toolCellGap) || awake.y != 0 {
		t.Fatalf("awake cell = %+v, want column 2 of row 0", awake)
	}
	if playing.y+playing.h <= 0 {
		t.Fatal("the playing cell has no height")
	}
}

// mustLayout is the default layout of a shelf with no data directory.
func mustLayout(t *testing.T) layout.Layout {
	t.Helper()
	a := &shelf{dataDir: t.TempDir()}
	return a.toolsLayout()
}
