package agentnotify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSkillFoldersAreNotInstalls: skill installers drop ~/.<tool>/skills for
// tools that were never installed; such a folder alone must not add a row.
func TestSkillFoldersAreNotInstalls(t *testing.T) {
	home := testHome(t)
	for _, dir := range []string{".claude", ".codex", ".cursor", ".copilot", ".qwen", ".factory", ".kiro", ".augment", ".grok", ".pi/agent", ".omp/agent", ".config/opencode", ".config/devin"} {
		if err := os.MkdirAll(filepath.Join(home, dir, "skills"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range Tools() {
		if Installed(home, tool) {
			t.Errorf("%s counted as installed from a skills folder alone", tool)
		}
	}
}

// TestCursorFlatEntryRoundTrip: Cursor's hooks.json takes flat entries under
// lower-camel events and a "version"; a file Rakko made goes away again.
func TestCursorFlatEntryRoundTrip(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	path := ConfigPath(home, ToolCursor)
	if err := Connect(home, dataDir, exePath, ToolCursor); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int `json:"version"`
		Hooks   map[string][]struct {
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	}
	body, _ := os.ReadFile(path)
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	if doc.Version != 1 || len(doc.Hooks["stop"]) != 1 || len(doc.Hooks["beforeSubmitPrompt"]) != 1 ||
		!strings.Contains(doc.Hooks["stop"][0].Command, "--agent-hook --tool cursor") {
		t.Fatalf("hooks.json = %s", body)
	}
	if err := Disconnect(home, dataDir, ToolCursor); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Rakko's own hooks.json survived the switch: %v", err)
	}

	// A user's file keeps its bytes.
	user := `{"version": 1, "hooks": {"stop": [{"command": "notify.sh"}]}}`
	writeFile(t, path, user)
	if err := Connect(home, dataDir, exePath, ToolCursor); err != nil {
		t.Fatal(err)
	}
	if err := Disconnect(home, dataDir, ToolCursor); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != user {
		t.Fatalf("user's hooks.json changed:\n%s", after)
	}
}

// TestCopilotHookFileNamesItsEvents: Copilot's payloads carry no event name,
// so its file's two commands each pass theirs.
func TestCopilotHookFileNamesItsEvents(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	if err := Connect(home, dataDir, exePath, ToolCopilot); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int `json:"version"`
		Hooks   map[string][]struct {
			Bash string `json:"bash"`
		} `json:"hooks"`
	}
	path := ConfigPath(home, ToolCopilot)
	body, _ := os.ReadFile(path)
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	for _, ev := range []string{"userPromptSubmitted", "agentStop"} {
		if len(doc.Hooks[ev]) != 1 || !strings.Contains(doc.Hooks[ev][0].Bash, "--tool copilot --event "+ev) {
			t.Fatalf("%s entry missing in %s", ev, body)
		}
	}
	if st, _ := Status(home, ToolCopilot); st != StateInjected {
		t.Fatalf("status = %v", st)
	}
	if err := Disconnect(home, dataDir, ToolCopilot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Copilot hook file survived the switch")
	}
}

// TestAntigravityOwnsOneNamedHook: Antigravity's hooks.json is keyed by hook
// name; Rakko adds and removes only its own member.
func TestAntigravityOwnsOneNamedHook(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	path := ConfigPath(home, ToolAntigravity)
	user := "{\n  \"mine\": {\"Stop\": [{\"type\": \"command\", \"command\": \"say done\"}]}\n}\n"
	writeFile(t, path, user)
	if err := Connect(home, dataDir, exePath, ToolAntigravity); err != nil {
		t.Fatal(err)
	}
	if err := Connect(home, dataDir, exePath, ToolAntigravity); err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string][]struct {
		Command string `json:"command"`
	}
	body, _ := os.ReadFile(path)
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	ours := doc[namedHook]["Stop"]
	if len(doc) != 2 || len(ours) != 1 || !strings.Contains(ours[0].Command, "--tool antigravity --event Stop") {
		t.Fatalf("hooks.json = %s", body)
	}
	if st, _ := Status(home, ToolAntigravity); st != StateInjected {
		t.Fatalf("status = %v", st)
	}
	if err := Disconnect(home, dataDir, ToolAntigravity); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != user {
		t.Fatalf("user's hooks.json changed:\n%s", after)
	}
}

