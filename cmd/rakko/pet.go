//go:build darwin

package main

// The desktop otter: the brand character as a small transparent window of
// its own, living wherever the user drops it. It is quiet — it stays put,
// never walks about, and changes only its pose (a different picture, never
// a frame-by-frame animation), and it is awake only when something is
// happening: a drag over it, a drag crossing the shelf, a drop, a moment of
// good news, or a long quiet spell.
//
// The window is the artwork's bounding box: AppKit cannot pass a click
// through a picture only where the picture is transparent (measured in
// internal/mac/overlay_hit.go), so the otter's clickable area is its own
// rectangle — the same trade every desktop pet makes. Only the five
// full-body pet_* pictures are drawn here; the panel's busts never appear,
// because a bust where a whole body stands reads as another character.
//
// It reuses the pinned card's window policy (non-activating floating
// panel, every Space, never key) through mac.ConfigurePetWindow, but not
// its full-screen company: over a film the otter steps aside.

import (
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

const (
	// The picture heights the 小 / 中 / 大 choice asks for.
	petSmallH  = 72
	petMediumH = 96
	petLargeH  = 128
	// petSnapDist is the release distance that seats the otter on the
	// work area's floor (the Dock's top, or the screen's edge when the
	// Dock is hidden) or flushes it to a side edge. Same 24pt as the
	// desktop cards' own dock distance.
	petSnapDist = 24
	// petDragStart is how far a press must travel to count as a move
	// rather than a tap — the floating island's own 4pt threshold.
	petDragStart = 4
	// petCaughtFor is how long the otter holds what it just caught.
	petCaughtFor = 600 * time.Millisecond
	// petSlide is the move to a landed or snapped spot; petArriveFor is
	// the longer fall of its first appearance.
	petSlide     = 240 * time.Millisecond
	petArriveFor = 720 * time.Millisecond
	// petSettleTrust is how long after showing a window its occlusion
	// state is taken at face value: AppKit answers "covered" for a window
	// it has not placed yet, and the arrival must not be cut short by it.
	petSettleTrust = 500 * time.Millisecond
	// otterSleepyAfter is how long without any use makes the otter sleepy,
	// and otterHappyFor how long good news keeps it cheerful.
	otterSleepyAfter = 10 * time.Minute
	otterHappyFor    = 1600 * time.Millisecond
)

// otterHappy makes the otter happy for a moment after something good: a
// drop it caught, a fresh conversion finished, a copy captured.
func (a *shelf) otterHappy(now time.Time) {
	a.happyUntil = now.Add(otterHappyFor)
	if a.pet != nil {
		a.pet.invalidate()
	}
}

// petWindow is the desktop otter: its place, its press session, and the
// move in flight. frame is the logical place — where the otter is, and
// where a release leaves it; a slide only changes where the window is
// drawn on the way there.
type petWindow struct {
	owner   *shelf
	display int64
	frame   mygo.Rectangle
	// drawn is the pose on screen last; the monitor tick repaints when
	// the moment's pose differs, so the window keeps no clock of its own.
	drawn              string
	caught             time.Time // the drop just caught, held until then
	shownAt            time.Time // when the window appeared, for petSettleTrust
	slideFrom, slideTo mygo.Rectangle
	slideAt            time.Time
	slideDur           time.Duration
	slideScale         float32
	// The press: where it landed, and whether it became a move.
	pressed, moving, didMove bool
	grabX, grabY             int
	tapReady                 bool // a press that may still be a tap
	// reduceMotion is the desktop's Reduce Motion, read from the view (the
	// only place with a Context) so the tick can honour it too.
	reduceMotion bool
	win          *mygo.Window
}

// petArea is the work area the otter lives in on a display: the same list
// the desktop cards measure against, so a display that goes away re-homes
// both the same way.
func petArea(display int64) mygo.Rectangle { return pinArea(display) }

// petSizeIndex and petSizeAt map the saved size to the segmented control's
// seats, and back.
func petSizeIndex(size settings.PetSize) int {
	switch size.Normalized() {
	case settings.PetSizeSmall:
		return 0
	case settings.PetSizeLarge:
		return 2
	}
	return 1
}

func petSizeAt(index int) settings.PetSize {
	switch index {
	case 0:
		return settings.PetSizeSmall
	case 2:
		return settings.PetSizeLarge
	}
	return settings.PetSizeMedium
}

// petPictureH is the saved size choice as the picture's height in points.
func petPictureH(size settings.PetSize) int {
	switch size.Normalized() {
	case settings.PetSizeSmall:
		return petSmallH
	case settings.PetSizeLarge:
		return petLargeH
	}
	return petMediumH
}

// petBox is the window's size for a picture height: the artwork's own
// aspect gives the width, so the window is exactly the otter.
func petBox(h int) (w, height int) {
	picW, picH := 491, 640
	if b := petArt(); b != nil {
		if bw, bh := b.Size(); bw > 0 && bh > 0 {
			picW, picH = bw, bh
		}
	}
	return int(math.Round(float64(h) * float64(picW) / float64(picH))), h
}

// petClampFrame holds r inside area: the otter is never parked off screen,
// under the menu bar or behind the Dock.
func petClampFrame(r mygo.Rectangle, area mygo.Rectangle) mygo.Rectangle {
	r.X = min(max(r.X, area.X), max(area.X, area.X+area.Width-r.Width))
	r.Y = min(max(r.Y, area.Y), max(area.Y, area.Y+area.Height-r.Height))
	return r
}

// petSettleFrame is where a released otter comes to rest: inside the work
// area, seated on its floor when it was dropped there (the Dock's top, or
// the screen's own edge), and flush to a side edge when it was dropped
// against one. Everywhere else it stays exactly where it was let go.
func petSettleFrame(r mygo.Rectangle, area mygo.Rectangle) mygo.Rectangle {
	r = petClampFrame(r, area)
	if r.Y+r.Height >= area.Y+area.Height-petSnapDist {
		r.Y = area.Y + area.Height - r.Height
	}
	if r.X <= area.X+petSnapDist {
		r.X = area.X
	} else if r.X+r.Width >= area.X+area.Width-petSnapDist {
		r.X = area.X + area.Width - r.Width
	}
	return r
}

// petDefaultSpot is where a display that never held the otter puts it:
// bottom right, standing on the work area's floor — on the Dock's top edge
// when the Dock is up — a hand's width from the side.
func petDefaultSpot(area mygo.Rectangle, w, h int) mygo.Rectangle {
	return petClampFrame(mygo.Rectangle{
		X:     area.X + area.Width - w - petSnapDist,
		Y:     area.Y + area.Height - h,
		Width: w, Height: h,
	}, area)
}

// petArrivalFrom is where the first appearance starts: under the middle of
// the display's top edge, so the otter is seen coming down from the notch.
func petArrivalFrom(area mygo.Rectangle, w, h int) mygo.Rectangle {
	return mygo.Rectangle{
		X: area.X + (area.Width-w)/2, Y: area.Y,
		Width: w, Height: h,
	}
}

// petEase is the slide's curve: fast away, slow into place.
func petEase(t float32) float32 {
	t = min(max(t, 0), 1)
	return 1 - (1-t)*(1-t)*(1-t)
}

// petPoseFor picks the picture for the moment, most specific first: what
// it just caught > being carried > something hovering over it > a drag
// crossing the shelf > good news > a long quiet spell > standing about. A
// failure is deliberately not a pose: the panel says what went wrong, and
// the half-body embarrassed bust has no full-body twin.
func (a *shelf) petPoseFor(now time.Time, hover bool) string {
	switch {
	case a.pet != nil && !a.pet.caught.IsZero() && now.Before(a.pet.caught):
		return petCaught
	case a.pet != nil && a.pet.moving:
		return petLifted
	case hover:
		return petReady
	case a.dragging && !a.ownDrag:
		return petStartled
	case !a.happyUntil.IsZero() && now.Before(a.happyUntil):
		return petHappy
	case !a.lastAct.IsZero() && now.Sub(a.lastAct) > otterSleepyAfter:
		return petSleep
	}
	return petIdle
}

// petHoverAt reports a drag held over the otter's own rectangle.
func (a *shelf) petHoverAt(p mygo.Point) bool {
	return a.pet != nil && a.dragging && !a.ownDrag && insideRect(p, a.pet.frame)
}

// petHome picks the display the otter belongs on and where it stood there:
// the display it was last left on, else the primary one — at that display's
// remembered spot, or its default one. A single window, so one place is
// current; the other displays keep their spots for when they come back.
func (a *shelf) petHome() (display int64, spot settings.IslandPos, remembered bool) {
	ds := pinDisplays()
	var primary int64
	for _, d := range ds {
		if d.Primary || primary == 0 {
			primary = d.ID
		}
	}
	// The last display wins even though other displays still hold older
	// spots: dragging the otter to a second screen must bring it back there,
	// not to whichever display the list happens to start with.
	if key := a.settings.PetDisplay; key != "" {
		for _, d := range ds {
			if pinDisplayID(d.ID) == key {
				pos, ok := a.petSpot(d.ID)
				return d.ID, pos, ok
			}
		}
	}
	if pos, ok := a.petSpot(primary); ok {
		return primary, pos, true
	}
	return primary, settings.IslandPos{}, false
}

// petStart opens the otter's window on its home display, at its remembered
// spot, or — the first time — at the default one, after falling in from the
// notch. It does nothing without the artwork.
func (a *shelf) petStart() {
	if a.pet != nil {
		return
	}
	if petArt() == nil {
		return
	}
	if !a.settings.Pet.Shown {
		return
	}
	w, h := petBox(petPictureH(a.settings.Pet.Size))
	display, spot, remembered := a.petHome()
	area := petArea(display)
	frame := petDefaultSpot(area, w, h)
	if remembered {
		frame = petClampFrame(mygo.Rectangle{X: area.X + spot.X, Y: area.Y + spot.Y, Width: w, Height: h}, area)
	}
	pet := &petWindow{owner: a, display: display, frame: frame}
	a.pet = pet
	petOpenWindow(a)
	if !remembered {
		// The arrival: seen coming down from the notch, once.
		pet.slideFrom = petArrivalFrom(area, w, h)
		pet.slideTo = frame
		pet.slideAt = time.Now()
		pet.slideDur = petArriveFor
		pet.placeAt(pet.slideFrom)
		a.savePetSpot()
	}
}

// petOpenWindow opens the otter's window; a seam, so the state machine can
// be tested without a window server.
var petOpenWindow func(a *shelf)

func init() { petOpenWindow = (*shelf).openPetWindow }

// openPetWindow creates the otter's native window: transparent, frameless,
// a non-activating floating panel that never takes the keyboard.
func (a *shelf) openPetWindow() {
	pet := a.pet
	if pet == nil {
		return
	}
	f := pet.slideFrom
	if pet.slideAt.IsZero() {
		f = pet.frame
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:  a.appLoc().WindowPet(),
		Width:  f.Width,
		Height: f.Height,
		X:      f.X,
		Y:      f.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(pet.view),
	})
	pet.win = w
	pet.shownAt = time.Now()
	w.SetVisibleOnAllWorkspaces(true)
	mac.ConfigurePetWindow(w.NativeHandle())
	mac.SetWindowFrame(w.NativeHandle(), f.X, f.Y, f.Width, f.Height)
	w.ShowInactive()
}

