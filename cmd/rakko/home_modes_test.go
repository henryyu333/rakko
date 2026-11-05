//go:build darwin

package main

// The home bar's three modes: 全部 is the default the panel always opens
// on, 应用 and 文件 narrow both the groups the sheet shows and the query the
// index is asked; Tab cycles them only while the field is empty, and the
// mode is never saved.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/store"
)

// modeShelf is a home bar with the field focused, a tray file, a clipboard
// text and an index answering one app, one folder and one file.
func modeShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	a := &shelf{
		store: &store.Store{Items: []store.Item{
			{ID: "t1", Kind: store.KindFile, Path: "/Users/x/find.png", CreatedAt: time.Now()},
			{ID: "c1", Kind: store.KindText, Text: "find the notes", Source: store.SourceClipboard},
		}},
		level: 1, visible: true, focusMode: true,
		homeQuery: "find", appNames: map[string]string{},
	}
	a.homeSpot.query, a.homeSpot.done = "find", true
	// Four groups fit the sheet's 330 pt; the folder joins below where a
	// test needs it alone.
	a.homeSpot.items = []mac.SpotlightHit{
		{Path: "/Applications/Find.app", Name: "Find", App: true},
		{Path: "/Users/x/Documents/find.txt", Name: "find.txt"},
	}
	h := float32(bandH+homeBarH) + resultsGap + resultsMaxH + navGap + navH + 8
	v := ui.NewTester(a.view, homeTypingW, int(h))
	v.SetLocale("zh-Hans")
	v.Frame()
	v.Frame()
	return a, v
}

// setMode is what a mode switch must leave the shelf in without asking the
// index: the hits answer the mode they were gathered for.
func setMode(a *shelf, scope mac.SearchScope) {
	a.homeScope, a.homeSpot.scope = scope, scope
}

// groups lists the groups of hits in order.
func groups(hits []homeHit) []string {
	var out []string
	for _, h := range hits {
		if len(out) == 0 || out[len(out)-1] != h.group {
			out = append(out, h.group)
		}
	}
	return out
}

// TestHomeModesShowTheirOwnGroups: 应用 is the apps group alone; 文件 keeps
// the tray, the clipboard, the notes, the folders and the files; 全部 keeps
// everything.
func TestHomeModesShowTheirOwnGroups(t *testing.T) {
	a, _ := modeShelf(t)
	l := a.appLoc()

	all := groups(a.homeHits())
	for _, want := range []string{l.HomeGroupTray(), l.HomeGroupClipboard(), l.HomeGroupApps(), l.HomeGroupFiles()} {
		if !contains(all, want) {
			t.Fatalf("全部 shows %v, want %q", all, want)
		}
	}

	setMode(a, mac.ScopeApps)
	apps := groups(a.homeHits())
	if !contains(apps, l.HomeGroupApps()) {
		t.Fatalf("应用 shows %v, want the apps group", apps)
	}
	for _, g := range apps {
		if g != l.HomeGroupApps() {
			t.Fatalf("应用 shows %q, want the apps group alone: %v", g, apps)
		}
	}

	setMode(a, mac.ScopeFiles)
	files := groups(a.homeHits())
	if !contains(files, l.HomeGroupTray()) || !contains(files, l.HomeGroupClipboard()) ||
		!contains(files, l.HomeGroupFiles()) {
		t.Fatalf("文件 shows %v, want tray, clipboard and files", files)
	}
	for _, g := range files {
		if g == l.HomeGroupApps() || g == l.HomeGroupActions() {
			t.Fatalf("文件 shows %q: %v", g, files)
		}
	}

	// A folder is a file result too.
	a.homeSpot.items = []mac.SpotlightHit{{Path: "/Users/x/Documents/find", Name: "find", Folder: true}}
	if gs := groups(a.homeHits()); !contains(gs, l.HomeGroupFolders()) {
		t.Fatalf("文件 shows %v, want the folders group", gs)
	}
	setMode(a, mac.ScopeApps)
	if gs := groups(a.homeHits()); contains(gs, l.HomeGroupFolders()) {
		t.Fatalf("应用 shows %v, want no folders", gs)
	}
}

