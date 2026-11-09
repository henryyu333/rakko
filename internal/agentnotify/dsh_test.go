package agentnotify

// DeepSeek Harness installs itself through a home-level YAML patch layer,
// so these tests cover the layer's bytes as carefully as the modules: the
// user's file must come back exactly as it was, a document Rakko cannot
// read is never rewritten, and the modules answer dsh's own events.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dshTestPaths(t *testing.T, home string) (patch, completion, approval string) {
	t.Helper()
	t.Setenv("DSH_HOME", "")
	return dshPatchPath(home), dshModulePath(home, dshEntryCompletion), dshModulePath(home, dshEntryApproval)
}

func TestDshInstalled(t *testing.T) {
	t.Setenv("DSH_HOME", "")
	home := testHome(t)
	if Installed(home, ToolDsh) {
		t.Fatal("an empty home counted as a dsh install")
	}
	if err := os.MkdirAll(filepath.Join(home, ".dsh", "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !Installed(home, ToolDsh) {
		t.Fatal("dsh's own profiles directory was not detected")
	}
}

// TestDshConnectRoundTrip: switching on writes two modules and two entries
// into the user's layer; switching off puts the layer back byte for byte.
func TestDshConnectRoundTrip(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	patch, completion, approval := dshTestPaths(t, home)
	user := "# my patch layer\n- id: ui-theme\n  name: \"@deepseek-ai/dsh-client-ui-theme\"\n  config:\n    preference: system\n"
	writeFile(t, patch, user)

	if err := Connect(home, dataDir, exePath, ToolDsh); err != nil {
		t.Fatal(err)
	}
	body := readFile(t, patch)
	if !strings.HasPrefix(body, user) {
		t.Fatalf("the user's layer was rewritten:\n%s", body)
	}
	if !strings.Contains(body, dshPatchMarker(dshEntryCompletion)) ||
		!strings.Contains(body, "      name: '"+completion+"'") {
		t.Fatalf("the completion entry is missing:\n%s", body)
	}
	module := readFile(t, completion)
	if !strings.Contains(module, "session/event") || !strings.Contains(module, "--agent-hook") {
		t.Fatalf("completion module looks wrong:\n%s", module)
	}
	if got, _ := Status(home, ToolDsh); got != StateInjected {
		t.Fatalf("status = %v, want injected", got)
	}

	if err := ConnectApprove(home, dataDir, exePath, ToolDsh); err != nil {
		t.Fatal(err)
	}
	body = readFile(t, patch)
	if !strings.Contains(body, dshPatchMarker(dshEntryApproval)) ||
		!strings.Contains(body, "      name: '"+approval+"'") {
		t.Fatalf("the approval entry is missing:\n%s", body)
	}
	if got, _ := StatusApprove(home, ToolDsh); got != StateInjected {
		t.Fatalf("approval status = %v, want injected", got)
	}
	if readFile(t, approval) == "" {
		t.Fatal("approval module is empty")
	}

	// A second connect is a no-op.
	before := readFile(t, patch)
	if err := Connect(home, dataDir, exePath, ToolDsh); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, patch); after != before {
		t.Fatalf("a second connect changed the layer:\n%s", after)
	}

	if err := DisconnectApprove(home, dataDir, ToolDsh); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(approval); !os.IsNotExist(err) {
		t.Fatalf("approval module survived: %v", err)
	}
	if got, _ := StatusApprove(home, ToolDsh); got != StateNotInjected {
		t.Fatalf("approval status = %v, want not injected", got)
	}
	if err := Disconnect(home, dataDir, ToolDsh); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, patch); after != user {
		t.Fatalf("the layer did not come back:\n%s", after)
	}
	if _, err := os.Stat(completion); !os.IsNotExist(err) {
		t.Fatalf("completion module survived: %v", err)
	}
}

