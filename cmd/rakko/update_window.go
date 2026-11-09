//go:build darwin

package main

// The Sparkle-style update window: one titled window that shows every face
// of an update — the found version with its notes, the download, the
// ready-to-restart state, and the results of a manual check (up to date,
// failed). A manual check always opens it (starting on the short-lived
// 正在检查更新 face); an automatic check opens the found face only when the
// version was neither skipped nor deferred and UpdateAutoInstall is off.
// The window follows the system appearance: two palettes, one like the
// Settings window's dark chrome and one in system light, picked per frame
// from c.Theme().Dark so flipping the appearance repaints without a
// rebuild.
//
// Window management (open/close/keyboard handover) lives in updater.go
// with the rest of the update state; this file owns how the window looks
// and the small pure helpers around it.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"rakko/locales"
)

const (
	updateFoundW   = 520
	updateFoundH   = 420
	updateFoundMin = 420 - 224 // found without notes: no 206pt card and its 18pt gap
	updateSmallW   = 400
	updateSmallH   = 146
	updateBarH     = 28 // the self-drawn title strip under the traffic lights
	updateNotesH   = 206
)

// updateStage is which face the update window shows.
type updateStage int

const (
	updateStageChecking updateStage = iota
	updateStageFound
	updateStageDownloading
	updateStageReady
	updateStageLatest
	updateStageFailed
)

// updatePalette is one appearance's colors, assembled after the design's
// .theme-dark / .theme-light variables (design/explore/update/index.html).
type updatePalette struct {
	win, bar, barEdge     ui.Color
	ink, ink2, ink3       ui.Color
	card, cardEdge, dot   ui.Color
	ctl, ctlHover, ghost  ui.Color
	boxBg, boxEdge, track ui.Color
	accent, danger        ui.Color
	btnRadius             float32 // 7 in dark, a capsule in light
	btnPadX               float32
}

var (
	updateDark = updatePalette{
		win: setPanel, bar: ui.Hex("#17181B"), barEdge: ui.RGBA(255, 255, 255, 0.07),
		ink: ink, ink2: ink2, ink3: ink3,
		card: setCard, cardEdge: setCardEdge, dot: ui.RGBA(245, 245, 247, 0.42),
		ctl: setCtl, ctlHover: setCtlHover, ghost: fill1,
		boxBg: setCtl, boxEdge: ui.RGBA(255, 255, 255, 0.22), track: ui.RGBA(255, 255, 255, 0.12),
		accent: accent, danger: danger, btnRadius: 7, btnPadX: 14,
	}
	updateLight = updatePalette{
		win: ui.Hex("#FFFFFF"), bar: ui.Hex("#F3F3F4"), barEdge: ui.RGBA(0, 0, 0, 0.12),
		ink: ui.Hex("#1D1D1F"), ink2: ui.Hex("#55555A"), ink3: ui.Hex("#86868B"),
		card: ui.Hex("#FFFFFF"), cardEdge: ui.Hex("#D9D9DE"), dot: ui.Hex("#97979D"),
		ctl: ui.Hex("#F0F0F2"), ctlHover: ui.Hex("#E6E6E9"), ghost: ui.RGBA(0, 0, 0, 0.05),
		boxBg: ui.Hex("#FFFFFF"), boxEdge: ui.Hex("#C9C9CE"), track: ui.Hex("#E3E3E7"),
		accent: ui.Hex("#007AFF"), danger: ui.Hex("#D70015"), btnRadius: 999, btnPadX: 16,
	}
)

// updatePal picks the palette of the current appearance.
func updatePal(c *ui.Context) updatePalette {
	if c.Theme().Dark {
		return updateDark
	}
	return updateLight
}

// updateWindowTitle is the window's native title (Mission Control and
// assistive technology read it) and the small faces' title-strip text.
func (a *shelf) updateWindowTitle() string {
	return updateWindowTitleIn(a.appLoc(), a.updateWinStage)
}

// updateWindowTitleIn is updateWindowTitle's message-table form for the
// view, which reads the window's own language.
func updateWindowTitleIn(l locales.Messages, stage updateStage) string {
	switch stage {
	case updateStageDownloading, updateStageReady:
		return l.UpdateUpdatingWindowTitle()
	default:
		return l.UpdateWindowTitle()
	}
}

// updateView is the window's whole content, rebuilt every frame.
func (a *shelf) updateView(c *ui.Context) {
	l := loc(c)
	p := updatePal(c)
	if a.updateWin != nil {
		c.OnShortcut(0, ui.KeyEscape, a.updateEscape)
		c.OnShortcut(ui.Cmd, ui.KeyW, func() { a.closeUpdateWindow() })
	}
	root := ui.Column(c.Key("win")).Fill().Background(p.win)
	root.Children(func() {
		a.updateBar(c, l, p)
		if a.updateWinStage == updateStageFound {
			a.updateFoundBody(c, l, p)
		} else {
			a.updateSmallBody(c, l, p)
		}
	})
}

