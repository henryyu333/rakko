# Changelog

## 1.18.8

- **交互与控件修复**：拖动音乐进度条或音量条中途失焦、手势被打断时，不再把音量误设为静音或让进度跳回开头；设置里的确认按钮滑出按钮外松手或按压被取消时不再误触发；首次引导在授予权限后的「重新启动」步骤，按回车或右方向键现在和点击按钮一样正常重启应用。
- **拖拽与识别提示**：工具页卡片拖离面板变形时，预览窗口的尺寸更新延后到渲染帧之后，避免边界帧闪烁；面板收起状态下通过桌面钉卡或快捷键识别文字超过 250ms 时，会可靠唤醒并显示「正在识别…」进度胶囊。
- **托盘与 AI 用量**：重复转换或解压出同一路径的文件和文件夹时，会直接复用托盘里已有的条目并保留置顶状态，不再出现重复项；Devin 的缓存写入字段改为仅在 Devin 解析器中生效，避免其他工具误多计。

### English

- **Input & control fixes**: if a drag on the music progress or volume slider loses focus or is canceled by the system, it no longer mutes the volume or jumps back to 0:00; confirmation buttons in Settings ignore canceled presses and releases outside the button; pressing Enter or Right on the restart step of first-launch onboarding now relaunches Rakko just like clicking the button.
- **Drag preview & OCR indicator**: dragging a tools card off the panel now defers window frame updates until after the render frame to avoid stretching artifacts; when OCR takes longer than 250ms while the panel is folded, the progress pill reliably wakes and appears.
- **Tray & AI usage**: re-converting or re-extracting a file or folder at the same path now reuses the existing tray item (keeping its pinned state) instead of creating duplicates; Devin's `cache_creation_tokens` field is now scoped to Devin's parser alone so other tools cannot overcount cache writes.

## 1.18.7

- **白板文字看得清**：图形选「实心」填充、填充色和描边色相同时，里面的文字会自动换成反色，不再和底色混在一起看不见；箭头标签和导出的 PNG / SVG 也一样。
- **白板画质**：画板上的形状很多时，不再把整页的手绘笔触一起变成等粗的线；现在只把最占资源的少数线条简化，钢笔笔锋尽量保留。形状很多的画板上缩放和拖动也更顺。
- **白板擦除**：用画笔来回涂抹擦除时，只删真正划到的那几笔，不再把它所在的整个编组一起删掉。
- **白板外观**：窗口够宽时，底部工具栏和左下导航用更大的尺寸；很窄的窗口里两者上下排开，不再挤在一起；属性面板宽度统一；方格和横线纸面的线条粗细一致；箭头走线选项「弧线」改叫「曲线」。
- **白板存储更稳**：删除白板、导出覆盖旧文件、用记事本另存过的白板文件、断电后留下的临时文件，这几种情况现在都能正确处理；白板里删掉的图片在下次打开后会从白板文件里清掉，导出的白板文件也不再夹带它们。
- **修复**：删除白板或页面的确认框可以按 Esc 取消。

### English

- **Readable whiteboard text**: when a shape uses a solid fill in the same color as its outline, its text switches to the contrasting color instead of disappearing into the fill; arrow labels and PNG / SVG exports follow suit.
- **Whiteboard drawing quality**: a board crowded with shapes no longer flattens every freehand stroke on the page into a plain line. Only the few heaviest lines are simplified and pen strokes keep their shape, and zooming and panning busy boards is smoother.
- **Whiteboard scribble erase**: scribbling over strokes with the pen now erases only the strokes you cross, not the whole group they belong to.
- **Whiteboard look**: on wide windows the bottom toolbar and the navigation pill are larger; on very narrow windows they stack instead of touching; the properties panel has one width; grid and lined paper draw every line at the same weight; the arrow route option is now called Curve.
- **Safer whiteboard files**: deleting a board, exporting over an old file, opening a board saved by Notepad and cleaning up after a power cut are all handled properly now. Pictures you deleted from a board are dropped from its file the next time it opens, and board exports no longer carry them.
- **Fixes**: the delete board / delete page confirmation closes with Esc.

## 1.18.6

