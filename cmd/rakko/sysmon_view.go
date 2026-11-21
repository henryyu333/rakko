package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/settings"
	"rakko/internal/sysmon"
	"rakko/locales"
)

var sysmonSymbol = symbol(`<path d="M3 12h3.5l2.5-6.5 3.5 13 2.5-6.5H21"/>`)

func sysmonLoadColor(pct float64, base ui.Color) ui.Color {
	switch {
	case pct >= 90:
		return danger
	case pct >= 75:
		return warning
	default:
		return base
	}
}

func sysmonBatteryColor(b sysmon.BatteryInfo, base ui.Color) ui.Color {
	if !b.Present {
		return base
	}
	if b.Charging {
		return success
	}
	if b.Percent <= 15 {
		return danger
	}
	return base
}

func sysmonFitSize(c *ui.Context, text string, weight int, features string, base, floor, avail float32) float32 {
	if avail <= 0 || text == "" {
		return base
	}
	size := base
	for ; size > floor; size -= 0.5 {
		w, _ := c.MeasureText(0, ui.Span{Text: text, Size: size, Weight: weight, Features: features})
		if w <= avail {
			return size
		}
	}
	return floor
}

func sysmonFitText(c *ui.Context, text string, base, floor float32, weight int, features string, avail float32) ui.Element {
	size := sysmonFitSize(c, text, weight, features, base, floor, avail)
	el := fitText(c, text, size, weight, avail)
	if features != "" {
		el.FontFeatures(features)
	}
	return el
}

func sysmonPercentText(snap sysmon.Snapshot, pct float64, cpu bool) string {
	if cpu && !snap.CPUReady {
		return "--"
	}
	if !cpu && !snap.MemReady {
		return "--"
	}
	return sysmon.FormatPercent(pct)
}

func sysmonBatteryCompactText(b sysmon.BatteryInfo, l locales.Messages) string {
	pct := sysmon.FormatPercent(float64(b.Percent))
	if b.Charging {
		return "⚡" + pct
	}
	return l.SysMonBatteryShort() + " " + pct
}

func sysmonBatteryBadge(c *ui.Context, key string, b sysmon.BatteryInfo, l locales.Messages, base, floor float32, weight int, avail float32, baseCol ui.Color) ui.Element {
	col := sysmonBatteryColor(b, baseCol)
	pct := sysmon.FormatPercent(float64(b.Percent))
	if b.Charging {
		iw := max(7, base*0.72)
		ih := max(9, base*0.92)
		row := ui.Row(c.Key(key)).Gap(2).AlignItems(ui.Center)
		row.Children(func() {
			ui.Icon(c, liveBoltSym).Size(iw, ih).TextColor(col)
			sysmonFitText(c, pct, base, floor, weight, "tnum", max(20, avail-iw-2)).TextColor(col)
		})
		return row
	}
	return sysmonFitText(c, l.SysMonBatteryShort()+" "+pct, base, floor, weight, "tnum", avail).TextColor(col)
}

func sysmonNetSeries(hist []uint64, cur uint64) (vals []float32, maxVal float32) {
	if len(hist) == 0 && cur > 0 {
		vals = []float32{float32(cur)}
		return vals, max(float32(cur)*1.15, 1024)
	}
	vals = make([]float32, len(hist))
	var peak uint64
	for i, v := range hist {
		vals[i] = float32(v)
		if v > peak {
			peak = v
		}
	}
	if peak < 1024 {
		peak = 1024
	}
	return vals, float32(peak) * 1.15
}

func sysmonPeakRate(hist []uint64, cur uint64) uint64 {
	peak := cur
	for _, v := range hist {
		if v > peak {
			peak = v
		}
	}
	return peak
}

func sysmonPeakPct(hist []float32, cur float64) float64 {
	peak := cur
	for _, v := range hist {
		if float64(v) > peak {
			peak = float64(v)
		}
	}
	return peak
}

