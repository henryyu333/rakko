//go:build darwin

package main

// The shared card skeleton (design/explore/tools-v2.html §2): a status
// graphic at the top left, one hero value, and the actions on a 24pt
// bottom row. Every widget draws through these helpers so the four sizes
// keep the same margins, type sizes and state colours.
//
// The hero value rolls per digit: only the characters that changed move,
// a falling value brings the new digit down from above, a rising value
// lifts it from below. The design's 1.5pt motion blur is not drawn —
// mygo has no text blur; the digits cross-fade instead (see cardHero).

import (
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
)

const (
	cardPad     = 10 // content inset, same as the design's 10
	cardRowH    = 28 // the header row (graphic, name, hero)
	cardCtrlH   = 24 // the action row
	cardHeroRow = 26 // 2-column hero
	cardHero11  = 22 // 1-column hero
	cardHeroBig = 40 // 2 × 2 hero
	cardRadius  = 14
)

var (
	// cardWarnText is the warning tone on the 18% orange primary button
	// and on the finished hero (@warnText).
	cardWarnText = ui.Hex("#FFB340")
	// cardAccentText is the hot action glyph's blue (@accText).
	cardAccentText = ui.Hex("#4BA3FF")
	// cardHover is the card's hover fill (the design's white 8.5%).
	cardHover = ui.RGBA(255, 255, 255, 0.085)
	// cardWarn18 is the primary button's fill.
	cardWarn18 = warning.Alpha(0.18)
)

// cardPhase is the five-state vocabulary of the cards (v2 §2): idle,
// running, paused, finished and switched off. Widgets without a clock
// use cardIdle.
type cardPhase int

const (
	cardIdle cardPhase = iota
	cardRunning
	cardPaused
	cardDone
	cardOff
)

// cardLeadColor is the state graphic's colour: orange while running or
// finished, the dimmed ink otherwise.
func cardLeadColor(phase cardPhase) ui.Color {
	switch phase {
	case cardRunning, cardDone:
		return warning
	}
	return ink2
}

// cardHeroColor follows the design's state colours: the restful value is
// ink2, a running one is ink, a finished one the warning tone, and a
// switched-off one ink3.
func cardHeroColor(phase cardPhase) ui.Color {
	switch phase {
	case cardRunning:
		return ink
	case cardDone:
		return cardWarnText
	case cardOff:
		return ink3
	}
	return ink2 // idle and paused
}

// cardRingAlpha is how present the progress arc is: hidden at rest and
// when switched off, 45% while paused, full while running.
func cardRingAlpha(phase cardPhase) float32 {
	switch phase {
	case cardRunning, cardDone:
		return 1
	case cardPaused:
		return 0.45
	}
	return 0
}

// cardTimerPhase maps the countdown's state.
func cardTimerPhase(m *clockModule) cardPhase {
	if m == nil {
		return cardIdle
	}
	switch m.phase {
	case timerRunning:
		return cardRunning
	case timerPaused:
		return cardPaused
	case timerDone:
		return cardDone
	}
	return cardIdle
}

// cardWatchPhase maps the stopwatch's state: no run and no elapsed time
// is idle, the frozen elapsed time is paused.
func cardWatchPhase(m *clockModule) cardPhase {
	if m == nil {
		return cardIdle
	}
	switch {
	case m.watchRun:
		return cardRunning
	case m.watchElapsed > 0:
		return cardPaused
	}
	return cardIdle
}

// cardAwakePhase maps the keep-awake widget.
func cardAwakePhase(m *awakeModule) cardPhase {
	if m == nil || !m.active() {
		return cardOff
	}
	return cardRunning
}

// cardTop is the header row: 10pt in from the card's edges, 28 high.
func (a *shelf) cardTop(c *ui.Context, card ui.Element, key string) ui.Element {
	return ui.Row(c.Key(key)).Absolute().Left(cardPad).Right(cardPad).Top(cardPad).
		Height(cardRowH).AlignItems(ui.Center).Gap(7)
}

