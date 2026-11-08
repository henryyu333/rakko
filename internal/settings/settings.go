// Package settings keeps the user's preferences in settings.json inside the
// data directory, next to the shelf's own state.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"rakko/internal/agentnotify"
)

// FileName is the preferences file inside the data directory.
const FileName = "settings.json"

// Settings are the user-adjustable preferences. Every field must have its
// default in Default: a key missing from the file keeps that default.
type Settings struct {
	// RetentionDays removes items older than this many days; 0 or less
	// keeps everything.
	RetentionDays int `json:"retentionDays"`
	// ClipboardLimit caps how many unpinned clipboard texts are kept;
	// values below 1 load as the default.
	ClipboardLimit int `json:"clipboardLimit"`
	// ExcludedApps lists bundle identifiers whose copies are never
	// recorded into the shelf.
	ExcludedApps []string `json:"excludedApps"`
	// AutoCheckUpdates checks the GitHub release feed for a newer version
	// in the background.
	AutoCheckUpdates bool `json:"autoCheckUpdates"`
	// UpdateAutoInstall downloads and stages a found version without
	// asking, so only the ready-to-restart window appears. Off by default:
	// the notes live only in the found window.
	UpdateAutoInstall bool `json:"updateAutoInstall"`
	// UpdateSkippedVersion is the version 跳过这个版本 declined; automatic
	// checks never prompt for it again.
	UpdateSkippedVersion string `json:"updateSkippedVersion"`
	// UpdateRemindVersion is the version 稍后提醒我 deferred, until
	// UpdateRemindUntil.
	UpdateRemindVersion string `json:"updateRemindVersion"`
	// UpdateRemindUntil is when the 稍后提醒我 quiet period ends, in unix
	// seconds of the local clock; 0 means no quiet period.
	UpdateRemindUntil int64 `json:"updateRemindUntil"`
	// RemoveAfterDrag takes an item off the tray once it was dragged out
	// and dropped; pinned items stay.
	RemoveAfterDrag bool `json:"removeAfterDrag"`
	// ShellStyle is the closed shelf's outer shape on a screen with a
	// camera housing. Screens without one always draw the island.
	ShellStyle ShellStyle `json:"shellStyle"`
	// SnapWindows lets a window dragged by its title bar to the notch pick
	// one of six arrangements.
	SnapWindows bool `json:"snapWindows"`
	// Language is the language the interface says: "" follows the system,
	// else a tag of a language the app ships ("zh-Hans", "en"). The app
	// keeps it here, not in the system's language settings.
	Language string `json:"language"`
	// AIUsage is the AI 用量 page: off by default; each source (quota and
	// local usage) can be switched separately.
	AIUsage AIUsageSettings `json:"aiUsage"`
	// LiveActivities is the 实时活动 group: the charging, headphone and
	// download notices on the notch. Every switch is on for a fresh
	// install; a settings.json without the object keeps all three.
	LiveActivities LiveActivitySettings `json:"liveActivities"`
	// AgentNotify is the 设置 → AI 用量 group that announces a finished
	// agent turn on the notch. Off by default: while it is on, Rakko's hook
	// entries live in the installed agents' own configuration files.
	AgentNotify AgentNotifySettings `json:"agentNotify"`
	// Approval is the 设置 → AI 用量 group that puts an agent's permission
	// requests on the notch. Off by default and independent of AgentNotify:
	// it is a separate opt-in writing its own hook entries.
	Approval ApprovalSettings `json:"approval"`
	// Lyrics remembers whether the home page's lyrics column is open (the
	// player's lyrics button). A missing object keeps it closed: nothing is
	// asked of lrclib.net until the user opens it.
	Lyrics *LyricsSettings `json:"lyrics,omitempty"`
	// IslandPos is where the floating island was left on each display, by
	// display id. A display with no entry keeps the default position; the
	// notch style ignores the whole map.
	IslandPos map[string]IslandPos `json:"islandPos,omitempty"`
	// Pet is the desktop otter: whether it is out at all, and its size.
	Pet PetSettings `json:"pet"`
	// PetPos is where the otter was left on each display, by display id,
	// measured from that display's origin like IslandPos. A display with
	// no entry gets the default spot — bottom right, above the Dock — and
	// that absence is also how the first launch knows to play its arrival.
	PetPos map[string]IslandPos `json:"petPos,omitempty"`
	// PetDisplay is the display the otter was last left on, by the same id
	// PetPos uses. The spots of every display stay remembered, so a display
	// that comes back keeps its place; this decides which one the otter
	// returns to.
	PetDisplay string `json:"petDisplay,omitempty"`
	// Shortcuts are the global keyboard shortcuts. They are user-editable
	// in the Settings window, and every one is an Electron-style
	// accelerator ("Ctrl+Alt+Space").
	Shortcuts ShortcutSettings `json:"shortcuts"`
	// SearchMode is the mode the home search bar opens on: 全部, 应用 or
	// 文件. It is the default for the next opening, not a memory of the
	// last search: Tab changes one search without saving here. A file
	// without the key (an older version, a hand edit) keeps 全部.
	SearchMode SearchMode `json:"searchMode"`
	// PinnedApps is the app switcher's 固定常用 list of bundle identifiers,
	// in the order the wings show them. nil means the user never edited it
	// and the switcher shows its defaults; an explicit empty list (the
	// user unpinned everything or saved none) stays empty — a missing key
	// and "none" are different states, like Lyrics's pointer.
	PinnedApps *[]string `json:"pinnedApps,omitempty"`
	// Onboarding is the first-launch guide: Pending is set only by a
	// launch that found no settings.json (a fresh install) and cleared
	// when the guide finishes or is skipped. A file without the object —
	// every install that upgraded to this version — keeps Pending false,
	// so the guide is never replayed for users who already know the app.
	Onboarding OnboardingSettings `json:"onboarding"`

	// Weekly is the 每周简报. AppStats is the explicit consent to collect
	// frontmost-app time; it is off until the user turns it on in the
	// report and nothing turns it on for them.
	Weekly WeeklySettings `json:"weekly"`
}

