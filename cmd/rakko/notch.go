//go:build darwin

package main

import (
	"errors"
	"math"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
)

// The notch shell: geometry, the three states (idle, peek, expanded), their
// animations and mouse pass-through. Content lives in panel.go and items.go.

func (a *shelf) place(p mygo.Point) {
	a.placeAt(p)
	if a.win != nil {
		// The anchor moved (a display change, or the island's row). An open
		// panel takes its envelope again at the new row in this one resize
		// instead of keeping a box measured around the old anchor.
		if a.visible && !a.dragging && !a.pointerDrag && !a.islandDrag {
			a.frameHold = a.sessionFrame()
		}
		a.syncWindowFrame()
		a.win.SetOpacity(1)
	}
	a.syncDragGuard()
	a.syncIdleIslands()
}

// displayNearest is the display lookup placeAt measures from. The app always
// asks WindowServer; tests replace it with a synthetic layout so placement,
// the saved island spot and the clamps can be driven without a screen.
var displayNearest = func(p mygo.Point) mygo.Display { return mygo.Screen.DisplayNearestPoint(p) }

// notchRect measures a display's camera housing; tests replace it with the
// synthetic layout displayNearest returns.
var notchRect = mac.NotchRect

func (a *shelf) placeAt(p mygo.Point) {
	d := displayNearest(p)
	a.dispBounds, a.dispWork = d.Bounds, d.WorkArea
	a.dispID = d.ID
	nx, ny, nw, nh, hasNotch := notchRect(d.ID)
	a.notchX, a.notchY, a.notchW, a.notchH, a.hasNotch = nx, ny, nw, nh, hasNotch
	a.topInset = nh
	a.applyShellStyle()
	// The panel, the peek pill and the strip share one axis: the anchor's
	// centre (the notch, or the display centre on a screen without one).
	// A different centre made the expansion jump sideways.
	a.x = a.anchorCenterX() - sheetW/2
	// The user's own place for this display, when the island was dragged:
	// showing, folding and a display change all replay it instead of the
	// default row.
	a.placeIslandAxis()
	// A held envelope was measured around the previous anchor; the caller
	// takes a fresh one for the new row (show, holdSurface).
	a.frameHold = mygo.Rectangle{}
}

// overAnchor 只认实测几何：光标真正进入锚点热区（刘海，或者灵动岛的
// 空闲胶囊；四周留 hover 余量）才为真。显示器未匹配时 notchW 为 0，交互
// 整体禁用，不会在屏幕左上角凭空出现一个悬停/触发区。
func (a *shelf) overAnchor(p mygo.Point) bool {
	if a.island {
		return a.overIsland(p)
	}
	return a.notchW > 0 && a.notchH > 0 &&
		p.X >= a.notchX-notchHoverPadX && p.X <= a.notchX+a.notchW+notchHoverPadX &&
		p.Y >= a.notchY && p.Y <= a.notchY+a.notchH+notchHoverPadY
}

// Hover changes only the drawing, not the native window geometry. Keeping one
// stable transparent strip for idle/peek avoids resizing the backing surface
// every time the pointer crosses the notch. Only opening the panel changes it.
// The strip is always wide enough for the now-playing peek pill, so a track
// coming or going changes the drawing without a native resize. Every width
// includes the two ear corners (earR): the shell's top edge reaches past its
// flanks by that much on each side, and no frame may clip the arc.
func (a *shelf) desiredFrame() mygo.Rectangle {
	closing := !a.visible && a.fx.openK > 0
	top := a.sheetTop()
	ears := a.earWidth()
	if a.visible || closing {
		if closing {
			// Follow the drawn shell through its fold, with room for the
			// corners and two points below the bottom curve. The shell
			// rides the animated axis (shellCentreX), so a panel clamped
			// off the island's line folds back off-centre too: both sides
			// hold its own offset until it reaches the island. An upward
			// panel grows out of the island's bottom, so the frame reaches
			// from the highest top it draws down to just under the island,
			// even when the work area lifted the panel's own bottom off it.
			w := max(int(math.Ceil(float64(a.fx.w)))+2*ears,
				frameCoverX(a.stripCenter(), a.shellCentreX(a.fx.openK), a.fx.w, ears))
			fold := a.fb.up(time.Now())
			if fold {
				// Folding into the capsule: the shell may grow wider than
				// the panel sliver it starts from. No glow reaches past it
				// anymore, so the room is the shell plus its corners, shake
				// and overshoot only.
				w = max(w, a.capsuleFrameW())
			}
			y := a.frameTop()
			h := int(a.fx.h+0.5) + top + 2
			// The results sheet and the page capsule fade out under the
			// folding shell: the frame keeps them until they are gone.
			h = max(h, top+int(math.Ceil(float64(a.fx.h+a.belowExtent(a.fx.h, a.fx.resultsH)))))
			if a.shellUp() {
				// frameTop already reaches up for a capsule fold taller
				// than the drawn shell; the frame ends under the island,
				// whose rows the shell grew out of even when the Dock
				// lifted the panel off them.
				h = a.upShellFloor() - y
			} else if fold {
				h = max(h, top+a.capsuleBodyH()+2)
			}
			return mygo.Rectangle{X: a.stripCenter() - w/2, Y: y, Width: w, Height: h}
		}
		// The frame follows the level the panel is on, never the larger of
		// the two: an island opening on the sheet's box laid a 451pt
		// transparent frame — the shell is 420 — over the menu-bar icons
		// beside it. A level spring leaves the drawn shell wider or taller
		// than its target for a few frames and a spring on its way
		// overshoots, so the frame holds that shell and the level's
		// overshoot room. The width keeps the room at rest too: shrinking
		// it when the spring settles would shift the window under a shell
		// that has not moved, and the transparent columns beside the shell
		// no longer take clicks anyway (overlayAcceptsMouse).
		tw, th := a.panelBox()
		return a.openFrame(tw, th, a.resultsTargetH())
	}
	// An incoming system drag shrinks the resident strip to the anchor's
	// band: outside it ShelfU only shows a widened band-height hint, inside
	// it the shell grows 「松开放入」. The frame always covers the shape
	// being aimed at (plus corners) and the shell actually drawn, so the
	// morph, a fold remnant and the Spaces edge guard stay covered — and
	// the wings/capsule's taller frames are never held through a drag.
	if a.dragging && !a.ownDrag {
		w := a.bandW()
		h := top + a.menuBand()
		pw, ph := a.peekSize()
		w = max(w, int(math.Ceil(float64(pw)))+2*ears)
		h = max(h, top+int(math.Ceil(float64(ph))))
		if a.fx.peek {
			w = max(w, int(math.Ceil(float64(a.fx.w)))+2*ears)
			h = max(h, top+int(math.Ceil(float64(a.fx.h)))+1)
		}
		if a.inBand {
			// The cursor is inside the band (entered through the menu
			// bar): the frame must hold the guard's landing row even
			// while the shell is still the small hint — a suppressed
			// drag grows the window too, so the clamped cursor never
			// leaves its surface. Before entry the window stays
			// band-high; the guard's push lands on the band's last
			// row, which is already inside this frame.
			_, baseH := a.anchorSize()
			h = max(h, top+dropBandH(int(baseH)))
		}
		r := mygo.Rectangle{X: a.stripCenter() - w/2, Y: a.y, Width: w, Height: h}
		return a.frameCoverAppsPop(r, ph)
	}
	// The resident strip holds the largest peek shape for the current
	// state, so hovering only redraws; it resizes when a track comes or
	// goes, while nothing is drawn.
	pw, ph := a.peekSize()
	if a.np != nil && a.np.hasTrack() {
		// The capsule and the wings can alternate on the same strip: keep
		// room for both so one hand-off never resizes the window.
		mw, mh := a.wingsSize()
		pw, ph = max(pw, mw), max(ph, mh)
	}
	if a.fx.peek {
		// The shell can still be the larger shape it is folding back from,
		// e.g. the capsule just expired or a track stopped playing.
		pw, ph = max(pw, a.fx.w), max(ph, a.fx.h)
	}
	if a.fx.sizeMoving {
		// A morph in flight overshoots its target by a couple of points
		// (Bounce 0.15); the strip keeps headroom so no frame is clipped.
		pw, ph = pw*(1+openOvershoot), ph*(1+openOvershoot)
	}
	w := int(math.Ceil(float64(pw))) + 2*ears
	h := top + int(math.Ceil(float64(ph)))
	if a.capsuleUp(time.Now()) || a.fb.drawn {
		// The completion capsule is another peek shape: while it shows, the
		// strip holds room for it at the widest the text can make it, plus
		// its corners, shake and grow-out overshoot. The shape grows out in
		// ~160ms, well inside the tick that resizes the strip, so no frame
		// is cut.
		w = max(w, a.capsuleFrameW())
		h = max(h, top+a.capsuleBodyH()+2)
	}
	r := mygo.Rectangle{X: a.stripCenter() - w/2, Y: a.y, Width: w, Height: h}
	return a.frameCoverAppsPop(r, ph)
}

