//go:build darwin

package main

// 每周简报页（design/explore/notch-apps-brief-prototype.html）：从首页的
// 「上周简报」进来，560 × 600 的阅读尺寸，前后周切换。三段：Mac 使用
// （活跃 / 停留两种口径，须用户同意后才有数据）、AI 使用（只读本机日志，
// 费用是估算）、用 Rakko 完成（文件转换、框选文字、托盘取出）。
//
// 缺失的时段保持未知：没有采样的那一天不画柱、不当成 0；整周没有数据时
// 只说明还没有记录。统计文件损坏或写不进时照实说，原文件保留。各口径只存
// 每日汇总，不存应用切换的时间线。

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"rakko/internal/aiusage"
	"rakko/internal/weekly"
	"rakko/locales"
)

const (
	weeklyPageW  = 560 // the report's reading width
	weeklyPageH  = 600 // the shell height it asks for
	weeklyHeadH  = 36
	weeklyFootH  = 28
	weeklyInnerX = 20
)

var (
	// weeklySymbol is the ear icon and the home entry's mark: a bar chart.
	weeklySymbol = symbol(`<path d="M4 20v-6M9 20V9M14 20V5M19 20v-4M2.5 20h19"/>`)
	weeklyLeft   = symbol(`<path d="M14.5 6l-6 6 6 6"/>`)
	weeklyRight  = symbol(`<path d="M9.5 6l6 6-6 6"/>`)
	weeklyShield = symbol(`<path d="M12 3l7 3v5c0 5-3 8.5-7 10-4-1.5-7-5-7-10V6z"/>`)
	weeklyDots   = symbol(`<circle cx="6" cy="12" r="1.5"/><circle cx="12" cy="12" r="1.5"/><circle cx="18" cy="12" r="1.5"/>`)
)

// The report's own palette: the prototype's soft blue bars on the black
// shell, and the three tool tints.
var (
	weeklyBar       = ui.Hex("#A7BBD3")
	weeklyBarWeek   = ui.Hex("#6C7889")
	weeklyToolTints = []ui.Color{
		ui.Hex("#B5C2D6"), ui.Hex("#B59B89"), ui.Hex("#858193"),
		ui.Hex("#8FA6A0"), ui.Hex("#B0A7C4"), ui.Hex("#9AA6B5"),
	}
)

// --- panelPage ----------------------------------------------------------

func (f *weeklyFeature) id() pageID    { return pageWeekly }
func (f *weeklyFeature) label() string { return f.host.appLoc().WeeklyPageTitle() }
func (f *weeklyFeature) labelIn(l locales.Messages) string {
	return l.WeeklyPageTitle()
}
func (f *weeklyFeature) title() string { return f.host.appLoc().WeeklyPageTitle() }
func (f *weeklyFeature) titleIn(l locales.Messages) string {
	return l.WeeklyPageTitle()
}
func (f *weeklyFeature) enabled() bool   { return true }
func (f *weeklyFeature) fullBleed() bool { return true }
func (f *weeklyFeature) width() float32  { return weeklyPageW }
func (f *weeklyFeature) height() float32 { return weeklyPageH }
func (f *weeklyFeature) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, weeklySymbol)
}
func (f *weeklyFeature) actions(c *ui.Context) []ui.Element { return nil }

// body draws the reading page: a header with the week picker, the report
// in a scroll box, and the local-only footer.
func (f *weeklyFeature) body(c *ui.Context, _ bool, live bool) ui.Element {
	now := c.Now()
	if live {
		f.setWeekOffset(now, f.weekOffset) // history may have grown
		if f.visible(now) {
			f.maybeScan(now)
		}
	}
	w := pageBoxW(f)
	h := pageBodyHeight(f)
	ui.Column(c.Key("weekly")).Width(w).Height(h).Children(func() {
		f.header(c, w, now)
		scrollH := h - weeklyHeadH - weeklyFootH
		if scrollH < 80 {
			scrollH = 80
		}
		f.report(c, w, scrollH, now)
		f.footer(c, w)
	})
	return ui.Box(c).Size(0, 0)
}

