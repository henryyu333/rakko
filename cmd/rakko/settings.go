package main

// The Settings window ("小岛"): a resizable dark window (640×600 default,
// minimum 600×420) with one centered floating pill island at the top (sharing
// its pill chrome with the panel's bottom capsule) and a single centered
// column of grouped cards below (capped at 720pt wide); overflow scrolls inside
// the page without scrollbars.
//
// Controls call applySettings — the same save-and-apply path the tray menus
// call — so window and menu never diverge.

import (
	"fmt"
	"runtime/debug"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/platform"
	"rakko/internal/settings"
)

// Seams around app-level calls so the headless view tests exercise the real
// controls without a running application.
var (
	openAtLogin          = mygo.App.OpenAtLogin
	setOpenAtLogin       = mygo.App.SetOpenAtLogin
	openPath             = mygo.Shell.OpenPath
	openExternal         = mygo.Shell.OpenExternal
	accessibilityRequest = platform.RequestAccessibility
	appVersionProbe      = mygo.App.Version
	setAppLocale         = mygo.App.SetLocale
)

// Settings v6 design tokens. Kept separate from theme.go's rename/update
// window surfaces so those windows stay unchanged.
var (
	settingsBg         = ui.Hex("#0B0B0D")
	settingsIslandBg   = ui.Hex("#1B1C1F")
	settingsCardBg     = ui.Hex("#17181B")
	settingsInk3       = ui.RGBA(255, 255, 255, 0.45)
	settingsSepColor   = ui.RGBA(255, 255, 255, 0.07)
	settingsCtlBg      = ui.Hex("#24252A")
	settingsCtlHoverBg = ui.Hex("#2E2F35")
	settingsSegOnBg    = ui.Hex("#3B3C42")
	settingsRowHoverBg = ui.RGBA(255, 255, 255, 0.03)
	settingsTileBg     = ui.Hex("#0F1012")
	settingsTileRim    = ui.RGBA(255, 255, 255, 0.06)
	settingsSwitchOff  = ui.RGBA(255, 255, 255, 0.17)
	settingsUnsetRim   = ui.RGBA(255, 255, 255, 0.16)
	settingsIslandOn   = ui.RGBA(255, 255, 255, 0.12)
)

// settingsTheme is the dark theme used by the Settings and Rename windows.
// Windows sets Segoe UI + Microsoft YaHei UI so CJK/ASCII typography aligns
// cleanly; macOS keeps the system UI font.
var settingsTheme = func() *ui.Theme {
	t := ui.DarkTheme()
	t.Background = settingsBg
	t.Accent = accent
	t.AccentHover = t.Accent.Mix(ui.RGB(255, 255, 255), 0.18)
	t.AccentPressed = t.Accent.Mix(ui.RGB(0, 0, 0), 0.16)
	t.Surface = setCtl
	t.SurfaceHover = setCtlHover
	t.SurfacePressed = setCtlActive
	t.Border = setCardEdge
	t.Text = ink
	t.TextMuted = ink2
	t.Danger = danger
	t.Success = success
	t.Focus = ui.RGBA(10, 132, 255, 0.55)
	t.Scrollbar = ui.RGBA(255, 255, 255, 0.18)
	t.ScrollbarWidth = 3
	t.Radius = 9
	t.Spacing = 42.0 / 9
	t.FontSize = 12.5
	if forWindows {
		t.Font = "Segoe UI, Microsoft YaHei UI"
	}
	return t
}()

func settingsThemeFor(windows bool) *ui.Theme {
	if !windows {
		t := *settingsTheme
		t.Font = ""
		return &t
	}
	t := *settingsTheme
	t.Font = "Segoe UI, Microsoft YaHei UI"
	return &t
}

// settingsPageID is one destination of the top island.
type settingsPageID int

const (
	settingsPageGeneral settingsPageID = iota
	settingsPageNotch
	settingsPageTray
	settingsPageClipboard
	settingsPageAIUsage
	settingsPageShortcuts
	settingsPagePermissions
	settingsPageAbout
)

// Metrics and timings from design/explore/settings-v6.html.
const (
	settingsPagePadX     = 60
	settingsPagePadTop   = 4
	settingsPagePadBot   = 30
	settingsRowMinHeight = 44
	settingsRowPadX      = 14
	settingsRowPadY      = 7
	settingsRowMark      = 18 // base artwork scale shared with home_page.go and agent_logos.go
	settingsMarkSlot     = 20 // v6 .r .lg slot (16pt artwork inside 20×20)
	settingsMarkArt      = 16
	settingsIslandH      = 38
	settingsIslandPad    = 3
	settingsIslandGap    = 2
	settingsIslandBtnH   = 32
	settingsScrollFadeH  = 16
)

