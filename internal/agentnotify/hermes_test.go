package agentnotify

// Hermes is the one tool whose installation is a plugin directory plus a few
// lines in a YAML config. These checks cover the round trip (the config comes
// back byte for byte), the refusal to edit a config another program already
// configured, and the detection rule that keeps a missing Hermes out of the
// settings list.

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const hermesUserConfig = `_config_version: 52
plugins:
  enabled:
    - agent-importers
  disabled: []
model:
  default: deepseek-v4.1-flash
`

func hermesHomeWithConfig(t *testing.T) (home, dataDir, path string) {
	t.Helper()
	home, dataDir = testHome(t), testDataDir(t)
	path = hermesConfigPath(home)
	writeFile(t, path, hermesUserConfig)
	return home, dataDir, path
}

func assertHermesConfig(t *testing.T, raw string) {
	t.Helper()
	hasEnabled, err := hermesHasEnabled([]byte(raw), hermesPluginName)
	if err != nil || !hasEnabled {
		t.Fatalf("plugins.enabled missing (%v):\n%s", err, raw)
	}
	hasTransport, err := hermesHasTransport([]byte(raw))
	if err != nil || !hasTransport {
		t.Fatalf("approval transport missing (%v):\n%s", err, raw)
	}
}

func TestHermesApprovalPayloadAndOutput(t *testing.T) {
	payload := `{"hook_event_name":"pre_approval_request","session_id":"s1","tool_name":"terminal","tool_input":{"command":"rm -rf /tmp/rakko-demo","description":"Clean the temporary directory"}}`
	ask, ok := extractAsk(ToolHermes, []byte(payload))
	if !ok || ask.Session != "s1" || ask.ToolName != "terminal" || !strings.Contains(string(ask.Input), "rm -rf") {
		t.Fatalf("extractAsk = %+v ok=%v", ask, ok)
	}
	an, ok := Analyze(ToolHermes, ask.ToolName, ask.Input)
	if !ok || !NeedsHuman(ToolHermes, an) {
		t.Fatalf("Hermes approval was filtered out: %+v ok=%v", an, ok)
	}
	if an.Description != "Clean the temporary directory" || an.Detail.Command != "rm -rf /tmp/rakko-demo" {
		t.Fatalf("analysis = %+v", an)
	}
	if got := ApproveOutput(ToolHermes, DecisionAllow, ""); got != `{"decision":"allow"}` {
		t.Fatalf("allow output = %s", got)
	}
	if got := ApproveOutput(ToolHermes, DecisionDeny, "no"); !strings.Contains(got, `"decision":"deny"`) || !strings.Contains(got, `"message":"no"`) {
		t.Fatalf("deny output = %s", got)
	}
	if got := ApproveOutput(ToolHermes, DecisionPass, ""); got != "" {
		t.Fatalf("pass output = %q", got)
	}
}

func TestHermesPluginRunsInPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	home, dataDir := testHome(t), testDataDir(t)
	dir := hermesPluginDir(home)
	events := filepath.Join(dir, "hook-events")
	approvePayload := filepath.Join(dir, "approve-payload")
	exe := filepath.Join(dir, "fake-rakko")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--agent-approve\" ]; then\n" +
		"  cat > " + shellQuote(approvePayload) + "\n" +
		"  [ \"$RAKKO_TEST_SILENT\" = \"1\" ] && exit 0\n" +
		"  [ \"$RAKKO_TEST_DENY\" = \"1\" ] && { printf '%s' '{\"decision\":\"deny\",\"message\":\"no\"}'; exit 0; }\n" +
		"  printf '%s' '{\"decision\":\"allow\"}'\n" +
		"  exit 0\n" +
		"fi\n" +
		"cat >> " + shellQuote(events) + "\n"
	writeFile(t, exe, script)
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeHermesPlugin(home, dataDir, exe); err != nil {
		t.Fatal(err)
	}
	if err := writeHermesState(home, hermesState{Completion: true, Approval: true}); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "driver.py")
	writeFile(t, driver, `
import importlib.util, json
spec = importlib.util.spec_from_file_location("rakko_hermes", "__init__.py")
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
hooks, transports = {}, {}
class Ctx:
    def register_hook(self, name, fn): hooks[name] = fn
    def register_approval_transport(self, name, fn): transports[name] = fn
mod.register(Ctx())
hooks["post_llm_call"](session_id="s1", user_message="Fix the parser")
class Request:
    request_id = "r1"
    command = "rm -rf /tmp/rakko-demo"
    description = "Clean the temporary directory"
    timeout_seconds = 300
    allowed_choices = ("once", "deny")
    def respond(self, choice): return {"choice": choice}
print(json.dumps({"transport": transports["rakko"](Request())}))
`)
	cmd := exec.Command(python, driver)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin driver: %v\n%s", err, out)
	} else if !strings.Contains(string(out), `"choice": "once"`) {
		t.Fatalf("transport returned %s", out)
	}

	// No answer from Rakko (the app is not running, or its 45 s card
	// expired): the plugin must raise, which hands the prompt back to
	// Hermes's own flow. A "deny" here would silently block the command.
	silent := filepath.Join(dir, "silent.py")
	writeFile(t, silent, `
import importlib.util, json
spec = importlib.util.spec_from_file_location("rakko_hermes", "__init__.py")
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
transports = {}
class Ctx:
    def register_hook(self, name, fn): pass
    def register_approval_transport(self, name, fn): transports[name] = fn
mod.register(Ctx())
class Request:
    request_id = "r2"
    command = "rm -rf /tmp/rakko-demo"
    description = "Clean the temporary directory"
    timeout_seconds = 300
    allowed_choices = ("once", "deny")
    def respond(self, choice): return {"choice": choice}
try:
    result = transports["rakko"](Request())
except RuntimeError:
    print("fallback")
else:
    print(json.dumps(result))
`)
	silentCmd := exec.Command(python, silent)
	silentCmd.Dir = dir
	silentCmd.Env = append(os.Environ(), "RAKKO_TEST_SILENT=1")
	if out, err := silentCmd.CombinedOutput(); err != nil {
		t.Fatalf("silent driver: %v\n%s", err, out)
	} else if !strings.Contains(string(out), "fallback") {
		t.Fatalf("a silent Rakko did not hand back to Hermes: %s", out)
	}

	denyCmd := exec.Command(python, silent)
	denyCmd.Dir = dir
	denyCmd.Env = append(os.Environ(), "RAKKO_TEST_DENY=1")
	if out, err := denyCmd.CombinedOutput(); err != nil {
		t.Fatalf("deny driver: %v\n%s", err, out)
	} else if !strings.Contains(string(out), `"choice": "deny"`) {
		t.Fatalf("deny was not passed through: %s", out)
	}

	var hook recordedHook
	deadline := time.Now().Add(3 * time.Second)
	for {
		body, err := os.ReadFile(events)
		if err == nil && strings.Contains(string(body), "hook_event_name") {
			if err := json.Unmarshal([]byte(strings.TrimSpace(string(body))), &hook); err != nil {
				t.Fatalf("hook payload: %v\n%s", err, body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the plugin never announced the turn: %v %s", err, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if hook.Event != "post_llm_call" || hook.Session != "s1" || hook.Prompt != "Fix the parser" {
		t.Fatalf("hook = %+v", hook)
	}
	var ask struct {
		ToolName string `json:"tool_name"`
		Input    struct {
			Command     string `json:"command"`
			Description string `json:"description"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal([]byte(readFile(t, approvePayload)), &ask); err != nil {
		t.Fatal(err)
	}
	if ask.ToolName != "terminal" || ask.Input.Command != "rm -rf /tmp/rakko-demo" ||
		ask.Input.Description != "Clean the temporary directory" {
		t.Fatalf("approval payload = %+v", ask)
	}
}

func TestHermesConnectRoundTrip(t *testing.T) {
	home, dataDir, path := hermesHomeWithConfig(t)
	if !Installed(home, ToolHermes) {
		t.Fatal("Hermes with a config.yaml is not detected")
	}

	if err := Connect(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if st, note := Status(home, ToolHermes); st != StateInjected {
		t.Fatalf("completion status = %v %s", st, note)
	}
	cfg := readFile(t, path)
	if !strings.Contains(cfg, "    - rakko\n") {
		t.Fatalf("plugins.enabled does not list rakko:\n%s", cfg)
	}
	init := readFile(t, hermesInitPath(home))
	for _, want := range []string{moduleMarker, `--agent-hook`, `"` + exePath + `"`, "register_approval_transport"} {
		if !strings.Contains(init, want) {
			t.Fatalf("plugin source lacks %q:\n%s", want, init)
		}
	}
	if manifest := readFile(t, hermesManifestPath(home)); !strings.Contains(manifest, "name: rakko") {
		t.Fatalf("plugin.yaml:\n%s", manifest)
	}

	if err := ConnectApprove(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if st, note := StatusApprove(home, ToolHermes); st != StateInjected {
		t.Fatalf("approval status = %v %s", st, note)
	}
	cfg = readFile(t, path)
	for _, want := range []string{
		"security:\n",
		"  approval:\n",
		"    transport: rakko\n",
		"    transport_fallback: builtin\n",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config lacks %q:\n%s", want, cfg)
		}
	}
	assertHermesConfig(t, cfg)

	// The two switches are independent: approval off leaves completion on.
	if err := DisconnectApprove(home, dataDir, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if st, _ := StatusApprove(home, ToolHermes); st != StateNotInjected {
		t.Fatalf("approval survived its switch: %v", st)
	}
	if st, _ := Status(home, ToolHermes); st != StateInjected {
		t.Fatalf("completion went with the approval switch: %v", st)
	}
	cfg = readFile(t, path)
	if strings.Contains(cfg, "security:") || strings.Contains(cfg, "transport: rakko") {
		t.Fatalf("approval config survived:\n%s", cfg)
	}

	if err := Disconnect(home, dataDir, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if st, _ := Status(home, ToolHermes); st != StateNotInjected {
		t.Fatalf("completion survived its switch: %v", st)
	}
	if after := readFile(t, path); after != hermesUserConfig {
		t.Fatalf("config is not byte for byte what the user had:\n%s", after)
	}
	if _, err := os.Stat(hermesPluginDir(home)); !os.IsNotExist(err) {
		t.Fatalf("the plugin directory survived: %v", err)
	}
	if strings.Contains(readFile(t, path), hermesPluginName) {
		t.Fatal("a Rakko line is still in config.yaml")
	}
}

func TestHermesApproveAloneKeepsThePlugin(t *testing.T) {
	home, dataDir, path := hermesHomeWithConfig(t)
	if err := ConnectApprove(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if st, _ := StatusApprove(home, ToolHermes); st != StateInjected {
		t.Fatalf("approval status = %v", st)
	}
	if st, _ := Status(home, ToolHermes); st != StateNotInjected {
		t.Fatalf("completion is on without its switch: %v", st)
	}
	if err := DisconnectApprove(home, dataDir, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, path); after != hermesUserConfig {
		t.Fatalf("config changed:\n%s", after)
	}
	if _, err := os.Stat(hermesPluginDir(home)); !os.IsNotExist(err) {
		t.Fatalf("plugin directory survived: %v", err)
	}
}

func TestHermesRefusesAConfiguredApproval(t *testing.T) {
	home, dataDir, path := hermesHomeWithConfig(t)
	user := hermesUserConfig + "security:\n  approval:\n    transport: phone\n"
	writeFile(t, path, user)
	if err := ConnectApprove(home, dataDir, exePath, ToolHermes); err == nil {
		t.Fatal("ConnectApprove overwrote another transport")
	}
	if after := readFile(t, path); after != user {
		t.Fatalf("the user's security block changed:\n%s", after)
	}
	if _, err := os.Stat(hermesPluginDir(home)); !os.IsNotExist(err) {
		t.Fatalf("a refused connect still wrote the plugin: %v", err)
	}
}

func TestHermesRefusesAFlowEnabledList(t *testing.T) {
	home, dataDir, path := hermesHomeWithConfig(t)
	user := "plugins:\n  enabled: [agent-importers]\n"
	writeFile(t, path, user)
	if err := Connect(home, dataDir, exePath, ToolHermes); err == nil {
		t.Fatal("Connect edited a flow list")
	}
	if after := readFile(t, path); after != user {
		t.Fatalf("config changed:\n%s", after)
	}
}

func TestHermesMergesAWriteFromAnotherProgram(t *testing.T) {
	home, dataDir, path := hermesHomeWithConfig(t)
	wrote := false
	stubBeforeReplace(t, func(p string) {
		if filepath.Base(p) != "config.yaml" || wrote {
			return
		}
		wrote = true
		writeFile(t, p, hermesUserConfig+"known_plugin_toolsets:\n  cli:\n    - a2a\n")
	})
	if err := Connect(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatalf("connect lost the race: %v", err)
	}
	cfg := readFile(t, path)
	if !strings.Contains(cfg, "known_plugin_toolsets:") {
		t.Fatalf("the other program's write was overwritten:\n%s", cfg)
	}
	if !strings.Contains(cfg, "    - rakko\n") {
		t.Fatalf("Rakko's entry is missing:\n%s", cfg)
	}
}

func TestHermesNotInstalledIsInvisible(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	if Installed(home, ToolHermes) {
		t.Fatal("an empty home counts as Hermes")
	}
	if st, _ := Status(home, ToolHermes); st != StateNotInjected {
		t.Fatalf("status = %v", st)
	}
	if err := Connect(home, dataDir, exePath, ToolHermes); err == nil {
		t.Fatal("Connect created a Hermes config from nothing")
	}
	if _, err := os.Stat(filepath.Join(home, ".hermes")); !os.IsNotExist(err) {
		t.Fatalf("Connect created ~/.hermes: %v", err)
	}
}

func TestHermesMigratesEnabledAndSecurityBlocks(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	path := hermesConfigPath(home)
	user := "model:\n  default: x\n"
	writeFile(t, path, user)
	if err := Connect(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if err := ConnectApprove(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	assertHermesConfig(t, readFile(t, path))
	if err := DisconnectApprove(home, dataDir, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if err := Disconnect(home, dataDir, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, path); after != user {
		t.Fatalf("created blocks were not removed:\n%s", after)
	}
}

func TestHermesReloadNudge(t *testing.T) {
	// The unix socket path is limited to about 104 bytes on macOS; a short
	// directory keeps room for it.
	home, err := os.MkdirTemp("", "rk-h-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HERMES_HOME", "")
	socket := join(home, ".hermes", "gateway.sock")
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, lerr := net.Listen("unix", socket)
	if lerr != nil {
		t.Fatal(lerr)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		got <- string(buf[:n])
		_, _ = conn.Write([]byte(`{"ok":true,"result":{"reloaded":true}}` + "\n"))
	}()
	nudgeHermesReload(home)
	var req struct {
		Verb     string `json:"verb"`
		Protocol int    `json:"protocol"`
		Params   struct {
			Home string `json:"home"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(<-got), &req); err != nil {
		t.Fatal(err)
	}
	if req.Verb != "reload-plugins" || req.Protocol != 1 || req.Params.Home != join(home, ".hermes") {
		t.Fatalf("reload request = %+v", req)
	}
}

func TestHermesKeepsAnotherApprovalKey(t *testing.T) {
	home, dataDir, path := hermesHomeWithConfig(t)
	if err := Connect(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if err := ConnectApprove(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	cfg := readFile(t, path)
	cfg = strings.Replace(cfg, "    transport_fallback: builtin\n",
		"    transport_fallback: builtin\n    note: mine\n", 1)
	writeFile(t, path, cfg)
	if err := DisconnectApprove(home, dataDir, ToolHermes); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)
	if !strings.Contains(after, "    note: mine\n") {
		t.Fatalf("the user's approval key went away:\n%s", after)
	}
	if strings.Contains(after, "transport: rakko") || strings.Contains(after, "transport_fallback") {
		t.Fatalf("Rakko's keys survived:\n%s", after)
	}
	if !strings.Contains(after, "- rakko\n") {
		t.Fatalf("the completion entry went with the approval keys:\n%s", after)
	}
}

func TestHermesRefusesToRemoveAChangedTransport(t *testing.T) {
	home, dataDir, path := hermesHomeWithConfig(t)
	if err := ConnectApprove(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(readFile(t, path), "    transport: rakko\n", "    transport: phone\n", 1)
	writeFile(t, path, cfg)
	if err := DisconnectApprove(home, dataDir, ToolHermes); err == nil {
		t.Fatal("DisconnectApprove removed a transport another program changed")
	}
	if after := readFile(t, path); after != cfg {
		t.Fatalf("the changed config was overwritten:\n%s", after)
	}
	if st, _ := StatusApprove(home, ToolHermes); st == StateInjected {
		t.Fatal("the row still says injected after the transport changed")
	}
}

func TestHermesHomeOverride(t *testing.T) {
	home := t.TempDir()
	custom := join(home, "profiles", "work")
	t.Setenv("HERMES_HOME", custom)
	writeFile(t, join(custom, "config.yaml"), hermesUserConfig)
	if got := ConfigPath(home, ToolHermes); got != join(custom, "config.yaml") {
		t.Fatalf("config path = %s", got)
	}
	if !Installed(home, ToolHermes) {
		t.Fatal("the overridden profile is not detected")
	}
	if err := Connect(home, testDataDir(t), exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(join(custom, "plugins", "rakko", "__init__.py")); err != nil {
		t.Fatalf("plugin did not follow HERMES_HOME: %v", err)
	}
}

// TestHermesConfigParsesWithHermesYAML runs Hermes's own YAML reader over the
// file Rakko edits, so a textual edit that only Rakko's scanner accepts does
// not pass as usable.
func TestHermesConfigParsesWithHermesYAML(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if bundled, _ := filepath.Glob(filepath.Join(userHome, ".hermes", "tools", "python-*", "bin", "python3")); len(bundled) > 0 {
		python = bundled[len(bundled)-1]
	}
	agentDir := filepath.Join(userHome, ".hermes", "hermes-agent")
	if _, err := os.Stat(filepath.Join(agentDir, "hermes_yaml.py")); err != nil {
		t.Skip("Hermes's YAML reader is not installed")
	}
	// Hermes keeps ruamel in its uv cache; put the archive on PYTHONPATH so
	// hermes_yaml imports with the same reader Hermes uses.
	archives, _ := filepath.Glob(filepath.Join(userHome, ".hermes", "cache", "uv", "archive-v0", "*", "ruamel"))
	if len(archives) == 0 {
		t.Skip("Hermes's YAML dependencies are not installed")
	}
	pythonPath := filepath.Dir(archives[0])
	home, dataDir, path := hermesHomeWithConfig(t)
	if err := Connect(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if err := ConnectApprove(home, dataDir, exePath, ToolHermes); err != nil {
		t.Fatal(err)
	}
	check := func(script string) {
		t.Helper()
		cmd := exec.Command(python, "-c", script, agentDir, path)
		cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Hermes's reader rejected the config: %v\n%s", err, out)
		}
	}
	check(`
import sys
sys.path.insert(0, sys.argv[1])
import hermes_yaml
doc = hermes_yaml.safe_load(open(sys.argv[2], encoding="utf-8"))
assert "rakko" in doc["plugins"]["enabled"], doc.get("plugins")
approval = doc["security"]["approval"]
assert approval["transport"] == "rakko", approval
assert approval["transport_fallback"] == "builtin", approval
`)
	if err := DisconnectApprove(home, dataDir, ToolHermes); err != nil {
		t.Fatal(err)
	}
	if err := Disconnect(home, dataDir, ToolHermes); err != nil {
		t.Fatal(err)
	}
	check(`
import sys
sys.path.insert(0, sys.argv[1])
import hermes_yaml
doc = hermes_yaml.safe_load(open(sys.argv[2], encoding="utf-8"))
assert "rakko" not in doc["plugins"]["enabled"], doc.get("plugins")
assert "security" not in doc, doc.get("security")
assert doc["model"]["default"] == "deepseek-v4.1-flash", doc.get("model")
`)
}

func TestHermesConnectAndDisconnectAreRepeatable(t *testing.T) {
	home, dataDir, path := hermesHomeWithConfig(t)
	for range 2 {
		if err := Connect(home, dataDir, exePath, ToolHermes); err != nil {
			t.Fatal(err)
		}
		if err := ConnectApprove(home, dataDir, exePath, ToolHermes); err != nil {
			t.Fatal(err)
		}
	}
	cfg := readFile(t, path)
	if strings.Count(cfg, "- rakko\n") != 1 || strings.Count(cfg, "security:\n") != 1 {
		t.Fatalf("a second connect duplicated its lines:\n%s", cfg)
	}
	for range 2 {
		if err := DisconnectApprove(home, dataDir, ToolHermes); err != nil {
			t.Fatal(err)
		}
		if err := Disconnect(home, dataDir, ToolHermes); err != nil {
			t.Fatal(err)
		}
	}
	if after := readFile(t, path); after != hermesUserConfig {
		t.Fatalf("repeated disconnects changed the config:\n%s", after)
	}
}