// cardFoot is the action row: 10pt up from the bottom, 24 high.
func (a *shelf) cardFoot(c *ui.Context, card ui.Element, key string) ui.Element {
	return ui.Row(c.Key(key)).Absolute().Left(cardPad).Right(cardPad).Bottom(cardPad).
		Height(cardCtrlH).AlignItems(ui.Center).Gap(5)
}

// cardTL is the 1-column graphic slot: 10 / 10.
func (a *shelf) cardTL(c *ui.Context, card ui.Element, key string) ui.Element {
	return ui.Row(c.Key(key)).Absolute().Left(cardPad).Top(cardPad).Height(22).AlignItems(ui.Center)
}

// cardTR is the 1-column control slot at the top right: 8 / 8.
func (a *shelf) cardTR(c *ui.Context, card ui.Element, key string) ui.Element {
	return ui.Row(c.Key(key)).Absolute().Right(8).Top(8).Height(22).Gap(4).AlignItems(ui.Center)
}

// cardBL is the 1-column value slot: 10pt from the left and bottom.
func (a *shelf) cardBL(c *ui.Context, card ui.Element, key string) ui.Element {
	return ui.Row(c.Key(key)).Absolute().Left(cardPad).Bottom(cardPad).Height(24).AlignItems(ui.End)
}

// cardName is the 12pt semibold name that follows the status graphic.
func cardName(c *ui.Context, key string, name string, col ui.Color, avail float32) ui.Element {
	t := fitText(c, name, 12, 600, avail).TextColor(col)
	_ = key
	return t
}

// cardRingLead draws the status graphic inside a progress ring: a fill3
// track, the warning arc and the glyph. size is 24 in the two-column
// cards and 22 in the 1 × 1 ones; stroke 2.4 and glyph 12 throughout
// (v2 §2). The arc fades between states over 250 ms and advances over
// 450 ms linearly, as the design's ring does.
func (a *shelf) cardRingLead(c *ui.Context, card ui.Element, key string, size, frac float32, phase cardPhase, glyph *ui.SVG) ui.Element {
	box := ui.Box(c.Key(key)).Size(size, size)
	targetAlpha := cardRingAlpha(phase)
	alpha := targetAlpha
	shownFrac := frac
	if reduceMotion(c) {
		alpha = card.Jump("ring-a-"+key, targetAlpha)
		shownFrac = card.Jump("ring-f-"+key, frac)
	} else {
		alpha = card.AnimateTo("ring-a-"+key, targetAlpha, ui.Animation{Duration: 250 * time.Millisecond, Ease: ui.EaseOut})
		shownFrac = card.AnimateTo("ring-f-"+key, frac, ui.Animation{Duration: 450 * time.Millisecond, Ease: ui.Linear})
	}
	col := card.AnimateColor("lead-"+key, cardLeadColor(phase), ui.Animation{Duration: 250 * time.Millisecond, Ease: ui.EaseOut})
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		rad := size/2 - 1.2 // the 2.4pt stroke centred on the radius
		var track ui.Path
		track.Circle(cx, cy, rad)
		p.StrokePath(&track, 2.4, fill3)
		if alpha > 0.01 {
			liveArc(p, cx, cy, rad, 0.75, shownFrac, 2.4, warning.Alpha(alpha))
		}
	})
	box.Children(func() {
		ui.Icon(c, glyph).Size(12, 12).TextColor(col)
	})
	return box
}

// cardGlyphLead draws a state graphic without a ring (finished cards and
// action cards). size and glyph size differ per card, so the caller
// decides them; the colour animates over 250 ms.
func (a *shelf) cardGlyphLead(c *ui.Context, card ui.Element, key string, glyph *ui.SVG, size, glyphSize float32, col ui.Color) ui.Element {
	box := ui.Box(c.Key(key)).Size(size, size).Center()
	shown := card.AnimateColor("lead-"+key, col, ui.Animation{Duration: 250 * time.Millisecond, Ease: ui.EaseOut})
	box.Children(func() {
		ui.Icon(c, glyph).Size(glyphSize, glyphSize).TextColor(shown)
	})
	return box
}

