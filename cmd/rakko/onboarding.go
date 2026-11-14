package main

// The first-launch guide: a page the panel shows instead of the search bar
// on a fresh install, walking the user through where Rakko lives, moving it
// to /Applications when it was launched from a download or a disk image,
// and the three system permissions. The permissions are the last step: the
// app is ready to use once they are offered. It is drawn with the panel's
// own surfaces — the band and the action row — because the guide's only job
// is teaching what the real shelf looks like.
//
// Lifecycle: a launch that found no settings.json owes the guide (main.go),
// which is written as onboarding.pending. Pending shows the guide, hides the
// page capsule, mutes the notch's transient activities, and keeps the desk
// pet off the desktop; it clears on finish or skip, never on a plain close —
// closing the panel mid-guide just pauses, and the next opening returns to
// the same step. A move or a permission grant that needs a restart persists
// the step to resume at, so the relaunched app opens on the permissions.
import (
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/locales"
)

// pageOnboarding is the guide's page id. The page is navless: it has no
// seat in the band or the capsule, and the capsule itself stays hidden
// while the guide runs.
const pageOnboarding pageID = "onboarding"

// The guide's steps by name: the persisted resume point (settings
// onboarding.step) and the layout both key off these, so a later version
// can reorder or drop a step without stranding a saved position.
const (
	onbStepIntro = "intro"
	onbStepMove  = "move"
	onbStepPerms = "perms"
)

// runOrigin is where the running bundle lives, which decides whether the
// guide shows the move step and how its copy reads. The translocated path
// Gatekeeper runs a quarantined app from resolves back to the bundle the
// user opened.
type runOrigin int

const (
	originInstalled    runOrigin = iota // already in an Applications folder (or anywhere settled)
	originDownloads                     // in ~/Downloads
	originDiskImage                     // on a read-only /Volumes mount
	originTranslocated                  // App Translocation after a direct DMG launch
)

// onboardingFeature is the guide: its page state, the run-location answer
// and the pending move.
type onboardingFeature struct {
	host       *shelf
	origin     runOrigin
	step       int       // index into steps(), not a name
	moved      bool      // the bundle sits at its destination
	moving     bool      // a move is running in the background
	moveErr    error     // the move's last failure, shown on the row
	relaunchAt time.Time // a successful move quits to reopen at this moment
}

// newOnboarding builds the guide for this launch: the run location is
// probed once, the step resumes where settings left it, and a saved step
// the current run no longer offers (a saved "move" on an already-moved
// bundle, or a name a later build dropped) folds to the last step the
// guide has.
func newOnboarding(host *shelf) *onboardingFeature {
	o := &onboardingFeature{host: host, origin: onboardingOrigin()}
	steps := o.steps()
	want := host.settings.Onboarding.Step
	o.step = 0
	if want == "" {
		return o
	}
	for i, name := range steps {
		if name == want {
			o.step = i
			return o
		}
	}
	// The saved step is gone: resume at the step it left for, or at the
	// last one when the name itself no longer exists.
	o.step = len(steps) - 1
	for i, name := range steps {
		if stepAfter(want) == name {
			o.step = i
			break
		}
	}
	return o
}

// stepAfter names the step a saved name leads to, for resuming a step the
// current run no longer offers.
func stepAfter(name string) string {
	switch name {
	case onbStepIntro:
		return onbStepMove
	case onbStepMove:
		return onbStepPerms
	default:
		return ""
	}
}

// steps is the guide's own order: the move step only exists for a bundle
// that should not stay where it runs, and the permissions are last, on the
// platforms that have them.
func (o *onboardingFeature) steps() []string {
	steps := []string{onbStepIntro}
	if o.origin != originInstalled {
		steps = append(steps, onbStepMove)
	}
	if permissionsSupported {
		steps = append(steps, onbStepPerms)
	}
	return steps
}

func (o *onboardingFeature) current() string { return o.steps()[o.step] }
func (o *onboardingFeature) last() bool      { return o.step == len(o.steps())-1 }

// goTo switches the step and persists it, so a restart resumes here. The
// guide's pages switch through the same element fade the panel gives every
// page; the steps inside animate their own swap.
func (o *onboardingFeature) goTo(step int) {
	steps := o.steps()
	o.step = min(max(step, 0), len(steps)-1)
	o.host.saveOnboarding(steps[o.step])
	if a := o.host; a.win != nil {
		a.win.Invalidate()
	}
}

func (o *onboardingFeature) next() {
	if o.last() {
		o.host.onboardingFinish()
		return
	}
	o.goTo(o.step + 1)
}

func (o *onboardingFeature) back() {
	o.goTo(o.step - 1)
}