// TestForwarderEventArgAndCursorFilters: an --event fills a payload that
// names none; Cursor's aborted loops are not finishes; a Claude entry run
// inside Cursor is left to Cursor's own.
func TestForwarderEventArgAndCursorFilters(t *testing.T) {
	dataDir := testDataDir(t)
	got := make(chan Event, 4)
	srv, err := Listen(dataDir, func(ev Event) { got <- ev }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	next := func() (Event, bool) {
		select {
		case ev := <-got:
			return ev, true
		case <-time.After(400 * time.Millisecond):
			return Event{}, false
		}
	}

	RunHookCLI([]string{"--agent-hook", "--tool", "copilot", "--event", "agentStop", "--data-dir", dataDir},
		strings.NewReader(`{"sessionId":"c1","cwd":"/p"}`))
	if ev, ok := next(); !ok || ev.Event != "agentStop" || ev.Session != "c1" || !DoneEvent(ev.Event) {
		t.Fatalf("copilot event = %+v ok=%v", ev, ok)
	}

	RunHookCLI([]string{"--agent-hook", "--tool", "cursor", "--data-dir", dataDir},
		strings.NewReader(`{"conversation_id":"k1","hook_event_name":"stop","status":"aborted","workspace_roots":["/w"]}`))
	if ev, ok := next(); ok {
		t.Fatalf("an aborted Cursor loop was announced: %+v", ev)
	}
	RunHookCLI([]string{"--agent-hook", "--tool", "cursor", "--data-dir", dataDir},
		strings.NewReader(`{"conversation_id":"k1","hook_event_name":"stop","status":"completed","workspace_roots":["/w"]}`))
	if ev, ok := next(); !ok || ev.Session != "k1" || ev.Cwd != "/w" {
		t.Fatalf("cursor event = %+v ok=%v", ev, ok)
	}

	t.Setenv("CURSOR_VERSION", "1.7.2")
	RunHookCLI([]string{"--agent-hook", "--tool", "claude", "--data-dir", dataDir},
		strings.NewReader(`{"session_id":"k1","hook_event_name":"Stop"}`))
	if ev, ok := next(); ok {
		t.Fatalf("Cursor's run of Claude's hook was announced as Claude: %+v", ev)
	}
}

// TestSymlinkedConfigStaysALink: a settings file kept in a dotfiles
// repository and linked into place keeps its link; the target gets the bytes.
func TestSymlinkedConfigStaysALink(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "dotfiles", "claude-settings.json")
	user := `{"theme": "dark"}`
	writeFile(t, target, user)
	link := ConfigPath(home, ToolClaude)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Connect(home, dataDir, exePath, ToolClaude); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v %v", fi, err)
	}
	if body, _ := os.ReadFile(target); !strings.Contains(string(body), "--agent-hook --tool claude") {
		t.Fatalf("target did not get the entry:\n%s", body)
	}
	if err := Disconnect(home, dataDir, ToolClaude); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(target); string(body) != user {
		t.Fatalf("target after off:\n%s", body)
	}
}

// TestDroidWritesWhereDroidReads: with ~/.factory/hooks.json present Droid
// ignores settings.json hooks, so the entry goes into hooks.json (keyed by
// event at its root); without it, into settings.json, and hooks.json is
// never created.
func TestDroidWritesWhereDroidReads(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	settings := filepath.Join(home, ".factory", "settings.json")
	writeFile(t, settings, `{"hooks": {}}`)
	if err := Connect(home, dataDir, exePath, ToolFactory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".factory", "hooks.json")); !os.IsNotExist(err) {
		t.Fatal("hooks.json was created; it would hide settings.json hooks")
	}
	if body, _ := os.ReadFile(settings); !strings.Contains(string(body), "--agent-hook --tool droid") {
		t.Fatalf("settings.json:\n%s", body)
	}
	if err := Disconnect(home, dataDir, ToolFactory); err != nil {
		t.Fatal(err)
	}

	hooks := filepath.Join(home, ".factory", "hooks.json")
	user := `{"Stop": [{"hooks": [{"type": "command", "command": "say done"}]}]}`
	writeFile(t, hooks, user)
	if err := Connect(home, dataDir, exePath, ToolFactory); err != nil {
		t.Fatal(err)
	}
	var doc map[string][]struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	body, _ := os.ReadFile(hooks)
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	if len(doc["Stop"]) != 2 || len(doc["UserPromptSubmit"]) != 1 {
		t.Fatalf("hooks.json:\n%s", body)
	}
	if st, _ := Status(home, ToolFactory); st != StateInjected {
		t.Fatalf("status = %v", st)
	}
	if err := Disconnect(home, dataDir, ToolFactory); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(hooks); string(after) != user {
		t.Fatalf("hooks.json after off:\n%s", after)
	}
}

// TestOffKeepsAUserMatcherGroup: a hook group that carries the user's own
// key keeps it when Rakko's command leaves.
func TestOffKeepsAUserMatcherGroup(t *testing.T) {
	home, dataDir := testHome(t), testDataDir(t)
	path := ConfigPath(home, ToolClaude)
	cmd := HookCommand(exePath, ToolClaude, "")
	raw, _ := json.Marshal(cmd)
	writeFile(t, path, `{"hooks": {"Stop": [{"matcher": "x", "hooks": [{"type": "command", "command": `+string(raw)+`}]}]}}`)
	if err := Disconnect(home, dataDir, ToolClaude); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(path); !strings.Contains(string(body), `"matcher": "x"`) || strings.Contains(string(body), "--agent-hook") {
		t.Fatalf("after off:\n%s", body)
	}
}

// TestForwarderDropsBorrowedAndInterrupted: Grok runs Cursor's entries too,
// and Auggie's Stop also fires on an interrupt; neither is announced.
func TestForwarderDropsBorrowedAndInterrupted(t *testing.T) {
	dataDir := testDataDir(t)
	got := make(chan Event, 4)
	srv, err := Listen(dataDir, func(ev Event) { got <- ev }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	RunHookCLI([]string{"--agent-hook", "--tool", "auggie", "--data-dir", dataDir},
		strings.NewReader(`{"conversation_id":"a1","hook_event_name":"Stop","agent_stop_cause":"interrupted"}`))
	t.Setenv("GROK_SESSION_ID", "g1")
	RunHookCLI([]string{"--agent-hook", "--tool", "cursor", "--data-dir", dataDir},
		strings.NewReader(`{"conversation_id":"g1","hook_event_name":"stop","status":"completed"}`))
	select {
	case ev := <-got:
		t.Fatalf("announced: %+v", ev)
	case <-time.After(400 * time.Millisecond):
	}
}
