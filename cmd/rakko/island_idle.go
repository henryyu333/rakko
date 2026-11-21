package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/platform"
)

// Idle islands on the other screens: every display without a camera
// housing shows its own resting capsule, even while the resident window
// (the one that grows into the panel) follows the pointer elsewhere. They
// are display-only lightweight windows that never take a click; hovering
// or pressing one moves the resident window to that screen, which then
// draws the same capsule itself with all the live states attached.

// idleIslandWindow is one extra display's resting capsule. Windows are
// kept per display id and only moved when the display layout changes.
type idleIslandWindow struct {
	dispID int64
	win    *mygo.Window
	frame  mygo.Rectangle
}

// drawIslandCapsule paints the 120 × 30 rounded capsule that fills a tiny
// idle window; the transparent corners are what makes it read as an island.
// It is the resident shell's own islandPath at the capsule's radius, so the
// picture window and the panel's idle frame draw the same shape: the two
// hand the capsule over to each other without moving a corner pixel.
func drawIslandCapsule(p *ui.Painter, r ui.Rect) {
	var path ui.Path
	islandPath(&path, r.X, r.Y, r.X+r.W, r.Y+r.H, min(r.W/2, r.H/2))
	p.FillPath(&path, surface)
}

// idleIslandView draws the extra displays' capsule.
func idleIslandView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	ui.Column(c.Key("idle-island")).Fill().Label(loc(c).IslandLabel()).Draw(drawIslandCapsule)
}

// mainIdleCapsule is the resident display's resting capsule in its own tiny
// window. The resident window also draws the capsule while idle, but it
// blanks its canvas before every native resize (mygo stretches the last
// canvas into the new frame); this window covers those frames so the
// capsule never disappears, and the panel's shell covers it once it draws.
type mainIdleCapsule struct {
	win   *mygo.Window
	frame mygo.Rectangle
	shown bool
}

// mainIdleCapsuleView draws the same capsule as the resident window's idle
// frame, in a window exactly the capsule's size.
func (a *shelf) mainIdleCapsuleView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	ui.Column(c.Key("main-idle-island")).Fill().Label(loc(c).PanelExpand()).Draw(drawIslandCapsule)
}

// openMainIdleCapsule creates the resident display's picture-only capsule.
// It takes no clicks: the global press observer still maps a press on it to
// the island move or the app entry, exactly like the panel's idle frame.
func (a *shelf) openMainIdleCapsule(f mygo.Rectangle) *mainIdleCapsule {
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: a.appLoc().WindowIsland(), Width: f.Width, Height: f.Height,
		X: f.X, Y: f.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: disableMoveOverlay, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(a.mainIdleCapsuleView),
	})
	if w == nil {
		return nil
	}
	w.SetVisibleOnAllWorkspaces(true)
	h := w.NativeHandle()
	platform.ConfigureIdleCapsuleWindow(h)
	// One level below the panel's resting level, so the opening shell and
	// the folding shell draw over it; it shows through only while their
	// canvas is blank or exactly capsule-sized.
	platform.SetWindowLayer(h, platform.LayerBelowStatus)
	platform.SetOverlayAcceptsMouse(h, false)
	platform.SetWindowFrame(h, f.X, f.Y, f.Width, f.Height)
	return &mainIdleCapsule{win: w, frame: f}
}

// syncMainIdleCapsule keeps the resting capsule window on the island's current
// box. It creates the window on first use and on a style switch back to the
// island; it never shows it by itself.
func (a *shelf) syncMainIdleCapsule() {
	if a.win == nil || a.winH == 0 || a.quitting || !a.island || a.dispBounds.Width <= 0 {
		a.destroyMainIdleCapsule()
		return
	}
	r := a.islandRect()
	if a.mainIdle == nil {
		a.mainIdle = a.openMainIdleCapsule(r)
		return
	}
	if a.mainIdle.frame == r {
		return
	}
	a.mainIdle.frame = r
	platform.SetWindowFrame(a.mainIdle.win.NativeHandle(), r.X, r.Y, r.Width, r.Height)
}

// showMainIdleCapsule raises the resting capsule over the resident window's
// blank canvas. It is idempotent and cheap after the first call.
func (a *shelf) showMainIdleCapsule() {
	a.syncMainIdleCapsule()
	if a.mainIdle == nil || a.mainIdle.shown {
		return
	}
	a.mainIdle.win.ShowInactive()
	a.mainIdle.shown = true
}

// hideMainIdleCapsule takes the capsule down once the panel draws something
// of its own; the window stays for the next idle.
func (a *shelf) hideMainIdleCapsule() {
	if a.mainIdle == nil || !a.mainIdle.shown {
		return
	}
	a.mainIdle.win.Hide()
	a.mainIdle.shown = false
}

// destroyMainIdleCapsule closes the resting capsule for a headless shelf, a
// style switch to the notch, or quit.
func (a *shelf) destroyMainIdleCapsule() {
	if a.mainIdle == nil {
		return
	}
	a.mainIdle.win.Destroy()
	a.mainIdle = nil
}