// TestHomeModesKeepTheirActions: the actions group belongs to 全部 only.
func TestHomeModesKeepTheirActions(t *testing.T) {
	a, _ := modeShelf(t)
	l := a.appLoc()
	a.homeQuery = strings.ToLower(l.PanelSettings())
	a.homeSpot.query = a.homeQuery

	setMode(a, mac.ScopeAll)
	if gs := groups(a.homeHits()); !contains(gs, l.HomeGroupActions()) {
		t.Fatalf("全部 shows %v, want the actions group", gs)
	}
	for _, scope := range []mac.SearchScope{mac.ScopeApps, mac.ScopeFiles} {
		setMode(a, scope)
		if gs := groups(a.homeHits()); contains(gs, l.HomeGroupActions()) {
			t.Fatalf("scope %d shows the actions group: %v", scope, gs)
		}
	}
}

// TestHomeTabCyclesModesWhenTheBarIsEmpty: Tab walks 全部 → 应用 → 文件 →
// 全部 and stays in the field.
func TestHomeTabCyclesModesWhenTheBarIsEmpty(t *testing.T) {
	a, v := modeShelf(t)
	a.homeQuery = ""
	a.homeSpot.query, a.homeSpot.items, a.homeSpot.done = "", nil, false
	v.Frame()

	for _, want := range []mac.SearchScope{mac.ScopeApps, mac.ScopeFiles, mac.ScopeAll} {
		v.Key(0, ui.KeyTab)
		v.Frame()
		if a.homeScope != want {
			t.Fatalf("Tab left the bar in scope %d, want %d", a.homeScope, want)
		}
		if !v.Focused(a.appLoc().HomeSearch()) {
			t.Fatalf("Tab moved the keyboard out of the search field")
		}
	}
}

// TestHomeTabKeepsItsFocusRoleWithATerm: a typed term returns Tab to the
// focus key it is everywhere else.
func TestHomeTabKeepsItsFocusRoleWithATerm(t *testing.T) {
	a, v := modeShelf(t)
	if !v.Focused(a.appLoc().HomeSearch()) {
		t.Fatal("the search field did not take the keyboard")
	}
	v.Key(0, ui.KeyTab)
	v.Frame()
	if a.homeScope != mac.ScopeAll {
		t.Fatalf("Tab switched to scope %d with \"%s\" typed", a.homeScope, a.homeQuery)
	}
	// The bar takes the keyboard back on every frame while it is open, so
	// the focus does not prove the key went through: the empty-bar case
	// above proves OnTextKey sees Tab, and the mode is what Tab may not
	// touch here.
	if a.homeQuery == "" {
		t.Fatal("the case needs a typed term")
	}
}

// TestHomeModeSwitchAsksAgainAtOnce: changing the mode re-runs the term
// already in the field against the new scope, without a debounce, and drops
// what the old scope's query would still deliver.
func TestHomeModeSwitchAsksAgainAtOnce(t *testing.T) {
	f := withFakeSpot(t)
	pending := withFakeDebounce(t)
	a := spotShelf()
	a.homeQuery = "rakko"
	a.homeQueryChanged()
	(*pending)()
	if len(f.terms) != 1 || f.scopes[0] != mac.ScopeAll {
		t.Fatalf("first query = %v in %v", f.terms, f.scopes)
	}

	a.cycleHomeScope()
	if len(f.terms) != 2 || f.terms[1] != "rakko" || f.scopes[1] != mac.ScopeApps {
		t.Fatalf("应用 asked %v / %v, want \"rakko\" in the app scope", f.terms, f.scopes)
	}
	if f.cancels != 1 {
		t.Fatalf("the 全部 query was cancelled %d times, want once", f.cancels)
	}

	stale := f.emit
	a.cycleHomeScope()
	if len(f.terms) != 3 || f.terms[2] != "rakko" || f.scopes[2] != mac.ScopeFiles {
		t.Fatalf("文件 asked %v / %v, want \"rakko\" in the file scope", f.terms, f.scopes)
	}
	stale([]mac.SpotlightHit{{Path: "/Applications/Rakko.app", Name: "Rakko", App: true}})
	if len(a.homeSpot.items) != 0 {
		t.Fatalf("a batch from the old mode landed: %+v", a.homeSpot.items)
	}

	// A hit the sheet does not belong to this mode never shows, even when
	// the index sent it.
	a.homeSpot.items = []mac.SpotlightHit{{Path: "/Applications/Rakko.app", Name: "Rakko", App: true}}
	if got := a.homeHits(); len(got) != 0 {
		t.Fatalf("文件 shows an app hit: %+v", got)
	}
}