var (
	settingsIslandTabSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.15}}
	settingsSwitchSpring    = ui.Animation{Spring: ui.SpringOptions{Duration: 220 * time.Millisecond, Bounce: 0.15}}
)

// appGlyph fills an excluded-app row when the app has no icon on disk.
var appGlyph = symbol(`<rect x="5" y="5" width="14" height="14" rx="3.5"/>`)

func symbol16(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg viewBox="0 0 16 16" xmlns="http://www.w3.org/2000/svg" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

// The island's icons, matching the 16pt line glyphs in settings-v6.html.
var (
	navGear  = symbol16(`<circle cx="8" cy="8" r="2.3"/><path d="M8 1.6v1.6M8 12.8v1.6M1.6 8h1.6M12.8 8h1.6M3.5 3.5l1.1 1.1M11.4 11.4l1.1 1.1M3.5 12.5l1.1-1.1M11.4 4.6l1.1-1.1"/>`)
	navNotch = symbol16(`<rect x="1.8" y="3" width="12.4" height="10" rx="2.2"/><path d="M6 3v1.2c0 .6.4 1 1 1h2c.6 0 1-.4 1-1V3"/>`)
	navTray  = symbol16(`<path d="M2.5 9.5v2.2c0 .7.6 1.3 1.3 1.3h8.4c.7 0 1.3-.6 1.3-1.3V9.5"/><path d="M8 2.5v7M5.2 6.8 8 9.6l2.8-2.8"/>`)
	navClip  = symbol16(`<rect x="3.2" y="2.6" width="9.6" height="11" rx="1.8"/><path d="M6 2.6h4v1.6H6zM5.6 7.4h4.8M5.6 10h3.2"/>`)
	navUsage = symbol16(`<path d="M2.6 11.4a5.4 5.4 0 1 1 10.8 0"/><path d="M8 11.4 10.4 7"/>`)
	navKey   = symbol16(`<rect x="1.6" y="4" width="12.8" height="8.4" rx="1.8"/><path d="M4.4 6.8h.1M6.8 6.8h.1M9.2 6.8h.1M11.6 6.8h.1M5.2 9.6h5.6"/>`)
	navHand  = symbol16(`<path d="M8 1.8 13 3.6v4.1c0 3-2.1 5.3-5 6.5-2.9-1.2-5-3.5-5-6.5V3.6z"/>`)
	navInfo  = symbol16(`<circle cx="8" cy="8" r="6.2"/><path d="M8 7.2v3.8M8 4.9v.1"/>`)
)

// settingsIcons caches decoded app icons for the 不记录这些应用 rows; nil
// values are cached too, so a bundle id that resolves to nothing is asked once.
var settingsIcons = map[string]*ui.Bitmap{}

// settingsAppIcon returns the bundle's icon as a bitmap, or nil.
func settingsAppIcon(bundleID string) *ui.Bitmap {
	if icon, ok := settingsIcons[bundleID]; ok {
		return icon
	}
	var icon *ui.Bitmap
	if png := platform.AppIcon(bundleID, 48); len(png) > 0 {
		icon, _ = ui.DecodeBitmap(png)
	}
	settingsIcons[bundleID] = icon
	return icon
}

// settingsOpen shows the Settings window; a seam, so the AI 用量
// shortcut's fallback is testable without a window server.
var settingsOpen func(*shelf)

func init() { settingsOpen = (*shelf).openSettings }

// openSettings shows the Settings window, creating it the first time, or
// re-focuses the one already open (single instance). The window opens centered
// on the cursor's display at the last content size used in this run (640×600
// initially).
func (a *shelf) openSettings() {
	if a.settingsWin != nil {
		a.loginOn, a.loginErr = openAtLogin(), ""
		a.repaintSettings()
		platform.ActivateSelf()
		a.settingsWin.Show()
		a.settingsWin.Focus()
		return
	}
	a.settingsTakeKeyboard()
	a.loginOn, a.loginErr = openAtLogin(), ""
	a.settingsRecording = shortcutNone
	if !permissionsSupported && a.settingsPage == settingsPagePermissions {
		a.settingsPage = settingsPageGeneral
	}
	winW, winH := a.settingsLastW, a.settingsLastH
	if winW <= 0 || winH <= 0 {
		winW, winH = settingsWidth, settingsHeight
	}
	work := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea
	frame := settingsFrame(work, winW, winH)
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: a.appLoc().WindowSettings(), Width: frame.Width, Height: frame.Height,
		MinWidth:  settingsMinWidth,
		MinHeight: settingsMinHeight,
		X:         frame.X, Y: frame.Y,
		Hidden: true, BackgroundColor: "#0B0B0D",
		UseContentSize:       true,
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 18, Y: 25},
		DisableFullScreen:    true,
		Content:              ui.View(a.settingsView),
	})
	platform.SetWindowAppearance(w.NativeHandle(), true)
	platform.ConfigureFramedWindow(w.NativeHandle(), true)
	w.SetWindowButtonsTheme(mygo.ThemeDark)
	frame = settingsFrameForChrome(work, winW, winH, forWindows, w.Bounds(), w.ContentBounds())
	w.SetContentBounds(frame)
	a.syncSettingsMinSize(w)
	w.OnResize(func() {
		a.syncSettingsMinSize(w)
		a.rememberSettingsSize(w)
	})
	w.OnClose(func(_ *mygo.CloseEvent) { a.rememberSettingsSize(w) })
	w.OnClosed(a.settingsClosed)
	w.OnBlur(a.settingsBlur)
	a.settingsWin = w
	w.Show()
	platform.ActivateSelf()
	w.Focus()
}