// header is the 每周简报 title and the week stepper.
func (f *weeklyFeature) header(c *ui.Context, w float32, now time.Time) {
	l := tloc(c)
	start := f.weekStart(now, f.weekOffset)
	ui.Row(c.Key("weekly-head")).Width(w).Height(weeklyHeadH).
		Padding(0, weeklyInnerX, 0, weeklyInnerX).AlignItems(ui.Center).Gap(8).Children(func() {
		panelLabel(c, "weekly-title", l.WeeklyPageTitle(), 15, 650).TextColor(ink)
		ui.Box(c.Key("weekly-head-gap")).Grow(1)
		f.navButton(c, "weekly-prev", weeklyLeft, l.WeeklyPrevWeek(), f.weekOffset > f.minOffset(now), func() {
			f.setWeekOffset(now, f.weekOffset-1)
		})
		panelLabel(c, "weekly-range", weeklyWeekLabel(c.Locale(), start, now), 11, 0).
			TextColor(ink2).Width(260).TextAlign(ui.Center)
		f.navButton(c, "weekly-next", weeklyRight, l.WeeklyNextWeek(), f.weekOffset < 0, func() {
			f.setWeekOffset(now, f.weekOffset+1)
		})
	})
}

// navButton is a 22pt stepper arrow; a disabled step keeps its box but
// takes no press and reads dimmer.
func (f *weeklyFeature) navButton(c *ui.Context, key string, icon *ui.SVG, label string, on bool, press func()) {
	b := ui.ButtonBase(c.Key(key)).Size(22, 22).Radius(11).Center().Label(label).Tooltip(label).Disabled(!on)
	if on && b.Hovered() {
		b.Background(fill2)
	}
	b.Children(func() {
		col := ink3
		if on {
			col = ink2
		}
		ui.Icon(c, icon).Size(13, 13).TextColor(col)
	})
	if on && b.Clicked() {
		press()
	}
}

// report is the scrolling content: warning, Mac use, AI use, Rakko
// completions. Every section draws its own heading and rule so the scroll
// stays one column.
func (f *weeklyFeature) report(c *ui.Context, w, h float32, now time.Time) {
	start := f.weekStart(now, f.weekOffset)
	wk := weekly.Week{Start: start}
	if f.store != nil {
		wk = f.store.Week(start)
	}
	ui.Scroll(c.Key("weekly-scroll")).Width(w).Height(h).TrackScroll(&f.scroll).
		Label(tloc(c).WeeklyScrollLabel()).Children(func() {
		inner := w - 2*weeklyInnerX - 4
		ui.Column(c.Key("weekly-body")).Width(w - weeklyInnerX).Padding(4, weeklyInnerX, 12, 0).Children(func() {
			if note := f.storageNote(c); note != "" {
				ui.Row(c.Key("weekly-storage")).Width(inner).Gap(7).AlignItems(ui.Start).Margin(0, 0, 10, 0).Children(func() {
					ui.Icon(c, warnSymbol).Size(13, 13).TextColor(warning).Margin(1, 0, 0, 0)
					ui.Text(c, note).FontSize(10.5).TextColor(warning).MaxWidth(inner - 20)
				})
			}
			f.macSection(c, inner, wk, now)
			f.rule(c, inner, "mac-ai")
			f.aiSection(c, inner, now)
			f.rule(c, inner, "ai-rakko")
			f.rakkoSection(c, inner, wk)
		})
	})
}

// rule is the hairline between report sections; key names the two sections
// it separates so the two rules never share layout state.
func (f *weeklyFeature) rule(c *ui.Context, w float32, key string) {
	ui.Box(c.Key("weekly-rule-"+key)).Width(w).Height(1).Background(fill1).Margin(14, 0, 14, 0)
}

// storageNote says why the history is not being written, honestly: the
// original file is preserved and nothing is deleted.
func (f *weeklyFeature) storageNote(c *ui.Context) string {
	l := tloc(c)
	if f.store != nil && f.store.Corrupt() {
		return l.WeeklyStoreCorrupt()
	}
	if f.store != nil && f.store.SaveErr() != nil {
		return l.WeeklyStoreSaveFailed()
	}
	if f.storeErr != nil {
		return l.WeeklyStoreCorrupt()
	}
	return ""
}

// --- Mac use ------------------------------------------------------------

