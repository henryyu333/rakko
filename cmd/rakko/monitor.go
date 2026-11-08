//go:build darwin

package main

// The monitoring loop. It runs on the main thread only, at 30 ms while the
// notch shell needs to follow the pointer and at the earliest deadline
// otherwise, so a resting shelf does not wake the process thirty times a
// second just to read the same state. Every wake — a pointer move, a drag,
// a clipboard poll, a helper line, a module deadline — ends in one tick;
// the tick then reports when it next needs to run.

import (
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
)

// idleDragPoll is the fallback read of the drag pasteboard while the shell
// is at rest. Every drag moves again after its first event, so a missed
// first event is recovered by the next one; this only bounds the case where
// the event monitor itself is unavailable.
const idleDragPoll = time.Second

// monitorActiveCondition is one state that makes the shell need 30 ms
// frames. The list is the contract the scheduler test walks: every entry
// must be reachable and must keep or enter the active cadence.
type monitorActiveCondition struct {
	name string
	on   func(*shelf, time.Time) bool
}

func anyPinsBusy(a *shelf) bool {
	if a.pinReturning {
		return true
	}
	for _, it := range a.pins {
		if it.wantKill || it.pressed || it.moving {
			return true
		}
		if it.edge != pinFree && it.expanded && !it.always {
			return true
		}
		if !it.leftAt.IsZero() {
			return true
		}
	}
	return false
}

