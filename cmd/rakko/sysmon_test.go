package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/platform"
	"rakko/internal/settings"
	"rakko/internal/store"
	"rakko/internal/sysmon"
	"rakko/locales"
)

type fakeSysMonReader struct {
	cpuBusy, cpuTotal             uint64
	memTotal, memUsed             uint64
	memApp, memWired, memComp     uint64
	netRX, netTX                  uint64
	diskTotal, diskFree, dR, dW   uint64
	diskDirect                    bool
	procs                         []sysmon.RawProc
	procReads                     atomic.Int64
}

func (r *fakeSysMonReader) ReadCPU() (uint64, uint64, bool) { return r.cpuBusy, r.cpuTotal, true }
func (r *fakeSysMonReader) ReadMem() (uint64, uint64, uint64, uint64, uint64, bool) {
	return r.memTotal, r.memUsed, r.memApp, r.memWired, r.memComp, true
}
func (r *fakeSysMonReader) ReadNet() (uint64, uint64, bool) { return r.netRX, r.netTX, true }
func (r *fakeSysMonReader) ReadDisk() (uint64, uint64, uint64, uint64, bool, bool) {
	return r.diskTotal, r.diskFree, r.dR, r.dW, r.diskDirect, true
}
func (r *fakeSysMonReader) ReadProcs() ([]sysmon.RawProc, bool) {
	r.procReads.Add(1)
	return r.procs, true
}

