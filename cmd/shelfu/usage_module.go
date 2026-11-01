//go:build darwin

package main

// AI 用量页的数据侧：刷新节奏、后台取数、回主线程发布。页面绘制在
// usage_page.go。取数和扫描的具体实现由别的部分提供，这里只依赖
// internal/aiusage 的类型和注入进来的函数，所以页面可以在没有真数据源
// 的测试里跑通整条刷新链路。
//
// 线程模型（同 updater.go）：慢活（查额度、扫本地记录）跑在 goWork 的
// goroutine 上，结果经 dispatchMain 回主线程再写入 f.* 状态并
// Invalidate；UI、设置和调度只在主线程读写。首次扫描的进度条是唯一后台
// 直写的状态，走 atomic。

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"shelf/internal/aiusage"
	"shelf/internal/settings"
)

const (
	// usageLocalInterval is how often the local token summary is rebuilt
	// while the page shows; a page visit refreshes it at once when stale.
	usageLocalInterval = 60 * time.Second
	// usageQuotaInterval is how often an automatic refresh asks a quota
	// endpoint: Codex at most this fast. A server RetryAfter holds a tool
	// back longer.
	usageQuotaInterval = 5 * time.Minute
	// usageQuotaManual is the shortest gap a manual refresh asks the Codex
	// quota endpoint again.
	usageQuotaManual = 60 * time.Second
	// usageClaudeFloor is the fastest any Claude Code /usage read runs,
	// however it was started (page open, refresh button, timer): each read
	// is a whole Claude Code process, and Magpie holds the same 30s floor.
	usageClaudeFloor = 30 * time.Second
	// usageClaudeProbeGap is the fastest the "was Claude Code used since
	// the last read" projects scan repeats. The tick driving it runs every
	// 30ms; the tree walk must not be part of that rate however idle the
	// allowance is.
	usageClaudeProbeGap = time.Minute
	// usageQuotaTimeout and usageScanTimeout bound one round of work so a
	// stalled source frees its busy flag instead of silencing refreshes.
	usageQuotaTimeout = 20 * time.Second
	usageScanTimeout  = 10 * time.Minute
)

// usageDeps injects the data collectors so the page never imports their
// implementations: quota fetches, the even-pace estimator, the local
// scanner and the clock (all swappable by tests).
type usageDeps struct {
	fetchCodex  func(context.Context) aiusage.Quota
	fetchClaude func(context.Context) aiusage.Quota
	pace        func(aiusage.Window, time.Time) aiusage.PaceInfo
	// claudeUsedSince reports whether Claude Code wrote a session log
	// after t; an unused allowance is not worth an automatic /usage read.
	// claudeWait is the random whole-minute gap before the next one.
	claudeUsedSince func(time.Time) bool
	claudeWait      func() time.Duration
	// claudeProbe starts the background projects scan (defaults to
	// goWork); tests replace it to decide when the scan runs.
	claudeProbe func(func())
	// loadLast/saveLast keep the last good reading across restarts.
	loadLast func() []aiusage.Quota
	saveLast func(aiusage.Quota)
	// newScanner builds a fresh local scanner per refresh; every
	// supported reader is on and the on-disk incremental cache makes a
	// rebuild cheap.
	newScanner func() usageScanner
	// home is the directory the readers and the settings probe inspect.
	home string
	now  func() time.Time
}

// usageScanner is the local record store slice B provides: Scan walks new
// records (incremental after the first pass) and Summary aggregates one
// range of what has been scanned so far.
type usageScanner interface {
	Scan(ctx context.Context, progress func(done, total int64)) error
	Summary(r aiusage.Range, now time.Time) aiusage.Summary
}

