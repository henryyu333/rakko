//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"

	"rakko/internal/mac"
)

// The smoke run pins real catalog cards: 隔空投送 floats, 转成 PDF docks,
// 缩小图片 takes the drag-snap. toolCardDefFor draws them as the tools
// page does (cardDesktop surface).

func pinSmokeEnv(t *testing.T) *shelf {
	t.Helper()
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	if err := os.MkdirAll("/tmp/shelf-review-pin", 0o755); err != nil {
		t.Fatal(err)
	}
	snapshotClipboard(t)
	area := mygo.Screen.PrimaryDisplay().WorkArea
	area.Y += 2
	area.Height -= 2
	newSmokePointerBackdrop(t, area)
	a := smokeShelf
	// The smoke harness never calls ready(), so these tests drive the
	// production monitorDrag entry point on its own 30 ms ticker,
	// including placement, the return invitation, and pinTick.
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		tk := time.NewTicker(pointerInterval)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-tk.C:
				mygo.RunOnMain(func() {
					p := cursorPoint()
					if !a.visible && !insideRect(p, a.dispBounds) {
						a.place(p)
					}
					// animate drives the peek/pull timelines exactly as
					// shelf.startMonitoring does before monitorDrag.
					a.animate(now)
					a.monitorDrag(now)
				})
			}
		}
	}()
	return a
}

// pinShot captures a real window by CGWindowID into the review evidence directory.
func pinShot(t *testing.T, w *mygo.Window, name string) {
	t.Helper()
	num := mac.WindowNumber(w.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	out := filepath.Join("/tmp/shelf-review-pin", name+".png")
	cmd := exec.Command("screencapture", "-l", fmt.Sprint(num), "-o", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screencapture %s: %v: %s", name, err, b)
	}
	t.Logf("saved %s (CGWindowID=%d)", out, num)
}

// pinShotFull captures the window including its native shadow (-o left
// out): the card keeps the design's drop shadow.
func pinShotFull(t *testing.T, w *mygo.Window, name string) {
	t.Helper()
	num := mac.WindowNumber(w.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	out := filepath.Join("/tmp/shelf-review-pin", name+".png")
	cmd := exec.Command("screencapture", "-l", fmt.Sprint(num), out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screencapture %s: %v: %s", name, err, b)
	}
	t.Logf("saved %s", out)
}

// pinShotRect captures a screen region (for the context menu, which lives
// its own window).
func pinShotRect(t *testing.T, r mygo.Rectangle, name string) {
	t.Helper()
	out := filepath.Join("/tmp/shelf-review-pin", name+".png")
	cmd := exec.Command("screencapture", "-x", "-o",
		"-R", fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height), out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screencapture %s: %v: %s", name, err, b)
	}
	t.Logf("saved %s", out)
}

// postMouse posts real mouse events at a screen point through the HID
// tap: the same path a physical mouse takes (see realkey_smoke_test.go).
// events are CGEvent types: 5 moved, 1 leftDown, 2 leftUp, 6 leftDragged,
// 3 rightDown, 4 rightUp.
func postMouse(t *testing.T, x, y float64, events ...uint32) {
	t.Helper()
	requireOwnMouseTarget(t, x, y)
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreGraphics: %v", err)
	}
	var (
		create  func(source uintptr, typ uint32, p mac.CGPoint, button uint32) uintptr
		post    func(tap uint32, ev uintptr)
		warp    func(p mac.CGPoint) int32
		release func(cf uintptr)
	)
	purego.RegisterLibFunc(&create, lib, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	purego.RegisterLibFunc(&warp, lib, "CGWarpMouseCursorPosition")
	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreFoundation: %v", err)
	}
	purego.RegisterLibFunc(&release, libCF, "CFRelease")
	button := uint32(0)
	for _, typ := range events {
		if typ == 3 || typ == 4 || typ == 27 {
			button = 1
		}
		ev := create(0, typ, mac.CGPoint{X: x, Y: y}, button)
		if ev == 0 {
			t.Fatal("CGEventCreateMouseEvent returned nil")
		}
		post(0, ev)
		release(ev)
		time.Sleep(30 * time.Millisecond)
	}
	warp(mac.CGPoint{X: x, Y: y})
}

// settlePin lets the 30 ms monitor tick apply a frame, then reports it.
func settlePin(t *testing.T, it *pinItem, d time.Duration) mygo.Rectangle {
	t.Helper()
	time.Sleep(d)
	var f mygo.Rectangle
	onMain(func() { f = it.frame })
	return f
}

