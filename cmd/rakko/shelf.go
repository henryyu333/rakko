//go:build darwin

package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/lyrics"
	"rakko/internal/mac"
	"rakko/internal/settings"
	"rakko/internal/store"
	"rakko/internal/weekly"
)

// UI、历史与本机 API 由 mygo 主线程串行拥有。
type shelf struct {
	win                                                         *mygo.Window
	winH                                                        uintptr // cached on creation; zero for a headless surface
	tray                                                        *mygo.Tray
	store                                                       *store.Store
	stop                                                        chan struct{}
	quitting, visible, edgeOpened, received                     bool
	status                                                      string
	statusUntil                                                 time.Time
	statusSeenAt                                                time.Time     // 提示当前被看见的窗口起点（pill 或胶囊画出其一）
	statusSeen                                                  time.Duration // 累计被看见时长：胶囊/暂停时段不计入
	statusKind                                                  statusKind    // 提示级别：成功 / 普通信息 / 错误（pill 红点、胶囊配色）
	statusCapsule                                               bool          // 面板收起时是否转交刘海胶囊（后台信息不转交）
	statusCapsuleLimit                                          time.Duration // progress notes must not occupy the notch for the whole operation
	x, y                                                        int
	dispBounds                                                  mygo.Rectangle
	frame                                                       mygo.Rectangle // 常驻窗口当前实际映射的屏幕矩形（desiredFrame 最近一次应用值）
	frameHold                                                   mygo.Rectangle // envelope held while something is drawn; zero = none
	settingFrame                                                bool
	buildingView, frameQueued                                   bool // defer native resize until the current UI frame has painted
	blankFrame, blankQueued, blankReady                         bool // draw one blank frame before a native resize
	idleIslandDrawn                                             bool // the canvas on screen is the island style's resting capsule
	animationStart                                              time.Time
	animationFrom, progress                                     float32
	level                                                       int       // drawn level: 1 search bar (76), 2 page (250)
	jumpLevel                                                   bool      // the next frame snaps the level spring (fresh open)
	closeAt                                                     time.Time // when hide started the content fade; zero = none
	closeFrom, contentFrom                                      float32   // content opacity a fade-out starts from, and an opening fades in from
	settings                                                    settings.Settings
	locale                                                      string // language the native surfaces say (tray, menus, dialogs); "" = source
	modules                                                     []module
	np                                                          *nowPlayingModule   // the peek wings read its state
	pwr                                                         *powerModule        // battery / adapter events (live.go)
	audio                                                       *audioModule        // headphone connect / disconnect
	dl                                                          *downloadsModule    // browser downloads in ~/Downloads
	live                                                        liveQueue           // transient charging / headphone / download-done events
	conv                                                        *converter          // image conversion jobs; nil until the app starts
	usage                                                       *usageFeature       // nil until the AI usage page registers itself
	weekly                                                      *weeklyFeature      // nil until the 每周简报 page registers itself
	agent                                                       *agentNotifyFeature // nil until the Agent 完成提醒 switch or a hook event needs it
	onb                                                         *onboardingFeature  // the first-launch guide; nil on an upgraded install
	approval                                                    *approvalFeature    // nil until Agent 审批 or an approval request needs it
	lyrCache                                                    *lyrics.Cache       // synced lyrics for the home page; nil until first used
	clip                                                        *clipboardModule    // nil until monitoring starts
	awake                                                       *awakeModule        // nil until the app starts
	vol                                                         volumeControl       // system output volume; nil = CoreAudio
	pages                                                       []panelPage         // registered pages in ear order (built-ins first)
	pagesReady                                                  bool                // built-in pages have been registered
	tab                                                         pageID              // current page of the expanded panel (registry in pages.go)
	tabAt                                                       time.Time           // when the tab last switched; drives the pages' crossfade
	tabFrom                                                     pageID              // the page being left by the last switch, drawn fading out
	tabOutK                                                     float32             // opacity the outgoing picture starts at (1, or where an interrupted switch left it)
	tabOutShift                                                 float32             // offset the outgoing picture starts at when a switch interrupted another
	warmed                                                      map[pageID]bool     // pages already drawn once while the panel rested
	warming                                                     pageID              // page queued for that warm draw
	search                                                      string              // clipboard page's search text
	homeQuery                                                   string              // the home search bar's text
	homeScope                                                   mac.SearchScope     // what the bar searches: 全部 / 应用 / 文件, never saved
	homeSel                                                     int                 // the selected result row
	homeSpot                                                    homeSpotState       // the bar's Spotlight query and hits
	homeRows                                                    []homeHit           // the current build pass's grouped rows
	homeRowsOK                                                  bool
	spotIcons                                                   map[string]*ui.Bitmap // result path → Finder icon, cached for the sheet
	spotPending                                                 map[string]bool       // result icons being rendered in the background
	titleWidths                                                 map[string]float32    // back-row page name → measured width at the title font (see backTitleWidth)
	reveal                                                      string                // row to keep in view after a keyboard move
	focusMode                                                   bool                  // the window took the keyboard for clipboard search
	prevAppPID                                                  int                   // app to hand the keyboard back to when focusMode ends
	appNames                                                    map[string]string     // bundle id → display name, cached for menu labels
	appIcons                                                    map[string]*ui.Bitmap // bundle id → icon, cached for clipboard rows
	write                                                       clipWriter            // where copies go; nil = the system clipboard
	dragSerial                                                  int64
	dragOpenTools                                               bool // 本次外部拖拽开始时面板开在工具页（design §1: 只有这时才用卡片接文件）
	dragging, suppressed, pointerDrag, ownDrag, dropHot, inBand bool
	drop                                                        dropZoneState           // incoming-drag zone memory; zero while no drag hovers
	dataDir                                                     string                  // where state.json and settings.json live
	settingsBroken                                              bool                    // settings.json unreadable: never overwrite it
	settingsWin                                                 *mygo.Window            // the 设置 window; nil while closed
	settingsPrevPID                                             int                     // app to hand the keyboard back to when it closes
	perm                                                        permissionFlow          // 系统权限 unified entry's guided walk; zero = idle
	loginOn                                                     bool                    // 开机时自动启动，read back each time the window opens
	loginErr                                                    string                  // last SetOpenAtLogin failure, shown inline
	rn                                                          *renameUI               // 重新命名 window session; nil until first use
	settingsScroll                                              ui.ScrollState          // the Settings window's scroll position
	settingsPage                                                settingsPageID          // the sidebar destination shown
	settingsRecording                                           shortcutKind            // the shortcut row recording keys, shortcutNone when idle
	shortcutConfirm                                             *shortcutConfirmation   // a bare text key waiting for the user's confirmation
	shortcutBusy                                                map[shortcutKind]bool   // shortcuts the system refused
	shortcutReg                                                 map[shortcutKind]string // what the system holds per shortcut, "" for none
	shortcutsSuspended                                          bool                    // a recording released the shortcuts; restore them after
	update                                                      *mygo.Update            // version the feed offers, waiting for the user
	updateChecking, updateInstalling                            bool
	updateInstalled                                             bool    // swapped into place; runs at the next launch
	updateProgress                                              float64 // download progress while installing
	updateNext                                                  time.Time
	updateLastCheck                                             time.Time         // when the last check ran (this launch)
	updateLastResult                                            updateCheckResult // how it went
	updateLastVersion                                           string            // the version it found, if any
	updateWin                                                   *mygo.Window      // the update window; nil while closed
	updateWinStage                                              updateStage       // which face it shows
	updateBytes, updateTotal                                    int64             // download counts; total may be 0
	updateInstallCancel, updateCheckCancel                      context.CancelFunc
	updateInstallQuiet                                          bool           // the auto-install path staged it: prompt once when ready
	updateFailDetail                                            string         // the failed face's reason; "" = the generic one
	updateCheckDead                                             bool           // the running check's window was canceled: bury its outcome
	updateNotes                                                 ui.ScrollState // release notes scroll, reset on each open
	updatePrompted                                              string         // the version already prompted for this launch
	updateWinMarked                                             bool           // window logically open (headless mark or native)
	updateCanceled                                              bool           // the download's cancel was clicked: the result unwinds quietly
	updatePromptPending                                         updateStage    // deferred auto-prompt's face; updateStageChecking = none
	updatePromptAt                                              time.Time      // a deferred auto-prompt's earliest moment; zero = none
	guard                                                       dragGuard
	snap                                                        snapState // 拖窗口到刘海分屏 session / panel
	escHeld, spaceHeld, cmdCHeld                                bool
	onbKeysHeld                                                 map[string]bool // the guide's held Left/Right/Enter
	selection                                                   selection
	stopMouseObservation, stopDragObservation                   func()
	stopAppearanceObs                                           func()
	leftSince                                                   time.Time
	hidePending                                                 bool // a press outside waited for the drop catch to finish
	topInset                                                    int
	notchX, notchY, notchW, notchH                              int
	hasNotch                                                    bool
	island                                                      bool           // this display draws the floating island shell
	islandTop                                                   int            // island shell's top inset inside the window
	dispWork                                                    mygo.Rectangle // the display's visible frame, measured by placeAt
	islandDX, islandDY                                          int            // dragged island: offsets from the default axis and row
	islandPress                                                 islandPress
	islandDrag                                                  bool             // the press on the closed island became a move
	bandBox                                                     mygo.Rectangle   // the band's drawn box (see bandBegin); bandBlank reads its size
	bandHits                                                    []mygo.Rectangle // the band's controls, relative to its top-left
	flip                                                        panelFlip        // a panel side change in flight after a band drag
	ownPress                                                    bool             // a left press is held in our own window
	dispID                                                      int64
	idleIslands                                                 []*idleIslandWindow
	mainIdle                                                    *mainIdleCapsule // the resident display's resting capsule picture
	mainIdleHideQueued                                          bool             // its hide waits for the frame that drew the shell
	peeking                                                     bool
	peekProgress, peekFrom                                      float32
	peekAnimStart                                               time.Time
	fb                                                          feedback      // the notch's completion capsule (feedback.go)
	apps                                                        appSwitcher   // the app-switcher wings mode (appswitch.go)
	music                                                       musicControls // the player's seek / volume rails (music_controls.go)
	fx                                                          shellMotion   // the shell drawn last, for the window frame
	ignoringMouse                                               bool
	// pointerTick caches the pointer for one monitoring pass: the
	// monitor, the gate and the drag logic must see one sample per tick,
	// not one WindowServer round trip each.
	pointerTick      mygo.Point
	pointerTickValid bool
	// The monitoring loop: started is readable from helper goroutines that
	// ask for a wake, the generation lets a superseded main-queue timer
	// retire without a second pass.
	monitorStarted    atomic.Bool
	monitorWakeQueued atomic.Bool
	monitorGen        atomic.Uint64
	monitorRunFn      func() // set at start; breaks the wake→tick init cycle
	activeTickerStop  func()
	wakeMainFn        func(func())
	pointerNear       bool      // the pointer is on a watched surface
	lastMoveAt        time.Time // last move event, for the gate poll window
	lastDragPoll      time.Time // last fallback drag read while idle
	watchingPointer   bool      // registered with the process-wide pointer gate
	reduceTrans       bool      // cached Reduce Transparency (refreshed on a slow tick)
	appearanceAt      time.Time
	thumbnails        map[string]*ui.Bitmap
	shake             shakeDetector // 摇晃召唤：拖拽中左右晃动检测
	basket            basketState   // 摇晃召唤的拖放篮
	thumbPending      map[string]bool
	thumbDiscard      map[string]bool
	thumbActive       int
	pins              []*pinItem // desktop-pinned cards (pin.go)
	pinReturning      bool       // a pin drag holds the notch's 松开放回 bulge
	// pet 是桌面海獭的窗口与按下会话（pet.go）；nil 表示桌面上没有它。
	// lastAct 是最近一次使用（打开面板、提起海獭、接住投放），十分钟
	// 没有它海獭就睡觉；happyUntil 是接到好消息后的开心表情截止时刻。
	pet          *petWindow
	petReturning bool // a carried otter holds the notch's 松开收进刘海 pill
	lastAct      time.Time
	happyUntil   time.Time

	// note 是便利贴卡片背后的那个 Markdown 文件，以及打开时的窗口
	// （note.go）；首次画到卡片或点开时才建立文件与监听。
	note *noteUI
	// noteIdx 是搜索「便利贴」组用的便签文本（notesearch.go）：只在保存、
	// 外部改动或搜索框重新拿到键盘时重扫，敲键时只查内存。
	noteIdx noteIndex

	// 框选文字：同一时间只有一轮（ocrScan，capture+识别全程）；ocrBusyAt
	// 记这轮开始时刻给进行中胶囊的 250ms 延迟计时；ocrSheet 为真时展开
	// 面板的舞台换成「屏幕录制」权限说明卡。三者只在 UI 线程读写。
	ocrScan   bool
	ocrBusyAt time.Time
	ocrRunID  int
	ocrSheet  bool
	// ocrSheetShot remembers that the sheet is the screenshot flow's: the
	// same permission card, with the screenshot title.
	ocrSheetShot bool

	// shot is the live screenshot session (screenshot.go): frozen displays,
	// the overlay windows and the selection. nil when no capture is running.
	shot *shotSession
	// shotPins are the pinned screenshots of this launch; they outlive the
	// session that made them and never enter layout.json.
	shotPins []*shotPin

	// 拖出后从托盘移除：当前拖出会话从托盘带走的项目（剪贴板页为 nil），
	// 以及已拖出、等待最终移除的托盘项目及其放下时刻。
	dragOutIDs []string
	leaving    map[string]time.Time

	// toolsLay 缓存工具页的 layout.json（internal/layout）；懒加载，
	// mutateTools 写盘后保持最新。toolsLayBroken 与 settingsBroken 同规：
	// 文件读不通时界面用默认布局，但禁止任何写回以免覆盖可恢复原件。
	toolsLay       *layout.Layout
	toolsLayBroken bool

	// pasteSeq 是剪贴板粘贴尝试的代号（paste.go）：每次新选择、收起或重新
	// 打开都会加一，旧尝试看到代号变了就不再按键，快速连按也不会双重粘贴。
	// 后台 goroutine 读它，因此用原子量而不是普通字段。
	pasteSeq atomic.Int64
}