// TestDshCreatesAndRemovesItsLayer: a layer Rakko made goes away again;
// the empty flow sequence and a missing final newline both come back.
func TestDshCreatesAndRemovesItsLayer(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	patch, _, _ := dshTestPaths(t, home)

	if err := Connect(home, dataDir, exePath, ToolDsh); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(patch); err != nil {
		t.Fatalf("the layer was not created: %v", err)
	}
	if backups, _ := filepath.Glob(filepath.Join(home, ".dsh", "*.rakko-backup-*")); len(backups) > 0 {
		t.Fatalf("Rakko backed up its own layer: %v", backups)
	}
	if err := Disconnect(home, dataDir, ToolDsh); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(patch); !os.IsNotExist(err) {
		t.Fatalf("the layer Rakko created survived: %v", err)
	}

	for _, original := range []string{"[]\n", "# keep me\n[]\n", "- id: a\n  name: x"} {
		writeFile(t, patch, original)
		if err := Connect(home, dataDir, exePath, ToolDsh); err != nil {
			t.Fatalf("%q: %v", original, err)
		}
		if err := Disconnect(home, dataDir, ToolDsh); err != nil {
			t.Fatalf("%q: %v", original, err)
		}
		if after := readFile(t, patch); after != original {
			t.Fatalf("layer %q became %q", original, after)
		}
	}
}

// TestDshTwoSurfacesOnEmptyFlow: a layer that held only "[]" takes one block
// per surface. Switching off one keeps the layer a list — the "[]" must not
// come back beside the other block — and switching off the second restores
// the original bytes, whichever order the two switches go off in.
func TestDshTwoSurfacesOnEmptyFlow(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	patch, _, _ := dshTestPaths(t, home)
	for _, original := range []string{"[]\n", "# keep me\n[]\n"} {
		for _, first := range []string{"completion", "approval"} {
			writeFile(t, patch, original)
			if err := Connect(home, dataDir, exePath, ToolDsh); err != nil {
				t.Fatalf("%q/%s first: connect: %v", original, first, err)
			}
			if err := ConnectApprove(home, dataDir, exePath, ToolDsh); err != nil {
				t.Fatalf("%q/%s first: connect approve: %v", original, first, err)
			}
			// Both blocks are in the file and the token is gone.
			if body := readFile(t, patch); strings.Contains(body, "[]") {
				t.Fatalf("%q: the empty sequence survived both connects:\n%s", original, body)
			}
			if first == "completion" {
				if err := Disconnect(home, dataDir, ToolDsh); err != nil {
					t.Fatalf("%q: disconnect completion: %v", original, err)
				}
			} else {
				if err := DisconnectApprove(home, dataDir, ToolDsh); err != nil {
					t.Fatalf("%q: disconnect approval: %v", original, err)
				}
			}
			// The other surface is still installed: the layer holds its block
			// and nothing that would make it invalid YAML.
			body := readFile(t, patch)
			if strings.Contains(body, "[]") {
				t.Fatalf("%q/%s first: \"[]\" came back beside the other block:\n%s", original, first, body)
			}
			want := dshPatchMarker(dshEntryApproval)
			if first == "approval" {
				want = dshPatchMarker(dshEntryCompletion)
			}
			if !strings.Contains(body, want) {
				t.Fatalf("%q/%s first: the other block is gone:\n%s", original, first, body)
			}
			if got, _ := Status(home, ToolDsh); first == "approval" && got != StateInjected {
				t.Fatalf("%q/%s first: status = %v, want injected", original, first, got)
			}
			// The second switch takes the last block away; the layer is the
			// user's again, byte for byte.
			if first == "completion" {
				if err := DisconnectApprove(home, dataDir, ToolDsh); err != nil {
					t.Fatalf("%q: disconnect approval: %v", original, err)
				}
			} else {
				if err := Disconnect(home, dataDir, ToolDsh); err != nil {
					t.Fatalf("%q: disconnect completion: %v", original, err)
				}
			}
			if after := readFile(t, patch); after != original {
				t.Fatalf("%q/%s first: layer became %q", original, first, after)
			}
		}
	}
}

