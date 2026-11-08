package agentnotify

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, data)
	}
	return doc
}

func TestApproveMarkerDistinct(t *testing.T) {
	for _, tool := range Tools() {
		c, a := marker(tool), approveMarker(tool)
		if strings.Contains(c, a) || strings.Contains(a, c) {
			t.Fatalf("%s: completion marker %q and approval marker %q overlap", tool, c, a)
		}
	}
}

func TestApproveTimeouts(t *testing.T) {
	want := int(approveBudget.Seconds()) + 5
	n := 0
	for _, tool := range Tools() {
		s, _ := specFor(tool)
		if s.approve == nil {
			continue
		}
		n++
		if s.approve.timeout < want {
			t.Errorf("%s: approval timeout %ds, want at least %ds", tool, s.approve.timeout, want)
		}
		if s.approve.event == "" {
			t.Errorf("%s: no approval event", tool)
		}
	}
	if n != 9 {
		t.Fatalf("%d approvable tools, want 9 (claude, codex, copilot, devin, antigravity, opencode, pi, qwen, droid)", n)
	}
}

func TestApproveRefusesUnsupported(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	for _, tool := range []Tool{ToolCursor, ToolGemini, ToolGrok, ToolKiro, ToolOmp, ToolAugment} {
		if tool.Approvable() {
			t.Errorf("%s should not be approvable", tool)
		}
		if err := ConnectApprove(home, data, "/tmp/Rakko.app/Contents/MacOS/Rakko", tool); err == nil {
			t.Errorf("%s: ConnectApprove succeeded", tool)
		}
	}
	if err := ConnectApprove(home, data, "/tmp/Rakko.app/Contents/MacOS/Rakko", ToolPi); err != nil {
		t.Fatalf("pi: %v", err)
	}
	path := filepath.Join(home, ".pi", "agent", "extensions", "rakko-agent-approval.ts")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "tool_call") || !strings.Contains(string(body), "--agent-approve") {
		t.Fatalf("pi approval module looks wrong:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions", "rakko-agent-completion.ts")); !os.IsNotExist(err) {
		t.Fatalf("connecting approval wrote the completion module too")
	}
}

