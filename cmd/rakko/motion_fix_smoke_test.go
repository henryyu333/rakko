//go:build darwin

package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
)

const fixShotDir = "/tmp/shelf-fix"

type motionSample struct {
	ms                  float64
	w, h, openK, peekK  float32
	window, desired     mygo.Rectangle
	ignoring, nativeHit bool
}

// sampleMotion reads the last rendered shell and the actual native frame.
// The 8ms cadence catches display frames without driving the spring clock.
func sampleMotion(a *shelf, span time.Duration) []motionSample {
	start := time.Now()
	var samples []motionSample
	for time.Since(start) < span {
		var row motionSample
		onMain(func() {
			row = motionSample{
				ms: float64(time.Since(start)) / float64(time.Millisecond),
				w:  a.fx.w, h: a.fx.h, openK: a.fx.openK, peekK: a.fx.peekK,
				window: a.win.Bounds(), desired: a.desiredFrame(), ignoring: a.ignoringMouse,
			}
			num := mac.WindowNumber(a.winH)
			screenH := float64(a.dispBounds.Y + a.dispBounds.Height)
			row.nativeHit = mac.WindowAtPoint(float64(a.stripCenter()), float64(a.y+a.notchH+4), screenH) == num
		})
		samples = append(samples, row)
		time.Sleep(8 * time.Millisecond)
	}
	return samples
}

func saveMotion(t *testing.T, name string, samples []motionSample) {
	t.Helper()
	if err := os.MkdirAll(fixShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixShotDir, name+".csv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := csv.NewWriter(f)
	if err := out.Write([]string{"ms", "shell_w", "shell_h", "open_k", "peek_k", "window_x", "window_y", "window_w", "window_h", "desired_w", "desired_h", "ignoring_mouse", "native_hit", "ear_margin"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		row := []string{
			fmt.Sprintf("%.3f", s.ms), fmt.Sprintf("%.3f", s.w), fmt.Sprintf("%.3f", s.h),
			fmt.Sprintf("%.5f", s.openK), fmt.Sprintf("%.5f", s.peekK),
			fmt.Sprint(s.window.X), fmt.Sprint(s.window.Y), fmt.Sprint(s.window.Width), fmt.Sprint(s.window.Height),
			fmt.Sprint(s.desired.Width), fmt.Sprint(s.desired.Height), fmt.Sprint(s.ignoring), fmt.Sprint(s.nativeHit),
			fmt.Sprintf("%.3f", (float64(s.window.Width)-float64(s.w))/2-earR),
		}
		if err := out.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	out.Flush()
	if err := out.Error(); err != nil {
		t.Fatal(err)
	}
	t.Log(path)
}

func checkMotion(t *testing.T, name string, samples []motionSample, closing bool, baseH float32) {
	t.Helper()
	minEar, maxGrowth := math.Inf(1), float32(0)
	firstFold, firstPass := -1.0, -1.0
	for i, s := range samples {
		margin := (float64(s.window.Width)-float64(s.w))/2 - earR
		minEar = min(minEar, margin)
		if margin < 0 || float32(s.window.Height) < s.h {
			t.Errorf("%s at %.1fms: actual frame %+v clips shell %.2fx%.2f including its ears", name, s.ms, s.window, s.w, s.h)
			break
		}
		if closing && i > 0 {
			maxGrowth = max(maxGrowth, s.w-samples[i-1].w, s.h-samples[i-1].h)
		}
		if s.h <= baseH+2 && firstFold < 0 {
			firstFold = s.ms
		}
		if s.ignoring && !s.nativeHit && firstPass < 0 {
			firstPass = s.ms
		}
	}
	if closing {
		if maxGrowth > 2 {
			t.Errorf("%s grew %.2fpt after close", name, maxGrowth)
		}
		last := samples[len(samples)-1]
		if !last.ignoring || last.nativeHit {
			t.Errorf("%s: the collapsed native window still receives clicks", name)
		}
		if firstFold < 0 || firstPass < 0 || firstPass-firstFold > 25 {
			t.Errorf("%s: folded at %.1fms, native pass-through at %.1fms", name, firstFold, firstPass)
		}
	}
	t.Logf("%s: ear margin min %.2fpt, outward growth max %.2fpt, folded %.1fms, pass-through %.1fms", name, minEar, maxGrowth, firstFold, firstPass)
}

func TestFixNativeMotionTraces(t *testing.T) {
	a := redesignShelf(t)
	motionClosed(a)
	time.Sleep(600 * time.Millisecond)
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(pointerInterval)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-tick.C:
				onMain(func() { a.animate(now) })
			}
		}
	}()
	defer func() { close(stop); <-stopped; motionClosed(a) }()
	baseH := float32(max(32, a.notchH))
	openSheet := func() {
		onMain(func() {
			a.show(false)
			a.setLevel(2)
			a.jumpLevel = true
		})
	}
	openSheet()
	open := sampleMotion(a, 900*time.Millisecond)
	saveMotion(t, "trace-open", open)
	checkMotion(t, "open", open, false, baseH)
	var peak float32
	for _, s := range open {
		peak = max(peak, s.h)
	}
	if peak <= open[len(open)-1].h+1 {
		t.Errorf("the native opening never overshot: peak %.2f, settled %.2f", peak, open[len(open)-1].h)
	}
	onMain(func() { a.hide(hidePassive) })
	closed := sampleMotion(a, 800*time.Millisecond)
	saveMotion(t, "trace-close", closed)
	checkMotion(t, "close", closed, true, baseH)
	openSheet()
	time.Sleep(100 * time.Millisecond)
	onMain(func() { a.hide(hidePassive) })
	interrupted := sampleMotion(a, 800*time.Millisecond)
	saveMotion(t, "trace-mid-open-close", interrupted)
	checkMotion(t, "mid-open-close", interrupted, true, baseH)
	onMain(func() { a.show(false) })
	time.Sleep(700 * time.Millisecond)
	onMain(func() { a.hide(hidePassive) })
	time.Sleep(100 * time.Millisecond)
	openSheet()
	reopened := sampleMotion(a, 900*time.Millisecond)
	saveMotion(t, "trace-mid-close-open", reopened)
	checkMotion(t, "mid-close-open", reopened, false, baseH)
	if reopened[len(reopened)-1].openK < 0.99 {
		t.Error("the interrupted close never reopened")
	}
}

