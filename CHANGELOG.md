# Changelog

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