// TestDshRefusesAnUnknownDocument: a layer that is not a top-level list is
// not guessed at, and no module is left behind.
func TestDshRefusesAnUnknownDocument(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	patch, completion, _ := dshTestPaths(t, home)
	user := "id: ui-theme\nname: something\n"
	writeFile(t, patch, user)
	if err := Connect(home, dataDir, exePath, ToolDsh); err == nil {
		t.Fatal("connect accepted a document that is not a patch list")
	}
	if after := readFile(t, patch); after != user {
		t.Fatalf("the refused document changed:\n%s", after)
	}
	if _, err := os.Stat(completion); !os.IsNotExist(err) {
		t.Fatalf("a refused connect left a module: %v", err)
	}
}

// TestDshConnectMergesAWriteFromAnotherProgram: dsh or the user rewrites
// the layer while Rakko edits it; the edit is recomputed on those bytes.
func TestDshConnectMergesAWriteFromAnotherProgram(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	patch, _, _ := dshTestPaths(t, home)
	writeFile(t, patch, "- id: first\n  name: a\n")

	wrote := false
	stubBeforeReplace(t, func(path string) {
		if path != patch || wrote {
			return
		}
		wrote = true
		body := readFile(t, path) + "- id: second\n  name: b\n"
		writeFile(t, path, body)
	})
	if err := Connect(home, dataDir, exePath, ToolDsh); err != nil {
		t.Fatalf("connect lost the race instead of recomputing: %v", err)
	}
	if !wrote {
		t.Fatal("the test's write never ran, so no conflict was exercised")
	}
	body := readFile(t, patch)
	for _, want := range []string{"- id: first", "- id: second", dshPatchMarker(dshEntryCompletion)} {
		if !strings.Contains(body, want) {
			t.Fatalf("%q is missing after the merge:\n%s", want, body)
		}
	}
	if got, _ := Status(home, ToolDsh); got != StateInjected {
		t.Fatalf("status = %v, want injected", got)
	}
}

