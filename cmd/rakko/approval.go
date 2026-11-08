//go:build darwin

package main

// Approvals on the notch: an agent's permission request becomes a card,
// and the hook process waits for one of three answers. The card never
// answers by itself: allowed only on a click or a deliberate hold, denied
// only on a click, and every timeout, disconnect or switch-off hands the
// decision back to the tool's own flow.

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
)

// approvalWait is how long a card stays before it is handed back to the
// terminal. It is below the client's 55 s budget and the hook's 60 s.
var approvalWait = 45 * time.Second

// approvalResultHold is how long an answer stays on the card.
const (
	approvalResultHold   = 1200 * time.Millisecond
	approvalExpiredHold  = 800 * time.Millisecond
	approvalTerminalHold = 800 * time.Millisecond
	// approvalMaxPending bounds the requests waiting in Rakko. The ninth
	// is passed straight back.
	approvalMaxPending = 8
	// approvalDuplicateWindow is how close two identical requests must be
	// to count as one report from two injection paths.
	approvalDuplicateWindow = 2 * time.Second
	// approvalArmedWindow is the second press' window in the two-step
	// keyboard allow for a risky request.
	approvalArmedWindow = 800 * time.Millisecond
	// approvalHoldDelay is the deliberate hold for a risky allow.
	approvalHoldDelay = 600 * time.Millisecond
)

type approvalState int

const (
	approvalShown approvalState = iota
	approvalHolding
	approvalArmed
	approvalDenying
	approvalAllowed
	approvalDenied
	approvalExpired
	approvalTerminal
	approvalWithdrawn
)

// active reports whether the card may still take a decision.
func (s approvalState) active() bool {
	return s == approvalShown || s == approvalHolding || s == approvalArmed || s == approvalDenying
}

// approval is one permission request and the card drawn for it.
type approval struct {
	id       string
	ask      agentnotify.Ask
	analysis agentnotify.Analysis
	done     chan agentnotify.Decision
	at       time.Time
	deadline time.Time

	state       approvalState
	verdict     string
	reason      string
	expanded    bool
	holdFrom    time.Time
	armedUntil  time.Time
	resultUntil time.Time
	inPending   bool
	scroll      ui.ScrollState
}

// approvalFeature owns the waiting requests and the settings group's last
// known state. Requests arrive on the socket's goroutine and are admitted
// on the main thread.
type approvalFeature struct {
	host *shelf
	home string

	mu        sync.Mutex
	pending   []*approval
	ids       map[string]*approval
	states    map[agentnotify.Tool]agentnotify.State
	notes     map[agentnotify.Tool]string
	failed    map[agentnotify.Tool]string
	refreshed time.Time

	enabled atomic.Bool
	// keyHeld is the card's ⌘↩ registration; main thread only.
	keyHeld bool
}

func newApprovalFeature(a *shelf) *approvalFeature {
	home, _ := agentNotifyHome()
	f := &approvalFeature{
		host: a, home: home,
		ids:    map[string]*approval{},
		states: map[agentnotify.Tool]agentnotify.State{},
		notes:  map[agentnotify.Tool]string{},
		failed: map[agentnotify.Tool]string{},
	}
	f.enabled.Store(a.settings.Approval.Enabled)
	return f
}

func (a *shelf) approvalFeature() *approvalFeature {
	if a.approval == nil {
		a.approval = newApprovalFeature(a)
	}
	return a.approval
}

// main runs fn on the app's main thread. agentRunOnMain is the test seam.
func (f *approvalFeature) main(fn func()) { agentRunOnMain(fn) }