// macSection is the two-measure app usage block.
func (f *weeklyFeature) macSection(c *ui.Context, w float32, wk weekly.Week, now time.Time) {
	l := tloc(c)
	ui.Column(c.Key("weekly-mac")).Width(w).Margin(0, 0, 0, 0).Children(func() {
		ui.Row(c.Key("weekly-mac-head")).Width(w).Height(20).AlignItems(ui.Center).Children(func() {
			panelLabel(c, "weekly-mac-name", l.WeeklyMacSection(), 12, 600).TextColor(ink2)
			ui.Box(c.Key("weekly-mac-gap")).Grow(1)
			if f.consent() {
				f.modeSeg(c)
			}
		})
		if !f.consent() {
			f.consentBlock(c, w)
			return
		}
		if !wk.Sampled {
			ui.Column(c.Key("weekly-mac-empty")).Width(w).Margin(14, 0, 0, 0).Gap(5).Children(func() {
				panelLabel(c, nil, l.WeeklyEmptyTitle(), 12, 550).TextColor(ink)
				ui.Text(c, l.WeeklyEmptyBody()).FontSize(11).TextColor(ink3).MaxWidth(w).Margin(2, 0, 0, 0)
			})
			return
		}
		f.macMetric(c, w, wk, now)
		f.barChart(c, w, wk)
		f.appRows(c, w, wk)
		ui.Text(c, l.WeeklySamplingNote()).FontSize(10).TextColor(ink3).
			MaxWidth(w).Margin(10, 0, 0, 0)
		ui.Text(c, l.WeeklyCoverageNote()).FontSize(10).TextColor(ink3).
			MaxWidth(w).Margin(4, 0, 0, 0)
	})
}

// modeSeg is the 活跃 / 停留 picker: the same segmented track the AI 用量
// page uses for its ranges.
func (f *weeklyFeature) modeSeg(c *ui.Context) {
	l := tloc(c)
	ui.Row(c.Key("weekly-mode")).Background(fill1).Radius(6).Padding(2, 2, 2, 2).Gap(2).Children(func() {
		for _, m := range []weekly.Metric{weekly.MetricActive, weekly.MetricResident} {
			name := l.WeeklyModeActive()
			key := "active"
			if m == weekly.MetricResident {
				name = l.WeeklyModeResident()
				key = "resident"
			}
			on := f.metric == m
			b := ui.ButtonBase(c.Key("weekly-mode-"+key)).Radius(4).Padding(2, 8, 2, 8).Label(name)
			if on {
				b.Background(fill2)
			}
			b.Children(func() {
				col := ink3
				if on {
					col = ink
				}
				panelLabel(c, nil, name, 10, 600).TextColor(col)
			})
			if b.Clicked() && f.metric != m {
				f.metric = m
			}
		}
	})
}

// consentBlock is the report's ask: nothing is sampled until the user
// turns it on here, and the explanation says exactly what is and is not
// stored.
func (f *weeklyFeature) consentBlock(c *ui.Context, w float32) {
	l := tloc(c)
	ui.Column(c.Key("weekly-consent")).Width(w).Margin(10, 0, 0, 0).Gap(9).Children(func() {
		ui.Row(c.Key("weekly-consent-head")).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, weeklySymbol).Size(14, 14).TextColor(ink3)
			panelLabel(c, nil, l.WeeklyConsentTitle(), 12, 600).TextColor(ink)
		})
		panelLines(c, l.WeeklyConsentBody(), 11, 0).TextColor(ink2).MaxWidth(w)
		b := ui.ButtonBase(c.Key("weekly-enable")).Height(24).Radius(12).Padding(0, 12, 0, 12).
			Background(accent).Center().Label(l.WeeklyConsentEnable())
		b.Children(func() {
			ui.Text(c, l.WeeklyConsentEnable()).SingleLine().FontSize(11).FontWeight(600).TextColor(ink)
		})
		if b.Clicked() {
			f.setStats(true)
		}
	})
}

// macMetric is the selected measure's total and, when both weeks were
// measured, its comparison with the week before.
func (f *weeklyFeature) macMetric(c *ui.Context, w float32, wk weekly.Week, now time.Time) {
	l := tloc(c)
	total := weekly.WeekTotal(wk, f.metric)
	value, unit := weeklyUnitValue(total, l)
	ui.Row(c.Key("weekly-metric")).Width(w).Margin(12, 0, 0, 0).AlignItems(ui.End).Gap(12).Children(func() {
		panelLabel(c, "weekly-metric-value", value, 29, 620).TextColor(ink)
		panelLabel(c, nil, unit, 12, 0).TextColor(ink2).Margin(0, 0, 4, 0)
		if f.weekOffset < 0 {
			if prev, ok := f.previousWeek(now); ok {
				before := weekly.WeekTotal(prev, f.metric)
				panelLabel(c, nil, weeklyCompare(before, total, l), 10, 0).TextColor(ink3).Margin(0, 0, 5, 0)
			}
		}
	})
}