func sysmonSparkline(c *ui.Context, key string, values []float32, maxVal float32, strokeCol ui.Color, showGrid bool, strokeW float32) ui.Element {
	box := ui.Box(c.Key(key)).MinHeight(0).MinWidth(0)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		if r.W <= 1 || r.H <= 1 {
			return
		}
		if showGrid {
			p.Line(r.X, r.Y+0.25, r.X+r.W, r.Y+0.25, 0.5, usageGrid)
			p.Line(r.X, r.Y+r.H/2, r.X+r.W, r.Y+r.H/2, 0.5, usageGrid)
			p.Line(r.X, r.Y+r.H-0.25, r.X+r.W, r.Y+r.H-0.25, 0.5, usageBase)
		}
		n := len(values)
		if n == 0 {
			if !showGrid {
				p.Line(r.X, r.Y+r.H-1, r.X+r.W, r.Y+r.H-1, 1, fill2)
			}
			return
		}
		mv := maxVal
		if mv <= 0 {
			mv = 1
		}
		padY := float32(1.5)
		x0, x1 := r.X+0.5, r.X+r.W-0.5
		yBottom := r.Y + r.H - padY
		plotH := max(1, r.H-2*padY)
		yAt := func(v float32) float32 {
			frac := min(max(v/mv, 0), 1)
			return yBottom - frac*plotH
		}
		if n == 1 {
			y := yAt(values[0])
			var area ui.Path
			area.MoveTo(x0, yBottom).LineTo(x0, y).LineTo(x1, y).LineTo(x1, yBottom).Close()
			p.FillPath(&area, strokeCol.Alpha(0.14))
			p.Line(x0, y, x1, y, strokeW, strokeCol)
			return
		}
		var area, line ui.Path
		line.Cap(ui.RoundCap).Join(ui.RoundJoin)
		stepX := (x1 - x0) / float32(n-1)
		firstY := yAt(values[0])
		area.MoveTo(x0, yBottom).LineTo(x0, firstY)
		line.MoveTo(x0, firstY)
		for i := 1; i < n; i++ {
			x := x0 + float32(i)*stepX
			y := yAt(values[i])
			area.LineTo(x, y)
			line.LineTo(x, y)
		}
		area.LineTo(x1, yBottom).Close()
		p.FillPath(&area, strokeCol.Alpha(0.14))
		p.StrokePath(&line, strokeW, strokeCol)
	})
	return box
}

// toolSysMonBody renders Style A「静谧线描 · 实时走势」in 1×1, 2×1, 2×2, and
// fixed-height 84pt desktop pin shapes.
func (a *shelf) toolSysMonBody(c *ui.Context, card ui.Element, span, rows int, surf toolCardSurface) {
	effectiveRows := rows
	if surf == cardDesktop {
		effectiveRows = 1
	}
	w := cardW(layout.Card{ID: "sysmon", Span: span, Rows: effectiveRows}, surf)
	snap, hist := a.sysmonSnapshot()
	l := loc(c)

	card.Children(func() {
		switch {
		case span <= 1:
			a.toolSysMon1x1(c, w, snap, hist, l)
		case effectiveRows >= 2:
			a.toolSysMon2x2(c, w, snap, hist, l)
		default:
			a.toolSysMon2x1(c, w, snap, hist, l)
		}
	})
}

func (a *shelf) toolSysMon1x1(c *ui.Context, cardW float32, snap sysmon.Snapshot, hist sysmon.History, l locales.Messages) {
	innerW := cardW - 2*cardPad
	metric := a.settings.SysMon.Normalized().PrimaryMetric

	var (
		tag       string
		hero      string
		heroCol   ui.Color
		strokeCol ui.Color
		series    []float32
		maxVal    float32
		activeDot int
	)
	switch metric {
	case settings.SysMonMetricMem:
		tag = l.SysMonMemShort()
		hero = sysmonPercentText(snap, snap.MemPercent, false)
		heroCol = ink2
		strokeCol = ink.Alpha(0.82)
		if snap.MemReady {
			heroCol = sysmonLoadColor(snap.MemPercent, ink)
			strokeCol = sysmonLoadColor(snap.MemPercent, ink.Alpha(0.85))
		}
		series, maxVal = hist.Mem, 100
		activeDot = 1
	case settings.SysMonMetricNet:
		tag = l.SysMonNetShort()
		if snap.NetReady {
			hero = "↓" + sysmon.FormatRate(snap.NetDownBps, true)
			heroCol = ink
		} else {
			hero = "--"
			heroCol = ink2
		}
		strokeCol = ink.Alpha(0.85)
		series, maxVal = sysmonNetSeries(hist.NetDown, snap.NetDownBps)
		activeDot = 2
	default:
		tag = l.SysMonCPU()
		hero = sysmonPercentText(snap, snap.CPUPercent, true)
		heroCol = ink2
		strokeCol = ink.Alpha(0.82)
		if snap.CPUReady {
			heroCol = sysmonLoadColor(snap.CPUPercent, ink)
			strokeCol = sysmonLoadColor(snap.CPUPercent, ink.Alpha(0.85))
		}
		series, maxVal = hist.CPU, 100
		activeDot = 0
	}

	ui.Column(c.Key("sm-1x1")).Fill().Padding(cardPad).Justify(ui.SpaceBetween).Children(func() {
		ui.Row(c.Key("sm-1x1-top")).FillWidth().Height(15).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			fitText(c, tag, 10.5, 600, max(20, innerW-22)).TextColor(ink2)
			ui.Row(c.Key("sm-1x1-dots")).Gap(3).AlignItems(ui.Center).Children(func() {
				for i := range 3 {
					dotCol := fill3
					if i == activeDot {
						dotCol = ink
					}
					ui.Box(c.Key(fmt.Sprintf("d%d", i))).Size(3.5, 3.5).Radius(2).Background(dotCol)
				}
			})
		})
		sysmonSparkline(c, "sm-1x1-spark", series, maxVal, strokeCol, false, 1.5).FillWidth().Height(23)
		ui.Row(c.Key("sm-1x1-bot")).FillWidth().Height(22).AlignItems(ui.End).Children(func() {
			sysmonFitText(c, hero, 19, 10.5, 600, "tnum", innerW).TextColor(heroCol)
		})
	})
}