// cardSwingingGlyph is the finished countdown's hourglass: ±14° every
// 900 ms (v2 §7), still under Reduce Motion.
func (a *shelf) cardSwingingGlyph(c *ui.Context, card ui.Element, key string, glyph *ui.SVG, size float32) ui.Element {
	box := ui.Box(c.Key(key)).Size(20, 20).Center()
	icon := ui.Icon(c, glyph).Size(size, size).TextColor(warning)
	if !reduceMotion(c) {
		k := icon.Loop("swing-"+key, 900*time.Millisecond, ui.Linear)
		icon.Rotate(float32(14*math.Sin(2*math.Pi*float64(k)))).TransformOrigin(0.5, 0.12)
		c.AnimationFrame()
	}
	box.Children(func() { _ = icon })
	return box
}

// cardButtonStyle is the three button fills of the design: the warning
// primary, the neutral secondary and the solid warning stop.
type cardButtonStyle int

const (
	cardButtonPrimary cardButtonStyle = iota
	cardButtonSecondary
	cardButtonSolid
)

func cardButtonColors(style cardButtonStyle) (bg, fg ui.Color) {
	switch style {
	case cardButtonPrimary:
		return cardWarn18, cardWarnText
	case cardButtonSolid:
		return warning, timerStopInk
	}
	return fill2, ink
}

// cardPill is a 24pt capsule button: 11.5 semibold, 12pt side padding.
// grow lets the stop button fill the row. icon, when set, draws the
// button's leading glyph in the button's own ink.
func (a *shelf) cardPill(c *ui.Context, key string, label string, icon func(fg ui.Color), style cardButtonStyle, grow bool) ui.Element {
	bg, fg := cardButtonColors(style)
	left := float32(10)
	if icon != nil {
		left = 8
	}
	b := ui.Row(c.Key(key)).Height(cardCtrlH).Radius(cardCtrlH/2).
		Padding(0, 10, 0, left).Gap(5).AlignItems(ui.Center).
		Background(bg).Label(label).Tooltip(label)
	if grow {
		b.Grow(1)
	}
	b.Children(func() {
		if icon != nil {
			icon(fg)
		}
		ui.Text(c, label).SingleLine().FontSize(11.5).FontWeight(600).TextColor(fg)
	})
	cardButtonPress(c, b, b.Pressed(), 0.94)
	return clockSwallow(b)
}

// cardRound is a circular button: 24pt in the action row, 22 in a 1 × 1
// card's corner.
func (a *shelf) cardRound(c *ui.Context, key string, size float32, style cardButtonStyle, glyph *ui.SVG, glyphSize float32, label string) ui.Element {
	bg, fg := cardButtonColors(style)
	b := ui.Box(c.Key(key)).Size(size, size).Radius(size / 2).Center().
		Background(bg).Label(label).Tooltip(label)
	b.Children(func() {
		ui.Icon(c, glyph).Size(glyphSize, glyphSize).TextColor(fg)
	})
	return clockSwallow(b)
}

// cardButtonPress scales a pressed control: pills to 0.94, rounds to
// 0.9, chips to 0.92 (v2 §2/§4), over 100 ms.
func cardButtonPress(c *ui.Context, el ui.Element, pressed bool, base float32) {
	target := float32(1)
	if pressed {
		target = base
	}
	if reduceMotion(c) {
		el.Scale(target, target).TransformOrigin(0.5, 0.5)
		return
	}
	s := el.AnimateTo("press", target, ui.Animation{Duration: 100 * time.Millisecond, Ease: ui.EaseOut})
	el.Scale(s, s).TransformOrigin(0.5, 0.5)
}

