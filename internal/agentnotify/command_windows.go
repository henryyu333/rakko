//go:build windows

package agentnotify

// How each tool runs Rakko's entry on Windows, and the entry that follows
// from it.
//
// Three shapes exist:
//
//   - an executable with an argument vector, run without a shell (Claude
//     Code's and Auggie's "args" field, Copilot CLI's "exec" field);
//   - a command line run through cmd.exe (Windows' default, which is also
//     what Node's child_process.exec uses: Rakko writes a batch file and
//     names it);
//   - a command line run through PowerShell (Codex, Cursor, Gemini CLI, and
//     Qwen Code when its ComSpec is PowerShell).
//
// Evidence for the mapping is recorded at toolShell. Tools whose
// documentation does not say which interpreter runs a hook are given
// cmd.exe, the system default, and are marked unverified in the report.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// windowsShell is the interpreter a tool uses for a hook command line.
type windowsShell int

const (
	shellCmd windowsShell = iota
	shellPowerShell
	// shellBash is the Git Bash / MSYS2 shell Qwen Code picks when it runs
	// inside one.
	shellBash
)

// toolShell is the interpreter that runs one tool's hook command on
// Windows.
func toolShell(tool Tool) windowsShell {
	switch tool {
	case ToolCodex:
		// Codex CLI 0.158–0.162 (Windows 11) runs command hooks through the
		// session shell, `powershell.exe -NoProfile -Command "<command>"`,
		// and commandWindows is used inside that same shell
		// (openai/codex#54072, #49716).
		return shellPowerShell
	case ToolCursor:
		// Cursor's documentation: on Windows the desktop app runs hook
		// commands through PowerShell.
		return shellPowerShell
	case ToolGemini:
		// gemini-cli packages/core/src/utils/shell-utils.ts
		// getShellConfiguration always returns PowerShell on Windows
		// (pwsh.exe when on PATH, else powershell.exe), and the hook runner
		// spawns it with -NoProfile -NonInteractive -Command.
		return shellPowerShell
	case ToolQwen:
		// Qwen Code decides at run time from MSYSTEM/TERM (Git Bash) and
		// ComSpec (PowerShell), defaulting to cmd.exe; Rakko mirrors that
		// decision from the same inputs.
		return qwenShell()
	}
	// Claude Code, Copilot CLI and Auggie never reach here: their entries
	// are argv. The rest run through cmd.exe, Windows' default command
	// interpreter (Dev, Factory Droid, Grok, Kiro, Antigravity).
	return shellCmd
}

// qwenShell mirrors Qwen Code's own getShellConfiguration for Windows.
func qwenShell() windowsShell {
	msystem := strings.ToUpper(os.Getenv("MSYSTEM"))
	term := strings.ToLower(os.Getenv("TERM"))
	if strings.HasPrefix(msystem, "MINGW") || strings.HasPrefix(msystem, "MSYS") ||
		strings.Contains(term, "msys") || strings.Contains(term, "cygwin") {
		return shellBash
	}
	comspec := strings.ToLower(os.Getenv("ComSpec"))
	if strings.HasSuffix(comspec, "powershell.exe") || strings.HasSuffix(comspec, "pwsh.exe") {
		return shellPowerShell
	}
	return shellCmd
}

// windowsArgvField is the JSON member an exec-form entry uses for the
// program; empty when the tool runs a command line instead.
func windowsArgvField(tool Tool) string {
	switch tool {
	case ToolClaude, ToolAugment:
		// Claude Code's hooks reference: "command" with "args" runs the
		// program directly, without a shell. Auggie's: setting "args" runs
		// "command" directly, without a shell.
		return "command"
	case ToolCopilot:
		// Copilot CLI's hooks reference: "exec" with "args" runs the
		// executable directly; must not be combined with bash, powershell
		// or command.
		return "exec"
	}
	return ""
}

// windowsShellTool reports whether the tool's entry is a command line on
// Windows, which is what needs a shim. Module tools whose module spawns the
// app itself (opencode, omp, pi, dsh, hermes) are argv and need none.
func windowsShellTool(tool Tool) bool {
	if windowsArgvField(tool) != "" {
		return false
	}
	switch tool {
	case ToolOpencode, ToolOmp, ToolPi, ToolHermes, ToolDsh:
		return false
	}
	return true
}

// hookEntryOS renders one completion entry for Windows.
func hookEntryOS(exe string, tool Tool, dataDir, event string) hookEntry {
	if field := windowsArgvField(tool); field != "" {
		return hookEntry{
			fields: []hookField{{field, escapedForTool(tool, exe)}},
			argv:   escapedArgs(tool, rakkoArgs(hookCompletion, tool, dataDir, event)),
		}
	}
	if toolShell(tool) == shellBash {
		return hookEntry{fields: []hookField{{
			"command", posixEntryLine(hookCompletion, tool, exe, dataDir, event),
		}}}
	}
	entry := hookEntry{fields: []hookField{{"command", shimInvocation(tool, hookCompletion, dataDir)}}}
	if tool == ToolCodex {
		// commandWindows is Codex's Windows-only override; it runs in the
		// same PowerShell as command, so both name the shim.
		entry.fields = append(entry.fields, hookField{"commandWindows", shimInvocation(tool, hookCompletion, dataDir)})
	}
	return entry
}

