//go:build darwin

package main

// Real-window proof for the island's search drop (Issue #6). Run with
// RAKKO_BASKET_SMOKE=1 through /Users/y3u/workspace/Rakko/.scratch/native-gate.sh.
//
// The window holds the keyboard below the menu bar while the search bar is
// open, so an island's shell has to drop to the row under the bar. It used to
// take that drop in the frame the keyboard state changed, which jumped the
// whole panel up or down before it folded. Every frame the overlay draws is
// sampled here: the shell's top edge may only travel those points over the
// drop's own ease, the settled panel must still clear the menu bar, an island
// dragged below the bar must not move its top at all, and every frame's native
// envelope must hold the shell it draws.

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/settings"
)

// focusDropTraceDir holds the per-frame traces this proof writes.
const focusDropTraceDir = "/tmp/Rakko-issue6/traces"

// dropFrame is one drawn frame: the shell's top on screen, the shell's
// height, and the native frame that had to hold it.
type dropFrame struct {
	phase  string
	ms     float64
	top    int
	shellH float32
	frame  mygo.Rectangle
	openK  float32
}

// dropTrace records the shell's top on every drawn (non-blank) frame.
type dropTrace struct {
	mu     sync.Mutex
	phase  string
	start  time.Time
	frames []dropFrame
}

func (d *dropTrace) begin(phase string) {
	d.mu.Lock()
	d.phase = phase
	d.mu.Unlock()
}

func (d *dropTrace) record(a *shelf) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frames = append(d.frames, dropFrame{
		phase:  d.phase,
		ms:     float64(time.Since(d.start)) / float64(time.Millisecond),
		top:    a.frameTop() + a.sheetTop(),
		shellH: a.fx.h,
		frame:  a.win.Bounds(),
		openK:  a.fx.openK,
	})
}

// steps returns the phase's frames and the largest move of the top edge
// between two frames drawn within one display frame (20ms: 60Hz plus slack).
func (d *dropTrace) steps(t *testing.T, phase string) (frames []dropFrame, maxStep int, maxGap float64) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, f := range d.frames {
		if f.phase == phase {
			frames = append(frames, f)
		}
	}
	for i := 1; i < len(frames); i++ {
		gap := frames[i].ms - frames[i-1].ms
		d := abs(frames[i].top - frames[i-1].top)
		if gap > maxGap {
			maxGap = gap
		}
		if gap <= 20 && d > maxStep {
			maxStep = d
		}
	}
	return frames, maxStep, maxGap
}

func (d *dropTrace) save(t *testing.T, name string) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := os.MkdirAll(focusDropTraceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := focusDropTraceDir + "/" + name + ".csv"
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintln(f, "phase,ms,top,open_k,gap_ms,step_pt,frame_y,frame_h,shell_h")
	prev := map[string]dropFrame{}
	for _, fr := range d.frames {
		step, gap := 0, 0.0
		if p, ok := prev[fr.phase]; ok {
			step, gap = abs(fr.top-p.top), fr.ms-p.ms
		}
		prev[fr.phase] = fr
		fmt.Fprintf(f, "%s,%.1f,%d,%.4f,%.1f,%d,%d,%d,%.1f\n",
			fr.phase, fr.ms, fr.top, fr.openK, gap, step, fr.frame.Y, fr.frame.Height, fr.shellH)
	}
	t.Logf("shell top trace -> %s", path)
}

// checkHeld asserts the native frame held the drawn shell in every frame of
// a phase: the envelope is measured at the settled search row, so a frame
// taken while the drop is still on its way must not clip the panel.
func checkHeld(t *testing.T, phase string, frames []dropFrame) {
	t.Helper()
	for _, f := range frames {
		bottom := float32(f.top) + f.shellH
		if float32(f.frame.Y) > float32(f.top) || float32(f.frame.Y+f.frame.Height) < bottom {
			t.Errorf("%s at %.0fms: native frame %+v does not hold the shell %d..%.0f",
				phase, f.ms, f.frame, f.top, bottom)
		}
	}
}

// topSeq prints a few top-edge values for a failing phase.
func topSeq(frames []dropFrame) string {
	out := ""
	for i, f := range frames {
		if i > 0 && i%4 != 0 {
			continue
		}
		out += fmt.Sprintf(" %.0fms:%d", f.ms, f.top)
	}
	return out
}

