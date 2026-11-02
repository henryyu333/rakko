package agentnotify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// testDataDir is a short data directory: a unix socket path is limited to
// about 104 bytes, and t.TempDir's name is longer than that on macOS.
func testDataDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rakko-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const exePath = "/Applications/Rakko.app/Contents/MacOS/Rakko"

func TestHookCommandGuardsMissingApp(t *testing.T) {
	cmd := HookCommand(exePath, ToolClaude, "")
	if !strings.HasPrefix(cmd, "if [ -x "+exePath+" ]; then ") {
		t.Fatalf("command does not guard on the binary: %q", cmd)
	}
	if !strings.HasSuffix(cmd, "; fi") {
		t.Fatalf("command does not end in fi: %q", cmd)
	}
	if !strings.Contains(cmd, marker(ToolClaude)) {
		t.Fatalf("command lacks the marker: %q", cmd)
	}
	// A deleted app: the shell runs the if, does nothing, and succeeds.
	out, err := exec.Command("/bin/sh", "-c", HookCommand("/nonexistent/Rakko", ToolClaude, "")).CombinedOutput()
	if err != nil {
		t.Fatalf("missing app made the hook fail: %v (%s)", err, out)
	}
	if len(out) != 0 {
		t.Fatalf("missing app wrote output: %q", out)
	}
}

func TestHookCommandPassesStdin(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake rakko")
	got := filepath.Join(dir, "got.json")
	writeFile(t, script, "#!/bin/sh\ncat > "+shellQuote(got)+"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", HookCommand(script, ToolCodex, ""))
	cmd.Stdin = strings.NewReader(`{"session_id":"s1","prompt":"hello"}`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	if body := readFile(t, got); !strings.Contains(body, `"session_id":"s1"`) {
		t.Fatalf("payload did not reach the binary: %q", body)
	}
}

func TestConnectClaudePreservesUserFile(t *testing.T) {
	home := testHome(t)
	dataDir := testDataDir(t)
	path := ConfigPath(home, ToolClaude)
	user := `{
    "theme": "dark",
    "hooks": {
        "Stop": [
            {
                "hooks": [
                    { "type": "command", "command": "say done" }
                ]
            }
        ]
    }
}
`
	writeFile(t, path, user)
	if err := Connect(home, dataDir, exePath, ToolClaude); err != nil {
		t.Fatal(err)
	}
	body := readFile(t, path)
	if !strings.Contains(body, marker(ToolClaude)) {
		t.Fatalf("Rakko's entry is missing:\n%s", body)
	}
	if !strings.Contains(body, "say done") || !strings.Contains(body, `"theme": "dark"`) {
		t.Fatalf("the user's file changed beyond our entry:\n%s", body)
	}
	if got, _ := Status(home, ToolClaude); got != StateInjected {
		t.Fatalf("status = %v, want injected", got)
	}
	// A second connect is a no-op.
	before := readFile(t, path)
	if err := Connect(home, dataDir, exePath, ToolClaude); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, path); after != before {
		t.Fatalf("a second connect changed the file:\n%s", after)
	}
	if err := Disconnect(home, dataDir, ToolClaude); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, path); after != user {
		t.Fatalf("disconnect did not restore the original:\n%s", after)
	}
	if got, _ := Status(home, ToolClaude); got != StateNotInjected {
		t.Fatalf("status after disconnect = %v", got)
	}
}

func TestConnectCreatesFileAndRemovesIt(t *testing.T) {
	home := testHome(t)
	dataDir := testDataDir(t)
	if err := Connect(home, dataDir, exePath, ToolCodex); err != nil {
		t.Fatal(err)
	}
	path := ConfigPath(home, ToolCodex)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("hooks.json was not created: %v", err)
	}
	if got, _ := Status(home, ToolCodex); got != StateInjected {
		t.Fatalf("status = %v", got)
	}
	if err := Disconnect(home, dataDir, ToolCodex); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file Rakko created survived the switch: %v", err)
	}
}

func TestDisconnectRestoresBlankFile(t *testing.T) {
	home := testHome(t)
	dataDir := testDataDir(t)
	path := ConfigPath(home, ToolGemini)
	writeFile(t, path, "")
	if err := Connect(home, dataDir, exePath, ToolGemini); err != nil {
		t.Fatal(err)
	}
	if err := Disconnect(home, dataDir, ToolGemini); err != nil {
		t.Fatal(err)
	}
	if body := readFile(t, path); body != "" {
		t.Fatalf("the empty file came back as %q", body)
	}
}

func TestConnectRefusesBrokenJSON(t *testing.T) {
	home := testHome(t)
	dataDir := testDataDir(t)
	path := ConfigPath(home, ToolClaude)
	broken := `{"hooks": {"Stop": [`
	writeFile(t, path, broken)
	err := Connect(home, dataDir, exePath, ToolClaude)
	if err == nil {
		t.Fatal("connect wrote into a broken file")
	}
	if body := readFile(t, path); body != broken {
		t.Fatalf("broken file changed: %q", body)
	}
	if got, note := Status(home, ToolClaude); got != StateError || note == "" {
		t.Fatalf("status = %v %q, want an error with a reason", got, note)
	}
}

