//go:build darwin

package main

// The eight pages of the Settings window, each the design's hero card over
// its grouped cards. Every preference that settings.json or the tray menus
// held before lives on one of them; the split follows where a user looks
// for it (Coucou's Hub), not the old file's order.

import (
	"fmt"
	"runtime"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
	"rakko/internal/aiusage"
	"rakko/internal/settings"
	"rakko/locales"
)

// Hero gradients: one deep tone per page, with a glow of the page's accent.
var (
	heroGeneralFrom = ui.Hex("#1C2233")
	heroGeneralTo   = ui.Hex("#0C0F18")
	heroGeneralGlow = ui.RGBA(140, 160, 255, 1)

	heroNotchFrom = ui.Hex("#191A1F")
	heroNotchTo   = ui.Hex("#07080A")
	heroNotchGlow = ui.RGBA(255, 255, 255, 1)

	heroTrayFrom = ui.Hex("#102641")
	heroTrayTo   = ui.Hex("#08111E")
	heroTrayGlow = ui.RGBA(10, 132, 255, 1)

	heroClipFrom = ui.Hex("#33230C")
	heroClipTo   = ui.Hex("#140E06")
	heroClipGlow = ui.RGBA(255, 159, 10, 1)

	heroUsageFrom = ui.Hex("#0F2A1A")
	heroUsageTo   = ui.Hex("#07120B")
	heroUsageGlow = ui.RGBA(48, 209, 88, 1)

	heroKeysFrom = ui.Hex("#232327")
	heroKeysTo   = ui.Hex("#0C0C0E")
	heroKeysGlow = ui.RGBA(255, 255, 255, 1)

	heroPermFrom = ui.Hex("#1D1736")
	heroPermTo   = ui.Hex("#0B0916")
	heroPermGlow = ui.RGBA(94, 92, 230, 1)
)

// settingsGeneralPage: 启动 (open at login, language) and 搜索 (default
// search mode). Only the login item survives a broken settings.json; every
// other row is locked with the file. Updates live on 关于, beside the
// version they apply to.
func (a *shelf) settingsGeneralPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "general", l.General(), "neutral",
		heroGeneralFrom, heroGeneralTo, heroGeneralGlow)

	settingsGroup(c, "startup", l.SettingsStartupGroup(), "", func() {
		loginCap, capColor := "", ink3
		if a.loginErr != "" {
			loginCap, capColor = a.loginErr, danger
		}
		settingsToggleRowCap(c, "login", l.SettingsLoginRow(), loginCap, capColor, a.loginOn, a.applyOpenAtLogin)
		settingsSep(c, "login")
		ui.Column(c.Key("language")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsRow(c, "language", l.LanguageRow(), "", func() {
				idx := languageIndex(a.settings.Language)
				if sg := settingsSegmented(c, "language", l.LanguageRow(), &idx,
					l.LanguageFollowSystem(), l.LanguageChinese(), l.LanguageEnglish()); sg.Changed() {
					a.setLanguage(languageAt(idx))
				}
			})
		})
	})

	settingsGroup(c, "search", l.SettingsSearchGroup(), "", func() {
		ui.Column(c.Key("search-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsRow(c, "searchmode", l.SettingsDefaultSearchMode(), "", func() {
				idx := searchModeIndex(a.settings.SearchMode)
				if sg := settingsSegmented(c, "searchmode", l.SettingsDefaultSearchMode(), &idx,
					l.SearchModeAll(), l.SearchModeApps(), l.SearchModeFiles()); sg.Changed() {
					a.setSearchMode(searchModeAt(idx))
				}
			})
		})
	})
}

