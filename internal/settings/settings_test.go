package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMissingFileIsCreatedWithDefaults(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, Default()) {
		t.Fatalf("fresh load = %+v, want defaults %+v", s, Default())
	}
	again, err := Load(dir)
	if err != nil || !reflect.DeepEqual(again, Default()) {
		t.Fatalf("reloading the created file = %+v, %v", again, err)
	}
}

func TestMissingKeyKeepsDefault(t *testing.T) {
	// A file from an older version (or hand-edited) without the key must not
	// silently turn a feature off through Go's zero value.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"somethingElse": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.ClipboardLimit != 30 {
		t.Fatalf("missing clipboardLimit = %d, want 30", s.ClipboardLimit)
	}
	if len(s.ExcludedApps) != 0 {
		t.Fatalf("missing excludedApps = %v, want empty", s.ExcludedApps)
	}
}

// The now-playing toggle still loads from the file, and the removed
// battery/volume island keys in a legacy settings.json are ignored
// (unknown keys never fail a load) while the rest keep their defaults.
// snapWindows defaults on: a file without the key (older versions) must
// not silently turn window snapping off, and an explicit false is honoured.
func TestSnapWindowsLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil || !s.SnapWindows {
		t.Fatalf("missing snapWindows = %v, %v; want true", s.SnapWindows, err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"snapWindows": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil || s.SnapWindows {
		t.Fatalf("snapWindows: false => %v, %v; want false", s.SnapWindows, err)
	}
}

// A legacy islandNowPlaying key (and the removed battery/volume keys) is
// ignored, and the load keeps every current default: the wings and the home
// player are always on.
func TestLegacyIslandKeysAreIgnored(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"islandNowPlaying": false, "islandBattery": false, "islandVolume": false}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, Default()) {
		t.Fatalf("legacy keys changed the settings: %+v", s)
	}
}

