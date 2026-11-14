//go:build windows

package agentnotify

// What Rakko writes into a tool's configuration on Windows, and what the
// tool's own interpreter does with it. The helper program in testdata stands
// in for Rakko.exe: it echoes its arguments and stdin, so a test can check
// the whole path from the written entry through cmd.exe, PowerShell or Git
// Bash to the payload Rakko receives.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

const testExe = `C:\Program Files\Rakko\Rakko.exe`

const testData = `C:\Users\me\AppData\Roaming\Rakko`

// helper builds the stand-in Rakko.exe once per test binary.
var helper struct {
	once sync.Once
	path string
	err  error
}

func helperExe(t *testing.T) string {
	t.Helper()
	helper.once.Do(func() {
		dir, err := os.MkdirTemp("", "rakko-helper-")
		if err != nil {
			helper.err = err
			return
		}
		exe := filepath.Join(dir, "Rakko.exe")
		cmd := exec.Command("go", "build", "-o", exe, "./testdata/rakkohelper")
		out, err := cmd.CombinedOutput()
		if err != nil {
			helper.err = err
			t.Logf("go build: %s", out)
			return
		}
		helper.path = exe
	})
	if helper.err != nil {
		t.Fatalf("building the helper: %v", helper.err)
	}
	return helper.path
}

func specForTool(t *testing.T, tool Tool) spec {
	t.Helper()
	s, ok := specFor(tool)
	if !ok {
		t.Fatalf("%s has no spec", tool)
	}
	return s
}

func argvEntry(t *testing.T, e hookEntry) (value string, args []string) {
	t.Helper()
	if len(e.fields) != 1 {
		t.Fatalf("entry has %d members, want one", len(e.fields))
	}
	return e.fields[0].value, e.argv
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Each tool's entry uses the shape its own documentation runs: a program and
// an argument array where the tool takes one, and otherwise the shim named
// through the interpreter that tool actually uses on Windows.
func TestHookEntriesUseEachToolsOwnForm(t *testing.T) {
	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)
	t.Setenv("MSYSTEM", "")
	t.Setenv("TERM", "")

	if value, args := argvEntry(t, hookEntryFor(testExe, ToolClaude, testData, "")); value != testExe ||
		!equalStrings(args, []string{"--agent-hook", "--tool", "claude", "--data-dir", testData}) {
		t.Fatalf("claude = %q %q", value, args)
	}
	// Copilot CLI's exec form, with the event its payload does not name.
	if value, args := argvEntry(t, hookEntryFor(testExe, ToolCopilot, testData, "agentStop")); value != testExe ||
		!equalStrings(args, []string{"--agent-hook", "--tool", "copilot", "--event", "agentStop", "--data-dir", testData}) {
		t.Fatalf("copilot = %q %q", value, args)
	}
	// Auggie expands ${VAR} in the program path and every argument; a
	// literal "${" has to be escaped.
	auggieExe := `C:\Program Files\Rakko ${HOME}\Rakko.exe`
	value, args := argvEntry(t, hookEntryFor(auggieExe, ToolAugment, `C:\d ${X}`, ""))
	if value != `C:\Program Files\Rakko \${HOME}\Rakko.exe` {
		t.Fatalf("auggie program = %q", value)
	}
	if args[4] != `C:\d \${X}` {
		t.Fatalf("auggie data dir = %q", args[3])
	}

	// PowerShell runners.
	for _, tool := range []Tool{ToolCodex, ToolCursor, ToolGemini} {
		e := hookEntryFor(testExe, tool, testData, "")
		want := "& '" + shimPath(testData, hookCompletion, tool) + "'"
		if e.fields[0].value != want || e.argv != nil {
			t.Fatalf("%s = %q %q, want %q", tool, e.fields[0].value, e.argv, want)
		}
	}
	if e := hookEntryFor(testExe, ToolCodex, testData, ""); len(e.fields) != 2 ||
		e.fields[1].name != "commandWindows" || e.fields[1].value != e.fields[0].value {
		t.Fatalf("codex entry lacks commandWindows: %+v", e.fields)
	}

	// cmd.exe runners.
	for _, tool := range []Tool{ToolAntigravity, ToolGrok, ToolFactory, ToolKiro, ToolDevin} {
		e := hookEntryFor(testExe, tool, testData, "")
		want := `""` + shimPath(testData, hookCompletion, tool) + `""`
		if e.fields[0].value != want {
			t.Fatalf("%s = %q, want %q", tool, e.fields[0].value, want)
		}
	}

	// Antigravity's payload names no event, so its shim carries it.
	antiSpec := specForTool(t, ToolAntigravity)
	if got := eventArgFor(antiSpec); got != "Stop" {
		t.Fatalf("antigravity event = %q, want Stop", got)
	}
	antiDir := t.TempDir()
	if err := prepareHookShim(hookCompletion, antiSpec, antiDir, testExe); err != nil {
		t.Fatal(err)
	}
	antiShim, err := os.ReadFile(shimPath(antiDir, hookCompletion, ToolAntigravity))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(antiShim), `"--event" "Stop"`) {
		t.Fatalf("antigravity shim lacks its event:\n%s", antiShim)
	}

	// Approvals keep their own shim and their own flag.
	if value, args := argvEntry(t, approveEntryFor(testExe, ToolClaude, testData)); value != testExe ||
		!equalStrings(args, []string{"--agent-approve", "--tool", "claude", "--data-dir", testData}) {
		t.Fatalf("claude approval = %q %q", value, args)
	}
	if value, args := argvEntry(t, approveEntryFor(testExe, ToolCopilot, testData)); value != testExe ||
		!equalStrings(args, []string{"--agent-approve", "--tool", "copilot", "--data-dir", testData}) {
		t.Fatalf("copilot approval = %q %q", value, args)
	}
	if e := approveEntryFor(testExe, ToolAntigravity, testData); e.fields[0].value !=
		`""`+shimPath(testData, hookApproval, ToolAntigravity)+`""` {
		t.Fatalf("antigravity approval = %q", e.fields[0].value)
	}
	if e := approveEntryFor(testExe, ToolCursor, testData); !strings.HasPrefix(e.fields[0].value, "& '") {
		t.Fatalf("cursor approval = %q", e.fields[0].value)
	}
}