func (a *shelf) toolSysMon2x1(c *ui.Context, cardW float32, snap sysmon.Snapshot, hist sysmon.History, l locales.Messages) {
	innerW := cardW - 2*cardPad
	colGap := float32(10)
	colW := (innerW - colGap) / 2

	cpuStr := sysmonPercentText(snap, snap.CPUPercent, true)
	cpuCol := ink2
	cpuStroke := ink.Alpha(0.82)
	if snap.CPUReady {
		cpuCol = sysmonLoadColor(snap.CPUPercent, ink)
		cpuStroke = sysmonLoadColor(snap.CPUPercent, ink.Alpha(0.85))
	}

	memStr := sysmonPercentText(snap, snap.MemPercent, false)
	memCol := ink2
	memStroke := ink.Alpha(0.68)
	if snap.MemReady {
		memCol = sysmonLoadColor(snap.MemPercent, ink)
		memStroke = sysmonLoadColor(snap.MemPercent, ink.Alpha(0.78))
	}

	netStr := "↓-- ↑--"
	if snap.NetReady {
		netStr = "↓" + sysmon.FormatRate(snap.NetDownBps, true) + " ↑" + sysmon.FormatRate(snap.NetUpBps, true)
	}

	var rightStr string
	rightCol := ink2
	rightAvail := float32(54)
	if snap.Battery.Present {
		rightStr = sysmonBatteryCompactText(snap.Battery, l)
		rightCol = sysmonBatteryColor(snap.Battery, ink2)
		rightAvail = 50
	} else {
		if snap.DiskReady {
			rightStr = l.SysMonDiskShort() + " " + sysmon.FormatPercent(snap.DiskPercent)
			rightCol = sysmonLoadColor(snap.DiskPercent, ink2)
		} else {
			rightStr = l.SysMonDiskShort() + " --"
		}
		rightAvail = 58
	}

	ui.Column(c.Key("sm-2x1")).Fill().Padding(cardPad).Justify(ui.SpaceBetween).Children(func() {
		ui.Row(c.Key("sm-2x1-twins")).FillWidth().Height(45).Gap(colGap).Children(func() {
			drawTwin := func(key, label, val string, valCol, lineCol ui.Color, series []float32) {
				ui.Column(c.Key(key)).Width(colW).FillHeight().Justify(ui.SpaceBetween).Children(func() {
					ui.Row(c.Key("hdr")).FillWidth().Height(15).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
						valW := float32(36)
						fitText(c, label, 10.5, 600, max(20, colW-valW-4)).TextColor(ink2)
						sysmonFitText(c, val, 12.5, 9.5, 600, "tnum", valW).TextColor(valCol)
					})
					sysmonSparkline(c, "sp", series, 100, lineCol, false, 1.5).FillWidth().Height(26)
				})
			}
			drawTwin("cpu", l.SysMonCPU(), cpuStr, cpuCol, cpuStroke, hist.CPU)
			drawTwin("mem", l.SysMonMemShort(), memStr, memCol, memStroke, hist.Mem)
		})
		ui.Row(c.Key("sm-2x1-foot")).FillWidth().Height(14).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			leftAvail := max(40, innerW-rightAvail-6)
			sysmonFitText(c, netStr, 10, 8.5, 500, "tnum", leftAvail).TextColor(ink2)
			if snap.Battery.Present {
				sysmonBatteryBadge(c, "sm-2x1-batt", snap.Battery, l, 10, 8.5, 600, rightAvail, ink2)
			} else {
				sysmonFitText(c, rightStr, 10, 8.5, 600, "tnum", rightAvail).TextColor(rightCol)
			}
		})
	})
}