func (a *shelf) syncSettingsMinSize(w *mygo.Window) {
	if w == nil || !forWindows || w.IsMinimized() {
		return
	}
	minW, minH := settingsWindowMinSize(forWindows, w.Bounds(), w.ContentBounds())
	w.SetMinimumSize(minW, minH)
}

// settingsWindowMinSize returns the Window.SetMinimumSize arguments that keep
// the Settings window's content area at least settingsMinWidth × settingsMinHeight.
// On macOS, NSWindow.SetMinimumSize already constrains the content size; on
// Windows, WM_GETMINMAXINFO constrains the outer window rectangle, so the
// non-client border difference (outer - inner) is added.
func settingsWindowMinSize(windows bool, outer, inner mygo.Rectangle) (int, int) {
	if !windows {
		return settingsMinWidth, settingsMinHeight
	}
	padW := max(0, outer.Width-inner.Width)
	padH := max(0, outer.Height-inner.Height)
	return settingsMinWidth + padW, settingsMinHeight + padH
}

func (a *shelf) rememberSettingsSize(w *mygo.Window) {
	if w == nil || w.IsMinimized() {
		return
	}
	b := w.ContentBounds()
	if b.Width >= settingsMinWidth && b.Height >= settingsMinHeight {
		a.settingsLastW, a.settingsLastH = b.Width, b.Height
	}
}

// settingsTakeKeyboard prepares the handover when the Settings window opens.
func (a *shelf) settingsTakeKeyboard() {
	if pid := frontmostPID(); pid > 0 && pid != platform.OwnPID() {
		a.settingsPrevPID = pid
	}
	a.releaseKeys()
}

// settingsClosed runs when the Settings window has closed.
func (a *shelf) settingsClosed() {
	a.stopPermissionFlow()
	a.endShortcutRecording()
	a.settingsWin = nil
	if a.quitting {
		return
	}
	a.settingsReleaseKeyboard()
}

type settingsKeyboardExit int

const (
	settingsToPanel settingsKeyboardExit = iota
	settingsToShelf
	settingsToPrevApp
)

func (a *shelf) settingsKeyboardExit() settingsKeyboardExit {
	switch {
	case a.focusMode:
		return settingsToPanel
	case a.visible:
		return settingsToShelf
	default:
		return settingsToPrevApp
	}
}

func (a *shelf) settingsReleaseKeyboard() {
	prev := a.settingsPrevPID
	a.settingsPrevPID = 0
	switch a.settingsKeyboardExit() {
	case settingsToPanel:
		if a.win != nil {
			a.win.Focus()
		}
		return
	case settingsToShelf:
		a.holdKeys()
	}
	a.handBackApp(prev)
}

func (a *shelf) repaintSettings() {
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
}

// settingsFrame centers the w × h Settings window inside the display's work
// area, enforcing the minimum size while clamping to the work area on unusually
// small displays.
func settingsFrame(work mygo.Rectangle, w, h int) mygo.Rectangle {
	w = max(w, settingsMinWidth)
	h = max(h, settingsMinHeight)
	if work.Width > 0 && w > work.Width {
		w = work.Width
	}
	if work.Height > 0 && h > work.Height {
		h = work.Height
	}
	x := work.X + (work.Width-w)/2
	y := work.Y + (work.Height-h)/2
	x = min(max(x, work.X), max(work.X, work.X+work.Width-w))
	y = min(max(y, work.Y), max(work.Y, work.Y+work.Height-h))
	return mygo.Rectangle{X: x, Y: y, Width: w, Height: h}
}