func TestConnectModuleTools(t *testing.T) {
	for _, tool := range []Tool{ToolOpencode, ToolOmp, ToolPi} {
		t.Run(string(tool), func(t *testing.T) {
			home := testHome(t)
			dataDir := testDataDir(t)
			if err := Connect(home, dataDir, exePath, tool); err != nil {
				t.Fatal(err)
			}
			path := ConfigPath(home, tool)
			body := readFile(t, path)
			if !strings.Contains(body, moduleMarker) || !strings.Contains(body, exePath) {
				t.Fatalf("%s module lacks the marker or the binary:\n%s", tool, body)
			}
			if got, _ := Status(home, tool); got != StateInjected {
				t.Fatalf("status = %v", got)
			}
			if err := Disconnect(home, dataDir, tool); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("%s module survived the switch", tool)
			}
		})
	}
}

func TestConnectLeavesForeignFileAlone(t *testing.T) {
	home := testHome(t)
	dataDir := testDataDir(t)
	path := ConfigPath(home, ToolOmp)
	writeFile(t, path, "export default function () {}\n")
	if err := Connect(home, dataDir, exePath, ToolOmp); err == nil {
		t.Fatal("connect replaced a file it did not write")
	}
	if body := readFile(t, path); !strings.Contains(body, "export default function") {
		t.Fatalf("foreign file changed: %q", body)
	}
}

func TestGrokFileIsDedicated(t *testing.T) {
	home := testHome(t)
	dataDir := testDataDir(t)
	path := ConfigPath(home, ToolGrok)
	writeFile(t, path, `{"hooks": {"Stop": []}}`)
	if err := Connect(home, dataDir, exePath, ToolGrok); err == nil {
		t.Fatal("connect wrote into a hooks file that is not Rakko's")
	}
	os.Remove(path)
	if err := Connect(home, dataDir, exePath, ToolGrok); err != nil {
		t.Fatal(err)
	}
	if got, _ := Status(home, ToolGrok); got != StateInjected {
		t.Fatalf("status = %v", got)
	}
	if err := Disconnect(home, dataDir, ToolGrok); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Grok's Rakko file survived the switch")
	}
}

func TestStatusOnlyTouchesKnownTools(t *testing.T) {
	if got, _ := Status(t.TempDir(), Tool("nope")); got != StateUnsupported {
		t.Fatalf("unknown tool = %v, want unsupported", got)
	}
}

func TestForwarderWithoutAppIsSilentAndQuick(t *testing.T) {
	dataDir := testDataDir(t)
	payload := `{"session_id":"abc","hook_event_name":"Stop","cwd":"/tmp/proj"}`
	start := time.Now()
	if handled := RunHookCLI([]string{"--agent-hook", "--tool", "claude", "--data-dir", dataDir}, strings.NewReader(payload)); !handled {
		t.Fatal("the forwarder did not recognise --agent-hook")
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("forwarder took %v without an app running", d)
	} else {
		t.Logf("forwarder without Rakko: %v", d)
	}
}

func TestForwarderDeliversToSocket(t *testing.T) {
	dataDir := testDataDir(t)
	got := make(chan Event, 1)
	srv, err := Listen(dataDir, func(ev Event) { got <- ev })
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	payload := `{"session_id":"sess-1","hook_event_name":"UserPromptSubmit","cwd":"/tmp/proj","prompt":"Fix the tests\nsecond line"}`
	if !RunHookCLI([]string{"--agent-hook", "--tool", "codex", "--data-dir", dataDir}, strings.NewReader(payload)) {
		t.Fatal("not handled")
	}
	select {
	case ev := <-got:
		if ev.Tool != ToolCodex || ev.Session != "sess-1" || ev.Event != "UserPromptSubmit" || ev.Cwd != "/tmp/proj" {
			t.Fatalf("event = %+v", ev)
		}
		if !strings.Contains(ev.Prompt, "Fix the tests") {
			t.Fatalf("prompt lost: %q", ev.Prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
	}
}

func TestForwarderIgnoresClaudeEntryInsideGrok(t *testing.T) {
	t.Setenv("GROK_SESSION_ID", "grok-session")
	dataDir := testDataDir(t)
	got := make(chan Event, 1)
	srv, err := Listen(dataDir, func(ev Event) { got <- ev })
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	RunHookCLI([]string{"--agent-hook", "--tool", "claude", "--data-dir", dataDir},
		strings.NewReader(`{"session_id":"grok-session","hook_event_name":"Stop"}`))
	select {
	case ev := <-got:
		t.Fatalf("Grok's run of Claude's hook was announced as Claude: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
	// The tool's own file still reports it, as Grok.
	RunHookCLI([]string{"--agent-hook", "--tool", "grok", "--data-dir", dataDir},
		strings.NewReader(`{"sessionId":"grok-session","hookEventName":"Stop"}`))
	select {
	case ev := <-got:
		if ev.Tool != ToolGrok || ev.Session != "grok-session" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Grok's own event did not arrive")
	}
}

func TestBuildRequestFallsBackToPid(t *testing.T) {
	ev := buildRequest(ToolPi, []byte(`{"prompt":"x"}`))
	if !strings.HasPrefix(ev.Session, "pid:") {
		t.Fatalf("session = %q, want the harness pid", ev.Session)
	}
}