// cardSwapRound is a 1 × 1 card's corner toggle: a 22pt warning circle
// whose ▶ and ❙❙ cross over in 160 ms.
func (a *shelf) cardSwapRound(c *ui.Context, card ui.Element, key string, playing bool, size float32, label string) ui.Element {
	b := ui.Box(c.Key(key)).Size(size, size).Radius(size / 2).Center().
		Background(cardWarn18).Label(label).Tooltip(label)
	b.Children(func() {
		a.cardSwapIcon(c, card, key+"-icon", playing, 11, cardWarnText)
	})
	cardButtonPress(c, b, b.Pressed(), 0.9)
	return clockSwallow(b)
}

// cardSwapIcon is the ▶ / ❙❙ pair: the two glyphs cross over in 160 ms by
// scaling and rotating, as the design's .icoswap. playing true shows the
// pause glyph.
func (a *shelf) cardSwapIcon(c *ui.Context, card ui.Element, key string, playing bool, glyph float32, col ui.Color) ui.Element {
	target := float32(0)
	if playing {
		target = 1
	}
	k := target
	if reduceMotion(c) {
		k = card.Jump("swap-"+key, target)
	} else {
		k = card.AnimateTo("swap-"+key, target, ui.Animation{Duration: 160 * time.Millisecond, Ease: ui.EaseOut})
	}
	box := ui.Box(c.Key("swap-"+key)).Size(glyph+2, glyph+2)
	box.Children(func() {
		play := ui.Icon(c, clockPlaySym).Size(glyph, glyph).TextColor(col).Absolute().Left(1).Top(1).
			Scale(0.4+0.6*(1-k), 0.4+0.6*(1-k)).Rotate(60*k).
			TransformOrigin(0.5, 0.5).Opacity(1 - k)
		pause := ui.Icon(c, clockPauseSym).Size(glyph, glyph).TextColor(col).Absolute().Left(1).Top(1).
			Scale(0.4+0.6*k, 0.4+0.6*k).Rotate(-60*(1-k)).
			TransformOrigin(0.5, 0.5).Opacity(k)
		_ = play
		_ = pause
	})
	return box
}

// cardCountBadge is the drag count: an 18pt accent capsule on the card's
// top right, replacing the old "N 张图片" subtitle.
func (a *shelf) cardCountBadge(c *ui.Context, key string, n int) ui.Element {
	b := ui.Row(c.Key(key)).Absolute().Right(7).Top(7).Height(18).MinWidth(18).
		Padding(0, 5, 0, 5).Radius(9).Center().Background(accent).Label(strconv.Itoa(n))
	b.Children(func() {
		ui.Text(c, strconv.Itoa(n)).SingleLine().FontSize(10.5).FontWeight(700).
			FontFeatures("tnum").TextColor(ui.Hex("#FFFFFF"))
	})
	return b
}

// cardSpinner is the busy action graphic: the glyph shrinks to 14 inside
// a 1-second rotating accent arc (v2 §4 "正在转换").
func (a *shelf) cardSpinner(c *ui.Context, key string, glyph *ui.SVG) ui.Element {
	box := ui.Box(c.Key(key)).Size(24, 24).Center()
	k := box.Loop("spin-"+key, time.Second, ui.Linear)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		rad := float32(10.5)
		var arc ui.Path
		arc.Circle(cx, cy, rad).Trim(k, k+0.22)
		p.StrokePath(&arc, 2, accent)
	})
	box.Children(func() {
		ui.Icon(c, glyph).Size(14, 14).TextColor(ink2)
	})
	c.AnimationFrame()
	return box
}

