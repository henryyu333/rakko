//go:build darwin

package main

// The clock: one countdown timer and one stopwatch, both widgets on the
// 工具 page. They run off the shared monitoring tick (no goroutine), keep
// no persisted state, and are driven by wall-clock deadlines so a sleeping
// Mac still reaches its deadline. The timer's expiry is not a live event:
// it owns the closed notch until the user answers 停止 or 重复, while the
// panel's band shows a 停止 chip instead.
//
// Time calculation is the methods' `now` argument; the drawing code reads
// m.clock(), whose seam tests replace with a fake clock.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"rakko/internal/mac"
	"rakko/locales"
)

const (
	// timerDefaultDur is the first 1 × 1 start and the card's first value.
	timerDefaultDur = 5 * time.Minute
	// timerMaxDur is the custom editor's cap: 23:59:59.
	timerMaxDur = 23*time.Hour + 59*time.Minute + 59*time.Second
	// timerRingFor is how long the alert sound loops before it mutes
	// itself; the card stays until answered.
	timerRingFor = time.Minute
)

// timerPhase is the countdown's state. done is the unanswered 时间到 alert.
type timerPhase int

const (
	timerIdle timerPhase = iota
	timerRunning
	timerPaused
	timerDone
)

// timerEdit is the custom-duration editor: three fields and the digits
// typed into the focused one.
type timerEdit struct {
	h, m, s int
	field   int    // 0 hours, 1 minutes, 2 seconds
	raw     string // the focused field's input text
	fresh   bool   // select raw on the next frame, so typing replaces it
}

// clockModule owns both clocks. stopwatch keeps the elapsed time before
// the current run segment; timer stores the deadline, never a count.
type clockModule struct {
	host *shelf
	now  func() time.Time
	// playSound and stopSound are the alert's seams; nil means NSSound.
	playSound func()
	stopSound func()

	phase       timerPhase
	dur         time.Duration
	end         time.Time
	remain      time.Duration // the frozen remainder while paused
	doneAt      time.Time     // when the countdown reached zero
	ringing     bool          // the sound still loops
	lastStarted time.Duration // the 1 × 1 start length

	edit *timerEdit

	watchRun     bool
	watchStart   time.Time
	watchElapsed time.Duration

	lastPaint string
}

// newClockModule wires the clocks into the shell.
func newClockModule(host *shelf) *clockModule {
	return &clockModule{host: host, dur: timerDefaultDur, lastStarted: timerDefaultDur}
}

// timerChip is the countdown's band capsule: a live activity of its own so
// the stopwatch chip can sit beside it.
type timerChip struct{ m *clockModule }

// stopwatchChip is the stopwatch's band capsule.
type stopwatchChip struct{ m *clockModule }

func (t timerChip) tick(time.Time)     {}
func (s stopwatchChip) tick(time.Time) {}

func (t timerChip) live() bool     { return t.m.timerLive() }
func (s stopwatchChip) live() bool { return s.m.watchLive() }
func (t timerChip) nextTick(n time.Time) time.Time {
	return t.m.nextTick(n)
}

func (m *clockModule) clock() time.Time {
	if m != nil && m.now != nil {
		return m.now()
	}
	return time.Now()
}

// refresh asks for one more frame after a state change.
func (m *clockModule) refresh() {
	if m != nil && m.host != nil && m.host.win != nil {
		m.host.win.Invalidate()
	}
}

func (m *clockModule) playing() {
	if m.playSound != nil {
		m.playSound()
		return
	}
	mac.PlayAlertSound()
}

func (m *clockModule) mute() {
	if m.stopSound != nil {
		m.stopSound()
	} else {
		mac.StopAlertSound()
	}
}

// --- countdown ---

// startTimer begins d from now, replacing whatever the countdown held.
func (m *clockModule) startTimer(now time.Time, d time.Duration) {
	if d <= 0 {
		return
	}
	d = min(d, timerMaxDur)
	m.stopAlert()
	m.edit = nil
	m.phase, m.dur, m.end = timerRunning, d, now.Add(d)
	m.lastStarted = d
	m.remain = 0
	m.refresh()
}

