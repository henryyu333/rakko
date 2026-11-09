//go:build darwin

package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/settings"
	"rakko/internal/weekly"
)

// monitorShelf is a headless shelf with just enough geometry for the
// pointer zones. No timers, windows or modules run.
func monitorShelf() *shelf {
	a := &shelf{
		settings:   settings.Default(),
		dispBounds: mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
	}
	a.x, a.y = 455, 0
	a.notchX, a.notchY, a.notchW, a.notchH, a.hasNotch = 700, 0, 200, 32, true
	return a
}

type weeklyPageStub struct{ fakePage }

func (weeklyPageStub) id() pageID { return pageWeekly }

type usagePageStub struct{ fakePage }

func (usagePageStub) id() pageID { return pageUsage }

func TestMonitorActiveConditionsEnterCadence(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		set  func(*shelf)
	}{
		{"panel-visible", func(a *shelf) { a.visible = true }},
		{"panel-opening", func(a *shelf) { a.fx.openK = 0.5 }},
		{"panel-spring", func(a *shelf) { a.animationStart = now.Add(-time.Millisecond) }},
		{"panel-fade", func(a *shelf) { a.closeAt = now.Add(-10 * time.Millisecond) }},
		{"drag-active", func(a *shelf) { a.dragging = true }},
		{"button-held", func(a *shelf) { a.ownPress = true }},
		{"island-gesture", func(a *shelf) { a.islandPress.active = true }},
		{"peek", func(a *shelf) { a.peeking = true }},
		{"feedback", func(a *shelf) { a.fb.drawn = true }},
		{"app-switcher", func(a *shelf) { a.apps.on = true }},
		{"live-activity", func(a *shelf) { a.live.shown = true }},
		{"snap-session", func(a *shelf) { a.snap.armed = true }},
		{"drop-zone", func(a *shelf) { a.drop.on = true }},
		{"basket", func(a *shelf) { a.basket.shown = true }},
		{"pins", func(a *shelf) { a.pins = []*pinItem{{pressed: true}} }},
		{"pet", func(a *shelf) { a.pet = &petWindow{pressed: true} }},
		{"pet-return-zone", func(a *shelf) { a.petReturning = true }},
		{"note-window", func(a *shelf) { a.note = &noteUI{win: new(mygo.Window)} }},
		{"rename-window", func(a *shelf) { a.rn = &renameUI{win: new(mygo.Window)} }},
		{"converter", func(a *shelf) { a.conv = &converter{job: &convertJob{}} }},
		{"awake-hold", func(a *shelf) { a.awake = &awakeModule{id: mac.PowerAssertionID(1)} }},
		{"usage-page", func(a *shelf) {
			a.visible, a.level, a.tab = true, 2, pageUsage
			a.usage = &usageFeature{host: a}
			a.registerPage(&usagePageStub{fakePage{on: true}})
		}},
		{"weekly-page", func(a *shelf) {
			a.visible, a.level, a.tab = true, 2, pageWeekly
			a.weekly = &weeklyFeature{host: a}
			a.registerPage(&weeklyPageStub{fakePage{on: true}})
		}},
		{"clipboard-pending", func(a *shelf) { a.clip = &clipboardModule{pending: &clipPending{}} }},
		{"pointer-near", func(a *shelf) { a.pointerNear = true }},
	}
	if got, want := len(cases), len(monitorActiveConditions); got != want {
		t.Fatalf("condition table has %d cases, scheduler has %d conditions", got, want)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := monitorShelf()
			c.set(a)
			if !a.monitorActive(now) {
				t.Fatalf("%s did not enter the active cadence: %v", c.name, a.monitorActiveNames(now))
			}
			if d := a.monitorDelay(now); d > pointerInterval || d < 0 {
				t.Fatalf("%s cadence = %v, want %v", c.name, d, pointerInterval)
			}
			found := false
			for _, n := range a.monitorActiveNames(now) {
				found = found || n == c.name
			}
			if !found {
				t.Fatalf("%s missing from %v", c.name, a.monitorActiveNames(now))
			}
		})
	}
}

func TestMonitorIdleHasNoCadence(t *testing.T) {
	old := dragLeftDown
	dragLeftDown = func() bool { return false }
	t.Cleanup(func() { dragLeftDown = old })
	a := monitorShelf()
	if names := a.monitorActiveNames(time.Now()); len(names) != 0 {
		t.Fatalf("an untouched shelf is active: %v", names)
	}
	if d := a.monitorDelay(time.Now()); d != time.Second {
		t.Fatalf("idle fallback = %v, want 1s", d)
	}
}