func (a *shelf) toggle() {
	if a.visible {
		a.hide(hideExplicit)
	} else {
		a.show(false)
	}
}

// statusKind 是提示的级别：做成的事情、普通信息、错误。面板底部 pill
// 只给错误加红点；刘海胶囊按级别给状态点配色（DESIGN.md：成功绿
// #30D158 ✓、普通信息中性白点、错误红 #FF453A !）。
type statusKind int

const (
	statusOK   statusKind = iota // 做成的结果：绿 ✓
	statusInfo                   // 普通信息：中性白点，无光晕
	statusErr                    // 错误：红 !，无期限时至少停 4 秒
)

// toastStatus posts a transient status pill at the panel's bottom centre:
// msg until the deadline (or toastLife from its first paint when until is
// zero); statusErr gives it the small red dot of error toasts. With the
// panel closed there is no pill to read, so the result of a user action
// grows out of the notch as a capsule instead (feedback.go) — even while
// the panel is still folding away, since the capsule's own clock starts
// when it can appear.
func (a *shelf) toastStatus(msg string, until time.Time, kind statusKind) {
	a.toast(msg, until, kind, true, 0)
}

// toastProgress preserves the panel deadline but caps the capsule's visible
// span. Completion and failure post their own unrestricted result.
func (a *shelf) toastProgress(msg string, until time.Time) {
	a.toast(msg, until, statusInfo, true, 4*time.Second)
}

