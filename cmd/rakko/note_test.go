//go:build darwin

package main

import (
	"os"
	"path/filepath"
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
			n.watch.Close()
			n.watch = nil
		}
	})
	return a, n
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
	typed := "# 周五发版前\n– 改 README 顶栏描述\n"
	n.draft = typed
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
	n.draft = typed + "– 补 v1.13 截图\n"
	n.tick(base.Add(350 * time.Millisecond))
	if n.tick(base.Add(600 * time.Millisecond)) {
		t.Fatal("wrote 250 ms after the second keystroke")
	}
	if !n.tick(base.Add(800 * time.Millisecond)) {
		t.Fatal("did not write 450 ms after the last keystroke")
	}
	if got := fileText(t, n); got != n.draft {
		t.Fatalf("file = %q, want %q", got, n.draft)
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
	n.draft = "# 随手记\n– 一条\n"
	n.tick(time.Now()) // seen, not yet due
	n.flush()
	if got := fileText(t, n); got != "# 随手记\n– 一条\n" {
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
	if n.text != "# 别处改的\n" || n.draft != "# 别处改的\n" {
		t.Fatalf("text %q, draft %q; want the outside text", n.text, n.draft)
	}
	if n.adopt("# 别处改的\n") {
		t.Fatal("the same text counted as a change")
	}
	n.draft = "# 正在写\n"
	if n.adopt("# 又改了一次\n") {
		t.Fatal("an unsaved draft was overwritten")
	}
	if n.draft != "# 正在写\n" {
		t.Fatalf("draft = %q after the outside change", n.draft)
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
	if n.draft != n.text {
		t.Fatal("the clean editor did not follow the file")
	}
}

// TestNoteUnreadableLocksTheEditor: a note whose file cannot be read has no
// editor to type into and no save path, so an empty window can never be
// written over the note.
func TestNoteUnreadableLocksTheEditor(t *testing.T) {
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
			n.watch.Close()
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
	n2.watch.Close()
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
	n2.draft = "一次全覆盖"
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
	if n2.draft != original {
		t.Fatalf("draft = %q, want the file's own text", n2.draft)
	}
	n2.draft = "改好了"
	if err := n2.flush(); err != nil {
		t.Fatal(err)
	}
	if got := fileText(t, n); got != "改好了" {
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
	n.draft = "# 记一笔"
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

// onMainCheck runs fn on the app's main thread and fails the test with what
// it reports. t.Fatal cannot run inside onMain: it abandons the goroutine
// that would have closed the handshake, leaving the caller waiting forever.
func onMainCheck(t *testing.T, fn func() string) {
	t.Helper()
	var problem string
	onMain(func() { problem = fn() })
	if problem != "" {
		t.Fatal(problem)
	}
}

// TestNoteWindowKeepsOnTop: 置顶 is the note's own state, and the window is
// rebuilt on every open — so a window built after the flag was set must come
// up on top, exactly as the bar draws it, and a plain one must not. A real
// window, because the flag reaches the window server only through one; the
// smoke harness is what runs a real app loop.
func TestNoteWindowKeepsOnTop(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to open a real note window")
	}
	a := smokeShelf
	onMainCheck(t, func() string {
		a.note = nil
		n := a.noteSession()
		if n == nil {
			return "no note session"
		}
		n.setTop(true)
		if !n.open() {
			return "the note did not open"
		}
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	onMainCheck(t, func() string {
		if w := a.note.win; w == nil || !w.IsAlwaysOnTop() {
			return "a window built over a lit 置顶 is not on top"
		}
		a.note.close()
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	onMainCheck(t, func() string {
		n := a.note
		n.setTop(false)
		if !n.open() {
			return "the note did not reopen"
		}
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	onMainCheck(t, func() string {
		n := a.note
		problem := ""
		if w := n.win; w != nil && w.IsAlwaysOnTop() {
			problem = "a window built without 置顶 came up on top"
		}
		n.close()
		if n.watch != nil {
			n.watch.Close()
		}
		n.watch = nil
		a.note = nil
		return problem
	})
}

// TestNoteCardShape: the card is a widget with the 1 × 1 and 2 × 1 sizes,
// and a pinned note is wide because its body needs two columns.
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
			n.watch.Close()
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
			n.watch.Close()
			n.watch = nil
		}
	}()
	if got := a.noteWindowTitle(); got == "" {
		t.Fatal("a blank note has no window title")
	}
	n.text = "## 随手记\n"
	n.draft = n.text
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
