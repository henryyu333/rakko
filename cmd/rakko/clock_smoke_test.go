//go:build darwin

package main

// Real-window evidence for the clock: the countdown wings, their paused
// state, the unanswered 时间到 card, 重复 and 停止, and the open panel's
// ringing chip, each on the notch and the island. It also records the
// shell's size on every built frame of the two shape changes and counts
// frames that are fully peeked while still too narrow for the content.
// Run with RAKKO_BASKET_SMOKE=1; shots land in /tmp/Rakko-clock-omp/smoke.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
)

// clockShotDir is where this run's pictures and frame CSVs go: the
// caller's RAKKO_CLOCK_SHOTS, or a temporary folder the log names.
func clockShotDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("RAKKO_CLOCK_SHOTS")
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "clock-shots")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// clockFrame is one built frame's shell while a clock shape is on screen.
type clockFrame struct {
	ms    float64
	w, h  float32
	peekK float32
	blank bool
}

func clockShot(t *testing.T, a *shelf, dir, name string) {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(120 * time.Millisecond)
	onMain(a.syncWindowFrame)
	time.Sleep(80 * time.Millisecond)
	path := filepath.Join(dir, name+".png")
	if out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput(); err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, path)
}

func TestClockSmokeStates(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	dir := clockShotDir(t)
	t.Logf("clock shots -> %s", dir)
	for _, style := range []struct {
		name   string
		island bool
	}{{"notch", false}, {"island", true}} {
		t.Run(style.name, func(t *testing.T) {
			a := liveSmokeShelf(t)
			setMotionStyle(a, style.island)
			onMain(func() {
				a.clock = newClockModule(a)
				a.clock.playSound = func() {}
				a.clock.stopSound = func() {}
				a.modules = []module{a.clock, timerChip{a.clock}, stopwatchChip{a.clock}}
			})

			var mu sync.Mutex
			var phase string
			phases := map[string][]clockFrame{}
			start := time.Now()
			onMain(func() {
				frameTrace = func(_ float32, _ float32, _ mygo.Rectangle, blank bool) {
					mu.Lock()
					defer mu.Unlock()
					if phase == "" {
						return
					}
					phases[phase] = append(phases[phase], clockFrame{
						ms: float64(time.Since(start)) / float64(time.Millisecond),
						w:  a.fx.w, h: a.fx.h, peekK: a.fx.peekK, blank: blank,
					})
				}
			})
			defer onMain(func() { frameTrace = nil })

			want := map[string]peekKind{}
			seen := map[string][]peekKind{}
			mark := func(name string, kind peekKind) {
				mu.Lock()
				phase = name
				phases[name] = nil
				seen[name] = nil
				want[name] = kind
				mu.Unlock()
			}
			onMain(func() {
				peekKindTrace = func(kind peekKind) {
					mu.Lock()
					if phase != "" {
						seen[phase] = append(seen[phase], kind)
					}
					mu.Unlock()
				}
			})
			defer onMain(func() { peekKindTrace = nil })
			shoot := func(name string) {
				time.Sleep(650 * time.Millisecond) // the grow-out spring
				clockShot(t, a, dir, style.name+"-"+name)
			}
			var ww, wh, aw, ah float32
			onMain(func() {
				ww, wh = a.timerWingsSize()
				aw, ah = a.timerAlarmSize()
			})

			motionClosed(a)
			settle(a, 300*time.Millisecond)
			onMain(func() { a.setPeek(true) })
			settle(a, 250*time.Millisecond)

			onMain(func() {
				mark("wings", peekTimerWings)
				a.clock.startTimer(time.Now(), 5*time.Minute)
			})
			shoot("01-wings")
			var dk peekKind
			var dw float32
			onMain(func() { dk, dw = a.fx.peekKind, a.fx.peekW })
			t.Logf("%s settled after wings: kind=%d peekW=%.0f", style.name, dk, dw)
			onMain(func() { a.clock.pauseTimer(time.Now()) })
			shoot("02-wings-paused")
			onMain(func() {
				a.clock.stopTimer(time.Now())
				a.setPeek(false)
			})
			settle(a, 500*time.Millisecond)
			clockShot(t, a, dir, style.name+"-03-canceled")

			motionClosed(a)
			settle(a, 500*time.Millisecond)
			onMain(func() {
				mark("alert", peekAlarm)
				a.clock.startTimer(time.Now().Add(-2*time.Second), 2*time.Second)
				a.clock.tick(time.Now())
			})
			shoot("04-alert")
			onMain(func() {
				mark("repeat", peekTimerWings)
				a.setPeek(true)
				a.clock.repeatTimer(time.Now())
			})
			shoot("05-repeat")
			onMain(func() {
				a.clock.stopTimer(time.Now())
				a.setPeek(false)
			})
			settle(a, 500*time.Millisecond)
			clockShot(t, a, dir, style.name+"-06-stopped")

			onMain(func() {
				mark("chip", peekChevron)
				a.show(false)
				a.setLevel(2)
				a.jumpLevel = true
				a.setTab(pageTools)
				a.clock.startTimer(time.Now().Add(-time.Second), time.Second)
				a.clock.tick(time.Now())
			})
			shoot("07-band-stop")
			onMain(func() {
				a.clock.startTimer(time.Now(), 5*time.Minute)
				a.clock.watchRun, a.clock.watchStart = true, time.Now().Add(-754*time.Second)
			})
			shoot("08-band-both")
			onMain(func() {
				a.clock.stopTimer(time.Now())
				a.clock.resetWatch()
				a.hide(hidePassive)
			})
			settle(a, 500*time.Millisecond)

			mu.Lock()
			defer mu.Unlock()
			for _, name := range []string{"wings", "alert"} {
				frames := phases[name]
				if len(frames) == 0 {
					t.Fatalf("%s: no frames recorded", name)
				}
				var maxW, maxH float32
				blanks := 0
				tw, th := ww, wh
				if name == "alert" {
					tw, th = aw, ah
				}
				for _, f := range frames {
					maxW, maxH = max(maxW, f.w), max(maxH, f.h)
					if f.blank {
						blanks++
					}
				}
				miss, kinds := 0, seen[name]
				for _, k := range kinds {
					if k != want[name] {
						miss++
					}
				}
				t.Logf("%s-%s: frames=%d max-shell=%.0fx%.0f target=%.0fx%.0f blank=%d content-missing=%d/%d kinds=%v",
					style.name, name, len(frames), maxW, maxH, tw, th, blanks, miss, len(kinds), kinds)
				if miss != 0 {
					t.Errorf("%s-%s: %d frames drew another shape instead of the clock", style.name, name, miss)
				}
				writeClockFrames(t, dir, style.name+"-"+name, frames)
			}
		})
	}
}

// writeClockFrames leaves the per-frame sizes beside the screenshots.
func writeClockFrames(t *testing.T, dir, name string, frames []clockFrame) {
	t.Helper()
	sort.Slice(frames, func(i, j int) bool { return frames[i].ms < frames[j].ms })
	var b []byte
	b = append(b, []byte("ms,w,h,peekK,blank\n")...)
	for _, f := range frames {
		b = append(b, []byte(fmt.Sprintf("%.1f,%.0f,%.0f,%.3f,%v\n", f.ms, f.w, f.h, f.peekK, f.blank))...)
	}
	if err := os.WriteFile(filepath.Join(dir, "frames-"+name+".csv"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
