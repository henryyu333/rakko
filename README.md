<p align="center">
  <img src="https://github.com/henryyu333/rakko/releases/download/v1.13.1/Rakko-icon.png" width="112" alt="Rakko icon">
</p>

<h1 align="center">Rakko</h1>

<p align="center">
  Files, clipboard, music and everyday tools that grow out of the top of your screen when you need them, and fold away when you're done.<br>
  Lives in the Mac notch and along the top edge on Windows.
</p>

<p align="center">
  <a href="https://github.com/henryyu333/rakko/releases/latest/download/Rakko.zip"><img src="https://img.shields.io/badge/Download_for_macOS-Rakko.zip-007AFF?style=for-the-badge&logo=apple&logoColor=white" alt="Download Rakko for macOS"></a>
  <a href="https://github.com/henryyu333/rakko/releases/latest/download/Rakko-Setup.exe"><img src="https://img.shields.io/badge/Download_for_Windows-Rakko--Setup.exe-0078D4?style=for-the-badge&logo=windows&logoColor=white" alt="Download Rakko for Windows"></a>
  <a href="https://rakko-yu.pages.dev"><img src="https://img.shields.io/badge/Website-rakko--yu.pages.dev-1F2328?style=for-the-badge&logo=cloudflarepages&logoColor=white" alt="Rakko Website"></a>
  <a href="https://rakko-yu.pages.dev/support/"><img src="https://img.shields.io/badge/Sponsor-Buy_me_a_coffee-EA4AAA?style=for-the-badge&logo=githubsponsors&logoColor=white" alt="Sponsor Rakko"></a>
</p>

<p align="center">
  <a href="https://github.com/henryyu333/rakko/releases/latest"><img src="https://img.shields.io/github/v/release/henryyu333/rakko?style=flat-square&label=release&color=2ea043" alt="Latest Release"></a>
  <a href="https://github.com/henryyu333/rakko/releases"><img src="https://img.shields.io/github/downloads/henryyu333/rakko/total?style=flat-square&label=downloads&color=007AFF" alt="Total Downloads"></a>
  <a href="https://github.com/henryyu333/rakko/stargazers"><img src="https://img.shields.io/github/stars/henryyu333/rakko?style=flat-square&color=f59e0b" alt="GitHub Stars"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square" alt="License: Apache-2.0"></a>
  <a href="TRADEMARKS.md"><img src="https://img.shields.io/badge/brand-not--licensed-lightgrey?style=flat-square" alt="Brand not licensed"></a>
  <img src="https://img.shields.io/badge/macOS-13%2B_Apple_Silicon-555?style=flat-square&logo=apple&logoColor=white" alt="macOS 13+ Apple Silicon">
  <img src="https://img.shields.io/badge/Windows-10_%7C_11_x64-0078D4?style=flat-square&logo=windows&logoColor=white" alt="Windows 10 | 11 x64">
  <img src="https://img.shields.io/badge/built_with-Go-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Built with Go">
</p>

<p align="center">
  <strong>English</strong> · <a href="README.zh-CN.md">简体中文</a> · <a href="docs/MANUAL.zh-CN.md">Manual (中文)</a>
</p>

---

## What it is

Rakko turns the top of your screen into a quiet working shelf: hover or push against the top edge to peek, click to search, drag files up to stage them, and hit `Esc` to fold it away. No Dock or taskbar clutter, no account, no cloud sync — Rakko tracks file paths without copying your files.

## Features

- **Tray & local file actions**: drop files, folders, images or text to keep at hand and drag into any app; convert images & PDFs (JPEG, PNG, HEIC, PDF, Word), compress to a target size, resize, zip/extract and batch-rename locally.
- **Clipboard, screenshots & search**: `⌃⌥V` searches clipboard history with source apps and OCR on copied images (skipping password managers); `⌃⌥A` freezes every screen to annotate, save or pin; `⌃⌥O` copies text from anywhere on screen; `⌃⌥Space` searches tray, clipboard, notes, apps and files together (`Ctrl+Alt` on Windows).
- **Tools, sticky notes & whiteboard**: customizable card grid that you can drag onto the desktop as pinned widgets — music player with synced lyrics, timer/stopwatch, keep awake, system monitor, plain-text sticky notes, and a standalone multi-board/multi-page whiteboard window with sketchy/clean/mono styles, bound arrows, images and PNG/SVG export.
- **AI usage & coding agent hooks** (optional): quota and balance cards for Codex (with reset credits), Claude Code, GLM, DeepSeek, Cursor, Kimi, OpenCode Go and Command Code plus local token/cost tracking across coding agents; notch cards for agent turn notices and permission approvals.
- **Live activities & desktop otter**: charging, headphones, downloads and now-playing visuals on the closed notch; a desktop sea otter that catches dropped files; weekly local & AI usage report.

