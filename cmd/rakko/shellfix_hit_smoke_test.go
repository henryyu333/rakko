//go:build darwin

package main

// Real-window proof for the shell-shaped mouse gate: with the sheet settled
// and the window at its level, WindowServer itself must hand a press on the
// drawn shell to ShelfU and a press 5pt beside it — still inside the window's
// transparent ear margin, still above the menu bar — to whatever is
// underneath. Run with RAKKO_BASKET_SMOKE=1; the probe log lands in
// /tmp/shelf-stagefix/shell-hit.txt.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
)

func TestSmokeShellOnlyTakesTheMouseOnItsShape(t *testing.T) {
	a := redesignShelf(t)
	expandShelf(t, a)
	// The opening spring settles well inside half a second.
	time.Sleep(500 * time.Millisecond)
	old := cursorPoint
	t.Cleanup(func() { onMain(func() { cursorPoint = old }) })

	var report []string
	var failures []string
	var shelfWin int
	var screenH float64
	onMain(func() {
		shelfWin = mac.WindowNumber(a.win.NativeHandle())
		screenH = float64(a.dispBounds.Y + a.dispBounds.Height)
		f := a.desiredFrame()
		report = append(report,
			fmt.Sprintf("window %d frame %+v, shell %.1fx%.1f, level %d",
				shelfWin, f, a.fx.w, a.fx.h, a.currentLevel()))
	})
	cx := 0
	shellLeft, shellRight := 0, 0
	onMain(func() {
		cx = a.stripCenter()
		shellRight = cx + int(a.fx.w/2)
		shellLeft = cx - int(a.fx.w/2)
	})
	for _, probe := range []struct {
		name      string
		p         mygo.Point
		wantShelf bool
	}{
		{"the shell's own body", mygo.Point{X: cx, Y: 20}, true},
		{"the shell's body, lower down", mygo.Point{X: cx, Y: 120}, true},
		{"5pt right of the shell at y=20", mygo.Point{X: shellRight + 5, Y: 20}, false},
		{"5pt left of the shell at y=20", mygo.Point{X: shellLeft - 5, Y: 20}, false},
		{"inside the right ear at y=2", mygo.Point{X: shellRight + 2, Y: 2}, true},
	} {
		// The gate is applied and WindowServer given a moment to order the
		// window, as the machine's pointer would take — then the routing is
		// asked of WindowServer itself.
		onMain(func() {
			p := mygo.Point{X: probe.p.X, Y: a.y + probe.p.Y}
			cursorPoint = func() mygo.Point { return p }
			a.syncMousePassThrough()
		})
		time.Sleep(150 * time.Millisecond)
		var got, ignoring bool
		var reached int
		onMain(func() {
			p := mygo.Point{X: probe.p.X, Y: a.y + probe.p.Y}
			reached = mac.WindowAtPoint(float64(p.X), float64(p.Y), screenH)
			got = reached == shelfWin
			ignoring = mac.OverlayIgnoresMouse(a.win.NativeHandle())
		})
		line := fmt.Sprintf("%-32s at (%d,%d): reached window %d shelf=%v want=%v ignoresMouseEvents=%v",
			probe.name, probe.p.X, probe.p.Y, reached, got, probe.wantShelf, ignoring)
		report = append(report, line)
		if got != probe.wantShelf {
			failures = append(failures, line)
		}
	}
	onMain(func() {
		cursorPoint = old
		a.syncMousePassThrough()
	})
	dir := "/tmp/shelf-stagefix"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shell-hit.txt"), []byte(joinLines(report)), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	motionClosed(a)
	for _, line := range failures {
		t.Errorf("WindowServer routed the press wrong: %s", line)
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}