func TestApproveConnectIndependent(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	exe := "/Applications/Rakko.app/Contents/MacOS/Rakko"

	// The user's own entry must survive everything.
	claudePath := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(claudePath), 0o700)
	userDoc := `{"hooks":{"PermissionRequest":[{"hooks":[{"type":"command","command":"my-own-hook"}]}]}}`
	if err := os.WriteFile(claudePath, []byte(userDoc), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Connect(home, data, exe, ToolClaude); err != nil {
		t.Fatal(err)
	}
	if err := ConnectApprove(home, data, exe, ToolClaude); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(claudePath)
	if !strings.Contains(string(raw), "--agent-hook --tool claude") || !strings.Contains(string(raw), "--agent-approve --tool claude") ||
		!strings.Contains(string(raw), "my-own-hook") {
		t.Fatalf("both entries and the user's should be there:\n%s", raw)
	}
	if st, note := StatusApprove(home, ToolClaude); st != StateInjected {
		t.Fatalf("approve status %v %s", st, note)
	}
	if err := Disconnect(home, data, ToolClaude); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(claudePath)
	if strings.Contains(string(raw), "--agent-hook --tool claude") {
		t.Fatalf("completion entry survived Disconnect:\n%s", raw)
	}
	if !strings.Contains(string(raw), "--agent-approve --tool claude") || !strings.Contains(string(raw), "my-own-hook") {
		t.Fatalf("Disconnect removed the approval entry or the user's:\n%s", raw)
	}
	if st, _ := StatusApprove(home, ToolClaude); st != StateInjected {
		t.Fatalf("approve status after Disconnect %v", st)
	}
	if err := Connect(home, data, exe, ToolClaude); err != nil {
		t.Fatal(err)
	}
	if err := DisconnectApprove(home, data, ToolClaude); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(claudePath)
	if strings.Contains(string(raw), "--agent-approve --tool claude") {
		t.Fatalf("approval entry survived DisconnectApprove:\n%s", raw)
	}
	if !strings.Contains(string(raw), "--agent-hook --tool claude") || !strings.Contains(string(raw), "my-own-hook") {
		t.Fatalf("DisconnectApprove removed the completion entry or the user's:\n%s", raw)
	}

	// Copilot's two files.
	if err := Connect(home, data, exe, ToolCopilot); err != nil {
		t.Fatal(err)
	}
	if err := ConnectApprove(home, data, exe, ToolCopilot); err != nil {
		t.Fatal(err)
	}
	completion := filepath.Join(home, ".copilot", "hooks", "rakko-agent-completion.json")
	approval := filepath.Join(home, ".copilot", "hooks", "rakko-agent-approval.json")
	if _, err := os.Stat(completion); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(approval); err != nil {
		t.Fatal(err)
	}
	if err := Disconnect(home, data, ToolCopilot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(completion); !os.IsNotExist(err) {
		t.Fatalf("completion file survived")
	}
	if _, err := os.Stat(approval); err != nil {
		t.Fatalf("Disconnect removed the approval file: %v", err)
	}
	if err := Connect(home, data, exe, ToolCopilot); err != nil {
		t.Fatal(err)
	}
	if err := DisconnectApprove(home, data, ToolCopilot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(approval); !os.IsNotExist(err) {
		t.Fatalf("approval file survived DisconnectApprove")
	}
	if _, err := os.Stat(completion); err != nil {
		t.Fatalf("DisconnectApprove removed the completion file: %v", err)
	}
}

func TestApproveNamedMemberIndependent(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	exe := "/Applications/Rakko.app/Contents/MacOS/Rakko"
	if err := Connect(home, data, exe, ToolAntigravity); err != nil {
		t.Fatal(err)
	}
	if err := ConnectApprove(home, data, exe, ToolAntigravity); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".gemini", "config", "hooks.json")
	doc := readJSONFile(t, path)
	if doc["rakko-agent-completion"] == nil || doc["rakko-agent-approval"] == nil {
		t.Fatalf("both members should be present: %v", doc)
	}
	if err := DisconnectApprove(home, data, ToolAntigravity); err != nil {
		t.Fatal(err)
	}
	doc = readJSONFile(t, path)
	if doc["rakko-agent-approval"] != nil {
		t.Fatalf("approval member survived")
	}
	if doc["rakko-agent-completion"] == nil {
		t.Fatalf("DisconnectApprove removed the completion member")
	}
	if err := ConnectApprove(home, data, exe, ToolAntigravity); err != nil {
		t.Fatal(err)
	}
	if err := Disconnect(home, data, ToolAntigravity); err != nil {
		t.Fatal(err)
	}
	doc = readJSONFile(t, path)
	if doc["rakko-agent-completion"] != nil {
		t.Fatalf("completion member survived Disconnect")
	}
	if doc["rakko-agent-approval"] == nil {
		t.Fatalf("Disconnect removed the approval member")
	}
}

func TestApproveDroidBareFile(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	exe := "/Applications/Rakko.app/Contents/MacOS/Rakko"
	path := filepath.Join(home, ".factory", "hooks.json")
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Connect(home, data, exe, ToolFactory); err != nil {
		t.Fatal(err)
	}
	if err := ConnectApprove(home, data, exe, ToolFactory); err != nil {
		t.Fatal(err)
	}
	doc := readJSONFile(t, path)
	if doc["UserPromptSubmit"] == nil || doc["PreToolUse"] == nil {
		t.Fatalf("both bare event arrays should be present: %v", doc)
	}
	if err := DisconnectApprove(home, data, ToolFactory); err != nil {
		t.Fatal(err)
	}
	doc = readJSONFile(t, path)
	if doc["PreToolUse"] != nil {
		t.Fatalf("approval event survived: %v", doc)
	}
	if doc["UserPromptSubmit"] == nil {
		t.Fatalf("completion event was removed")
	}
}

func TestApproveOutputs(t *testing.T) {
	const reason = "Denied by the user in Rakko."
	claudeAllow := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	claudeDeny := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"` + reason + `"}}}`
	droidAllow := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"Approved in Rakko"}}`
	droidDeny := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + reason + `"}}`
	antigravityAllow := `{"decision":"auto_approve"}`
	antigravityDeny := `{"decision":"deny","reason":"` + reason + `"}`
	cases := []struct {
		tool              Tool
		allow, deny, pass string
	}{
		{ToolClaude, claudeAllow, claudeDeny, ""},
		{ToolCodex, claudeAllow, claudeDeny, ""},
		{ToolQwen, claudeAllow, claudeDeny, ""},
		{ToolDevin, `{"decision":"approve"}`, `{"decision":"block","reason":"` + reason + `"}`, ""},
		{ToolCopilot, `{"behavior":"allow"}`, `{"behavior":"deny","message":"` + reason + `"}`, ""},
		{ToolFactory, droidAllow, droidDeny, ""},
		{ToolAntigravity, antigravityAllow, antigravityDeny, `{"decision":"allow"}`},
		{ToolOpencode, `{"decision":"allow"}`, `{"decision":"deny","message":"` + reason + `"}`, ""},
		{ToolPi, `{"decision":"allow"}`, `{"decision":"deny","message":"` + reason + `"}`, ""},
	}
	for _, tc := range cases {
		if got := ApproveOutput(tc.tool, DecisionAllow, ""); got != tc.allow {
			t.Errorf("%s allow = %q, want %q", tc.tool, got, tc.allow)
		}
		if got := ApproveOutput(tc.tool, DecisionDeny, ""); got != tc.deny {
			t.Errorf("%s deny = %q, want %q", tc.tool, got, tc.deny)
		}
		if got := ApproveOutput(tc.tool, DecisionPass, ""); got != tc.pass {
			t.Errorf("%s pass = %q, want %q", tc.tool, got, tc.pass)
		}
	}
}

