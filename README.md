<p align="center">
  <img src="https://github.com/henryyu333/rakko/releases/download/v1.13.1/Rakko-icon.png" width="128" alt="Rakko icon">
</p>

<h1 align="center">Rakko</h1>

<p align="center">
  Files, clipboard, music and everyday tools that grow out of the top of your screen when you need them, and fold away when you're done. For the Mac notch and for Windows.<br>
  需要时从屏幕顶部长出来、用完就收回去的工具：文件、剪贴板、音乐和常用小工具。Mac 住在刘海里，Windows 住在屏幕顶边。
</p>

<p align="center">
  <a href="https://github.com/henryyu333/rakko/releases/latest/download/Rakko.zip"><img src="https://img.shields.io/badge/Download_for_macOS-Rakko.zip-007AFF?style=for-the-badge&logo=apple&logoColor=white" alt="Download Rakko for macOS"></a>
  <a href="https://github.com/henryyu333/rakko/releases/latest/download/Rakko-Setup.exe"><img src="https://img.shields.io/badge/Download_for_Windows-Rakko--Setup.exe-0078D4?style=for-the-badge&logo=windows&logoColor=white" alt="Download Rakko for Windows"></a>
</p>

<p align="center">
  macOS 13+ on Apple Silicon · Windows 10 22H2 / 11, 64-bit · Free and open source · <a href="https://rakko-yu.pages.dev">Website</a> · <a href="https://github.com/henryyu333/rakko/releases/latest">All downloads</a> · <a href="https://rakko-yu.pages.dev/support/">Buy me a coffee</a><br>
  Apple 芯片 macOS 13 或以上 · Windows 10 22H2 / 11 64 位 · 免费开源 · <a href="https://rakko-yu.pages.dev">官网</a> · <a href="https://github.com/henryyu333/rakko/releases/latest">全部下载</a> · <a href="https://rakko-yu.pages.dev/support/">请我喝杯咖啡</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License: Apache-2.0"></a>
  <a href="TRADEMARKS.md"><img src="https://img.shields.io/badge/brand-not--licensed-lightgrey.svg" alt="Brand not licensed"></a>
  <img src="https://img.shields.io/badge/macOS-13%2B-blue.svg" alt="macOS 13+">
  <img src="https://img.shields.io/badge/Windows-10%20%7C%2011-blue.svg" alt="Windows 10 | 11">
  <img src="https://img.shields.io/badge/built%20with-Go-00ADD8.svg" alt="Built with Go">
</p>

<p align="center">
  <a href="#english">English</a> | <a href="#简体中文">简体中文</a>
</p>

---

<a id="english"></a>

## What it is

Rakko turns the top of the screen into a working shelf. On a MacBook it lives in the camera notch; on a Mac without a notch, or on Windows, it is a small black shape at the top centre. It stays out of the way until you want it: hover it (on Windows, push the pointer against the top edge) and it peeks out; click and a search bar opens; drag a file toward it and the Tray opens, ready for the drop. Esc steps back, and the next Esc folds everything away.

