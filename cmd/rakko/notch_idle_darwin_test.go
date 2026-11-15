//go:build darwin

package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
)

func TestDarwinNotchHoverUnchanged(t *testing.T) {
	if notchTopEdgeHoverOnly {
		t.Fatal("notchTopEdgeHoverOnly must remain false on macOS")
	}
	a := &shelf{
		notchX:       645,
		notchY:       0,
		notchW:       180,
		notchH:       32,
		hasNotch:     true,
		x:            (1470 - sheetW) / 2,
		y:            0,
		dispBounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
		settings:     settings.Default(),
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
	oldCursor, oldState, oldTypes, oldDown := cursorPoint, dragState, dragTypes, dragLeftDown
	t.Cleanup(func() {
		cursorPoint, dragState, dragTypes, dragLeftDown = oldCursor, oldState, oldTypes, oldDown
	})
	p := mygo.Point{X: 735, Y: 20}
	cursorPoint = func() mygo.Point { return p }
	dragState = func() (bool, int64) { return false, 0 }
	dragTypes = func() []string { return nil }
	dragLeftDown = func() bool { return false }

	a.monitorDrag(time.Now())
	if !a.peeking {
		t.Fatal("macOS notch hover at y=20 inside the camera housing must still peek")
	}
}