// settingsNotchPage: the shelf's mantle (shape, island position), the
// desktop otter, the three live activities, and window snapping.
func (a *shelf) settingsNotchPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "notch", l.SettingsNotchPage(), "curious",
		heroNotchFrom, heroNotchTo, heroNotchGlow)

	settingsGroup(c, "shape", l.SettingsShapeGroup(), "", func() {
		ui.Column(c.Key("shape-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsRow(c, "shellstyle", l.ShellStyleRow(), l.ShellStyleCaption(), func() {
				style := 0
				if a.settings.ShellStyle.Normalized() == settings.ShellStyleIsland {
					style = 1
				}
				if sg := settingsSegmented(c, "shellstyle", l.ShellStyleRow(), &style,
					l.ShellStyleNotch(), l.ShellStyleIsland()); sg.Changed() {
					next := settings.ShellStyleNotch
					if style == 1 {
						next = settings.ShellStyleIsland
					}
					a.setShellStyle(next)
				}
			})
			if a.island {
				settingsSep(c, "islandpos")
				settingsRow(c, "islandpos", l.SettingsIslandPosRow(), l.SettingsIslandPosCaption(), func() {
					settingsButton(c, "island-reset", l.SettingsIslandReset(), false, a.resetIslandPosition)
				})
			}
		})
	})
	settingsFootnote(c, "shape-foot", l.SettingsNotchFoot())

	// 桌宠: the desktop otter's own two switches. A build without the
	// artwork draws no otter, so the group is not there either — an empty
	// promise is worse than no row.
	if petArt() != nil {
		settingsGroup(c, "pet", l.SettingsPetGroup(), "", func() {
			ui.Column(c.Key("pet-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
				settingsToggleRow(c, "pet-show", l.SettingsPetShowRow(), "", a.settings.Pet.Shown, a.setPetShown)
				settingsSep(c, "pet-show")
				settingsRow(c, "pet-size", l.PetSizeRow(), "", func() {
					index := petSizeIndex(a.settings.Pet.Size)
					if sg := settingsSegmented(c, "pet-size", l.PetSizeRow(), &index,
						l.PetSizeSmall(), l.PetSizeMedium(), l.PetSizeLarge()); sg.Changed() {
						a.setPetSize(petSizeAt(index))
					}
				})
			})
		})
	}

	settingsGroup(c, "live", l.SettingsLiveSection(), "", func() {
		ui.Column(c.Key("live-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsToggleRow(c, "live-charging", l.SettingsLiveCharging(), "", a.settings.LiveActivities.Charging,
				func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Charging = on }) })
			settingsSep(c, "live-charging")
			settingsToggleRow(c, "live-headphones", l.SettingsLiveHeadphones(), "", a.settings.LiveActivities.Headphones,
				func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Headphones = on }) })
			settingsSep(c, "live-headphones")
			settingsToggleRow(c, "live-downloads", l.SettingsLiveDownloads(), "", a.settings.LiveActivities.Downloads,
				func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Downloads = on }) })
		})
	})

	settingsGroup(c, "window", l.SettingsWindowGroup(), "", func() {
		ui.Column(c.Key("window-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsToggleRow(c, "snap", l.SettingsSnapRow(), l.SettingsSnapCaption(),
				a.settings.SnapWindows, a.setSnapWindows)
		})
	})
}

// setLiveActivity saves one 实时活动 switch and repaints.
func (a *shelf) setLiveActivity(mutate func(*settings.Settings)) {
	next := a.settings
	mutate(&next)
	a.applySettings(next)
}

// settingsTrayPage: the tray's own two rules.
func (a *shelf) settingsTrayPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "tray", l.SettingsTraySection(), "happy",
		heroTrayFrom, heroTrayTo, heroTrayGlow)

	settingsGroupRows(c, "tidy", l.SettingsTidyGroup(), "", a.settingsBroken, func() {
		days := max(0, a.settings.RetentionDays)
		labels, values, selected := choiceSegments(days, retentionChoices, func(n int) string {
			if n <= 0 {
				return l.SettingsRetentionNever()
			}
			return l.SettingsRetentionDays(n)
		})
		settingsRow(c, "retention", l.SettingsRetentionRow(), l.SettingsRetentionCaption(), func() {
			if sg := settingsSegmented(c, "retention", l.SettingsRetentionRow(), &selected, labels...); sg.Changed() {
				a.setRetention(values[selected])
			}
		})
		settingsSep(c, "retention")
		settingsToggleRow(c, "dragout", l.SettingsDragOutRow(), l.SettingsDragOutCaption(),
			a.settings.RemoveAfterDrag, a.setRemoveAfterDrag)
	})
}

