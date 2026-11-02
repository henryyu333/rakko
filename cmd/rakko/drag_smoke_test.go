//go:build darwin

package main

// Evidence for the drag fix on the real window: a file drag that leaves the
// open panel folds it and suppresses the rest of the drag; the same drag
// coming back to the band opens the panel again. The drag itself is driven
// through the app's own seams (dragState, cursorPoint) — no Finder session
// is started — but the window, its springs and the shots are real.
// Run with RAKKO_BASKET_SMOKE=1; the shots land in /tmp/shelf-stagef-A.

import (
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// pumpDragMonitor runs the app's drag monitor for d, the way the 30 ms tick
// does, with the test's cursor and drag state in place.
func pumpDragMonitor(t *testing.T, a *shelf, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		onMain(func() { a.monitorDrag(time.Now()) })
		time.Sleep(30 * time.Millisecond)
	}
}

func TestSmokeDragReturnReopensThePanel(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the drag-return smoke test")
	}
	a := redesignShelf(t)
	oldCursor, oldState, oldDown, oldTypes := cursorPoint, dragState, dragLeftDown, dragTypes
	t.Cleanup(func() {
		onMain(func() {
			cursorPoint, dragState, dragLeftDown, dragTypes = oldCursor, oldState, oldDown, oldTypes
			a.dragging, a.suppressed, a.inBand, a.dropHot = false, false, false, false
			a.syncMousePassThrough()
		})
	})
	var p mygo.Point
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.visible, a.progress, a.animationStart = false, 0, time.Time{}
		a.level, a.tab, a.tabFrom = 1, pageTray, pageTray
		a.dragSerial = 99
		a.dragging, a.suppressed, a.inBand, a.dropHot = true, false, false, false
		cursorPoint = func() mygo.Point { return p }
		dragState = func() (bool, int64) { return true, 99 }
		dragLeftDown = func() bool { return true }
		dragTypes = func() []string { return []string{"public.file-url"} }
		// Previous smoke tests may leave the shared window ordered out.
		a.win.ShowInactive()
	})
	band := mygo.Point{X: a.stripCenter(), Y: a.notchY + a.notchH - 1}
	away := mygo.Point{X: a.dispBounds.X + 120, Y: a.dispBounds.Y + a.dispBounds.Height - 120}

	// The drag enters the band: the panel opens on the tray.
	onMain(func() { p = band })
	pumpDragMonitor(t, a, 150*time.Millisecond)
	var opened, inBand, hot bool
	onMain(func() { opened, inBand, hot = a.visible, a.inBand, a.dropHot })
	if !opened || !inBand || !hot {
		t.Fatalf("the entering drag did not open the panel: visible=%v inBand=%v dropHot=%v", opened, inBand, hot)
	}
	stageAShot(t, a, "drag-return-1-open")

	// The drag leaves the panel: it folds and suppresses the rest of the drag.
	onMain(func() { p = away; a.animationStart = time.Time{} })
	pumpDragMonitor(t, a, edgeLeaveDelay+250*time.Millisecond)
	var visible, suppressed bool
	onMain(func() { visible, suppressed = a.visible, a.suppressed })
	if visible || !suppressed {
		t.Fatalf("leaving did not fold and suppress: visible=%v suppressed=%v", visible, suppressed)
	}
	stageAShot(t, a, "drag-return-2-folded")

	// The same drag comes back to the band: the panel opens again.
	onMain(func() { p = band })
	pumpDragMonitor(t, a, 200*time.Millisecond)
	onMain(func() { visible, suppressed, inBand = a.visible, a.suppressed, a.inBand })
	if !visible || suppressed || !inBand {
		t.Fatalf("the returning drag did not reopen the panel: visible=%v suppressed=%v inBand=%v",
			visible, suppressed, inBand)
	}
	stageAShot(t, a, "drag-return-3-reopened")
	t.Logf("drag-return shots -> %s", stageADir)
}