func makeTestSysMonShelf(t *testing.T, lang string, cards []layout.Card) (*shelf, *fakeSysMonReader, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := settings.Default()
	cfg.Language = lang
	if cards != nil {
		if err := layout.Write(dir, layout.Layout{Version: layout.Version, Cards: cards}); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.Local)
	reader := &fakeSysMonReader{
		cpuBusy:   1000,
		cpuTotal:  4000,
		memTotal:  32 * 1024 * 1024 * 1024,
		memUsed:   20*1024*1024*1024 + 512*1024*1024,
		memApp:    14 * 1024 * 1024 * 1024,
		memWired:  4 * 1024 * 1024 * 1024,
		memComp:   2*1024*1024*1024 + 512*1024*1024,
		netRX:     100 * 1024 * 1024,
		netTX:     20 * 1024 * 1024,
		diskTotal: 1000 * 1024 * 1024 * 1024,
		diskFree:  328 * 1024 * 1024 * 1024,
		dR:        500 * 1024 * 1024,
		dW:        200 * 1024 * 1024,
		procs: []sysmon.RawProc{
			{PID: 101, Name: "clang", CPUTimeNS: 2_000_000_000, MemBytes: 2*1024*1024*1024 + 400*1024*1024},
			{PID: 102, Name: "WindowServer", CPUTimeNS: 1_200_000_000, MemBytes: 1*1024*1024*1024 + 200*1024*1024},
			{PID: 103, Name: "GoLand.exe", CPUTimeNS: 900_000_000, MemBytes: 3*1024*1024*1024 + 100*1024*1024},
			{PID: 104, Name: "Finder", CPUTimeNS: 100_000_000, MemBytes: 320 * 1024 * 1024},
		},
	}

	a := &shelf{
		dataDir:      dir,
		store:        s,
		settings:     cfg,
		locale:       lang,
		notchX:       640,
		notchY:       0,
		notchW:       180,
		notchH:       32,
		topInset:     32,
		hasNotch:     true,
		dispBounds:   mygo.Rectangle{Width: 1440, Height: 900},
		dispWork:     mygo.Rectangle{Width: 1440, Height: 868},
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
	a.registerSysMonModule()
	a.sysmon.sampler = sysmon.NewSampler(reader)
	a.sysmon.syncSample = true
	a.sysmon.now = func() time.Time { return now }
	a.sysmon.readBattery = func() sysmon.BatteryInfo {
		return sysmon.BatteryInfo{Present: true, Percent: 82, Charging: true, External: true}
	}
	a.sysmon.dispatch = func(fn func()) { fn() }
	a.ensurePages()
	return a, reader, &now
}

func stepFakeSysMon(reader *fakeSysMonReader, now *time.Time, dt time.Duration, cpuPct, memPct float64, downBps, upBps uint64) {
	*now = now.Add(dt)
	dTotal := uint64(10000)
	dBusy := uint64(math.Round(cpuPct / 100 * float64(dTotal)))
	reader.cpuTotal += dTotal
	reader.cpuBusy += dBusy
	reader.memUsed = uint64(math.Round(memPct / 100 * float64(reader.memTotal)))
	reader.memApp = uint64(float64(reader.memUsed) * 0.65)
	reader.memWired = uint64(float64(reader.memUsed) * 0.22)
	reader.memComp = reader.memUsed - reader.memApp - reader.memWired
	secs := dt.Seconds()
	reader.netRX += uint64(float64(downBps) * secs)
	reader.netTX += uint64(float64(upBps) * secs)
	reader.dR += uint64(42 * 1024 * 1024 * secs)
	reader.dW += uint64(18 * 1024 * 1024 * secs)
	for i := range reader.procs {
		// Rank by descending PID weights so clang/WindowServer/GoLand keep clear percentages.
		factor := 0.35 / float64(i+1)
		reader.procs[i].CPUTimeNS += uint64(secs * 1e9 * factor)
	}
}

func TestSysMonOnDemandZeroWakeups(t *testing.T) {
	a, reader, now := makeTestSysMonShelf(t, "zh-Hans", []layout.Card{
		{ID: "ocr", Span: 1, Rows: 1},
		{ID: "sysmon", Span: 2, Rows: 1},
	})
	m := a.sysmon

	// 1. Resting with WingsEnabled == false and panel closed -> zero wakeups.
	if next := m.nextTick(*now); !next.IsZero() {
		t.Fatalf("resting nextTick = %v, want zero", next)
	}
	m.tick(*now)
	if m.snap.Ready {
		t.Fatal("sampled while unneeded")
	}

	// 2. Panel open on tray tab -> still zero wakeups for sysmon.
	a.visible = true
	a.level = 2
	a.tab = pageTray
	if next := m.nextTick(*now); !next.IsZero() {
		t.Fatalf("tray tab nextTick = %v, want zero", next)
	}

	// 3. Switch to tools page containing sysmon -> wakes immediately, then 1s interval.
	a.tab = pageTools
	if !m.needed() {
		t.Fatal("expected needed() when tools page with sysmon card is open")
	}
	if next := m.nextTick(*now); !next.Equal(*now) {
		t.Fatalf("first tools nextTick = %v, want %v", next, *now)
	}
	m.tick(*now)
	if !m.snap.Ready || m.snap.CPUReady {
		t.Fatalf("first sample ready=%v cpuReady=%v", m.snap.Ready, m.snap.CPUReady)
	}
	// Second tick shortly after uses sysmonColdDelta to populate CPU baseline.
	if next := m.nextTick(*now); !next.Equal(now.Add(sysmonColdDelta)) {
		t.Fatalf("cold nextTick = %v, want +250ms", next.Sub(*now))
	}
	stepFakeSysMon(reader, now, sysmonColdDelta, 34, 62, 4_000_000, 800_000)
	m.tick(*now)
	if !m.snap.CPUReady || math.Abs(m.snap.CPUPercent-34) > 1 {
		t.Fatalf("second sample cpuReady=%v pct=%.1f", m.snap.CPUReady, m.snap.CPUPercent)
	}
	// Normal interval = 1s; IntervalSec = 2 -> 2s.
	if d := m.nextTick(*now).Sub(*now); d != time.Second {
		t.Fatalf("interval 1s nextTick delta = %v, want 1s", d)
	}
	a.settings.SysMon.IntervalSec = 2
	if d := m.nextTick(*now).Sub(*now); d != 2*time.Second {
		t.Fatalf("interval 2s nextTick delta = %v, want 2s", d)
	}
	a.settings.SysMon.IntervalSec = 1

	// Procs must NOT have been read while popover is closed.
	if reads := reader.procReads.Load(); reads != 0 {
		t.Fatalf("ReadProcs called %d times while popover closed", reads)
	}
	m.setPopoverOpen(true)
	stepFakeSysMon(reader, now, time.Second, 45, 63, 5_000_000, 900_000)
	m.tick(*now)
	if reads := reader.procReads.Load(); reads == 0 {
		t.Fatal("expected ReadProcs when popoverOpen && ShowTopProcs")
	}
	beforeReads := reader.procReads.Load()
	a.settings.SysMon.ShowTopProcs = false
	stepFakeSysMon(reader, now, time.Second, 46, 63, 5_000_000, 900_000)
	m.tick(*now)
	if reads := reader.procReads.Load(); reads != beforeReads {
		t.Fatalf("ReadProcs called (%d -> %d) with ShowTopProcs=false", beforeReads, reads)
	}

	// Closing panel stops wakeups again.
	a.visible = false
	m.setPopoverOpen(false)
	if next := m.nextTick(*now); !next.IsZero() {
		t.Fatalf("closed panel nextTick = %v, want zero", next)
	}

	// 4. Desktop pin: collapsed edge strip stays asleep; expanded pin wakes.
	pin := &pinItem{owner: a, cardID: "sysmon", span: 1, edge: pinRight, expanded: false}
	a.pins = []*pinItem{pin}
	if next := m.nextTick(*now); !next.IsZero() {
		t.Fatalf("collapsed strip nextTick = %v, want zero", next)
	}
	pin.expanded = true
	if !m.needed() {
		t.Fatal("expected needed() when sysmon pin is expanded")
	}
	pin.expanded = false
	a.pins = nil

	// 5. WingsEnabled == true wakes when panel is closed, and pauses while panel opens on non-tools page.
	a.settings.SysMon.WingsEnabled = true
	if !m.needed() {
		t.Fatal("expected needed() when WingsEnabled && !visible")
	}
	a.visible = true
	a.tab = pageTray
	if m.needed() {
		t.Fatal("expected !needed() when panel is open on tray (wings hidden)")
	}
}

func TestSysMonCardsAndFit(t *testing.T) {
	origFit := fitIssue
	var issues []string
	fitIssue = func(msg string) { issues = append(issues, msg) }
	t.Cleanup(func() { fitIssue = origFit })

	for _, lang := range []string{"zh-Hans", "en"} {
		t.Run(lang, func(t *testing.T) {
			a, reader, now := makeTestSysMonShelf(t, lang, []layout.Card{
				{ID: "sysmon", Span: 2, Rows: 2},
			})
			a.visible = true
			a.level = 2
			a.tab = pageTools
			for range 12 {
				stepFakeSysMon(reader, now, time.Second, 24, 62, 18_400_000, 1_200_000)
				a.sysmon.tick(*now)
			}

			// Render across 2x2, 2x1, 1x1 (cpu, mem, net) in normal + stress + desktop no-battery.
			scenarios := []struct {
				name    string
				span    int
				rows    int
				metric  string
				stress  bool
				noBatt  bool
				desktop bool
			}{
				{"2x2-normal", 2, 2, "cpu", false, false, false},
				{"2x2-stress", 2, 2, "cpu", true, false, false},
				{"2x2-desktop", 2, 2, "cpu", false, true, false},
				{"2x1-normal", 2, 1, "cpu", false, false, false},
				{"2x1-stress-desktop", 2, 1, "cpu", true, true, false},
				{"1x1-cpu", 1, 1, "cpu", true, false, false},
				{"1x1-mem", 1, 1, "mem", true, false, false},
				{"1x1-net", 1, 1, "net", true, false, false},
				{"pin-1x1", 1, 1, "net", true, false, true},
				{"pin-2x1", 2, 1, "cpu", true, false, true},
			}
			for _, sc := range scenarios {
				a.settings.SysMon.PrimaryMetric = sc.metric
				if sc.noBatt {
					a.sysmon.readBattery = func() sysmon.BatteryInfo { return sysmon.BatteryInfo{Present: false} }
				} else {
					a.sysmon.readBattery = func() sysmon.BatteryInfo { return sysmon.BatteryInfo{Present: true, Percent: 100, Charging: true} }
				}
				if sc.stress {
					stepFakeSysMon(reader, now, time.Second, 100, 99.7, 1_300_000_000, 1_046_000_000)
				} else {
					stepFakeSysMon(reader, now, time.Second, 18, 56, 12_400_000, 900_000)
				}
				reader.diskFree = 10 * 1024 * 1024 * 1024
				a.sysmon.tick(*now)

				surf := cardPanel
				w, h := int(math.Ceil(float64(toolColW(sc.span)))), int(toolRowH(sc.rows))
				if sc.desktop {
					surf = cardDesktop
					w, h = pinCardWidth("sysmon", sc.span), pinCardH
				}
				issues = nil
				v := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(panelTheme)
					a.toolCard(c, layout.Card{ID: "sysmon", Span: sc.span, Rows: sc.rows}, surf).
						Width(float32(w)).Height(float32(h))
				}, w, h)
				v.SetLocale(lang)
				v.Frame()
				v.Frame()
				if len(issues) > 0 {
					t.Errorf("%s (%s): fitIssues = %v", sc.name, lang, issues)
				}
			}

			// Clicking 1x1 card on tools page cycles cpu -> mem -> net -> cpu.
			a.settings.SysMon.PrimaryMetric = settings.SysMonMetricCPU
			a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "sysmon", Span: 1, Rows: 1}}}
			v := ui.NewTester(a.view, int(math.Ceil(float64(a.pageShellW())))+2*earR, int(math.Ceil(float64(a.pageShellH())))+20)
			v.SetLocale(lang)
			v.Frame()
			v.Frame()
			l := locales.In(lang)
			tapRect, ok := v.Find(l.SysMonCPU())
			if !ok {
				t.Fatalf("missing 1x1 CPU label; texts=%v", v.Texts())
			}
			v.ClickAt(tapRect.X+tapRect.W/2, tapRect.Y+tapRect.H/2)
			v.Advance(500 * time.Millisecond)
			v.Frame()
			if a.settings.SysMon.PrimaryMetric != settings.SysMonMetricMem {
				t.Fatalf("after 1st tap: metric = %q, want mem", a.settings.SysMon.PrimaryMetric)
			}
			v.ClickAt(tapRect.X+tapRect.W/2, tapRect.Y+tapRect.H/2)
			v.Advance(500 * time.Millisecond)
			v.Frame()
			if a.settings.SysMon.PrimaryMetric != settings.SysMonMetricNet {
				t.Fatalf("after 2nd tap: metric = %q, want net", a.settings.SysMon.PrimaryMetric)
			}
			v.ClickAt(tapRect.X+tapRect.W/2, tapRect.Y+tapRect.H/2)
			v.Advance(500 * time.Millisecond)
			v.Frame()
			if a.settings.SysMon.PrimaryMetric != settings.SysMonMetricCPU {
				t.Fatalf("after 3rd tap: metric = %q, want cpu (%s)", a.settings.SysMon.PrimaryMetric, l.SysMonCPU())
			}
		})
	}

	// Verify nil a.sysmon renders safely without panic.
	bare := &shelf{dataDir: t.TempDir(), settings: settings.Default()}
	bare.ensurePages()
	bv := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(panelTheme)
		bare.toolCard(c, layout.Card{ID: "sysmon", Span: 2, Rows: 2}, cardPanel).Width(205).Height(174)
	}, 205, 174)
	bv.Frame()
	if !bv.HasText("--") {
		t.Fatalf("bare nil sysmon card expected '--', got %v", bv.Texts())
	}
}