func fixCapsuleShot(t *testing.T, a *shelf, name string) {
	t.Helper()
	time.Sleep(650 * time.Millisecond)
	var num int
	onMain(func() {
		a.syncWindowFrame()
		num = mac.WindowNumber(a.winH)
		if !a.fb.drawn || (!a.fb.live && !a.fb.armed) || a.fb.retracting {
			t.Errorf("%s: capsule content did not draw (drawn=%v armed=%v retracting=%v)", name, a.fb.drawn, a.fb.armed, a.fb.retracting)
		}
	})
	if err := os.MkdirAll(fixShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixShotDir, name+".png")
	if out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput(); err != nil {
		t.Fatalf("capture %s: %v: %s", name, err, out)
	}
	img, err := pngDecode(path)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 200 || img.Bounds().Dy() < 40 {
		t.Fatalf("%s: invalid capture size %v", name, img.Bounds())
	}
	t.Log(path)
}

func TestFixNativeCapsuleScreenshots(t *testing.T) {
	a := redesignShelf(t)
	reset := func() {
		onMain(func() {
			a.conv = nil
			a.np = nil
			a.dragging, a.ownDrag = false, false
		})
		motionClosed(a)
		time.Sleep(600 * time.Millisecond)
	}
	defer reset()
	for _, tc := range []struct {
		name, text string
		kind       statusKind
	}{
		{"capsule-success", "已复制 · 在其他应用按 ⌘V 粘贴", statusOK},
		{"capsule-info", "已取消 · 已完成 2 项", statusInfo},
		{"capsule-error", "转换失败：文件无法读取", statusErr},
	} {
		reset()
		onMain(func() { a.toastStatus(tc.text, time.Now().Add(6*time.Second), tc.kind) })
		fixCapsuleShot(t, a, tc.name)
	}
	reset()
	onMain(func() {
		a.conv = &converter{host: a, job: fixLiveJob()}
		a.win.Invalidate()
	})
	fixCapsuleShot(t, a, "capsule-running")
	reset()
	onMain(func() {
		fakeTrack(a, true)
		a.toastStatus("已转换 3 项 · 已选中，可直接拖出", time.Now().Add(6*time.Second), statusOK)
	})
	fixCapsuleShot(t, a, "capsule-success-with-track")
	reset()
	onMain(func() { a.toastStatus("第一条提示", time.Now().Add(6*time.Second), statusOK) })
	time.Sleep(650 * time.Millisecond)
	onMain(func() {
		a.fb.until = time.Now().Add(-time.Millisecond)
		a.win.Invalidate()
	})
	time.Sleep(90 * time.Millisecond)
	onMain(func() {
		if !a.fb.retracting {
			t.Error("the first capsule was not retracting when the second arrived")
		}
		a.toastStatus("第二条提示 · 内容已完成", time.Now().Add(6*time.Second), statusOK)
	})
	fixCapsuleShot(t, a, "capsule-reposted-mid-retract")
}

