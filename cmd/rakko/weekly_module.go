//go:build darwin

package main

// 每周简报的数据侧：应用使用采样（须用户明确同意）、Rakko 完成事件、以及
// 每日本机 AI 用量的后台汇总。页面绘制在 weekly_page.go。
//
// 采样与记录全部在本机：weekly.json 只存 bundle id、显示名和累计毫秒，不存
// 窗口标题、网址或输入内容。停用统计后立即停止采样，不补历史，缺的时段保持
// 未知，不当作零。AI 部分只读本机日志，不请求线上额度。

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/aiusage"
	"rakko/internal/weekly"
)

const (
	// weeklyScanInterval is how often a visible report rescans the local
	// AI logs: the logs grow slowly, and a manual revisiting refreshes at
	// once.
	weeklyScanInterval = 60 * time.Second
	// weeklyScanTimeout bounds one scan so a stuck reader cannot leave the
	// section on 正在读取 forever.
	weeklyScanTimeout = 10 * time.Minute
	// weeklyFlushInterval is how often dirty app samples reach the disk.
	weeklyFlushInterval = 60 * time.Second
	// weeklyWeekOptions is the oldest week the picker steps to when the
	// history is younger: a year of local AI logs may reach further back
	// than app samples or Rakko events do.
	weeklyWeekOptions = 52
)

// weeklyScanner is the slice of aiusage.Scanner the report uses: a local
// scan and one arbitrary span, with the scanner's own dedupe.
type weeklyScanner interface {
	Scan(ctx context.Context, progress func(done, total int64)) error
	SummarySpan(from, to time.Time) aiusage.Summary
	// FailedTools names the readers the pass could not read: with a zero
	// total it separates "no usage" from "usage Rakko could not see".
	FailedTools() []aiusage.Tool
}

// weeklyDeps injects every system call and background seam the feature
// needs, so tests describe a machine and a log set without AppKit or the
// user's real data.
type weeklyDeps struct {
	now           func() time.Time
	frontApp      func() (bundleID, name string)
	idleSeconds   func() (float64, error)
	sessionActive func() bool
	displayAsleep func() bool
	newScanner    func() weeklyScanner
	goWork        func(func())
	onMain        func(func())
}

// weeklyFeature is the 每周简报 page and its sampler.
type weeklyFeature struct {
	host  *shelf
	deps  weeklyDeps
	store *weekly.Store

	// storeErr is the load error Open reported: a corrupt file keeps its
	// original bytes and refuses new writes, which the page must say.
	storeErr error

	sampler    *weekly.Sampler
	lastSample time.Time
	lastFlush  time.Time

	metric     weekly.Metric
	weekOffset int // 0 = this week, negative counts back; never > 0

	// AI summary for one week. scanWeek is the week of the last attempt
	// (successful or not); a different visible week starts a new scan.
	scanBusy   atomic.Bool
	scanCancel context.CancelFunc
	scanWeek   time.Time
	scanAt     time.Time
	aiWeek     time.Time
	aiHave     bool
	aiSummary  aiusage.Summary
	aiFailed   []aiusage.Tool
	aiErr      error
	gen        int

	scroll ui.ScrollState
}

// newWeeklyFeature wires the feature around an opened store. A store load
// error is kept for the page; the store is still usable in memory.
func newWeeklyFeature(a *shelf, deps weeklyDeps, store *weekly.Store, storeErr error) *weeklyFeature {
	f := &weeklyFeature{host: a, deps: deps, store: store, storeErr: storeErr}
	if f.deps.now == nil {
		f.deps.now = time.Now
	}
	if f.unwrap() != nil {
		probe := weekly.Probe{
			Front:         f.deps.frontApp,
			IdleSeconds:   f.deps.idleSeconds,
			SessionActive: f.deps.sessionActive,
			DisplayAsleep: f.deps.displayAsleep,
		}
		f.sampler = weekly.NewSampler(f.unwrap(), probe)
	}
	return f
}

// unwrap is the store or nil: a nil store means the feature only renders
// empty states (a test without a data directory).
func (f *weeklyFeature) unwrap() *weekly.Store { return f.store }

