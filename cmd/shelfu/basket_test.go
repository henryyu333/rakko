//go:build darwin

package main

import (
	"image"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
)

// TestMain keeps go test hermetic by default; with SHELF_BASKET_SMOKE=1 it
// runs the real app loop so the basket's native window can be inspected
// (CGWindowListCopyWindowInfo) and screenshotted. The test instance uses a
// temporary data dir and never touches ~/Library/Application Support/Rakko.
func TestMain(m *testing.M) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "shelf-fix-smoke-")
	if err != nil {
		panic(err)
	}
	s, err := store.Open(dir)
	if err != nil {
		panic(err)
	}
	smokeShelf = &shelf{store: s, dataDir: dir, stop: make(chan struct{}), thumbnails: make(map[string]*ui.Bitmap), thumbPending: map[string]bool{}}
	mygo.App.SetName(appName)
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.WhenReady(func() {
		if err := mac.Init(); err != nil {
			panic(err)
		}
		go func() {
			code := m.Run()
			mygo.App.Quit()
			os.Exit(code)
		}()
	})
	if err := mygo.App.Run(); err != nil {
		panic(err)
	}
}

var smokeShelf *shelf

// onMain runs fn on the app's main thread and waits for it. Only reachable
// in the smoke harness where the app loop is up.
func onMain(fn func()) {
	done := make(chan struct{})
	mygo.RunOnMain(func() {
		fn()
		close(done)
	})
	<-done
}

// TestBasketWindowShownAndPlaced proves the summoned card is a real window:
// WindowServer sees it, its frame is the basket placement rect, and it
// sits inside the display's visible area. Run with SHELF_BASKET_SMOKE=1.
func TestBasketWindowShownAndPlaced(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := smokeShelf
	cursor := mygo.Screen.CursorScreenPoint()
	onMain(func() {
		// Place near the cursor the way a shake at that point would.
		a.showBasket(cursor)
	})
	if a.basket.win == nil {
		t.Fatal("basket window was not created")
	}
	defer onMain(a.closeBasket)
	want := a.basket.frame
	num := mac.WindowNumber(a.basket.win.NativeHandle())
	if num == 0 {
		t.Fatal("basket window has no CGWindowID")
	}
	t.Logf("basket CGWindowID=%d frame=%+v — screencapture -l %d shots it", num, want, num)
	// SHELF_BASKET_HOLD=1 keeps the card up so an external
	// `screencapture -l <id>` can run against the live window.
	if os.Getenv("SHELF_BASKET_HOLD") != "" {
		time.Sleep(8 * time.Second)
	}
	// WindowServer can report a transient frame while the window orders in;
	// poll until it matches the requested rect.
	var got mac.NSRect
	match := false
	for i := range 60 {
		onMain(func() {
			var ok bool
			got, ok = mac.WindowScreenRect(num)
			match = ok && int(got.Size.Width) == want.Width && int(got.Size.Height) == want.Height &&
				int(got.Origin.X) == want.X && int(got.Origin.Y) == want.Y
		})
		if match {
			break
		}
		if i == 59 {
			t.Fatalf("window %d never settled to %+v; last seen %+v", num, want, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
	onMain(func() {
		t.Logf("mygo Bounds=%+v", a.basket.win.Bounds())
	})
}

// TestBasketFrameStaysInsideVisible clamps the card into the display's
// visible frame: never under the menu bar, never off-screen.
func TestBasketFrameStaysInsideVisible(t *testing.T) {
	vis := mygo.Rectangle{X: 0, Y: 25, Width: 1512, Height: 920}
	fits := func(r mygo.Rectangle) bool {
		return r.X >= vis.X && r.Y >= vis.Y &&
			r.X+r.Width <= vis.X+vis.Width && r.Y+r.Height <= vis.Y+vis.Height
	}
	for _, c := range []mygo.Point{
		{X: 100, Y: 100},  // middle: card lands down-right
		{X: 1480, Y: 900}, // bottom-right corner: flips left/above
		{X: 0, Y: 25},     // top-left corner
		{X: 1510, Y: 25},  // top-right corner
		{X: 700, Y: 26},   // just under the menu bar
		{X: 1490, Y: 919}, // one point of room left/down
	} {
		if r := basketFrame(vis, c); !fits(r) {
			t.Fatalf("cursor %+v produced out-of-bounds frame %+v", c, r)
		}
	}
	// A display whose WorkArea starts away from the origin (secondary
	// screen above/beside the main one) still clamps inside itself.
	vis2 := mygo.Rectangle{X: -1440, Y: -1080, Width: 1440, Height: 1030}
	for _, c := range []mygo.Point{{X: -1400, Y: -1070}, {X: -20, Y: -60}} {
		r := basketFrame(vis2, c)
		if r.X < vis2.X || r.Y < vis2.Y || r.X+r.Width > vis2.X+vis2.Width || r.Y+r.Height > vis2.Y+vis2.Height {
			t.Fatalf("secondary display: cursor %+v produced out-of-bounds frame %+v", c, r)
		}
	}
}

// TestBasketViewStates covers the card's three visual states headlessly:
// idle prompt, then the confirmation after basketReceive.
func TestBasketViewStates(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s}
	view := ui.NewTester(a.basketView, basketWidth, basketHeight)
	if !view.HasText("放到这里") {
		t.Fatal("basket card does not show the drop prompt")
	}
	a.basketReceive(transfer.TextData("hello"))
	if len(s.Items) != 1 || s.Items[0].Text != "hello" {
		t.Fatal("drop on the basket did not reach the store")
	}
	view.Frame()
	if !view.HasText("已放入 Rakko") {
		t.Fatal("basket card does not show the confirmation")
	}
	if a.basket.hideAt.IsZero() {
		t.Fatal("confirmation did not arm the auto-close timer")
	}
}

// TestBasketViewFailedIntake: a drop the store rejects (here a malformed
// PNG) must surface as a failure in the card, never as "已放入 Rakko".
func TestBasketViewFailedIntake(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s}
	a.basketReceive(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, []byte("not a png")))))
	if len(s.Items) != 0 {
		t.Fatal("a malformed PNG was stored")
	}
	if !a.basket.failed || a.basket.dropped {
		t.Fatal("failed intake marked the card as confirmed")
	}
	if a.basket.hideAt.IsZero() {
		t.Fatal("failed intake did not arm the display timer")
	}
	view := ui.NewTester(a.basketView, basketWidth, basketHeight)
	if view.HasText("已放入 Rakko") {
		t.Fatal("failed intake still shows the success confirmation")
	}
	if !view.HasText("放入失败") {
		t.Fatal("failed intake does not surface in the card")
	}
}