// TestFixNativeFoldTrace replays the user's cancelled-drag video: a track
// is loaded, the pointer is away from the notch, and the drop pill folds.
// Before the fix the shell grew toward the player and the player's text
// drew mid-fold; now every frame only shrinks — the wings have no text —
// and the strip sheds its click surface with it.
func TestFixNativeFoldTrace(t *testing.T) {
	a := redesignShelf(t)
	motionClosed(a)
	time.Sleep(600 * time.Millisecond)
	defer motionClosed(a)
	onMain(func() {
		fakeTrack(a, true)
		a.dragging = true
		a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
		a.win.Invalidate()
	})
	time.Sleep(500 * time.Millisecond)
	motionShot(t, a, "fold-drop-pill", 0)
	// The drag ends with the pointer away from the notch: the pill folds.
	onMain(func() {
		a.dragging = false
		a.peeking, a.peekAnimStart = false, time.Now()
		a.peekFrom = 1
		a.win.Invalidate()
	})
	fold := sampleMotion(a, 700*time.Millisecond)
	saveMotion(t, "trace-fold-drop", fold)
	baseH := float32(max(32, a.notchH))
	for i, s := range fold {
		if i == 0 {
			continue
		}
		prev := fold[i-1]
		if s.w > prev.w+0.5 || s.h > prev.h+0.5 {
			t.Fatalf("frame at %.1fms: folding shell grew %.1fx%.1f -> %.1fx%.1f", s.ms, prev.w, prev.h, s.w, s.h)
		}
		if float32(s.window.Height) < s.h {
			t.Fatalf("frame at %.1fms: window %+v cuts the folding shell %.1f", s.ms, s.window, s.h)
		}
	}
	if last := fold[len(fold)-1]; last.h > baseH+4 || last.w > float32(max(180, a.notchW))+4 {
		t.Fatalf("the fold ended at %.1fx%.1f, not back at the notch", last.w, last.h)
	}
	motionShot(t, a, "folded-notch", 300*time.Millisecond)
}