See the [manual](docs/MANUAL.zh-CN.md) (Chinese) for every page, setting and Windows difference.

## Install

### macOS (Apple Silicon, macOS 13+)

Download **[Rakko.zip](https://github.com/henryyu333/rakko/releases/latest/download/Rakko.zip)** and open it, or install via Terminal with signature verification:

```sh
curl -fsSL https://github.com/henryyu333/rakko/releases/latest/download/install.sh | sh
```

<details>
<summary>First-launch Gatekeeper steps &amp; optional permissions</summary>

Rakko is free and not notarized by Apple, so macOS blocks the first launch (allow only downloads from this repository):
- **macOS 15+**: click **Done** → open **System Settings → Privacy & Security** → click **Open Anyway** next to Rakko and confirm.
- **macOS 13 / 14**: Control-click Rakko → **Open** → **Open**.

A short notch guide offers to move Rakko to Applications and turn on optional permissions (Full Disk Access, Accessibility, Screen Recording).
</details>

### Windows (Windows 10 22H2 / 11, 64-bit)

Download and run **[Rakko-Setup.exe](https://github.com/henryyu333/rakko/releases/latest/download/Rakko-Setup.exe)** (per-user, no admin required), or install via PowerShell with SHA-256 verification:

```powershell
irm https://github.com/henryyu333/rakko/releases/latest/download/install.ps1 | iex
```

<details>
<summary>SmartScreen note &amp; Windows behavior</summary>

The installer is unsigned. If "Windows protected your PC" appears, confirm it came from this repository and choose **More info → Run anyway**. Push the pointer against the top center or press `Ctrl+Alt+Space` to open; fullscreen games and videos are left undisturbed. Explorer adds **Add to Rakko** to the context menu.
</details>

## Privacy & Updates

Everything stays on your machine (`~/Library/Application Support/Rakko/` on macOS, `%APPDATA%\Rakko` on Windows): no account, no telemetry, no cloud sync. Rakko only connects when checking signed GitHub releases once a day (can be turned off), fetching lyrics from lrclib.net / NetEase while the lyrics column is open, or querying official quota endpoints for the AI cards you turn on (using local tool credentials or keys stored in a user-only `0600` file).

## Build from source

Requires Go 1.27+ (and Xcode Command Line Tools on macOS), built with [MyGo](https://github.com/egoist/mygo):

```sh
go run github.com/egoist/mygo/cmd/mygo build                          # macOS
go run github.com/egoist/mygo/cmd/mygo build -platform windows/amd64  # Windows
```

<details>
<summary>Repository layout &amp; checks</summary>

- `cmd/rakko/` — application panels, pages, tools, whiteboard window and tests
- `internal/platform/` — OS integration (`_darwin.go` / `_windows.go`)
- `internal/board/` — whiteboard document, editor core and geometry
- `internal/` — storage, settings, layout, notes, AI usage, agent hooks, document export
- `docs/` — user manual

Brand icons and otter artwork are excluded from the repository, so source builds show default icons. Checks: `go vet ./...` and `go test -race -count=1 ./...`.
</details>

## Support & License

Rakko is built solo in my spare time. If it helps you, you can [buy me a coffee](https://rakko-yu.pages.dev/support/).

Source code is licensed under [Apache-2.0](LICENSE) ([NOTICE](NOTICE), `resources/THIRD_PARTY_NOTICES.txt`; v1.0.0–v1.12.1 remain MIT). The Rakko name, logo, app/tray icons and otter character are reserved — see [TRADEMARKS.md](TRADEMARKS.md).