// closePet takes the window down; the placement stays in settings.json so
// the next appearance is where the user left it.
func (a *shelf) closePet() {
	pet := a.pet
	if pet == nil {
		return
	}
	a.pet = nil
	a.petReturning = false
	if pet.win != nil {
		w := pet.win
		pet.win = nil
		w.Destroy()
	}
}

// hidePet is 收进刘海: the otter leaves the desktop and the choice is
// remembered, so it does not come back on the next launch. The tray menu's
// 放出海獭 and the Settings switch bring it back.
func (a *shelf) hidePet() {
	if a.settings.Pet.Shown {
		next := a.settings
		next.Pet.Shown = false
		a.applySettings(next)
	}
	a.closePet()
	if a.win != nil {
		a.win.Invalidate()
	}
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
}

// setPetShown is 显示海獭 from the Settings window or the tray menu.
func (a *shelf) setPetShown(on bool) {
	if a.settingsBroken {
		return
	}
	if a.settings.Pet.Shown == on {
		return
	}
	if petArt() == nil && on {
		return // no artwork in this build: nothing to show, nothing to say
	}
	next := a.settings
	next.Pet.Shown = on
	if !a.applySettings(next) {
		return
	}
	if on {
		a.petStart()
	} else {
		a.closePet()
	}
}