// Qwen Code decides its shell at run time; Rakko decides from the same
// inputs, so the entry is the one Qwen will actually hand to that shell.
func TestQwenFollowsItsOwnShellChoice(t *testing.T) {
	t.Setenv("MSYSTEM", "MINGW64")
	t.Setenv("TERM", "xterm")
	if e := hookEntryFor(testExe, ToolQwen, testData, ""); !strings.HasPrefix(e.fields[0].value, "if [ -x ") {
		t.Fatalf("git bash entry = %q", e.fields[0].value)
	}

	t.Setenv("MSYSTEM", "")
	t.Setenv("ComSpec", `C:\Program Files\PowerShell\7\pwsh.exe`)
	if e := hookEntryFor(testExe, ToolQwen, testData, ""); !strings.HasPrefix(e.fields[0].value, "& '") {
		t.Fatalf("powershell entry = %q", e.fields[0].value)
	}

	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)
	if e := hookEntryFor(testExe, ToolQwen, testData, ""); !strings.HasPrefix(e.fields[0].value, `""`) {
		t.Fatalf("cmd entry = %q", e.fields[0].value)
	}
}

// A path cmd.exe would expand or mangle is refused before the tool's
// configuration is touched; an argv tool has no shell layer, so it is not
// affected.
func TestUnquotablePathsAreRefused(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(t.TempDir(), "Rakko.exe")
	for _, bad := range []string{"100%", "bang!", "caret^", `quote"`} {
		dir := filepath.Join(t.TempDir(), bad)
		if err := Connect(home, dir, exe, ToolCursor); err == nil {
			t.Fatalf("Connect accepted a data directory containing %q", bad)
		}
		if _, err := os.Stat(filepath.Join(dir, shimName(hookCompletion, ToolCursor))); err == nil {
			t.Fatal("a refused connect left a shim behind")
		}
		if _, err := os.Stat(ConfigPath(home, ToolCursor)); err == nil {
			t.Fatal("a refused connect wrote the tool's configuration")
		}
	}
	// The same values in an exec-form entry are fine: nothing parses them.
	if err := Connect(home, filepath.Join(t.TempDir(), "100%"), exe, ToolClaude); err != nil {
		t.Fatalf("claude was refused: %v", err)
	}
}

