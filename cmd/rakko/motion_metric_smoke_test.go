//go:build darwin

package main

// Motion metrics for the shell's animations, collected from the real
// window: every frame the overlay built (its canvas size and the native
// frame at that moment), the springs' drawn values and the content's
// opacity. Run with RAKKO_BASKET_SMOKE=1 through the native gate; CSVs land
// in $MOTION_METRIC_DIR (default /tmp/Rakko-anim-fix/metrics).
//
// MOTION_METRIC_COLLECT=1 only records (the pre-fix code points); without
// it the test also holds the acceptance checks of the motion work.

import (
	"encoding/csv"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

type motionFrame struct {
	rep                  int
	phase                string
	ms                   float64
	canvasW, canvasH     float32
	winX, winY           int
	winW, winH           int
	w, h                 float32
	openK, levelK, peekK float32
	revealK              float32
	homeW, resultsH      float32
	navK, tabK           float32
	openMoving           bool
	sizeMoving           bool
	progress, peekProg   float32
	visible              bool
}

type motionMeter struct {
	a     *shelf
	mu    sync.Mutex
	rep   int
	phase string
	start time.Time
	rows  []motionFrame
}

// frame is the view seam's callback; it runs on the main thread at the end
// of every built frame.
func (m *motionMeter) frame(canvasW, canvasH float32, native mygo.Rectangle) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	fx := m.a.fx
	m.rows = append(m.rows, motionFrame{
		rep: m.rep, phase: m.phase, ms: float64(now.Sub(m.start)) / float64(time.Millisecond),
		canvasW: canvasW, canvasH: canvasH,
		winX: native.X, winY: native.Y, winW: native.Width, winH: native.Height,
		w: fx.w, h: fx.h,
		openK: fx.openK, levelK: fx.levelK, peekK: fx.peekK,
		revealK: fx.revealK, homeW: fx.homeW, resultsH: fx.resultsH, navK: fx.navK,
		tabK:       m.a.tabPhase(now),
		openMoving: fx.openMoving, sizeMoving: fx.sizeMoving,
		progress: m.a.progress, peekProg: m.a.peekProgress, visible: m.a.visible,
	})
}

// nextRep marks the frames after this call as a new repetition.
func (m *motionMeter) nextRep() {
	m.mu.Lock()
	m.rep++
	m.mu.Unlock()
}

// mark starts a phase (open, close, a tab switch) whose frames are
// measured on their own.
func (m *motionMeter) mark(phase string) {
	m.mu.Lock()
	m.phase = phase
	m.mu.Unlock()
}

func (m *motionMeter) take() []motionFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.rows
	m.rows = nil
	return out
}

func saveMotionFrames(t *testing.T, dir, name string, rows []motionFrame) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".csv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := csv.NewWriter(f)
	head := []string{"rep", "phase", "ms", "canvas_w", "canvas_h", "win_x", "win_y", "win_w", "win_h",
		"shell_w", "shell_h", "open_k", "level_k", "peek_k", "reveal_k", "home_w", "results_h", "nav_k", "tab_k",
		"open_moving", "size_moving", "progress", "peek_progress", "visible"}
	if err := out.Write(head); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		row := []string{
			strconv.Itoa(r.rep), r.phase, fmt.Sprintf("%.3f", r.ms),
			fmt.Sprintf("%.2f", r.canvasW), fmt.Sprintf("%.2f", r.canvasH),
			strconv.Itoa(r.winX), strconv.Itoa(r.winY), strconv.Itoa(r.winW), strconv.Itoa(r.winH),
			fmt.Sprintf("%.2f", r.w), fmt.Sprintf("%.2f", r.h),
			fmt.Sprintf("%.5f", r.openK), fmt.Sprintf("%.5f", r.levelK), fmt.Sprintf("%.5f", r.peekK),
			fmt.Sprintf("%.5f", r.revealK), fmt.Sprintf("%.2f", r.homeW), fmt.Sprintf("%.2f", r.resultsH),
			fmt.Sprintf("%.5f", r.navK), fmt.Sprintf("%.5f", r.tabK), fmt.Sprint(r.openMoving), fmt.Sprint(r.sizeMoving),
			fmt.Sprintf("%.5f", r.progress), fmt.Sprintf("%.5f", r.peekProg), fmt.Sprint(r.visible),
		}
		if err := out.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	out.Flush()
	if err := out.Error(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d frames -> %s", name, len(rows), path)
	return path
}

// startMotionTicker runs the 30 ms tick production's monitoring runs: the
// timer-driven progress and the native frame sync that follows the springs.
func startMotionTicker(a *shelf) func() {
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
				onMain(func() {
					a.animate(now)
					a.syncWindowFrame()
				})
			}
		}
	}()
	return func() { close(stop); <-stopped }
}

