//go:build darwin

package main

import (
	"testing"

	"github.com/egoist/mygo"

	"rakko/internal/layout"
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

// TestIslandEnvelopeReservesTheToolsWidthOnItsSide: with 340–420pt below
// the island, the tools page's editing maximum (420) does not fit that
// side, but the 651pt six-column width still must. The envelope uses the
// tallest tools page that fits the current side, so adding cards to six
// columns never resizes the window mid-spring.
func TestIslandEnvelopeReservesTheToolsWidthOnItsSide(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleIsland)
	a.visible, a.level, a.tab = true, 2, pageTools
	a.ensurePages()
	// Start on a small page: the envelope must already reserve the width
	// the page can reach, not only its current desired frame.
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "ocr"}, {ID: "zip"}}}
	a.y = a.workArea().Y + a.workArea().Height - 360 - a.islandTop
	a.frame = mygo.Rectangle{}
	if got := a.panelPlacement(); got.Up {
		t.Fatalf("the panel is not downward: %+v", got)
	}
	env := a.sessionFrame()
	target := a.openFrame(toolPanelW(toolMaxCols), toolPanelH(toolMaxRows), 0)
	if !(env.X <= target.X && env.Y <= target.Y &&
		env.X+env.Width >= target.X+target.Width && env.Y+env.Height >= target.Y+target.Height) {
		t.Fatalf("the envelope %+v does not cover a 6-column tools page at %+v", env, target)
	}
}
