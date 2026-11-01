//go:build darwin

package main

// 任务 C 的真实验证：用测试进程自己的普通窗口走一遍「拖到刘海 → 停在左半
// → 松手」，截面板、预览框和排好之后的窗口。与其余冒烟一样由
// SHELF_BASKET_SMOKE=1 打开，只在本测试自己的窗口区域里发送真实鼠标事件。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

const snapShotDir = "/tmp/shelf-snap"

func snapShot(t *testing.T, w *mygo.Window, name string) string {
	t.Helper()
	num := mac.WindowNumber(w.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	if err := os.MkdirAll(snapShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(snapShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, path)
	return path
}

// snapPostDrag posts one real mouse event at a screen point. The point must
// lie on a window this test owns: the notch overlay or the dragged window.
func snapPostDrag(t *testing.T, x, y float64, typ uint32) {
	t.Helper()
	// The drag guard pins events at the notch corridor to its lower row, so
	// the posted location is only a request; the springs work from the real
	// cursor.
	if err := postSnapEvent(x, y, typ); err != nil {
		t.Fatalf("posting mouse event %d at %.0f,%.0f: %v", typ, x, y, err)
	}
}

// snapWindowFrame reads the WindowServer frame of a native window.
func snapWindowFrame(t *testing.T, w *mygo.Window) mygo.Rectangle {
	t.Helper()
	var r mygo.Rectangle
	onMain(func() { r = w.Bounds() })
	return r
}

// snapTicker drives the snap tick the way the production monitor loop does
// (30 ms, main thread), so the panel's springs and native frame follow the
// drag. The smoke harness itself does not start that loop.
func snapTicker(t *testing.T, a *shelf) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(pointerInterval)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-tk.C:
				onMain(func() {
					if a.quitting {
						return
					}
					a.animate(now)
					p := mygo.Screen.CursorScreenPoint()
					active, _ := dragState()
					a.snapTick(now, p, active)
					a.syncWindowFrame()
				})
			}
		}
	}()
	t.Cleanup(func() { close(stop); <-done })
}