// setPetSize saves 小 / 中 / 大 and reshapes the window around the otter's
// feet: the bottom centre stays put, so a bigger otter stands where the
// smaller one did.
func (a *shelf) setPetSize(size settings.PetSize) {
	if a.settingsBroken {
		return
	}
	size = size.Normalized()
	if a.settings.Pet.Size == size {
		return
	}
	next := a.settings
	next.Pet.Size = size
	if !a.applySettings(next) {
		return
	}
	pet := a.pet
	if pet == nil {
		return
	}
	w, h := petBox(petPictureH(size))
	pet.frame = mygo.Rectangle{
		X:      pet.frame.X + (pet.frame.Width-w)/2,
		Y:      pet.frame.Y + pet.frame.Height - h,
		Width:  w,
		Height: h,
	}
	pet.frame = petSettleFrame(pet.frame, petArea(pet.display))
	pet.slideAt = time.Time{}
	pet.applyFrame()
	a.savePetSpot()
}

// petSpot is the remembered place for a display, as an offset from that
// display's origin.
func (a *shelf) petSpot(display int64) (settings.IslandPos, bool) {
	pos, ok := a.settings.PetPos[pinDisplayID(display)]
	return pos, ok
}

// savePetSpot writes where the otter is standing, by display. A spot that
// has not moved is not rewritten, so a drag that ended where it began
// costs no file write.
func (a *shelf) savePetSpot() {
	pet := a.pet
	if pet == nil {
		return
	}
	area := petArea(pet.display)
	pos := settings.IslandPos{X: pet.frame.X - area.X, Y: pet.frame.Y - area.Y}
	key := pinDisplayID(pet.display)
	// The display is remembered even when the spot is the same: two screens
	// can share an offset, and the last one is what the otter returns to.
	if a.settings.PetPos[key] == pos && a.settings.PetDisplay == key {
		return
	}
	next := a.settings
	next.PetPos = make(map[string]settings.IslandPos, len(a.settings.PetPos)+1)
	for k, v := range a.settings.PetPos {
		next.PetPos[k] = v
	}
	next.PetPos[key] = pos
	next.PetDisplay = key
	a.applySettings(next)
}