func (a *shelf) toolSysMon2x2(c *ui.Context, cardW float32, snap sysmon.Snapshot, hist sysmon.History, l locales.Messages) {
	innerW := cardW - 2*cardPad

	cpuStr := sysmonPercentText(snap, snap.CPUPercent, true)
	cpuCol := ink2
	cpuStroke := ink.Alpha(0.88)
	if snap.CPUReady {
		cpuCol = sysmonLoadColor(snap.CPUPercent, ink)
		cpuStroke = sysmonLoadColor(snap.CPUPercent, ink.Alpha(0.9))
	}

	memPair := "--"
	memValCol := ink2
	memFillCol := progress
	memFrac := float32(0)
	if snap.MemReady {
		memPair = sysmon.FormatMemPair(snap.MemUsed, snap.MemTotal)
		memValCol = sysmonLoadColor(snap.MemPercent, ink)
		memFillCol = sysmonLoadColor(snap.MemPercent, progress)
		memFrac = min(max(float32(snap.MemPercent/100), 0), 1)
	}

	netStr := "↓--  ↑--"
	if snap.NetReady {
		netStr = "↓" + sysmon.FormatRate(snap.NetDownBps, false) + "  ↑" + sysmon.FormatRate(snap.NetUpBps, false)
	}

	ui.Column(c.Key("sm-2x2")).Fill().Padding(cardPad).Justify(ui.SpaceBetween).Children(func() {
		// Section 1: CPU 60s area chart
		ui.Column(c.Key("sm-2x2-cpu")).FillWidth().Gap(3).Children(func() {
			ui.Row(c.Key("hdr")).FillWidth().Height(20).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				fitText(c, l.SysMonCPU(), 11, 600, 48).TextColor(ink2)
				sysmonFitText(c, cpuStr, 18, 13, 600, "tnum", innerW-54).TextColor(cpuCol)
			})
			sysmonSparkline(c, "chart", hist.CPU, 100, cpuStroke, true, 1.5).FillWidth().Height(48)
		})

		// Section 2: Memory progress bar
		ui.Column(c.Key("sm-2x2-mem")).FillWidth().Gap(4).Children(func() {
			ui.Row(c.Key("hdr")).FillWidth().Height(14).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				fitText(c, l.SysMonMem(), 10.5, 600, 62).TextColor(ink2)
				sysmonFitText(c, memPair, 10.5, 8.5, 600, "tnum", innerW-68).TextColor(memValCol)
			})
			track := ui.Box(c.Key("rail")).FillWidth().Height(4).Radius(2).Background(fill2)
			if memFrac > 0 {
				track.Children(func() {
					ui.Box(c.Key("fill")).Height(4).Width(max(2, innerW*memFrac)).Radius(2).Background(memFillCol)
				})
			}
		})

		// Section 3: Network throughput
		ui.Row(c.Key("sm-2x2-net")).FillWidth().Height(14).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			fitText(c, l.SysMonNet(), 10.5, 600, 56).TextColor(ink2)
			sysmonFitText(c, netStr, 10.5, 8.5, 500, "tnum", innerW-62).TextColor(ink)
		})

		// Section 4: Disk + optional Battery
		ui.Row(c.Key("sm-2x2-foot")).FillWidth().Height(14).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			if snap.Battery.Present {
				diskLeft := l.SysMonDiskShort() + " --"
				if snap.DiskReady {
					diskLeft = l.SysMonDiskShort() + " " + l.SysMonDiskFree(sysmon.FormatBytesShort(snap.DiskFree))
				}
				sysmonFitText(c, diskLeft, 10, 8.5, 500, "tnum", innerW-58).TextColor(ink2)
				sysmonBatteryBadge(c, "sm-2x2-batt", snap.Battery, l, 10, 8.5, 600, 52, ink2)
			} else {
				diskLeft := l.SysMonDisk() + " --"
				diskRight := "--"
				diskCol := ink2
				if snap.DiskReady {
					diskLeft = l.SysMonDisk() + " " + l.SysMonDiskFree(sysmon.FormatBytesShort(snap.DiskFree))
					diskRight = sysmon.FormatPercent(snap.DiskPercent)
					diskCol = sysmonLoadColor(snap.DiskPercent, ink2)
				}
				sysmonFitText(c, diskLeft, 10, 8.5, 500, "tnum", innerW-48).TextColor(ink2)
				sysmonFitText(c, diskRight, 10, 8.5, 600, "tnum", 42).TextColor(diskCol)
			}
		})
	})
}