// A path with spaces, Chinese, a dollar sign and a backtick is quoted
// correctly by each interpreter, and the payload arrives unchanged.
func TestShimsCarryOddPathsAndPayloads(t *testing.T) {
	exe := helperExe(t)
	dataDir := filepath.Join(t.TempDir(), "数据 目录 $HOME `tick`")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s1","prompt":"你好 ` + "`x`" + ` $HOME"}`
	for _, kind := range []hookKind{hookCompletion, hookApproval} {
		if err := writeShim(dataDir, kind, ToolCursor, exe, rakkoArgs(kind, ToolCursor, dataDir, "")); err != nil {
			t.Fatalf("%v shim: %v", kind, err)
		}
	}
	type result struct {
		Args  []string `json:"args"`
		Stdin string   `json:"stdin"`
	}
	runArgs := func(t *testing.T, argv []string) result {
		t.Helper()
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v (%s)", argv, err, out)
		}
		var r result
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("output %q: %v", out, err)
		}
		return r
	}
	runRaw := func(t *testing.T, commandLine, _ string) result {
		t.Helper()
		cmd := exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: commandLine}
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v (%s)", commandLine, err, out)
		}
		var r result
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("output %q: %v", out, err)
		}
		return r
	}

	// cmd.exe, the way a shell tool's entry runs the shim, with one extra
	// argument to show %* hands it over.
	got := runRaw(t, "cmd.exe /d /s /c "+
		cmdInvocation(shimPath(dataDir, hookCompletion, ToolCursor))+" extra", helperExe(t))
	if got.Stdin != payload {
		t.Fatalf("cmd stdin = %q", got.Stdin)
	}
	if !equalStrings(got.Args, append(rakkoArgs(hookCompletion, ToolCursor, dataDir, ""), "extra")) {
		t.Fatalf("cmd args = %q", got.Args)
	}

	// A cmd.exe tool's entry, which is just the quoted shim path.
	if err := writeShim(dataDir, hookCompletion, ToolGrok, exe,
		rakkoArgs(hookCompletion, ToolGrok, dataDir, "")); err != nil {
		t.Fatal(err)
	}
	grok := hookEntryFor(exe, ToolGrok, dataDir, "")
	grokOut := runRaw(t, "cmd.exe /d /s /c "+grok.fields[0].value, helperExe(t))
	if !equalStrings(grokOut.Args, rakkoArgs(hookCompletion, ToolGrok, dataDir, "")) {
		t.Fatalf("grok args = %q", grokOut.Args)
	}

	// PowerShell, the way Cursor's entry runs it.
	ps := runArgs(t, []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", hookEntryFor(exe, ToolCursor, dataDir, "").fields[0].value})
	if ps.Stdin != payload {
		t.Fatalf("powershell stdin = %q", ps.Stdin)
	}
	if !equalStrings(ps.Args, rakkoArgs(hookCompletion, ToolCursor, dataDir, "")) {
		t.Fatalf("powershell args = %q", ps.Args)
	}

	// Git Bash, the way Qwen Code runs a hook when it picks bash.
	gitBash := `C:\Program Files\Git\bin\bash.exe`
	if _, err := os.Stat(gitBash); err != nil {
		t.Logf("skipping the Git Bash run: %v", err)
		return
	}
	t.Setenv("MSYSTEM", "MINGW64")
	line := hookEntryFor(exe, ToolQwen, dataDir, "").fields[0].value
	bash := runArgs(t, []string{gitBash, "-c", line})
	if bash.Stdin != payload {
		t.Fatalf("bash stdin = %q", bash.Stdin)
	}
	// MSYS resolves the data directory through its forward-slash spelling;
	// the bytes the app receives are the same path.
	want := rakkoArgs(hookCompletion, ToolQwen, filepath.ToSlash(dataDir), "")
	if !equalStrings(bash.Args, want) {
		t.Fatalf("bash args = %q, want %q", bash.Args, want)
	}
}

// A removed app changes nothing: the approval shim prints the tool's own
// fallback and the completion shim prints nothing, in every interpreter.
func TestShimsFallBackWhenTheAppIsGone(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", "Rakko.exe")
	dataDir := t.TempDir()
	run := func(t *testing.T, kind hookKind, tool Tool) string {
		t.Helper()
		if err := writeShim(dataDir, kind, tool, missing, rakkoArgs(kind, tool, dataDir, "")); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd.exe /d /s /c " +
			cmdInvocation(shimPath(dataDir, kind, tool))}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v %v: %v", tool, kind, err)
		}
		return string(out)
	}
	if out := run(t, hookCompletion, ToolCursor); out != "" {
		t.Fatalf("completion fallback printed %q", out)
	}
	if out := run(t, hookApproval, ToolAntigravity); strings.TrimSpace(out) != `{"decision":"allow"}` {
		t.Fatalf("antigravity fallback printed %q", out)
	}
	if out := run(t, hookApproval, ToolClaude); out != "" {
		t.Fatalf("claude fallback printed %q", out)
	}
}

