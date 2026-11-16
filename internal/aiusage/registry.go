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

// dbSpec is one SQLite database file and its reader.
type dbSpec struct {
	path string
	read func(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) ([]rec, error)
}

// toolEntry is one entry in the AI tool registry: display name, installation
// probe (for unreadable tools), local data locations, and parser.
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
		roots: func(home string) []rootSpec {
			base := envOrHome("CODEX_HOME", home, ".codex")
			return []rootSpec{
				{dir: filepath.Join(base, "sessions"), match: "rollout-*.jsonl"},
				{dir: filepath.Join(base, "archived_sessions"), match: "*.jsonl"},
			}
		},
		parseLine: func(_ string, line []byte, pool map[string]*string, st *fileState) (rec, string, bool) {
			r, ok := st.tail.parseCodexLine(line, pool)
			return r, "", ok
		},
	},
	{
		tool: ToolPi,
		name: "Pi",
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
		dbs: func(home string) []dbSpec {
			return []dbSpec{{path: opencodeDBPath(home), read: readOpencode}}
		},
	},
	{
		tool: ToolGrok,
		name: "Grok",
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
		dbs: func(home string) []dbSpec {
			base := envOrHome("HERMES_HOME", home, ".hermes")
			out := []dbSpec{{path: filepath.Join(base, "state.db"), read: readHermesDB}}
			for _, p := range allGlobs(filepath.Join(base, "profiles", "*", "state.db")) {
				out = append(out, dbSpec{path: p, read: readHermesDB})
			}
			return out
		},
	},
	{
		tool: ToolDevin,
		name: "Devin",
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
		roots: func(home string) []rootSpec {
			return []rootSpec{{dir: joinHome(home, ".augment", "sessions"), match: "*.json", whole: true}}
		},
		parseWhole: parseAuggieSessionFile,
	},
	{
		tool: ToolCopilot,
		name: "Copilot CLI",
		roots: func(home string) []rootSpec {
			out := []rootSpec{{dir: joinHome(home, ".copilot", "otel"), match: "*.jsonl"}}
			if p := strings.TrimSpace(os.Getenv("COPILOT_OTEL_FILE_EXPORTER_PATH")); p != "" {
				out = append(out, rootSpec{dir: p, match: "*.jsonl"})
			}
			return out
		},
		parseLine: func(path string, line []byte, pool map[string]*string, _ *fileState) (rec, string, bool) {
			return parseFlexJSONLLine("copilot", path, line, pool)
		},
	},
	{
		tool: ToolKiro,
		name: "Kiro",
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
					filepath.Join(winAppData(home), "Cursor"),
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
				joinHome(home, ".trae"),
				filepath.Join(appSupportDir(home), "Trae"),
			}
			if runtime.GOOS == "darwin" {
				out = append(out, "/Applications/Trae.app", joinHome(home, "Applications", "Trae.app"))
			}
			return out
		},
	},
	{
		tool:       ToolWarp,
		name:       "Warp",
		unreadable: true,
		detect: func(home string) []string {
			out := []string{joinHome(home, ".warp")}
			switch runtime.GOOS {
			case "darwin":
				out = append(out, joinHome(home, "Library", "Group Containers", "2BBY89MBSN.dev.warp"))
			case "windows":
				out = append(out, filepath.Join(winLocalAppData(home), "warp"))
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
				filepath.Join(xdgDataHome(home), "amp"),
				filepath.Join(xdgConfigHome(home), "amp"),
			}
		},
	},
	{
		tool:       ToolCapy,
		name:       "Capy",
		unreadable: true,
		detect: func(home string) []string {
			return []string{joinHome(home, ".capy")}
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
