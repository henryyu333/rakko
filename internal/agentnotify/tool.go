// Package agentnotify tells the notch when an agent finishes a turn.
//
// One path only: every supported tool's hook or plugin hands Rakko the
// tool's own event (through the app binary's --agent-hook mode), and Rakko
// drops a completion onto the notch. Nothing here approves, answers or
// reads a conversation; the hook side only forwards the fields needed to
// name the session and its task.
//
// The hook entries Rakko writes into a tool's configuration are its own:
// they carry a marker, they are removed whole when the switch goes off, and
// a file that cannot be parsed is never rewritten.
package agentnotify

import (
	"os"
	"path/filepath"
	"strings"
)

// Tool names one agent harness Rakko can announce.
type Tool string

const (
	ToolClaude      Tool = "claude"
	ToolCodex       Tool = "codex"
	ToolCursor      Tool = "cursor"
	ToolCopilot     Tool = "copilot"
	ToolDevin       Tool = "devin"
	ToolGemini      Tool = "gemini"
	ToolAntigravity Tool = "antigravity"
	ToolOpencode    Tool = "opencode"
	ToolOmp         Tool = "omp"
	ToolPi          Tool = "pi"
	ToolGrok        Tool = "grok"
	ToolQwen        Tool = "qwen"
	ToolFactory     Tool = "droid"
	ToolKiro        Tool = "kiro"
	ToolAugment     Tool = "auggie"
	ToolHermes      Tool = "hermes"
)

// Kind says how a tool takes Rakko's entry.
type Kind int

const (
	// KindHooks is a shared JSON configuration with a "hooks" object keyed
	// by event (Claude Code's layout, and Cursor's flat variant).
	KindHooks Kind = iota
	// KindModule is a file of Rakko's own in the tool's plugin or hooks
	// directory: a JavaScript/TypeScript module (opencode, omp, pi) or a
	// JSON hook file (Copilot CLI, Kiro).
	KindModule
	// KindNamed is a shared JSON document keyed by hook name, where Rakko
	// owns one member (Antigravity's hooks.json).
	KindNamed
	// KindHermes is Hermes's Python plugin directory plus the few lines in
	// its YAML config that enable the plugin and select its approval
	// transport. Both are Rakko's, and both go away with the switch.
	KindHermes
)

// spec is one tool's installation surface.
type spec struct {
	tool Tool
	name string
	kind Kind
	// detect lists paths the tool itself creates when it is installed or
	// first run; any one of them is enough. A bare configuration directory
	// is not: skill installers create ~/.<tool>/skills for tools that were
	// never installed.
	detect func(home string) []string
	// config is the file Rakko writes: the hooks document, or the module.
	config func(home string) string
	// events are the hook events registered in a KindHooks document.
	events []string
	// timeout is the number written into a hook entry in that tool's unit
	// (seconds unless the tool reads milliseconds). Zero means hookTimeout.
	timeout int
	// flat writes each entry as {"command", "timeout"} straight in the
	// event's list (Cursor) instead of Claude Code's {"hooks": [...]}.
	flat bool
	// version is the "version" a new document of this tool starts with.
	version string
	// eventArg passes the event name on the command line, for tools whose
	// payload does not name it.
	eventArg bool
	// module is the KindModule source.
	module string
	// bareName is the file name of a hooks document that is keyed by event
	// at its root, with no "hooks" wrapper (Factory's hooks.json). bare is
	// set for the file at hand by at().
	bareName string
	bare     bool
	// also lists other files an earlier switch-on may have written to;
	// switching off cleans them too.
	also func(home string) []string
	// approve describes the tool's "approve for the user" channel. nil
	// means the tool only announces completions.
	approve *approveSpec
}

// approveSpec is one tool's approval surface: the event its hook entry
// registers, the timeout written into that entry, and how the entry is
// installed. The timeout is the outer wall of the three-level budget
// (45 s card, 55 s client, 60 s hook).
type approveSpec struct {
	event   string
	timeout int
	// fallback is emitted by the installed shell command when the app
	// binary is gone. Empty means "print nothing" (the tool's own flow).
	fallback string
	// config overrides the file an approve entry lives in. Empty uses the
	// completion file (KindHooks / KindNamed).
	config func(home string) string
	// member is the named member for KindNamed tools.
	member string
	// module is the source file for module tools (Copilot's JSON hook file,
	// opencode's plugin, pi's extension).
	module string
}

// at resolves the file Rakko writes now, and whether it is bare.
func (s spec) at(home string) (spec, string) {
	path := s.config(home)
	s.bare = s.bareName != "" && filepath.Base(path) == s.bareName
	return s, path
}