// TestSmokeOpenPanelKeepsOneFrame proves the open panel keeps one native
// frame from the moment it is drawn until it folds away: typing widens the
// bar, the results sheet grows and shrinks, and pages switch, and
// WindowServer must never be asked to resize the surface under the content.
// mygo presents a resized frame's last canvas stretched into the new frame,
// so every such resize is a one-frame flash; the strip's exact box returns
// only after the close.
func TestSmokeOpenPanelKeepsOneFrame(t *testing.T) {
	a := redesignShelf(t)
	motionClosed(a)
	time.Sleep(600 * time.Millisecond)
	stop := startMotionTicker(a)
	defer stop()
	t.Cleanup(func() { motionClosed(a) })
	onMain(func() { a.show(false) })
	if !waitMain(2*time.Second, func() bool {
		return a.progress >= 0.999 && !a.fx.openMoving
	}) {
		t.Fatal("the panel never settled open")
	}
	var opened, exact, hold mygo.Rectangle
	onMain(func() {
		a.syncWindowFrame()
		opened = a.win.Bounds()
		exact = a.desiredFrame()
		hold = a.frameHold
	})
	t.Logf("open panel: native frame %+v, held envelope %+v, exact box %+v", opened, hold, exact)
	if hold == (mygo.Rectangle{}) || opened != hold {
		t.Fatalf("the settled open panel's native frame %+v is not the held envelope %+v", opened, hold)
	}
	if opened == exact {
		t.Fatal("the settled open panel kept the exact box; the envelope was dropped")
	}
	step := func(name string, fn func()) {
		onMain(fn)
		// Long enough for every spring on the bar, the sheet and the page
		// to rest, and for the old code to have dropped the envelope.
		time.Sleep(700 * time.Millisecond)
		var got mygo.Rectangle
		onMain(func() { got = a.win.Bounds() })
		if got != opened {
			t.Fatalf("%s resized the open panel: %+v -> %+v", name, opened, got)
		}
	}
	step("typing", func() { startHomeQuery(a, "rakko", false, nil) })
	step("results", func() { startHomeQuery(a, "rakko", true, fakeHits(9)) })
	step("clear", func() { resetHomeQuery(a) })
	step("level 2", func() { a.setLevel(2) })
	step("clipboard tab", func() { a.setTab(pageClipboard) })
	step("home tab", func() { a.setTab(pageHome) })
	step("tray tab", func() { a.setTab(pageTray) })
	step("level 1", func() { a.setLevel(1) })
	var frames atomic.Int64
	onMain(func() {
		frameTrace = func(float32, float32, mygo.Rectangle) { frames.Add(1) }
	})
	onMain(func() { a.hide(hidePassive) })
	if !waitMain(4*time.Second, func() bool {
		return !a.visible && a.frameHold == (mygo.Rectangle{}) && a.closeAt.IsZero() && !a.fx.openMoving
	}) {
		var frame, desired, hold mygo.Rectangle
		var diag string
		onMain(func() {
			frame, desired, hold = a.win.Bounds(), a.desiredFrame(), a.frameHold
			diag = fmt.Sprintf("visible=%v closeAt=%v openMoving=%v sizeMoving=%v progress=%.4f openK=%.4f peekK=%.4f peekProgress=%.4f peeking=%v frames=%d",
				a.visible, a.closeAt, a.fx.openMoving, a.fx.sizeMoving, a.progress, a.fx.openK, a.fx.peekK, a.peekProgress, a.peeking, frames.Load())
		})
		t.Fatalf("the closed shelf never released its envelope: frame %+v hold %+v exact %+v (%s)", frame, hold, desired, diag)
	}
	time.Sleep(200 * time.Millisecond)
	var frame, desired mygo.Rectangle
	onMain(func() {
		a.syncWindowFrame()
		frame, desired = a.win.Bounds(), a.desiredFrame()
	})
	if frame != desired {
		t.Fatalf("the closed shelf's native frame %+v is not the strip's box %+v", frame, desired)
	}
}