// settingsClipboardPage: the retained count and the excluded apps, with the
// same removal the old list offered.
func (a *shelf) settingsClipboardPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "clip", l.SettingsClipboardSection(), "surprised",
		heroClipFrom, heroClipTo, heroClipGlow)

	settingsGroupRows(c, "record", l.SettingsClipGroup(), "", a.settingsBroken, func() {
		limit := a.settings.ClipboardLimit
		if limit < 1 {
			limit = settings.Default().ClipboardLimit
		}
		labels, values, selected := choiceSegments(limit, clipboardLimitChoices, strconv.Itoa)
		settingsRow(c, "limit", l.SettingsClipboardLimitRow(), "", func() {
			if sg := settingsSegmented(c, "limit", l.SettingsClipboardLimitRow(), &selected, labels...); sg.Changed() {
				a.setClipboardLimit(values[selected])
			}
		})
	})

	rows := func() {
		if len(a.settings.ExcludedApps) == 0 {
			settingsRow(c, "excl-none", l.SettingsClipboardExcludedNone(), "", func() {})
			return
		}
		for i, id := range a.settings.ExcludedApps {
			bundleID := id
			if i > 0 {
				settingsSep(c, "excl-"+bundleID)
			}
			ui.Row(c.Key("app-"+bundleID)).AlignItems(ui.Center).Gap(14).
				MinHeight(settingsRowMinHeight).Padding(settingsRowPadY, settingsRowPadX).FillWidth().Children(func() {
				if icon := settingsAppIcon(bundleID); icon != nil {
					ui.Image(c.Key("ic"), icon).Size(20, 20).Radius(5)
				} else {
					ui.Icon(c.Key("ic"), appGlyph).Size(20, 20).TextColor(ink3)
				}
				ui.Column(c.Key("id")).Gap(1).Grow(1).MinWidth(0).Children(func() {
					settingsLabel(c, a.appName(bundleID), 13.5, 600).TextColor(ink)
				})
				settingsButton(c, "rm-"+bundleID, l.SettingsClipboardRemove(), false, func() {
					a.removeExcludedApp(bundleID)
				}).Label(l.SettingsClipboardRemoveLabel(bundleID))
			})
		}
	}
	settingsGroupRows(c, "excluded", l.SettingsClipboardExcludedRow(), "", a.settingsBroken, rows)
}

// settingsAIUsagePage: the display switches and the two quota switches.
// Local usage is auto-detected and summed into one total, so there is no
// per-agent list and no source checkbox table.
func (a *shelf) settingsAIUsagePage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "usage", l.SettingsAISection(), "curious",
		heroUsageFrom, heroUsageTo, heroUsageGlow)

	settingsGroupRows(c, "ai", l.SettingsAIEnableGroup(), "", a.settingsBroken, func() {
		settingsToggleRow(c, "aiusage", l.SettingsAIEnableRow(), "", a.settings.AIUsage.Enabled,
			func(on bool) { a.setAIUsage(func(s *settings.Settings) { s.AIUsage.Enabled = on }) })
		settingsSep(c, "aiusage")
		settingsRow(c, "quota-show", l.SettingsAIQuotaDisplayRow(), "", func() {
			shown := 0
			if a.settings.AIUsage.QuotaLeft {
				shown = 1
			}
			sg := settingsSegmented(c, "quota-show", l.SettingsAIQuotaDisplayRow(), &shown,
				l.SettingsAIQuotaUsed(), l.SettingsAIQuotaLeft())
			if !a.settings.AIUsage.Enabled {
				sg.Disabled(true)
			}
			if sg.Changed() {
				a.setAIUsage(func(s *settings.Settings) { s.AIUsage.QuotaLeft = shown == 1 })
			}
		})
	})

	// The quota cards are only visible while the page is on; keep their
	// switches locked with the same rule the old source table used.
	quotaLocked := a.settingsBroken || !a.settings.AIUsage.Enabled
	settingsGroupRows(c, "quota", l.SettingsAIQuotaGroup(), "", quotaLocked, func() {
		rows := []struct {
			tool  aiusage.Tool
			on    bool
			apply func(*settings.Settings, bool)
		}{
			{aiusage.ToolClaude, a.settings.AIUsage.QuotaClaude,
				func(s *settings.Settings, on bool) { s.AIUsage.QuotaClaude = on }},
			{aiusage.ToolCodex, a.settings.AIUsage.QuotaCodex,
				func(s *settings.Settings, on bool) { s.AIUsage.QuotaCodex = on }},
		}
		for i, row := range rows {
			if i > 0 {
				settingsSep(c, "quota-"+string(row.tool))
			}
			apply := row.apply
			logo, color, mark := toolLogo(row.tool)
			settingsToggleRowMark(c, "quota-"+string(row.tool), toolName(row.tool),
				a.aiQuotaCaption(row.tool, l), logo, color, mark, row.on,
				func(on bool) {
					a.setAIUsage(func(s *settings.Settings) { apply(s, on) })
				})
		}
	})

	a.settingsAgentGroup(c)
	a.settingsApprovalGroup(c)

	settingsFootnote(c, "ai-foot", l.SettingsAILocalFoot())
}

