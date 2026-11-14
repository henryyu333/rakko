//go:build windows

package agentnotify

// What Rakko writes into a tool's configuration on Windows, and what the
// tool's own interpreter does with it. The helper program in testdata stands
// in for Rakko.exe: it echoes its arguments and stdin, so a test can check
// the whole path from the written entry through cmd.exe, PowerShell or Git
// Bash to the payload Rakko receives.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// Qwen Code runs a hook with the interpreter its entry names, so the entry
// says PowerShell and the command is one PowerShell runs — whatever shell
// the user started Qwen from, and whatever Rakko was started from.
func TestQwenPinsItsShell(t *testing.T) {
	for _, env := range []struct{ msystem, comspec string }{
		{"MINGW64", `C:\Windows\System32\cmd.exe`},
		{"", `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`},
		{"", `C:\Windows\System32\cmd.exe`},
	} {
		t.Setenv("MSYSTEM", env.msystem)
		t.Setenv("COMSPEC", env.comspec)
		e := hookEntryFor(testExe, ToolQwen, testData, "")
		if len(e.fields) != 2 || e.fields[1].name != "shell" || e.fields[1].value != "powershell" {
			t.Fatalf("qwen entry lacks its shell field: %+v", e.fields)
		}
		want := "& '" + shimPath(testData, hookCompletion, ToolQwen) + "'"
		if e.fields[0].value != want {
			t.Fatalf("qwen command = %q, want %q", e.fields[0].value, want)
		}
		if a := approveEntryFor(testExe, ToolQwen, testData); len(a.fields) != 2 ||
			a.fields[1].name != "shell" || a.fields[1].value != "powershell" {
			t.Fatalf("qwen approval lacks its shell field: %+v", a.fields)
		}
	}
}

// A path cmd.exe would expand or mangle is refused before the tool's
// configuration is touched; an argv tool has no shell layer, so it is not
// affected.
func TestUnquotablePathsAreRefused(t *testing.T) {
	home := t.TempDir()
	redirectAppData(t, home)
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
			redirectAppData(t, home)
			dataDir := t.TempDir()
			installHermes(t, home, tool)
			if err := Connect(home, dataDir, exe, tool); err != nil {
				t.Fatalf("connect: %v", err)
			}
			if got, note := Status(home, dataDir, tool); got != StateInjected {
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
			if got, note := Status(home, dataDir, tool); got != StateInjected {
				t.Fatalf("status after second connect = %v (%s)", got, note)
			}
			if err := Disconnect(home, dataDir, tool); err != nil {
				t.Fatalf("disconnect: %v", err)
			}
			if got, _ := Status(home, dataDir, tool); got == StateInjected {
				t.Fatal("disconnect left the entry in place")
			}
			if _, err := os.Stat(shimPath(dataDir, hookCompletion, tool)); err == nil {
				t.Fatal("disconnect left the shim behind")
			}
		})
	}
}