// autoCheckUpdates defaults on so updates reach users; a file without the
// key (older versions, hand edits) must not silently disable it, and an
// explicit false is honoured.
func TestAutoCheckUpdatesLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil || !s.AutoCheckUpdates {
		t.Fatalf("missing autoCheckUpdates = %v, %v; want true", s.AutoCheckUpdates, err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"autoCheckUpdates": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil || s.AutoCheckUpdates {
		t.Fatalf("autoCheckUpdates: false ignored: %v, %v", s.AutoCheckUpdates, err)
	}
}

// aiUsage is a nested object: a file without the key (every install before
// the AI 用量 page) must load the page's defaults — the feature off, both
// quota cards on — never Go's zero value. Partial objects keep defaults for
// their missing keys too, and an explicit enabled is honoured.
func TestAIUsageLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.AIUsage.Enabled {
		t.Fatal("missing aiUsage turned the page on")
	}
	if !s.AIUsage.QuotaCodex || !s.AIUsage.QuotaClaude || s.AIUsage.QuotaLeft {
		t.Fatalf("missing aiUsage loaded wrong defaults: %+v", s.AIUsage)
	}
	// A partial object fills only the keys it sets.
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"aiUsage": {"enabled": true, "quotaCodex": false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil || !s.AIUsage.Enabled || s.AIUsage.QuotaCodex || !s.AIUsage.QuotaClaude {
		t.Fatalf("partial aiUsage = %+v, %v; want enabled, quotaCodex off, quotaClaude on", s.AIUsage, err)
	}
}

// Older files carry localOmp/localClaude/localCodex. Load ignores them (the
// readers are automatic now), and the next Save drops them.
func TestAIUsageDropsOldLocalSwitches(t *testing.T) {
	dir := t.TempDir()
	old := `{"aiUsage":{"enabled":true,"quotaCodex":false,"quotaClaude":true,"quotaLeft":true,` +
		`"localOmp":false,"localClaude":true,"localCodex":false}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.AIUsage.Enabled || s.AIUsage.QuotaCodex || !s.AIUsage.QuotaClaude || !s.AIUsage.QuotaLeft {
		t.Fatalf("old aiUsage loaded wrong: %+v", s.AIUsage)
	}
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"localOmp", "localClaude", "localCodex"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("saved settings still carries %s: %s", key, data)
		}
	}
}

func TestSaveRoundTrips(t *testing.T) {
	dir := t.TempDir()
	// The pet's size is normalized on load, so the literal spells out
	// medium rather than the empty string.
	want := Settings{RetentionDays: 7, ClipboardLimit: 12, ExcludedApps: []string{"com.example.one", "com.example.two"},
		ShellStyle: ShellStyleIsland, SearchMode: SearchModeApps,
		Pet: PetSettings{Size: PetSizeMedium}, Lyrics: Default().Lyrics}
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load after Save = %+v, %v; want %+v", got, err, want)
	}
}

// A stored clipboardLimit below 1 means "use the default", so the file
// can never disable or blow up the history cap by hand-editing.
func TestNonPositiveClipboardLimitLoadsAsDefault(t *testing.T) {
	for _, jsonLimit := range []string{"0", "-5"} {
		dir := t.TempDir()
		data := []byte(`{"clipboardLimit": ` + jsonLimit + `}`)
		if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if s.ClipboardLimit != 30 {
			t.Fatalf("clipboardLimit %s loaded as %d, want 30", jsonLimit, s.ClipboardLimit)
		}
	}
}

func TestInvalidFileIsNeverRewritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	bad := []byte(`{"clipboardLimit": invalid`)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err == nil {
		t.Fatal("invalid JSON must be reported")
	}
	if !reflect.DeepEqual(s, Default()) {
		t.Fatalf("invalid file gave %+v, want defaults", s)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(bad) {
		t.Fatalf("invalid settings.json was overwritten: %q", got)
	}
}

// searchMode is new in this version: a file without it keeps 全部, only the
// exact "apps" and "files" values choose another mode, and a hand-edited
// value the app cannot draw falls back to 全部.
func TestSearchModeLoad(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want SearchMode
	}{
		{`{}`, SearchModeAll},
		{`{"searchMode": "all"}`, SearchModeAll},
		{`{"searchMode": "apps"}`, SearchModeApps},
		{`{"searchMode": "files"}`, SearchModeFiles},
		{`{"searchMode": "APPS"}`, SearchModeAll},
		{`{"searchMode": ""}`, SearchModeAll},
		{`{"searchMode": "everything"}`, SearchModeAll},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(tt.raw), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(dir)
		if err != nil {
			t.Fatalf("%s: %v", tt.raw, err)
		}
		if s.SearchMode != tt.want {
			t.Fatalf("%s loaded searchMode %q, want %q", tt.raw, s.SearchMode, tt.want)
		}
	}
}

// A chosen mode survives a save and the next load; saving 全部 writes the
// explicit value, and a file without the key reads as 全部 too.
func TestSearchModeSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SearchMode = SearchModeFiles
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	back, err := Load(dir)
	if err != nil || back.SearchMode != SearchModeFiles {
		t.Fatalf("saved searchMode reloaded as %q, %v", back.SearchMode, err)
	}

	s.SearchMode = SearchModeAll
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"searchMode": "all"`) {
		t.Fatalf("saving 全部 wrote no mode: %s", data)
	}
	back, err = Load(dir)
	if err != nil || back.SearchMode != SearchModeAll {
		t.Fatalf("a saved 全部 loaded %q, %v", back.SearchMode, err)
	}
}

// shellStyle is new in this version: a file without it keeps the hardware
// notch shape, and only the exact "island" value switches the style —
// hand-edited junk must not choose something the app cannot draw.
func TestShellStyleLoad(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want ShellStyle
	}{
		{`{}`, ShellStyleNotch},
		{`{"shellStyle": "island"}`, ShellStyleIsland},
		{`{"shellStyle": "notch"}`, ShellStyleNotch},
		{`{"shellStyle": "ISLAND"}`, ShellStyleNotch},
		{`{"shellStyle": ""}`, ShellStyleNotch},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(tt.raw), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(dir)
		if err != nil {
			t.Fatalf("%s: %v", tt.raw, err)
		}
		if s.ShellStyle != tt.want {
			t.Fatalf("%s loaded shellStyle %q, want %q", tt.raw, s.ShellStyle, tt.want)
		}
	}
}

