package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/geo"
	"rakko/locales"
)

func TestBoardFlushPersistsInlineEdit(t *testing.T) {
	_, b := boardTestShelf(t)
	id := b.ed.CreateNote(geo.V(100, 100), "")
	if err := b.flush(); err != nil {
		t.Fatal(err)
	}
	b.ed.StartEditing(id)
	b.ed.EditText = "重要的会议纪要"
	b.ed.EditChanged()
	if !b.dirtyFlag {
		t.Fatal("inline edit did not mark the board dirty")
	}
	if err := b.flush(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(b.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "重要的会议纪要") {
		t.Fatal("flush dropped the text typed during inline editing")
	}
	if b.ed.Editing != "" {
		t.Fatal("flush left an inline edit open")
	}
}

func TestBoardAutosaveKeepsInlineEditOpen(t *testing.T) {
	_, b := boardTestShelf(t)
	id := b.ed.CreateNote(geo.V(100, 100), "")
	if err := b.flush(); err != nil {
		t.Fatal(err)
	}
	b.ed.StartEditing(id)
	b.ed.EditText = "边打字边自动保存"
	b.ed.EditChanged()
	base := time.Now()
	b.tick(base)
	if !b.tick(base.Add(500 * time.Millisecond)) {
		t.Fatal("debounced autosave did not run during inline editing")
	}
	if err := b.waitSaves(); err != nil {
		t.Fatal(err)
	}
	if b.ed.Editing != id {
		t.Fatal("autosave committed the inline edit and interrupted typing")
	}
	raw, err := os.ReadFile(b.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "边打字边自动保存") {
		t.Fatal("autosave dropped text typed during inline editing")
	}
}

func TestBoardSwitchAndNewPersistInlineEdit(t *testing.T) {
	for _, tc := range []string{"switch", "new"} {
		t.Run(tc, func(t *testing.T) {
			_, b := boardTestShelf(t)
			firstID := b.store.ID()
			if err := b.newBoardDoc(); err != nil {
				t.Fatal(err)
			}
			secondID := b.store.ID()
			if err := b.selectBoard(firstID); err != nil {
				t.Fatal(err)
			}
			id := b.ed.CreateNote(geo.V(100, 100), "")
			if err := b.flush(); err != nil {
				t.Fatal(err)
			}
			b.ed.StartEditing(id)
			b.ed.EditText = "切换前写下的字"
			b.ed.EditChanged()

			var err error
			if tc == "switch" {
				err = b.selectBoard(secondID)
			} else {
				err = b.newBoardDoc()
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(b.store.PathFor(firstID))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "切换前写下的字") {
				t.Fatalf("%s dropped inline text before switching boards", tc)
			}
		})
	}
}

func TestBoardPageDeleteRequiresConfirmation(t *testing.T) {
	a, b := boardTestShelf(t)
	b.ed.AddPage("")
	pagesBefore := len(b.ed.Pages())
	pageName := b.pageTitle(locales.In("zh-Hans"), b.ed.ActivePageInfo())

	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	if err := view.Click(pageName); err != nil {
		t.Fatalf("open page menu: %v", err)
	}
	view.Frame()
	if err := view.ChooseMenuItem(locales.In("").BoardPageDelete()); err != nil {
		t.Fatalf("choose delete page: %v", err)
	}
	view.Frame()
	if !b.confirmPageDelete {
		t.Fatal("delete page did not ask for confirmation")
	}
	saveBoardShot(t, "page-delete-confirm.png", view.Image())
	if got := len(b.ed.Pages()); got != pagesBefore {
		t.Fatalf("page deleted before confirmation: pages=%d want %d", got, pagesBefore)
	}

	if err := view.Click(locales.In("").BoardDeleteAction()); err != nil {
		t.Fatalf("confirm delete page: %v", err)
	}
	view.Frame()
	if got := len(b.ed.Pages()); got != pagesBefore-1 {
		t.Fatalf("pages after confirmation = %d, want %d", got, pagesBefore-1)
	}
	if !b.ed.CanUndo() {
		t.Fatal("confirmed page delete is not undoable")
	}
	b.ed.Undo()
	if got := len(b.ed.Pages()); got != pagesBefore {
		t.Fatalf("undo restored %d pages, want %d", got, pagesBefore)
	}
}

func TestBoardPageRenameCommitsOnBlur(t *testing.T) {
	_, b := boardTestShelf(t)
	b.ed.AddPage("")
	b.startPageRename()
	b.renamePageDraft = "想法"
	b.onBlur()
	if got := b.ed.ActivePageInfo().Name; got != "想法" {
		t.Fatalf("page name after blur = %q, want 想法", got)
	}
	raw, err := os.ReadFile(b.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "想法") {
		t.Fatal("page rename was not persisted on blur")
	}
}

func TestShelfHideDoesNotInterruptBoardInlineEdit(t *testing.T) {
	a, b := boardTestShelf(t)
	id := b.ed.CreateNote(geo.V(100, 100), "")
	if err := b.flush(); err != nil {
		t.Fatal(err)
	}
	b.ed.StartEditing(id)
	b.ed.EditText = "刘海收起不能打断"
	b.ed.EditChanged()
	a.visible = true

	a.hide(hidePassive)

	if b.ed.Editing != id {
		t.Fatalf("shelf.hide interrupted board inline edit: Editing=%q, want %q", b.ed.Editing, id)
	}
	if err := b.waitSaves(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(b.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "刘海收起不能打断") {
		t.Fatal("shelf.hide did not trigger non-interrupting save of inline edit")
	}
}

func TestKeysAreOursFalseWhenBoardOrNoteOpen(t *testing.T) {
	oldReg, oldUnreg := registerKey, unregisterKey
	var held map[string]bool
	held = map[string]bool{}
	registerKey = func(key string, _ func()) error {
		held[key] = true
		return nil
	}
	unregisterKey = func(key string) {
		delete(held, key)
	}
	t.Cleanup(func() {
		registerKey, unregisterKey = oldReg, oldUnreg
	})

	a := &shelf{}
	if !a.keysAreOurs() {
		t.Fatal("idle shelf should own keys")
	}
	a.holdKeys()
	if !a.escHeld || !a.spaceHeld {
		t.Fatalf("holdKeys did not grab keys: esc=%v space=%v", a.escHeld, a.spaceHeld)
	}

	a.note = &noteUI{win: &mygo.Window{}}
	if a.keysAreOurs() {
		t.Fatal("keysAreOurs must be false while note window is open")
	}
	a.holdKeys()
	if a.escHeld || a.spaceHeld || len(held) != 0 {
		t.Fatalf("holdKeys must release keys while note window is open: held=%v", held)
	}
	a.note.win = nil
	if !a.keysAreOurs() {
		t.Fatal("keysAreOurs should be true after note window closes")
	}

	a.holdKeys()
	a.board = &boardUI{win: &mygo.Window{}}
	if a.keysAreOurs() {
		t.Fatal("keysAreOurs must be false while board window is open")
	}
	a.holdKeys()
	if a.escHeld || a.spaceHeld || len(held) != 0 {
		t.Fatalf("holdKeys must release keys while board window is open: held=%v", held)
	}
	a.board.win = nil
	if !a.keysAreOurs() {
		t.Fatal("keysAreOurs should be true after board window closes")
	}
}