// previousWeek is the week before the one on screen, when this store has
// any samples there: without both weeks a comparison would invent a zero.
func (f *weeklyFeature) previousWeek(now time.Time) (weekly.Week, bool) {
	if f.store == nil {
		return weekly.Week{}, false
	}
	prev := f.store.Week(f.weekStart(now, f.weekOffset).AddDate(0, 0, -7))
	return prev, prev.Sampled
}

// barChart draws one bar per day for the selected measure. A day without
// samples draws only the baseline dash and says 未记录 on hover: missing
// time is never drawn as zero.
func (f *weeklyFeature) barChart(c *ui.Context, w float32, wk weekly.Week) {
	l := tloc(c)
	var maxV time.Duration
	values := make([]time.Duration, 7)
	for i := range wk.Days {
		values[i] = weekly.DayTotal(wk.Days[i], f.metric)
		if values[i] > maxV {
			maxV = values[i]
		}
	}
	if maxV <= 0 {
		maxV = time.Hour
	}
	ui.Row(c.Key("weekly-chart")).Width(w).Height(96).Margin(14, 0, 0, 0).Gap(6).
		AlignItems(ui.End).Children(func() {
		for i := range wk.Days {
			day := wk.Days[i]
			label := weeklyDayLabel(c.Locale(), l, day, values[i])
			col := ui.Column(c.Key("weekly-day-"+strconv.Itoa(i))).Grow(1).FillHeight().
				Justify(ui.End).AlignItems(ui.Center).Gap(5).Label(label)
			col.Tooltip(label)
			col.Children(func() {
				area := ui.Box(c.Key("area")).Width(26).Height(70).AlignItems(ui.End).Justify(ui.End)
				area.Children(func() {
					if !day.Sampled {
						ui.Box(c.Key("dash")).Width(26).Height(2).Radius(1).Background(fill3)
						return
					}
					if values[i] <= 0 {
						return // measured zero draws no bar
					}
					barH := float32(float64(values[i])/float64(maxV)) * 68
					barH = max(2, barH)
					tint := weeklyBar
					if i >= 5 {
						tint = weeklyBarWeek
					}
					ui.Box(c.Key("bar")).Width(24).Height(barH).Radius(3).Background(tint)
				})
				panelLabel(c, nil, weeklyWeekday(c.Locale(), day.Date), 9, 0).TextColor(ink3)
			})
		}
	})
}

// appRows are the week's busiest apps under the chart, at most five, with
// the rest folded into 其他应用. Only apps with time in the selected
// measure appear: a zero row would claim the measure says something about
// them when it does not.
func (f *weeklyFeature) appRows(c *ui.Context, w float32, wk weekly.Week) {
	l := tloc(c)
	type row struct {
		app weekly.AppReport
		v   time.Duration
	}
	rows := make([]row, 0, len(wk.Apps))
	for _, a := range wk.Apps {
		if v := weekly.AppTotal(a, f.metric); v > 0 {
			rows = append(rows, row{app: a, v: v})
		}
	}
	if len(rows) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].v != rows[j].v {
			return rows[i].v > rows[j].v
		}
		return weeklyAppName(rows[i].app) < weeklyAppName(rows[j].app)
	})
	const maxRows = 5
	shown := rows
	var other weekly.AppReport
	if len(rows) > maxRows {
		shown = rows[:maxRows]
		for _, r := range rows[maxRows:] {
			other.Active += r.app.Active
			other.Resident += r.app.Resident
		}
	}
	maxV := shown[0].v
	if v := weekly.AppTotal(other, f.metric); v > maxV {
		maxV = v
	}
	ui.Column(c.Key("weekly-apps")).Width(w).Margin(14, 0, 0, 0).Gap(8).Children(func() {
		for _, r := range shown {
			f.appRow(c, w, r.app.BundleID, weeklyAppName(r.app), r.v, maxV)
		}
		if v := weekly.AppTotal(other, f.metric); v > 0 {
			f.appRow(c, w, "", l.WeeklyAppsOther(), v, maxV)
		}
	})
}