// petDisplaysChanged re-homes an otter whose display went away to the
// primary display's default spot, the rule the desktop cards keep.
func (a *shelf) petDisplaysChanged() {
	pet := a.pet
	if pet == nil {
		return
	}
	for _, d := range pinDisplays() {
		if d.ID == pet.display {
			return
		}
	}
	var primary int64
	for _, d := range pinDisplays() {
		if d.Primary || primary == 0 {
			primary = d.ID
		}
	}
	pet.display = primary
	pet.frame = petDefaultSpot(petArea(primary), pet.frame.Width, pet.frame.Height)
	pet.slideAt = time.Time{}
	pet.applyFrame()
	a.savePetSpot()
}

// petTick drives the otter from the monitor: the press that follows the
// pointer, the 收进刘海 pill, a slide in flight, and the pose repaint. It
// does no work at all while the otter stands still — a sleeping pet costs
// nothing but the comparisons below.
func (a *shelf) petTick(now time.Time, p mygo.Point, down bool) {
	pet := a.pet
	if pet == nil {
		a.petReturning = false
		return
	}
	returning := false
	if pet.pressed {
		if down && pet.moving {
			pet.display = displayIDAt(p, pet.display)
			pet.frame = petClampFrame(mygo.Rectangle{
				X: p.X - pet.grabX, Y: p.Y - pet.grabY,
				Width: pet.frame.Width, Height: pet.frame.Height,
			}, petArea(pet.display))
			pet.slideAt = time.Time{}
			pet.applyFrame()
		}
		centre := mygo.Point{X: pet.frame.X + pet.frame.Width/2, Y: pet.frame.Y + pet.frame.Height/2}
		returning = pet.moving && a.pinReturnZone(centre)
	}
	// A pet held over the notch grows 松开收进刘海 exactly where a card
	// held there grows 松开放回: the release reads the same zone.
	if a.petReturning != returning {
		a.petReturning = returning
		if returning && !a.visible {
			a.setPeek(true)
		}
	}
	a.petAdvance(now)
	// One comparison per tick decides whether anything is redrawn: a
	// standing otter asks for no frames at all.
	if pose := a.petPoseFor(now, a.petHoverAt(p)); pose != pet.drawn {
		pet.drawn = pose
		pet.invalidate()
	}
}