// settingsFrameForChrome clamps the Settings window inside work by its outer
// frame on Windows (accounting for non-client borders derived from outer - inner)
// and returns the resulting content bounds. On macOS, it matches settingsFrame.
func settingsFrameForChrome(work mygo.Rectangle, w, h int, windows bool, outer, inner mygo.Rectangle) mygo.Rectangle {
	if !windows {
		return settingsFrame(work, w, h)
	}
	padW := max(0, outer.Width-inner.Width)
	padH := max(0, outer.Height-inner.Height)
	padL := max(0, inner.X-outer.X)
	padT := max(0, inner.Y-outer.Y)
	w = max(w, settingsMinWidth)
	h = max(h, settingsMinHeight)
	availW := max(0, work.Width-padW)
	availH := max(0, work.Height-padH)
	if availW > 0 && w > availW {
		w = availW
	}
	if availH > 0 && h > availH {
		h = availH
	}
	outerW, outerH := w+padW, h+padH
	outerX := work.X + (work.Width-outerW)/2
	outerY := work.Y + (work.Height-outerH)/2
	outerX = min(max(outerX, work.X), max(work.X, work.X+work.Width-outerW))
	outerY = min(max(outerY, work.Y), max(work.Y, work.Y+work.Height-outerH))
	return mygo.Rectangle{X: outerX + padL, Y: outerY + padT, Width: w, Height: h}
}

// setSettingsPage switches to page and resets scroll without changing the
// window's size.
func (a *shelf) setSettingsPage(page settingsPageID) {
	if !permissionsSupported && page == settingsPagePermissions {
		return
	}
	if a.settingsPage == page {
		return
	}
	a.settingsPage = page
	a.settingsScroll.Y = 0
	a.endShortcutRecording()
	a.repaintSettings()
}

// settingsView draws the Settings window: scrollable page content below the
// 64pt top drag zone, the 16pt top scroll fade, the centered top island
// navigation, and (while recording a shortcut) the key-capture overlay.
func (a *shelf) settingsView(c *ui.Context) {
	c.SetTheme(settingsThemeFor(forWindows))
	c.Root().Background(settingsBg)

	recording := a.settingsRecording != shortcutNone
	pending := a.shortcutConfirm != nil
	switch {
	case pending:
		c.OnShortcut(0, ui.KeyEscape, a.cancelShortcutConfirm)
	case a.settingsWin != nil && recording:
	case a.settingsWin != nil:
		c.OnShortcut(0, ui.KeyEscape, a.settingsWin.Close)
		c.OnShortcut(ui.Cmd, ui.KeyW, a.settingsWin.Close)
	}

	win := ui.Box(c.Key("win")).Fill().Background(settingsBg)
	if pending {
		win = win.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputPointerDown {
				a.cancelShortcutConfirm()
			}
			return false
		})
	}
	win.Children(func() {
		// Top 64pt drag region (clearing any Windows caption buttons on the right)
		// with the floating island navigation centered inside it. Placed first in
		// tree/focus order with ZIndex(5) so navigation stays above scrolled content.
		bar := c.TitleBar()
		dragRight := float32(bar.Right)
		ui.Box(c.Key("top-drag")).Absolute().Top(0).Left(0).Right(dragRight).Height(settingsTopH).
			ZIndex(4).DragWindow()
		if dragRight > 0 && float32(bar.Height) < settingsTopH {
			ui.Box(c.Key("top-drag-r")).Absolute().Top(float32(bar.Height)).Right(0).Width(dragRight).
				Height(settingsTopH - float32(bar.Height)).ZIndex(4).DragWindow()
		}
		ui.Row(c.Key("top-bar")).Absolute().Top(0).Left(0).Right(0).Height(settingsTopH).
			ZIndex(5).Center().PassThrough().Children(func() {
			a.settingsIsland(c)
		})

		// Content area below the 64pt top bar; no visible scrollbar.
		ui.Scroll(c.Key("page-scroll")).Absolute().Top(settingsTopH).Bottom(0).Left(0).Right(0).
			Scrollbars(false).TrackScroll(&a.settingsScroll).Children(func() {
			enterAnim := ui.ElementTransition{
				Duration: 260 * time.Millisecond,
				Ease:     ui.EaseOut,
				Enter:    &ui.Motion{Y: 6, Opacity: 0},
			}
			if reduceMotion(c) {
				enterAnim = ui.ElementTransition{Duration: -1}
			}
			ui.Column(c.Key("page-wrap")).FillWidth().AlignItems(ui.Center).Children(func() {
				pg := ui.Column(c.Key(fmt.Sprintf("page-%d", a.settingsPage))).
					FillWidth().MaxWidth(settingsMaxContentW+2*settingsPagePadX).
					Padding(settingsPagePadTop, settingsPagePadX, settingsPagePadBot, settingsPagePadX).
					Transition(enterAnim)
				pg.Children(func() {
					if a.settingsBroken {
						settingsBrokenBanner(c)
					}
					a.settingsPageBody(c)
				})
			})
		})

		// Top 16pt scroll fade directly under the 64pt top zone while scrolled.
		if fadeAlpha := min(max(a.settingsScroll.Y/12, 0), 1); fadeAlpha > 0 {
			ui.Box(c.Key("scroll-fade")).Absolute().Top(settingsTopH).Left(0).Right(0).
				Height(settingsScrollFadeH).ZIndex(3).IgnoreInput(true).Opacity(fadeAlpha).
				LinearGradient(ui.LinearGradient{
					Angle: 180,
					Stops: []ui.GradientStop{
						{Color: settingsBg, At: 0},
						{Color: settingsBg.Alpha(0), At: 1},
					},
				})
		}

		if recording && !pending {
			a.settingsCaptureLayer(c)
		}
	})
}