- **白板**：工具页新增「白板」卡片，打开一块独立窗口里的无限画布。有画笔、荧光笔、橡皮（整个对象 / 局部擦除）、激光笔、二十多种形状、能连在图形上的箭头、文字、便签、框容器和图片；手绘、干净、单色三种风格，点阵、方格、横线、空白四种纸面，深色和浅色主题。支持多块白板、多个页面、编组、对齐、锁定、裁切图片和小地图；可以导出 PNG、SVG 或白板文件，选中内容能直接拖出窗口。停手就自动保存，写盘在后台进行。也可以在搜索条里选「新建白板」，或在「设置 → 快捷键」里设快捷键。
- **系统窗口按钮**：白板和便利贴窗口改用系统的红黄绿按钮（Windows 上是标题栏的最小化、最大化、关闭），鼠标停在绿色按钮上可以选平铺位置，白板还能全屏。
- **便利贴**：标题太长时字号自动缩小，完整显示不截断。
- **导航图标**：工具页换成工具箱图标，应用切换换成九宫点图标，两个不再容易看混。
- **工具页**：移除「最近文件」和「剪贴板最新」两张卡片，它们的内容在托盘页和剪贴板页里都能看到；已经放着这两张卡的布局和桌面钉卡会自动去掉，不留空格。
- **更省电**：开着「保持唤醒」时，面板收起后不再在后台空转。
- **修复**：面板收起后，桌面上的倒计时、秒表、保持唤醒、AI 用量和便利贴钉卡不再停住不动；桌面海獭在松手没被系统送到或拖动被打断时能正确收尾；工具页保存布局失败时不再卡死；解压 ZIP 时拒绝带盘符的路径并限制总大小。

### English

- **Whiteboard**: a new Whiteboard card on the tools page opens an infinite canvas in its own window. It has pens, a highlighter, an eraser (whole object or partial), a laser pointer, 20-plus shapes, arrows that stay attached to shapes, text, notes, frames and pictures; sketchy, clean and mono looks, dotted, grid, lined and blank paper, and dark and light themes. Multiple boards and pages, grouping, alignment, locking, image cropping and a minimap are all there; export as PNG, SVG or a board file, or drag a selection straight out of the window. It saves as you go, writing in the background. You can also pick New Board in the search bar or set a shortcut in Settings → Shortcuts.
- **System window buttons**: the whiteboard and sticky note windows now use the system's red, yellow and green buttons (minimize, maximize and close in the title bar on Windows). Hover the green button to tile the window; the whiteboard can also go full screen.
- **Sticky notes**: a long title shrinks to fit instead of being cut off.
- **Navigation icons**: the tools page now has a toolbox icon and the app switcher a nine-dot icon, so the two are no longer easy to mix up.
- **Tools page**: the Recent Files and Latest Clipboard cards are gone; their contents are on the Tray and Clipboard pages. Layouts and desktop pins that still hold them drop them cleanly, leaving no gaps.
- **Power**: with Keep Awake on, Rakko no longer keeps working in the background once the panel is folded.
- **Fixes**: the timer, stopwatch, Keep Awake, AI usage and sticky note cards pinned to the desktop keep updating after the panel folds; the desktop otter finishes a drag correctly when the system drops the release or the drag is interrupted; a failed tools-page layout save no longer freezes Rakko; ZIP extraction rejects drive-letter paths and caps the total size.

## 1.18.5

- **「正在播放」开关**：「设置 → 外形与提示 → 实时活动」新增「正在播放」。关掉后，刘海两翼、首页播放器、工具页音乐卡和播放快捷键都不再出现，后台也不再读取播放状态；打开刘海就是首页。默认开启，随时切换，无需重启。
- **音乐卡残留修复**：播放器退出后，刘海和首页的音乐卡会随之消失，不再一直停在上一首。
- **AI 用量**：DeepSeek 余额卡改为显示 DeepSeek 标志，不再是一个空圆环加「余额」二字。
- **底层升级与整理**：界面框架 MyGo 升级到 0.10.0；合并了各处重复的内部实现（浮层窗口、Agent 提醒与审批、AI 用量读取、Mac 与 Windows 共用部分），功能与外观保持不变。

### English

- **Now Playing switch**: Settings → Appearance → Live Activities has a new Now Playing switch. Turned off, the notch wings, the home player, the music card on the tools page and the playback shortcuts all go away and Rakko stops reading playback state, so opening the notch shows the home page. On by default; changes apply at once, no restart.
- **Stale music card**: when the player quits, the music card on the notch and the home page now goes away instead of staying on the last track.
- **AI usage**: the DeepSeek balance card shows the DeepSeek logo instead of an empty ring labelled "Balance".
- **Under the hood**: the MyGo UI framework is updated to 0.10.0, and duplicated internals (floating windows, agent notifications and approvals, AI usage readers, code shared by Mac and Windows) are merged into one each, with no change to features or looks.

## 1.18.4