// updateEscape is Esc on the window: while downloading it means cancel,
// anywhere else it just closes.
func (a *shelf) updateEscape() {
	if a.updateWinStage == updateStageDownloading {
		a.cancelUpdateInstall()
		return
	}
	a.closeUpdateWindow()
}

// updateBar is the 28pt strip the AppKit traffic lights sit over (they are
// positioned at TrafficLightPosition, inside it). It drags the window;
// only the small faces carry a title, as the design draws it.
func (a *shelf) updateBar(c *ui.Context, l locales.Messages, p updatePalette) {
	bar := ui.Row(c.Key("bar")).FillWidth().Height(updateBarH).
		AlignItems(ui.Center).Padding(0, 20, 0, 20).Background(p.bar).DragWindow()
	bar.Children(func() {
		if a.updateWinStage != updateStageFound {
			// The traffic lights float over this strip (20 in, three 12pt
			// circles 8 apart); the title starts 14pt after them, as the
			// design draws it.
			ui.Box(c.Key("lights")).Width(66)
			ui.Text(c, updateWindowTitleIn(l, a.updateWinStage)).SingleLine().FontSize(13).FontWeight(600).TextColor(p.ink)
		}
	})
	ui.Box(c.Key("bar-edge")).FillWidth().Height(0.5).Background(p.barEdge)
}

var (
	appIconOnce   sync.Once
	appIconBitmap *ui.Bitmap
)

// bundleIcon is the running app's icon: the bundle's icon.png while packaged,
// the checkout's resources file under go test. The update window draws it as
// its hero image and the guide's move step as the app on the path. Loaded
// once; nil when the file is missing.
func bundleIcon() *ui.Bitmap {
	appIconOnce.Do(func() {
		dir, err := mygo.App.Path(mygo.PathResources)
		if err != nil {
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, "icon.png"))
		if err == nil {
			appIconBitmap, _ = ui.DecodeBitmap(data)
		}
	})
	return appIconBitmap
}

// updateIcon is the app icon at the size one face asks for, or nothing
// when the icon is missing (dev builds without resources draw text only).
func updateIcon(c *ui.Context, size float32) {
	if b := bundleIcon(); b != nil {
		ui.Image(c.Key("icon"), b).Size(size, size).Fit(ui.Contain)
	}
}

// updateFoundBody is the tall face: headline, one version sentence, the
// notes card, the auto-install checkbox and the three actions.
func (a *shelf) updateFoundBody(c *ui.Context, l locales.Messages, p updatePalette) {
	if a.update == nil {
		// The offer the face announced is gone (a background check found
		// nothing new while the window was open): there is no version or
		// notes left to draw.
		return
	}
	ui.Column(c.Key("found")).Fill().Padding(18, 22, 20, 22).Children(func() {
		ui.Row(c.Key("head")).Gap(16).Children(func() {
			updateIcon(c, 56)
			ui.Column(c.Key("head-txt")).Padding(1, 0, 0, 0).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, l.UpdateFoundTitle()).SingleLine().FontSize(15).FontWeight(600).TextColor(p.ink)
				ui.Text(c, l.UpdateFoundBody(a.update.Version, appVersion())).
					FontSize(13).TextColor(p.ink2).LineHeight(1.45).Padding(5, 0, 0, 0)
			})
		})
		if strings.TrimSpace(a.update.Notes) != "" {
			a.updateNotesCard(c, p)
		}
		a.updateAutoRow(c, l, p)
		a.updateFoundButtons(c, l, p)
	})
}

// updateNotesCard is the 206pt release-notes card: a scrolling Markdown
// view that fades at its bottom edge while more content sits below.
func (a *shelf) updateNotesCard(c *ui.Context, p updatePalette) {
	box := ui.Column(c.Key("notes")).FillWidth().Height(updateNotesH).Margin(18, 0, 0, 0).
		Radius(10).Background(p.card).Border(0.5, p.cardEdge).Clip()
	box.Children(func() {
		sc := ui.Scroll(c.Key("scroll")).Fill().TrackScroll(&a.updateNotes)
		// While content continues below, fade the last 42pt to transparent —
		// the same language as the clipboard page's scroll edge.
		if a.updateNotes.MaxY > 1 {
			black := ui.RGB(0, 0, 0)
			sc.MaskLinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
				{Color: black, At: 0},
				{Color: black, At: float32(updateNotesH-42-12) / updateNotesH},
				{Color: ui.Transparent, At: float32(updateNotesH-12) / updateNotesH},
			}})
		}
		sc.Children(func() {
			ui.Column(c.Key("notes-pad")).FillWidth().Padding(12, 20, 12, 12).Children(func() {
				markdown.View(c, a.update.Notes, updateMarkdownOptions(p))
			})
		})
	})
}