// appRow is one app line: icon, name, a 3pt track and its time.
func (f *weeklyFeature) appRow(c *ui.Context, w float32, bundle, name string, value, maxV time.Duration) {
	l := tloc(c)
	frac := float32(0)
	if maxV > 0 {
		frac = float32(float64(value) / float64(maxV))
	}
	frac = min(1, max(0, frac))
	ui.Row(c.Key("weekly-app-"+bundle+name)).Width(w).Height(20).Gap(9).AlignItems(ui.Center).
		Label(name + " · " + weeklyHM(value, l)).Children(func() {
		if bundle != "" {
			if bmp := f.host.clipAppIcon(bundle); bmp != nil {
				ui.Image(c.Key("icon"), bmp).Width(18).Height(18).Radius(4)
			} else {
				ui.Box(c.Key("icon")).Size(18, 18).Radius(4).Background(fill1)
			}
		} else {
			ui.Row(c.Key("icon")).Size(18, 18).Center().Children(func() {
				ui.Icon(c, weeklyDots).Size(14, 14).TextColor(ink3)
			})
		}
		ui.Text(c, name).SingleLine().FontSize(11).TextColor(ink2).Width(130).Ellipsis("…")
		ui.Box(c.Key("track")).Grow(1).MinWidth(0).Height(3).Radius(2).Background(fill3).
			Draw(func(p *ui.Painter, r ui.Rect) {
				if frac > 0 {
					p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * frac, H: r.H}, weeklyBar, 2)
				}
			})
		panelLabel(c, nil, weeklyHM(value, l), 10, 0).TextColor(ink3).Width(64).TextAlign(ui.End)
	})
}

// --- AI use -------------------------------------------------------------

// aiSection reports the week's local token use and price estimate. It
// never fetches a quota: the numbers come from the logs on this Mac, so a
// missing source is said out loud instead of drawn as zero.
func (f *weeklyFeature) aiSection(c *ui.Context, w float32, now time.Time) {
	l := tloc(c)
	start := f.weekStart(now, f.weekOffset)
	same := f.aiHave && f.aiWeek.Equal(start)
	ui.Column(c.Key("weekly-ai")).Width(w).Children(func() {
		ui.Row(c.Key("weekly-ai-head")).Width(w).Height(20).AlignItems(ui.Center).Children(func() {
			panelLabel(c, "weekly-ai-name", l.WeeklyAiSection(), 12, 600).TextColor(ink2)
			ui.Box(c.Key("gap")).Grow(1)
			panelLabel(c, nil, l.WeeklyAiLocal(), 10, 0).TextColor(ink3)
		})
		switch {
		case f.scanBusy.Load() && !same:
			// A fresh week is being read; this week has no usable answer
			// yet, so do not show another week's numbers or an old error.
			f.aiNote(c, w, l.WeeklyAiScanning())
		case f.scanWeek.Equal(start) && f.aiErr != nil:
			f.aiNote(c, w, l.WeeklyAiScanFailed())
			if msg := strings.TrimSpace(f.aiErr.Error()); msg != "" {
				ui.Text(c, msg).FontSize(9.5).TextColor(ink3).MaxWidth(w).Margin(4, 0, 0, 0)
			}
		case !same:
			// No summary for this week yet: the picker just moved and
			// the scan has not started.
			f.aiNote(c, w, l.WeeklyAiScanning())
		case f.aiSummary.Total.Requests == 0 && len(f.aiFailed) > 0:
			f.aiNote(c, w, l.WeeklyAiSourcesUnreadable(len(f.aiFailed)))
			f.aiNote(c, w, l.WeeklyAiNote())
		case f.aiSummary.Total.Requests == 0:
			f.aiNote(c, w, l.WeeklyAiNoData())
			f.aiNote(c, w, l.WeeklyAiNote())
		default:
			f.aiMetrics(c, w, f.aiSummary)
			f.aiTrack(c, w, f.aiSummary)
			if len(f.aiFailed) > 0 {
				ui.Text(c, l.WeeklyAiSourcesPartial(len(f.aiFailed))).FontSize(10).
					TextColor(warning).MaxWidth(w).Margin(6, 0, 0, 0)
			}
			if n := f.aiSummary.Total.Unpriced; n > 0 {
				ui.Text(c, l.WeeklyAiUnpriced(strconv.FormatInt(n, 10))).FontSize(10).
					TextColor(warning).MaxWidth(w).Margin(6, 0, 0, 0)
			}
			f.aiNote(c, w, l.WeeklyAiNote())
		}
	})
}

// aiNote is one quiet line inside the AI section.
func (f *weeklyFeature) aiNote(c *ui.Context, w float32, text string) {
	ui.Text(c, text).FontSize(10.5).TextColor(ink3).MaxWidth(w).Margin(8, 0, 0, 0)
}

