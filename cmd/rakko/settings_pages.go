package main

// The pages of the Settings window, matching design/explore/settings-v6.html
// ("小岛"): one centered single-column flow per page without page titles or
// hero banners, sharing identical geometry and typography across macOS and
// Windows.

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
	"rakko/internal/aiusage"
	"rakko/internal/settings"
	"rakko/locales"
)

// settingsGeneralPage: 启动 (open at login, language) and 搜索 (default
// search mode). Only the login item survives a broken settings.json.
func (a *shelf) settingsGeneralPage(c *ui.Context) {
	l := loc(c)
	settingsFirstGroup(c, "startup", l.SettingsStartupGroup(), "", func() {
		loginCap, capColor := "", settingsInk3
		if a.loginErr != "" {
			loginCap, capColor = a.loginErr, danger
		}
		settingsToggleRowCap(c, "login", osText(l.SettingsLoginRow(), l.SettingsLoginRowWin()), loginCap, capColor, a.loginOn, a.applyOpenAtLogin)
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

	settingsGroupRows(c, "search", l.SettingsSearchGroup(), "", a.settingsBroken, func() {
		settingsRow(c, "searchmode", l.SettingsDefaultSearchMode(), "", func() {
			idx := searchModeIndex(a.settings.SearchMode)
			if sg := settingsSegmented(c, "searchmode", l.SettingsDefaultSearchMode(), &idx,
				l.SearchModeAll(), l.SearchModeApps(), l.SearchModeFiles()); sg.Changed() {
				a.setSearchMode(searchModeAt(idx))
			}
		})
	})
}

// settingsNotchPage: 外形 (two preview tiles + optional island position row),
// 桌宠, 实时活动, and 窗口 snapping.
func (a *shelf) settingsNotchPage(c *ui.Context) {
	l := loc(c)
	settingsFirstGroupRows(c, "shape", l.SettingsShapeGroup(), "", a.settingsBroken, func() {
		islandSelected := a.settings.ShellStyle.Normalized() == settings.ShellStyleIsland
		ui.Row(c.Key("shape-tiles")).FillWidth().Padding(12).Gap(10).Children(func() {
			settingsShapeTile(c, "notch", osText(l.ShellStyleNotch(), l.ShellStyleNotchWin()), false, !islandSelected, func() {
				a.setShellStyle(settings.ShellStyleNotch)
			})
			settingsShapeTile(c, "island", l.ShellStyleIsland(), true, islandSelected, func() {
				a.setShellStyle(settings.ShellStyleIsland)
			})
		})
		if a.island {
			settingsSep(c, "islandpos")
			settingsRow(c, "islandpos", l.SettingsIslandPosRow(), "", func() {
				settingsButton(c, "island-reset", l.SettingsIslandReset(), false, a.resetIslandPosition)
			})
		}
	})

	if opts := a.displaySettingOptions(l); len(opts) > 0 {
		settingsGroupRows(c, "display", l.SettingsDisplayGroup(), "", a.settingsBroken, func() {
			for i, opt := range opts {
				opt := opt
				if i > 0 {
					settingsSep(c, "disp-"+opt.key)
				}
				row := ui.ButtonBase(c.Key("disp-"+opt.key)).FillWidth().Radius(0).
					MinHeight(settingsRowMinHeight).AlignItems(ui.Center).Justify(ui.Start).
					Gap(14).Padding(settingsRowPadY, settingsRowPadX).Label(opt.a11yLabel)
				if row.Hovered() {
					row.Background(settingsRowHoverBg)
				}
				row.Children(func() {
					ui.Row(c.Key("lbl")).Grow(1).MinWidth(0).AlignItems(ui.Baseline).Gap(4).Children(func() {
						settingsLabel(c, opt.name, 13, 400).TextColor(ink).MinWidth(0).Shrink(1)
						if opt.suffix != "" {
							settingsText(c, opt.suffix, 12).SingleLine().TextColor(settingsInk3).Shrink(0)
						}
					})
					ui.Box(c.Key("chk")).Size(18, 18).Shrink(0).Center().Children(func() {
						if opt.selected {
							ui.Icon(c.Key("tick"), checkSymbol16).Size(14, 14).TextColor(accent)
						}
					})
				})
				if row.Clicked() {
					a.setDisplay(opt.key)
				}
			}
		})
	}

	if petArt() != nil {
		settingsGroupRows(c, "pet", l.SettingsPetGroup(), "", a.settingsBroken, func() {
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
	}

	settingsGroupRows(c, "live", l.SettingsLiveSection(), "", a.settingsBroken, func() {
		settingsToggleRow(c, "live-charging", l.SettingsLiveCharging(), "", a.settings.LiveActivities.Charging,
			func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Charging = on }) })
		settingsSep(c, "live-charging")
		settingsToggleRow(c, "live-headphones", l.SettingsLiveHeadphones(), "", a.settings.LiveActivities.Headphones,
			func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Headphones = on }) })
		settingsSep(c, "live-headphones")
		settingsToggleRow(c, "live-downloads", l.SettingsLiveDownloads(), "", a.settings.LiveActivities.Downloads,
			func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Downloads = on }) })
		settingsSep(c, "live-downloads")
		settingsToggleRow(c, "live-nowplaying", l.NowPlaying(), "", a.settings.LiveActivities.NowPlaying,
			func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.NowPlaying = on }) })
	})

	settingsGroupRows(c, "window", l.SettingsWindowGroup(), "", a.settingsBroken, func() {
		settingsToggleRow(c, "snap", osText(l.SettingsSnapRow(), l.SettingsSnapRowWin()), "",
			a.settings.SnapWindows, a.setSnapWindows)
	})

	a.settingsSysMonGroup(c)
}

