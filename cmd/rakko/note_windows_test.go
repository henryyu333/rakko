//go:build windows

package main

import (
	"testing"

	"github.com/egoist/mygo"
)

// TestNoteFrameUnderTallAnchorStaysBelow: on Windows the open panel's window
// leaves room neither above nor below; the note stays below it, clamped to
// the work area, rather than flipping to the top under the capsule.
func TestNoteFrameUnderTallAnchorStaysBelow(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}
	anchor := mygo.Rectangle{X: 618, Y: 0, Width: 685, Height: 659}
	want := mygo.Rectangle{X: 618 + (685-noteW)/2, Y: 1040 - noteH, Width: noteW, Height: noteH}
	if got := noteFrameUnder(anchor, area); got != want {
		t.Fatalf("note frame = %+v, want %+v", got, want)
	}
}
