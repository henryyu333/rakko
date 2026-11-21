package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
	"rakko/internal/aiusage"
	"rakko/internal/netproxy"
	"rakko/internal/platform"
	"rakko/internal/settings"
	"rakko/internal/store"
	"rakko/internal/weekly"
	"rakko/locales"
)

func main() {
	// The mygo updater takes no client: it calls http.DefaultClient directly
	// for its manifest and its download. Route that through the same proxy
	// rules as everything else, before any request can be issued and before
	// any goroutine starts (a GUI launch from Finder has no proxy variables,
	// so the system settings decide).
	netproxy.InstallDefaultClient()
	// mygo starts this same binary with MYGO_GENERATE set to write the
	// TypeScript client, on every `mygo build` and `mygo dev`. That run must
	// not touch the data directory (it would create an empty one and block
	// the migration of the old one), rename a bundle, or open a store.
	if generatingClient() {
		mygo.App.SetName(appName)
		if err := mygo.App.Run(); err != nil {
			log.Fatal(err)
		}
		return
	}
	// A tool's hook runs this same binary with --agent-hook: it forwards one
	// event and exits before the app touches its data directory, its
	// bundle, or any window.
	if agentnotify.RunHookCLI(os.Args[1:], os.Stdin) {
		return
	}
	// The same binary as a blocking approval client: it asks the running app
	// and prints the tool's decision, also before any window or data work.
	if agentnotify.RunApproveCLI(os.Args[1:], os.Stdin, os.Stdout) {
		return
	}
	// A second launch hands its arguments to the running app and exits. On
	// macOS the lock is always granted: LaunchServices already made the
	// second launch an activation of the first.
	if !requestSingleInstanceLock() {
		return
	}
	// Windows' Explorer entries launch this binary with the selected
	// paths. Consume them after the hand-off — a second launch forwards
	// its raw arguments — and before flag.Parse, which would reject the
	// verb. Other platforms return the command line untouched.
	os.Args = append(os.Args[:1], platform.ConsumeServiceArgs(os.Args[1:])...)
	// A bundle an in-app update left at an old bundle name moves to
	// Rakko.app and reopens from there before anything reads the data
	// directory.
	selfRenameBundle()
	dataDir := flag.String("data-dir", "", "本地数据目录（默认 Library/Application Support/Rakko）")
	show := flag.Bool("show", false, "启动时显示托盘")
	flag.Parse()
	if *dataDir == "" {
		root, err := store.DefaultRoot()
		if err != nil {
			log.Fatal(err)
		}
		// The default directory is this app's; the previous ones are copied
		// over once, newest name first. A --data-dir the
		// user gave is never migrated into.
		if oldRoots, lerr := store.LegacyRoots(); lerr == nil {
			for _, oldRoot := range oldRoots {
				migrated, merr := migrateDataRoot(oldRoot, root)
				switch {
				case errors.Is(merr, errLegacyRootLocked):
					log.Printf("%s: %v", appName, merr)
					lm := startupLoc(root, oldRoot)
					exitWithDialog(lm.StartupLegacyRunningTitle(),
						osText(lm.StartupLegacyRunningDetail(appName, oldRoot, root),
							lm.StartupLegacyRunningDetailWin(appName, oldRoot, root)))
					return
				case merr != nil:
					log.Printf("%s: 数据迁移失败：%v", appName, merr)
					lm := startupLoc(root, oldRoot)
					exitWithDialog(lm.StartupMigrateFailedTitle(),
						lm.StartupMigrateFailedDetail(oldRoot, root, merr.Error()))
					return
				}
				if migrated {
					log.Printf("%s: 数据已从 %s 迁移到 %s，旧目录保留为备份", appName, oldRoot, root)
					break
				}
			}
		}
		*dataDir = root
	}
	s, err := store.Open(*dataDir)
	if err != nil {
		// 坏掉的 state.json 先给选择框：恢复最近的备份，或把坏文件挪到
		// 一边重新开始；原件永远保留。其余打不开的原因照旧只报错退出。
		if errors.Is(err, store.ErrStateUnreadable) {
			log.Printf("%s: %v", appName, err)
			startupStateRecovery(*dataDir, err)
			return
		}
		// 历史未成功打开时不创建托盘、快捷键或监控，也不改写原件。
		log.Printf("%s: %v", appName, err)
		lm := startupLoc(*dataDir)
		message := lm.StartupHistoryUnreadable(appName)
		detail := lm.StartupHistoryUnreadableDetail(*dataDir, err.Error())
		switch {
		case errors.Is(err, store.ErrStateNewer):
			// 更新版 Rakko 写的历史：文件没坏，这一版太旧，不给恢复
			// 选择（「重新开始」会让人误以为历史全没了）。
			message = lm.StartupStateNewer(appName)
			detail = lm.StartupStateNewerDetail(appName, *dataDir, err.Error())
		case errors.Is(err, store.ErrLocked):
			message = lm.StartupAlreadyRunning(appName)
			detail = lm.StartupAlreadyRunningDetail(*dataDir)
		}
		exitWithDialog(message, detail)
		return
	}
	if s != nil {
		if merr := s.MigrationError(); merr != nil {
			// 备份失败时存储已停止写入；这里只做启动日志，界面状态在 ready 中显示。
			log.Printf("Rakko: 迁移前备份失败，已停止写入以免覆盖可恢复原件：%v", merr)
		}
	}
	freshInstall := !settings.Exists(*dataDir)
	// The first day of companionship is written once, so every later launch
	// counts from the same date. A file that cannot be read stays untouched
	// and this run shows no days.
	comp, cerr := loadCompanion(*dataDir, freshInstall, time.Now())
	if cerr != nil {
		log.Printf("%s: 陪伴记录不可用，本次不显示陪伴天数：%v", appName, cerr)
	}
	prefs, perr := settings.Load(*dataDir)
	// A launch that found no settings.json at all owes the first-launch
	// guide; a launch that already owed it (a move or permission relaunch)
	// keeps it. An upgraded install has the file without the flag, and a
	// broken read never claims the install is new — that file is never
	// rewritten behind the user's back.
	prefs.Onboarding.Pending = onboardingOwed(freshInstall, perr, prefs.Onboarding.Pending)
	a := &shelf{store: s, settings: prefs, dataDir: *dataDir, companion: comp, stop: make(chan struct{}), thumbnails: make(map[string]*ui.Bitmap), thumbPending: make(map[string]bool)}
	// 剪贴板文字限额来自设置；小于 1 的值由 store 回落到默认 30。
	s.SetTextLimit(prefs.ClipboardLimit)
	if perr != nil {
		a.settingsBroken = true
		a.notice(locales.In(resolveLanguage(prefs.Language, preferredLanguages())).StartupSettingsBroken(perr.Error()))
	}
	mygo.App.SetName(appName)
	// The language of settings.json wins over the system's; "" follows the
	// system again, with the one rule resolveLanguage states (a Chinese first
	// language shows Chinese, every other English). The launch pins what the
	// rule resolves, so what the app shows, its native AppKit text and its own
	// strings agree; every launch resolves again from settings.json.
	mygo.App.OnLocaleChanged(a.localeChanged)
	a.showLanguage()
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		// Quitting never drops what was typed into the 便利贴 window; the
		// file watch goes with the process.
		a.noteFlush()
		if a.note != nil && a.note.watch != nil {
			a.note.watch.Close()
			a.note.watch = nil
		}
		a.closePet()
		if a.agent != nil {
			a.agent.stop()
		}
		close(a.stop)
		a.stopDragGuard()
		a.endSnapSession(true)
		a.cancelSnapConfirm()
		mygo.GlobalShortcut.UnregisterAll()
		if a.stopMouseObservation != nil {
			a.stopMouseObservation()
		}
		if a.stopDragObservation != nil {
			a.stopDragObservation()
		}
		if a.stopWakeObs != nil {
			a.stopWakeObs()
		}
		if a.stopAppearanceObs != nil {
			a.stopAppearanceObs()
		}
		if a.clip != nil {
			a.clip.stop()
		}
		if a.tray != nil {
			a.tray.Destroy()
		}
		a.flushLeaving(time.Now().Add(time.Second)) // dragged-out items leave for good
		a.flushToolsLayout()
		if a.weekly != nil {
			a.weekly.flush()
		}
		a.closeIdleIslands()
		a.store.Close()
	})
	mygo.App.WhenReady(func() {
		if err := platform.Init(); err != nil {
			log.Fatal(err)
		}
		a.ready(*show)
	})
	mygo.App.OnDidBecomeActive(a.followSystemLanguage)
	// macOS reopen（如 open -a / Finder 再次启动已运行的实例）和
	// Windows 的第二次启动走同一个落点：设置窗口开着就归还给它；否则
	// 等价于图标点击——切换而非强制展开，避免常驻窗口自行弹出。
	mygo.App.OnActivate(func(bool) { a.reopened() })
	registerSecondInstance(a.reopened)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// reopened is what a second launch asks the running app to do: give the