// settingsIsland builds the floating pill navigation centered in the 64pt
// top bar, sharing navCapsuleRow / navCapsuleButton with the panel's bottom
// capsule.
func (a *shelf) settingsIsland(c *ui.Context) {
	l := loc(c)
	type islandTab struct {
		id    settingsPageID
		icon  *ui.SVG
		label string
	}
	tabs := []islandTab{
		{settingsPageGeneral, navGear, l.General()},
		{settingsPageNotch, navNotch, l.SettingsNotchNav()},
		{settingsPageTray, navTray, l.SettingsTraySection()},
		{settingsPageClipboard, navClip, l.SettingsClipboardSection()},
		{settingsPageAIUsage, navUsage, l.SettingsAISection()},
		{settingsPageShortcuts, navKey, l.SettingsShortcutsSection()},
	}
	if permissionsSupported {
		tabs = append(tabs, islandTab{settingsPagePermissions, navHand, l.PermTitle()})
	}
	tabs = append(tabs, islandTab{settingsPageAbout, navInfo, l.SettingsAboutPage()})

	missingPerm := permissionsSupported && len(missingSystemPermissions()) > 0
	island := navCapsuleRow(c, "settings-island", settingsIslandH, settingsIslandPad, settingsIslandGap).
		Background(settingsIslandBg).
		Shadow(0, 8, 22, 0, ui.RGBA(0, 0, 0, 0.40))
	island.Children(func() {
		for _, tab := range tabs {
			tab := tab
			on := a.settingsPage == tab.id
			b := navCapsuleButton(c, fmt.Sprintf("tab-%d", tab.id), tab.label, 0, settingsIslandBtnH,
				settingsIslandOn, ui.Color{}, on, !on)
			target := float32(0)
			if on {
				target = 1
			}
			k := b.AnimateTo("expand", target, fadeOr(c, settingsIslandTabSpring))
			pos := max(k, 0)
			b.Padding(0, 9+4*min(pos, 1.15), 0, 9+1*min(pos, 1.15))

			color := settingsInk3
			if on {
				color = ink
			} else if b.Hovered() {
				color = ink2
			}
			labelW, _ := c.MeasureText(0, ui.Span{Text: tab.label, Size: 12.5, Weight: 700})
			slotW := (7 + labelW) * pos
			b.Children(func() {
				iconBox := ui.Box(c.Key("ic-box")).Size(16, 16)
				iconBox.Children(func() {
					ui.Icon(c.Key("ic"), tab.icon).Size(16, 16).TextColor(color)
					if tab.id == settingsPagePermissions && missingPerm {
						ui.Box(c.Key("perm-dot")).Absolute().Right(-1).Top(-1).Size(6, 6).Radius(3).
							Background(warning).Border(1, settingsIslandBg)
					}
				})
				if slotW > 0.5 {
					ui.Row(c.Key("lb-slot")).Width(slotW).AlignItems(ui.Center).Clip().
						Opacity(min(pos, 1)).Children(func() {
						ui.Box(c.Key("lb-gap")).Width(7).Shrink(0)
						settingsText(c, tab.label, 12.5).SingleLine().FontWeight(700).TextColor(color)
					})
				}
			})
			if b.Clicked() {
				a.setSettingsPage(tab.id)
			}
		}
	})
}

// settingsBrokenBanner explains an unreadable settings.json above the page's
// content: preference groups below it are locked.
func settingsBrokenBanner(c *ui.Context) {
	l := loc(c)
	ui.Column(c.Key("broken")).Margin(8, 0, 12, 0).Padding(10, 12).Gap(4).Radius(12).
		Background(danger.Alpha(0.12)).
		Border(0.5, danger.Alpha(0.35)).Children(func() {
		settingsText(c, l.SettingsBrokenTitle(), 12).FontWeight(700).TextColor(danger)
		settingsText(c, l.SettingsBrokenBody(), 11.5).TextColor(settingsInk3)
	})
}

