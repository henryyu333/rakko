//go:build windows

package aiusage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeClaudeCmd puts a claude.cmd in a folder whose name holds the
// characters a launch has to survive, and returns its path.
func writeFakeClaudeCmd(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "npm & tools (x86)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "claude.cmd")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestClaudeLaunchQuotesABatchFile: an npm claude.cmd runs under cmd.exe,
// with the script path and every argument quoted, so a path with spaces and
// cmd's metacharacters reach the script as one token each.
func TestClaudeLaunchQuotesABatchFile(t *testing.T) {
	script := `C:\Users\Jane Doe\AppData\Roaming\npm\claude.cmd`
	launch, err := claudeLaunch([]string{script, "auth", "login", "a&b", "c^d", "100%", "with space"})
	if err != nil {
		t.Fatalf("claudeLaunch: %v", err)
	}
	if !strings.EqualFold(filepath.Base(launch.exe), "cmd.exe") {
		t.Fatalf("executable = %q, want cmd.exe", launch.exe)
	}
	if !strings.HasPrefix(launch.cmdLine, quoteCmd(launch.exe)+" /d /s /c \"") {
		t.Fatalf("command line %q does not start the interpreter with /d /s /c", launch.cmdLine)
	}
	want := `"` + script + `" "auth" "login" "a&b" "c^d" "100%" "with space"`
	if !strings.Contains(launch.cmdLine, want) {
		t.Fatalf("command line %q does not hold %q", launch.cmdLine, want)
	}
	if !strings.HasSuffix(launch.cmdLine, `"`+`"`) {
		t.Fatalf("command line %q does not close cmd's quoted command", launch.cmdLine)
	}
}

// A quote cannot survive cmd's parsing of a batch file's arguments, and
// letting one through could start a second command: it is refused.
func TestClaudeLaunchRefusesAQuoteInABatchArgument(t *testing.T) {
	script := `C:\npm\claude.cmd`
	if _, err := claudeLaunch([]string{script, `a" & calc`}); err == nil {
		t.Fatal("an argument with a quote was accepted for a batch file")
	}
}

// An .exe starts as argv says it does, not through cmd.exe.
func TestClaudeLaunchKeepsAnExeLine(t *testing.T) {
	script := `C:\Program Files\Claude\claude.exe`
	launch, err := claudeLaunch([]string{script, "auth", "status"})
	if err != nil {
		t.Fatalf("claudeLaunch: %v", err)
	}
	if launch.exe != script {
		t.Fatalf("executable = %q, want %q", launch.exe, script)
	}
	if strings.Contains(launch.cmdLine, "cmd.exe") {
		t.Fatalf("an .exe was routed through cmd: %q", launch.cmdLine)
	}
	if !strings.Contains(launch.cmdLine, script) || !strings.HasSuffix(launch.cmdLine, "auth status") {
		t.Fatalf("command line %q does not hold the argv", launch.cmdLine)
	}
}

// TestRunClaudeCommandRunsABatchFile: the plain run reaches a claude.cmd in
// a path with a space and passes its flags through untouched.
func TestRunClaudeCommandRunsABatchFile(t *testing.T) {
	script := writeFakeClaudeCmd(t, "@echo off\r\necho ARGS=[%*]\r\nexit /b 0\r\n")
	env := Env{ClaudePath: script}
	out, stderr, err := runClaudeCommand(context.Background(), env, []string{"auth", "status", "a&b", "100%"}, "")
	if err != nil {
		t.Fatalf("running a batch file: %v (stderr %q)", err, stderr)
	}
	if want := `ARGS=["auth" "status" "a&b" "100%"]`; !strings.Contains(out, want) {
		t.Fatalf("output %q does not hold %q", out, want)
	}
}

// TestRunClaudeCommandReportsABatchExit: the run's exit code still comes
// back as the error, not as a failed launch.
func TestRunClaudeCommandReportsABatchExit(t *testing.T) {
	script := writeFakeClaudeCmd(t, "@echo off\r\nexit /b 7\r\n")
	env := Env{ClaudePath: script}
	if _, _, err := runClaudeCommand(context.Background(), env, []string{"auth", "status"}, ""); err == nil {
		t.Fatal("exit code 7 reported success")
	}
}

// TestRunClaudeLoginRunsABatchFile: the sign-in may be the npm claude.cmd;
// it runs under the pseudo console with the same launch line, and its exit
// code is the result.
func TestRunClaudeLoginRunsABatchFile(t *testing.T) {
	script := writeFakeClaudeCmd(t, "@echo off\r\nexit /b 0\r\n")
	if err := runClaudeLoginTTY(context.Background(), []string{script, "auth", "login"}, t.TempDir(), os.Environ()); err != nil {
		t.Fatalf("a batch-file sign-in: %v", err)
	}
	failing := writeFakeClaudeCmd(t, "@echo off\r\nexit /b 3\r\n")
	if err := runClaudeLoginTTY(context.Background(), []string{failing, "auth", "login"}, t.TempDir(), os.Environ()); err == nil {
		t.Fatal("a batch-file sign-in that failed reported success")
	}
}
