package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/note"
	"rakko/locales"
)

// noteSmokeShelf is an instance with a temporary data directory and the note
// session it opens; the watcher is closed when the test ends.
func noteTestShelf(t *testing.T) (*shelf, *noteUI) {
	t.Helper()
	a := &shelf{dataDir: t.TempDir()}
	n := a.noteSession()
	if n == nil {
		t.Fatal("no note session")
	}
	t.Cleanup(func() {
		if n.watch != nil {
			closeNoteWatch(n)
			n.watch = nil
		}
	})
	return a, n
}

// disableNoteWatch keeps headless tests from starting a file watch: its
// goroutine calls RunOnMain, which waits forever without an app loop.
func disableNoteWatch() {
	noteWatchFile = func(string, func()) (*note.Watch, error) { return nil, nil }
}

// closeNoteWatch stops a test note's watcher when one was started.
func closeNoteWatch(n *noteUI) {
	if n != nil && n.watch != nil {
		closeNoteWatch(n)
		n.watch = nil
	}
}

// fileText reads the note straight from disk.
func fileText(t *testing.T, n *noteUI) string {
	t.Helper()
	data, err := os.ReadFile(n.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestNoteSaveDebounce: typing lands the note 400 ms after it stops, not
// per keystroke; a keystroke in between restarts the delay.
func TestNoteSaveDebounce(t *testing.T) {
	_, n := noteTestShelf(t)
	base := time.Now()
	typed := "# 周五发版前\n\n– 改 README 顶栏描述"
	n.setDraft(typed)
	if n.tick(base) {
		t.Fatal("wrote on the first keystroke")
	}
	if got := fileText(t, n); got != "" {
		t.Fatalf("file holds %q before the delay is over", got)
	}
	if n.tick(base.Add(300 * time.Millisecond)) {
		t.Fatal("wrote 300 ms after the last keystroke")
	}
	// Another keystroke moves the deadline.
	n.setDraft(typed + "\n\n– 补 v1.13 截图")
	n.tick(base.Add(350 * time.Millisecond))
	if n.tick(base.Add(600 * time.Millisecond)) {
		t.Fatal("wrote 250 ms after the second keystroke")
	}
	if !n.tick(base.Add(800 * time.Millisecond)) {
		t.Fatal("did not write 450 ms after the last keystroke")
	}
	if got := fileText(t, n); got != n.draftText() {
		t.Fatalf("file = %q, want %q", got, n.draftText())
	}
	if n.dirty() {
		t.Fatal("still unsaved after the write")
	}
	// Nothing left to write: the following ticks do nothing.
	if n.tick(base.Add(2 * time.Second)) {
		t.Fatal("wrote again with nothing to save")
	}
}

// TestNoteFlushOnClose: closing, Esc, the panel folding away and quitting
// all call flush, so a sentence typed a moment ago is on disk at once.
func TestNoteFlushOnClose(t *testing.T) {
	_, n := noteTestShelf(t)
	n.setDraft("# 随手记\n\n– 一条")
	n.tick(time.Now()) // seen, not yet due
	n.flush()
	if got := fileText(t, n); got != "# 随手记\n– 一条" {
		t.Fatalf("flush left %q on disk", got)
	}
	if n.dirty() {
		t.Fatal("flush left the draft unsaved")
	}
}

// TestNoteAdoptOutsideChange: a change another editor made shows up while
// nothing is unsaved; a draft in progress wins until it is written.
func TestNoteAdoptOutsideChange(t *testing.T) {
	_, n := noteTestShelf(t)
	if !n.adopt("# 别处改的\n") {
		t.Fatal("a clean note did not take the outside change")
	}
	if n.text != "# 别处改的\n" || n.draftText() != "# 别处改的\n" {
		t.Fatalf("text %q, draft %q; want the outside text", n.text, n.draftText())
	}
	if n.adopt("# 别处改的\n") {
		t.Fatal("the same text counted as a change")
	}
	n.setDraft("# 正在写")
	if n.adopt("# 又改了一次\n") {
		t.Fatal("an unsaved draft was overwritten")
	}
	if n.draftText() != "# 正在写\n" {
		t.Fatalf("draft = %q after the outside change", n.draftText())
	}
	n.flush()
	if !n.adopt("# 现在可以了\n") {
		t.Fatal("a saved note did not take the next outside change")
	}
}

// TestNoteOutsideRunsOnMainThread: the watch's report reloads the file
// through the panel, which is what the window and the card draw.
func TestNoteOutsideRunsOnMainThread(t *testing.T) {
	a, n := noteTestShelf(t)
	if err := note.WriteFile(n.store.Path(), "# 外部写入\n– 一行\n"); err != nil {
		t.Fatal(err)
	}
	a.noteOutside()
	if want := "# 外部写入\n– 一行\n"; n.text != want {
		t.Fatalf("text = %q, want %q", n.text, want)
	}
	if n.draftText() != n.text {
		t.Fatal("the clean editor did not follow the file")
	}
}

// TestNoteUnreadableLocksTheEditor: a note whose file cannot be read has no
// editor to type into and no save path, so an empty window can never be
// written over the note.
func TestNoteUnreadableLocksTheEditor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0 does not make a file unreadable on Windows")
	}
	a := &shelf{dataDir: t.TempDir()}
	if _, err := note.Open(a.dataDir); err != nil {
		t.Fatal(err)
	}
	n := a.noteSession()
	if n == nil {
		t.Fatal("no note session")
	}
	t.Cleanup(func() {
		if n.watch != nil {
			closeNoteWatch(n)
			n.watch = nil
		}
	})
	original := "# 原件\n一行的内容\n"
	if err := note.WriteFile(n.store.Path(), original); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(n.store.Path(), 0); err != nil {
		t.Fatal(err)
	}
	// The session is made again, as a launch would: its read fails.
	a.note = nil
	a2 := a
	n2 := a2.noteSession()
	if n2 == nil {
		t.Fatal("no note session over an unreadable note")
	}
	closeNoteWatch(n2)
	n2.watch = nil
	if !n2.locked {
		t.Fatal("an unreadable note was opened for writing")
	}
	view := ui.NewTester(a.noteView, noteW, noteH)
	if !view.HasText("无法读取便利贴文件") {
		t.Fatalf("the window offers no explanation; texts %v", view.Texts())
	}
	if _, found := view.Find("note-area"); found {
		t.Fatal("the editor is still on screen over an unreadable note")
	}
	// Restoring the permissions lets the test read the file back; the note
	// still may not be written while it was never read.
	if err := os.Chmod(n.store.Path(), 0o600); err != nil {
		t.Fatal(err)
	}
	n2.setDraft("一次全覆盖")
	if err := n2.flush(); err != nil {
		t.Fatal(err)
	}
	if got := fileText(t, n); got != original {
		t.Fatalf("the note became %q, want the original text", got)
	}
	if !n2.tick(time.Now()) && n2.dirty() {
		// the debounce must not write either
		n2.tick(time.Now().Add(time.Second))
	}
	if got := fileText(t, n); got != original {
		t.Fatalf("a delayed save wrote %q over the original", got)
	}
	// A read that works again unlocks the window and adopts the file.
	a.noteOutside()
	if n2.locked {
		t.Fatal("the note stayed locked after its file became readable")
	}
	if n2.draftText() != original {
		t.Fatalf("draft = %q, want the file's own text", n2.draftText())
	}
	n2.setDraft("改好了")
	if err := n2.flush(); err != nil {
		t.Fatal(err)
	}
	if got := fileText(t, n); got != "改好了\n" {
		t.Fatalf("the unlocked note wrote %q", got)
	}
}