// toastQuiet posts a status that never grows the notch capsule: background
// bookkeeping the user did not ask for (automatic cleanup) waits for the
// panel's next opening in the bottom pill.
func (a *shelf) toastQuiet(msg string, until time.Time, kind statusKind) {
	a.toast(msg, until, kind, false, 0)
}

func (a *shelf) toast(msg string, until time.Time, kind statusKind, capsule bool, limit time.Duration) {
	// The search bar has no strip for a toast: a confirmation there would
	// be invisible, and an error belongs to Notification Center. Pages and
	// the closed capsule keep their pills.
	if a.onSearchBar() {
		if kind == statusErr {
			sendNotification(appName, oneLine(msg))
		}
		return
	}
	a.status = msg
	a.statusUntil, a.statusKind, a.statusCapsule = until, kind, capsule
	a.statusSeen, a.statusSeenAt = 0, time.Time{}
	a.statusCapsuleLimit = limit
	if !a.visible && capsule {
		a.showStatusFeedback(time.Now())
	}
	if a.win != nil {
		a.win.Invalidate()
	}
	a.wakeMonitor()
}

func (a *shelf) showStatusFeedback(now time.Time) {
	until := a.statusUntil
	if limit := a.statusCapsuleLimit; limit > 0 && (until.IsZero() || until.After(now.Add(limit))) {
		until = now.Add(limit)
	}
	a.fb.show(a.status, a.statusKind, until, now)
}