// aiMetrics is the token total beside the estimated cost.
func (f *weeklyFeature) aiMetrics(c *ui.Context, w float32, sum aiusage.Summary) {
	l := tloc(c)
	tokens := weeklyTokens(sum.Total)
	ui.Row(c.Key("weekly-ai-metrics")).Width(w).Margin(12, 0, 0, 0).Gap(42).AlignItems(ui.End).Children(func() {
		ui.Row(c.Key("tok")).AlignItems(ui.End).Gap(4).Children(func() {
			panelLabel(c, nil, tokens, 23, 620).TextColor(ink)
			panelLabel(c, nil, "Token", 11, 0).TextColor(ink2).Margin(0, 0, 2, 0)
		})
		ui.Column(c.Key("cost")).Gap(2).Children(func() {
			value := "—"
			if sum.Total.Cost > 0 {
				value = fmt.Sprintf("≈$%.2f", sum.Total.Cost)
			}
			panelLabel(c, nil, value, 23, 620).TextColor(ink)
			panelLabel(c, nil, l.WeeklyAiCost(), 10, 0).TextColor(ink3)
		})
	})
}

// aiTrack is the tool split: a 5pt bar of every source's share and a
// legend with each one's tokens, three per row. Sources stay separate: a
// single 其他 slice would hide which tool the time came from.
func (f *weeklyFeature) aiTrack(c *ui.Context, w float32, sum aiusage.Summary) {
	type slice struct {
		tool  aiusage.Tool
		value int64
	}
	var parts []slice
	var total int64
	for tool, tot := range sum.ByTool {
		n := weeklyToolTokens(tot)
		if n <= 0 {
			continue
		}
		parts = append(parts, slice{tool, n})
		total += n
	}
	if total <= 0 {
		return
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].value != parts[j].value {
			return parts[i].value > parts[j].value
		}
		return parts[i].tool < parts[j].tool
	})
	ui.Row(c.Key("weekly-ai-track")).Width(w).Height(5).Margin(16, 0, 0, 0).Gap(3).Radius(3).Children(func() {
		usable := w - 3*float32(len(parts)-1)
		used := float32(0)
		for i, p := range parts {
			width := float32(0)
			if i == len(parts)-1 {
				width = usable - used
			} else {
				width = max(2, usable*float32(p.value)/float32(total))
				used += width
			}
			if width <= 0 {
				continue
			}
			ui.Box(c.Key("seg-"+string(p.tool))).Height(5).Radius(2).Width(width).
				Background(weeklyToolTint(i)).Label(weeklyToolName(p.tool))
		}
	})
	for i := 0; i < len(parts); i += 3 {
		end := min(i+3, len(parts))
		ui.Row(c.Key("weekly-ai-legend-"+strconv.Itoa(i))).Width(w).Margin(9, 0, 0, 0).
			Gap(16).AlignItems(ui.Center).Children(func() {
			for j := i; j < end; j++ {
				p := parts[j]
				row := ui.Row(c.Key("leg-"+string(p.tool))).Gap(5).AlignItems(ui.Center)
				row.Children(func() {
					ui.Box(c.Key("dot")).Size(5, 5).Radius(3).Background(weeklyToolTint(j))
					panelLabel(c, nil, weeklyToolName(p.tool)+" "+weeklyTokensValue(p.value), 10, 0).TextColor(ink2)
				})
			}
		})
	}
}

// weeklyToolTint is one source's bar and dot color. The palette stays in
// the panel's muted family; a source beyond the list reuses it.
func weeklyToolTint(i int) ui.Color {
	i %= len(weeklyToolTints)
	if i < 0 {
		i += len(weeklyToolTints)
	}
	return weeklyToolTints[i]
}

// --- Rakko completions --------------------------------------------------

// weekTracked reports whether the history had started by the shown week.
// A week before the first recorded day cannot be a measured zero: Rakko
// was not keeping the count yet.
func (f *weeklyFeature) weekTracked(start time.Time) bool {
	earliest, ok := f.earliest()
	if !ok {
		return false
	}
	return !weekly.WeekStart(earliest).After(start)
}