- **多显示器选择与拖拽防误穿**：「设置 → 显示在」新增「跟随鼠标」「主显示器」以及固定到指定显示器的选项（标注与主显示器的相对方位）；当所在屏幕上方还接了一块显示器时，从下方拖入文件会在屏幕顶边先停住让你松手投放，继续用力上推才跨到上方屏幕。
- **AI 用量 API Key 存储调整**：不再将 AI 用量的 API Key 写入 macOS 登录钥匙圈，改为保存在数据目录的仅本人可读写（`0600`）文件中（首次读取自动从旧钥匙圈迁移），避免版本更新后反复弹出钥匙圈授权提示。
- **拖拽投放与动效修复**：将拖入路径分类与扫描移出主线程，并修复拖拽离开后托盘「松开放入」提示状态卡住或被裁剪的问题；修复收起探头胶囊在弹性回缩到位前提前中断动画保持的问题；更新弹窗勾选框图标适配 16×16 框尺寸；修复音乐卡片固定到桌面并隐藏主面板后音量条停止轮询同步的问题。
- **AI 用量与 Agent 接入修复**：AI 用量扫描遵循 `$XDG_DATA_HOME` 读取 `opencode` 数据、修复指定 `CLAUDE_CONFIG_DIR` 时的 Claude Code 凭据探测，并避免单次扫描重复复制 SQLite 数据库；Hermes 接入支持配置首次备份、正确处理 YAML 行尾注释与引号，且在配置文件损坏时停止改写；修复 `dsh` 带缩进空数组解析位置与 Windows 命名管道并发竞态。
- **数据兼容与 Windows 优化**：读取本地 JSON 配置与历史文件时自动兼容 UTF-8 BOM；Windows 下 PDF 文字提取对全角与兼容字符做归一化处理，图片与截图落盘统一使用原子写。

### English

- **Display selection and stacked-display edge barrier**: Settings → Display On lets you keep the shelf Following Mouse, pin it to the Primary Display, or pin it to a specific monitor (with relative position labels); when another display sits above Rakko's screen, dragging files up stops briefly at the top edge so you can drop them before pushing through to the upper monitor.
- **API key storage**: AI usage API keys now live in a mode-`0600` file inside Rakko's data directory instead of the macOS login keychain (existing keychain entries migrate automatically on first read), avoiding repeated keychain prompts after app updates.
- **Drag-and-drop and animation fixes**: moved dropped-path inspection off the UI thread and fixed stale hover state keeping the drop pill stuck or clipped; kept the closed peek overshoot animation settling cleanly; sized the update window checkmark icon to its 16×16 frame; kept the volume slider polling when the music card is pinned to the desktop with the main panel hidden.
- **AI usage and Agent hook fixes**: honor `$XDG_DATA_HOME` for `opencode` usage lookup, align Claude Code keychain lookup with `CLAUDE_CONFIG_DIR`, and copy each SQLite usage database at most once per scan; back up Hermes configs once before editing, preserve/handle YAML trailing comments and quotes safely, refuse edits when `hooks.json` is damaged, fix indented empty flow sequences in `dsh`, and close a concurrency race in Windows named pipe deadlines/closure.
- **Data compatibility & Windows polish**: strip UTF-8 BOMs when loading local JSON files; normalize compatibility characters (NFKC) in Windows PDF text extraction and use atomic writes for Windows screenshots and converted images.

## 1.18.3

- 托盘：放在 U 盘、移动硬盘或网络盘上的文件，拔盘后重启 Rakko 不再消失，会显示为灰色，插回后自动恢复。
- 历史文件损坏时不再直接退出：可以选择用最近的备份恢复，或把坏文件移到一边重新开始，原文件始终保留。历史文件来自更新版本的 Rakko 时，会提示安装最新版。
- 歌词：偶尔查不到时会自动重试，不再一周都显示没有歌词；暂停时不再卡在「正在查找歌词…」；同名的现场版、专辑版不再串词。
- 代理需要账号密码时，歌词和 AI 用量会说明「代理需要认证，Rakko 暂不支持」。
- AI 用量：读不到额度时显示「读不到」，不再显示错误的 0% 或旧数字；Codex 重置卡在重置过程中不会被重复使用，重置后张数立即更新；额度卡全部关闭时页面自动收窄；API Key 失效时提示去设置里重新粘贴。
- Agent 审批：同时有多个请求时，排队的卡从显示出来才开始倒计时；等不到的请求交回终端，并在刘海提示「另有 N 个请求已交回终端」。修复内容很大的审批请求会让 Agent 卡住约一分钟的问题，以及 Hermes、dsh 配置可能被写坏的问题。
- 截图与框选文字：授权「屏幕录制」后，权限卡会提示「重新打开 Rakko」才生效。
- 灵动岛样式：空闲胶囊和音乐两翼不再显示应用切换按钮，入口统一在面板底部导航。
- 系统监控：读不到的数据显示「--」，不再显示 0%；进程排行打开时直接按 CPU 排序；走势图注随采样间隔变化。
- 时间显示跟随系统的地区格式和 12/24 小时制；每月 1 号也能正确显示「昨天」。
- 修复全屏应用接管时方向键和回车仍被 Rakko 占用、工具页拖开 ⊖ 仍删卡、首页拖入显示「松开放入」却不接收、双屏时点另一块屏的胶囊无反应、钉在桌面的卡片在分辨率变小后跑出屏幕等交互问题。
- Windows：独占全屏游戏只让所在屏幕的胶囊让位；部分提示改用 Windows 的说法。
- 更省电：页面看不见时不再后台刷新，两翼被其他内容挡住时停止采样，空闲的便利贴窗口不再频繁唤醒。