// TestNoteSaveRedrawsThePin: the note's desktop card draws from the same
// text, and a free pin does not repaint on its own — so a save and an
// outside change must both reach it.
func TestNoteSaveRedrawsThePin(t *testing.T) {
	a, n := noteTestShelf(t)
	var redrawn []string
	old := notePinRedraw
	notePinRedraw = func(it *pinItem) { redrawn = append(redrawn, it.cardID) }
	t.Cleanup(func() { notePinRedraw = old })
	a.pins = []*pinItem{{cardID: "note"}, {cardID: "clipboard"}}
	n.setDraft("# 记一笔")
	n.flush()
	if len(redrawn) != 1 || redrawn[0] != "note" {
		t.Fatalf("the save redrew %v, want only the note's pin", redrawn)
	}
	if err := note.WriteFile(n.store.Path(), "# 外面改的"); err != nil {
		t.Fatal(err)
	}
	a.noteOutside()
	if len(redrawn) != 2 {
		t.Fatalf("the outside change redrew %v, want the pin again", redrawn)
	}
}

func TestNoteCardShape(t *testing.T) {
	def := toolCardDefFor("note")
	if def == nil || def.kind != cardWidget {
		t.Fatalf("note card = %+v, want a widget", def)
	}
	sizes := cardSizes("note")
	if len(sizes) != 2 || sizes[0] != [2]int{1, 1} || sizes[1] != [2]int{2, 1} {
		t.Fatalf("sizes = %v, want 1 × 1 then 2 × 1", sizes)
	}
	if !pinWide("note") {
		t.Fatal("the pinned note card is not wide")
	}
	for _, class := range []dropClass{classImages, classPDFs, classArchives, classMixed} {
		if def.accepts(class) {
			t.Fatalf("the note card takes %v", class)
		}
	}
}