func TestSnapSmokeDragToNotch(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the real window drag")
	}
	if !mac.AccessibilityTrusted() {
		t.Skip("the test process needs Accessibility for the real window drag")
	}
	a := redesignShelf(t)
	onMain(func() {
		a.settings.ShellStyle = settings.ShellStyleNotch
		a.settings.SnapWindows = true
		a.place(mygo.Screen.CursorScreenPoint())
		a.visible, a.peeking = false, false
		a.progress, a.animationFrom = 0, 0
		a.snap = snapState{}
		// The production monitor arms the notch drag guard on every tick;
		// start the same active filter so the drag crosses the real
		// corridor clamp on its way to the tiles.
		a.syncDragGuard()
	})
	d := mygo.Screen.PrimaryDisplay()
	work := d.WorkArea
	x, y := work.X+300, work.Y+300
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: "ShelfU snap smoke", Width: 520, Height: 330, X: x, Y: y,
		AlwaysOnTop: true, SkipTaskbar: true,
		Content: ui.View(func(c *ui.Context) {
			c.Root().Background(ui.RGB(246, 246, 247))
			ui.Text(c, "ShelfU 拖窗口分屏 · 冒烟窗口").FontSize(20)
		}),
	})
	t.Cleanup(func() {
		onMain(func() {
			a.endSnapSession(true)
			if a.visible {
				a.hide(hidePassive)
			}
			a.stopDragGuard()
			w.Close()
		})
	})
	onMain(func() { w.Show(); mac.ActivateSelf() })
	snapTicker(t, a)
	time.Sleep(500 * time.Millisecond)
	frame := snapWindowFrame(t, w)
	t.Logf("test window %+v; notch %d,%d %d×%d", frame, a.notchX, a.notchY, a.notchW, a.notchH)
	titleX, titleY := float64(frame.X+180), float64(frame.Y+14)

	var axW, axPid int
	onMain(func() {
		win, pid, ok := mac.AXWindowAt(titleX, titleY)
		if !ok {
			t.Fatalf("AX found no window at the title bar (%.0f,%.0f)", titleX, titleY)
		}
		axW, axPid = 1, pid
		f, _ := win.Frame()
		t.Logf("AX window frame=%+v frontmost=%d", f, mac.FrontmostPID())
		win.Release()
	})
	t.Logf("AX window under the title bar: ok=%d pid=%d (own pid %d), test window #%d", axW, axPid, mac.OwnPID(), mac.WindowNumber(w.NativeHandle()))
	onMain(func() {
		d := mygo.Screen.PrimaryDisplay()
		hit := mac.WindowAtPoint(titleX, titleY, float64(d.Bounds.Y+d.Bounds.Height))
		t.Logf("WindowAtPoint(%.0f,%.0f)=%d", titleX, titleY, hit)
		notchHit := mac.WindowAtPoint(float64(a.stripCenter()), 10, float64(d.Bounds.Y+d.Bounds.Height))
		t.Logf("notch WindowAtPoint=%d, overlay #%d", notchHit, mac.WindowNumber(a.win.NativeHandle()))
	})

	const (
		moved   = 5
		down    = 1
		dragged = 6
		up      = 2
	)
	snapPostDrag(t, titleX, titleY, moved)
	snapPostDrag(t, titleX, titleY, down)
	onMain(func() { a.mouseDown(false, 0, mygo.Point{X: int(titleX), Y: int(titleY)}) })
	// Travel from the title bar to the notch, then to the 左半 tile.
	cx := float64(a.stripCenter())
	for _, step := range []struct{ x, y float64 }{
		{titleX - 40, titleY - 60},
		{titleX - 120, float64(work.Y + 120)},
		{cx, float64(work.Y + 100)},
		{cx, 20},
		{cx - 114, 63},
	} {
		snapPostDrag(t, step.x, step.y, dragged)
		time.Sleep(80 * time.Millisecond)
	}
	time.Sleep(700 * time.Millisecond)
	onMain(func() {
		t.Logf("session armed=%v dragging=%v panel=%v hot=%d visible=%v",
			a.snap.armed, a.snap.dragging, a.snap.panel, a.snap.hot, a.visible)
		if a.win != nil {
			t.Logf("panel frame %+v", a.win.Bounds())
		}
	})
	if a.visible && a.win != nil {
		snapShot(t, a.win, "snap-panel")
	}
	if a.snap.ghost != nil && a.snap.ghost.win != nil {
		snapShot(t, a.snap.ghost.win, "snap-ghost")
	}
	snapPostDrag(t, cx-114, 63, up)
	time.Sleep(700 * time.Millisecond)
	snapShot(t, w, "snap-arranged")
	got := snapWindowFrame(t, w)
	want := snapSlotRect(work, 0)
	t.Logf("arranged window %+v, want %+v", got, want)
	if absInt(got.X-want.X) > 4 || absInt(got.Width-want.Width) > 4 || absInt(got.Height-want.Height) > 4 {
		t.Fatalf("window not arranged: %+v, want %+v", got, want)
	}
}