// petAdvance steps a move in flight. With no move there is nothing to do
// and no frame is asked for. While the screen is dark, the window is
// covered, or the user asked for less motion, the move lands at once.
func (a *shelf) petAdvance(now time.Time) {
	pet := a.pet
	if pet == nil || pet.slideAt.IsZero() {
		return
	}
	if !a.petMoves(now) {
		pet.slideAt = time.Time{}
		pet.slideScale = 0
		pet.applyFrame()
		pet.invalidate()
		return
	}
	k := float32(now.Sub(pet.slideAt)) / float32(pet.slideDur)
	if k >= 1 {
		pet.slideAt = time.Time{}
		pet.slideScale = 0
		pet.applyFrame()
		pet.invalidate()
		return
	}
	e := petEase(k)
	pet.placeAt(petLerp(pet.slideFrom, pet.slideTo, e))
	pet.slideScale = 0.98 + 0.02*e // the landing settles up into full size
	pet.invalidate()
}

// petMoves reports whether the otter may animate at all right now: the
// user has not asked for less motion, the display is awake, and the window
// is not covered by something else. Every false simply lands the otter at
// once — the shape never carries information, so nothing is lost.
func (a *shelf) petMoves(now time.Time) bool {
	pet := a.pet
	if pet == nil || pet.reduceMotion {
		return false
	}
	return !mac.DisplayAsleep(uint32(pet.display)) && !a.petCovered(now)
}

// petCovered reports the window hidden behind something else — another
// app's full-screen window, the lock screen, a Space the user is not on.
// The occlusion answer is trusted only after the window has had a moment
// to be placed; before that AppKit reports every new window as covered.
func (a *shelf) petCovered(now time.Time) bool {
	pet := a.pet
	if pet == nil || pet.win == nil {
		return false
	}
	if !pet.shownAt.IsZero() && now.Sub(pet.shownAt) < petSettleTrust {
		return false
	}
	return mac.WindowOccluded(pet.win.NativeHandle())
}

// petLerp is one point on the slide.
func petLerp(from, to mygo.Rectangle, t float32) mygo.Rectangle {
	at := func(a, b int) int { return a + int(math.Round(float64(float32(b-a)*t))) }
	return mygo.Rectangle{X: at(from.X, to.X), Y: at(from.Y, to.Y), Width: to.Width, Height: to.Height}
}

