package main

// Geometry both platforms share: noteFrameUnder stays the same, and
// noteClearShell runs everywhere. A macOS hardware notch sits in the menu bar,
// above the work area, so it leaves the note alone; a simulated notch or an
// island capsule lives inside the work area, so its notes start below it.

import (
	"testing"

	"github.com/egoist/mygo"
)

// TestNoteClearShellModes verifies noteClearShell on both platforms:
//   - A macOS hardware notch sitting inside the menu bar (above area.Y) leaves
//     a top-clamped note untouched.
//   - An Island capsule inside the work area (macOS or Windows) pushes a
//     note placed under it below the capsule.
//   - A simulated Windows notch inside the work area pushes a note below it.
func TestNoteClearShellModes(t *testing.T) {
	// macOS hardware notch in menu bar: work area starts below notch (Y=38).
	macArea := mygo.Rectangle{X: 0, Y: 38, Width: 1470, Height: 918}
	macAnchor := mygo.Rectangle{X: 500, Y: 0, Width: 470, Height: 659}
	macFrame := noteFrameUnder(macAnchor, macArea)
	if macFrame.Y != macArea.Y {
		t.Fatalf("mac flipped note Y = %d, want %d", macFrame.Y, macArea.Y)
	}
	macNotch := &shelf{hasNotch: true, notchX: 645, notchY: 0, notchW: 180, notchH: 38}
	if got := macNotch.noteClearShell(macFrame, macArea); got != macFrame {
		t.Fatalf("mac hardware notch moved note: got %+v, want %+v", got, macFrame)
	}

	// macOS Island mode: capsule lives inside the work area (a.y=macArea.Y).
	macIsland := &shelf{
		island: true, hasNotch: true,
		x: 735 - sheetW/2, y: macArea.Y, islandTop: 8,
		notchX: 645, notchW: 180, notchH: 32,
	}
	islandWantY := macIsland.islandRect().Y + macIsland.islandRect().Height + 6
	if got := macIsland.noteClearShell(macFrame, macArea); got.Y != islandWantY {
		t.Fatalf("mac island noteClearShell = %+v, want Y=%d", got, islandWantY)
	}

	// Windows simulated notch and Island mode inside Y=0 work area.
	winArea := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}
	winAnchor := mygo.Rectangle{X: 618, Y: 0, Width: 685, Height: 659}
	winFrame := noteFrameUnder(winAnchor, winArea)
	if winFrame.Y != 0 {
		t.Fatalf("win flipped note Y = %d, want 0", winFrame.Y)
	}
	winShell := &shelf{hasNotch: true, notchX: 870, notchY: 0, notchW: 180, notchH: 32}
	if got := winShell.noteClearShell(winFrame, winArea); got.Y != 38 {
		t.Fatalf("win notch noteClearShell = %+v, want Y=38", got)
	}
	winShell.island, winShell.islandTop = true, islandTopGap
	winShell.x = 960 - sheetW/2
	if wantY := winShell.islandRect().Y + winShell.islandRect().Height + 6; winShell.noteClearShell(winFrame, winArea).Y != wantY {
		t.Fatalf("win island noteClearShell want Y=%d", wantY)
	}
}