// handoffStatus moves a live status to the notch capsule when the panel
// folds: a toast posted moments before a collapse (回车粘贴即收起) would
// otherwise die with the pill after a single frame. A toast the panel or
// the capsule already showed long enough (its kind's floor) is not
// replayed — an armed capsule that ran its span stays done, and a capsule
// still holding the words simply plays out its remaining time. Background
// bookkeeping (toastQuiet) never hands off.
func (a *shelf) handoffStatus(now time.Time) {
	if a.status == "" || !a.statusCapsule {
		return
	}
	if a.dismissReadFeedback(now) {
		return
	}
	if !a.statusUntil.IsZero() && now.After(a.statusUntil) {
		return
	}
	line, _, _ := strings.Cut(a.status, "\n")
	if f := &a.fb; f.text == line && (f.armed || f.up(now)) {
		return // shown already, or still on its own remaining time
	}
	if a.fb.played == line {
		return // the capsule already played this toast out: no second show
	}
	a.showStatusFeedback(now)
}

// dismissReadFeedback counts only the time the words were actually on
// screen: the pill's visible span plus the capsule's drawn span. It also
// runs before the panel expires its text, so an unseen capsule cannot
// outlive the acknowledgement already read there.
func (a *shelf) dismissReadFeedback(now time.Time) bool {
	seen := a.statusSeen
	if !a.statusSeenAt.IsZero() && a.status != "" &&
		(a.visible || (a.fb.drawn && a.fb.pausedAt.IsZero())) {
		seen += now.Sub(a.statusSeenAt)
	}
	if seen < a.statusKind.holdFloor() {
		return false
	}
	line, _, _ := strings.Cut(a.status, "\n")
	if f := &a.fb; f.text == line {
		f.text, f.liveToken, f.pausedAt = "", nil, time.Time{}
	}
	return true
}