// ask is the socket's blocking side: it admits the request on the main
// thread, then waits for an answer, a timeout or the peer hanging up.
func (f *approvalFeature) ask(ctx context.Context, a agentnotify.Ask) agentnotify.Decision {
	if !f.enabled.Load() {
		return agentnotify.Decision{ID: a.ID, Decision: agentnotify.DecisionPass}
	}
	an, ok := agentnotify.Analyze(a.Tool, a.ToolName, a.Input)
	if !ok {
		return agentnotify.Decision{ID: a.ID, Decision: agentnotify.DecisionPass}
	}
	now := time.Now()
	ap := &approval{
		id: a.ID, ask: a, analysis: an,
		done:     make(chan agentnotify.Decision, 1),
		at:       now,
		deadline: now.Add(approvalWait),
		state:    approvalShown,
	}
	if !f.admit(ctx, ap) {
		return agentnotify.Decision{ID: a.ID, Decision: agentnotify.DecisionPass}
	}
	return f.wait(ctx, ap)
}

// wait blocks one admitted request until it is answered, expires or the
// hook hangs up. It is ask's second half, so tests can admit on the test
// thread and then wait without a second goroutine touching the queue.
func (f *approvalFeature) wait(ctx context.Context, ap *approval) agentnotify.Decision {
	a := ap.ask
	timer := time.NewTimer(time.Until(ap.deadline))
	defer timer.Stop()
	pass := agentnotify.Decision{ID: a.ID, Decision: agentnotify.DecisionPass}
	select {
	case d := <-ap.done:
		return d
	case <-ctx.Done():
		f.main(func() { f.finish(ap, pass, approvalWithdrawn, time.Now()) })
		return pass
	case <-timer.C:
		f.main(func() { f.finish(ap, pass, approvalExpired, time.Now()) })
		return pass
	}
}

// admit puts the request in the queue on the main thread. False means the
// app is not taking requests (capacity, switch, shutdown).
func (f *approvalFeature) admit(ctx context.Context, ap *approval) bool {
	ch := make(chan bool, 1)
	f.main(func() { ch <- f.enqueue(ap) })
	select {
	case ok := <-ch:
		return ok
	case <-ctx.Done():
		f.main(func() {
			f.finish(ap, agentnotify.Decision{ID: ap.id, Decision: agentnotify.DecisionPass}, approvalWithdrawn, time.Now())
		})
		return false
	}
}

// enqueue (main thread) appends the card, folds an open panel, and starts
// the queue. A duplicate of a request already waiting is passed back and
// replaced by this newer one.
func (f *approvalFeature) enqueue(ap *approval) bool {
	if !f.enabled.Load() {
		return false
	}
	f.mu.Lock()
	if len(f.pending) >= approvalMaxPending {
		f.mu.Unlock()
		return false
	}
	dupes := make([]*approval, 0, 1)
	for _, old := range f.pending {
		if old.id == ap.id {
			continue
		}
		if sameApproval(old, ap) && ap.at.Sub(old.at) < approvalDuplicateWindow {
			dupes = append(dupes, old)
		}
	}
	f.mu.Unlock()
	for _, old := range dupes {
		f.finish(old, agentnotify.Decision{ID: old.id, Decision: agentnotify.DecisionPass}, approvalWithdrawn, ap.at)
	}

	f.mu.Lock()
	f.pending = append(f.pending, ap)
	ap.inPending = true
	f.ids[ap.id] = ap
	f.mu.Unlock()

	// The card owns the notch: the panel folds before it appears and is not
	// reopened when the answer is in.
	if f.host.visible {
		f.host.hide(hidePassive)
	}
	f.host.pushLive(liveEvent{kind: liveAgentAsk, ask: ap}, approvalWait, ap.at)
	return true
}

// sameApproval reports whether two requests describe the same tool call.
func sameApproval(a, b *approval) bool {
	return a.ask.Tool == b.ask.Tool && a.ask.Session == b.ask.Session &&
		a.ask.ToolName == b.ask.ToolName && a.analysis.Description == b.analysis.Description
}