func (a *shelf) settingsPageBody(c *ui.Context) {
	a.settingsPageBodyFor(c, a.settingsPage)
}

func (a *shelf) settingsPageBodyFor(c *ui.Context, page settingsPageID) {
	switch page {
	case settingsPageGeneral:
		a.settingsGeneralPage(c)
	case settingsPageNotch:
		a.settingsNotchPage(c)
	case settingsPageTray:
		a.settingsTrayPage(c)
	case settingsPageClipboard:
		a.settingsClipboardPage(c)
	case settingsPageAIUsage:
		a.settingsAIUsagePage(c)
	case settingsPageShortcuts:
		a.settingsShortcutsPage(c)
	case settingsPagePermissions:
		a.settingsPermissionsPage(c)
	case settingsPageAbout:
		a.settingsAboutPage(c)
	}
}

// settingsGroup builds a v6 section: 12pt/700/ink3 header (margin-top 8 for the
// first section, 20 for subsequent ones; 7pt below, 4pt left inset), #17181B
// 12pt-rounded card without border, and optional muted footnote.
func settingsGroup(c *ui.Context, key, title, foot string, fn func()) {
	settingsGroupAt(c, key, title, foot, false, fn)
}

func settingsFirstGroup(c *ui.Context, key, title, foot string, fn func()) {
	settingsGroupAt(c, key, title, foot, true, fn)
}

func settingsGroupAt(c *ui.Context, key, title, foot string, first bool, fn func()) {
	topGap := float32(20)
	if first {
		topGap = 8
	}
	ui.Column(c.Key("grp-" + key)).FillWidth().Children(func() {
		if title != "" {
			settingsText(c, title, 12).SingleLine().FontWeight(700).
				TextColor(settingsInk3).Padding(topGap, 0, 7, 4)
		} else {
			ui.Box(c.Key("grp-gap-" + key)).Height(topGap)
		}
		ui.Column(c.Key("card-" + key)).Radius(12).Background(settingsCardBg).
			FillWidth().Clip().Children(fn)
		if foot != "" {
			settingsText(c, foot, 11.5).TextColor(settingsInk3).
				Padding(10, 4, 0, 4).MaxWidth(460)
		}
	})
}

func settingsGroupRows(c *ui.Context, key, title, foot string, disabled bool, fn func()) {
	settingsGroup(c, key, title, foot, func() {
		ui.Column(c.Key("rows-" + key)).FillWidth().Disabled(disabled).Children(fn)
	})
}

func settingsFirstGroupRows(c *ui.Context, key, title, foot string, disabled bool, fn func()) {
	settingsFirstGroup(c, key, title, foot, func() {
		ui.Column(c.Key("rows-" + key)).FillWidth().Disabled(disabled).Children(fn)
	})
}

// settingsSep is the 1pt hairline between two rows: white 7%, inset 14pt on
// the left and flush on the right.
func settingsSep(c *ui.Context, key string) {
	ui.Row(c.Key("sep-"+key)).FillWidth().Padding(0, 0, 0, settingsRowPadX).Children(func() {
		ui.Box(c.Key("line")).Height(1).FillWidth().Background(settingsSepColor)
	})
}

// settingsRowLine is one row's frame: 44pt minimum height, 7pt vertical and
// 14pt horizontal padding, 14pt gap.
func settingsRowLine(c *ui.Context, key string) ui.Element {
	return ui.Row(c.Key("row-"+key)).FillWidth().MinHeight(settingsRowMinHeight).
		AlignItems(ui.Center).Gap(14).Padding(settingsRowPadY, settingsRowPadX)
}

func settingsRowText(c *ui.Context, label, caption string) {
	settingsRowTextColor(c, label, caption, settingsInk3)
}

func settingsRowTextColor(c *ui.Context, label, caption string, captionColor ui.Color) {
	ui.Column(c.Key("text")).Gap(1).Grow(1).MinWidth(0).Children(func() {
		settingsLabel(c, label, 13, 400).TextColor(ink)
		if caption != "" {
			settingsText(c, caption, 11.5).TextColor(captionColor)
		}
	})
}

// settingsFit, when a test installs it, reports single-line labels wider than
// their layout box.
var settingsFit func(string)

// settingsLine is the design's line height for a Settings font size: the
// mock's 1.35 line-height in points. The macOS system font and Segoe UI +
// Microsoft YaHei UI have different ascent and descent at the same size
// (about two points at 12pt), which would move every row and the window's
// own content height apart; pinning the line box makes both platforms lay
// out to the same heights.
func settingsLine(size float32) float32 { return size * 1.35 }

