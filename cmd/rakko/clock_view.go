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

	"rakko/internal/layout"
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
	clockStopSym  = fillSymbol(`<rect x="6" y="6" width="12" height="12" rx="2.5"/>`)
	clockCloseSym = symbol(`<path d="M7.5 7.5l9 9M16.5 7.5l-9 9"/>`)
)

// timerPresets are the 2 × 1 card's minute choices.
var timerPresets = []int{1, 3, 5, 10, 15, 30, 60}

// clockReadingSize is the largest 600-weight tabular size whose reading
// fits avail: short readings keep base, long ones shrink a point at a time
// instead of taking an ellipsis. The card's buttons keep their fixed sizes
// because the reading gives up width first.
func clockReadingSize(c *ui.Context, text string, avail, base float32) float32 {
	if avail <= 0 {
		return base
	}
	size := base
	for ; size > 12; size-- {
		w, _ := c.MeasureText(0, ui.Span{Text: text, Size: size, Weight: 600, Features: "tnum"})
		if w <= avail {
			return size
		}
	}
	return size
}

// clockCardWidth is the card's box width on a surface: the tool grid's
// cell or the desktop pin's window.
func clockCardWidth(span int, surf toolCardSurface) float32 {
	if surf == cardDesktop {
		return float32(pinCardWidth("", span))
	}
	return toolColW(span)
}

// clockSwallow makes an element the press target of its own control: it
// keeps the down and the moves from starting a card press behind it, and
// leaves the release to Clicked, as the chips do.
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

// cardTimerFrac is the countdown ring's share, quantized to the reading's
// seconds so the 450 ms linear advance plays once per second with the
// digits, as the design has it.
func cardTimerFrac(m *clockModule, now time.Time) float32 {
	if m == nil || m.dur <= 0 {
		return 0
	}
	rem := m.timerRemainAt(now)
	secs := int64(math.Ceil(rem.Seconds()))
	return min(max(float32(secs)/float32(m.dur.Seconds()), 0), 1)
}

// cardWatchFrac is the stopwatch ring's minute share, quantized to whole
// seconds.
func cardWatchFrac(elapsed time.Duration) float32 {
	secs := int64(elapsed / time.Second)
	return float32(secs%60) / 60
}