// TestSnapSmokeIslandPanel shoots the arrangement panel under 设置 › 通用 ›
// 刘海样式 = 灵动岛: the shell must float below the housing, four round
// corners, with the trigger following the island's position.
func TestSnapSmokeIslandPanel(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the real window drag")
	}
	if !mac.AccessibilityTrusted() {
		t.Skip("the test process needs Accessibility for the real window drag")
	}
	a := redesignShelf(t)
	onMain(func() {
		a.settings.ShellStyle = settings.ShellStyleIsland
		a.settings.SnapWindows = true
		a.place(mygo.Screen.CursorScreenPoint())
		a.visible, a.peeking = false, false
		a.progress, a.animationFrom = 0, 0
		a.snap = snapState{}
		a.syncDragGuard()
	})
	t.Cleanup(func() {
		onMain(func() {
			a.endSnapSession(true)
			if a.visible {
				a.hide(hidePassive)
			}
			a.stopDragGuard()
			a.settings.ShellStyle = settings.ShellStyleNotch
			a.place(mygo.Screen.CursorScreenPoint())
		})
	})
	var island bool
	var islandTop int
	var anchor mygo.Rectangle
	onMain(func() {
		island, islandTop, anchor = a.island, a.islandTop, a.snapAnchor()
	})
	if !island {
		t.Fatalf("the island style did not take effect: island=%v top=%d", island, islandTop)
	}
	d := mygo.Screen.PrimaryDisplay()
	work := d.WorkArea
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: "ShelfU snap island smoke", Width: 520, Height: 330,
		X: work.X + 300, Y: work.Y + 300,
		AlwaysOnTop: true, SkipTaskbar: true,
		Content: ui.View(func(c *ui.Context) {
			c.Root().Background(ui.RGB(246, 246, 247))
			ui.Text(c, "ShelfU 拖窗口分屏 · 灵动岛冒烟").FontSize(20)
		}),
	})
	onMain(func() { w.Show(); mac.ActivateSelf() })
	t.Cleanup(func() { onMain(func() { w.Close() }) })
	snapTicker(t, a)
	time.Sleep(400 * time.Millisecond)
	frame := snapWindowFrame(t, w)
	titleX, titleY := float64(frame.X+180), float64(frame.Y+14)
	const (
		moved   = 5
		down    = 1
		dragged = 6
		up      = 2
	)
	snapPostDrag(t, titleX, titleY, moved)
	snapPostDrag(t, titleX, titleY, down)
	onMain(func() { a.mouseDown(false, 0, mygo.Point{X: int(titleX), Y: int(titleY)}) })
	cx := float64(a.stripCenter())
	tileY := float64(anchor.Y + snapGridTop + snapTileH/2)
	for _, step := range []struct{ x, y float64 }{
		{titleX - 40, titleY - 60},
		{titleX - 120, float64(work.Y + 120)},
		{cx, float64(work.Y + 100)},
		{cx, float64(anchor.Y + 10)},
		{cx - 114, tileY},
	} {
		snapPostDrag(t, step.x, step.y, dragged)
		time.Sleep(80 * time.Millisecond)
	}
	time.Sleep(700 * time.Millisecond)
	onMain(func() {
		t.Logf("island=%v top=%d anchor=%+v session armed=%v dragging=%v panel=%v hot=%d",
			a.island, a.islandTop, a.snapAnchor(), a.snap.armed, a.snap.dragging, a.snap.panel, a.snap.hot)
		if a.win != nil {
			t.Logf("island panel frame %+v", a.win.Bounds())
		}
	})
	if !a.visible {
		t.Fatal("the arrangement panel did not open from the island")
	}
	snapShot(t, a.win, "snap-panel-island")
	snapPostDrag(t, cx-114, tileY, up)
	// The shelf verifies the arrangement for a moment after the release
	// (the window server may re-apply its own drag-time tile first): wait
	// for that verification to finish, not a fixed while.
	deadline := time.Now().Add(2 * time.Second)
	for {
		var pending bool
		onMain(func() { pending = a.snap.confirm != nil })
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the arrangement is still being verified")
		}
		time.Sleep(30 * time.Millisecond)
	}
	if got, want := snapWindowFrame(t, w), snapSlotRect(work, 0); absInt(got.X-want.X) > 4 ||
		absInt(got.Width-want.Width) > 4 || absInt(got.Height-want.Height) > 4 {
		t.Fatalf("island drag did not arrange the window: %+v, want %+v", got, want)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// postSnapEvent posts one mouse event through the HID tap (CoreGraphics).
func postSnapEvent(x, y float64, typ uint32) error {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		return err
	}
	var (
		create  func(source uintptr, typ uint32, p mac.CGPoint, button uint32) uintptr
		post    func(tap uint32, ev uintptr)
		release func(cf uintptr)
		warp    func(p mac.CGPoint) int32
	)
	purego.RegisterLibFunc(&create, lib, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		return err
	}
	purego.RegisterLibFunc(&release, libCF, "CFRelease")
	purego.RegisterLibFunc(&warp, lib, "CGWarpMouseCursorPosition")
	ev := create(0, typ, mac.CGPoint{X: x, Y: y}, 0)
	if ev == 0 {
		return fmt.Errorf("CGEventCreateMouseEvent returned nil")
	}
	post(0, ev)
	release(ev)
	warp(mac.CGPoint{X: x, Y: y})
	time.Sleep(30 * time.Millisecond)
	return nil
}