There is no account, no Dock or taskbar window, and nothing leaves your computer except what is listed under [Privacy](#privacy). Rakko never copies your files into the shelf — it remembers where they are.

## What it does

- **Tray** — drop files, folders, pictures or text on the notch to keep them at hand, then drag them into any app. Convert pictures and PDFs (JPEG, PNG, HEIC, PDF, Word), compress pictures to a file size, resize, zip, extract archives and rename a batch of files, all on your computer.
- **Clipboard history** — everything you copy, with the app it came from; text in screenshots and copied pictures is recognized and searchable. Press `⌃⌥V` (Windows `Ctrl+Alt+V`), type to filter, and paste. Password managers' private copies are skipped, and you can exclude any app.
- **Search** — `⌃⌥Space` (Windows `Ctrl+Alt+Space`) searches the Tray, the clipboard, your notes, apps, folders and files in one list.
- **Tools** — a grid of cards you arrange yourself: music player, timer and stopwatch, keep awake, AI usage, sticky notes, recent files, the clipboard's latest item, and one-tap file actions. Drag a card out of the panel to keep it on the desktop.
- **Screenshot and Capture Text** — `⌃⌥A` freezes every screen to pick a window or an area, mark it up and copy, save or pin it; `⌃⌥O` copies the text from any part of the screen. With the panel open, it stays in the picture.
- **Music** — the player on Home with synced lyrics; on the closed notch, the cover and a moving equalizer.
- **AI usage** (optional) — Claude Code and Codex quotas, and this computer's token use and estimated cost across the coding agents you use: omp, Claude Code, Codex, Antigravity CLI, Gemini CLI, Copilot CLI, opencode, Grok, pi, Qwen Code, Kimi, Droid, Hermes and more. A tool that is installed but whose usage cannot be read is named, not counted as zero.
- **Agent notices and approvals** (optional) — a card drops from the notch when an agent finishes a turn, and an agent's permission request can be approved or denied right there.
- **Weekly report** — last week's computer use (opt-in), AI usage and what Rakko did for you.
- **Live activities** — charging, headphones and downloads show for a moment on the closed notch.
- **Desktop otter** — a small sea otter on the desktop that catches what you drop on it.

The [manual](docs/MANUAL.zh-CN.md) (Chinese) covers every page and setting.

## Install on macOS

Requires Apple Silicon (M1 or later) and macOS 13 or later.

1. Download **[Rakko.zip](https://github.com/henryyu333/rakko/releases/latest/download/Rakko.zip)** and open it in Downloads.
2. Double-click **Rakko**. Rakko is free and not notarized by Apple, so macOS stops it the first time:
   - **macOS 15 or later**: click **Done**, open **System Settings → Privacy & Security**, click **Open Anyway** next to the Rakko line, confirm with your password or Touch ID, then **Open Anyway** again.
   - **macOS 13 or 14**: Control-click Rakko, choose **Open**, then **Open**.
3. A short guide in the notch offers to move Rakko to Applications and to turn on the optional permissions.

Only allow a copy downloaded from this repository. If macOS says the app **is damaged**, delete it and download it again. Prefer Terminal? This installs the latest release with its signature checked:

```sh
curl -fsSL https://github.com/henryyu333/rakko/releases/latest/download/install.sh | sh
```

**Optional permissions** (Settings → System Permissions walks you through them): Full Disk Access, so files dragged from Downloads, Desktop or Documents need no extra prompt and finished downloads show on the notch; Accessibility, for the notch's drop area and arranging windows; Screen Recording, for Screenshot and Capture Text.

## Install on Windows

Requires Windows 10 22H2 or Windows 11, 64-bit.

1. Download **[Rakko-Setup.exe](https://github.com/henryyu333/rakko/releases/latest/download/Rakko-Setup.exe)** and run it. It installs for your account only; no administrator rights needed.
2. Rakko is not code-signed, so Windows may say "Windows protected your PC": click **More info** → **Run anyway**. Only do this for a file downloaded from this repository.
3. Push the pointer against the top centre of the screen, or press `Ctrl+Alt+Space`.

Prefer PowerShell? This installs the latest release for your account, with the installer's SHA-256 checked against the release:

```powershell
irm https://github.com/henryyu333/rakko/releases/latest/download/install.ps1 | iex
```

Rakko sits in the notification area. Settings → Appearance can switch the hidden top edge to an always-visible floating capsule. While a game or video runs full screen, the top edge is left alone; the shortcut still opens Rakko. Explorer's right-click menu gains **Add to Rakko**, and Send To gains **Rakko**.

Shortcuts use Ctrl and Alt where the Mac uses ⌘, ⌥ and ⌃. AirDrop and the permissions page are Mac-only; Windows Search takes Spotlight's place, and HEIC needs Microsoft's codec extensions. The manual's [Windows chapter](docs/MANUAL.zh-CN.md#windows-版) lists the rest.

## Updates

Rakko checks this repository's releases once a day and shows the release notes when a new version is out; Settings → About can turn the check off or install updates automatically. Every update is signed, and Rakko refuses one whose signature does not match.

<a id="privacy"></a>

## Privacy

Everything stays on your computer: no account, no telemetry, no cloud sync. Rakko makes only these network requests:

- **Update check** — asks this repository's latest GitHub release whether a newer version exists, sending only the app version.
- **Lyrics** — only while the lyrics column is open, the song's title, artist, album and length go to lrclib.net, and title and artist to NetEase Cloud Music when lrclib.net has nothing.
- **AI quotas** — only if you turn on the AI usage page: the Codex quota is asked from chatgpt.com with the login Codex saved, and the Claude Code quota is read by running your installed Claude Code.

Local AI usage is read from files on your computer and priced with a list bundled in the app. Agent notices and approvals travel over a local socket (a named pipe on Windows) in Rakko's own folder. Requests follow the system proxy. Clipboard text is stored as plain text in Rakko's data folder: `~/Library/Application Support/Rakko/` on macOS, `%APPDATA%\Rakko` on Windows.

## Build from source

Requires Go 1.27 or later; on macOS also the Xcode Command Line Tools. From the repository root:

```sh
go run github.com/egoist/mygo/cmd/mygo build                          # macOS: Rakko.app and a .dmg
go run github.com/egoist/mygo/cmd/mygo build -platform windows/amd64  # Windows installer and Rakko.exe
```

The Windows build works from macOS or Windows; MyGo fetches NSIS for the installer the first time. Output lands under the `out` folder named in `mygo.json`. Rakko is written in Go with [MyGo](https://github.com/egoist/mygo). The app icon, menu-bar icon and otter artwork are not in this repository, so a build from source shows default artwork.

| Path | Contents |
| --- | --- |
| `cmd/rakko/` | The app: panels, pages, tools and their tests |
| `internal/platform/` | System integration — `_darwin.go` for macOS, `_windows.go` for Windows |
| `internal/` | Storage, settings, layout, notes, agent hooks, AI usage, document export |
| `docs/` | User manual |

Checks: `go vet ./...` and `go test -race -count=1 ./...`.

## Support

I make Rakko on my own, in my spare time. If it is useful to you, you can [buy me a coffee](https://rakko-yu.pages.dev/support/).

## License

The source code is licensed under the Apache License 2.0 ([LICENSE](LICENSE)); required notices are in [NOTICE](NOTICE), and third-party software in `resources/THIRD_PARTY_NOTICES.txt`.

The Rakko brand is not covered by that license: the name, logo, app icon, menu-bar icon and the sea otter character are reserved — see [TRADEMARKS.md](TRADEMARKS.md). You may build and run Rakko from source and say your work is based on Rakko; a modified release must use a different name and icons. Releases v1.0.0 through v1.12.1 were published under the MIT License and remain available under it.

---

<a id="简体中文"></a>

## Rakko 是什么

Rakko 把屏幕顶部变成一个随手可用的架子。在 MacBook 上它住在摄像头的刘海里；在没有刘海的 Mac 和 Windows 上，它是屏幕顶部中间的一小块黑色。平时不占地方：鼠标停到上面（Windows 是把鼠标推到屏幕顶边）它会探出来；点一下打开搜索栏；把文件往那里拖，托盘自己打开等你放下。按 Esc 退一步，再按一次就全部收回去。

不用注册账号，不在 Dock 或任务栏占窗口，除了[隐私](#隐私)里列出的几项，什么都不会离开你的电脑。Rakko 不会把文件复制进托盘，只记住它们在哪。

## 能做什么

- **托盘**：把文件、文件夹、图片或文字拖到刘海上先放着，用时再拖进任何应用。图片和 PDF 可以转格式（JPEG、PNG、HEIC、PDF、Word），图片可以压到指定大小、缩小尺寸，还能打 zip、解压、批量重命名，全部在本机完成。
- **剪贴板历史**：复制过的内容连同来源应用都会记下，截图和复制的图片里的文字也能搜。按 `⌃⌥V`（Windows 是 `Ctrl+Alt+V`），打字筛选，直接粘贴。密码管理器标记为私密的内容不记，也可以排除指定应用。
- **搜索**：`⌃⌥Space`（Windows 是 `Ctrl+Alt+Space`）在一个列表里搜托盘、剪贴板、便利贴、应用、文件夹和文件。
- **工具页**：自己摆放的一格格卡片：音乐、倒计时和秒表、保持唤醒、AI 用量、便利贴、最近文件、剪贴板最新一条，以及一点就能用的文件操作。把卡片拖出面板，就能固定在桌面上。
- **截图和框选文字**：`⌃⌥A` 定格所有屏幕，选窗口或拖出区域，标注后复制、保存或钉在桌面；`⌃⌥O` 识别屏幕任意区域里的文字并复制。面板开着时截图，面板会一起截进去。
- **音乐**：首页是播放器，带滚动歌词；刘海收起时显示封面和跳动的均衡器。
- **AI 用量**（可选）：Claude Code 和 Codex 的额度，以及这台电脑上各个编程 Agent 的 token 用量和费用估算：omp、Claude Code、Codex、Antigravity CLI、Gemini CLI、Copilot CLI、opencode、Grok、pi、Qwen Code、Kimi、Droid、Hermes 等。装了但读不到用量的工具会写明「读不到」，不会当成 0。
- **Agent 提醒和审批**（可选）：Agent 跑完一轮时从刘海弹出卡片；Agent 申请权限时，可以直接在刘海里允许或拒绝。
- **每周简报**：上周的电脑使用情况（需要你开启）、AI 用量，以及 Rakko 帮你做了什么。
- **实时活动**：充电、耳机连接、下载进度会在收起的刘海上显示一会儿。
- **桌面海獭**：桌面上的一只小海獭，把东西丢给它也能收进托盘。

每个页面和设置的详细说明见[使用手册](docs/MANUAL.zh-CN.md)。

## 在 macOS 上安装

需要 Apple 芯片（M1 或更新）和 macOS 13 或以上。

1. 下载 **[Rakko.zip](https://github.com/henryyu333/rakko/releases/latest/download/Rakko.zip)**，在「下载」里打开。
2. 双击 **Rakko**。Rakko 免费，没有经过 Apple 公证，第一次打开会被拦下：
   - **macOS 15 或以上**：点 **完成**，打开 **系统设置 → 隐私与安全性**，在 Rakko 那一行点 **仍要打开**，输入密码或用触控 ID 确认，再点一次 **仍要打开**。
   - **macOS 13 或 14**：按住 Control 点 Rakko，选 **打开**，再点 **打开**。
3. 刘海里会出现一段简短引导，帮你把 Rakko 移到「应用程序」，并打开可选的权限。

只放行从本仓库下载的版本。如果系统提示应用**已损坏**，删掉后重新下载。习惯用终端的话，下面这行会安装最新版并校验签名：

```sh
curl -fsSL https://github.com/henryyu333/rakko/releases/latest/download/install.sh | sh
```

**可选权限**（设置 → 系统权限 会一步步带你打开）：完全磁盘访问，从「下载」「桌面」「文稿」拖文件时不再弹提示，下载完成也能在刘海上显示；辅助功能，用于刘海的投放区和排列窗口；屏幕录制，用于截图和框选文字。

## 在 Windows 上安装

需要 Windows 10 22H2 或 Windows 11，64 位。

1. 下载 **[Rakko-Setup.exe](https://github.com/henryyu333/rakko/releases/latest/download/Rakko-Setup.exe)** 并运行。只安装给当前用户，不需要管理员权限。
2. Rakko 没有代码签名，Windows 可能提示「Windows 已保护你的电脑」：点 **更多信息** → **仍要运行**。只放行从本仓库下载的文件。
3. 把鼠标推到屏幕顶部中间，或按 `Ctrl+Alt+Space`。

习惯用 PowerShell 的话，下面这行会为当前用户安装最新版，并按 Release 里的校验值核对安装包：

```powershell
irm https://github.com/henryyu333/rakko/releases/latest/download/install.ps1 | iex
```

Rakko 的图标在任务栏通知区域。设置 →「外形与提示」可以把隐藏的顶边换成常驻的浮动胶囊。全屏玩游戏或看视频时，碰到顶边不会打扰前台程序，快捷键照样能打开。资源管理器的右键菜单会多出 **添加到 Rakko**，「发送到」里会多出 **Rakko**。

Mac 上用 ⌘、⌥、⌃ 的快捷键，在 Windows 上换成 Ctrl 和 Alt。隔空投送和系统权限页只有 Mac 有；搜索用 Windows 搜索代替聚焦；HEIC 需要微软的编解码扩展。其余差异见手册的 [Windows 版](docs/MANUAL.zh-CN.md#windows-版)一章。

## 更新

Rakko 每天检查一次本仓库的新版本，有新版时显示更新说明；在 设置 → 关于 里可以关闭检查，或改成自动安装。每个更新都有签名，签名对不上的更新不会安装。

<a id="隐私"></a>

## 隐私

所有内容都留在你的电脑上：不用账号，不收集使用数据，不做云同步。Rakko 只会发出这几类网络请求：

- **检查更新**：向本仓库最新的 GitHub Release 询问是否有新版本，只带上应用版本号。
- **歌词**：只在歌词栏打开时，把歌曲名、歌手、专辑和时长发给 lrclib.net；lrclib.net 没有时，把歌曲名和歌手发给网易云音乐。
- **AI 额度**：只在你打开 AI 用量页后：Codex 额度用 Codex 保存的登录信息向 chatgpt.com 查询；Claude Code 额度通过运行你安装的 Claude Code 读取。

本机 AI 用量从你电脑上的文件读取，费用用应用内置的价目表估算。Agent 提醒和审批通过 Rakko 自己文件夹里的本地 socket（Windows 上是命名管道）传递。网络请求遵循系统代理。剪贴板文字以纯文本保存在 Rakko 的数据文件夹：macOS 是 `~/Library/Application Support/Rakko/`，Windows 是 `%APPDATA%\Rakko`。

## 从源码构建

需要 Go 1.27 或以上；macOS 还需要 Xcode 命令行工具。在仓库根目录运行：

```sh
go run github.com/egoist/mygo/cmd/mygo build                          # macOS：Rakko.app 和 .dmg
go run github.com/egoist/mygo/cmd/mygo build -platform windows/amd64  # Windows 安装包和 Rakko.exe
```

Windows 版在 macOS 或 Windows 上都能构建，第一次构建时 MyGo 会自动下载 NSIS 用来生成安装包。产物在 `mygo.json` 指定的 `out` 文件夹里。Rakko 用 Go 和 [MyGo](https://github.com/egoist/mygo) 编写。应用图标、菜单栏图标和海獭图片不在仓库里，自己构建的版本会显示默认图标。

| 路径 | 内容 |
| --- | --- |
| `cmd/rakko/` | 应用本体：面板、页面、工具和测试 |
| `internal/platform/` | 系统对接：`_darwin.go` 是 macOS，`_windows.go` 是 Windows |
| `internal/` | 存储、设置、布局、便利贴、Agent 钩子、AI 用量、文档导出 |
| `docs/` | 使用手册 |

检查：`go vet ./...` 和 `go test -race -count=1 ./...`。

## 支持

Rakko 是我一个人利用业余时间做的。如果它对你有用，可以[请我喝杯咖啡](https://rakko-yu.pages.dev/support/)。

## 许可

源代码采用 Apache License 2.0（见 [LICENSE](LICENSE)）；必须随附的声明在 [NOTICE](NOTICE)，第三方软件声明在 `resources/THIRD_PARTY_NOTICES.txt`。

Rakko 品牌不在这份许可范围内：名称、标志、应用图标、菜单栏图标和海獭形象均保留权利，见 [TRADEMARKS.md](TRADEMARKS.md)。你可以从源码构建、运行 Rakko，并说明作品基于 Rakko；发布修改版时必须换用不同的名称和图标。v1.0.0 至 v1.12.1 以 MIT 许可发布，这些版本仍可按 MIT 使用。