// updateMarkdownOptions builds the release notes' look from the palette:
// 12pt callout text in ink2, strong in ink, links in accent, and ### as a
// small section name, the CHANGELOG's bilingual separator. Rebuilt per
// frame, so an appearance change restyles the notes with the window.
func updateMarkdownOptions(p updatePalette) *markdown.Options {
	th := markdown.DefaultTheme(ui.DarkTheme())
	if !paletteIsDark(p) {
		th = markdown.DefaultTheme(ui.LightTheme())
	}
	th.FontSize = 12
	th.LineHeight = 1.6
	th.Text, th.Muted = p.ink2, p.ink3
	th.Link = p.accent
	th.Strong, th.StrongWeight = p.ink, 600
	th.Code, th.CodeBackground = p.ink, p.ghost
	th.Headings[2] = markdown.HeadingStyle{Size: 0.92, Weight: 600, Color: p.ink3, Top: 0.85, Bottom: 0.4}
	return &markdown.Options{Theme: th, OnLink: func(u string) { _ = openExternal(u) }}
}

// paletteIsDark reports whether p is the dark set — the palettes are
// package values, so comparing the window color is exact.
func paletteIsDark(p updatePalette) bool { return p.win == updateDark.win }

// updateAutoRow is the 以后自动下载并安装更新 checkbox: the setting's value,
// unchecked by default, written through applySettings like every
// preference.
func (a *shelf) updateAutoRow(c *ui.Context, l locales.Messages, p updatePalette) {
	on := a.settings.UpdateAutoInstall
	row := ui.ToggleBase(c.Key("auto"), &on).
		FillWidth().AlignItems(ui.Center).Justify(ui.Start).Gap(9).Margin(15, 0, 0, 0).
		Disabled(a.settingsBroken).Label(l.UpdateAutoInstallRow())
	row.Children(func() {
		box := ui.Box(c.Key("box")).Size(16, 16).Radius(4).AlignItems(ui.Center).Justify(ui.Center)
		if on {
			box.Background(p.accent)
		} else {
			box.Background(p.boxBg).Border(1, p.boxEdge)
		}
		box.Children(func() {
			if on {
				ui.Icon(c.Key("tick"), updateCheckSymbol).Size(10, 10).TextColor(ui.Hex("#FFFFFF"))
			}
		})
		ui.Text(c, l.UpdateAutoInstallRow()).FontSize(12).TextColor(p.ink2)
	})
	if row.Changed() {
		a.setUpdateAutoInstall(on)
	}
}

// updateCheckSymbol is the checkbox's tick (10×8 in the design).
var updateCheckSymbol = symbolWidth(`<path d="M1 4.2 3.6 6.8 9 1.2"/>`, 1.8)

// updateFoundButtons is the row 跳过这个版本 · · · 稍后提醒我 [安装更新].
func (a *shelf) updateFoundButtons(c *ui.Context, l locales.Messages, p updatePalette) {
	ui.Row(c.Key("buttons")).FillWidth().Margin(16, 0, 0, 0).Gap(8).AlignItems(ui.Center).Children(func() {
		ghost := ui.ButtonBase(c.Key("skip")).Height(28).Padding(0, 10, 0, 10).
			AlignItems(ui.Center).Label(l.UpdateSkipVersion())
		if ghost.Hovered() {
			ghost.Background(p.ghost).Radius(p.btnRadius)
		}
		ghost.Children(func() {
			ui.Text(c, l.UpdateSkipVersion()).SingleLine().FontSize(13).FontWeight(500).TextColor(p.ink2)
		})
		if ghost.Clicked() {
			a.skipUpdateVersion()
		}
		ui.Box(c.Key("spacer")).Grow(1)
		if a.updateButton(c, "later", l.UpdateLater(), false, p).Clicked() {
			a.remindUpdateLater()
		}
		if a.updateButton(c, "install", l.UpdateInstallNow(), true, p).Clicked() {
			a.installFromWindow()
		}
	})
}

// updateButton is one 28pt control: the secondary kind on ctl, the primary
// on accent with white text. The light palette's capsule radius is carried
// in p.btnRadius.
func (a *shelf) updateButton(c *ui.Context, key, label string, primary bool, p updatePalette) ui.Element {
	b := ui.ButtonBase(c.Key("btn-"+key)).Height(28).Radius(p.btnRadius).
		Padding(0, p.btnPadX, 0, p.btnPadX).AlignItems(ui.Center).Label(label)
	fg := p.ink
	if primary {
		b.Background(p.accent)
		fg = ui.Hex("#FFFFFF")
	} else {
		b.Background(p.ctl)
		if b.Hovered() {
			b.Background(p.ctlHover)
		}
	}
	b.Children(func() {
		t := ui.Text(c, label).SingleLine().FontSize(13).TextColor(fg)
		if primary {
			t.FontWeight(500)
		}
	})
	return b
}