func (a *shelf) settingsSysMonGroup(c *ui.Context) {
	l := loc(c)
	s := a.settings.SysMon.Normalized()

	primaryOpts := []string{settings.SysMonMetricCPU, settings.SysMonMetricMem, settings.SysMonMetricNet}
	leftOpts := []string{settings.SysMonWingCPU, settings.SysMonWingMem, settings.SysMonWingNetDown, settings.SysMonWingDisk, settings.SysMonWingBattery}
	rightOpts := []string{settings.SysMonWingNetDown, settings.SysMonWingNetBoth, settings.SysMonWingMem, settings.SysMonWingCPU, settings.SysMonWingBattery}

	settingsGroupRows(c, "sysmon", l.SettingsSysMonGroup(), "", a.settingsBroken, func() {
		settingsRow(c, "sysmon-primary", l.SettingsSysMonPrimaryRow(), "", func() {
			idx := max(0, slices.Index(primaryOpts, s.PrimaryMetric))
			if sg := settingsSegmented(c, "sysmon-primary", l.SettingsSysMonPrimaryRow(), &idx,
				l.SysMonCPU(), l.SysMonMem(), l.SysMonNet()); sg.Changed() {
				chosen := primaryOpts[idx]
				a.setSysMon(func(m *settings.SysMonSettings) { m.PrimaryMetric = chosen })
			}
		})
		settingsSep(c, "sysmon-primary")
		settingsToggleRow(c, "sysmon-top-procs", l.SettingsSysMonTopProcsRow(), "", s.ShowTopProcs,
			func(on bool) { a.setSysMon(func(m *settings.SysMonSettings) { m.ShowTopProcs = on }) })
		settingsSep(c, "sysmon-top-procs")
		settingsToggleRow(c, "sysmon-wings", osText(l.SettingsSysMonWingsRow(), l.SettingsSysMonWingsRowWin()), "", s.WingsEnabled,
			func(on bool) { a.setSysMon(func(m *settings.SysMonSettings) { m.WingsEnabled = on }) })
		settingsSep(c, "sysmon-wings")
		settingsRow(c, "sysmon-wing-left", l.SettingsSysMonWingLeftRow(), "", func() {
			idx := max(0, slices.Index(leftOpts, s.WingLeft))
			sg := settingsSegmented(c, "sysmon-wing-left", l.SettingsSysMonWingLeftRow(), &idx,
				l.SysMonCPU(), l.SysMonMem(), l.SysMonDown(), l.SysMonDisk(), l.SysMonBattery())
			if !s.WingsEnabled {
				sg.Disabled(true)
			}
			if sg.Changed() {
				chosen := leftOpts[idx]
				a.setSysMon(func(m *settings.SysMonSettings) { m.WingLeft = chosen })
			}
		})
		settingsSep(c, "sysmon-wing-left")
		settingsRow(c, "sysmon-wing-right", l.SettingsSysMonWingRightRow(), "", func() {
			idx := max(0, slices.Index(rightOpts, s.WingRight))
			sg := settingsSegmented(c, "sysmon-wing-right", l.SettingsSysMonWingRightRow(), &idx,
				l.SysMonDown(), l.SysMonUpDown(), l.SysMonMem(), l.SysMonCPU(), l.SysMonBattery())
			if !s.WingsEnabled {
				sg.Disabled(true)
			}
			if sg.Changed() {
				chosen := rightOpts[idx]
				a.setSysMon(func(m *settings.SysMonSettings) { m.WingRight = chosen })
			}
		})
		settingsSep(c, "sysmon-wing-right")
		settingsRow(c, "sysmon-interval", l.SettingsSysMonIntervalRow(), "", func() {
			idx := 0
			if s.IntervalSec == 2 {
				idx = 1
			}
			if sg := settingsSegmented(c, "sysmon-interval", l.SettingsSysMonIntervalRow(), &idx,
				l.SettingsSysMonInterval1s(), l.SettingsSysMonInterval2s()); sg.Changed() {
				sec := 1
				if idx == 1 {
					sec = 2
				}
				a.setSysMon(func(m *settings.SysMonSettings) { m.IntervalSec = sec })
			}
		})
	})
}