// cardEqBars is the small animated equalizer beside a track title: n
// bars of h height, moving only while the track plays (and never under
// Reduce Motion).
func (a *shelf) cardEqBars(c *ui.Context, key string, playing bool, n int, h float32) ui.Element {
	row := ui.Row(c.Key(key)).Height(h).Gap(2).AlignItems(ui.End)
	row.Children(func() {
		for i := range n {
			bar := ui.Box(c.Key(i)).Width(2.5).Radius(1).Background(ink2).AlignSelf(ui.End)
			barH := h * 0.35
			if playing && !reduceMotion(c) {
				k := bar.Loop("eq", eqPeriods[i%len(eqPeriods)], ui.Bounce(ui.EaseInOut))
				wave := math.Sin(float64(float32(i)*0.6+k) * math.Pi)
				barH = h * (0.25 + 0.75*float32(wave*wave))
				c.AnimationFrame()
			} else if playing {
				barH = h * 0.6
			}
			bar.Height(barH)
		}
	})
	return row
}

// cardRoll is one hero value's roll state: the text before the change,
// the text drawn now, when it changed and which way it moved.
type cardRoll struct {
	prev string
	text string
	at   time.Time
	dir  int
}

// cardRolls stores each hero's roll state on the shelf (main thread).
// The key carries the surface, so the panel card and its desktop pin do
// not share an animation.
func (a *shelf) cardRollsFor(key string) *cardRoll {
	if a.cardRolls == nil {
		a.cardRolls = map[string]*cardRoll{}
	}
	r := a.cardRolls[key]
	if r == nil {
		r = &cardRoll{}
		a.cardRolls[key] = r
	}
	return r
}

// cardRollDirection compares the numeric content of two readings: a
// smaller new value falls in from above, a larger one rises from below.
// Non-numeric or equal readings cross-fade instead.
func cardRollDirection(old, text string) int {
	a, okA := cardRollNumber(old)
	b, okB := cardRollNumber(text)
	if !okA || !okB || a == b {
		return 0
	}
	if b > a {
		return 1
	}
	return -1
}

// cardRollNumber reads the digits of a reading as one number ("1:02:03"
// → 10203), so the comparison ignores the punctuation.
func cardRollNumber(s string) (int, bool) {
	v, seen := 0, false
	for _, r := range s {
		if r < '0' || r > '9' {
			continue
		}
		v = v*10 + int(r-'0')
		seen = true
	}
	return v, seen
}