// slideAcross starts a move from where the otter stands now to where it
// belongs, and takes it there over dur.
func (pet *petWindow) slideAcross(to mygo.Rectangle, dur time.Duration) {
	pet.slideFrom = pet.frame
	pet.slideTo = to
	pet.frame = to
	pet.slideAt = time.Now()
	pet.slideDur = dur
}

// placeAt puts the native window at r without touching the logical place.
func (pet *petWindow) placeAt(r mygo.Rectangle) {
	if pet.win != nil {
		mac.SetWindowFrame(pet.win.NativeHandle(), r.X, r.Y, r.Width, r.Height)
	}
}

// applyFrame puts the native window at the logical place, ending any slide.
func (pet *petWindow) applyFrame() {
	pet.placeAt(pet.frame)
}

func (pet *petWindow) invalidate() {
	if pet.win != nil {
		pet.win.Invalidate()
	}
}

// pressAt arms a press: the grab point is where it landed, and the
// previous gesture can neither poison this tap nor be seen by it.
func (pet *petWindow) pressAt(px, py float64) {
	pet.grabX, pet.grabY = int(px+0.5), int(py+0.5)
	pet.pressed, pet.moving, pet.didMove, pet.tapReady = true, false, false, true
	if pet.owner != nil {
		pet.owner.lastAct = time.Now() // taking hold of the otter is activity
	}
	// A press mid-slide stops the move where the otter is going: the
	// window's drawn place and its logical place must agree before the
	// press measures anything.
	pet.slideAt = time.Time{}
	pet.applyFrame()
}

// lockMove turns the press into a window move once it travels past
// petDragStart, and re-anchors the grab to the top-left so the press does
// not jump.
func (pet *petWindow) lockMove(dx, dy int) {
	if pet.moving || (abs(dx) < petDragStart && abs(dy) < petDragStart) {
		return
	}
	pet.moving, pet.didMove, pet.tapReady = true, true, false
	pet.grabX, pet.grabY = 0, 0
}

// petRelease settles a moved otter: 收进刘海 when it was carried to the
// notch, otherwise it lands where it let go — seated on the floor, flush
// to a side, or exactly there — sliding the last few points into place.
func (a *shelf) petRelease(p mygo.Point) {
	pet := a.pet
	if pet == nil {
		return
	}
	pet.display = displayIDAt(p, pet.display)
	area := petArea(pet.display)
	frame := mygo.Rectangle{
		X: p.X - pet.grabX, Y: p.Y - pet.grabY,
		Width: pet.frame.Width, Height: pet.frame.Height,
	}
	centre := mygo.Point{X: frame.X + frame.Width/2, Y: frame.Y + frame.Height/2}
	if a.pinReturnZone(centre) {
		a.hidePet()
		return
	}
	landed := petSettleFrame(frame, area)
	if landed.X == frame.X && landed.Y == frame.Y {
		pet.frame = landed
		pet.slideAt = time.Time{}
		pet.applyFrame()
	} else {
		pet.frame = frame
		pet.slideAcross(landed, petSlide)
	}
	a.savePetSpot()
}

// petReceive is a drop on the otter itself: the files land in the tray
// through the same path the notch uses, and the otter holds them for a
// moment. A drop from our own tray is not an incoming drag — the item is
// already here — so it neither re-adds it at the front nor plays the catch.
func (a *shelf) petReceive(data transfer.Data) {
	pet := a.pet
	if pet == nil || a.ownDrag {
		return
	}
	if err := a.receive(data); err != nil {
		pet.invalidate()
		return
	}
	pet.caught = time.Now().Add(petCaughtFor)
	a.lastAct = time.Now() // catching something is an activity
	pet.invalidate()
}

