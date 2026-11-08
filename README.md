<p align="center">
  <img src="https://github.com/henryyu333/rakko/releases/download/v1.13.1/Rakko-icon.png" width="128" alt="Rakko icon">
</p>

<h1 align="center">Rakko</h1>

<p align="center">
  A notch tool for the Mac. Files, clipboard, music and everyday tools grow out of the MacBook notch when you need them, and fold back into it when you're done.<br>
  住在 MacBook 刘海里的工具：文件、剪贴板、音乐和常用小工具，需要时从刘海里长出来，用完就缩回去。
</p>

<p align="center">
  <strong>Early development · 早期开发中</strong><br>
  Features and stability are still being improved. · 功能与稳定性仍在持续完善。
</p>

<p align="center">
  <a href="https://github.com/henryyu333/rakko/releases/latest/download/Rakko.dmg"><img src="https://img.shields.io/badge/Download_for_macOS-Rakko.dmg-007AFF?style=for-the-badge&logo=apple&logoColor=white" alt="Download Rakko.dmg for macOS"></a>
</p>

<p align="center">
  Apple Silicon · macOS 13+ · Free and open source · <a href="#install">Install guide</a> · <a href="https://github.com/henryyu333/rakko/releases/latest">All downloads</a><br>
  Apple Silicon 芯片 · macOS 13 或以上 · 免费开源 · <a href="#安装">安装说明</a> · <a href="https://github.com/henryyu333/rakko/releases/latest">全部下载</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License: Apache-2.0"></a>
  <a href="TRADEMARKS.md"><img src="https://img.shields.io/badge/brand-not--licensed-lightgrey.svg" alt="Brand not licensed"></a>
  <img src="https://img.shields.io/badge/macOS-13%2B%20Apple%20Silicon-blue.svg" alt="macOS 13+ Apple Silicon">
  <img src="https://img.shields.io/badge/built%20with-Go-00ADD8.svg" alt="Built with Go">
</p>

<p align="center">
  <a href="#english">English</a> | <a href="#简体中文">简体中文</a>
</p>

---

<a id="english"></a>

## What is Rakko

Rakko is a notch tool for the Mac: it turns the black notch around the camera into a working part of the screen. It stays out of sight until you need it: hover the notch and it grows a pair of wings, or a small down-arrow pill, and a click opens the search bar. Type to search this Mac — the sheet under the bar lists the Tray and Clipboard first, then your sticky notes, applications, folders, files and actions — or pick a page from the capsule hanging under the bar: Home, Tray, Clipboard, Tools, and AI Usage when you turn it on. A page grows out of the notch in the bar's place, 440 × 250; Home widens to 640 while its lyrics column is open. Drag something onto the notch instead and the Tray opens on its own, already waiting for the drop. Only the black shape on screen takes clicks: the menu bar beside it works as usual. Esc steps back…

It lives in the menu bar, has no Dock icon, keeps everything on your Mac, and never copies your files — it only remembers where they are.

## Highlights

**A shelf in the notch**
- Drag files, folders, images or text toward the notch — it shows "Release to Add" and the Tray opens while you are still dragging. The page becomes a drop zone: drop on Put in Tray, or on an action tile that fits the files — compress pictures to 1 MB, turn them into a PDF or JPEG, convert PDFs to Word or images, extract archives, zip anything, or AirDrop it. Results land on the shelf; originals stay where they are. The otter under the Put in Tray row pokes its head out as the drag arrives and hugs the file when you let go.
- Files show as thumbnails. Drag one or several (⌘/⇧ to select) straight into any app.
- Press Space for Quick Look; right-click to open, reveal in Finder, AirDrop, copy, pin or remove — pictures get an extra "Copy Image" entry that copies the image itself.
- Convert in place: right-click a picture → Convert to → JPEG, HEIC, PNG or PDF, or Word (Recognize Text) for the text in it; select several to merge them into one PDF. Right-click a PDF → Convert to → Word (Extract Text), or PNG (One per Page) or JPEG (One per Page). Resize Images sets the long edge to 2560, 1600 or 800 px. Everything runs on this Mac, the original stays untouched, and results land at the front of the shelf.
- Tidy files where they sit: right-click a picture → Compress to and pick 500 KB, 1 MB, 2 MB or 5 MB; right-click a zip, tar, 7z or rar archive → Extract; right-click files → Rename… to rename a batch the way Finder does — replace text, add text, or name plus number — with a live preview and undo.
- Other ways in: shake the pointer while dragging to summon a drop basket beside the cursor, or use Services → "Add to Rakko" in Finder and other apps.

**Desktop otter**
- The sea otter stands on the desktop as a small companion. Drag it anywhere — it settles above the Dock or against a screen edge and stays there on that display; drag it up to the notch and it goes back in. Click it to open the panel, or right-click for Open Rakko, Stow in the Notch, Size and Settings….
- Drop files or text on it and they land in the Tray, the same way the notch takes them; the otter holds what it caught for a moment. It is quiet otherwise: it changes pose (standing, paws up while a drag hovers over it, lifted while you carry it, startled when a drag crosses the screen, asleep after ten quiet minutes) and never walks about.
- Settings → Appearance → Desktop Otter: Show the otter, and Small, Medium or Large. While it is stowed, right-click the menu-bar icon → Bring Out the Otter brings it back.

**Clipboard history**
- Text you copy is kept automatically, along with the app it came from. Screenshots and copied images land on the shelf too.
- Press `⌃⌥V` to paste: type to filter — rows are numbered; `⌘1`…`⌘9` paste the first nine of the list as shown, and ↑/↓ then ⏎ pastes the selected one. The panel folds away, the app you were in takes the keyboard back, and the item is pasted there. Without Accessibility permission Rakko only copies and says so — press `⌘V` yourself. Text inside screenshots and copied pictures is recognized on this Mac and searchable too; right-click a picture → Copy Recognized Text to copy what it says.
- Pin what matters. Exclude apps you never want recorded. Passwords that password managers mark as private are skipped.

**Sticky notes**
- The Tools page has a Sticky Note card (add it from the + catalogue). Click it and a 320 × 380 window opens on the note: plain Markdown, the first line becomes its heading, and it is saved 400 ms after you stop typing — no save button, no saved line. Esc closes the window; a change another editor makes to the file shows up here while nothing is unsaved.
- The card follows the note: 1 × 1 is the glyph and the name, 2 × 1 is the heading over the first three lines. The window's top row has Show in Finder, Keep on Top and Close.
- The note is searchable: type in the notch's search bar and the Sticky Notes group lists the notes whose text matches; ⏎ brings that note up in its window. A hit in the heading comes before a hit in the body.

**App switching**
- Click the dedicated App Switcher button on the collapsed notch or its navigation capsule. The left wing holds up to four pinned apps; the right holds other running apps and More. Click to activate an app, or launch a pinned app that is not running. The notch's centre still opens Search.
- More → Edit Pinned Apps lets you add installed apps, unpin them and change their order. Your pinned order is saved on this Mac. Left/Right selects, Return switches, and Esc steps out of the editor, More, then the wings. A separate App Switcher shortcut is available in Settings → Shortcuts, unset by default.
- Switching is app-level, not window or tab switching. It needs no Accessibility or Screen Recording permission. Optional app usage tracking for the weekly report is separate and off by default.

**Home and music**
- Hover the notch while music plays and it grows two small wings: the cover on the left, a moving equalizer and App Switcher button on the right. Nothing drops down over your windows. The cover and equalizer open Search; the App Switcher button opens the app wings.
- Home is the player while something plays or stays paused: the cover, the title and artist, an equalizer that moves while the track plays, a progress bar you drag to skip with the elapsed time on its left and what is left on its right, previous, play-pause and next, and the lyrics button at the bottom left.
- The lyrics button opens a column beside the player and the page widens to 640. Synced lyrics scroll there with the current line bold; Rakko asks lrclib.net first and NetEase Cloud Music when lrclib.net has none or is busy. While the lookup runs the column says Finding lyrics…, a finished lookup with nothing to show says No lyrics found for this song, and a failed one says Lyrics are unavailable right now and tries again after 30 seconds. The column is closed on a fresh install and the choice is remembered; only an open column asks for lyrics.
- With nothing playing Home shows the otter — asleep after ten quiet minutes — with today's date and weekday, and up to three lines: the newest agent turn running or just finished (only while Agent Completion notices are on), how many files the Tray holds, and either how much Keep Awake time is left or the tightest AI quota.
- The music widget is still a card on the Tools page: at 2 × 2 it is the full player with the volume bar for your default output device, at 2 × 1 the cover, title, artist and the transport buttons, at 1 × 1 the cover with a play/pause button on it. A tap on the card itself, beside its own buttons, opens Home.

**Weekly report**
- Open Last Week's Report from Home to review Mac use, local AI usage and Rakko actions by calendar week. Switch between active and resident time; move between weeks with the arrows.
- App usage tracking is off until you opt in. It keeps app names, bundle identifiers and daily totals only on this Mac, not window titles, URLs or input. Tracking covers only periods when Rakko is running and tracking is enabled; unavailable periods cannot be recovered. Pause it from the report.
- AI totals come from locally readable logs and estimated cost is not your subscription bill. Rakko counts successful conversions, Capture Text and items taken from the Tray from when recording began.