// openFrame is the window a settled open panel of tw × th needs with a
// results sheet of resultsH hanging under its bar. The drawn shell and the
// overshoot room of a spring on its way widen it. desiredFrame computes the
// current level through it; the session envelope asks it for every level and
// page the panel can reach.
func (a *shelf) openFrame(tw, th, resultsH float32) mygo.Rectangle {
	return a.openFrameIn(a.panelPlacement(), tw, th, resultsH, 0)
}

// openFrameIn is openFrame for a given placement. headroom > 0 builds the
// envelope's box for a candidate target: the panel may spring past its
// target by that many points, and the side is read from the candidate's
// placement rather than from the shell drawn now. A candidate on the
// upward side reaches from the panel's top (plus headroom) down to just
// under the island; the downward one keeps the drawn shell's row.
func (a *shelf) openFrameIn(place panelPlacement, tw, th, resultsH float32, headroom int) mygo.Rectangle {
	top := a.sheetTop()
	ears := a.earWidth()
	up := a.shellUp()
	y := a.frameTop()
	if headroom > 0 && place.Up {
		up, y = true, place.Y-headroom
	}
	w, roomH := a.frameRoom(tw, float32(th+float32(top)))
	// The frame is symmetric about the strip so the shell stays centred in
	// its canvas; a panel clamped away from the island's axis widens both
	// sides equally.
	off := abs(place.X + place.W/2 - a.stripCenter())
	w = max(w, 2*(off+place.W/2)+int(math.Ceil(float64(tw)*panelOpenOvershoot))+2*ears)
	w = max(w, int(math.Ceil(float64(a.fx.w)))+2*ears,
		frameCoverX(a.stripCenter(), a.shellCentreX(a.fx.openK), a.fx.w, ears))
	// Once settled the height is the shell's exact box, so its transparent
	// margin cannot swallow clicks below.
	h := max(int(math.Ceil(float64(th)))+top, int(math.Ceil(float64(a.fx.h)))+top)
	if headroom > 0 && place.Up {
		// The upward envelope covers the whole flight: from the panel's
		// top, with the spring's overshoot room, down to the island's row
		// and the results sheet or capsule hanging under it.
		below := max(th+a.belowExtent(th, resultsH), a.fx.h+a.belowExtent(a.fx.h, a.fx.resultsH))
		h := a.upShellFloor() - y + int(math.Ceil(float64(below-th)))
		return mygo.Rectangle{X: a.stripCenter() - w/2, Y: y, Width: w, Height: h}
	}
	if up {
		// The island's bottom is the shell's floor: frameTop already reaches
		// up for a shell that overshoots its level or still shows the page
		// while the bar folds back, and the frame ends two points under the
		// island, which stays on screen even when the Dock lifted the
		// panel's own bottom off it.
		h = a.upShellFloor() - y
	} else if a.fx.openMoving {
		h = max(h, roomH)
	}
	// Under the shell hang the results sheet and the page capsule: the
	// frame holds them at the drawn and the settled size, and the level's
	// overshoot room below them while a spring is in flight. An upward
	// panel hangs them under the island's row, where its shell's bottom
	// edge is.
	below := max(th+a.belowExtent(th, resultsH), a.fx.h+a.belowExtent(a.fx.h, a.fx.resultsH))
	if up {
		h = max(h, a.upShellFloor()-y+int(math.Ceil(float64(below-th))))
	} else {
		h = max(h, top+int(math.Ceil(float64(below))))
		if a.fx.openMoving {
			h = max(h, roomH+int(math.Ceil(float64(below-th))))
		}
	}
	return mygo.Rectangle{X: a.stripCenter() - w/2, Y: y, Width: w, Height: h}
}