func TestSysMonPopover(t *testing.T) {
	origFit := fitIssue
	var issues []string
	fitIssue = func(msg string) { issues = append(issues, msg) }
	t.Cleanup(func() { fitIssue = origFit })

	a, reader, now := makeTestSysMonShelf(t, "zh-Hans", []layout.Card{
		{ID: "ocr", Span: 1, Rows: 1},
		{ID: "shot", Span: 1, Rows: 1},
		{ID: "sysmon", Span: 2, Rows: 2},
		{ID: "clip", Span: 2, Rows: 1},
	})
	a.visible = true
	a.level = 2
	a.tab = pageTools
	for range 20 {
		stepFakeSysMon(reader, now, time.Second, 42, 68, 22_000_000, 3_500_000)
		a.sysmon.tick(*now)
	}

	tp := a.toolsPage()
	w := int(math.Ceil(float64(a.pageShellW()))) + 2*earR
	h := int(math.Ceil(float64(a.pageShellH()))) + navGap + navH + 4
	v := ui.NewTester(a.view, w, h)
	v.SetLocale("zh-Hans")
	v.Frame()
	v.Frame()

	// Move pointer over sysmon cell (row 0 col 2..3).
	var smCell toolCell
	for _, cell := range tp.grid().cells {
		if cell.id == "sysmon" {
			smCell = cell
		}
	}
	if smCell.id == "" {
		t.Fatal("sysmon cell not found in grid")
	}
	cardCX := float32(earR+sheetPadX) + smCell.x + smCell.w/2
	cardCY := toolsContentTop() + smCell.y + smCell.h/2
	v.Move(cardCX, cardCY)
	v.Frame()
	v.Frame()
	if !tp.sysmonPopOpen || !v.HasText("60s 前") || !v.HasText("占用排行") {
		t.Fatalf("popover did not open cleanly: open=%v texts=%v", tp.sysmonPopOpen, v.Texts())
	}
	if !a.sysmon.snap.CPUReady || a.sysmon.snap.CPUPercent <= 0 {
		t.Fatalf("opening popover cleared CPU baseline: ready=%v pct=%.1f", a.sysmon.snap.CPUReady, a.sysmon.snap.CPUPercent)
	}
	if len(issues) > 0 {
		t.Fatalf("popover fitIssues = %v", issues)
	}

	// Slide slowly onto the "网络" focus pill inside popover (sticky hover test).
	netBtn, ok := v.AccessByRole(ui.RoleButton, "网络")
	if !ok {
		t.Fatalf("missing 网络 focus button in popover; texts=%v", v.Texts())
	}
	tx, ty := netBtn.Bounds.X+netBtn.Bounds.W/2, netBtn.Bounds.Y+netBtn.Bounds.H/2
	const steps = 40
	for i := 1; i <= steps; i++ {
		k := float32(i) / steps
		v.Move(cardCX+(tx-cardCX)*k, cardCY+(ty-cardCY)*k)
		v.Frame()
	}
	if !tp.sysmonPopOpen {
		t.Fatal("popover closed while sliding onto focus tab")
	}
	v.ClickAt(tx, ty)
	v.Frame()
	if a.sysmon.activeFocusTab() != settings.SysMonMetricNet {
		t.Fatalf("focusTab = %q, want net", a.sysmon.activeFocusTab())
	}

	// Click "占用排行 · 内存" sort pill inside popover.
	sortBtn, ok := v.AccessByRole(ui.RoleButton, "占用排行 · 内存")
	if !ok {
		t.Fatalf("missing sort mem button in popover")
	}
	v.ClickAt(sortBtn.Bounds.X+sortBtn.Bounds.W/2, sortBtn.Bounds.Y+sortBtn.Bounds.H/2)
	v.Frame()
	if a.sysmon.activeProcSort() != settings.SysMonMetricMem {
		t.Fatalf("procSort = %q, want mem", a.sysmon.activeProcSort())
	}

	// Test ShowTopProcs == false hides Top 3 section.
	a.settings.SysMon.ShowTopProcs = false
	v.Frame()
	v.Frame()
	if v.HasText("占用排行") {
		t.Fatal("expected 占用排行 hidden when ShowTopProcs=false")
	}
	a.settings.SysMon.ShowTopProcs = true

	// Test 1-row tools page compact popover (<220pt height) has zero fitIssue and does not overflow shellH.
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{
		{ID: "ocr", Span: 1, Rows: 1},
		{ID: "sysmon", Span: 2, Rows: 1},
	}}
	tp.sysmonPopOpen = false
	tp.sysmonPopHovered = false
	v1 := ui.NewTester(a.view, int(math.Ceil(float64(a.pageShellW())))+2*earR, int(math.Ceil(float64(a.pageShellH())))+navGap+navH+4)
	v1.SetLocale("zh-Hans")
	v1.Frame()
	v1.Frame()
	issues = nil
	for _, cell := range tp.grid().cells {
		if cell.id == "sysmon" {
			smCell = cell
		}
	}
	v1.Move(float32(earR+sheetPadX)+smCell.x+smCell.w/2, toolsContentTop()+smCell.y+smCell.h/2)
	v1.Frame()
	v1.Frame()
	if !tp.sysmonPopOpen || !v1.HasText("60s 前") || v1.HasText("占用排行") {
		t.Fatalf("1-row compact popover unexpected: open=%v texts=%v", tp.sysmonPopOpen, v1.Texts())
	}
	if len(issues) > 0 {
		t.Fatalf("1-row compact popover fitIssues = %v", issues)
	}
}