func anyPinDue(a *shelf, now time.Time) time.Time {
	var next time.Time
	for _, it := range a.pins {
		if it.always || !it.expanded || it.leftAt.IsZero() {
			continue
		}
		due := it.leftAt.Add(pinHideDelay)
		if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	return next
}

var monitorActiveConditions = []monitorActiveCondition{
	{"panel-visible", func(a *shelf, _ time.Time) bool { return a.visible }},
	{"panel-opening", func(a *shelf, _ time.Time) bool { return a.fx.openK > 0.02 }},
	{"panel-spring", func(a *shelf, _ time.Time) bool {
		return !a.animationStart.IsZero() || !a.peekAnimStart.IsZero() ||
			a.fx.openMoving || a.fx.sizeMoving
	}},
	{"panel-fade", func(a *shelf, now time.Time) bool {
		return !a.closeAt.IsZero() && now.Sub(a.closeAt) < closeFade
	}},
	{"drag-active", func(a *shelf, _ time.Time) bool { return a.dragging || a.pointerDrag }},
	{"button-held", func(a *shelf, _ time.Time) bool { return a.ownPress || dragLeftDown() }},
	{"island-gesture", func(a *shelf, _ time.Time) bool { return a.islandDrag || a.islandPress.active }},
	{"peek", func(a *shelf, _ time.Time) bool { return a.peeking || a.peekProgress > 0.01 }},
	{"feedback", func(a *shelf, now time.Time) bool {
		return a.fb.drawn || a.fb.retracting || a.capsuleUp(now)
	}},
	{"app-switcher", func(a *shelf, _ time.Time) bool { return a.appsModeOn() }},
	{"live-activity", func(a *shelf, _ time.Time) bool {
		return a.live.shown || a.liveUp() || len(a.live.queued) > 0
	}},
	{"snap-session", func(a *shelf, _ time.Time) bool {
		s := &a.snap
		return s.armed || s.dragging || s.panel || s.drawn || s.confirm != nil || s.ghost != nil
	}},
	{"drop-zone", func(a *shelf, now time.Time) bool {
		return a.drop.on || a.dropHot || a.drop.catchOn(now)
	}},
	{"basket", func(a *shelf, _ time.Time) bool { return a.basket.shown }},
	{"pins", func(a *shelf, _ time.Time) bool { return anyPinsBusy(a) }},
	{"pet", func(a *shelf, _ time.Time) bool {
		p := a.pet
		return p != nil && (p.pressed || p.moving || !p.slideAt.IsZero())
	}},
	{"pet-return-zone", func(a *shelf, _ time.Time) bool { return a.petReturning }},
	{"note-window", func(a *shelf, _ time.Time) bool { return a.note != nil && a.note.win != nil }},
	{"rename-window", func(a *shelf, _ time.Time) bool { return a.rn != nil && a.rn.win != nil }},
	{"converter", func(a *shelf, _ time.Time) bool { return a.conv != nil && a.conv.job != nil }},
	{"awake-hold", func(a *shelf, _ time.Time) bool { return a.awake != nil && a.awake.active() }},
	{"usage-page", func(a *shelf, _ time.Time) bool { return a.usage != nil && a.usage.usageVisible() }},
	{"weekly-page", func(a *shelf, now time.Time) bool {
		return a.weekly != nil && a.weekly.visible(now)
	}},
	{"clipboard-pending", func(a *shelf, _ time.Time) bool {
		return a.clip != nil && a.clip.pending != nil
	}},
	{"pointer-near", func(a *shelf, _ time.Time) bool { return a.pointerNear }},
}

// monitorActive reports whether the shell needs the 30 ms cadence now.
func (a *shelf) monitorActive(now time.Time) bool {
	for _, c := range monitorActiveConditions {
		if c.on(a, now) {
			return true
		}
	}
	return false
}

// monitorActiveNames lists the conditions currently holding the cadence;
// the scheduler test uses it to prove each condition keeps the loop awake.
func (a *shelf) monitorActiveNames(now time.Time) []string {
	var names []string
	for _, c := range monitorActiveConditions {
		if c.on(a, now) {
			names = append(names, c.name)
		}
	}
	return names
}

// monitorDeadline is the earliest clock-driven work: module polls, clocks
// the shell itself owns, and platform settings that can change while the
// app runs. Zero means nothing is due.
func (a *shelf) monitorDeadline(now time.Time) time.Time {
	var next time.Time
	add := func(at time.Time) {
		if at.IsZero() {
			return
		}
		if at.Before(now) {
			at = now
		}
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}

	for _, m := range a.modules {
		if d, ok := m.(interface{ nextTick(time.Time) time.Time }); ok {
			add(d.nextTick(now))
		}
	}

	// The shell's own clocks.
	if a.visible || a.fb.drawn {
		if !a.statusUntil.IsZero() {
			until := a.statusUntil
			if f := &a.fb; !f.pausedAt.IsZero() && f.until.After(until) {
				until = f.until
			}
			add(until)
		} else if a.status != "" && !a.statusSeenAt.IsZero() && a.statusSeen < toastLife {
			add(a.statusSeenAt.Add(toastLife - a.statusSeen))
		}
	}
	if b := &a.basket; b.shown {
		switch {
		case b.closing:
			add(b.closedAt.Add(basketCloseFallback))
		case !b.hideAt.IsZero():
			add(b.hideAt)
		case !b.endAt.IsZero() && !b.dropped && !b.failed:
			add(b.endAt.Add(basketAfterEnd))
		case !b.leftSince.IsZero():
			add(b.leftSince.Add(time.Second))
		}
	}
	if c := a.snap.confirm; c != nil {
		add(c.at)
		add(c.until)
	}
	add(anyPinDue(a, now))
	if p := a.pet; p != nil {
		if a.happyUntil.After(now) {
			add(a.happyUntil)
		}
		if sleepy := a.lastAct.Add(otterSleepyAfter); a.lastAct.After(now) || sleepy.After(now) {
			add(sleepy)
		}
	}
	if !a.appearanceAt.IsZero() {
		add(a.appearanceAt.Add(appearancePoll))
	}
	if !a.updateNext.IsZero() {
		add(a.updateNext)
	}
	if !a.updatePromptAt.IsZero() {
		add(a.updatePromptAt)
	}
	return next
}

// monitorDelay is the time until the next pass: the active cadence, or the
// earliest deadline. A resting shelf still has the 100 ms clipboard poll,
// so this is always finite.
func (a *shelf) monitorDelay(now time.Time) time.Duration {
	if a.monitorActive(now) {
		return pointerInterval
	}
	next := a.monitorDeadline(now)
	if next.IsZero() {
		return time.Second
	}
	if d := next.Sub(now); d > 0 {
		return d
	}
	return 0
}

// startMonitoring starts the main-thread loop. It ticks once immediately so
// module gates arm their own deadlines, then schedules the next pass.
func (a *shelf) startMonitoring() {
	if a.monitorStarted.Load() || a.quitting {
		return
	}
	a.monitorRunFn = a.monitorRun
	a.monitorStarted.Store(true)
	if a.clip != nil {
		a.clip.startBackground()
	}
	// A pointer already resting on the notch must peek at once, exactly as
	// the old 30 ms loop did, without waiting for the next move event.
	p := cursorPoint()
	a.pointerNear = a.pointerNearZone(p)
	a.lastMoveAt = time.Now()
	a.monitorRun()
}

// monitorRun performs one pass and schedules the next. Main thread only.
func (a *shelf) monitorRun() {
	if a.quitting || !a.monitorStarted.Load() {
		return
	}
	now := time.Now()
	a.monitorTick(now)
	a.scheduleMonitor(now)
}

// scheduleMonitor keeps exactly one clock: a repeating native run-loop
// timer at the active 30 ms cadence, or one Go timer at the earliest idle
// deadline. Switching between them starts/stops the native timer, so an
// active shell never pays a cross-thread wakeup per frame.
func (a *shelf) scheduleMonitor(now time.Time) {
	if a.quitting || !a.monitorStarted.Load() {
		return
	}
	if a.monitorActive(now) {
		if a.activeTickerStop == nil {
			a.activeTickerStop = mac.StartMainTicker(pointerInterval, func() { a.monitorRun() })
		}
		return
	}
	if a.activeTickerStop != nil {
		a.activeTickerStop()
		a.activeTickerStop = nil
	}
	gen := a.monitorGen.Add(1)
	delay := a.monitorDelay(now)
	mac.AfterMainOnce(delay, func() {
		if a.quitting || a.monitorGen.Load() != gen {
			return
		}
		a.monitorRun()
	})
}

// postMain delivers fn to the main run loop. Tests replace wakeMainFn to
// drive the scheduler without AppKit.
func (a *shelf) postMain(fn func()) {
	if a.wakeMainFn != nil {
		a.wakeMainFn(fn)
		return
	}
	mac.WakeMain(fn)
}

// wakeMonitor asks for an immediate pass. Safe from any goroutine; repeated
// wakes coalesce into one queued pass.
func (a *shelf) wakeMonitor() {
	if a.quitting || !a.monitorStarted.Load() {
		return
	}
	if !a.monitorWakeQueued.CompareAndSwap(false, true) {
		return
	}
	run := a.monitorRunFn
	if run == nil {
		a.monitorWakeQueued.Store(false)
		return
	}
	a.postMain(func() {
		a.monitorWakeQueued.Store(false)
		if a.quitting {
			return
		}
		run()
	})
}

// monitorTick is the old 30 ms body, unchanged in order: modules first,
// then the shell's own animation and pointer work. The pointer is sampled
// once for the whole pass.
func (a *shelf) monitorTick(now time.Time) {
	if a.quitting {
		return
	}
	a.pointerTick, a.pointerTickValid = cursorPoint(), true
	defer func() { a.pointerTickValid = false }()

	active := a.monitorActive(now)
	for _, m := range a.modules {
		m.tick(now)
	}
	a.refreshAppearance(now)
	// The idle pass only exists for the clipboard poll and the shell's own
	// clocks. Pointer and drag work runs at the active cadence, or as a
	// fallback once per idleDragPoll in case a drag event was missed.
	if active || a.lastDragPoll.IsZero() || now.Sub(a.lastDragPoll) >= idleDragPoll {
		a.lastDragPoll = now
		a.animate(now)
		a.monitorDrag(now)
		a.syncWindowFrame()
		a.syncPointerGate()
	}
	// 内容高度随项目数/正在播放卡片/搜索状态变化：
	// desiredFrame 未变时是恒等比较，不触发原生缩放。
	a.syncWindowFrame()
	// The gate follows the shape the last frame drew, so it is normally
	// right already; asking again every tick covers what draws nothing (a
	// settled panel), a missed move event and a shape that changed under a
	// still pointer, so a press never waits for the next move.
	a.syncPointerGate()
	if active {
		a.nextWarmPage(now)
	}
	a.tickUpdates(now)
	// The status's clock runs only while it is actually on screen: the open
	// panel's pill counts, a drawn capsule counts, and a capsule paused by
	// hover or a drag holds both its own hold and the panel's deadline.
	seeing := a.status != "" && (a.visible || (a.fb.drawn && a.fb.pausedAt.IsZero()))
	if seeing {
		if a.statusSeenAt.IsZero() {
			a.statusSeenAt = now
		} else {
			a.statusSeen += now.Sub(a.statusSeenAt)
			a.statusSeenAt = now
		}
	} else {
		a.statusSeenAt = time.Time{}
	}
	// A capsule paused mid-hold extends the shared deadline: the toast must
	// not expire while its words are parked off screen.
	until := a.statusUntil
	if !a.fb.pausedAt.IsZero() {
		if f := &a.fb; f.until.After(until) {
			until = f.until
		}
	}
	if !a.statusUntil.IsZero() && now.After(until) {
		a.dismissReadFeedback(now)
		a.status, a.statusUntil, a.statusSeen, a.statusSeenAt = "", time.Time{}, 0, time.Time{}
		if a.win != nil {
			a.win.Invalidate()
		}
	}
	// statusToast 给无期限状态（报错、启动提示）计时：看见满 toastLife 后清掉。
	if a.status != "" && a.statusUntil.IsZero() && a.statusSeen >= toastLife {
		a.dismissReadFeedback(now)
		a.status, a.statusSeen, a.statusSeenAt = "", 0, time.Time{}
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

// pointerNearZone reports whether p belongs to a surface the shell watches
// for hover: the notch or island hot zone, the drop corridor, the peek
// pill, the open-page shapes, a desktop card, the otter or the basket.
func (a *shelf) pointerNearZone(p mygo.Point) bool {
	if a.overAnchor(p) || a.dropBandX(p) || a.overPeekPill(p) || a.overAppsPop(p) {
		return true
	}
	if a.win != nil && (a.overShell(p) || a.overBelow(p)) {
		return true
	}
	if a.snap.panel || a.snap.drawn {
		if insideRect(p, a.snapPanelBox()) {
			return true
		}
	}
	for _, it := range a.pins {
		r := it.frame
		if p.X >= r.X-pinWakeDist && p.X < r.X+r.Width+pinWakeDist &&
			p.Y >= r.Y-pinWakeDist && p.Y < r.Y+r.Height+pinWakeDist {
			return true
		}
	}
	if pet := a.pet; pet != nil {
		r := pet.frame
		if p.X >= r.X-8 && p.X < r.X+r.Width+8 && p.Y >= r.Y-8 && p.Y < r.Y+r.Height+8 {
			return true
		}
	}
	if b := &a.basket; b.shown && insideRect(p, b.frame) {
		return true
	}
	return false
}

// pointerMoved is every global/local mouse move and drag. A move near a
// watched surface switches the loop to the active cadence; a move to
// another display re-homes the overlay; a move far away only refreshes the
// pointer gate and records that the gate may poll for a moment.
func (a *shelf) pointerMoved() {
	if a.quitting {
		return
	}
	now := time.Now()
	a.lastMoveAt = now
	gateMoveUntil.Store(now.Add(gatePollWindow).UnixNano())
	wakeGatePoll()
	p := cursorPoint()
	if !a.visible && a.dispBounds.Width > 0 && !insideRect(p, a.dispBounds) {
		a.place(p)
		a.wakeMonitor()
	}
	near := a.pointerNearZone(p)
	if near != a.pointerNear {
		a.pointerNear = near
		if near {
			a.wakeMonitor()
		}
	}
}