// LanguageOf reads the language without touching the directory: the startup
// path that cannot open its data directory still names the user's language in
// its refusal, and must not create settings.json, rewrite a broken one, or
// report a language from a file it could not parse.
func TestLanguageOf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if lang, ok := LanguageOf(dir); ok || lang != "" {
		t.Fatalf("a missing settings.json answered %q, %v", lang, ok)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("LanguageOf created settings.json: %v", err)
	}

	s := Default()
	s.Language = "zh-Hans"
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	if lang, ok := LanguageOf(dir); !ok || lang != "zh-Hans" {
		t.Fatalf("LanguageOf = %q, %v, want zh-Hans, true", lang, ok)
	}
	// Follow System is a language the file names: "" with ok true.
	if err := Save(dir, Default()); err != nil {
		t.Fatal(err)
	}
	if lang, ok := LanguageOf(dir); !ok || lang != "" {
		t.Fatalf("LanguageOf = %q, %v, want the empty language, true", lang, ok)
	}

	bad := []byte(`{"language": `)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if lang, ok := LanguageOf(dir); ok || lang != "" {
		t.Fatalf("a broken settings.json answered %q, %v", lang, ok)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(bad) {
		t.Fatalf("LanguageOf rewrote the broken file: %q, %v", got, err)
	}
}