// OnboardingSettings is the onboarding object in settings.json: the
// first-launch guide's pending flag and the step it reached. Pending is
// true only after a launch that found no settings.json at all (a fresh
// install wrote it into the defaults); an upgraded install's file simply
// lacks the object, which reads as pending=false, so the guide never
// reappears. Step names the page to resume at after a relaunch (a move to
// /Applications or a permission grant that needs a restart); "" starts at
// the first step.
type OnboardingSettings struct {
	Pending bool   `json:"pending"`
	Step    string `json:"step,omitempty"`
}

// WeeklySettings is the 每周简报's preferences. AppStats is the user's
// consent to sample the frontmost app and the idle clock; without it the
// report still shows AI usage and Rakko completions, and the app section
// asks to be turned on instead of drawing an empty chart.
type WeeklySettings struct {
	AppStats bool `json:"appStats"`
}

// ShortcutSettings are the global keyboard shortcuts. A missing key keeps
// its default (see Default); an explicit empty string turns that shortcut
// off, which is what Delete in the Settings window saves.
type ShortcutSettings struct {
	Toggle     string `json:"toggle"`
	Clipboard  string `json:"clipboard"`
	Capture    string `json:"capture"`
	Screenshot string `json:"screenshot"`
	Tray       string `json:"tray"`
	// PlayPause, Previous and Next drive the system's now-playing app.
	PlayPause string `json:"playPause"`
	Previous  string `json:"previous"`
	Next      string `json:"next"`
	// Tools and Usage open a panel page.
	Tools string `json:"tools"`
	Usage string `json:"usage"`
	// Awake toggles 保持唤醒, Note starts a new 便利贴, and Pet shows or
	// hides the desktop otter.
	Awake string `json:"awake"`
	Note  string `json:"note"`
	Pet   string `json:"pet"`
	// AppSwitch opens the app switcher wings on the notch; empty by default.
	AppSwitch string `json:"appSwitch"`
}

