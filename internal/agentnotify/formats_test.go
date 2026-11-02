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
	srv, err := Listen(dataDir, func(ev Event) { got <- ev })
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