// toggleTimer is a tap on the card or the chip: idle starts the last
// length, running pauses, paused resumes, and the unanswered alert stops.
func (m *clockModule) toggleTimer(now time.Time) {
	switch m.phase {
	case timerIdle:
		m.startTimer(now, m.lastStarted)
	case timerRunning:
		m.pauseTimer(now)
	case timerPaused:
		m.resumeTimer(now)
	case timerDone:
		m.stopTimer(now)
	}
}

func (m *clockModule) pauseTimer(now time.Time) {
	if m.phase != timerRunning {
		return
	}
	rem := m.end.Sub(now)
	if !now.Before(m.end) {
		m.finish(now)
		return
	}
	m.phase, m.remain = timerPaused, rem
	m.refresh()
}

func (m *clockModule) resumeTimer(now time.Time) {
	if m.phase != timerPaused {
		return
	}
	m.phase, m.end = timerRunning, now.Add(m.remain)
	m.refresh()
}

// stopTimer closes the alert, or cancels a running countdown, back to idle.
func (m *clockModule) stopTimer(now time.Time) {
	m.stopAlert()
	m.phase, m.end, m.remain = timerIdle, time.Time{}, 0
	m.refresh()
}

// repeatTimer starts the same length again from now.
func (m *clockModule) repeatTimer(now time.Time) {
	m.startTimer(now, m.dur)
}

// stopAlert silences the sound but keeps the timer's state.
func (m *clockModule) stopAlert() {
	if m.ringing {
		m.ringing = false
		m.mute()
	}
}

// finish turns a running countdown that reached zero into the alert.
func (m *clockModule) finish(now time.Time) {
	if m.phase == timerDone {
		return
	}
	m.phase, m.remain = timerDone, 0
	m.doneAt, m.ringing = now, true
	m.playing()
	m.refresh()
}

// tick advances the deadline and the one-minute mute; the panel redraws
// when its second changed.
func (m *clockModule) tick(now time.Time) {
	before := m.paintKey(now)
	if m.phase == timerRunning && !now.Before(m.end) {
		m.finish(now)
	}
	if m.phase == timerDone && m.ringing && !now.Before(m.doneAt.Add(timerRingFor)) {
		m.stopAlert()
		m.refresh()
	}
	if m.host == nil {
		return
	}
	shown := m.host.visible || m.host.peeking || m.host.peekProgress > 0.01
	if shown && m.paintKey(now) != before {
		m.refresh()
	}
}

// paintKey is the second-granular text the panel shows, so tick repaints
// only when a drawn number moved.
func (m *clockModule) paintKey(now time.Time) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(int(m.phase)))
	if m.phase != timerIdle {
		b.WriteString(clockTextCeil(m.timerRemainAt(now)))
	}
	if m.watchRun || m.watchElapsed > 0 {
		b.WriteString(clockText(m.watchElapsedAt(now)))
	}
	if m.edit != nil {
		b.WriteString("e")
		b.WriteString(strconv.Itoa(m.edit.field))
	}
	return b.String()
}