// usageQuotaState keeps the latest answer and the last usable windows.
// attemptAt enforces each provider's cadence; retryTill is server backoff.
type usageQuotaState struct {
	latest, good aiusage.Quota
	attemptAt    time.Time // when the last request was dispatched
	ask          bool      // the user asked to see a fresh Claude reading
	nextAt       time.Time // Claude's next unasked read is due
	retryTill    time.Time // Codex's server RetryAfter
	busy         bool
	generation   int // distinguishes dispatches when fake-clock times tie

	// Claude's "was Claude Code used since attemptAt" probe: the answer
	// costs a projects walk, so it runs in the background, one at a time,
	// no faster than usageClaudeProbeGap. usedFor records which attempt
	// the answer belongs to.
	usedProbing bool
	usedProbeAt time.Time
	usedFor     time.Time
	usedSince   bool
}

// usageFeature is the AI 用量 page: the panelPage whose ear icon and stage
// usage_page.go draws, and the module that schedules refreshes from the
// monitor tick.
type usageFeature struct {
	host *shelf
	deps usageDeps

	quota      [2]usageQuotaState // indexed by aiusage.Tool, via quotaIndex
	scanDone   atomic.Int64
	scanTotal  atomic.Int64
	localBusy  bool
	scanned    bool      // a local pass finished at least once (errors count)
	localErr   error     // last local scan/summary error, nil after success
	localAt    time.Time // last successful local refresh (drives 「N 前更新」)
	lastLocal  time.Time // last local refresh attempt (schedules the next)
	summary    aiusage.Summary
	hasSummary bool
	rangeSel   aiusage.Range
	wasVisible bool
	hoverTool  aiusage.Tool // the quota card under the pointer this frame

	// agents is the settings page's last probe of every reader.
	agents   []aiusage.AgentStatus
	agentsAt time.Time

	// workGen/workCancel retire local scans and cancel in-flight fetches.
	// Completed quota answers remain useful after a page change.
	workGen    int
	workCtx    context.Context
	workCancel context.CancelFunc
}

// newUsageFeature wires the feature; integration registers it as a page
// and appends it to the modules.
func newUsageFeature(a *shelf, deps usageDeps) *usageFeature {
	f := &usageFeature{host: a, deps: deps}
	a.usage = f // hide/setTab/setAIUsage retire its work synchronously
	if f.deps.now == nil {
		f.deps.now = time.Now
	}
	if f.deps.claudeWait == nil {
		f.deps.claudeWait = defaultClaudeWait
	}
	if f.deps.loadLast != nil {
		for _, q := range f.deps.loadLast() {
			if q.Status != aiusage.QuotaOK || len(q.Windows) == 0 {
				continue
			}
			st := &f.quota[quotaIndex(q.Tool)]
			st.latest, st.good = q, q
		}
	}
	return f
}

// defaultClaudeWait is the next unasked /usage read's gap: a whole minute
// from 5 to 15, drawn afresh after each run as Magpie does.
func defaultClaudeWait() time.Duration {
	return (5 + time.Duration(rand.N(11))) * time.Minute
}

func (f *usageFeature) cfg() *settings.AIUsageSettings { return &f.host.settings.AIUsage }

// quotaIndex maps the quota tools onto the fixed [2]state slots.
func quotaIndex(t aiusage.Tool) int {
	if t == aiusage.ToolClaude {
		return 1
	}
	return 0
}

// quotaTools is the card order: Codex first, then Claude Code (设计稿).
func quotaTools() []aiusage.Tool { return []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude} }

// localTools is the local-usage order: omp, Codex, then Claude Code.
func localTools() []aiusage.Tool {
	return []aiusage.Tool{aiusage.ToolOmp, aiusage.ToolCodex, aiusage.ToolClaude}
}

// quotaOn reports whether tool's quota card is enabled.
func (f *usageFeature) quotaOn(t aiusage.Tool) bool {
	cfg := f.cfg()
	if t == aiusage.ToolClaude {
		return cfg.QuotaClaude && f.deps.fetchClaude != nil
	}
	return cfg.QuotaCodex && f.deps.fetchCodex != nil
}

// localOn reports whether the local usage section can draw: it needs a
// scanner, and every supported reader is on.
func (f *usageFeature) localOn() bool {
	return f.deps.newScanner != nil
}

