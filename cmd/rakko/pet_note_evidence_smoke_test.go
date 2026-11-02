//go:build darwin

package main

// Real-window proof for the desktop otter and the 便利贴 window, run only
// under RAKKO_BASKET_SMOKE=1 (the shared TestMain hosts the app loop). Shots
// land in /tmp/rakko-pet/; the instance works in the smoke data dir and
// never touches ~/Library/Application Support/Rakko.

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/mac"
	"rakko/internal/settings"
)

const petShotDir = "/tmp/rakko-pet"

// smokeArt points the otter's loader at the repository's artwork: a test
// binary resolves its resources inside cmd/rakko, where nothing ships. The
// smoke harness only runs locally, where the brand files are present.
func smokeArt(t *testing.T) {
	t.Helper()
	old := otterDir
	otterDir = func() string { return "../../resources/otter" }
	resetOtters()
	t.Cleanup(func() { otterDir = old; resetOtters() })
	if petArt() == nil {
		t.Skip("resources/otter is not in this checkout (public build)")
	}
}

// petShot captures one window of this process into petShotDir.
func petShot(t *testing.T, win *mygo.Window, name string) string {
	t.Helper()
	if win == nil {
		t.Fatalf("%s: no window", name)
	}
	num := mac.WindowNumber(win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(500 * time.Millisecond) // let the drawn frame settle
	if err := os.MkdirAll(petShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(petShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, path)
	return path
}

// petRegionShot captures what is behind and around the otter's window: the
// wallpaper and the Dock make its place readable.
func petRegionShot(t *testing.T, a *shelf, name string) string {
	t.Helper()
	var r mygo.Rectangle
	onMain(func() {
		if a.pet != nil {
			r = a.pet.frame
		}
	})
	if r.Width == 0 {
		t.Fatalf("%s: the otter has no frame", name)
	}
	if err := os.MkdirAll(petShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pad := 160
	path := filepath.Join(petShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", "-R",
		fmt.Sprintf("%d,%d,%d,%d", max(r.X-pad, 0), max(r.Y-pad, 0), r.Width+2*pad, r.Height+2*pad),
		path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, path)
	return path
}

// petAdvanceToRest runs the settle's frames to the end, as the monitor
// would.
func petAdvanceToRest(t *testing.T, a *shelf) {
	t.Helper()
	for range 200 {
		done := false
		onMain(func() {
			done = a.pet == nil || a.pet.slideAt.IsZero()
			if !done {
				a.petAdvance(time.Now())
			}
		})
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the otter never came to rest")
}

// TestSmokePetDesktop puts the otter on the desktop: standing, carried,
// seated, holding a catch, with its right-click menu written beside the
// shots.
func TestSmokePetDesktop(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to capture the desktop otter")
	}
	smokeArt(t)
	a := smokeShelf
	onMain(func() {
		if a.win != nil {
			a.hide(hideExplicit)
		}
		a.settings.Pet = settings.PetSettings{Shown: true, Size: settings.PetSizeMedium}
		a.settings.PetPos = nil
		a.settings.PetDisplay = ""
		a.closePet()
		a.petStart()
		if a.pet == nil {
			t.Fatal("the otter did not start")
		}
		primary := mygo.Screen.PrimaryDisplay()
		area := primary.WorkArea
		w, h := petBox(petPictureH(settings.PetSizeMedium))
		a.pet.display = primary.ID
		a.pet.frame = petDefaultSpot(area, w, h)
		a.pet.slideAt = time.Time{}
		a.pet.applyFrame()
	})
	time.Sleep(time.Second)
	if a.pet == nil || a.pet.win == nil {
		t.Fatal("the otter window did not open")
	}
	petShot(t, a.pet.win, "pet-desktop")
	petRegionShot(t, a, "pet-desktop-region")

	// Carried: the press the pointer session arms, and the frame the tick
	// applies — the same two calls a real drag makes.
	onMain(func() {
		pet := a.pet
		pet.pressAt(30, 40)
		pet.lockMove(petDragStart+40, -120)
		a.petTick(time.Now(), mygo.Point{X: pet.frame.X + 120, Y: pet.frame.Y + 60}, true)
	})
	time.Sleep(500 * time.Millisecond)
	petShot(t, a.pet.win, "pet-carried")
	if pose := a.pet.drawn; pose != petLifted {
		t.Fatalf("carried pose = %q, want %q", pose, petLifted)
	}

	// Seated on the work area's floor: the release near the Dock's top.
	onMain(func() {
		pet := a.pet
		area := petArea(pet.display)
		pet.pressed, pet.moving = false, false
		a.petRelease(mygo.Point{X: pet.frame.X + 10, Y: area.Y + area.Height - 8})
	})
	petAdvanceToRest(t, a)
	petShot(t, a.pet.win, "pet-seated")
	petRegionShot(t, a, "pet-seated-region")
	onMain(func() {
		pet := a.pet
		area := petArea(pet.display)
		if pet.frame.Y+pet.frame.Height != area.Y+area.Height {
			t.Fatalf("the otter is not on the floor: %+v vs %+v", pet.frame, area)
		}
	})

	// A catch: the same path a drop on the otter takes, with a real file.
	p := writeSmokePNG(t, t.TempDir(), "接住.png", 320, 240)
	u := (&url.URL{Scheme: "file", Path: p}).String()
	onMain(func() {
		a.petReceive(transfer.New(transfer.NewItem(transfer.Bytes(transfer.FileList, []byte(u)))))
		a.pet.invalidate()
	})
	time.Sleep(300 * time.Millisecond)
	petShot(t, a.pet.win, "pet-caught")

	// The right-click menu's items, as the view builds them. The native
	// menu is AppKit's own, so the evidence is its content.
	var lines []string
	onMain(func() {
		view := ui.NewTester(a.pet.view, a.pet.frame.Width, a.pet.frame.Height)
		view.Frame()
		if err := view.RightClick("海獭"); err != nil {
			t.Fatalf("the otter's menu did not open: %v", err)
		}
		lines = view.Menu()
	})
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	if err := os.MkdirAll(petShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(petShotDir, "pet-menu.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("otter menu items: %v", lines)
	if len(lines) < 4 {
		t.Fatalf("the otter's menu is short: %v", lines)
	}
	onMain(func() { a.closePet() })
}

// TestSmokeNoteCardAndWindow proves the 便利贴 card and its real window:
// the card's tap opens it, what was typed is on screen, and the window
// outlives the panel.
func TestSmokeNoteCardAndWindow(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to capture the note card and window")
	}
	a := dropSmokeShelf(t)
	onMain(func() {
		if a.note != nil && a.note.win != nil {
			a.note.close()
		}
		a.settings.Pet = settings.PetSettings{Shown: true, Size: settings.PetSizeMedium}
		a.mutateTools(func(l *layout.Layout) {
			l.AddCardSize("note", 2, 1)
			l.MoveCard("note", 0)
			l.SetCardSize("note", 2, 1)
		})
	})
	expandShelf(t, a)
	onMain(func() { a.setTab(pageTools) })
	time.Sleep(700 * time.Millisecond)
	onMain(func() {
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	time.Sleep(300 * time.Millisecond)
	petShot(t, a.win, "note-card-2x1")

	var opened bool
	onMain(func() { opened = a.runToolCard("note") })
	if !opened || a.note == nil || a.note.win == nil {
		t.Fatal("the note card did not open its window")
	}
	onMain(func() {
		a.note.draft = "# 周五发版前\n– 改 README 顶栏描述\n– 补 v1.13 截图\n– 问 Codex 额度为什么 5 小时窗口跳\n– [ ] 桌宠姿态验收"
		a.note.text = a.note.draft
		a.note.invalidate()
	})
	petShot(t, a.note.win, "note-window")

	// The panel folds away; the window stays, with what it holds.
	onMain(func() { a.hide(hideExplicit) })
	time.Sleep(400 * time.Millisecond)
	var up bool
	onMain(func() { up = a.note.win != nil && a.note.win.IsVisible() })
	if !up {
		t.Fatal("the note window went away with the panel")
	}
	petShot(t, a.note.win, "note-window-panel-closed")
	// The card and its heading after the note has text: the wide form
	// shows the heading and its preview lines.
	onMain(func() {
		a.note.close()
		a.mutateTools(func(l *layout.Layout) { l.SetCardSize("note", 2, 1) })
	})
	time.Sleep(400 * time.Millisecond)
	expandShelf(t, a)
	onMain(func() {
		a.setTab(pageTools)
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	time.Sleep(500 * time.Millisecond)
	onMain(a.syncWindowFrame)
	petShot(t, a.win, "note-card-with-note")
	onMain(func() {
		if a.note.watch != nil {
			a.note.watch.Close()
		}
		a.note = nil
		a.hide(hidePassive)
	})
}

// TestSmokePetSettings captures the 桌宠 group in the real Settings window.
func TestSmokePetSettings(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to capture the desktop otter's settings")
	}
	smokeArt(t)
	a := smokeShelf
	var oldDir string
	onMain(func() {
		oldDir = a.dataDir
		a.dataDir = t.TempDir()
		prefs, _ := settings.Load(a.dataDir)
		a.settings = prefs
		a.openSettings()
		a.settingsPage = settingsPageNotch
		a.settingsScroll.Y = 0
	})
	t.Cleanup(func() {
		onMain(func() {
			a.dataDir = oldDir
			a.settings = settings.Default()
		})
	})
	if a.settingsWin == nil {
		t.Fatal("the Settings window did not open")
	}
	time.Sleep(700 * time.Millisecond)
	petShot(t, a.settingsWin, "pet-settings")
	onMain(func() {
		if a.settingsWin != nil {
			a.settingsWin.Close()
			a.settingsClosed()
		}
	})
}