// consent is the user's explicit choice to collect app time.
func (f *weeklyFeature) consent() bool { return f.host.settings.Weekly.AppStats }

// weekStart is offset weeks from this week's Monday, in local time.
func (f *weeklyFeature) weekStart(now time.Time, offset int) time.Time {
	return weekly.WeekStart(now).AddDate(0, 0, 7*offset)
}

// minOffset is the oldest week the picker may show: at least the previous
// week, and further back when the history reaches there. The previous week
// stays reachable even before anything was recorded, so the home entry's
// 上周简报 never lands on a different week than its label.
func (f *weeklyFeature) minOffset(now time.Time) int {
	minOff := -weeklyWeekOptions
	earliest, ok := f.earliest()
	if !ok {
		return minOff
	}
	first := weekly.WeekStart(earliest)
	for off := 0; off > -520; off-- {
		if !f.weekStart(now, off).After(first) {
			return min(off, minOff)
		}
	}
	return -520
}

// earliest is the first recorded day, the nav's oldest anchor.
func (f *weeklyFeature) earliest() (time.Time, bool) {
	if f.store == nil {
		return time.Time{}, false
	}
	return f.store.Earliest()
}

// setWeekOffset clamps the picker to [minOffset, 0].
func (f *weeklyFeature) setWeekOffset(now time.Time, off int) {
	minOff := f.minOffset(now)
	if off > 0 {
		off = 0
	}
	if off < minOff {
		off = minOff
	}
	if off != f.weekOffset {
		f.scroll = ui.ScrollState{}
		f.cancelScan()
	}
	f.weekOffset = off
}

// openLastWeek opens the report on the previous calendar week, as the home
// entry says.
func (f *weeklyFeature) openLastWeek() {
	f.setWeekOffset(f.deps.now(), -1)
}

// openLastWeekPage is the home surfaces' tap: show the previous week's
// report. Both the idle entry and the player's report button land here.
func (f *weeklyFeature) openLastWeekPage() {
	f.openLastWeek()
	f.host.showPage(pageWeekly)
}

// tick runs from the monitor loop on the main thread. Sampling runs while
// consent is on, even with the panel closed; the AI scan runs only while
// the report is the visible page.
func (f *weeklyFeature) tick(now time.Time) {
	if f.sampler != nil {
		if f.lastSample.IsZero() || now.Sub(f.lastSample) >= weekly.SampleInterval {
			f.lastSample = now
			f.sampler.Tick(now, f.consent())
		}
	}
	if f.store != nil && f.store.Dirty() {
		if f.lastFlush.IsZero() || now.Sub(f.lastFlush) >= weeklyFlushInterval {
			f.lastFlush = now
			_ = f.store.MaybeFlush(now, 0)
		}
	}
	if f.visible(now) {
		f.maybeScan(now)
	}
}

// visible reports whether the report is the page on screen and its switch
// has settled.
func (f *weeklyFeature) visible(now time.Time) bool {
	if !f.host.visible || f.host.currentLevel() != 2 || f.host.currentPage().id() != pageWeekly {
		return false
	}
	if f.host.win != nil && f.host.tabPhase(now) < 1 {
		return false
	}
	return true
}

// flush writes the app history now, best effort: a failure stays visible
// on the page and the in-memory data is kept.
func (f *weeklyFeature) flush() {
	if f.store == nil {
		return
	}
	_ = f.store.Flush()
}

// setStats records the user's consent and starts or stops collection at
// once. Turning it off flushes what was measured, then resets the sampler
// so no later tick counts an interval from before the switch.
func (f *weeklyFeature) setStats(on bool) {
	next := f.host.settings
	next.Weekly.AppStats = on
	if !f.host.applySettings(next) {
		return // settings.json could not be saved: do not collect without a record of consent
	}
	if on {
		if f.sampler != nil {
			f.sampler.Reset()
		}
		f.lastSample = time.Time{}
		return
	}
	if f.sampler != nil {
		f.sampler.Reset()
	}
	f.flush()
	f.invalidate()
}