// unionRect is the smallest rectangle holding both.
func unionRect(a, b mygo.Rectangle) mygo.Rectangle {
	if b.Width <= 0 || b.Height <= 0 {
		return a
	}
	if a.Width <= 0 || a.Height <= 0 {
		return b
	}
	x0, y0 := min(a.X, b.X), min(a.Y, b.Y)
	x1, y1 := max(a.X+a.Width, b.X+b.Width), max(a.Y+a.Height, b.Y+b.Height)
	return mygo.Rectangle{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// sessionFrame is the envelope an open panel holds for as long as anything
// is drawn: the union of the bar's widest form with its tallest results
// sheet, every enabled page, and the arrangement panel. Resizing the
// surface under a running spring presents the last canvas stretched into
// the new frame and makes the frame wait for its transaction, so one box
// covers every shape the panel can grow into and the window is resized only
// while the surface is blank.
//
// An island takes the envelope on the side its panel hangs on now; a box
// holding both sides would cover the display. A target that would land on
// the other side is left to the side change itself, which is allowed to
// move the window.
func (a *shelf) sessionFrame() mygo.Rectangle {
	r := a.desiredFrame()
	up := a.panelPlacement().Up
	side := func(sideH float32) bool {
		return !a.island || (a.anchorScreenTop()+int(sideH) > a.workArea().Y+a.workArea().Height) == up
	}
	// The bar shares the current page's side: the panel's side follows the
	// tallest box it takes, not the level drawn now.
	pageH := a.pageShellH()
	add := func(tw, th, resultsH, sideH float32) {
		if !side(sideH) {
			return
		}
		headroom := 0
		if up {
			headroom = int(math.Ceil(float64(th)*panelOpenOvershoot)) + 2
		}
		r = unionRect(r, a.openFrameIn(a.panelSpotFor(int(math.Ceil(float64(tw))), int(math.Ceil(float64(th))), up), tw, th, resultsH, headroom))
	}
	add(homeTypingW, float32(homeShellH), resultsMaxH, pageH)
	for _, p := range a.enabledPages() {
		w := pageShellWidth(p)
		if p.id() == pageHome {
			// The home page widens to its lyrics column when a track gains
			// lyrics while the panel is open; the envelope holds its widest
			// form from the start so that widening never resizes the surface
			// under the drawn page.
			w = max(w, homeLyricsW)
		}
		add(w, pageShellHeight(p), 0, pageShellHeight(p))
	}
	add(snapW, snapH, 0, pageH)
	return r
}

// beginSurfaceHold raises the native window to the envelope everything the
// panel can draw fits in. The window then keeps that frame until every
// spring has come to rest (view() drops it), so no spring resizes it.
func (a *shelf) beginSurfaceHold() {
	a.holdSurface(a.sessionFrame())
}

// holdSurface grows the held envelope (or starts one) to cover t. It does
// nothing while the shelf is settled closed, or while a drag owns the
// frame.
func (a *shelf) holdSurface(t mygo.Rectangle) {
	if a.win == nil || a.dragging || a.pointerDrag || a.islandDrag {
		return
	}
	// Nothing is up to hold a surface for: a closed, unpeeked shelf's
	// frame is the strip's own.
	if !a.visible && a.fx.openK <= 0.002 && !a.peeking && !a.fb.drawn && !a.fb.retracting {
		return
	}
	// A fold only shrinks: it runs inside the frame the panel has, and the
	// window is not widened for a box the folding shell no longer needs.
	if !a.visible && a.fx.openK > 0.002 && a.frame.Width > 0 {
		t = a.frame
	}
	if a.frameHold == (mygo.Rectangle{}) {
		a.frameHold = unionRect(a.desiredFrame(), t)
		return
	}
	a.frameHold = unionRect(a.frameHold, t)
}

// holdFaded reports whether the panel has finished folding and its content
// faded: the envelope has nothing left to hold and the window may return to
// the closed strip's exact box. The values are read directly, not through
// the zone's settled flags: the last fold frame snaps openK to zero and the
// next frame is the idle one, which draws nothing and returns before the
// spring bookkeeping ever runs.
func (a *shelf) holdFaded() bool {
	return !a.visible && a.progress <= 0.01 && a.fx.openK <= 0.002 && a.fx.peekK <= 0.002 &&
		!a.peeking && !a.fx.openMoving && !a.fx.sizeMoving &&
		(a.closeAt.IsZero() || time.Since(a.closeAt) >= closeFade)
}

// targetFrame is the frame the window maps. While a hold is up it is the
// envelope, grown to cover whatever the current frame asks for (never
// shrunk under a running spring). The envelope lasts as long as the panel
// stays open: dropping it at rest would make the next keystroke, result
// batch or page change resize the surface under drawn content, and a resized
// surface presents its previous canvas stretched for a display frame. Once
// the panel has folded away and nothing of it is drawn, the hold ends and
// the closed strip's exact box returns.
func (a *shelf) targetFrame() mygo.Rectangle {
	r := a.desiredFrame()
	if a.frameHold == (mygo.Rectangle{}) {
		return r
	}
	if a.holdFaded() {
		a.frameHold = mygo.Rectangle{}
		return r
	}
	// A drag owns the frame it draws: the drop band and its guard landing
	// row must stay the size WindowServer sees, so the band always takes
	// the pointer.
	if a.dragging || a.pointerDrag || a.islandDrag {
		return r
	}
	if a.flip.active {
		// A side change slides the window itself, so it owns the frame
		// while it runs. It lands with the exact box; the next transition
		// takes the envelope of the side it landed on.
		a.frameHold = mygo.Rectangle{}
		return r
	}
	a.frameHold = unionRect(a.frameHold, r)
	return a.frameHold
}

// frameCoverAppsPop grows the resident frame r (top edge at a.y) so it
// covers two app-switcher boxes: the wings' shell when asymmetric seats
// lean it off the anchor axis (a cornered island), and the popover pane
// that hangs under — or above — the shell. ph is the peek height the
// caller resolved. The wings union keys on appsDrawOff — the drawn
// state, not apps.on — so a mid-fold frame still widened by the wings
// never re-clips the shell it is folding from.
func (a *shelf) frameCoverAppsPop(r mygo.Rectangle, ph float32) mygo.Rectangle {
	if off := a.appsDrawOff(); off != 0 {
		// The drawn shell sits off the anchor axis by off: extend the
		// frame's left edge into the lean for a negative offset, its
		// right edge for a positive one.
		if off < 0 {
			ext := int(math.Ceil(float64(-off)))
			r.X -= ext
			r.Width += ext
		} else {
			r.Width += int(math.Ceil(float64(off)))
		}
	}
	// The wings' hover tips hang under their buttons, outside the drawn
	// shell: reserve the strip below the shell for them so the frame holds
	// the tip the way it holds the shell. The strip is transparent and the
	// tip itself passes the pointer through, so clicks below the shell are
	// unaffected. Which shell counts is the drawn one, like the width
	// above: a fold leaves it in place until the wings are gone.
	if a.apps.on || a.fx.peekKind == peekApps {
		h := ph
		if a.fx.h > h {
			h = a.fx.h
		}
		bot := a.y + a.sheetTop() + int(math.Ceil(float64(h))) + appsTipRoom
		if bot > r.Y+r.Height {
			r.Height = bot - r.Y
		}
	}
	if !a.apps.more {
		return r
	}
	h := ph
	if a.fx.h > h {
		h = a.fx.h
	}
	pop := a.appsPopSpot(h)
	if pop.X < r.X {
		r.Width += r.X - pop.X
		r.X = pop.X
	}
	if right := pop.X + pop.Width; right > r.X+r.Width {
		r.Width = right - r.X
	}
	if pop.Y < r.Y {
		r.Height += r.Y - pop.Y
		r.Y = pop.Y
	}
	if bot := pop.Y + pop.Height; bot > r.Y+r.Height {
		r.Height = bot - r.Y
	}
	return r
}

func (a *shelf) syncWindowFrame() {
	if a.win == nil || a.settingFrame {
		return
	}
	r := a.targetFrame()
	if r == a.frame {
		return
	}
	if a.buildingView && a.winH != 0 {
		// MyGo captured the surface size before view() ran. Resizing here
		// would present this frame's old canvas in the new native frame.
		// Update posts to the next main-loop pass; resolve the latest target
		// there, including any close or reopen issued in the meantime.
		if !a.frameQueued {
			a.frameQueued = true
			a.win.Update(func() {
				a.frameQueued = false
				if a.win != nil && !a.win.IsDestroyed() {
					a.syncWindowFrame()
				}
			})
		}
		return
	}
	a.settingFrame = true
	defer func() { a.settingFrame = false }()
	mac.SetWindowFrame(a.winH, r.X, r.Y, r.Width, r.Height)
	a.frame = r
	// The shell moved with the window, so the pointer may now be on it (a
	// display change recentres the overlay under a still pointer) or off
	// it. Re-ask before the frame that will draw the new position.
	a.syncPointerGate()
	a.win.Invalidate()
}

// Native window movement can invalidate our cached frame independently of a
// state transition. Never let the cached target hide a displaced window.
func (a *shelf) windowGeometryChanged() {
	if a.settingFrame || a.win == nil {
		return
	}
	if actual := a.win.Bounds(); actual == a.targetFrame() {
		a.frame = actual
		return
	}
	a.frame = mygo.Rectangle{}
	a.syncWindowFrame()
}

// peekSize is the hover shape: the status capsule while the closed panel
// has something to report, the now-playing wings while a track is loaded
// (the notch grows 52pt to each side, barely below), the drop pill while a
// drag is over the notch band, the narrow drag hint while one is in
// flight, otherwise a bulge carrying a chevron. The glyphs sit wholly
// below the hardware notch: nothing drawn inside it is visible.
func (a *shelf) peekSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	if a.pinReturning || a.petReturning {
		// A pinned card or the carried otter held over the notch wins
		// over the capsule and the wings: the release reads this same
		// zone, so the affordance must always be the pill it acts on
		// (design §4 松开放回).
		return baseW + dropPillExtraW, baseH + dropPillBody
	}
	now := time.Now()
	if a.capsuleUp(now) {
		// The completion capsule takes the peek shape while the panel is
		// closed: it is what the shell grows into, whether or not a track
		// is loaded.
		return a.capsuleSize()
	}
	if a.dragging && !a.ownDrag {
		if a.dropHot {
			return baseW + dropPillExtraW, baseH + dropPillBody
		}
		// Drag hint: the anchor's own height, a little wider — visible in
		// the menu bar, never over the windows below it.
		return baseW + dropHintExtraW, baseH
	}
	if a.appsModeOn() {
		// The app switcher owns the closed shell until it ends: the wings
		// it draws are the shape the frame and the gate read.
		return a.appsSize()
	}
	// Charging, headphones and a finished download take the notch while
	// their words are up; a running download holds the wings below them.
	if ev, ok := a.liveNow(now, a.liveAllow()); ok {
		return a.liveShape(ev)
	}
	if a.downloadRunning() {
		return a.downloadShape()
	}
	if a.np != nil && a.np.showsPlayer() {
		return a.wingsSize()
	}
	return baseW + 40, baseH + peekBody
}

// liveWingW is how far each side grows for charging and downloads: the
// notch's camera housing forces the right-hand content into one visible
// wing (112), while the island's continuous pill uses the preview's 88.
func (a *shelf) liveWingW() int {
	if a.island {
		return islandLiveWingW
	}
	return liveWingW
}

// wingsSize is the hover wings' size: the anchor plus npWingsW on each
// side, and npWingsBody below it. Wide notches keep their width and gain
// the same wings; the island's 120pt capsule grows to 224 × 34.
func (a *shelf) wingsSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	return baseW + 2*npWingsW, baseH + npWingsBody
}

// shellSize is the one interpolation the shell Path and the content layout
// share: hardware notch → peek shape peekW×peekH (peekK) → search bar →
// page (levelK, then openK). The bar is 520 wide and a page 440, so a page
// only ever grows taller. The caller passes the drawn values: view()
// animates them per display frame and substitutes final ones in headless
// tests.
func (a *shelf) shellSize(openK, levelK, peekK, peekW, peekH float32) (w, h float32) {
	baseW, baseH := a.anchorSize()
	// The search bar widens while a query is typed; the shell takes the
	// width view() drew, or the target before the first frame.
	barW := a.fx.homeW
	if barW <= 0 {
		barW = a.homeBoxW()
	}
	lvlW := barW + (a.pageShellW()-barW)*levelK
	lvlH := float32(homeShellH) + (a.pageShellH()-float32(homeShellH))*levelK
	if a.snap.panel || a.snap.drawn {
		// The arrangement panel is its own box; it never morphs through
		// the sheet's size.
		lvlW, lvlH = snapW, snapH
	}
	w0 := baseW + (peekW-baseW)*peekK
	h0 := baseH + (peekH-baseH)*peekK
	return w0 + (lvlW-w0)*openK, h0 + (lvlH-h0)*openK
}