// saveOnboarding persists the guide's position without touching any other
// preference: the file the launch already wrote takes the step, and a
// broken file is left alone like every other write path.
func (a *shelf) saveOnboarding(step string) {
	if a.settings.Onboarding.Step == step {
		return
	}
	next := a.settings
	next.Onboarding.Step = step
	a.applySettings(next)
}

// onboardingOwed is the first-launch rule: the guide is owed when this
// launch found no settings.json at all (a fresh install), or when the file
// it read still says so — a move or a permission grant relaunches the app
// mid-guide, and that launch must keep it. A file that could not be read
// never claims either: it is never rewritten behind the user's back.
func onboardingOwed(fresh bool, loadErr error, saved bool) bool {
	if loadErr != nil {
		return false
	}
	return fresh || saved
}

// onboardingPending reports whether the guide is owed: set by a fresh
// install's first launch, cleared by finish or skip. Everything that
// quiets down for the guide — the capsule, live activities, the pet — and
// every path that routes to it reads this one bit.
func (a *shelf) onboardingPending() bool {
	return a.settings.Onboarding.Pending
}

// onboardingDone clears the pending flag: the guide never comes back for
// this install. The step clears with it.
func (a *shelf) onboardingDone() {
	if !a.onboardingPending() {
		return
	}
	next := a.settings
	next.Onboarding.Pending = false
	next.Onboarding.Step = ""
	a.applySettings(next)
	a.syncOnboardingKeys()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// onboardingFinish is the last step's 开始使用: the panel folds to the
// notch with the ready capsule on it, and the guide retires.
func (a *shelf) onboardingFinish() {
	l := a.appLoc()
	a.toastStatus(l.OnboardingReady(), time.Now().Add(3*time.Second), statusOK)
	a.onboardingDone()
	a.hide(hideExplicit)
	// The pet was held back while the guide was owed; its arrival belongs
	// to the first quiet moment after it.
	a.petStart()
}

// onboardingSkip is 跳过 and Esc: the same fold to the notch with the
// neutral hint on it.
func (a *shelf) onboardingSkip() {
	l := a.appLoc()
	a.toastStatus(osText(l.OnboardingSkippedHint(), l.OnboardingSkippedHintWin()), time.Now().Add(3*time.Second), statusInfo)
	a.onboardingDone()
	a.hide(hideExplicit)
	a.petStart()
}

// --- page ------------------------------------------------------------------

func (o *onboardingFeature) id() pageID    { return pageOnboarding }
func (o *onboardingFeature) label() string { return o.host.appLoc().OnboardingPageTitle() }
func (o *onboardingFeature) labelIn(l locales.Messages) string {
	return l.OnboardingPageTitle()
}
func (o *onboardingFeature) title() string { return o.host.appLoc().OnboardingPageTitle() }
func (o *onboardingFeature) titleIn(l locales.Messages) string {
	return l.OnboardingPageTitle()
}
func (o *onboardingFeature) enabled() bool   { return o.host.onboardingPending() }
func (o *onboardingFeature) navless() bool   { return true }
func (o *onboardingFeature) fullBleed() bool { return true }
func (o *onboardingFeature) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, trayNavSymbol)
}
func (o *onboardingFeature) actions(c *ui.Context) []ui.Element { return nil }

// body draws the whole page area the sheet gives a full-bleed page: the
// 22pt action row (progress dots and 跳过), then the 144pt step over a 30pt
// button row, all inside the 416-pt content box.
func (o *onboardingFeature) body(c *ui.Context, _ bool, _ bool) ui.Element {
	l := loc(c)
	ui.Column(c.Key("onb")).FillWidth().Grow(1).Children(func() {
		o.actionRow(c, l)
		ui.Box(c.Key("onb-gap")).Height(pageActionsGap)
		ui.Column(c.Key("onb-step")).FillWidth().Height(onbStepH).Clip().Children(func() {
			switch o.current() {
			case onbStepIntro:
				o.stepIntro(c, l)
			case onbStepMove:
				o.stepMove(c, l)
			case onbStepPerms:
				o.stepPerms(c, l)
			}
		})
		o.buttonRow(c, l)
	})
	return ui.Box(c).Size(0, 0)
}

// onbStepH is the step area: the 174pt content box keeps 30pt for the
// button row.
const onbStepH = 144

