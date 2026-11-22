//go:build windows

package agentnotify

// The batch file behind one shell-tool entry. cmd.exe is what a .cmd file is
// for; PowerShell can run it too.
//
// The shim bakes in everything the tool does not pass: the mode flag, the
// tool, the event when the payload does not name it, and Rakko's data
// directory when it is not the default. %* hands over whatever the tool
// itself appends. When the app binary is gone the approval shim prints the
// tool's own fallback (Antigravity's allow, nothing for the rest) and the
// completion shim does nothing, so the agent's behaviour is unchanged.
//
// Two details keep non-ASCII paths working whatever console code page the
// caller uses: the file is written as UTF-8, and the script switches to
// UTF-8 before it reads the first path, restoring the caller's code page at
// the end. Arguments handed to the app are quoted by the native command-line
// rules, not by cmd's own, so a path ending in a backslash survives.

import (
	"os"
	"path/filepath"
	"strings"

	"rakko/internal/atomicfile"
)

// writeShim writes (or replaces) one tool's shim atomically through a
// temporary file in the same directory so a write failure cannot truncate a
// working entry. It is idempotent: a second connect writes the same bytes.
func writeShim(dataDir string, kind hookKind, tool Tool, exe string, args []string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	path := shimPath(dataDir, kind, tool)
	return atomicfile.WriteFile(filepath.Dir(path), filepath.Base(path), []byte(shimBody(kind, tool, exe, args)), 0o600)
}

func shimBody(kind hookKind, tool Tool, exe string, args []string) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	// Read the rest of this file (which may hold Chinese, a dollar sign or a
	// space) as UTF-8, whatever code page the caller is on, and put the
	// caller's page back afterwards.
	b.WriteString("for /f \"tokens=2 delims=:\" %%i in ('chcp') do set \"RAKKO_CHCP=%%i\"\r\n")
	b.WriteString("chcp 65001 >nul\r\n")
	b.WriteString("if not exist " + cmdQuote(exe) + " goto rakko_missing\r\n")
	b.WriteString(cmdQuote(exe))
	for _, a := range args {
		b.WriteString(" " + cmdArg(a))
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
	b.WriteString("if defined RAKKO_CHCP chcp %RAKKO_CHCP% >nul\r\n")
	return b.String()
}

// cmdArg quotes one argument the way the native program that receives it
// parses it: backslashes are literal except immediately before a closing
// quote, so a path ending in a backslash keeps it.
func cmdArg(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\\`, slashes))
			slashes = 0
			b.WriteString(`\"`)
			continue
		}
		b.WriteString(strings.Repeat(`\`, slashes))
		slashes = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat(`\\`, slashes))
	b.WriteByte('"')
	return b.String()
}

// shimExisted reports whether this tool's shim was already on disk before a
// connect. A failed connect must not take away a shim the tool's existing
// configuration still names.
func shimExisted(dataDir string, kind hookKind, tool Tool) bool {
	if dataDir == "" {
		return false
	}
	_, err := os.Stat(shimPath(dataDir, kind, tool))
	return err == nil
}
