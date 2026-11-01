//go:build darwin

package main

// The expanded panel follows the system appearance: the glass frosts what
// the window painted under it (a defined black), so the panel reads dark on
// any desktop. Reduce Transparency always means pure black. Hover shapes,
// the drop hint, the completion capsule and the live activities stay pure
// black at all times: they hang off the notch and must read as hardware.

import (
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

const appearancePoll = 250 * time.Millisecond

// refreshAppearance re-reads Reduce Transparency on a slow tick: the panel
// can change while it runs.
func (a *shelf) refreshAppearance(now time.Time) {
	if !a.appearanceAt.IsZero() && now.Sub(a.appearanceAt) < appearancePoll {
		return
	}
	a.appearanceAt = now
	reduce := macReduceTransparency()
	if reduce != a.reduceTrans {
		a.reduceTrans = reduce
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

// panelFill is the shell's own paint. Hover shapes, the drop hint, the
// completion capsule and the live activities stay pure black: they hang
// off the notch and must read as hardware. The open panel (glassy) is
// mygo's glass under a black band that joins the hardware notch for the
// menu bar's 32pt and melts into the glass over the next 24pt
// (design/explore/notch-v3.html). body is the shell's box without its top
// ears, botR its bottom corners.
func (a *shelf) panelFill(p *ui.Painter, path *ui.Path, body ui.Rect, botR float32, glassy bool) {
	if !glassy || a.reduceTrans || body.H <= 0 {
		p.FillPath(path, surface)
		return
	}
	// The glass frosts what this frame painted under it: the whole shell,
	// ears included, is painted black first, so that is a defined black
	// and never the transparent window.
	p.FillPath(path, surface)
	glass.Glass{}.PaintMaterial(p, body, [4]float32{0, 0, botR, botR})
	band := min(float32(bandH+24), body.H)
	p.FillGradient(ui.Rect{X: body.X, Y: body.Y, W: body.W, H: band}, ui.LinearGradient{
		Angle: 180,
		Stops: []ui.GradientStop{
			{Color: surface, At: 0},
			{Color: surface, At: min(float32(bandH)/band, 1)},
			{Color: surface.Alpha(0), At: 1},
		},
	}, 0)
}

// macReduceTransparency is a seam so headless tests never touch AppKit.
var macReduceTransparency = func() bool { return mac.ReduceTransparency() }
