//go:build darwin

package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/store"
)

// writeOtterPNG puts a solid-red picture 491 × 640 — the pet artwork's own
// size — in dir under the installed name, so the otter decodes to
// something findable in the pixels the row draws.
func writeOtterPNG(t *testing.T, dir, stem string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 491, 640))
	for i := range img.Pix {
		if i%4 == 3 {
			img.Pix[i] = 255
			continue
		}
		img.Pix[i] = [3]byte{210, 60, 40}[i%4]
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shelfu-otter_"+stem+"_v001.png"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// stageOtterArt installs the three catch poses for the test and points the
// loader at them; the cleanup drops the decoded pictures.
func stageOtterArt(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, pose := range []string{"pet_idle", "pet_catch_wait", "pet_catch_ready", "pet_caught"} {
		writeOtterPNG(t, dir, pose)
	}
	old := otterDir
	otterDir = func() string { return dir }
	t.Cleanup(func() { otterDir = old; resetOtters() })
	resetOtters()
}

// otterRowShelf is a shelf that can draw the tray row on its own.
func otterRowShelf(t *testing.T) *shelf {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return &shelf{
		store: s, dragging: true, visible: true,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
	}
}

// drawTray renders the 放入托盘 row alone, in a window its own size.
func drawTray(t *testing.T, a *shelf, paths []string) *image.RGBA {
	t.Helper()
	view := ui.NewTester(func(c *ui.Context) { a.dropTrayCell(c, paths) }, contentW, dropTrayH)
	return view.Image()
}

// redPixels counts the artwork's red in the rectangle x0..x1, y0..y1.
func redPixels(img *image.RGBA, x0, x1, y0, y1 int) int {
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 > 150 && g>>8 < 110 && b>>8 < 110 {
				n++
			}
		}
	}
	return n
}

// TestDropOtterPeeksUnderTheEdge: with the artwork installed the otter
// waits under the row's bottom edge — its head shows in the last ~32pt of
// the slot, and the row clips everything past that edge.
func TestDropOtterPeeksUnderTheEdge(t *testing.T) {
	stageOtterArt(t)
	a := otterRowShelf(t)
	img := drawTray(t, a, nil)
	slotX := contentW - dropOtterSlot
	if n := redPixels(img, slotX+4, contentW-dropOtterMargin-4, 0, dropTrayH-40); n != 0 {
		t.Fatalf("%d red pixels above the peek line: the otter stood up without a drag over it", n)
	}
	if n := redPixels(img, slotX+4, contentW-dropOtterMargin-4, dropTrayH-26, dropTrayH-2); n == 0 {
		t.Fatal("no otter head under the row's edge")
	}
	if n := redPixels(img, 0, slotX, 0, dropTrayH); n != 0 {
		t.Fatalf("%d red pixels left of the otter's slot", n)
	}
}

// TestDropOtterWithoutArt: a build without the pictures draws the row
// exactly as before — no otter, and no room kept for one.
func TestDropOtterWithoutArt(t *testing.T) {
	old := otterDir
	otterDir = func() string { return "" }
	t.Cleanup(func() { otterDir = old; resetOtters() })
	resetOtters()
	if dropOtterBase() != nil {
		t.Fatal("no artwork, yet an otter")
	}
	a := otterRowShelf(t)
	img := drawTray(t, a, nil)
	if n := redPixels(img, 0, contentW, 0, dropTrayH); n != 0 {
		t.Fatalf("%d red pixels without the artwork", n)
	}
}

// TestDropOtterPosesAndPlaces: the picture for the moment, and where its
// bottom sits: waiting (head to the eyes), up (paws) while the files hover
// the row, up while it holds them, and under the edge once the retract
// starts.
func TestDropOtterPosesAndPlaces(t *testing.T) {
	stageOtterArt(t)
	a := otterRowShelf(t)
	if got := a.dropOtterPose(false); got != petWait {
		t.Fatalf("waiting pose = %q", got)
	}
	if got := a.dropOtterPose(true); got != petReady {
		t.Fatalf("hovering pose = %q", got)
	}
	if got := a.dropOtterAt(time.Now(), false); got != dropOtterPeek {
		t.Fatalf("waiting bottom = %v, want %v", got, dropOtterPeek)
	}
	if got := a.dropOtterAt(time.Now(), true); got != dropOtterUp {
		t.Fatalf("hovering bottom = %v, want %v", got, dropOtterUp)
	}
	a.catchDrop(float32(contentW/2), float32(dropTrayH/2))
	if got := a.dropOtterPose(false); got != petCaught {
		t.Fatalf("caught pose = %q", got)
	}
	if got := a.dropOtterAt(time.Now(), false); got != dropOtterUp {
		t.Fatalf("holding bottom = %v, want %v", got, dropOtterUp)
	}
	_, h := dropOtterBox()
	if got := a.dropOtterAt(time.Now().Add(dropCatchFly+dropCatchHold), false); got != -h-2 {
		t.Fatalf("retracting bottom = %v, want %v", got, -h-2)
	}
}