// TestNoteCardPreviewLines: the 2 × 1 card shows the heading and up to
// three preview lines; a pinned card shows the same shape even at one
// column. The 1 × 1 card is only the glyph and the name.
func TestNoteCardPreviewLines(t *testing.T) {
	a := &shelf{dataDir: t.TempDir()}
	n := a.noteSession()
	if n == nil {
		t.Fatal("no note session")
	}
	defer func() {
		if n.watch != nil {
			closeNoteWatch(n)
			n.watch = nil
		}
	}()
	text := "# 周五发版前\n– 改 README 顶栏描述\n– 补 v1.13 截图\n– 问 Codex 额度\n– 第四行不该出现\n"
	if err := n.store.Write(text); err != nil {
		t.Fatal(err)
	}
	if err := n.load(); err != nil {
		t.Fatal(err)
	}

	draw := func(span int, surf toolCardSurface) *ui.Tester {
		h := float32(toolRowH(1))
		if surf == cardDesktop {
			h = pinCardH
		}
		view := ui.NewTester(func(c *ui.Context) {
			card := ui.Column(c.Key("card")).Width(toolColW(span)).Height(h).Radius(14).Background(fill1)
			a.noteBody(c, card, span, surf)
		}, 400, int(h)+20)
		view.Frame()
		view.Frame()
		return view
	}

	wide := draw(2, cardPanel)
	if _, ok := wide.Find("周五发版前"); !ok {
		t.Fatal("the heading is not on the 2 × 1 card")
	}
	for _, line := range []string{"– 改 README 顶栏描述", "– 补 v1.13 截图", "– 问 Codex 额度"} {
		if _, ok := wide.Find(line); !ok {
			t.Fatalf("preview line %q missing", line)
		}
	}
	if _, ok := wide.Find("– 第四行不该出现"); ok {
		t.Fatal("the card shows a fourth preview line")
	}

	pinned := draw(1, cardDesktop)
	if _, ok := pinned.Find("– 补 v1.13 截图"); !ok {
		t.Fatal("the pinned card does not show the preview")
	}

	narrow := draw(1, cardPanel)
	if _, ok := narrow.Find("– 补 v1.13 截图"); ok {
		t.Fatal("the 1 × 1 card shows preview lines")
	}
	// The card's accessible name is the heading, whatever the size.
	if got := a.noteLabel(locales.In("")); got != "周五发版前" {
		t.Fatalf("accessible name = %q, want the heading", got)
	}
}

