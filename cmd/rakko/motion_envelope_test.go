//go:build darwin

package main

import (
	"testing"

	"github.com/egoist/mygo"

	"rakko/internal/settings"
)

// The island's envelope holds the side the panel hangs on: a downward one
// starts at the window's top edge and reaches under the results sheet; an
// upward one starts above the panel's top (with the spring's overshoot
// room) and ends at the island's row. Both cover every enabled page and
// the bar's tallest results sheet, which is what keeps a level change or a
// query from resizing the window under a running spring.
func TestIslandSessionEnvelopeHoldsOneSide(t *testing.T) {
	a := islandShelf(false, settings.ShellStyleIsland)
	a.visible, a.level = true, 2

	contains := func(r, b mygo.Rectangle) bool {
		return r.X <= b.X && r.Y <= b.Y && r.X+r.Width >= b.X+b.Width && r.Y+r.Height >= b.Y+b.Height
	}
	covers := func(env mygo.Rectangle) {
		t.Helper()
		if settled := a.desiredFrame(); !contains(env, settled) {
			t.Fatalf("envelope %+v does not cover the settled frame %+v", env, settled)
		}
		if bar := a.openFrame(homeTypingW, float32(homeShellH), resultsMaxH); !contains(env, bar) {
			t.Fatalf("envelope %+v does not cover the bar with its tallest results %+v", env, bar)
		}
		for _, p := range a.enabledPages() {
			target := a.openFrame(pageShellWidth(p), pageShellHeight(p), 0)
			if !contains(env, target) {
				t.Fatalf("envelope %+v does not cover page %s at %+v", env, p.id(), target)
			}
		}
	}

	// Downward: the island at the top of the display, its panel below.
	if got := a.panelPlacement(); got.Up {
		t.Fatalf("the panel above a top-row island grew upward: %+v", got)
	}
	env := a.sessionFrame()
	covers(env)
	if env.Y != a.y {
		t.Fatalf("the downward envelope starts at %d, not the window's top edge %d", env.Y, a.y)
	}
	if pageH := a.panelPlacement(); env.Y+env.Height < pageH.Y+pageH.H {
		t.Fatalf("the downward envelope %+v ends above the panel's bottom", env)
	}

	// Upward: the island near the bottom row, where 250pt does not fit
	// below it, so the panel hangs above the island.
	a.y = a.dispBounds.Y + a.dispBounds.Height - 60 - islandIdleH
	a.frame = mygo.Rectangle{}
	up := a.panelPlacement()
	if !up.Up {
		t.Fatalf("a panel at the bottom row did not grow upward: %+v", up)
	}
	env = a.sessionFrame()
	covers(env)
	if env.Y >= up.Y {
		t.Fatalf("the upward envelope starts at %d, not above the panel's top %d", env.Y, up.Y)
	}
	if bottom := env.Y + env.Height; bottom < a.upShellFloor() {
		t.Fatalf("the upward envelope ends at %d, short of the island's row %d", bottom, a.upShellFloor())
	}

	// A target that must land on the other side is left to the side change:
	// the envelope keeps the current side only. Back on the top row (the
	// panel hangs down), a page taller than the room below the island
	// belongs above it, so it must not widen this envelope.
	a.y = a.dispBounds.Y
	a.frame = mygo.Rectangle{}
	a.level = 2
	tall := &envelopeTallPage{fakePage: &fakePage{on: true}}
	a.registerPage(tall)
	if got := a.panelPlacement(); got.Up {
		t.Fatalf("the current panel is not downward: %+v", got)
	}
	env = a.sessionFrame()
	tw, th := pageShellWidth(tall), pageShellHeight(tall)
	if float64(th) <= float64(a.workArea().Height-a.anchorScreenTop()) {
		t.Fatalf("the tall page (%v) fits below the island, the check exercises nothing", th)
	}
	headroom := int(float64(th)*float64(panelOpenOvershoot)) + 2
	other := a.openFrameIn(a.panelSpotFor(int(tw)+1, int(th)+1, true), tw, th, 0, headroom)
	if contains(env, other) {
		t.Fatalf("the downward envelope %+v covered an upward box %+v", env, other)
	}
}

// envelopeTallPage asks for a panel too tall to hang under a top-row island.
type envelopeTallPage struct {
	*fakePage
}

func (p *envelopeTallPage) id() pageID      { return "tall" }
func (p *envelopeTallPage) label() string   { return "高页面" }
func (p *envelopeTallPage) height() float32 { return 950 }
