//go:build darwin

package main

// Layout of the home search sheet: its rows carry the hit's category on
// the right, a second line under the name, and the actions sit in a bar
// inside the sheet's bottom edge rather than a capsule over its corner.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
)

// resultsShelf is a home shelf whose query finds a tray file, a clipboard
// text and two Spotlight rows, with the index already answered.
func resultsShelf() *shelf {
	s := &store.Store{Items: []store.Item{
		{ID: "t1", Kind: store.KindFile, Path: "/Users/x/Pictures/find.png", CreatedAt: time.Now().Add(-8 * time.Minute)},
		{ID: "c1", Kind: store.KindText, Text: "find the notes", Source: store.SourceClipboard,
			SourceApp: "com.example.Editor", CreatedAt: time.Now().Add(-2 * time.Hour)},
	}}
	a := &shelf{store: s, level: 1, visible: true, homeQuery: "find",
		appNames: map[string]string{"com.example.Editor": "Editor"}}
	a.homeSpot.query = "find"
	a.homeSpot.done = true
	a.homeSpot.items = []mac.SpotlightHit{
		{Path: "/Applications/Find.app", Name: "Find", App: true},
		{Path: "/Users/x/Documents/find", Name: "find", Folder: true},
	}
	return a
}

func resultsView(t *testing.T, a *shelf) *ui.Tester {
	t.Helper()
	h := float32(bandH+homeBarH) + resultsGap + resultsHeight(a.homeHits()) + navGap + navH + 2
	v := ui.NewTester(a.view, homeTypingW, int(h))
	v.SetLocale("zh-Hans")
	v.Frame()
	v.Frame()
	return v
}

// TestResultsRowsSecondLineAndCategory: every row ends at the same right
// edge with its group's name, and the line under a hit is its source —
// the folder for a file, app and time for the clipboard, the time for a
// tray row.
func TestResultsRowsSecondLineAndCategory(t *testing.T) {
	a := resultsShelf()
	hits := a.homeHits()
	if len(hits) != 4 {
		t.Fatalf("hits = %+v", hits)
	}
	l := a.appLoc()
	if hits[0].group != l.HomeGroupTray() || hits[0].sub != relativeTime(l, a.store.Items[0].CreatedAt, time.Now()) {
		t.Fatalf("tray row = %+v, want the time under it", hits[0])
	}
	if hits[1].group != l.HomeGroupClipboard() || hits[1].sub != l.ClipMeta("Editor", relativeTime(l, a.store.Items[1].CreatedAt, time.Now())) {
		t.Fatalf("clipboard row = %+v, want 来源应用 · 时间", hits[1])
	}
	if hits[2].sub != filepath.Dir("/Applications/Find.app") {
		t.Fatalf("app row sub = %q", hits[2].sub)
	}
	if hits[3].sub != filepath.Dir("/Users/x/Documents/find") {
		t.Fatalf("folder row sub = %q", hits[3].sub)
	}

	v := resultsView(t, a)
	for _, hit := range hits {
		if !v.HasText(hit.group) {
			t.Fatalf("the row lost its category %q: %v", hit.group, v.Texts())
		}
		if hit.sub != "" && !v.HasText(hit.sub) {
			t.Fatalf("the row lost its second line %q: %v", hit.sub, v.Texts())
		}
	}
	// Rows share width and inset, so the category each ends with lands on
	// the same right line.
	var edge float32
	for i, hit := range hits {
		r, ok := v.Find(hit.title)
		if !ok {
			t.Fatalf("row %d %q has no box", i, hit.title)
		}
		if i > 0 {
			prev, _ := v.Find(hits[i-1].title)
			if r.X != prev.X || r.W != prev.W {
				t.Fatalf("row %d is %+v, row %d %+v: rows do not align", i, r, i-1, prev)
			}
		} else {
			edge = r.X + r.W
		}
		if r.X < resultsPad || r.X+r.W > homeTypingW-resultsPad {
			t.Fatalf("row %d %+v leaves the sheet's padding", i, r)
		}
		if got := r.X + r.W; got != edge {
			t.Fatalf("row %d ends at %v, row 0 at %v", i, got, edge)
		}
	}
}

// TestResultsActionBarInsideTheSheet: the 打开 hint is the sheet's last
// row — inside its corners, under the results, not over them — and the
// reveal hint joins it only for a file, folder or app.
func TestResultsActionBarInsideTheSheet(t *testing.T) {
	a := resultsShelf()
	v := resultsView(t, a)
	lit := func(sel int) (ui.Rect, ui.Rect) {
		a.homeSel = sel
		v.Frame()
		open, ok := v.Find(a.appLoc().ItemOpen())
		if !ok {
			t.Fatalf("no 打开 hint with selection %d: %v", sel, v.Texts())
		}
		last, _ := v.Find(a.homeHits()[len(a.homeHits())-1].title)
		return open, last
	}
	open, last := lit(3)
	if open.Y < last.Y+last.H {
		t.Fatalf("the action bar (y %v) overlaps the last row (bottom %v)", open.Y, last.Y+last.H)
	}
	if open.X+open.W > homeTypingW-resultsPad {
		t.Fatalf("the action bar ends at %v, outside the sheet's %v", open.X+open.W, homeTypingW-resultsPad)
	}
	// The sheet is resultsGap below the bar's foot; the hint must sit
	// above that foot inside the pane.
	sheetTop := float32(bandH + homeBarH + resultsGap)
	sheetBottom := sheetTop + resultsHeight(a.homeHits())
	if open.Y+open.H > sheetBottom-resultsPad {
		t.Fatalf("the action bar's foot %v passes the sheet's padding at %v", open.Y+open.H, sheetBottom-resultsPad)
	}
	if !v.HasText(a.appLoc().ItemShowInFinder()) {
		t.Fatalf("a folder selection lost 在 Finder 中显示: %v", v.Texts())
	}
	// The clipboard row cannot show in Finder: only 打开 is left.
	lit(1)
	if v.HasText(a.appLoc().ItemShowInFinder()) {
		t.Fatalf("a clipboard selection offers 在 Finder 中显示: %v", v.Texts())
	}
}
