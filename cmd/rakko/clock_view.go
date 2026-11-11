//go:build darwin

package main

// Drawing for the clock: the two tool cards, the band chips, the closed
// notch's countdown wings and its unanswered 时间到 card. Sizes and
// colours follow design/explore/clock-v1.html; the shell itself grows
// around this content in panel.go and notch.go.

import (
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/locales"
)

const (
	// timerRing is the progress ring's thickness in the chip (13) and the
	// wings (18).
	timerRingChip = 2
	timerRingWing = 2.4
	// timerAlertH is the alert line below the notch band.
	timerAlertH = 46
	// timerIslandAlertW is the island alert's whole capsule.
	timerIslandAlertW = 340
	timerIslandAlertH = 52
	// timerSwingPeriod and timerSwingDeg are the ringing hourglass: ±14°
	// every 0.9 s.
	timerSwingPeriod = 900 * time.Millisecond
	timerSwingDeg    = 14
	// timerWarnText is the alert tone on its 18% orange chip (design's
	// --warnText), and timerStopInk the black on the solid 停止 button.
	timerWarnText = "#FFB340"
)

var (
	timerStopInk = ui.Hex("#140D02")

	timerGlyph = symbol(`<path d="M7 3.5h10M7 20.5h10"/><path d="M8.5 3.5V6c0 2.3 1.6 3.9 3.5 6 1.9-2.1 3.5-3.7 3.5-6V3.5M8.5 20.5V18c0-2.3 1.6-3.9 3.5-6 1.9 2.1 3.5 3.7 3.5 6v2.5"/>`)
	watchGlyph = symbol(`<circle cx="12" cy="13.5" r="7"/><path d="M12 13.5V10M10 3.5h4M12 3.5v3M17.6 7.4 19 6"/>`)

	clockPlaySym  = fillSymbol(`<path d="M7.5 4.8v14.4L19.2 12z"/>`)
	clockPauseSym = fillSymbol(`<rect x="6.2" y="4.8" width="4" height="14.4" rx="1.2"/><rect x="13.8" y="4.8" width="4" height="14.4" rx="1.2"/>`)
	clockResetSym = symbol(`<path d="M5.2 12.5A6.8 6.8 0 1 0 7.4 7"/><path d="M5 4.2V8h3.8"/>`)
	clockCloseSym = symbol(`<path d="M7.5 7.5l9 9M16.5 7.5l-9 9"/>`)
)

// timerPresets are the 2 × 1 card's minute choices.
var timerPresets = []int{1, 3, 5, 10, 15, 30, 60}

// clockBadgeLeft is the 1 × 1 card's top-right badge offset: the card is
// sized after its content is built, so a Right() would resolve against the
// page; the two surfaces' widths are known here instead.
func clockBadgeLeft(surf toolCardSurface) float32 {
	w := toolColW(1)
	if surf == cardDesktop {
		w = pinCardW
	}
	return w - 18 - 6
}

// clockSwallow makes an element the press target of its own control: it
// keeps the down and the moves from starting a card press behind it, and
// leaves the release to Clicked, as awakeChip does.
func clockSwallow(el ui.Element) ui.Element {
	el.HandleInput(func(ev ui.InputEvent) bool {
		return ev.Button == 0 && ev.Kind >= ui.InputPointerDown && ev.Kind <= ui.InputPointerMove
	})
	return el
}

// clockRing is a progress ring: a fill3 track and col's share from 12
// o'clock, clockwise.
func clockRing(c *ui.Context, key string, size, stroke, frac float32, col, track ui.Color) ui.Element {
	box := ui.Box(c.Key(key)).Size(size, size)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		rad := size/2 - stroke/2
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		var tr ui.Path
		tr.Circle(cx, cy, rad)
		p.StrokePath(&tr, stroke, track)
		liveArc(p, cx, cy, rad, 0.75, frac, stroke, col)
	})
	return box
}