func openSheetNow(a *shelf) {
	onMain(func() {
		a.show(false)
		a.setLevel(2)
		a.jumpLevel = true
	})
}

func hideNow(a *shelf) {
	onMain(func() { a.hide(hidePassive) })
}

func settle(a *shelf, d time.Duration) { time.Sleep(d) }

// setMotionStyle switches the shell style the way 设置 does, without
// saving it, and re-places on the display the shelf lives on.
func setMotionStyle(a *shelf, island bool) {
	onMain(func() {
		style := settings.ShellStyleNotch
		if island {
			style = settings.ShellStyleIsland
		}
		a.settings.ShellStyle = style
		a.applyShellStyle()
		a.place(mygo.Screen.CursorScreenPoint())
		a.win.Invalidate()
	})
}

func resetHomeQuery(a *shelf) {
	onMain(func() {
		a.homeQuery = ""
		a.homeSpot.items, a.homeSpot.done, a.homeSpot.query = nil, false, ""
		a.homeSel = 0
		a.invalidateHome()
	})
}

// startHomeQuery shows the bar's typed state without asking the real index:
// searching=true keeps the results sheet shut while the bar widens.
func startHomeQuery(a *shelf, q string, done bool, hits []mac.SpotlightHit) {
	onMain(func() {
		a.homeQuery = q
		a.homeSpot.items = hits
		a.homeSpot.done = done
		a.homeSpot.query = q
		a.homeSpot.scope = a.homeScope
		a.homeSel = 0
		a.invalidateHome()
	})
}

func fakeHits(n int) []mac.SpotlightHit {
	hits := make([]mac.SpotlightHit, 0, n)
	for i := range n {
		hits = append(hits, mac.SpotlightHit{
			Path: fmt.Sprintf("/tmp/Rakko-anim-fix/hits/file-%02d.txt", i),
			Name: fmt.Sprintf("file-%02d", i),
		})
	}
	return hits
}

// frameDelta reports whether the frame moved any spring the shell, the
// search bar, the results sheet or the content follows.
func frameDelta(prev, cur motionFrame) bool {
	const eps = 1e-4
	d := func(a, b float32) bool {
		return math.Abs(float64(a-b)) > eps*float64(max(1, float32(math.Abs(float64(b)))))
	}
	return d(prev.openK, cur.openK) || d(prev.levelK, cur.levelK) || d(prev.peekK, cur.peekK) ||
		d(prev.homeW, cur.homeW) || d(prev.resultsH, cur.resultsH) || d(prev.navK, cur.navK) ||
		d(prev.revealK, cur.revealK) || d(prev.tabK, cur.tabK) || d(prev.w, cur.w) || d(prev.h, cur.h) ||
		cur.openMoving || cur.sizeMoving
}

// motionStats summarises one scenario across its repetitions: frame pacing
// while a spring moved, native resizes that happened while it moved, and
// frames whose native size did not match the canvas they were built in.
type motionStats struct {
	name        string
	reps        int
	frames      int
	motion      int
	p50, p95    float64
	maxGap      float64
	drops       int
	midResizes  int // between two frames with the spring moving on both sides
	edgeResizes int // at a transition's first or last frame
	staleHits   int // frame pairs whose native size differs from the canvas
	reverse     int
	revealJump  float64
	windowAuth  []string
}