// view draws the otter: the picture of the moment, scaled a hair while a
// move settles. The element is the whole window, so the drop target and
// the click target are the picture's own rectangle.
func (pet *petWindow) view(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	a := pet.owner
	pet.reduceMotion = c.Preferences().ReduceMotion
	zone := ui.Column(c.Key("pet")).Fill().Label(a.appLoc().PetLabel())
	opts := transfer.DropOptions{
		Formats:    []transfer.Format{transfer.FileList, transfer.URIList, transfer.PNG, transfer.Text},
		Operations: transfer.Copy,
	}
	// A drag held over the otter is the view's own reading of the pointer:
	// asking the screen module here would need the main loop, which a
	// headless render does not run.
	_, over := ui.DataDragOver(zone, opts)
	if a.ownDrag {
		over = false // our own drag-out is not something to catch
	}
	pose := a.petPoseFor(c.Now(), over)
	pet.drawn = pose
	b := otterImage(pose)
	if b != nil {
		zone.Children(func() {
			art := ui.Image(c.Key("pet-art"), b).
				Size(float32(pet.frame.Width), float32(pet.frame.Height)).Fit(ui.Contain)
			// The settle: a hair under full size while a move lands, back
			// to full on the last frame. Reduce Motion swaps pictures only.
			if s := pet.slideScale; s > 0 && s < 1 && !pet.reduceMotion {
				art.Scale(s, s)
			}
		})
	}
	pet.trackPress(zone, a)
	pet.menu(zone)
	if drop, ok := ui.DropData(zone, opts); ok {
		a.petReceive(drop.Data)
	}
}

// trackPress wires the otter's pointer session: a primary Down arms a
// fresh press, a Move past petDragStart carries the window, and the Up ends
// it — a tap opens the notch, a move settles the otter down.
// The secondary button is left to the context menu.
func (pet *petWindow) trackPress(e ui.Element, a *shelf) {
	e.PointerPosition()
	e.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			if ev.Button != 0 {
				return false // the context menu takes the secondary press
			}
			pet.pressAt(float64(ev.X), float64(ev.Y))
			return true
		case ui.InputPointerMove:
			if !pet.pressed {
				return false
			}
			pet.lockMove(int(ev.X)-pet.grabX, int(ev.Y)-pet.grabY)
			return true
		case ui.InputPointerUp:
			if !pet.pressed || ev.Button != 0 {
				return false
			}
			// A fast drag's release still locks as a move before it ends.
			pet.lockMove(int(ev.X)-pet.grabX, int(ev.Y)-pet.grabY)
			tap := pet.tapReady
			pet.pressed, pet.moving, pet.tapReady = false, false, false
			if tap {
				a.show(false)
				return true
			}
			if pet.didMove {
				// The window has not moved since the last poll, so this
				// point is the release in screen coordinates.
				a.petRelease(mygo.Point{X: pet.frame.X + int(ev.X), Y: pet.frame.Y + int(ev.Y)})
			}
			return true
		}
		return false
	})
}

// menu is the otter's right-click menu: 打开 Rakko, 收进刘海, 大小 ▸, 设置…
func (pet *petWindow) menu(e ui.Element) {
	a := pet.owner
	l := a.appLoc()
	e.ContextMenu(func(m *ui.Menu) {
		if m.Item(l.PetOpen()).Chosen() {
			a.show(false)
		}
		if m.Item(l.PetStow()).Chosen() {
			a.hidePet()
		}
		m.Separator()
		m.Submenu(l.PetSizeRow(), func(sub *ui.Menu) {
			for _, choice := range []struct {
				size  settings.PetSize
				label string
			}{
				{settings.PetSizeSmall, l.PetSizeSmall()},
				{settings.PetSizeMedium, l.PetSizeMedium()},
				{settings.PetSizeLarge, l.PetSizeLarge()},
			} {
				size := choice.size
				if sub.Item(choice.label).Checked(a.settings.Pet.Size.Normalized() == size).Chosen() {
					a.setPetSize(size)
				}
			}
		})
		if m.Item(l.TraySettings()).Chosen() {
			a.openSettings()
		}
	})
}