// candidates is every file switching off must clean.
func (s spec) candidates(home string) []string {
	out := []string{s.config(home)}
	if s.also != nil {
		for _, p := range s.also(home) {
			if p != out[0] {
				out = append(out, p)
			}
		}
	}
	return out
}

func join(home string, parts ...string) string {
	return filepath.Join(append([]string{home}, parts...)...)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// paths joins home-relative markers. Every marker is inside the user's home,
// so the answer is about this user, not about what else the Mac has.
func paths(home string, rel ...string) []string {
	out := make([]string, len(rel))
	for i, r := range rel {
		out[i] = join(home, r)
	}
	return out
}

// codexHome is the directory Codex keeps its sessions and configuration
// in: $CODEX_HOME when the environment names one (a leading "~/" expands
// against home), else the default ~/.codex.
func codexHome(home string) string {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		return join(home, ".codex")
	}
	if dir == "~" {
		return home
	}
	if strings.HasPrefix(dir, "~/") {
		return join(home, dir[2:])
	}
	return dir
}

// specs lists every supported tool. Order is the order the settings rows
// show, most used first. Event names, files and units follow each tool's
// own hook documentation as of 2026-11.
var specs = []spec{
	{
		tool: ToolClaude,
		name: "Claude Code",
		kind: KindHooks,
		detect: func(home string) []string {
			return paths(home, ".claude/projects", ".claude/history.jsonl", ".claude.json")
		},
		config:  func(home string) string { return join(home, ".claude", "settings.json") },
		events:  []string{"UserPromptSubmit", "Stop"},
		approve: &approveSpec{event: "PermissionRequest", timeout: approveTimeout},
	},
	{
		tool: ToolCodex,
		name: "Codex",
		kind: KindHooks,
		detect: func(home string) []string {
			return paths(codexHome(home), "sessions", "auth.json", "config.toml", ".codex-global-state.json")
		},
		config:  func(home string) string { return join(codexHome(home), "hooks.json") },
		events:  []string{"UserPromptSubmit", "Stop"},
		approve: &approveSpec{event: "PermissionRequest", timeout: approveTimeout},
	},
	{
		// Cursor (the editor and cursor-agent): ~/.cursor/hooks.json,
		// lower-camel events, flat entries, "version": 1.
		tool: ToolCursor,
		name: "Cursor",
		kind: KindHooks,
		detect: func(home string) []string {
			return paths(home, ".cursor/cli-config.json", ".cursor/ide_state.json", ".cursor/chats",
				".local/share/cursor-agent", "Library/Application Support/Cursor")
		},
		config:  func(home string) string { return join(home, ".cursor", "hooks.json") },
		events:  []string{"beforeSubmitPrompt", "stop"},
		flat:    true,
		version: "1",
	},
	{
		// GitHub Copilot CLI: a hook file of Rakko's own in ~/.copilot/hooks;
		// its payload carries no event name.
		tool: ToolCopilot,
		name: "Copilot CLI",
		kind: KindModule,
		detect: func(home string) []string {
			return paths(home, ".copilot/config.json", ".copilot/session-state", ".copilot/logs")
		},
		config: func(home string) string { return join(home, ".copilot", "hooks", "rakko-agent-completion.json") },
		module: copilotHooks,
		approve: &approveSpec{
			event: "permissionRequest", timeout: approveTimeout,
			config: func(home string) string {
				return join(home, ".copilot", "hooks", "rakko-agent-approval.json")
			},
			module: copilotApproveHooks,
		},
	},
	{
		// Devin CLI: Claude Code's hook format under the "hooks" key of its
		// user config (docs.devin.ai/cli/extensibility/hooks).
		tool: ToolDevin,
		name: "Devin",
		kind: KindHooks,
		detect: func(home string) []string {
			return paths(home, ".config/devin/config.json", ".config/devin/cli")
		},
		config:  func(home string) string { return join(home, ".config", "devin", "config.json") },
		events:  []string{"UserPromptSubmit", "Stop"},
		approve: &approveSpec{event: "PermissionRequest", timeout: approveTimeout},
	},
	{
		tool: ToolGemini,
		name: "Gemini CLI",
		kind: KindHooks,
		// ~/.gemini alone is not Gemini CLI: Antigravity and other Google
		// tools keep their own files there too. These are what Gemini CLI
		// itself writes on first run and sign-in.
		detect: func(home string) []string {
			return paths(home, ".gemini/installation_id", ".gemini/google_accounts.json",
				".gemini/oauth_creds.json", ".gemini/tmp")
		},
		config: func(home string) string { return join(home, ".gemini", "settings.json") },
		events: []string{"BeforeAgent", "AfterAgent"},
		// Gemini CLI reads timeout in milliseconds.
		timeout: 5000,
	},
	{
		// Antigravity CLI (agy): ~/.gemini/config/hooks.json, keyed by hook
		// name. Only Stop exists, and its payload names no event.
		tool:     ToolAntigravity,
		name:     "Antigravity",
		kind:     KindNamed,
		detect:   func(home string) []string { return paths(home, ".gemini/antigravity-cli") },
		config:   func(home string) string { return join(home, ".gemini", "config", "hooks.json") },
		events:   []string{"Stop"},
		eventArg: true,
		approve: &approveSpec{
			event: "PreToolUse", timeout: approveTimeout, fallback: `{"decision":"allow"}`,
			member: "rakko-agent-approval",
		},
	},
	{
		tool: ToolOpencode,
		name: "opencode",
		kind: KindModule,
		detect: func(home string) []string {
			return paths(home, ".local/share/opencode", ".config/opencode/opencode.json", ".config/opencode/opencode.jsonc")
		},
		config: func(home string) string {
			return join(home, ".config", "opencode", "plugins", "rakko-agent-completion.js")
		},
		module: opencodeModule,
		approve: &approveSpec{
			event: "evaluate", timeout: approveTimeout,
			config: func(home string) string {
				return join(home, ".config", "opencode", "plugins", "rakko-agent-approval.js")
			},
			module: opencodeApproveModule,
		},
	},
	{
		tool: ToolOmp,
		name: "omp",
		kind: KindModule,
		detect: func(home string) []string {
			return paths(home, ".omp/agent/agent.db", ".omp/agent/config.yml", ".omp/agent/sessions")
		},
		config: func(home string) string {
			return join(home, ".omp", "agent", "hooks", "pre", "rakko-agent-completion.ts")
		},
		module: ompModule,
	},
	{
		tool: ToolPi,
		name: "pi",
		kind: KindModule,
		detect: func(home string) []string {
			return paths(home, ".pi/agent/auth.json", ".pi/agent/sessions", ".pi/agent/settings.json")
		},
		config: func(home string) string {
			return join(home, ".pi", "agent", "extensions", "rakko-agent-completion.ts")
		},
		module: extensionModule,
		approve: &approveSpec{
			event: "tool_call", timeout: approveTimeout,
			config: func(home string) string {
				return join(home, ".pi", "agent", "extensions", "rakko-agent-approval.ts")
			},
			module: piApproveModule,
		},
	},
	{
		tool: ToolGrok,
		name: "Grok",
		kind: KindHooks,
		detect: func(home string) []string {
			return paths(home, ".grok/auth.json", ".grok/bin", ".grok/active_sessions.json")
		},
		config: func(home string) string { return join(home, ".grok", "hooks", "rakko.json") },
		events: []string{"UserPromptSubmit", "Stop"},
	},
	{
		// Qwen Code: Claude Code's layout in ~/.qwen/settings.json.
		tool: ToolQwen,
		name: "Qwen Code",
		kind: KindHooks,
		detect: func(home string) []string {
			return paths(home, ".qwen/settings.json", ".qwen/oauth_creds.json", ".qwen/installation_id", ".qwen/tmp", ".qwen/projects")
		},
		config:  func(home string) string { return join(home, ".qwen", "settings.json") },
		events:  []string{"UserPromptSubmit", "Stop"},
		approve: &approveSpec{event: "PermissionRequest", timeout: approveTimeout},
	},
	{
		// Factory Droid reads ~/.factory/hooks.json (keyed by event at its
		// root) and only falls back to the "hooks" key of settings.json when
		// that file is absent. Rakko writes where Droid reads, and never
		// creates hooks.json: that would hide the user's settings.json
		// hooks. Installers create settings.json for tools never run, so
		// detection looks for Droid's own sessions or binary.
		tool: ToolFactory,
		name: "Droid",
		kind: KindHooks,
		detect: func(home string) []string {
			return paths(home, ".factory/projects", ".factory/sessions", ".local/bin/droid")
		},
		config: func(home string) string {
			if hooks := join(home, ".factory", "hooks.json"); exists(hooks) {
				return hooks
			}
			return join(home, ".factory", "settings.json")
		},
		bareName: "hooks.json",
		also: func(home string) []string {
			return paths(home, ".factory/hooks.json", ".factory/settings.json")
		},
		events:  []string{"UserPromptSubmit", "Stop"},
		approve: &approveSpec{event: "PreToolUse", timeout: approveTimeout},
	},
	{
		// Kiro: a v1 hook file of Rakko's own in ~/.kiro/hooks.
		tool: ToolKiro,
		name: "Kiro",
		kind: KindModule,
		detect: func(home string) []string {
			return paths(home, ".kiro/settings", ".kiro/agents", "Library/Application Support/Kiro")
		},
		config: func(home string) string { return join(home, ".kiro", "hooks", "rakko-agent-completion.json") },
		module: kiroHooks,
	},
	{
		// Augment's auggie: Claude Code's layout in ~/.augment/settings.json,
		// timeouts in milliseconds.
		tool: ToolAugment,
		name: "Auggie",
		kind: KindHooks,
		detect: func(home string) []string {
			return paths(home, ".augment/settings.json", ".augment/sessions", ".augment/session.json")
		},
		config:  func(home string) string { return join(home, ".augment", "settings.json") },
		events:  []string{"PromptSubmit", "Stop"},
		timeout: 5000,
	},
	{
		// Hermes Agent: a Python plugin of Rakko's own in ~/.hermes/plugins,
		// enabled from the profile's config.yaml. Its approval transport is
		// selected in the same file, so a tool-triggered prompt becomes a
		// Rakko card only when Hermes itself was going to ask.
		tool: ToolHermes,
		name: "Hermes",
		kind: KindHermes,
		detect: func(home string) []string {
			return []string{hermesConfigPath(home), join(hermesHome(home), "hermes-agent")}
		},
		config: func(home string) string { return hermesConfigPath(home) },
		approve: &approveSpec{
			event: "pre_approval_request", timeout: approveTimeout,
		},
	},
}

