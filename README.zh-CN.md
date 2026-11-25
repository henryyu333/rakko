<p align="center">
  <img src="https://github.com/henryyu333/rakko/releases/download/v1.13.1/Rakko-icon.png" width="112" alt="Rakko icon">
</p>

<h1 align="center">Rakko</h1>

<p align="center">
  需要时从屏幕顶部长出来、用完就收回去的随手工具箱。<br>
  Mac 住在刘海里，Windows 住在屏幕顶边。
</p>

<p align="center">
  <a href="https://github.com/henryyu333/rakko/releases/latest/download/Rakko.zip"><img src="https://img.shields.io/badge/macOS_下载-Rakko.zip-007AFF?style=for-the-badge&logo=apple&logoColor=white" alt="下载 Rakko macOS 版"></a>
  <a href="https://github.com/henryyu333/rakko/releases/latest/download/Rakko-Setup.exe"><img src="https://img.shields.io/badge/Windows_下载-Rakko--Setup.exe-0078D4?style=for-the-badge&logo=windows&logoColor=white" alt="下载 Rakko Windows 版"></a>
  <a href="https://rakko-yu.pages.dev"><img src="https://img.shields.io/badge/官网-rakko--yu.pages.dev-1F2328?style=for-the-badge&logo=cloudflarepages&logoColor=white" alt="Rakko 官网"></a>
  <a href="https://rakko-yu.pages.dev/support/"><img src="https://img.shields.io/badge/支持作者-请我喝杯咖啡-EA4AAA?style=for-the-badge&logo=githubsponsors&logoColor=white" alt="请我喝杯咖啡"></a>
</p>

<p align="center">
  <a href="https://github.com/henryyu333/rakko/releases/latest"><img src="https://img.shields.io/github/v/release/henryyu333/rakko?style=flat-square&label=%E6%9C%80%E6%96%B0%E7%89%88%E6%9C%AC&color=2ea043" alt="最新版本"></a>
  <a href="https://github.com/henryyu333/rakko/releases"><img src="https://img.shields.io/github/downloads/henryyu333/rakko/total?style=flat-square&label=%E6%80%BB%E4%B8%8B%E8%BD%BD&color=007AFF" alt="总下载量"></a>
  <a href="https://github.com/henryyu333/rakko/stargazers"><img src="https://img.shields.io/github/stars/henryyu333/rakko?style=flat-square&label=Stars&color=f59e0b" alt="GitHub Stars"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square" alt="License: Apache-2.0"></a>
  <a href="TRADEMARKS.md"><img src="https://img.shields.io/badge/brand-not--licensed-lightgrey?style=flat-square" alt="Brand not licensed"></a>
  <img src="https://img.shields.io/badge/macOS-13%2B_Apple_%E8%8A%AF%E7%89%87-555?style=flat-square&logo=apple&logoColor=white" alt="macOS 13+ Apple 芯片">
  <img src="https://img.shields.io/badge/Windows-10_%7C_11_64%E4%BD%8D-0078D4?style=flat-square&logo=windows&logoColor=white" alt="Windows 10 | 11 64位">
  <img src="https://img.shields.io/badge/built_with-Go-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Built with Go">
</p>

<p align="center">
  <a href="README.md">English</a> · <strong>简体中文</strong> · <a href="docs/MANUAL.zh-CN.md">使用手册</a>
</p>

---

## 这是什么

Rakko 把屏幕顶部变成一块平时看不见的效率面板：悬停或推到顶边探出，点击打开搜索，把文件拖过去直接暂存，按 `Esc` 收回。不占 Dock / 任务栏，不用注册账号，不上传数据，所有文件只记录路径而不复制进托盘。

## 主要功能

- **托盘与本地文件处理**：暂存文件、文件夹、图片和文字，随时拖进任意应用；本机完成图片/PDF 格式转换（JPEG / PNG / HEIC / PDF / Word）、按大小压缩、缩放、打包解压和批量重命名。
- **剪贴板、截图与全局搜索**：`⌃⌥V` 检索含来源应用和图片 OCR 文字的剪贴板历史（自动跳过密码管理器）；`⌃⌥A` 冻结全屏截图、标注并钉在桌面，`⌃⌥O` 框选识别屏幕文字；`⌃⌥Space` 一框搜托盘、剪贴板、便利贴、应用与文件（Windows 均改用 `Ctrl+Alt`）。
- **工具页、便利贴与白板**：自由排列的工具卡片，任意卡片拖出即可钉在桌面；内置音乐播放与滚动歌词、倒计时/秒表、保持唤醒、系统监控、纯文本便利贴，以及支持多画板多页、手绘/干净/单色风格、连线箭头、贴图和 PNG/SVG 导出的独立白板窗口。
- **AI 用量与 Agent 协作**（可选）：查看 Codex（含重置卡）、Claude Code、GLM、DeepSeek、Cursor、Kimi、OpenCode Go、Command Code 等套餐额度与本机多工具 Token 费用估算；Agent 跑完一轮或申请权限时，直接在刘海弹出提醒与审批卡。
- **实时活动与桌面海獭**：充电、耳机、下载进度和音乐封面在收起态短暂显示；桌面小海獭随时接住拖给它的文件；每周生成本机使用与 AI 用量简报。