### English

- Shelf: files on a USB drive, external disk or network share no longer vanish when the drive is unplugged and Rakko restarts; they show greyed out and come back when the drive does.
- A damaged history file no longer just stops Rakko: restore the newest backup, or move the damaged file aside and start fresh; the original is always kept. History written by a newer Rakko now asks you to update.
- Lyrics: a failed lookup retries instead of showing no lyrics for a week; a paused track no longer sticks on "Looking for lyrics…"; live and album versions with the same title no longer share lyrics.
- When a proxy asks for a username and password, lyrics and AI usage say the proxy needs authentication, which Rakko does not support yet.
- AI usage: unreadable quotas say so instead of showing 0% or stale numbers; a Codex reset credit cannot be spent twice while a reset runs, and the count updates right after; with every quota card off the page narrows; an expired API key points you to Settings.
- Agent approvals: with several requests at once, a queued card's countdown starts when it appears; requests that cannot wait go back to the terminal, and the notch says how many. Fixed very large approval requests stalling the agent for about a minute, and Hermes and dsh configs that could be written incorrectly.
- Screenshot and text capture: after granting Screen Recording, the permission card tells you to reopen Rakko.
- Dynamic Island style: the idle capsule and the music wings no longer show the app switcher button; it lives in the panel's bottom bar.
- System monitor: unreadable readings show "--" instead of 0%; the process list ranks by CPU as soon as it opens; the chart caption follows the sampling interval.
- Times follow your region and 12/24-hour setting; "Yesterday" now works on the first of the month.
- Fixed arrow keys and Return staying captured when a full-screen app takes over, dragging off the ⊖ badge still removing a card, the home page promising a drop it then ignored, clicks on another display's capsule doing nothing, and desktop pins left off-screen after the resolution shrinks.
- Windows: an exclusive full-screen game only makes its own display's capsule step aside; a few hints now use Windows wording.
- Lighter on power: hidden pages stop refreshing in the background, covered wings stop sampling, and an idle sticky note no longer wakes Rakko constantly.

## 1.18.2

- Windows：去除透明悬浮窗（主岛、副屏胶囊、桌宠、固定卡片、拖放篮与截图遮罩）在 Windows 11 上可能出现的 1px 系统圆角边框与阴影。
- 灵动岛与常驻两翼：修复开启系统监控等常驻两翼时，面板收起后透明窗口未缩回紧凑尺寸的问题。
- Windows：收窄全屏抑制判定，避免桌面美化工具、动态壁纸或系统忙态下，焦点离开 Rakko 后点不开灵动岛。
- AI 用量：未安装 Claude Code CLI 时直接提示「未找到 Claude Code」，避免误显「登录」按钮且点击后静默失败；其他登录失败同步通过底部提示说明原因。

### English

- Windows: strip Win32 caption bits and disable DWM rounded corners, border and shadow on transparent overlays (main island, idle capsules, pet, pinned cards, drop basket and capture overlay) so no visible outline appears around transparent HWNDs on Windows 11.
- Island & persistent wings: release the open-panel window envelope once folded back onto settled persistent wings (such as system monitor wings) instead of keeping the expanded window size.
- Windows: tighten fullscreen suppression so `QUNS_BUSY` or desktop-enhancer / wallpaper overlays no longer block clicking the island when Rakko is not in the foreground.
- AI Usage: report `QuotaNoCLI` when Claude Code CLI is not installed instead of showing a sign-in button that fails silently, and surface login failure reasons via status toast.

## 1.18.1

- 灵动岛样式：拔掉外接显示器再合盖、开盖后，灵动岛和全局快捷键可能完全没有反应，现在会在唤醒后重新对齐到当前屏幕。
- 诊断：`kill -USR1 <Rakko 的 PID>` 写出的诊断文件（`~/Library/Logs/Rakko/`）增加窗口位置、所在屏幕、是否被遮挡和快捷键触发记录。
- Windows：系统监控读取网络流量的方式更稳妥。

### English