**AI usage** (optional, off by default)
- Turn it on in Settings → AI → Show AI Usage in the Notch and the panel gains one more page icon. The page fits one screen and never scrolls: the left column holds a double-ring card per tool — outer ring weekly, inner ring 5-hour — and the right column holds this Mac's total token usage over Today / 7D / 30D / All, as four numbers (tokens, requests, cost, cache hits) and one chart. Hover a card for its plan, reset times, the even-pace mark and any per-model weekly limits; hover a number for its parts.
- Quotas show how much is used by default; switch to what is left in Settings → AI → Quota Shown. The same page has one switch per quota source — Claude Code and Codex. The AI Usage card on Tools reads the same quotas: at 1 × 1 it draws one double ring — outer weekly, inner 5-hour — around the mark of the tool with the least left; hover it for the numbers. At 2 × 1 it keeps the ring beside its name.
- The Claude Code quota comes from Claude Code itself: Rakko runs your installed Claude Code's `/usage` command, so the numbers match what Claude Code reports. Claude Code must be installed; Rakko never downloads it. When Claude Code on this Mac is signed out, the Claude card says Not Signed In with a Sign In button: it opens Claude's sign-in page in your browser and keeps that sign-in in Rakko's own folder (`~/Library/Application Support/Rakko/claude/`), apart from your Claude Code's. Rakko reads the quota with its own sign-in when it has one, and with your Claude Code's otherwise. The Codex quota reads the login Codex already saved on this Mac and sends it only to chatgpt.com.
- Opening the page or pressing refresh reads the quota (Claude Code at most once every 30 seconds); while the page stays open, Claude Code is read again every 5–15 minutes, and only after you used it. The last reading is kept, so the page shows it right after a restart.
- Local usage is found on its own: the session records of omp, Claude Code, Codex, pi, opencode and Grok are read where those tools keep them. The AI Usage page shows a combined total; the weekly report also shows the available sources.

**Agent completion notices** (optional, off by default)
- Settings → AI → Agent Completion → Notify When a Turn Finishes. While it is on, Rakko writes a hook entry into the configuration file of each agent it finds installed, so a finished turn reaches the notch. Turning it off removes every entry it wrote, whole; a file it cannot parse is never rewritten.
- The notice is a two-line card under the notch: the tool's mark, its name and a green Done on the first line, the task it was working on below. It only reports — nothing answers or reads a conversation, and the agent keeps working exactly as it would without Rakko.
- Codex asks once before it runs the entry: open `/hooks` in Codex, review the hook Rakko added and trust it. Until then Codex skips it.
- Supported tools, and the file Rakko writes for each:

| Tool | File |
| --- | --- |
| Claude Code | `~/.claude/settings.json` |
| Codex | `~/.codex/hooks.json` |
| Cursor | `~/.cursor/hooks.json` |
| Copilot CLI | `~/.copilot/hooks/rakko-agent-completion.json` |
| Devin | `~/.config/devin/config.json` |
| Gemini CLI | `~/.gemini/settings.json` |
| Antigravity | `~/.gemini/config/hooks.json` |
| opencode | `~/.config/opencode/plugins/rakko-agent-completion.js` |
| omp | `~/.omp/agent/hooks/pre/rakko-agent-completion.ts` |
| pi | `~/.pi/agent/extensions/rakko-agent-completion.ts` |
| Grok | `~/.grok/hooks/rakko.json` |
| Qwen Code | `~/.qwen/settings.json` |
| Droid | `~/.factory/settings.json` |
| Kiro | `~/.kiro/hooks/rakko-agent-completion.json` |
| Auggie | `~/.augment/settings.json` |

A tool counts as installed only by files it writes itself, so a stray `~/.claude` folder from an installer does not put a row in the list. The switch needs the installed app: a build run from a terminal writes nothing and says so.

