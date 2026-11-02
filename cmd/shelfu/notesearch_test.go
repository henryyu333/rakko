//go:build darwin

package main

// The home search's 便利贴 group: where it sits, what it ranks, that the
// index is never re-read while typing, what ⏎ does with a row, and the
// panel it draws.

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/note"
	"shelf/internal/store"
)

// noteID returns a note file name of the package's shape.
func noteID(c byte) string { return strings.Repeat(string(c), 32) }

// notesHomeShelf is a shelf on the home level whose data directory holds
// the notes given, id → text, each written at its entry's stamp.
func notesHomeShelf(t *testing.T, notes map[string]string, stamps map[string]time.Time) *shelf {
	t.Helper()
	dir := t.TempDir()
	folder := filepath.Join(dir, note.DirName)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	for id, text := range notes {
		path := filepath.Join(folder, id+note.FileExt)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if at, ok := stamps[id]; ok {
			if err := os.Chtimes(path, at, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	return &shelf{dataDir: dir, store: &store.Store{}}
}

// groupSpan is where a group's rows start and how many there are.
func groupSpan(hits []homeHit, group string) (int, int) {
	first, n := -1, 0
	for i, h := range hits {
		if h.group != group {
			continue
		}
		if first < 0 {
			first = i
		}
		n++
	}
	return first, n
}

// TestHomeFindsNotesByText: a word in a note brings the 便利贴 group up
// between the clipboard's rows and the apps', heading hits first and
// newest first, three rows at most, and only a body hit carries a second
// line.
func TestHomeFindsNotesByText(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	a := notesHomeShelf(t,
		map[string]string{
			noteID('a'): "# 发版清单\n– 改 README\n",
			noteID('b'): "# 周五\n– 发版前跑测试\n",
			noteID('c'): "# 发版\n",
			noteID('d'): "# 发版 备忘\n",
		},
		map[string]time.Time{
			noteID('a'): base.Add(4 * time.Second),
			noteID('b'): base.Add(3 * time.Second),
			noteID('c'): base.Add(1 * time.Second),
			noteID('d'): base.Add(2 * time.Second),
		})
	a.store.Items = []store.Item{
		{ID: "c1", Kind: store.KindText, Text: "发版 clipboard", Source: store.SourceClipboard},
	}
	a.homeQuery = "发版"

	hits := a.homeHits()
	group := a.appLoc().HomeGroupNotes()
	first, n := groupSpan(hits, group)
	if n != homeGroupMax {
		t.Fatalf("便利贴 rows = %d, want %d: %+v", n, homeGroupMax, hits)
	}
	// Right after the clipboard's rows and before the rest.
	clipFirst, clipN := groupSpan(hits, a.appLoc().HomeGroupClipboard())
	if clipN == 0 || first != clipFirst+clipN {
		t.Fatalf("便利贴 group at %d (clipboard %d+%d) in %+v", first, clipFirst, clipN, hits)
	}
	// Heading hits, newest first; the capped fourth row is the body hit.
	var titles []string
	for _, h := range hits[first : first+n] {
		titles = append(titles, h.title)
		if h.sub != "" {
			t.Fatalf("a heading hit carried a second line: %+v", h)
		}
		if h.run == nil {
			t.Fatalf("a note row does nothing on ⏎: %+v", h)
		}
	}
	if want := []string{"发版清单", "发版 备忘", "发版"}; strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("note titles = %v, want %v", titles, want)
	}

	// A body hit is in the group with the line that matched, and the
	// heading it belongs to.
	a.homeQuery = "README"
	hits = a.homeHits()
	first, n = groupSpan(hits, group)
	if n != 1 || hits[first].title != "发版清单" || hits[first].sub != "– 改 README" {
		t.Fatalf("body hit = %+v", hits[first:first+n])
	}
	// Case does not matter.
	a.homeQuery = "readme"
	if first, n := groupSpan(a.homeHits(), group); n != 1 || first < 0 {
		t.Fatalf("a lower-case query found %+v", a.homeHits())
	}
}

// TestNoteHitsComeFromTheIndex: typing never reads the folder; a save or
// an outside change invalidates the index, and the next search sees it.
func TestNoteHitsComeFromTheIndex(t *testing.T) {
	id := noteID('a')
	a := notesHomeShelf(t, map[string]string{id: "# 发版清单\n"}, nil)
	a.homeQuery = "发版"
	if first, n := groupSpan(a.homeHits(), a.appLoc().HomeGroupNotes()); n != 1 || first < 0 {
		t.Fatalf("the note is not found: %+v", a.homeHits())
	}
	// Another editor rewrote the note: the index still holds the old text
	// until something says the notes changed.
	path := filepath.Join(a.dataDir, note.DirName, id+note.FileExt)
	if err := os.WriteFile(path, []byte("# 换成别的\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.homeQuery = "换成"
	if _, n := groupSpan(a.homeHits(), a.appLoc().HomeGroupNotes()); n != 0 {
		t.Fatal("a search read the folder instead of the index")
	}
	a.noteStale()
	if _, n := groupSpan(a.homeHits(), a.appLoc().HomeGroupNotes()); n != 1 {
		t.Fatal("the invalidated index did not pick the new text up")
	}
	a.homeQuery = "发版"
	if _, n := groupSpan(a.homeHits(), a.appLoc().HomeGroupNotes()); n != 0 {
		t.Fatal("the old text is still in the index")
	}
}

// TestEnterOpensTheNoteRow: ⏎ on a 便利贴 row makes that note the card's,
// loads it, and brings its window up through the existing path.
func TestEnterOpensTheNoteRow(t *testing.T) {
	current, other := noteID('a'), noteID('b')
	a := notesHomeShelf(t, map[string]string{
		current: "# 手上的\n",
		other:   "# 另一张\n– 里面有 关键词\n",
	}, nil)
	if err := os.WriteFile(filepath.Join(a.dataDir, note.DirName, "current"),
		[]byte(current+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.visible = false // hide() then does nothing: no panel to fold away
	a.homeQuery = "关键词"

	hits := a.homeHits()
	first, n := groupSpan(hits, a.appLoc().HomeGroupNotes())
	if n != 1 {
		t.Fatalf("the note is not found: %+v", hits)
	}
	a.homeSel = first

	oldFront := openNoteFront
	t.Cleanup(func() { openNoteFront = oldFront })
	var fronted *noteUI
	openNoteFront = func(n *noteUI) bool {
		fronted = n
		return true
	}
	a.runHomeHit()

	if a.note == nil || fronted != a.note {
		t.Fatal("⏎ did not bring the note up")
	}
	t.Cleanup(func() {
		if a.note.watch != nil {
			a.note.watch.Close()
			a.note.watch = nil
		}
	})
	if got := a.note.store.ID(); got != other {
		t.Fatalf("the card edits %q, want the note the row named %q", got, other)
	}
	if !strings.Contains(a.note.draft, "关键词") || a.note.dirty() {
		t.Fatalf("the note's text was not loaded: %q (dirty %v)", a.note.draft, a.note.dirty())
	}
}

// TestNoteFileDoesNotRepeatInSpotlightRows: the note file the index finds
// is not shown a second time as a Spotlight file row, even if the index
// does return it.
func TestNoteFileDoesNotRepeatInSpotlightRows(t *testing.T) {
	id := noteID('a')
	a := notesHomeShelf(t, map[string]string{id: "# 发版清单\n"}, nil)
	path := filepath.Join(a.dataDir, note.DirName, id+note.FileExt)
	other := filepath.Join(a.dataDir, "README-发版.txt")
	a.homeQuery = "发版"
	a.homeSpot.query = a.homeQuery
	// An index that did return the note file, beside an ordinary one.
	a.homeSpot.items = []mac.SpotlightHit{
		{Path: path, Name: filepath.Base(path)},
		{Path: other, Name: filepath.Base(other)},
	}

	hits := a.homeHits()
	sawOther := false
	for _, h := range hits {
		if h.path == path {
			t.Fatalf("the note file showed as its own row: %+v", h)
		}
		if h.path == other {
			sawOther = true
		}
	}
	if !sawOther {
		t.Fatalf("the ordinary index row was dropped too: %+v", hits)
	}
	if _, n := groupSpan(hits, a.appLoc().HomeGroupNotes()); n != 1 {
		t.Fatalf("the note row is missing: %+v", hits)
	}
}

// TestNoteResultsPanelShot renders the results sheet with a 便利贴 hit.
// The PNG is evidence for the change: RAKKO_NOTES_SHOT names the file to
// write, and a plain test run writes nothing.
func TestNoteResultsPanelShot(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	a := notesHomeShelf(t,
		map[string]string{
			noteID('a'): "# 周五发版前\n– 改 README 顶栏描述\n– 补 v1.13 截图\n",
			noteID('b'): "# 随手记\n– 发版前跑一遍 race\n",
		},
		map[string]time.Time{
			noteID('a'): base.Add(time.Minute),
			noteID('b'): base,
		})
	a.store.Items = []store.Item{
		{ID: "c1", Kind: store.KindText, Text: "发版流程", Source: store.SourceClipboard},
	}
	a.homeQuery = "发版"
	a.visible = true
	a.level = 1
	a.thumbnails = map[string]*ui.Bitmap{}
	a.thumbPending = map[string]bool{}
	// The bar, its sheet and the page capsule hung under it, no more.
	h := float32(bandH+homeBarH) + resultsGap + resultsHeight(a.homeHits()) + navGap + navH + 2
	view := ui.NewTester(a.view, homeTypingW, int(h))
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	if !view.HasText(a.appLoc().HomeGroupNotes()) {
		t.Fatalf("the 便利贴 caption is not drawn: %v", view.Texts())
	}
	if !view.HasText("周五发版前") {
		t.Fatalf("the note row is not drawn: %v", view.Texts())
	}
	shot := os.Getenv("RAKKO_NOTES_SHOT")
	if shot == "" {
		return
	}
	f, err := os.Create(shot)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, view.Image()); err != nil {
		t.Fatal(err)
	}
}