// actionRow is the page's own 22pt row: the step dots on the left, 跳过 on
// the right.
func (o *onboardingFeature) actionRow(c *ui.Context, l locales.Messages) {
	a := o.host
	row := ui.Row(c.Key("onb-acts")).Height(pageActionH).FillWidth().AlignItems(ui.Center)
	row.Children(func() {
		ui.Row(c.Key("onb-dots")).Gap(4).AlignItems(ui.Center).Children(func() {
			for i := range o.steps() {
				dot := ui.Box(c.Key(i)).Height(5).Radius(3)
				switch {
				case i < o.step:
					dot.Width(5).Background(progress.Alpha(0.45))
				case i == o.step:
					dot.Width(15).Background(progress.Alpha(0.82))
				default:
					dot.Width(5).Background(progress.Alpha(0.22))
				}
			}
		})
		ui.Box(c.Key("onb-acts-mid")).Grow(1)
		skip := ui.ButtonBase(c.Key("onb-skip")).Height(20).Padding(0, 6, 0, 6).Radius(10).
			Label(l.OnboardingSkip()).Tooltip(l.OnboardingSkip()).Center()
		if skip.Hovered() {
			skip.Background(fill1)
		}
		skip.Children(func() {
			ui.Text(c, l.OnboardingSkip()).FontSize(10.5).FontWeight(600).TextColor(ink2)
		})
		if skip.Clicked() {
			a.onboardingSkip()
		}
	})
}

// stepIntro is 住在刘海里: the neutral otter, the title, the keycaps of
// the toggle shortcut and the one-line note.
func (o *onboardingFeature) stepIntro(c *ui.Context, l locales.Messages) {
	ui.Row(c.Key("onb-intro")).FillWidth().Grow(1).AlignItems(ui.Center).Gap(16).Children(func() {
		if otter := otterImage(otterNeutral); otter != nil {
			ui.Column(c.Key("onb-otter")).Width(84).Height(112).Children(func() {
				ui.Box(c).Grow(1)
				ui.Image(c.Key("img"), otter).Width(84).Height(84).Fit(ui.Contain)
			})
		}
		ui.Column(c.Key("onb-tx")).Gap(6).Children(func() {
			panelLabel(c, "onb-intro-title", osText(l.OnboardingIntroTitle(), l.OnboardingIntroTitleWin()), 15, 650).TextColor(ink)
			panelLabel(c, nil, osText(l.OnboardingIntroLead(), l.OnboardingIntroLeadWin()), 13, 0).TextColor(ink2)
			ui.Row(c.Key("onb-keys")).Gap(4).Children(func() {
				for i, k := range o.shortcutCaps() {
					ui.Box(c.Key(i)).Height(24).MinWidth(24).Padding(0, 6, 0, 6).Radius(6).
						Background(fill2).Border(0.5, ui.RGBA(255, 255, 255, 0.14)).Center().Children(func() {
						ui.Text(c, k).FontSize(12.5).TextColor(ink)
					})
				}
			})
			panelLabel(c, nil, osText(l.OnboardingIntroNote(), l.OnboardingIntroNoteWin()), 11, 0).TextColor(ink3)
		})
	})
}

// shortcutCaps splits the panel's own toggle shortcut into the glyphs the
// keycaps show, so a remapped shortcut is never taught wrong.
func (o *onboardingFeature) shortcutCaps() []string {
	if t, ok := shortcutTokens(o.host.settings.Shortcuts.Toggle); ok {
		return t
	}
	if t, ok := shortcutTokens(settings.DefaultShortcutToggle); ok {
		return t
	}
	if forWindows {
		return []string{"Ctrl", "Alt", "Space"}
	}
	return []string{"⌃", "⌥", "Space"}
}

