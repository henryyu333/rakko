//go:build windows

package main

import (
	"testing"

	"github.com/egoist/mygo"
)

// TestNoteFrameUnderTallAnchorFlipsAboveAndClearsShell: when there is no room
// below a tall anchor on Windows, noteFrameUnder flips the note above and
// clamps to the work area just like macOS, and noteClearShell keeps its top
// clear of the topmost simulated notch or island capsule.
func TestNoteFrameUnderTallAnchorFlipsAboveAndClearsShell(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}
	anchor := mygo.Rectangle{X: 618, Y: 0, Width: 685, Height: 659}
	want := mygo.Rectangle{X: 618 + (685-noteW)/2, Y: 0, Width: noteW, Height: noteH}
	got := noteFrameUnder(anchor, area)
	if got != want {
		t.Fatalf("noteFrameUnder = %+v, want %+v", got, want)
	}

	a := &shelf{hasNotch: true, notchX: 870, notchY: 0, notchW: 180, notchH: 32}
	cleared := a.noteClearShell(got, area)
	if cleared.Y != 38 {
		t.Fatalf("noteClearShell under notch = %+v, want Y=38", cleared)
	}

	a.island, a.islandTop = true, islandTopGap
	a.x = 960 - sheetW/2
	clearedIsland := a.noteClearShell(got, area)
	if wantY := a.islandRect().Y + a.islandRect().Height + 6; clearedIsland.Y != wantY {
		t.Fatalf("noteClearShell under island = %+v, want Y=%d", clearedIsland, wantY)
	}
}