func settingsShapeTile(c *ui.Context, key, label string, island, selected bool, onSelect func()) {
	b := ui.ButtonBase(c.Key("shape-"+key)).Grow(1).MinWidth(0).Radius(10).
		Background(settingsTileBg).Padding(10, 10, 8, 10).Label(label)
	if selected {
		b.InsetShadow(0, 0, 0, 2, accent)
	} else {
		b.InsetShadow(0, 0, 0, 1, settingsTileRim)
	}
	labelColor := ink2
	if selected {
		labelColor = ink
	}
	b.Children(func() {
		ui.Column(c.Key("tile")).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			scr := ui.Box(c.Key("scr")).FillWidth().Height(58).Radius(7).Clip().
				LinearGradient(ui.LinearGradient{
					Angle: 165,
					Stops: []ui.GradientStop{
						{Color: ui.Hex("#36456a"), At: 0},
						{Color: ui.Hex("#1d2740"), At: 1},
					},
				})
			scr.Children(func() {
				ui.Box(c.Key("bar")).Absolute().Top(0).Left(0).Right(0).Height(8).
					Background(ui.RGBA(0, 0, 0, 0.22))
				if island {
					ui.Row(c.Key("i-wrap")).Absolute().Top(6).Left(0).Right(0).Justify(ui.Center).Children(func() {
						ui.Box(c.Key("i")).Size(46, 15).Background(ui.RGB(0, 0, 0)).Radius(8)
					})
				} else {
					ui.Row(c.Key("n-wrap")).Absolute().Top(0).Left(0).Right(0).Justify(ui.Center).Children(func() {
						ui.Box(c.Key("n")).Size(52, 14).Background(ui.RGB(0, 0, 0)).Radius(0, 0, 7, 7)
					})
				}
			})
			settingsText(c, label, 12).SingleLine().TextColor(labelColor)
		})
	})
	if b.Clicked() {
		onSelect()
	}
}

func (a *shelf) setLiveActivity(mutate func(*settings.Settings)) {
	wasNowPlaying := a.settings.LiveActivities.NowPlaying
	next := a.settings
	mutate(&next)
	if !a.applySettings(next) {
		return
	}
	if wasNowPlaying && !next.LiveActivities.NowPlaying && a.np != nil {
		a.np.stop()
	}
	if wasNowPlaying != next.LiveActivities.NowPlaying {
		a.invalidatePins("playing")
	}
}