// keyboard back to an open Settings window, or toggle the shelf the way a
// click on the icon does.
func (a *shelf) reopened() {
	if a.settingsWin != nil {
		platform.ActivateSelf()
		a.settingsWin.Focus()
		return
	}
	if a.win != nil {
		a.toggle()
	}
}

// generatingClient reports whether mygo started this binary to write the
// TypeScript client (MYGO_GENERATE), as mygo build, mygo dev and mygo
// generate do.
func generatingClient() bool {
	return os.Getenv("MYGO_GENERATE") != ""
}

// exitWithDialog shows one error dialog and quits: the startup path that
// cannot open a usable data directory never creates a tray, shortcuts or
// monitors, and never writes to the files it could not read.
func exitWithDialog(message, detail string) {
	mygo.App.SetName(appName)
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.WhenReady(func() {
		go func() {
			_, _ = mygo.Dialog.Message(mygo.MessageOptions{
				Type:    mygo.MessageError,
				Title:   appName,
				Message: message,
				Detail:  detail,
			})
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// startupStateRecovery is the way out of an unreadable state.json: a
// dialog offers restoring the newest readable backup, or moving the bad
// file aside and starting over — whichever the user picks, the corrupt
// original is kept, never deleted or overwritten. Dismissing the dialog
// leaves every file as it was and shows the usual unreadable notice. A
// choice made, the recovered directory is relaunched into with the same
// arguments (--data-dir included), and this process ends with the
// dialog's app.
func startupStateRecovery(dir string, why error) {
	lm := startupLoc(dir)
	backups := store.RecoveryBackups(dir)
	buttons := []string{lm.StartupStateStartOver()}
	restore := -1 // the restore button's index, -1 when there is no backup to offer
	if len(backups) > 0 {
		restore = 0
		buttons = []string{lm.StartupStateRestoreBackup(), lm.StartupStateStartOver()}
	}
	startOver := len(buttons) - 1
	mygo.App.SetName(appName)
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.WhenReady(func() {
		go func() {
			res, derr := mygo.Dialog.Message(mygo.MessageOptions{
				Type:               mygo.MessageError,
				Title:              appName,
				Message:            lm.StartupHistoryUnreadable(appName),
				Detail:             lm.StartupStateCorruptDetail(dir, why.Error()),
				Buttons:            buttons,
				DefaultButton:      0,
				CancelButton:       -1,
				DestructiveButtons: []int{startOver},
			})
			// Dismissed without a button (Esc, the close box, a dialog that
			// could not show): mygo reports the CancelButton value, -1 here,
			// and -1 can never be a button's index. Nothing has been touched
			// then — say so and quit, whichever button layout there was.
			if derr != nil || !(res.Button == startOver || (restore >= 0 && res.Button == restore)) {
				showStartupNotice(lm, dir, why)
				return
			}
			var backup string
			if restore >= 0 && res.Button == restore {
				backup = backups[0]
			}
			if rerr := store.RecoverState(dir, backup); rerr != nil {
				log.Printf("%s: %v", appName, rerr)
				// 恢复失败：坏原件未动，原样说明后退出。
				showStartupNotice(lm, dir, why)
				return
			}
			// 恢复好的目录要一条干净的启动：同一个可执行文件、同样的
			// 参数（--data-dir 一并保留）重启自己，本次进程随 dialog 的
			// app 一起结束。
			mygo.App.Relaunch()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// showStartupNotice ends a recovery run by saying what stopped the
// launch and that every file stayed as it was, then quits.
func showStartupNotice(lm locales.Messages, dir string, why error) {
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{
		Type:    mygo.MessageError,
		Title:   appName,
		Message: lm.StartupHistoryUnreadable(appName),
		Detail:  lm.StartupHistoryUnreadableDetail(dir, why.Error()),
	})
	mygo.App.Quit()
}

// startupLoc says the messages of the language a launch shows, for the
// startup path that fails before the shelf exists: the language settings.json
// names, or the system's by the same rule resolveLanguage states. It reads
// settings.json without writing it (settings.LanguageOf), so a launch that
// cannot open the data directory leaves every file as it was; dirs are tried
// in order, the first with a settings.json answering.
func startupLoc(dirs ...string) locales.Messages {
	setting := ""
	for _, dir := range dirs {
		if lang, ok := settings.LanguageOf(dir); ok {
			setting = lang
			break
		}
	}
	return locales.In(resolveLanguage(setting, preferredLanguages()))
}

func (a *shelf) ready(show bool) {
	a.locale = mygo.App.Locale()
	// Layouts saved under an earlier display identity are re-keyed before the
	// first window is placed, so the user's own place restores.
	a.migrateSavedLayouts()
	a.newNotchWindow()
	a.startDiagnostics()
	// Pay the index's per-process setup now, so the first search of the
	// session is as fast as the ones after it.
	a.warmSearch()
	a.stopMouseObservation = platform.ObserveMouseDown(a.win.NativeHandle(), func(inside bool, button int) {
		a.mouseDown(inside, button, mygo.Screen.CursorScreenPoint())
	})
	a.stopDragObservation = platform.ObserveLeftMouseDragged(a.pointerDragged)
	a.stopWakeObs = platform.ObserveSystemWake(func() {
		a.handleSystemWake()
		// The screens settle a moment after the wake; align once more then.
		time.AfterFunc(500*time.Millisecond, func() {
			mygo.RunOnMain(func() {
				if !a.quitting {
					a.handleSystemWake()
				}
			})
		})
	})
	a.stopAppearanceObs = platform.ObserveAccessibilityChange(func() {
		a.appearanceAt = time.Time{}
		a.wakeMonitor()
	})
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting {
			e.PreventDefault()
			a.hide(hideExplicit)
		}
	})
	var err error
	trayOpts := mygo.TrayOptions{IconIsTemplate: true, ToolTip: osText(a.appLoc().AppTrayTooltip(), a.appLoc().AppTrayTooltipWin())}
	if dir, derr := mygo.App.Path(mygo.PathResources); derr == nil {
		trayOpts.Icon = trayIcon(dir)
	}
	if len(trayOpts.Icon) == 0 {
		// No icon file: the title keeps the menu-bar item visible and
		// clickable.
		trayOpts.Title = appName
	}
	a.tray, err = mygo.NewTray(trayOpts)
	if err != nil {
		log.Fatal(err)
	}
	a.tray.OnClick(a.toggle)
	a.tray.OnRightClick(func() {
		a.tray.SetMenu(a.trayMenu())
		defer a.tray.SetMenu(nil)
		a.tray.PopUpMenu()
	})
	a.registerShortcuts()
	a.applyClipboardHistory()
	a.modules = append(a.modules, &retentionModule{host: a}, noteModule{host: a})
	a.np = &nowPlayingModule{host: a}
	a.modules = append(a.modules, a.np)
	a.conv = &converter{host: a}
	a.modules = append(a.modules, a.conv)
	a.registerClockModules()
	a.registerSysMonModule()
	a.dl = newDownloadsModule(a)
	// The strip keeps the first two activities. The converter's cancel
	// control comes first, then the countdown and the stopwatch; the
	// download chip follows them.
	a.modules = append(a.modules, a.dl, &leavingModule{host: a}, &ocrIndex{host: a})
	// After the converter: a running conversion's only cancel control must
	// not be displaced.
	a.awake = newAwakeModule(a)
	a.modules = append(a.modules, a.awake)
	// Realtime activities: battery, headphone and download notices. They
	// only post to the shared queue (live.go); the queue decides what the
	// notch shows.
	a.pwr = newPowerModule(a)
	a.audio = newAudioModule(a)
	a.modules = append(a.modules, a.pwr, a.audio)
	// IOKit's change notifications wake the battery poll, so a plug-in
	// posts within a tick. Without the observer the two-second poll alone
	// carries the notices, as it always did.
	a.pwr.watchPower()

	// AI 用量页：额度查询与本地用量扫描都注入为可替换的实现，页面只认
	// internal/aiusage 的类型。额度读取本机已保存的登录信息，只发给
	// 对应服务的官方端点；本地扫描的增量缓存放数据目录的
	// usage-cache/。这里的组装不碰凭据、文件与网络——一切取数都在
	// tick/点击触发且 aiUsage.enabled 为 true 之后才开始。
	usageEnv := aiusage.DefaultEnv()
	// Rakko's own Claude Code lives in its data directory, so an app update
	// never moves the sign-in and the Mac's own ~/.claude stays untouched.
	usageEnv.RakkoClaudeDir = filepath.Join(a.dataDir, "claude")
	usageEnv.Secrets = func(tool aiusage.Tool) string {
		v, err := mygo.Secrets.Get("aiusage-" + string(tool))
		if err != nil {
			return ""
		}
		return string(v)
	}
	deps := usageDeps{
		fetchCodex: func(ctx context.Context) aiusage.Quota {
			q := aiusage.FetchCodex(ctx, usageEnv)
			if q.Status == aiusage.QuotaOK {
				rc, err := aiusage.FetchCodexResetCredits(ctx, usageEnv)
				switch {
				case err != nil:
					// The quota answer stands, but the credit count was not
					// read: say unknown, so the last known count keeps
					// showing instead of the row vanishing.
					q.ResetCreditsStale = true
				case rc != nil:
					q.ResetCredits = rc
				}
				// A readable answer with no credits leaves ResetCredits
				// nil: the row is gone for real.
			}
			return q
		},
		fetchClaude: func(ctx context.Context) aiusage.Quota { return aiusage.FetchClaude(ctx, usageEnv) },
		fetchMore: map[aiusage.Tool]func(context.Context) aiusage.Quota{
			aiusage.ToolGLM:         func(ctx context.Context) aiusage.Quota { return aiusage.FetchGLM(ctx, usageEnv) },
			aiusage.ToolDeepSeek:    func(ctx context.Context) aiusage.Quota { return aiusage.FetchDeepSeek(ctx, usageEnv) },
			aiusage.ToolCursor:      func(ctx context.Context) aiusage.Quota { return aiusage.FetchCursor(ctx, usageEnv) },
			aiusage.ToolKimi:        func(ctx context.Context) aiusage.Quota { return aiusage.FetchKimi(ctx, usageEnv) },
			aiusage.ToolOpencode:    func(ctx context.Context) aiusage.Quota { return aiusage.FetchOpenCodeGo(ctx, usageEnv) },
			aiusage.ToolCommandCode: func(ctx context.Context) aiusage.Quota { return aiusage.FetchCommandCode(ctx, usageEnv) },
		},
		useCodexReset: func(ctx context.Context) (aiusage.ResetOutcome, error) {
			return aiusage.UseCodexReset(ctx, usageEnv)
		},
		detectSource: func(tool aiusage.Tool) aiusage.QuotaSource {
			return aiusage.DetectQuotaSource(tool, usageEnv)
		},
		saveSecret: func(tool aiusage.Tool, key string) error {
			k := "aiusage-" + string(tool)
			if key == "" {
				return mygo.Secrets.Delete(k)
			}
			return mygo.Secrets.Set(k, []byte(key))
		},
		// Rakko signs in its own Claude Code; Claude Code keeps, refreshes
		// and revokes the credentials, this app only starts the process.
		loginClaude: func(ctx context.Context) error {
			return aiusage.ClaudeAuthLogin(ctx, usageEnv)
		},
		pace: aiusage.Pace,
		// Claude Code's own logs tell whether an allowance moved at all;
		// the /usage read runs only when it did.
		claudeUsedSince: func(t time.Time) bool {
			return aiusage.ClaudeUsedSince(usageEnv.ClaudeConfigDir, usageEnv.Home, t)
		},
		loadLast: func() []aiusage.Quota { return aiusage.LoadLastQuotas(a.dataDir) },
		saveLast: func(q aiusage.Quota) { _ = aiusage.SaveLastQuota(a.dataDir, q) },
		// A fresh scanner per refresh; every supported local reader is on.
		// The incremental cache in usage-cache keeps rebuilds cheap.
		newScanner: func() usageScanner {
			if usageEnv.Home == "" {
				return nil
			}
			return aiusage.NewScanner(usageEnv.Home, filepath.Join(a.dataDir, "usage-cache"))
		},
		now: time.Now,
	}
	usage := newUsageFeature(a, deps)
	a.registerPage(usage)
	a.modules = append(a.modules, usage)

	// 每周简报：应用时长须用户在本页明确开启才采样；AI 部分只读本机
	// 日志；Rakko 完成事件由各成功回调计数。历史与设置分文件，读取失败
	// 时保留原文件并只在页内提示。
	weeklyStore, weeklyErr := weekly.Open(a.dataDir)
	wdeps := weeklyDeps{
		now:           time.Now,
		frontApp:      platform.FrontmostApp,
		idleSeconds:   platform.IdleSeconds,
		sessionActive: platform.SessionActive,
		displayAsleep: platform.MainDisplayAsleep,
		newScanner: func() weeklyScanner {
			if usageEnv.Home == "" {
				return nil
			}
			return aiusage.NewScanner(usageEnv.Home, filepath.Join(a.dataDir, "usage-cache"))
		},
		goWork: goWork,
		onMain: dispatchMain,
	}
	a.weekly = newWeeklyFeature(a, wdeps, weeklyStore, weeklyErr)
	a.registerPage(a.weekly)
	a.modules = append(a.modules, a.weekly)
	_, a.dragSerial = platform.DragState()
	// The first-launch guide registers before the panel can open: a fresh
	// install's flag is on already, and the page stays until finish/skip.
	if a.onboardingPending() {
		a.onb = newOnboarding(a)
		a.registerPage(a.onb)
		// Persist at once: a crash or force-quit before the first step
		// change still resumes the guide instead of losing it.
		next := a.settings
		next.Onboarding.Step = a.onb.steps()[a.onb.step]
		a.applySettings(next)
	}
	mygo.Screen.OnDisplaysChanged(func() {
		// 常驻窗口跟随光标所在显示器重新定位（收起态无感）。
		a.place(mygo.Screen.CursorScreenPoint())
		a.wakeMonitor()
		// 拔掉显示器的固定项回主显示器同一边。
		a.pinDisplaysChanged()
		// 桌宠所在的显示器拔掉了就回主显示器。
		a.petDisplaysChanged()
	})
	if a.store != nil {
		if merr := a.store.MigrationError(); merr != nil {
			a.notice(a.appLoc().StartupBackupFailed(merr.Error()))
		}
	}
	if show {
		a.show(false)
	} else {
		// Keep the same top-anchored transparent strip when idle or peeking;
		// only its drawing and mouse pass-through change on hover.
		a.place(mygo.Screen.CursorScreenPoint())
		a.syncMousePassThrough()
		a.win.ShowInactive()
	}
	// Platform work that finishes after its call returned reports failures to
	// the same notice the call's own error would use.
	platform.InstallErrorNotice(a.report)
	a.installServices()
	a.pinRestore()
	// The desktop otter comes out after the pins: it stands among them on
	// the same floating level, outside the dock's and the panel's zones.
	a.petStart()
	a.agentStartEnabled()
	a.approvalStartEnabled()
	a.startMonitoring()
	log.Print("Rakko ready")
}

// newNotchWindow creates the persistent notch window: one transparent,
// frameless, non-activating panel that the three states redraw. The smoke
// test builds the same window, so the ⌃⌥V focus behaviour is checked
// against what production shows.
func (a *shelf) newNotchWindow() {
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title: appName, Width: sheetW, Height: sheetH,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: disableMoveOverlay, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(a.view),
	})
	a.win.SetVisibleOnAllWorkspaces(true)
	a.winH = a.win.NativeHandle()
	platform.ConfigureNotchWindow(a.winH)
	// The panel is topmost, and a system popup menu opened from it is not:
	// lift the menus this process shows into the topmost band, so one opened
	// over the panel is not painted over. The watch needs the window's own
	// handle (no-op on macOS).
	platform.WatchOwnMenus(a.winH)
	// The gate belongs to the window, not to a placement: an app that
	// starts with the panel open never places, and a test shelf must watch
	// the pointer as production does.
	a.watchPointer()
	a.win.OnMove(a.windowGeometryChanged)
	a.win.OnResize(a.windowGeometryChanged)
	logWindowDiagnostics(a.winH)
}

// trayMenu is rebuilt on every right-click so it reflects the current
// system-permission state, which the user changes in System Settings, and
// the current settings.
func (a *shelf) trayMenu() *mygo.Menu {
	l := a.appLoc()
	items := []*mygo.MenuItem{
		{Label: l.TrayShowShelf(), Click: func(*mygo.MenuItem, *mygo.Window) { a.toggle() }},
	}
	if it := a.permissionMenuItem(); it != nil {
		items = append(items, it)
	}
	items = append(items,
		&mygo.MenuItem{Label: l.TrayRetention(), Type: mygo.MenuItemSubmenu, Submenu: a.retentionMenu()},
		&mygo.MenuItem{Label: l.TrayClipboard(), Type: mygo.MenuItemSubmenu, Submenu: a.clipboardMenu()},
	)
	if it := a.petMenuItem(); it != nil {
		items = append(items, it)
	}
	items = append(items,
		&mygo.MenuItem{Label: l.AwakeTitle(), Type: mygo.MenuItemSubmenu, Submenu: a.awakeMenu()},
		&mygo.MenuItem{Label: l.ItemScreenshot(), Click: func(*mygo.MenuItem, *mygo.Window) { a.startScreenshot() }},
		&mygo.MenuItem{Label: l.ItemCaptureText(), Click: func(*mygo.MenuItem, *mygo.Window) { a.startScreenOCR() }},
		mygo.Separator(),
		&mygo.MenuItem{Label: l.TraySettings(), Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }},
		a.updateMenu(),
		mygo.Separator(),
		&mygo.MenuItem{Label: l.TrayQuit(), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	)
	return mygo.NewMenu(items)
}

// petMenuItem offers 放出海獭 while the otter is stowed — the way back when
// it was sent into the notch and the Settings window is not open. It is
// absent from a build without the artwork, which shows no otter at all.
func (a *shelf) petMenuItem() *mygo.MenuItem {
	l := a.appLoc()
	if petArt() == nil || a.settings.Pet.Shown {
		return nil
	}
	return &mygo.MenuItem{Label: l.TrayShowPet(), Disabled: a.settingsBroken,
		Click: func(*mygo.MenuItem, *mygo.Window) { a.setPetShown(true) }}
}

// retentionMenu offers the retention periods. It is disabled while
// settings.json could not be read, so a broken file is never overwritten.
func (a *shelf) retentionMenu() []*mygo.MenuItem {
	l := a.appLoc()
	choice := func(label string, days int) *mygo.MenuItem {
		current := a.settings.RetentionDays
		if current < 0 {
			current = 0
		}
		return &mygo.MenuItem{
			Label: label, Type: mygo.MenuItemRadio, Checked: current == days, Disabled: a.settingsBroken,
			Click: func(*mygo.MenuItem, *mygo.Window) { a.setRetention(days) },
		}
	}
	items := []*mygo.MenuItem{choice(l.RetentionNever(), 0)}
	for _, d := range retentionChoices {
		items = append(items, choice(l.RetentionDays(d), d))
	}
	return items
}

// setRetention saves the new period and applies it at once.
func (a *shelf) setRetention(days int) {
	if a.settings.RetentionDays == days {
		return
	}
	next := a.settings
	next.RetentionDays = days
	if !a.applySettings(next) {
		return
	}
	a.removeExpired(time.Now())
}

// clipboardMenu offers the retained text limits. A broken settings file locks
// these choices, but never disables the clipboard recorder itself.
func (a *shelf) clipboardMenu() []*mygo.MenuItem {
	l := a.appLoc()
	var items []*mygo.MenuItem
	limit := a.settings.ClipboardLimit
	if limit < 1 {
		limit = settings.Default().ClipboardLimit
	}
	for _, n := range clipboardLimitChoices {
		items = append(items, &mygo.MenuItem{
			Label: l.ClipMenuKeep(n), Type: mygo.MenuItemRadio, Checked: limit == n, Disabled: a.settingsBroken,
			Click: func(*mygo.MenuItem, *mygo.Window) { a.setClipboardLimit(n) },
		})
	}
	return items
}