func (a *shelf) receive(data transfer.Data) error {
	l := a.appLoc()
	if a.store == nil {
		return errors.New(l.StatusNoStore())
	}
	var err error
	if paths, e := data.Files(); e == nil && len(paths) > 0 {
		err = a.store.AddFiles(paths)
	} else if b, e := data.Read(transfer.PNG); e == nil && len(b) > 0 {
		err = a.store.AddImage(b)
	} else if b, e := data.Read(transfer.Text); e == nil && len(b) > 0 {
		err = a.store.AddText(string(b))
	} else {
		err = errors.New(l.StatusUnsupported())
		a.report(err)
		return err
	}
	if err == nil {
		a.received = true
		a.toastStatus(l.StatusAdded(), time.Now().Add(3*time.Second), statusOK)
	}
	a.changed(err)
	return err
}

func (a *shelf) changed(err error) {
	if err != nil {
		a.report(err)
		return
	}
	if a.store != nil && len(a.store.Items) > 0 {
		a.invalidateThumbnail(a.store.Items[0].ID)
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}
func (a *shelf) report(err error) {
	msg := a.appLoc().StatusFailed(err.Error())
	a.toastStatus(msg, time.Time{}, statusErr)
	log.Print(msg)
	if a.win != nil {
		a.win.Invalidate()
	}
}

// onSearchBar reports whether the panel shows the search bar, where the
// toast strip does not exist.
func (a *shelf) onSearchBar() bool {
	return a.visible && a.currentLevel() == 1 && !a.snap.panel && !a.snap.drawn
}

// sendNotification posts a system notification (title plus one line). A
// packaged app shows it; a test process is not packaged, and tests replace
// the seam. Show asks for permission on the first notification and waits
// for the answer, so it runs off the main thread.
var sendNotification = func(title, body string) bool {
	if !mygo.App.IsReady() || !mygo.NotificationsSupported() {
		return false
	}
	note := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body, Silent: true})
	go func() { _ = note.Show() }()
	return true
}