// settingsAgentGroup is the Agent 完成提醒 card: one switch for every
// tool, then one row per detected tool with its configuration path, the
// state of Rakko's entry and the tool's own switch. A tool's switch is
// locked while the group switch is off: nothing may be written then.
func (a *shelf) settingsAgentGroup(c *ui.Context) {
	l := loc(c)
	f := a.agentFeature()
	f.refreshIfStale()
	settingsGroupRows(c, "agentnotify", l.SettingsAgentGroup(), "", a.settingsBroken, func() {
		settingsToggleRow(c, "agentnotify", l.SettingsAgentRow(), l.SettingsAgentCaption(),
			a.settings.AgentNotify.Enabled, a.setAgentNotify)
		tools := f.detected()
		if len(tools) == 0 {
			settingsSep(c, "agent-none")
			settingsRowLine(c, "agent-none").Children(func() {
				settingsRowText(c, l.SettingsAgentNone(), "")
			})
			return
		}
		for _, tool := range tools {
			settingsSep(c, "agent-"+string(tool))
			state, note, _ := f.settingRow(tool)
			toolOn := !a.settings.AgentNotify.ToolOff(string(tool))
			status, color := l.SettingsAgentNotInjected(), ink3
			switch state {
			case agentnotify.StateInjected:
				status = l.SettingsAgentInjected()
				if tool == agentnotify.ToolCodex {
					// Codex runs a hook only after the user trusted it once.
					status = l.SettingsAgentCodexTrust()
				}
			case agentnotify.StateUnsupported:
				status = l.SettingsAgentUnsupported()
			case agentnotify.StateError:
				status, color = l.SettingsAgentError(), danger
			}
			if state == agentnotify.StateNotInjected && a.settings.AgentNotify.Enabled && toolOn {
				// The switches say the entry should be there: the tool
				// rewrote its configuration and dropped it (Gemini's
				// settings.json). Flipping the row writes it again.
				status, color = l.SettingsAgentRewritten(), warning
			}
			caption := agentnotify.DisplayPath(f.home, tool)
			if state == agentnotify.StateError && note != "" {
				// The reason is more useful than the path the user can't use.
				caption = note
				if note == agentNeedsAppNote {
					caption = l.SettingsAgentNeedsApp()
				}
			}
			a.settingsAgentToolRow(c, "agent", tool, caption, status, color,
				toolOn, !a.settings.AgentNotify.Enabled, a.setAgentNotifyTool)
		}
	})
}

// settingsApprovalGroup is the Agent 审批 card: one switch for every tool,
// then one row per installed approvable tool. It is independent of the
// completion group above it.
func (a *shelf) settingsApprovalGroup(c *ui.Context) {
	l := loc(c)
	f := a.approvalFeature()
	f.refreshIfStale()
	settingsGroupRows(c, "approval", l.SettingsApprovalGroup(), "", a.settingsBroken, func() {
		settingsToggleRow(c, "approval", l.SettingsApprovalRow(), l.SettingsApprovalCaption(),
			a.settings.Approval.Enabled, a.setApproval)
		tools := f.detected()
		if len(tools) == 0 {
			settingsSep(c, "approval-none")
			settingsRowLine(c, "approval-none").Children(func() {
				settingsRowText(c, l.SettingsApprovalNone(), "")
			})
			return
		}
		for _, tool := range tools {
			settingsSep(c, "approval-"+string(tool))
			state, note := f.settingRow(tool)
			toolOn := !a.settings.Approval.ToolOff(string(tool))
			status, color := l.SettingsAgentNotInjected(), ink3
			switch state {
			case agentnotify.StateInjected:
				status = l.SettingsAgentInjected()
				if tool == agentnotify.ToolCodex {
					status = l.SettingsAgentCodexTrust()
				}
			case agentnotify.StateUnsupported:
				status = l.SettingsAgentUnsupported()
			case agentnotify.StateError:
				status, color = l.SettingsAgentError(), danger
			}
			if state == agentnotify.StateNotInjected && a.settings.Approval.Enabled && toolOn {
				status, color = l.SettingsAgentRewritten(), warning
			}
			caption := agentnotify.ApproveDisplayPath(f.home, tool)
			if state == agentnotify.StateError && note != "" {
				caption = note
				if note == agentNeedsAppNote {
					caption = l.SettingsAgentNeedsApp()
				}
			}
			a.settingsAgentToolRow(c, "approval", tool, caption, status, color,
				toolOn, !a.settings.Approval.Enabled, a.setApprovalTool)
		}
	})
}