- Dynamic Island style: after unplugging an external display, closing the lid and opening it again, the island and the global shortcut could stop responding entirely; Rakko now re-aligns to the current screen after waking.
- Diagnostics: the file `kill -USR1 <Rakko's PID>` writes to `~/Library/Logs/Rakko/` now includes the window's position, its display, whether it is occluded, and global shortcut activity.
- Windows: the system monitor reads network traffic more safely.

## 1.18.0

- **系统性能监控**：工具页新增「系统监控」卡片（支持 2×1、2×2、1×1 与桌面固定，1×1 点击切换 CPU / 内存 / 网络），平时保持单色，高负载或低电量时高亮提醒；悬停展开 60 秒实时走势、内存细分、磁盘读写、电池状态与占用前 3 的进程（可按 CPU / 内存排序）。
- **刘海与顶部胶囊常驻两翼**：「设置 → 外形与提示」可开启收起时的左右两翼实时监控（自由组合 CPU、内存、网络、磁盘、电池），遇到通知、悬停探出或倒计时自动让位，未放置卡片且未开两翼时不后台唤醒。
- **工具页编辑抽屉**：Windows 与单向滚轮鼠标可在底部工具库抽屉直接滚轮横向翻找工具；横向滚动条移至卡片下方独立通道不再遮挡标题；修复面板底角在编辑态下的直角虚影。

### English

- **System performance monitor**: add the System Monitor card to Tools (2×1, 2×2, 1×1 and desktop pins; tap 1×1 to cycle CPU / Memory / Network), staying monochrome at rest and lighting up only under load or low battery; hover for a 60-second chart, memory breakdown, disk R/W, battery state and top 3 processes sorted by CPU or memory.
- **Closed-notch and capsule wings**: optionally show live metrics on the left and right wings while closed (CPU, memory, network, disk, battery), yielding automatically to notices, hover peeks and timers, with zero background wakeups when unused.
- **Tools editing drawer**: scroll the bottom card library horizontally with a standard vertical mouse wheel, move the horizontal scrollbar below the tiles so it never covers titles, and fix the square corner ghosts at the bottom of the panel shell.

## 1.17.0

- **Codex 重置卡**：Codex 额度卡上能看到还剩几张重置卡和到期时间，点一下（确认后）就能立刻重置额度。
- **更多套餐额度与余额**：除了 Claude Code 和 Codex，还能看 GLM、DeepSeek 余额、Cursor、Kimi、OpenCode Go 和 Command Code；设置页按服务开关并可拖动排序，3–4 张自动加宽为两列，5–8 张切换为紧凑行。
- **AI 用量**：同时统计 Antigravity 应用（Gemini app）与 CLI 的本地会话用量；修复 Copilot 换表时本机用量可能多算的问题。
- **稳定性**：修复截图子进程卡住或菜单展开期间窗口点击状态不更新的问题。

### English

- **Codex reset credits**: the Codex quota card shows how many reset credits are left and when the earliest one expires; clicking to use one (after confirmation) resets the quota windows right away.
- **More subscription quotas and balances**: alongside Claude Code and Codex, track GLM, DeepSeek balance, Cursor, Kimi, OpenCode Go and Command Code; toggle and reorder them in Settings, with 3–4 cards widening to two columns and 5–8 switching to compact rows.
- **AI usage**: count local sessions from the Antigravity app together with the CLI, and fix Copilot usage occasionally being overcounted when its backing table rotates.
- **Stability**: fix stalled screenshot subprocesses and window click states not updating while a menu is open.

## 1.16.9

- 首页歌词：歌名里带「-」的歌能找到对的歌词；一个歌词来源很慢或出错时，会换另一个来源，失败后更快重试。
- 刘海收起时，实时活动、应用切换两翼和完成胶囊的伸缩动画更顺，灵动岛样式下不再闪一下空白。
- Hermes 同时开着几个会话时，每张审批卡都显示自己会话的项目。
- Windows：删除到回收站偶尔失败的问题修好了。

### English

- Home lyrics: songs with a "-" in the title find the right lyrics, and when one lyrics source is slow or fails, Rakko asks the other one and retries sooner.
- With the notch closed, live activities, the app-switch wings and the completion capsule grow and shrink smoothly, without a blank flash in the Dynamic Island style.
- With several Hermes sessions open, each permission card names its own session's project.
- Windows: moving files to the Recycle Bin no longer fails now and then.

## 1.16.8

- 灵动岛样式下把面板移到外接屏后，内建屏上也会留着空闲胶囊。
- 空闲胶囊上的应用切换按钮有了悬停效果，点它打开的是应用切换。
- 没有刘海的屏幕上展开时，胶囊上沿不再被菜单栏切掉一截；打开时第一帧不再跳一下。

### English