// TestBasketPendingCloseKeepsAcceptedDrop is the regression test for the
// pending-close window: a drop accepted by the card must reach the store
// even when the pointer has already left and the monitor has requested
// the close — the close only takes effect on the next render, after
// DropData has been consumed.
func TestBasketPendingCloseKeepsAcceptedDrop(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s}
	a.basket = basketState{shown: true, frame: mygo.Rectangle{X: 100, Y: 100, Width: basketWidth, Height: basketHeight}}
	// Drag ends with the pointer already outside the card: the monitor
	// requests the close instead of destroying the window outright.
	a.dragging = false
	a.basketTick(time.Now(), mygo.Point{X: 900, Y: 900})
	if !a.basket.closing {
		t.Fatal("drag end with the pointer outside did not request a close")
	}
	if !a.basket.shown {
		t.Fatal("the close destroyed the card before its last render")
	}
	// The acknowledged drop lands during the closing render.
	a.basketReceive(transfer.TextData("late drop"))
	if a.basket.closing {
		t.Fatal("an incoming drop failed to cancel the pending close")
	}
	if len(s.Items) != 1 || s.Items[0].Text != "late drop" {
		t.Fatal("the accepted drop was lost to the pending close")
	}
	if !a.basket.dropped || !a.basket.shown {
		t.Fatal("the card did not switch to its confirmation state")
	}
}

// TestBasketPendingCloseRendersAway: with no drop arriving, the requested
// close completes on the next render and releases the card.
func TestBasketPendingCloseRendersAway(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s}
	a.basket = basketState{shown: true, frame: mygo.Rectangle{X: 100, Y: 100, Width: basketWidth, Height: basketHeight}}
	view := ui.NewTester(a.basketView, basketWidth, basketHeight)
	a.basketTick(time.Now(), mygo.Point{X: 900, Y: 900})
	if !a.basket.closing {
		t.Fatal("drag end with the pointer outside did not request a close")
	}
	view.Frame()
	if a.basket.shown {
		t.Fatal("the pending close never took effect")
	}
}

// opaqueBounds is the bounding box of the non-transparent pixels; the gated
// smoke tests use it to size up real-window screenshots.
func opaqueBounds(img image.Image) image.Rectangle {
	b := img.Bounds()
	box := image.Rectangle{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, alpha := img.At(x, y).RGBA(); alpha > 0 {
				if box.Empty() {
					box = image.Rect(x, y, x+1, y+1)
					continue
				}
				if x < box.Min.X {
					box.Min.X = x
				}
				if y < box.Min.Y {
					box.Min.Y = y
				}
				if x+1 > box.Max.X {
					box.Max.X = x + 1
				}
				if y+1 > box.Max.Y {
					box.Max.Y = y + 1
				}
			}
		}
	}
	return box
}