// sysmonPopoverOverlay draws the 300pt hover detail popover over the tools
// page overlay layer so it is never cropped by the grid scroll viewport.
func (p *toolsPage) sysmonPopoverOverlay(c *ui.Context, live bool) {
	a := p.host
	if !live || p.editing || p.menu != nil || p.press != nil || p.rs != nil {
		p.sysmonPopOpen = false
		p.sysmonPopHovered = false
		if a.sysmon != nil {
			a.sysmon.setPopoverOpen(false)
		}
		return
	}
	wasOpen := p.sysmonPopOpen
	show := p.sysmonHoveredThisFrame
	tentative := false
	if wasOpen && !show {
		show = true
		tentative = true
	}
	if !show {
		p.sysmonPopHovered = false
		p.sysmonPopOpen = false
		if a.sysmon != nil {
			a.sysmon.setPopoverOpen(false)
		}
		return
	}

	shellW := a.pageShellW()
	shellH := a.pageShellH()
	popW := min(float32(300), max(214, shellW-16))
	if shellW < 420 && p.sysmonCellRect.W > 0 && p.sysmonCellRect.W <= toolColW(1)+1 {
		popW = min(popW, max(214, shellW-p.sysmonCellRect.W-20))
	}
	compact := shellH < 220

	showProcs := !compact && a.settings.SysMon.ShowTopProcs
	estH := float32(204)
	if compact {
		estH = 120
	} else if !showProcs {
		estH = 142
	}

	cardCX := p.sysmonCellRect.X + p.sysmonCellRect.W/2
	var popX float32
	if cardCX < shellW/2 {
		popX = min(p.sysmonCellRect.X+p.sysmonCellRect.W-4, shellW-popW-8)
	} else {
		popX = max(8, p.sysmonCellRect.X-popW+4)
	}
	popX = min(max(popX, 8), max(8, shellW-popW-8))

	popY := max(float32(bandH+2), min(p.sysmonCellRect.Y, max(float32(bandH+2), shellH-estH-6)))

	snap, hist := a.sysmonSnapshot()
	l := loc(c)
	focus := settings.SysMonMetricCPU
	sortBy := settings.SysMonMetricCPU
	if a.sysmon != nil {
		focus = a.sysmon.activeFocusTab()
		sortBy = a.sysmon.activeProcSort()
	}

	padV, padH, gap := float32(8), float32(12), float32(4)
	chartH := float32(38)
	if compact {
		padV, padH, gap = 7, 10, 3
		chartH = 24
	}
	innerW := popW - 2*padH

	pop := ui.Column(c.Key("sysmon-pop")).Absolute().Left(popX).Top(popY).Width(popW).
		Radius(rCard).Background(surfaceRaise).Border(0.5, usagePopEdge).
		Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.55)).
		Padding(padV, padH, padV, padH).Gap(gap)
	_, _, popOver := pop.PointerPosition()

	pop.Children(func() {
		// 1. Focus pills + active reading
		var heroStr string
		heroCol := ink
		var chartVals []float32
		var chartMax float32 = 100
		chartCol := ink.Alpha(0.88)
		var peakStr string

		switch focus {
		case settings.SysMonMetricMem:
			heroStr = sysmonPercentText(snap, snap.MemPercent, false)
			if snap.MemReady {
				heroCol = sysmonLoadColor(snap.MemPercent, ink)
				chartCol = sysmonLoadColor(snap.MemPercent, ink.Alpha(0.88))
				peakStr = l.SysMonPeak(sysmon.FormatPercent(sysmonPeakPct(hist.Mem, snap.MemPercent)))
			} else {
				heroCol = ink2
				peakStr = l.SysMonPeak("--")
			}
			chartVals = hist.Mem
		case settings.SysMonMetricNet:
			if snap.NetReady {
				heroStr = "↓" + sysmon.FormatRate(snap.NetDownBps, false)
				peakStr = l.SysMonPeak(sysmon.FormatRate(sysmonPeakRate(hist.NetDown, snap.NetDownBps), false))
			} else {
				heroStr = "--"
				heroCol = ink2
				peakStr = l.SysMonPeak("--")
			}
			chartVals, chartMax = sysmonNetSeries(hist.NetDown, snap.NetDownBps)
		default:
			heroStr = sysmonPercentText(snap, snap.CPUPercent, true)
			if snap.CPUReady {
				heroCol = sysmonLoadColor(snap.CPUPercent, ink)
				chartCol = sysmonLoadColor(snap.CPUPercent, ink.Alpha(0.88))
				peakStr = l.SysMonPeak(sysmon.FormatPercent(sysmonPeakPct(hist.CPU, snap.CPUPercent)))
			} else {
				heroCol = ink2
				peakStr = l.SysMonPeak("--")
			}
			chartVals = hist.CPU
		}

		hdrH := float32(20)
		if compact {
			hdrH = 18
		}
		ui.Row(c.Key("pop-hdr")).FillWidth().Height(hdrH).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c.Key("pop-tabs")).Gap(3).AlignItems(ui.Center).Children(func() {
				tabs := []struct {
					id, label string
				}{
					{settings.SysMonMetricCPU, l.SysMonCPU()},
					{settings.SysMonMetricMem, l.SysMonMem()},
					{settings.SysMonMetricNet, l.SysMonNet()},
				}
				for _, tab := range tabs {
					active := focus == tab.id
					btn := ui.ButtonBase(c.Key("pop-tab-" + tab.id)).Height(hdrH).Radius(6).
						Padding(0, 7, 0, 7).Center().Label(tab.label)
					fg := ink2
					if active {
						btn.Background(fill2)
						fg = ink
					} else if btn.Hovered() {
						btn.Background(fill1)
						fg = ink
					}
					btn.Children(func() {
						fitText(c, tab.label, 10.5, 600, 68).TextColor(fg)
					})
					if btn.Clicked() && a.sysmon != nil {
						a.sysmon.setFocusTab(tab.id)
					}
				}
			})
			sysmonFitText(c, heroStr, 13, 10, 600, "tnum", max(50, innerW-146)).TextColor(heroCol)
		})

		// 2. 60s area chart + axis
		ui.Column(c.Key("pop-chart-grp")).FillWidth().Gap(2).Children(func() {
			sysmonSparkline(c, "pop-chart", chartVals, chartMax, chartCol, true, 1.5).FillWidth().Height(chartH)
			ui.Row(c.Key("pop-axis")).FillWidth().Height(11).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				fitText(c, l.SysMonWindowAgoSec(sysmonWindowSeconds(a)), 9.5, 0, 80).TextColor(ink3)
				sysmonFitText(c, peakStr, 9.5, 8.5, 0, "tnum", innerW-76).TextColor(ink3)
			})
		})

		// 3. Memory 3-segment bar & breakdown
		ui.Column(c.Key("pop-mem-grp")).FillWidth().Gap(2).Children(func() {
			memPair := "--"
			memUsedStr := l.SysMonMemUsedLabel("--")
			memCol := ink2
			if snap.MemReady {
				memPair = sysmon.FormatMemPair(snap.MemUsed, snap.MemTotal)
				pctStr := sysmon.FormatPercent(snap.MemPercent)
				memUsedStr = l.SysMonMemUsedLabel(pctStr)
				memCol = sysmonLoadColor(snap.MemPercent, ink2)
			}
			ui.Row(c.Key("mem-hdr")).FillWidth().Height(13).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				sysmonFitText(c, l.SysMonMem()+" "+memPair, 10, 8.5, 600, "tnum", innerW-76).TextColor(ink2)
				sysmonFitText(c, memUsedStr, 10, 8.5, 600, "tnum", 70).TextColor(memCol)
			})
			ui.Box(c.Key("mem-seg")).FillWidth().Height(4).Radius(2).Background(fill2).Draw(func(p *ui.Painter, r ui.Rect) {
				if !snap.MemReady || snap.MemTotal == 0 {
					return
				}
				tot := float32(snap.MemTotal)
				seg := []struct {
					val uint64
					col ui.Color
				}{
					{snap.MemApp, ink.Alpha(0.86)},
					{snap.MemWired, ink.Alpha(0.48)},
					{snap.MemCompressed, ink.Alpha(0.24)},
				}
				p.Clip(r, 2, func() {
					x := r.X
					for _, s := range seg {
						if s.val == 0 {
							continue
						}
						sw := r.W * float32(s.val) / tot
						if x+sw > r.X+r.W {
							sw = r.X + r.W - x
						}
						if sw > 0.5 {
							p.Fill(ui.Rect{X: x, Y: r.Y, W: sw, H: r.H}, s.col, 0)
							x += sw
						}
					}
				})
			})
			appStr, wiredStr, compStr := "--", "--", "--"
			if snap.MemReady {
				appStr = sysmon.FormatBytesShort(snap.MemApp)
				wiredStr = sysmon.FormatBytesShort(snap.MemWired)
				compStr = sysmon.FormatBytesShort(snap.MemCompressed)
			}
			breakdown := osText(
				l.SysMonMemBreakdown(appStr, wiredStr, compStr),
				l.SysMonMemBreakdownWin(appStr, wiredStr, compStr),
			)
			ui.Row(c.Key("mem-sub")).FillWidth().Height(11).AlignItems(ui.Center).Children(func() {
				sysmonFitText(c, breakdown, 9.5, 8, 0, "tnum", innerW).TextColor(ink3)
			})
		})

		// 4. Disk + optional Battery
		ui.Row(c.Key("pop-disk")).FillWidth().Height(12).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			diskDetail := l.SysMonDiskDetail("--", "--", "--")
			if snap.DiskReady {
				diskDetail = l.SysMonDiskDetail(
					sysmon.FormatBytesShort(snap.DiskFree),
					sysmon.FormatRate(snap.DiskReadBps, true),
					sysmon.FormatRate(snap.DiskWriteBps, true),
				)
			}
			battAvail := float32(0)
			if snap.Battery.Present {
				battAvail = 52
			}
			sysmonFitText(c, diskDetail, 9.5, 8, 0, "tnum", innerW-battAvail-6).TextColor(ink2)
			if snap.Battery.Present {
				sysmonBatteryBadge(c, "sm-pop-batt", snap.Battery, l, 9.5, 8, 600, battAvail, ink2)
			}
		})

		// 5. Optional Top 3 processes (hidden in 1-row compact mode or when disabled in Settings)
		if showProcs {
			ui.Column(c.Key("pop-procs")).FillWidth().Gap(1).Children(func() {
				ui.Box(c.Key("div")).FillWidth().Height(1).Background(fill1).Margin(1, 0, 3, 0)
				ui.Row(c.Key("procs-hdr")).FillWidth().Height(15).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					fitText(c, l.SysMonTopProcs(), 10, 600, 120).TextColor(ink2)
					ui.Row(c.Key("sort-tabs")).Gap(3).AlignItems(ui.Center).Children(func() {
						for _, st := range []struct {
							id, label string
						}{
							{settings.SysMonMetricCPU, l.SysMonCPU()},
							{settings.SysMonMetricMem, l.SysMonMem()},
						} {
							active := sortBy == st.id
							btn := ui.ButtonBase(c.Key("sort-" + st.id)).Height(15).Radius(4).
								Padding(0, 5, 0, 5).Center().Label(l.SysMonTopProcs() + " · " + st.label)
							fg := ink3
							if active {
								btn.Background(fill2)
								fg = ink
							} else if btn.Hovered() {
								fg = ink2
							}
							btn.Children(func() {
								fitText(c, st.label, 9.5, 600, 48).TextColor(fg)
							})
							if btn.Clicked() && a.sysmon != nil {
								a.sysmon.setProcSort(st.id)
							}
						}
					})
				})
				procs := snap.TopCPU
				if sortBy == settings.SysMonMetricMem {
					procs = snap.TopMem
				}
				for i := range 3 {
					ui.Row(c.Key(fmt.Sprintf("pr-%d", i))).FillWidth().Height(13).AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
						if i < len(procs) {
							pr := procs[i]
							ui.Text(c, pr.Name).SingleLine().Ellipsis("…").FontSize(10).
								TextColor(ink).Grow(1).MinWidth(0)
							sysmonFitText(c, formatProcMetric(pr, sortBy), 10, 8.5, 500, "tnum", 52).TextColor(ink2)
						} else if i == 0 {
							ui.Text(c, "--").SingleLine().FontSize(10).TextColor(ink3)
						}
					})
				}
			})
		}
	})

	if tentative && !popOver {
		p.sysmonPopHovered = false
		p.sysmonPopOpen = false
	} else {
		p.sysmonPopHovered = popOver
		p.sysmonPopOpen = p.sysmonHoveredThisFrame || popOver
	}
	if a.sysmon != nil {
		a.sysmon.setPopoverOpen(p.sysmonPopOpen)
	}
}

