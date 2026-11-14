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

// TestClaudeLaunchCarriesABatchFileInTheEnvironment: an npm claude.cmd runs
// under cmd.exe with delayed expansion off, and the script and every
// argument travel in the environment, named once in the line. cmd expands
// each value exactly once, so a %OS% in the path or a %PATH% in an argument
// arrives as typed instead of being rewritten.
func TestClaudeLaunchCarriesABatchFileInTheEnvironment(t *testing.T) {
	script := `C:\Users\Jane Doe\AppData\Roaming\npm\claude.cmd`
	args := []string{"auth", "login", "a&b", "c^d", "100%", "%PATH%", "!x!", "with space"}
	tokens := append([]string{script}, args...)
	launch, err := claudeLaunch(tokens)
	if err != nil {
		t.Fatalf("claudeLaunch: %v", err)
	}
	if !strings.EqualFold(filepath.Base(launch.exe), "cmd.exe") {
		t.Fatalf("executable = %q, want cmd.exe", launch.exe)
	}
	if !strings.HasPrefix(launch.cmdLine, quoteShell(launch.exe)+" /d /v:off /s /c \"") {
		t.Fatalf("command line %q does not start the interpreter with /d /v:off /s /c", launch.cmdLine)
	}
	var tail strings.Builder
	tail.WriteString(`"`)
	for i := range tokens {
		if i > 0 {
			tail.WriteString(" ")
		}
		tail.WriteString(`"%` + launchEnvName(i) + `%"`)
	}
	tail.WriteString(`"`)
	if !strings.HasSuffix(launch.cmdLine, tail.String()) {
		t.Fatalf("command line %q does not end with %q", launch.cmdLine, tail.String())
	}
	if len(launch.env) != len(tokens) {
		t.Fatalf("launch carries %d environment entries, want %d", len(launch.env), len(tokens))
	}
	for i, token := range tokens {
		if want := launchEnvName(i) + "=" + token; launch.env[i] != want {
			t.Fatalf("environment entry %d = %q, want %q", i, launch.env[i], want)
		}
		if strings.Contains(launch.cmdLine, token) {
			t.Fatalf("command line %q holds the raw token %q, which cmd would rewrite", launch.cmdLine, token)
		}
	}
}

// withEnv adds the launch's entries to a copy of the child's environment: the
// caller's slice is untouched, and a name it already had is replaced.
func TestClaudeLaunchWithEnv(t *testing.T) {
	launch := launchCommand{env: []string{
		"RAKKO_LAUNCH_0=C:\\npm\\claude.cmd",
		"RAKKO_LAUNCH_1=%PATH%",
	}}
	env := []string{"PATH=C:\\Windows", "RAKKO_LAUNCH_0=stale"}
	got := launch.withEnv(env)
	if env[1] != "RAKKO_LAUNCH_0=stale" {
		t.Fatalf("the caller's environment was modified: %q", env)
	}
	if len(got) != 3 {
		t.Fatalf("environment = %q, want three entries", got)
	}
	if got[0] != "PATH=C:\\Windows" {
		t.Fatalf("PATH = %q", got[0])
	}
	want := map[string]string{
		"RAKKO_LAUNCH_0": `C:\npm\claude.cmd`,
		"RAKKO_LAUNCH_1": `%PATH%`,
	}
	for _, entry := range got[1:] {
		name, value, _ := strings.Cut(entry, "=")
		if want[name] != value {
			t.Fatalf("entry %q = %q, want %q", name, value, want[name])
		}
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

// An .exe starts as argv says it does, not through cmd.exe, and needs no
// environment of its own.
func TestClaudeLaunchKeepsAnExeLine(t *testing.T) {
	script := `C:\Program Files\Claude\claude.exe`
	launch, err := claudeLaunch([]string{script, "auth", "status"})
	if err != nil {
		t.Fatalf("claudeLaunch: %v", err)
	}
	if launch.exe != script {
		t.Fatalf("executable = %q, want %q", launch.exe, script)
	}
	if len(launch.env) != 0 {
		t.Fatalf("an .exe launch carries environment entries: %q", launch.env)
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

// writeArgEchoCmd puts a claude.cmd inside a folder named with a %OS% pair —
// a legal install path — and returns it. The script echoes its raw
// arguments and writes them to the file named by RAKKO_ARGS_FILE, so a run
// under the pseudo console, whose output is drained, can be read back.
func writeArgEchoCmd(t *testing.T) (script, argsFile string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "npm %OS% tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script = filepath.Join(dir, "claude.cmd")
	body := "@echo off\r\n" +
		"echo ARGS=[%*]\r\n" +
		"echo ARGS=[%*]>\"%RAKKO_ARGS_FILE%\"\r\n" +
		"exit /b 0\r\n"
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	argsFile = filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("RAKKO_ARGS_FILE", argsFile)
	return script, argsFile
}

// TestRunClaudeCommandPassesPercentLiterally: the plain run of a claude.cmd
// whose path holds %OS% delivers an argument holding %PATH% and an argument
// holding !x! as typed, in its output and to the script itself.
func TestRunClaudeCommandPassesPercentLiterally(t *testing.T) {
	script, argsFile := writeArgEchoCmd(t)
	out, stderr, err := runClaudeCommand(context.Background(), Env{ClaudePath: script},
		[]string{"auth", "%PATH%", "!x!", "100%"}, "")
	if err != nil {
		t.Fatalf("running a batch file: %v (stderr %q)", err, stderr)
	}
	want := `ARGS=["auth" "%PATH%" "!x!" "100%"]`
	if !strings.Contains(out, want) {
		t.Fatalf("output %q does not hold %q", out, want)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the script never wrote its arguments: %v", err)
	}
	if !strings.Contains(string(got), want) {
		t.Fatalf("the script received %q, want %q", got, want)
	}
}

// TestRunClaudeLoginPassesPercentLiterally: the sign-in's entry point
// behaves the same; a path with %OS% and arguments with %PATH% and !x! reach
// the script verbatim.
func TestRunClaudeLoginPassesPercentLiterally(t *testing.T) {
	script, argsFile := writeArgEchoCmd(t)
	err := runClaudeLoginTTY(context.Background(),
		[]string{script, "auth", "%PATH%", "!x!"}, t.TempDir(), os.Environ())
	if err != nil {
		t.Fatalf("a batch-file sign-in: %v", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the sign-in never ran the script: %v", err)
	}
	want := `ARGS=["auth" "%PATH%" "!x!"]`
	if !strings.Contains(string(got), want) {
		t.Fatalf("the script received %q, want %q", got, want)
	}
}
