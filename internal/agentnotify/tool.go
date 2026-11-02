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
	ToolClaude   Tool = "claude"
	ToolCodex    Tool = "codex"
	ToolOpencode Tool = "opencode"
	ToolOmp      Tool = "omp"
	ToolPi       Tool = "pi"
	ToolGemini   Tool = "gemini"
	ToolGrok     Tool = "grok"
	ToolDevin    Tool = "devin"
)

// Kind says how a tool takes Rakko's entry.
type Kind int

const (
	// KindHooks is a JSON configuration with a hooks object (Claude, Codex,
	// Gemini, Grok, Devin).
	KindHooks Kind = iota
	// KindModule is a JavaScript/TypeScript module in a plugin directory
	// (opencode, omp, pi).
	KindModule
)

// spec is one tool's installation surface.
type spec struct {
	tool Tool
	name string
	kind Kind
	// detect lists the paths whose presence says the tool is installed for
	// this user: any one of them is enough.
	detect func(home string) []string
	// config is the file Rakko writes: the hooks document, or the module.
	config func(home string) string
	// events are the hook events registered in a KindHooks document, and
	// also the event names the module reports (in the module itself).
	events []string
	// timeout is the number written into the hook entry in that tool's
	// unit: seconds for Claude Code, Codex and Grok, milliseconds for
	// Gemini CLI. Zero means hookTimeout (seconds).
	timeout int
	// module is the KindModule source, keyed by tool.
	module string
}

func join(home string, parts ...string) string {
	return filepath.Join(append([]string{home}, parts...)...)
}

// specs lists every supported tool. Order is the order the settings rows
// show, most used first.
var specs = []spec{
	{
		tool:   ToolClaude,
		name:   "Claude Code",
		kind:   KindHooks,
		detect: func(home string) []string { return []string{join(home, ".claude")} },
		config: func(home string) string { return join(home, ".claude", "settings.json") },
		events: []string{"UserPromptSubmit", "Stop"},
	},
	{
		tool:   ToolCodex,
		name:   "Codex",
		kind:   KindHooks,
		detect: func(home string) []string { return []string{join(home, ".codex")} },
		config: func(home string) string { return join(home, ".codex", "hooks.json") },
		events: []string{"UserPromptSubmit", "Stop"},
	},
	{
		tool:   ToolOpencode,
		name:   "opencode",
		kind:   KindModule,
		detect: func(home string) []string { return []string{join(home, ".config", "opencode")} },
		config: func(home string) string {
			return join(home, ".config", "opencode", "plugins", "rakko-agent-completion.js")
		},
		module: opencodeModule,
	},
	{
		tool:   ToolOmp,
		name:   "omp",
		kind:   KindModule,
		detect: func(home string) []string { return []string{join(home, ".omp", "agent")} },
		config: func(home string) string {
			return join(home, ".omp", "agent", "hooks", "pre", "rakko-agent-completion.ts")
		},
		module: ompModule,
	},
	{
		tool:   ToolPi,
		name:   "pi",
		kind:   KindModule,
		detect: func(home string) []string { return []string{join(home, ".pi", "agent")} },
		config: func(home string) string {
			return join(home, ".pi", "agent", "extensions", "rakko-agent-completion.ts")
		},
		module: extensionModule,
	},
	{
		tool: ToolGemini,
		name: "Gemini CLI",
		kind: KindHooks,
		// ~/.gemini alone is not Gemini CLI: Antigravity and other Google
		// tools keep their own files there too. These are what Gemini CLI
		// itself writes on first run and sign-in.
		detect: func(home string) []string {
			return []string{
				join(home, ".gemini", "installation_id"),
				join(home, ".gemini", "google_accounts.json"),
				join(home, ".gemini", "oauth_creds.json"),
				join(home, ".gemini", "tmp"),
			}
		},
		config: func(home string) string { return join(home, ".gemini", "settings.json") },
		events: []string{"BeforeAgent", "AfterAgent"},
		// Gemini CLI reads timeout in milliseconds.
		timeout: 5000,
	},
	{
		tool:   ToolGrok,
		name:   "Grok",
		kind:   KindHooks,
		detect: func(home string) []string { return []string{join(home, ".grok")} },
		config: func(home string) string { return join(home, ".grok", "hooks", "rakko.json") },
		events: []string{"UserPromptSubmit", "Stop"},
	},
	{
		// Devin CLI: Claude Code's hook format under the "hooks" key of its
		// user config (docs.devin.ai/cli/extensibility/hooks).
		tool:   ToolDevin,
		name:   "Devin",
		kind:   KindHooks,
		detect: func(home string) []string { return []string{join(home, ".config", "devin")} },
		config: func(home string) string { return join(home, ".config", "devin", "config.json") },
		events: []string{"UserPromptSubmit", "Stop"},
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
	case "Stop", "StopFailure", "AfterAgent", "session.idle", "agent_end", "agent_settled", "session_stop":
		return true
	}
	return false
}

// PromptEvent reports whether a hook event carries the user's prompt.
func PromptEvent(event string) bool {
	switch event {
	case "UserPromptSubmit", "BeforeAgent", "before_agent_start":
		return true
	}
	return false
}