// The shortcuts are preferences: they round-trip, and a file from an older
// version without them keeps every default.
func TestShortcutSettingsLoad(t *testing.T) {
	dir := t.TempDir()
	prefs := Default()
	prefs.Shortcuts.Toggle = "Super+Shift+K"
	prefs.Shortcuts.Tray = ""
	if err := Save(dir, prefs); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil || loaded.Shortcuts != prefs.Shortcuts {
		t.Fatalf("shortcuts did not round-trip: %+v, %v", loaded.Shortcuts, err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"language": ""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(dir)
	if err != nil || loaded.Shortcuts != Default().Shortcuts {
		t.Fatalf("missing shortcuts did not default: %+v, %v", loaded.Shortcuts, err)
	}
}

// A settings.json from a build that still saved panelAppearance loads as
// usual: the retired key is ignored, never an error.
func TestRetiredPanelAppearanceIsIgnored(t *testing.T) {
	dir := t.TempDir()
	body := `{"panelAppearance": "glass", "shellStyle": "island", "clipboardLimit": 12}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.ShellStyle != ShellStyleIsland || s.ClipboardLimit != 12 {
		t.Fatalf("the saved keys around it were lost: %+v", s)
	}
}

// A settings.json from before the otter lived on the desktop has no pet
// object: it must load shown and medium, never Go's zero value (hidden).
// A partial object keeps the default it does not set, and a size this
// build does not know falls back to medium.
func TestPetLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Pet.Shown || s.Pet.Size != PetSizeMedium {
		t.Fatalf("missing pet loaded as %+v, want shown and medium", s.Pet)
	}
	if len(s.PetPos) != 0 {
		t.Fatalf("a fresh file carries pet positions: %v", s.PetPos)
	}

	if err := os.WriteFile(filepath.Join(dir, FileName),
		[]byte(`{"pet": {"size": "large"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err = Load(dir); err != nil || !s.Pet.Shown || s.Pet.Size != PetSizeLarge {
		t.Fatalf("partial pet = %+v, %v; want shown and large", s.Pet, err)
	}

	if err := os.WriteFile(filepath.Join(dir, FileName),
		[]byte(`{"pet": {"shown": false, "size": "huge"}, "petPos": {"1": {"x": 20, "y": 30}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err = Load(dir); err != nil || s.Pet.Shown || s.Pet.Size != PetSizeMedium {
		t.Fatalf("stowed pet = %+v, %v; want hidden and medium", s.Pet, err)
	}
	if got := s.PetPos["1"]; got.X != 20 || got.Y != 30 {
		t.Fatalf("pet position = %+v, want (20, 30)", got)
	}
}

// Lyrics default closed: a settings.json without the key (and a fresh
// install) asks lrclib.net nothing; opening the column is remembered.
func TestLyricsDefaultsClosedAndRemembersOpen(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil || s.LyricsOn() || Default().LyricsOn() {
		t.Fatalf("missing lyrics = %v, %v; want closed", s.LyricsOn(), err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"lyrics":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil || !s.LyricsOn() {
		t.Fatalf("lyrics.enabled true => %v, %v; want open", s.LyricsOn(), err)
	}
}

// TestAgentNotifyOffLoadsClean: the per-tool switch list keeps only tools
// Rakko knows, once each, and a settings.json without it keeps every tool on.
func TestAgentNotifyOffLoadsClean(t *testing.T) {
	dir := t.TempDir()
	body := `{"agentNotify": {"enabled": true, "off": ["gemini", "nope", "gemini", "claude", ""]}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gemini", "claude"}; !reflect.DeepEqual(s.AgentNotify.Off, want) {
		t.Fatalf("off = %v, want %v", s.AgentNotify.Off, want)
	}

	// The key missing entirely: nothing is switched off, as before the
	// per-tool switches existed.
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"agentNotify": {"enabled": true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil || len(s.AgentNotify.Off) != 0 || s.AgentNotify.ToolOff("claude") {
		t.Fatalf("a file without off = %+v, %v; want every tool on", s.AgentNotify, err)
	}
}

// TestAgentNotifyToolSwitchRoundTrips: the row's switch edits the list, and
// the value survives a save and a load.
func TestAgentNotifyToolSwitchRoundTrips(t *testing.T) {
	dir := t.TempDir()
	s := Default()
	s.AgentNotify.Enabled = true
	s.AgentNotify.SetToolOn("codex", false)
	s.AgentNotify.SetToolOn("claude", false)
	s.AgentNotify.SetToolOn("codex", true)
	if want := []string{"claude"}; !reflect.DeepEqual(s.AgentNotify.Off, want) {
		t.Fatalf("off = %v, want %v", s.AgentNotify.Off, want)
	}
	if s.AgentNotify.ToolOff("codex") || !s.AgentNotify.ToolOff("claude") {
		t.Fatalf("switch lookup disagrees with the list: %+v", s.AgentNotify)
	}
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.AgentNotify, s.AgentNotify) {
		t.Fatalf("reload = %+v, want %+v", got.AgentNotify, s.AgentNotify)
	}
	// Switching everything back on drops the key entirely.
	got.AgentNotify.SetToolOn("claude", true)
	if err := Save(dir, got); err != nil {
		t.Fatal(err)
	}
	again, err := Load(dir)
	if err != nil || again.AgentNotify.Off != nil {
		t.Fatalf("off after every tool is on = %v, %v; want nil", again.AgentNotify.Off, err)
	}
}

// The shortcuts added for playback, panel pages and the desktop features
// default to unset and survive a save and load like the original four.
func TestShortcutSettingsNewKeys(t *testing.T) {
	def := Default().Shortcuts
	if def.PlayPause != "" || def.Previous != "" || def.Next != "" ||
		def.Tools != "" || def.Usage != "" || def.Awake != "" ||
		def.Note != "" || def.Pet != "" {
		t.Fatalf("a fresh install sets a new shortcut: %+v", def)
	}
	dir := t.TempDir()
	prefs := Default()
	prefs.Shortcuts.PlayPause = "Ctrl+Alt+P"
	prefs.Shortcuts.Previous = "Ctrl+Alt+Left"
	prefs.Shortcuts.Next = "Ctrl+Alt+Right"
	prefs.Shortcuts.Tools = "Ctrl+Alt+G"
	prefs.Shortcuts.Usage = "Ctrl+Alt+U"
	prefs.Shortcuts.Awake = "Ctrl+Alt+W"
	prefs.Shortcuts.Note = "Ctrl+Alt+N"
	prefs.Shortcuts.Pet = "Ctrl+Alt+E"
	if err := Save(dir, prefs); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil || loaded.Shortcuts != prefs.Shortcuts {
		t.Fatalf("new shortcuts did not round-trip: %+v, %v", loaded.Shortcuts, err)
	}
}

// The update window's keys default to asking: automatic install off, no
// skipped version, no quiet period. A file without them (every install
// before the window) keeps those defaults; saved values round-trip.
func TestUpdateWindowKeys(t *testing.T) {
	def := Default()
	if def.UpdateAutoInstall || def.UpdateSkippedVersion != "" ||
		def.UpdateRemindVersion != "" || def.UpdateRemindUntil != 0 {
		t.Fatalf("update window defaults = %+v", def)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.UpdateAutoInstall || s.UpdateSkippedVersion != "" ||
		s.UpdateRemindVersion != "" || s.UpdateRemindUntil != 0 {
		t.Fatalf("missing keys did not fall back to the defaults: %+v", s)
	}
	want := def
	want.UpdateAutoInstall = true
	want.UpdateSkippedVersion = "1.14.0"
	want.UpdateRemindVersion = "1.15.0"
	want.UpdateRemindUntil = 1_760_000_000
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("saved update keys did not round-trip:\n got %+v\nwant %+v", got, want)
	}
}

// Exists answers the bare fact without creating the file: the first-launch
// check must run before Load, which writes the default file on a fresh
// install. A file without the onboarding object — every install that
// upgraded — is never read as an owed guide.
func TestOnboardingExistsAndUpgradesAreNotFresh(t *testing.T) {
	dir := t.TempDir()
	if Exists(dir) {
		t.Fatal("Exists reported a file before one was written")
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	if !Exists(dir) {
		t.Fatal("Load did not create settings.json")
	}

	upgrade := t.TempDir()
	if err := os.WriteFile(filepath.Join(upgrade, FileName), []byte(`{"snapWindows": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(upgrade)
	if err != nil {
		t.Fatal(err)
	}
	if s.Onboarding.Pending {
		t.Fatal("an upgraded install reads as a fresh one")
	}

	saved := Default()
	saved.Onboarding = OnboardingSettings{Pending: true, Step: "move"}
	if err := Save(upgrade, saved); err != nil {
		t.Fatal(err)
	}
	got, err := Load(upgrade)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Onboarding.Pending || got.Onboarding.Step != "move" {
		t.Fatalf("onboarding did not round-trip: %+v", got.Onboarding)
	}
}

// Approvals are a separate, off-by-default opt-in: a settings file written
// before the feature existed keeps them off, and a tool id that cannot be
// approved is dropped from the off list.
func TestApprovalDefaultsOff(t *testing.T) {
	if Default().Approval.Enabled {
		t.Fatal("a fresh install has approvals on")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName),
		[]byte(`{"agentNotify":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Approval.Enabled {
		t.Fatal("an old settings file turned approvals on")
	}
	s.Approval.Off = []string{"claude", "cursor", "claude", "nonsense"}
	s.Approval.pruneOff()
	if len(s.Approval.Off) != 1 || s.Approval.Off[0] != "claude" {
		t.Fatalf("pruned off list %v", s.Approval.Off)
	}
}