**Agent approvals** (optional, off by default)
- Settings → AI → Agent Approvals → Approve agent requests in the notch. When an agent asks for permission — to run a command, edit files, fetch a URL — a card drops from the notch instead of your terminal waiting: the project and one plain sentence (the agent's own description when it gives one), risk tags only when something is risky (deletes files, network or downloads, installs packages, admin rights, rewrites history, pipes into a shell, publishes), and the full command, file list or URL one click away.
- Allow, Deny (with an optional reason the agent reads) or Handle in Terminal. A risky request turns the allow button amber and asks you to hold it for 0.6 s. The card never takes the keyboard; `⌘↩` is its one shortcut, and on a risky request it has to be pressed twice. The card shows how long it has waited; after 45 seconds it hands the request back to the terminal. Nothing is ever allowed for you.
- Claude Code, Codex, Qwen Code, Devin and opencode ask only when they would have asked you anyway, so every request shows a card. Copilot CLI, Droid, Antigravity and pi check every tool call, so Rakko shows a card only for risky ones and leaves the rest to the tool's own rules. Cursor, Gemini CLI, Grok, Kiro, Auggie and omp keep completion notices only: their hooks cannot approve on your behalf.
- Rakko answers only through its own socket and checks that the process on the other end is this signed Rakko; if Rakko is not running or the check fails, the tool falls back to asking in the terminal. Approval entries are separate from completion entries — each switch adds and removes only its own. Codex asks you to trust the new hook once in `/hooks`. Copilot CLI uses `~/.copilot/hooks/rakko-agent-approval.json`, opencode `~/.config/opencode/plugins/rakko-agent-approval.js`, pi `~/.pi/agent/extensions/rakko-agent-approval.ts`; the other tools get their entry in the same file as in the table above.

**Stay awake**
- Right-click the menu-bar icon → Keep Awake and your Mac won't fall asleep for 30 minutes, 1, 2 or 4 hours, or until you turn it off. Tick Keep Screen On to keep the display awake as well.
- While it is on, every open page shows a moon in its top row: hover it for the time left, click it to end the hold.

**Search bar and pages**
- A click on the notch (or `⌃⌥Space`) grows a black search bar out of it and puts the keyboard in its field. Type to search this Mac: the sheet under the bar lists the Tray, the Clipboard and your sticky notes first, then apps, folders, files and actions. A row is two lines — the name (pictures show a thumbnail), then the folder it lives in, the app it came from and the time, or the time. ↑/↓ chooses a row, ⏎ opens it (or copies the text, brings up the note, or runs the action), and ⌘⏎ shows a file in the Finder. The action bar inside the sheet's last row names both keys: Open ↩, and Show in Finder ⌘↩ for files, folders and apps.
- The bar searches everything (全部) unless you say otherwise: before typing, press Tab to switch to 应用 (applications only) or 文件 (files and your own content, no applications) — a small capsule right of the field names the mode and carries the Tab hint. Tab changes that one search only, without changing the default; the bar opens on the default mode chosen in Settings → General → Search (全部 unless you change it), and the choice is kept across launches. In 应用 mode the index is only asked for application bundles, never for document text.
- The capsule hanging under the bar carries the pages — Home, Tray, Clipboard, Tools, and AI Usage when it is on — beside Settings and Collapse. A page grows out of the notch in the bar's place; the current page's button takes you back to the bar.
- Esc steps back: a page returns to the search bar, a typed query clears first, and the next Esc folds the panel into the notch.

**Tools page**
- Tools is one grid: the action cards and the widgets live together. A new install fits exactly two rows with no scrolling: the music widget at 2 × 2, Keep Awake and AI Usage at 1 × 1 (Fit 1 MB takes that cell while AI Usage is off), then Capture Text and Zip. Every other card and widget — AirDrop, Sticky Note and the rest — is in the + catalogue. A layout you already have stays as it is. The grid scrolls inside the page when it holds more than two rows.
- Only a real drag of files over the panel dims the action cards that cannot take them. While a conversion runs, the action cards dim and refuse taps. Widgets and cards pinned to the desktop stay as they are.
- Tap an action card to run it on the pictures or files selected on the shelf, or drop files straight onto it. Tap the Sticky Note card to open its window; tap it again to close. Tap the music card itself, beside its buttons, to open Home and its player.
- Long-press a card (or click the pencil) to edit: drag to reorder, ⊖ to remove, the corner handle to cycle the sizes a widget offers (1 × 1, 2 × 1, 2 × 2), and the dashed + cell to add a card from the catalogue. Done or Esc to finish. Drag a card out of the panel to keep it on the desktop.

**Pin to the desktop**
- While editing, drag a card or a widget out of the shelf and it stays on the desktop as a small floating card that still takes taps and dropped files.
- Let go near the left or right screen edge and it tucks into a thin strip: hover to open it, move away and it closes after 0.6 s, or bring a file drag close and it opens to take the drop. Right-click → Always Expanded, Return to Notch or Remove from Desktop; drag it up to the notch to put it back — a widget returns to its cell on the Tools page, an action card to its cell there too.

**Copy text from the screen**
- Press `⌃⌥O`, use the menu-bar icon → Capture Text, or tap its card, then drag over any part of the screen: the text in it is recognized on this Mac, copied to the clipboard and kept in the Clipboard list, marked Capture Text.
- Needs Screen Recording permission — see [Optional](#optional).

**Screenshot**
- Press `⌃⌥A`, use the menu-bar icon → Screenshot, or tap the Screenshot card (add it from the + on Tools). Every screen freezes; hover a window and click to take it, or drag out an area, then adjust it with the eight handles or the arrow keys. A magnifier helps while you choose.
- Mark it up: rectangle, arrow, pen, mosaic brush, text and numbered steps, in five colors and three sizes; ⇧ keeps lines straight and boxes square, `⌘Z` / `⇧⌘Z` undo and redo.
- ⏎, a double-click or `⌘C` copies the picture (and adds it to the Clipboard list, marked Screenshot); `⌘S` saves it where macOS saves screenshots; Recognize Text copies the text in it; Pin to Desktop keeps it floating on the desktop until Rakko quits. Esc steps back one level at a time.
- Needs Screen Recording permission — see [Optional](#optional).

**Live activities**
- Plug in the charger and the notch widens for a few seconds: a bolt on the left, a green level bar and the percentage on the right. It also tells you once when the battery is full, and once at 20 % and 10 % while on battery.
- Connect Bluetooth headphones and the notch shows their name with the system symbol for the model. When macOS reports a level for each part, the left bud, right bud and case each get an icon, a battery ring and the percentage, the ring turning red at 20 % or below. The notice shows again when they disconnect.
- While a browser downloads into Downloads, the notch shows a progress ring and the percentage (or the size so far); when the download finishes, click the notice to put the file on the Tray. Several downloads that finish together share one notice, and a click puts all of them on the tray. Needs Full Disk Access — see [Optional](#optional).
- Each one can be turned off in Settings → Appearance → Live Activities.

**Dynamic Island style**
- On a display without a notch, Rakko floats as a Dynamic Island: a small black pill near the top that grows into everything the notch does.
- On a MacBook you can choose it too: Settings → Appearance → Notch Style → Dynamic Island. The notch style stays the default.
- Hold the island and drag it anywhere on the screen; it stays where you let go, on each display. The panel finds its own place: it opens below the island when there is room, moves aside near a screen edge, and opens above the island when the space below is too small. While the panel is open, hold a blank part of its top row and the whole panel moves with the island. Settings → Appearance → Reset Position brings it back to the top.
- The notch style cannot be dragged.

**Arrange windows from the notch**
- Drag any window by its title bar toward the notch: a panel offers left half, full, right half and left, middle or right third. Hover a tile to see where the window will go, let go to arrange it. Move away to cancel.
- Uses Accessibility permission; turn it off in Settings → Appearance → Snap Windows at the Notch.

**Language**
- Settings → General → Language offers Follow System (the default), 简体中文 and English.
- Follow System shows Chinese when the Mac's first preferred language is Chinese — Simplified or Traditional, both in Simplified Chinese — and English for every other first language. A language you pick is shown exactly as you picked it. The switch takes effect at once, with no restart.
- Dates and times follow the Mac's region and its 12- or 24-hour setting, whatever the interface language. The Services menu item, Add to Rakko, follows the system language.

**Quiet by design**
- Show / Hide (`⌃⌥Space`) opens the search bar or closes the shelf; clicking anywhere else tucks it away, and Esc steps back one level at a time — a page returns to the search bar, a typed query clears first, and the bar folds into the notch.
- Reduce Transparency draws the panel pure black; Reduce Motion keeps the fades and drops the travel and bounce. Secondary text — times, counts, captions — is drawn brighter than before, so it stays readable on black.
- Every page switch is one fade: the page you leave fades out while the new page fades in. Over the open panel the pointer stays a plain arrow.
- Settings… is a normal window with a sidebar: General (Open at Login, Language, default search mode), Appearance (notch style, island position, desktop otter, live activities, snap windows), Tray, Clipboard, AI (display, quota, Agent Completion, Agent Approvals), Shortcuts, System Permissions and About. The sidebar's footer says whether every permission is on; About holds the version, Check for Updates, the data folder, the update settings, the privacy note and the source link. Every control applies at once.
- Fourteen global shortcuts live in Settings → Shortcuts, in three groups: Global Shortcuts (Show / Hide `⌃⌥Space`, Clipboard Search `⌃⌥V`, Open Tray, App Switcher), Playback (Play / Pause, Previous Track, Next Track) and Tools (Capture Text `⌃⌥O`, Screenshot `⌃⌥A`, Open Tools, Open AI Usage, Toggle Keep Awake, New Note, Show or Hide the Otter). Only the four with defaults start set; every other row says Not Set. Click a row and press the new keys: a key with ⌃, ⌥ or ⌘ saves at once; F1–F12, the arrows, Home, End, PageUp, PageDown and Insert work alone or with ⇧; a key that types text asks first — "X" will be taken from every app — with Use Anyway or Cancel. Delete clears the row, Esc cancels, and a combination another app already holds leaves the previous one in place.
- Pressing Clipboard Search, Open Tray, Open Tools or Open AI Usage while that page is already showing closes the panel. With AI Usage off, its shortcut opens Settings → AI.
- Updates are checked against this repository's GitHub releases. When a new version is out, an update window opens with its release notes: Skip This Version, Remind Me Later (a day), or Install Update, which downloads in the same window and ends with Install and Relaunch. The window follows the system's light or dark appearance. Settings → About holds Check for Updates, the last check's time and result, Check for Updates Automatically and Automatically download and install updates (off by default; when on, only the ready-to-relaunch window appears), plus a Restart button once a version is downloaded; the pill at the bottom of the panel offers the same restart.

<a id="install"></a>

## Install

Requires a Mac with Apple Silicon (M1 or later) running macOS 13 or later. Intel Macs are not supported.

1. Download **[Rakko.dmg](https://github.com/henryyu333/rakko/releases/latest/download/Rakko.dmg)**.
2. Open the downloaded file and drag **Rakko** onto the **Applications** folder.
3. Open Rakko from Applications. The first time, macOS will stop it — follow [First launch](#first-launch) below. You only do this once.
4. Rakko lives in the menu bar, not the Dock. Hover the notch, or press `⌃⌥Space`. A fresh install opens a short guide in the notch the first time: where Rakko lives, the optional permissions, and dropping one file to try it — Skip or Esc leaves at any step. If Rakko is running from Downloads or the disk image, the guide offers Move to Applications (an older Rakko already there goes to the Trash first) and reopens from there. Updating from an earlier version never shows the guide.

Prefer a zip? Every release on the [Releases page](https://github.com/henryyu333/rakko/releases/latest) also has `Rakko-<version>-macos-arm64.zip` and the `.dmg` of the same version.

<a id="first-launch"></a>

### First launch: "Rakko Not Opened"

Rakko is free and not notarized by Apple, so macOS asks you to confirm it once. Only do this for a copy you downloaded from this repository.

**macOS 15 or later**

1. Double-click Rakko in Applications. When macOS says Rakko was not opened, click **Done** (not Move to Trash).
2. Open **System Settings → Privacy & Security** and scroll down to **Security**.
3. Next to "Rakko was blocked to protect your Mac", click **Open Anyway**.
4. Enter your password or use Touch ID, then click **Open Anyway** once more.

**macOS 13 or 14**

Control-click (or right-click) Rakko in Applications, choose **Open**, then click **Open** in the dialog.

From then on Rakko opens normally. If macOS ever says the app **is damaged**, don't bypass it — delete it and download it again from the Releases page. Apple's guide: [Safely open apps on your Mac](https://support.apple.com/102445).

### Optional

- **Start at login**: right-click the menu-bar icon → Settings → General → Open at Login.
- **System permissions**: Settings → System Permissions lists the three permissions with their state, and its last row is one switch; you can also right-click the menu-bar icon → Turn On System Permissions (3 missing)… — the number is how many are still off. Turning it on walks you through three System Settings pages in turn: Full Disk Access, Accessibility, then Screen Recording. Turn Rakko on in each (on the Full Disk Access page, press + and choose Rakko, or drag in the Rakko that Finder shows), and Rakko moves to the next one by itself; at the end, click Reopen Rakko. macOS requires you to switch on each one yourself. Coming from the app's earlier name asks for them once more (see below). Official Rakko releases keep the grants across later updates; a build you compile yourself is signed afresh each time and may ask again.
- **Full Disk Access**: files dragged in from Downloads, Desktop, Documents or iCloud Drive no longer bring up a "Rakko would like to access files…" dialog.
- **Accessibility** (notch drop protection): while you drag toward the notch, the pointer stays in the drop area instead of hitting the very top edge of the screen.
- **Screen Recording** (Capture Text, Screenshot): Capture Text keeps only the area you select, recognizes its text on this Mac and does not keep the shot; Screenshot freezes the screens on this Mac and keeps nothing until you copy, save or pin it.
- **Uninstall**: right-click the menu-bar icon → Quit, then move Rakko from Applications to the Trash. To also delete your saved items, remove `~/Library/Application Support/Rakko/`.

### Coming from ShelfU

Rakko is the new name of ShelfU. The first time Rakko opens, it copies the data folder over: `~/Library/Application Support/ShelfU/` becomes `~/Library/Application Support/Rakko/`, with the saved items, managed images, converted files, layout and settings intact, and absolute paths inside them rewritten. It copies rather than moves, in one step, so a failure leaves both folders as they were; the old folder stays as a complete backup, and you can delete it once everything is there. The copy is skipped when the new folder already exists, and Rakko refuses to migrate while ShelfU is running — quit ShelfU first.

macOS treats Rakko as a new app, so turn its system permissions and Open at Login on again after the upgrade.

- **Updating inside ShelfU**: after the update is installed, open ShelfU from Applications. It renames its bundle to `Rakko.app` and opens from there; the data folder is copied on that first launch.
- **Installing from Rakko.dmg**: quit ShelfU and move `ShelfU.app` to the Trash first, so that only Rakko keeps using your items.

Coming from the older Shelf app? Rakko copies from `Application Support/Shelf/` the same way when no ShelfU folder exists, and renames a `Shelf.app` bundle the same way too.

## Everyday use

| To | Do this |
| --- | --- |
| Open or close the shelf | Press `⌃⌥Space`, click the menu-bar icon, or click the notch (hover first to see the wings or the pill) — the search bar opens |
| Open a page | Click a page in the capsule under the search bar: Home, Tray, Clipboard, Tools, or AI Usage when enabled; the page grows out of the notch |
| Open the player | Click Home in the capsule, or tap the music card on Tools |
| Go back | Press Esc — a page returns to the search bar, a query clears first, and Esc again folds it into the notch; clicking outside the panel (not on the menu bar) or the shortcut closes the whole shelf |
| Put something in | Drag it toward the notch: the Tray opens as the drag arrives, and you let go to add it; or shake the pointer while dragging and drop on the basket; or drop it on the desktop otter |
| Write a sticky note | On Tools, tap the Sticky Note card (add it from +) and type — it saves as you stop; Esc closes the window |
| Search your notes | Type in the notch's search bar; the Sticky Notes group lists the matches and ⏎ opens the note |
| Take something out | Drag it from the shelf into any app; ⌘/⇧-click to take several at once |
| Copy text back | Click a text item, then paste with `⌘V` |
| Copy image data | Select one picture and press `⌘C`, or right-click it → Copy Image — the clipboard gets PNG data, not the path |
| Paste what you copied | Press `⌃⌥V` — the Clipboard page opens with the search field ready; type, then `⌘1`…`⌘9` paste that row, or pick with ↑/↓ and press ⏎ |
| Switch pages | Click a page in the capsule under the search bar; the current page's button returns to the bar. Home widens for lyrics; the weekly report opens a taller reading view |
| Control music | On Home: previous, play-pause and next, or drag the progress bar to skip; the lyrics button is at its bottom left, and the 2 × 2 music card on Tools keeps the volume bar |
| Pin, remove, Quick Look, AirDrop | Right-click an item (Space also opens Quick Look) |
| Copy text from the screen | Press `⌃⌥O` and drag over the text |
| Take a screenshot | Press `⌃⌥A`, pick a window or drag an area, mark it up, then ⏎ to copy or `⌘S` to save |
| Convert a picture or a PDF | Right-click a picture → Convert to; right-click several pictures → Merge into One PDF; right-click a PDF → Convert to → Word (Extract Text), or PNG (One per Page) / JPEG (One per Page) |
| Shrink a picture, zip files | Right-click a picture → Resize Images and pick 2560 / 1600 / 800 px; right-click files or folders → Compress to zip |
| Run a tool card | Open Tools, then tap a card for the selected shelf items or drop files on it |
| Arrange widgets or cards | Long-press a card or widget on Tools to edit; drag it out of the shelf to pin it to the desktop |
| Shrink a picture to a file size | Right-click it → Compress to → 500 KB / 1 MB / 2 MB / 5 MB |
| Unpack an archive | Right-click a zip, tar, 7z or rar file → Extract; the folder lands on the shelf |
| Rename files | Right-click one or more files → Rename…, check the preview, then rename; undo from the notice that follows |
| Keep the Mac awake | Right-click the menu-bar icon → Keep Awake and pick how long |
| Clear the shelf | Click the trash icon in the page's action row — only that page is cleared, pinned items stay, original files are never deleted |
| Change settings | Right-click the menu-bar icon → Settings… |
| Change a shortcut | Settings → Shortcuts: click the row, press the new combination; Delete clears it, Esc cancels |
| Check for updates | Right-click the menu-bar icon → Check for Updates…, or Settings → About → Check for Updates |
| Get told an agent finished | Settings → AI → Agent Completion → Notify When a Turn Finishes; Codex asks you to trust the hook once in its own `/hooks` |
| Approve an agent's request from the notch | Settings → AI → Agent Approvals → Approve agent requests in the notch |

The full manual is in [docs/MANUAL.zh-CN.md](docs/MANUAL.zh-CN.md) (Chinese).

## Privacy

Everything stays on your Mac: no account, no telemetry, no cloud sync. Rakko makes three kinds of network requests. The update check — shortly after launch and then once a day, Rakko asks this repository's latest GitHub release whether a newer version exists; it sends nothing but the app version in the standard User-Agent header. Turn it off in Settings → About → Check for Updates Automatically; checking by hand from the menu still works. Lyrics — only after you open the lyrics column with the button on the Home player, and only for a song Rakko has no answer for yet, it sends the title, artist, album and length to lrclib.net and, when lrclib.net has nothing or is busy, the title and artist to NetEase Cloud Music (music.163.com); the answer is kept on this Mac, and close the column and nothing more is asked. And, only if you enable the optional AI usage page, its quota lookups: the Codex quota goes to chatgpt.com with the login Codex saved, and the Claude Code quota is read by running your installed Claude Code, which talks to Anthropic itself as it always does. Nothing else is sent. Agent approvals never leave the Mac: the hook and Rakko talk over a socket in Rakko's own folder. These requests follow the proxy settings on the Mac: `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` decide when the app was started with them, and otherwise the system proxy from System Settings → Network → Proxies applies as CFNetwork itself reads it — HTTP for http, HTTPS for https, SOCKS only when the scheme's own proxy is off, never one scheme's proxy standing in for the other, with the system's exception list and the simple-hostname exclusion. Turning a proxy on takes effect without relaunching Rakko. A proxy auto-configuration (PAC) script is not evaluated: with only a PAC configured, Rakko connects directly.

The Agent Completion switch adds no traffic: those hooks run Rakko's own binary on this Mac, hand it the tool's own event through a local socket in the data folder, and send nothing over the network. They report only that a turn finished and what the task was — never a conversation, never an answer.

Clipboard text is stored in plain text in `~/Library/Application Support/Rakko/`. Recording is always on. To keep an app's copies out, right-click one of its clipboard entries and exclude the app; to copy something you don't want kept, quit Rakko first.

## Build from source

Requires Go 1.27 or later and Xcode Command Line Tools. Run the following command from the repository root:

```sh
go run github.com/egoist/mygo/cmd/mygo build
```

The packaged `Rakko.app` and a `.dmg` land in a `darwin-arm64` subfolder under the output directory configured in `mygo.json`. Rakko is written in Go with [MyGo](https://github.com/egoist/mygo). The app icon and the menu-bar icon are not in this repository: a build from source uses the default icon and shows the name in the menu bar.

### Source layout

| Path | Contents |
| --- | --- |
| `cmd/rakko/` | Application entry point, native interface and application tests |
| `internal/` | macOS integration, storage, settings, layout, notes, agent hooks, document export and AI usage modules |
| `resources/` | Third-party notices and generated native helper |
| `docs/` | User manual |

Tests stay beside the code they exercise. Run the non-interactive checks from the repository root:

```sh
go vet ./...
go test -race -count=1 ./...
```

MyGo reads the application package from `mygo.json`; the build command above also builds the native now-playing helper.

## License

Rakko's source code is licensed under the Apache License, Version 2.0 (see [LICENSE](LICENSE)); the notices that must travel with it are in [NOTICE](NOTICE), and third-party software and marks in `resources/THIRD_PARTY_NOTICES.txt`.

The Rakko brand is not licensed by Apache-2.0 (section 6). The name Rakko, the logo, the app icon, the menu-bar icon and the sea otter character — its poses, variants and original design files — are reserved; see [TRADEMARKS.md](TRADEMARKS.md). The artwork is not in this repository. You may build and run Rakko from source, and say your work is based on Rakko; a modified release must use a different name and different icons.

Releases v1.0.0 through v1.12.1 were published under the MIT License and stay available under it.

---

<a id="简体中文"></a>

## Rakko 是什么

Rakko 是住在 Mac 刘海里的工具：它把摄像头周围那块黑色刘海变成屏幕上能用的一部分。平时它不露面；鼠标移到刘海上，它会向两侧长出两翼或一个小箭头提示，点一下就打开搜索条：输入就能搜整台 Mac（下方结果面板先列托盘、剪贴板和便利贴，再列应用、文件夹、文件和动作），也可以点搜索条下方胶囊里的页面：首页、托盘、剪贴板、工具，打开 AI 用量后还有 AI 用量。页面从刘海原来的位置长出来，440 × 250；首页在歌词栏打开时加宽到 640。如果是拖着东西过来，打开的是「托盘」页，直接等着接收。屏幕上只有画出来的黑块接鼠标，旁边的菜单栏照常可点。Esc 一级一级往回退：页面回到搜索条，有输入时先清空输入，再按一次收回刘海。

它只在菜单栏里，不占 Dock；所有内容都留在你的 Mac 上；放进来的文件不会被复制，Rakko 只记住它们在哪儿。

## 亮点

**刘海里的托盘**
- 把文件、文件夹、图片或文字往刘海拖，刘海会提示「松开放入」，还在拖着的时候「托盘」页就已经打开。整页变成投放区：松在「放入托盘」就收进托盘；松在下方的动作格上，就按文件类型直接压缩到 1 MB、转成 PDF 或 JPEG、PDF 转 Word 或图片、解压、打成 zip，或隔空投送。结果放进托盘，原文件不动。「放入托盘」那一行下沿会探出海獭的头，松手时它把文件抱进怀里。
- 文件以缩略图显示（iPhone 拍的 HEIC 照片也有预览），可以一次拖出一个或多个（按住 ⌘ 或 ⇧ 多选），直接拖进任何应用；也可以在设置里让拖出去的东西自动离开托盘。
- 按空格快速查看；右键可以打开、在 Finder 中显示、隔空投送、复制（图片多一项「复制图片」，复制图片数据本身）、置顶或移除。
- 图片和 PDF 可以直接转换：右键图片 →「转换为」JPEG、HEIC、PNG、PDF，或识别图里的文字生成 Word，多张还能合并成一个 PDF；大图可以一键缩小；右键 PDF 可以提取文字生成 Word，或每页存成一张图片。文件和文件夹可以压缩成 zip。全部在本机完成，原文件不动，做好的文件放在托盘最前面。
- 文件整理也能就地完成：右键图片 →「压缩到」500 KB / 1 MB / 2 MB / 5 MB 以内；右键 zip、tar、7z、rar 压缩包 →「解压」；右键托盘里的文件 →「重新命名…」，像 Finder 一样替换文字、添加文字或按序号命名，改之前先预览，改完可以撤销。
- 还有别的放法：拖着东西时左右晃动指针，光标旁会出现一个暂存篮；也可以在 Finder 或其他应用里右键 →「服务」→「添加到 Rakko」。

**桌宠**
- 海獭会站在桌面上陪着你。拖它可以放到任何地方——靠近 Dock 或屏幕左右边缘会自动站好（贴边），每块屏幕分别记住；拖到刘海上就是「收进刘海」。点它一下打开面板；右键有「打开 Rakko」「收进刘海」「大小」和「设置…」。
- 把文件或文字拖到它身上，就和平常放进刘海一样收进托盘，海獭会把刚接到的东西抱一会儿。平时它很安静，只换表情和姿态（站着；有东西悬在它上面时抬起爪子；被你拎着时被拎起；有拖拽经过屏幕时吓一跳；安静十分钟后打瞌睡），不会到处走动。
- 设置… →「外形与提示」→「桌宠」：「显示海獭」和「大小」小 / 中 / 大。收进刘海以后，右键菜单栏图标 →「放出海獭」让它回来。

**剪贴板记录**
- 复制过的文字会自动收好，并记住来自哪个应用；截图和复制的图片也会进托盘。
- 按 `⌃⌥V` 粘贴：输入即筛选，行首带编号，`⌘1`…`⌘9` 直接粘贴当前显示顺序的前九项；`↑`/`↓` 选择后回车粘贴选中项。面板收起、键盘回到你原来的应用，内容直接贴在光标处。没有辅助功能权限时只复制并明确提示，自己按 `⌘V` 即可。截图和复制的图片里的文字会在本机识别，也能搜到；右键图片 →「识别文字并复制」可以直接复制图中文字。
- 重要的内容可以置顶；不想被记录的应用可以排除；密码管理器标记为隐私的密码不会被记录。

**便利贴**
- 工具页有「便利贴」卡片（在「+」的目录里加）。点一下打开一块 320 × 380 的窗口，里面就是这张便利贴：纯 Markdown，第一行是标题，停止输入 400 毫秒后自动保存——没有保存按钮，也没有「已保存」字样。Esc 关窗口；没有未保存的输入时，别的编辑器改了文件这里也会跟着变。
- 卡片跟着便利贴走：1×1 是图标和名字，2×1 是标题加前三行。窗口顶栏有「在访达中显示」「置顶」「关闭」。
- 便利贴能被搜到：在刘海搜索条里输入，「便利贴」一组列出文字相符的便利贴，回车把它在窗口里打开；标题命中的排在正文命中之前。

**应用切换**
- 点播放中音乐两翼右侧的独立「应用切换」按钮、灵动岛收起胶囊右端的按钮，或展开面板导航胶囊里的「应用切换」，左翼放最多四个固定常用应用，右翼放其他运行中应用和「更多」。点应用就切过去，固定应用没运行时会启动；悬停刘海只露出下箭头，点它仍打开搜索。
- 「更多 → 编辑常用应用」可以添加已安装应用、取消固定、调整顺序，固定顺序保存在本机。左右键选择、回车切换，Esc 依次退出编辑、「更多」和两翼。设置… → 快捷键 里可设置独立的「应用切换」快捷键，默认未设置。
- 只切应用，不切具体窗口或标签页；不需要辅助功能或屏幕录制权限。周报里的应用使用统计是独立功能，默认关闭。

**首页与音乐**
- 放音乐时把鼠标移到刘海上，左边是封面，右边是跳动的音频条和独立的应用切换按钮，不会向下盖住窗口。点封面、音频条打开搜索条；点应用按钮展开应用两翼。
- 有歌在放、或者只是暂停着，首页就是播放器：封面、歌名和歌手，播放时音频条在跳动，可拖动跳转的进度条左边是已经播了多久、右边是还剩多久，中间是上一首 / 播放暂停 / 下一首，左下角是歌词按钮。
- 点左下角的歌词按钮，播放器右边多出一栏歌词，页面加宽到 640。同步歌词在这里滚动，当前一行是粗体；先问 lrclib.net，它没有或太忙时再问网易云音乐。找的时候写「正在找歌词…」，找不到写「没有找到这首歌的歌词」，取不到写「歌词暂时取不到」，30 秒后再试。全新安装时这一栏是关着的，开关记在 `settings.json` 的 `lyrics` 里；只有打开它才会去查歌词。
- 没有歌在放时，首页是海獭陪着你——安静十分钟后它打瞌睡——旁边是今天的日期和星期，下面最多三行：最近一轮 agent 是正在做还是刚做完（只在打开「Agent 完成提醒」时出现）、托盘里有多少个文件、保持唤醒还剩多久；没有开启保持唤醒时，这一行是剩余最少的那个 AI 额度（AI 用量页关着时不显示）。
- 音乐小组件仍留在工具页：2×2 是完整播放器，带系统默认输出设备的音量条；2×1 显示封面、歌名、歌手和上一首 / 播放暂停 / 下一首；1×1 只有封面，上面盖着播放 / 暂停按钮。点卡片本身（不是卡片上的按钮）打开首页。

**每周简报**
- 从首页打开「上周简报」，按自然周查看 Mac 使用、AI 本机用量和 Rakko 完成的操作；用箭头切换周，Mac 时长可切换「活跃 / 停留」。
- 应用统计默认关闭，明确同意后才开始；只在本机保存应用名、标识和每日累计时长，不记录窗口标题、网址或输入内容。只覆盖 Rakko 运行且统计开启的时段，缺失时间无法补回；可在简报底部暂停。
- AI 数据来自仍可读取的本机日志，费用为估算，不代表订阅账单。Rakko 统计开始记录后的成功转换、框选文字和托盘取出次数。

**AI 用量**（可选，默认关闭）
- 在 设置… →「AI」→「在刘海里显示 AI 用量」打开后，顶栏会多出一个页面图标。整页一屏看全，不用滚动：左栏是 Claude Code、Codex 的套餐额度卡（每张卡内外双圈：外圈每周、内圈 5 小时），右栏是这台电脑上的 token 用量总量——今天 / 7 天 / 30 天 / 全部，四个数字（Token、请求、费用、缓存命中）和一根柱图。悬停额度卡可以看到套餐名、重置时间、均匀消耗标记和按模型的每周额度；悬停数字可以看到它的组成部分。
- 额度默认显示「已用」；可以在 设置… →「AI」→「额度显示」改成「剩余」。同一页里每个额度来源各有一个开关（Claude Code、Codex）。工具页的 AI 用量卡用同一份额度数据：1×1 只画一个双圈——外圈每周、内圈 5 小时——中间是剩余额度最少的那个工具的标志，鼠标悬停看具体数字；2×1 仍是圈在左、名字在右。
- Claude Code 的额度由 Claude Code 自己给出：Rakko 运行你装好的 Claude Code 的 `/usage` 命令，所以数字和 Claude Code 显示的一致。需要先装好 Claude Code，Rakko 不会替你下载。本机 Claude Code 没有登录时，Claude 卡片显示「未登录」和「登录」按钮：点了会在浏览器里打开 Claude 的登录页，登录结果只存在 Rakko 自己的目录（`~/Library/Application Support/Rakko/claude/`），不碰你自己的 Claude Code；Rakko 有自己的登录就用它查额度，没有就用你 Claude Code 的登录。Codex 的额度读取 Codex 保存在本机的登录信息，只发给 chatgpt.com。
- 打开这一页或点刷新时会查询额度（Claude Code 最多 30 秒查一次）；页面一直开着时，只有你用过 Claude Code 才会每 5–15 分钟再查一次。上次的结果会保存，重启后打开就能看到。
- 本机用量自己去找：omp、Claude Code、Codex、pi、opencode、Grok 各自的会话记录在它们自己的位置被读取。AI 用量页显示合计；每周简报也会显示可读取的来源。

**Agent 完成提醒**（可选，默认关闭）
- 设置… →「AI」→「Agent 完成提醒」→「完成时在刘海提醒」。打开后，Rakko 会在它找到的每个 agent 的配置文件里写入一条 hook，让「这一轮做完了」传到刘海。关掉时，它写进去的条目会整条移除；解析不了的文件绝不会被改写。
- 提示是刘海下面的一张两行卡：第一行是这个工具的标志、名字和绿色的「完成」，第二行是它刚才在做的事。它只负责通知——不回复、不读对话，agent 的工作和没有 Rakko 时完全一样。
- Codex 要你自己确认一次：在 Codex 里打开 `/hooks`，审阅 Rakko 加的那条 hook 并信任它；在那之前 Codex 会跳过它。
- 支持的工具，以及 Rakko 为各自写入的文件：

| 工具 | 文件 |
| --- | --- |
| Claude Code | `~/.claude/settings.json` |
| Codex | `~/.codex/hooks.json` |
| Cursor | `~/.cursor/hooks.json` |
| Copilot CLI | `~/.copilot/hooks/rakko-agent-completion.json` |
| Devin | `~/.config/devin/config.json` |
| Gemini CLI | `~/.gemini/settings.json` |
| Antigravity | `~/.gemini/config/hooks.json` |
| opencode | `~/.config/opencode/plugins/rakko-agent-completion.js` |
| omp | `~/.omp/agent/hooks/pre/rakko-agent-completion.ts` |
| pi | `~/.pi/agent/extensions/rakko-agent-completion.ts` |
| Grok | `~/.grok/hooks/rakko.json` |
| Qwen Code | `~/.qwen/settings.json` |
| Droid | `~/.factory/settings.json` |
| Kiro | `~/.kiro/hooks/rakko-agent-completion.json` |
| Auggie | `~/.augment/settings.json` |

一个工具算不算装好，只看它自己写出来的文件，所以安装器留下的空文件夹不会在列表里多出一行。这个开关需要安装版：从终端直接跑起来的构建不会写入任何东西，设置页里也会这样说明。

**Agent 审批**（可选，默认关闭）
- 设置… →「AI」→「Agent 审批」→「在刘海审批 Agent 请求」。agent 要权限时（跑命令、改文件、联网），刘海里直接弹出一张卡，不用切回终端：第一行是项目，下面一句人话（agent 自己写了说明就用它的说明）；有风险时才出现风险标签（会删除文件、联网 / 下载、安装软件包、管理员权限、改写历史、管道进 shell、发布）；点一下展开完整命令、文件列表或网址。
- 三个按钮：「拒绝」（可以附一句原因，agent 会收到）、「在终端处理」、「允许」。有风险时「允许」变成警示色，要「按住允许」0.6 秒。卡片不抢键盘，唯一的快捷键是 `⌘↩`，有风险时要按两次。卡上显示已经等了多少秒，45 秒没处理就交回终端；Rakko 从不替你自动允许。
- Claude Code、Codex、Qwen Code、Devin、opencode 只在本来就要问你的时候问，所以每次都上卡；Copilot CLI、Droid、Antigravity、pi 每次调用工具都会问，Rakko 只对有风险的上卡，其余照工具自己的规则走。Cursor、Gemini CLI、Grok、Kiro、Auggie、omp 只做完成提醒：它们的 hook 不能替你批准。
- Rakko 只通过自己数据目录里的 socket 回答，并确认另一头就是这个签名的 Rakko；Rakko 没在运行或校验不通过时，工具照常在终端问你。审批条目和完成提醒的条目互相独立，各自的开关只增删自己的。Codex 要在 `/hooks` 里信任一次新加的 hook。Copilot CLI 写 `~/.copilot/hooks/rakko-agent-approval.json`，opencode 写 `~/.config/opencode/plugins/rakko-agent-approval.js`，pi 写 `~/.pi/agent/extensions/rakko-agent-approval.ts`；其他工具写在上表同一个文件里。

**保持唤醒**
- 右键菜单栏图标 →「保持唤醒」，选 30 分钟、1 小时、2 小时、4 小时或「一直（直到关闭）」，这段时间里 Mac 不会自动睡眠；勾选「屏幕也保持亮着」，屏幕也不会熄灭。
- 开启后，打开任何一页都能在顶栏看到一个月亮图标：鼠标停在上面看剩余时间，点一下结束。

**搜索条与页面**
- 点刘海（或按 `⌃⌥Space`）从刘海里长出一条黑色搜索条，键盘直接进输入框。输入就能搜整台 Mac：下方结果面板先列托盘、剪贴板、便利贴，再列应用、文件夹、文件和动作；每一行是两行字——名字（图片在最左边显示缩略图），下面是所在文件夹 / 来源应用 · 时间 / 时间。`↑`/`↓` 选择，回车打开（文字则复制，便利贴则打开它的窗口，动作则执行），`⌘回车` 在 Finder 中显示。面板最后一行的动作条把两个键写出来：「打开 ↩」，文件和文件夹、应用还有「在 Finder 中显示 ⌘↩」。
- 还没输入时按 `Tab` 可在「全部」→「应用」（只搜应用）→「文件」（文件、文件夹和你自己的内容，不含应用）之间切换，搜索框右侧的小胶囊写明当前模式，空框时还带 `Tab` 键帽提示。`Tab` 只改这一次搜索；搜索条每次打开用 设置… → 通用 → 搜索 里的「默认搜索模式」（默认「全部」），这个选择跨启动保存。选「应用」时只向系统索引要应用包，不再查文档正文。
- 搜索条下方挂着页面胶囊：首页、托盘、剪贴板、工具，打开 AI 用量后还有 AI 用量，旁边是设置和收起。点一个页面，它在搜索条原来的位置长出来；点当前页面的按钮回到搜索条。
- Esc 一级一级往回退：页面回到搜索条，有输入时先清空输入，再按一次收回刘海。

**工具页**
- 工具页是一张统一的网格：动作卡和小组件放在一起。全新安装时正好两行、不用滚动：音乐 2×2，保持唤醒和 AI 用量各 1×1（AI 用量页关着时，这一格是「压缩到 1 MB」），下面是框选文字和打成 zip。其余卡片和小组件（隔空投送、便利贴等）都在「+」打开的目录里，可以自己加。已有的布局不变。超过两行时在页内滚动。
- 只有真的把文件拖过面板时，不能处理的动作卡才变暗；转换进行时，动作卡变暗、点按没有反应。小组件和固定到桌面的卡片保持原样。
- 点按动作卡，对托盘里选中的图片或文件执行；也可以把文件直接拖到卡片上。点「便利贴」卡片打开它的窗口，再点一下关掉；点音乐卡片本身（不是卡片上的按钮）打开首页。
- 长按卡片（或点铅笔图标）进入编辑：拖动排序、点 ⊖ 删除、拖右下角的把手在小组件支持的 1×1、2×1、2×2 之间切换、点虚线「+」格从目录添加。点「完成」或按 Esc 结束。把卡片拖出面板，它就固定到桌面。

**固定到桌面**
- 编辑时把小组件或卡片拖出面板，它会留在桌面上成为一张小浮窗，照样可以点按、接收拖入的文件。
- 松在屏幕左右边缘附近，它会收成一条窄条：鼠标移上去展开，移开 0.6 秒后收回；拖着文件靠近也会展开接收。右键可选「始终展开」「放回刘海」「从桌面移除」；把它拖回刘海也能放回——小组件和动作卡都回到工具页原来的格子。

**框选屏幕文字**
- 按 `⌃⌥O`、右键菜单栏图标 →「框选文字」，或点它的卡片，然后在屏幕上框一块区域：里面的文字在本机识别后直接复制到剪贴板，并记进剪贴板页，来源显示「框选文字」。
- 需要「屏幕录制」权限，见下面的[可选](#可选)。

**截图**
- 按 `⌃⌥A`、右键菜单栏图标 →「截图」，或点「截图」卡片（在工具页的「+」里添加）。所有屏幕先定格；鼠标移到窗口上单击就选中这个窗口，也可以拖出一块区域，再用八个把手或方向键微调。选择时有放大镜帮你对准。
- 标注：矩形、箭头、画笔、马赛克笔刷、文字、序号，五种颜色三种粗细；按住 ⇧ 画直线和正方形，`⌘Z` / `⇧⌘Z` 撤销和重做。
- 回车、双击或 `⌘C` 复制图片（同时记进剪贴板页，来源显示「截图」）；`⌘S` 保存到系统截图的保存位置；「识别文字」直接复制图里的文字；「钉到桌面」让它浮在桌面上，退出 Rakko 前一直在。Esc 一级一级往回退。
- 需要「屏幕录制」权限，见下面的[可选](#可选)。

**实时活动**
- 插上电源时，刘海向两边变宽几秒：左边是闪电图标，右边是绿色电量条和电量数字。充满时提醒一次；不插电时，电量降到 20% 和 10% 各提醒一次。
- 蓝牙耳机连上时，刘海显示耳机名字和对应型号的系统图标；系统给出各部件电量时，左耳、右耳和充电盒各有一个图标、一圈电量环和百分比，电量 20% 及以下时电量环变红。断开时也会提示一次。
- 浏览器往「下载」文件夹下载文件时，刘海显示进度环和百分比（不知道总大小时显示已下载多少）；下载完成后点一下提示，文件就放进「托盘」。同时下载完的几个文件合成一条提示，点一下全部放进托盘。需要「完整磁盘访问」，见下面的[可选](#可选)。
- 每一项都可以在 设置… →「外形与提示」→「实时活动」里关掉。

**灵动岛样式**
- 在没有刘海的屏幕上，Rakko 变成一颗悬浮的灵动岛：屏幕顶部一颗小黑胶囊，刘海能做的事它都能做。
- MacBook 上也可以选：设置… →「外形与提示」→「刘海样式」→「灵动岛」。默认仍是刘海样式。
- 按住小岛可以拖到屏幕任何位置，松手就留在那里，每块屏幕分别记住。展开后的面板自己找位置：下方放得下就向下展开，靠边时和岛错开，下方放不下就向岛上方展开。面板展开时，按住顶栏的空白处也能拖，整块面板跟着岛一起走。设置… →「外形与提示」→「恢复默认位置」让它回到屏幕顶部。
- 刘海样式不能拖。

**语言**
- 设置… → 通用 →「语言」有三档：「跟随系统」（默认）、「简体中文」和「English」。
- 跟随系统时，Mac 的首选语言是中文就显示中文（简体、繁体都显示简体中文），其他语言显示英文；选定哪一档就按哪一档显示。切换立即生效，不用重新打开 Rakko。
- 日期和时间按 Mac 的地区和 12/24 小时制显示；服务菜单里的「添加到 Rakko」跟系统语言走。

**拖窗口到刘海分屏**
- 拖着任意窗口的标题栏往刘海走，会出现排列面板：左半、全屏、右半、左 1/3、中 1/3、右 1/3。指针停在哪一格就预览窗口会落在哪儿，松手就排好；拖离刘海就取消。
- 需要「辅助功能」权限；可以在 设置… →「外形与提示」→「拖窗口到刘海分屏」关掉。

**安静、不打扰**
- 「显示 / 收起」是 `⌃⌥Space`，用来打开搜索条或收起；点别处就自动缩回刘海。Esc 一级一级往回退：页面先回到搜索条，有输入时先清空输入，再按一次收回刘海。
- 打开「减少透明度」时面板画成纯黑；「减少动态效果」时只保留淡入淡出。时间、计数、说明这类次要文字在纯黑上更容易看清。
- 页面之间用同一种切换：旧页淡出，新页淡入。指针在展开的面板上始终是普通箭头。
- 设置… 是一个带侧栏的普通窗口：通用（开机时自动启动、语言、默认搜索模式）、外形与提示（刘海样式、灵动岛位置、桌宠、实时活动、拖窗口分屏）、托盘、剪贴板、AI（显示、额度、Agent 完成提醒、Agent 审批）、快捷键、系统权限和关于。侧栏底部显示权限状态；关于页有版本号、「检查更新」、数据文件夹、更新设置、隐私说明和源代码链接。改完立即生效。
- 设置… → 快捷键 里有十四个全局快捷键，分三组：「全局快捷键」（显示 / 收起 `⌃⌥Space`、剪贴板搜索 `⌃⌥V`、打开托盘、应用切换）、「播放」（播放 / 暂停、上一首、下一首）、「工具」（框选文字 `⌃⌥O`、截图 `⌃⌥A`、打开工具页、打开 AI 用量、保持唤醒开 / 关、新建便利贴、放出 / 收回海獭）。除显示 / 收起、剪贴板搜索、框选文字、截图外，其余初始都是「未设置」。点一下那一行，按新的组合键：带 ⌃、⌥ 或 ⌘ 的立即保存；F1–F12、方向键、Home、End、PageUp、PageDown、Insert 可以单用，也可以加 ⇧；会打字的键先问一句「X」会在所有应用里被截走，再选「仍然使用」或「取消」。Delete 清空，Esc 取消；组合被其他应用占用时，保留原来的组合并提示。
- 页面已经打开时，再按剪贴板搜索、打开托盘、打开工具页或打开 AI 用量，就收起面板；AI 用量页关着时，「打开 AI 用量」打开 设置… →「AI」。
- 新版本从本仓库的 GitHub Releases 检查。发现新版本时会弹出更新窗口，更新说明就在里面：「跳过这个版本」「稍后提醒我」（一天后再提醒）或「安装更新」，下载在同一个窗口里进行，下载好后点「安装并重启应用」。窗口跟随系统的深色 / 浅色。设置… →「关于」里有「检查更新」、上次检查的时间和结果、「自动检查更新」和「自动下载并安装更新」（默认关；打开后只在下载好时弹窗），下载好后还有「重新启动」按钮；面板底部的小胶囊也会提示，并给出同样的重新启动。

<a id="安装"></a>

## 安装

需要 Apple Silicon 芯片（M1 或更新）的 Mac，系统 macOS 13 或以上。不支持 Intel 芯片的 Mac。

1. 下载 **[Rakko.dmg](https://github.com/henryyu333/rakko/releases/latest/download/Rakko.dmg)**。
2. 打开下载好的文件，把 **Rakko** 拖到 **Applications（应用程序）** 文件夹上。
3. 从“应用程序”里打开 Rakko。第一次打开会被系统拦下，按下面的[第一次打开](#第一次打开)操作即可，只需要做一次。
4. Rakko 在屏幕顶部的菜单栏里，不在 Dock 上。把鼠标移到刘海上，或按 `⌃⌥Space`。全新安装第一次打开时，刘海里会走一段简短的引导：Rakko 住在哪、可选的系统权限、拖一个文件进来试试；任何一步都可以点「跳过」或按 Esc 离开。如果 Rakko 是从「下载」或磁盘映像里打开的，引导会提供「移到「应用程序」」（那里已有的旧 Rakko 会先移到废纸篓），然后从新位置重新打开。从旧版本升级的不会看到引导。

想要 zip 压缩包？[Releases 页面](https://github.com/henryyu333/rakko/releases/latest)的每个版本里也有同一版本的 `Rakko-<version>-macos-arm64.zip` 和 `.dmg`。

<a id="第一次打开"></a>

### 第一次打开：提示“未打开 Rakko”

Rakko 是免费软件，没有经过 Apple 公证，所以 macOS 会让你确认一次。请只对从本仓库下载的版本这样做。

**macOS 15 或更新**

1. 在“应用程序”里双击 Rakko。系统提示未打开时，点 **完成**（不要点“移到废纸篓”）。
2. 打开 **系统设置 → 隐私与安全性**，往下滚动到 **安全性**。
3. 在“已阻止 Rakko 以保护 Mac”旁边，点 **仍要打开**。
4. 输入开机密码或用触控 ID 确认，再点一次 **仍要打开**。

**macOS 13 或 14**

在“应用程序”里按住 Control 点按（或右键点按）Rakko，选 **打开**，再在弹窗里点 **打开**。

之后就能正常打开了。如果系统提示 App **已损坏**，不要绕过，删掉后从 Releases 页面重新下载。Apple 官方说明：[在 Mac 上安全地打开 App](https://support.apple.com/zh-cn/102445)。

### 可选

- **开机自动启动**：右键菜单栏图标 → 设置… → 通用 →「开机时自动启动」。
- **系统权限**：设置… → 系统权限 里列出三项权限的状态，最后一行是一个开关；也可以右键菜单栏图标 →「开启系统权限」。打开开关后，Rakko 依次打开系统设置里的三页：「完整磁盘访问」「辅助功能」「屏幕录制」。在每页打开 Rakko 的开关（「完整磁盘访问」这一页要点 + 选 Rakko，或把访达里选中的 Rakko 拖进列表），Rakko 会自动走到下一项；最后点「重新打开 Rakko」。macOS 要求每一项都由你自己打开。从旧名字换过来时要再开一次（见下）；Rakko 的正式发布版在之后更新时保留授权，自己编译的构建每次签名都会变，可能要重新开启。
- **完整磁盘访问**：从「下载」「桌面」「文稿」或 iCloud 云盘拖文件进来时，不再弹出「Rakko 想要取用……中的文件」。
- **辅助功能**（刘海拖放保护）：拖着东西靠近刘海时，指针会停在托盘的接收区里，不会顶到屏幕最上沿。
- **屏幕录制**（框选文字、截图）：框选文字只截取你框选的那一块，文字在本机识别，不保存截图；截图在本机定格屏幕，你复制、保存或钉到桌面之前什么都不留。
- **卸载**：右键菜单栏图标 → 退出 Rakko，再把“应用程序”里的 Rakko 移到废纸篓。如果连保存的内容也要删除，删掉 `~/Library/Application Support/Rakko/` 文件夹。

### 从 ShelfU 升级

Rakko 是 ShelfU 的新名字。Rakko 第一次打开时会把数据文件夹整个复制过来：`~/Library/Application Support/ShelfU/` 变成 `~/Library/Application Support/Rakko/`，托盘内容、Rakko 自己保存的图片、转换结果、布局和设置都在，里面指向旧文件夹的绝对路径也会一并改写。它是复制而不是搬移，并且一步到位：失败时两个文件夹都保持原样，旧文件夹完整保留，确认内容都在以后可以删掉。新文件夹已经存在时不会复制；ShelfU 还在运行时 Rakko 不会迁移——先退出 ShelfU。

macOS 会把 Rakko 当成一个新应用，所以升级后系统权限和「开机时自动启动」要重新打开一次。

- **在 ShelfU 里应用内更新**：更新装好后，在“应用程序”里打开 ShelfU。它会把应用包改名为 `Rakko.app` 并从那里打开；数据文件夹在那一次启动时复制过来。
- **用 Rakko.dmg 安装**：先退出 ShelfU，把 `ShelfU.app` 移到废纸篓，只让 Rakko 使用你的内容。

从更早的 Shelf 升上来？没有 ShelfU 文件夹时，Rakko 同样从 `Application Support/Shelf/` 复制，也会把 `Shelf.app` 改名。

## 日常使用

| 想做什么 | 怎么做 |
| --- | --- |
| 打开或收起 | 按 `⌃⌥Space`、单击菜单栏图标，或点刘海（可以先悬停看两翼或小箭头）——打开的是搜索条 |
| 打开某一页 | 点搜索条下方胶囊里的页面：首页、托盘、剪贴板、工具，开了 AI 用量时还有 AI 用量；页面从刘海里长出来 |
| 打开播放器 | 点胶囊里的「首页」，或打开「工具」页点音乐卡片 |
| 退回上一层 | 按 Esc：页面先回到搜索条（有输入时先清空），再按一次收回刘海。点面板外（菜单栏高度内不算）或再按快捷键都直接收起 |
| 放东西进来 | 拖着它靠近刘海，还没松手「托盘」页就打开了，松手放进去；或拖拽时左右晃动指针，放进暂存篮；也可以直接丢到桌面上的海獭身上 |
| 写便利贴 | 打开「工具」页，点「便利贴」卡片（在「+」里加）直接输入，停手就自动保存；Esc 关窗口 |
| 搜索便利贴 | 在刘海搜索条里输入，「便利贴」一组列出相符的内容，回车打开 |
| 拿东西出去 | 从托盘拖到任意应用；按住 `⌘` 或 `⇧` 可以一次拖多个 |
| 把文字复制回来 | 单击文字项，再在任意地方 `⌘V` 粘贴 |
| 复制图片数据 | 选中一张图片后按 `⌘C`，或右键图片 →「复制图片」，再在目标应用 `⌘V` 粘贴（复制的是图片，不是路径） |
| 粘贴复制过的内容 | 按 `⌃⌥V`，「剪贴板」页直接打开、搜索框已就位；输入关键词，`⌘1`…`⌘9` 直接粘贴该行，或 `↑`/`↓` 选择后回车 |
| 切换页面 | 点搜索条下方胶囊里的页面（点当前页面的按钮回到搜索条）；首页歌词会加宽，周报打开较高的阅读页面 |
| 控制音乐 | 在首页：上一首 / 播放暂停 / 下一首，拖动进度条跳转；歌词按钮在左下角，音量条在工具页 2×2 的音乐卡片上 |
| 置顶、移除、快速查看、隔空投送 | 右键项目（快速查看也可以直接按空格） |
| 框选屏幕文字 | 按 `⌃⌥O`，框住要复制的文字 |
| 截图 | 按 `⌃⌥A`，选窗口或拖出区域，标注后回车复制或 `⌘S` 保存 |
| 用工具卡 | 打开「工具」页，点按卡片处理托盘里选中的项目，或把文件拖到卡片上 |
| 排列小组件或卡片 | 长按「工具」页的卡片或小组件进入编辑；把它们拖出面板就固定到桌面 |
| 转换图片格式、转 PDF 或 Word | 右键图片 →「转换为」；多张图片右键 →「合并为一个 PDF」；右键 PDF →「转换为」Word 或每页一张图片 |
| 缩小图片、压缩成 zip | 右键图片 →「缩小图片」选大小；右键文件或文件夹 →「压缩为 zip」 |
| 压缩图片到指定大小 | 右键图片 →「压缩到」→ 500 KB / 1 MB / 2 MB / 5 MB 以内 |
| 解压压缩包 | 右键 zip、tar、7z、rar 文件 →「解压」，解出的文件夹放进托盘 |
| 批量重新命名 | 右键一个或多个文件 →「重新命名…」，看过预览再点「改名」；改完可以在提示上撤销 |
| 不让 Mac 睡眠 | 右键菜单栏图标 →「保持唤醒」，选时长 |
| 清空当前列表 | 点页面动作行右边的垃圾桶图标——只清这一页，置顶的保留，原文件不会被删除 |
| 修改设置 | 右键菜单栏图标 → 设置… |
| 修改快捷键 | 设置… → 快捷键：点一下那一行，按新的组合键；Delete 清空，Esc 取消 |
| 检查更新 | 右键菜单栏图标 →「检查更新…」，或 设置… →「关于」→「检查更新」 |
| 在刘海审批 agent 的请求 | 设置… →「AI」→「Agent 审批」→「在刘海审批 Agent 请求」 |

完整说明见 [使用手册](docs/MANUAL.zh-CN.md)。

## 隐私

一切都留在你的 Mac 上：不用注册账号，没有遥测，没有云同步。联网请求只有三类：检查更新（启动后不久一次、之后每天一次，向本仓库 GitHub 上的最新 Release 询问有没有新版本，请求里只带一个标准的 User-Agent 头；「自动检查更新」可以在 设置… →「关于」里关闭，关闭后仍可在菜单里手动检查）；歌词（只有你用首页播放器左下的歌词按钮打开歌词栏后，才把一首还没有结果的歌的歌名、歌手、专辑和时长发给 lrclib.net；lrclib.net 没有或太忙时，再把歌名和歌手发给网易云音乐（music.163.com）；结果存在本机；收起歌词栏就不再联网）；以及可选的 AI 用量页的额度查询——只有打开它才会发生：Codex 的额度用 Codex 保存的登录信息发给 chatgpt.com；Claude Code 的额度通过运行你装好的 Claude Code 读取，由 Claude Code 自己照常联系 Anthropic。除此之外不发送任何内容。Agent 审批不出本机：hook 和 Rakko 通过 Rakko 自己数据目录里的 socket 通信。这些请求跟随这台 Mac 的代理设置：启动 Rakko 时带了 `HTTP_PROXY`、`HTTPS_PROXY` 或 `NO_PROXY` 就按它们走（`NO_PROXY` 里的绕过项单独生效，不会退回系统代理）；否则用 系统设置 → 网络 → 代理 里的 HTTP / HTTPS / SOCKS 代理及其例外列表，改完不用重开 Rakko，几秒内生效。自动代理配置脚本（PAC）不解析：只配了 PAC 时 Rakko 直连。

Agent 完成提醒这个开关不增加任何流量：那些 hook 只在本机运行 Rakko 自己的可执行文件，把工具自己的事件通过数据文件夹里的本地 socket 交给它，不经过网络。它们只报告「这一轮做完了」和这一轮在做什么——不读对话，也不回话。

剪贴板文字以明文保存在 `~/Library/Application Support/Rakko/`。剪贴板记录始终开启。不想记录某个应用复制的内容，右键它的剪贴板条目把应用排除；要复制不希望被保存的内容时，先退出 Rakko 再复制。

## 从源码构建

需要 Go 1.27 或以上，以及 Xcode Command Line Tools。在仓库根目录运行以下命令：

```sh
go run github.com/egoist/mygo/cmd/mygo build
```

打包好的 `Rakko.app` 和 `.dmg` 在 `mygo.json` 配置的输出目录下的 `darwin-arm64` 子文件夹中。Rakko 用 Go 和 [MyGo](https://github.com/egoist/mygo) 编写。应用图标和菜单栏图标不在本仓库里：自己从源码构建时用的是默认图标，菜单栏里显示文字名称。

### 源码结构

| 路径 | 内容 |
| --- | --- |
| `cmd/rakko/` | 应用入口、原生界面和应用测试 |
| `internal/` | macOS 集成、存储、设置、布局、便利贴、agent hook、文档导出和 AI 用量模块 |
| `resources/` | 第三方声明和构建生成的原生辅助模块 |
| `docs/` | 使用手册 |

测试和它们覆盖的代码放在同一目录。在仓库根目录运行非交互式检查：

```sh
go vet ./...
go test -race -count=1 ./...
```

MyGo 从 `mygo.json` 读取应用入口；上面的构建命令也会生成音乐播放使用的原生辅助模块。

## 许可

Rakko 的源代码采用 Apache License 2.0，详见 [LICENSE](LICENSE)；随附的声明在 [NOTICE](NOTICE)，第三方软件与商标列在 `resources/THIRD_PARTY_NOTICES.txt`。

Rakko 品牌不在 Apache-2.0 的授权范围内（许可证第 6 条）：Rakko 名称、logo、应用图标、菜单栏图标和海獭形象（含各种姿态、变体与原始设计文件）保留所有权利，见 [TRADEMARKS.md](TRADEMARKS.md)。这些图片不在本仓库里。你可以自己从源码构建使用，也可以说明你的作品基于 Rakko；修改版必须改名并换图标。

v1.0.0 到 v1.12.1 的发布版本采用 MIT 许可证，并继续按 MIT 提供。