// capsuleFrameW is the window width the completion capsule needs once the
// glow margin is gone: the widest capsule, plus both ear corners, the
// failure shake's reach, and the grow-out spring's overshoot (Bounce 0.28
// widens the notch→capsule delta by about openOvershoot).
func capsuleFrameW() int {
	wide := int(math.Ceil(float64((fbMaxW - 180) * openOvershoot)))
	return fbMaxW + 2*(earR+fbShakeX+wide)
}

// peekPillRect is the on-screen box of the hovered notch capsule, matching
// the shell drawn in view(): the now-playing pill while a track is
// loaded, otherwise the chevron bulge. A press anywhere on it expands the
// shelf — the design has no in-pill controls. While the completion capsule
// or a fold in flight is on the shell the box is the shell actually drawn
// (a fold remnant still takes its own clicks), shifted by the error shake
// so the moving capsule keeps its own hit box.
func (a *shelf) peekPillRect() mygo.Rectangle {
	w, h := a.peekSize()
	if (a.peeking || a.fb.drawn || a.fb.retracting || a.peekProgress > 0.01) && a.fx.w > 0 {
		w, h = a.fx.w, a.fx.h
	}
	y, cx := a.frameTop()+a.sheetTop(), a.stripCenter()
	if a.shellUp() {
		// A peek or a completion capsule in flight on an upward panel is
		// drawn at the animated shell, not at the island's anchor row: the
		// pill's box follows the same top and axis the drawing uses.
		y = a.frameTop() + int(math.Round(float64(a.shellTopLocal(h, a.fx.openK))))
		cx = a.shellCentreX(a.fx.openK)
	}
	x := cx - int(w/2) + int(a.fx.shakeX+a.appsDrawOff())
	return mygo.Rectangle{X: x, Y: y, Width: int(w + 0.5), Height: int(h + 0.5)}
}

func (a *shelf) overPeekPill(p mygo.Point) bool {
	r := a.peekPillRect()
	return r.Width > 0 && r.Height > 0 &&
		p.X >= r.X && p.X < r.X+r.Width &&
		p.Y >= r.Y && p.Y < r.Y+r.Height
}

// mouseDown is the AppKit mouse-down observer. inside means the event was
// delivered to our window; otherwise it went to another app or the menu bar.
// Every left press also reaches the drag tracker: the 30 ms board poll can
// miss a release between two drags, and the press is the edge that splits
// their serials (see dragTracker.press).
func (a *shelf) mouseDown(inside bool, button int, p mygo.Point) {
	if button == 0 {
		mac.NoteLeftPress()
	}
	if a.quitting {
		return
	}
	a.wakeMonitor()
	// A left press on our own window is the user's gesture, not an
	// incoming drag: park the notch corridor for as long as the button is
	// held (released in monitorDrag), so a small wobble on a band icon
	// under the corridor cannot be pushed off the target.
	// A new foreign left press also ends any previous ownership when the
	// monitoring tick missed the intervening release.
	if button == 0 {
		a.ownPress = inside
		a.syncDragGuard()
	}
	// A press on another screen's idle capsule moves the resident window
	// there first — every screen's island opens its own panel. It arms the
	// drag the same way, so an idle capsule anywhere can be moved.
	if !inside && button == 0 && !a.visible && a.idleIslandAt(p) {
		a.place(p)
		a.beginIslandPress(p)
		return
	}
	// A press on the app entry that slipped to another surface still opens
	// the switcher — the entry's drawn box is the click target, and an
	// inside press already reached the button itself through the window.
	if a.overAppEntry(p) && !a.visible {
		if !inside {
			a.openAppSwitch()
		}
		return
	}
	// The capsule is drawn over the menu bar's empty centre. WindowServer
	// often gives that press to the menu bar or the app underneath, so the
	// overlay paints a "click to expand" pill that never receives the click.
	// A press on the visible pill expands either way — except a press the
	// hover wings received itself: they have no controls, and their own
	// handler opens the shelf for a press anywhere on them. Only a press
	// inside the drawn shape acts: transparent window margin neither opens
	// the panel nor claims the event for it.
	// The idle island is a click target of its own: its window takes the
	// press only while the pointer is on the capsule, and a press that
	// slipped through to the window underneath still reaches the island —
	// the capsule is always visible, unlike the notch's hidden band. The
	// press only arms the move; the release either drops the panel open (a
	// tap) or ends the drag where it stopped. A finished download's capsule
	// is a button of its own; its view must receive the press, so it is
	// never swallowed here. The app switcher's wings are the shell while
	// the mode is open: they carry real buttons, so no press on them arms
	// the island move or expands the panel.
	if a.island && !a.visible && !a.appsModeOn() && !a.liveClickable(time.Now()) && a.overShell(p) {
		a.beginIslandPress(p)
		return
	}
	if (a.peeking || a.fb.drawn || a.fb.retracting || a.peekProgress > 0.01 || a.appsModeOn()) && !a.visible && a.overPeekPill(p) {
		// The completion capsule is a button of its own: its view adds the
		// file and opens the tray page. The shell must not swallow that
		// press by expanding first. The switcher's buttons likewise take
		// their own presses.
		if inside && a.liveClickable(time.Now()) {
			return
		}
		if inside && a.peeking && a.np != nil && a.np.showsPlayer() {
			return
		}
		if a.apps.on {
			// 中央仍是搜索：外路由按压落在锚带就走原展开路径（与带内
			// 点击同义），翼内空白才真的空掉。
			if !inside && a.overAnchor(p) {
				a.show(false)
			}
			return // wing buttons take the press; blank wing space is inert
		}
		a.show(false)
		return
	}
	if inside {
		// Esc and Space are taken only while the panel is open; a press
		// on the collapsed notch band or a closing panel must not hold them.
		if a.visible {
			a.hidePending = false // the user came back before the catch ended
			a.holdKeys()
			// The open panel's band is a handle, but only its blank
			// stretches: the page icons, the activity chips, the settings
			// button and the page below keep their own presses.
			if a.island && !a.focusMode && !a.snap.panel && !a.snap.drawn && a.bandBlank(p) {
				a.beginPanelPress(p)
			}
		}
		a.takeServiceOwnership()
		return
	}
	a.releaseKeys()
	// A left press delivered to another app may be the start of a window
	// drag; the arrangement panel follows it from here (snap.go).
	a.snapPress(false, button, p)
	menuBottom := a.notchY + max(36, a.notchH+4)
	inMenuBar := p.Y >= a.notchY && p.Y <= menuBottom
	if a.island {
		// The island may float far below the menu bar. Only the bar itself
		// and the island's own capsule are the shell; the rows between them
		// are the desktop's. A panel that grew upward sits above the
		// island, so its box counts too.
		inMenuBar = inMenuBar || insideRect(p, a.islandRect())
	}
	if a.apps.on && !a.overAppsPop(p) && !a.overAppEntry(p) {
		// 锚点（刘海 / 灵动岛胶囊）上的外路由按压仍是搜索——和带内
		// UI 一样先收两翼再展开，不在激活后把焦点切回去。
		if a.overAnchor(p) {
			a.show(false)
			return
		}
		// 点外面取消应用切换：浮层与自己的收起态入口不算外面。
		// Apps mode never opened the panel or took another app's focus,
		// so there is nothing to hand back — endAppSwitch only folds.
		a.endAppSwitch()
		return
	}
	if a.visible && !a.dragging && !inMenuBar && !insideRect(p, a.panelDropRect()) {
		if a.drop.catchOn(time.Now()) {
			// The otter is still holding what it just caught: the panel
			// stays for the animation, and settlePendingHide closes it
			// when the retract ends.
			a.hidePending = true
			return
		}
		a.hide(hidePassive)
	}
}

// windowLevel is the overlay's one window-level policy, applied by
// syncMousePassThrough on every state change: a file drag sits above the
// menu bar so its strip cannot steal the drop; keyboard focus (clipboard
// search) must drop below kCGDockWindowLevel or the input method's
// candidate panel — drawn at 20 and anchored inside the opaque panel —
// stays completely hidden behind it (marked text still composes inline,
// which is why the bug reads as "IME types but no candidates").
func (a *shelf) windowLevel() int {
	switch {
	case a.dragging || a.pointerDrag:
		return mac.PopUpMenuWindowLevel
	case a.focusMode:
		return mac.SearchKeyboardLevel
	default:
		return mac.StatusWindowLevel
	}
}