func TestDenyReasonField(t *testing.T) {
	if got := ApproveOutput(ToolClaude, DecisionDeny, " 先别删,\n留着我排查 "); !strings.Contains(got, `"message":"先别删, 留着我排查"`) {
		t.Fatalf("reason not flattened: %s", got)
	}
	long := strings.Repeat("长", 600)
	got := ApproveOutput(ToolDevin, DecisionDeny, long)
	var d struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(got), &d); err != nil {
		t.Fatal(err)
	}
	if len([]rune(d.Reason)) != 500 {
		t.Fatalf("reason %d runes, want 500", len([]rune(d.Reason)))
	}
	if got := ApproveOutput(ToolCopilot, DecisionDeny, ""); !strings.Contains(got, approveDefaultReason) {
		t.Fatalf("empty reason did not fall back: %s", got)
	}
	if strings.Contains(ApproveOutput(ToolClaude, DecisionAllow, "ignored"), "ignored") {
		t.Fatal("allow carried a message")
	}
}

func TestApproveFallbackCommand(t *testing.T) {
	exe := "/tmp/rakko-approve-missing/Rakko.app/Contents/MacOS/Rakko"
	claude := ApproveCommand(exe, ToolClaude, "")
	if strings.Contains(claude, "else") {
		t.Fatalf("claude command grew an else branch: %s", claude)
	}
	anti := ApproveCommand(exe, ToolAntigravity, "")
	if !strings.Contains(anti, `printf '%s' '{"decision":"allow"}'`) {
		t.Fatalf("antigravity fallback missing: %s", anti)
	}
	out, err := exec.Command("/bin/sh", "-c", anti).Output()
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	if string(out) != `{"decision":"allow"}` {
		t.Fatalf("fallback printed %q", out)
	}
	out, err = exec.Command("/bin/sh", "-c", claude).Output()
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("claude fallback printed %q, want nothing", out)
	}
	for _, tool := range []Tool{ToolClaude, ToolCopilot, ToolAntigravity, ToolFactory, ToolOpencode, ToolPi} {
		cmd := ApproveCommand(exe, tool, "/tmp/data dir")
		if !strings.Contains(cmd, "--agent-approve --tool "+string(tool)) {
			t.Errorf("%s command missing marker: %s", tool, cmd)
		}
		if !strings.Contains(cmd, "'/tmp/data dir'") {
			t.Errorf("%s data dir not quoted: %s", tool, cmd)
		}
	}
}

// A fake listener that is not this binary must never be believed: the
// client has to leave without waiting for a decision, even though the
// socket exists and accepts the connection.
func TestApprovalForeignServerRejected(t *testing.T) {
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("nc is not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "rakko-foreign-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, SocketName)
	server := exec.Command("/bin/sh", "-c", "sleep 5 | nc -lU "+shellQuote(sock)+" >/dev/null")
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Process.Kill(); _, _ = server.Process.Wait() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake listener never made its socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	payload := `{"session_id":"foreign","cwd":"/tmp","hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`
	var out strings.Builder
	start := time.Now()
	handled := RunApproveCLI([]string{"--agent-approve", "--tool", "claude", "--data-dir", dir},
		strings.NewReader(payload), &out)
	elapsed := time.Since(start)
	if !handled {
		t.Fatal("the client mode was not handled")
	}
	if out.Len() != 0 {
		t.Fatalf("a foreign server got an answer printed: %q", out.String())
	}
	if elapsed > time.Second {
		t.Fatalf("the client waited %v on a foreign server", elapsed)
	}
}

