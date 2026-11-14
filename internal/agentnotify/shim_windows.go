//go:build windows

package agentnotify

// The batch file behind one shell-tool entry. cmd.exe is what a .cmd file is
// for; PowerShell and Git Bash can run it too, and Qwen Code under Git Bash
// gets a POSIX line instead (it cannot execute a .cmd file at all).
//
// The shim bakes in everything the tool does not pass: the mode flag, the
// tool, the event when the payload does not name it, and Rakko's data
// directory when it is not the default. %* hands over whatever the tool
// itself appends. When the app binary is gone the approval shim prints the
// tool's own fallback (Antigravity's allow, nothing for the rest) and the
// completion shim does nothing, so the agent's behaviour is unchanged.

import (
	"fmt"
	"os"
	"strings"
)

// writeShim writes (or replaces) one tool's shim. It is idempotent: a second
// connect writes the same bytes.
func writeShim(dataDir string, kind hookKind, tool Tool, exe string, args []string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(shimPath(dataDir, kind, tool), []byte(shimBody(kind, tool, exe, args)), 0o600)
}

func shimBody(kind hookKind, tool Tool, exe string, args []string) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("if not exist " + cmdQuote(exe) + " goto rakko_missing\r\n")
	b.WriteString(cmdQuote(exe))
	for _, a := range args {
		b.WriteString(" " + cmdQuote(a))
	}
	b.WriteString(" %*\r\n")
	b.WriteString("goto rakko_done\r\n")
	b.WriteString(":rakko_missing\r\n")
	if kind == hookApproval {
		if fallback := ApproveFallback(tool); fallback != "" {
			b.WriteString("echo " + fallback + "\r\n")
		}
	}
	b.WriteString(":rakko_done\r\n")
	return b.String()
}

// shimFallbackEchoSafe reports whether a fallback line can be echoed by
// cmd.exe unchanged. Every fallback Rakko writes today is plain JSON.
func shimFallbackEchoSafe(fallback string) bool {
	return !strings.ContainsAny(fallback, "%!^&|<>()\r\n")
}

// validateShimFallbacks checks every tool's fallback once, so a future
// fallback that cmd.exe would mangle fails the tests instead of a user's
// tool.
func validateShimFallbacks() error {
	for _, s := range specs {
		if s.approve == nil || s.approve.fallback == "" {
			continue
		}
		if !shimFallbackEchoSafe(s.approve.fallback) {
			return fmt.Errorf("agentnotify: %s's fallback cannot be echoed by cmd.exe", s.tool)
		}
	}
	return nil
}