// nextTick is the countdown's deadline, the alert's mute, or the next
// second boundary while a clock is on screen.
func (m *clockModule) nextTick(now time.Time) time.Time {
	var next time.Time
	add := func(at time.Time) {
		if at.IsZero() {
			return
		}
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	if m.phase == timerRunning {
		add(m.end)
	}
	if m.phase == timerDone && m.ringing {
		add(m.doneAt.Add(timerRingFor))
	}
	if m.host != nil && (m.host.visible || m.host.peeking || m.host.peekProgress > 0.01) {
		if m.phase == timerRunning || m.watchRun {
			add(now.Truncate(time.Second).Add(time.Second))
		}
	}
	return next
}

// timerRemainAt is the countdown's remaining time in each phase: fresh
// from the deadline while running, the frozen remainder while paused.
func (m *clockModule) timerRemainAt(now time.Time) time.Duration {
	switch m.phase {
	case timerRunning:
		rem := m.end.Sub(now)
		if rem < 0 {
			return 0
		}
		return rem
	case timerPaused:
		return m.remain
	case timerDone:
		return 0
	}
	return m.dur
}

// timerFracAt is the remaining share of the chosen length, for the rail
// and the rings.
func (m *clockModule) timerFracAt(now time.Time) float32 {
	if m.dur <= 0 {
		return 0
	}
	frac := float32(m.timerRemainAt(now)) / float32(m.dur)
	return min(max(frac, 0), 1)
}

// timerLive is whether the band shows the countdown chip: running, paused
// or waiting to be answered.
func (m *clockModule) timerLive() bool {
	return m != nil && (m.phase == timerRunning || m.phase == timerPaused || m.phase == timerDone)
}

// timerWingsUp is whether the closed notch shows the countdown's wings.
func (m *clockModule) timerWingsUp() bool {
	return m != nil && (m.phase == timerRunning || m.phase == timerPaused)
}

// alarmUp is whether the unanswered alert owns the closed shell.
func (m *clockModule) alarmUp() bool { return m != nil && m.phase == timerDone }

// clockAlarmFree is alarmUp for the shelf, without an approval card's
// claim: that card is the one thing the alert yields to.
func (a *shelf) clockAlarmFree() bool {
	return a.clock != nil && a.clock.alarmUp() && !a.visible
}

// clockAlarmUp reports whether the alert is the shape to draw now. An
// active approval card shows first; the alert returns once it is answered.
func (a *shelf) clockAlarmUp(now time.Time) bool {
	if !a.clockAlarmFree() {
		return false
	}
	if a.live.shown && a.live.cur.kind == liveAgentAsk && now.Before(a.live.until) {
		return false
	}
	return true
}

// --- stopwatch ---

func (m *clockModule) startWatch(now time.Time) {
	if m.watchRun {
		return
	}
	m.watchRun, m.watchStart = true, now
	m.refresh()
}

func (m *clockModule) pauseWatch(now time.Time) {
	if !m.watchRun {
		return
	}
	m.watchElapsed += now.Sub(m.watchStart)
	m.watchRun = false
	m.refresh()
}

func (m *clockModule) resumeWatch(now time.Time) {
	if m.watchRun {
		return
	}
	m.watchRun, m.watchStart = true, now
	m.refresh()
}

// toggleWatch is a card tap: idle starts, running pauses, paused resumes.
func (m *clockModule) toggleWatch(now time.Time) {
	switch {
	case m.watchRun:
		m.pauseWatch(now)
	case m.watchElapsed > 0:
		m.resumeWatch(now)
	default:
		m.startWatch(now)
	}
}

func (m *clockModule) resetWatch() {
	m.watchRun, m.watchElapsed = false, 0
	m.refresh()
}

func (m *clockModule) watchElapsedAt(now time.Time) time.Duration {
	if m.watchRun {
		return m.watchElapsed + now.Sub(m.watchStart)
	}
	return m.watchElapsed
}

// watchLive is whether the band shows the stopwatch chip.
func (m *clockModule) watchLive() bool {
	return m != nil && (m.watchRun || m.watchElapsed > 0)
}

// --- custom duration editor ---

// value is the edited duration; 0 is not a valid start.
func (e *timerEdit) value() time.Duration {
	return time.Duration(e.h)*time.Hour + time.Duration(e.m)*time.Minute + time.Duration(e.s)*time.Second
}

// beginEdit opens the three-field editor with the countdown's current
// length and takes the keyboard, like the clipboard's search field.
func (m *clockModule) beginEdit(now time.Time) {
	if m.phase != timerIdle || m.edit != nil {
		return
	}
	d := m.dur
	h := int(d / time.Hour)
	mi := int(d % time.Hour / time.Minute)
	s := int(d % time.Minute / time.Second)
	field := 1
	if h > 0 {
		field = 0
	}
	e := &timerEdit{h: h, m: mi, s: s, field: field, fresh: true}
	e.raw = e.text()
	m.edit = e
	if m.host != nil {
		m.host.takeKeyboard()
	}
	m.refresh()
}

// endEdit closes the editor without starting anything.
func (m *clockModule) endEdit() {
	if m.edit == nil {
		return
	}
	m.edit = nil
	m.refresh()
}

func (m *clockModule) editing() bool { return m != nil && m.edit != nil }

// editField returns a pointer to the focused field, with its maximum.
func (e *timerEdit) fieldValue() (val, maxv int) {
	switch e.field {
	case 0:
		return e.h, 23
	case 1:
		return e.m, 59
	}
	return e.s, 59
}

func (e *timerEdit) setField(v int) {
	switch e.field {
	case 0:
		e.h = v
	case 1:
		e.m = v
	default:
		e.s = v
	}
}

// text is the focused field as two digits.
func (e *timerEdit) text() string {
	v, _ := e.fieldValue()
	return pad2(v)
}

// nudge moves the focused field by delta, wrapping round its range.
func (e *timerEdit) nudge(delta int) {
	v, maxv := e.fieldValue()
	v = (v + delta) % (maxv + 1)
	if v < 0 {
		v += maxv + 1
	}
	e.setField(v)
	e.raw, e.fresh = e.text(), true
}

// moveField shifts the focus left or right (or wraps with Tab).
func (e *timerEdit) moveField(delta int) {
	e.field = (e.field + delta + 3) % 3
	e.raw, e.fresh = e.text(), true
}

// typeDigits applies the digits in raw to the focused field. A third
// digit replaces the value with the last one typed, and an out-of-range
// value falls back to its last digit (9 then 9 makes 9, not 59).
func (e *timerEdit) typeDigits() {
	digits := e.raw
	var b strings.Builder
	for _, r := range digits {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits = b.String()
	if digits == "" {
		e.setField(0)
		return
	}
	if len(digits) > 2 {
		digits = digits[len(digits)-2:]
		e.raw = digits
	}
	v, _ := strconv.Atoi(digits)
	_, maxv := e.fieldValue()
	if v > maxv {
		v = int(digits[len(digits)-1] - '0')
		e.raw = strconv.Itoa(v)
	}
	e.setField(v)
	e.fresh = false
}

// --- display ---

// clockText writes m:ss below an hour and h:mm:ss above it, rounding the
// fraction down (the stopwatch's elapsed time).
func clockText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return clockSeconds(int(d / time.Second))
}

// clockTextCeil rounds up: a countdown still shows 5:00 for its first
// fraction of a second and reaches 0:00 only at the deadline.
func clockTextCeil(d time.Duration) string {
	if d <= 0 {
		return "0:00"
	}
	s := int(d / time.Second)
	if d%time.Second != 0 {
		s++
	}
	return clockSeconds(s)
}

func clockSeconds(s int) string {
	h, m, x := s/3600, s%3600/60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, x)
	}
	return fmt.Sprintf("%d:%02d", m, x)
}

// timerCardTitle is the countdown card's accessible name.
func (m *clockModule) timerCardTitle(l locales.Messages) string {
	if m == nil {
		return l.CardTimer()
	}
	now := m.clock()
	switch m.phase {
	case timerRunning:
		return l.TimerChipLabel(clockTextCeil(m.timerRemainAt(now)))
	case timerPaused:
		return l.TimerChipPausedLabel(clockTextCeil(m.timerRemainAt(now)))
	case timerDone:
		return l.TimerChipDone()
	}
	return l.CardTimer()
}

// watchCardTitle is the stopwatch card's accessible name.
func (m *clockModule) watchCardTitle(l locales.Messages) string {
	if m == nil {
		return l.CardStopwatch()
	}
	now := m.clock()
	if m.watchRun {
		return l.StopwatchElapsed(clockText(m.watchElapsedAt(now)))
	}
	if m.watchElapsed > 0 {
		return l.StopwatchPaused(clockText(m.watchElapsedAt(now)))
	}
	return l.CardStopwatch()
}
