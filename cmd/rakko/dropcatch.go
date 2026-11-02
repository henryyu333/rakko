//go:build darwin

package main

// The otter in the drop zone. When a file drag opens the 放入托盘 row, the
// otter under the row's bottom edge pokes its head out; while the files
// hover the row it lifts its paws; when they land it hugs the file — the
// file shrinking into its arms — and after a moment it sinks back under
// the edge. The row clips it, so only the part above the edge shows, the
// way design/explore/otter-catch.html (A) draws it.
//
// The poses are the desktop otter's own — pet_catch_wait, pet_catch_ready,
// pet_caught — so the panel and the desk pet show one character. A build
// without the artwork draws the row exactly as before: no otter, no slot
// kept for it, and no clip.

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	// The panel's otter is 60pt wide; the height follows the artwork's
	// aspect (60 × 640/491 ≈ 78).
	dropOtterW = 60
	// The row's bottom edge cuts the picture. These are how much of it
	// hangs below that edge to peek (head to the eyes), to stand up (the
	// raised paws), and to be out of sight — the design's own numbers.
	dropOtterPeek = -46
	dropOtterUp   = -20
	// dropOtterMargin is the gap between the picture and the row's right
	// edge, its own padding, and dropOtterSlot the room the row keeps
	// clear for it: the picture, the margin, and a gap from the text.
	dropOtterMargin = 16
	dropOtterSlot   = dropOtterMargin + dropOtterW + 8
	// The catch, in the design's timing: the file shrinks into the arms,
	// the otter holds it, then it sinks under the edge and the zone
	// collapses as usual.
	dropCatchFly     = 300 * time.Millisecond
	dropCatchHold    = 600 * time.Millisecond
	dropCatchRetract = 350 * time.Millisecond
	dropCatchFor     = dropCatchFly + dropCatchHold + dropCatchRetract
	// dropCatchIcon is the file in flight: the tray stack's 26pt square.
	dropCatchIcon = 26
)

// dropOtterBase is the picture the zone falls back to: the desk pet's own
// ladder decides what a build ships, so a build with only pet_idle (or
// only pet_catch_wait) still draws the otter. nil when there is no art.
func dropOtterBase() *ui.Bitmap {
	if b := otterImage(petIdle); b != nil {
		return b
	}
	return otterImage(petWait)
}

// dropOtterBox is the picture's size in the row.
func dropOtterBox() (w, h float32) {
	w = dropOtterW
	h = w * 640 / 491 // the artwork's own aspect, before it decodes
	if b := dropOtterBase(); b != nil {
		if bw, bh := b.Size(); bw > 0 && bh > 0 {
			h = w * float32(bh) / float32(bw)
		}
	}
	return w, h
}

// dropRowBox is the tray row's inner box, the one its Absolute children
// place against: the content width less the 1pt border.
func dropRowBox() (pw, ph float32) { return float32(contentW) - 2, dropTrayH - 2 }

// dropOtterPose is the picture for the moment, most specific first: the
// file it just caught > something hovering the row > waiting under it.
func (a *shelf) dropOtterPose(hot bool) string {
	switch {
	case !a.drop.catchAt.IsZero():
		return petCaught
	case hot:
		return petReady
	}
	return petWait
}

// dropOtterAt is where the picture's bottom sits now: under the edge
// (peek), standing up, or halfway through the catch's retract.
func (a *shelf) dropOtterAt(now time.Time, hot bool) float32 {
	_, h := dropOtterBox()
	switch {
	case !a.drop.catchAt.IsZero():
		if now.Sub(a.drop.catchAt) >= dropCatchFly+dropCatchHold {
			return -h - 2 // sunk back under the edge: the zone goes with it
		}
		return float32(dropOtterUp)
	case hot:
		return float32(dropOtterUp)
	}
	return float32(dropOtterPeek)
}