// clockCircle is a round icon button.
func clockCircle(c *ui.Context, key string, size float32, bg ui.Color, glyph *ui.SVG, gs float32, fg ui.Color, label string) ui.Element {
	b := ui.Box(c.Key(key)).Size(size, size).Radius(size / 2).Center().
		Background(bg).Label(label).Tooltip(label)
	b.Children(func() {
		ui.Icon(c, glyph).Size(gs, gs).TextColor(fg)
	})
	return b
}

// clockRail is the countdown's remaining track; dimmed while paused.
func clockRail(c *ui.Context, key string, frac float32, dim bool) ui.Element {
	box := ui.Box(c.Key(key)).Height(4).Radius(2)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.FillPath(liveRoundPath(r.X, r.Y, r.W, r.H, 2), fill3)
		if frac <= 0 {
			return
		}
		col := warning
		if dim {
			col = warning.Alpha(0.45)
		}
		p.FillPath(liveRoundPath(r.X, r.Y, max(2, r.W*frac), r.H, 2), col)
	})
	return box
}

// toolTimerBody draws the countdown card in its 2 × 1 and 1 × 1 forms.
func (a *shelf) toolTimerBody(c *ui.Context, card ui.Element, span int, surf toolCardSurface) {
	m := a.clock
	l := loc(c)
	now := placeholderNow()
	if m != nil {
		now = m.clock()
	}
	if span == 1 {
		a.toolTimerNarrow(c, card, m, surf, now, l)
		return
	}
	card.Padding(10, 12, 10, 12).Children(func() {
		row := ui.Row(c.Key("timer-row")).FillWidth().Height(30).Gap(7).AlignItems(ui.Center)
		row.Children(func() {
			glyphCol := ink3
			switch {
			case m == nil:
			case m.phase == timerRunning || m.phase == timerDone:
				glyphCol = warning
			case m.phase == timerPaused:
				glyphCol = ink3
			}
			ui.Icon(c, timerGlyph).Size(16, 16).TextColor(glyphCol)

			if m != nil && m.edit != nil {
				a.timerEditRow(c, m)
			} else {
				numCol := ink
				if m != nil {
					switch m.phase {
					case timerRunning, timerDone:
						numCol = warning
					case timerPaused:
						numCol = ink2
					}
				}
				text := clockText(timerDefaultDur)
				if m != nil {
					text = clockTextCeil(m.timerRemainAt(now))
				}
				size := float32(26)
				if len(text) > 5 { // h:mm:ss is wider than the row allows at 26
					size = 20
				}
				num := ui.Box(c.Key("timer-num")).Height(30).Center().Padding(0, 2, 0, 2)
				num.Children(func() {
					ui.Text(c, text).SingleLine().FontSize(size).FontWeight(600).
						FontFeatures("tnum").TextColor(numCol)
				})
				if m != nil && m.phase == timerIdle && surf == cardPanel {
					clockSwallow(num)
					if num.Clicked() {
						m.beginEdit(m.clock())
					}
				}
				num.Label(l.TimerCustomLabel())
			}

			ui.Box(c.Key("timer-gap")).Grow(1)

			if m == nil || m.phase == timerIdle {
				btn := clockCircle(c, "timer-start", 30, accent, clockPlaySym, 13, ink, l.TimerStart())
				clockSwallow(btn)
				if btn.Clicked() && m != nil {
					m.startTimer(m.clock(), m.dur)
				}
			} else if m.phase == timerDone {
				btn := clockCircle(c, "timer-cancel", 30, fill2, clockCloseSym, 13, ink2, l.TimerStop())
				clockSwallow(btn)
				if btn.Clicked() {
					m.stopTimer(m.clock())
				}
			} else {
				cancel := clockCircle(c, "timer-cancel", 26, fill1, clockCloseSym, 12, ink2, l.TimerCancel())
				clockSwallow(cancel)
				if cancel.Clicked() {
					m.stopTimer(m.clock())
				}
				var btn ui.Element
				if m.phase == timerPaused {
					btn = clockCircle(c, "timer-resume", 30, accent, clockPlaySym, 13, ink, l.TimerResume())
				} else {
					btn = clockCircle(c, "timer-pause", 30, fill2, clockPauseSym, 12, ink, l.TimerPause())
				}
				clockSwallow(btn)
				if btn.Clicked() {
					if m.phase == timerPaused {
						m.resumeTimer(m.clock())
					} else {
						m.pauseTimer(m.clock())
					}
				}
			}
		})
		ui.Box(c.Key("timer-mid")).Grow(1)
		row2 := ui.Row(c.Key("timer-foot")).FillWidth().Height(20).AlignItems(ui.Center)
		row2.Children(func() {
			if m != nil && (m.phase == timerRunning || m.phase == timerPaused || m.phase == timerDone) {
				a.timerRail(c, m, now)
			} else {
				a.timerPresets(c, m)
			}
		})
	})
}

