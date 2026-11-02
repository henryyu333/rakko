//go:build darwin

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"shelf/internal/mac"
)

// TestNowPlayingPeekSmokeScreenshot draws the now-playing hover wings in
// the real notch window, screenshots it with `screencapture -l <id>` and
// checks the picture: the drawn pill must reach the wings' width (notch
// plus 52pt on each side) and must contain the cover's colours. Run with
// SHELF_BASKET_SMOKE=1 (see basket_test.go).
func TestNowPlayingPeekSmokeScreenshot(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native now-playing smoke test")
	}
	a := smokeShelf
	if a.win == nil {
		onMain(func() { a.newNotchWindow() })
	}
	if a.win == nil {
		t.Fatal("no notch window")
	}
	// A test cover: a 64×64 four-quadrant image, easy to spot in the shot
	// without touching any real library.
	art := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			switch {
			case x < 32 && y < 32:
				art.Set(x, y, color.RGBA{R: 220, G: 40, B: 90, A: 255})
			case x >= 32 && y < 32:
				art.Set(x, y, color.RGBA{R: 240, G: 160, B: 30, A: 255})
			case x < 32:
				art.Set(x, y, color.RGBA{R: 40, G: 170, B: 220, A: 255})
			default:
				art.Set(x, y, color.RGBA{R: 120, G: 220, B: 120, A: 255})
			}
		}
	}
	mod := &nowPlayingModule{}
	onMain(func() {
		a.np = mod
		mod.host = a
		mod.rememberArt(art, false) // same path as decodeArt: bitmap + glow
		mod.have = true
		mod.last = mac.NowPlayingState{
			Playing: true, Title: "Fashion Killa", Artist: "A$AP Rocky",
			Duration: 236, Elapsed: 28, Rate: 1, Received: time.Now(),
		}
		a.place(mygo.Screen.CursorScreenPoint())
		a.peeking = true
		a.peekProgress, a.peekFrom = 1, 1
		a.peekAnimStart = time.Time{}
		a.syncMousePassThrough()
		a.win.ShowInactive()
		a.win.Invalidate()
	})
	// No controls to find anymore: the draw is verified by the
	// screenshot below. One extra invalidate lets a frame settle.
	onMain(func() { a.win.Invalidate() })
	time.Sleep(200 * time.Millisecond)
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatal("notch window has no CGWindowID")
	}
	time.Sleep(250 * time.Millisecond) // let the shot see the settled frame
	shot := filepath.Join(os.TempDir(), "shelf-nowplaying-peek-smoke.png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("screencapture failed (%v): %s", err, out)
	}
	f, err := os.Open(shot)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatalf("decode %s: %v", shot, err)
	}
	box := opaqueBounds(img)
	var wantW float32
	onMain(func() { wantW, _ = a.peekSize() })
	if box.Dx() < int(wantW) {
		t.Fatalf("screenshot %s shows %dpt of paint; the wings must be %.0fpt wide", shot, box.Dx(), wantW)
	}
	if !hasColour(img) {
		t.Fatalf("screenshot %s has no cover colours; the artwork did not draw", shot)
	}
	t.Logf("now-playing peek CGWindowID=%d shot=%s drawn=%v", num, shot, box)
}

// hasColour reports whether any pixel is clearly coloured (the cover),
// rather than the black shell or the transparent desktop.
func hasColour(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			maxc := max(r, max(g, bl))
			minc := min(r, min(g, bl))
			if maxc > 0x6000 && maxc-minc > 0x3000 {
				return true
			}
		}
	}
	return false
}