func TestSysMonWingsPriorityAndFit(t *testing.T) {
	origFit := fitIssue
	var issues []string
	fitIssue = func(msg string) { issues = append(issues, msg) }
	t.Cleanup(func() { fitIssue = origFit })

	a, reader, now := makeTestSysMonShelf(t, "zh-Hans", nil)
	a.settings.SysMon.WingsEnabled = true
	stepFakeSysMon(reader, now, time.Second, 24, 62, 18_400_000, 1_200_000)
	a.sysmon.tick(*now)
	a.np = &nowPlayingModule{
		host: a,
		have: true,
		last: platform.NowPlayingState{
			Title: "Test Song", Artist: "Tester", Duration: 180, Elapsed: 10, Rate: 1, Received: *now,
		},
	}

	// Resting (!peeking): sysmon wings show even when music track is loaded!
	a.peeking, a.peekProgress, a.peekFrom = false, 1, 1
	v := ui.NewTester(a.view, 500, 100)
	v.SetLocale("zh-Hans")
	v.Frame()
	v.Frame()
	if a.fx.peekKind != peekSysmonWings {
		t.Fatalf("resting peekKind = %v, want peekSysmonWings (%v)", a.fx.peekKind, peekSysmonWings)
	}

	// Hovering yields to music wings (peekWings).
	a.peeking = true
	v.Frame()
	v.Frame()
	if a.fx.peekKind != peekWings {
		t.Fatalf("hover with track peekKind = %v, want peekWings (%v)", a.fx.peekKind, peekWings)
	}

	// Clear track, still hovering -> yields to chevron.
	a.np.have = false
	v.Frame()
	v.Frame()
	if a.fx.peekKind != peekChevron {
		t.Fatalf("hover without track peekKind = %v, want peekChevron (%v)", a.fx.peekKind, peekChevron)
	}
	a.peeking = false
	v.Frame()
	if a.fx.peekKind != peekSysmonWings {
		t.Fatalf("after hover restore peekKind = %v, want peekSysmonWings", a.fx.peekKind)
	}

	// Higher-priority states preempt sysmon wings and restore cleanly when cleared.
	a.registerClockModules()
	a.clock.now = func() time.Time { return *now }
	a.clock.startTimer(*now, 5*time.Minute)
	v.Frame()
	if a.fx.peekKind != peekTimerWings {
		t.Fatalf("timer preemption kind = %v, want peekTimerWings", a.fx.peekKind)
	}
	a.clock.stopTimer(*now)
	v.Frame()
	if a.fx.peekKind != peekSysmonWings {
		t.Fatalf("after timer stop kind = %v, want peekSysmonWings", a.fx.peekKind)
	}

	a.dragging = true
	v.Frame()
	if a.fx.peekKind != peekHint {
		t.Fatalf("drag preemption kind = %v, want peekHint", a.fx.peekKind)
	}
	a.dragging = false
	v.Frame()
	if a.fx.peekKind != peekSysmonWings {
		t.Fatalf("after drag end kind = %v, want peekSysmonWings", a.fx.peekKind)
	}

	// Settled shell covers resting sysmon wings even when peekProgress == 0,
	// and sysmonWingsShown yields when App Switcher or timer is active.
	a.peeking, a.peekProgress = false, 0
	sw, _ := a.sysmonWingsSize()
	if gotW, _, _, gotOpenK := a.settledShell(); gotW != sw || gotOpenK != 0 {
		t.Fatalf("settledShell for resting sysmon wings = (w=%.1f, openK=%.1f), want (w=%.1f, openK=0)", gotW, gotOpenK, sw)
	}
	if !a.sysmonWingsShown() {
		t.Fatal("sysmonWingsShown = false while resting")
	}
	a.apps.on = true
	if a.sysmonWingsShown() {
		t.Fatal("sysmonWingsShown = true while appsModeOn is active")
	}
	a.apps.on = false
	a.peekProgress = 1

	// Stress values fit + zero overlap with Mac center notch cutout.
	stepFakeSysMon(reader, now, time.Second, 100, 99, 1_300_000_000, 1_046_000_000)
	a.sysmon.tick(*now)
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, island := range []bool{false, true} {
			a.island = island
			a.settings.SysMon.WingLeft = settings.SysMonWingCPU
			a.settings.SysMon.WingRight = settings.SysMonWingNetBoth
			issues = nil
			wv := ui.NewTester(a.view, 500, 100)
			wv.SetLocale(lang)
			wv.Frame()
			wv.Frame()
			if len(issues) > 0 {
				t.Errorf("wings lang=%s island=%v fitIssues=%v", lang, island, issues)
			}
			if !island {
				rLeft, okL := wv.Find("CPU 100%")
				rRight, okR := wv.Find("↓1.21G ↑998M")
				if !okL || !okR {
					t.Fatalf("notch wings missing texts (lang=%s): okL=%v okR=%v texts=%v", lang, okL, okR, wv.Texts())
				}
				notchLeftEdge := (float32(500) - float32(a.notchW)) / 2
				notchRightEdge := (float32(500) + float32(a.notchW)) / 2
				if rLeft.X+rLeft.W > notchLeftEdge+0.5 {
					t.Errorf("left wing [%.1f..%.1f] enters notch [%.1f..%.1f]", rLeft.X, rLeft.X+rLeft.W, notchLeftEdge, notchRightEdge)
				}
				if rRight.X < notchRightEdge-0.5 {
					t.Errorf("right wing [%.1f..%.1f] enters notch [%.1f..%.1f]", rRight.X, rRight.X+rRight.W, notchLeftEdge, notchRightEdge)
				}
			}
		}
	}
}

