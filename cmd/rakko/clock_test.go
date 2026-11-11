//go:build darwin

package main

// The countdown, the stopwatch and the home clock's rules: deadline-based
// timing (a sleeping Mac still lands on 时间到), the unanswered alert's
// priority in the closed notch and the band, the one-minute mute, and the
// card catalogue's two new entries.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/mac"
	"rakko/internal/settings"
	"rakko/locales"
)

func clockShelf() (*shelf, *clockModule) {
	a := motionShelf()
	a.settings = settings.Default()
	a.tab = pageTools
	a.registerClockModules()
	return a, a.clock
}

func TestTimerStartPauseResumeCancel(t *testing.T) {
	_, m := clockShelf()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	m.startTimer(now, 5*time.Minute)
	if m.phase != timerRunning || !m.end.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("start = phase %v end %v", m.phase, m.end)
	}
	if got := clockTextCeil(m.timerRemainAt(now.Add(2 * time.Minute))); got != "3:00" {
		t.Fatalf("remaining after 2m = %q", got)
	}
	m.pauseTimer(now.Add(2 * time.Minute))
	if m.phase != timerPaused || m.remain != 3*time.Minute {
		t.Fatalf("pause = phase %v remain %v", m.phase, m.remain)
	}
	if got := clockTextCeil(m.timerRemainAt(now.Add(20 * time.Minute))); got != "3:00" {
		t.Fatalf("a paused timer moved: %q", got)
	}
	m.resumeTimer(now.Add(20 * time.Minute))
	if m.phase != timerRunning || !m.end.Equal(now.Add(23*time.Minute)) {
		t.Fatalf("resume = phase %v end %v", m.phase, m.end)
	}
	m.stopTimer(now.Add(21 * time.Minute))
	if m.phase != timerIdle {
		t.Fatalf("cancel left phase %v", m.phase)
	}
}

func TestTimerSleepsPastTheDeadline(t *testing.T) {
	_, m := clockShelf()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	played := 0
	m.playSound = func() { played++ }
	m.stopSound = func() {}
	m.startTimer(now, 5*time.Minute)
	// The Mac slept through the deadline: the next tick, two hours later,
	// must land on 时间到 immediately.
	m.tick(now.Add(2 * time.Hour))
	if m.phase != timerDone || !m.ringing || played != 1 {
		t.Fatalf("wake = phase %v ringing %v played %d", m.phase, m.ringing, played)
	}
	if got := clockTextCeil(m.timerRemainAt(now.Add(2 * time.Hour))); got != "0:00" {
		t.Fatalf("done shows %q", got)
	}
	// The mute runs a full minute from the moment it rang, not from the
	// deadline that passed while asleep.
	m.tick(now.Add(2*time.Hour + timerRingFor - time.Millisecond))
	if !m.ringing {
		t.Fatal("muted before the minute was up")
	}
	m.tick(now.Add(2*time.Hour + timerRingFor))
	if m.ringing {
		t.Fatal("still ringing after a minute")
	}
}

func TestTimerAlertStaysAcrossPanelNoticesAndApproval(t *testing.T) {
	a, m := clockShelf()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	m.playSound = func() {}
	m.stopSound = func() {}
	m.startTimer(now, time.Minute)
	m.tick(now.Add(time.Minute))
	if !a.clockAlarmUp(now.Add(time.Minute)) {
		t.Fatal("the alert does not own the closed notch")
	}
	if !a.liveClickable(now.Add(time.Minute)) {
		t.Fatal("the alert's buttons would be swallowed by the shell's open handler")
	}
	// Opening the panel hides the closed card (its band chip replaces it)
	// and clearing live events must not touch the alert.
	a.visible = true
	a.clearLive()
	if a.clockAlarmUp(now.Add(time.Minute)) {
		t.Fatal("the alert drew over the open panel")
	}
	a.visible = false
	if !a.clockAlarmUp(now.Add(time.Minute)) {
		t.Fatal("the alert did not return after the panel closed")
	}
	// A transient notice waits behind the alert.
	a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 80}, 3*time.Second, now)
	if _, ok := a.liveNow(now, a.liveAllow() && !a.clockAlarmFree()); ok {
		t.Fatal("a notice took the notch while the alert was up")
	}
	if _, ok := a.liveNow(now, a.liveAllow()); !ok {
		t.Fatal("the queued notice never showed after the alert")
	}
	a.clearLive()
	// A queued approval card preempts the alert even though the queue is
	// otherwise held back.
	a.settings.Approval.Enabled = true
	a.live.queued = []liveEvent{{kind: liveAgentAsk, ask: &approval{deadline: now.Add(time.Minute)}}}
	ev, ok := a.liveNow(now, false)
	if !ok || ev.kind != liveAgentAsk {
		t.Fatalf("queued approval = %+v ok=%v", ev, ok)
	}
	if a.clockAlarmUp(now) {
		t.Fatal("the alert preempted an approval card")
	}
	// Answered: the card leaves and the alert returns.
	a.live.shown = false
	if !a.clockAlarmUp(now) {
		t.Fatal("the alert did not return after the approval")
	}
}