// Every tool connects, reports itself installed, survives a second connect
// unchanged, and cleans up after itself; each one keeps its own files.
func TestConnectStatusDisconnectEveryTool(t *testing.T) {
	t.Setenv("HERMES_HOME", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("DSH_HOME", "")
	exe := helperExe(t)
	for _, tool := range Tools() {
		t.Run(string(tool), func(t *testing.T) {
			home := t.TempDir()
			dataDir := t.TempDir()
			installHermes(t, home, tool)
			if err := Connect(home, dataDir, exe, tool); err != nil {
				t.Fatalf("connect: %v", err)
			}
			if got, note := Status(home, tool); got != StateInjected {
				t.Fatalf("status = %v (%s)", got, note)
			}
			if s, ok := specFor(tool); ok && windowsShellTool(tool) && s.kind != KindHermes && s.kind != KindPatch {
				shim := shimPath(dataDir, hookCompletion, tool)
				if _, err := os.Stat(shim); err != nil {
					t.Fatalf("connect did not write %s: %v", shim, err)
				}
			}
			before := snapshotTree(t, home)
			if err := Connect(home, dataDir, exe, tool); err != nil {
				t.Fatalf("second connect: %v", err)
			}
			if after := snapshotTree(t, home); after != before {
				t.Fatalf("a second connect changed the files:\n%s\n---\n%s", before, after)
			}
			if got, note := Status(home, tool); got != StateInjected {
				t.Fatalf("status after second connect = %v (%s)", got, note)
			}
			if err := Disconnect(home, dataDir, tool); err != nil {
				t.Fatalf("disconnect: %v", err)
			}
			if got, _ := Status(home, tool); got == StateInjected {
				t.Fatal("disconnect left the entry in place")
			}
			if _, err := os.Stat(shimPath(dataDir, hookCompletion, tool)); err == nil {
				t.Fatal("disconnect left the shim behind")
			}
		})
	}
}

// snapshotTree renders every file under dir, so a second connect can be
// compared byte for byte.
func snapshotTree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		if info.IsDir() {
			b.WriteString("dir " + rel + "\n")
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		b.WriteString("file " + rel + "\n" + string(raw) + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// hermesTestConfig is what Hermes writes for itself; Rakko only extends an
// existing profile.
const hermesTestConfig = `_config_version: 52
plugins:
  enabled:
    - agent-importers
  disabled: []
model:
  default: deepseek-v4.1-flash
`

// installHermes gives the Hermes tests the config file Hermes itself
// creates; Rakko refuses to invent one for a tool that is not installed.
func installHermes(t *testing.T, home string, tool Tool) {
	t.Helper()
	if tool != ToolHermes {
		return
	}
	path := hermesConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(hermesTestConfig), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The approval entry has its own life: connecting it writes its own shim,
// disconnecting it leaves the completion entry alone.
func TestApprovalIsSeparateFromCompletion(t *testing.T) {
	t.Setenv("HERMES_HOME", "")
	exe := helperExe(t)
	for _, tool := range Tools() {
		if !tool.Approvable() {
			continue
		}
		t.Run(string(tool), func(t *testing.T) {
			home := t.TempDir()
			dataDir := t.TempDir()
			installHermes(t, home, tool)
			if err := Connect(home, dataDir, exe, tool); err != nil {
				t.Fatal(err)
			}
			if err := ConnectApprove(home, dataDir, exe, tool); err != nil {
				t.Fatalf("connect approve: %v", err)
			}
			if got, note := StatusApprove(home, tool); got != StateInjected {
				t.Fatalf("approval status = %v (%s)", got, note)
			}
			if got, note := Status(home, tool); got != StateInjected {
				t.Fatalf("completion status = %v (%s)", got, note)
			}
			shim := shimPath(dataDir, hookApproval, tool)
			if windowsShellTool(tool) {
				if _, err := os.Stat(shim); err != nil {
					t.Fatalf("approval connect did not write %s: %v", shim, err)
				}
			}
			if err := DisconnectApprove(home, dataDir, tool); err != nil {
				t.Fatalf("disconnect approve: %v", err)
			}
			if got, _ := StatusApprove(home, tool); got == StateInjected {
				t.Fatal("disconnect left the approval entry in place")
			}
			if got, _ := Status(home, tool); got != StateInjected {
				t.Fatal("disconnecting the approval entry removed the completion entry")
			}
			if _, err := os.Stat(shim); err == nil {
				t.Fatal("disconnect left the approval shim behind")
			}
		})
	}
}

// Disconnecting one tool leaves every other tool's files byte for byte.
func TestDisconnectingOneToolLeavesTheOthers(t *testing.T) {
	exe := helperExe(t)
	home := t.TempDir()
	dataDir := t.TempDir()
	for _, tool := range []Tool{ToolClaude, ToolCursor, ToolGrok} {
		if err := Connect(home, dataDir, exe, tool); err != nil {
			t.Fatal(err)
		}
	}
	cursor := ConfigPath(home, ToolCursor)
	before, err := os.ReadFile(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if err := Disconnect(home, dataDir, ToolClaude); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("disconnecting claude changed cursor's file")
	}
	if got, _ := Status(home, ToolGrok); got != StateInjected {
		t.Fatal("grok was cleaned up with claude")
	}
}

// Entries written by a Rakko that used the shell line everywhere are still
// recognized and removed.
func TestLegacyEntriesAreRecognizedAndCleaned(t *testing.T) {
	home := t.TempDir()
	dataDir := t.TempDir()
	legacy := `{
  "hooks": {
    "Stop": [
      { "hooks": [
        { "type": "command", "command": "if [ -x '/Applications/Rakko.app/Contents/MacOS/Rakko' ]; then '/Applications/Rakko.app/Contents/MacOS/Rakko' --agent-hook --tool claude; fi", "timeout": 5 }
      ] }
    ]
  }
}
`
	path := ConfigPath(home, ToolClaude)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, note := Status(home, ToolClaude); got != StateInjected {
		t.Fatalf("legacy entry status = %v (%s)", got, note)
	}
	if err := Disconnect(home, dataDir, ToolClaude); err != nil {
		t.Fatal(err)
	}
	if got, _ := Status(home, ToolClaude); got == StateInjected {
		t.Fatal("the legacy entry survived a disconnect")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "--agent-hook") {
		t.Fatalf("the legacy entry is still in the file:\n%s", body)
	}
}

// Copilot CLI's Windows files use its exec form: no shell field at all, the
// program and its arguments in their own members.
func TestCopilotUsesExecForm(t *testing.T) {
	t.Setenv("HERMES_HOME", "")
	exe := helperExe(t)
	home := t.TempDir()
	dataDir := t.TempDir()
	if err := Connect(home, dataDir, exe, ToolCopilot); err != nil {
		t.Fatal(err)
	}
	if err := ConnectApprove(home, dataDir, exe, ToolCopilot); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		ConfigPath(home, ToolCopilot),
		ApproveConfigPath(home, ToolCopilot),
	} {
		var doc struct {
			Hooks map[string][]struct {
				Type string   `json:"type"`
				Exec string   `json:"exec"`
				Args []string `json:"args"`
				Bash string   `json:"bash"`
				Pow  string   `json:"powershell"`
				Cmd  string   `json:"command"`
			} `json:"hooks"`
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, raw)
		}
		if len(doc.Hooks) == 0 {
			t.Fatalf("%s has no hooks", path)
		}
		for event, entries := range doc.Hooks {
			if len(entries) != 1 {
				t.Fatalf("%s %s: %d entries", path, event, len(entries))
			}
			e := entries[0]
			if e.Exec != exe || len(e.Args) == 0 {
				t.Fatalf("%s %s: exec = %q, args = %q", path, event, e.Exec, e.Args)
			}
			if e.Bash != "" || e.Pow != "" || e.Cmd != "" {
				t.Fatalf("%s %s mixes the shell forms in: %+v", path, event, e)
			}
			if e.Args[0] != "--agent-hook" && e.Args[0] != "--agent-approve" {
				t.Fatalf("%s %s: args = %q", path, event, e.Args)
			}
		}
	}
}