// redirectAppData points the AppData lookups at the temporary profile, so a
// test never touches the machine's real Cursor, Kiro or Devin folders.
func redirectAppData(t *testing.T, home string) {
	t.Helper()
	oldRoaming, oldLocal := roamingAppData, localAppData
	roamingAppData = func() string { return filepath.Join(home, "AppData", "Roaming") }
	localAppData = func() string { return filepath.Join(home, "AppData", "Local") }
	t.Cleanup(func() { roamingAppData, localAppData = oldRoaming, oldLocal })
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
			redirectAppData(t, home)
			dataDir := t.TempDir()
			installHermes(t, home, tool)
			if err := Connect(home, dataDir, exe, tool); err != nil {
				t.Fatal(err)
			}
			if err := ConnectApprove(home, dataDir, exe, tool); err != nil {
				t.Fatalf("connect approve: %v", err)
			}
			if got, note := StatusApprove(home, dataDir, tool); got != StateInjected {
				t.Fatalf("approval status = %v (%s)", got, note)
			}
			if got, note := Status(home, dataDir, tool); got != StateInjected {
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
			if got, _ := StatusApprove(home, dataDir, tool); got == StateInjected {
				t.Fatal("disconnect left the approval entry in place")
			}
			if got, _ := Status(home, dataDir, tool); got != StateInjected {
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
	redirectAppData(t, home)
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
	if got, _ := Status(home, dataDir, ToolGrok); got != StateInjected {
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
	if got, note := Status(home, dataDir, ToolClaude); got != StateInjected {
		t.Fatalf("legacy entry status = %v (%s)", got, note)
	}
	if err := Disconnect(home, dataDir, ToolClaude); err != nil {
		t.Fatal(err)
	}
	if got, _ := Status(home, dataDir, ToolClaude); got == StateInjected {
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
	redirectAppData(t, home)
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

// A user's own hook that merely mentions a shim's file name is not Rakko's:
// Connect adds Rakko's entry beside it and Disconnect leaves it alone.
func TestUserEntriesMentioningRakkoAreNotOurs(t *testing.T) {
	home := t.TempDir()
	redirectAppData(t, home)
	dataDir := t.TempDir()
	exe := helperExe(t)
	path := ConfigPath(home, ToolCursor)
	user := `{
  "version": 1,
  "hooks": {
    "stop": [
      { "command": "echo agent-hook-cursor.cmd", "timeout": 5 },
      { "exec": "C:\\Tools\\other.exe", "args": ["--agent-hook", "--tool", "cursor"], "timeout": 5 }
    ]
  }
}
`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, note := Status(home, dataDir, ToolCursor); got == StateInjected {
		t.Fatalf("a user entry was mistaken for Rakko's (%s)", note)
	}
	if err := Connect(home, dataDir, exe, ToolCursor); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "echo agent-hook-cursor.cmd") ||
		!strings.Contains(string(body), `C:\\Tools\\other.exe`) {
		t.Fatalf("connect removed a user's entry:\n%s", body)
	}
	if got, note := Status(home, dataDir, ToolCursor); got != StateInjected {
		t.Fatalf("Rakko's entry was not installed (%s)", note)
	}
	if err := Disconnect(home, dataDir, ToolCursor); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "echo agent-hook-cursor.cmd") ||
		!strings.Contains(string(body), `C:\\Tools\\other.exe`) {
		t.Fatalf("disconnect removed a user's entry:\n%s", body)
	}
	// Rakko's entry embeds the data directory; the user's mentions only the
	// file name.
	if strings.Contains(string(body), dataDir) {
		t.Fatalf("disconnect left Rakko's entry behind:\n%s", body)
	}
}

// A data directory ending in a backslash keeps it: the shim quotes the
// argument by the native command-line rules.
func TestShimKeepsTrailingBackslash(t *testing.T) {
	exe := helperExe(t)
	base := filepath.Join(t.TempDir(), "data dir")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	dataDir := base + `\`
	if err := writeShim(dataDir, hookCompletion, ToolGrok, exe,
		rakkoArgs(hookCompletion, ToolGrok, dataDir, "")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(shimPath(base, hookCompletion, ToolGrok))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"`+base+`\\"`) {
		t.Fatalf("the trailing backslash was not doubled:\n%s", raw)
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd.exe /d /s /c " +
		cmdInvocation(shimPath(base, hookCompletion, ToolGrok))}
	cmd.Stdin = strings.NewReader(`{}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("shim: %v (%s)", err, out)
	}
	var got struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if n := len(got.Args); n < 5 || got.Args[n-1] != dataDir {
		t.Fatalf("data directory reached the app as %q", got.Args)
	}
}

// The shim's own text is UTF-8 and it switches the console to UTF-8 before
// reading a path, so a Chinese directory works under a Chinese or a US code
// page. The wrapper that sets the page lives at an ASCII path (its own text
// cannot be UTF-8 yet); the Chinese data directory is inside the shim.
func TestShimPathsSurviveConsoleCodePages(t *testing.T) {
	exe := helperExe(t)
	dataDir := filepath.Join(t.TempDir(), "数据 目录")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	shim := filepath.Join(shimDir, shimName(hookCompletion, ToolGrok))
	body := shimBody(hookCompletion, ToolGrok, exe,
		rakkoArgs(hookCompletion, ToolGrok, dataDir, ""))
	if err := os.WriteFile(shim, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s1","prompt":"你好"}`
	for _, page := range []string{"936", "437"} {
		t.Run("cp"+page, func(t *testing.T) {
			wrapper := filepath.Join(shimDir, "run-"+page+".cmd")
			script := "@echo off\r\nchcp " + page + " >nul\r\ncall \"" + shim + "\"\r\nexit /b %errorlevel%\r\n"
			if err := os.WriteFile(wrapper, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("cmd.exe")
			cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd.exe /d /c \"" + wrapper + "\""}
			cmd.Stdin = strings.NewReader(payload)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("shim under code page %s: %v (%s)", page, err, out)
			}
			var got struct {
				Args  []string `json:"args"`
				Stdin string   `json:"stdin"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("output %q: %v", out, err)
			}
			if got.Stdin != payload {
				t.Fatalf("payload changed: %q", got.Stdin)
			}
			if n := len(got.Args); n < 5 || got.Args[n-1] != dataDir {
				t.Fatalf("data directory reached the app as %q", got.Args)
			}
		})
	}
}

// A failed reconnect keeps the shim the tool's configuration still names.
func TestFailedReconnectKeepsTheOldShim(t *testing.T) {
	home := t.TempDir()
	redirectAppData(t, home)
	dataDir := t.TempDir()
	exe := helperExe(t)

	// A shim from an earlier successful connect.
	if err := writeShim(dataDir, hookCompletion, ToolCodex, exe,
		rakkoArgs(hookCompletion, ToolCodex, dataDir, "")); err != nil {
		t.Fatal(err)
	}
	shim := shimPath(dataDir, hookCompletion, ToolCodex)
	raw, err := os.ReadFile(shim)
	if err != nil {
		t.Fatal(err)
	}
	// Codex's configuration is unreadable, so the reconnect fails after the
	// shim was rewritten.
	path := ConfigPath(home, ToolCodex)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks": `), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Connect(home, dataDir, exe, ToolCodex); err == nil {
		t.Fatal("Connect accepted an unreadable configuration")
	}
	if _, err := os.Stat(shim); err != nil {
		t.Fatal("the failed reconnect deleted the shim the configuration names")
	}
	again, err := os.ReadFile(shim)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(raw) {
		t.Fatal("the failed reconnect changed the shim")
	}

	// A failed first connect leaves nothing behind.
	home2 := t.TempDir()
	dataDir2 := t.TempDir()
	path2 := ConfigPath(home2, ToolCodex)
	if err := os.MkdirAll(filepath.Dir(path2), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path2, []byte(`{"hooks": `), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Connect(home2, dataDir2, exe, ToolCodex); err == nil {
		t.Fatal("Connect accepted an unreadable configuration")
	}
	if _, err := os.Stat(shimPath(dataDir2, hookCompletion, ToolCodex)); err == nil {
		t.Fatal("a failed first connect left a shim behind")
	}
}

// The AppData lookups follow the environment, with the profile's default
// spelling only as a fallback.
func TestAppDataPathsFollowTheEnvironment(t *testing.T) {
	home := t.TempDir()
	roaming := filepath.Join(t.TempDir(), "Roaming")
	local := filepath.Join(t.TempDir(), "Local")
	oldRoaming, oldLocal := roamingAppData, localAppData
	roamingAppData = func() string { return roaming }
	localAppData = func() string { return local }
	defer func() { roamingAppData, localAppData = oldRoaming, oldLocal }()

	if got, want := devinConfigPath(home), filepath.Join(roaming, "devin", "config.json"); got != want {
		t.Fatalf("devin config = %q, want %q", got, want)
	}
	paths := hostToolPaths(ToolCursor, home)
	want := []string{filepath.Join(roaming, "Cursor"), filepath.Join(local, "Cursor")}
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("cursor paths = %q, want %q", paths, want)
	}

	roamingAppData = func() string { return "" }
	localAppData = func() string { return "" }
	if got, want := devinConfigPath(home), filepath.Join(home, "AppData", "Roaming", "devin", "config.json"); got != want {
		t.Fatalf("fallback devin config = %q, want %q", got, want)
	}
}

// A data directory with a space or a single quote in it survives the entry
// forms that quote a path: Status sees Rakko's entry, a second connect adds
// nothing, and disconnect removes both the entry and the shim.
func TestQuotedShimPathsOwnTheEntry(t *testing.T) {
	exe := helperExe(t)
	for _, tc := range []struct {
		name string
		dir  string
	}{
		{"space", "data dir"},
		{"quote", "data's dir"},
		{"both", "First Last's data"},
	} {
		for _, tool := range []Tool{ToolCursor, ToolGrok} {
			t.Run(tc.name+"/"+string(tool), func(t *testing.T) {
				home := t.TempDir()
				redirectAppData(t, home)
				dataDir := filepath.Join(t.TempDir(), tc.dir)
				if err := os.MkdirAll(dataDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := Connect(home, dataDir, exe, tool); err != nil {
					t.Fatalf("connect: %v", err)
				}
				entry := hookEntryFor(exe, tool, dataDir, "").fields[0].value
				if !slices.Contains(expectedShellCommands(dataDir, tool, hookCompletion), entry) {
					t.Fatalf("the generated entry is not one Rakko expects: %q", entry)
				}
				if got, note := Status(home, dataDir, tool); got != StateInjected {
					t.Fatalf("status = %v (%s)", got, note)
				}
				path := ConfigPath(home, tool)
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := Connect(home, dataDir, exe, tool); err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(before) != string(after) {
					t.Fatalf("a second connect duplicated the entry:\n%s\n---\n%s", before, after)
				}
				if err := Disconnect(home, dataDir, tool); err != nil {
					t.Fatal(err)
				}
				if got, _ := Status(home, dataDir, tool); got == StateInjected {
					t.Fatal("disconnect left the entry in place")
				}
				if _, err := os.Stat(shimPath(dataDir, hookCompletion, tool)); err == nil {
					t.Fatal("disconnect left the shim behind")
				}
			})
		}
	}
}

// Ownership is exact: only the forms Rakko generates match, and text that
// merely contains a shim's name — even without a space, so cmd's redirect
// and chaining syntax could hide it — is somebody else's command.
func TestOwnershipIsExact(t *testing.T) {
	dataDir := filepath.Join(`C:\Users\First Last`, `data's dir`)
	shim := shimPath(dataDir, hookCompletion, ToolCursor)
	accepted := expectedShellCommands(dataDir, ToolCursor, hookCompletion)
	want := []string{
		`""` + shim + `""`,
		`"` + shim + `"`,
		`& '` + strings.ReplaceAll(shim, `'`, `''`) + `'`,
	}
	if !slices.Equal(accepted, want) {
		t.Fatalf("expected forms = %q, want %q", accepted, want)
	}
	// Near misses: every one of these mentions the shim but is not one of
	// Rakko's command lines.
	rejected := []string{
		`echo>` + shim, // cmd redirect, no space
		`x&` + shim,    // cmd chaining, no space
		`echo ` + shim,
		`& '` + shim + `' extra`,
		`& '` + shim + `'; echo hi`,
		`""` + shim + `"" & calc`,
		shim, // the bare path is never written
		`& "` + shim + `"`,
		`"` + shim + `" extra`,
		`"` + shim + `"` + `"` + shim + `"`,
		`""` + shim + `""""`,
		`'` + shim + `''`,
		`& ''` + shim + `''`,
		`&& '` + shim + `'`,
		`& '` + shim + `"`,
		`  ` + `""` + shim + `""  `, // surrounding space is not ours
		`""` + "C:\\other\\agent-hook-cursor.cmd" + `""`,
	}
	for _, value := range rejected {
		if slices.Contains(accepted, value) {
			t.Errorf("a near miss equals an accepted form: %q", value)
		}
		m := map[string]any{"command": value}
		if entryOwned(m, ToolCursor, hookCompletion, dataDir) {
			t.Errorf("accepted %q as Rakko's own command", value)
		}
	}
	for _, value := range accepted {
		m := map[string]any{"command": value}
		if !entryOwned(m, ToolCursor, hookCompletion, dataDir) {
			t.Errorf("rejected the generated form %q", value)
		}
	}
	// The same shim name in another data directory is not ours either.
	other := map[string]any{"command": `""` + shimPath(`C:\elsewhere`, hookCompletion, ToolCursor) + `""`}
	if entryOwned(other, ToolCursor, hookCompletion, dataDir) {
		t.Error("accepted a shim path from another data directory")
	}
}

// Under a Chinese console code page, PowerShell runs the shim the way
// Cursor, Gemini CLI, Qwen Code and Codex do: the payload reaches the app
// byte for byte, and the ASCII decision the tool reads comes back unchanged.
func TestShimUnderPowerShellWithACodePage(t *testing.T) {
	exe := helperExe(t)
	dataDir := filepath.Join(t.TempDir(), "数据 目录")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeShim(dataDir, hookApproval, ToolCursor, exe,
		rakkoArgs(hookApproval, ToolCursor, dataDir, "")); err != nil {
		t.Fatal(err)
	}
	entry := approveEntryFor(exe, ToolCursor, dataDir).fields[0].value
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"echo 你好"}}`
	received := filepath.Join(t.TempDir(), "stdin.bin")

	run := func(env ...string) []byte {
		t.Helper()
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive",
			"-ExecutionPolicy", "Bypass", "-Command", "chcp 936 | Out-Null; "+entry)
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("powershell: %v (%s)", err, out)
		}
		return out
	}

	out := run("RAKKO_HELPER_STDIN_FILE="+received, "RAKKO_HELPER_ASCII=1")
	got, err := os.ReadFile(received)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("the payload changed in flight:\n got %q\nwant %q", got, payload)
	}
	if decision := strings.TrimSpace(string(out)); decision != `{"decision":"allow"}` {
		t.Fatalf("the decision reached the tool as %q", decision)
	}

	// Without the ASCII switch the helper prints a JSON document containing
	// non-ASCII text; this is where PowerShell's own console decoding shows
	// up, and it is why the real decision is ASCII.
	raw := run()
	t.Logf("non-ASCII stdout through PowerShell under cp936: %q", raw)
}

// unescapeJSON turns \uXXXX escapes into the characters they stand for, so a
// test can see the decision as it would have been without the Windows rule.
func unescapeJSON(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) || s[i+1] != 'u' || i+5 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		var r rune
		if _, err := fmt.Sscanf(s[i+2:i+6], "%04x", &r); err != nil {
			b.WriteByte(s[i])
			continue
		}
		i += 5
		if r >= 0xD800 && r <= 0xDBFF && i+6 < len(s) && s[i+1] == '\\' && s[i+2] == 'u' {
			var lo rune
			if _, err := fmt.Sscanf(s[i+3:i+7], "%04x", &lo); err == nil {
				b.WriteRune(0x10000 + (r-0xD800)<<10 + (lo - 0xDC00))
				i += 6
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// A deny reason typed in Chinese must reach the tool intact through the
// console that runs the hook. Comparing the bytes PowerShell delivers for
// the raw UTF-8 JSON and for the \uXXXX-escaped one shows which survives; the
// escaped form is what Rakko emits on Windows.
func TestDecisionReasonSurvivesPowerShellUnderACodePage(t *testing.T) {
	exe := helperExe(t)
	dataDir := filepath.Join(t.TempDir(), "数据 目录")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeShim(dataDir, hookApproval, ToolCursor, exe,
		rakkoArgs(hookApproval, ToolCursor, dataDir, "")); err != nil {
		t.Fatal(err)
	}
	entry := approveEntryFor(exe, ToolCursor, dataDir).fields[0].value

	reason := `拒绝：这条命令会删除文件 "构建目录"`
	escaped := ApproveOutput(ToolClaude, DecisionDeny, reason)
	raw := unescapeJSON(t, escaped)
	if escaped == raw {
		t.Fatal("the Windows decision is not escaped")
	}
	if !isASCII(escaped) {
		t.Fatalf("the escaped decision still holds non-ASCII: %q", escaped)
	}
	var decoded struct {
		HookSpecificOutput struct {
			Decision struct {
				Message string `json:"message"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(escaped), &decoded); err != nil {
		t.Fatalf("the escaped decision is not JSON: %v", err)
	}
	if decoded.HookSpecificOutput.Decision.Message != reason {
		t.Fatalf("a JSON parser reads back %q, want %q",
			decoded.HookSpecificOutput.Decision.Message, reason)
	}

	say := func(payload string) string {
		t.Helper()
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive",
			"-ExecutionPolicy", "Bypass", "-Command", "chcp 936 | Out-Null; "+entry)
		cmd.Env = append(os.Environ(), "RAKKO_HELPER_SAY="+payload)
		cmd.Stdin = strings.NewReader(`{}`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("powershell: %v (%s)", err, out)
		}
		return string(out)
	}
	gotRaw := say(raw)
	gotEscaped := say(escaped)
	t.Logf("raw UTF-8 decision delivered as %q", gotRaw)
	if gotEscaped != escaped {
		t.Fatalf("the escaped decision was re-encoded:\n got %q\nwant %q", gotEscaped, escaped)
	}
	if gotRaw == raw {
		t.Logf("this console passed the raw UTF-8 through unchanged")
	} else {
		t.Logf("the raw UTF-8 decision was re-encoded (mojibake for the tool)")
	}
}
