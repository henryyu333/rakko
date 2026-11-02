//go:build darwin

package main

// 桌宠的行为：几何（大小、夹屏、吸附、默认位置、落地）、姿态优先级与回退、4pt
// 拖动阈值、松手后的落位与记忆、收进刘海、换显示器、改大小，以及"静止时不刷
// 新"。所有测试都不创建原生窗口：窗口相关的调用都是 nil-safe 的。

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/internal/store"
)

// petShelf is a headless instance with the artwork in place: the resting pose
// is on disk, so petStart builds a state machine. Opening the window is
// stubbed out — these tests own the state, not a window server.
func petShelf(t *testing.T) *shelf {
	t.Helper()
	oldOpen := petOpenWindow
	petOpenWindow = func(*shelf) {}
	t.Cleanup(func() { petOpenWindow = oldOpen })
	oldCursor := cursorPoint
	cursorPoint = func() mygo.Point { return mygo.Point{X: 900, Y: 700} }
	t.Cleanup(func() { cursorPoint = oldCursor })
	oldDir := otterDir
	t.Cleanup(func() { otterDir = oldDir; resetOtters() })
	dir := t.TempDir()
	otterDir = func() string { return dir }
	for _, stem := range []string{"pet_catch_wait", "pet_catch_ready", "pet_caught", "pet_lifted", "pet_raised_paw"} {
		if err := os.WriteFile(filepath.Join(dir, "rakko-otter_"+stem+"_v001.png"), testPNGBytes(t), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resetOtters()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{
		store: s, dataDir: t.TempDir(), settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
	}
	withPinDisplays(t, pinTestDisplays)
	return a
}

// windowlessPet places the otter the way petStart would, without a window.
func windowlessPet(a *shelf, x, y, w, h int) *petWindow {
	pet := &petWindow{owner: a, display: pinTestDisplays[0].ID,
		frame: mygo.Rectangle{X: x, Y: y, Width: w, Height: h}}
	a.pet = pet
	return pet
}

func TestPetBoxMatchesTheArtwork(t *testing.T) {
	oldDir := otterDir
	t.Cleanup(func() { otterDir = oldDir; resetOtters() })
	otterDir = func() string { return "" }
	resetOtters()
	// Without the artwork the aspect is the canvas's own 491 × 640.
	for _, c := range []struct{ h, w int }{{72, 55}, {96, 74}, {128, 98}} {
		if gotW, gotH := petBox(c.h); gotW != c.w || gotH != c.h {
			t.Errorf("petBox(%d) = %dx%d, want %dx%d", c.h, gotW, gotH, c.w, c.h)
		}
	}
	if got := petPictureH(settings.PetSizeSmall); got != petSmallH {
		t.Fatalf("small = %d, want %d", got, petSmallH)
	}
	if got := petPictureH(settings.PetSizeLarge); got != petLargeH {
		t.Fatalf("large = %d, want %d", got, petLargeH)
	}
	if got := petPictureH("nonsense"); got != petMediumH {
		t.Fatalf("unknown size = %d, want medium %d", got, petMediumH)
	}
}

func TestPetClampKeepsItOnScreen(t *testing.T) {
	area := pinTestArea
	for _, c := range []struct {
		name string
		in   mygo.Rectangle
		want mygo.Rectangle
	}{
		{"inside", mygo.Rectangle{X: 300, Y: 400, Width: 74, Height: 96},
			mygo.Rectangle{X: 300, Y: 400, Width: 74, Height: 96}},
		{"left of the screen", mygo.Rectangle{X: -80, Y: 400, Width: 74, Height: 96},
			mygo.Rectangle{X: 0, Y: 400, Width: 74, Height: 96}},
		{"under the menu bar", mygo.Rectangle{X: 300, Y: -40, Width: 74, Height: 96},
			mygo.Rectangle{X: 300, Y: area.Y, Width: 74, Height: 96}},
		{"past the right edge", mygo.Rectangle{X: area.Width - 10, Y: 400, Width: 74, Height: 96},
			mygo.Rectangle{X: area.Width - 74, Y: 400, Width: 74, Height: 96}},
		{"below the Dock", mygo.Rectangle{X: 300, Y: area.Y + area.Height - 10, Width: 74, Height: 96},
			mygo.Rectangle{X: 300, Y: area.Y + area.Height - 96, Width: 74, Height: 96}},
	} {
		if got := petClampFrame(c.in, area); got != c.want {
			t.Errorf("%s: clamp = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestPetSettleSeatsAndFlushes: a release near the work area's floor seats
// the otter on it (the Dock's top, or the screen's own edge), a release
// near a side edge flushes it there, and anywhere else it stays exactly
// where it was let go.
func TestPetSettleSeatsAndFlushes(t *testing.T) {
	area := pinTestArea
	floor := area.Y + area.Height
	const w, h = 74, 96
	cases := []struct {
		name string
		in   mygo.Rectangle
		want mygo.Rectangle
	}{
		{"free", mygo.Rectangle{X: 400, Y: 300, Width: w, Height: h},
			mygo.Rectangle{X: 400, Y: 300, Width: w, Height: h}},
		{"on the floor", mygo.Rectangle{X: 400, Y: floor - h - pinSnapDist, Width: w, Height: h},
			mygo.Rectangle{X: 400, Y: floor - h, Width: w, Height: h}},
		{"just above the snap", mygo.Rectangle{X: 400, Y: floor - h - pinSnapDist - 1, Width: w, Height: h},
			mygo.Rectangle{X: 400, Y: floor - h - pinSnapDist - 1, Width: w, Height: h}},
		{"left edge", mygo.Rectangle{X: 3, Y: 300, Width: w, Height: h},
			mygo.Rectangle{X: 0, Y: 300, Width: w, Height: h}},
		{"right edge", mygo.Rectangle{X: area.Width - w - 5, Y: 300, Width: w, Height: h},
			mygo.Rectangle{X: area.Width - w, Y: 300, Width: w, Height: h}},
		{"corner", mygo.Rectangle{X: area.Width - w - 2, Y: floor - h - 2, Width: w, Height: h},
			mygo.Rectangle{X: area.Width - w, Y: floor - h, Width: w, Height: h}},
	}
	for _, c := range cases {
		if got := petSettleFrame(c.in, area); got != c.want {
			t.Errorf("%s: settle = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestPetDefaultSpotSitsOnTheFloor: a display the otter never stood on puts
// it bottom right, its feet on the work area's floor — the Dock's top edge.
func TestPetDefaultSpotSitsOnTheFloor(t *testing.T) {
	area := pinTestArea
	got := petDefaultSpot(area, 74, 96)
	if got.Y+got.Height != area.Y+area.Height {
		t.Fatalf("the otter's feet are at %d, want the floor %d", got.Y+got.Height, area.Y+area.Height)
	}
	if got.X+got.Width != area.X+area.Width-petSnapDist {
		t.Fatalf("the otter's right edge is at %d, want %d", got.X+got.Width, area.X+area.Width-petSnapDist)
	}
	// The arrival starts under the middle of the display's top edge.
	from := petArrivalFrom(area, 74, 96)
	if from.Y != area.Y || from.X+from.Width/2 != area.X+area.Width/2 {
		t.Fatalf("arrival from %+v, want top centre", from)
	}
}

// TestPetPosePriority walks the precedence: what it caught > being carried >
// something hovering over it > a drag crossing the shelf >
// good news > a long quiet spell > standing about. An error is not a pose:
// the panel says what failed, and there is no full-body embarrassed.
func TestPetPosePriority(t *testing.T) {
	a := petShelf(t)
	now := time.Now()
	pet := windowlessPet(a, 400, 300, 74, 96)
	if got := a.petPoseFor(now, false); got != petIdle {
		t.Fatalf("at rest = %q, want %q", got, petIdle)
	}
	a.lastAct = now.Add(-otterSleepyAfter - time.Minute)
	if got := a.petPoseFor(now, false); got != petSleep {
		t.Fatalf("after a quiet spell = %q, want %q", got, petSleep)
	}
	a.lastAct = now
	a.happyUntil = now.Add(time.Second)
	if got := a.petPoseFor(now, false); got != petHappy {
		t.Fatalf("after good news = %q, want %q", got, petHappy)
	}
	a.dragging = true
	if got := a.petPoseFor(now, false); got != petStartled {
		t.Fatalf("during a drag = %q, want %q", got, petStartled)
	}
	if got := a.petPoseFor(now, true); got != petReady {
		t.Fatalf("with a drag over it = %q, want %q", got, petReady)
	}
	pet.moving = true
	if got := a.petPoseFor(now, true); got != petLifted {
		t.Fatalf("being carried = %q, want %q", got, petLifted)
	}
	pet.moving = false
	pet.caught = now.Add(petCaughtFor)
	if got := a.petPoseFor(now, true); got != petCaught {
		t.Fatalf("having just caught a drop = %q, want %q", got, petCaught)
	}
	// The hold expires on its own and the otter goes back to what it was.
	if got := a.petPoseFor(now.Add(petCaughtFor+time.Millisecond), true); got != petReady {
		t.Fatalf("after the hold = %q, want %q", got, petReady)
	}
	// An error is deliberately not on the ladder.
	pet.caught = time.Time{}
	a.dragging, a.happyUntil = false, time.Time{}
	a.status, a.statusKind = "没转成功", statusErr
	if got := a.petPoseFor(now, false); got != petIdle {
		t.Fatalf("after a failure = %q, want %q", got, petIdle)
	}
}

// TestPetArtLadder: a pose the build does not ship falls back down the
// ladder to one it has — sleep, happy and surprised to standing, standing to
// the waiting pose. A build with nothing draws no otter at all.
func TestPetArtLadder(t *testing.T) {
	oldDir := otterDir
	t.Cleanup(func() { otterDir = oldDir; resetOtters() })
	dir := t.TempDir()
	otterDir = func() string { return dir }
	resetOtters()
	if petArt() != nil {
		t.Fatal("an empty folder answered the resting pose")
	}
	if err := os.WriteFile(filepath.Join(dir, "rakko-otter_pet_catch_wait_v001.png"), testPNGBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	resetOtters()
	wait := petArt()
	if wait == nil {
		t.Fatal("the waiting pose did not decode")
	}
	for _, name := range []string{petIdle, petSleep, petHappy, petStartled, petReady, petCaught, petLifted} {
		if got := otterImage(name); got != wait {
			t.Fatalf("%s did not fall back to the waiting pose", name)
		}
	}
	if otterImage("not-a-pose") != nil {
		t.Fatal("an unknown name answered a picture")
	}
}

// petPNG is a solid picture of the given size: two poses can then be told
// apart by what decoded.
func petPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{R: 30, G: 90, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestPetPosesReadTheirOwnFiles: a pose the build ships is the file it
// decoded, and shipping one new pose is dropping one file in — the loader
// asks for pet_idle, not for the waiting pose that used to stand in for it.
func TestPetPosesReadTheirOwnFiles(t *testing.T) {
	oldDir := otterDir
	t.Cleanup(func() { otterDir = oldDir; resetOtters() })
	dir := t.TempDir()
	otterDir = func() string { return dir }
	write := func(stem string, w, h int) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "rakko-otter_"+stem+"_v001.png"), petPNG(t, w, h), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pet_idle", 6, 6)
	write("pet_catch_wait", 4, 4)
	resetOtters()
	idle := otterImage(petIdle)
	if idle == nil {
		t.Fatal("pet_idle did not decode")
	}
	if w, h := idle.Size(); w != 6 || h != 6 {
		t.Fatalf("the resting pose decoded %dx%d, want its own 6x6 file", w, h)
	}
	// A pose with no file of its own still walks the ladder to it.
	if got := otterImage(petSleep); got != idle {
		t.Fatal("a missing pose did not fall back to the resting one")
	}
	// Only the resting pose on disk is enough for the otter to exist.
	if err := os.Remove(filepath.Join(dir, "rakko-otter_pet_catch_wait_v001.png")); err != nil {
		t.Fatal(err)
	}
	resetOtters()
	if petArt() == nil {
		t.Fatal("a build with only pet_idle has no otter")
	}
}

// TestPetDragThresholdIsFourPoints: a press that never travels 4pt is a tap
// — it opens the notch — and one that does carries the otter.
func TestPetDragThresholdIsFourPoints(t *testing.T) {
	a := petShelf(t)
	pet := windowlessPet(a, 400, 300, 74, 96)

	pet.pressAt(10, 10)
	pet.lockMove(3, 3) // inside the threshold on both axes
	if pet.moving || pet.didMove {
		t.Fatal("a 3pt press already counts as a move")
	}
	if pet.grabX != 10 || pet.grabY != 10 {
		t.Fatalf("the grab point moved to %d,%d", pet.grabX, pet.grabY)
	}
	pet.lockMove(4, 0) // past it
	if !pet.moving || !pet.didMove {
		t.Fatal("a 4pt press did not become a move")
	}
	if pet.grabX != 0 || pet.grabY != 0 {
		t.Fatalf("the grab re-anchored to %d,%d, want the top-left", pet.grabX, pet.grabY)
	}

	// A tap opens the notch, never a move.
	a.pet = &petWindow{owner: a, display: pinTestDisplays[0].ID, frame: mygo.Rectangle{Width: 74, Height: 96}}
	a.visible, a.level = false, 2
	view := ui.NewTester(a.pet.view, 74, 96)
	view.Frame()
	view.ClickAt(30, 40)
	if !a.visible {
		t.Fatal("a tap on the otter did not open the notch")
	}
	if a.currentLevel() != 1 {
		t.Fatalf("a tap landed on level=%d, want the search bar", a.currentLevel())
	}
}

// TestPetReleaseSeatsAndRemembers: a release near the floor seats the otter
// on it and writes the spot for this display; a release elsewhere keeps the
// point and writes that.
func TestPetReleaseSeatsAndRemembers(t *testing.T) {
	a := petShelf(t)
	area := pinTestArea
	floor := area.Y + area.Height
	pet := windowlessPet(a, 400, floor-96-10, 74, 96)
	pet.grabX, pet.grabY = 0, 0
	pet.moving, pet.pressed = true, true
	// The release point is the window's top-left (the grab re-anchored).
	a.petRelease(mygo.Point{X: 400, Y: floor - 96 - 10})
	if pet.frame.Y != floor-96 {
		t.Fatalf("the otter sits at %d, want the floor at %d", pet.frame.Y, floor-96)
	}
	want := settings.IslandPos{X: 400, Y: floor - 96 - area.Y}
	if got := a.settings.PetPos[pinDisplayID(pinTestDisplays[0].ID)]; got != want {
		t.Fatalf("remembered spot = %+v, want %+v", got, want)
	}

	// Nowhere near an edge: it stays exactly there.
	pet.frame = mygo.Rectangle{X: 300, Y: 200, Width: 74, Height: 96}
	pet.moving, pet.pressed = true, true
	a.petRelease(mygo.Point{X: 301, Y: 201})
	if pet.frame.X != 301 || pet.frame.Y != 201 {
		t.Fatalf("free release landed at %+v, want 301,201", pet.frame)
	}
}

// TestPetReleaseToTheNotchStows: carrying the otter up to the notch hides
// it and remembers that it is stowed.
func TestPetReleaseToTheNotchStows(t *testing.T) {
	a := petShelf(t)
	a.hasNotch, a.notchX, a.notchY, a.notchW, a.notchH = true, 600, 0, 180, 32
	pet := windowlessPet(a, 500, 400, 74, 96)
	a.savePetSpot() // the last drag left it at 500,400
	pet.moving, pet.pressed, pet.grabX, pet.grabY = true, true, 0, 0
	a.petRelease(mygo.Point{X: 650, Y: 20})
	if a.pet != nil {
		t.Fatal("the otter was not stowed")
	}
	if a.settings.Pet.Shown {
		t.Fatal("a stowed otter is still marked shown")
	}
	// 放出海獭 brings it back where it was left.
	a.setPetShown(true)
	if a.pet == nil {
		t.Fatal("the otter did not come back")
	}
	if a.pet.frame.X != 500 || a.pet.frame.Y != 400 {
		t.Fatalf("the otter came back at %+v, want its old spot 500,400", a.pet.frame)
	}
}

// TestPetHomeRestoresTheLastDisplay: the otter goes back to the screen it
// was last left on, not to the first display in the list that still holds an
// older spot; the other screens keep their spots for when they return.
func TestPetHomeRestoresTheLastDisplay(t *testing.T) {
	a := petShelf(t)
	second := mygo.Rectangle{X: 1512, Y: 25, Width: 1280, Height: 740}
	two := []mygo.Display{
		{ID: 1, Primary: true, Bounds: mygo.Rectangle{Width: 1512, Height: 982}, WorkArea: pinTestArea},
		{ID: 2, Bounds: mygo.Rectangle{X: 1512, Width: 1280, Height: 800}, WorkArea: second},
	}
	withPinDisplays(t, two)
	// It was first left on the primary display…
	primarySpot := settings.IslandPos{X: 100, Y: 200}
	a.settings.PetPos = map[string]settings.IslandPos{pinDisplayID(1): primarySpot}
	a.settings.PetDisplay = pinDisplayID(1)
	// …then carried to the second screen, where it was let go.
	pet := windowlessPet(a, second.X+300, second.Y+120, 74, 96)
	pet.display = 2
	a.savePetSpot()
	if got := a.settings.PetDisplay; got != pinDisplayID(2) {
		t.Fatalf("last display = %q, want %q", got, pinDisplayID(2))
	}
	id, spot, ok := a.petHome()
	if id != 2 || !ok || spot != (settings.IslandPos{X: 300, Y: 120}) {
		t.Fatalf("home = display %d spot %+v (ok=%v), want the second screen", id, spot, ok)
	}
	// Both spots survive; with the second screen unplugged the otter falls
	// back to the primary display's own spot, not its default one.
	withPinDisplays(t, two[:1])
	id, spot, ok = a.petHome()
	if id != 1 || !ok || spot != primarySpot {
		t.Fatalf("unplugged home = display %d spot %+v (ok=%v), want %+v", id, spot, ok, primarySpot)
	}
}

// TestPetDisplaysChangedRehomes: a display that went away sends the otter to
// the primary display's default spot.
func TestPetDisplaysChangedRehomes(t *testing.T) {
	a := petShelf(t)
	withPinDisplays(t, []mygo.Display{
		{ID: 1, Primary: true, Bounds: mygo.Rectangle{Width: 1512, Height: 982}, WorkArea: pinTestArea},
		{ID: 2, Bounds: mygo.Rectangle{X: 1512, Width: 1280, Height: 800},
			WorkArea: mygo.Rectangle{X: 1512, Y: 25, Width: 1280, Height: 740}},
	})
	pet := windowlessPet(a, 1600, 100, 74, 96)
	pet.display = 2
	withPinDisplays(t, []mygo.Display{
		{ID: 1, Primary: true, Bounds: mygo.Rectangle{Width: 1512, Height: 982}, WorkArea: pinTestArea},
	})
	a.petDisplaysChanged()
	if pet.display != 1 {
		t.Fatalf("the otter stayed on display %d", pet.display)
	}
	want := petDefaultSpot(pinTestArea, 74, 96)
	if pet.frame != want {
		t.Fatalf("the otter landed at %+v, want %+v", pet.frame, want)
	}
}

// TestPetSizeKeepsTheFeet: growing the otter keeps its feet where they were,
// so a bigger one stands where the smaller one did.
func TestPetSizeKeepsTheFeet(t *testing.T) {
	a := petShelf(t)
	pet := windowlessPet(a, 400, 500, 74, 96)
	feet := pet.frame.Y + pet.frame.Height
	centre := pet.frame.X + pet.frame.Width/2

	a.setPetSize(settings.PetSizeLarge)
	if a.settings.Pet.Size != settings.PetSizeLarge {
		t.Fatalf("size = %q, want large", a.settings.Pet.Size)
	}
	if pet.frame.Height != petLargeH {
		t.Fatalf("height = %d, want %d", pet.frame.Height, petLargeH)
	}
	if got := pet.frame.Y + pet.frame.Height; got != feet {
		t.Fatalf("the feet moved from %d to %d", feet, got)
	}
	if got := pet.frame.X + pet.frame.Width/2; got != centre {
		t.Fatalf("the centre moved from %d to %d", centre, got)
	}
}

// TestPetTickRedrawsOnlyWhenThePoseChanges: a standing otter asks for no
// frames — one comparison per tick — and a pose change asks for exactly one.
func TestPetTickRedrawsOnlyWhenThePoseChanges(t *testing.T) {
	a := petShelf(t)
	pet := windowlessPet(a, 400, 300, 74, 96)
	now := time.Now()
	pet.drawn = petIdle // what the last frame drew
	for range 5 {
		a.petTick(now, mygo.Point{X: 0, Y: 0}, false)
	}
	if pet.drawn != petIdle || !pet.slideAt.IsZero() {
		t.Fatalf("a resting otter changed state: drawn=%q slide=%v", pet.drawn, pet.slideAt)
	}
	a.happyUntil = now.Add(time.Second)
	a.petTick(now, mygo.Point{X: 0, Y: 0}, false)
	if pet.drawn != petHappy {
		t.Fatalf("the tick did not pick up the happy moment: %q", pet.drawn)
	}
	// A drag held over it turns it ready even with no window on screen.
	a.dragging = true
	a.petTick(now, mygo.Point{X: 410, Y: 310}, false)
	if pet.drawn != petReady {
		t.Fatalf("a drag over the otter left it %q, want %q", pet.drawn, petReady)
	}
}

// TestPetHideKeepsTheChoice: 收进刘海 writes shown=false, and a build
// without the artwork never shows the otter whatever the file says.
func TestPetHideWithoutArt(t *testing.T) {
	a := petShelf(t)
	oldDir := otterDir
	t.Cleanup(func() { otterDir = oldDir; resetOtters() })
	otterDir = func() string { return "" }
	resetOtters()
	a.settings.Pet.Shown = true
	a.petStart()
	if a.pet != nil {
		t.Fatal("a build without the artwork opened an otter window")
	}
	// Asking to show it is a no-op, not an error.
	a.setPetShown(true)
	if a.pet != nil || !a.settings.Pet.Shown {
		t.Fatal("a build without the artwork changed the setting")
	}
}

// TestPetStowPeekDrawsThePill: carrying the otter to the notch grows the
// same pill a card gets there, saying 松开收进刘海 — the release reads the
// same zone, so the affordance must always be the pill it acts on.
func TestPetStowPeekDrawsThePill(t *testing.T) {
	// The motion window is the pin's own harness for this pill: a closed
	// shell that can grow and fold.
	a := motionWindow()
	a.hasNotch = true
	a.petReturning, a.peeking = true, true
	a.peekProgress, a.peekFrom = 1, 1
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	if !view.HasText("松开收进刘海") {
		t.Fatalf("the stow pill is not up while the otter is held at the notch; texts %v", view.Texts())
	}
	if a.fx.peekKind != peekPetStow {
		t.Fatalf("peek kind = %v, want the stow pill", a.fx.peekKind)
	}
	if w, h := a.peekSize(); w < float32(max(180, a.notchW))+dropPillExtraW || h <= float32(32) {
		t.Fatalf("stow peek is %v×%v, want the drop pill's shape", w, h)
	}
	// The release outside the zone clears it.
	a.petReturning, a.peeking = false, false
}

// TestPetTickStowsAtTheNotch: a carried otter released over the notch sets
// the pill while it is still held there, and clears it the moment the press
// ends.
func TestPetTickStowsAtTheNotch(t *testing.T) {
	a := petShelf(t)
	a.hasNotch, a.notchX, a.notchY, a.notchW, a.notchH = true, 600, 0, 180, 32
	pet := windowlessPet(a, 600, 18, 74, 96)
	pet.pressed, pet.moving, pet.grabX, pet.grabY = true, true, 0, 0
	a.petTick(time.Now(), mygo.Point{X: 640, Y: 20}, true)
	if !a.petReturning {
		t.Fatalf("the pill did not come up over the notch (frame %+v)", pet.frame)
	}
	pet.pressed, pet.moving = false, false
	a.petTick(time.Now(), mygo.Point{X: 640, Y: 500}, false)
	if a.petReturning {
		t.Fatal("the pill stayed after the press ended")
	}
}

// TestPetReceiveCatchesIntoTheTray: files dropped on the otter land in the
// tray through the same path a drop on the notch takes, and the otter holds
// them for a moment. The transfer is built the way a file drag offers it.
func TestPetReceiveCatchesIntoTheTray(t *testing.T) {
	a := petShelf(t)
	windowlessPet(a, 400, 300, 74, 96)
	if len(a.store.Items) != 0 {
		t.Fatalf("the store starts with %d items", len(a.store.Items))
	}
	p := filepath.Join(t.TempDir(), "照片.png")
	if err := os.WriteFile(p, testPNGBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	u := (&url.URL{Scheme: "file", Path: p}).String()
	a.petReceive(transfer.New(transfer.NewItem(transfer.Bytes(transfer.FileList, []byte(u)))))
	if len(a.store.Items) != 1 {
		t.Fatalf("the tray holds %d items after the drop, want 1", len(a.store.Items))
	}
	if a.pet.caught.IsZero() || !time.Now().Before(a.pet.caught) {
		t.Fatal("the otter did not hold what it caught")
	}
	if a.petPoseFor(time.Now(), false) != petCaught {
		t.Fatalf("the caught drop is not the pose: %q", a.petPoseFor(time.Now(), false))
	}
}