// settingsAgentToolRow is one detected tool: its mark, name and
// configuration path on the left, the state of Rakko's entry, then the
// tool's own switch. The switch mirrors the settings value and is locked
// while the group switch is off.
func (a *shelf) settingsAgentToolRow(c *ui.Context, prefix string, tool agentnotify.Tool, caption, status string, color ui.Color, on, locked bool, apply func(agentnotify.Tool, bool)) {
	key := prefix + "-" + string(tool)
	row := ui.ToggleBase(c.Key("toggle-"+key), &on).FillWidth().AlignItems(ui.Center).
		Gap(14).MinHeight(settingsRowMinHeight).Padding(settingsRowPadY, settingsRowPadX).
		Label(tool.Name()).Disabled(locked)
	var sw ui.Element
	row.Children(func() {
		logo, tint, mark := agentLogo(tool)
		settingsMark(c, logo, tint, mark)
		settingsRowText(c, tool.Name(), caption)
		ui.Text(c, status).SingleLine().FontSize(11.5).FontWeight(500).TextColor(color)
		if !locked {
			// With the group switch off nothing may be written, so the row
			// carries no control that would do nothing.
			sw = ui.Switch(c, &on).Label(tool.Name())
		}
	})
	if !locked && (row.Changed() || sw.Changed()) {
		apply(tool, on)
	}
}

// aiQuotaCaption is the quota row's secondary line: the plan name when the
// last answer carried one, otherwise the account state. Empty before any
// answer, so a fresh install shows no invented text.
func (a *shelf) aiQuotaCaption(t aiusage.Tool, l locales.Messages) string {
	if a.usage == nil {
		return ""
	}
	latest := a.usage.quota[quotaIndex(t)].latest
	if latest.FetchedAt.IsZero() {
		return ""
	}
	switch latest.Status {
	case aiusage.QuotaOK:
		if q, _ := a.usage.quotaShown(t); q.Plan != "" {
			return q.Plan
		}
		return l.SettingsAIQuotaSignedIn()
	case aiusage.QuotaNotLoggedIn:
		return l.SettingsAIQuotaSignedOut()
	case aiusage.QuotaExpired:
		return l.SettingsAIQuotaExpired()
	case aiusage.QuotaNoCLI:
		return l.SettingsAIQuotaNoCLI()
	case aiusage.QuotaLimitReached:
		return l.SettingsAIQuotaLimit()
	default:
		return l.SettingsAIQuotaUnavailable()
	}
}

// settingsShortcutsPage: the editable global shortcuts, grouped by what
// they act on, and the two the panel answers, read-only.
func (a *shelf) settingsShortcutsPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "keys", l.SettingsShortcutsSection(), "neutral",
		heroKeysFrom, heroKeysTo, heroKeysGlow)

	settingsGroupRows(c, "keys-panel", l.SettingsKeysGlobalGroup(), "", a.settingsBroken, func() {
		a.settingsShortcutRow(c, shortcutToggle, l.SettingsShortcutTray(), "")
		settingsSep(c, "sc-toggle")
		a.settingsShortcutRow(c, shortcutClipboard, l.SettingsShortcutClipboard(), "")
		settingsSep(c, "sc-clipboard")
		a.settingsShortcutRow(c, shortcutOpenTray, l.SettingsShortcutOpenTray(), "")
	})

	settingsGroupRows(c, "keys-play", l.SettingsShortcutPlaybackGroup(), "", a.settingsBroken, func() {
		a.settingsShortcutRow(c, shortcutPlayPause, l.SettingsShortcutPlayPause(), "")
		settingsSep(c, "sc-play")
		a.settingsShortcutRow(c, shortcutPrevious, l.SettingsShortcutPrevious(), "")
		settingsSep(c, "sc-prev")
		a.settingsShortcutRow(c, shortcutNext, l.SettingsShortcutNext(), "")
	})

	settingsGroupRows(c, "keys-tools", l.SettingsShortcutToolsGroup(), "", a.settingsBroken, func() {
		a.settingsShortcutRow(c, shortcutCapture, l.SettingsShortcutOCR(), "")
		settingsSep(c, "sc-capture")
		a.settingsShortcutRow(c, shortcutScreenshot, l.SettingsShortcutScreenshot(), "")
		settingsSep(c, "sc-shot")
		a.settingsShortcutRow(c, shortcutTools, l.SettingsShortcutTools(), "")
		settingsSep(c, "sc-tools")
		a.settingsShortcutRow(c, shortcutUsage, l.SettingsShortcutUsage(), "")
		settingsSep(c, "sc-usage")
		a.settingsShortcutRow(c, shortcutAwake, l.SettingsShortcutAwake(), "")
		settingsSep(c, "sc-awake")
		a.settingsShortcutRow(c, shortcutNote, l.SettingsShortcutNewNote(), "")
		settingsSep(c, "sc-note")
		a.settingsShortcutRow(c, shortcutPet, l.SettingsShortcutPet(), "")
		settingsSep(c, "sc-pet")
		a.settingsShortcutRow(c, shortcutAppSwitch, l.SettingsShortcutAppSwitch(), "")
	})

	settingsGroup(c, "keys-notch", l.SettingsKeysNotchGroup(), "", func() {
		settingsReadonlyShortcutRow(c, "sc-esc", l.SettingsShortcutEscRow(), l.SettingsShortcutEscCaption(),
			[]string{"Esc"})
		settingsSep(c, "sc-esc")
		settingsReadonlyShortcutRow(c, "sc-select", l.SettingsShortcutSelectRow(), "",
			[]string{"↑", "↓", "↩"})
	})
}