// WindowServer, not a placeholder Window or desiredFrame arithmetic, must
// send presses in the settled panel's transparent margin to the windows
// underneath it: below the frame, and beside the shell inside the frame.
// The open panel now keeps its whole envelope for as long as it stays open,
// so the margin beside the shell is always part of its window; the shell
// accepts the mouse only under the pointer, so each probe parks the pointer
// on its own point before asking.
func TestSmokeSettledPanelBottomMarginPassesThrough(t *testing.T) {
	for _, tall := range []bool{false, true} {
		name := "tray"
		if tall {
			name = "tall page"
		}
		t.Run(name, func(t *testing.T) {
			a := redesignShelf(t)
			motionClosed(a)
			time.Sleep(600 * time.Millisecond)
			onMain(func() {
				a.dragging, a.pointerDrag, a.ownDrag = false, false, false
				if tall {
					a.registerPage(&fakePage{on: true})
					a.setTab("fake")
				}
				a.show(false)
				a.setLevel(2)
				a.jumpLevel = true
			})
			t.Cleanup(func() { motionClosed(a) })
			time.Sleep(850 * time.Millisecond)
			var below, side *mygo.Window
			var frame, exact mygo.Rectangle
			var belowPoint, leftPoint, rightPoint, topPoint mygo.Point
			var shelfNum, belowNum, sideNum, targetH, shellBottom, shellLeft, shellRight int
			var moving bool
			onMain(func() {
				if a.winH == 0 {
					t.Error("the check requires a native window handle")
					return
				}
				frame, targetH, moving = a.win.Bounds(), a.panelH(), a.fx.openMoving
				exact = a.desiredFrame()
				shellBottom = a.y + a.sheetTop() + targetH
				shellLeft = a.stripCenter() - int(math.Ceil(float64(a.fx.w)/2))
				shellRight = a.stripCenter() + int(math.Ceil(float64(a.fx.w)/2))
				midY := a.y + a.sheetTop() + targetH/2
				// The page capsule hangs under the shell, so the margin that
				// must pass clicks through is below the whole window, not
				// two points under the sheet.
				belowPoint = mygo.Point{X: a.stripCenter(), Y: frame.Y + frame.Height + 2}
				leftPoint = mygo.Point{X: frame.X + 10, Y: midY}
				rightPoint = mygo.Point{X: frame.X + frame.Width - 10, Y: midY}
				// The menu-bar row beside the shell: the window's top band
				// is transparent there (the shell starts under the band),
				// so an icon under it must still take the press.
				topPoint = mygo.Point{X: frame.X + 12, Y: 20}
				below = mygo.NewWindow(mygo.WindowOptions{
					Title: "ShelfU bottom-edge click receiver", Width: 40, Height: 40,
					Hidden: true, Frameless: true, DisableShadow: true, Panel: true, SkipTaskbar: true,
					Content: ui.View(func(c *ui.Context) { ui.Text(c, "Receiver") }),
				})
				mac.SetWindowFrame(below.NativeHandle(), belowPoint.X-20, belowPoint.Y-20, 40, 40)
				below.ShowInactive()
				mac.SetWindowLevel(below.NativeHandle(), mac.StatusWindowLevel-1)
				belowNum = mac.WindowNumber(below.NativeHandle())
				side = mygo.NewWindow(mygo.WindowOptions{
					Title: "ShelfU side-margin click receiver", Width: frame.Width - 16, Height: 30,
					Hidden: true, Frameless: true, DisableShadow: true, Panel: true, SkipTaskbar: true,
					Content: ui.View(func(c *ui.Context) { ui.Text(c, "Receiver") }),
				})
				mac.SetWindowFrame(side.NativeHandle(), frame.X+8, midY-15, frame.Width-16, 30)
				side.ShowInactive()
				mac.SetWindowLevel(side.NativeHandle(), mac.StatusWindowLevel-1)
				sideNum = mac.WindowNumber(side.NativeHandle())
				shelfNum = mac.WindowNumber(a.winH)
			})
			if below == nil || side == nil {
				t.Fatal("no native receiver")
			}
			t.Cleanup(func() {
				onMain(side.Close)
				onMain(below.Close)
			})
			if leftPoint.X >= shellLeft || rightPoint.X <= shellRight {
				t.Fatalf("the frame %+v leaves no side margin around the %.0fpt shell (left %d right %d)",
					frame, a.fx.w, shellLeft, shellRight)
			}
			oldCursor := cursorPoint
			t.Cleanup(func() { onMain(func() { cursorPoint = oldCursor }) })
			probe := func(name string, p mygo.Point, want int) {
				var hit int
				onMain(func() {
					cursorPoint = func() mygo.Point { return p }
					a.syncMousePassThrough()
				})
				time.Sleep(150 * time.Millisecond) // let WindowServer apply the flag
				onMain(func() {
					screenH := float64(a.dispBounds.Y + a.dispBounds.Height)
					hit = mac.WindowAtPoint(float64(p.X), float64(p.Y), screenH)
				})
				if hit != want {
					t.Errorf("%s at (%d,%d): target=%d want %d (ShelfU=%d below=%d side=%d frame=%+v)",
						name, p.X, p.Y, hit, want, shelfNum, belowNum, sideNum, frame)
				}
			}
			// Acceptance is pointer-gated per frame (overlayAcceptsMouse):
			// the shell takes a press only where the pointer is, so a probe
			// of the shell's own bottom row parks the pointer there. The
			// margin probes park it in the transparent part, where the old
			// exact frame did not exist at all.
			probe("shell bottom", mygo.Point{X: belowPoint.X, Y: shellBottom - 4}, shelfNum)
			probe("below frame", belowPoint, belowNum)
			probe("left margin", leftPoint, sideNum)
			probe("right margin", rightPoint, sideNum)
			// The menu-bar row belongs to the menu bar (or whichever status
			// item is there), not to a test window: the only thing this row
			// must prove is that the overlay lets the press through.
			var topHit int
			onMain(func() {
				cursorPoint = func() mygo.Point { return topPoint }
				a.syncMousePassThrough()
			})
			time.Sleep(150 * time.Millisecond)
			onMain(func() {
				screenH := float64(a.dispBounds.Y + a.dispBounds.Height)
				topHit = mac.WindowAtPoint(float64(topPoint.X), float64(topPoint.Y), screenH)
			})
			if topHit == shelfNum {
				t.Errorf("menu-bar margin at (%d,%d): the overlay took the press (window %d)", topPoint.X, topPoint.Y, topHit)
			}
			if moving || frame.Height < exact.Height || frame.Width < exact.Width {
				t.Errorf("settled native frame %+v does not cover the panel's box %+v (moving=%v)", frame, exact, moving)
			}
			t.Logf("native frame=%+v panelH=%d margins: below=(%d,%d) left=(%d,%d) right=(%d,%d) top=(%d,%d)",
				frame, targetH, belowPoint.X, belowPoint.Y, leftPoint.X, leftPoint.Y, rightPoint.X, rightPoint.Y, topPoint.X, topPoint.Y)
		})
	}
}