// updateSmallBody is the compact face shared by checking, downloading,
// ready, latest and failed: icon left, status + detail + progress +
// actions right.
func (a *shelf) updateSmallBody(c *ui.Context, l locales.Messages, p updatePalette) {
	ui.Row(c.Key("body")).Fill().AlignItems(ui.Center).Gap(20).Padding(0, 22, 0, 22).Children(func() {
		updateIcon(c, 48)
		ui.Column(c.Key("col")).Grow(1).MinWidth(0).Children(func() {
			a.updateSmallText(c, l, p)
			if a.updateWinStage == updateStageDownloading {
				a.updateProgressBar(c, p)
			}
			a.updateSmallActions(c, l, p)
		})
	})
}

// updateSmallText is the status line and, where the face has one, the
// detail line under it.
func (a *shelf) updateSmallText(c *ui.Context, l locales.Messages, p updatePalette) {
	var status, detail string
	statusColor := p.ink
	switch a.updateWinStage {
	case updateStageChecking:
		status = l.UpdateCheckingShort()
	case updateStageDownloading:
		status = l.UpdateDownloadingShort()
	case updateStageReady:
		status = l.UpdateReadyShort()
	case updateStageLatest:
		status = l.UpdateUpToDateTitle()
		detail = l.UpdateUpToDateMeta(appVersion(), a.updateLastCheck.Format("15:04"))
	case updateStageFailed:
		status = l.UpdateFailedTitle()
		statusColor = p.danger
		detail = a.updateFailDetail
		if detail == "" {
			detail = l.UpdateFailedDetail()
		}
	}
	ui.Text(c, status).SingleLine().FontSize(13).FontWeight(600).TextColor(statusColor)
	if detail != "" {
		ui.Text(c, detail).FontSize(12).TextColor(p.ink3).Padding(4, 0, 0, 0)
	}
}

// updateProgressBar is the download's track and fill under the status
// line; a negative progress would draw mygo's moving bar, so only the
// known fraction fills.
func (a *shelf) updateProgressBar(c *ui.Context, p updatePalette) {
	ui.Box(c.Key("track")).FillWidth().Height(6).Radius(3).Background(p.track).Margin(12, 0, 0, 0).Clip().Children(func() {
		if a.updateProgress > 0 {
			ui.Box(c.Key("fill")).Height(6).Radius(3).Background(p.accent).WidthPercent(float32(math.Min(a.updateProgress, 1) * 100))
		}
	})
}

// updateSmallActions is the foot row of every compact face: the bytes (or
// nothing) left, buttons right.
func (a *shelf) updateSmallActions(c *ui.Context, l locales.Messages, p updatePalette) {
	ui.Row(c.Key("foot")).FillWidth().Margin(14, 0, 0, 0).Gap(8).AlignItems(ui.Center).Children(func() {
		if a.updateWinStage == updateStageDownloading && a.updateTotal > 0 {
			ui.Text(c, l.UpdateDownloadBytes(formatBytes(l, a.updateBytes), formatBytes(l, a.updateTotal))).
				SingleLine().FontSize(12).TextColor(p.ink2)
		}
		ui.Box(c.Key("spacer")).Grow(1)
		switch a.updateWinStage {
		case updateStageChecking:
			if a.updateButton(c, "cancel-check", l.UpdateCancel(), false, p).Clicked() {
				a.cancelUpdateCheck()
			}
		case updateStageDownloading:
			if a.updateButton(c, "cancel", l.UpdateCancel(), false, p).Clicked() {
				a.cancelUpdateInstall()
			}
		case updateStageReady:
			if a.updateButton(c, "relaunch", l.UpdateInstallAndRelaunch(), true, p).Clicked() {
				relaunchApp()
			}
		case updateStageLatest:
			if a.updateButton(c, "ok", l.UpdateOK(), true, p).Clicked() {
				a.closeUpdateWindow()
			}
		case updateStageFailed:
			if a.updateButton(c, "dismiss", l.UpdateOK(), false, p).Clicked() {
				a.closeUpdateWindow()
			}
			if a.updateButton(c, "retry", l.UpdateRetry(), true, p).Clicked() {
				a.checkUpdates(true)
			}
		}
	})
}

// nextRemindUntil is the end of 稍后提醒我's quiet period: a day out, which
// covers exactly one automatic-check round. Pure; unit-tested.
func nextRemindUntil(now time.Time) time.Time { return now.Add(24 * time.Hour) }