func TestTimerRepeatAndStop(t *testing.T) {
	_, m := clockShelf()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	played, stopped := 0, 0
	m.playSound = func() { played++ }
	m.stopSound = func() { stopped++ }
	m.startTimer(now, 7*time.Minute)
	m.tick(now.Add(7 * time.Minute))
	if m.phase != timerDone {
		t.Fatal("did not reach 时间到")
	}
	retry := now.Add(10 * time.Minute)
	m.repeatTimer(retry)
	if m.phase != timerRunning || !m.end.Equal(retry.Add(7*time.Minute)) {
		t.Fatalf("repeat = phase %v end %v", m.phase, m.end)
	}
	if m.ringing || stopped != 1 {
		t.Fatalf("repeat left ringing=%v stopped=%d", m.ringing, stopped)
	}
	m.tick(retry.Add(7 * time.Minute))
	if m.phase != timerDone {
		t.Fatal("the repeat did not reach 时间到")
	}
	m.stopTimer(retry.Add(8 * time.Minute))
	if m.phase != timerIdle || m.ringing || stopped != 2 {
		t.Fatalf("stop = phase %v ringing=%v stopped=%d", m.phase, m.ringing, stopped)
	}
	_ = played
}

func TestTimerTapStartsLastLength(t *testing.T) {
	_, m := clockShelf()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	m.toggleTimer(now) // idle → the first run's default five minutes
	if m.phase != timerRunning || !m.end.Equal(now.Add(timerDefaultDur)) {
		t.Fatalf("first tap = phase %v end %v", m.phase, m.end)
	}
	m.stopTimer(now.Add(time.Minute))
	m.startTimer(now.Add(2*time.Minute), 10*time.Minute)
	m.stopTimer(now.Add(3 * time.Minute))
	m.toggleTimer(now.Add(4 * time.Minute))
	if m.phase != timerRunning || !m.end.Equal(now.Add(14*time.Minute)) {
		t.Fatalf("second start = phase %v end %v", m.phase, m.end)
	}
}

func TestStopwatchStartPauseResumeReset(t *testing.T) {
	_, m := clockShelf()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	m.startWatch(now)
	if got := m.watchElapsedAt(now.Add(90 * time.Second)); got != 90*time.Second {
		t.Fatalf("elapsed = %v", got)
	}
	// The stopwatch reads the wall clock: sleeping for two hours counts
	// as two hours of elapsed time.
	if got := m.watchElapsedAt(now.Add(2 * time.Hour)); got != 2*time.Hour {
		t.Fatalf("after a sleep = %v", got)
	}
	m.pauseWatch(now.Add(90 * time.Second))
	if got := m.watchElapsedAt(now.Add(time.Hour)); got != 90*time.Second {
		t.Fatalf("paused elapsed moved: %v", got)
	}
	m.resumeWatch(now.Add(time.Hour))
	if got := m.watchElapsedAt(now.Add(time.Hour + 30*time.Second)); got != 2*time.Minute {
		t.Fatalf("resumed elapsed = %v", got)
	}
	m.resetWatch()
	if m.watchRun || m.watchElapsed != 0 {
		t.Fatalf("reset = running %v elapsed %v", m.watchRun, m.watchElapsed)
	}
}