- With the Dynamic Island style, moving the panel to an external display keeps the resting capsule on the built-in screen.
- The app-switch button on the resting capsule now shows a hover state, and clicking it opens app switching.
- On a screen without a notch, the capsule's top edge is no longer clipped by the menu bar while it opens, and the first frame no longer jumps.

## 1.16.7

- opencode 2.x 的 Agent 完成提醒恢复正常（它改了事件格式，之前收不到提醒）。
- Hermes 的审批卡会显示正确的项目名，而不是启动目录。

### English

- Agent completion notices work again with opencode 2.x, which changed its event format.
- Hermes permission cards now name the project instead of the launch folder.

## 1.16.6

- Windows：opencode 的 AI 用量有时会少算，现在每次都能读到新记录。

### English

- Windows: opencode usage could be undercounted; new records are now always read.

## 1.16.5

- Windows：没有安装 HEIF / HEVC 扩展时，转换、压缩 HEIC 图片会直接说明需要安装哪个扩展，不再显示一串错误代码。

### English

- Windows: converting or compressing a HEIC image without the HEIF or HEVC extension now names the extension to install instead of showing an error code.

## 1.16.4

- 倒计时到点的铃声更可靠。
- 英文界面下，工具页 2×1 秒表在读数很长时名称不再被截成「Stop…」。

### English

- The timer's alarm sound is more reliable.
- In English, the 2 × 1 stopwatch card no longer cuts its name to "Stop…" when the reading gets long.

## 1.16.3

- Agent 完成提醒在 Mac 上会先确认收件的是 Rakko 本身再转发，避免别的程序读到提示词内容。
- 审批卡快到时间时点「允许」，卡片上显示的结果和 Agent 实际收到的一致了。

### English

- On the Mac, agent completion notices are only handed over once the receiver is confirmed to be Rakko itself, so no other program can read the prompt text.
- Clicking Allow just as a permission card runs out of time now gives the agent the same answer the card shows.

## 1.16.2

- 时间戳异常（比当前时间还新）的下载临时文件，不会再让下载进度一直显示「0 B」。
- 每周简报里「读不到」的提示改成和 AI 用量页一样的灰色说明文字。
- 安装包小了约 0.8 MB（去掉重复的图片）。

### English

- A download's temporary file with a timestamp in the future no longer keeps the download progress stuck at "0 B".
- The weekly report's "couldn't read" note now uses the same grey caption as the AI Usage page.
- The installer is about 0.8 MB smaller (duplicate images removed).

## 1.16.1

- 设置文件或布局文件被编辑器存成带 BOM 的格式时，Rakko 现在能正常读取，不会再退回默认设置。

### English

- Rakko now reads a settings or layout file that an editor saved with a byte-order mark, instead of falling back to the defaults.

## 1.16.0

- **Windows 版**：同一个 Rakko 现在也能在 Windows 10 22H2 / Windows 11（64 位）上用。屏幕顶部中间平时看不见，鼠标推到顶边才探出；也可以在设置里换成常驻的浮动胶囊。托盘、剪贴板、工具页、搜索、截图、框选文字、音乐、AI 用量和 Agent 提醒都有，资源管理器右键多了「添加到 Rakko」。全屏玩游戏或看视频时，碰到顶边不会再打扰前台程序。
- **设置窗口重做**：左侧换成一列「小岛」导航，所有页面同一个尺寸，可以拖边缘放大缩小，也能最大化。
- **截图**：面板开着时按截图，面板会一起截进画面；收起时和以前一样。框选文字也一样。
- **AI 用量更全**：除了原来的工具，现在也读 Antigravity CLI、Gemini CLI、Copilot CLI、Qwen Code、Kimi、Droid、Hermes 等十几个工具的本机记录，Codex 的压缩会话也能读了。没有费用记录的模型按公开价目表估算。装了但读不到用量的工具会在用量页和每周简报里写明「读不到」，不再悄悄算成 0。
- 工具页拖卡片更顺：拖出面板时卡片在面板里抬起，离开面板后才变成桌面卡片；松手落位不再跳。
- 「最近文件」「转 Word」「便利贴」换了更好认的图标。

### English

