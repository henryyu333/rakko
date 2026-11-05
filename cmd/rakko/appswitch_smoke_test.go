//go:build darwin

package main

// Native smoke coverage for the app switcher, gated behind
// RAKKO_BASKET_SMOKE=1 like every other *_smoke_test.go: real window,
// real HID clicks through clickAt, real screencapture shots into
// /tmp/rakko-appswitch/native/. Every click lands inside this test's own
// window: seat clicks prove the shell's hit paths without touching the
// user's apps — the switchToApp seams stay stubbed, and no HID event is
// ever sent to another app's surface or the bare desktop. Real
// launch/activate proof lives in the gate's controlled blank fixtures
// (/tmp/rakko-appswitch/native-check.sh), not here.
//
// Run through the gate:  bash .scratch/native-gate.sh /tmp/rakko-appswitch/native/log.txt \
//   env RAKKO_BASKET_SMOKE=1 go test -count=1 -run 'TestSmokeApps' ./cmd/rakko

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

const appsShotDir = "/tmp/rakko-appswitch/native"

// shotAppsWindow screenshots w into the R2 evidence directory.
func shotAppsWindow(t *testing.T, w *mygo.Window, name string) string {
	t.Helper()
	num := mac.WindowNumber(w.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(350 * time.Millisecond) // let the drawn frame settle
	if err := os.MkdirAll(appsShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(appsShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s", shot)
	return shot
}

// appsSmokeShelf is redesignShelf plus a deterministic app world: the
// system seams are stubbed (no click ever switches a real app), the shelf
// placed, and a fixed pinned list set in the smoke settings. The shared
// smoke shelf is also reset on entry AND on cleanup — island shape, drag
// offset, live queue, panel and wings all go back, so no test inherits or
// leaks switcher state.
func appsSmokeShelf(t *testing.T, running []mac.RunningApp) *shelf {
	t.Helper()
	a := redesignShelf(t)
	stubApps(t, &appsStub{
		running: running,
		paths: map[string]string{
			// Fake install paths: only the stubbed openPath ever sees
			// them, so a mistake can never launch a real application.
			"com.smoke.pin1": "/tmp/com.smoke.pin1.app",
			"com.smoke.pin2": "/tmp/com.smoke.pin2.app",
		},
		names: map[string]string{"com.smoke.pin1": "Pin One", "com.smoke.pin2": "Pin Two"},
		actOK: map[int]bool{},
	})
	// reset returns the shared shelf to a known closed state: settings
	// back to Default (LiveActivities on — a zero Settings left over
	// from an older run silently disabled every notice), feedback
	// capsule, live queue, drag flags, panel, fold animation and the
	// drawn-shell motion state all cleared. Runs on entry and on
	// cleanup so no test inherits or leaks state.
	pinned := []string{"com.smoke.pin1", "com.smoke.pin2"}
	reset := func() {
		onMain(func() {
			a.endAppSwitch()
			a.island, a.islandDX = false, 0
			a.settings = settings.Default()
			a.settings.PinnedApps = &pinned
			a.fb = feedback{}
			a.fx = shellMotion{}
			a.live = liveQueue{}
			a.snap = snapState{}
			a.bandBox, a.bandHits = mygo.Rectangle{}, nil
			a.islandPress = islandPress{}
			a.dragging, a.dropHot, a.ownDrag = false, false, false
			a.search, a.focusMode = "", false
			a.visible, a.edgeOpened = false, false
			a.closeAt = time.Time{}
			a.peeking, a.peekProgress, a.peekFrom, a.peekAnimStart = false, 0, 0, time.Time{}
			a.progress, a.animationFrom, a.animationStart = 0, 0, time.Time{}
			a.syncMousePassThrough()
			a.syncWindowFrame()
			// The window is created Hidden: without this no frame ever
			// presents — the GPU keeps an old picture while fx.peekKind,
			// the entry/pop boxes and appsDrawOff stay stale (exactly
			// what the native round showed). Idempotent like every
			// sibling smoke suite.
			a.win.SetOpacity(1)
			a.win.ShowInactive()
			a.win.Invalidate()
		})
	}
	reset()
	t.Cleanup(reset)
	onMain(func() { a.place(mygo.Screen.CursorScreenPoint()) })
	return a
}

// waitApps polls cond on the main thread through the shared waitMain.
// Every poll also invalidates the window: the peek/flip springs only
// advance while frames render, and a wait that never draws would sit
// forever at the state it started from — the native round showed
// peekKind stuck on peekApps long after apps.on had already folded.
// Failure dumps the drawn state so a real defect is still loud.
func waitApps(t *testing.T, a *shelf, what string, cond func() bool) {
	t.Helper()
	ok := waitMain(4*time.Second, func() bool {
		done := cond()
		if a.win != nil {
			a.win.Invalidate() // pump the springs the wait is watching
		}
		return done
	})
	if ok {
		return
	}
	var st string
	onMain(func() {
		st = fmt.Sprintf("visible=%v progress=%.2f openK=%.2f peekKind=%d peek=%.2f entry=%+v pop=%+v fb.drawn=%v live.shown=%v liveAllow=%v apps.on=%v",
			a.visible, a.progress, a.fx.openK, a.fx.peekKind, a.peekProgress,
			a.apps.entryRect, a.apps.popRect, a.fb.drawn, a.live.shown, a.liveAllow(), a.apps.on)
	})
	t.Fatalf("timed out waiting for %s: %s", what, st)
}

// clickOverlayAt posts a real click inside this test's own overlay window
// while the point sits in the menu-bar band the shared clickAt refuses.
// The proof is exact, not policy: the point must be on this display,
// inside the test window's frame AND on the shape the frame currently
// draws (wings pill, popover or the recorded entry box — the island
// capsule counts as its anchor), never on the hardware camera band, and
// WindowServer must report the test's own window id at that point once
// the pointer lands there. The pointer is restored by postRealClick.
func clickOverlayAt(t *testing.T, a *shelf, x, y float64) {
	t.Helper()
	postRealClick(t, x, y, func() {
		p := mygo.Point{X: int(x), Y: int(y)}
		var drawable bool
		onMain(func() {
			d := mygo.Screen.DisplayNearestPoint(p)
			if p.X < d.Bounds.X || p.X >= d.Bounds.X+d.Bounds.Width ||
				p.Y < d.Bounds.Y || p.Y >= d.Bounds.Y+d.Bounds.Height {
				return // off this display entirely
			}
			if !a.island && a.notchW > 0 && a.notchH > 0 &&
				p.Y < a.notchY+a.notchH &&
				p.X >= a.notchX && p.X <= a.notchX+a.notchW {
				return // the hardware camera band itself
			}
			if a.fx.peekKind == peekApps {
				// The wings are the drawn shape now: the pill box tracks
				// the stale chevron, so verify against the wings' real
				// extent — anchor + both side wings, leaning by off.
				g := a.appGeom()
				baseW, baseH := a.anchorSize()
				w := baseW + g.sideL + g.sideR
				cx := float32(a.stripCenter()) + a.appsDrawOff()
				top := a.frameTop() + a.sheetTop()
				drawable = float64(p.X) >= float64(cx-w/2)-0.5 &&
					float64(p.X) <= float64(cx+w/2)+0.5 &&
					p.Y >= top && float64(p.Y) <= float64(top)+float64(baseH+appsBody)+0.5
			} else {
				drawable = insideRect(p, a.frame) &&
					(a.overPeekPill(p) || a.overAppsPop(p) || a.overAppEntry(p) ||
						(a.island && a.overAnchor(p)))
			}
			// The popover hangs below the wings' own box; its rows are
			// still this window's drawn content.
			drawable = drawable || a.overAppsPop(p) || a.overAppEntry(p)
		})
		if !drawable {
			var diag string
			onMain(func() {
				diag = fmt.Sprintf("frame=%+v pill=%+v pop=%+v entry=%+v peekKind=%d off=%.1f island=%v",
					a.frame, a.peekPillRect(), a.apps.popRect, a.apps.entryRect,
					a.fx.peekKind, a.appsDrawOff(), a.island)
			})
			t.Fatalf("refusing click at (%.0f,%.0f): outside the test window's drawn shape — %s", x, y, diag)
		}
		time.Sleep(150 * time.Millisecond) // let the pointer gate re-ask
		var hit, num int
		onMain(func() {
			primary := mygo.Screen.PrimaryDisplay()
			hit = mac.WindowAtPoint(x, y, float64(primary.Bounds.Y+primary.Bounds.Height))
			num = mac.WindowNumber(a.win.NativeHandle())
		})
		if hit != num {
			t.Fatalf("refusing click at (%.0f,%.0f): native target %d is not our window %d", x, y, hit, num)
		}
	})
}

// openAppsWings puts the real window into the wings state and settles it.
// Idempotent: a state where the wings already opened (an entry click ran
// first) is left as-is — the helper settles, it never toggles the mode.
// If a stray system press folded the mode during the settle wait, the
// open is retried once before failing.
func openAppsWings(t *testing.T, a *shelf) {
	t.Helper()
	// Idempotent setup only: an already-open mode is settled, never
	// toggled shut. After the settle wait the wings must still be open —
	// a folded mode is a real defect, not noise to open past.
	onMain(func() {
		if !a.apps.on {
			a.openAppSwitch()
		}
		a.peekProgress, a.peekFrom = 1, 1
		a.peekAnimStart = time.Time{}
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	waitApps(t, a, "the drawn wings", func() bool {
		return a.apps.on && a.fx.peekKind == peekApps
	})
}

// closeAppsWings folds the wings and waits for the fold to land.
func closeAppsWings(t *testing.T, a *shelf) {
	onMain(func() {
		a.endAppSwitch()
		a.win.Invalidate()
	})
	// fx.peekKind keeps its last drawn kind once the shell stops
	// peeking — it is a display cache, not a state flag — so the fold is
	// only observable as: mode off, no peek target, spring at rest.
	waitApps(t, a, "the wings folded", func() bool {
		return !a.apps.on && !a.peeking && a.peekProgress <= 0.01
	})
}

var smokeApps = []mac.RunningApp{
	{BundleID: "com.smoke.pin1", Name: "Pin One", PID: 101},
	{BundleID: "com.smoke.pin2", Name: "Pin Two", PID: 102},
	{BundleID: "com.smoke.run1", Name: "Run One", PID: 103},
	{BundleID: "com.smoke.run2", Name: "Run Two", PID: 104},
	{BundleID: "com.smoke.run3", Name: "Run Three", PID: 105},
	{BundleID: "com.smoke.run4", Name: "Run Four", PID: 106},
}

// appSeat returns the screen point at the centre of wing seat index i:
// 0..ls-1 are the left wing's buttons left→right, ls.. are the right
// wing's (last is always 更多 when a right wing exists).
func appSeat(t *testing.T, a *shelf, i int) (x, y float64) {
	t.Helper()
	var g appGeom
	var cx float64
	var baseW float32
	var ls, rs int
	onMain(func() {
		g = a.appGeom()
		cx = float64(a.stripCenter()) + float64(g.off)
		baseW, _ = a.anchorSize()
		ls, rs = a.appsSlots()
	})
	left := cx - float64(baseW+g.sideL+g.sideR)/2
	if i < ls {
		x = left + float64(appPadX+appBtn/2) + float64(i*(appBtn+appGap))
	} else if i < ls+rs {
		// Right wing seats are right-aligned inside the wing: the j-th
		// seat's centre from the wing's right edge.
		j := rs - 1 - (i - ls) // distance from the rightmost seat inward
		right := left + float64(g.sideL) + float64(baseW) + float64(g.sideR) - float64(appPadX)
		x = right - float64(j)*float64(appBtn+appGap) - float64(appBtn/2)
	} else {
		t.Fatalf("seat %d out of range %d/%d", i, ls, rs)
	}
	onMain(func() { y = float64(a.frameTop()+a.sheetTop()) + float64(a.fx.h)/2 })
	return x, y
}

// The collapsed-state entry opens the wings through a real click; the
// left wing's pinned seat launches through the recorded stub, proving the
// button is a real target inside the real window.
func TestSmokeAppsWingsOpenFromEntry(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	onMain(func() {
		a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	var er mygo.Rectangle
	waitApps(t, a, "the collapsed-state entry", func() bool {
		er = a.apps.entryRect
		return er.Width > 0 && a.fx.peekKind == peekChevron
	})
	clickOverlayAt(t, a, float64(er.X+er.Width/2), float64(er.Y+er.Height/2))
	onMain(func() {
		if !a.apps.on {
			t.Fatal("clicking the entry did not open the wings")
		}
	})
	openAppsWings(t, a)
	shotAppsWindow(t, a.win, "wings-open")

	// The first pinned seat is a real button in the real window.
	x, y := appSeat(t, a, 0)
	clickOverlayAt(t, a, x, y)
	onMain(func() {
		if a.apps.on {
			t.Fatal("the wings must fold after a successful switch")
		}
	})
	closeAppsWings(t, a)
}

// The 更多 seat opens the glass popover under the wings at a real box the
// frame covers; 编辑常用应用 swaps to the manage list, whose 前移/取消固定
// controls reorder and persist through real clicks.
func TestSmokeAppsPopoverAndManage(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	openAppsWings(t, a)
	ls, rs := a.appsSlots()
	if rs == 0 {
		t.Skip("no right wing on this display")
	}
	x, y := appSeat(t, a, ls+rs-1) // the last right-wing seat: 更多
	clickOverlayAt(t, a, x, y)
	waitApps(t, a, "the 更多 popover", func() bool {
		return a.apps.more && a.apps.popRect.Width > 0
	})
	shotAppsWindow(t, a.win, "wings-more")

	var pop, fr mygo.Rectangle
	onMain(func() {
		pop = a.apps.popRect
		fr = a.desiredFrame()
	})
	if pop.Width < 200 || pop.Height < 100 {
		t.Fatalf("popover box %v is not a real pane", pop)
	}
	if pop.X < fr.X || pop.X+pop.Width > fr.X+fr.Width || pop.Y < fr.Y || pop.Y+pop.Height > fr.Y+fr.Height {
		t.Fatalf("frame %v does not cover popover %v", fr, pop)
	}
	// 编辑常用应用 is the pane's footer row.
	clickOverlayAt(t, a, float64(pop.X+pop.Width/2), float64(pop.Y+pop.Height-appPopFoot/2))
	waitApps(t, a, "the manage list", func() bool {
		return a.apps.manage && !a.apps.installedLoad
	})
	shotAppsWindow(t, a.win, "wings-manage")

	// Pinned rows: index 0 is the first row, index 1 the last. Controls
	// sit 73/47/21 DIP from the pane's right padding edge (up/down/rm).
	rowY := func(i int) float64 {
		onMain(func() { pop = a.apps.popRect })
		return float64(pop.Y+appPopHead) + float64(i)*float64(appPopRowH) + float64(appPopRowH)/2
	}
	upX := float64(pop.X + pop.Width - 8 - 73)
	downX := float64(pop.X + pop.Width - 8 - 47)
	rmX := float64(pop.X + pop.Width - 8 - 21)
	var order []string
	pin := func() []string { onMain(func() { order = pinned(a) }); return order }

	// Row i=1's 前移 swaps pin2 in front of pin1.
	clickOverlayAt(t, a, upX, rowY(1))
	if pin := pin(); len(pin) != 2 || pin[0] != "com.smoke.pin2" {
		t.Fatalf("前移 did not reorder the real list: %v", pin)
	}
	// pin2 is now row i=0: its 上移 is dimmed — the same control is inert.
	clickOverlayAt(t, a, upX, rowY(0))
	if pin := pin(); pin[0] != "com.smoke.pin2" {
		t.Fatalf("first row's 上移 must be inert, got %v", pin)
	}
	// pin1 is the last row (i=1): its 下移 is dimmed — inert the same way.
	clickOverlayAt(t, a, downX, rowY(1))
	if pin := pin(); pin[1] != "com.smoke.pin1" {
		t.Fatalf("last row's 下移 must be inert, got %v", pin)
	}
	// 取消固定 on the current first row (pin2) removes it for real,
	// leaving pin1.
	clickOverlayAt(t, a, rmX, rowY(0))
	if pin := pin(); len(pin) != 1 || pin[0] != "com.smoke.pin1" {
		t.Fatalf("取消固定 did not remove: %v", pin)
	}
	closeAppsWings(t, a)
}

// The anchor stays the search path while the wings are open: a real click
// on the anchor column folds the wings into the panel, and Esc folds the
// wings then the panel — all clicks inside this test's own window.
func TestSmokeAppsCenterSearchAndCancel(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	openAppsWings(t, a)
	// Inside routing: a drawn point in the anchor column — the shell's
	// bottom band, left of the collapse chevron's 38pt box — is the
	// search press; the chevron keeps its own target. Below the hardware
	// band, inside our drawn shell, never on another app's surface.
	var baseH float32
	var cx, top int
	onMain(func() {
		_, baseH = a.anchorSize()
		cx, top = a.stripCenter(), a.frameTop()+a.sheetTop()
	})
	clickOverlayAt(t, a, float64(cx-30), float64(top)+float64(baseH)+18)
	var visible, on bool
	onMain(func() { visible, on = a.visible, a.apps.on })
	if !visible || on {
		t.Fatalf("anchor click must fold wings into search: visible=%v on=%v", visible, on)
	}
	onMain(func() { a.hide(hideExplicit) })
	waitApps(t, a, "the panel closed", func() bool {
		return !a.visible && a.progress <= 0.01
	})

	openAppsWings(t, a)
	onMain(func() { a.escape() })
	onMain(func() { visible, on = a.visible, a.apps.on })
	if on || visible {
		t.Fatalf("Esc must fold only the wings: visible=%v on=%v", visible, on)
	}
	// Esc a second time folds the panel it opened — still inside our own
	// window; the outside-press cancel (a click on another app's surface)
	// is covered by the headless outside-routing tests instead of an HID
	// event aimed at the bare desktop.
	openAppsWings(t, a)
	onMain(func() { a.escape() })
	onMain(func() { visible, on = a.visible, a.apps.on })
	if on || visible {
		t.Fatalf("Esc must fold only the wings: visible=%v on=%v", visible, on)
	}
	closeAppsWings(t, a)
}

// While a track is loaded the right wing keeps its own app entry — the
// music wings and the entry coexist; clicking it opens the apps mode and
// folding returns the music wings, not the bare chevron.
func TestSmokeAppsEntryOnMusicWings(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	onMain(func() { fakeTrack(a, true) })
	peekShelf(a)
	// The entry is drawn only once the real loop renders the wings:
	// wait for its recorded box, not a fixed sleep.
	var er mygo.Rectangle
	waitApps(t, a, "the music wings' app entry", func() bool {
		er = a.apps.entryRect
		return er.Width > 0 && a.fx.peekKind == peekWings
	})
	shotAppsWindow(t, a.win, "music-wings-entry")
	clickOverlayAt(t, a, float64(er.X+er.Width/2), float64(er.Y+er.Height/2))
	var on bool
	onMain(func() { on = a.apps.on })
	if !on {
		t.Fatal("the music wings' app entry did not open the mode")
	}
	shotAppsWindow(t, a.win, "music-to-apps")
	closeAppsWings(t, a)
	// The fold hands the shell back to the track's wings — wait for the
	// real transition, then compare the live sizes.
	waitApps(t, a, "the music wings back", func() bool {
		return a.fx.peekKind == peekWings
	})
	var pw, mw float32
	onMain(func() { pw, _ = a.peekSize(); mw, _ = a.wingsSize() })
	if pw != mw {
		t.Fatalf("wings did not return after the mode: %.0f != %.0f", pw, mw)
	}
	onMain(func() { a.np = nil })
}

// A showing live notice pauses for the wings and resumes after — through
// the real window's own state, not only the model.
func TestSmokeAppsPausesLiveNotice(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	now := time.Now()
	onMain(func() {
		a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 80}, 8*time.Second, now)
		a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	// The queue must actually show the notice — a fixed sleep once hid
	// settings left over from an earlier run that disabled it entirely.
	waitApps(t, a, "the charging notice", func() bool {
		return a.live.shown && a.fx.peekKind == peekLiveWings
	})
	shotAppsWindow(t, a.win, "notice-showing")
	openAppsWings(t, a)
	var ok bool
	onMain(func() { _, ok = a.liveNow(time.Now(), a.liveAllow()) })
	if ok {
		t.Fatal("notice must hide while the wings are up")
	}
	shotAppsWindow(t, a.win, "notice-paused")
	closeAppsWings(t, a)
	waitApps(t, a, "the notice back", func() bool {
		return a.live.shown && a.fx.peekKind == peekLiveWings
	})
	onMain(func() { a.live = liveQueue{} })
}

// An island dragged to the work area's edge keeps the wings inside the
// display: seats drop on the pinned side, the shell leans off the anchor,
// and the frame covers all of the drawn shell.
func TestSmokeAppsCorneredIsland(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	onMain(func() {
		a.island = true
		a.islandDX = a.dispWork.Width/2 - 100 // near the right edge
	})
	openAppsWings(t, a)
	var g appGeom
	var fr mygo.Rectangle
	onMain(func() {
		g = a.appGeom()
		fr = a.desiredFrame()
	})
	if g.off == 0 && g.ls == g.rs {
		t.Fatal("cornered island kept symmetric seats — the bug is back")
	}
	shotAppsWindow(t, a.win, "island-cornered")
	// The drawn shell — not the transparent strip around it — must stay
	// on the display, and the frame must cover what is drawn. Ear and
	// overshoot padding may legally extend past the work area.
	var drawnL, drawnR float32
	onMain(func() {
		w, _ := a.appsSize()
		cx := float32(a.stripCenter()) + a.appsDrawOff()
		drawnL, drawnR = cx-w/2, cx+w/2
	})
	disp := a.dispBounds
	if drawnL < float32(disp.X) || drawnR > float32(disp.X+disp.Width) {
		t.Fatalf("drawn wings %.0f..%.0f escape the display %v", drawnL, drawnR, disp)
	}
	if drawnL < float32(fr.X) || drawnR > float32(fr.X+fr.Width) {
		t.Fatalf("frame %v clips the drawn wings %.0f..%.0f", fr, drawnL, drawnR)
	}
	onMain(func() {
		a.island, a.islandDX = false, 0
		a.endAppSwitch()
	})
}

// The wings open and fold under whatever motion setting the desktop has;
// a reduced-motion re-run of the gate only changes the easing. (The real
// window's ReduceMotion flag is the OS's — the headless gallery covers
// the preference's drawing paths; this proves state correctness either
// way.)
func TestSmokeAppsReduceMotionState(t *testing.T) {
	a := appsSmokeShelf(t, smokeApps)
	openAppsWings(t, a)
	var on bool
	onMain(func() { on = a.apps.on })
	if !on {
		t.Fatal("wings did not open")
	}
	shotAppsWindow(t, a.win, "open-settled")
	closeAppsWings(t, a)
}