// TestMonitorPointerNearWakes drives the pointer event path: far away is
// idle, the notch hot zone switches the cadence on, leaving it switches
// back off.
func TestMonitorPointerNearWakes(t *testing.T) {
	oldCursor := cursorPoint
	t.Cleanup(func() { cursorPoint = oldCursor })
	a := monitorShelf()
	p := mygo.Point{X: 100, Y: 800}
	cursorPoint = func() mygo.Point { return p }
	a.pointerMoved()
	if a.monitorActive(time.Now()) {
		t.Fatalf("a far pointer must not wake the cadence: %v", a.monitorActiveNames(time.Now()))
	}
	p = mygo.Point{X: 800, Y: 10}
	a.pointerMoved()
	if !a.pointerNear || !a.monitorActive(time.Now()) {
		t.Fatalf("a pointer on the notch must wake the cadence")
	}
	p = mygo.Point{X: 100, Y: 800}
	a.pointerMoved()
	if a.pointerNear || a.monitorActive(time.Now()) {
		t.Fatalf("leaving the notch must return to idle: %v", a.monitorActiveNames(time.Now()))
	}
}

func TestMonitorDragAndClipboardSwitchToActive(t *testing.T) {
	now := time.Now()
	old := dragLeftDown
	t.Cleanup(func() { dragLeftDown = old })
	a := monitorShelf()
	dragLeftDown = func() bool { return true }
	if !a.monitorActive(now) {
		t.Fatal("a held button must keep the cadence")
	}
	dragLeftDown = func() bool { return false }
	a.clip = &clipboardModule{pending: &clipPending{source: "x", seen: now}}
	if !a.monitorActive(now) {
		t.Fatal("an unreadable clipboard change must keep the cadence")
	}
}

// TestMonitorDeadlines checks that every timed source reports its next due
// time into the one scheduler. Each case starts from a fresh shelf.
func TestMonitorDeadlines(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		set  func(*shelf)
		want time.Duration
	}{
		{"clipboard", func(a *shelf) {
			a.clip = &clipboardModule{pending: &clipPending{seen: now}}
			a.modules = append(a.modules, a.clip)
		}, 100 * time.Millisecond},
		{"audio", func(a *shelf) {
			a.audio = &audioModule{next: now.Add(time.Second)}
			a.modules = append(a.modules, a.audio)
		}, time.Second},
		{"power", func(a *shelf) {
			a.pwr = &powerModule{next: now.Add(2 * time.Second)}
			a.modules = append(a.modules, a.pwr)
		}, 2 * time.Second},
		{"downloads", func(a *shelf) {
			a.dl = &downloadsModule{next: now.Add(time.Second)}
			a.modules = append(a.modules, a.dl)
		}, time.Second},
		{"retention", func(a *shelf) { a.modules = append(a.modules, &retentionModule{host: a, last: now}) }, retentionCheck},
		{"ocr", func(a *shelf) {
			o := &ocrIndex{host: a, nextAt: now.Add(2 * time.Second)}
			a.modules = append(a.modules, o)
		}, 2 * time.Second},
		{"awake", func(a *shelf) {
			a.awake = &awakeModule{id: mac.PowerAssertionID(1), until: now.Add(5 * time.Second)}
			a.modules = append(a.modules, a.awake)
		}, 5 * time.Second},
		{"leaving", func(a *shelf) {
			a.leaving = map[string]time.Time{"x": now.Add(-time.Second)}
			a.modules = append(a.modules, &leavingModule{host: a})
		}, 9 * time.Second},
		{"note", func(a *shelf) {
			a.note = &noteUI{text: "a", draft: "ab", since: now.Add(-time.Second)}
			a.modules = append(a.modules, noteModule{host: a})
		}, 0},
		{"rename", func(a *shelf) {
			a.rn = &renameUI{undoUntil: now.Add(3 * time.Second)}
			a.modules = append(a.modules, renameModule{host: a})
		}, 3 * time.Second},
		{"now-playing-retry", func(a *shelf) {
			a.np = &nowPlayingModule{host: a, retryAt: now.Add(4 * time.Second)}
			a.modules = append(a.modules, a.np)
		}, 4 * time.Second},
		{"now-playing-position", func(a *shelf) {
			a.np = &nowPlayingModule{host: a, started: true, have: true, posNext: now.Add(700 * time.Millisecond), last: mac.NowPlayingState{Playing: true}}
			a.visible = true
			a.modules = append(a.modules, a.np)
		}, 700 * time.Millisecond},
		{"weekly-sample", func(a *shelf) {
			a.settings.Weekly.AppStats = true
			f := &weeklyFeature{host: a, sampler: weekly.NewSampler(nil, weekly.Probe{}), lastSample: now.Add(-time.Second)}
			a.modules = append(a.modules, f)
		}, weekly.SampleInterval - time.Second},
		{"status", func(a *shelf) { a.visible = true; a.status = "ok"; a.statusUntil = now.Add(3 * time.Second) }, 3 * time.Second},
		{"basket", func(a *shelf) { a.basket.shown = true; a.basket.hideAt = now.Add(2 * time.Second) }, 2 * time.Second},
		{"snap-confirm", func(a *shelf) {
			a.snap.confirm = &snapConfirm{at: now.Add(500 * time.Millisecond), until: now.Add(2 * time.Second)}
		}, 500 * time.Millisecond},
		{"pin-hide", func(a *shelf) {
			a.pins = []*pinItem{{edge: pinLeft, expanded: true, leftAt: now.Add(-500 * time.Millisecond)}}
		}, pinHideDelay - 500*time.Millisecond},
		{"pet-happy", func(a *shelf) { a.pet = &petWindow{}; a.happyUntil = now.Add(4 * time.Second) }, 4 * time.Second},
		{"update", func(a *shelf) { a.updateNext = now.Add(10 * time.Second) }, 10 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := monitorShelf()
			c.set(a)
			got := a.monitorDeadline(now)
			if got.IsZero() {
				t.Fatalf("%s reported no deadline", c.name)
			}
			d := got.Sub(now)
			if d < 0 {
				d = 0
			}
			if d != c.want {
				t.Fatalf("%s deadline = %v, want %v", c.name, d, c.want)
			}
		})
	}
}