func TestClockTextFormats(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0:00"},
		{9 * time.Second, "0:09"},
		{59 * time.Second, "0:59"},
		{time.Minute, "1:00"},
		{59*time.Minute + 59*time.Second, "59:59"},
		{time.Hour, "1:00:00"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1:02:03"},
		{23*time.Hour + 59*time.Minute + 59*time.Second, "23:59:59"},
	}
	for _, c := range cases {
		if got := clockText(c.d); got != c.want {
			t.Errorf("clockText(%v) = %q, want %q", c.d, got, c.want)
		}
	}
	// The countdown rounds a partial second up, so it starts on its whole
	// length and reaches 0:00 only at the deadline.
	if got := clockTextCeil(4*time.Minute + 59*time.Second + 500*time.Millisecond); got != "5:00" {
		t.Errorf("ceil = %q, want 5:00", got)
	}
	if got := clockTextCeil(500 * time.Millisecond); got != "0:01" {
		t.Errorf("ceil under a second = %q, want 0:01", got)
	}
}

func TestHomeClockFollowsHourCycle(t *testing.T) {
	when := time.Date(2026, 11, 11, 21, 47, 0, 0, time.Local)
	en12 := locales.In("en-US-u-hc-h12")
	en24 := locales.In("en-US-u-hc-h23")
	if got := en12.HomeClock(when); got != "9:47 PM" {
		t.Fatalf("en 12h = %q", got)
	}
	if got := en24.HomeClock(when); got != "21:47" {
		t.Fatalf("en 24h = %q", got)
	}
	zh12 := locales.In("zh-Hans-u-hc-h12")
	if got := homeClockText(zh12, when); got != "下午 9:47" {
		t.Fatalf("zh 12h = %q", got)
	}
	if got := en12.HomeDateLine(when); got != "Wednesday, November 11" {
		t.Fatalf("en date line = %q", got)
	}
	if got := locales.In("zh-Hans").HomeDateLine(when); got != "11月11日 星期三" {
		t.Fatalf("zh date line = %q", got)
	}
}

func TestClockCatalogEntries(t *testing.T) {
	timer := toolCardDefFor("timer")
	if timer == nil || timer.kind != cardWidget || timer.span != 2 || !timer.variableSpan {
		t.Fatalf("timer definition = %+v", timer)
	}
	watch := toolCardDefFor("stopwatch")
	if watch == nil || watch.kind != cardWidget || watch.span != 1 || !watch.variableSpan {
		t.Fatalf("stopwatch definition = %+v", watch)
	}
	if got := cardSizes("timer"); len(got) != 2 || got[0] != [2]int{2, 1} || got[1] != [2]int{1, 1} {
		t.Fatalf("timer sizes = %v", got)
	}
	if got := cardSizes("stopwatch"); len(got) != 2 || got[0] != [2]int{1, 1} || got[1] != [2]int{2, 1} {
		t.Fatalf("stopwatch sizes = %v", got)
	}
	for _, c := range layout.Default().Cards {
		if c.ID == "timer" || c.ID == "stopwatch" {
			t.Fatalf("the new card %q changed the fresh-install layout", c.ID)
		}
	}
}

func TestClockChipPriority(t *testing.T) {
	a, m := clockShelf()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	m.startTimer(now, 5*time.Minute)
	m.startWatch(now)
	acts := a.liveActivities()
	if len(acts) != chipMax {
		t.Fatalf("chips = %d", len(acts))
	}
	if _, ok := acts[0].(timerChip); !ok {
		t.Fatalf("first chip = %T, want the countdown", acts[0])
	}
	if _, ok := acts[1].(stopwatchChip); !ok {
		t.Fatalf("second chip = %T, want the stopwatch", acts[1])
	}
	// A running conversion's 取消 leads both clocks even beside a loaded
	// track: while a clock is under way the strip follows the design's
	// order, converter first, then countdown, then stopwatch.
	st := mac.NowPlayingState{Title: "夜曲", Playing: true, Duration: 200, Elapsed: 10, Received: time.Now()}
	np := &nowPlayingModule{host: a, have: true, last: st}
	a.conv = &converter{host: a, job: &convertJob{started: time.Now().Add(-time.Second)}}
	a.modules = []module{np, a.conv, timerChip{m}, stopwatchChip{m}}
	acts = a.liveActivities()
	if _, ok := acts[0].(*converter); !ok {
		t.Fatalf("conversion chip = %T, want first", acts[0])
	}
	if _, ok := acts[1].(timerChip); !ok {
		t.Fatalf("second chip = %T, want the countdown", acts[1])
	}
	// Without a clock the old module order is untouched: the player chip
	// still leads the conversion.
	m.stopTimer(now)
	m.resetWatch()
	acts = a.liveActivities()
	if _, ok := acts[0].(*nowPlayingModule); !ok {
		t.Fatalf("no-clock first chip = %T, want the player", acts[0])
	}
}

