//go:build darwin

package main

// Real-window proof for the resting island capsule's hand-over: the
// picture window that covers the panel's blank resizes and the panel's own
// idle frame must draw the same capsule — same box, same corner path, same
// app entry — and the entry's hover fill must appear in both. Run with
// RAKKO_BASKET_SMOKE=1 through the native gate; shots land in
// /tmp/shelf-island.

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/platform"
	"rakko/internal/settings"
)

// capsuleShot captures one window's own content (no shadow, transparent
// corners kept) and decodes it.
func capsuleShot(t *testing.T, win *mygo.Window, name string) (image.Image, mygo.Rectangle) {
	t.Helper()
	if win == nil {
		t.Fatalf("%s: no window", name)
	}
	num := platform.WindowNumber(win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	if err := os.MkdirAll(islandShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(islandShotDir, name+".png")
	if out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput(); err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	img, err := pngDecode(path)
	if err != nil {
		t.Fatal(err)
	}
	var frame mygo.Rectangle
	onMain(func() { frame = win.Bounds() })
	t.Logf("shot %s -> %s (%v)", name, path, frame)
	return img, frame
}

// inkBox is the bounding box of the pixels brighter than threshold, used
// for the entry's glyph against the black capsule.
func inkBox(img image.Image, area image.Rectangle, threshold uint32) image.Rectangle {
	box := image.Rectangle{}
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a == 0 || r < threshold || g < threshold || b < threshold {
				continue
			}
			if box.Empty() {
				box = image.Rect(x, y, x+1, y+1)
				continue
			}
			box = box.Union(image.Rect(x, y, x+1, y+1))
		}
	}
	return box
}

// capsuleCrop is the capsule's pixel rect inside a captured window: the
// capsule sits at `origin` inside the window, in logical points.
func capsuleCrop(img image.Image, frame, origin mygo.Rectangle) image.Rectangle {
	scale := float64(img.Bounds().Dx()) / float64(frame.Width)
	minX := float64(origin.X-frame.X) * scale
	minY := float64(origin.Y-frame.Y) * scale
	return image.Rect(int(minX+0.5), int(minY+0.5),
		int(minX+float64(islandIdleW)*scale+0.5), int(minY+float64(islandIdleH)*scale+0.5))
}

// TestSmokeIslandCapsuleHandover captures the panel's idle frame and the
// picture window that covers its blank resizes at rest, and proves they
// draw the same capsule and the same app entry, hover included.
func TestSmokeIslandCapsuleHandover(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native island smoke test")
	}
	a := redesignShelf(t)
	islandStyle(t, a, settings.ShellStyleIsland)
	// Both captures are of this process's own windows, not a composited
	// screen rect: the installed Rakko's resting capsule or any other
	// overlay may sit in the same place without touching these shots.
	time.Sleep(400 * time.Millisecond)
	if !waitMain(2*time.Second, func() bool {
		return a.mainIdle != nil && a.mainIdle.shown
	}) {
		t.Fatal("the resting capsule's picture never came up")
	}
	var capsule mygo.Rectangle
	onMain(func() { capsule = a.mainIdle.frame })
	if want := a.islandRect(); capsule != want {
		t.Fatalf("picture frame %+v, want the island %+v", capsule, want)
	}

	// Both windows at rest.
	onMain(func() {
		a.win.Invalidate()
		a.mainIdle.win.Invalidate()
	})
	time.Sleep(250 * time.Millisecond)
	pic, _ := capsuleShot(t, a.mainIdle.win, "capsule-picture")
	panel, panelFrame := capsuleShot(t, a.win, "capsule-panel")

	picBox := opaqueBounds(pic)
	panelArea := capsuleCrop(panel, panelFrame, capsule)
	panelBox := opaqueBounds(cropImage(panel, panelArea))
	if d := picBox.Dx() - panelBox.Dx(); d < -2 || d > 2 {
		t.Fatalf("capsule width: picture %dpx, panel %dpx", picBox.Dx(), panelBox.Dx())
	}
	if d := picBox.Dy() - panelBox.Dy(); d < -2 || d > 2 {
		t.Fatalf("capsule height: picture %dpx, panel %dpx", picBox.Dy(), panelBox.Dy())
	}

	// Both boxes are read in their own capsule-relative space: the picture's
	// window is the capsule itself, the panel's capture was cropped to it.
	if dx, dy := panelBox.Min.X-picBox.Min.X, panelBox.Min.Y-picBox.Min.Y; dx < -2 || dx > 2 || dy < -2 || dy > 2 {
		t.Fatalf("capsule box: picture %v, panel %v (panel relative to its crop)", picBox, panelBox)
	}
	t.Logf("capsule %+v: picture %v; panel %v", capsule, picBox, panelBox)
}

// captureOnly takes a shot of one window for a second look at the same
// geometry.
func captureOnly(t *testing.T, win *mygo.Window, name string) image.Image {
	t.Helper()
	if win == nil {
		t.Fatalf("%s: no window", name)
	}
	num := platform.WindowNumber(win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	path := filepath.Join(islandShotDir, name+".png")
	if out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput(); err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	img, err := pngDecode(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("shot %s -> %s", name, path)
	return img
}

// cropImage returns the pixels of img inside area, on an image whose
// bounds start at the area's origin.
func cropImage(img image.Image, area image.Rectangle) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, area.Dx(), area.Dy()))
	for y := range area.Dy() {
		for x := range area.Dx() {
			dst.Set(x, y, img.At(area.Min.X+x, area.Min.Y+y))
		}
	}
	return dst
}
