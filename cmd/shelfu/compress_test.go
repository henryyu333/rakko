//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

// sizedShelf is convertShelf with file bodies of the given sizes, so the
// menu's already-small tiers grey and the planner can skip them.
func sizedShelf(t *testing.T, sizes map[string]int64, names ...string) (*shelf, []string) {
	t.Helper()
	a, _, _ := convertShelf(t)
	dir := t.TempDir()
	var paths []string
	for i := len(names) - 1; i >= 0; i-- {
		p := filepath.Join(dir, names[i])
		if err := os.WriteFile(p, make([]byte, sizes[names[i]]), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := a.store.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
		paths = append([]string{p}, paths...)
	}
	return a, paths
}

// 「压缩到」offers every tier a selection still exceeds and greys the ones
// it already fits; a tier shrinks each picture into 「名（压缩）.ext」 with
// its format kept by extension and alpha.
func TestCompressToTier(t *testing.T) {
	f := fakeConverters(t)
	f.alpha = map[string]bool{"logo.png": true}
	a, _ := sizedShelf(t, map[string]int64{"a.jpg": 2_100_000, "logo.png": 3_500_000}, "a.jpg", "logo.png")
	selectAll(a)
	a.win = nil
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	if err := view.RightClick("a.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("压缩到", "5 MB 以内"); err == nil && len(f.fit) > 0 {
		t.Fatal("a tier every picture already fits must not run")
	}
	view.CloseMenu()
	if err := view.RightClick("a.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("压缩到", "2 MB 以内"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"a（压缩）.jpg", "logo（压缩）.png"}) {
		t.Fatalf("outputs = %v", got)
	}
	if !slices.Equal(f.formats, []mac.ImageFormat{mac.FormatJPEG, mac.FormatPNG}) {
		t.Fatalf("formats = %v, want JPEG for a.jpg and PNG for the alpha logo", f.formats)
	}
	if a.store.Items[0].Path == "" || a.store.Items[0].Kind != "file" {
		t.Fatal("outputs must be file items")
	}
	if !strings.HasPrefix(a.status, "已压缩 2 张") {
		t.Fatalf("status = %q", a.status)
	}
}

// A picture already under the budget produces no output and is reported
// as skipped; when every picture fits, no job runs.
func TestCompressSkipsAlreadySmall(t *testing.T) {
	f := fakeConverters(t)
	f.fitBytes = 400_000
	a, _ := sizedShelf(t, map[string]int64{"small.jpg": 300_000, "big.jpg": 2_000_000}, "small.jpg", "big.jpg")
	selectAll(a)
	a.win = nil
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	if err := view.RightClick("small.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("压缩到", "1 MB 以内"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"big（压缩）.jpg"}) {
		t.Fatalf("outputs = %v, the small one must be skipped", got)
	}
	if len(f.fit) != 1 {
		t.Fatalf("fit ran %d times, want 1", len(f.fit))
	}
	if !strings.Contains(a.status, "1 张本来就小于目标") {
		t.Fatalf("status = %q, want the skip reported", a.status)
	}

	view.Frame()
	if err := view.RightClick("small.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("压缩到", "500 KB 以内"); err == nil && len(f.fit) > 1 {
		t.Fatal("a lone small picture must not offer a compressible tier")
	}
}

// A picture that cannot reach the budget still produces its smallest
// result and the status says so.
func TestCompressStillOverLimit(t *testing.T) {
	f := fakeConverters(t)
	f.fitBytes = 600_000
	a, _ := sizedShelf(t, map[string]int64{"a.jpg": 2_000_000}, "a.jpg")
	selectAll(a)
	a.win = nil
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	if err := view.RightClick("a.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("压缩到", "500 KB 以内"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"a（压缩）.jpg"}) {
		t.Fatalf("outputs = %v, the smallest result must still be written", got)
	}
	if !strings.Contains(a.status, "没能压到") {
		t.Fatalf("status = %q, want the shortfall reported", a.status)
	}
}