func TestClockEditNudgesAndLimits(t *testing.T) {
	e := &timerEdit{h: 23, m: 59, s: 59, field: 1}
	e.nudge(1)
	if e.m != 0 {
		t.Fatalf("minute wrap = %d", e.m)
	}
	e.field = 0
	e.nudge(1)
	if e.h != 0 {
		t.Fatalf("hour wrap = %d", e.h)
	}
	e.field = 2
	e.raw = "7"
	e.typeDigits()
	if e.s != 7 {
		t.Fatalf("typed seconds = %d", e.s)
	}
	e.raw = "99"
	e.typeDigits()
	if e.s != 9 {
		t.Fatalf("out-of-range digit = %d", e.s)
	}
	e.raw = "123"
	e.typeDigits()
	if e.raw != "23" || e.s != 23 {
		t.Fatalf("third digit = raw %q value %d", e.raw, e.s)
	}
}

// TestClockCustomEditTypesAndStarts drives the three-field editor through
// a real headless window: click the big number, type 35, press Return.
func TestClockCustomEditTypesAndStarts(t *testing.T) {
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(), level: 2, visible: true, tab: pageTools,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.ensurePages()
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "timer", Span: 2, Rows: 1}}}
	now := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	m := newClockModule(a)
	m.now = func() time.Time { return now }
	a.clock = m
	view := ui.NewTester(a.view, sheetW+2*earR, panelTestH)
	view.Frame()
	view.Frame()
	// The number sits after the glyph in the 2 × 1 card at (12, 64).
	view.ClickAt(70, 79)
	view.Frame()
	if m.edit == nil {
		t.Fatal("clicking the number did not open the editor")
	}
	view.Type("3")
	view.Frame()
	view.Type("5")
	view.Frame()
	view.Key(0, ui.KeyEnter)
	view.Frame()
	if m.phase != timerRunning || !m.end.Equal(now.Add(35*time.Minute)) {
		t.Fatalf("after typing 35 and Return: phase=%v end=%v", m.phase, m.end)
	}
}

// TestHomeClockUsesTheSystemHourCycle drives the page itself: the same
// minute reads 9:47 PM or 21:47 by the window's regional settings.
func TestHomeClockUsesTheSystemHourCycle(t *testing.T) {
	for _, tc := range []struct {
		cycle int
		want  func(t time.Time) string
	}{
		{12, func(t time.Time) string {
			return i18n.FormatDate("en-US-u-hc-h12", t, i18n.DateOptions{Skeleton: "jm"})
		}},
		{24, func(t time.Time) string {
			return i18n.FormatDate("en-US-u-hc-h23", t, i18n.DateOptions{Skeleton: "jm"})
		}},
	} {
		a, view, _ := homeShelf(t, nil)
		a.locale = "en"
		view.SetLocale("en")
		view.SetRegion(i18n.Region{Locale: "en-US", HourCycle: tc.cycle})
		view.Frame()
		view.Frame()
		want := tc.want(view.Now())
		if !view.HasText(want) {
			t.Fatalf("hour cycle %d: the page lacks %q", tc.cycle, want)
		}
	}
}