// While idle the overlay ignores the mouse altogether, so clicks under and
// beside the notch reach whatever is below. An in-flight drag takes the
// whole window: file drags to the top of the notch must hit a window, or
// WindowServer treats Y=0 as the Spaces edge (pointerDrag flips this on
// the drag's first movement, long before the top). Every other state takes
// the mouse only over the shell the last frame drew: the window is a
// rectangle (the shell plus its transparent ear margins) sitting above the
// menu bar, and WindowServer routes a press to a window before any view is
// hit-tested, so a window-wide press would eat the menu-bar icons beside
// the shell. A closing panel follows the drawn shell (fx.openK), not the
// tick's eased progress: the moment the shell has folded back to the peek
// shape the window lets clicks through again.
func (a *shelf) syncMousePassThrough() {
	a.syncWindowFrame()
	setGatePolling(anyOverlayWanted())
	accept := a.overlayAcceptsMouse()
	a.ignoringMouse = !accept
	if a.win == nil {
		return
	}
	h := a.winH
	mac.SetOverlayAcceptsMouse(h, accept)
	mac.SetWindowLevel(h, a.windowLevel())
}

// overlayWanted reports whether the current state needs the overlay to take
// the mouse at all: hovering, opening, open, folding, or a capture capsule.
func (a *shelf) overlayWanted() bool {
	return a.visible || a.fx.openK > 0.02 || a.dragging || a.pointerDrag || a.islandDrag ||
		a.peeking || a.peekProgress > 0.01 || a.fb.drawn || a.fb.retracting ||
		a.appsModeOn() || a.liveUp()
}

// pointerSample is the pointer a monitoring pass reads: the caller caches
// the sample once (drag.monitorDrag, or the tick), and every hit test in
// the pass reuses it. Without a pass it is a direct read.
func (a *shelf) pointerSample() mygo.Point {
	if a.pointerTickValid {
		return a.pointerTick
	}
	return cursorPoint()
}

// overlayAcceptsMouse answers whether the overlay takes the mouse at the
// current pointer, reading the pointer only when the answer depends on it:
// a headless surface has no window to gate, a drag owns the whole frame
// (the drop and the Spaces edge guard land on the window, not on the drawn
// shape), and an idle overlay ignores the mouse everywhere.
func (a *shelf) overlayAcceptsMouse() bool {
	wanted := a.overlayWanted()
	if a.winH == 0 {
		return wanted
	}
	if a.dragging || a.pointerDrag || a.islandDrag {
		return wanted
	}
	p := a.pointerSample()
	if !wanted {
		// A notched shelf at rest draws nothing: every press belongs to
		// whatever is under the strip. The island's resting capsule is a
		// real, visible target and takes presses on its own shape.
		return a.island && a.overShell(p)
	}
	if a.overShell(p) || a.overBelow(p) || a.overAppsPop(p) {
		return true
	}
	// The drawn shell is not the only shape a press can belong to. An
	// opening island, the sheet springing out of the island and a capsule
	// growing out of the notch reach their box a frame or two later, and
	// the frame already covers it (desiredFrame follows the level), while a
	// still pointer produces no move event to re-ask with. So the shape the
	// state settles at counts too: a press that lands where the shell is
	// about to be must reach ShelfU, not the window underneath.
	tw, th, tl, tk := a.settledShell()
	return a.overShellShapeAt(p, tw, th, tl, a.fx.shakeX, tk)
}

// settledShell is the shell the current state is heading for: the level's
// box while the panel is open, the peek shape (wings, drop pill, completion
// capsule, chevron) while it is closed. levelK is the drawn corner's level
// progress at rest and openK the axis the shell settles on, so the hit shape
// rounds and sits exactly like the settled frame.
func (a *shelf) settledShell() (w, h, levelK, openK float32) {
	var peekK float32
	if a.visible {
		openK = 1
	}
	if a.level >= 2 {
		levelK = 1
	}
	if a.peeking || a.peekProgress > 0.01 || a.fb.drawn || a.fb.retracting || a.appsModeOn() {
		peekK = 1
	}
	pw, ph := a.peekSize()
	w, h = a.shellSize(openK, levelK, peekK, pw, ph)
	return w, h, levelK, openK
}

// The pointer gate's sources are installed once per process and fan out to
// every overlay that follows the pointer: ShelfU owns one, and the smoke
// harness builds one per test. A watch bound to the first shelf left every
// other window's flag stale — its gate could only change on the next frame,
// never on a move.
var (
	pointerWatched bool
	pointerShelves []*shelf
)

// gatePollInterval is how often the gate re-reads the pointer while a shell
// is on screen. Move events alone are not enough: macOS coalesces
// mouse-moved events and an inactive overlay's event may be delivered tens
// of milliseconds late, so a press can arrive after the pointer reached the
// shell but before the monitor ran — WindowServer would hand that press to
// the window underneath. Polling bounds that window to one interval.
const gatePollInterval = 10 * time.Millisecond

// gatePolling mirrors "some overlay is on screen", set on the main thread so
// the poller never reads a shelf off it (the fields are main-thread only).
var gatePolling atomic.Bool

// gateMoveUntil is the last move plus gatePollWindow, in Unix nanoseconds.
// The poller re-reads the pointer only while move events are still likely
// to arrive late or be coalesced; a still pointer is covered by shape
// changes, which call syncPointerGate directly.
var gateMoveUntil atomic.Int64

// gatePollWindow bounds the poll after a move. It only has to cover the
// event delivery/coalescing gap documented on gatePollInterval.
const gatePollWindow = 150 * time.Millisecond

// wakeGatePoll restarts the poller after a move when an overlay already
// needs it. setGatePolling only signals on the off→on edge, so a move that
// lands while the gate was already on would otherwise leave the poller
// asleep.
func wakeGatePoll() {
	if !gatePolling.Load() {
		return
	}
	select {
	case gateWake <- struct{}{}:
	default:
	}
}

// gateShouldPoll reports whether the move window is still open. It is the
// only reason the 10 ms poller wakes; a still pointer is covered by the
// direct syncPointerGate calls every shape change makes.
func gateShouldPoll(now time.Time) bool {
	return now.UnixNano() < gateMoveUntil.Load()
}

// gateWake carries "polling started" to the poller, which sleeps otherwise.
var gateWake = make(chan struct{}, 1)

// watchPointer registers the shelf with the pointer gate's sources. A
// settled panel draws no frames, so nothing else would notice the pointer
// leaving the drawn shell — and WindowServer decides which window a press
// reaches from ignoresMouseEvents, which only the pointer position keeps
// current. Both sources are mouse-only, so neither needs Accessibility.
// They live as long as the process.
func (a *shelf) watchPointer() {
	if a.watchingPointer {
		return
	}
	a.watchingPointer = true
	pointerShelves = append(pointerShelves, a)
	if pointerWatched {
		return
	}
	pointerWatched = true
	mac.ObserveMouseMoved(func() {
		for _, s := range pointerShelves {
			s.pointerMoved()
			s.syncPointerGate()
		}
	})
	go pollPointerGate()
}

// pollPointerGate samples the pointer while a shell is on screen, so the
// flag is current without waiting for an event. The sample itself runs on
// the main thread: every field the gate reads belongs to it.
func pollPointerGate() {
	for range gateWake {
		for gatePolling.Load() {
			if !gateShouldPoll(time.Now()) {
				break
			}
			time.Sleep(gatePollInterval)
			mygo.RunOnMain(func() {
				if !gatePolling.Load() {
					return
				}
				for _, s := range pointerShelves {
					s.syncPointerGate()
				}
			})
		}
	}
}

// anyOverlayWanted reports whether any registered overlay is on screen, so
// one idle shelf cannot stop the poller another shelf relies on. Main
// thread only.
func anyOverlayWanted() bool {
	for _, s := range pointerShelves {
		if s.overlayWanted() {
			return true
		}
	}
	return false
}

// setGatePolling tells the poller that a shell is (or is no longer) on
// screen. On the main thread only.
func setGatePolling(on bool) {
	if gatePolling.Swap(on) == on {
		return
	}
	if on {
		select {
		case gateWake <- struct{}{}:
		default:
		}
	}
}