func metricsFrom(name string, rows []motionFrame) motionStats {
	st := motionStats{name: name}
	reps := map[int]map[string][]motionFrame{}
	for _, r := range rows {
		if reps[r.rep] == nil {
			reps[r.rep] = map[string][]motionFrame{}
		}
		reps[r.rep][r.phase] = append(reps[r.rep][r.phase], r)
	}
	st.reps = len(reps)
	var gaps []float64
	for _, phases := range reps {
		var all []motionFrame
		for _, fs := range phases {
			all = append(all, fs...)
		}
		// The content's opacity is a continuity check across the phase
		// edges too (open cut short by close); the springs within a phase
		// are measured on their own.
		sortFrames(all)
		for i := 1; i < len(all); i++ {
			a, b := all[i-1], all[i]
			if b.ms-a.ms < 1.5 {
				continue // a rebuild pass of the same frame
			}
			if j := math.Abs(float64(b.revealK - a.revealK)); j > st.revealJump {
				st.revealJump = j
			}
		}
		for phase, fs := range phases {
			if phase == "" {
				continue // setup and settle frames
			}
			// A frame may build twice (a consumed action rebuilds the
			// view); passes of one frame land within a millisecond.
			frames := dedupePasses(fs)
			st.frames += len(frames)
			if len(frames) < 2 {
				continue
			}
			moving := make([]bool, len(frames)-1)
			for i := 1; i < len(frames); i++ {
				moving[i-1] = frameDelta(frames[i-1], frames[i])
			}
			for i := 1; i < len(frames); i++ {
				a, b := frames[i-1], frames[i]
				if moving[i-1] {
					st.motion++
					gap := b.ms - a.ms
					gaps = append(gaps, gap)
					if gap > 2*motionFrameMS {
						st.drops++
					}
					if b.h < a.h-1 && (b.openK > a.openK || b.levelK > a.levelK || b.peekK > a.peekK) {
						st.reverse++
					}
				}
				if b.winW == a.winW && b.winH == a.winH {
					continue
				}
				// The resize that takes the envelope (rest → first moving
				// frame) and the one that drops it (last moving → rest) are
				// edges; only a resize with the spring moving on both sides
				// is a mid-flight resize.
				prevMoving := i >= 2 && moving[i-2]
				nextMoving := i+1 < len(frames) && frameDelta(frames[i], frames[i+1])
				if prevMoving && nextMoving {
					st.midResizes++
				} else {
					st.edgeResizes++
				}
			}
		}
		for _, fs := range phases {
			for _, b := range fs {
				if b.winW != int(math.Round(float64(b.canvasW))) || b.winH != int(math.Round(float64(b.canvasH))) {
					st.staleHits++
					if len(st.windowAuth) < 8 {
						st.windowAuth = append(st.windowAuth, fmt.Sprintf("%.1fms win %dx%d canvas %.1fx%.1f", b.ms, b.winW, b.winH, b.canvasW, b.canvasH))
					}
				}
			}
		}
	}
	if len(gaps) > 0 {
		sort.Float64s(gaps)
		at := func(p float64) float64 {
			i := int(p * float64(len(gaps)-1))
			return gaps[i]
		}
		st.p50, st.p95, st.maxGap = at(0.5), at(0.95), gaps[len(gaps)-1]
	}
	return st
}

func sortFrames(fs []motionFrame) {
	sort.Slice(fs, func(i, j int) bool { return fs[i].ms < fs[j].ms })
}

// dedupePasses keeps one row per built frame: a second pass of one frame
// lands within a millisecond of the first.
func dedupePasses(fs []motionFrame) []motionFrame {
	sortFrames(fs)
	var frames []motionFrame
	for _, r := range fs {
		if n := len(frames); n > 0 && r.ms-frames[n-1].ms < 1.5 {
			frames[n-1] = r
			continue
		}
		frames = append(frames, r)
	}
	return frames
}

const (
	motionFrameMS = 1000.0 / 60.0 // the display's refresh: 60 Hz here
	motionMetric  = 1.5 * motionFrameMS
)

type motionScenario struct {
	name string
	run  func(rep int)
}

// motionScenarios drives the shell the way the user does: opening and
// closing, a close in the middle of an opening, a hover morph, three page
// switches, a widening search bar and a growing results sheet.
func motionScenarios(a *shelf, m *motionMeter) []motionScenario {
	return []motionScenario{
		{"open-close", func(rep int) {
			m.nextRep()
			m.mark("")
			motionClosed(a)
			settle(a, 220*time.Millisecond)
			m.mark("open")
			openSheetNow(a)
			settle(a, 700*time.Millisecond)
			m.mark("close")
			hideNow(a)
			settle(a, 700*time.Millisecond)
		}},
		{"mid-open-close", func(rep int) {
			m.nextRep()
			m.mark("")
			motionClosed(a)
			settle(a, 220*time.Millisecond)
			m.mark("open")
			openSheetNow(a)
			settle(a, 120*time.Millisecond)
			m.mark("close")
			hideNow(a)
			settle(a, 700*time.Millisecond)
		}},
		{"peek-morph", func(rep int) {
			m.nextRep()
			m.mark("")
			motionClosed(a)
			settle(a, 220*time.Millisecond)
			onMain(func() { fakeTrack(a, true) })
			m.mark("peek")
			onMain(func() { a.setPeek(true) })
			settle(a, 420*time.Millisecond)
			m.mark("retract")
			onMain(func() { a.setPeek(false); a.np = nil })
			settle(a, 420*time.Millisecond)
		}},
		{"page-switch", func(rep int) {
			m.nextRep()
			m.mark("")
			motionClosed(a)
			settle(a, 220*time.Millisecond)
			openSheetNow(a)
			settle(a, 800*time.Millisecond)
			for i, id := range []pageID{pageClipboard, pageHome, pageTray} {
				id := id
				m.mark(fmt.Sprintf("tab-%d", i+1))
				onMain(func() { a.setTab(id) })
				settle(a, 340*time.Millisecond)
			}
			m.mark("close")
			hideNow(a)
			settle(a, 500*time.Millisecond)
		}},
		{"search-widen", func(rep int) {
			m.nextRep()
			m.mark("")
			motionClosed(a)
			settle(a, 220*time.Millisecond)
			onMain(func() { a.show(false) })
			settle(a, 700*time.Millisecond)
			resetHomeQuery(a)
			settle(a, 120*time.Millisecond)
			m.mark("widen")
			startHomeQuery(a, "rakko", false, nil)
			settle(a, 520*time.Millisecond)
			m.mark("close")
			hideNow(a)
			settle(a, 500*time.Millisecond)
		}},
		{"results-grow", func(rep int) {
			m.nextRep()
			m.mark("")
			motionClosed(a)
			settle(a, 220*time.Millisecond)
			onMain(func() { a.show(false) })
			settle(a, 700*time.Millisecond)
			resetHomeQuery(a)
			startHomeQuery(a, "rakko", false, nil)
			settle(a, 420*time.Millisecond)
			m.mark("grow")
			startHomeQuery(a, "rakko", true, fakeHits(9))
			settle(a, 520*time.Millisecond)
			m.mark("close")
			hideNow(a)
			settle(a, 500*time.Millisecond)
		}},
	}
}

