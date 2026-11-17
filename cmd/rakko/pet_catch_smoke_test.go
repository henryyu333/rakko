//go:build darwin

package main

// Real-window proof for the desktop otter's two states that only unit
// tests covered: the ready pose a drag held over it draws, and the panel
// staying open while it keeps a file it just caught. Run with
// RAKKO_BASKET_SMOKE=1 through the native gate; shots land in
// /tmp/rakko-pet.

import (
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/settings"
)

// petReadyShelf starts the otter the way the app does and leaves it
// standing at its default spot.
func petReadyShelf(t *testing.T) *shelf {
	t.Helper()
	smokeArt(t)
	a := redesignShelf(t)
	onMain(func() {
		if a.win != nil {
			a.hide(hideExplicit)
		}
		a.settings.Pet = settings.PetSettings{Shown: true, Size: settings.PetSizeMedium}
		a.settings.PetPos = nil
		a.settings.PetDisplay = ""
		a.closePet()
		a.petStart()
		if a.pet == nil {
			t.Fatal("the otter did not start")
		}
		primary := mygo.Screen.PrimaryDisplay()
		w, h := petBox(petPictureH(settings.PetSizeMedium))
		a.pet.display = primary.ID
		a.pet.frame = petDefaultSpot(primary.WorkArea, w, h)
		a.pet.slideAt = time.Time{}
		a.pet.applyFrame()
	})
	t.Cleanup(func() {
		onMain(func() {
			a.dragging, a.ownDrag = false, false
			a.closePet()
		})
	})
	time.Sleep(400 * time.Millisecond)
	if a.pet == nil || a.pet.win == nil {
		t.Fatal("the otter window did not open")
	}
	return a
}

// TestSmokePetPoseOnARealDrag drives the poses the monitor and the view
// pick for a drag held over the otter on the real window. The ready pose
// itself is the view's branch for a system drag the window is the
// destination of (ui.DataDragOver): a test process cannot open one, so the
// shot here is the in-flight pose the view draws for a drag it cannot see
// as its own, and the ready decision is checked on the state the monitor
// feeds. Rakko's own drag-out is pinned as well: it is not a drop the otter
// catches, so it draws no invitation for one.
func TestSmokePetPoseOnARealDrag(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to capture the desktop otter")
	}
	a := petReadyShelf(t)
	onMain(func() {
		pet := a.pet
		at := mygo.Point{X: pet.frame.X + pet.frame.Width/2, Y: pet.frame.Y + pet.frame.Height/2}
		a.dragging, a.ownDrag = true, false
		a.petTick(time.Now(), at, false)
	})
	time.Sleep(250 * time.Millisecond)
	// The monitor's own answer for a drag held over the otter.
	if pose := a.petPoseFor(time.Now(), true); pose != petReady {
		t.Fatalf("a drag held over the otter decides %q, want %q", pose, petReady)
	}
	petShot(t, a.pet.win, "pet-drag-in-flight")

	// Our own drag-out: the otter never catches it, so the view draws no
	// ready pose and keeps no caught one.
	onMain(func() {
		pet := a.pet
		at := mygo.Point{X: pet.frame.X + pet.frame.Width/2, Y: pet.frame.Y + pet.frame.Height/2}
		a.ownDrag = true
		a.petTick(time.Now(), at, false)
	})
	time.Sleep(250 * time.Millisecond)
	if pose := a.pet.drawn; pose == petReady || pose == petCaught {
		t.Fatalf("our own drag-out drew %q", pose)
	}
	petShot(t, a.pet.win, "pet-own-drag")
	onMain(func() { a.dragging, a.ownDrag = false, false })
}

// TestSmokeDropCatchDefersTheHide keeps the panel open for the whole catch
// on the real window: the press outside during it waits, the frames keep
// coming, and the panel folds when the otter's retract ends.
func TestSmokeDropCatchDefersTheHide(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to capture the drop zone")
	}
	smokeArt(t)
	a := redesignShelf(t)
	fix := t.TempDir()
	pic := writeSmokePNG(t, fix, "catch.png", 320, 240)
	t.Setenv("RAKKO_DEBUG_DROP_FILES", pic)
	t.Cleanup(func() {
		onMain(func() {
			a.drop = dropZoneState{}
			a.hide(hideExplicit)
		})
		a.drop = dropZoneState{}
	})
	if dropOtterBase() == nil {
		t.Skip("resources/otter is not in this checkout")
	}
	onMain(func() {
		a.drop = dropZoneState{}
		a.show(false)
		a.win.Invalidate()
	})
	time.Sleep(600 * time.Millisecond)
	var frames int
	onMain(func() {
		frameTrace = func(_, _ float32, _ mygo.Rectangle, blank bool) {
			if !blank {
				frames++
			}
		}
	})
	t.Cleanup(func() { onMain(func() { frameTrace = nil }) })
	stop := startMotionTicker(a)
	t.Cleanup(stop)

	caught := time.Now()
	onMain(func() { a.catchDrop(120, 30) })
	time.Sleep(200 * time.Millisecond)
	islandShot(t, a, "catch-flying", 0)
	time.Sleep(500 * time.Millisecond)

	// A press outside the panel while the otter holds the file: the panel
	// stays and remembers the press.
	onMain(func() {
		// Far below the panel and off its axis: a press on the desktop.
		a.mouseDown(false, 0, mygo.Point{X: a.dispBounds.X + 60, Y: a.dispBounds.Y + a.dispBounds.Height - 80})
	})
	if !a.visible {
		t.Fatal("the press outside closed the panel during the catch")
	}
	if !a.hidePending {
		t.Fatal("the press outside was not remembered for after the catch")
	}
	// The app's own tick runs settlePendingHide; drive it at the same
	// cadence until the panel folds.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		hidden := false
		now := time.Now()
		onMain(func() {
			a.settlePendingHide(now)
			hidden = !a.visible
		})
		if hidden {
			break
		}
		time.Sleep(pointerInterval)
	}
	if a.visible {
		t.Fatal("the panel never folded after the catch")
	}
	waited := time.Since(caught)
	if waited < dropCatchFly+dropCatchHold-150*time.Millisecond {
		t.Fatalf("the panel folded after %v, before the otter's retract began", waited)
	}
	if frames < 10 {
		t.Fatalf("only %d frames were drawn during the catch", frames)
	}
	t.Logf("catch: panel stayed %v, %d frames drawn", waited, frames)
}