// dropOtter draws the otter as an Absolute child of the tray row: the
// picture pinned to the row's bottom edge, its rise animated with the
// design's spring. The first frame of a zone seeds it under the edge, so
// it pops up instead of being there. A headless render and Reduce Motion
// show it at its place at once: only the picture changes.
func (a *shelf) dropOtter(c *ui.Context, hot bool) {
	w, h := dropOtterBox()
	pose := a.dropOtterPose(hot)
	b := otterImage(pose)
	if b == nil {
		b = dropOtterBase()
	}
	if b == nil {
		return
	}
	art := ui.Box(c.Key("drop-otter")).Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).
		Right(dropOtterMargin).Width(w).Height(h)
	target := a.dropOtterAt(c.Now(), hot)
	if a.win == nil || c.Preferences().ReduceMotion {
		art.Bottom(target)
	} else {
		art.Bottom(art.AnimateTo("rise", target, ui.Animation{
			Spring: ui.SpringOptions{Duration: 320 * time.Millisecond, Bounce: 0.3},
		}))
		if !a.drop.riseSeeded {
			// The value starts at its first target: seed it below the
			// edge on the zone's first frame, and come up from there.
			art.Bottom(art.Jump("rise", -h-2))
			a.drop.riseSeeded = true
			c.AnimationFrame()
		}
	}
	art.Children(func() {
		ui.Image(c.Key("art"), b).Size(w, h).Fit(ui.Contain)
	})
}

// startDropCatch begins the catch a release on the tray row earns: the
// otter keeps the file a moment, then sinks back. The release point comes
// from the pointer the drag left on the row, so the file is seen falling
// from where it was let go; a drop that answered no pointer point (a test,
// a promised-file source) starts it just left of the otter instead. Nothing
// starts without the artwork.
func (a *shelf) startDropCatch(row ui.Element) {
	if dropOtterBase() == nil {
		return
	}
	x, y, _ := row.PointerPosition()
	a.catchDrop(x, y)
}

// catchDrop begins the catch at (x, y) in the row's own box.
func (a *shelf) catchDrop(x, y float32) {
	d := &a.drop
	d.catchAt = time.Now()
	d.flySeeded = false
	pw, _ := dropRowBox()
	if x < 8 || y < 4 || x > pw-8 || y > dropTrayH-4 {
		d.catchX, d.catchY = -1, -1 // no usable point: just left of the otter
		return
	}
	d.catchX, d.catchY = int(x), int(y)
}

// dropCatchFile draws the file the otter just caught: the drag's own
// thumbnail, left where the release happened, shrinking into the otter's
// arms as it fades. It lives only for the flight; Reduce Motion skips it,
// and so does a headless render.
func (a *shelf) dropCatchFile(c *ui.Context, paths []string) {
	d := &a.drop
	if d.catchAt.IsZero() || a.win == nil || c.Preferences().ReduceMotion {
		return
	}
	elapsed := c.Now().Sub(d.catchAt)
	if elapsed >= dropCatchFly {
		return
	}
	pw, ph := dropRowBox()
	chestX := pw - dropOtterMargin - dropOtterW/2
	chestY := ph - 18 // the arms' own line, a hand's width above the edge
	fromX, fromY := float32(pw-dropOtterSlot), ph/2
	if d.catchX >= 0 {
		fromX, fromY = float32(d.catchX), float32(d.catchY)
	}
	cell := ui.Box(c.Key("drop-fly")).Attach(ui.AnchorTopLeft, ui.AnchorTopLeft)
	k := cell.AnimateTo("fly", 1, ui.Animation{Duration: dropCatchFly})
	if !d.flySeeded {
		k = cell.Jump("fly", 0)
		d.flySeeded = true
		c.AnimationFrame()
	}
	size := dropCatchIcon * (1 - 0.5*k)
	x := fromX + (chestX-fromX)*k
	y := fromY + (chestY-fromY)*k
	cell.Left(x-size/2).Top(y-size/2).Size(size, size).Radius(6).Clip().
		Opacity(1 - max(0, (k-0.6)/0.4))
	cell.Children(func() {
		var bmp *ui.Bitmap
		// The stack's own cache: the whole set, never a subset, so the
		// flight neither prunes the other thumbnails nor restarts them.
		if bmps := a.dropThumbs(paths); len(bmps) > 0 {
			bmp = bmps[0]
		}
		if bmp != nil {
			ui.Image(c.Key("bmp"), bmp).Size(size, size).Fit(ui.Cover)
			return
		}
		ui.Icon(c, fileSymbol).Size(size*0.6, size*0.6).TextColor(ink2)
	})
}

// dropCatchAsk schedules the frame each phase of the catch needs: the
// flight's end, the hold's end (the retract starts), and the zone's close.
// Frames in between come from the animations themselves; once the zone is
// gone nothing asks for another.
func dropCatchAsk(c *ui.Context, elapsed time.Duration) {
	next := dropCatchFor
	switch {
	case elapsed < dropCatchFly:
		next = dropCatchFly
	case elapsed < dropCatchFly+dropCatchHold:
		next = dropCatchFly + dropCatchHold
	}
	if wait := next - elapsed; wait > 0 {
		c.After(wait + time.Millisecond)
	}
}