- **Rakko for Windows**: the same Rakko now runs on Windows 10 22H2 and Windows 11 (64-bit). The top centre of the screen stays out of sight until you push the pointer against the edge; Settings can switch it to an always-visible floating capsule. Tray, Clipboard, Tools, search, screenshots, Capture Text, music, AI Usage and agent notices are all there, and Explorer's right-click menu gains Add to Rakko. While a game or video runs full screen, touching the top edge leaves it alone.
- **Settings, rebuilt**: an island navigation column on the left, every page the same size, and a window you can resize or maximize.
- **Screenshots**: with the panel open, a screenshot now keeps the panel in the picture; with it closed nothing changes. Capture Text does the same.
- **Fuller AI usage**: besides the tools it already read, Rakko now reads the local records of Antigravity CLI, Gemini CLI, Copilot CLI, Qwen Code, Kimi, Droid, Hermes and more, and Codex's compressed sessions. Models without a recorded cost are estimated from a public price list. A tool that is installed but unreadable is named on the usage page and in the weekly report instead of counting as zero.
- Dragging Tools cards feels smoother: a card lifts inside the panel and becomes a desktop card only once it leaves; dropping it no longer jumps.
- Recent Files, To Word and Sticky Note have clearer icons.

## 1.15.1

- 设置 →「关于」里多了「请我喝杯咖啡」。

### English

- Settings › About now has Buy Me a Coffee.

## 1.15.0

- **工具页重做**：面板跟着卡片多少变大变小（3–6 列、1–3 行），不再固定 440 × 250。所有卡片换成同一套排法：左上图形和进度环、一个大数字、底行按钮；进行中是橙色，暂停时数字变灰，数字变化时逐位滚动。
- **编辑更直接**：卡片不再摆动；拖动时卡片随手倾斜，落点先出现虚线格。拖卡片右下角的弧形把手改尺寸，或右键卡片选尺寸。添加卡片改成编辑时面板底部的卡片库，点一下或拖进去；把卡片拖回卡片库就是移除。
- 倒计时先选预设再点 ▶ 开始；AI 用量 2×1 显示剩得最少的额度窗口；拖文件进来时，能处理的卡片显示文件数量。

### English

- **Tools, rebuilt**: the panel now grows and shrinks with its cards (3–6 columns, 1–3 rows) instead of staying 440 × 250. Every card shares one layout — a glyph and progress ring at the top left, one big number, buttons along the bottom; running is orange, paused greys the number, and changing digits roll.
- **Easier editing**: cards no longer wobble; a lifted card tilts with your hand and a dashed cell shows where it will land. Drag the arc in a card's corner to resize it, or right-click it for its sizes. Adding a card now happens in the card library at the bottom of the panel while editing — tap or drag; drag a card back onto the library to remove it.
- The timer starts with ▶ after you pick a preset; AI Usage at 2 × 1 shows the tighter quota window; while you drag files over Tools, the cards that can take them show the count.

## 1.14.5

- **倒计时和秒表**：在工具页的「+」目录里添加。倒计时有 1–60 分钟预设，也能自己输入时分秒；刘海收起时在摄像头两侧显示剩余时间，时间到弹出卡片并响铃，点「重复」或「停止」才收起。秒表精确到秒。
- **首页显示当前时间**：没在放歌时，首页大字显示当前时间和日期；放歌时，时间在左上角。跟随系统时区和 12 / 24 小时制。

### English

- **Timer and stopwatch**: add them from the Tools page's + catalogue. The timer has presets from 1 to 60 minutes or takes your own hours, minutes and seconds; with the notch closed it shows the time left beside the camera, and when it runs out a card drops from the notch and rings until you choose Repeat or Stop. The stopwatch counts to the second.
- **Current time on Home**: with nothing playing, Home shows the time in large type above the date; while music plays, the time sits at the top left. It follows your Mac's time zone and 12- or 24-hour setting.

## 1.14.4

- 刘海样式选「灵动岛」时，打开搜索再收起，面板不再先整块往上跳一下才缩回；灵动岛被拖离屏幕顶边后，搜索面板也不再比胶囊低一截（#6）。

### English