// TestPinSmokeWindowsAndShots proves pinned items are real windows and
// captures the four states for the design comparison: floating, edge
// strip, edge-expanded and the file-drag highlight.
func TestPinSmokeWindowsAndShots(t *testing.T) {
	a := pinSmokeEnv(t)
	d := mygo.Screen.PrimaryDisplay()
	var free, docked *pinItem
	onMain(func() {
		free = a.addPin("airdrop", 1, d.ID, pinFree, false,
			mygo.Rectangle{X: d.WorkArea.X + d.WorkArea.Width - 360, Y: d.WorkArea.Y + 300, Width: pinCardW, Height: pinCardH})
		docked = a.addPin("topdf", 1, d.ID, pinRight, false,
			pinStripFrame(pinRight, d.WorkArea, d.WorkArea.Y+500))
	})
	defer onMain(func() {
		a.unpin(free, false)
		a.unpin(docked, false)
	})
	time.Sleep(700 * time.Millisecond)

	// Floating card, expanded.
	if level := mac.WindowLevel(free.win.NativeHandle()); level != 3 {
		t.Fatalf("pin window level = %d, want 3 (floating)", level)
	}
	pinShot(t, free.win, "pin-floating")
	// The design's drop shadow is the window's own: captured without -o.
	pinShotFull(t, free.win, "pin-floating-shadow")
	// Docked strip.
	pinShot(t, docked.win, "pin-strip")

	// Hover the strip: the monitor tick expands it.
	f := settlePin(t, docked, 0)
	postMouse(t, float64(f.X+4), float64(f.Y+pinStripH/2), 5)
	f = settlePin(t, docked, 500*time.Millisecond)
	if !docked.expanded || f.Width != pinCardW {
		t.Fatalf("strip did not expand on hover: expanded=%v frame=%+v", docked.expanded, f)
	}
	pinShot(t, docked.win, "pin-expanded")

	// Drop highlight: the accent frame a dragged file over the card shows.
	onMain(func() { docked.dropAt = time.Now().Add(30 * time.Second); docked.win.Invalidate() })
	time.Sleep(300 * time.Millisecond)
	pinShot(t, docked.win, "pin-hot")
	onMain(func() { docked.dropAt = time.Time{}; docked.win.Invalidate() })

	// Right-click menu: post a real secondary click on the card, capture
	// the region where the menu opens, then dismiss it.
	postMouse(t, float64(f.X+f.Width/2), float64(f.Y+f.Height/2), 3, 4)
	time.Sleep(600 * time.Millisecond)
	menuRect := mygo.Rectangle{X: f.X, Y: f.Y, Width: f.Width + 220, Height: f.Height + 160}
	pinShotRect(t, menuRect, "pin-menu")
	postMouse(t, float64(f.X-300), float64(f.Y+300), 1, 2) // click away: dismiss
	time.Sleep(300 * time.Millisecond)
}

// TestPinSmokeDragSnap moves a floating pin to the left edge with a real
// pointer drag and asserts it docks as a strip.
func TestPinSmokeDragSnap(t *testing.T) {
	a := pinSmokeEnv(t)
	d := mygo.Screen.PrimaryDisplay()
	var it *pinItem
	onMain(func() {
		it = a.addPin("resize", 1, d.ID, pinFree, false,
			mygo.Rectangle{X: d.WorkArea.X + 700, Y: d.WorkArea.Y + 400, Width: pinCardW, Height: pinCardH})
	})
	defer onMain(func() { a.unpin(it, false) })
	time.Sleep(700 * time.Millisecond)

	f := settlePin(t, it, 0)
	// Press the card, drag to within 24 pt of the left edge, release.
	x, y := float64(f.X+f.Width/2), float64(f.Y+f.Height/2)
	postMouse(t, x, y, 5, 1)
	for i := 1; i <= 10; i++ {
		postMouse(t, x-float64(i)*(x-float64(d.WorkArea.X+10))/10, y, 6)
		time.Sleep(40 * time.Millisecond)
	}
	postMouse(t, float64(d.WorkArea.X+10), y, 2)
	time.Sleep(700 * time.Millisecond)
	f = settlePin(t, it, 0)
	if it.edge != pinLeft || it.expanded {
		t.Fatalf("drag to the left edge did not dock: edge=%v expanded=%v frame=%+v", it.edge, it.expanded, f)
	}
	if f.Width != pinStripW || f.X != d.WorkArea.X {
		t.Fatalf("docked frame wrong: %+v", f)
	}
	pinShot(t, it.win, "pin-docked-left")
}

