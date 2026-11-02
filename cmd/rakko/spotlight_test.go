//go:build darwin

package main

// The home bar's Spotlight search: debounce and cancellation, stale
// batches, the sheet's grouping and ordering, and when 「没有找到」 may
// appear.

import (
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/store"
)

// fakeSpot replaces the index: it records the terms asked for, keeps the
// callbacks so the test delivers batches, and counts cancellations.
type fakeSpot struct {
	mu      sync.Mutex
	terms   []string
	cancels int
	emit    func([]mac.SpotlightHit)
	done    func()
}

func withFakeSpot(t *testing.T) *fakeSpot {
	t.Helper()
	f := &fakeSpot{}
	old := spotlightStart
	spotlightStart = func(term string, _ int, emit func([]mac.SpotlightHit), done func()) func() {
		f.mu.Lock()
		f.terms = append(f.terms, term)
		f.emit, f.done = emit, done
		f.mu.Unlock()
		return func() {
			f.mu.Lock()
			f.cancels++
			f.mu.Unlock()
		}
	}
	t.Cleanup(func() { spotlightStart = old })
	return f
}

// withFakeDebounce replaces the 80 ms wait with a callback the test fires
// itself, so no query starts from another goroutine.
func withFakeDebounce(t *testing.T) *func() {
	t.Helper()
	var pending func()
	old := homeAfterFunc
	homeAfterFunc = func(_ time.Duration, fn func()) *time.Timer {
		pending = fn
		return time.NewTimer(time.Hour)
	}
	t.Cleanup(func() { homeAfterFunc = old })
	return &pending
}

// spotShelf is a shelf on the home level with a window-free view.
func spotShelf() *shelf {
	return &shelf{store: &store.Store{}, level: 1, visible: true}
}

// TestHomeSpotDebouncesAndCancels: typing twice inside the wait asks the
// index once, for the last text; a further change cancels that query.
func TestHomeSpotDebouncesAndCancels(t *testing.T) {
	f := withFakeSpot(t)
	pending := withFakeDebounce(t)
	a := spotShelf()

	a.homeQuery = "r"
	a.homeQueryChanged()
	a.homeQuery = "ra"
	a.homeQueryChanged()
	if (*pending) == nil {
		t.Fatal("the debounce was not scheduled")
	}
	(*pending)()

	if len(f.terms) != 1 || f.terms[0] != "ra" {
		t.Fatalf("index asked for %v, want one query for \"ra\"", f.terms)
	}
	if f.cancels != 0 {
		t.Fatalf("cancel called %d times before the query started", f.cancels)
	}

	a.homeQuery = "rak"
	a.homeQueryChanged()
	if f.cancels != 1 {
		t.Fatalf("cancel called %d times, want the in-flight query stopped", f.cancels)
	}
}

// TestHomeSpotDropsStaleBatches: a batch from a query the field has moved
// past never reaches the sheet.
func TestHomeSpotDropsStaleBatches(t *testing.T) {
	f := withFakeSpot(t)
	pending := withFakeDebounce(t)
	a := spotShelf()
	a.homeQuery = "rak"
	a.homeQueryChanged()
	(*pending)()
	emit, done := f.emit, f.done

	a.homeQuery = "rakko"
	a.homeQueryChanged()
	emit([]mac.SpotlightHit{{Path: "/tmp/old.txt", Name: "old.txt"}})
	done()
	if len(a.homeSpot.items) != 0 || a.homeSpot.done {
		t.Fatalf("a stale batch landed: %+v", a.homeSpot)
	}
}

// TestHomeSpotShowsFirstBatchBeforeTheEnd: results paint as they arrive;
// done only closes the query.
func TestHomeSpotShowsFirstBatchBeforeTheEnd(t *testing.T) {
	f := withFakeSpot(t)
	pending := withFakeDebounce(t)
	a := spotShelf()
	a.homeQuery = "rakko"
	a.homeQueryChanged()
	(*pending)()
	f.emit([]mac.SpotlightHit{{Path: "/Applications/Rakko.app", Name: "Rakko", App: true}})
	if len(a.homeSpot.items) != 1 || a.homeSpot.done {
		t.Fatalf("first batch not shown: %+v", a.homeSpot)
	}
	if !a.homeSearching() {
		t.Fatal("the query is still gathering")
	}
	f.done()
	if a.homeSearching() {
		t.Fatal("a finished query is not searching")
	}
}