// TestDropOtterSlotKeepsTheRowTextWhole: the room the otter keeps clear on
// the right must not cut or wrap the row's title and hint, in either
// language.
func TestDropOtterSlotKeepsTheRowTextWhole(t *testing.T) {
	stageOtterArt(t)
	for _, tag := range []string{"en", "zh-Hans"} {
		t.Run(tag, func(t *testing.T) {
			drain := fitSink(t)
			a := otterRowShelf(t)
			view := ui.NewTester(func(c *ui.Context) { a.dropTrayCell(c, nil) }, contentW, dropTrayH)
			view.SetLocale(tag)
			// The row is the tester's root here: its first frame lays it out
			// before the window's width reaches it, and measures text in
			// boxes the app never draws. The frames after it are the ones a
			// release draws.
			view.Frame()
			drain()
			view.Frame()
			if notes := drain(); len(notes) != 0 {
				t.Fatalf("the otter's slot cut the row: %v", notes)
			}
		})
	}
}

// TestDropCatchKeepsTheReleasePoint: the file in flight starts where the
// drag was let go, and falls back to the thumbnails' end when the drag
// left no usable point (a promised-file source, a headless drop).
func TestDropCatchKeepsTheReleasePoint(t *testing.T) {
	a := otterRowShelf(t)
	a.catchDrop(180, 30)
	if a.drop.catchX != 180 || a.drop.catchY != 30 {
		t.Fatalf("release point = (%d, %d), want (180, 30)", a.drop.catchX, a.drop.catchY)
	}
	a.drop.catchAt = time.Time{}
	a.catchDrop(0, 0)
	if a.drop.catchX >= 0 || a.drop.catchY >= 0 {
		t.Fatalf("no pointer, yet a point: (%d, %d)", a.drop.catchX, a.drop.catchY)
	}
}

// TestDropZoneHoldsThroughTheCatch: the file is in the tray the moment it
// is let go, but the zone stays laid out while the otter holds it, and
// goes with the last frame of the retract.
func TestDropZoneHoldsThroughTheCatch(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, _, _ := snapShelf(t, []string{img})
	if got := a.dropZoneSnap(true); len(got) != 1 {
		t.Fatal("no zone while dragging")
	}
	// The release: the drop landed on the row, the drag flag fell.
	a.dragging = false
	a.drop.done = true
	a.catchDrop(120, 30)
	if got := a.dropZoneSnap(true); len(got) != 1 {
		t.Fatal("the catch dropped the zone before the otter held the file")
	}
	a.drop.catchAt = time.Now().Add(-dropCatchFor - time.Millisecond)
	if got := a.dropZoneSnap(true); got != nil {
		t.Fatalf("the zone outlived the retract: %v", got)
	}
	if !a.drop.catchAt.IsZero() {
		t.Fatal("the finished catch stayed in the state")
	}
}

// TestDropCatchYieldsToANewDrag: a drag that starts while the otter holds
// the file is the user's now; the old catch never serves its set.
func TestDropCatchYieldsToANewDrag(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, _, _ := snapShelf(t, []string{img})
	a.dropZoneSnap(true)
	a.dragging = false
	a.drop.done = true
	a.catchDrop(120, 30)
	// A fresh drag, still loading its own files.
	a.dragSerial = a.drop.serial + 1
	a.dragging = true
	old := dragFiles
	dragFiles = func(*shelf) []string { return []string{img} }
	t.Cleanup(func() { dragFiles = old })
	if got := a.dropZoneSnap(true); len(got) != 1 || got[0] != img {
		t.Fatalf("the new drag's files = %v", got)
	}
	if !a.drop.catchAt.IsZero() {
		t.Fatal("the old catch held on under a new drag")
	}
}