// The shortcut defaults: the tray toggle, clipboard search and text
// capture ship set; every other shortcut has none.
const (
	DefaultShortcutToggle     = "Ctrl+Alt+Space"
	DefaultShortcutClipboard  = "Ctrl+Alt+V"
	DefaultShortcutCapture    = "Ctrl+Alt+O"
	DefaultShortcutScreenshot = "Ctrl+Alt+A"
	DefaultShortcutTray       = ""
)

// IslandPos is the floating island's place on one display: the shell's
// centre X and its top Y, measured from the display's origin. Storing the
// spot rather than a delta keeps it meaningful when a display's origin
// moves in 显示器 settings.
type IslandPos struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// LiveActivitySettings switches the three realtime activities. A missing
// key keeps the default (on), so an older settings.json shows all of them
// after the update.
type LiveActivitySettings struct {
	Charging   bool `json:"charging"`
	Headphones bool `json:"headphones"`
	Downloads  bool `json:"downloads"`
}

// AgentNotifySettings is the agentNotify object in settings.json. Enabled
// is off for a fresh install: turning it on writes Rakko's hook entries
// into the agents' own configuration files, and turning it off removes them
// again.
//
// Off lists the tools whose own switch the user turned off while the group
// switch stays on. Empty, or a missing key, means every detected tool is on
// — the behaviour settings.json had before the per-tool switches existed.
type AgentNotifySettings struct {
	Enabled bool     `json:"enabled"`
	Off     []string `json:"off,omitempty"`
}

// ToolOff reports whether the user switched one tool off on its own row.
func (s AgentNotifySettings) ToolOff(id string) bool {
	for _, off := range s.Off {
		if off == id {
			return true
		}
	}
	return false
}

// SetToolOn stores one tool's own switch: on drops the tool from Off, off
// adds it. The slice is rebuilt, never edited in place, so the settings
// value a caller copied earlier keeps its meaning.
func (s *AgentNotifySettings) SetToolOn(id string, on bool) {
	off := make([]string, 0, len(s.Off)+1)
	for _, v := range s.Off {
		if v != id {
			off = append(off, v)
		}
	}
	if !on {
		off = append(off, id)
	}
	if len(off) == 0 {
		off = nil
	}
	s.Off = off
}

// pruneOff drops IDs of tools Rakko does not know and repeats, so a
// hand-edited settings.json cannot keep a phantom tool switched off
// forever.
func (s *AgentNotifySettings) pruneOff() {
	off := make([]string, 0, len(s.Off))
	seen := make(map[string]bool, len(s.Off))
	for _, id := range s.Off {
		if seen[id] || !agentnotify.Tool(id).Valid() {
			continue
		}
		seen[id] = true
		off = append(off, id)
	}
	if len(off) == 0 {
		off = nil
	}
	s.Off = off
}

// ApprovalSettings is the approval object in settings.json. Enabled is off
// for a fresh install. Off lists the tools whose approval row the user
// switched off while the group switch stays on; empty means every detected
// tool is on.
type ApprovalSettings struct {
	Enabled bool     `json:"enabled"`
	Off     []string `json:"off,omitempty"`
}

// ToolOff reports whether the user switched one tool off on its own row.
func (s ApprovalSettings) ToolOff(id string) bool {
	for _, off := range s.Off {
		if off == id {
			return true
		}
	}
	return false
}

// SetToolOn stores one tool's own approval switch.
func (s *ApprovalSettings) SetToolOn(id string, on bool) {
	off := make([]string, 0, len(s.Off)+1)
	for _, v := range s.Off {
		if v != id {
			off = append(off, v)
		}
	}
	if !on {
		off = append(off, id)
	}
	if len(off) == 0 {
		off = nil
	}
	s.Off = off
}