// TestSpotGroupHitsRanksAndSplits: name prefix, then name contains, then a
// text-only hit; recent use breaks ties inside one rank.
func TestSpotGroupHitsRanksAndSplits(t *testing.T) {
	now := time.Now()
	items := []mac.SpotlightHit{
		{Path: "/Users/x/budget.xlsx", Name: "budget.xlsx", LastUsed: now},
		{Path: "/Users/x/my-rakko-notes.txt", Name: "my-rakko-notes.txt", LastUsed: now.Add(-time.Hour)},
		{Path: "/Users/x/rakko-old.txt", Name: "rakko-old.txt", LastUsed: now.Add(-2 * time.Hour)},
		{Path: "/Users/x/rakko-new.txt", Name: "rakko-new.txt", LastUsed: now},
		{Path: "/Applications/Rakko.app", Name: "Rakko", App: true},
		{Path: "/Users/x/Rakko Files", Name: "Rakko Files", Folder: true},
	}
	apps, folders, files := spotGroupHits("rakko", items, nil)
	if len(apps) != 1 || apps[0].Name != "Rakko" {
		t.Fatalf("apps = %+v", apps)
	}
	if len(folders) != 1 || folders[0].Name != "Rakko Files" {
		t.Fatalf("folders = %+v", folders)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	want := []string{"rakko-new.txt", "rakko-old.txt", "my-rakko-notes.txt", "budget.xlsx"}
	if len(names) != len(want) {
		t.Fatalf("files = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("files = %v, want %v", names, want)
		}
	}
}

// TestHomeHitsGroupsFitTheSheet: tray, clipboard, then the index's three
// groups, at most three rows each and never taller than the sheet.
func TestHomeHitsGroupsFitTheSheet(t *testing.T) {
	a := spotShelf()
	a.homeQuery = "rakko"
	a.homeSpot.query = "rakko"
	a.homeSpot.done = true
	a.homeSpot.items = []mac.SpotlightHit{
		{Path: "/Applications/Rakko.app", Name: "Rakko", App: true},
		{Path: "/Users/x/Rakko Files", Name: "Rakko Files", Folder: true},
		{Path: "/Users/x/rakko.txt", Name: "rakko.txt"},
	}
	a.store.Items = []store.Item{
		{ID: "t1", Kind: store.KindFile, Path: "/Users/x/rakko-tray.png"},
		{ID: "t2", Kind: store.KindFile, Path: "/Users/x/rakko-tray2.png"},
		{ID: "t3", Kind: store.KindFile, Path: "/Users/x/rakko-tray3.png"},
		{ID: "t4", Kind: store.KindFile, Path: "/Users/x/rakko-tray4.png"},
		{ID: "c1", Kind: store.KindText, Text: "rakko clipboard", Source: store.SourceClipboard},
		{ID: "c2", Kind: store.KindText, Text: "rakko clipboard 2", Source: store.SourceClipboard},
	}
	hits := a.homeHits()
	if len(hits) == 0 {
		t.Fatal("no rows")
	}
	order := map[string]int{
		a.appLoc().HomeGroupTray():      0,
		a.appLoc().HomeGroupClipboard(): 1,
		a.appLoc().HomeGroupApps():      2,
		a.appLoc().HomeGroupFolders():   3,
		a.appLoc().HomeGroupFiles():     4,
		a.appLoc().HomeGroupActions():   5,
	}
	last := -1
	perGroup := map[string]int{}
	for _, h := range hits {
		idx, ok := order[h.group]
		if !ok {
			t.Fatalf("unknown group %q", h.group)
		}
		if idx < last {
			t.Fatalf("group %q out of order: %+v", h.group, hits)
		}
		last = idx
		perGroup[h.group]++
		if perGroup[h.group] > homeGroupMax {
			t.Fatalf("group %q has %d rows", h.group, perGroup[h.group])
		}
	}
	if perGroup[a.appLoc().HomeGroupTray()] != homeGroupMax {
		t.Fatalf("tray rows = %d, want %d", perGroup[a.appLoc().HomeGroupTray()], homeGroupMax)
	}
	if raw := resultsRawHeight(hits); raw > resultsMaxH {
		t.Fatalf("sheet %v pt tall, max %d", raw, resultsMaxH)
	}
	if got := resultsHeight(hits); got > resultsMaxH {
		t.Fatalf("drawn sheet %v pt tall", got)
	}
	// The tray's files are not repeated by the index.
	for _, h := range hits {
		if h.path == "/Users/x/rakko-tray.png" && h.group == a.appLoc().HomeGroupFiles() {
			t.Fatal("a tray file showed again in the files group")
		}
	}
}

// TestNoMatchWaitsForTheQuery: while the index gathers, the sheet stays
// shut; only a finished query with no rows says 「没有找到」.
func TestNoMatchWaitsForTheQuery(t *testing.T) {
	a := spotShelf()
	a.homeQuery = "nothingmatches"
	a.homeSpot.query = "nothingmatches"
	if !a.homeSearching() {
		t.Fatal("a query that has not started is still searching")
	}
	if h := a.resultsTargetH(); h != 0 {
		t.Fatalf("sheet opened at %v pt while searching", h)
	}
	a.homeSpot.done = true
	if a.homeSearching() {
		t.Fatal("a finished query is not searching")
	}
	if h := a.resultsTargetH(); h != resultsHeight(nil) {
		t.Fatalf("sheet = %v pt, want the no-match row at %v", h, resultsHeight(nil))
	}
}

// TestSpotIconAsksTheMainThread: NSWorkspace's Finder icon may only be
// asked for on the main thread. The icon fetch goes through the main-thread
// seam; the PNG decode stays in the background goroutine.
func TestSpotIconAsksTheMainThread(t *testing.T) {
	oldIcon, oldRun := fileIcon, runOnMain
	t.Cleanup(func() { fileIcon, runOnMain = oldIcon, oldRun })
	asked := make(chan bool, 1)
	inRun := false
	runOnMain = func(fn func()) {
		inRun = true
		fn()
		inRun = false
	}
	fileIcon = func(string, int) []byte {
		asked <- inRun
		return nil
	}
	a := &shelf{win: &mygo.Window{}, spotPending: map[string]bool{}}
	if got := a.spotIcon("/tmp/rakko-icon-probe"); got != nil {
		t.Fatalf("the first paint returned an icon instead of the file glyph")
	}
	select {
	case onMainThread := <-asked:
		if !onMainThread {
			t.Fatal("the Finder icon was asked for off the main thread")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the icon was never requested")
	}
}