// settingsText is a Settings text at size with that pinned line box.
func settingsText(c *ui.Context, text string, size float32) ui.Element {
	return ui.Text(c, text).FontSize(size).FixedLineHeight(settingsLine(size))
}

func settingsLabel(c *ui.Context, text string, size float32, weight int) ui.Element {
	t := settingsText(c, text, size).SingleLine()
	if weight != 0 {
		t.FontWeight(weight)
	}
	if settingsFit != nil {
		if b := t.Bounds(); b.W > 0 {
			mw, _ := c.MeasureText(0, ui.Span{Text: text, Size: size, Weight: weight})
			if mw > b.W+0.5 {
				settingsFit(fmt.Sprintf("%q needs %.1fpt, box is %.1fpt (size %g): the label is cut", text, mw, b.W, size))
			}
		}
	}
	return t
}

func settingsToggleRow(c *ui.Context, key, label, caption string, on bool, apply func(bool)) {
	settingsToggleRowCap(c, key, label, caption, settingsInk3, on, apply)
}

func settingsToggleRowCap(c *ui.Context, key, label, caption string, captionColor ui.Color, on bool, apply func(bool)) {
	settingsSwitchRow(c, key, label, caption, captionColor, nil, on, apply)
}

func settingsToggleRowMark(c *ui.Context, key, label, caption string, logo *ui.SVG, color ui.Color, mark float32, on bool, apply func(bool)) {
	settingsSwitchRow(c, key, label, caption, settingsInk3, func() {
		settingsMark(c, logo, color, mark)
	}, on, apply)
}

// settingsMark draws a row's lead logo inside a 20×20 slot scaled to 16pt.
func settingsMark(c *ui.Context, logo *ui.SVG, color ui.Color, mark float32) {
	slot := ui.Box(c.Key("mark")).Size(settingsMarkSlot, settingsMarkSlot)
	slot.Children(func() {
		if logo == nil {
			return
		}
		scaled := mark * settingsMarkArt / settingsRowMark
		off := (settingsMarkSlot - scaled) / 2
		ui.Icon(c, logo).Size(scaled, scaled).TextColor(color).Absolute().Left(off).Top(off)
	})
}

// settingsSwitch is the 38×22 v6 switch with a 220ms spring knob.
func settingsSwitch(c *ui.Context, on *bool, label string) ui.Element {
	sw := ui.SwitchBase(c, on).Size(38, 22).Shrink(0).Radius(11).Label(label)
	anim := fadeOr(c, settingsSwitchSpring)
	target := float32(0)
	if *on {
		target = 1
	}
	pos := sw.AnimateTo("knob", target, anim)
	k := min(max(pos, 0), 1)
	sw.Background(settingsSwitchOff.Mix(accent, k))
	rtl := c.Direction() == ui.RightToLeft
	sw.Draw(func(p *ui.Painter, r ui.Rect) {
		at := k
		if rtl {
			at = 1 - k
		}
		knob := ui.Rect{X: r.X + 2 + at*16, Y: r.Y + 2, W: 18, H: 18}
		p.Shadow(knob, 9, 0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.35))
		p.Fill(knob, ui.RGB(255, 255, 255), 9)
	})
	return sw
}

func settingsSwitchRow(c *ui.Context, key, label, caption string, captionColor ui.Color, lead func(), on bool, apply func(bool)) {
	row := ui.ToggleBase(c.Key("toggle-"+key), &on).FillWidth().AlignItems(ui.Center).
		Gap(14).MinHeight(settingsRowMinHeight).Padding(settingsRowPadY, settingsRowPadX).Label(label)
	if row.Hovered() {
		row.Background(settingsRowHoverBg)
	}
	var sw ui.Element
	row.Children(func() {
		if lead != nil {
			lead()
		}
		settingsRowTextColor(c, label, caption, captionColor)
		sw = settingsSwitch(c, &on, label)
	})
	if row.Changed() || sw.Changed() {
		apply(on)
	}
}

func settingsRow(c *ui.Context, key, label, caption string, fn func()) {
	settingsRowLine(c, key).Children(func() {
		settingsRowText(c, label, caption)
		fn()
	})
}

// settingsButton is the 26pt v6 pill-rect button (#24252A, hover #2E2F35).
func settingsButton(c *ui.Context, key, label string, primary bool, fn func()) ui.Element {
	b := ui.ButtonBase(c.Key("btn-"+key)).Height(26).Shrink(0).Radius(7).Padding(0, 12, 0, 12).
		AlignItems(ui.Center).Label(label)
	fg := ink
	if primary {
		b.Background(ink)
		fg = ui.RGB(0, 0, 0)
	} else {
		b.Background(settingsCtlBg)
		if b.Hovered() {
			b.Background(settingsCtlHoverBg)
		}
	}
	b.Children(func() {
		t := settingsText(c, label, 12).SingleLine().TextColor(fg)
		if primary {
			t.FontWeight(700)
		}
	})
	if b.Clicked() {
		fn()
	}
	return b
}