// sysmonWingsSize is the closed resting sysmon wings' shell size: the anchor
// plus one visible wing on each side, matching the live charging/timer wings.
// sysmonWindowSeconds is how much time the 60-sample chart covers at the
// configured interval: the axis label must follow the interval, or a 2 s
// setting still claims "60s ago".
func sysmonWindowSeconds(a *shelf) int {
	interval := 1
	if a != nil && a.settings.SysMon.IntervalSec == 2 {
		interval = 2
	}
	return sysmon.HistoryCap * interval
}

func (a *shelf) sysmonWingsSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	return baseW + 2*float32(a.liveWingW()), baseH + npWingsBody
}

func (a *shelf) sysmonWingText(metric string, snap sysmon.Snapshot, l locales.Messages) (string, ui.Color) {
	switch metric {
	case settings.SysMonWingMem:
		txt := l.SysMonMemShort() + " " + sysmonPercentText(snap, snap.MemPercent, false)
		col := ink
		if snap.MemReady {
			col = sysmonLoadColor(snap.MemPercent, ink)
		}
		return txt, col
	case settings.SysMonWingNetDown:
		if !snap.NetReady {
			return "↓--", ink
		}
		return "↓" + sysmon.FormatRate(snap.NetDownBps, true), ink
	case settings.SysMonWingNetBoth:
		if !snap.NetReady {
			return "↓-- ↑--", ink
		}
		return "↓" + sysmon.FormatRate(snap.NetDownBps, true) + " ↑" + sysmon.FormatRate(snap.NetUpBps, true), ink
	case settings.SysMonWingDisk:
		txt := l.SysMonDiskShort() + " " + sysmonPercentText(snap, snap.DiskPercent, false)
		col := ink
		if snap.DiskReady {
			col = sysmonLoadColor(snap.DiskPercent, ink)
		}
		return txt, col
	case settings.SysMonWingBattery:
		if snap.Battery.Present {
			return sysmonBatteryCompactText(snap.Battery, l), sysmonBatteryColor(snap.Battery, ink)
		}
		// Desktops without a battery fall back gracefully to Disk instead of blank space.
		return a.sysmonWingText(settings.SysMonWingDisk, snap, l)
	default:
		txt := l.SysMonCPU() + " " + sysmonPercentText(snap, snap.CPUPercent, true)
		col := ink
		if snap.CPUReady {
			col = sysmonLoadColor(snap.CPUPercent, ink)
		}
		return txt, col
	}
}