// dshHarness renders one module and a fake Rakko binary that records its
// stdin and answers like the app: deny "rm -rf", allow the rest.
func dshHarness(t *testing.T, approve bool, driver string) (dir, record string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir = t.TempDir()
	record = filepath.Join(dir, "payloads")
	exe := filepath.Join(dir, "fake-rakko")
	script := "#!/bin/sh\npayload=$(cat)\nprintf '%s\\n' \"$payload\" >> " + shellQuote(record) + "\n" +
		"case \"$payload\" in\n  *\"rm -rf\"*) printf '%s' '{\"decision\":\"deny\",\"message\":\"stop\"}' ;;\n" +
		"  *) printf '%s' '{\"decision\":\"allow\"}' ;;\nesac\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s, _ := specFor(ToolDsh)
	body := renderModule(s.module, ToolDsh, exe, "")
	if approve {
		body = renderApproveModule(s.approve.module, ToolDsh, exe, "")
	}
	if err := os.WriteFile(filepath.Join(dir, "module.mjs"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.mjs"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, record
}

// TestDshCompletionModule: a completed turn announces with its prompt, a
// subagent session and a non-completed turn do not.
func TestDshCompletionModule(t *testing.T) {
	dir, events := dshHarness(t, false, `
import mod from "./module.mjs";
const handlers = {};
mod({ on(name, fn) { (handlers[name] ||= []).push(fn); } });
const fire = (name, ...args) => (handlers[name] || []).forEach((fn) => fn(...args));
const session = { id: "s1", header: { cwd: "/tmp/proj" } };
fire("session/event", session, { type: "user/message", data: { role: "user", source: { kind: "user" }, content: [{ type: "text", text: "Fix the tests\nsecond line" }] } });
fire("session/event", session, { type: "user/message", data: { role: "user", source: { kind: "agent-instructions" }, content: [{ type: "text", text: "<system-reminder>workspace instructions" }] } });
fire("session/event", session, { type: "user/message", data: { role: "user", source: { kind: "runtime-context" }, content: [{ type: "text", text: "Current runtime context." }] } });
fire("session/event", session, { type: "turn/end", data: { turn: 1, reason: { kind: "aborted" } } });
fire("session/event", session, { type: "turn/end", data: { turn: 2, reason: { kind: "completed" } } });
const sub = { id: "s2", header: { cwd: "/tmp/proj", origin: "subagent" } };
fire("session/event", sub, { type: "user/message", data: { content: [{ type: "text", text: "delegated" }] } });
fire("session/event", sub, { type: "turn/end", data: { turn: 1, reason: { kind: "completed" } } });
`)
	runNodeDriver(t, dir)
	lines := waitEvents(t, events, 1)
	time.Sleep(200 * time.Millisecond)
	again, _ := os.ReadFile(events)
	if strings.Count(string(again), "hook_event_name") != 1 {
		t.Fatalf("aborted or subagent turns announced:\n%s", again)
	}
	h := decodeHook(t, lines[0])
	if h.Event != "session_stop" || h.Session != "s1" || h.Cwd != "/tmp/proj" || h.Prompt != "Fix the tests\nsecond line" {
		t.Fatalf("event = %+v", h)
	}
}

// TestDshApproveModule: dsh's waterfall decision follows Rakko's answer,
// and the tool call's arguments reach the app.
func TestDshApproveModule(t *testing.T) {
	dir, record := dshHarness(t, true, `
import mod from "./module.mjs";
const handlers = {};
mod({ on(name, fn) { handlers[name] = fn; } });
const session = { id: "s1", header: { cwd: "/tmp/proj" } };
handlers["session/event"](session, { type: "tool/call", data: { callId: "c1", name: "bash", arguments: JSON.stringify({ command: "rm -rf /tmp/x", sandbox_permissions: "danger-full-access" }) } });
handlers["session/event"](session, { type: "tool/call", data: { callId: "c2", name: "bash", arguments: JSON.stringify({ command: "ls -la" }) } });
const request = (callId) => ({ agent: { session }, toolName: "bash", callId });
const denied = await handlers["approval/request"](request("c1"), async () => "unavailable");
const allowed = await handlers["approval/request"](request("c2"), async () => "unavailable");
console.log(JSON.stringify({ denied, allowed }));
`)
	out := runNodeDriver(t, dir)
	if !strings.Contains(out, `"denied":"rejected","allowed":"allowed-once"`) {
		t.Fatalf("module decisions = %s", out)
	}
	lines := waitEvents(t, record, 2)
	var first struct {
		Tool  string         `json:"tool_name"`
		Input map[string]any `json:"tool_input"`
		Cwd   string         `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Tool != "bash" || first.Cwd != "/tmp/proj" || first.Input["command"] != "rm -rf /tmp/x" ||
		first.Input["sandbox_permissions"] != "danger-full-access" {
		t.Fatalf("approval payload = %+v", first)
	}
}

// TestDshApproveModuleFallsBack: without an app the module calls next(),
// leaving the decision to dsh's own channel.
func TestDshApproveModuleFallsBack(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	s, _ := specFor(ToolDsh)
	body := renderApproveModule(s.approve.module, ToolDsh, "/nonexistent/Rakko", "")
	if err := os.WriteFile(filepath.Join(dir, "module.mjs"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import mod from "./module.mjs";
const handlers = {};
mod({ on(name, fn) { handlers[name] = fn; } });
let next = false;
const outcome = await handlers["approval/request"](
  { agent: { session: { id: "s1", header: {} } }, toolName: "bash" },
  async () => { next = true; return "unavailable"; });
console.log(JSON.stringify({ outcome, next }));
`
	if err := os.WriteFile(filepath.Join(dir, "driver.mjs"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runNodeDriver(t, dir)
	if !strings.Contains(out, `"outcome":"unavailable","next":true`) {
		t.Fatalf("missing app did not fall back to dsh: %s", out)
	}
}