// stepMove is the conditional step: the path row from where the bundle
// runs to /Applications, the reason line under it, and the move itself.
func (o *onboardingFeature) stepMove(c *ui.Context, l locales.Messages) {
	a := o.host
	ui.Column(c.Key("onb-move")).FillWidth().Grow(1).Children(func() {
		panelLabel(c, "onb-move-title", l.OnboardingMoveTitle(), 15, 650).TextColor(ink)
		row := ui.Row(c.Key("onb-path")).FillWidth().Height(52).Radius(11).Margin(8, 0, 0, 0).
			Padding(0, 9, 0, 9).Gap(9).AlignItems(ui.Center)
		if o.moved {
			row.Background(success.Alpha(0.12))
		} else {
			row.Background(fill1)
		}
		if !o.moved && row.Hovered() {
			row.Background(fill2)
		}
		row.Children(func() {
			if icon := bundleIcon(); icon != nil {
				ui.Image(c.Key("appic"), icon).Size(32, 32).Radius(8).Fit(ui.Contain)
			}
			ui.Column(c.Key("onb-from")).Gap(1).Children(func() {
				panelLabel(c, nil, o.fromLabel(l), 10, 0).TextColor(ink3)
				panelLabel(c, nil, newBundleName, 12, 600).TextColor(ink)
			})
			ui.Box(c.Key("onb-arrow-gap")).Grow(1)
			if !o.moved {
				// The design puts the folder on its own 30pt tile, the
				// same size as the app icon on the left.
				ui.Icon(c, arrowRightSymbol).Size(13, 13).TextColor(ink3)
				ui.Row(c.Key("onb-dest")).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Row(c.Key("onb-dest-tile")).Size(30, 30).Radius(9).Background(fill2).Center().Children(func() {
						ui.Icon(c, folderSymbol).Size(15, 15).TextColor(ink2)
					})
					panelLabel(c, nil, l.OnboardingMoveDest(), 12, 600).TextColor(ink)
				})
			} else {
				ui.Row(c.Key("onb-ok")).Size(17, 17).Radius(9).Background(success).Center().Children(func() {
					ui.Icon(c, checkSymbol).Size(10, 10).TextColor(knockout)
				})
			}
		})
		if row.Clicked() && !o.moved && !o.moving {
			a.onboardingMove()
		}
		ui.Box(c.Key("onb-move-sub")).Margin(8, 0, 0, 0).Children(func() {
			switch {
			case o.moveErr != nil:
				panelLabel(c, "onb-move-err", l.OnboardingMoveFailed(o.moveErr.Error()), 11, 0).TextColor(danger)
			case o.moving:
				panelLabel(c, nil, l.OnboardingMoving(), 11, 0).TextColor(ink3)
			case o.moved:
				panelLabel(c, nil, l.OnboardingReopening(), 11, 0).TextColor(ink3)
			default:
				panelLabel(c, "onb-move-why", o.whyLabel(l), 11, 0).TextColor(ink3)
			}
		})
	})
}

// fromLabel is where the path row says the bundle runs now: 下载 inside
// ~/Downloads, 磁盘映像 on a mounted image or under translocation.
func (o *onboardingFeature) fromLabel(l locales.Messages) string {
	if o.origin == originDownloads {
		return l.OnboardingMoveFromDownloads()
	}
	return l.OnboardingMoveFromDiskImage()
}

// whyLabel is the reason the step gives for moving: downloads stall
// updates, a mounted image cannot keep the app at all.
func (o *onboardingFeature) whyLabel(l locales.Messages) string {
	switch o.origin {
	case originDownloads:
		return l.OnboardingMoveWhyDownloads()
	case originTranslocated:
		return l.OnboardingMoveWhyTranslocated()
	default:
		return l.OnboardingMoveWhyDiskImage()
	}
}

// stepPerms lists the three system permissions with their live grant
// state: a row click asks for that one permission through the same flow
// the Settings window's switch drives — the request is the system's, the
// state is the probe's, never a local toggle.
func (o *onboardingFeature) stepPerms(c *ui.Context, l locales.Messages) {
	a := o.host
	ui.Column(c.Key("onb-perms")).FillWidth().Grow(1).Children(func() {
		panelLabel(c, "onb-perms-title", l.OnboardingPermsTitle(), 15, 650).TextColor(ink)
		panelLabel(c, nil, l.OnboardingPermsNote(), 11, 0).TextColor(ink3).Margin(2, 0, 6, 0)
		perms := systemPermissionSet(l)
		for i, p := range perms {
			p := p
			on := p.enabled()
			ui.Column(c.Key("onb-perm-" + p.id)).FillWidth().Children(func() {
				row := ui.Row(c.Key("row")).FillWidth().Height(29).Gap(8).AlignItems(ui.Center).
					Label(p.short + " · " + p.gloss).Tooltip(p.detail)
				if row.Hovered() {
					row.Background(fill1)
				}
				row.Children(func() {
					dot := ui.Box(c.Key("dot")).Size(6, 6).Radius(3)
					if on {
						dot.Background(success)
					} else {
						dot.Background(warning)
					}
					panelLabel(c, nil, p.short, 13, 500).TextColor(ink)
					panelLabel(c, nil, p.gloss, 11, 0).TextColor(ink3)
					ui.Box(c.Key("grow")).Grow(1)
					sw := ui.Row(c.Key("sw")).Width(28).Height(17).Radius(9).AlignItems(ui.Center)
					if on {
						sw.Background(accent).Justify(ui.End).Padding(0, 2, 0, 0)
					} else {
						sw.Background(ui.RGBA(255, 255, 255, 0.18)).Justify(ui.Start).Padding(0, 0, 0, 2)
					}
					sw.Children(func() {
						ui.Box(c.Key("knob")).Size(13, 13).Radius(7).Background(ink)
					})
				})
				if row.Clicked() && !on && a.perm.stage == permissionIdle {
					a.startPermissionWalk([]string{p.id})
				}
				if i < len(perms)-1 {
					ui.Box(c.Key("sep")).Height(0.5).FillWidth().Background(ui.RGBA(255, 255, 255, 0.06))
				}
			})
		}
	})
}