// decide (main thread) answers one card. Only an active card may decide.
func (f *approvalFeature) decide(id, verdict, reason string) {
	f.mu.Lock()
	ap := f.ids[id]
	f.mu.Unlock()
	if ap == nil || !ap.state.active() {
		return
	}
	d := agentnotify.Decision{ID: id, Decision: verdict, Message: cleanReason(reason)}
	st := approvalAllowed
	switch verdict {
	case agentnotify.DecisionDeny:
		st = approvalDenied
	case agentnotify.DecisionPass:
		st = approvalTerminal
	}
	f.finish(ap, d, st, time.Now())
}

// finish (main thread) records the answer, wakes the waiting hook and turns
// the card into its result state. The decision channel is buffered, so a
// hook that already left does not block it.
func (f *approvalFeature) finish(ap *approval, d agentnotify.Decision, st approvalState, now time.Time) {
	f.mu.Lock()
	if !ap.state.active() {
		f.mu.Unlock()
		return
	}
	wasDenying := ap.state == approvalDenying
	ap.state = st
	ap.verdict = d.Decision
	hold := approvalResultHold
	switch st {
	case approvalWithdrawn:
		hold = 0
	case approvalExpired:
		hold = approvalExpiredHold
	case approvalTerminal:
		hold = approvalTerminalHold
	}
	ap.resultUntil = now.Add(hold)
	if ap.inPending {
		for i, p := range f.pending {
			if p == ap {
				f.pending = append(f.pending[:i], f.pending[i+1:]...)
				break
			}
		}
		ap.inPending = false
	}
	delete(f.ids, ap.id)
	f.mu.Unlock()

	a := f.host
	// The reason field was the only reason this card took the keyboard;
	// the answer (or the timeout) hands it back to the terminal.
	if wasDenying && a.focusMode {
		a.leaveFocusMode()
	}
	if a.live.cur.ask == ap {
		if st == approvalWithdrawn {
			a.live.shown = false
			a.live.cur = liveEvent{}
			a.live.until = time.Time{}
			a.live.coveredAt = time.Time{}
		} else {
			a.live.until = ap.resultUntil
		}
	} else {
		f.removeQueued(ap)
	}
	f.syncKey()
	if a.win != nil {
		a.win.Invalidate()
	}
	// The waiting hook hears last, once the card's state has settled: a
	// returned ask never races the next frame's read.
	select {
	case ap.done <- d:
	default:
	}
}

// failPending (main thread) hands every waiting request back to its tool.
func (f *approvalFeature) failPending(now time.Time) {
	f.mu.Lock()
	pending := append([]*approval(nil), f.pending...)
	f.mu.Unlock()
	for _, ap := range pending {
		f.finish(ap, agentnotify.Decision{ID: ap.id, Decision: agentnotify.DecisionPass}, approvalWithdrawn, now)
	}
}

// removeQueued drops a card that is still waiting behind another.
func (f *approvalFeature) removeQueued(ap *approval) {
	q := &f.host.live.queued
	for i, ev := range *q {
		if ev.ask == ap {
			*q = append((*q)[:i], (*q)[i+1:]...)
			break
		}
	}
}

// currentAsk (main thread) is the card on screen, if any.
func (f *approvalFeature) currentAsk() *approval {
	if f.host.live.shown && f.host.live.cur.kind == liveAgentAsk {
		return f.host.live.cur.ask
	}
	return nil
}

// queueHead reports whether a card is up and still taking a decision.
func (f *approvalFeature) queueHead() *approval {
	ap := f.currentAsk()
	if ap != nil && ap.state.active() {
		return ap
	}
	return nil
}

// queueCount is how many further requests wait behind ap (the +N badge).
func (f *approvalFeature) queueCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.pending)
	for _, p := range f.pending {
		if p.id == id {
			n--
		}
	}
	return n
}