// rakkoSection counts the week's completed Rakko actions. A corrupt
// history or a week before tracking began shows dashes, not zeros.
func (f *weeklyFeature) rakkoSection(c *ui.Context, w float32, wk weekly.Week) {
	l := tloc(c)
	ui.Column(c.Key("weekly-rakko")).Width(w).Children(func() {
		ui.Row(c.Key("weekly-rakko-head")).Width(w).Height(20).AlignItems(ui.Center).Children(func() {
			panelLabel(c, "weekly-rakko-name", l.WeeklyRakkoSection(), 12, 600).TextColor(ink2)
		})
		unknown := (f.store != nil && f.store.Corrupt()) || !f.weekTracked(wk.Start)
		ui.Row(c.Key("weekly-counts")).Width(w).Margin(12, 0, 0, 0).Justify(ui.SpaceBetween).Children(func() {
			f.count(c, "convert", docSymbol, wk.Rakko.Convert, l.WeeklyRakkoConvert(), unknown)
			f.count(c, "ocr", ocrSymbol, wk.Rakko.OCR, l.WeeklyRakkoText(), unknown)
			f.count(c, "tray", trayNavSymbol, wk.Rakko.Tray, l.WeeklyRakkoTray(), unknown)
		})
		ui.Text(c, l.WeeklyRakkoNote()).FontSize(10).TextColor(ink3).
			MaxWidth(w).Margin(10, 0, 0, 0)
	})
}

// count is one icon, number and label in the completion row.
func (f *weeklyFeature) count(c *ui.Context, key string, icon *ui.SVG, n int, label string, unknown bool) {
	value := strconv.Itoa(n)
	if unknown {
		value = "—"
	}
	ui.Row(c.Key("weekly-count-"+key)).Gap(9).AlignItems(ui.Center).
		Label(label + " · " + value).Children(func() {
		ui.Icon(c, icon).Size(16, 16).TextColor(ink3)
		ui.Column(c.Key("num")).Gap(1).Children(func() {
			panelLabel(c, nil, value, 17, 600).TextColor(ink)
			panelLabel(c, nil, label, 10, 0).TextColor(ink3)
		})
	})
}

// --- footer -------------------------------------------------------------

// footer says where the history lives and offers the pause switch, so the
// user can stop collection without leaving the report.
func (f *weeklyFeature) footer(c *ui.Context, w float32) {
	l := tloc(c)
	ui.Row(c.Key("weekly-foot")).Width(w).Height(weeklyFootH).
		Padding(0, weeklyInnerX, 0, weeklyInnerX).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Icon(c, weeklyShield).Size(11, 11).TextColor(ink3)
		panelLabel(c, nil, l.WeeklyLocalOnly(), 10, 0).TextColor(ink3)
		ui.Box(c.Key("gap")).Grow(1)
		text := l.WeeklyResume()
		if f.consent() {
			text = l.WeeklyPause()
		}
		b := ui.ButtonBase(c.Key("weekly-toggle")).Height(22).Radius(11).Padding(0, 9, 0, 9).
			Label(text).Description(text)
		if b.Hovered() {
			b.Background(fill1)
		}
		b.Children(func() {
			panelLabel(c, nil, text, 10, 0).TextColor(ink2)
		})
		if b.Clicked() {
			f.setStats(!f.consent())
		}
	})
}

// --- home entry ---------------------------------------------------------

// homeEntry is the home page's 上周简报 row (design/explore/
// notch-apps-brief-prototype.html): one quiet line under today's status.
// It always opens the previous calendar week, as its label says.
func (f *weeklyFeature) homeEntry(c *ui.Context) {
	l := tloc(c)
	now := c.Now()
	start := f.weekStart(now, -1)
	ui.Box(c.Key("home-weekly-rule")).FillWidth().Height(1).Background(fill1).Margin(4, 0, 0, 0)
	b := ui.ButtonBase(c.Key("home-weekly")).FillWidth().Height(28).Radius(6).
		Label(l.WeeklyEntryTitle()).Description(l.WeeklyEntryTitle())
	if b.Hovered() {
		b.Background(fill1)
	}
	b.Children(func() {
		ui.Row(c.Key("row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, weeklySymbol).Size(15, 15).TextColor(ink3)
			panelLabel(c, nil, l.WeeklyEntryTitle(), 12, 0).TextColor(ink2)
			ui.Box(c.Key("gap")).Grow(1)
			panelLabel(c, nil, weeklyShortRange(start), 10.5, 0).TextColor(ink3)
			ui.Icon(c, weeklyRight).Size(12, 12).TextColor(ink3)
		})
	})
	if b.Clicked() {
		f.openLastWeekPage()
	}
}

// --- formatting ---------------------------------------------------------

// weeklyWeekLabel is the picker's date range: the Monday through Sunday of
// the shown week in the reader's language. A week outside the current year
// gains its year, so a report from last December never looks like this one.
func weeklyWeekLabel(locale string, start, now time.Time) string {
	last := start.AddDate(0, 0, 6)
	skeleton := "MMMd"
	if start.Year() != now.Year() || last.Year() != now.Year() {
		skeleton = "yMMMd"
	}
	return weeklyDate(locale, start, skeleton) + " — " + weeklyDate(locale, last, skeleton)
}