// TestClockPeekShapeKinds proves the closed shell picks the countdown's
// wings while it runs and its 时间到 card once the deadline passes.
func TestClockPeekShapeKinds(t *testing.T) {
	a := liveShelf()
	now := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	m := newClockModule(a)
	m.now = func() time.Time { return now }
	a.clock = m
	a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
	view := ui.NewTester(a.view, 500, 130)
	view.Frame()
	view.Frame()
	if a.fx.peekKind != peekChevron {
		t.Fatalf("idle kind = %d, want chevron", a.fx.peekKind)
	}
	m.startTimer(now, 5*time.Minute)
	view.Frame()
	view.Frame()
	if a.fx.peekKind != peekTimerWings {
		t.Fatalf("running kind = %d, want timer wings", a.fx.peekKind)
	}
	m.finish(now)
	view.Frame()
	view.Frame()
	if a.fx.peekKind != peekAlarm {
		t.Fatalf("done kind = %d, want the alert", a.fx.peekKind)
	}
}

// TestClockAlertButtons clicks 重复 and 停止 on the drawn card: the same
// view wiring the real notch uses, without a pointer outside the test.
func TestClockAlertButtons(t *testing.T) {
	a, m := clockShelf()
	now := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	m.now = func() time.Time { return now }
	m.playSound = func() {}
	m.stopSound = func() {}
	m.startTimer(now, time.Minute)
	m.finish(now)
	a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
	view := ui.NewTester(a.view, 500, 130)
	view.Frame()
	view.Frame()
	if err := view.Click("重复"); err != nil {
		t.Fatalf("重复: %v", err)
	}
	view.Frame()
	if m.phase != timerRunning || !m.end.Equal(now.Add(time.Minute)) {
		t.Fatalf("重复: phase=%v end=%v", m.phase, m.end)
	}
	m.finish(now.Add(time.Minute))
	view.Frame()
	view.Frame()
	if err := view.Click("停止"); err != nil {
		t.Fatalf("停止: %v", err)
	}
	view.Frame()
	if m.phase != timerIdle {
		t.Fatalf("停止 left phase %v", m.phase)
	}
}

// TestClockDoneChipStops is the open panel's ringing capsule: the orange
// 停止 chip clears the alert, as it does in the real band.
func TestClockDoneChipStops(t *testing.T) {
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(), level: 2, visible: true, tab: pageTools,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.ensurePages()
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "timer", Span: 2, Rows: 1}}}
	now := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	m := newClockModule(a)
	m.now = func() time.Time { return now }
	m.playSound = func() {}
	m.stopSound = func() {}
	m.startTimer(now, time.Minute)
	m.finish(now)
	a.clock = m
	a.modules = []module{timerChip{m}}
	view := ui.NewTester(a.view, sheetW+2*earR, panelTestH)
	view.Frame()
	view.Frame()
	if err := view.Click("停止"); err != nil {
		t.Fatalf("band stop chip: %v", err)
	}
	view.Frame()
	if m.phase != timerIdle {
		t.Fatalf("the stop chip left phase %v", m.phase)
	}
}

// TestClockRegistrationDrivesTheDeadline exercises the production
// registration: registerClockModules is the function ready() calls, and
// tickModules is the dispatch monitorTick runs. No test shelf fills in
// modules or calls clockModule.tick by hand.
func TestClockRegistrationDrivesTheDeadline(t *testing.T) {
	a := &shelf{settings: settings.Default()}
	a.registerClockModules()
	if a.clock == nil {
		t.Fatal("registerClockModules did not create the clock")
	}
	played := 0
	a.clock.playSound = func() { played++ }
	a.clock.stopSound = func() {}
	now := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	a.clock.startTimer(now, time.Minute)
	a.tickModules(now.Add(time.Minute))
	if a.clock.phase != timerDone || played != 1 {
		t.Fatalf("shared tick at the deadline: phase=%v played=%d", a.clock.phase, played)
	}
	if !a.clock.alarmUp() {
		t.Fatal("the shared tick did not leave the alert up")
	}
	a.tickModules(now.Add(2 * time.Minute))
	if a.clock.ringing {
		t.Fatal("the shared tick did not mute the alert after a minute")
	}
}