// buttonRow is the 30pt row under the step area: 上一步 on the left past
// the first step, the primary action on the right — 继续 / 移到「应用程序」
// / 开始使用.
func (o *onboardingFeature) buttonRow(c *ui.Context, l locales.Messages) {
	a := o.host
	ui.Row(c.Key("onb-btns")).FillWidth().Height(30).AlignItems(ui.Center).Justify(ui.End).Gap(4).Children(func() {
		ui.Box(c.Key("onb-btns-grow")).Grow(1)
		if o.step > 0 {
			back := ui.ButtonBase(c.Key("onb-back")).Height(28).Padding(0, 10, 0, 10).Radius(14).
				Label(l.OnboardingBack()).Tooltip(l.OnboardingBack()).Center()
			if back.Hovered() {
				back.Background(fill1)
			}
			back.Children(func() {
				ui.Text(c, l.OnboardingBack()).FontSize(12.5).TextColor(ink2)
			})
			if back.Clicked() {
				o.back()
			}
		}
		var label string
		switch {
		case a.perm.stage == permissionRelaunch:
			label = l.PermRelaunchButton()
		case o.last():
			label = l.OnboardingStart()
		case o.current() == onbStepMove && !o.moved:
			if o.moveErr != nil {
				label = l.OnboardingMoveRetry()
			} else if o.moving {
				label = l.OnboardingMoving()
			} else {
				label = l.OnboardingMoveButton()
			}
		default:
			label = l.OnboardingContinue()
		}
		primary := ui.ButtonBase(c.Key("onb-next")).Height(28).Padding(0, 14, 0, 14).Radius(14).
			Label(label).Tooltip(label).Center()
		if o.moving {
			primary.Disabled(true)
		}
		if primary.Hovered() {
			primary.Background(ui.Hex("#2B95FF"))
		} else {
			primary.Background(accent)
		}
		primary.Children(func() {
			ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(ink)
		})
		if primary.Clicked() && !o.moving {
			switch {
			case a.perm.stage == permissionRelaunch:
				a.relaunchShelf()
			case o.last():
				a.onboardingFinish()
			case o.current() == onbStepMove && !o.moved:
				a.onboardingMove()
			default:
				o.next()
			}
		}
	})
}

// --- keyboard --------------------------------------------------------------

// onbKeys are the global keys the guide holds while its page is on screen:
// → and ⏎ advance, ← goes back. Esc is the panel's own hold (a.escape
// routes it to skip); Space stays Quick Look's.
var onbKeys = []string{"Left", "Right", "Enter"}

// syncOnboardingKeys registers or releases the guide's keys: they are
// held only while the guide's page is the one drawn, so arrows and Enter
// never reach the guide from another page or from a closed panel.
func (a *shelf) syncOnboardingKeys() {
	want := a.onboardingPending() && a.visible && a.currentLevel() == 2 && a.tab == pageOnboarding
	for _, k := range onbKeys {
		held := a.onbKeysHeld[k]
		switch {
		case want && !held:
			key := k
			if err := registerKey(key, func() { a.onboardingKey(key) }); err == nil {
				if a.onbKeysHeld == nil {
					a.onbKeysHeld = map[string]bool{}
				}
				a.onbKeysHeld[key] = true
			}
		case !want && held:
			unregisterKey(k)
			delete(a.onbKeysHeld, k)
		}
	}
}

// onboardingKey routes the guide's held keys.
func (a *shelf) onboardingKey(key string) {
	if a.onb == nil || !a.onboardingPending() {
		return
	}
	switch key {
	case "Left":
		a.onb.back()
	case "Right", "Enter":
		a.onb.primary()
	}
}

// primary is what the primary button does, for → and ⏎: the move on the
// step that offers it, 开始使用 on the last step, otherwise the next step.
func (o *onboardingFeature) primary() {
	switch {
	case o.last():
		o.host.onboardingFinish()
	case o.current() == onbStepMove && !o.moved:
		o.host.onboardingMove()
	default:
		o.next()
	}
}

// Symbols the guide borrows or adds.
var (
	arrowRightSymbol = symbol(`<path d="M4 12h15M14 7l5 5-5 5"/>`)
	folderSymbol     = symbol(`<path d="M3 6.5h6l2 2.5h10V18a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18z"/>`)
)
