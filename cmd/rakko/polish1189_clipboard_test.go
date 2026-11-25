//go:build darwin

package main

// The clipboard and tray polish: colour swatches, search highlight and
// clear, the row's hover actions, and the tray tile's one-click removal.
// Headless: the store is a temp directory and copying goes to a recorder.

import (
	"os"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/store"
	"rakko/locales"
)

func TestParseColorSwatch(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want ui.Color
		ok   bool
	}{
		{"#FFF", ui.RGBA(255, 255, 255, 255), true},
		{"#ff453a", ui.RGBA(255, 69, 58, 255), true},
		{" #0A84FF ", ui.RGBA(10, 132, 255, 255), true},
		{"rgb(10, 132, 255)", ui.RGBA(10, 132, 255, 255), true},
		{"RGB(255,0,0)", ui.RGBA(255, 0, 0, 255), true},
		{"rgb( 1 , 2 , 3 )", ui.RGBA(1, 2, 3, 255), true},
		{"#12", ui.Color{}, false},
		{"#GGGGGG", ui.Color{}, false},
		{"rgb(300,0,0)", ui.Color{}, false},
		{"rgb(1,2)", ui.Color{}, false},
		{"#FFF and some words", ui.Color{}, false},
		{"#FFF\n#000", ui.Color{}, false},
		{"hello", ui.Color{}, false},
		{"", ui.Color{}, false},
	} {
		got, ok := parseColorSwatch(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseColorSwatch(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestClipTitleSpansHighlightFirstHit(t *testing.T) {
	spans, matched := clipTitleSpans("Dentist on Tuesday", "dentist")
	if !matched || len(spans) != 2 {
		t.Fatalf("spans = %d, matched=%v, want hit+after: %+v", len(spans), matched, spans)
	}
	if spans[0].Text != "Dentist" || spans[1].Text != " on Tuesday" {
		t.Fatalf("highlight sliced the wrong run: %+v", spans)
	}
	if spans[0].Background != accent.Alpha(0.34) {
		t.Fatalf("hit has no highlight background: %+v", spans[0])
	}

	spans, matched = clipTitleSpans("alpha beta alpha", "ALPHA")
	if !matched || len(spans) != 2 || spans[0].Text != "alpha" || spans[1].Text != " beta alpha" {
		t.Fatalf("only the first hit should be highlighted: %+v", spans)
	}

	// A query that equals the whole title is one span, but still a hit:
	// the row must take the RichText path for it.
	spans, matched = clipTitleSpans("#FF453A", "#ff453a")
	if !matched || len(spans) != 1 || spans[0].Text != "#FF453A" ||
		spans[0].Background != accent.Alpha(0.34) {
		t.Fatalf("whole-title hit was lost: matched=%v %+v", matched, spans)
	}

	if spans, matched := clipTitleSpans("nothing here", "needle"); matched || len(spans) != 1 ||
		spans[0].Text != "nothing here" || spans[0].Background != (ui.Color{}) {
		t.Fatalf("no hit must stay plain: matched=%v %+v", matched, spans)
	}
	if spans, matched := clipTitleSpans("text", "  "); matched || len(spans) != 1 {
		t.Fatalf("a blank query must stay plain: matched=%v %+v", matched, spans)
	}

	// Case folding that changes either byte length falls back to an exact
	// search instead of slicing at a byte offset from the folded string.
	if spans, matched := clipTitleSpans("İ", "İ"); !matched || len(spans) != 1 || spans[0].Text != "İ" {
		t.Fatalf("folded whole-title hit was lost: matched=%v %+v", matched, spans)
	}
}

func TestClipboardSearchClearKeepsFocus(t *testing.T) {
	a, view, _ := clipShelf(t, "alpha one", "beta two")
	l := locales.In("")
	a.setTab(pageClipboard)
	view.Frame()
	if err := view.Click(l.PanelSearch()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if !view.Focused(l.PanelSearch()) {
		t.Fatal("the search field did not take the focus")
	}
	a.search = "beta"
	view.Frame()
	if got := clipTexts(a); !slices.Equal(got, []string{"beta two"}) {
		t.Fatalf("search filtered to %v", got)
	}
	if !view.HasText(l.ClipClearSearch()) {
		t.Fatalf("a filled search shows no clear button: %v", view.Texts())
	}
	if err := view.Click(l.ClipClearSearch()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if a.search != "" {
		t.Fatalf("clear left the query %q", a.search)
	}
	if got := clipTexts(a); len(got) != 2 {
		t.Fatalf("clear did not restore the list: %v", got)
	}
	if !view.Focused(l.PanelSearch()) {
		t.Fatal("clearing dropped the focus")
	}
}

// The row's hover actions pin and delete without the row's own click: no
// copy, no selection change.
func TestClipboardRowHoverActions(t *testing.T) {
	a, view, rec := clipShelf(t, "alpha capture", "beta capture")
	l := locales.In("")
	a.setTab(pageClipboard)
	view.Frame()

	row, ok := view.Find("alpha capture")
	if !ok {
		t.Fatalf("row did not draw: %v", view.Texts())
	}
	view.Move(row.X+row.W/2, row.Y+row.H/2)
	view.Frame()
	view.Frame()
	if !view.HasText(l.ItemPin()) || !view.HasText(l.ItemRemove()) {
		t.Fatalf("hover actions missing: %v", view.Texts())
	}
	if err := view.Click(l.ItemPin()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if !a.store.Items[findItem(a.store.Items, "alpha capture")].Pinned {
		t.Fatal("the hover pin did not pin the row")
	}
	if rec.text != "" || a.selection.count() != 0 {
		t.Fatalf("hover pin leaked into the row: copy=%q selection=%d", rec.text, a.selection.count())
	}

	// Unpin through the same button; it now reads 取消置顶.
	view.Frame()
	if err := view.Click(l.ItemUnpin()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if a.store.Items[findItem(a.store.Items, "alpha capture")].Pinned {
		t.Fatal("the hover unpin did not clear the pin")
	}

	// Delete the second row from its hover button.
	row, ok = view.Find("beta capture")
	if !ok {
		t.Fatalf("second row did not draw: %v", view.Texts())
	}
	view.Move(row.X+row.W/2, row.Y+row.H/2)
	view.Frame()
	view.Frame()
	if err := view.Click(l.ItemRemove()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if slices.Contains(clipTexts(a), "beta capture") {
		t.Fatalf("the hover delete left the row: %v", clipTexts(a))
	}
	if rec.text != "" || a.selection.count() != 0 {
		t.Fatalf("hover delete leaked into the row: copy=%q selection=%d", rec.text, a.selection.count())
	}
}

func findItem(items []store.Item, text string) int {
	for i, it := range items {
		if it.Text == text {
			return i
		}
	}
	return -1
}

// A non-pinned tile clears itself on hover; a pinned tile keeps its badge
// and offers no ×, so the pin protects it.
func TestTrayTileHoverRemove(t *testing.T) {
	a, view, rec := clipShelf(t)
	l := locales.In("")
	for _, txt := range []string{"tray one", "tray two"} {
		if err := a.store.AddText(txt); err != nil {
			t.Fatal(err)
		}
	}
	view.Frame()
	if err := a.store.SetPinned(a.store.Items[findItem(a.store.Items, "tray two")].ID, true); err != nil {
		t.Fatal(err)
	}
	view.Frame()

	row, ok := view.Find("tray one")
	if !ok {
		t.Fatalf("tray tile did not draw: %v", view.Texts())
	}
	view.Move(row.X+row.W/2, row.Y+row.H/2)
	view.Frame()
	view.Frame()
	if !view.HasText(l.TrayRemove()) {
		t.Fatalf("hovering a tile shows no remove button: %v", view.Texts())
	}
	if err := view.Click(l.TrayRemove()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if slices.Contains(clipTexts(a), "tray one") {
		t.Fatalf("the tile × left the item: %v", clipTexts(a))
	}
	if rec.text != "" || a.selection.count() != 0 {
		t.Fatalf("tile × leaked into the tile: copy=%q selection=%d", rec.text, a.selection.count())
	}

	// The pinned tile shows its pin and never the remove button.
	pinned, ok := view.Find("tray two")
	if !ok {
		t.Fatalf("pinned tile did not draw: %v", view.Texts())
	}
	view.Move(pinned.X+pinned.W/2, pinned.Y+pinned.H/2)
	view.Frame()
	view.Frame()
	if view.HasText(l.TrayRemove()) {
		t.Fatal("a pinned tile offered its remove button")
	}
	if !view.HasText(l.ClipPinned()) {
		t.Fatal("a pinned tile lost its pin badge")
	}
}

func TestPolish1189ClipboardTrayShots(t *testing.T) {
	if os.Getenv("RAKKO_SHOTS") == "" {
		t.Skip("set RAKKO_SHOTS to write the clipboard and tray shots")
	}
	l := locales.In("")

	a, view, _ := clipShelf(t, "Dentist on Tuesday at 3pm", "#FF453A", "rgb(10, 132, 255)", "Shipping notes for the release")
	a.setTab(pageClipboard)
	view.SetScale(2)
	view.SetDark(true)
	view.Frame()
	if err := view.Click(l.PanelSearch()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	a.search = "dentist"
	view.Frame()
	row, ok := view.Find("Dentist on Tuesday at 3pm")
	if !ok {
		t.Fatalf("clipboard shot row missing: %v", view.Texts())
	}
	view.Move(row.X+row.W/2, row.Y+row.H/2)
	view.Frame()
	view.Frame()
	saveTestShot(t, "polish1189", "clipboard-page.png", view.Image())

	// The unfiltered page shows the colour swatches beside the ordinary
	// rows, with one row's hover actions out.
	a.search = ""
	view.Frame()
	row, ok = view.Find("Shipping notes for the release")
	if !ok {
		t.Fatalf("clipboard swatch shot row missing: %v", view.Texts())
	}
	view.Move(row.X+row.W/2, row.Y+row.H/2)
	view.Frame()
	view.Frame()
	saveTestShot(t, "polish1189", "clipboard-page-swatches.png", view.Image())

	b, tray, _ := clipShelf(t)
	for _, txt := range []string{"Design brief.pdf", "Screenshot 2026-11-26.png", "release-notes.zip"} {
		if err := b.store.AddText(txt); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.store.SetPinned(b.store.Items[findItem(b.store.Items, "Design brief.pdf")].ID, true); err != nil {
		t.Fatal(err)
	}
	tray.SetScale(2)
	tray.SetDark(true)
	tray.Frame()
	if err := tray.ClickWith(ui.Cmd, "release-notes.zip"); err != nil {
		t.Fatal(err)
	}
	tray.Frame()
	tile, ok := tray.Find("Screenshot 2026-11-26.png")
	if !ok {
		t.Fatalf("tray shot tile missing: %v", tray.Texts())
	}
	tray.Move(tile.X+tile.W/2, tile.Y+tile.H/2)
	tray.Frame()
	tray.Frame()
	if !tray.HasText(l.TrayRemove()) {
		t.Fatalf("tray shot shows no ×: %v", tray.Texts())
	}
	saveTestShot(t, "polish1189", "tray-page.png", tray.Image())
}