// settingsSegmented builds the 28pt v6 segmented control using one shared path
// across platforms. Each segment's text baseline is measured via ShapeText so
// mixed CJK/ASCII controls (e.g. 跟随系统 / 简体中文 / English) share one visual
// baseline inside their 24pt segments.
func settingsSegmented(c *ui.Context, key, label string, selected *int, labels ...string) ui.Element {
	return settingsSegmentedWidth(c, key, label, selected, false, labels...)
}

// settingsSegmentedWidth is settingsSegmented, optionally stretching each
// segment to an equal share of a full-width track (the rename window's mode
// picker); Settings' rows keep the control at its intrinsic width.
func settingsSegmentedWidth(c *ui.Context, key, label string, selected *int, fill bool, labels ...string) ui.Element {
	const segH = float32(24)
	font := ui.Font{Family: c.Theme().Font, Size: 12}
	baselines := make([]float32, len(labels))
	var maxBaseline float32
	for i, segLabel := range labels {
		tl := ui.ShapeText(segLabel, font, 0)
		_, th := tl.Size()
		if lines := tl.Lines(); len(lines) > 0 {
			b := (segH-th)/2 + lines[0].Baseline
			baselines[i] = b
			if b > maxBaseline {
				maxBaseline = b
			}
		}
	}

	parts := ui.SegmentedBase(c.Key("seg-"+key), selected, len(labels))
	g := parts.Track.Height(28).AlignItems(ui.Stretch).
		Padding(2).Gap(2).Radius(8).Background(settingsCtlBg).Label(label)
	if fill {
		g.FillWidth()
	} else {
		g.Shrink(0)
	}
	g.Children(func() {
		for i, segLabel := range labels {
			s := parts.Segment(i)
			top, bot := float32(0), float32(0)
			if dy := maxBaseline - baselines[i]; dy > 0.05 {
				// Centering a child shifts its y by (padTop - padBot)/2; doubling dy
				// moves the text down by dy so all segments share maxBaseline.
				top = dy * 2
			}
			s.Height(segH).Padding(top, 12, bot, 12).Radius(6)
			if fill {
				s.Grow(1)
			}
			fg := ink2
			switch {
			case i == *selected:
				s.Background(settingsSegOnBg).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.25))
				fg = ink
			case s.Hovered():
				s.Background(ui.RGBA(255, 255, 255, 0.05))
			}
			s.Children(func() {
				ui.Text(c, segLabel).SingleLine().FontSize(12).TextColor(fg)
			})
		}
	})
	return g
}

func choiceSegments(current int, choices []int, format func(int) string) (labels []string, values []int, selected int) {
	values = slices.Clone(choices)
	if !slices.Contains(values, current) {
		values = append(values, current)
	}
	slices.Sort(values)
	selected = slices.Index(values, current)
	for _, v := range values {
		labels = append(labels, format(v))
	}
	return labels, values, selected
}

func languageIndex(tag string) int {
	switch tag {
	case "zh-Hans":
		return 1
	case "en":
		return 2
	}
	return 0
}

func languageAt(idx int) string {
	switch idx {
	case 1:
		return "zh-Hans"
	case 2:
		return "en"
	}
	return ""
}

func searchModeIndex(mode settings.SearchMode) int {
	switch mode.Normalized() {
	case settings.SearchModeApps:
		return 1
	case settings.SearchModeFiles:
		return 2
	}
	return 0
}

func searchModeAt(idx int) settings.SearchMode {
	switch idx {
	case 1:
		return settings.SearchModeApps
	case 2:
		return settings.SearchModeFiles
	}
	return settings.SearchModeAll
}

func (a *shelf) setLanguage(tag string) {
	tag = languageTag(tag)
	if tag == a.settings.Language {
		return
	}
	next := a.settings
	next.Language = tag
	if !a.applySettings(next) {
		return
	}
	a.showLanguage()
}

func (a *shelf) applyOpenAtLogin(on bool) {
	err := setOpenAtLogin(on)
	a.loginOn = openAtLogin()
	if err != nil {
		a.loginErr = err.Error()
	} else {
		a.loginErr = ""
	}
	a.repaintSettings()
}

func settingsFootnote(c *ui.Context, key, text string) {
	settingsText(c, text, 11.5).TextColor(settingsInk3).Padding(10, 4, 0, 4).MaxWidth(460)
}

func appVersion() string {
	if v := appVersionProbe(); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