// timerRail is the running card's bottom progress.
func (a *shelf) timerRail(c *ui.Context, m *clockModule, now time.Time) {
	rail := clockRail(c, "timer-progress", m.timerFracAt(now), m.phase == timerPaused)
	rail.Grow(1).Label(loc(c).TimerRemaining(clockTextCeil(m.timerRemainAt(now))))
}

// timerPresets is the idle card's row of minute choices.
func (a *shelf) timerPresets(c *ui.Context, m *clockModule) {
	l := loc(c)
	row := ui.Row(c.Key("timer-presets")).Grow(1).Gap(3)
	row.Children(func() {
		for _, minutes := range timerPresets {
			on := m != nil && m.dur == time.Duration(minutes)*time.Minute
			chip := ui.Row(c.Key("timer-p" + strconv.Itoa(minutes))).Grow(1).Height(20).MinWidth(0).
				Radius(6).Center().Label(l.TimerPreset(minutes))
			if on {
				chip.Background(accent)
			} else {
				chip.Background(fill1)
			}
			chip.Children(func() {
				t := ui.Text(c, strconv.Itoa(minutes)).SingleLine().FontSize(10.5).
					FontWeight(600).FontFeatures("tnum")
				if on {
					t.TextColor(ink)
				} else {
					t.TextColor(ink2)
				}
			})
			clockSwallow(chip)
			if chip.Clicked() && m != nil {
				d := time.Duration(minutes) * time.Minute
				if m.edit != nil {
					m.endEdit()
					a.leaveFocusMode()
				}
				m.dur = d
				m.refresh()
			}
		}
	})
}

// timerEditRow is the three-field custom length editor.
func (a *shelf) timerEditRow(c *ui.Context, m *clockModule) {
	e := m.edit
	l := loc(c)
	row := ui.Row(c.Key("timer-segs")).Height(30).AlignItems(ui.Center)
	row.Children(func() {
		for i := range 3 {
			if i > 0 {
				ui.Text(c, ":").FontSize(26).FontWeight(600).FontFeatures("tnum").TextColor(ink3)
			}
			seg := ui.Box(c.Key("timer-seg-" + strconv.Itoa(i))).Height(30).Width(33).
				Radius(6).Center()
			focused := e.field == i
			if focused {
				seg.Background(accent24)
				seg.Draw(func(p *ui.Painter, r ui.Rect) {
					p.FillPath(liveRoundPath(r.X, r.Y+r.H-2, r.W, 2, 1), accent)
				})
			} else {
				seg.Background(fill2)
			}
			seg.Children(func() {
				if focused {
					in := ui.TextInputBase(c.Key("timer-edit"), &e.raw).Width(33).Height(30).
						FontSize(24).FontWeight(600).FontFeatures("tnum").
						TextColor(ink).TextAlign(ui.Center).Label(l.TimerCustomLabel())
					in.Focus()
					if e.fresh {
						in.SetTextSelection(0, len(e.raw))
						e.fresh = false
					}
					in.OnChange(func() {
						e.typeDigits()
						m.refresh()
					})
					in.OnTextKey(func(k ui.TextKey) bool {
						switch k.Key {
						case ui.KeyUp:
							e.nudge(1)
						case ui.KeyDown:
							e.nudge(-1)
						case ui.KeyLeft:
							e.moveField(-1)
						case ui.KeyRight:
							e.moveField(1)
						case ui.KeyTab:
							e.moveField(1)
						case ui.KeyEnter:
							if d := e.value(); d > 0 {
								m.startTimer(m.clock(), d)
								a.leaveFocusMode()
							}
						case ui.KeyEscape:
							m.endEdit()
							a.leaveFocusMode()
						default:
							return false
						}
						m.refresh()
						return true
					})
				} else {
					ui.Text(c, timerEditDigits(e, i)).SingleLine().FontSize(24).
						FontWeight(600).FontFeatures("tnum").TextColor(ink)
				}
			})
			if !focused {
				clockSwallow(seg)
				if seg.Clicked() {
					e.field = i
					e.raw, e.fresh = e.text(), true
					m.refresh()
				}
			}
		}
	})
	row.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputScroll || ev.DY == 0 {
			return false
		}
		if ev.DY > 0 {
			e.nudge(-1)
		} else {
			e.nudge(1)
		}
		m.refresh()
		return true
	})
}