// pruneOff drops unknown and repeated tool ids, like AgentNotify's.
func (s *ApprovalSettings) pruneOff() {
	off := make([]string, 0, len(s.Off))
	seen := make(map[string]bool, len(s.Off))
	for _, id := range s.Off {
		t := agentnotify.Tool(id)
		if seen[id] || !t.Valid() || !t.Approvable() {
			continue
		}
		seen[id] = true
		off = append(off, id)
	}
	if len(off) == 0 {
		off = nil
	}
	s.Off = off
}

// LyricsSettings is the lyrics object in settings.json: whether the home
// page's lyrics column is open, which is also the only time it asks
// lrclib.net for synced lyrics. Closed for a fresh install and for a file
// that has no lyrics object; the player's lyrics button opens it.
type LyricsSettings struct {
	Enabled bool `json:"enabled"`
}

// LyricsOn reports whether the lyrics column is open.
func (s Settings) LyricsOn() bool {
	return s.Lyrics != nil && s.Lyrics.Enabled
}

// SetLyrics stores the switch.
func (s *Settings) SetLyrics(on bool) {
	s.Lyrics = &LyricsSettings{Enabled: on}
}

// PetSettings is the pet object in settings.json: 显示海獭 and its size.
// A missing object keeps the default (shown, medium), so an older
// settings.json gets the otter after the update.
type PetSettings struct {
	Shown bool    `json:"shown"`
	Size  PetSize `json:"size"`
}

// PetSize picks how tall the otter stands on the desktop: 72, 96 or 128
// points for the picture.
type PetSize string

const (
	PetSizeSmall  PetSize = "small"
	PetSizeMedium PetSize = "medium"
	PetSizeLarge  PetSize = "large"
)

// Normalized maps an unknown or empty value (an older settings.json, a
// hand edit) to the default, medium.
func (p PetSize) Normalized() PetSize {
	switch p {
	case PetSizeSmall, PetSizeLarge:
		return p
	}
	return PetSizeMedium
}

// ShellStyle picks the closed shelf's outer shape on a screen with a
// camera housing. A screen without one always draws the island, whatever
// this says ("notch" is the default).
type ShellStyle string

const (
	ShellStyleNotch  ShellStyle = "notch"
	ShellStyleIsland ShellStyle = "island"
)

// Normalized maps an unknown or empty value (an older settings.json, a
// hand edit) to the default style.
func (s ShellStyle) Normalized() ShellStyle {
	if s == ShellStyleIsland {
		return ShellStyleIsland
	}
	return ShellStyleNotch
}

// SearchMode is the mode the home search bar opens on. It is a default,
// not a memory of the last search: every opening starts here, and Tab
// changes only the search on screen.
type SearchMode string

const (
	SearchModeAll   SearchMode = "all"
	SearchModeApps  SearchMode = "apps"
	SearchModeFiles SearchMode = "files"
)

// Normalized maps an unknown or empty value (an older settings.json, a
// hand edit) to the default, all.
func (m SearchMode) Normalized() SearchMode {
	switch m {
	case SearchModeApps, SearchModeFiles:
		return m
	}
	return SearchModeAll
}

// AIUsageSettings is the aiUsage object in settings.json. Enabled turns
// the AI 用量 page on; QuotaCodex and QuotaClaude pick which subscription
// cards it shows; QuotaLeft picks the quota display. Local usage is read
// from every supported agent automatically and has no switch.
type AIUsageSettings struct {
	Enabled     bool `json:"enabled"`
	QuotaCodex  bool `json:"quotaCodex"`
	QuotaClaude bool `json:"quotaClaude"`
	// QuotaLeft shows how much of a quota window is left instead of how
	// much is used; the used share is the default, as Magpie shows it.
	QuotaLeft bool `json:"quotaLeft"`
}

// defaultClipboardLimit is the clipboard text history cap a fresh
// install uses.
const defaultClipboardLimit = 30