// TestGatePollingFollowsMovement: the 10 ms gate poll runs only inside the
// window opened by a move event, and only while an overlay is on screen.
func TestGatePollingFollowsMovement(t *testing.T) {
	oldCursor := cursorPoint
	t.Cleanup(func() { cursorPoint = oldCursor })
	now := time.Now()
	gateMoveUntil.Store(now.Add(-time.Millisecond).UnixNano())
	if gateShouldPoll(now) {
		t.Fatal("an old move kept the gate poller awake")
	}
	a := monitorShelf()
	cursorPoint = func() mygo.Point { return mygo.Point{X: 100, Y: 800} }
	a.pointerMoved()
	if !gateShouldPoll(time.Now()) {
		t.Fatal("a fresh move did not open the poll window")
	}
	if gateShouldPoll(time.Now().Add(gatePollWindow + time.Millisecond)) {
		t.Fatal("the poll window did not close")
	}
}

// TestMonitorDeadlineTakesEarliest: the loop arms one timer at the earliest
// due item, not one timer per module.
func TestMonitorDeadlineTakesEarliest(t *testing.T) {
	now := time.Now()
	a := monitorShelf()
	a.clip = &clipboardModule{pending: &clipPending{seen: now}}
	a.modules = append(a.modules, a.clip)
	a.audio = &audioModule{next: now.Add(time.Second)}
	a.modules = append(a.modules, a.audio)
	a.pins = []*pinItem{{edge: pinLeft, expanded: true, leftAt: now.Add(-550 * time.Millisecond)}}
	if got := a.monitorDeadline(now).Sub(now); got != pinHideDelay-550*time.Millisecond {
		t.Fatalf("earliest deadline = %v, want the pin's %v", got, pinHideDelay-550*time.Millisecond)
	}
}

// TestPointerMovedFollowsDisplayOnlyWhenClosed: a closed shelf follows the
// pointer to another display; an open panel stays where it is, matching
// monitorDrag's rule.
func TestPointerMovedFollowsDisplayOnlyWhenClosed(t *testing.T) {
	oldDisplay, oldNotch, oldCursor := displayNearest, notchRect, cursorPoint
	t.Cleanup(func() { displayNearest, notchRect, cursorPoint = oldDisplay, oldNotch, oldCursor })
	displayNearest = func(p mygo.Point) mygo.Display {
		if p.X < 200 {
			return mygo.Display{ID: 1, Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 200, Height: 200}}
		}
		return mygo.Display{ID: 2, Bounds: mygo.Rectangle{X: 200, Y: 0, Width: 200, Height: 200}}
	}
	notchRect = func(int64) (int, int, int, int, bool) { return 0, 0, 0, 0, false }
	a := monitorShelf()
	a.dispID, a.dispBounds = 1, mygo.Rectangle{X: 0, Y: 0, Width: 200, Height: 200}
	cursorPoint = func() mygo.Point { return mygo.Point{X: 300, Y: 20} }
	a.pointerMoved()
	if a.dispID != 2 {
		t.Fatalf("closed shelf did not follow the pointer: dispID=%d", a.dispID)
	}
	a.visible = true
	cursorPoint = func() mygo.Point { return mygo.Point{X: 20, Y: 20} }
	a.pointerMoved()
	if a.dispID != 2 {
		t.Fatalf("open panel moved to another display: dispID=%d", a.dispID)
	}
}

// TestWakeAndTimerJobsBothRun: a timer wake and a wakeMonitor wake queued
// before the main thread runs must both execute, and the coalescing flag
// must return to false so later event wakes are not swallowed.
func TestWakeAndTimerJobsBothRun(t *testing.T) {
	a := monitorShelf()
	a.monitorStarted.Store(true)
	runs := 0
	a.monitorRunFn = func() { runs++ }
	var queued []func()
	a.wakeMainFn = func(fn func()) { queued = append(queued, fn) }
	a.postMain(func() { runs++ })
	a.wakeMonitor()
	if len(queued) != 2 {
		t.Fatalf("queued %d jobs, want timer + wake", len(queued))
	}
	for _, fn := range queued {
		fn()
	}
	if runs != 2 {
		t.Fatalf("ran %d jobs, want 2", runs)
	}
	if a.monitorWakeQueued.Load() {
		t.Fatal("monitorWakeQueued stayed true")
	}
}