// checkSearchDrop runs the three transitions that used to jump — opening the
// search bar, handing the keyboard back with the panel open, and folding it —
// and checks the shell's top edge frame by frame. tag names the geometry in
// the log; wantDrop is the search drop that geometry should need.
func checkSearchDrop(t *testing.T, a *shelf, tr *dropTrace, tag string, wantDrop int) {
	t.Helper()
	capsule := a.anchorScreenTop()
	line := a.dispBounds.Y + max(a.topInset, menuBarMinH) + headerTopGap

	// The resting capsule is on its own row.
	tr.begin(tag + "-idle")
	onMain(a.win.Invalidate)
	time.Sleep(250 * time.Millisecond)
	idle, _, _ := tr.steps(t, tag+"-idle")
	if len(idle) == 0 {
		t.Fatalf("%s: no idle frame was drawn", tag)
	}
	if last := idle[len(idle)-1]; last.top != capsule {
		t.Fatalf("%s: resting capsule top %d, want the island's own %d", tag, last.top, capsule)
	}

	// Opening the search bar: the top eases down to the row under the menu
	// bar, one short step per frame.
	tr.begin(tag + "-open")
	onMain(func() { a.show(false) })
	time.Sleep(900 * time.Millisecond)
	onMain(func() {
		if !a.focusMode || a.progress < 0.999 {
			t.Fatalf("%s: the search bar never settled open: focus=%v progress=%.3f", tag, a.focusMode, a.progress)
		}
	})
	open, openStep, openGap := tr.steps(t, tag+"-open")
	if len(open) < 10 {
		t.Fatalf("%s: only %d frames drawn while opening", tag, len(open))
	}
	settled := open[len(open)-1].top
	if got := settled - capsule; got != wantDrop {
		t.Errorf("%s: the settled search panel sits %dpt below the capsule's row, want %d", tag, got, wantDrop)
	}
	if want := max(capsule, line); settled != want {
		t.Errorf("%s: settled search panel top %d, want %d (the row below the menu bar)", tag, settled, want)
	}
	if settled <= a.dispWork.Y {
		t.Errorf("%s: settled search panel top %d does not clear the menu bar's bottom %d", tag, settled, a.dispWork.Y)
	}
	if openStep > 3 {
		t.Errorf("%s: opening moved the shell's top %dpt in one frame (max gap %.1fms): %s",
			tag, openStep, openGap, topSeq(open))
	}
	checkHeld(t, tag+"-open", open)
	t.Logf("%s-open: %d frames, top %d->%d, max step %dpt, max gap %.1fms",
		tag, len(open), open[0].top, settled, openStep, openGap)

	// Handing the keyboard back with the panel open: the shell comes up on
	// the same ease, and the settled row is the island's own.
	tr.begin(tag + "-leave-focus")
	onMain(func() { a.leaveFocusMode() })
	time.Sleep(800 * time.Millisecond)
	onMain(func() {
		if a.focusMode {
			t.Fatalf("%s: the keyboard was not handed back", tag)
		}
	})
	left, leftStep, leftGap := tr.steps(t, tag+"-leave-focus")
	if len(left) == 0 {
		t.Fatalf("%s: no frame was drawn while the keyboard went back", tag)
	}
	if got := left[len(left)-1].top; got != capsule {
		t.Errorf("%s: after leaveFocusMode the shell's top is %d, want its own %d", tag, got, capsule)
	}
	if leftStep > 3 {
		t.Errorf("%s: leaveFocusMode moved the shell's top %dpt in one frame (max gap %.1fms): %s",
			tag, leftStep, leftGap, topSeq(left))
	}
	checkHeld(t, tag+"-leave-focus", left)
	t.Logf("%s-leave-focus: %d frames, top %d->%d, max step %dpt, max gap %.1fms",
		tag, len(left), left[0].top, left[len(left)-1].top, leftStep, leftGap)
	onMain(func() { a.hide(hidePassive) })
	waitRestingFold(t, a)
	time.Sleep(300 * time.Millisecond)

	// Opening and folding the whole search bar: the fold's first frame may
	// not jump the panel up to the capsule's row.
	tr.begin(tag + "-reopen")
	onMain(func() { a.show(false) })
	time.Sleep(900 * time.Millisecond)
	tr.begin(tag + "-close")
	onMain(func() { a.hide(hidePassive) })
	waitRestingFold(t, a)
	time.Sleep(300 * time.Millisecond)
	closing, closeStep, closeGap := tr.steps(t, tag+"-close")
	if len(closing) == 0 {
		t.Fatalf("%s: no frame was drawn while folding", tag)
	}
	if got := closing[len(closing)-1].top; got != capsule {
		t.Errorf("%s: the folded shell's top is %d, want its own %d", tag, got, capsule)
	}
	if closeStep > 3 {
		t.Errorf("%s: folding moved the shell's top %dpt in one frame (max gap %.1fms): %s",
			tag, closeStep, closeGap, topSeq(closing))
	}
	checkHeld(t, tag+"-close", closing)
	t.Logf("%s-close: %d frames, top %d->%d, max step %dpt, max gap %.1fms",
		tag, len(closing), closing[0].top, closing[len(closing)-1].top, closeStep, closeGap)
	// Frames drawn while the next section sets itself up are not part of this
	// geometry's transitions.
	tr.begin("settling")
}