// sysmonWings draws Style 1「纯净数字」in the closed notch / dynamic island
// wings, strictly outside the hardware notch cutout.
func (a *shelf) sysmonWings(c *ui.Context, shellW, shellH, k float32) {
	s := a.settings.SysMon.Normalized()
	snap, _ := a.sysmonSnapshot()
	l := loc(c)
	leftText, leftCol := a.sysmonWingText(s.WingLeft, snap, l)
	rightText, rightCol := a.sysmonWingText(s.WingRight, snap, l)

	padBottom := float32(npWingsBody)
	var wingMaxW float32
	if a.island {
		padBottom = 0
		wingMaxW = max(60, (shellW-2*liveWingPad-12)/2)
	} else {
		wingMaxW = max(56, float32(a.liveWingW()-liveWingPad-4))
	}

	row := ui.Row(c.Key("sysmon-wings")).Width(shellW).Height(shellH).
		Padding(0, liveWingPad, padBottom, liveWingPad).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).
		Opacity(min(max(k*k, 0), 1)).
		Label(l.SysMonWingLabel(leftText, rightText))
	row.Children(func() {
		ui.Row(c.Key("sm-wing-l")).Width(wingMaxW).AlignItems(ui.Center).Justify(ui.Start).Children(func() {
			if s.WingLeft == settings.SysMonWingBattery && snap.Battery.Present {
				sysmonBatteryBadge(c, "sm-wl-batt", snap.Battery, l, 11.5, 8.5, 600, wingMaxW, ink)
			} else {
				sysmonFitText(c, leftText, 11.5, 8.5, 600, "tnum", wingMaxW).TextColor(leftCol)
			}
		})
		ui.Row(c.Key("sm-wing-r")).Width(wingMaxW).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			if s.WingRight == settings.SysMonWingBattery && snap.Battery.Present {
				sysmonBatteryBadge(c, "sm-wr-batt", snap.Battery, l, 11.5, 8.5, 600, wingMaxW, ink)
			} else {
				sysmonFitText(c, rightText, 11.5, 8.5, 600, "tnum", wingMaxW).TextColor(rightCol)
			}
		})
	})
	if row.Clicked() {
		a.goHome()
	}
}