// agentStatuses is the settings page's 本机用量 list, probed at most every
// 30 seconds so drawing the page does not stat every reader each frame.
func (f *usageFeature) agentStatuses() []aiusage.AgentStatus {
	now := f.deps.now()
	if f.agents != nil && now.Sub(f.agentsAt) < 30*time.Second {
		return f.agents
	}
	f.agents = aiusage.Probe(f.deps.home)
	f.agentsAt = now
	return f.agents
}

// usageVisible reports whether the AI 用量 page is the one the open panel
// shows: refreshes only happen while the user can see them.
func (f *usageFeature) usageVisible() bool {
	return f.host.visible && f.host.currentPage().id() == pageUsage
}

// workContext returns the background context current work runs under; it is
// (re)created after stopWork cancelled the previous one. Main thread only.
func (f *usageFeature) workContext() context.Context {
	if f.workCtx == nil || f.workCtx.Err() != nil {
		f.workCtx, f.workCancel = context.WithCancel(context.Background())
	}
	return f.workCtx
}

// stopWork retires everything running in the background: the shared
// context is cancelled so workers stop at their next checkpoint (fetchers
// check it before reading credentials, the scanner before each file and
// before writing its cache), busy flags clear so nothing looks in flight,
// and workGen invalidates any result that still arrives.
func (f *usageFeature) stopWork() {
	f.workGen++
	if f.workCancel != nil {
		f.workCancel()
		f.workCtx, f.workCancel = nil, nil
	}
	for i := range f.quota {
		f.quota[i].busy = false
	}
	f.localBusy = false
	f.invalidate()
}

// tick runs from the monitor loop (main thread). It does nothing while the
// feature is disabled; once the page becomes visible it refreshes stale
// data at once and keeps it fresh at the two intervals. Leaving visibility
// (feature off, panel hidden, page switched away) cancels in-flight work.
func (f *usageFeature) tick(now time.Time) {
	if !f.enabled() || !f.usageVisible() {
		if f.wasVisible {
			f.wasVisible = false
			f.stopWork()
		}
		return
	}
	if !f.wasVisible {
		f.wasVisible = true
		// Opening the page is the user asking to see fresh numbers; the
		// Claude floor still applies, so this only marks the ask.
		f.askQuota(aiusage.ToolClaude)
	}
	// A page switch is still running: start no fetch or scan until it has
	// settled. Reading credentials and walking the local usage logs would
	// otherwise contend with the 240ms transition the user is watching.
	if f.host.win != nil && f.host.tabPhase(now) < 1 {
		return
	}
	f.ensureFresh(now)
	// Keep repainting while the first scan's progress bar moves.
	if f.localBusy && !f.hasSummary {
		f.invalidate()
	}
}

// ensureFresh starts the refreshes whose data is due at now: Codex every
// usageQuotaInterval (or its RetryAfter), Claude at the user's ask but no
// faster than usageClaudeFloor and otherwise on its random wait once
// Claude Code was used, the local summary every usageLocalInterval or once
// the chosen range has none.
func (f *usageFeature) ensureFresh(now time.Time) {
	for _, t := range quotaTools() {
		if !f.quotaOn(t) {
			continue
		}
		st := &f.quota[quotaIndex(t)]
		if st.busy || now.Before(st.retryTill) {
			continue
		}
		if t == aiusage.ToolClaude {
			if f.claudeDue(st, now) {
				f.startQuota(t)
			}
			continue
		}
		if st.attemptAt.IsZero() || now.Sub(st.attemptAt) >= usageQuotaInterval {
			f.startQuota(t)
		}
	}
	if f.localOn() && !f.localBusy {
		missing := f.summary.Range != f.rangeSel || !f.hasSummary
		if f.lastLocal.IsZero() || missing || now.Sub(f.lastLocal) >= usageLocalInterval {
			f.startLocal(now)
		}
	}
}

