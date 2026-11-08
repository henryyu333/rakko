//go:build darwin

package main

// The first-launch guide: a page the panel shows instead of the search bar
// on a fresh install, walking the user through where Rakko lives, moving it
// to /Applications when it was launched from a download or a disk image,
// the three system permissions, and one file dropped into the tray. It is
// drawn with the panel's own surfaces — the band, the action row and the
// real drop zone — because the guide's only job is teaching what the real
// shelf looks like.
//
// Lifecycle: a launch that found no settings.json owes the guide (main.go),
// which is written as onboarding.pending. Pending shows the guide, hides the
// page capsule, mutes the notch's transient activities, and keeps the desk
// pet off the desktop; it clears on finish or skip, never on a plain close —
// closing the panel mid-guide just pauses, and the next opening returns to
// the same step. A move or a permission grant that needs a restart persists
// the step to resume at, so the relaunched app opens on the permissions.
import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
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
	onbStepTry   = "try"
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
	tried      bool      // the tray already got a file (demo or real)
	sample     string    // the generated sample file's path, once written
	sampleErr  error

	// The demo chip's drag: the offset it is dragged by while the pointer
	// holds it, and the scripted run's clock. demoOn mirrors a real file
	// drag's flag for dropZoneSnap.
	chipOff  ui.Point
	chipDrag bool
	demoAt   time.Time
	demoOn   bool
}

// newOnboarding builds the guide for this launch: the run location is
// probed once, the step resumes where settings left it, and a step the
// current location no longer offers (a saved "move" on an already-moved
// bundle) folds to the step after it.
func newOnboarding(host *shelf) *onboardingFeature {
	o := &onboardingFeature{host: host, origin: onboardingOrigin()}
	steps := o.steps()
	want := host.settings.Onboarding.Step
	o.step = 0
	for i, name := range steps {
		if name == want {
			o.step = i
			break
		}
	}
	if want != "" && steps[o.step] != want {
		// The saved step is gone (the bundle moved since): resume at the
		// step it left for.
		o.step = len(steps) - 1
		for i, name := range steps {
			if name == want || stepAfter(want) == name {
				o.step = i
				break
			}
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
	case onbStepPerms:
		return onbStepTry
	default:
		return ""
	}
}

// steps is the guide's own order: the move step only exists for a bundle
// that should not stay where it runs.
func (o *onboardingFeature) steps() []string {
	if o.origin == originInstalled {
		return []string{onbStepIntro, onbStepPerms, onbStepTry}
	}
	return []string{onbStepIntro, onbStepMove, onbStepPerms, onbStepTry}
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

// onboardingOwed is the first-launch rule: the guide is owed only when the
// launch found no settings.json at all (fresh) and the file it made — or
// failed to read — is readable. A broken file is never rewritten behind the
// user's back, so nothing may claim the install is new.
func onboardingOwed(fresh bool, loadErr error) bool {
	return fresh && loadErr == nil
}

// onboardingPending reports whether the guide is owed: set by a fresh
// install's first launch, cleared by finish or skip. Everything that
// quiets down for the guide — the capsule, live activities, the pet — and
// every path that routes to it reads this one bit.
func (a *shelf) onboardingPending() bool {
	return a.settings.Onboarding.Pending
}

// onboardingTried reports whether the try step already landed a file: the
// guide's last state, where the page capsule comes back.
func (a *shelf) onboardingTried() bool {
	return a.onb != nil && a.onb.tried
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
	a.toastStatus(l.OnboardingSkippedHint(), time.Now().Add(3*time.Second), statusInfo)
	a.onboardingDone()
	a.hide(hideExplicit)
	a.petStart()
}

// onboardingCatch marks the try step done by whatever landed in the tray —
// the demo file or a real drag — while the guide's page is the one that
// accepted it. It plays the real catch: the zone keeps the otter's hug for
func (a *shelf) onboardingCatch() {
	if a.onb == nil || a.onb.tried {
		return
	}
	a.onb.tried = true
	a.onb.demoOn = false
	a.onb.chipDrag = false
	a.drop.done = true
	a.catchDrop(-1, -1)
	// The capsule under the panel returns for the finish block, the same
	// moment the design brings it back. Pending stays until 开始使用:
	// the guide is still owed, only its last step answered.
	if a.win != nil {
		a.win.Invalidate()
	}
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
func (o *onboardingFeature) body(c *ui.Context, over bool, live bool) ui.Element {
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
			case onbStepTry:
				o.stepTry(c, l, live)
			}
		})
		o.buttonRow(c, l)
	})
	// A scripted demo run is clocked by the view: while it is armed, keep
	// frames coming and let its phases land on time.
	if live {
		o.demoTick(c)
	}
	return ui.Box(c).Size(0, 0)
}

