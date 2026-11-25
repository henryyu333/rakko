//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
	"rakko/internal/platform"
	"rakko/internal/settings"
)

func seg4ShotDir(t *testing.T) string {
	t.Helper()
	root := os.Getenv("RAKKO_SHOTS")
	if root == "" {
		return ""
	}
	return filepath.Join(root, "seg4")
}

func seg4CaptureWindow(t *testing.T, win *mygo.Window, name string) {
	t.Helper()
	if win == nil {
		t.Fatalf("%s: nil window", name)
	}
	dir := seg4ShotDir(t)
	if dir == "" {
		return
	}
	var num int
	onMain(func() { num = platform.WindowNumber(win.NativeHandle()) })
	if num == 0 {
		t.Fatalf("%s: zero CGWindowID", name)
	}
	outPath := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screencapture -l %s failed: %v (%s)", name, err, out)
	}
	t.Logf("saved %s", outPath)
}

func TestFix1186bBoardTypingAndKeysSmoke(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run native window smoke tests")
	}
	a := redesignShelf(t)
	oldReg, oldUnreg := registerKey, unregisterKey
	held := map[string]bool{}
	registerKey = func(key string, _ func()) error {
		held[key] = true
		return nil
	}
	unregisterKey = func(key string) {
		delete(held, key)
	}
	t.Cleanup(func() {
		onMain(func() {
			a.releaseKeys()
			if a.board != nil && a.board.win != nil {
				a.board.close()
			}
			a.board = nil
			if a.note != nil {
				if a.note.win != nil {
					a.note.close()
				}
				closeNoteWatch(a.note)
				a.note = nil
			}
			if a.rn != nil && a.rn.win != nil {
				a.rn.win.Close()
			}
			a.rn = nil
			a.hide(hidePassive)
		})
		registerKey, unregisterKey = oldReg, oldUnreg
	})

	work := mygo.Screen.PrimaryDisplay().WorkArea
	expandShelf(t, a)

	// 1. A-P2-6: hiding the panel must not interrupt whiteboard inline typing.
	var editID doc.ID
	onMainCheck(t, func() string {
		a.board = nil
		b := a.boardSession()
		if b == nil {
			return "no board session"
		}
		editID = b.ed.CreateNote(geo.V(120, 120), "")
		b.frame = mygo.Rectangle{X: work.X + 80, Y: work.Y + 80, Width: 760, Height: 520}
		if !b.open() {
			return "board open failed"
		}
		b.ed.StartEditing(editID)
		b.ed.EditText = "面板收起继续编辑"
		b.ed.EditChanged()
		a.visible = true
		a.hide(hidePassive)
		return ""
	})
	time.Sleep(400 * time.Millisecond)

	onMainCheck(t, func() string {
		if a.board == nil || a.board.win == nil || !a.board.win.IsVisible() {
			return "board window closed or hidden when shelf folded"
		}
		if a.board.ed.Editing != editID {
			return fmt.Sprintf("board Editing=%q, want %q", a.board.ed.Editing, editID)
		}
		if err := a.board.waitSaves(); err != nil {
			return err.Error()
		}
		raw, err := os.ReadFile(a.board.store.Path())
		if err != nil {
			return err.Error()
		}
		if !strings.Contains(string(raw), "面板收起继续编辑") {
			return "board inline edit did not flush to disk during passive hide"
		}
		return ""
	})
	seg4CaptureWindow(t, a.board.win, "mac-board-typing-panel-hidden.png")

	// 2. B-P3-4: panel must not grab Esc/Space while board, note, or rename window is open.
	onMainCheck(t, func() string {
		a.visible = true
		if a.keysAreOurs() {
			return "keysAreOurs returned true while real board window is open"
		}
		a.holdKeys()
		if a.escHeld || a.spaceHeld || len(held) != 0 {
			return fmt.Sprintf("panel grabbed keys while board open: esc=%v space=%v held=%v", a.escHeld, a.spaceHeld, held)
		}
		a.board.close()
		a.board = nil
		if !a.keysAreOurs() {
			return "keysAreOurs stayed false after board window closed"
		}
		a.holdKeys()
		if !a.escHeld || !a.spaceHeld {
			return fmt.Sprintf("panel did not reclaim keys after board closed: esc=%v space=%v", a.escHeld, a.spaceHeld)
		}

		n := a.noteSession()
		n.text = "# B-P3-4 便利贴窗口测试\n"
		n.draft = n.text
		n.seen = n.text
		n.frame = mygo.Rectangle{X: work.X + 180, Y: work.Y + 140, Width: noteW, Height: noteH}
		if !n.open() {
			return "note open failed"
		}
		if a.keysAreOurs() {
			return "keysAreOurs returned true while real note window is open"
		}
		a.holdKeys()
		if a.escHeld || a.spaceHeld || len(held) != 0 {
			return fmt.Sprintf("panel grabbed keys while note open: esc=%v space=%v held=%v", a.escHeld, a.spaceHeld, held)
		}
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	seg4CaptureWindow(t, a.note.win, "mac-note-open.png")

	tmpFile := filepath.Join(t.TempDir(), "样本.txt")
	if err := os.WriteFile(tmpFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	onMainCheck(t, func() string {
		a.note.close()
		closeNoteWatch(a.note)
		a.note = nil
		if err := a.store.Clear(); err != nil {
			return err.Error()
		}
		if err := a.store.AddFiles([]string{tmpFile}); err != nil {
			return err.Error()
		}
		a.openRename(a.visibleItems())
		if a.rn == nil || a.rn.win == nil {
			return "rename window did not open"
		}
		a.visible = true
		if a.keysAreOurs() {
			return "keysAreOurs returned true while real rename window is open"
		}
		a.holdKeys()
		if a.escHeld || a.spaceHeld || len(held) != 0 {
			return fmt.Sprintf("panel grabbed keys while rename open: esc=%v space=%v held=%v", a.escHeld, a.spaceHeld, held)
		}
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	seg4CaptureWindow(t, a.rn.win, "mac-rename-open.png")

	onMainCheck(t, func() string {
		a.rn.win.Close()
		a.rn = nil
		if !a.keysAreOurs() {
			return "keysAreOurs stayed false after rename window closed"
		}
		return ""
	})
}

func TestFix1186bAwakeTimerPinsAndPetDragSmoke(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run native window smoke tests")
	}
	smokeArt(t)
	a := liveSmokeShelf(t)
	d := mygo.Screen.PrimaryDisplay()
	area := d.WorkArea

	origCreate := func(platform.PowerAssertionKind) (platform.PowerAssertionID, error) { return 42, nil }
	var released int
	origRelease := func(platform.PowerAssertionID) { released++ }

	origPinRedraw := pinRedraw
	redraws := map[string]int{}
	pinRedraw = func(it *pinItem) {
		redraws[it.cardID]++
		origPinRedraw(it)
	}

	var awakePin, timerPin *pinItem
	t.Cleanup(func() {
		pinRedraw = origPinRedraw
		onMain(func() {
			if awakePin != nil {
				a.unpin(awakePin, false)
			}
			if timerPin != nil {
				a.unpin(timerPin, false)
			}
			if a.awake != nil {
				a.awake.stop(time.Now())
				a.awake = nil
			}
			if a.clock != nil {
				a.clock.stopTimer(time.Now())
				a.clock = nil
			}
			a.modules = nil
			a.closePet()
		})
	})

	// 1. B-P2-1 + B-P2-4: awake on + panel folded keeps desktop pin countdowns ticking every second without 33Hz spin.
	onMainCheck(t, func() string {
		a.awake = &awakeModule{
			host:    a,
			create:  origCreate,
			release: origRelease,
		}
		a.clock = newClockModule(a)
		a.clock.playSound = func() {}
		a.clock.stopSound = func() {}
		a.modules = []module{a.awake, a.clock, timerChip{a.clock}, stopwatchChip{a.clock}}

		awakePin = a.addPin("awake", 1, d.ID, pinFree, false, mygo.Rectangle{
			X: area.X + area.Width - 260, Y: area.Y + 80, Width: pinCardW, Height: pinCardH,
		})
		timerPin = a.addPin("timer", 1, d.ID, pinFree, false, mygo.Rectangle{
			X: area.X + area.Width - 260, Y: area.Y + 240, Width: pinCardW, Height: pinCardH,
		})
		if awakePin == nil || awakePin.win == nil || timerPin == nil || timerPin.win == nil {
			return "failed to open awake/timer desktop pin windows"
		}
		now := time.Now()
		a.awake.start(now, 30*time.Minute)
		a.clock.startTimer(now, 25*time.Second)
		a.hide(hidePassive)
		if a.monitorActive(now) {
			return "panel-closed shelf with awake on stayed in 33Hz active polling"
		}
		return ""
	})
	time.Sleep(350 * time.Millisecond)
	seg4CaptureWindow(t, awakePin.win, "mac-pin-awake-t0.png")
	seg4CaptureWindow(t, timerPin.win, "mac-pin-timer-t0.png")

	var firstAwakeRemain string
	var distinctAwakeRemains int
	onMain(func() {
		firstAwakeRemain = a.awake.remain
		if firstAwakeRemain != "" {
			distinctAwakeRemains = 1
		}
	})
	prevRemain := firstAwakeRemain
	start := time.Now()
	for i := 0; i < 25; i++ {
		time.Sleep(100 * time.Millisecond)
		onMain(func() {
			now := time.Now()
			for _, m := range a.modules {
				m.tick(now)
			}
			if a.awake.remain != prevRemain {
				prevRemain = a.awake.remain
				distinctAwakeRemains++
			}
		})
	}
	elapsed := time.Since(start)
	onMainCheck(t, func() string {
		if distinctAwakeRemains < 3 {
			return fmt.Sprintf("awake pin countdown saw %d distinct values in %v (first=%q, last=%q)", distinctAwakeRemains, elapsed, firstAwakeRemain, a.awake.remain)
		}
		if redraws["awake"] < 2 || redraws["timer"] < 2 {
			return fmt.Sprintf("pin redraws too low while panel closed: awake=%d timer=%d", redraws["awake"], redraws["timer"])
		}
		return ""
	})
	seg4CaptureWindow(t, awakePin.win, "mac-pin-awake-t2.png")
	seg4CaptureWindow(t, timerPin.win, "mac-pin-timer-t2.png")

	// 2. B-P2-3: desktop otter real window drag, missed PointerUp auto-finish, and Canceled restore.
	onMainCheck(t, func() string {
		a.settings.Pet = settings.PetSettings{Shown: true, Size: settings.PetSizeMedium}
		a.settings.PetPos = nil
		a.settings.PetDisplay = ""
		a.closePet()
		a.petStart()
		if a.pet == nil || a.pet.win == nil {
			return "pet window did not open"
		}
		w, h := petBox(petPictureH(settings.PetSizeMedium))
		a.pet.display = d.ID
		a.pet.frame = petDefaultSpot(area, w, h)
		a.pet.slideAt = time.Time{}
		a.pet.applyFrame()
		return ""
	})
	time.Sleep(400 * time.Millisecond)
	seg4CaptureWindow(t, a.pet.win, "mac-pet-before-drag.png")

	var draggedFrame, initialFrame mygo.Rectangle
	onMainCheck(t, func() string {
		pet := a.pet
		initialFrame = pet.frame
		pet.pressAt(20, 20)
		pet.lockMove(petDragStart+30, 0)
		a.petTick(time.Now(), mygo.Point{X: pet.frame.X + 80, Y: pet.frame.Y - 30}, true)
		draggedFrame = pet.frame
		if !pet.pressed || !pet.moving || draggedFrame == initialFrame {
			return fmt.Sprintf("pet did not enter active drag: pressed=%v moving=%v frame=%+v initial=%+v", pet.pressed, pet.moving, draggedFrame, initialFrame)
		}
		// Simulate lost PointerUp: 3 consecutive !down polls end the session cleanly.
		for i := 0; i < 3; i++ {
			a.petTick(time.Now(), mygo.Point{X: pet.frame.X + 20, Y: pet.frame.Y + 20}, false)
		}
		if pet.pressed || pet.moving {
			return fmt.Sprintf("pet drag remained stuck after 3 !down polls: pressed=%v moving=%v", pet.pressed, pet.moving)
		}
		if a.settings.PetPos == nil {
			return "pet drag lost_up did not persist PetPos after session ended"
		}
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	seg4CaptureWindow(t, a.pet.win, "mac-pet-after-lost-up.png")

	// Cancel during press must restore pressFrame on the real window.
	onMainCheck(t, func() string {
		pet := a.pet
		before := pet.frame
		view := ui.NewTester(pet.view, pet.frame.Width, pet.frame.Height)
		view.Frame()
		view.Press(25, 25)
		view.Move(45, 45)
		if !pet.pressed || !pet.moving {
			return fmt.Sprintf("expected active drag before cancel, got pressed=%v moving=%v", pet.pressed, pet.moving)
		}
		pet.frame = mygo.Rectangle{X: before.X + 48, Y: before.Y, Width: before.Width, Height: before.Height}
		pet.applyFrame()
		view.CancelPress()
		if pet.pressed || pet.moving {
			return fmt.Sprintf("canceled drag left flags set: pressed=%v moving=%v", pet.pressed, pet.moving)
		}
		if pet.frame != before || pet.win.Bounds() != before {
			return fmt.Sprintf("canceled drag frame=%+v bounds=%+v, want %+v", pet.frame, pet.win.Bounds(), before)
		}
		if released < 0 {
			return "unexpected negative release count"
		}
		return ""
	})
}