// settingsShortcutRow is one editable shortcut: the label and caption on the
// left, the keycaps on the right. The row itself starts the recording, as a
// switch row toggles. A row that could not claim its combination says so in
// place of its caption. A key that types text waits here as a question with
// its two answers.
func (a *shelf) settingsShortcutRow(c *ui.Context, kind shortcutKind, label, caption string) {
	l := loc(c)
	if p := a.shortcutConfirm; p != nil && p.kind == kind {
		ui.Row(c.Key(fmt.Sprintf("sc-confirm-%d", kind))).FillWidth().Radius(0).
			MinHeight(settingsRowMinHeight).AlignItems(ui.Center).Gap(8).
			Padding(settingsRowPadY, settingsRowPadX).Children(func() {
			ui.Text(c, l.SettingsShortcutConfirm(shortcutLabel(p.acc))).FontSize(12).
				TextColor(ink2).Grow(1).MinWidth(0)
			settingsConfirmButton(c, fmt.Sprintf("sc-use-%d", kind), l.SettingsShortcutUseAnyway(), true, a.confirmShortcut)
			settingsConfirmButton(c, fmt.Sprintf("sc-drop-%d", kind), l.SettingsShortcutCancel(), false, a.cancelShortcutConfirm)
		})
		return
	}
	rowCaption, captionColor := caption, ink3
	if a.shortcutBusy[kind] {
		rowCaption, captionColor = l.SettingsShortcutBusy(), danger
	}
	recording := a.settingsRecording == kind
	row := ui.ButtonBase(c.Key(fmt.Sprintf("sc-%d", kind))).FillWidth().Radius(0).
		MinHeight(settingsRowMinHeight).AlignItems(ui.Center).Justify(ui.Start).
		Gap(14).Padding(settingsRowPadY, settingsRowPadX).Label(label)
	if acc := a.shortcutAcc(kind); acc != "" {
		// VoiceOver says the combination the row shows.
		row.Description(shortcutLabel(acc))
	}
	row.Children(func() {
		ui.Column(c.Key("text")).Gap(2).Grow(1).MinWidth(0).Children(func() {
			settingsLabel(c, label, 13.5, 600).TextColor(ink)
			if rowCaption != "" {
				ui.Text(c, rowCaption).FontSize(11.5).TextColor(captionColor)
			}
		})
		ui.Row(c.Key("keys")).AlignItems(ui.Center).Gap(4).Children(func() {
			if recording {
				ui.Box(c.Key("rec")).Height(26).Radius(7).Padding(0, 8, 0, 8).
					Border(1.5, accent).Center().Children(func() {
					ui.Text(c, l.SettingsShortcutRecording()).SingleLine().FontSize(11.5).TextColor(accent)
				})
				return
			}
			if tokens, ok := shortcutTokens(a.shortcutAcc(kind)); ok {
				for i, token := range tokens {
					settingsKeycap(c, fmt.Sprintf("k%d", i), token, false)
				}
				return
			}
			ui.Box(c.Key("none")).Height(26).Radius(7).Padding(0, 8, 0, 8).
				Border(1, ui.RGBA(255, 255, 255, 0.12)).Center().Children(func() {
				ui.Text(c, l.SettingsShortcutNoShortcut()).SingleLine().FontSize(11.5).TextColor(ink3)
			})
		})
	})
	if row.Clicked() {
		a.startShortcutRecording(kind)
	}
}