- With Notch Style set to Dynamic Island, closing the search panel no longer jumps the whole panel up before it folds back, and an island dragged away from the top edge no longer opens its search panel a step below the capsule (#6).

## 1.14.3

- 修复 1.14.2 引入的刘海动画卡顿：通知卡、实时活动、悬停探出、展开面板等状态切换时，内容不再整帧消失一下，动画恢复流畅。

### English

- Fixes notch animations that stuttered in 1.14.2: switching between notification cards, live activities, the hover peek and the open panel no longer blanks the content for a frame, and the motion is smooth again.

## 1.14.2

- 刘海样式选「灵动岛」时，展开面板不再闪一帧放大的黑色方块（#5）。
- 关掉 DeepSeek Harness 的提醒或审批其中一个开关时，不再把它的 `cordis.patch.yml` 写坏。
- 检查更新时正好在安装或等待重启，不再崩溃。
- 框选文字时如果还没有屏幕录制权限，不再把刚开好的权限清掉。
- 数据目录搬家或状态文件丢失后，图片 / PDF 转换的结果不再被误删。
- Rakko 自己写进剪贴板的内容，不再偶尔被记进剪贴板历史。

### English

- With Notch Style set to Dynamic Island, opening the panel no longer flashes an oversized black block for one frame (#5).
- Switching off one of DeepSeek Harness's notice or approval switches no longer leaves its `cordis.patch.yml` broken.
- Rakko no longer crashes when an update check lands while an update is installing or waiting for a restart.
- Selecting text on screen without Screen Recording permission no longer clears a grant you had just made.
- Converted images and PDFs are no longer deleted after the data folder moves or its state file goes missing.
- What Rakko itself puts on the clipboard no longer shows up in clipboard history now and then.

## 1.14.1

- **Agent 完成提醒和审批支持 Hermes Agent 与 DeepSeek Harness**：在 设置 → AI 里分别打开。审批只在它们本来就要问你时弹卡；45 秒没处理或 Rakko 没在运行，就交回它们自己的审批流程，从不替你自动允许。关掉开关时，Rakko 写进它们配置里的内容会全部移除。
- 首次启动引导和「系统权限」里，「屏幕录制」的说明补上了截图。

### English

- **Agent completion notices and approvals now support Hermes Agent and DeepSeek Harness**: turn each on in Settings → AI. A card appears only when the tool was going to ask you anyway; after 45 seconds, or when Rakko is not running, the request goes back to the tool's own approval flow, and nothing is ever allowed for you. Turning a switch off removes everything Rakko wrote into the tool's configuration.
- The Screen Recording permission text in the first-launch guide and System Permissions now mentions screenshots.

## 1.14.0

- **更省电**：Rakko 闲着时几乎不再占用 CPU。本机实测：空闲时 CPU 从约 3.3% 降到 0.6%，每秒唤醒次数从约 76 次降到 3–4 次，能耗约为原来的七分之一；播放音乐时 CPU 也从约 5.7% 降到 3.2%。
- 刘海展开更快一些。
- 剪贴板改在后台检测，不再占用界面线程；复制后约 0.1 秒内记下。

### English

- **Lighter on battery**: Rakko now barely touches the CPU while idle. Measured on a test Mac: idle CPU dropped from about 3.3% to 0.6%, wakeups from about 76 to 3–4 per second, and energy impact to about one seventh; with music playing, CPU dropped from about 5.7% to 3.2%.
- The notch opens a little faster.
- The clipboard is checked in the background instead of on the interface thread; a copy is recorded within about 0.1 seconds.

## 1.13.8

- **Agent 审批**：Claude Code、Codex、Qwen Code、Devin、opencode 要权限时，刘海里直接弹出审批卡：一句人话说明要做什么，有风险时标出来，展开能看完整命令；可以允许、拒绝（附一句原因）或交回终端。Copilot CLI、Droid、Antigravity、pi 只在高风险操作时弹卡。45 秒没处理就交回终端，从不替你自动允许。在 设置 → AI →「Agent 审批」开启，默认关闭。
- **更新窗口**：发现新版本时弹出更新窗口，更新说明就在里面；可以跳过这个版本、稍后提醒，或直接下载安装。窗口跟随系统深浅色。「以后自动下载并安装更新」默认不勾。
- **首次启动引导**：全新安装第一次打开时，刘海里会走一段简短引导；从「下载」或磁盘映像运行时，可以一键移到「应用程序」。升级用户不会看到。
- **设置**：「AI 用量」页改名为「AI」；更新相关的设置从「通用」移到「关于」，按钮改为「检查更新」。
- 下载改为推荐 `Rakko.zip`（只需放行一次），也可以用终端一行命令安装。
- opencode 的完成提醒改用 opencode V2 的插件格式。

### English

- **Agent approvals**: when Claude Code, Codex, Qwen Code, Devin or opencode asks for permission, a card on the notch says what it wants in plain words, flags risky actions and shows the full command on demand; allow, deny (with an optional reason) or hand it back to the terminal. Copilot CLI, Droid, Antigravity and pi ask only for risky actions. After 45 seconds the request goes back to the terminal; nothing is ever allowed for you. Turn it on in Settings → AI → Agent Approvals; off by default.
- **Update window**: a new version opens a window with its release notes, where you can skip it, be reminded later, or download and install it. It follows the system's light or dark appearance. Automatic download and install stays off unless you turn it on.
- **First-launch guide**: a fresh install opens a short guide in the notch and offers to move Rakko to Applications when it runs from Downloads or a disk image. Upgrades skip it.
- **Settings**: the AI Usage page is now AI; update settings moved from General to About, where the button reads Check for Updates.
- The recommended download is now `Rakko.zip` (one approval instead of two), or install from Terminal with one command.
- opencode completion notices now use opencode's V2 plugin format.