// cardHero draws one hero value: 26pt when there is room, 22 for readings
// over five characters (both shrinking further to fit), with the design's
// per-digit roll. avail is the width the value may use. key must be
// unique to this card and surface.
func (a *shelf) cardHero(c *ui.Context, card ui.Element, key, text string, base, small, avail float32, col ui.Color, blink bool) ui.Element {
	size := base
	if len([]rune(text)) > 5 {
		size = small
	}
	size = clockReadingSize(c, text, avail, size)
	shownCol := card.AnimateColor("hero-"+key, col, ui.Animation{Duration: 250 * time.Millisecond, Ease: ui.EaseOut})

	now := c.Now()
	state := a.cardRollsFor(key)
	if state.text != text {
		fresh := state.text == ""
		state.prev = state.text
		state.dir = cardRollDirection(state.text, text)
		state.at = now
		if fresh {
			// A hero seen for the first time is simply there: the 150 ms
			// fade belongs to values that change (v2 §7).
			state.at = now.Add(-time.Second)
		}
		state.text = text
	}
	// rollK is how far the 280 ms roll has gone; a length change or a
	// non-numeric value fades instead, over 150 ms.
	rollDur := 280 * time.Millisecond
	if reduceMotion(c) || state.dir == 0 || len([]rune(state.prev)) != len([]rune(text)) {
		rollDur = 150 * time.Millisecond
	}
	elapsed := now.Sub(state.at)
	if elapsed < 0 {
		elapsed = 0
	}
	p := float32(1)
	if rollDur > 0 && elapsed < rollDur {
		p = float32(elapsed) / float32(rollDur)
	}
	eased := ui.EaseOut(p)
	if p < 1 {
		// The roll/fade is the only movement here: ask for the next frame,
		// or a preset click that changes no other animation would leave
		// the digits frozen mid-flight.
		c.AnimationFrame()
	}

	// A paused reading's colons blink on a 1.6 s step (v2 §2), unless the
	// desktop asks for less motion.
	blinkOff := false
	if blink && !reduceMotion(c) {
		k := card.Loop("blink-"+key, 1600*time.Millisecond, ui.Linear)
		blinkOff = k >= 0.5
		c.AnimationFrame()
	}

	prevRunes, newRunes := []rune(state.prev), []rune(text)
	rolling := p < 1 && state.dir != 0 && len(prevRunes) == len(newRunes) && state.prev != "" && !reduceMotion(c)
	line := size * 1.1
	row := ui.Row(c.Key("hero-" + key)).Height(line).AlignItems(ui.Center).Gap(0).Label(text)
	row.Children(func() {
		for i, ch := range newRunes {
			w, _ := c.MeasureText(0, ui.Span{Text: string(ch), Size: size, Weight: 600, Features: "tnum"})
			box := ui.Box(c.Key("d" + strconv.Itoa(i))).Width(w).Height(line).Clip()
			box.Children(func() {
				digit := ch >= '0' && ch <= '9'
				punctOff := blinkOff && !digit
				if rolling && prevRunes[i] != ch {
					oldY, newY := float32(0), float32(0)
					if state.dir > 0 {
						// Rising: the new digit comes from below.
						newY = line * (1 - eased)
						oldY = -line * eased
					} else {
						// Falling: the new digit comes from above.
						newY = -line * (1 - eased)
						oldY = line * eased
					}
					ui.Text(c, string(prevRunes[i])).SingleLine().FontSize(size).FontWeight(600).
						LineHeight(1).FontFeatures("tnum").TextColor(shownCol).Opacity(1 - eased).
						Absolute().Left(0).Top(oldY)
					ui.Text(c, string(ch)).SingleLine().FontSize(size).FontWeight(600).
						LineHeight(1).FontFeatures("tnum").TextColor(shownCol).Opacity(eased).
						Absolute().Left(0).Top(newY)
					return
				}
				op := float32(1)
				if p < 1 {
					op = 0.3 + 0.7*eased
				}
				if punctOff {
					op *= 0.25
				} else if !digit {
					op *= 0.9
				}
				ui.Text(c, string(ch)).SingleLine().FontSize(size).FontWeight(600).
					LineHeight(1).FontFeatures("tnum").TextColor(shownCol).Opacity(op).
					Absolute().Left(0).Top(0)
			})
		}
	})
	return row
}

// cardClipText is a small two-line text block for the 1 × 1 cards: 11pt
// semibold, at most two lines, with the full content in the label.
func cardClipText(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).FontSize(11).FontWeight(600).LineHeight(1.35).MaxLines(2).
		Ellipsis("…").Label(text)
}

// cardRail is a thin meter: a fill3 track and col's share from the left.
func cardRail(c *ui.Context, key string, frac float32, col ui.Color) ui.Element {
	box := ui.Box(c.Key(key)).Height(4).Radius(2)
	shown := frac
	if reduceMotion(c) {
		shown = box.Jump("rail", frac)
	} else {
		shown = box.AnimateTo("rail", frac, ui.Animation{Duration: 450 * time.Millisecond, Ease: ui.Linear})
	}
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.FillPath(liveRoundPath(r.X, r.Y, r.W, r.H, 2), fill3)
		if shown > 0 {
			p.FillPath(liveRoundPath(r.X, r.Y, max(2, r.W*min(shown, 1)), r.H, 2), col)
		}
	})
	return box
}