// settingsConfirmButton is the answer button of the shortcut question. It
// takes the pointer itself (HandleInput) rather than as a ButtonBase: the
// window's click-anywhere-else cancel listens on an ancestor, and only an
// element that handles its own input stands in front of it.
func settingsConfirmButton(c *ui.Context, key, label string, primary bool, fn func()) ui.Element {
	b := ui.Row(c.Key("btn-"+key)).Height(28).Radius(8).Padding(0, 12, 0, 12).
		AlignItems(ui.Center).Label(label)
	fg := ink
	if primary {
		b.Background(ink)
		fg = ui.RGB(0, 0, 0)
	} else {
		b.Background(setCtl)
		if b.Hovered() {
			b.Background(setCtlHover)
		}
	}
	b.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			return true
		case ui.InputPointerUp:
			fn()
			return true
		}
		return false
	})
	b.Children(func() {
		t := ui.Text(c, label).SingleLine().FontSize(12.5).TextColor(fg)
		if primary {
			t.FontWeight(600)
		}
	})
	return b
}

// settingsReadonlyShortcutRow shows a shortcut the panel answers itself.
func settingsReadonlyShortcutRow(c *ui.Context, key, label, caption string, tokens []string) {
	settingsRowLine(c, key).Children(func() {
		settingsRowText(c, label, caption)
		ui.Row(c.Key("keys")).AlignItems(ui.Center).Gap(4).Children(func() {
			for i, token := range tokens {
				settingsKeycap(c, fmt.Sprintf("%s-%d", key, i), token, false)
			}
		})
	})
}

// settingsKeycap is one 26pt key: #1F2024, 7pt corners, a hairline of light
// along the bottom, 12.5pt semibold label.
func settingsKeycap(c *ui.Context, key, label string, on bool) {
	box := ui.Box(c.Key("cap-"+key)).Height(26).MinWidth(26).Radius(7).
		Padding(0, 7, 0, 7).Center().Background(setCtl)
	if on {
		box.Border(1.5, accent)
	} else {
		box.InsetShadow(0, -1, 0, 0, ui.RGBA(255, 255, 255, 0.06))
	}
	box.Children(func() {
		ui.Text(c, label).SingleLine().FontSize(12.5).FontWeight(600).TextColor(ink)
	})
}

// settingsPermissionsPage: every permission with its live state, and the
// one switch that walks the user through whatever is missing — the same
// flow the tray menu's item starts.
func (a *shelf) settingsPermissionsPage(c *ui.Context) {
	l := loc(c)
	permissions := systemPermissionSet(l)
	missing := missingPermissions(permissions)
	settingsHero(c, "perm", l.PermTitle(), "neutral",
		heroPermFrom, heroPermTo, heroPermGlow)

	settingsGroup(c, "perm", l.SettingsPermNeeded(), "", func() {
		for i, p := range permissions {
			if i > 0 {
				settingsSep(c, "perm-"+p.id)
			}
			granted := p.enabled()
			row := settingsRowLine(c, "perm-"+p.id).Tooltip(p.detail)
			row.Children(func() {
				settingsRowText(c, p.short, "")
				ui.Row(c.Key("state")).AlignItems(ui.Center).Gap(6).Children(func() {
					dot := success
					text := l.SettingsPermOn()
					if !granted {
						dot, text = warning, l.SettingsPermOff()
					}
					ui.Box(c.Key("dot")).Size(7, 7).Radius(4).Background(dot)
					ui.Text(c, text).SingleLine().FontSize(12).TextColor(ink2)
				})
			})
		}
		settingsSep(c, "perm-switch")
		a.settingsPermissionSwitch(c, permissions, missing)
	})
}

