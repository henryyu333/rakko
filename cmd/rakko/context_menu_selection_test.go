package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/store"
)

// trayWithFiles stores the named files, newest first, headless.
func trayWithFiles(t *testing.T, names ...string) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	dir := t.TempDir()
	for _, name := range names {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
	}
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	return a, ui.NewTester(a.view, sheetW, sheetH+navGap+navH+2)
}

// itemNamed returns the id of the stored file with the given base name.
func itemNamed(t *testing.T, a *shelf, name string) string {
	t.Helper()
	for _, it := range a.store.Items {
		if filepath.Base(it.Path) == name {
			return it.ID
		}
	}
	t.Fatalf("no stored item named %s", name)
	return ""
}

// TestContextMenuSelection: right-clicking an unselected tile selects that
// tile alone on every platform. While the native menu is still open, frames
// continue to paint the pre-menu selection so the accent ring appears only
// after the menu closes, matching macOS.
func TestContextMenuSelection(t *testing.T) {
	a, view := trayWithFiles(t, "a.txt", "b.txt")
	view.Frame()

	oldMenuOpen := menuOpen
	holdOpen := true
	menuOpen = func(s *shelf) bool { return holdOpen && s.menuHoldSet }
	t.Cleanup(func() { menuOpen = oldMenuOpen })

	if err := view.RightClick("a.txt"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), "移除") {
		t.Fatalf("menu on an unselected tile should act on it alone: %v", view.Menu())
	}
	if a.selection.count() != 1 || !a.selection.has(itemNamed(t, a, "a.txt")) {
		t.Fatalf("selection = %d items; right-clicking an unselected tile must select it", a.selection.count())
	}

	// While the native popup menu loop is active, owner repaints must keep the
	// pre-menu selection so the accent ring appears only after it closes.
	idA := itemNamed(t, a, "a.txt")
	view.Frame()
	if a.itemSelected(idA) {
		t.Fatal("tile painted as selected while the native menu was still open")
	}

	holdOpen = false
	view.Frame()
	if !a.itemSelected(idA) {
		t.Fatal("tile did not show selected after the native menu closed")
	}
}

// TestContextMenuKeepsAMultiSelection: a right click inside a multi-item
// selection acts on all of it, and changes nothing either way.
func TestContextMenuKeepsAMultiSelection(t *testing.T) {
	a, view := trayWithFiles(t, "a.txt", "b.txt", "c.txt")
	view.Frame()
	if err := view.Click("a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := view.ClickWith(ui.Cmd, "b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := view.RightClick("b.txt"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), "移除 2 项") {
		t.Fatalf("menu on a selected tile should act on the selection: %v", view.Menu())
	}
	if a.selection.count() != 2 || !a.selection.has(itemNamed(t, a, "a.txt")) || !a.selection.has(itemNamed(t, a, "b.txt")) {
		t.Fatalf("the multi-selection changed: %d items", a.selection.count())
	}
}