// cardTicks is the 2 × 2 keep-awake scale: 24 thin bars, the first lit
// ones at 18pt in the warning tone.
func (a *shelf) cardTicks(c *ui.Context, key string, frac float32) ui.Element {
	row := ui.Row(c.Key(key)).Height(22).Gap(2).AlignItems(ui.End).FillWidth()
	lit := int(math.Round(float64(min(max(frac, 0), 1)) * 24))
	row.Children(func() {
		for i := range 24 {
			h := float32(12)
			col := fill3
			if i < lit {
				h, col = 18, warning
			}
			ui.Box(c.Key(i)).Grow(1).Height(h).Radius(1.5).Background(col)
		}
	})
	return row
}

// cardChip is one small choice chip (timer presets, awake durations):
// 22 high, radius 7, the selected one in its accent.
func (a *shelf) cardChip(c *ui.Context, key, label string, on bool, warm bool) ui.Element {
	chip := ui.Row(c.Key(key)).Height(22).Grow(1).MinWidth(0).Radius(7).Center().Label(label)
	fg := ink2
	switch {
	case on && warm:
		chip.Background(cardWarn18)
		fg = cardWarnText
	case on:
		chip.Background(accent)
		fg = ink
	default:
		chip.Background(fill1)
	}
	chip.Children(func() {
		ui.Text(c, label).SingleLine().FontSize(10.5).FontWeight(600).
			FontFeatures("tnum").TextColor(fg)
	})
	cardButtonPress(c, chip, chip.Pressed(), 0.92)
	return clockSwallow(chip)
}

// cardPaint is the transient picture a card draws for the page: files it
// accepts hovering it (hot, count) and its own conversion running.
type cardPaint struct {
	hot   bool
	count int
	busy  bool
}

// cardW is a card's width on a surface: the grid's cell or the desktop
// window's card width (the pin widens the widgets that need it).
func cardW(spec layout.Card, surf toolCardSurface) float32 {
	if surf == cardDesktop {
		return float32(pinCardWidth(spec.ID, spec.Cols()))
	}
	return toolColW(spec.Cols())
}

// toolCardBusy reports whether this action card's own conversion is
// running: it draws the spinning glyph instead of dimming (v2 §4).
func (a *shelf) toolCardBusy(id string) bool {
	if a.conv == nil || a.conv.job == nil {
		return false
	}
	return cardForConvertTarget(a.conv.job.target) == id
}

// cardForConvertTarget maps a running conversion to the card that
// started it (the tray's menu and a card share the target).
func cardForConvertTarget(t convertTarget) string {
	switch t {
	case toPDF, toMergedPDF:
		return "topdf"
	case toJPEG:
		return "tojpeg"
	case toWord:
		return "toword"
	case toExtract:
		return "extract"
	case toZip:
		return "zip"
	case toFit500, toFit1M, toFit2M, toFit5M:
		return "fit1m"
	case toLarge, toMedium, toSmall:
		return "resize"
	}
	return ""
}

// specFor is a bare card spec for the width helpers when only the span
// matters.
func specFor(span int) layout.Card { return layout.Card{Span: span} }

// cardFootIn fades a freshly rebuilt action row in and lifts it 6pt over
// 240 ms when the card's state changes (v2 §7). The phase index is the
// animated value: a row first seen starts at its target, so it draws
// without an intro; a phase change moves the value from the old index and
// the distance is the row's offset and transparency. Reduce Motion only
// swaps the row.
func (a *shelf) cardFootIn(c *ui.Context, card ui.Element, key string, phase cardPhase, row ui.Element) {
	target := float32(phase)
	if reduceMotion(c) {
		card.Jump("foot-k-"+key, target)
		return
	}
	k := card.AnimateTo("foot-k-"+key, target, ui.Animation{Duration: 240 * time.Millisecond, Ease: ui.EaseOut})
	d := float32(math.Abs(float64(k - target)))
	if d < 0.001 {
		return
	}
	row.Opacity(1-d).Translate(0, 6*d)
}