// settingsTrayPage: 整理 (auto-clean retention and remove-after-drag).
func (a *shelf) settingsTrayPage(c *ui.Context) {
	l := loc(c)
	settingsFirstGroupRows(c, "tidy", l.SettingsTidyGroup(), "", a.settingsBroken, func() {
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

// settingsClipboardPage: 记录 (retention limit) and 不记录这些应用.
func (a *shelf) settingsClipboardPage(c *ui.Context) {
	l := loc(c)
	settingsFirstGroupRows(c, "record", l.SettingsClipGroup(), "", a.settingsBroken, func() {
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
			settingsRowLine(c, "app-"+bundleID).Children(func() {
				if icon := settingsAppIcon(bundleID); icon != nil {
					ui.Image(c.Key("ic"), icon).Size(20, 20).Radius(5)
				} else {
					ui.Box(c.Key("ic-slot")).Size(20, 20).Center().Children(func() {
						ui.Icon(c.Key("ic"), appGlyph).Size(20, 20).TextColor(settingsInk3)
					})
				}
				ui.Column(c.Key("id")).Gap(1).Grow(1).MinWidth(0).Children(func() {
					settingsLabel(c, a.appName(bundleID), 13, 400).TextColor(ink)
				})
				settingsButton(c, "rm-"+bundleID, l.SettingsClipboardRemove(), false, func() {
					a.removeExcludedApp(bundleID)
				}).Label(l.SettingsClipboardRemoveLabel(bundleID))
			})
		}
	}
	settingsGroupRows(c, "excluded", l.SettingsClipboardExcludedRow(), "", a.settingsBroken, rows)
}

// settingsAIUsagePage: 显示, 套餐额度, Agent 完成提醒, Agent 审批, and footnote.
func (a *shelf) settingsAIUsagePage(c *ui.Context) {
	l := loc(c)
	settingsFirstGroupRows(c, "ai", l.SettingsAIEnableGroup(), "", a.settingsBroken, func() {
		settingsToggleRow(c, "aiusage", osText(l.SettingsAIEnableRow(), l.SettingsAIEnableRowWin()), "", a.settings.AIUsage.Enabled,
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

	if a.usage != nil {
		a.usage.refreshSources(false)
	}
	quotaLocked := a.settingsBroken || !a.settings.AIUsage.Enabled
	tools := a.settings.AIUsage.OrderedQuotaTools()

	settingsGroupRows(c, "quota", l.SettingsAIQuotaGroup(), l.SettingsAIQuotaOrderFoot(), quotaLocked, func() {
		if (a.aiQuotaDragPending || a.aiQuotaDragActive) && (quotaLocked || !dragLeftDown()) {
			a.aiQuotaDragPending = false
			a.aiQuotaDragActive = false
			a.aiQuotaDragTool = ""
		}
		for i, tool := range tools {
			if i > 0 {
				settingsSep(c, "quota-"+string(tool))
			}
			t := tool
			panelOn := a.settings.AIUsage.QuotaCardEnabled(t)

			row := settingsRowLine(c, "quota-"+string(t)).Label(settingsQuotaToolTitle(t, l))
			row.Children(func() {
				handle := ui.Row(c.Key("drag-" + string(t))).AlignItems(ui.Center).Gap(14).Grow(1).MinWidth(0)
				handle.HandleInput(func(ev ui.InputEvent) bool {
					if quotaLocked {
						return false
					}
					switch ev.Kind {
					case ui.InputPointerDown:
						if ev.Button == 0 {
							a.aiQuotaDragPending = true
							a.aiQuotaDragActive = false
							a.aiQuotaDragTool = t
							a.aiQuotaDragDownX = ev.X
							a.aiQuotaDragDownY = ev.Y
							return true
						}
					case ui.InputPointerMove:
						if !a.aiQuotaDragPending && !a.aiQuotaDragActive {
							return false
						}
						if !dragLeftDown() {
							a.aiQuotaDragPending = false
							a.aiQuotaDragActive = false
							a.aiQuotaDragTool = ""
							return false
						}
						if a.aiQuotaDragPending {
							dx := ev.X - a.aiQuotaDragDownX
							dy := ev.Y - a.aiQuotaDragDownY
							const quotaDragSlop = 5
							if dx*dx+dy*dy < quotaDragSlop*quotaDragSlop {
								return true
							}
							a.aiQuotaDragActive = true
							a.aiQuotaDragPending = false
						}
						if a.aiQuotaDragActive {
							curTools := a.settings.AIUsage.OrderedQuotaTools()
							curIdx := slices.Index(curTools, a.aiQuotaDragTool)
							if curIdx < 0 {
								a.aiQuotaDragActive = false
								a.aiQuotaDragTool = ""
								return false
							}
							const rowStep = float32(settingsRowMinHeight + 1)
							target := curIdx
							if ev.Y < -rowStep/2 && curIdx > 0 {
								steps := max(1, int((-ev.Y+rowStep/2)/rowStep))
								target = max(0, curIdx-steps)
							} else if ev.Y > float32(settingsRowMinHeight)+rowStep/2 && curIdx < len(curTools)-1 {
								steps := max(1, int((ev.Y-float32(settingsRowMinHeight)+rowStep/2)/rowStep))
								target = min(len(curTools)-1, curIdx+steps)
							}
							if target != curIdx {
								a.setAIUsage(func(s *settings.Settings) { s.AIUsage.MoveQuotaTool(curIdx, target) })
							}
						}
						return true
					case ui.InputPointerUp:
						if a.aiQuotaDragPending || a.aiQuotaDragActive {
							a.aiQuotaDragPending = false
							a.aiQuotaDragActive = false
							a.aiQuotaDragTool = ""
							return true
						}
					}
					return false
				})
				handle.Children(func() {
					logo, color, mark := toolLogo(t)
					settingsMark(c, logo, color, mark)
					settingsLabel(c, settingsQuotaToolTitle(t, l), 13, 400).TextColor(ink).Grow(1).MinWidth(0)
				})
				a.settingsQuotaSourceControl(c, t, quotaLocked)
				on := panelOn
				sw := settingsSwitch(c.Key("panel-"+string(t)), &on, settingsQuotaToolTitle(t, l)+" "+l.SettingsAIQuotaGroup())
				if !quotaLocked && sw.Changed() {
					a.setAIUsage(func(s *settings.Settings) { s.AIUsage.SetQuotaCard(t, on) })
				}
			})

			if a.aiQuotaEditTool == string(t) {
				a.settingsQuotaKeyEditor(c, t)
			}
		}
	})

	a.settingsAgentGroup(c)
	a.settingsApprovalGroup(c)

	settingsFootnote(c, "ai-foot", l.SettingsAILocalFoot())
}

func (a *shelf) settingsAgentGroup(c *ui.Context) {
	l := loc(c)
	f := a.agentFeature()
	f.refreshIfStale()
	settingsGroupRows(c, "agentnotify", l.SettingsAgentGroup(), "", a.settingsBroken, func() {
		settingsToggleRow(c, "agentnotify", osText(l.SettingsAgentRow(), l.SettingsAgentRowWin()), "",
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
			status, color, caption, capColor := agentRowStatus(l, tool, state, note,
				a.settings.AgentNotify.Enabled, toolOn, agentnotify.DisplayPath(f.home, tool))
			a.settingsAgentToolRow(c, "agent", tool, caption, capColor, status, color,
				toolOn, !a.settings.AgentNotify.Enabled, a.setAgentNotifyTool)
		}
	})
}

func (a *shelf) settingsApprovalGroup(c *ui.Context) {
	l := loc(c)
	f := a.approvalFeature()
	f.refreshIfStale()
	settingsGroupRows(c, "approval", l.SettingsApprovalGroup(), "", a.settingsBroken, func() {
		settingsToggleRow(c, "approval", osText(l.SettingsApprovalRow(), l.SettingsApprovalRowWin()), "",
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
			status, color, caption, capColor := agentRowStatus(l, tool, state, note,
				a.settings.Approval.Enabled, toolOn, agentnotify.ApproveDisplayPath(f.home, tool))
			a.settingsAgentToolRow(c, "approval", tool, caption, capColor, status, color,
				toolOn, !a.settings.Approval.Enabled, a.setApprovalTool)
		}
	})
}

// agentRowStatus is one detected tool's row state, shared by the 提醒 and
// 审批 groups: the status line and its color, plus the caption under the
// tool name and its color. groupOn is that group's master switch, which
// turns an uninjected tool into the rewritten warning.
func agentRowStatus(l locales.Messages, tool agentnotify.Tool, state agentnotify.State, note string, groupOn, toolOn bool, caption string) (status string, color ui.Color, cap string, capColor ui.Color) {
	status, color = l.SettingsAgentNotInjected(), settingsInk3
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
	if state == agentnotify.StateNotInjected && groupOn && toolOn {
		status, color = l.SettingsAgentRewritten(), warning
	}
	cap, capColor = caption, settingsInk3
	if state == agentnotify.StateError && note != "" {
		cap, capColor = note, danger
		if note == agentNeedsAppNote {
			cap = osText(l.SettingsAgentNeedsApp(), l.SettingsAgentNeedsAppWin())
		}
	}
	return status, color, cap, capColor
}

func (a *shelf) settingsAgentToolRow(c *ui.Context, prefix string, tool agentnotify.Tool, caption string, capColor ui.Color, status string, color ui.Color, on, locked bool, apply func(agentnotify.Tool, bool)) {
	key := prefix + "-" + string(tool)
	row := ui.ToggleBase(c.Key("toggle-"+key), &on).FillWidth().AlignItems(ui.Center).
		Gap(14).MinHeight(settingsRowMinHeight).Padding(settingsRowPadY, settingsRowPadX).
		Label(tool.Name()).Disabled(locked)
	if !locked && row.Hovered() {
		row.Background(settingsRowHoverBg)
	}
	var sw ui.Element
	row.Children(func() {
		logo, tint, mark := agentLogo(tool)
		markBox := ui.Box(c.Key("lg-box")).Size(settingsMarkSlot, settingsMarkSlot)
		if locked {
			markBox.Opacity(0.4)
		}
		markBox.Children(func() {
			settingsMark(c, logo, tint, mark)
		})
		textCol := ui.Column(c.Key("text")).Gap(1).Grow(1).MinWidth(0)
		if locked {
			textCol.Opacity(0.4)
		}
		textCol.Children(func() {
			settingsLabel(c, tool.Name(), 13, 400).TextColor(ink)
			if caption != "" {
				settingsText(c, caption, 11.5).TextColor(capColor)
			}
		})
		settingsText(c, status, 11.5).SingleLine().TextColor(color)
		if !locked {
			sw = settingsSwitch(c, &on, tool.Name())
		}
	})
	if !locked && (row.Changed() || sw.Changed()) {
		apply(tool, on)
	}
}

func settingsQuotaToolTitle(t aiusage.Tool, l locales.Messages) string {
	if t == aiusage.ToolDeepSeek {
		return "DeepSeek " + l.UsageBalanceLabel()
	}
	return toolName(t)
}

func (a *shelf) settingsQuotaSourceControl(c *ui.Context, t aiusage.Tool, locked bool) {
	l := loc(c)
	var src aiusage.QuotaSource
	var planSuffix string
	if a.usage != nil {
		src = a.usage.sourceOf(t)
		if q, _ := a.usage.quotaShown(t); q.Plan != "" {
			planSuffix = " · " + q.Plan
		}
	}
	canPaste := t != aiusage.ToolCodex && t != aiusage.ToolClaude
	switch src.Kind {
	case aiusage.SourceLocalLogin:
		txt := l.SettingsAISourceLocalLogin()
		if t == aiusage.ToolCodex {
			txt = l.SettingsAISourceCodexLogin()
		} else if t == aiusage.ToolClaude {
			txt = l.SettingsAISourceClaudeLogin()
		}
		col := success
		if t == aiusage.ToolCodex || t == aiusage.ToolClaude {
			col = settingsInk3
		}
		settingsLabel(c, txt+planSuffix, 11, 0).TextColor(col)
	case aiusage.SourceLocalKey:
		settingsLabel(c, l.SettingsAISourceLocalKey()+planSuffix, 11, 0).TextColor(success)
	case aiusage.SourceSavedKey:
		ui.Row(c.Key("src-saved-" + string(t))).AlignItems(ui.Center).Gap(6).Children(func() {
			settingsLabel(c, l.SettingsAISourceSavedKey()+planSuffix, 11, 0).TextColor(success)
			if !locked && canPaste {
				settingsButton(c, "clr-"+string(t), l.SettingsAIClearKey(), false, func() {
					if a.usage != nil {
						a.usage.saveAPIKey(t, "")
					}
					if a.aiQuotaEditTool == string(t) {
						a.aiQuotaEditTool = ""
						a.aiQuotaKeyInput = ""
					}
				})
			}
		})
	default:
		if !canPaste {
			settingsLabel(c, l.SettingsAISourceMissing()+planSuffix, 11, 0).TextColor(settingsInk3)
			return
		}
		btn := ui.ButtonBase(c.Key("paste-"+string(t))).Padding(2, 6, 2, 6).Radius(5).Label(l.SettingsAISourcePasteKey())
		if btn.Hovered() && !locked {
			btn.Background(fill1)
		}
		btn.Children(func() {
			settingsLabel(c, l.SettingsAISourcePasteKey(), 11, 0).TextColor(settingsInk3)
		})
		if !locked && btn.Clicked() {
			if a.aiQuotaEditTool == string(t) {
				a.aiQuotaEditTool = ""
				a.aiQuotaKeyInput = ""
			} else {
				a.aiQuotaEditTool = string(t)
				a.aiQuotaKeyInput = ""
			}
		}
	}
}

func (a *shelf) settingsQuotaKeyEditor(c *ui.Context, t aiusage.Tool) {
	l := loc(c)
	ui.Row(c.Key("key-edit-"+string(t))).FillWidth().Padding(4, 14, 8, 44).Gap(6).AlignItems(ui.Center).Children(func() {
		inp := ui.TextInput(c.Key("key-inp-"+string(t)), &a.aiQuotaKeyInput).
			Grow(1).Height(26).Padding(4, 8, 4, 8).Radius(6).
			Background(fill1).Border(1, ui.RGBA(255, 255, 255, 0.14)).
			Placeholder(l.SettingsAIPasteKeyHint()).FontSize(12).TextColor(ink)
		_ = inp
		settingsButton(c, "key-save-"+string(t), l.SettingsAISaveKey(), true, func() {
			key := strings.TrimSpace(a.aiQuotaKeyInput)
			a.aiQuotaEditTool = ""
			a.aiQuotaKeyInput = ""
			if key != "" && a.usage != nil {
				a.usage.saveAPIKey(t, key)
			}
		})
	})
}

// settingsShortcutsPage: 全局, 播放, 工具, and read-only panel shortcuts.
func (a *shelf) settingsShortcutsPage(c *ui.Context) {
	l := loc(c)
	settingsFirstGroupRows(c, "keys-panel", l.SettingsKeysGlobalGroup(), "", a.settingsBroken, func() {
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
		settingsSep(c, "sc-board")
		a.settingsShortcutRow(c, shortcutBoard, l.SettingsShortcutNewBoard(), "")
		settingsSep(c, "sc-note")
		a.settingsShortcutRow(c, shortcutPet, l.SettingsShortcutPet(), "")
		settingsSep(c, "sc-pet")
		a.settingsShortcutRow(c, shortcutAppSwitch, l.SettingsShortcutAppSwitch(), "")
	})

	settingsGroup(c, "keys-notch", osText(l.SettingsKeysNotchGroup(), l.SettingsKeysNotchGroupWin()), "", func() {
		settingsReadonlyShortcutRow(c, "sc-esc", l.SettingsShortcutEscRow(), "",
			[]string{"Esc"})
		settingsSep(c, "sc-esc")
		settingsReadonlyShortcutRow(c, "sc-select", l.SettingsShortcutSelectRow(), "",
			[]string{"↑", "↓", osText("↩", "Enter")})
	})
}

func (a *shelf) settingsShortcutRow(c *ui.Context, kind shortcutKind, label, caption string) {
	l := loc(c)
	if p := a.shortcutConfirm; p != nil && p.kind == kind {
		ui.Row(c.Key(fmt.Sprintf("sc-confirm-%d", kind))).FillWidth().Radius(0).
			MinHeight(settingsRowMinHeight).AlignItems(ui.Center).Gap(8).
			Padding(settingsRowPadY, settingsRowPadX).Children(func() {
			settingsText(c, l.SettingsShortcutConfirm(shortcutLabel(p.acc)), 12).
				TextColor(ink2).Grow(1).MinWidth(0)
			settingsConfirmButton(c, fmt.Sprintf("sc-use-%d", kind), l.SettingsShortcutUseAnyway(), true, a.confirmShortcut)
			settingsConfirmButton(c, fmt.Sprintf("sc-drop-%d", kind), l.SettingsShortcutCancel(), false, a.cancelShortcutConfirm)
		})
		return
	}
	rowCaption, captionColor := caption, settingsInk3
	if a.shortcutBusy[kind] {
		rowCaption, captionColor = l.SettingsShortcutBusy(), danger
	}
	recording := a.settingsRecording == kind
	if recording {
		a.shortcutClearRect = ui.Rect{}
	}
	acc := a.shortcutAcc(kind)
	canClear := acc != ""
	cleared := false
	row := ui.ButtonBase(c.Key(fmt.Sprintf("sc-%d", kind))).FillWidth().Radius(0).
		MinHeight(settingsRowMinHeight).AlignItems(ui.Center).Justify(ui.Start).
		Gap(14).Padding(settingsRowPadY, settingsRowPadX).Label(label)
	if row.Hovered() {
		row.Background(settingsRowHoverBg)
	}
	if acc := a.shortcutAcc(kind); acc != "" {
		row.Description(shortcutLabel(acc))
	}
	row.Children(func() {
		settingsRowTextColor(c, label, rowCaption, captionColor)
		ui.Row(c.Key("keys")).AlignItems(ui.Center).Gap(4).Shrink(0).Children(func() {
			// The clear button sits left of the keycaps, so the right edge
			// of every row stays put whether or not it shows.
			slot := ui.Box(c.Key("clear-slot")).Size(22, 22).Shrink(0).Center()
			slot.Children(func() {
				switch {
				case recording && canClear:
					// The key-capture layer holds the pointer while a row
					// records; its hit test answers this box.
					a.shortcutClearRect = settingsShortcutClearButton(c, "clear-rec", l.SettingsShortcutClear(), false).Bounds()
				case !recording && canClear && row.Hovered():
					if settingsShortcutClearButton(c, "clear", l.SettingsShortcutClear(), true).Clicked() {
						cleared = true
					}
				}
			})
			if recording {
				ui.Box(c.Key("rec")).Height(24).Radius(6).Padding(0, 10, 0, 10).
					Border(1.5, accent).Center().Children(func() {
					ui.Row(c.Key("rec-text")).AlignItems(ui.Center).Gap(6).Children(func() {
						settingsText(c, l.SettingsShortcutRecording(), 11.5).SingleLine().TextColor(accent)
						settingsText(c, l.SettingsShortcutClearHint(), 11).SingleLine().
							TextColor(ui.RGBA(255, 255, 255, 0.42))
					})
				})
				return
			}
			if tokens, ok := shortcutTokens(acc); ok {
				for i, token := range tokens {
					settingsKeycap(c, fmt.Sprintf("k%d", i), token, false)
				}
				return
			}
			ui.Box(c.Key("none")).Height(24).Radius(6).Padding(0, 9, 0, 9).
				Border(1, settingsUnsetRim).BorderStyle(ui.BorderDashed).Center().Children(func() {
				settingsText(c, l.SettingsShortcutNoShortcut(), 11.5).SingleLine().TextColor(settingsInk3)
			})
		})
	})
	if cleared {
		a.clearShortcut(kind)
		return
	}
	if row.Clicked() {
		a.startShortcutRecording(kind)
	}
}

// settingsShortcutClearButton is the 22pt × that unbinds a shortcut. live
// is false while its row records: the key-capture layer owns the pointer
// then, and answers the box a.shortcutClearRect names.
func settingsShortcutClearButton(c *ui.Context, key, label string, live bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Size(22, 22).Radius(6).Center().Shrink(0).Label(label)
	b.IgnoreInput(!live)
	fg := settingsInk3
	if live && b.Hovered() {
		b.Background(settingsCtlHoverBg)
		fg = ink
	}
	b.Children(func() {
		ui.Icon(c.Key("x"), closeSymbol).Size(12, 12).TextColor(fg)
	})
	return b
}

func settingsConfirmButton(c *ui.Context, key, label string, primary bool, fn func()) ui.Element {
	b := ui.Row(c.Key("btn-"+key)).Height(26).Shrink(0).Radius(7).Padding(0, 12, 0, 12).
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
	bw, bh := b.Bounds().W, b.Bounds().H
	b.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			return true
		case ui.InputPointerUp:
			// Only the pointer letting go on the button chooses it: a
			// canceled press (the system took the pointer) and a release
			// outside the button decide nothing. Bounds is the box the
			// last frame laid out, read here rather than in the handler,
			// which runs on the element it would leave behind.
			if ev.Canceled {
				return true
			}
			if ev.X < 0 || ev.Y < 0 || ev.X > bw || ev.Y > bh {
				return true
			}
			fn()
			return true
		}
		return false
	})
	b.Children(func() {
		t := settingsText(c, label, 12).SingleLine().TextColor(fg)
		if primary {
			t.FontWeight(700)
		}
	})
	return b
}

func settingsReadonlyShortcutRow(c *ui.Context, key, label, caption string, tokens []string) {
	settingsRowLine(c, key).Children(func() {
		settingsRowText(c, label, caption)
		ui.Row(c.Key("keys")).AlignItems(ui.Center).Gap(4).Shrink(0).Children(func() {
			for i, token := range tokens {
				settingsKeycap(c, fmt.Sprintf("%s-%d", key, i), token, false)
			}
		})
	})
}

// settingsKeycap is a 24pt v6 keycap: #24252A, 6pt corners, bottom hairline,
// 12pt regular label.
func settingsKeycap(c *ui.Context, key, label string, on bool) {
	box := ui.Box(c.Key("cap-"+key)).Height(24).MinWidth(24).Radius(6).
		Padding(0, 7, 0, 7).Center().Background(settingsCtlBg)
	if on {
		box.Border(1.5, accent)
	} else {
		box.InsetShadow(0, -1, 0, 0, ui.RGBA(255, 255, 255, 0.06))
	}
	box.Children(func() {
		settingsText(c, label, 12).SingleLine().TextColor(ink)
	})
}

// settingsPermissionsPage: two untitled cards (live permission statuses and
// the guided-walk switch row).
func (a *shelf) settingsPermissionsPage(c *ui.Context) {
	l := loc(c)
	permissions := systemPermissionSet(l)
	missing := missingPermissions(permissions)

	settingsFirstGroup(c, "perm-list", "", "", func() {
		for i, p := range permissions {
			if i > 0 {
				settingsSep(c, "perm-"+p.id)
			}
			granted := p.enabled()
			row := settingsRowLine(c, "perm-"+p.id).Tooltip(p.detail)
			row.Children(func() {
				settingsRowText(c, p.short, "")
				ui.Row(c.Key("state")).AlignItems(ui.Center).Gap(14).Shrink(0).Children(func() {
					dot := success
					text := l.SettingsPermOn()
					if !granted {
						dot, text = warning, l.SettingsPermOff()
					}
					ui.Box(c.Key("dot")).Size(7, 7).Radius(4).Background(dot)
					settingsText(c, text, 11.5).SingleLine().TextColor(settingsInk3)
				})
			})
		}
	})

	settingsGroup(c, "perm-ctl", "", "", func() {
		a.settingsPermissionSwitch(c, permissions, missing)
	})
}

// settingsAboutPage: centered 112pt happy otter hero, 更新 card, and 隐私与源代码 card.
func (a *shelf) settingsAboutPage(c *ui.Context) {
	l := loc(c)

	ui.Column(c.Key("about-hero")).FillWidth().Padding(14, 0, 6, 0).AlignItems(ui.Center).Children(func() {
		if b := otterImage("happy"); b != nil {
			ui.Image(c.Key("otter"), b).Size(112, 112).Fit(ui.Contain)
		}
		settingsText(c, appName, 22).SingleLine().FontWeight(700).TextColor(ink).Margin(4, 0, 0, 0)
		settingsText(c, l.SettingsAboutVersion(appVersion(), runtime.GOARCH), 12).SingleLine().
			TextColor(settingsInk3).Margin(1, 0, 0, 0)
		if days, ok := a.companionDay(c.Now()); ok {
			text := l.CompanionAboutDays(days)
			if a.companionClicked() {
				text = l.CompanionAboutThanks(days)
			}
			settingsText(c, text, 12).SingleLine().TextColor(ink2).Margin(4, 0, 0, 0)
		}
		ui.Row(c.Key("about-actions")).AlignItems(ui.Center).Justify(ui.Center).Gap(8).Margin(14, 0, 0, 0).Children(func() {
			busy := a.updateChecking || a.updateInstalling || a.updateUnavailableReason() != ""
			settingsButton(c, "update", l.SettingsUpdateCheckRow(), false, func() { a.checkUpdates(true) }).
				Disabled(busy)
			settingsButton(c, "data", l.SettingsOpenDataFolder(), false, func() {
				if err := openPath(a.dataDir); err != nil {
					a.report(err)
				}
			})
			settingsButton(c, "coffee", l.CompanionAboutCoffee(), false, func() { a.openSupport() })
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
			settingsRow(c, "check", l.SettingsUpdateLastRow(), a.updateCaption(c.Locale(), c.Region(), l), func() {})
			if a.updateInstalled && a.update != nil {
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

const sourceURL = "https://github.com/henryyu333/rakko"

func (a *shelf) updateCaption(locale string, region i18n.Region, l locales.Messages) string {
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
	when := a.updateWhen(locale, region, l, a.updateLastCheck, time.Now())
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

func (a *shelf) updateWhen(locale string, region i18n.Region, l locales.Messages, at, now time.Time) string {
	clock := clockIn(locale, region, at)
	day := func(t time.Time) int {
		y, m, d := t.Date()
		return y*10000 + int(m)*100 + d
	}
	switch {
	case day(at) == day(now):
		return l.SettingsUpdateToday(clock)
	case day(at) == day(now.AddDate(0, 0, -1)):
		// Yesterday, across month and year boundaries alike.
		return l.SettingsUpdateYesterday(clock)
	default:
		return l.SettingsUpdateOn(dayIn(locale, region, at) + " " + clock)
	}
}
