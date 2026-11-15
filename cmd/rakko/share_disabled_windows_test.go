//go:build windows

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/settings"
	"rakko/internal/store"
)

func TestWindowsHidesShareEverywhereAndPreservesLayoutRecord(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "pic.png")
	pdf := filepath.Join(dir, "doc.pdf")
	zipf := filepath.Join(dir, "arc.zip")
	txt := filepath.Join(dir, "note.txt")
	for _, p := range []string{img, pdf, zipf, txt} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Drop actions exclude nearby sharing without padding gaps.
	cases := []struct {
		name  string
		paths []string
		want  []string
	}{
		{"images", []string{img}, []string{"fit1m", "topdf", "tojpeg"}},
		{"pdfs", []string{pdf}, []string{"toword", "topng", "zip"}},
		{"archives", []string{zipf}, []string{"extract"}},
		{"mixed", []string{img, txt}, []string{"zip"}},
	}
	for _, tc := range cases {
		acts := dropActions(tc.paths)
		got := make([]string, len(acts))
		for i, a := range acts {
			got[i] = a.id
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s dropActions = %v, want %v", tc.name, got, tc.want)
		}
	}

	// 2. Tray right-click menu omits nearby sharing.
	aTray := windowsDropShelf(t)
	aTray.visible = true
	aTray.tab = pageTray
	aTray.store.AddText("hello share")
	trayView := ui.NewTester(aTray.view, sheetW, winDropTestH)
	trayView.Frame()
	if err := trayView.RightClick("hello share"); err != nil {
		t.Fatalf("RightClick failed: %v", err)
	}
	for _, item := range trayView.Menu() {
		if strings.Contains(item, "附近共享") || strings.Contains(item, "隔空投送") || strings.Contains(item, "AirDrop") || strings.Contains(item, "Nearby") {
			t.Fatalf("tray context menu still offers sharing item %q: %v", item, trayView.Menu())
		}
	}

	// 3. Existing Mac layout containing "airdrop" stays intact on disk while
	// remaining hidden without gaps or errors on Windows.
	dataDir := t.TempDir()
	macLayout := layout.Layout{
		Version: layout.Version,
		Cards: []layout.Card{
			{ID: "playing", Span: 2, Rows: 2},
			{ID: "airdrop", Span: 1, Rows: 1},
			{ID: "zip", Span: 1, Rows: 1},
		},
	}
	if err := layout.Write(dataDir, macLayout); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{
		store: s, settings: settings.Default(),
		tab: pageTools, visible: true, dataDir: dataDir,
		notchW: 180, notchH: 32,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.conv = &converter{host: a}
	p := a.toolsPage()
	p.editing = true
	for _, def := range p.libraryCards() {
		if def.id == "airdrop" {
			t.Fatal("tools libraryCards still includes airdrop on Windows")
		}
	}
	if !a.inactiveWidget("airdrop") {
		t.Fatal("inactiveWidget(\"airdrop\") = false on Windows, want true")
	}

	toolsView := ui.NewTester(a.view, sheetW, winDropTestH)
	toolsView.Frame()
	if a.toolsLayBroken {
		t.Fatal("loading Mac layout with airdrop set toolsLayBroken")
	}
	for _, forbid := range []string{"airdrop", "附近共享", "隔空投送"} {
		if toolsView.HasText(forbid) {
			t.Fatalf("tools page rendered %q; texts=%v", forbid, toolsView.Texts())
		}
	}

	// Packed grid places "zip" directly into col 2 without leaving a blank hole for "airdrop".
	grid := toolGridFor(a.toolsLayout().Cards, a.inactiveWidget, p.bandMinCols())
	if len(grid.cells) != 2 || grid.cells[0].id != "playing" || grid.cells[1].id != "zip" {
		t.Fatalf("packed cells = %+v, want [playing, zip]", grid.cells)
	}
	if got := grid.cells[1]; got.x != grid.cells[0].x+grid.cells[0].w+toolCellGap || got.y != 0 {
		t.Fatalf("zip cell landed at (%v,%v) instead of filling row 0 next to playing (%v+%v)",
			got.x, got.y, grid.cells[0].x, grid.cells[0].w)
	}

	// Saving another card edit preserves the stored "airdrop" entry in layout.json.
	if !a.mutateTools(func(l *layout.Layout) { l.AddCardSize("fit1m", 1, 1) }) {
		t.Fatal("mutateTools returned false")
	}
	reloaded, err := layout.LoadWith(dataDir, a.settings.AIUsage.Enabled)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(reloaded.Cards, func(c layout.Card) bool { return c.ID == "airdrop" }) {
		t.Fatalf("reloaded layout.json dropped airdrop entry: %+v", reloaded.Cards)
	}
}
