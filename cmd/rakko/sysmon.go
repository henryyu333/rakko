package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/platform"
	"rakko/internal/settings"
	"rakko/internal/sysmon"
	"rakko/locales"
)

const sysmonColdDelta = 250 * time.Millisecond

// sysmonModule owns on-demand system performance sampling. When neither the
// tools page (with a sysmon card), an expanded sysmon desktop pin, nor resting
// sysmon wings are active, nextTick returns zero so the app sleeps with zero
// background wakeups.
//
// Thread safety: all fields except sampler are main-thread only.sampler is
// read/mutated only on the main thread when syncSample is true, or inside the
// single worker goroutine while inFlight is true; completed snapshots and
// cloned histories return to the main thread via dispatch.
type sysmonModule struct {
	host        *shelf
	sampler     *sysmon.Sampler
	now         func() time.Time
	readBattery func() sysmon.BatteryInfo
	dispatch    func(func())
	syncSample  bool

	snap       sysmon.Snapshot
	hist       sysmon.History
	lastSample time.Time
	inFlight   bool

	popoverOpen   bool
	wantProcsSoon bool
	focusTab      string // "cpu", "mem", "net"
	procSort      string // "cpu", "mem"
}

func newSysMonModule(host *shelf) *sysmonModule {
	return &sysmonModule{
		host:     host,
		sampler:  sysmon.NewSampler(sysmon.NewPlatformReader()),
		dispatch: mygo.RunOnMain,
		focusTab: settings.SysMonMetricCPU,
		procSort: settings.SysMonMetricCPU,
	}
}

func (a *shelf) registerSysMonModule() {
	a.sysmon = newSysMonModule(a)
	a.modules = append(a.modules, a.sysmon)
}

func (m *sysmonModule) clock() time.Time {
	if m != nil && m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *sysmonModule) batteryNow() sysmon.BatteryInfo {
	if m != nil && m.readBattery != nil {
		return m.readBattery()
	}
	b, ok := platform.Battery()
	if !ok {
		return sysmon.BatteryInfo{}
	}
	return sysmon.BatteryInfo{
		Present:  true,
		Percent:  b.Percent,
		Charging: b.Charging,
		External: b.External,
	}
}

func (m *sysmonModule) sendMain(fn func()) {
	if m != nil && m.dispatch != nil {
		m.dispatch(fn)
		return
	}
	mygo.RunOnMain(fn)
}

// needed reports whether any sysmon surface is actively visible right now.
// Collapsed edge-docked pin strips are hidden off-edge and do not wake sampling.
func (m *sysmonModule) needed() bool {
	if m == nil || m.host == nil {
		return false
	}
	a := m.host
	if a.quitting {
		return false
	}
	if a.sysmonWingsShown() {
		return true
	}
	if a.hasExpandedSysMonPin() {
		return true
	}
	toolsVisible := a.visible && a.currentLevel() == 2 && a.tab == pageTools
	if toolsVisible && a.hasSysMonCard() {
		return true
	}
	return false
}

func (m *sysmonModule) interval() time.Duration {
	if m != nil && m.host != nil && m.host.settings.SysMon.IntervalSec == 2 {
		return 2 * time.Second
	}
	return time.Second
}

func (m *sysmonModule) nextTick(now time.Time) time.Time {
	if !m.needed() {
		return time.Time{}
	}
	if m.lastSample.IsZero() {
		return now
	}
	if !m.snap.CPUReady || m.wantProcsSoon {
		return m.lastSample.Add(sysmonColdDelta)
	}
	return m.lastSample.Add(m.interval())
}