// requestRefresh rebuilds local usage and asks each enabled quota source
// within its minimum gap and RetryAfter. Every click gets a status toast,
// even when a fast local scan leaves no visibly busy frame.
func (f *usageFeature) requestRefresh() {
	if !f.enabled() {
		return
	}
	now := f.deps.now()
	started := false
	var wait time.Duration
	for _, t := range quotaTools() {
		if !f.quotaOn(t) {
			continue
		}
		minGap := usageQuotaManual
		st := &f.quota[quotaIndex(t)]
		if t == aiusage.ToolClaude {
			minGap = usageClaudeFloor
			st.ask = true
		}
		if st.busy {
			started = true
			continue
		}
		due := st.attemptAt.Add(minGap)
		if st.retryTill.After(due) {
			due = st.retryTill
		}
		if now.Before(due) {
			if d := due.Sub(now); wait == 0 || d < wait {
				wait = d
			}
			continue
		}
		f.startQuota(t)
		started = true
	}
	if f.localOn() {
		if !f.localBusy {
			f.startLocal(now)
		}
		started = true
	}
	// A held-back quota source is what makes a click look dead when the
	// local scan is instant: say when it may be asked again.
	l := f.host.appLoc()
	switch {
	case wait > 0:
		f.host.toastStatus(l.UsageRefreshHeld(usageWait(l, wait)), time.Time{}, statusInfo)
	case started:
		f.host.toastProgress(l.UsageRefreshing(), time.Time{})
	default:
		f.host.toastStatus(l.UsageNothingToRefresh(), time.Time{}, statusInfo)
	}
}

// startQuota begins one fetch for tool on a worker goroutine, under the
// feature's work context so hiding the panel or disabling the page stops
// it; a result from a retired generation still reaches gotQuota, which
// records it without touching the busy flag.
func (f *usageFeature) startQuota(tool aiusage.Tool) {
	st := &f.quota[quotaIndex(tool)]
	if st.busy {
		return
	}
	fetch := f.deps.fetchCodex
	if tool == aiusage.ToolClaude {
		fetch = f.deps.fetchClaude
	}
	if fetch == nil {
		return
	}
	st.busy = true
	attempted := f.deps.now()
	st.attemptAt = attempted
	if tool == aiusage.ToolClaude {
		st.ask = false
		st.nextAt = attempted.Add(f.deps.claudeWait())
	}
	f.invalidate()
	gen := f.workGen
	st.generation = gen
	base := f.workContext()
	goWork(func() {
		ctx, cancel := context.WithTimeout(base, usageQuotaTimeout)
		q := fetch(ctx)
		// Our own retirement (hide, page switch, disable) cancels ctx; the
		// timeout belongs to the request. Only the former means the fetch
		// says nothing about the endpoint.
		aborted := errors.Is(ctx.Err(), context.Canceled)
		cancel()
		q.Tool = tool
		dispatchMain(func() { f.gotQuota(q, gen, attempted, aborted) })
	})
}

// gotQuota retains completed answers even after a page change, so reopening
// never shows an unanswered card for a request that already finished.
// Cancelled Codex work may be retried immediately. Claude attempts still
// retain their 30-second floor because cancellation cannot prove that the
// process never ran, but the failed outcome of a cancelled
// fetch never replaces a previous answer (that would fake 网络不通); a first
// look still publishes it so the card cannot sit on 正在查询…. Server
// RetryAfter always survives.
func (f *usageFeature) gotQuota(q aiusage.Quota, gen int, attempted time.Time, aborted bool) {
	st := &f.quota[quotaIndex(q.Tool)]
	if q.RetryAfter > 0 {
		at := q.FetchedAt
		if at.IsZero() {
			at = f.deps.now()
		}
		if until := at.Add(q.RetryAfter); until.After(st.retryTill) {
			st.retryTill = until
		}
	}
	if aborted && q.Status == aiusage.QuotaFailed && q.RetryAfter <= 0 {
		if st.generation == gen && st.attemptAt.Equal(attempted) {
			st.busy = false
			if q.Tool == aiusage.ToolCodex {
				st.attemptAt = time.Time{}
			}
		}
		if q.Tool == aiusage.ToolCodex || !st.latest.FetchedAt.IsZero() {
			f.invalidate()
			return
		}
	}
	if q.FetchedAt.IsZero() {
		q.FetchedAt = f.deps.now()
	}
	if st.generation == gen && st.attemptAt.Equal(attempted) {
		st.busy = false // this answer belongs to the attempt still in flight
	}
	if q.FetchedAt.Before(st.latest.FetchedAt) {
		return
	}
	st.latest = q
	if q.Status == aiusage.QuotaOK && len(q.Windows) > 0 {
		st.good = q
		if f.deps.saveLast != nil {
			f.deps.saveLast(q)
		}
	}
	f.invalidate()
}

