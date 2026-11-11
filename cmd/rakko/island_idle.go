//go:build darwin

package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
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
func drawIslandCapsule(p *ui.Painter, r ui.Rect) {
	rad := min(r.W/2, r.H/2)
	var path ui.Path
	path.MoveTo(r.X+rad, r.Y)
	path.LineTo(r.X+r.W-rad, r.Y)
	path.QuadTo(r.X+r.W, r.Y, r.X+r.W, r.Y+rad)
	path.LineTo(r.X+r.W, r.Y+r.H-rad)
	path.QuadTo(r.X+r.W, r.Y+r.H, r.X+r.W-rad, r.Y+r.H)
	path.LineTo(r.X+rad, r.Y+r.H)
	path.QuadTo(r.X, r.Y+r.H, r.X, r.Y+r.H-rad)
	path.LineTo(r.X, r.Y+rad)
	path.QuadTo(r.X, r.Y, r.X+rad, r.Y)
	path.Close()
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

// mainIdleCapsuleView draws the same capsule and app entry as the resident
// window's idle frame, in a window exactly the capsule's size.
func (a *shelf) mainIdleCapsuleView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	ui.Column(c.Key("main-idle-island")).Fill().Label(loc(c).PanelExpand()).Draw(drawIslandCapsule)
	entry := ui.Row(c.Key("apps-island-entry")).Absolute().Right(4).Top(0).Size(appEntryW, appEntryW).Center()
	entry.Children(func() { ui.Icon(c, appsSymbol).Size(16, 16).TextColor(ink2) })
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
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(a.mainIdleCapsuleView),
	})
	if w == nil {
		return nil
	}
	w.SetVisibleOnAllWorkspaces(true)
	h := w.NativeHandle()
	mac.ConfigureOverlayWindow(h)
	// One level below the panel's resting level, so the opening shell and
	// the folding shell draw over it; it shows through only while their
	// canvas is blank or exactly capsule-sized.
	mac.SetWindowLevel(h, mac.StatusWindowLevel-1)
	mac.SetOverlayAcceptsMouse(h, false)
	mac.SetWindowFrame(h, f.X, f.Y, f.Width, f.Height)
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
	mac.SetWindowFrame(a.mainIdle.win.NativeHandle(), r.X, r.Y, r.Width, r.Height)
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
		a.liveUp() || a.clockAlarmFree() || a.dragging || a.pointerDrag || a.islandDrag:
		a.hideMainIdleCapsuleSoon()
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
		// An idle or capsule-sized closing frame may have taken over while
		// this hide waited; the picture stays for those.
		if !a.visible && !a.peeking && !a.apps.on && a.fx.peekK <= 0.01 &&
			!a.fb.drawn && !a.fb.retracting && !a.liveUp() && !a.clockAlarmFree() &&
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
	displays := mygo.Screen.Displays()
	ds := make([]islandDisplay, 0, len(displays))
	for _, d := range displays {
		_, _, _, _, hasNotch := mac.NotchRect(d.ID)
		ds = append(ds, islandDisplay{ID: d.ID, Bounds: d.Bounds, HasNotch: hasNotch})
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
			mac.SetWindowFrame(w.win.NativeHandle(), pl.Frame.X, pl.Frame.Y, pl.Frame.Width, pl.Frame.Height)
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
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(idleIslandView),
	})
	if w == nil {
		return nil
	}
	w.SetVisibleOnAllWorkspaces(true)
	h := w.NativeHandle()
	mac.ConfigureOverlayWindow(h)
	// The capsule is a picture: the resident window handles the pointer
	// and the global mouse-down observer moves it here on a press.
	mac.SetOverlayAcceptsMouse(h, false)
	mac.SetWindowFrame(h, f.X, f.Y, f.Width, f.Height)
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
