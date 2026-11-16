package aiusage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// rootSpec is one directory a reader walks and the basename it collects.
// whole files are parsed as one document (JSON or compressed JSONL); the rest
// are plain JSONL read incrementally.
type rootSpec struct {
	dir    string
	match  string
	whole  bool
	filter func(path string) bool
}

// dbSpec is one SQLite database file and its reader. Stateless DBs set read;
// cumulative-counter DBs (Hermes) set readDelta so successive scans only
// attribute newly added tokens to the new timestamp.
type dbSpec struct {
	path      string
	read      func(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) ([]rec, error)
	readDelta func(ctx context.Context, dbPath, cacheDir string, pool map[string]*string, old *fileState) ([]rec, map[string]cumSnapshot, error)
}

// toolEntry is one entry in the AI tool registry: display name, installation
// probe, local data locations, and parser.
type toolEntry struct {
	tool       Tool
	name       string
	unreadable bool
	detect     func(home string) []string
	roots      func(home string) []rootSpec
	dbs        func(home string) []dbSpec
	parseLine  func(path string, line []byte, pool map[string]*string, st *fileState) (rec, string, bool)
	parseWhole func(path string, data []byte, pool map[string]*string) ([]rec, error)
}

func envOrHome(env, home string, fallback ...string) string {
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		if v == "~" {
			return home
		}
		if strings.HasPrefix(v, "~/") || strings.HasPrefix(v, `~\`) {
			return filepath.Join(home, v[2:])
		}
		return v
	}
	return joinHome(home, fallback...)
}

// winAppData returns %APPDATA% when home is the current user's home, or
// <home>/AppData/Roaming in isolated tests.
func winAppData(home string) string {
	if uh, err := os.UserHomeDir(); err == nil && filepath.Clean(uh) == filepath.Clean(home) {
		if v := os.Getenv("APPDATA"); v != "" {
			return v
		}
	}
	return filepath.Join(home, "AppData", "Roaming")
}

// winLocalAppData returns %LOCALAPPDATA% when home is the current user's home,
// or <home>/AppData/Local in isolated tests.
func winLocalAppData(home string) string {
	if uh, err := os.UserHomeDir(); err == nil && filepath.Clean(uh) == filepath.Clean(home) {
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return v
		}
	}
	return filepath.Join(home, "AppData", "Local")
}

func xdgDataHome(home string) string {
	if uh, err := os.UserHomeDir(); err == nil && filepath.Clean(uh) == filepath.Clean(home) {
		if v := os.Getenv("XDG_DATA_HOME"); v != "" {
			return v
		}
	}
	return joinHome(home, ".local", "share")
}

func xdgConfigHome(home string) string {
	if uh, err := os.UserHomeDir(); err == nil && filepath.Clean(uh) == filepath.Clean(home) {
		if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
			return v
		}
	}
	return joinHome(home, ".config")
}

// appSupportDir returns the platform application-support / roaming config dir
// (macOS: ~/Library/Application Support, Windows: %APPDATA%, Linux: ~/.config).
func appSupportDir(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return joinHome(home, "Library", "Application Support")
	case "windows":
		return winAppData(home)
	default:
		return xdgConfigHome(home)
	}
}

func vscodeGlobalStorageDirs(home, extID string) []string {
	var dirs []string
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, joinHome(home, "Library", "Application Support", "Code", "User", "globalStorage", extID, "tasks"))
	case "windows":
		dirs = append(dirs, filepath.Join(winAppData(home), "Code", "User", "globalStorage", extID, "tasks"))
	default:
		dirs = append(dirs, filepath.Join(xdgConfigHome(home), "Code", "User", "globalStorage", extID, "tasks"))
	}
	dirs = append(dirs, joinHome(home, ".vscode-server", "data", "User", "globalStorage", extID, "tasks"))
	return dirs
}

func allGlobs(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

var registry = []toolEntry{
	{
		tool: ToolOmp,
		name: "omp",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".omp", "agent", "agent.db"),
				joinHome(home, ".omp", "agent", "config.yml"),
				joinHome(home, ".omp", "agent", "sessions"),
			}
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{{dir: joinHome(home, ".omp", "agent", "sessions"), match: "*.jsonl"}}
		},
		parseLine: func(_ string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseOmpLine(line, pool)
		},
	},
	{
		tool: ToolClaude,
		name: "Claude Code",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".claude", "projects"),
				joinHome(home, ".claude", "history.jsonl"),
				joinHome(home, ".claude.json"),
			}
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{
				{dir: joinHome(home, ".claude", "projects"), match: "*.jsonl"},
				{dir: joinHome(home, ".claude", "transcripts"), match: "*.jsonl"},
			}
		},
		parseLine: func(_ string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseClaudeLine(line, pool)
		},
	},
	{
		tool: ToolCodex,
		name: "Codex",
		detect: func(home string) []string {
			base := envOrHome("CODEX_HOME", home, ".codex")
			return []string{
				filepath.Join(base, "sessions"),
				filepath.Join(base, "auth.json"),
				filepath.Join(base, "config.toml"),
				filepath.Join(base, ".codex-global-state.json"),
			}
		},
		roots: func(home string) []rootSpec {
			base := envOrHome("CODEX_HOME", home, ".codex")
			return []rootSpec{
				{dir: filepath.Join(base, "sessions"), match: "rollout-*.jsonl"},
				{dir: filepath.Join(base, "sessions"), match: "rollout-*.jsonl.zst", whole: true},
				{dir: filepath.Join(base, "archived_sessions"), match: "*.jsonl"},
				{dir: filepath.Join(base, "archived_sessions"), match: "*.jsonl.zst", whole: true},
			}
		},
		parseLine: func(path string, line []byte, pool map[string]*string, st *fileState) (rec, string, bool) {
			return st.tail.parseCodexLineWithKey(path, line, pool)
		},
		parseWhole: parseCodexZstdFile,
	},
	{
		tool: ToolPi,
		name: "Pi",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".pi", "agent", "auth.json"),
				joinHome(home, ".pi", "agent", "sessions"),
				joinHome(home, ".pi", "agent", "settings.json"),
			}
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{{dir: joinHome(home, ".pi", "agent", "sessions"), match: "*.jsonl"}}
		},
		parseLine: func(_ string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseOmpLine(line, pool)
		},
	},
	{
		tool: ToolOpencode,
		name: "opencode",
		detect: func(home string) []string {
			return []string{
				filepath.Join(xdgDataHome(home), "opencode", "storage"),
				filepath.Join(xdgDataHome(home), "opencode", "auth.json"),
				filepath.Join(xdgConfigHome(home), "opencode", "opencode.json"),
				filepath.Join(xdgConfigHome(home), "opencode", "opencode.jsonc"),
				opencodeDBPath(home),
			}
		},
		dbs: func(home string) []dbSpec {
			return []dbSpec{{path: opencodeDBPath(home), read: readOpencode}}
		},
	},
	{
		tool: ToolGrok,
		name: "Grok",
		detect: func(home string) []string {
			base := envOrHome("GROK_HOME", home, ".grok")
			return []string{
				filepath.Join(base, "auth.json"),
				filepath.Join(base, "bin"),
				filepath.Join(base, "active_sessions.json"),
				filepath.Join(base, "sessions"),
			}
		},
		roots: func(home string) []rootSpec {
			base := envOrHome("GROK_HOME", home, ".grok")
			return []rootSpec{
				{dir: filepath.Join(base, "sessions"), match: "usage.json", whole: true},
				{dir: filepath.Join(base, "sessions"), match: "updates.jsonl"},
			}
		},
		parseWhole: func(_ string, data []byte, pool map[string]*string) ([]rec, error) {
			return parseGrokUsageFile(data, pool)
		},
		parseLine: func(_ string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseGrokUpdateLine(line, pool)
		},
	},
	{
		tool: ToolAntigravity,
		name: "Antigravity",
		detect: func(home string) []string {
			base := envOrHome("GEMINI_CLI_HOME", home, ".gemini")
			return []string{
				filepath.Join(base, "antigravity-cli", "brain"),
				filepath.Join(base, "antigravity-cli", "conversations"),
			}
		},
		roots: func(home string) []rootSpec {
			base := envOrHome("GEMINI_CLI_HOME", home, ".gemini")
			return []rootSpec{
				{
					dir:   filepath.Join(base, "antigravity-cli", "brain"),
					match: "transcript_full.jsonl",
					filter: func(p string) bool {
						return filepath.Base(filepath.Dir(p)) == "logs" &&
							filepath.Base(filepath.Dir(filepath.Dir(p))) == ".system_generated"
					},
				},
			}
		},
		parseLine: func(path string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseAntigravityLine(path, line, pool)
		},
	},
	{
		tool: ToolHermes,
		name: "Hermes",
		detect: func(home string) []string {
			base := envOrHome("HERMES_HOME", home, ".hermes")
			return []string{
				filepath.Join(base, "config.yaml"),
				filepath.Join(base, "hermes-agent"),
				filepath.Join(base, "state.db"),
			}
		},
		dbs: func(home string) []dbSpec {
			base := envOrHome("HERMES_HOME", home, ".hermes")
			out := []dbSpec{{path: filepath.Join(base, "state.db"), readDelta: readHermesDB}}
			for _, p := range allGlobs(filepath.Join(base, "profiles", "*", "state.db")) {
				out = append(out, dbSpec{path: p, readDelta: readHermesDB})
			}
			return out
		},
	},
	{
		tool: ToolDevin,
		name: "Devin",
		detect: func(home string) []string {
			out := []string{
				filepath.Join(xdgConfigHome(home), "devin", "config.json"),
				filepath.Join(xdgConfigHome(home), "devin", "cli"),
				filepath.Join(xdgDataHome(home), "devin", "cli", "sessions.db"),
			}
			if runtime.GOOS == "windows" {
				out = append(out, filepath.Join(winLocalAppData(home), "devin", "cli", "sessions.db"))
			}
			return out
		},
		dbs: func(home string) []dbSpec {
			out := []dbSpec{{path: filepath.Join(xdgDataHome(home), "devin", "cli", "sessions.db"), read: readDevinDB}}
			if runtime.GOOS == "windows" {
				out = append(out, dbSpec{path: filepath.Join(winLocalAppData(home), "devin", "cli", "sessions.db"), read: readDevinDB})
			}
			return out
		},
	},
	{
		tool: ToolDsh,
		name: "DeepSeek Harness",
		detect: func(home string) []string {
			base := envOrHome("DSH_HOME", home, ".dsh")
			return []string{
				filepath.Join(base, "profiles"),
				filepath.Join(base, "sessions"),
				filepath.Join(base, ".credentials.yaml"),
			}
		},
		roots: func(home string) []rootSpec {
			dir := filepath.Join(envOrHome("DSH_HOME", home, ".dsh"), "sessions")
			return []rootSpec{
				{dir: dir, match: "session*.jsonl", whole: true},
				{dir: dir, match: "session*.jsonl.zstd", whole: true},
			}
		},
		parseWhole: func(path string, data []byte, pool map[string]*string) ([]rec, error) {
			if strings.HasSuffix(path, ".zstd") {
				return parseDSHZstdFile(path, data, pool)
			}
			return parseDSHFile(path, data, pool)
		},
	},
	{
		tool: ToolGemini,
		name: "Gemini CLI",
		detect: func(home string) []string {
			base := envOrHome("GEMINI_CLI_HOME", home, ".gemini")
			return []string{
				filepath.Join(base, "installation_id"),
				filepath.Join(base, "google_accounts.json"),
				filepath.Join(base, "oauth_creds.json"),
				filepath.Join(base, "tmp"),
			}
		},
		roots: func(home string) []rootSpec {
			base := envOrHome("GEMINI_CLI_HOME", home, ".gemini")
			return []rootSpec{{
				dir:   filepath.Join(base, "tmp"),
				match: "*.json",
				whole: true,
				filter: func(p string) bool {
					return filepath.Base(filepath.Dir(p)) == "chats"
				},
			}}
		},
		parseWhole: parseGeminiChatFile,
	},
	{
		tool: ToolQwen,
		name: "Qwen Code",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".qwen", "settings.json"),
				joinHome(home, ".qwen", "oauth_creds.json"),
				joinHome(home, ".qwen", "installation_id"),
				joinHome(home, ".qwen", "tmp"),
				joinHome(home, ".qwen", "projects"),
			}
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{
				{dir: joinHome(home, ".qwen", "projects"), match: "*.jsonl"},
				{
					dir:   joinHome(home, ".qwen", "tmp"),
					match: "*.json",
					whole: true,
					filter: func(p string) bool {
						return filepath.Base(filepath.Dir(p)) == "chats"
					},
				},
			}
		},
		parseLine: func(_ string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseClaudeLine(line, pool)
		},
		parseWhole: parseGeminiChatFile,
	},
	{
		tool: ToolDroid,
		name: "Droid",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".factory", "projects"),
				joinHome(home, ".factory", "sessions"),
				joinHome(home, ".local", "bin", "droid"),
			}
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{
				{dir: joinHome(home, ".factory", "sessions"), match: "*.jsonl"},
				{dir: joinHome(home, ".factory", "projects"), match: "*.jsonl"},
			}
		},
		parseLine: func(path string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseFlexJSONLLine("droid", path, line, pool)
		},
	},
	{
		tool: ToolAugment,
		name: "Auggie",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".augment", "settings.json"),
				joinHome(home, ".augment", "sessions"),
				joinHome(home, ".augment", "session.json"),
			}
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{{dir: joinHome(home, ".augment", "sessions"), match: "*.json", whole: true}}
		},
		parseWhole: parseAuggieSessionFile,
	},
	{
		tool: ToolCopilot,
		name: "Copilot CLI",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".copilot", "config.json"),
				joinHome(home, ".copilot", "session-state"),
				joinHome(home, ".copilot", "session-store.db"),
				joinHome(home, ".copilot", "data.db"),
				joinHome(home, ".copilot", "logs"),
				joinHome(home, ".copilot", "otel"),
			}
		},
		dbs: func(home string) []dbSpec {
			return []dbSpec{
				{path: joinHome(home, ".copilot", "session-store.db"), read: readCopilotDB},
				{path: joinHome(home, ".copilot", "data.db"), readDelta: readCopilotDataDB},
			}
		},
		roots: func(home string) []rootSpec {
			out := []rootSpec{{dir: joinHome(home, ".copilot", "otel"), match: "*.jsonl"}}
			if p := strings.TrimSpace(os.Getenv("COPILOT_OTEL_FILE_EXPORTER_PATH")); p != "" {
				if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
					clean := filepath.Clean(p)
					out = append(out, rootSpec{
						dir:   filepath.Dir(clean),
						match: filepath.Base(clean),
						filter: func(cand string) bool {
							return filepath.Clean(cand) == clean
						},
					})
				} else {
					out = append(out, rootSpec{dir: p, match: "*.jsonl"})
				}
			}
			return out
		},
		parseLine: func(path string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseCopilotOTELLine(path, line, pool)
		},
	},
	{
		tool: ToolKiro,
		name: "Kiro",
		detect: func(home string) []string {
			out := []string{
				joinHome(home, ".kiro", "settings"),
				joinHome(home, ".kiro", "agents"),
				joinHome(home, ".kiro", "sessions"),
				filepath.Join(appSupportDir(home), "Kiro", "User"),
			}
			switch runtime.GOOS {
			case "darwin":
				out = append(out, "/Applications/Kiro.app", joinHome(home, "Applications", "Kiro.app"))
			case "windows":
				out = append(out,
					filepath.Join(winLocalAppData(home), "Programs", "Kiro", "Kiro.exe"),
					filepath.Join(winAppData(home), "Kiro", "User"),
				)
			}
			return out
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{{dir: joinHome(home, ".kiro", "sessions", "cli"), match: "*.jsonl"}}
		},
		parseLine: func(path string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseFlexJSONLLine("kiro", path, line, pool)
		},
	},
	{
		tool: ToolCline,
		name: "Cline",
		detect: func(home string) []string {
			out := []string{joinHome(home, ".cline", "data")}
			for _, d := range vscodeGlobalStorageDirs(home, "saoudrizwan.claude-dev") {
				out = append(out, filepath.Dir(d))
			}
			return out
		},
		roots: func(home string) []rootSpec {
			var out []rootSpec
			for _, d := range vscodeGlobalStorageDirs(home, "saoudrizwan.claude-dev") {
				out = append(out, rootSpec{dir: d, match: "ui_messages.json", whole: true})
			}
			out = append(out, rootSpec{dir: joinHome(home, ".cline", "data", "sessions"), match: "*.jsonl"})
			return out
		},
		parseWhole: func(path string, data []byte, pool map[string]*string) ([]rec, error) {
			return parseClineUIMessagesFile("cline", path, data, pool)
		},
		parseLine: func(path string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseFlexJSONLLine("cline", path, line, pool)
		},
	},
	{
		tool: ToolRoo,
		name: "Roo Code",
		detect: func(home string) []string {
			var out []string
			for _, d := range vscodeGlobalStorageDirs(home, "rooveterinaryinc.roo-cline") {
				out = append(out, filepath.Dir(d))
			}
			return out
		},
		roots: func(home string) []rootSpec {
			var out []rootSpec
			for _, d := range vscodeGlobalStorageDirs(home, "rooveterinaryinc.roo-cline") {
				out = append(out, rootSpec{dir: d, match: "ui_messages.json", whole: true})
			}
			return out
		},
		parseWhole: func(path string, data []byte, pool map[string]*string) ([]rec, error) {
			return parseClineUIMessagesFile("roo", path, data, pool)
		},
	},
	{
		tool: ToolKilo,
		name: "Kilo",
		detect: func(home string) []string {
			var out []string
			for _, d := range vscodeGlobalStorageDirs(home, "kilocode.kilo-code") {
				out = append(out, filepath.Dir(d))
			}
			return out
		},
		roots: func(home string) []rootSpec {
			var out []rootSpec
			for _, d := range vscodeGlobalStorageDirs(home, "kilocode.kilo-code") {
				out = append(out, rootSpec{dir: d, match: "ui_messages.json", whole: true})
			}
			return out
		},
		parseWhole: func(path string, data []byte, pool map[string]*string) ([]rec, error) {
			return parseClineUIMessagesFile("kilo", path, data, pool)
		},
	},
	{
		tool: ToolOpenClaw,
		name: "OpenClaw",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".openclaw", "openclaw.json"),
				joinHome(home, ".openclaw", "agents"),
				joinHome(home, ".openclaw", "credentials"),
				joinHome(home, ".openclaw", "identity"),
			}
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{{
				dir:   joinHome(home, ".openclaw", "agents"),
				match: "*.jsonl",
				filter: func(p string) bool {
					return filepath.Base(filepath.Dir(p)) == "sessions"
				},
			}}
		},
		parseLine: func(_ string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseOmpLine(line, pool)
		},
	},
	{
		tool: ToolKimi,
		name: "Kimi",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".kimi", "config.toml"),
				joinHome(home, ".kimi", "kimi.json"),
				joinHome(home, ".kimi", "sessions"),
				joinHome(home, ".kimi", "credentials"),
				joinHome(home, ".kimi-code", "config.toml"),
				joinHome(home, ".kimi-code", "sessions"),
				joinHome(home, ".kimi-code", "auth.json"),
			}
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{
				{dir: joinHome(home, ".kimi", "sessions"), match: "*.jsonl"},
				{dir: joinHome(home, ".kimi-code", "sessions"), match: "*.jsonl"},
			}
		},
		parseLine: func(path string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseFlexJSONLLine("kimi", path, line, pool)
		},
	},
	{
		tool: ToolJunie,
		name: "Junie",
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".junie", "sessions"),
				joinHome(home, ".junie", "config.json"),
				joinHome(home, ".junie", "auth.json"),
			}
		},
		roots: func(home string) []rootSpec {
			return []rootSpec{{dir: joinHome(home, ".junie", "sessions"), match: "events.jsonl"}}
		},
		parseLine: func(path string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseFlexJSONLLine("junie", path, line, pool)
		},
	},
	{
		tool: ToolCherryStudio,
		name: "Cherry Studio",
		detect: func(home string) []string {
			base := filepath.Join(appSupportDir(home), "CherryStudio")
			out := []string{
				filepath.Join(base, "Data"),
				filepath.Join(base, "config.json"),
				filepath.Join(base, "Preferences"),
			}
			switch runtime.GOOS {
			case "darwin":
				out = append(out, "/Applications/Cherry Studio.app", joinHome(home, "Applications", "Cherry Studio.app"))
			case "windows":
				out = append(out, filepath.Join(winLocalAppData(home), "Programs", "Cherry Studio", "Cherry Studio.exe"))
			}
			return out
		},
		roots: func(home string) []rootSpec {
			base := appSupportDir(home)
			return []rootSpec{
				{dir: filepath.Join(base, "CherryStudio", "Data", "Agents", ".claude", "projects"), match: "*.jsonl"},
				{dir: filepath.Join(base, "CherryStudio", ".claude", "projects"), match: "*.jsonl"},
			}
		},
		parseLine: func(_ string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseClaudeLine(line, pool)
		},
	},
	{
		tool: ToolLMStudio,
		name: "LM Studio",
		detect: func(home string) []string {
			base := envOrHome("LM_STUDIO_HOME", home, ".lmstudio")
			return []string{
				filepath.Join(base, "settings.json"),
				filepath.Join(base, "server-logs"),
				filepath.Join(base, ".internal"),
			}
		},
		roots: func(home string) []rootSpec {
			base := envOrHome("LM_STUDIO_HOME", home, ".lmstudio")
			return []rootSpec{{dir: filepath.Join(base, "server-logs"), match: "*.log"}}
		},
		parseLine: func(_ string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseLMStudioLine(line, pool)
		},
	},
	// --- Installed-but-unreadable (API / RPC / sync-only) tools ---
	{
		tool:       ToolCursor,
		name:       "Cursor",
		unreadable: true,
		detect: func(home string) []string {
			out := []string{
				joinHome(home, ".cursor", "cli-config.json"),
				joinHome(home, ".cursor", "ide_state.json"),
				joinHome(home, ".cursor", "chats"),
				joinHome(home, ".local", "share", "cursor-agent"),
			}
			switch runtime.GOOS {
			case "darwin":
				out = append(out, "/Applications/Cursor.app", joinHome(home, "Applications", "Cursor.app"))
			case "windows":
				out = append(out,
					filepath.Join(winLocalAppData(home), "Programs", "cursor", "Cursor.exe"),
					filepath.Join(winAppData(home), "Cursor", "User"),
				)
			}
			return out
		},
	},
	{
		tool:       ToolTrae,
		name:       "Trae",
		unreadable: true,
		detect: func(home string) []string {
			out := []string{
				joinHome(home, ".trae", "User"),
				filepath.Join(appSupportDir(home), "Trae", "User"),
			}
			switch runtime.GOOS {
			case "darwin":
				out = append(out, "/Applications/Trae.app", joinHome(home, "Applications", "Trae.app"))
			case "windows":
				out = append(out, filepath.Join(winLocalAppData(home), "Programs", "Trae", "Trae.exe"))
			}
			return out
		},
	},
	{
		tool:       ToolWarp,
		name:       "Warp",
		unreadable: true,
		detect: func(home string) []string {
			out := []string{
				joinHome(home, ".warp", "launch_configurations"),
				joinHome(home, ".warp", "keybindings.yaml"),
			}
			switch runtime.GOOS {
			case "darwin":
				groupAppSupport := joinHome(home, "Library", "Group Containers", "2BBY89MBSN.dev.warp", "Library", "Application Support")
				out = append(out,
					filepath.Join(groupAppSupport, "dev.warp.Warp-Stable", "warp.sqlite"),
					filepath.Join(groupAppSupport, "dev.warp.Warp-Preview", "warp.sqlite"),
					joinHome(home, "Library", "Application Support", "dev.warp.Warp-Stable", "warp.sqlite"),
					"/Applications/Warp.app",
					joinHome(home, "Applications", "Warp.app"),
				)
			case "windows":
				out = append(out,
					filepath.Join(winLocalAppData(home), "warp", "Warp", "data"),
					filepath.Join(winLocalAppData(home), "Programs", "Warp", "warp.exe"),
				)
			}
			return out
		},
	},
	{
		tool:       ToolAmp,
		name:       "Amp",
		unreadable: true,
		detect: func(home string) []string {
			return []string{
				filepath.Join(xdgDataHome(home), "amp", "secrets.json"),
				filepath.Join(xdgDataHome(home), "amp", "threads"),
				filepath.Join(xdgConfigHome(home), "amp", "settings.json"),
			}
		},
	},
	{
		tool:       ToolCapy,
		name:       "Capy",
		unreadable: true,
		detect: func(home string) []string {
			return []string{
				joinHome(home, ".capy", "config.json"),
				joinHome(home, ".capy", "auth.json"),
				joinHome(home, ".capy", "sessions"),
			}
		},
	},
	{
		tool:       ToolAntigravityIDE,
		name:       "Antigravity IDE",
		unreadable: true,
		detect: func(home string) []string {
			return []string{joinHome(home, ".gemini", "antigravity", "conversations")}
		},
	},
}

var registryByTool = func() map[Tool]toolEntry {
	m := make(map[Tool]toolEntry, len(registry))
	for _, e := range registry {
		m[e.tool] = e
	}
	return m
}()

// AllTools returns every registered AI tool in display order.
func AllTools() []Tool {
	out := make([]Tool, len(registry))
	for i, e := range registry {
		out[i] = e.tool
	}
	return out
}

// Name returns the tool's human-readable product name.
func (t Tool) Name() string {
	if e, ok := registryByTool[t]; ok {
		return e.name
	}
	return string(t)
}

func (e toolEntry) isInstalled(home string) bool {
	if e.detect == nil || home == "" {
		return false
	}
	for _, p := range e.detect(home) {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