// oneLine folds a message into one line for a notification.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 180 {
		return string(r[:179]) + "…"
	}
	return s
}

// notice adds a startup message to the status toast without hiding an
// earlier one (settings, shortcut and migration problems can all occur).
// Notices are problems, so they show the error dot and live a little
// longer than a confirmation.
func (a *shelf) notice(msg string) {
	log.Print(msg)
	if a.status != "" {
		a.status += "\n"
	}
	a.toastQuiet(a.status+msg, time.Now().Add(8*time.Second), statusErr)
}
func (a *shelf) completed(r transfer.Result) {
	a.ownDrag = false
	ids := a.dragOutIDs
	a.dragOutIDs = nil
	if r.Err != nil {
		a.report(r.Err)
		return
	}
	if !r.Canceled && r.Operation != transfer.None {
		// A drag that carried tray items and landed is one 托盘取出 for the
		// 每周简报. The clipboard page and widget cards leave ids nil: they
		// copy history, they do not take from the tray.
		if ids != nil {
			a.weeklyEvent(weekly.EventTrayTake)
		}
		a.leaveTray(ids, time.Now())
		a.hide(hidePassive)
	}
}

// applySettings is the only path that changes a preference: it saves next
// to settings.json first and only then replaces the running copy, so a
// failed write leaves the running setting unchanged and a broken file is
// never overwritten. The tray menus and the Settings window both go
// through it, so they can never disagree. After the save both windows
// repaint, which keeps an open Settings window in sync with menu choices.
// Side effects that follow a changed field (module start/stop, store
// limit, an immediate cleanup pass) run in each setter after this returns.
func (a *shelf) applySettings(next settings.Settings) bool {
	if a.settingsBroken {
		return false
	}
	if err := settings.Save(a.dataDir, next); err != nil {
		a.report(err)
		return false
	}
	a.settings = next
	// The pinned list may have changed anywhere (settings window, a
	// reload): the wings' derived rows follow it, not just the snapshot.
	a.apps.rowsDirty = true
	a.apps.manageDirty = true
	if a.win != nil {
		a.win.Invalidate()
	}
	a.repaintSettings()
	a.repaintUpdateWindow()
	a.wakeMonitor()
	return true
}

// applyClipboardHistory starts the recorder once for this session. Recording
// is always enabled; settings still control retention and excluded apps.
func (a *shelf) applyClipboardHistory() {
	if a.clip != nil {
		return
	}
	a.clip = newClipboardModule(a)
	a.modules = append(a.modules, a.clip)
}

// setClipboardLimit saves the new clipboard text limit and applies it to
// the store at once (the next add trims to it). Disabled while
// settings.json could not be read, so a broken file is never overwritten.
func (a *shelf) setClipboardLimit(n int) {
	if n < 1 {
		n = settings.Default().ClipboardLimit
	}
	if a.settings.ClipboardLimit == n {
		return
	}
	next := a.settings
	next.ClipboardLimit = n
	if !a.applySettings(next) {
		return
	}
	if a.store != nil {
		a.store.SetTextLimit(n)
	}
}