func (m *sysmonModule) tick(now time.Time) {
	if !m.needed() || m.inFlight || m.sampler == nil {
		return
	}
	if !m.lastSample.IsZero() && now.Sub(m.lastSample) < sysmonColdDelta {
		return
	}
	due := m.nextTick(now)
	if !due.IsZero() && now.Before(due) {
		return
	}
	a := m.host
	batt := m.batteryNow()
	wantProcs := m.popoverOpen && a.settings.SysMon.ShowTopProcs
	if !wantProcs {
		m.wantProcsSoon = false
	}
	m.lastSample = now
	if m.syncSample {
		snap := m.sampler.Sample(now, wantProcs, batt)
		hist := m.sampler.History()
		m.publish(snap, hist)
		m.noteProcs(wantProcs, snap)
		return
	}
	m.inFlight = true
	sampler := m.sampler
	go func() {
		snap := sampler.Sample(now, wantProcs, batt)
		hist := sampler.History()
		m.sendMain(func() {
			m.finishSample(snap, hist)
		})
	}()
}

func (m *sysmonModule) finishSample(snap sysmon.Snapshot, hist sysmon.History) {
	m.inFlight = false
	m.publish(snap, hist)
	wantProcs := m.host != nil && m.popoverOpen && m.host.settings.SysMon.ShowTopProcs
	m.noteProcs(wantProcs, snap)
	if m.host != nil {
		m.host.wakeMonitor()
	}
}

// noteProcs decides whether the popover still needs the quick re-sample that
// gives it a real CPU ranking: a frame without one keeps the 250 ms cadence
// (the reopened popover's first frame is the waiting state), and a ranked
// frame goes back to the normal interval.
func (m *sysmonModule) noteProcs(wantProcs bool, snap sysmon.Snapshot) {
	m.wantProcsSoon = wantProcs && len(snap.TopCPU) == 0
}

func (m *sysmonModule) publish(snap sysmon.Snapshot, hist sysmon.History) {
	m.snap = snap
	m.hist = hist
	a := m.host
	if a == nil {
		return
	}
	if a.win != nil && !a.win.IsDestroyed() {
		if (a.visible && a.tab == pageTools) || a.sysmonWingsShown() {
			a.win.Invalidate()
		}
	}
	for _, it := range a.pins {
		if it.cardID == "sysmon" && !it.preview && (it.edge == pinFree || it.expanded) && it.win != nil {
			it.win.Invalidate()
		}
	}
}

func (m *sysmonModule) wake(now time.Time) {
	if m == nil || !m.needed() {
		return
	}
	if m.lastSample.IsZero() || now.Sub(m.lastSample) >= sysmonColdDelta {
		m.tick(now)
	}
	if m.host != nil {
		m.host.wakeMonitor()
	}
}

func (m *sysmonModule) setPopoverOpen(open bool) {
	if m == nil || m.popoverOpen == open {
		return
	}
	m.popoverOpen = open
	if open && m.host != nil && m.host.settings.SysMon.ShowTopProcs {
		m.wantProcsSoon = true
		m.wake(m.clock())
	} else {
		m.wantProcsSoon = false
	}
}

func (a *shelf) hasSysMonCard() bool {
	if a == nil {
		return false
	}
	for _, c := range a.toolsLayout().Cards {
		if c.ID == "sysmon" {
			return true
		}
	}
	return false
}

func (a *shelf) hasExpandedSysMonPin() bool {
	if a == nil {
		return false
	}
	for _, it := range a.pins {
		if it.cardID == "sysmon" && !it.preview && (it.edge == pinFree || it.expanded) {
			return true
		}
	}
	return false
}

// sysmonWingsUp reports whether resting sysmon wings should occupy the closed
// notch / capsule when no higher-priority event is shown.
func (a *shelf) sysmonWingsUp() bool {
	return a != nil && a.sysmon != nil && a.settings.SysMon.WingsEnabled && !a.visible
}

// sysmonWingsShown reports whether resting sysmon wings are the active
// closed-shell view (not preempted by hover peek, app switcher, live notices,
// alarms, downloads, or timer wings).
func (a *shelf) sysmonWingsShown() bool {
	if !a.sysmonWingsUp() || a.peeking || a.appsModeOn() || a.liveUp() || a.clockAlarmFree() || a.downloadRunning() {
		return false
	}
	if a.clock != nil && a.clock.timerWingsUp() {
		return false
	}
	return true
}