func TestSmokeEdgeOpenedDropZoneFollowsPointer(t *testing.T) {
	a := redesignShelf(t)
	motionClosed(a)
	time.Sleep(600 * time.Millisecond)
	path := writeFile(t, t.TempDir(), "incoming.png", testPNGBytes(t))
	var point mygo.Point
	oldFiles, oldCount, oldCursor := dragFiles, dragFilesChange, cursorPoint
	onMain(func() {
		dragFiles = func(*shelf) []string { return []string{path} }
		dragFilesChange = func(*shelf) int64 { return 123 }
		cursorPoint = func() mygo.Point { return point }
		point = mygo.Point{X: a.stripCenter(), Y: a.y + a.notchH + 8}
		a.dragging, a.pointerDrag, a.ownDrag = true, true, false
		a.dragSerial++
		a.show(true)
	})
	t.Cleanup(func() {
		onMain(func() {
			dragFiles, dragFilesChange, cursorPoint = oldFiles, oldCount, oldCursor
			a.dragging, a.pointerDrag = false, false
		})
		motionClosed(a)
	})
	time.Sleep(850 * time.Millisecond)
	var withZone, withoutZone int
	onMain(func() {
		if a.winH == 0 || !a.edgeOpened || !a.drop.on {
			t.Errorf("incoming drag did not show its native drop zone")
		}
		withZone = a.win.Bounds().Height
		point = mygo.Point{X: a.x + sheetW + 80, Y: a.y + a.panelH() + 40}
		a.win.Invalidate()
	})
	time.Sleep(80 * time.Millisecond)
	onMain(func() {
		if a.drop.on {
			t.Error("the drop zone stayed lit after the pointer left the edge-opened panel")
		}
	})
	time.Sleep(500 * time.Millisecond)
	onMain(func() {
		withoutZone = a.win.Bounds().Height
		settled := a.desiredFrame().Height
		if withoutZone != withZone || withZone != settled {
			t.Errorf("the drop zone changed the native panel: with=%d outside=%d settled=%d", withZone, withoutZone, settled)
		}
		point = mygo.Point{X: a.stripCenter(), Y: a.y + a.notchH + 8}
		a.win.Invalidate()
	})
	time.Sleep(500 * time.Millisecond)
	onMain(func() {
		if !a.drop.on || a.win.Bounds().Height != withZone {
			t.Errorf("returning to the panel did not restore the drop zone: on=%v frame=%+v", a.drop.on, a.win.Bounds())
		}
		t.Logf("edgeOpened=%v pointer exit: drop zone off; native height %d -> %d -> %d on reentry",
			a.edgeOpened, withZone, withoutZone, a.win.Bounds().Height)
	})
}