// hideExit is how a hide was reached. Restoring the keyboard to the app
// the user copied from is right only for exits the user chose while using
// the shelf — Esc, Enter-copy, the collapse button, the tray/shortcut
// toggle. A click outside or a finished drag-out means the user moved on
// to another app themselves; stealing it back would fight that choice.
type hideExit int

const (
	hideExplicit hideExit = iota // the user dismissed the shelf itself
	hidePassive                  // click elsewhere, a finished drag-out, a flash timer
)

// shouldRestoreApp reports whether hide wants the remembered frontmost app
// re-activated: only when the shelf had the keyboard (focusMode) and the
// exit was the user's own dismiss. Pure; unit-tested.
func (a *shelf) shouldRestoreApp(exit hideExit) bool {
	return a.focusMode && exit == hideExplicit
}

// openClipboardSearch opens the shelf on the clipboard tab with the search
// field focused and ready for typing (the clipboard hotkey). The app is
// activated so the window takes the keyboard, and the app the user came
// from is remembered so hiding hands the keyboard back to it. Only this
// keyboard flow activates the app; hover, drag, tray and service opens keep
// their ShowInactive behaviour.
func (a *shelf) openClipboardSearch() {
	// This open takes the panel over: a queued services flash or a
	// drag-edge auto-collapse must never hide the search underneath.
	a.takeServiceOwnership()
	// 重新打开是新一轮：上一次还在等焦点的粘贴立即作废。
	a.cancelPaste()
	a.edgeOpened = false
	if a.win != nil && !a.focusMode {
		if pid := mac.FrontmostPID(); pid > 0 && pid != mac.OwnPID() {
			a.prevAppPID = pid
		}
	}
	a.switchTab(pageClipboard) // leaving pageUsage retires its work the same way setTab does
	a.search = ""
	a.focusMode = true
	a.level = 2
	a.jumpLevel = true
	if a.win == nil {
		return // headless surface: state only
	}
	// The window must sit below the IM candidate band before the search
	// field takes the keyboard — even when the panel was already open
	// and show() returns early. syncMousePassThrough applies windowLevel.
	a.syncMousePassThrough()
	a.show(false)
	mac.ActivateSelf()
	a.win.Focus()
	// 键盘已经在这个窗口里：Esc 与空格交给窗口处理，全局注册会把
	// 按键从搜索框手里抢走。
	a.releaseKeys()
}

// switchTab changes the current page without a fade: the search hotkey's
// direct jump. setTab keeps its own copy because it also records tabFrom
// for the crossfade; both retire the AI usage page's background work the
// moment it is left.
func (a *shelf) switchTab(id pageID) {
	if a.tab == pageUsage && id != pageUsage && a.usage != nil {
		a.usage.stopWork()
	}
	if a.tab == pageTools && id != pageTools {
		a.resetToolsEdit()
	}
	a.tab, a.tabFrom = id, id
	a.tabAt = time.Time{}
	a.tabOutK, a.tabOutShift = 1, 0
}

// Keyboard handback seams: tests stub these (mac's own calls need the app
// running) to prove who gets the keyboard when a keyboard-taking surface
// closes.
var (
	appIsActive = mac.AppActive
	activateApp = mac.ActivateApp
	resignSelf  = mac.DeactivateSelf
)

// activatePreviousApp hands the keyboard back to the app the user came
// from when the shelf was opened for clipboard search. It runs only while
// this app is still the active one — a user who switched away already
// must not be pulled back — and if the remembered app is gone, ShelfU's
// own activation is resigned rather than leaving a hidden app in front.
func (a *shelf) activatePreviousApp(pid int) { a.handBack(pid, true) }

// handBackApp is activatePreviousApp without the resign fallback: the
// Settings window hands the keyboard back the same way, but must never
// hide this app (NSApp hide would take the notch window with it).
func (a *shelf) handBackApp(pid int) { a.handBack(pid, false) }

func (a *shelf) handBack(pid int, resignWhenGone bool) {
	if pid <= 0 || pid == mac.OwnPID() || !appIsActive() {
		return
	}
	if !activateApp(pid) && resignWhenGone {
		resignSelf()
	}
}