func (a *shelf) setSysMon(mutate func(*settings.SysMonSettings)) {
	if a == nil {
		return
	}
	prevWings := a.settings.SysMon.WingsEnabled
	next := a.settings
	mutate(&next.SysMon)
	next.SysMon = next.SysMon.Normalized()
	if !a.applySettings(next) {
		return
	}
	if prevWings != next.SysMon.WingsEnabled {
		a.syncWindowFrame()
		a.syncMousePassThrough()
		a.syncMainIdleCapsule()
	}
	if a.sysmon != nil && a.sysmon.needed() {
		a.sysmon.wake(a.sysmon.clock())
	}
}

func (a *shelf) cycleSysMonPrimary() {
	a.setSysMon(func(s *settings.SysMonSettings) {
		switch s.Normalized().PrimaryMetric {
		case settings.SysMonMetricCPU:
			s.PrimaryMetric = settings.SysMonMetricMem
		case settings.SysMonMetricMem:
			s.PrimaryMetric = settings.SysMonMetricNet
		default:
			s.PrimaryMetric = settings.SysMonMetricCPU
		}
	})
}

func (a *shelf) runSysMonTap(span int) bool {
	if span == 1 {
		a.cycleSysMonPrimary()
	}
	return true
}

func (a *shelf) sysmonSpanForTap() int {
	for _, c := range a.toolsLayoutNow().Cards {
		if c.ID == "sysmon" {
			return c.Cols()
		}
	}
	for _, it := range a.pins {
		if it.cardID == "sysmon" && !it.preview {
			return it.span
		}
	}
	return 2
}

func (a *shelf) sysmonSnapshot() (sysmon.Snapshot, sysmon.History) {
	if a == nil || a.sysmon == nil {
		return sysmon.Snapshot{}, sysmon.History{}
	}
	return a.sysmon.snap, a.sysmon.hist
}

func (a *shelf) sysmonCardTitle(l locales.Messages) string {
	snap, _ := a.sysmonSnapshot()
	if !snap.CPUReady && !snap.MemReady {
		return l.CardSysMon()
	}
	cpuStr := "--"
	if snap.CPUReady {
		cpuStr = sysmon.FormatPercent(snap.CPUPercent)
	}
	memStr := "--"
	if snap.MemReady {
		memStr = sysmon.FormatPercent(snap.MemPercent)
	}
	return l.SysMonCardLabel(cpuStr, memStr)
}

// sysmonActiveTab returns the current popover focus tab ("cpu", "mem", or "net").
func (m *sysmonModule) activeFocusTab() string {
	if m == nil {
		return settings.SysMonMetricCPU
	}
	switch m.focusTab {
	case settings.SysMonMetricCPU, settings.SysMonMetricMem, settings.SysMonMetricNet:
		return m.focusTab
	default:
		return settings.SysMonMetricCPU
	}
}

func (m *sysmonModule) setFocusTab(tab string) {
	if m == nil {
		return
	}
	switch tab {
	case settings.SysMonMetricCPU, settings.SysMonMetricMem, settings.SysMonMetricNet:
		m.focusTab = tab
		if m.host != nil && m.host.win != nil {
			m.host.win.Invalidate()
		}
	}
}

// activeProcSort returns the current popover Top 3 sort ("cpu" or "mem").
func (m *sysmonModule) activeProcSort() string {
	if m == nil {
		return settings.SysMonMetricCPU
	}
	if m.procSort == settings.SysMonMetricMem {
		return settings.SysMonMetricMem
	}
	return settings.SysMonMetricCPU
}

func (m *sysmonModule) setProcSort(sortBy string) {
	if m == nil {
		return
	}
	if sortBy != settings.SysMonMetricMem {
		sortBy = settings.SysMonMetricCPU
	}
	m.procSort = sortBy
	if m.host != nil && m.host.win != nil {
		m.host.win.Invalidate()
	}
}

// formatProcMetric formats a process row's memory or CPU metric for display.
func formatProcMetric(p sysmon.ProcEntry, sortBy string) string {
	if sortBy == settings.SysMonMetricMem {
		return sysmon.FormatBytesShort(p.MemBytes)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", p.CPUPercent), ".0") + "%"
}