// keyAllow (main thread) is ⌘↩. A risky request needs a second press
// within 800 ms; a plain one is allowed at once.
func (f *approvalFeature) keyAllow() {
	ap := f.queueHead()
	if ap == nil {
		return
	}
	now := time.Now()
	if len(ap.analysis.Risks) == 0 {
		f.decide(ap.id, agentnotify.DecisionAllow, "")
		return
	}
	switch ap.state {
	case approvalArmed:
		if now.Before(ap.armedUntil) {
			f.decide(ap.id, agentnotify.DecisionAllow, "")
		} else {
			ap.state = approvalShown
		}
	case approvalShown:
		ap.state = approvalArmed
		ap.armedUntil = now.Add(approvalArmedWindow)
	}
	if f.host.win != nil {
		f.host.win.Invalidate()
	}
}

// syncKey keeps ⌘↩ registered exactly while a card can take it. A busy
// shortcut is a silent downgrade to the pointer.
func (f *approvalFeature) syncKey() {
	if f.host.win == nil {
		return // headless: nothing can own a global shortcut
	}
	active := f.queueHead() != nil
	if active && !f.keyHeld {
		if registerKey("Cmd+Return", f.keyAllow) == nil {
			f.keyHeld = true
		}
		return
	}
	if !active && f.keyHeld {
		unregisterKey("Cmd+Return")
		f.keyHeld = false
	}
}

// holdProgress is the risky allow button's fill, 0…1.
func (ap *approval) holdProgress(now time.Time) float32 {
	if ap.state != approvalHolding || ap.holdFrom.IsZero() {
		return 0
	}
	return min(float32(now.Sub(ap.holdFrom))/float32(approvalHoldDelay), 1)
}

// holdTick (main thread) advances the deliberate hold. It returns true
// when the hold completed.
func (f *approvalFeature) holdTick(ap *approval, now time.Time) bool {
	if ap.state != approvalHolding || ap.holdFrom.IsZero() {
		return false
	}
	if now.Sub(ap.holdFrom) >= approvalHoldDelay {
		f.decide(ap.id, agentnotify.DecisionAllow, "")
		return true
	}
	return false
}

// holdRelease abandons a hold that did not fill: no progress is kept.
func (ap *approval) holdRelease() {
	if ap.state == approvalHolding {
		ap.state = approvalShown
		ap.holdFrom = time.Time{}
	}
}

// switch methods

// setApproval saves the group switch and applies it.
func (a *shelf) setApproval(on bool) {
	next := a.settings
	next.Approval.Enabled = on
	if !a.applySettings(next) {
		return
	}
	a.approvalApply(on)
}

// setApprovalTool stores one tool's approval switch and applies it alone.
func (a *shelf) setApprovalTool(tool agentnotify.Tool, on bool) {
	next := a.settings
	next.Approval.SetToolOn(string(tool), on)
	if !a.applySettings(next) {
		return
	}
	f := a.approvalFeature()
	var err error
	if on {
		a.agentFeature().start()
		if exe, xerr := agentNotifyExe(); xerr == nil && agentnotify.StableExe(exe) {
			err = agentnotify.ConnectApprove(f.home, f.host.dataDir, exe, tool)
		}
	} else {
		err = agentnotify.DisconnectApprove(f.home, f.host.dataDir, tool)
	}
	f.mu.Lock()
	if err != nil {
		f.failed[tool] = err.Error()
	} else {
		delete(f.failed, tool)
	}
	f.mu.Unlock()
	f.refresh()
}