// syncMainIdleCapsuleVisibility decides, at the end of a built frame,
// whether the resting capsule picture belongs on screen. The panel keeps
// drawing the capsule itself; the picture only covers the panel's blank
// frames (before/after a native resize) and the moment between the fold and
// the next idle frame. While the panel draws a peek or an open shell, the
// shell covers it or it is hidden.
func (a *shelf) syncMainIdleCapsuleVisibility(blank bool) {
	if a.win == nil || a.winH == 0 || a.quitting || !a.island {
		a.destroyMainIdleCapsule()
		return
	}
	switch {
	case a.visible:
		// The opening keeps the capsule up until the panel paints its own
		// shell; later blank resizes (a style or keyboard level change)
		// leave it down.
		if !blank {
			a.hideMainIdleCapsuleSoon()
		}
	case a.peeking || a.apps.on || a.fx.peekK > 0.01 || a.fb.drawn || a.fb.retracting ||
		a.liveUp() || a.clockAlarmFree() || a.sysmonWingsUp() || a.dragging || a.pointerDrag || a.islandDrag:
		// A transparent frame is what covers a resize (issue #5, a closed
		// peek's growth); the capsule's picture stays over it.
		if !blank {
			a.hideMainIdleCapsuleSoon()
		}
	case a.fx.w <= float32(islandIdleW+4) && a.fx.h <= float32(islandIdleH+4):
		// The closing shell has folded back onto the capsule: the picture
		// takes over before the panel blanks for its strip resize.
		a.showMainIdleCapsule()
	default:
		a.hideMainIdleCapsule()
	}
}

// hideMainIdleCapsuleSoon takes the picture down after the frame that drew
// the panel's own shell has committed, so no display frame shows neither.
func (a *shelf) hideMainIdleCapsuleSoon() {
	if a.mainIdle == nil || !a.mainIdle.shown || a.mainIdleHideQueued {
		return
	}
	a.mainIdleHideQueued = true
	a.win.Update(func() {
		a.mainIdleHideQueued = false
		if a.win == nil || a.win.IsDestroyed() {
			return
		}
		// A transparent frame is on its way (a native resize waits for it):
		// the capsule's picture is what keeps the capsule on screen under
		// it, so it stays until the resize has landed and a drawn frame
		// covers it.
		if a.blankQueued || a.blankFrame {
			return
		}
		// An idle or capsule-sized closing frame may have taken over while
		// this hide waited; the picture stays for those.
		if !a.visible && !a.peeking && !a.apps.on && a.fx.peekK <= 0.01 &&
			!a.fb.drawn && !a.fb.retracting && !a.liveUp() && !a.clockAlarmFree() && !a.sysmonWingsUp() &&
			!a.dragging && !a.pointerDrag && !a.islandDrag &&
			a.fx.w <= float32(islandIdleW+4) && a.fx.h <= float32(islandIdleH+4) {
			return
		}
		a.hideMainIdleCapsule()
	})
}

// syncIdleIslands reconciles the extra capsules with the current display
// layout and the resident window's display. Main thread only; a headless
// test shelf (no native window) owns none.
func (a *shelf) syncIdleIslands() {
	if a.win == nil || a.quitting {
		a.closeIdleIslands()
		return
	}
	displays := screenDisplays()
	ds := make([]islandDisplay, 0, len(displays))
	for _, d := range displays {
		_, _, _, notchH, hasNotch := platform.NotchRect(d.ID)
		ds = append(ds, islandDisplay{ID: d.ID, Bounds: d.Bounds, HasNotch: hasNotch, NotchH: notchH})
	}
	want := idleIslandPlacements(ds, a.dispID, a.settings.ShellStyle, a.settings.IslandPos)
	have := make(map[int64]*idleIslandWindow, len(a.idleIslands))
	for _, w := range a.idleIslands {
		have[w.dispID] = w
	}
	next := make([]*idleIslandWindow, 0, len(want))
	for _, pl := range want {
		w := have[pl.DisplayID]
		delete(have, pl.DisplayID)
		if w == nil {
			w = a.openIdleIsland(pl)
			if w == nil {
				continue
			}
		} else if w.frame != pl.Frame {
			w.frame = pl.Frame
			platform.SetWindowFrame(w.win.NativeHandle(), pl.Frame.X, pl.Frame.Y, pl.Frame.Width, pl.Frame.Height)
		}
		next = append(next, w)
	}
	for _, w := range have {
		w.win.Destroy()
	}
	a.idleIslands = next
}

// openIdleIsland creates one display-only capsule window for pl.
func (a *shelf) openIdleIsland(pl idlePlacement) *idleIslandWindow {
	f := pl.Frame
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: a.appLoc().WindowIsland(), Width: f.Width, Height: f.Height,
		X: f.X, Y: f.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: disableMoveOverlay, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(idleIslandView),
	})
	if w == nil {
		return nil
	}
	w.SetVisibleOnAllWorkspaces(true)
	h := w.NativeHandle()
	platform.ConfigureIdleCapsuleWindow(h)
	// The capsule is a picture: the resident window handles the pointer
	// and the global mouse-down observer moves it here on a press.
	platform.SetOverlayAcceptsMouse(h, false)
	platform.SetWindowFrame(h, f.X, f.Y, f.Width, f.Height)
	w.ShowInactive()
	return &idleIslandWindow{dispID: pl.DisplayID, win: w, frame: f}
}

// closeIdleIslands destroys every extra capsule; quit and a headless
// shelf both go through it.
func (a *shelf) closeIdleIslands() {
	for _, w := range a.idleIslands {
		w.win.Destroy()
	}
	a.idleIslands = nil
	a.destroyMainIdleCapsule()
}

// idleIslandAt reports whether p is on one of the display-only capsules,
// so a press that reached another app can still bring the shelf there.
func (a *shelf) idleIslandAt(p mygo.Point) bool {
	for _, w := range a.idleIslands {
		if p.X >= w.frame.X && p.X < w.frame.X+w.frame.Width &&
			p.Y >= w.frame.Y && p.Y < w.frame.Y+w.frame.Height {
			return true
		}
	}
	return false
}