func TestSysMonSettingsControls(t *testing.T) {
	origWin := forWindows
	origFit := settingsFit
	t.Cleanup(func() {
		forWindows = origWin
		settingsFit = origFit
	})

	for _, tc := range []struct {
		name string
		win  bool
	}{
		{"macOS", false},
		{"Windows", true},
	} {
		for _, lang := range []string{"zh-Hans", "en"} {
			t.Run(tc.name+"/"+lang, func(t *testing.T) {
				forWindows = tc.win
				var fitErrs []string
				settingsFit = func(msg string) { fitErrs = append(fitErrs, msg) }

				a, _, _ := makeTestSysMonShelf(t, lang, nil)
				a.settingsPage = settingsPageNotch
				v := ui.NewTester(a.settingsView, settingsMinWidth, settingsHeight)
				if tc.win {
					v.SetTitleBar(ui.TitleBar{Height: 32, Right: 138})
				}
				v.SetLocale(lang)
				v.Frame()
				v.Frame()

				// Scroll to reveal the sysmon section cleanly.
				a.settingsScroll.Y = 400
				v.Frame()
				v.Frame()

				l := locales.In(lang)
				if !v.HasText(l.SettingsSysMonGroup()) {
					t.Fatalf("missing SettingsSysMonGroup %q; texts=%v", l.SettingsSysMonGroup(), v.Texts())
				}
				if len(fitErrs) > 0 {
					t.Errorf("%s/%s settingsFit errors: %v", tc.name, lang, fitErrs)
				}

				// Click Wings toggle -> enables WingsEnabled and saves to settings.json.
				wingsLabel := osText(l.SettingsSysMonWingsRow(), l.SettingsSysMonWingsRowWin())
				if err := v.Click(wingsLabel); err != nil {
					t.Fatalf("click %q: %v", wingsLabel, err)
				}
				v.Frame()
				if !a.settings.SysMon.WingsEnabled {
					t.Fatal("WingsEnabled did not toggle on")
				}

				// Click Interval 2s segment -> updates IntervalSec and persists.
				if err := v.Click(l.SettingsSysMonInterval2s()); err != nil {
					t.Fatalf("click %q: %v", l.SettingsSysMonInterval2s(), err)
				}
				v.Frame()
				if a.settings.SysMon.IntervalSec != 2 {
					t.Fatalf("IntervalSec = %d, want 2", a.settings.SysMon.IntervalSec)
				}
				loaded, err := settings.Load(a.dataDir)
				if err != nil {
					t.Fatal(err)
				}
				if !loaded.SysMon.WingsEnabled || loaded.SysMon.IntervalSec != 2 {
					t.Fatalf("persisted SysMon = %+v", loaded.SysMon)
				}
			})
		}
	}
}