// weeklyShortRange is the home entry's compact "10.26 — 11.1": numeric
// dates, unambiguous in both languages.
func weeklyShortRange(start time.Time) string {
	last := start.AddDate(0, 0, 6)
	return start.Format("1.2") + " — " + last.Format("1.2")
}

// weeklyDate is a calendar date in the locale, "10月26日" or "Oct 26".
func weeklyDate(locale string, t time.Time, skeleton string) string {
	tag := locale
	if tag == "" {
		tag = "zh-Hans"
	}
	return i18n.FormatDate(tag, t, i18n.DateOptions{Skeleton: skeleton})
}

// weeklyWeekday is the narrow weekday for a chart label.
func weeklyWeekday(locale string, t time.Time) string {
	tag := locale
	if tag == "" {
		tag = "zh-Hans"
	}
	return i18n.FormatDate(tag, t, i18n.DateOptions{Skeleton: "EEEEE"})
}

// weeklyDayLabel is one bar's hover text; an unsampled day says so rather
// than claiming zero minutes.
func weeklyDayLabel(locale string, l locales.Messages, day weekly.DayReport, v time.Duration) string {
	name := weeklyWeekday(locale, day.Date)
	if !day.Sampled {
		return name + " · " + l.WeeklyDayUnrecorded()
	}
	return name + " · " + weeklyHM(v, l)
}

// weeklyUnitValue splits a total into the big number and its unit: seconds
// below a minute, then minutes, then hours.
func weeklyUnitValue(d time.Duration, l locales.Messages) (string, string) {
	if d < time.Minute {
		return strconv.Itoa(int(math.Round(d.Seconds()))), l.WeeklyUnitSeconds()
	}
	if d < time.Hour {
		return strconv.Itoa(int(math.Round(d.Minutes()))), l.WeeklyUnitMinutes()
	}
	h := d.Hours()
	if h < 100 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", h), ".0"), l.WeeklyUnitHours()
	}
	return strconv.Itoa(int(math.Round(h))), l.WeeklyUnitHours()
}

// weeklyHM is an app row's time: "2 小时 30 分", "12 分钟" or "45 秒".
func weeklyHM(d time.Duration, l locales.Messages) string {
	if d < time.Minute {
		return l.WeeklySecond(strconv.Itoa(int(math.Round(d.Seconds()))))
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if h == 0 {
		return l.WeeklyMinute(strconv.Itoa(m))
	}
	if m == 0 {
		return l.WeeklyHour(strconv.Itoa(h))
	}
	return l.WeeklyHourMin(strconv.Itoa(h), strconv.Itoa(m))
}

// weeklyCompare is the metric's comparison with the week before.
func weeklyCompare(prev, cur time.Duration, l locales.Messages) string {
	delta := cur - prev
	if delta == 0 {
		return l.WeeklyCompareSame()
	}
	sign := "+"
	if delta < 0 {
		sign, delta = "−", -delta
	}
	return l.WeeklyCompare(sign + weeklyHM(delta, l))
}

// weeklyTokensValue formats a token count for the legend.
func weeklyTokensValue(n int64) string {
	switch {
	case n < 1_000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return compact(float64(n)/1e3, "K")
	case n < 1_000_000_000:
		return compact(float64(n)/1e6, "M")
	default:
		return compact(float64(n)/1e9, "B")
	}
}

// weeklyTokens is the metric's leading number.
func weeklyTokens(t aiusage.Totals) string { return weeklyTokensValue(totalTokens(t)) }

// weeklyToolTokens is one tool's tokens, the same sum as the chart.
func weeklyToolTokens(t aiusage.Totals) int64 { return totalTokens(t) }

// weeklyToolName names every reader the scanner supports, not just the
// two quota cards.
func weeklyToolName(t aiusage.Tool) string {
	switch t {
	case aiusage.ToolClaude:
		return "Claude Code"
	case aiusage.ToolCodex:
		return "Codex"
	case aiusage.ToolPi:
		return "Pi"
	case aiusage.ToolOpencode:
		return "opencode"
	case aiusage.ToolGrok:
		return "Grok"
	case aiusage.ToolOmp:
		return "omp"
	default:
		return string(t)
	}
}

// weeklyAppName is the stored display name, or the bundle id when the app
// never told one.
func weeklyAppName(a weekly.AppReport) string {
	if a.Name != "" {
		return a.Name
	}
	return a.BundleID
}