// approveEntryOS renders one approval entry for Windows.
func approveEntryOS(exe string, tool Tool, dataDir string) hookEntry {
	if field := windowsArgvField(tool); field != "" {
		return hookEntry{
			fields: []hookField{{field, escapedForTool(tool, exe)}},
			argv:   escapedArgs(tool, rakkoArgs(hookApproval, tool, dataDir, "")),
		}
	}
	if toolShell(tool) == shellBash {
		return hookEntry{fields: []hookField{{
			"command", posixEntryLine(hookApproval, tool, exe, dataDir, ""),
		}}}
	}
	return hookEntry{fields: []hookField{{"command", shimInvocation(tool, hookApproval, dataDir)}}}
}

// prepareHookShim writes the batch file a shell tool runs, and refuses a
// path the interpreter cannot quote safely. It runs before the tool's
// configuration is touched, so a refused write leaves the tool as it was.
func prepareHookShim(kind hookKind, s spec, dataDir, exe string) error {
	if !windowsShellTool(s.tool) {
		return nil
	}
	if dataDir == "" {
		return errors.New("agentnotify: " + string(s.tool) + " needs Rakko's data directory")
	}
	shell := toolShell(s.tool)
	values := append([]string{exe, dataDir}, rakkoArgs(kind, s.tool, "", "")...)
	if shell == shellBash {
		// The entry is a POSIX shell line of its own; no shim file is
		// involved, and single quotes keep every other byte literal.
		for _, v := range values {
			if strings.ContainsAny(v, "\r\n") {
				return fmt.Errorf("agentnotify: %q cannot be written into a shell line", v)
			}
		}
		return nil
	}
	// The shim's own body is a batch file, whatever interpreter runs it, so
	// cmd.exe's rules decide what can be embedded.
	for _, v := range values {
		if !quotableFor(shellCmd, v) {
			return fmt.Errorf("agentnotify: %q cannot be quoted safely for cmd.exe", v)
		}
	}
	event := ""
	if kind == hookCompletion {
		event = eventArgFor(s)
	}
	return writeShim(dataDir, kind, s.tool, exe, rakkoArgs(kind, s.tool, dataDir, event))
}

// removeShim deletes one tool's shim, reporting nothing when it is absent.
func removeShim(dataDir string, kind hookKind, tool Tool) error {
	if !windowsShellTool(tool) || dataDir == "" || toolShell(tool) == shellBash {
		return nil
	}
	err := os.Remove(shimPath(dataDir, kind, tool))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// shimInvocation is the command line that runs a shim in the tool's own
// interpreter.
func shimInvocation(tool Tool, kind hookKind, dataDir string) string {
	path := shimPath(dataDir, kind, tool)
	if toolShell(tool) == shellPowerShell {
		return "& " + powerShellQuote(path)
	}
	return cmdInvocation(path)
}

// posixEntryLine is the entry Qwen Code needs when it runs hooks through Git
// Bash: a POSIX shell line, with the Windows paths written the way MSYS
// resolves them.
func posixEntryLine(kind hookKind, tool Tool, exe, dataDir, event string) string {
	exePath := filepath.ToSlash(exe)
	call := shellQuote(exePath) + " " + posixArgs(kind, tool, dataDir, event)
	line := "if [ -x " + shellQuote(exePath) + " ]; then " + call + "; "
	if kind == hookApproval {
		if fallback := ApproveFallback(tool); fallback != "" {
			line += "else printf '%s' " + shellQuote(fallback) + "; "
		}
	}
	return line + "fi"
}

func posixArgs(kind hookKind, tool Tool, dataDir, event string) string {
	args := rakkoArgs(kind, tool, dataDir, event)
	out := make([]string, len(args))
	for i, a := range args {
		if a == dataDir {
			a = filepath.ToSlash(a)
		}
		out[i] = shellQuote(a)
	}
	return strings.Join(out, " ")
}

// escapedForTool escapes a value the tool itself expands. Auggie expands
// ${VAR} and ${VAR:-default} in the command and every argument, and its
// documentation spells a literal "${" as "\${".
func escapedForTool(tool Tool, value string) string {
	if tool == ToolAugment {
		return strings.ReplaceAll(value, "${", `\${`)
	}
	return value
}

func escapedArgs(tool Tool, args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = escapedForTool(tool, a)
	}
	return out
}

// cmdInvocation is the command line that runs path under cmd.exe. The
// doubled outer quotes are what /c needs to keep a path with spaces (or
// Chinese, or a dollar sign) whole, and they leave any arguments the tool
// appends in place. Measured on Windows 10 with cmd.exe /d /s /c.
func cmdInvocation(path string) string { return `""` + path + `""` }

// cmdQuote quotes one token inside a batch file for cmd.exe. The caller has
// already refused values cmd expands or treats specially.
func cmdQuote(s string) string { return `"` + s + `"` }

// powerShellQuote quotes one word as a PowerShell single-quoted string.
func powerShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// quotableFor reports whether a value can be handed to the interpreter
// unchanged. cmd.exe expands % even inside double quotes, ! under delayed
// expansion, and treats ^ and " as syntax; a newline ends the command
// everywhere. PowerShell and Bash single quotes keep every other character
// literal.
func quotableFor(shell windowsShell, value string) bool {
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	if shell == shellCmd {
		return !strings.ContainsAny(value, `%!^"`)
	}
	return true
}

func shellName(shell windowsShell) string {
	switch shell {
	case shellPowerShell:
		return "PowerShell"
	case shellBash:
		return "Git Bash"
	}
	return "cmd.exe"
}
