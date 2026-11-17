# Changelog

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