// startLocal begins the local refresh: scan what's new, then aggregate the
// currently selected range. Both run on the worker under the feature's
// work context; only the progress counters (atomics) and the published
// summary reach the UI.
func (f *usageFeature) startLocal(now time.Time) {
	if f.localBusy {
		return
	}
	scanner := f.deps.newScanner
	if scanner == nil {
		return
	}
	sc := scanner()
	if sc == nil {
		return
	}
	f.localBusy = true
	f.lastLocal = now
	f.scanDone.Store(0)
	f.scanTotal.Store(0)
	f.invalidate()
	r := f.rangeSel // captured on the main thread: the worker never reads f.*
	gen := f.workGen
	base := f.workContext()
	goWork(func() {
		ctx, cancel := context.WithTimeout(base, usageScanTimeout)
		scanErr := sc.Scan(ctx, func(done, total int64) {
			f.scanDone.Store(done)
			f.scanTotal.Store(total)
		})
		var sum aiusage.Summary
		if ctx.Err() == nil {
			sum = sc.Summary(r, f.deps.now())
		}
		cancel()
		dispatchMain(func() { f.gotLocal(sum, r, scanErr, gen) })
	})
}

// gotLocal publishes the local refresh outcome on the main thread. A
// summary the user has since switched away from is dropped and a fresh
// pass for the new range is started instead; a result retired by stopWork
// while the worker ran is dropped outright.
func (f *usageFeature) gotLocal(sum aiusage.Summary, r aiusage.Range, err error, gen int) {
	if gen != f.workGen || !f.enabled() || !f.usageVisible() {
		return // retired by stopWork or a page/settings change while in flight
	}
	f.localBusy = false
	f.scanned = true
	f.localErr = err
	if err == nil {
		sum.Range = r
		f.summary, f.hasSummary = sum, true
		f.localAt = f.deps.now()
	}
	f.invalidate()
	// The range may have moved on while the worker ran: refresh it.
	if f.localOn() && f.summary.Range != f.rangeSel && f.usageVisible() {
		f.startLocal(f.deps.now())
	}
}

// quotaShown is the quota a card draws: the latest answer while it was
// good, otherwise the last good one (shown dimmed as stale data) when the
// newest attempt only failed to reach the endpoint. An answer about the
// account — signed out, sign-in expired, allowance used up, no CLI — is
// returned as it came: last good numbers must never paper it over.
// stale is false when there is nothing to fall back to, so no fake
// numbers render.
func (f *usageFeature) quotaShown(t aiusage.Tool) (q aiusage.Quota, stale bool) {
	st := &f.quota[quotaIndex(t)]
	now := f.deps.now()
	if st.latest.Status == aiusage.QuotaOK && len(st.latest.Windows) > 0 {
		q := st.latest
		q.Windows = aiusage.Elapsed(q.Windows, now)
		return q, false
	}
	if st.latest.Status != aiusage.QuotaOK && !st.latest.FetchedAt.IsZero() && !st.latest.Status.Transient() {
		return st.latest, false
	}
	q = st.good
	q.Windows = aiusage.Elapsed(q.Windows, now)
	return q, !st.good.FetchedAt.IsZero()
}