// TestPinSmokeReturnBulge drags a pin to the notch with a real pointer
// drag and holds it there: the shelf's peek shows the 松开放回 pill
// (design §4), the release puts the card back on the tools page.
func TestPinSmokeReturnBulge(t *testing.T) {
	a := pinSmokeEnv(t)
	d := mygo.Screen.PrimaryDisplay()
	var it *pinItem
	// The other pin shots photograph pin windows of their own; this one
	// also needs the shelf's notch window, so bring up the same idle
	// notch window ready(false) does — placed, shown, mouse-synced.
	// If this test created the shared window, it takes it down again:
	// a later smoke test expects the window it finds to still be hidden
	// (an already-shown panel ordered in by show() becomes the key
	// window, which the ⌘C global-key test asserts against).
	onMain(func() {
		if a.win == nil {
			a.newNotchWindow()
		}
		a.place(mygo.Screen.CursorScreenPoint())
		// Exercise the return zone below the real menu bar, over our
		// own backdrop rather than the user's installed ShelfU.
		a.y, a.notchY = d.WorkArea.Y+120, d.WorkArea.Y+120
		a.syncMousePassThrough()
		a.win.ShowInactive()
		it = a.addPin("tojpeg", 1, d.ID, pinFree, false,
			mygo.Rectangle{X: d.WorkArea.X + 700, Y: d.WorkArea.Y + 400, Width: pinCardW, Height: pinCardH})
	})
	t.Cleanup(func() {
		// Back to the idle shelf the next smoke test expects: peek folded,
		// nothing visible, and the notch window gone (a later show() must
		// order in a hidden window, or macOS makes an already-shown panel
		// the key window and the ⌘C global-key test fails).
		onMain(func() {
			a.pinReturning, a.peeking, a.visible = false, false, false
			a.progress, a.peekProgress, a.peekFrom = 0, 0, 0
			a.animationStart, a.peekAnimStart = time.Time{}, time.Time{}
			if a.win != nil {
				a.win.Hide()
				a.win.Destroy()
				a.win, a.winH = nil, 0
			}
			a.frame, a.fx = mygo.Rectangle{}, shellMotion{}
		})
	})
	defer onMain(func() {
		if len(a.pins) > 0 { // the release may already have unpinned it
			a.unpin(it, false)
		}
	})
	time.Sleep(700 * time.Millisecond)

	f := settlePin(t, it, 0)
	x, y := float64(f.X+f.Width/2), float64(f.Y+f.Height/2)
	var notchCx float64
	onMain(func() { notchCx = float64(a.notchX + a.notchW/2) })
	if a.notchW <= 0 {
		t.Skip("the test display has no measured notch")
	}
	postMouse(t, x, y, 5, 1)
	for i := 1; i <= 12; i++ {
		nx := x + (notchCx-x)*float64(i)/12
		ny := y + (float64(a.notchY+30)-y)*float64(i)/12
		postMouse(t, nx, ny, 6)
		time.Sleep(40 * time.Millisecond)
	}
	settled := false
	for range 60 {
		onMain(func() {
			w, h := a.peekSize()
			settled = a.pinReturning && a.peeking && a.fx.peekKind == peekReturn &&
				a.peekProgress > 0.99 && !a.fx.sizeMoving &&
				abs(int(a.fx.w-w)) <= 1 && abs(int(a.fx.h-h)) <= 1 &&
				float32(a.frame.Height) >= a.fx.h
		})
		if settled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !settled {
		t.Fatal("the 松开放回 pill did not settle inside its native frame")
	}
	r := mygo.Rectangle{}
	onMain(func() {
		// Another running ShelfU may own a same-level hover pill. Order
		// only this test window forward, without changing its level or
		// touching the installed app.
		a.win.ShowInactive()
		pr := a.peekPillRect()
		r = mygo.Rectangle{X: pr.X - 24, Y: pr.Y - 12, Width: pr.Width + 48, Height: pr.Height + 36}
	})
	time.Sleep(200 * time.Millisecond)
	pinShot(t, a.win, "pin-return")
	time.Sleep(200 * time.Millisecond)
	pinShotRect(t, r, "pin-return-screen")
	postMouse(t, notchCx, float64(a.notchY+30), 2)
	time.Sleep(500 * time.Millisecond)
	if len(a.pins) != 0 {
		t.Fatalf("release on the bulge kept the pin: %d pins", len(a.pins))
	}
}