// onbStepH is the step area: the 174pt content box keeps 30pt for the
// button row.
const onbStepH = 144

// actionRow is the page's own 22pt row: the step dots on the left, 跳过 on
// the right. A finished guide keeps the dots and drops the skip — the
// only way out is 开始使用.
func (o *onboardingFeature) actionRow(c *ui.Context, l locales.Messages) {
	a := o.host
	row := ui.Row(c.Key("onb-acts")).Height(pageActionH).FillWidth().AlignItems(ui.Center)
	row.Children(func() {
		ui.Row(c.Key("onb-dots")).Gap(4).AlignItems(ui.Center).Children(func() {
			for i := range o.steps() {
				dot := ui.Box(c.Key(i)).Height(5).Radius(3)
				switch {
				case i < o.step || (o.last() && o.tried):
					dot.Width(5).Background(progress.Alpha(0.45))
				case i == o.step:
					dot.Width(15).Background(progress.Alpha(0.82))
				default:
					dot.Width(5).Background(progress.Alpha(0.22))
				}
			}
		})
		ui.Box(c.Key("onb-acts-mid")).Grow(1)
		if !(o.last() && o.tried) {
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
			panelLabel(c, "onb-intro-title", l.OnboardingIntroTitle(), 15, 650).TextColor(ink)
			panelLabel(c, nil, l.OnboardingIntroLead(), 13, 0).TextColor(ink2)
			ui.Row(c.Key("onb-keys")).Gap(4).Children(func() {
				for i, k := range o.shortcutCaps() {
					ui.Box(c.Key(i)).Height(24).MinWidth(24).Padding(0, 6, 0, 6).Radius(6).
						Background(fill2).Border(0.5, ui.RGBA(255, 255, 255, 0.14)).Center().Children(func() {
						ui.Text(c, k).FontSize(12.5).TextColor(ink)
					})
				}
			})
			panelLabel(c, nil, l.OnboardingIntroNote(), 11, 0).TextColor(ink3)
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
			if icon := appIconBitmap(); icon != nil {
				ui.Image(c.Key("appic"), icon).Size(32, 32).Radius(8).Fit(ui.Contain)
			}
			ui.Column(c.Key("onb-from")).Gap(1).Children(func() {
				panelLabel(c, nil, o.fromLabel(l), 10, 0).TextColor(ink3)
				panelLabel(c, nil, newBundleName, 12, 600).TextColor(ink)
			})
			ui.Box(c.Key("onb-arrow-gap")).Grow(1)
			if !o.moved {
				ui.Icon(c, arrowRightSymbol).Size(13, 13).TextColor(ink3)
				ui.Icon(c, folderSymbol).Size(30, 30).TextColor(ink3)
				panelLabel(c, nil, l.OnboardingMoveDest(), 12, 600).TextColor(ink)
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

// stepTry is 试一试: the sample file's chip, which the pointer can drag
// across the panel — past the point a real drag would cross, the real drop
// zone takes over and the release lands the file — the autoplay button
// beside it, and the finish block once the tray has the file.
func (o *onboardingFeature) stepTry(c *ui.Context, l locales.Messages, live bool) {
	if o.tried {
		ui.Row(c.Key("onb-done")).FillWidth().Grow(1).AlignItems(ui.Center).Gap(16).Children(func() {
			if otter := otterImage(otterHappy); otter != nil {
				ui.Column(c.Key("onb-otter")).Width(84).Height(112).Children(func() {
					ui.Box(c).Grow(1)
					ui.Image(c.Key("img"), otter).Width(84).Height(84).Fit(ui.Contain)
				})
			}
			ui.Column(c.Key("onb-tx")).Gap(6).Children(func() {
				panelLabel(c, "onb-done-title", l.OnboardingDoneTitle(), 15, 650).TextColor(ink)
				panelLabel(c, "onb-done-note", l.OnboardingDoneNote(), 13, 0).TextColor(ink2)
			})
		})
		return
	}
	ui.Column(c.Key("onb-try")).FillWidth().Grow(1).Children(func() {
		panelLabel(c, "onb-try-title", l.OnboardingTryTitle(), 15, 650).TextColor(ink)
		panelLabel(c, nil, l.OnboardingTryLead(), 13, 0).TextColor(ink2).Margin(2, 0, 10, 0)
		ui.Row(c.Key("onb-chips")).Gap(8).AlignItems(ui.Center).Children(func() {
			name := o.sampleName(l)
			chip := ui.Row(c.Key("onb-chip")).Height(34).Radius(10).Padding(0, 12, 0, 10).Gap(8).
				AlignItems(ui.Center).Background(ui.RGBA(255, 255, 255, 0.08)).
				Border(0.5, ui.RGBA(255, 255, 255, 0.12)).Label(name).Tooltip(name)
			chip.Cursor(ui.CursorGrab)
			chip.Children(func() {
				ui.Icon(c, fileSymbol).Size(15, 15).TextColor(ink2)
				panelLabel(c, "onb-chip-name", name, 11.5, 0).TextColor(ink)
			})
			chip.Draggable(&o.chipOff, ui.DraggableOptions{
				Snap: ui.Nearest(ui.Point{}),
			})
			if live {
				o.trackChip(c, chip)
			}
			// A click that never became a drag plays the demo, as the
			// prototype's chip does.
			if chip.Clicked() && o.demoAt.IsZero() {
				o.demoAt = c.Now()
			}
			play := ui.ButtonBase(c.Key("onb-play")).Size(30, 30).Radius(15).Center().
				Label(l.OnboardingTryDemo()).Tooltip(l.OnboardingTryDemo())
			if play.Hovered() {
				play.Background(fill1)
			} else {
				play.Background(fill2)
			}
			play.Children(func() {
				ui.Icon(c, playSymbol).Size(11, 11).TextColor(ink2)
			})
			if play.Clicked() && o.demoAt.IsZero() {
				o.demoAt = c.Now()
			}
		})
		switch {
		case o.sampleErr != nil:
			panelLabel(c, "onb-sample-err", l.OnboardingTryFailed(o.sampleErr.Error()), 11, 0).
				TextColor(danger).Margin(8, 0, 0, 0)
		default:
			panelLabel(c, nil, l.OnboardingTryNote(), 11, 0).TextColor(ink3).Margin(8, 0, 0, 0)
		}
	})
}

// trackChip watches the chip's drag: while the pointer holds it and moves,
// the real drop zone comes up (the same frame a Finder drag would), and
// letting go lands the sample on the tray — the chip lives inside the
// panel's clip, so every place the drag can reach counts as over the zone.
func (o *onboardingFeature) trackChip(c *ui.Context, chip ui.Element) {
	a := o.host
	if chip.Dragging() {
		o.chipDrag = true
		o.demoOn = true
		c.AnimationFrame()
		return
	}
	if _, _, ended := chip.DragEnded(); ended && o.chipDrag {
		o.chipDrag = false
		o.demoOn = false
		a.onboardingDemoDrop()
		return
	}
	if o.chipDrag {
		// The press ended without a drag-end event (a cancelled press):
		// the chip returns where it started.
		o.chipDrag = false
		o.demoOn = false
		o.chipOff = ui.Point{}
	}
}

// demoTick runs the scripted drag the play button arms: the zone comes up
// as if a file hovered, then the sample lands. Reduce Motion skips the
// staged timing — the file lands on the click.
func (o *onboardingFeature) demoTick(c *ui.Context) {
	if o.demoAt.IsZero() || o.tried {
		return
	}
	elapsed := c.Now().Sub(o.demoAt)
	switch {
	case reduceMotion(c):
		o.demoAt = time.Time{}
		o.host.onboardingDemoDrop()
	case elapsed < 120*time.Millisecond:
		c.After(120*time.Millisecond - elapsed)
	case elapsed < 700*time.Millisecond:
		o.demoOn = true
		c.After(700*time.Millisecond - elapsed)
	default:
		o.demoAt = time.Time{}
		o.host.onboardingDemoDrop()
	}
}

// onboardingDemoDrop lands the sample file on the tray exactly like a
// real release: the zone's own receive path, then the catch.
func (a *shelf) onboardingDemoDrop() {
	o := a.onb
	if o == nil || o.tried {
		return
	}
	o.demoOn = false
	path, err := o.ensureSample()
	if err != nil {
		o.sampleErr = err
		a.report(err)
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	data, err := transfer.FileData(path)
	if err != nil {
		o.sampleErr = err
		a.report(err)
		return
	}
	if err := a.receive(data); err != nil {
		return
	}
	a.onboardingCatch()
}

// buttonRow is the 30pt row under the step area: 上一步 on the left only
// past the first step and never on the finish block, the primary action on
// the right — 继续 / 移到「应用程序」 / 完成 / 开始使用.
func (o *onboardingFeature) buttonRow(c *ui.Context, l locales.Messages) {
	a := o.host
	finished := o.last() && o.tried
	ui.Row(c.Key("onb-btns")).FillWidth().Height(30).AlignItems(ui.Center).Justify(ui.End).Gap(4).Children(func() {
		ui.Box(c.Key("onb-btns-grow")).Grow(1)
		if o.step > 0 && !finished {
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
		case finished:
			label = l.OnboardingStart()
		case a.perm.stage == permissionRelaunch:
			label = l.PermRelaunchButton()
		case o.current() == onbStepTry:
			label = l.OnboardingDone()
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
			case finished:
				a.onboardingFinish()
			case a.perm.stage == permissionRelaunch:
				a.relaunchShelf()
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
		a.onb.next()
	}
}

// --- run location ------------------------------------------------------------

// onboardingOriginSeams keeps the location probe and the move testable: no
// unit test touches /Applications, the Trash or the real executable.
var (
	onbExecutable = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		return exe, nil
	}
	onbHome = os.UserHomeDir
	// onbApplicationsDir is where the move lands. /Applications in the app;
	// RAKKO_ONBOARDING_APPS points it at a temporary folder for the native
	// smoke, so no test can move anything into the real Applications.
	onbApplicationsDir = func() string {
		if dir := os.Getenv("RAKKO_ONBOARDING_APPS"); dir != "" {
			return dir
		}
		return "/Applications"
	}()
	onbTrashPath = mac.TrashFile
	onbOpenPlan  = relaunchStart
	onbQuit      = relaunchQuit
)

// onboardingOrigin answers where the running bundle lives. Only the four
// cases matter: inside an Applications folder (settled, whatever the
// volume), inside ~/Downloads, on a mounted image, and the translocated
// path Gatekeeper substitutes when a quarantined app launches straight
// from a DMG.
func onboardingOrigin() runOrigin {
	exe, err := onbExecutable()
	if err != nil {
		return originInstalled
	}
	home, _ := onbHome()
	return runLocation(exe, home)
}

// runLocation is the pure answer: exe is the resolved executable path,
// home the user's home directory. Translocation resolves the staged
// bundle back to where the user put it before judging.
func runLocation(exe, home string) runOrigin {
	bundle, ok := appBundleForExecutable(exe)
	if !ok {
		return originInstalled // go run, go test: not a bundle to move
	}
	if strings.Contains(bundle, "/AppTranslocation/") {
		if real := translocatedSource(bundle); real != "" {
			bundle = real
		} else {
			return originTranslocated
		}
	}
	if strings.HasPrefix(bundle, "/Volumes/") {
		return originDiskImage
	}
	if home != "" && withinDir(bundle, filepath.Join(home, "Downloads")) {
		return originDownloads
	}
	return originInstalled
}

// withinDir reports whether path sits inside dir (not dir itself: the
// bundle in ~/Downloads, never the folder).
func withinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

// translocatedSource recovers the bundle a translocated app was staged
// from: com.apple.quarantine's staging area keeps the original under the
// same AppTranslocation uuid's sibling. When nothing resolves, the
// translocated path itself still says the app was staged.
func translocatedSource(bundle string) string {
	// /private/var/folders/…/AppTranslocation/<uuid>/d/Rakko.app: the
	// original bundle's location is what the user's own record says — the
	// path the app was opened from, which the quarantine attribute does
	// not preserve. With no reliable way back, still call it translocated.
	return ""
}

// --- the move ---------------------------------------------------------------

// onbMoveAfterFunc and onbMoveDispatch mirror the permission flow's seams:
// the move's own clock, so a test can hold the relaunch.
var (
	onbMoveAfterFunc = func(d time.Duration, fn func()) permissionTimer {
		return time.AfterFunc(d, fn)
	}
	onbMoveDispatch = mygo.RunOnMain
)

// onboardingMove runs the 移到「应用程序」 action: the bundle copies or
// moves to /Applications (a disk image copies; a local folder moves), an
// older Rakko.app at the destination goes to the Trash first, and the app
// relaunches from the new home after the row has shown the check mark.
// Everything after the user's click is best-effort: a failure stays on
// the row and 继续 still works.
func (a *shelf) onboardingMove() {
	o := a.onb
	if o == nil || o.moving || o.moved {
		return
	}
	exe, err := onbExecutable()
	if err != nil {
		o.moveErr = err
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	bundle, ok := appBundleForExecutable(exe)
	if !ok {
		o.moveErr = fmt.Errorf("not inside an app bundle")
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	o.moving = true
	o.moveErr = nil
	if a.win != nil {
		a.win.Invalidate()
	}
	move := func() (string, error) { return moveBundleToApplications(bundle, onbApplicationsDir) }
	go func() {
		dest, err := move()
		onbMoveDispatch(func() {
			o.moving = false
			if err != nil {
				o.moveErr = err
				if a.win != nil {
					a.win.Invalidate()
				}
				return
			}
			o.moved = true
			// The next step is persisted before the relaunch so the new
			// process opens on the permissions, not the intro.
			a.saveOnboarding(o.steps()[o.step+1])
			o.relaunchAt = time.Now().Add(900 * time.Millisecond)
			onbMoveAfterFunc(900*time.Millisecond, func() {
				onbMoveDispatch(func() { a.onboardingRelaunch(dest) })
			})
			if a.win != nil {
				a.win.Invalidate()
			}
		})
	}()
}

// onboardingRelaunch reopens the app from its new bundle, after the old
// process lets the single-instance lock go — the same wait-then-open the
// permission flow's relaunch plans.
func (a *shelf) onboardingRelaunch(bundle string) {
	plan := relaunchPlanForBundle(bundle, os.Args[1:], os.Getpid())
	if err := onbOpenPlan(plan); err != nil {
		a.report(err)
		return
	}
	onbQuit()
}

// relaunchPlanForBundle is relaunchPlanFor with the bundle already known:
// the moved app reopens at dest even while the old executable's path still
// points at the source directory.
func relaunchPlanForBundle(bundle string, args []string, pid int) relaunchPlan {
	var b strings.Builder
	fmt.Fprintf(&b, "while kill -0 %d 2>/dev/null; do sleep 0.1; done; sleep 0.3; ", pid)
	fmt.Fprintf(&b, "exec /usr/bin/open -n %s", shellQuote(bundle))
	if len(args) > 0 {
		b.WriteString(" --args")
	}
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(shellQuote(arg))
	}
	return relaunchPlan{Exec: "/bin/sh", Args: []string{"-c", b.String()}}
}

// moveBundleToApplications puts bundle at /Applications/Rakko.app. A
// bundle on a read-only volume copies; anywhere else it moves. An older
// Rakko.app already there goes to the Trash first — recoverable, never
// overwritten in place.
func moveBundleToApplications(bundle, destDir string) (string, error) {
	dest := filepath.Join(destDir, newBundleName)
	if bundle == dest {
		return dest, nil
	}
	if _, err := os.Stat(dest); err == nil {
		if _, err := onbTrashPath(dest); err != nil {
			return "", fmt.Errorf("existing %s could not be trashed: %w", dest, err)
		}
	}
	readOnly := strings.HasPrefix(bundle, "/Volumes/") || strings.Contains(bundle, "/AppTranslocation/")
	if readOnly {
		if err := copyBundle(bundle, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	if err := os.Rename(bundle, dest); err != nil {
		if errorsIsEXDEV(err) {
			// Downloads and /Applications on different volumes: copy, then
			// leave nothing behind by removing the source after a full copy.
			if err := copyBundle(bundle, dest); err != nil {
				return "", err
			}
			if err := os.RemoveAll(bundle); err != nil {
				log.Printf("%s: moved to %s but the old copy at %s stayed: %v", appName, dest, bundle, err)
			}
			return dest, nil
		}
		return "", err
	}
	return dest, nil
}

func errorsIsEXDEV(err error) bool {
	for e := err; e != nil; e = unwrapErr(e) {
		if e == syscall.EXDEV {
			return true
		}
	}
	return false
}

func unwrapErr(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

// copyBundle copies the app bundle wholesale (symlinks preserved) for the
// read-only sources. It fails before writing when the destination's parent
// refuses a probe file, so the error names the real cause — /Applications
// needing admin, a full disk — rather than a missing file deep inside.
func copyBundle(src, dest string) error {
	parent := filepath.Dir(dest)
	probe, err := os.CreateTemp(parent, ".rakko-move-*")
	if err != nil {
		return err
	}
	probe.Close()
	os.Remove(probe.Name())
	if err := copyTree(src, dest); err != nil {
		os.RemoveAll(dest)
		return err
	}
	// A copied bundle keeps the quarantine bit the source had: without
	// clearing it the relaunched app is translocated again — moved in
	// name only. Clearing is best-effort; a failure keeps the copy.
	if out, err := runXattr(dest); err != nil {
		log.Printf("%s: copied to %s but quarantine stayed (%v: %s)", appName, dest, err, out)
	}
	return nil
}

// runXattr is a seam for the quarantine strip, which a unit test replaces.
var runXattr = func(bundle string) (string, error) {
	out, err := exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", bundle).CombinedOutput()
	return string(out), err
}

// copyTree duplicates a directory tree, preserving modes and symlinks.
func copyTree(src, dest string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dest)
	}
	if !info.IsDir() {
		return copyOnboardingFile(src, dest, info.Mode())
	}
	if err := os.MkdirAll(dest, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyOnboardingFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// --- the sample file ----------------------------------------------------------

// sampleName is the chip's file name in the panel's language: today's
// date, like the screenshots macOS writes.
func (o *onboardingFeature) sampleName(l locales.Messages) string {
	return l.OnboardingSampleName(time.Now().Format("2006-01-02"))
}

// ensureSample writes the demo file once, into the data directory's own
// onboarding folder: a real, user-removable picture that lands on the tray
// exactly like a dragged one.
func (o *onboardingFeature) ensureSample() (string, error) {
	a := o.host
	if o.sample != "" {
		if _, err := os.Stat(o.sample); err == nil {
			return o.sample, nil
		}
	}
	name := o.sampleName(a.appLoc())
	dir := filepath.Join(a.dataDir, "onboarding")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		o.sample = path
		return path, nil
	}
	img := image.NewRGBA(image.Rect(0, 0, 640, 400))
	for y := range 400 {
		for x := range 640 {
			img.Set(x, y, color.RGBA{
				uint8(30 + 60*float64(x)/640),
				uint8(60 + 80*float64(y)/400),
				uint8(140 + 80*float64(x+y)/1040),
				255,
			})
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	o.sample = path
	return path, nil
}

// --- app icon ----------------------------------------------------------------

// appIconBitmap decodes the app's own icon for the path row, from the
// same resources file the bundle ships; a dev run without resources draws
// the file glyph instead.
var appIconBitmap = func() func() *ui.Bitmap {
	var once *ui.Bitmap
	var tried bool
	return func() *ui.Bitmap {
		if tried {
			return once
		}
		tried = true
		if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
			if data, rerr := os.ReadFile(filepath.Join(dir, "icon.png")); rerr == nil {
				once, _ = ui.DecodeBitmap(data)
			}
		}
		return once
	}
}()

// Symbols the guide borrows or adds.
var (
	arrowRightSymbol = symbol(`<path d="M4 12h15M14 7l5 5-5 5"/>`)
	folderSymbol     = symbol(`<path d="M3 6.5h6l2 2.5h10V18a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18z"/>`)
	playSymbol       = symbol(`<path d="M8 5.5l11 6.5-11 6.5z"/>`)
)
