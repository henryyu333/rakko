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

	// Both windows at rest, hover off.
	onMain(func() {
		a.idleEntryHover = false
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

	// The app entry's glyph sits in the same place in both.
	glyph := func(img image.Image, area image.Rectangle) image.Rectangle {
		return inkBox(img, area, 0x6000)
	}
	picGlyph := glyph(pic, picBox)
	// The panel's capture is its whole window; the glyph's place there is
	// read in the capsule crop's own space.
	panelGlyph := glyph(panel, panelArea).Sub(panelArea.Min)
	if dx, dy := picGlyph.Min.X-panelGlyph.Min.X, picGlyph.Min.Y-panelGlyph.Min.Y; dx < -2 || dx > 2 || dy < -2 || dy > 2 {
		t.Fatalf("entry glyph moved between the picture and the panel: %+v vs %+v", picGlyph, panelGlyph)
	}
	t.Logf("capsule %+v: picture %v glyph %v; panel %v glyph %v", capsule,
		picBox, picGlyph, panelBox, panelGlyph)

	// Both boxes are read in their own capsule-relative space: the picture's
	// window is the capsule itself, the panel's capture was cropped to it.
	if dx, dy := panelBox.Min.X-picBox.Min.X, panelBox.Min.Y-picBox.Min.Y; dx < -2 || dx > 2 || dy < -2 || dy > 2 {
		t.Fatalf("capsule box: picture %v, panel %v (panel relative to its crop)", picBox, panelBox)
	}

	// Hover on: the entry's fill appears in both windows, and nothing else
	// of the capsule changes. redAt reads one pixel's red channel: the fill
	// is a white opacity over the black capsule, the body stays black.
	redAt := func(img image.Image, at image.Point) uint32 {
		r, _, _, _ := img.At(at.X, at.Y).RGBA()
		return r
	}
	// Both captures are of the same 120 × 30 capsule: one offset inside it
	// lands on the same point in each, scaled by its own backing.
	picScale := float64(pic.Bounds().Dx()) / islandIdleW
	panelScale := float64(panel.Bounds().Dx()) / float64(panelFrame.Width)
	// Four points inside the entry's left edge, three points left of the
	// glyph: fill only, at the capsule's mid-height.
	entryOffset := image.Pt(int((islandIdleW-4-appEntryW+4)*picScale), int(float64(islandIdleH)*picScale/2))
	entryPoint := picBox.Min.Add(entryOffset)
	panelEntryPoint := panelArea.Min.Add(image.Pt(
		int((islandIdleW-4-appEntryW+4)*panelScale), int(float64(islandIdleH)*panelScale/2)))
	bodyPoint := picBox.Min.Add(image.Pt(picBox.Dx()/10, picBox.Dy()/2))
	bodyBefore := redAt(pic, bodyPoint)

	onMain(func() {
		a.idleEntryHover = true
		a.win.Invalidate()
		a.mainIdle.win.Invalidate()
	})
	time.Sleep(250 * time.Millisecond)
	pic2 := captureOnly(t, a.mainIdle.win, "capsule-picture-hover")
	panel2 := captureOnly(t, a.win, "capsule-panel-hover")

	r0 := redAt(pic, entryPoint)
	r1 := redAt(pic2, entryPoint)
	if r1 <= r0 {
		t.Fatalf("the picture's entry did not brighten under hover: %d -> %d", r0, r1)
	}
	pr := redAt(panel, panelEntryPoint)
	pr2 := redAt(panel2, panelEntryPoint)
	if pr2 <= pr {
		t.Fatalf("the panel's entry did not brighten under hover: %d -> %d", pr, pr2)
	}
	if got := redAt(pic2, bodyPoint); got != bodyBefore {
		t.Fatalf("the hover repainted the capsule body: %d -> %d", bodyBefore, got)
	}

	onMain(func() {
		a.idleEntryHover = false
		a.win.Invalidate()
		a.mainIdle.win.Invalidate()
	})
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
