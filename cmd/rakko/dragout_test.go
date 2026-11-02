//go:build darwin

package main

// Tests for the readable file name a clipboard screenshot is dragged out
// under, and for the stale-link cleanup that keeps the scratch folder
// bounded.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/internal/store"
)

// imageShelf returns a shelf owning one managed screenshot plus its bytes.
func imageShelf(t *testing.T) (*shelf, store.Item, []byte) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pngBytes := testPNG(t)
	if err := s.AddImage(pngBytes); err != nil {
		t.Fatal(err)
	}
	a := &shelf{store: s, dataDir: dir, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	for _, it := range s.Items {
		if it.Kind == store.KindImage {
			return a, it, pngBytes
		}
	}
	t.Fatal("the store did not keep the image")
	return nil, store.Item{}, nil
}

// A dragged-out screenshot must offer the tray's readable name — with the
// time's colons written as dots — and the very same bytes, while the
// content-addressed original stays untouched.
func TestScreenshotDragOutUsesTrayName(t *testing.T) {
	a, item, pngBytes := imageShelf(t)
	data := a.dragData(item)
	files, err := data.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("drag carried %d file URLs, want 1: %v", len(files), files)
	}
	wantName := "截图 " + item.CreatedAt.Local().Format("01-02 15.04.05") + ".png"
	if got := filepath.Base(files[0]); got != wantName {
		t.Fatalf("drag-out name = %q, want %q", got, wantName)
	}
	if got, want := filepath.Dir(files[0]), filepath.Join(a.dataDir, dragOutDir); got != want {
		t.Fatalf("drag-out URL lives in %q, want the scratch folder %q", got, want)
	}
	got, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pngBytes) {
		t.Fatal("the drag-out file does not hold the screenshot's bytes")
	}
	orig, err := os.ReadFile(item.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, pngBytes) {
		t.Fatal("the managed images/ file changed")
	}
	if filepath.Base(item.Path) == wantName {
		t.Fatalf("managed file %q must keep its sha name", filepath.Base(item.Path))
	}
	// The PNG representation keeps coming from the managed file.
	pngRep, err := data.Read(transfer.PNG)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pngRep, pngBytes) {
		t.Fatal("the PNG representation changed")
	}
}

// twoImageShelf returns a shelf with two distinct screenshots — both with
// the same displayed second so their readable names collide — plus the
// view, the items in store order (newest first) and a bytes map by ID.
func twoImageShelf(t *testing.T) (*shelf, *ui.Tester, *recordClip, []store.Item, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pngs := map[string][]byte{}
	for _, mk := range []func() []byte{func() []byte { return testPNG(t) }, func() []byte { return bluePNG(t) }} {
		b := mk()
		if err := s.AddImage(b); err != nil {
			t.Fatal(err)
		}
		pngs[s.Items[0].ID] = b
	}
	rec := &recordClip{}
	a := &shelf{
		store: s, dataDir: dir, visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{}, write: rec,
	}
	// Force the same second: two screenshots taken within one second of
	// each other would display the same name and must not share a URL.
	same := time.Now().Local()
	s.Items[0].CreatedAt = same
	s.Items[1].CreatedAt = same
	return a, ui.NewTester(a.view, sheetW, panelTestH), rec, s.Items, pngs
}

// bluePNG is a second, distinct fixture so AddImage does not dedupe it.
func bluePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := range 4 {
		for y := range 4 {
			img.Set(x, y, color.RGBA{R: 20, G: 60, B: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Each drag-out clears links older than an hour and leaves newer ones (a
// drag may still be reading them) alone.
func TestDragOutClearsOnlyStaleLinks(t *testing.T) {
	a, item, _ := imageShelf(t)
	dir := filepath.Join(a.dataDir, dragOutDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "stale.png")
	fresh := filepath.Join(dir, "fresh.png")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * dragOutLife)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	a.dragOut(item)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a link older than %v survived the next drag-out", dragOutLife)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("a fresh link was removed: %v", err)
	}
}

// Screenshots older than dragOutLife must still be draggable: their
// readable names are copies with their own inode and mtime, not hard
// links that inherit the managed file's age. (Regression test: the
// hard-link version let the sweep delete a just-offered URL.)
func TestOldScreenshotDragOutsSurviveTheSweep(t *testing.T) {
	a, _, _, items, pngs := twoImageShelf(t)
	i1, i2 := items[0], items[1]
	old := time.Now().Add(-2 * dragOutLife)
	for _, it := range a.store.Items {
		if err := os.Chtimes(it.Path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	// Program the multi-selection directly: both tiles show the same
	// same-second name, so a ⌘-click by label cannot reach the second.
	a.selection.onlyAll([]string{i1.ID, i2.ID})
	files, err := a.dragOut(i1).Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("a two-item drag carried %v", files)
	}
	for k, it := range []store.Item{i1, i2} {
		got, err := os.ReadFile(files[k])
		if err != nil {
			t.Fatalf("drag-out URL %q is already gone: %v", files[k], err)
		}
		if !bytes.Equal(got, pngs[it.ID]) {
			t.Fatalf("%q does not hold its picture's bytes", files[k])
		}
	}
	if files[0] == files[1] {
		t.Fatalf("two screenshots offered the same path %q", files[0])
	}
	// Same-second names differ only by the unique-path suffix.
	if filepath.Base(files[1]) == filepath.Base(files[0]) {
		t.Fatalf("same-second screenshots collide at %q", files[0])
	}
	if !strings.HasSuffix(filepath.Base(files[1]), " 2.png") {
		t.Fatalf("second drag-out = %q, want the tray name plus 「 2」", filepath.Base(files[1]))
	}
	for _, it := range a.store.Items {
		if b, err := os.ReadFile(it.Path); err != nil || len(b) == 0 {
			t.Fatalf("managed original %q touched: %v", it.Path, err)
		}
		if info, err := os.Stat(it.Path); err != nil || !info.ModTime().Equal(old) {
			t.Fatalf("managed original %q was restamped", it.Path)
		}
	}
}

// Without a data directory (bare test shelves) the drag falls back to the
// managed path instead of inventing a scratch location.
func TestDragOutWithoutDataDirFallsBack(t *testing.T) {
	a, item, _ := imageShelf(t)
	a.dataDir = ""
	files, err := a.dragData(item).Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != item.Path {
		t.Fatalf("drag without a data dir carried %v, want the managed path %q", files, item.Path)
	}
}

// Dragging the same picture again reuses its link: the scratch folder must
// not accumulate 「 2」, 「 3」 names for the same screenshot.
func TestRepeatedScreenshotDragReusesLink(t *testing.T) {
	a, item, _ := imageShelf(t)
	first, err := a.dragData(item).Files()
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.dragData(item).Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 || first[0] != second[0] {
		t.Fatalf("repeated drags carried %v then %v, want the same link", first, second)
	}
	entries, err := os.ReadDir(filepath.Join(a.dataDir, dragOutDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("scratch folder holds %d files after two drags, want 1", len(entries))
	}
}
