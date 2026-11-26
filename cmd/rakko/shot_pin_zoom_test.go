package main

// The desktop pin's wheel zoom: a discrete notch steps once, however many
// units the platform encodes it with, while a precise trackpad delta grows
// with the distance scrolled.

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPinZoomFactor(t *testing.T) {
	cases := []struct {
		name string
		ev   ui.InputEvent
		want float64
	}{
		{"windows wheel in", ui.InputEvent{Kind: ui.InputScroll, DY: -99}, 1.2},
		{"windows wheel out", ui.InputEvent{Kind: ui.InputScroll, DY: 99}, 1 / 1.2},
		{"darwin wheel in", ui.InputEvent{Kind: ui.InputScroll, DY: -1}, 1.2},
		{"trackpad in", ui.InputEvent{Kind: ui.InputScroll, DY: -2, Precise: true}, math.Exp(0.12)},
		{"trackpad out", ui.InputEvent{Kind: ui.InputScroll, DY: 2, Precise: true}, math.Exp(-0.12)},
		{"no delta", ui.InputEvent{Kind: ui.InputScroll}, 1},
	}
	for _, c := range cases {
		if got := pinZoomFactor(c.ev); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: pinZoomFactor = %v, want %v", c.name, got, c.want)
		}
	}
	// One notch must be a modest step, not the old exponential's ×380.
	if got := pinZoomFactor(ui.InputEvent{Kind: ui.InputScroll, DY: -99}); got > 1.3 {
		t.Fatalf("one Windows wheel notch zooms ×%v: too coarse", got)
	}
}
