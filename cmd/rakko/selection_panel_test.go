//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/store"
)

// shelfWithFiles stores the named files, newest first, and returns the
// shelf, its view and the files' paths by name.
func shelfWithFiles(t *testing.T, names ...string) (*shelf, *ui.Tester, map[string]string) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	dir := t.TempDir()
	paths := map[string]string{}
	for _, name := range names {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
		paths[name] = p
	}
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	return a, ui.NewTester(a.view, sheetW, panelTestH), paths
}

func storedNames(s *store.Store) []string {
	var out []string
	for _, it := range s.Items {
		out = append(out, filepath.Base(it.Path))
	}
	return out
}

func TestRightClickOnSelectionRemovesAllSelected(t *testing.T) {
	a, view, paths := shelfWithFiles(t, "a.txt", "b.txt", "c.txt")
	if err := view.Click("a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := view.ClickWith(ui.Cmd, "b.txt"); err != nil {
		t.Fatal(err)
	}
	if a.selection.count() != 2 {
		t.Fatalf("⌘-click selection = %d items; want 2", a.selection.count())
	}
	if err := view.RightClick("b.txt"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), "移除 2 项") {
		t.Fatalf("menu on a selected row should act on the selection: %v", view.Menu())
	}
	if err := view.ChooseMenuItem("移除 2 项"); err != nil {
		t.Fatal(err)
	}
	if got := storedNames(a.store); !slices.Equal(got, []string{"c.txt"}) {
		t.Fatalf("after removing the selection the shelf holds %v; want [c.txt]", got)
	}
	if a.selection.count() != 0 {
		t.Fatal("removed items are still selected")
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(paths[name]); err != nil {
			t.Fatalf("removing %s from the shelf deleted the original: %v", name, err)
		}
	}
}

func TestRightClickOutsideSelectionActsOnThatRowOnly(t *testing.T) {
	a, view, _ := shelfWithFiles(t, "a.txt", "b.txt", "c.txt")
	if err := view.Click("a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := view.ClickWith(ui.Shift, "b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := view.RightClick("c.txt"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("移除"); err != nil {
		t.Fatalf("menu on an unselected row should offer single removal: %v (%v)", err, view.Menu())
	}
	if got := storedNames(a.store); !slices.Equal(got, []string{"b.txt", "a.txt"}) {
		t.Fatalf("right-click outside the selection removed %v; want only c.txt gone", got)
	}
}

func TestDraggingASelectedRowCarriesTheWholeSelection(t *testing.T) {
	a, view, paths := shelfWithFiles(t, "a.txt", "b.txt", "c.txt")
	if err := view.Click("c.txt"); err != nil {
		t.Fatal(err)
	}
	if err := view.ClickWith(ui.Cmd, "a.txt"); err != nil {
		t.Fatal(err)
	}
	var cItem, bItem store.Item
	for _, it := range a.store.Items {
		switch filepath.Base(it.Path) {
		case "c.txt":
			cItem = it
		case "b.txt":
			bItem = it
		}
	}
	got, err := a.dragOut(cItem).Files()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{paths["c.txt"], paths["a.txt"]}; !slices.Equal(got, want) {
		t.Fatalf("dragging a selected row carried %v; want %v", got, want)
	}
	// An unselected row drags alone.
	got, err = a.dragOut(bItem).Files()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{paths["b.txt"]}) {
		t.Fatalf("dragging an unselected row carried %v", got)
	}
}
