//go:build darwin

package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/note"
	"rakko/locales"
)

// TestNoteWindowTypesAndCloses drives the window's view headlessly through
// the real input path: the area takes the keyboard on its first draw, typed
// characters land in the note, and Escape closes the window (flush first).
func TestNoteWindowTypesAndCloses(t *testing.T) {
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
	// The window itself is the native one; the view runs headless here.
	n.win = nil
	a.note = n
	var view *ui.Tester
	view = ui.NewTester(a.noteView, noteW, noteH)
	view.Frame()
	view.Frame()

	// The bar shows 便利贴 while the note is blank, and the buttons carry
	// their labels.
	blank := locales.In("")
	if _, ok := view.Find(blank.CardNote()); !ok {
		t.Fatal("a blank note has no title")
	}
	for _, label := range []string{blank.NoteReveal(), blank.NoteKeepOnTop(), blank.NoteClose()} {
		if _, ok := view.Find(label); !ok {
			t.Fatalf("bar button %q missing", label)
		}
	}

	// The area took the keyboard on the first draw: typing reaches it.
	view.Type("# 周五发版前")
	view.Type("\n– 改 README")
	view.Frame()
	if n.draft != "# 周五发版前\n– 改 README" {
		t.Fatalf("draft = %q after typing", n.draft)
	}
	if n.tick(time.Now()) {
		t.Fatal("saved on the first tick")
	}
	n.flush()
	if got := fileText(t, n); got != "# 周五发版前\n– 改 README" {
		t.Fatalf("file = %q", got)
	}

	// The note's heading shows in the bar once the file has one.
	view.Frame()
	if _, ok := view.Find("周五发版前"); !ok {
		t.Fatal("the heading is not in the bar")
	}

	// Escape closes the window, landing what was unsaved. There is no
	// native window in this test, so the close reaches the session and its
	// file; the text stays in memory for the next open.
	n.draft += "\n– 刚打的最后一句"
	view.Frame()
	view.Key(0, ui.KeyEscape)
	view.Frame()
	if got := fileText(t, n); got != "# 周五发版前\n– 改 README\n– 刚打的最后一句" {
		t.Fatalf("Escape left %q on disk", got)
	}
	if n.dirty() {
		t.Fatal("Escape left unsaved text")
	}
}

// TestNoteReopensWithTheFile: what a closed window wrote is what the next
// one shows, and what the card draws in between.
func TestNoteReopensWithTheFile(t *testing.T) {
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
	n.draft = "# 关窗前的最后一句\n"
	n.flush()

	// A fresh session over the same folder, as the next launch has it.
	again, err := note.Open(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	text, err := again.Read()
	if err != nil {
		t.Fatal(err)
	}
	if text != "# 关窗前的最后一句\n" {
		t.Fatalf("reopened note = %q", text)
	}
	if again.ID() != n.store.ID() {
		t.Fatalf("the reopen made a new note: %q vs %q", again.ID(), n.store.ID())
	}
}
