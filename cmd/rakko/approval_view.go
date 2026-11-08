//go:build darwin

package main

// The approval card: three layers — the agent's own sentence, the risks
// that were matched, and the raw command, file list or URL one click away.
// The card owns the notch, never the keyboard: its only shortcut is ⌘↩.

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
	"rakko/locales"
)

const (
	approvalCardW     = 360
	approvalPadX      = 16
	approvalPadBot    = 12
	approvalGap       = 6
	approvalRow1H     = 18
	approvalLineH     = 18
	approvalRiskH     = 20
	approvalCmdH      = 28
	approvalBtnH      = 32
	approvalBtnR      = 8
	approvalDetailMax = 118
	approvalMono      = "SF Mono, Menlo, monospace"
)

// The warning and danger text tones from the design; the shell's own
// warning colour is the fill.
var (
	approvalWarnText   = ui.Hex("#FFB340")
	approvalDangerText = ui.Hex("#FF6961")
	approvalHoldBase   = ui.Hex("#B96F00")
	approvalHoldFill   = ui.Hex("#FFC561")
	approvalHoldInk    = ui.Hex("#140D02")
)

var (
	approvalChevSym   = ui.MustParseSVG([]byte(`<svg viewBox="0 0 12 12" xmlns="http://www.w3.org/2000/svg"><path d="M3 4.5 6 7.5l3-3" stroke="currentColor" stroke-width="1.4" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
	approvalCheckSym  = ui.MustParseSVG([]byte(`<svg viewBox="0 0 14 14" xmlns="http://www.w3.org/2000/svg"><path d="M2.6 7.4 5.6 10.4 11.4 4.2" stroke="currentColor" stroke-width="1.9" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
	approvalCrossSym  = ui.MustParseSVG([]byte(`<svg viewBox="0 0 14 14" xmlns="http://www.w3.org/2000/svg"><path d="M3.6 3.6l6.8 6.8M10.4 3.6l-6.8 6.8" stroke="currentColor" stroke-width="1.9" fill="none" stroke-linecap="round"/></svg>`))
	approvalClockSym  = ui.MustParseSVG([]byte(`<svg viewBox="0 0 14 14" xmlns="http://www.w3.org/2000/svg"><circle cx="7" cy="7" r="5.2" stroke="currentColor" stroke-width="1.3" fill="none"/><path d="M7 4.3V7l1.9 1.2" stroke="currentColor" stroke-width="1.3" fill="none" stroke-linecap="round"/></svg>`))
	approvalReturnSym = ui.MustParseSVG([]byte(`<svg viewBox="0 0 14 14" xmlns="http://www.w3.org/2000/svg"><path d="M5 3.2 2.2 7 5 10.8M9 3.2 11.8 7 9 10.8" stroke="currentColor" stroke-width="1.3" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
)

// approvalCardSize is the shell a card needs: the notch band plus the body,
// or the island's floating box.
func (a *shelf) approvalCardSize(ap *approval) (w, h float32) {
	baseW, _ := a.anchorSize()
	// The 380 pt cap keeps the card from growing beyond the design, but a
	// notch wider than that still needs its own width.
	w = min(max(approvalCardW, baseW+60), max(float32(fbMaxW), baseW))
	body := ap.bodyHeight(w)
	if a.island {
		// The island has no notch band: 6 pt above the body, whose own
		// bottom padding is the card's lower inset.
		return w, body + 6
	}
	return w, float32(max(32, a.notchH)) + body
}

// bodyHeight is the content under the notch band. It is computed from the
// same constants the layout uses, so the buttons never fall out of the
// shell; the sentence's height comes from both languages, because the shell
// is sized before the locale is known.
func (ap *approval) bodyHeight(w float32) float32 {
	textW := w - 2*approvalPadX - 27
	lines := max(
		approvalSentenceLines(ap.analysis.Sentence(locales.In("zh-Hans")), textW),
		approvalSentenceLines(ap.analysis.Sentence(locales.In("en")), textW),
	)
	body := float32(approvalRow1H+approvalGap) + float32(lines*approvalLineH+approvalGap)
	if len(ap.analysis.Risks) > 0 {
		body += approvalRiskH + approvalGap
	}
	body += ap.detailHeight() + approvalGap
	if ap.state == approvalDenying {
		body += approvalBtnH + approvalGap + approvalBtnH
	} else {
		body += approvalBtnH
	}
	body += approvalPadBot
	return body
}

// approvalSentenceLines estimates the wrapped line count at 13 pt, capped
// at the card's two lines.
func approvalSentenceLines(text string, width float32) int {
	if width <= 0 {
		return 1
	}
	lines := 1
	var used float32
	for _, r := range text {
		w := float32(7.2)
		if r > 0x2E80 {
			w = 13
		}
		if used+w > width {
			lines++
			used = 0
			if lines >= 2 {
				return 2
			}
		}
		used += w
	}
	return lines
}

// detailHeight is the collapsed one-line box or the expanded panel.
func (ap *approval) detailHeight() float32 {
	if !ap.expanded || !ap.state.active() {
		return approvalCmdH
	}
	switch ap.analysis.Detail.Kind {
	case agentnotify.DetailFiles:
		h := float32(12 + len(ap.analysis.Detail.Files)*18)
		if ap.analysis.Detail.More > 0 {
			h += 16
		}
		return min(h, approvalDetailMax)
	default:
		return 112
	}
}

// approvalCard draws the request at the shell's final size.
func (a *shelf) approvalCard(c *ui.Context, shellW, shellH, k float32, ap *approval) ui.Element {
	l := loc(c)
	now := c.Now()
	top, padBot := float32(max(32, a.notchH)), float32(approvalPadBot)
	if a.island {
		top, padBot = 6, 0
	}
	card := ui.Column(c.Key("approval-card")).Width(shellW).Height(shellH).
		Padding(top, approvalPadX, padBot, approvalPadX).Gap(approvalGap).
		Clip().Opacity(min(max(k*k, 0), 1)).Label(approvalStatusLine(l, ap, now))
	card.Children(func() {
		a.approvalRow1(c, shellW, ap, now)
		a.approvalSentence(c, ap)
		if len(ap.analysis.Risks) > 0 {
			a.approvalRisks(c, ap)
		}
		a.approvalDetail(c, ap, now)
		a.approvalTail(c, shellW, ap, now)
	})
	// The age and the armed window are time-driven.
	if ap.state.active() {
		c.After(time.Second)
	}
	if ap.state == approvalArmed && now.After(ap.armedUntil) {
		ap.state = approvalShown
	}
	return card
}

// approvalResultLabel is the spoken result state, when the card has one.
func approvalResultLabel(l locales.Messages, state approvalState) (string, bool) {
	switch state {
	case approvalAllowed:
		return l.LiveApprovalAllowed(), true
	case approvalDenied:
		return l.LiveApprovalDenied(), true
	case approvalExpired:
		return l.LiveApprovalExpired(), true
	case approvalTerminal:
		return l.LiveApprovalTerminalDone(), true
	}
	return "", false
}

// approvalAgeColor warns once the request has waited 30 seconds.
func approvalAgeColor(age int) ui.Color {
	if age >= 30 {
		return approvalWarnText
	}
	return ink3
}

// approvalStatusLine is the card's one spoken sentence.
func approvalStatusLine(l locales.Messages, ap *approval, now time.Time) string {
	if label, ok := approvalResultLabel(l, ap.state); ok {
		return l.LiveStatusAgentAsk(ap.ask.Tool.Name(), label)
	}
	what := ap.analysis.Sentence(l)
	for _, r := range ap.analysis.Risks {
		what += " · " + r.Label(l)
	}
	age := int(now.Sub(ap.at).Seconds())
	return l.LiveStatusAgentAsk(ap.ask.Tool.Name(), what) + " · " + l.LiveApprovalAge(age)
}

func (a *shelf) approvalRow1(c *ui.Context, shellW float32, ap *approval, now time.Time) {
	l := loc(c)
	age := int(now.Sub(ap.at).Seconds())
	ageColor := approvalAgeColor(age)
	row := ui.Row(c.Key("approval-row1")).FillWidth().Height(approvalRow1H).Gap(6).AlignItems(ui.Center)
	row.Children(func() {
		logo, tint, mark := agentLogo(ap.ask.Tool)
		settingsMark(c, logo, tint, mark)
		ui.Text(c, ap.ask.Tool.Name()).SingleLine().FontSize(13).FontWeight(600).TextColor(ink).
			Shrink(1).MinWidth(0).Ellipsis("…")
		ui.Box(c.Key("approval-dot")).Size(3, 3).Radius(1.5).Background(ink3)
		ui.Text(c, approvalProject(ap.ask.Cwd)).SingleLine().FontSize(12).FontWeight(400).
			TextColor(ink3).Shrink(1).MinWidth(0).Ellipsis("…")
		if n := a.approvalFeature().queueCount(ap.id); n > 0 {
			badge := ui.Box(c.Key("approval-queue")).Height(17).MinWidth(17).Radius(9).
				Padding(0, 5).Center().Background(fill2)
			badge.Children(func() {
				ui.Text(c, "+"+strconv.Itoa(n)).SingleLine().FontSize(10).FontWeight(650).
					FontFeatures("tnum").TextColor(ink2)
			})
		}
		ui.Box(c).Grow(1)
		ui.Text(c, l.LiveApprovalAge(age)).SingleLine().FontSize(11).
			FontWeight(500).FontFeatures("tnum").TextColor(ageColor)
	})
}

func (a *shelf) approvalSentence(c *ui.Context, ap *approval) {
	l := loc(c)
	ui.Text(c, ap.analysis.Sentence(l)).FontSize(13).FontWeight(500).TextColor(ink2).
		MaxLines(2).FixedLineHeight(approvalLineH).Margin(0, 0, 0, 27).
		Shrink(1).MinWidth(0)
}

func (a *shelf) approvalRisks(c *ui.Context, ap *approval) {
	l := loc(c)
	row := ui.Row(c.Key("approval-risks")).FillWidth().Wrap().Gap(4).AlignItems(ui.Center)
	row.Children(func() {
		for _, r := range ap.analysis.Risks {
			bg, fg := ui.RGBA(255, 159, 10, 0.16), approvalWarnText
			if r.Danger {
				bg, fg = ui.RGBA(255, 69, 58, 0.17), approvalDangerText
			}
			chip := ui.Box(c.Key("approval-risk-"+strconv.Itoa(int(r.Kind)))).Height(approvalRiskH).
				Radius(6).Padding(0, 8).Center().Background(bg)
			chip.Children(func() {
				ui.Text(c, r.Label(l)).SingleLine().FontSize(11).FontWeight(600).TextColor(fg)
			})
		}
	})
}

// approvalDetail is the collapsed one-line block or the expanded raw data.
// Clicking it never decides anything.
func (a *shelf) approvalDetail(c *ui.Context, ap *approval, now time.Time) {
	l := loc(c)
	h := ap.detailHeight()
	bg := fill1
	if len(ap.analysis.Risks) > 0 {
		bg = ui.RGBA(255, 159, 10, 0.07)
	}
	box := ui.Box(c.Key("approval-detail")).FillWidth().Height(h).Radius(7).Padding(6, 8).
		Background(bg).Clip().Label(approvalDetailLabel(ap))
	if ap.state.active() {
		if box.Clicked() {
			ap.expanded = !ap.expanded
			if a.win != nil {
				a.win.Invalidate()
			}
		}
	}
	if !ap.expanded || !ap.state.active() {
		box.Children(func() {
			row := ui.Row(c.Key("approval-detail-one")).FillWidth().Gap(8).AlignItems(ui.Center)
			row.Children(func() {
				ui.Text(c, approvalCollapsed(l, ap)).Font(approvalMono).FontSize(12).TextColor(ink).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
				ui.Icon(c, approvalChevSym).Size(12, 12).TextColor(ink3)
			})
		})
		return
	}
	box.Children(func() {
		row := ui.Row(c.Key("approval-detail-expanded")).FillWidth().Gap(8).AlignItems(ui.Start)
		row.Children(func() {
			scroll := ui.Scroll(c).FillWidth().Grow(1).TrackScroll(&ap.scroll).Height(h - 12)
			scroll.Children(func() {
				switch ap.analysis.Detail.Kind {
				case agentnotify.DetailFiles:
					a.approvalFiles(c, ap)
				case agentnotify.DetailURL:
					ui.Text(c, ap.analysis.Detail.URL).Font(approvalMono).FontSize(12).
						TextColor(ink)
				default:
					ui.Text(c, ap.analysis.Detail.Command).Font(approvalMono).FontSize(12).
						FixedLineHeight(16).TextColor(ink)
				}
			})
			ui.Icon(c, approvalChevSym).Size(12, 12).TextColor(ink3).Rotate(180)
		})
	})
}

// approvalCollapsed is the one-line summary of the raw detail.
func approvalCollapsed(l locales.Messages, ap *approval) string {
	switch ap.analysis.Detail.Kind {
	case agentnotify.DetailFiles:
		n := len(ap.analysis.Detail.Files) + ap.analysis.Detail.More
		if n == 1 {
			return ap.analysis.Detail.Files[0].Path
		}
		return l.LiveFiles(n)
	case agentnotify.DetailURL:
		return ap.analysis.Detail.URL
	default:
		return ap.analysis.Detail.Command
	}
}

func (a *shelf) approvalFiles(c *ui.Context, ap *approval) {
	l := loc(c)
	column := ui.Column(c.Key("approval-files")).FillWidth().Gap(3)
	column.Children(func() {
		for _, f := range ap.analysis.Detail.Files {
			row := ui.Row(c.Key("approval-file-" + f.Path)).FillWidth().AlignItems(ui.Baseline).Gap(8)
			row.Children(func() {
				ui.Text(c, f.Path).SingleLine().FontSize(12).TextColor(ink).
					Ellipsis("…").Shrink(1).MinWidth(0)
				ui.Box(c).Grow(1)
				if f.CountsKnown {
					if f.Add > 0 {
						ui.Text(c, "+"+strconv.Itoa(f.Add)).SingleLine().FontSize(11).
							FontFeatures("tnum").TextColor(success)
					}
					if f.Del > 0 {
						ui.Text(c, "−"+strconv.Itoa(f.Del)).SingleLine().FontSize(11).
							FontFeatures("tnum").TextColor(approvalDangerText)
					}
				}
			})
		}
		if ap.analysis.Detail.More > 0 {
			ui.Text(c, l.LiveApprovalMore(ap.analysis.Detail.More)).SingleLine().
				FontSize(11).TextColor(ink3)
		}
	})
}

// approvalTail is the buttons, the deny input or the result line.
func (a *shelf) approvalTail(c *ui.Context, shellW float32, ap *approval, now time.Time) {
	l := loc(c)
	f := a.approvalFeature()
	switch ap.state {
	case approvalAllowed, approvalDenied, approvalExpired, approvalTerminal:
		a.approvalResult(c, ap)
		return
	}
	if ap.state == approvalDenying {
		column := ui.Column(c.Key("approval-deny")).FillWidth().Gap(approvalGap)
		column.Children(func() {
			input := ui.TextInput(c, &ap.reason).FillWidth().Height(approvalBtnH).
				FontSize(13).Padding(0, 10).Radius(approvalBtnR).
				Background(fill1).Placeholder(l.LiveApprovalReason()).Focus()
			_ = input
			row := ui.Row(c.Key("approval-deny-row")).FillWidth().Gap(8).Justify(ui.End)
			row.Children(func() {
				if approvalTextButton(c, "approval-cancel", l.LiveApprovalCancel(), ink2).Clicked() {
					ap.state = approvalShown
					a.leaveFocusMode()
				}
				if approvalFillButton(c, "approval-deny-ok", l.LiveApprovalDeny(), fill2, ink, 500).Clicked() {
					f.decide(ap.id, agentnotify.DecisionDeny, ap.reason)
				}
			})
		})
		return
	}
	row := ui.Row(c.Key("approval-actions")).FillWidth().Gap(8).AlignItems(ui.Center)
	row.Children(func() {
		if approvalFillButton(c, "approval-deny", l.LiveApprovalDeny(), fill2, ink, 500).Clicked() {
			ap.state = approvalDenying
			// Only the reason field takes the keyboard; the card itself
			// never does. The hand-back happens on cancel or on the answer.
			a.takeKeyboard()
			if a.win != nil {
				a.win.Invalidate()
			}
		}
		term := ui.Box(c.Key("approval-terminal")).Height(approvalBtnH).Radius(approvalBtnR).
			Padding(0, 8).Center()
		term.Children(func() {
			ui.Text(c, l.LiveApprovalTerminal()).FontSize(13).FontWeight(500).TextColor(ink2)
		})
		if term.Clicked() {
			f.decide(ap.id, agentnotify.DecisionPass, "")
		}
		ui.Box(c).Grow(1)
		a.approvalAllow(c, ap, now)
	})
}

// approvalAllow draws the one button whose meaning depends on risk: a plain
// click, a 600 ms hold, or the keyboard's second press.
func (a *shelf) approvalAllow(c *ui.Context, ap *approval, now time.Time) {
	l := loc(c)
	f := a.approvalFeature()
	risky := len(ap.analysis.Risks) > 0
	label := l.LiveApprovalAllow()
	base := accent
	fg := ui.RGB(255, 255, 255)
	if risky {
		label = l.LiveApprovalHold()
		base = warning
		fg = approvalHoldInk
	}
	if ap.state == approvalArmed {
		label = l.LiveApprovalAgain()
	}
	progress := ap.holdProgress(now)
	if ap.state == approvalHolding {
		base = approvalHoldBase
	}
	btn := ui.Box(c.Key("approval-allow")).Height(approvalBtnH).MinWidth(92).Radius(approvalBtnR).
		Padding(0, 12).Center().Label(label)
	btn.Draw(func(p *ui.Painter, r ui.Rect) {
		p.FillPath(liveRoundPath(r.X, r.Y, r.W, r.H, approvalBtnR), base)
		if progress > 0 {
			p.FillPath(liveRoundPath(r.X, r.Y, r.W*progress, r.H, approvalBtnR), approvalHoldFill)
		}
	})
	btn.Children(func() {
		ui.Text(c, label).SingleLine().FontSize(13).FontWeight(600).TextColor(fg)
	})
	if risky {
		switch {
		case ap.state == approvalHolding:
			// Headless (the gallery) has no pointer to hold the button; the
			// frozen progress frame is what it draws.
			if a.win != nil && !btn.Pressed() {
				ap.holdRelease()
			} else if f.holdTick(ap, now) {
				return
			} else {
				c.AnimationFrame()
			}
		default:
			if btn.Pressed() {
				ap.state = approvalHolding
				ap.holdFrom = now
				if a.win != nil {
					a.win.Invalidate()
				}
			}
		}
		return
	}
	if btn.Clicked() {
		f.decide(ap.id, agentnotify.DecisionAllow, "")
	}
}

func (a *shelf) approvalResult(c *ui.Context, ap *approval) {
	l := loc(c)
	label := l.LiveApprovalAllowed()
	color := success
	var icon *ui.SVG
	switch ap.state {
	case approvalDenied:
		label, color, icon = l.LiveApprovalDenied(), approvalDangerText, approvalCrossSym
	case approvalExpired:
		label, color, icon = l.LiveApprovalExpired(), ink2, approvalClockSym
	case approvalTerminal:
		label, color, icon = l.LiveApprovalTerminalDone(), ink2, approvalReturnSym
	default:
		icon = approvalCheckSym
	}
	row := ui.Row(c.Key("approval-result")).FillWidth().Height(approvalBtnH).
		Justify(ui.Center).AlignItems(ui.Center).Gap(6).Label(label)
	row.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(color)
		ui.Text(c, label).SingleLine().FontSize(13).FontWeight(600).TextColor(color)
	})
}

// approvalFillButton is a 32 pt button with a background.
func approvalFillButton(c *ui.Context, key, text string, bg, fg ui.Color, weight int) ui.Element {
	btn := ui.Box(c.Key(key)).Height(approvalBtnH).Radius(approvalBtnR).Padding(0, 12).
		Center().Background(bg).Label(text)
	btn.Children(func() {
		ui.Text(c, text).SingleLine().FontSize(13).FontWeight(weight).TextColor(fg)
	})
	return btn
}

// approvalTextButton is the quiet action: no fill until hovered.
func approvalTextButton(c *ui.Context, key, text string, fg ui.Color) ui.Element {
	btn := ui.Box(c.Key(key)).Height(approvalBtnH).Radius(approvalBtnR).Padding(0, 8).
		Center().Label(text)
	btn.Children(func() {
		ui.Text(c, text).SingleLine().FontSize(13).FontWeight(500).TextColor(fg)
	})
	return btn
}

// approvalDetailLabel is what assistive technology reads for the raw block.
func approvalDetailLabel(ap *approval) string {
	switch ap.analysis.Detail.Kind {
	case agentnotify.DetailFiles:
		return strings.Join(fileNames(ap.analysis.Detail.Files), ", ")
	case agentnotify.DetailURL:
		return ap.analysis.Detail.URL
	default:
		return ap.analysis.Detail.Command
	}
}

func fileNames(files []agentnotify.FileChange) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.Base(f.Path)
	}
	return out
}