// Default is what a fresh install uses.
func Default() Settings {
	return Settings{
		ClipboardLimit:   defaultClipboardLimit,
		AutoCheckUpdates: true,
		RemoveAfterDrag:  false,
		SnapWindows:      true,
		ShellStyle:       ShellStyleNotch,
		SearchMode:       SearchModeAll,
		LiveActivities: LiveActivitySettings{
			Charging:   true,
			Headphones: true,
			Downloads:  true,
		},
		AIUsage: AIUsageSettings{
			Enabled:     false,
			QuotaCodex:  true,
			QuotaClaude: true,
		},
		AgentNotify: AgentNotifySettings{Enabled: false},
		Approval:    ApprovalSettings{Enabled: false},
		Pet:         PetSettings{Shown: true, Size: PetSizeMedium},
		Weekly:      WeeklySettings{AppStats: false},
		Shortcuts: ShortcutSettings{
			Toggle:     DefaultShortcutToggle,
			Clipboard:  DefaultShortcutClipboard,
			Capture:    DefaultShortcutCapture,
			Screenshot: DefaultShortcutScreenshot,
			Tray:       DefaultShortcutTray,
		},
	}
}

// Save replaces dir/settings.json with s, atomically.
func Save(dir string, s Settings) error {
	if err := write(dir, s); err != nil {
		return fmt.Errorf("settings: saving %s: %w", FileName, err)
	}
	return nil
}

// Load reads dir/settings.json over the defaults.
//
// A missing file is created with the defaults so the user can find and edit
// it. A file that cannot be read or parsed is never rewritten: Load returns
// the defaults together with the error, and the caller decides how to tell
// the user.
func Load(dir string) (Settings, error) {
	s := Default()
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if werr := write(dir, s); werr != nil {
			return s, fmt.Errorf("settings: creating %s: %w", FileName, werr)
		}
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("settings: reading %s: %w", FileName, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Default(), fmt.Errorf("settings: %s is not valid JSON, using defaults: %w", FileName, err)
	}
	if s.ClipboardLimit < 1 {
		s.ClipboardLimit = defaultClipboardLimit
	}
	s.ShellStyle = s.ShellStyle.Normalized()
	s.SearchMode = s.SearchMode.Normalized()
	s.Pet.Size = s.Pet.Size.Normalized()
	s.AgentNotify.pruneOff()
	s.Approval.pruneOff()
	s.PinnedApps = normalizePinnedApps(s.PinnedApps)
	return s, nil
}

// PinnedAppsMax is the app switcher's 固定常用 cap: the wings have four seats.
const PinnedAppsMax = 4

// normalizePinnedApps drops blank and repeated bundle ids and applies the
// wing cap, keeping the saved order. nil stays nil: an untouched setting is
// not the same as a saved empty list.
func normalizePinnedApps(apps *[]string) *[]string {
	if apps == nil {
		return nil
	}
	seen := make(map[string]bool, len(*apps))
	out := make([]string, 0, len(*apps))
	for _, id := range *apps {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > PinnedAppsMax {
		out = out[:PinnedAppsMax]
	}
	return &out
}

// LanguageOf reads the language dir/settings.json names, without creating,
// rewriting or otherwise touching the file: the startup path that cannot open
// the data directory still has to say its refusal in the user's language, and
// it must leave the directory as it found it. ok is false when the file is
// missing, unreadable or not JSON, so the caller can try another directory; a
// file that parses reports its language, "" for follow the system.
func LanguageOf(dir string) (language string, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return "", false
	}
	var s struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return "", false
	}
	return s.Language, true
}

// Exists reports whether dir/settings.json is there, without creating or
// reading it. The first-launch check needs the bare fact, and it must run
// before Load because Load writes the file when it is missing — after Load
// the answer is always yes.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, FileName))
	return err == nil
}

// write saves s atomically: a temporary file in dir renamed over the target,
// so a crash never leaves a half-written settings.json.
func write(dir string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "."+FileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, FileName))
}