// TestClockDoneChipStopsAtTheSearchBar: the open panel's level-1 bar also
// carries the ringing capsule, so 停止 is reachable before opening a page.
func TestClockDoneChipStopsAtTheSearchBar(t *testing.T) {
	a := motionShelf()
	a.settings = settings.Default()
	a.level, a.visible, a.progress = 1, true, 1
	a.registerClockModules()
	now := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	m := a.clock
	m.now = func() time.Time { return now }
	m.playSound = func() {}
	m.stopSound = func() {}
	m.startTimer(now, time.Minute)
	m.finish(now)
	view := ui.NewTester(a.view, homeW+2*earR, homeShellH+navGap+navH+4)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	if !view.HasText("停止") {
		t.Fatal("the search bar's band has no 停止 capsule")
	}
	if err := view.Click("停止"); err != nil {
		t.Fatalf("停止 on the search bar: %v", err)
	}
	view.Frame()
	if m.phase != timerIdle {
		t.Fatalf("the search bar's stop capsule left phase %v", m.phase)
	}
}

// TestClockBandFitsTheNotchEars: a 180pt notch leaves about 112pt in each
// ear, so two full clock readings cannot share the right ear. The notch
// puts the countdown beside the left ear and the stopwatch on the right;
// the island's continuous body keeps both on the right. Neither reading
// may be compressed below its single-chip width.
func TestClockBandFitsTheNotchEars(t *testing.T) {
	l := locales.In("zh-Hans")
	now := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	build := func(island, withTimer, withWatch bool) (*shelf, *ui.Tester) {
		var a *shelf
		if island {
			a = islandShelf(true, settings.ShellStyleIsland)
		} else {
			a = motionShelf()
			a.settings = settings.Default()
		}
		a.level, a.visible, a.progress = 2, true, 1
		a.tab = pageTray
		a.ensurePages()
		a.registerClockModules()
		m := a.clock
		m.now = func() time.Time { return now }
		m.playSound = func() {}
		m.stopSound = func() {}
		if withTimer {
			m.startTimer(now, 5*time.Minute)
		}
		if withWatch {
			m.watchRun, m.watchStart = true, now.Add(-754*time.Second)
		}
		a.locale = "zh-Hans"
		view := ui.NewTester(a.view, sheetW+2*earR, panelTestH)
		view.SetLocale("zh-Hans")
		view.Frame()
		view.Frame()
		return a, view
	}
	timerLabel := l.TimerChipLabel("5:00")
	watchLabel := l.StopwatchElapsed("12:34")

	// The same capsules alone: the widths below are what an unconstrained
	// capsule takes.
	_, tv := build(false, true, false)
	timerAlone, ok := tv.Find(timerLabel)
	if !ok {
		t.Fatal("no countdown chip alone")
	}
	_, wv := build(false, false, true)
	watchAlone, ok := wv.Find(watchLabel)
	if !ok {
		t.Fatal("no stopwatch chip alone")
	}

	// Island: one continuous body has room for both without shrinking.
	_, iv := build(true, true, true)
	timerIsland, ok := iv.Find(timerLabel)
	if !ok {
		t.Fatal("no countdown chip on the island")
	}
	watchIsland, ok := iv.Find(watchLabel)
	if !ok {
		t.Fatal("no stopwatch chip on the island")
	}
	if timerIsland.W < timerAlone.W-0.5 || watchIsland.W < watchAlone.W-0.5 {
		t.Fatalf("island capsules were compressed: %+v %+v (alone %+v %+v)",
			timerIsland, watchIsland, timerAlone, watchAlone)
	}

	// Notch: full readings, countdown left of the housing, stopwatch right.
	a, view := build(false, true, true)
	timerEl, ok := view.Find(timerLabel)
	if !ok {
		t.Fatal("no countdown chip under the notch")
	}
	watchEl, ok := view.Find(watchLabel)
	if !ok {
		t.Fatal("no stopwatch chip under the notch")
	}
	if timerEl.W < timerAlone.W-0.5 || watchEl.W < watchAlone.W-0.5 {
		t.Fatalf("notch capsules were compressed: %+v %+v (alone %+v %+v)",
			timerEl, watchEl, timerAlone, watchAlone)
	}
	center := float32(sheetW+2*earR) / 2
	halfHousing := float32(a.notchW+2*6) / 2
	if timerEl.X+timerEl.W > center-halfHousing+1 {
		t.Fatalf("countdown chip reaches the camera housing: %+v", timerEl)
	}
	if watchEl.X < center+halfHousing-1 {
		t.Fatalf("stopwatch chip starts under the camera housing: %+v", watchEl)
	}
}
