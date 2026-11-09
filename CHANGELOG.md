# Changelog

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