// specFor returns the tool's surface.
func specFor(tool Tool) (spec, bool) {
	for _, s := range specs {
		if s.tool == tool {
			return s, true
		}
	}
	return spec{}, false
}

// Approvable reports whether the tool has a channel Rakko can answer for
// the user. Tools without one only announce completions.
func (t Tool) Approvable() bool {
	s, ok := specFor(t)
	return ok && s.approve != nil
}

// ApproveEvent is the hook event the approval entry registers.
func (t Tool) ApproveEvent() string {
	if s, ok := specFor(t); ok && s.approve != nil {
		return s.approve.event
	}
	return ""
}

// ApproveConfigPath is the file the approval entry lives in.
func ApproveConfigPath(home string, t Tool) string {
	s, ok := specFor(t)
	if !ok || s.approve == nil {
		return ""
	}
	if s.approve.config != nil {
		return s.approve.config(home)
	}
	return s.config(home)
}

// ApproveDisplayPath is ApproveConfigPath with home written as "~".
func ApproveDisplayPath(home string, t Tool) string {
	p := ApproveConfigPath(home, t)
	if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// Tools lists the tools Rakko can announce, in settings order.
func Tools() []Tool {
	out := make([]Tool, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.tool)
	}
	return out
}

// Name is the tool's product name, as its own window would write it.
func (t Tool) Name() string {
	if s, ok := specFor(t); ok {
		return s.name
	}
	return string(t)
}