func TestNoteWindowTitle(t *testing.T) {
	a := &shelf{dataDir: t.TempDir()}
	n := a.noteSession()
	if n == nil {
		t.Fatal("no note session")
	}
	defer func() {
		if n.watch != nil {
			closeNoteWatch(n)
			n.watch = nil
		}
	}()
	if got := a.noteWindowTitle(); got == "" {
		t.Fatal("a blank note has no window title")
	}
	n.resetText("## 随手记\n")
	if got := a.noteWindowTitle(); strings.TrimSpace(got) != "随手记" {
		t.Fatalf("window title = %q, want the heading", got)
	}
	// The file path is a Markdown note in the data directory's notes/.
	if dir := filepath.Base(n.store.Dir()); dir != note.DirName {
		t.Fatalf("note folder = %q, want %q", dir, note.DirName)
	}
}

// TestNoteFrameUnder: the window opens under its anchor, centred on it;
// with no room below it opens above, and it never leaves the work area.
func TestNoteFrameUnder(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 33, Width: 1470, Height: 862}
	cases := []struct {
		name   string
		anchor mygo.Rectangle
		want   mygo.Rectangle
	}{
		{"under the notch panel", mygo.Rectangle{X: 500, Y: 0, Width: 470, Height: 250},
			mygo.Rectangle{X: 500 + (470-noteW)/2, Y: 258, Width: noteW, Height: noteH}},
		{"under a pinned card", mygo.Rectangle{X: 600, Y: 100, Width: 278, Height: 84},
			mygo.Rectangle{X: 600 + (278-noteW)/2, Y: 192, Width: noteW, Height: noteH}},
		{"no room below, opens above", mygo.Rectangle{X: 600, Y: 700, Width: 200, Height: 84},
			mygo.Rectangle{X: 600 + (200-noteW)/2, Y: 700 - noteH - 8, Width: noteW, Height: noteH}},
		{"tall anchor at the top, no room on either side: above, clamped to the top", mygo.Rectangle{X: 0, Y: 0, Width: 470, Height: 659},
			mygo.Rectangle{X: 0 + (470-noteW)/2, Y: 33, Width: noteW, Height: noteH}},
		{"clamped at the left edge", mygo.Rectangle{X: 0, Y: 100, Width: 100, Height: 84},
			mygo.Rectangle{X: 0, Y: 192, Width: noteW, Height: noteH}},
		{"clamped at the right edge", mygo.Rectangle{X: 1400, Y: 100, Width: 60, Height: 84},
			mygo.Rectangle{X: 1470 - noteW, Y: 192, Width: noteW, Height: noteH}},
	}
	for _, c := range cases {
		if got := noteFrameUnder(c.anchor, area); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestNoteShowFrontHidesPanel verifies raising an existing note folds the
// panel on every platform.
func TestNoteShowFrontHidesPanel(t *testing.T) {
	a, n := noteTestShelf(t)
	a.visible = true
	n.win = &mygo.Window{}
	origShow, origAct, origFocus := noteShow, noteActivate, noteFocus
	noteShow = func(*mygo.Window) {}
	noteActivate = func() {}
	noteFocus = func(*mygo.Window) {}
	t.Cleanup(func() { noteShow, noteActivate, noteFocus = origShow, origAct, origFocus })

	if !n.showFront() {
		t.Fatal("showFront returned false")
	}
	if a.visible {
		t.Fatal("panel stayed visible after showFront")
	}
}

// A hide lands the note window's draft even when the panel was already
// folded (opening the note folds it): the second hide must not skip it and
// leave the sentence only in memory.
func TestHideLandsNoteDraftWhenPanelAlreadyFolded(t *testing.T) {
	a, n := noteTestShelf(t)
	n.setDraft("# 面板早已收起，这句要落盘")
	a.hide(hidePassive) // already folded: nothing to fold
	if got := fileText(t, n); got != "# 面板早已收起，这句要落盘" {
		t.Fatalf("the folded panel's hide did not land the draft; disk holds %q", got)
	}
}