// syncPointerGate re-asks the shell-shaped hit test at the current pointer
// and applies the answer, touching the native flag only when it changed. It
// is both the pointer watch's entry and the refresh every change of the
// shell's shape or position goes through (window frame, show, level, page,
// fold, capsule, drag end), so no press waits for a move event.
func (a *shelf) syncPointerGate() {
	if a.win != nil && a.win.IsDestroyed() {
		return
	}
	accept := a.overlayAcceptsMouse()
	if accept == !a.ignoringMouse {
		return
	}
	a.ignoringMouse = !accept
	if a.win == nil {
		return // headless surface: the flag is all there is
	}
	mac.SetOverlayAcceptsMouse(a.winH, accept)
}

// overShell reports whether p is on the shell the last frame drew — the
// black body plus the two ear corners its top edge reaches past its
// flanks, and not the corners its bottom rounds off. Screen coordinates,
// top-left origin. The shell is centred on stripCenter and hangs from
// a.y + sheetTop() in the window, exactly as view() draws it.
func (a *shelf) overShell(p mygo.Point) bool {
	return a.overShellShapeAt(p, a.fx.w, a.fx.h, a.fx.levelK, a.fx.shakeX, a.fx.openK)
}

// overShellShape is overShell for an explicit shell drawn at the current
// open fraction.
func (a *shelf) overShellShape(p mygo.Point, w, h, levelK, shakeX float32) bool {
	return a.overShellShapeAt(p, w, h, levelK, shakeX, a.fx.openK)
}

// overShellShapeAt is overShell for an explicit shell: w×h, its bottom
// corner rounded by the island/sheet radius at levelK, shifted by shakeX,
// on the axis the shell has at openK. The drawn shell spreads from the
// island's axis toward the panel's exactly as view() draws it, so a caller
// holding the shape a state settles at passes its open fraction too.
func (a *shelf) overShellShapeAt(p mygo.Point, w, h, levelK, shakeX, openK float32) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	top := float64(a.frameTop() + a.sheetTop())
	if a.shellUp() {
		top = float64(a.frameTop()) + float64(a.shellTopLocal(h, openK))
	}
	y := float64(p.Y) - top
	if y < 0 || y >= float64(h) {
		return false
	}
	cx := float64(a.shellCentreX(openK)) + float64(shakeX+a.appsDrawOff())
	x0, x1 := cx-float64(w)/2, cx+float64(w)/2
	corner := float64(homeRadius) + float64(sheetRadius-homeRadius)*float64(levelK)
	if a.island {
		// Four round corners, no top ears: the island's path is a rounded
		// rectangle (islandPath's circular arcs) whose radius shrinks to
		// half the height for a capsule. The hit edge follows the circle.
		r := math.Min(float64(h)/2, corner)
		inset := func(d float64) float64 {
			if r <= 0 || d >= r {
				return 0
			}
			return r - math.Sqrt(sq(r)-sq(r-d))
		}
		cut := math.Max(inset(y), inset(float64(h)-y))
		x := float64(p.X)
		return x >= x0+cut && x < x1-cut
	}
	left, right := x0, x1
	// inset is how far the path's corner cuts in d points from the edge it
	// starts on, for radius r: 0 at the flank, r at the outer edge. Both
	// top ears and bottom corners are quadratics with their control point
	// in the corner, which reduce to exactly this.
	inset := func(d, r float64) float64 {
		if r <= 0 || d >= r {
			return 0
		}
		return r * sq(1-math.Sqrt(d/r))
	}
	if ear := float64(earR); y < ear {
		// The ear: the top edge runs earR past the flank at y == 0 and
		// meets the flank at earR below.
		left, right = x0-inset(y, ear), x1+inset(y, ear)
	}
	if botR := math.Min(float64(h)/2, corner); y > float64(h)-botR {
		cut := inset(float64(h)-y, botR)
		left, right = x0+cut, x1-cut
	}
	x := float64(p.X)
	return x >= left && x < right
}

// sq is the square of v.
func sq(v float64) float64 { return v * v }