// toolTimerBody draws the countdown card (v2 §2–3, v3 §5). The 2 × 1
// form: the ring and name on the header, the reading on its right, and
// the action row below. Idle shows the minute presets and a ▶; choosing
// a preset only selects it (accent chip, the reading rolls to it), and ▶
// starts. The 1 × 1 form keeps the last chosen length and puts ▶ in the
// corner.
func (a *shelf) toolTimerBody(c *ui.Context, card ui.Element, span int, surf toolCardSurface) {
	card.Children(func() {
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
		if m != nil && m.edit != nil {
			a.timerEditCard(c, card, m, surf)
			return
		}
		phase := cardTimerPhase(m)
		frac := float32(1)
		text := clockText(timerDefaultDur)
		if m != nil {
			frac = cardTimerFrac(m, now)
			text = clockTextCeil(m.timerRemainAt(now))
		}
		w := cardW(layout.Card{ID: "timer", Span: 2}, surf)
		top := a.cardTop(c, card, "timer-top")
		top.Children(func() {
			if phase == cardDone {
				a.cardSwingingGlyph(c, card, "timer-lead", timerGlyph, 18)
			} else {
				a.cardRingLead(c, card, "timer-lead", 24, frac, phase, timerGlyph)
			}
			fitText(c, l.CardTimer(), 12, 600, 42).TextColor(ink2)
			ui.Box(c).Grow(1)
			a.cardHero(c, card, "timer-hero", text, 26, 22, w-2*cardPad-24-7-42-7,
				cardHeroColor(phase), phase == cardPaused)
		})
		foot := a.cardFoot(c, card, "timer-foot")
		foot.Children(func() {
			switch phase {
			case cardIdle:
				chips := ui.Row(c.Key("timer-presets")).Grow(1).Gap(3).AlignItems(ui.Center)
				chips.Children(func() {
					for _, minutes := range timerPresets {
						d := time.Duration(minutes) * time.Minute
						on := m != nil && m.dur == d
						chip := a.cardChip(c, "timer-p"+strconv.Itoa(minutes),
							strconv.Itoa(minutes), on, false)
						if chip.Clicked() && m != nil {
							if m.edit != nil {
								m.endEdit()
								a.leaveFocusMode()
							}
							m.dur = d
							m.refresh()
						}
					}
				})
				start := a.cardRound(c, "timer-start", cardCtrlH, cardButtonPrimary,
					clockPlaySym, 12, l.TimerStart())
				if start.Clicked() && m != nil {
					m.startTimer(m.clock(), m.dur)
				}
			case cardDone:
				stop := a.cardPill(c, "timer-stop", l.TimerStop(), func(fg ui.Color) {
					ui.Icon(c, clockStopSym).Size(11, 11).TextColor(fg)
				}, cardButtonSolid, true)
				if stop.Clicked() && m != nil {
					m.stopTimer(m.clock())
				}
				plus := a.cardPill(c, "timer-plus", l.TimerPlusOne(), nil, cardButtonSecondary, false)
				if plus.Clicked() && m != nil {
					m.addMinute(m.clock())
				}
			default:
				resume := phase == cardPaused
				label := l.TimerPause()
				if resume {
					label = l.TimerResume()
				}
				toggle := a.cardPill(c, "timer-toggle", label, func(fg ui.Color) {
					a.cardSwapIcon(c, card, "timer-swap", !resume, 12, fg)
				}, cardButtonPrimary, false)
				if toggle.Clicked() && m != nil {
					if resume {
						m.resumeTimer(m.clock())
					} else {
						m.pauseTimer(m.clock())
					}
				}
				plus := a.cardPill(c, "timer-plus", l.TimerPlusOne(), nil, cardButtonSecondary, false)
				if plus.Clicked() && m != nil {
					m.addMinute(m.clock())
				}
				ui.Box(c).Grow(1)
				cancel := a.cardRound(c, "timer-cancel", cardCtrlH, cardButtonSecondary,
					clockCloseSym, 12, l.TimerCancel())
				if cancel.Clicked() && m != nil {
					m.stopTimer(m.clock())
				}
			}
			a.cardFootIn(c, card, "timer", phase, foot)
		})

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

// timerEditCard puts the three-field custom length editor in the 2 × 1
// card: the reading itself was the way in (v3 §5).
func (a *shelf) timerEditCard(c *ui.Context, card ui.Element, m *clockModule, surf toolCardSurface) {
	card.Children(func() {
		e := m.edit
		l := loc(c)
		// The top row keeps the skeleton: the countdown's glyph, then the
		// three editable fields (v3 §5 keeps the old editor inside the new
		// card).
		a.cardGlyphLead(c, card, "timer-edit-lead", timerGlyph, 16, 16, ink2).
			Absolute().Left(cardPad).Top(12)
		box := ui.Box(c.Key("timer-edit")).Absolute().Left(cardPad + 22).Right(cardPad).
			Top(8).Height(34)
		box.Children(func() {
			a.timerEditRow(c, m)
		})
		// The action row stays the idle card's: the presets select a
		// length into the editor and the warning play starts the typed
		// one. Before this, the edit state had no way to start with the
		// mouse at all.
		foot := a.cardFoot(c, card, "timer-edit-foot")
		foot.Children(func() {
			chips := ui.Row(c.Key("timer-edit-presets")).Grow(1).Gap(3).AlignItems(ui.Center)
			chips.Children(func() {
				cur := e.value()
				for _, minutes := range timerPresets {
					d := time.Duration(minutes) * time.Minute
					chip := a.cardChip(c, "timer-p"+strconv.Itoa(minutes),
						strconv.Itoa(minutes), cur == d, false)
					if chip.Clicked() {
						e.h = int(d / time.Hour)
						e.m = int(d % time.Hour / time.Minute)
						e.s = int(d % time.Minute / time.Second)
						e.raw, e.fresh = e.text(), false
						m.refresh()
					}
				}
			})
			start := a.cardRound(c, "timer-edit-start", cardCtrlH, cardButtonPrimary,
				clockPlaySym, 12, l.TimerStart())
			if start.Clicked() {
				if d := e.value(); d > 0 {
					m.startTimer(m.clock(), d)
					a.leaveFocusMode()
				}
			}
		})
	})
}

// addMinute extends the countdown by one minute; a finished alarm goes
// back to running (v2 §3's +1 分钟).
func (m *clockModule) addMinute(now time.Time) {
	switch m.phase {
	case timerRunning, timerPaused:
		remain := m.timerRemainAt(now) + time.Minute
		if m.phase == timerPaused {
			m.remain = remain
		} else {
			m.end = now.Add(remain)
		}
		m.dur = max(m.dur, remain)
		m.refresh()
	case timerDone:
		m.stopAlert()
		m.phase = timerRunning
		m.end = now.Add(time.Minute)
		m.dur = max(m.dur, time.Minute)
		m.refresh()
	}
}

// toolTimerNarrow is the 1 × 1 countdown: the 22pt ring at the top left,
// the value at the bottom left and one ▶/❙❙/stop button in the corner.
// Idle shows the last chosen length; the whole card starts it.
func (a *shelf) toolTimerNarrow(c *ui.Context, card ui.Element, m *clockModule, surf toolCardSurface, now time.Time, l locales.Messages) {
	card.Children(func() {
		phase := cardTimerPhase(m)
		frac := float32(0)
		text := clockText(timerDefaultDur)
		if m != nil {
			frac = cardTimerFrac(m, now)
			text = clockTextCeil(m.timerRemainAt(now))
			if phase == cardIdle {
				text = clockText(m.dur)
			}
		}
		w := cardW(layout.Card{ID: "timer", Span: 1}, surf)
		if phase == cardDone {
			a.cardSwingingGlyph(c, card, "timer-lead", timerGlyph, 18).
				Absolute().Left(cardPad).Top(cardPad)
		} else {
			a.cardRingLead(c, card, "timer-lead", 22, frac, phase, timerGlyph).
				Absolute().Left(cardPad).Top(cardPad)
		}
		a.cardHero(c, card, "timer-hero", text, 22, 16, w-2*cardPad, cardHeroColor(phase),
			phase == cardPaused).Absolute().Left(cardPad).Bottom(cardPad)
		tr := a.cardTR(c, card, "timer-11-ctl")
		tr.Children(func() {
			switch phase {
			case cardIdle:
				btn := a.cardRound(c, "timer-11-start", 22, cardButtonPrimary,
					clockPlaySym, 10, l.TimerStart())
				if btn.Clicked() && m != nil {
					m.startTimer(m.clock(), m.dur)
				}
			case cardRunning:
				btn := a.cardSwapRound(c, card, "timer-11-toggle", true, 22, l.TimerPause())
				if btn.Clicked() && m != nil {
					m.pauseTimer(m.clock())
				}
			case cardPaused:
				btn := a.cardSwapRound(c, card, "timer-11-toggle", false, 22, l.TimerResume())
				if btn.Clicked() && m != nil {
					m.resumeTimer(m.clock())
				}
			case cardDone:
				btn := a.cardRound(c, "timer-11-stop", 22, cardButtonSolid,
					clockStopSym, 10, l.TimerStop())
				if btn.Clicked() && m != nil {
					m.stopTimer(m.clock())
				}
			}
		})

	})
}

// toolStopwatchBody draws the stopwatch card (v2 §3). The 2 × 1 form has
// the ring and name on the header and the transport in the action row;
// the 1 × 1 form puts pause / reset / play in the corner.
func (a *shelf) toolStopwatchBody(c *ui.Context, card ui.Element, span int, surf toolCardSurface) {
	card.Children(func() {
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
		phase := cardWatchPhase(m)
		elapsed := time.Duration(0)
		if m != nil {
			elapsed = m.watchElapsedAt(now)
		}
		frac := cardWatchFrac(elapsed)
		text := clockText(elapsed)
		w := cardW(layout.Card{ID: "stopwatch", Span: 2}, surf)
		top := a.cardTop(c, card, "watch-top")
		top.Children(func() {
			a.cardRingLead(c, card, "watch-lead", 24, frac, phase, watchGlyph)
			fitText(c, l.CardStopwatch(), 12, 600, 42).TextColor(ink2)
			ui.Box(c).Grow(1)
			a.cardHero(c, card, "watch-hero", text, 26, 22, w-2*cardPad-24-7-42-7,
				cardHeroColor(phase), phase == cardPaused)
		})
		foot := a.cardFoot(c, card, "watch-foot")
		foot.Children(func() {
			switch phase {
			case cardIdle:
				start := a.cardPill(c, "watch-start", l.TimerStart(), func(fg ui.Color) {
					ui.Icon(c, clockPlaySym).Size(12, 12).TextColor(fg)
				}, cardButtonPrimary, false)
				if start.Clicked() && m != nil {
					m.startWatch(m.clock())
				}
			case cardRunning:
				pause := a.cardPill(c, "watch-pause", l.TimerPause(), func(fg ui.Color) {
					a.cardSwapIcon(c, card, "watch-swap", true, 12, fg)
				}, cardButtonPrimary, false)
				if pause.Clicked() && m != nil {
					m.pauseWatch(m.clock())
				}
			case cardPaused:
				play := a.cardPill(c, "watch-resume", l.TimerResume(), func(fg ui.Color) {
					a.cardSwapIcon(c, card, "watch-swap", false, 12, fg)
				}, cardButtonPrimary, false)
				if play.Clicked() && m != nil {
					m.resumeWatch(m.clock())
				}
				reset := a.cardPill(c, "watch-reset", l.StopwatchReset(), func(fg ui.Color) {
					ui.Icon(c, clockResetSym).Size(12, 12).TextColor(fg)
				}, cardButtonSecondary, false)
				if reset.Clicked() && m != nil {
					m.resetWatch()
				}
			}
			a.cardFootIn(c, card, "watch", phase, foot)
		})

	})
}

// toolStopwatchNarrow is the 1 × 1 stopwatch: ring and value, and in the
// paused state ↺ beside ▶ in the corner.
func (a *shelf) toolStopwatchNarrow(c *ui.Context, card ui.Element, m *clockModule, surf toolCardSurface, now time.Time, l locales.Messages) {
	card.Children(func() {
		phase := cardWatchPhase(m)
		elapsed := time.Duration(0)
		if m != nil {
			elapsed = m.watchElapsedAt(now)
		}
		frac := cardWatchFrac(elapsed)
		w := cardW(layout.Card{ID: "stopwatch", Span: 1}, surf)
		a.cardRingLead(c, card, "watch-lead", 22, frac, phase, watchGlyph).
			Absolute().Left(cardPad).Top(cardPad)
		a.cardHero(c, card, "watch-hero", clockText(elapsed), 22, 16, w-2*cardPad,
			cardHeroColor(phase), phase == cardPaused).Absolute().Left(cardPad).Bottom(cardPad)
		tr := a.cardTR(c, card, "watch-11-ctl")
		tr.Children(func() {
			switch phase {
			case cardRunning:
				btn := a.cardSwapRound(c, card, "watch-11-toggle", true, 22, l.TimerPause())
				if btn.Clicked() && m != nil {
					m.pauseWatch(m.clock())
				}
			case cardPaused:
				reset := a.cardRound(c, "watch-11-reset", 22, cardButtonSecondary,
					clockResetSym, 11, l.StopwatchReset())
				if reset.Clicked() && m != nil {
					m.resetWatch()
				}
				btn := a.cardSwapRound(c, card, "watch-11-toggle", false, 22, l.TimerResume())
				if btn.Clicked() && m != nil {
					m.resumeWatch(m.clock())
				}
			}
		})

	})
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
	col, text := warning, ink
	if paused {
		col, text = warning.Alpha(0.45), ink3
	}
	remain := clockTextCeil(m.timerRemainAt(now))
	label := l.TimerChipLabel(remain)
	if paused {
		label = l.TimerChipPausedLabel(remain)
	}
	chip := ui.Row(c.Key("timer-chip")).Height(chipH).Radius(chipH/2).
		Padding(0, 9, 0, 6).Gap(5).AlignItems(ui.Center).Background(fill1).
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