// addEvent counts one completed Rakko action. Callers reach here only after
// the action actually succeeded; a failed conversion, a capture without
// text or an empty drag never calls it.
func (a *shelf) weeklyEvent(kind weekly.EventKind) {
	if a.weekly == nil || a.weekly.store == nil {
		return
	}
	a.weekly.store.AddEvent(kind, time.Now())
}

// maybeScan rebuilds the AI summary when the visible week changed or the
// last answer is older than weeklyScanInterval.
func (f *weeklyFeature) maybeScan(now time.Time) {
	if f.scanBusy.Load() {
		return
	}
	start := f.weekStart(now, f.weekOffset)
	if f.deps.newScanner == nil {
		f.aiErr = errors.New("no local home directory")
		f.aiHave = false
		f.aiWeek = time.Time{}
		f.scanWeek, f.scanAt = start, now
		return
	}
	if f.scanWeek.Equal(start) && now.Sub(f.scanAt) < weeklyScanInterval {
		return
	}
	f.startScan(start, now)
}

// startScan runs one scan and one span summary on a worker. The summary is
// published on the main thread and only kept when it still belongs to the
// week on screen.
func (f *weeklyFeature) startScan(start, now time.Time) {
	scanner := f.deps.newScanner()
	if scanner == nil {
		f.aiErr = errors.New("no local scanner")
		f.scanWeek, f.scanAt = start, now
		return
	}
	f.scanBusy.Store(true)
	f.gen++
	gen := f.gen
	goWork := f.deps.goWork
	if goWork == nil {
		goWork = func(fn func()) { go fn() }
	}
	onMain := f.deps.onMain
	if onMain == nil {
		onMain = dispatchMain
	}
	ctx, cancel := context.WithTimeout(context.Background(), weeklyScanTimeout)
	f.scanCancel = cancel
	end := start.AddDate(0, 0, 7)
	goWork(func() {
		defer cancel()
		err := scanner.Scan(ctx, nil)
		var sum aiusage.Summary
		var failed []aiusage.Tool
		if err == nil {
			sum = scanner.SummarySpan(start, end)
			failed = scanner.FailedTools()
		}
		onMain(func() { f.gotScan(start, sum, failed, err, gen) })
	})
}

// gotScan publishes one scan's outcome. A result for a week the user has
// since left is dropped; the visible week is scanned again by the next
// tick.
func (f *weeklyFeature) gotScan(start time.Time, sum aiusage.Summary, failed []aiusage.Tool, err error, gen int) {
	if gen != f.gen {
		return
	}
	f.scanBusy.Store(false)
	f.scanCancel = nil
	now := f.deps.now()
	f.scanWeek, f.scanAt = start, now
	if !start.Equal(f.weekStart(now, f.weekOffset)) {
		// The picker moved while this scan ran. Keep no answer for the
		// wrong week; the next tick starts the right one.
		f.aiErr, f.aiHave, f.aiWeek = nil, false, time.Time{}
		f.aiFailed = nil
		f.scanWeek = time.Time{}
		f.invalidate()
		return
	}
	if err != nil {
		f.aiErr = err
		f.aiHave = false
		f.aiWeek = time.Time{}
		f.aiFailed = nil
		f.invalidate()
		return
	}
	f.aiErr = nil
	f.aiHave = true
	f.aiWeek = start
	f.aiSummary = sum
	f.aiFailed = failed
	f.invalidate()
}

// cancelScan drops a scan in flight and its answer. The caller decides
// whether the next tick should start a new one.
func (f *weeklyFeature) cancelScan() {
	f.gen++
	f.scanBusy.Store(false)
	if f.scanCancel != nil {
		f.scanCancel()
		f.scanCancel = nil
	}
}

// leavingPage cancels an AI scan in flight when the report is no longer on
// screen. App sampling keeps running: it is not the page's work.
func (f *weeklyFeature) leavingPage() {
	f.cancelScan()
	f.scanAt = time.Time{} // a later visit starts a fresh read
}

// invalidate repaints the panel so the arriving summary shows.
func (f *weeklyFeature) invalidate() {
	if f.host.win != nil {
		f.host.win.Invalidate()
	}
}