// TestHomeModeResetsWhenThePanelCloses: a mode belongs to one search; the
// next opening always starts on 全部, with no query and no saved setting.
func TestHomeModeResetsWhenThePanelCloses(t *testing.T) {
	a := &shelf{store: &store.Store{}}
	a.show(false)
	a.homeQuery, a.homeScope = "rakko", mac.ScopeFiles
	a.hide(hideExplicit)
	a.finishClose()
	if a.homeScope != mac.ScopeAll || a.homeQuery != "" {
		t.Fatalf("the closed panel kept %d / %q", a.homeScope, a.homeQuery)
	}

	// Reopening after a close in flight goes through the same reset.
	a.homeScope = mac.ScopeApps
	a.show(false)
	if a.homeScope != mac.ScopeAll {
		t.Fatalf("the reopened bar starts on scope %d", a.homeScope)
	}
	if a.goHome(); a.homeScope != mac.ScopeAll {
		t.Fatalf("going home left the bar in scope %d", a.homeScope)
	}
}

// TestHomeModeCapsuleVisibleOnlyOffAll: 全部 paints no capsule; 应用 and
// 文件 do, and the empty bar carries the Tab hint inside it.
func TestHomeModeCapsuleVisibleOnlyOffAll(t *testing.T) {
	a, v := modeShelf(t)
	l := a.appLoc()
	a.homeQuery, a.homeSpot.query, a.homeSpot.items, a.homeSpot.done = "", "", nil, false
	v.Frame()
	if shows(v, l.SearchModeApps()) || shows(v, l.SearchModeFiles()) {
		t.Fatalf("全部 painted a mode capsule: %v", v.Texts())
	}

	a.homeScope = mac.ScopeApps
	v.Frame()
	if !shows(v, l.SearchModeApps()) {
		t.Fatalf("应用 has no capsule: %v", v.Texts())
	}
	if !shows(v, "Tab") {
		t.Fatalf("the empty bar lost its Tab hint: %v", v.Texts())
	}

	// The hint belongs to the empty bar: typed text hides it.
	a.homeQuery, a.homeSpot.query = "find", "find"
	a.homeSpot.done = true
	v.Frame()
	if !shows(v, l.SearchModeApps()) {
		t.Fatalf("the capsule went away with a term: %v", v.Texts())
	}
	if shows(v, "Tab") {
		t.Fatalf("the Tab hint stayed with a term: %v", v.Texts())
	}

	a.homeScope = mac.ScopeFiles
	a.homeQuery, a.homeSpot.query, a.homeSpot.items, a.homeSpot.done = "", "", nil, false
	v.Frame()
	if !shows(v, l.SearchModeFiles()) {
		t.Fatalf("文件 has no capsule: %v", v.Texts())
	}
}

// TestHomeModeCapsuleCyclesOnClick: clicking the capsule is the same walk
// Tab does, and it leaves the keyboard in the field.
func TestHomeModeCapsuleCyclesOnClick(t *testing.T) {
	a, v := modeShelf(t)
	l := a.appLoc()
	a.homeScope = mac.ScopeApps
	a.homeQuery, a.homeSpot.query, a.homeSpot.items, a.homeSpot.done = "", "", nil, false
	v.Frame()
	if err := v.Click(l.SearchModeApps()); err != nil {
		t.Fatalf("the capsule did not take the click: %v", err)
	}
	v.Frame()
	if a.homeScope != mac.ScopeFiles {
		t.Fatalf("the click left the bar in scope %d, want 文件", a.homeScope)
	}
	if !v.Focused(l.HomeSearch()) {
		t.Fatal("the capsule took the keyboard from the field")
	}
}

// shows reports whether the frame draws exactly s, as the capsule's mode
// name is drawn: HasText would also match 应用切换 in the page capsule.
func shows(v *ui.Tester, s string) bool {
	_, ok := v.Find(s)
	return ok
}

// contains reports whether list holds want.
func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