// TestMotionMetricSmoke records the animations' frames and checks the
// motion acceptance: no native resize under a moving spring, no frame built
// in a canvas the window did not have, frame intervals inside 1.5 display
// refreshes and content opacity that moves frame by frame.
func TestMotionMetricSmoke(t *testing.T) {
	a := redesignShelf(t)
	dir := os.Getenv("MOTION_METRIC_DIR")
	if dir == "" {
		dir = "/tmp/Rakko-anim-fix/metrics"
	}
	// Frame logs carry microseconds so their pacing can be read beside
	// the traces.
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	collectOnly := os.Getenv("MOTION_METRIC_COLLECT") == "1"
	m := &motionMeter{a: a, start: time.Now()}
	onMain(func() { frameTrace = m.frame })
	defer onMain(func() { frameTrace = nil })
	stop := startMotionTicker(a)
	defer stop()
	motionClosed(a)
	settle(a, 500*time.Millisecond)

	// The shell's two styles animate the same scenes: the notch hangs the
	// panel from the screen's top edge, the island from its own row.
	styles := []struct {
		name   string
		island bool
	}{
		{"notch", false},
		{"island", true},
	}
	restoreStyle := func() { setMotionStyle(a, false) }
	t.Cleanup(restoreStyle)

	var stats []motionStats
	for _, style := range styles {
		setMotionStyle(a, style.island)
		motionClosed(a)
		// First openings pay the GPU's pipeline setup (a 300 ms first frame
		// in MYGO_FRAME_STATS): warm the surfaces up before a measured scene.
		for range 3 {
			motionClosed(a)
			settle(a, 200*time.Millisecond)
			openSheetNow(a)
			settle(a, 600*time.Millisecond)
			hideNow(a)
			settle(a, 600*time.Millisecond)
		}
		m.take()

		const reps = 10

		scenarios := motionScenarios(a, m)
		for _, sc := range scenarios {
			m.take() // the settled frames before the scene are not its data
			for rep := range reps {
				sc.run(rep)
			}
			rows := m.take()
			name := style.name + "-" + sc.name
			saveMotionFrames(t, dir, name, rows)
			stats = append(stats, metricsFrom(name, rows))
		}
	}
	restoreStyle()

	for _, st := range stats {
		t.Logf("%-15s reps=%d frames=%d motion=%d gap p50=%.2f p95=%.2f max=%.2f drops=%d mid-resizes=%d edge-resizes=%d canvas-mismatch=%d reverse=%d reveal-jump=%.3f",
			st.name, st.reps, st.frames, st.motion, st.p50, st.p95, st.maxGap, st.drops, st.midResizes, st.edgeResizes, st.staleHits, st.reverse, st.revealJump)
		for _, line := range st.windowAuth {
			t.Logf("  mismatch: %s", line)
		}
		if collectOnly {
			continue
		}
		if st.staleHits != 0 {
			t.Errorf("%s: %d frames were built in a canvas the native frame did not have", st.name, st.staleHits)
		}
		if st.midResizes != 0 {
			t.Errorf("%s: %d native resizes happened while a spring ran", st.name, st.midResizes)
		}
		if st.reverse != 0 {
			t.Errorf("%s: %d frames shrank the shell while its spring grew", st.name, st.reverse)
		}
		if st.p95 > motionMetric {
			t.Errorf("%s: frame interval p95 %.2fms is over %.2fms", st.name, st.p95, motionMetric)
		}
		if st.revealJump > 0.5 {
			t.Errorf("%s: content opacity jumped %.3f in one frame", st.name, st.revealJump)
		}
	}
}