// settingsAboutPage: the big otter, the version, its actions, the 更新
// group the version belongs to, and the privacy and source notes.
func (a *shelf) settingsAboutPage(c *ui.Context) {
	l := loc(c)

	ui.Row(c.Key("about")).FillWidth().AlignItems(ui.Center).Gap(18).Padding(4, 4, 4, 4).Children(func() {
		if b := otterImage("happy"); b != nil {
			w, h := b.Size()
			width := float32(150)
			height := width * float32(h) / float32(w)
			ui.Image(c.Key("otter"), b).Size(width, height).Fit(ui.Contain)
		}
		ui.Column(c.Key("about-text")).Gap(2).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, appName).SingleLine().FontSize(24).FontWeight(700).TextColor(ink)
			ui.Text(c, l.SettingsAboutVersion(appVersion(), runtime.GOARCH)).SingleLine().
				FontSize(12).TextColor(ink3).Padding(2, 0, 0, 0)
			ui.Row(c.Key("about-actions")).AlignItems(ui.Center).Gap(8).Padding(14, 0, 0, 0).Children(func() {
				// The one check action on this page: the 更新 group below
				// only reports what the last one found.
				busy := a.updateChecking || a.updateInstalling || a.updateUnavailableReason() != ""
				settingsButton(c, "update", l.SettingsUpdateCheckRow(), false, func() { a.checkUpdates(true) }).
					Disabled(busy)
				settingsButton(c, "data", l.SettingsOpenDataFolder(), false, func() {
					if err := openPath(a.dataDir); err != nil {
						a.report(err)
					}
				})
			})
		})
	})

	settingsGroup(c, "update", l.SettingsUpdateGroup(), "", func() {
		ui.Column(c.Key("update-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsToggleRow(c, "autocheck", l.SettingsAutoCheckRow(), "",
				a.settings.AutoCheckUpdates, a.setAutoCheckUpdates)
			settingsSep(c, "autocheck")
			settingsToggleRow(c, "autoinstall", l.UpdateAutoInstallSettingsRow(), "",
				a.settings.UpdateAutoInstall, a.setUpdateAutoInstall)
			settingsSep(c, "autoinstall")
			settingsRow(c, "check", l.SettingsUpdateCheckRow(), a.updateCaption(l), func() {})
			if a.updateInstalled && a.update != nil {
				// The ready update lives here, not under the search bar:
				// the restart is one click from where updates are managed.
				settingsSep(c, "ready")
				settingsRow(c, "ready", l.UpdateReady(), "", func() {
					settingsButton(c, "restart", l.PanelRestart(), true, relaunchApp).Label(l.PanelRestartLabel())
				})
			}
		})
	})

	settingsGroup(c, "privacy", l.SettingsAboutPrivacyGroup(), "", func() {
		settingsRow(c, "privacy", l.SettingsAboutPrivacy(), l.SettingsAboutPrivacyCaption(), func() {})
		settingsSep(c, "source")
		settingsRowLine(c, "source").Tooltip(l.SettingsAboutSourceLicense()).Children(func() {
			settingsRowText(c, l.SettingsAboutSource(), l.SettingsAboutSourceCaption())
			settingsButton(c, "source-open", l.SettingsAboutSourceOpen(), false, func() {
				if err := openExternal(sourceURL); err != nil {
					a.report(err)
				}
			})
		})
	})
}

// sourceURL is the project's public repository, the one the 关于 page links
// to.
const sourceURL = "https://github.com/henryyu333/rakko"

// updateCaption is the 检查更新 row's second line: the last check's time and
// outcome, or what is happening now.
func (a *shelf) updateCaption(l locales.Messages) string {
	if reason := a.updateUnavailableReason(); reason != "" {
		return reason
	}
	switch {
	case a.updateChecking:
		return l.UpdateChecking()
	case a.updateInstalling && a.update != nil:
		return l.UpdateDownloading(a.update.Version)
	case a.updateInstalled && a.update != nil:
		return l.UpdateInstalled(a.update.Version)
	case a.updateLastCheck.IsZero():
		return l.SettingsUpdateNever()
	}
	when := a.updateWhen(l, a.updateLastCheck, time.Now())
	var result string
	switch a.updateLastResult {
	case updateResultLatest:
		result = l.SettingsUpdateResultLatest()
	case updateResultAvailable:
		result = l.SettingsUpdateResultAvailable(a.updateLastVersion)
	case updateResultFailed:
		result = l.SettingsUpdateResultFailed()
	case updateResultDisabled:
		result = l.SettingsUpdateResultDisabled()
	default:
		result = l.SettingsUpdateResultLatest()
	}
	return l.SettingsUpdateLast(when, result)
}

// updateWhen says a check's time as 今天 09:12, 昨天 21:05 or a date, in the
// window's language. Pure; unit-tested.
func (a *shelf) updateWhen(l locales.Messages, at, now time.Time) string {
	y1, m1, d1 := at.Date()
	y2, m2, d2 := now.Date()
	clock := at.Format("15:04")
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return l.SettingsUpdateToday(clock)
	case y1 == y2 && m1 == m2 && d1 == d2-1:
		return l.SettingsUpdateYesterday(clock)
	}
	return l.SettingsUpdateOn(at.Format("2006-01-02 15:04"))
}