// claudeDue reports whether an unasked /usage read is due: the random
// whole-minute gap has passed and Claude Code was used since the last
// attempt. The "was it used" answer costs a walk of ~/.claude/projects, so
// it comes from an earlier tick's background probe; a tick that finds no
// answer yet asks for one and is not due.
func (f *usageFeature) claudeDue(st *usageQuotaState, now time.Time) bool {
	if st.attemptAt.IsZero() {
		return true
	}
	if now.Sub(st.attemptAt) < usageClaudeFloor {
		return false
	}
	if st.ask {
		return true
	}
	if st.nextAt.IsZero() || now.Before(st.nextAt) {
		return false
	}
	if f.deps.claudeUsedSince == nil {
		return true
	}
	// An answer is trusted only while it is fresh: past the probe gap the
	// tree may have changed, so a new scan is due.
	if !st.usedProbing && (st.usedProbeAt.IsZero() || now.Sub(st.usedProbeAt) >= usageClaudeProbeGap) {
		f.probeClaudeUsed(st, now)
		return false
	}
	return st.usedFor.Equal(st.attemptAt) && st.usedSince
}

// probeClaudeUsed starts the projects scan for st, off the main thread: the
// walk gets slower with every session history, and this runs on a 30ms
// monitor tick that also drives animation and drops. One scan is in flight
// at a time and at most one starts per usageClaudeProbeGap, so an idle
// machine never rereads the tree; the answer is left on st for a later
// tick's claudeDue.
func (f *usageFeature) probeClaudeUsed(st *usageQuotaState, now time.Time) {
	used := f.deps.claudeUsedSince
	if used == nil || st.usedProbing {
		return
	}
	if !st.usedProbeAt.IsZero() && now.Sub(st.usedProbeAt) < usageClaudeProbeGap {
		return
	}
	st.usedProbing = true
	st.usedProbeAt = now
	probeAt, since := now, st.attemptAt
	run := f.deps.claudeProbe
	if run == nil {
		run = goWork
	}
	run(func() {
		hit := used(since)
		dispatchMain(func() {
			if !st.usedProbeAt.Equal(probeAt) {
				return // retired while scanning: a newer probe owns the state
			}
			st.usedProbing = false
			st.usedFor, st.usedSince = since, hit
		})
	})
}

// askQuota marks the next Claude read as one the user asked for.
func (f *usageFeature) askQuota(t aiusage.Tool) {
	if t != aiusage.ToolClaude {
		return
	}
	f.quota[quotaIndex(t)].ask = true
}

// offline reports whether an enabled quota source's last fetch failed on
// the network: the module bar appends 「· 网络不通」 while stale data shows.
func (f *usageFeature) offline() bool {
	for _, t := range quotaTools() {
		if f.quotaOn(t) && f.quota[quotaIndex(t)].latest.Status == aiusage.QuotaFailed {
			return true
		}
	}
	return false
}

// paceOf returns the even-pace estimate for a window, safe when the
// estimator was not injected.
func (f *usageFeature) paceOf(w *aiusage.Window, now time.Time) (aiusage.PaceInfo, bool) {
	if w == nil || f.deps.pace == nil {
		return aiusage.PaceInfo{}, false
	}
	return f.deps.pace(*w, now), true
}

// lastUpdatedAt is the newest moment any shown data was fetched (the last
// good quota answer, not a failed attempt); zero while nothing ever
// arrived.
func (f *usageFeature) lastUpdatedAt() time.Time {
	var t time.Time
	for _, tool := range quotaTools() {
		q, _ := f.quotaShown(tool)
		if at := q.FetchedAt; at.After(t) {
			t = at
		}
	}
	if f.localAt.After(t) {
		t = f.localAt
	}
	return t
}

// invalidate asks the panel for a repaint after state changed.
func (f *usageFeature) invalidate() {
	if a := f.host; a != nil && a.win != nil {
		a.win.Invalidate()
	}
}