// timerEditDigits is one segment's text: the digits typed so far, or the
// field's value padded to two.
func timerEditDigits(e *timerEdit, i int) string {
	if e.field == i && e.raw != "" {
		return e.raw
	}
	switch i {
	case 0:
		return pad2(e.h)
	case 1:
		return pad2(e.m)
	}
	return pad2(e.s)
}

func pad2(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

// toolTimerNarrow is the 1 × 1 countdown: icon and name when idle, the
// big remaining time and its rail while it runs.
func (a *shelf) toolTimerNarrow(c *ui.Context, card ui.Element, m *clockModule, surf toolCardSurface, now time.Time, l locales.Messages) {
	card.Center().Children(func() {
		if m == nil || m.phase == timerIdle {
			ui.Icon(c, timerGlyph).Size(20, 20).TextColor(ink)
			panelLabel(c, nil, l.CardTimer(), 12, 600).Margin(4, 0, 0, 0).TextColor(ink)
			if m != nil {
				panelLabel(c, nil, timerDurationSub(l, m.lastStarted), 10, 0).Margin(2, 0, 0, 0).TextColor(ink3)
			}
			return
		}
		col := warning
		if m.phase == timerPaused {
			col = ink2
		}
		ui.Text(c, clockTextCeil(m.timerRemainAt(now))).SingleLine().FontSize(20).
			FontWeight(600).FontFeatures("tnum").TextColor(col)
		clockRail(c, "timer-11-rail", m.timerFracAt(now), m.phase == timerPaused).
			Width(56).Margin(9, 0, 0, 0)
	})
	if m == nil || (m.phase != timerRunning && m.phase != timerPaused) {
		return
	}
	if card.Hovered() {
		cancel := clockCircle(c, "timer-11-cancel", 18, fill2, clockCloseSym, 11, ink2, l.TimerCancel()).
			Absolute().Top(6).Left(clockBadgeLeft(surf))
		clockSwallow(cancel)
		if cancel.Clicked() {
			m.stopTimer(m.clock())
		}
	}
}

// timerDurationSub is the 1 × 1 idle subtitle: 「5 分钟」 for whole
// minutes, the clock for anything else.
func timerDurationSub(l locales.Messages, d time.Duration) string {
	if d > 0 && d%time.Minute == 0 && d <= time.Hour {
		return l.TimerMinutes(int(d / time.Minute))
	}
	return clockText(d)
}

// toolStopwatchBody draws the stopwatch card in its 1 × 1 and 2 × 1 forms.
func (a *shelf) toolStopwatchBody(c *ui.Context, card ui.Element, span int, surf toolCardSurface) {
	m := a.clock
	l := loc(c)
	now := placeholderNow()
	if m != nil {
		now = m.clock()
	}
	if span == 1 {
		a.toolStopwatchNarrow(c, card, m, surf, now, l)
		return
	}
	card.Padding(0, 12, 0, 12).Justify(ui.Center).Children(func() {
		row := ui.Row(c.Key("watch-row")).FillWidth().Height(30).Gap(7).AlignItems(ui.Center)
		row.Children(func() {
			glyphCol := ink3
			numCol := ink3
			if m != nil && (m.watchRun || m.watchElapsed > 0) {
				glyphCol, numCol = ink, ink
				if !m.watchRun {
					glyphCol, numCol = ink3, ink2
				}
			}
			ui.Icon(c, watchGlyph).Size(16, 16).TextColor(glyphCol)
			text := clockText(0)
			if m != nil {
				text = clockText(m.watchElapsedAt(now))
			}
			ui.Text(c, text).SingleLine().FontSize(26).FontWeight(600).
				FontFeatures("tnum").TextColor(numCol)
			ui.Box(c.Key("watch-gap")).Grow(1)
			switch {
			case m == nil || (!m.watchRun && m.watchElapsed == 0):
				btn := clockCircle(c, "watch-start", 30, accent, clockPlaySym, 13, ink, l.TimerStart())
				clockSwallow(btn)
				if btn.Clicked() && m != nil {
					m.startWatch(m.clock())
				}
			case m.watchRun:
				btn := clockCircle(c, "watch-pause", 30, fill2, clockPauseSym, 12, ink, l.TimerPause())
				clockSwallow(btn)
				if btn.Clicked() {
					m.pauseWatch(m.clock())
				}
			default:
				reset := clockCircle(c, "watch-reset", 26, fill1, clockResetSym, 14, ink2, l.StopwatchReset())
				clockSwallow(reset)
				if reset.Clicked() {
					m.resetWatch()
				}
				btn := clockCircle(c, "watch-resume", 30, accent, clockPlaySym, 13, ink, l.TimerResume())
				clockSwallow(btn)
				if btn.Clicked() {
					m.resumeWatch(m.clock())
				}
			}
		})
	})
}

// toolStopwatchNarrow is the 1 × 1 stopwatch: icon and name when idle,
// the elapsed time while it runs, and a reset badge while paused.
func (a *shelf) toolStopwatchNarrow(c *ui.Context, card ui.Element, m *clockModule, surf toolCardSurface, now time.Time, l locales.Messages) {
	card.Center().Children(func() {
		if m == nil || (!m.watchRun && m.watchElapsed == 0) {
			ui.Icon(c, watchGlyph).Size(20, 20).TextColor(ink)
			panelLabel(c, nil, l.CardStopwatch(), 12, 600).Margin(4, 0, 0, 0).TextColor(ink)
			return
		}
		col := ink
		glyph := ink
		if !m.watchRun {
			col, glyph = ink2, ink3
		}
		ui.Icon(c, watchGlyph).Size(16, 16).TextColor(glyph)
		ui.Text(c, clockText(m.watchElapsedAt(now))).SingleLine().FontSize(20).
			FontWeight(600).FontFeatures("tnum").TextColor(col).Margin(6, 0, 0, 0)
	})
	if m == nil || m.watchRun || m.watchElapsed == 0 {
		return
	}
	reset := clockCircle(c, "watch-11-reset", 18, fill2, clockResetSym, 11, ink2, l.StopwatchReset()).
		Absolute().Top(6).Left(clockBadgeLeft(surf))
	clockSwallow(reset)
	if reset.Clicked() {
		m.resetWatch()
	}
}

// --- band chips ---

func (t timerChip) chip(c *ui.Context) ui.Element {
	m := t.m
	l := loc(c)
	if !m.timerLive() {
		return ui.Box(c).Size(0, 0)
	}
	now := m.clock()
	if m.phase == timerDone {
		chip := ui.Row(c.Key("timer-chip")).Height(chipH).Radius(chipH/2).
			Padding(0, 9, 0, 9).Gap(5).AlignItems(ui.Center).
			Background(warning.Alpha(0.18)).Label(l.TimerChipDone()).Tooltip(l.TimerStop())
		chip.Children(func() {
			a := m.host
			glyph := ui.Icon(c, timerGlyph).Size(13, 13).TextColor(warning)
			if a != nil && !reduceMotion(c) && m.ringing {
				k := glyph.Loop("timer-chip-swing", timerSwingPeriod, ui.Linear)
				glyph.Rotate(float32(timerSwingDeg*math.Sin(2*math.Pi*float64(k)))).
					TransformOrigin(0.5, 0.12)
				c.AnimationFrame()
			}
			ui.Text(c, l.TimerStop()).SingleLine().FontSize(11.5).FontWeight(600).
				TextColor(ui.Hex(timerWarnText))
		})
		if chip.Clicked() {
			m.stopTimer(now)
		}
		return chip
	}
	paused := m.phase == timerPaused
	frac := m.timerFracAt(now)
	col, text := warning, ui.Hex(timerWarnText)
	bg := warning.Alpha(0.18)
	if paused {
		col, text, bg = warning.Alpha(0.45), ink3, fill1
	}
	remain := clockTextCeil(m.timerRemainAt(now))
	label := l.TimerChipLabel(remain)
	if paused {
		label = l.TimerChipPausedLabel(remain)
	}
	chip := ui.Row(c.Key("timer-chip")).Height(chipH).Radius(chipH/2).
		Padding(0, 9, 0, 6).Gap(5).AlignItems(ui.Center).Background(bg).
		Label(label).Tooltip(label)
	chip.Children(func() {
		clockRing(c, "timer-chip-ring", 13, timerRingChip, frac, col, fill3)
		ui.Text(c, remain).SingleLine().FontSize(11.5).FontWeight(600).
			FontFeatures("tnum").TextColor(text)
	})
	if chip.Clicked() {
		m.toggleTimer(now)
	}
	return chip
}

func (s stopwatchChip) chip(c *ui.Context) ui.Element {
	m := s.m
	l := loc(c)
	if !m.watchLive() {
		return ui.Box(c).Size(0, 0)
	}
	now := m.clock()
	elapsed := clockText(m.watchElapsedAt(now))
	label := l.StopwatchElapsed(elapsed)
	col := ink
	if !m.watchRun {
		label = l.StopwatchPaused(elapsed)
		col = ink3
	}
	chip := ui.Row(c.Key("watch-chip")).Height(chipH).Radius(chipH/2).
		Padding(0, 8, 0, 5).Gap(5).AlignItems(ui.Center).Background(fill1).
		Label(label).Tooltip(label)
	chip.Children(func() {
		ui.Icon(c, watchGlyph).Size(13, 13).TextColor(col)
		ui.Text(c, elapsed).SingleLine().FontSize(11.5).FontWeight(600).
			FontFeatures("tnum").TextColor(ink)
	})
	if chip.Clicked() {
		m.toggleWatch(now)
	}
	return chip
}

// --- closed notch ---

// timerWingsSize is the countdown wings' shell: the anchor plus a visible
// wing on each side, the same box charging and downloads use.
func (a *shelf) timerWingsSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	return baseW + 2*float32(a.liveWingW()), baseH + npWingsBody
}