// setPeek animates the notch shell between its idle hardware shape and the
// peeked "I'm here" pill, and updates event pass-through to match.
func (a *shelf) setPeek(peek bool) {
	if a.peeking == peek {
		return
	}
	a.peeking = peek
	a.peekFrom, a.peekAnimStart = a.peekProgress, time.Now()
	// A peek starts from a blank surface: take the envelope before the pill
	// is drawn, so neither the pill's morph nor a later opening resizes it.
	if peek {
		a.beginSurfaceHold()
	}
	a.syncMousePassThrough()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// panelH is the open panel's fixed height: the arrangement panel while a
// window-drag session owns the shell, otherwise the sheet's 250. Both come
// from layout constants, never from page content.
func (a *shelf) panelH() int {
	if a.snap.panel || a.snap.drawn {
		return snapH
	}
	return int(a.pageShellH())
}

// pageShellW is the open panel's width for the page on screen: 440 for
// every page but the home page, which asks for 640 beside its lyrics. The
// shell path, the window frame and the page layout all read this one
// number, so a track gaining lyrics cannot leave the shell and its content
// on different widths.
func (a *shelf) pageShellW() float32 { return pageShellWidth(a.currentPage()) }

// pageShellH is the open panel's height for the page on screen: the sheet's
// 250 for every page but a reading page (the 每周简报 asks for 600). The
// shell path, the window frame and the page layout all read this number.
func (a *shelf) pageShellH() float32 { return pageShellHeight(a.currentPage()) }

// pageShellWidth is pageShellW for a page that may not be current: the
// outgoing picture of a switch keeps its own width while it fades.
func pageShellWidth(p panelPage) float32 {
	if p == nil {
		return sheetW
	}
	if w, ok := p.(pageWidth); ok {
		return max(sheetW, w.width())
	}
	return sheetW
}

// pageShellHeight is pageShellH for a page that may not be current.
func pageShellHeight(p panelPage) float32 {
	if p == nil {
		return sheetH
	}
	if h, ok := p.(pageHeight); ok {
		return max(sheetH, h.height())
	}
	return sheetH
}

// pageBodyHeight is the page area a page draws in: the shell minus the band
// above it and the shell's bottom padding (200 at 250, 550 at 600).
func pageBodyHeight(p panelPage) float32 {
	return pageShellHeight(p) - bandH - bodyGap - padBottom
}

// pageContentHeight is the content box below a page's action row. A full
// bleed page owns the whole page area instead.
func pageContentHeight(p panelPage) float32 {
	return pageBodyHeight(p) - pageActionH - pageActionsGap
}

// pageBoxW is the width a page draws in inside the shell's horizontal
// padding.
func pageBoxW(p panelPage) float32 { return pageShellWidth(p) - 2*sheetPadX }

// pageBleeds reports whether a page owns the whole page area (no action
// row above its content).
func pageBleeds(p panelPage) bool {
	b, ok := p.(pageBleed)
	return ok && b.fullBleed()
}

// panelBox is the open panel's target size. The arrangement panel replaces
// the island box; it is reached by the open spring out of the notch, never
// by a level change.
func (a *shelf) panelBox() (w, h float32) {
	if a.snap.panel || a.snap.drawn {
		return snapW, snapH
	}
	if a.currentLevel() == 2 {
		return a.pageShellW(), a.pageShellH()
	}
	return a.homeBoxW(), homeShellH
}

// panelContentH is the height of the content block the panel draws at level
// progress levelK: the search bar's 76 at rest, the sheet's 250 at level 2,
// the arrangement panel's fixed 150. The shell interpolates its own height
// with the same levelK, so an upward block and its shell keep one top edge
// through a level change and only the shell's bottom edge moves.
func (a *shelf) panelContentH(levelK float32) float32 {
	if a.snap.panel || a.snap.drawn {
		return snapH
	}
	return float32(homeShellH) + (a.pageShellH()-float32(homeShellH))*levelK
}

func (a *shelf) show(edge bool) {
	a.endAppSwitch() // 面板与应用切换互斥: 展开前两翼先退场
	a.apps.entryRect = mygo.Rectangle{}
	if a.visible {
		return
	}
	// A permission request owns the notch: a click on it must not fold the
	// card away behind an open panel while its hook is still waiting.
	if a.approval != nil && a.approval.currentAsk() != nil {
		return
	}
	a.hidePending = false
	// 跟随系统时，系统语言可能已经变了（用户在系统设置里改过）：每次打开
	// 面板都按同一条规则重新解析；显式选择的语言不受影响。
	a.followSystemLanguage()
	// Transient notices never pop out after the panel closes.
	a.clearLive()
	if a.win != nil {
		a.placeAt(mygo.Screen.CursorScreenPoint())
	}
	if a.store != nil {
		a.store.Refresh()
	}
	// Opening after a close in flight keeps the old view until the fade is
	// over; cancel that pending reset so the fresh opening cannot be
	// clobbered a frame later.
	a.closeAt = time.Time{}
	// A drag or the clipboard hotkey opens straight on the page it needs; a
	// plain click on the notch opens the search bar on the tray.
	level := 1
	a.tabAt = time.Time{}
	switch {
	case a.onboardingPending():
		// The guide owns the panel while it is owed: every opening — the
		// click, the hotkey, a drag hovering — lands on it, never on the
		// search bar or another page.
		level, a.tabFrom, a.tab = 2, pageOnboarding, pageOnboarding
	case a.focusMode || a.ocrSheet:
		// The caller already chose the page (the clipboard search, the
		// recording card); it keeps it.
		level = 2
	case edge || (a.dragging && !a.ownDrag) || a.drop.on:
		level, a.tabFrom, a.tab = 2, pageTray, pageTray
	default:
		a.tabFrom, a.tab = pageTray, pageTray
	}
	a.level = level
	a.jumpLevel = true
	a.lastAct = time.Now() // using the shelf is activity: the otter stays awake
	a.snap.drawn = a.snap.panel
	a.visible, a.edgeOpened, a.received = true, edge, false
	a.dropZoneSnap(edge)
	a.fx.openMoving = true
	a.animationFrom, a.animationStart = a.progress, time.Now()
	// The search bar takes the keyboard as it grows (below); focus mode
	// lowers an island's panel row for the input method, so the envelope
	// must be measured with that row already in place. Remember the app the
	// keyboard came from here, while it is still the frontmost one.
	if level == 1 && !a.snap.panel {
		if !a.focusMode {
			if pid := mac.FrontmostPID(); pid > 0 && pid != mac.OwnPID() {
				a.prevAppPID = pid
			}
		}
		a.focusMode = true
		a.resetHomeSearch()
		a.warmSpotlight()
	}
	// The surface was blank before this opening (or holds the previous
	// envelope): raise it to the envelope the whole panel fits in now,
	// while nothing of the opening is drawn yet, so no spring resizes it.
	a.beginSurfaceHold()
	a.contentFrom = a.fx.revealK
	prevFrame := a.frame
	a.syncMousePassThrough()
	a.syncDragGuard()
	if a.win != nil {
		a.win.SetOpacity(1)
		a.win.ShowInactive()
		// ShowInactive 不夺焦，面板收不到按键，只能临时注册全局 Esc 与空格。
		a.holdKeys()
		if a.frame == prevFrame {
			a.win.Invalidate()
		}
	}
	// The search bar takes the keyboard as it grows: typing starts at
	// once. The arrangement panel is not a search and keeps it away. The
	// keyboard mode was taken above, before the envelope was measured.
	if level == 1 && !a.snap.panel {
		a.takeKeyboard()
	}
	// The guide holds its own keys while its page is the one on screen.
	a.syncOnboardingKeys()
}

// Global hotkey seams: holdKeys/releaseKeys register and unregister the
// panel's Esc and Space, which needs the app running; tests stub them to
// check who owns the keys.
var (
	registerKey   = mygo.GlobalShortcut.Register
	unregisterKey = mygo.GlobalShortcut.Unregister
)

// holdKeys takes Esc (close Quick Look, then collapse) and Space (Quick
// Look the selection) while the shelf is in use; releaseKeys returns them
// as soon as the user clicks in another app.
func (a *shelf) holdKeys() {
	if !a.keysAreOurs() {
		// 设置窗口在前台时它自己处理 Esc 与空格（Esc/⌘W 关窗、控件收
		// 空格），全局注册会把按键从它手里抢走；剪贴板搜索模式的窗口同
		// 理。两条路径都先确保已注册的按键已经归还。
		a.releaseKeys()
		return
	}
	if !a.escHeld {
		if err := registerKey("Escape", a.escape); err != nil {
			a.report(errors.New(a.appLoc().StatusEscBusy(err.Error())))
		} else {
			a.escHeld = true
		}
	}
	if !a.spaceHeld {
		// Without Space, Quick Look stays available from the context menu.
		a.spaceHeld = registerKey("Space", a.toggleQuickLook) == nil
	}
	a.syncCopyKey()
}

// ShowInactive does not give the overlay keyboard focus. Hold ⌘C like
// Space only while a selected tray picture can actually be copied.
const copyKey = "Cmd+C"

// wantsCopyKey reports whether the tray would act on ⌘C right now: the
// panel is open with its keys, on the tray page, and exactly one selected
// item is a picture. Pure; unit-tested.
func (a *shelf) wantsCopyKey() bool {
	if !a.visible || !a.keysAreOurs() || a.tab != pageTray || a.selection.count() != 1 || a.store == nil {
		return false
	}
	for _, item := range a.store.Items {
		if a.selection.has(item.ID) {
			return !item.OffTray && !a.isLeaving(item) && convertible(item)
		}
	}
	return false
}

// syncCopyKey follows selection changes without reclaiming keys released
// by an outside click. Headless views have no native key to acquire.
func (a *shelf) syncCopyKey() {
	if a.win == nil {
		return // headless test shelf: no app to register with
	}
	switch want := a.wantsCopyKey() && (a.escHeld || a.spaceHeld); {
	case want && !a.cmdCHeld:
		if registerKey(copyKey, a.copyTrayImage) == nil {
			a.cmdCHeld = true
		}
	case !want && a.cmdCHeld:
		unregisterKey(copyKey)
		a.cmdCHeld = false
	}
}

// keysAreOurs reports whether the shelf may hold the global Esc and Space:
// never while the Settings, 重新命名 or update window is key, and never
// while the panel itself has the keyboard for clipboard search. Pure;
// unit-tested.
func (a *shelf) keysAreOurs() bool {
	if a.settingsWin != nil {
		return false
	}
	if a.rn != nil && a.rn.win != nil {
		return false
	}
	if a.updateWin != nil {
		return false
	}
	return !a.focusMode
}

func (a *shelf) releaseKeys() {
	if a.escHeld {
		unregisterKey("Escape")
		a.escHeld = false
	}
	if a.spaceHeld {
		unregisterKey("Space")
		a.spaceHeld = false
	}
	if a.cmdCHeld {
		unregisterKey(copyKey)
		a.cmdCHeld = false
	}
}

// escape closes Quick Look first, like Finder, then dismisses the
// screen-recording card or clears the clipboard
// search while it holds text; a sheet backs out to the island and the
// island folds away.
func (a *shelf) escape() {
	if a.apps.on {
		// Esc 先退管理浮层和「更多」, 再收两翼——逐层退出, 不一次全关。
		switch {
		case a.apps.manage:
			a.apps.manage = false
		case a.apps.more:
			a.apps.more = false
		default:
			a.endAppSwitch()
		}
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	if mac.QuickLookVisible() {
		mac.CloseQuickLook()
		return
	}
	if a.ocrSheet {
		a.hide(hideExplicit)
		return
	}
	if a.tab == pageClipboard && a.search != "" {
		a.search = ""
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	if a.tab == pageTools {
		if p := a.toolsPage(); p != nil && (p.editing || p.picking) {
			// Esc in the 「+」 catalogue only backs out to the edit grid —
			// same as its 完成; outside it, Esc ends editing. Neither
			// hides the panel.
			if p.picking {
				p.picking = false
			} else {
				p.closeDragPreview()
				p.editing, p.press = false, nil
			}
			if a.win != nil {
				a.win.Invalidate()
			}
			return
		}
	}
	// The guide's Esc is its 跳过: the same fold to the notch, never a step
	// back to a search bar it does not have.
	if a.currentLevel() == 2 && a.currentPage().id() == pageOnboarding {
		a.onboardingSkip()
		return
	}
	// The report opens from Home, so its Esc step is back to Home rather than
	// out to the search bar. Every other page backs out to the search bar.
	if a.currentLevel() == 2 && a.currentPage().id() == pageWeekly {
		a.showPage(pageHome)
		return
	}
	// Any page backs out to the search bar first, which takes the
	// keyboard; a query there is cleared before the next Esc closes.
	if a.currentLevel() == 2 {
		a.goHome()
		return
	}
	if a.homeQuery != "" {
		a.resetHomeSearch()
		return
	}
	a.hide(hideExplicit)
}

// leaveFocusMode gives up the keyboard the search took: the window drops
// back to the shell's level, the field stops being focused and the panel
// holds Esc/Space again like any other page. The keyboard goes back to the
// app the user came from at the same moment — the panel stays open, so a
// later close has nothing left to hand back and must not lose the
// remembered app on the way out.
func (a *shelf) leaveFocusMode() {
	if !a.focusMode {
		return
	}
	a.focusMode = false
	prev := a.prevAppPID
	a.prevAppPID = 0
	a.syncWindowFrame()
	if a.win != nil {
		// Headless surfaces (tests) have no app to register keys with.
		a.holdKeys()
		a.win.Invalidate()
	}
	// The window level follows the mode: without the keyboard the shell
	// goes back above the menu bar, so its top band takes presses again.
	a.syncMousePassThrough()
	// The panel stays open, so the hand-back must not hide this app: the
	// close path's fallback (resignSelf) would take the notch window with
	// it when the remembered app is gone. Only the close may resign.
	a.handBackApp(prev)
}

// hide collapses the shelf. exit decides whether the remembered app gets
// the keyboard back (see shouldRestoreApp); the remembered pid is always
// cleared so a later explicit hide cannot reactivate it a second time.
// The view state (page, search, keyboard mode) survives until the 120 ms
// content fade ends, so the closing panel fades the page it was showing
// instead of flashing the default one; finishClose retires it.
func (a *shelf) hide(exit hideExit) {
	a.endAppSwitch() // 收起同样结束应用切换(保险路径; 通常已经不在)
	a.apps.entryRect = mygo.Rectangle{}
	if !a.visible && !a.peeking {
		return
	}
	// A band drag and a side change belong to a panel that stays open: a
	// hide (Esc, a click elsewhere, the clipboard search taking over)
	// abandons both, so the release cannot move a folded panel.
	if a.islandPress.active {
		a.islandPress = islandPress{}
		a.islandDrag = false
		a.syncDragGuard()
	}
	a.finishPanelFlip()
	a.bandBox, a.bandHits = mygo.Rectangle{}, nil
	a.handoffStatus(time.Now())
	// Any hide — manual or the flash's own timer — ends service ownership
	// so a queued timer can never reclaim a panel the user reopened.
	a.takeServiceOwnership()
	restore := a.shouldRestoreApp(exit)
	prev := a.prevAppPID
	a.visible, a.edgeOpened, a.peeking = false, false, false
	// Any hide ends the arrangement panel's claim on the shell; its box
	// stays drawn (snap.drawn) until the fold completes.
	a.snap.panel = false
	a.snap.wasOpen, a.snap.prevLevel = false, 0
	a.closeAt = time.Now()
	// The fade starts from what the content shows now: a close issued in
	// the middle of an opening must not flash the page to full opacity
	// before it fades.
	a.closeFrom = a.fx.revealK
	// 收起也复位层级进度：二级收起后 fx.levelK 若停在 1，下一次打开一级
	// 的第一帧仍会按二级壳开窗（451pt），在菜单栏图标上方压一层透明窗。
	// 绘制通道由 jumpLevel 在下一帧直接落位，这里同步帧几何读到的值。
	a.fx.levelK = 0
	a.jumpLevel = true
	if a.usage != nil { // 隐藏即取消 AI 用量在途取数和扫描
		a.usage.stopWork()
	}
	if a.weekly != nil { // 隐藏即取消简报的日志扫描；应用采样继续
		a.weekly.leavingPage()
	}
	a.animationFrom, a.animationStart = a.progress, time.Now()
	a.peekFrom, a.peekAnimStart = a.peekProgress, time.Now()
	a.prevAppPID = 0
	// 收起即作废在途粘贴（paste.go）：代号变了，等待焦点的尝试不再按键。
	a.cancelPaste()
	// The keyboard went back with the close: a reopen before the fade ends
	// must open the search bar, not the clipboard search it was left on.
	a.focusMode = false
	// A closed bar keeps no query in flight; the drawn hits survive the
	// content fade and finishClose retires them.
	a.cancelHomeSpot()
	a.releaseKeys()
	// The note window outlives the panel; what it holds is landed now.
	a.noteFlush()
	// 当前拖拽不再次触发；松开鼠标或出现新会话后解除。
	a.suppressed = true
	a.hidePending = false
	a.dropHot = false
	// The fold runs inside the envelope the open panel had: take it before
	// the first closing frame, so no tick resizes the surface under the
	// shell coming down.
	a.beginSurfaceHold()
	a.syncMousePassThrough()
	// The guide's arrow keys leave with the panel they belong to.
	a.syncOnboardingKeys()
	// A suppressed drag is not pushed: the corridor parks with it.
	a.syncDragGuard()
	if restore {
		a.activatePreviousApp(prev)
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// finishClose retires the view state after the content fade: the next
// opening starts on the island's default face with no search or edit.
func (a *shelf) finishClose() {
	a.closeAt = time.Time{}
	a.closeFrom, a.contentFrom = 0, 0
	a.focusMode, a.tab, a.tabFrom, a.search, a.reveal = false, pageTray, pageTray, "", ""
	a.resetHomeSearch()
	a.tabAt = time.Time{}
	a.tabOutK, a.tabOutShift = 1, 0
	a.warming = ""
	// The window was closed: the GPU may have trimmed the atlas and the
	// thumbnails, so the next opening warms the pages again.
	a.warmed = nil
	a.level = 1
	a.tab = pageTray
	// 收起即放弃权限说明卡：面板再开时它要么还在等授权（重新触发再出），
	// 要么授权已完成（预检通过直接框选），都不该回到一张旧卡。
	a.ocrSheet = false
	a.ocrSheetShot = false
	a.resetToolsEdit()         // 工具页的编辑与目录不跨收起保留
	a.music.out.loaded = false // the output device may have changed while closed
	a.selection.clear()
}

// animate updates the shell drawing; idle/peek retain one native surface.
func (a *shelf) animate(now time.Time) {
	finished := false
	if !a.animationStart.IsZero() {
		k := min(float32(now.Sub(a.animationStart))/float32(panelAnimation), 1)
		target := float32(0)
		if a.visible {
			target = 1
		}
		a.progress = a.animationFrom + (target-a.animationFrom)*ui.EaseOut(k)
		if k >= 1 {
			a.animationStart = time.Time{}
			finished = true
		}
	}
	if !a.peekAnimStart.IsZero() {
		k := min(float32(now.Sub(a.peekAnimStart))/float32(peekAnimation), 1)
		target := float32(0)
		if a.peeking || a.visible {
			target = 1
		}
		a.peekProgress = a.peekFrom + (target-a.peekFrom)*ui.EaseOut(k)
		if k >= 1 {
			a.peekAnimStart = time.Time{}
			finished = true
		}
	}
	if a.win == nil {
		return
	}
	// 动画途中由 zone.AnimateTo 以显示器帧率自驱动重绘；一个被 AppKit 自身
	// display 周期吞掉的 tick 会暂停 display link，所以每个 tick 在有东西
	// 在动时再要一帧（下一次帧前的重复请求只合并成一帧）。结束时补一次
	// 穿透状态同步。
	if finished {
		a.syncMousePassThrough()
	}
	if !a.animationStart.IsZero() || !a.peekAnimStart.IsZero() || a.fx.openMoving || a.fx.sizeMoving || finished {
		a.win.Invalidate()
	}
}

// anchorCenterX is the shell's default horizontal axis on the current
// display: the hardware notch's centre, or the display's centre without one.
// a.x is the fallback for a shelf with no measured geometry (a headless
// test shelf that only knows its own window).
func (a *shelf) anchorCenterX() int {
	switch {
	case a.notchW > 0:
		return a.notchX + a.notchW/2
	case a.dispBounds.Width > 0:
		return a.dispBounds.X + a.dispBounds.Width/2
	}
	return a.x + sheetW/2
}

// stripCenter is shared by the drop surface and its protected approach
// region: the default axis, moved by wherever the user dragged the island.
func (a *shelf) stripCenter() int {
	return a.anchorCenterX() + a.islandDX
}

// panelDropRect is the open panel's box as the drop and release checks read
// it: the sheet's width on the strip's axis, hanging from the window's top
// edge. A dragged island moves the panel, so these checks must not read the
// window's default left edge.
func (a *shelf) panelDropRect() mygo.Rectangle {
	p := a.panelPlacement()
	return mygo.Rectangle{X: p.X, Y: p.Y, Width: p.W, Height: p.H}
}