// Valid reports whether t is a supported tool.
func (t Tool) Valid() bool {
	_, ok := specFor(t)
	return ok
}

// Installed reports whether the tool looks installed for this user's home:
// one of its own paths exists there. Only presence is looked at, never the
// files' contents, so the answer is per machine and per user.
func Installed(home string, tool Tool) bool {
	s, ok := specFor(tool)
	if !ok || home == "" {
		return false
	}
	for _, p := range s.detect(home) {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// ConfigPath is the file Rakko writes for a tool.
func ConfigPath(home string, tool Tool) string {
	s, ok := specFor(tool)
	if !ok {
		return ""
	}
	return s.config(home)
}

// DisplayPath is ConfigPath with the home directory written as "~": the
// settings row shows "~/.claude/settings.json" rather than the user's name.
func DisplayPath(home string, tool Tool) string {
	p := ConfigPath(home, tool)
	if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// DoneEvent reports whether a hook event means "this turn is finished".
func DoneEvent(event string) bool {
	switch event {
	case "Stop", "StopFailure", "AfterAgent", "session.idle", "agent_end", "agent_settled", "session_stop",
		"stop", "agentStop", "post_llm_call":
		return true
	}
	return false
}

// PromptEvent reports whether a hook event carries the user's prompt.
func PromptEvent(event string) bool {
	switch event {
	case "UserPromptSubmit", "BeforeAgent", "before_agent_start",
		"beforeSubmitPrompt", "userPromptSubmitted", "PromptSubmit":
		return true
	}
	return false
}