// TestSmokeFocusDropKeepsTheTopContinuous drives the real island window
// through the three transitions that used to jump, on the two geometries an
// island can have here: a screen without a camera housing (33pt of drop) and
// this machine's notched screen in island style (the 2pt the header gap
// adds). It then proves a dragged island's panel top is the capsule's row.
func TestSmokeFocusDropKeepsTheTopContinuous(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := redesignShelf(t)
	realNotch := notchRect
	// A screen without a camera housing: the island rests on the 7pt row and
	// its search drop is the full 33pt, while every other measured value
	// stays the real display's.
	notchRect = func(int64) (int, int, int, int, bool) { return 0, 0, 0, 32, false }
	t.Cleanup(func() { notchRect = realNotch })
	islandStyle(t, a, settings.ShellStyleIsland)
	motionClosed(a)
	time.Sleep(500 * time.Millisecond)
	stop := startMotionTicker(a)
	defer stop()

	tr := &dropTrace{start: time.Now(), phase: "idle"}
	onMain(func() {
		frameTrace = func(_ float32, _ float32, _ mygo.Rectangle, blank bool) {
			if blank {
				return
			}
			tr.record(a)
		}
	})
	defer onMain(func() { frameTrace = nil })
	defer tr.save(t, "focus-drop-trace")

	// A bare screen's island: 33pt from the capsule's row to the row below
	// the menu bar.
	bareDrop := max(32, menuBarMinH) + headerTopGap - islandTopGap
	if got := a.focusDropOnScreen(); got != bareDrop {
		t.Fatalf("bare screen drop = %d, want %d", got, bareDrop)
	}
	checkSearchDrop(t, a, tr, "bare", bareDrop)

	// This machine's screened housing, still in island style: the shell rests
	// 6pt under it and needs only the rest of the header gap.
	onMain(func() {
		notchRect = realNotch
		a.place(mygo.Point{X: a.dispBounds.X + a.dispBounds.Width/2, Y: a.dispBounds.Y + 1})
		a.win.Invalidate()
	})
	time.Sleep(300 * time.Millisecond)
	_, _, _, housing, hasNotch := realNotch(a.dispID)
	if !hasNotch {
		t.Skip("this display has no camera housing: the island style's own row was covered")
	}
	notchedDrop := max(housing, menuBarMinH) + headerTopGap - (housing + islandNotchGap)
	if got := a.focusDropOnScreen(); got != notchedDrop {
		t.Fatalf("notched island drop = %d, want %d (housing %d)", got, notchedDrop, housing)
	}
	checkSearchDrop(t, a, tr, "notched", notchedDrop)

	// The user's island: dragged below the menu bar, so no drop at all. The
	// panel's top edge is the capsule's row on every frame, and the fold has
	// nothing to jump back from.
	row := a.dispBounds.Y + 200
	key := islandSpotKey(a.dispID)
	onMain(func() {
		if a.settings.IslandPos == nil {
			a.settings.IslandPos = map[string]settings.IslandPos{}
		}
		// Only this launch's settings are touched; nothing is written to disk.
		a.settings.IslandPos[key] = settings.IslandPos{X: a.stripCenter() - a.dispBounds.X, Y: 200}
		a.place(mygo.Point{X: a.dispBounds.X + a.dispBounds.Width/2, Y: a.dispBounds.Y + 1})
		a.win.Invalidate()
	})
	t.Cleanup(func() {
		onMain(func() {
			delete(a.settings.IslandPos, key)
			a.place(mygo.Point{X: a.dispBounds.X + a.dispBounds.Width/2, Y: a.dispBounds.Y + 1})
			a.win.Invalidate()
		})
	})
	time.Sleep(300 * time.Millisecond)
	if got := a.anchorScreenTop(); got != row {
		t.Fatalf("the island rests at %d, want the dragged row %d", got, row)
	}
	tr.begin("dragged-open")
	onMain(func() { a.show(false) })
	time.Sleep(900 * time.Millisecond)
	draggedOpen, draggedStep, draggedGap := tr.steps(t, "dragged-open")
	if len(draggedOpen) == 0 {
		t.Fatal("no frame was drawn on the dragged island")
	}
	for _, f := range draggedOpen {
		if f.top != row {
			t.Fatalf("dragged island: frame at %.0fms drew the panel's top at %d, want the capsule's row %d",
				f.ms, f.top, row)
		}
	}
	if draggedStep != 0 {
		t.Errorf("dragged island: the top moved %dpt in one frame", draggedStep)
	}
	checkHeld(t, "dragged-open", draggedOpen)
	t.Logf("dragged-open: %d frames, top %d throughout, max step %dpt, max gap %.1fms",
		len(draggedOpen), row, draggedStep, draggedGap)

	tr.begin("dragged-close")
	onMain(func() { a.hide(hidePassive) })
	waitRestingFold(t, a)
	time.Sleep(300 * time.Millisecond)
	draggedClose, draggedCloseStep, _ := tr.steps(t, "dragged-close")
	if len(draggedClose) == 0 {
		t.Fatal("no frame was drawn folding the dragged island")
	}
	for _, f := range draggedClose {
		if f.top != row {
			t.Fatalf("dragged island fold: frame at %.0fms drew the top at %d, want %d (no drop, no jump)",
				f.ms, f.top, row)
		}
	}
	if draggedCloseStep != 0 {
		t.Errorf("dragged island fold: the top moved %dpt in one frame", draggedCloseStep)
	}
	t.Logf("dragged-close: %d frames, top %d throughout, max step %dpt", len(draggedClose), row, draggedCloseStep)
}
