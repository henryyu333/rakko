//go:build darwin

package main

// The expanded panel follows the system appearance: dark shows pure black,
// light shows dark frosted glass (a pure black band joins the hardware
// notch, then a translucent dark material with the desktop blurred behind
// it). Reduce Transparency always means pure black. Hover shapes, the drop
// hint, the completion capsule and the live activities stay pure black at
// all times: they hang off the notch and must read as hardware.
//
// Only the panel's own tiling is affected. The material itself is a system
// NSVisualEffectView behind the window, framed to the shell every time the
// window frame is applied (internal/mac), so the blur follows the shape
// without a per-frame mask.

import (
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

const appearancePoll = 250 * time.Millisecond

// refreshAppearance re-reads the system appearance and Reduce Transparency
// on a slow tick: the panel can change while it runs.
func (a *shelf) refreshAppearance(now time.Time) {
	if !a.appearanceAt.IsZero() && now.Sub(a.appearanceAt) < appearancePoll {
		return
	}
	if a.appearanceAt.IsZero() {
		// First read also seeds the frame's material decision.
		a.appearanceAt = now
	}
	a.appearanceAt = now
	dark, reduce := macSystemDark(), macReduceTransparency()
	if dark != a.systemDark || reduce != a.reduceTrans {
		a.systemDark, a.reduceTrans = dark, reduce
		// The change reaches the open panel at once: the material is
		// created, hidden or re-placed here, not on the next size change.
		a.syncMaterial()
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

// glassNow reports whether the native backdrop material is shown. It never
// is: the open panel draws mygo's own glass (panelFill), which frosts what
// the window painted and needs no native view to follow the shell.
func (a *shelf) glassNow() bool { return false }

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

// panelAppearanceIndex maps a saved appearance to the segmented control's
// segment: 0 follows the system, 1 is black, 2 is glass.
func panelAppearanceIndex(p settings.PanelAppearance) int {
	switch p.Normalized() {
	case settings.PanelAppearanceBlack:
		return 1
	case settings.PanelAppearanceGlass:
		return 2
	}
	return 0
}

// panelAppearanceAt maps the segmented control's segment to a saved
// appearance.
func panelAppearanceAt(idx int) settings.PanelAppearance {
	switch idx {
	case 1:
		return settings.PanelAppearanceBlack
	case 2:
		return settings.PanelAppearanceGlass
	}
	return settings.PanelAppearanceFollow
}

// setPanelAppearance saves the expanded panel's look and repaints an open
// panel at once: the fill reads the setting on the next build, and the
// material view is placed, hidden or shown here rather than on the next
// size change.
func (a *shelf) setPanelAppearance(p settings.PanelAppearance) {
	p = p.Normalized()
	if p == a.settings.PanelAppearance.Normalized() {
		return
	}
	next := a.settings
	next.PanelAppearance = p
	if !a.applySettings(next) {
		return
	}
	a.syncMaterial()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// macSystemDark and macReduceTransparency are seams so headless tests never
// touch AppKit.
var (
	macSystemDark         = func() bool { return mac.SystemDark() }
	macReduceTransparency = func() bool { return mac.ReduceTransparency() }
)