// timerWings draws the running countdown in the closed notch: a ring on
// the left, the remaining time on the right. Paused dims both.
func (a *shelf) timerWings(c *ui.Context, shellW, shellH, k float32) {
	m := a.clock
	if m == nil {
		return
	}
	now := m.clock()
	paused := m.phase == timerPaused
	frac := m.timerFracAt(now)
	col := warning
	if paused {
		col = warning.Alpha(0.45)
	}
	remain := clockTextCeil(m.timerRemainAt(now))
	label := loc(c).TimerChipLabel(remain)
	if paused {
		label = loc(c).TimerChipPausedLabel(remain)
	}
	row := ui.Row(c.Key("timer-wings")).Width(shellW).Height(shellH).
		Padding(0, liveWingPad, npWingsBody, liveWingPad).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).
		Opacity(min(max(k*k, 0), 1)).Label(label)
	row.Children(func() {
		clockRing(c, "timer-wing-ring", 18, timerRingWing, frac, col, fill3)
		textCol := warning
		if paused {
			textCol = ink3
		}
		ui.Text(c, remain).SingleLine().FontSize(13).FontWeight(600).
			FontFeatures("tnum").TextColor(textCol)
	})
}

// timerAlarmSize is the unanswered alert's shell: a 360pt card (never
// narrower than the notch plus 60) with a 46pt line below the band, or
// the island's one 340 × 52 capsule.
func (a *shelf) timerAlarmSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	if a.island {
		return timerIslandAlertW, timerIslandAlertH
	}
	return min(max(float32(360), baseW+60), float32(fbMaxW)), baseH + timerAlertH
}

