package agentnotify

// Behavioral checks for the three module tools: each rendered module is run
// by the real Node runtime against a fake Rakko binary that records the JSON
// it receives. The event sequences are the ones each host documents.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordedHook is what the fake binary received.
type recordedHook struct {
	Event   string `json:"hook_event_name"`
	Session string `json:"session_id"`
	Cwd     string `json:"cwd"`
	Prompt  string `json:"prompt"`
}

func decodeHook(t *testing.T, line string) recordedHook {
	t.Helper()
	var h recordedHook
	if err := json.Unmarshal([]byte(line), &h); err != nil {
		t.Fatalf("%q is not one hook payload: %v", line, err)
	}
	return h
}

// moduleHarness writes a fake Rakko binary that appends each hook payload as
// one line to events, the rendered module as module.mjs, and driver.mjs.
func moduleHarness(t *testing.T, tool Tool, driver string) (dir, events string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir = t.TempDir()
	events = filepath.Join(dir, "events")
	exe := filepath.Join(dir, "fake-rakko")
	// One O_APPEND write per payload: detach: true lets two completions run
	// at once, so a cat plus a printf could interleave.
	script := "#!/bin/sh\npayload=$(cat)\nprintf '%s\\n' \"$payload\" >> " + shellQuote(events) + "\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s, _ := specFor(tool)
	body := renderModule(s.module, tool, exe, "")
	if err := os.WriteFile(filepath.Join(dir, "module.mjs"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.mjs"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, events
}

func runNodeDriver(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("node", "driver.mjs")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node driver: %v\n%s", err, out)
	}
	return string(out)
}

// waitEvents waits until path holds want newline-terminated records.
func waitEvents(t *testing.T, path string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		body, _ := os.ReadFile(path)
		text := strings.TrimSpace(string(body))
		var lines []string
		if text != "" {
			lines = strings.Split(text, "\n")
		}
		if len(lines) == want {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s holds %d events, want %d:\n%s", path, len(lines), want, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// omp reports through session_stop only: a subagent never announces, and
// the handler returns nothing so omp reads no decision from it.
func TestOmpModuleUsesSessionStop(t *testing.T) {
	dir, events := moduleHarness(t, ToolOmp, `
import mod from "./module.mjs";
const seen = [];
const handlers = {};
mod({ on(name, fn) { seen.push(name); handlers[name] = fn; } });
handlers["before_agent_start"]({ prompt: "Fix the subagent bug\nsecond line" });
const returned = handlers["session_stop"]({}, { cwd: "/tmp/proj" });
console.log(JSON.stringify({ seen, returned: returned === undefined ? "undefined" : typeof returned }));
if (!seen.includes("session_stop") || seen.includes("agent_end")) process.exit(3);
`)
	out := runNodeDriver(t, dir)
	if !strings.Contains(out, `"returned":"undefined"`) {
		t.Fatalf("session_stop returned something:\n%s", out)
	}
	lines := waitEvents(t, events, 1)
	h := decodeHook(t, lines[0])
	if h.Event != "session_stop" || h.Cwd != "/tmp/proj" || h.Prompt != "Fix the subagent bug\nsecond line" {
		t.Fatalf("event = %+v", h)
	}
	body := readFile(t, filepath.Join(dir, "module.mjs"))
	if strings.Contains(body, `pi.on("agent_end"`) {
		t.Fatal("the omp module still listens to agent_end")
	}
}

// pi keeps agent_end but ignores an end that is only waiting for the
// session's own background work.
func TestPiModuleWaitsForAsyncWork(t *testing.T) {
	dir, events := moduleHarness(t, ToolPi, `
import mod from "./module.mjs";
const handlers = {};
mod({ on(name, fn) { handlers[name] = fn; } });
handlers["before_agent_start"]({ prompt: "Ship it" });
handlers["agent_end"]({ awaitingAsyncWork: true }, { cwd: "/tmp/proj" });
wait(120).then(() => {
  handlers["agent_end"]({ awaitingAsyncWork: false }, { cwd: "/tmp/proj" });
});
function wait(ms) { return new Promise(r => setTimeout(r, ms)); }
`)
	runNodeDriver(t, dir)
	lines := waitEvents(t, events, 1)
	// The paused end must not have produced a second record.
	time.Sleep(200 * time.Millisecond)
	if again, _ := os.ReadFile(events); strings.Count(string(again), "hook_event_name") != 1 {
		t.Fatalf("a paused agent_end announced as finished:\n%s", again)
	}
	if h := decodeHook(t, lines[0]); h.Event != "agent_end" || h.Prompt != "Ship it" || h.Cwd != "/tmp/proj" {
		t.Fatalf("event = %+v", h)
	}
}

// opencode's V2 plugin subscribes to the event stream and names the session
// after the newest prompt, not its first one.
func TestOpencodeModuleNamesTheLatestPrompt(t *testing.T) {
	var turn = func(id, text string) string {
		return `{type:"message.updated", properties:{info:{id:"` + id + `", role:"user", sessionID:"s1"}}},
{type:"message.part.updated", properties:{part:{type:"text", messageID:"` + id + `", text:"` + text + `", sessionID:"s1"}}},
{type:"session.idle", properties:{sessionID:"s1", cwd:"/tmp/proj"}}`
	}
	dir, events := moduleHarness(t, ToolOpencode, `
import mod from "./module.mjs";
const events = [`+turn("m1", "first task")+`,`+turn("m2", "second task")+`];
let cleanup;
const ctx = {
  location: { directory: "/tmp/proj" },
  event: {
    subscribe() {
      return (async function* () { for (const event of events) yield event; })();
    },
  },
};
cleanup = await mod.setup(ctx);
await new Promise((resolve) => setTimeout(resolve, 300));
if (typeof cleanup === "function") cleanup();
`)
	// The module imports @opencode/plugin; the real package is not part of
	// this repository, so the harness supplies the one symbol it uses.
	stub := filepath.Join(dir, "node_modules", "@opencode", "plugin")
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stub, "package.json"),
		[]byte(`{"name":"@opencode/plugin","type":"module","exports":"./index.js"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stub, "index.js"),
		[]byte("export const Plugin = { define: (plugin) => plugin };\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runNodeDriver(t, dir)
	lines := waitEvents(t, events, 2)
	// Detached children may reach the recorder in either order; both turns
	// must name their own prompt.
	prompts := map[string]bool{}
	for _, line := range lines {
		h := decodeHook(t, line)
		if h.Event != "session.idle" || h.Cwd != "/tmp/proj" || h.Session != "s1" {
			t.Fatalf("event = %+v", h)
		}
		prompts[h.Prompt] = true
	}
	if !prompts["first task"] || !prompts["second task"] {
		t.Fatalf("turns named %v, want both prompts", prompts)
	}
}