func TestSysMonExportShots(t *testing.T) {
	dir := os.Getenv("RAKKO_SYSMON_SHOT_DIR")
	if dir == "" {
		t.Skip("RAKKO_SYSMON_SHOT_DIR not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG := func(name string, img image.Image) {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. mygo-sysmon-cards.png: all card sizes, stress, desktop no-battery, and pins.
	aCards, rCards, nCards := makeTestSysMonShelf(t, "zh-Hans", []layout.Card{
		{ID: "sysmon", Span: 2, Rows: 1},
	})
	aCards.tab = pageTools
	aCards.level = 2
	aCards.visible = true
	for i := range 36 {
		cpu := 18 + 14*math.Sin(float64(i)*0.35) + float64(i%5)*2
		mem := 61 + 5*math.Sin(float64(i)*0.2)
		stepFakeSysMon(rCards, nCards, time.Second, cpu, mem, 16_000_000+uint64(i*450_000), 1_400_000+uint64(i*90_000))
		aCards.sysmon.tick(*nCards)
	}
	if !aCards.sysmon.snap.Ready || !aCards.sysmon.snap.CPUReady {
		t.Fatalf("aCards failed to populate sysmon snapshot: ready=%v cpuReady=%v", aCards.sysmon.snap.Ready, aCards.sysmon.snap.CPUReady)
	}
	aStress, _, _ := makeTestSysMonShelf(t, "zh-Hans", nil)
	aStress.sysmon.snap = aCards.sysmon.snap
	aStress.sysmon.hist = aCards.sysmon.hist
	aStress.sysmon.snap.CPUPercent = 94
	aStress.sysmon.snap.MemPercent = 91
	aStress.sysmon.snap.MemUsed = aStress.sysmon.snap.MemTotal * 91 / 100
	aStress.sysmon.snap.NetDownBps = 1_300_000_000
	aStress.sysmon.snap.NetUpBps = 1_046_000_000
	aStress.sysmon.snap.DiskPercent = 91
	aStress.sysmon.snap.Battery = sysmon.BatteryInfo{Present: false}

	aMem, _, _ := makeTestSysMonShelf(t, "zh-Hans", nil)
	aMem.sysmon.snap = aCards.sysmon.snap
	aMem.sysmon.hist = aCards.sysmon.hist
	aMem.settings.SysMon.PrimaryMetric = settings.SysMonMetricMem
	aNet, _, _ := makeTestSysMonShelf(t, "zh-Hans", nil)
	aNet.sysmon.snap = aCards.sysmon.snap
	aNet.sysmon.hist = aCards.sysmon.hist
	aNet.settings.SysMon.PrimaryMetric = settings.SysMonMetricNet

	cardsTester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(panelTheme)
		ui.Column(c.Key("shot-cards")).Fill().Background(ui.Hex("#0D0D10")).Padding(18).Gap(16).Children(func() {
			ui.Text(c, "Style A · Panel Cards & Desktop Pins (1x1 / 2x1 / 2x2 / Stress / Desktop No-Battery)").
				FontSize(12).FontWeight(600).TextColor(ink2)
			ui.Row(c.Key("r1")).Gap(12).AlignItems(ui.Start).Children(func() {
				aCards.toolCard(c, layout.Card{ID: "sysmon", Span: 1, Rows: 1}, cardPanel, "s1").Width(toolColW(1)).Height(84)
				aMem.toolCard(c, layout.Card{ID: "sysmon", Span: 1, Rows: 1}, cardPanel, "s2").Width(toolColW(1)).Height(84)
				aNet.toolCard(c, layout.Card{ID: "sysmon", Span: 1, Rows: 1}, cardPanel, "s3").Width(toolColW(1)).Height(84)
				aCards.toolCard(c, layout.Card{ID: "sysmon", Span: 2, Rows: 1}, cardPanel, "s4").Width(toolColW(2)).Height(84)
			})
			ui.Row(c.Key("r2")).Gap(12).AlignItems(ui.Start).Children(func() {
				aStress.toolCard(c, layout.Card{ID: "sysmon", Span: 2, Rows: 2}, cardPanel, "s5").Width(toolColW(2)).Height(174)
				ui.Column(c.Key("r2-col")).Gap(10).Children(func() {
					ui.Row(c.Key("r2-top")).Gap(12).Children(func() {
						ui.Column(c.Key("pin1")).Width(136).Height(84).Radius(18).Clip().Background(surface).Border(0.5, pinEdgeColor).Children(func() {
							aCards.toolCard(c, layout.Card{ID: "sysmon", Span: 1, Rows: 1}, cardDesktop, "s6").Fill()
						})
						ui.Column(c.Key("pin2")).Width(278).Height(84).Radius(18).Clip().Background(surface).Border(0.5, pinEdgeColor).Children(func() {
							aCards.toolCard(c, layout.Card{ID: "sysmon", Span: 2, Rows: 1}, cardDesktop, "s7").Fill()
						})
					})
					aStress.toolCard(c, layout.Card{ID: "sysmon", Span: 2, Rows: 1}, cardPanel, "s8").Width(toolColW(2)).Height(84)
				})
			})
		})
	}, 680, 340)
	cardsTester.SetScale(2)
	cardsTester.SetLocale("zh-Hans")
	cardsTester.Frame()
	cardsTester.Frame()
	writePNG("mygo-sysmon-cards.png", cardsTester.Image())

	// 2. mygo-sysmon-popover.png: real 2-row tools page with 300pt hover detail popover open.
	aPop, rPop, nPop := makeTestSysMonShelf(t, "zh-Hans", []layout.Card{
		{ID: "ocr", Span: 1, Rows: 1},
		{ID: "shot", Span: 1, Rows: 1},
		{ID: "sysmon", Span: 2, Rows: 2},
		{ID: "clip", Span: 2, Rows: 1},
	})
	aPop.visible = true
	aPop.level = 2
	aPop.tab = pageTools
	aPop.sysmon.setPopoverOpen(true)
	for i := range 36 {
		stepFakeSysMon(rPop, nPop, time.Second, 36+12*math.Sin(float64(i)*0.4), 66, 20_000_000+uint64(i*500_000), 2_400_000)
		aPop.sysmon.tick(*nPop)
	}
	popW := int(math.Ceil(float64(aPop.pageShellW()))) + 2*earR
	popH := int(math.Ceil(float64(aPop.pageShellH()))) + navGap + navH + 8
	popTester := ui.NewTester(aPop.view, popW, popH)
	popTester.SetScale(2)
	popTester.SetLocale("zh-Hans")
	popTester.Frame()
	popTester.Frame()
	var smCell toolCell
	for _, cell := range aPop.toolsPage().grid().cells {
		if cell.id == "sysmon" {
			smCell = cell
		}
	}
	popTester.Move(float32(earR+sheetPadX)+smCell.x+smCell.w/2, toolsContentTop()+smCell.y+smCell.h/2)
	popTester.Frame()
	popTester.Frame()
	writePNG("mygo-sysmon-popover.png", popTester.Image())

	// 3. mygo-sysmon-wings.png: Mac notch normal, Mac notch stress, and Dynamic Island wings.
	aWing, _, _ := makeTestSysMonShelf(t, "zh-Hans", nil)
	aWing.settings.SysMon.WingsEnabled = true
	aWing.peekProgress, aWing.peekFrom = 1, 1
	aWing.sysmon.snap = aCards.sysmon.snap
	aWing.sysmon.hist = aCards.sysmon.hist

	renderWing := func(island bool, stress bool) *image.RGBA {
		aWing.island = island
		if island {
			aWing.hasNotch, aWing.notchW, aWing.notchH, aWing.topInset = false, 0, 0, 0
			aWing.islandTop = islandTopGap
		} else {
			aWing.hasNotch, aWing.notchW, aWing.notchH, aWing.topInset = true, 180, 32, 32
			aWing.islandTop = 0
		}
		if stress {
			aWing.sysmon.snap.CPUPercent = 92
			aWing.sysmon.snap.NetDownBps = 1_300_000_000
			aWing.sysmon.snap.NetUpBps = 1_046_000_000
			aWing.settings.SysMon.WingRight = settings.SysMonWingNetBoth
		} else {
			aWing.sysmon.snap.CPUPercent = 24
			aWing.sysmon.snap.NetDownBps = 18_400_000
			aWing.settings.SysMon.WingRight = settings.SysMonWingNetDown
		}
		wt := ui.NewTester(aWing.view, 460, 56)
		wt.SetScale(2)
		wt.SetLocale("zh-Hans")
		wt.Frame()
		wt.Frame()
		return wt.Image()
	}
	w1 := renderWing(false, false)
	w2 := renderWing(false, true)
	w3 := renderWing(true, false)
	canvas := image.NewRGBA(image.Rect(0, 0, w1.Bounds().Dx(), w1.Bounds().Dy()*3+24))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{R: 0x1e, G: 0x20, B: 0x28, A: 0xff}), image.Point{}, draw.Src)
	draw.Draw(canvas, w1.Bounds(), w1, image.Point{}, draw.Over)
	r2Rect := image.Rect(0, w1.Bounds().Dy()+12, w2.Bounds().Dx(), w1.Bounds().Dy()*2+12)
	draw.Draw(canvas, r2Rect, w2, image.Point{}, draw.Over)
	r3Rect := image.Rect(0, w1.Bounds().Dy()*2+24, w3.Bounds().Dx(), w1.Bounds().Dy()*3+24)
	draw.Draw(canvas, r3Rect, w3, image.Point{}, draw.Over)
	writePNG("mygo-sysmon-wings.png", canvas)

	// 4. mygo-sysmon-settings.png: Settings 外形与提示 scrolled to 系统性能监控 group.
	aSet, _, _ := makeTestSysMonShelf(t, "zh-Hans", nil)
	aSet.settings.SysMon.WingsEnabled = true
	aSet.settingsPage = settingsPageNotch
	st := ui.NewTester(aSet.settingsView, settingsWidth, settingsHeight)
	st.SetScale(2)
	st.SetLocale("zh-Hans")
	st.Frame()
	aSet.settingsScroll.Y = 400
	st.Frame()
	st.Frame()
	writePNG("mygo-sysmon-settings.png", st.Image())
}