// timerAlarmView draws 时间到: a swinging hourglass, the words, 重复 and
// the orange 停止 button, hanging below the notch (or inside the island).
func (a *shelf) timerAlarmView(c *ui.Context, shellW, shellH, k float32) {
	m := a.clock
	if m == nil || m.phase != timerDone {
		return
	}
	l := loc(c)
	now := m.clock()
	_, notchH := a.anchorSize()
	top, padX := float32(notchH), float32(16)
	rowH := float32(timerAlertH)
	if a.island {
		top, padX, rowH = 0, 18, shellH
	}
	box := ui.Column(c.Key("timer-alarm")).Width(shellW).Height(shellH).
		Padding(top, padX, 0, padX).Clip().Opacity(min(max(k*k, 0), 1)).
		Label(l.TimerChipDone())
	box.Children(func() {
		row := ui.Row(c.Key("timer-alarm-row")).FillWidth().Height(rowH).Gap(9).AlignItems(ui.Center)
		row.Children(func() {
			glyph := ui.Icon(c, timerGlyph).Size(18, 18).TextColor(warning)
			if m.ringing && !reduceMotion(c) {
				phase := glyph.Loop("timer-alarm-swing", timerSwingPeriod, ui.Linear)
				glyph.Rotate(float32(timerSwingDeg*math.Sin(2*math.Pi*float64(phase)))).
					TransformOrigin(0.5, 0.12)
				c.AnimationFrame()
			}
			ui.Text(c, l.TimerTimeUp()).SingleLine().FontSize(13).FontWeight(600).
				TextColor(ink).Grow(1).MinWidth(0).Ellipsis("…")
			repeat := ui.Box(c.Key("timer-repeat")).Height(30).Padding(0, 10, 0, 10).
				Radius(9).Center().Label(l.TimerRepeat()).Tooltip(l.TimerRepeat())
			repeat.Children(func() {
				ui.Text(c, l.TimerRepeat()).SingleLine().FontSize(12.5).
					FontWeight(600).TextColor(ink2)
			})
			if repeat.Clicked() {
				m.repeatTimer(now)
			}
			stop := ui.Box(c.Key("timer-stop")).Height(30).MinWidth(72).
				Padding(0, 14, 0, 14).Radius(9).Center().
				Background(warning).Label(l.TimerStop()).Tooltip(l.TimerStop())
			stop.Children(func() {
				ui.Text(c, l.TimerStop()).SingleLine().FontSize(12.5).
					FontWeight(600).TextColor(timerStopInk)
			})
			if stop.Clicked() {
				m.stopTimer(now)
			}
		})
	})
}

// placeholderNow keeps the card builders answerable on shelves without a
// clock (headless fixtures that never registered the feature).
func placeholderNow() time.Time { return time.Now() }
