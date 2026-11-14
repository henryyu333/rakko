//go:build darwin

package platform

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// noiseImage renders a w×h bitmap of random-coloured blocks — the kind of
// content that does not compress away, so quality and size actually trade
// off — and writes it to path as the given UTI.
func noiseImage(t *testing.T, path string, w, h int, uti string, orientation int) {
	t.Helper()
	img := renderFixture(t, w, h, func(ctx uintptr) {
		rng := rand.New(rand.NewPCG(42, 0))
		for y := 0; y < h; y += 8 {
			for x := 0; x < w; x += 8 {
				cgCtxSetFill(ctx, rng.Float64(), rng.Float64(), rng.Float64(), 1)
				cgCtxFillRect(ctx, NSRect{
					Origin: NSPoint{X: float64(x), Y: float64(y)},
					Size:   NSSize{Width: 8, Height: 8},
				})
			}
		}
	})
	defer cfRelease(img)
	writeEncoded(t, path, img, uti, orientation)
	if fi, err := os.Stat(path); err != nil || fi.Size() < 1000 {
		t.Fatalf("noise fixture %s too small to exercise fitting: %v", path, err)
	}
}

// An alpha-free JPEG reports no transparency; a PNG drawn in a premultiplied
// context keeps its alpha channel.
func TestImageAlpha(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	jpg := filepath.Join(dir, "n.jpg")
	png := filepath.Join(dir, "a.png")
	noiseImage(t, jpg, 200, 100, "public.jpeg", 0)
	noiseImage(t, png, 200, 100, "public.png", 0)
	if a, err := ImageAlpha(jpg); err != nil || a {
		t.Fatalf("ImageAlpha(jpeg) = %v, %v; want false", a, err)
	}
	if a, err := ImageAlpha(png); err != nil || !a {
		t.Fatalf("ImageAlpha(png) = %v, %v; want true", a, err)
	}
	if _, err := ImageAlpha(filepath.Join(dir, "gone.png")); err == nil {
		t.Fatal("a missing image must fail")
	}
}

// A big noisy JPEG fits a reachable limit without shrinking; a limit nothing
// can reach still writes the smallest result and reports over.
func TestFitImageJPEG(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "n.jpg")
	noiseImage(t, src, 1600, 1200, "public.jpeg", 0)

	dst := filepath.Join(dir, "fit.jpg")
	over, err := FitImage(src, dst, FormatJPEG, 500_000)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(dst)
	if over {
		t.Fatalf("over=true at 500 KB for a %d-byte source", fi.Size())
	}
	if fi.Size() > 500_000 {
		t.Fatalf("fitted size %d exceeds 500 KB", fi.Size())
	}
	// Quality alone should have done it: the picture stays full-size.
	if w, h, err := ImageSize(dst); err != nil || w != 1600 || h != 1200 {
		t.Fatalf("size = %dx%d (%v), want 1600x1200", w, h, err)
	}

	small := filepath.Join(dir, "tiny.jpg")
	over, err = FitImage(src, small, FormatJPEG, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if !over {
		t.Fatal("a 10 KB limit on this noise must report over")
	}
	w, h, err := ImageSize(small)
	if err != nil {
		t.Fatal(err)
	}
	if max(w, h) > fitMinPx {
		t.Fatalf("smallest output = %dx%d, longer side must cap at %d", w, h, fitMinPx)
	}
	if fi, _ := os.Stat(small); fi.Size() == 0 {
		t.Fatal("the smallest result must still be written")
	}
}

// PNG only ever shrinks — a reachable limit keeps the pixels and a hopeless
// one lands at the 640 px floor — and a rotated JPEG comes out upright.
func TestFitImagePNGAndOrientation(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	png := filepath.Join(dir, "n.png")
	noiseImage(t, png, 900, 600, "public.png", 0)

	dst := filepath.Join(dir, "fit.png")
	if _, err := FitImage(png, dst, FormatPNG, 4_000_000); err != nil {
		t.Fatal(err)
	}
	if w, h, err := ImageSize(dst); err != nil || w != 900 || h != 600 {
		t.Fatalf("size = %dx%d (%v), want 900x600", w, h, err)
	}
	if _, err := FitImage(png, filepath.Join(dir, "tiny.png"), FormatPNG, 1_000); err != nil {
		t.Fatal(err)
	}
	if w, h, err := ImageSize(filepath.Join(dir, "tiny.png")); err != nil || max(w, h) > fitMinPx {
		t.Fatalf("size = %dx%d (%v), longer side must cap at %d", w, h, err, fitMinPx)
	}

	rot := filepath.Join(dir, "rot.jpg")
	noiseImage(t, rot, 1200, 800, "public.jpeg", 6)
	out := filepath.Join(dir, "rot-fit.jpg")
	if _, err := FitImage(rot, out, FormatJPEG, 500_000); err != nil {
		t.Fatal(err)
	}
	if w, h, err := ImageSize(out); err != nil || w != 800 || h != 1200 {
		t.Fatalf("upright size = %dx%d (%v), want 800x1200", w, h, err)
	}
	if o := orientationOf(t, out); o != 0 && o != 1 {
		t.Fatalf("output keeps an orientation flag: %d", o)
	}
}