func TestExtractAsk(t *testing.T) {
	cases := []struct {
		tool string
		in   string
		name string
		sess string
		want string
	}{
		{"claude", `{"session_id":"s1","cwd":"/p","hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf x"}}`,
			"Bash", "s1", "rm -rf x"},
		{"codex", `{"session_id":"s2","tool_name":"Bash","tool_input":{"command":["bash","-lc","echo hi"]}}`,
			"Bash", "s2", `["bash","-lc","echo hi"]`},
		{"copilot", `{"sessionId":"s3","toolName":"shell","toolArgs":"{\"command\":\"ls\"}"}`,
			"shell", "s3", "ls"},
		{"antigravity", `{"conversationId":"s4","toolCall":{"name":"run_command","args":{"CommandLine":"npm install"}}}`,
			"run_command", "s4", "npm install"},
		{"opencode", `{"action":"edit","resources":["/tmp/a.go"],"sessionID":"s5"}`,
			"edit", "s5", "/tmp/a.go"},
		{"pi", `{"toolName":"bash","input":{"command":"rm -rf x"},"sessionID":"s6"}`,
			"bash", "s6", "rm -rf x"},
	}
	for _, tc := range cases {
		a, ok := extractAsk(Tool(tc.tool), []byte(tc.in))
		if !ok {
			t.Fatalf("%s: extract failed", tc.tool)
		}
		if a.ToolName != tc.name || a.Session != tc.sess {
			t.Errorf("%s: name %q session %q, want %q %q", tc.tool, a.ToolName, a.Session, tc.name, tc.sess)
		}
		if !strings.Contains(string(a.Input), tc.want) {
			t.Errorf("%s: input %s does not name %q", tc.tool, a.Input, tc.want)
		}
		if !validID(a.ID) {
			t.Errorf("%s: bad id %q", tc.tool, a.ID)
		}
	}
}

// The entries Rakko writes must match each tool's documented shape. This
// reads the files back rather than trusting the writer's intent.
func TestApproveEntryShapes(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	exe := "/Applications/Rakko.app/Contents/MacOS/Rakko"
	for _, tool := range []Tool{ToolClaude, ToolCodex, ToolQwen, ToolDevin, ToolCopilot,
		ToolFactory, ToolAntigravity, ToolOpencode, ToolPi} {
		if err := ConnectApprove(home, data, exe, tool); err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
	}
	commandIn := func(v any) string {
		switch x := v.(type) {
		case []any:
			if len(x) == 0 {
				return ""
			}
			item, _ := x[0].(map[string]any)
			if inner, ok := item["hooks"].([]any); ok && len(inner) > 0 {
				h, _ := inner[0].(map[string]any)
				s, _ := h["command"].(string)
				return s
			}
			if s, ok := item["command"].(string); ok {
				return s
			}
			s, _ := item["bash"].(string)
			return s
		}
		return ""
	}
	for _, tc := range []struct {
		tool  Tool
		event string
	}{
		{ToolClaude, "PermissionRequest"}, {ToolCodex, "PermissionRequest"},
		{ToolQwen, "PermissionRequest"}, {ToolDevin, "PermissionRequest"},
		{ToolFactory, "PreToolUse"},
	} {
		doc := readJSONFile(t, ApproveConfigPath(home, tc.tool))
		hooks, _ := doc["hooks"].(map[string]any)
		if tc.tool == ToolFactory {
			hooks = doc // Droid's hooks.json is bare when it exists
			if _, ok := doc["PreToolUse"]; !ok {
				hooks, _ = doc["hooks"].(map[string]any)
			}
		}
		cmd := commandIn(hooks[tc.event])
		if !strings.Contains(cmd, "--agent-approve --tool "+string(tc.tool)) {
			t.Errorf("%s %s command %q", tc.tool, tc.event, cmd)
		}
	}

	copilot := readJSONFile(t, ApproveConfigPath(home, ToolCopilot))
	chooks, _ := copilot["hooks"].(map[string]any)
	if cmd := commandIn(chooks["permissionRequest"]); !strings.Contains(cmd, "--agent-approve --tool copilot") {
		t.Fatalf("copilot command %q", cmd)
	}
	anti := readJSONFile(t, ApproveConfigPath(home, ToolAntigravity))
	member, _ := anti["rakko-agent-approval"].(map[string]any)
	if cmd := commandIn(member["PreToolUse"]); !strings.Contains(cmd, "--agent-approve --tool antigravity") {
		t.Fatalf("antigravity command %q", cmd)
	}

	for _, tool := range []Tool{ToolOpencode, ToolPi} {
		body, err := os.ReadFile(ApproveConfigPath(home, tool))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, "--agent-approve") {
			t.Errorf("%s module missing the client call", tool)
		}
		if tool == ToolOpencode && (!strings.Contains(text, "Plugin.define") || !strings.Contains(text, `ctx.permission.hook("evaluate"`)) {
			t.Errorf("opencode module is not the V2 plugin shape")
		}
		if tool == ToolPi && !strings.Contains(text, `pi.on("tool_call"`) {
			t.Errorf("pi module does not hook tool_call")
		}
	}
}