完整说明与平台差异见 [使用手册](docs/MANUAL.zh-CN.md)。

## 安装

### macOS（Apple 芯片，macOS 13+）

下载 **[Rakko.zip](https://github.com/henryyu333/rakko/releases/latest/download/Rakko.zip)** 打开运行，或用终端一键安装（带签名校验）：

```sh
curl -fsSL https://github.com/henryyu333/rakko/releases/latest/download/install.sh | sh
```

<details>
<summary>首次打开拦截说明与可选权限</summary>

Rakko 免费开源、未做 Apple 公证，首次打开会被系统拦下（只放行从本仓库下载的版本）：
- **macOS 15+**：点「完成」→ 打开 **系统设置 → 隐私与安全性** → 点 Rakko 旁的「仍要打开」并验证。
- **macOS 13 / 14**：按住 Control 点击 Rakko → 选「打开」→ 再点「打开」。

打开后刘海引导会协助移入「应用程序」，并按需引导开启可选权限（完全磁盘访问、辅助功能、屏幕录制）。
</details>

### Windows（Windows 10 22H2 / 11，64 位）

下载 **[Rakko-Setup.exe](https://github.com/henryyu333/rakko/releases/latest/download/Rakko-Setup.exe)** 安装（仅当前用户，无需管理员权限），或用 PowerShell 一键安装（校验 SHA-256）：

```powershell
irm https://github.com/henryyu333/rakko/releases/latest/download/install.ps1 | iex
```

<details>
<summary>SmartScreen 提示与使用说明</summary>

安装包未签名，如弹出「Windows 已保护你的电脑」，确认来自本仓库后点 **更多信息 → 仍要运行**。安装后把鼠标推到屏幕顶部中间或按 `Ctrl+Alt+Space` 打开，全屏游戏/视频时自动让路。右键菜单新增「添加到 Rakko」。
</details>

## 隐私与更新

所有内容保存在本机（macOS `~/Library/Application Support/Rakko/`，Windows `%APPDATA%\Rakko`），无账号、无遥测、无云同步。应用仅在以下场景联网：每日检查本仓库签名更新（可关闭）、打开歌词栏时向 lrclib.net / 网易云查歌词、开启对应 AI 额度卡时用本机凭证或你填写的本地 Key（仅本人可读的 `0600` 文件）查询官方额度接口。

## 从源码构建

需要 Go 1.27+（macOS 另需 Xcode 命令行工具），基于 [MyGo](https://github.com/egoist/mygo) 构建：

```sh
go run github.com/egoist/mygo/cmd/mygo build                          # macOS
go run github.com/egoist/mygo/cmd/mygo build -platform windows/amd64  # Windows
```

<details>
<summary>源码目录与测试命令</summary>

- `cmd/rakko/`：应用主体（面板、页面、工具、白板窗口与测试）
- `internal/platform/`：系统集成（`_darwin.go` / `_windows.go`）
- `internal/board/`：白板文档、编辑器内核与几何
- `internal/`：存储、设置、布局、便利贴、AI 用量、Agent 钩子、文档导出
- `docs/`：使用手册

自建版本不含保留的品牌图标与海獭素材，会显示默认图标。检查命令：`go vet ./...`、`go test -race -count=1 ./...`。
</details>

## 支持与许可

Rakko 由我利用业余时间独立开发，欢迎[请我喝杯咖啡](https://rakko-yu.pages.dev/support/)。

源码基于 [Apache-2.0](LICENSE) 开源（附 [NOTICE](NOTICE) 与 `resources/THIRD_PARTY_NOTICES.txt`；v1.0.0 至 v1.12.1 为 MIT）。Rakko 名称、Logo、应用/托盘图标与海獭形象保留品牌权利，见 [TRADEMARKS.md](TRADEMARKS.md)。