// approvalApply is the group switch's side effects, shared with startup.
func (a *shelf) approvalApply(on bool) {
	f := a.approvalFeature()
	f.enabled.Store(on)
	if !on {
		failed := map[agentnotify.Tool]string{}
		for _, tool := range agentnotify.Tools() {
			if !tool.Approvable() {
				continue
			}
			if err := agentnotify.DisconnectApprove(f.home, f.host.dataDir, tool); err != nil {
				failed[tool] = err.Error()
			}
		}
		f.mu.Lock()
		f.failed = failed
		f.mu.Unlock()
		f.failPending(time.Now())
		if !a.settings.AgentNotify.Enabled {
			a.agentFeature().stop()
		}
		f.refresh()
		return
	}
	a.agentFeature().start()
	exe, err := agentNotifyExe()
	if err != nil {
		exe = ""
	}
	if exe != "" && !agentnotify.StableExe(exe) {
		exe = "" // a development build: nothing may be written
	}
	failed := map[agentnotify.Tool]string{}
	if exe != "" {
		for _, tool := range f.detected() {
			if a.settings.Approval.ToolOff(string(tool)) {
				if err := agentnotify.DisconnectApprove(f.home, f.host.dataDir, tool); err != nil {
					failed[tool] = err.Error()
				}
				continue
			}
			if err := agentnotify.ConnectApprove(f.home, f.host.dataDir, exe, tool); err != nil {
				failed[tool] = err.Error()
			}
		}
	}
	f.mu.Lock()
	f.failed = failed
	f.mu.Unlock()
	f.refresh()
}

// approvalStartEnabled starts the socket at launch when the switch is on.
// It writes nothing: entries belong to the switch being turned on.
func (a *shelf) approvalStartEnabled() {
	if !a.settings.Approval.Enabled {
		return
	}
	f := a.approvalFeature()
	f.enabled.Store(true)
	a.agentFeature().start()
	f.refresh()
}

// refresh recomputes the settings rows' states.
func (f *approvalFeature) refresh() {
	exe, _ := agentNotifyExe()
	stable := exe != "" && agentnotify.StableExe(exe)
	on := f.host.settings.Approval.Enabled
	states := map[agentnotify.Tool]agentnotify.State{}
	notes := map[agentnotify.Tool]string{}
	f.mu.Lock()
	failed := make(map[agentnotify.Tool]string, len(f.failed))
	for tool, why := range f.failed {
		failed[tool] = why
	}
	f.mu.Unlock()
	for _, tool := range f.detected() {
		st, note := agentnotify.StateNotInjected, ""
		if on {
			st, note = agentnotify.StatusApprove(f.home, tool)
			if st != agentnotify.StateInjected && !stable {
				st, note = agentnotify.StateError, agentNeedsAppNote
			}
		}
		if why, bad := failed[tool]; bad {
			st, note = agentnotify.StateError, why
		}
		states[tool] = st
		if note != "" {
			notes[tool] = note
		}
	}
	f.mu.Lock()
	f.states, f.notes, f.refreshed = states, notes, time.Now()
	f.mu.Unlock()
}

// refreshIfStale recomputes the rows at most once a second while the
// settings page redraws.
func (f *approvalFeature) refreshIfStale() {
	f.mu.Lock()
	stale := f.states == nil || time.Since(f.refreshed) > time.Second
	f.mu.Unlock()
	if stale {
		f.refresh()
	}
}

// settingRow is one detected tool's row state and note.
func (f *approvalFeature) settingRow(tool agentnotify.Tool) (agentnotify.State, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states[tool], f.notes[tool]
}

// detected lists installed, approvable tools in settings order.
func (f *approvalFeature) detected() []agentnotify.Tool {
	var out []agentnotify.Tool
	for _, tool := range agentnotify.Tools() {
		if tool.Approvable() && agentnotify.Installed(f.home, tool) {
			out = append(out, tool)
		}
	}
	return out
}

// cleanReason compresses a typed deny reason to one line of at most 500
// characters, the same shape the hook writes for the agent.
func cleanReason(reason string) string {
	s := strings.Join(strings.Fields(reason), " ")
	if r := []rune(s); len(r) > 500 {
		return string(r[:500])
	}
	return s
}

// approvalProject is the project name on the first line: the cwd's last
// element, or "~" when there is none.
func approvalProject(cwd string) string {
	base := filepath.Base(cwd)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "~"
	}
	return base
}
