//go:build darwin

package main

// The real approval path, with a real socket and a separate hook process:
// the installed command is read back from Claude Code's settings, run
// through /bin/sh with a real PermissionRequest payload, and the card's
// answer must be exactly what the hook prints. Run with
// RAKKO_APPROVE_SMOKE=1. The test binary doubles as the hook client (see
// TestMain), so the client's peer check sees the same executable the server
// runs from.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rakko/internal/agentnotify"
	"rakko/locales"
)

func smokeHookCommand(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, group := range doc.Hooks["PermissionRequest"] {
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "--agent-approve --tool claude") {
				return h.Command
			}
		}
	}
	t.Fatalf("no approval command in %s:\n%s", path, data)
	return ""
}

func waitApprovalCard(t *testing.T, a *shelf, f *approvalFeature) *approval {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.liveNow(time.Now(), true)
		if ap := f.queueHead(); ap != nil {
			return ap
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the approval card never appeared")
	return nil
}

func startHook(t *testing.T, command, payload string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Stdin = strings.NewReader(payload)
	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, out
}

func waitHook(t *testing.T, cmd *exec.Cmd, out *bytes.Buffer) string {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hook exit: %v\n%s", err, out.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the hook did not finish")
	}
	return out.String()
}

func TestApprovalSmoke(t *testing.T) {
	if os.Getenv("RAKKO_APPROVE_SMOKE") != "1" {
		t.Skip("set RAKKO_APPROVE_SMOKE=1 to run the real approval smoke")
	}
	a, home, data := agentShelf(t)
	a.settings.Approval.Enabled = true
	f := a.approvalFeature()
	f.enabled.Store(true)
	a.agentFeature().start()
	t.Cleanup(func() { a.agentFeature().stop() })

	exe := os.Args[0]
	if err := agentnotify.ConnectApprove(home, data, exe, agentnotify.ToolClaude); err != nil {
		t.Fatal(err)
	}
	command := smokeHookCommand(t, home)
	payload := func(session string) string {
		return `{"session_id":"` + session + `","cwd":"/tmp/rakko-approve/proj","hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf build","description":"清理构建产物"}}`
	}

	// 1. Allow.
	cmd, out := startHook(t, command, payload("smoke-allow"))
	ap := waitApprovalCard(t, a, f)
	if !strings.Contains(ap.analysis.Sentence(locales.In("zh-Hans")), "清理构建产物") {
		t.Fatalf("the card lost the agent's sentence: %q", ap.analysis.Sentence(locales.In("zh-Hans")))
	}
	time.Sleep(100 * time.Millisecond)
	if out.Len() != 0 {
		t.Fatalf("the hook answered before a decision: %q", out.String())
	}
	f.decide(ap.id, agentnotify.DecisionAllow, "")
	if got := waitHook(t, cmd, out); got != `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}` {
		t.Fatalf("allow output %q", got)
	}

	// 2. Deny with a reason.
	cmd, out = startHook(t, command, payload("smoke-deny"))
	ap = waitApprovalCard(t, a, f)
	f.decide(ap.id, agentnotify.DecisionDeny, "keep the build")
	got := waitHook(t, cmd, out)
	if !strings.Contains(got, `"behavior":"deny"`) || !strings.Contains(got, `"message":"keep the build"`) {
		t.Fatalf("deny output %q", got)
	}

	// 3. No answer: the card expires and the hook prints nothing. The deny
	// result is cleared first so the new card starts at once.
	a.liveNow(time.Now().Add(2*time.Second), true)
	oldWait := approvalWait
	approvalWait = 700 * time.Millisecond
	t.Cleanup(func() { approvalWait = oldWait })
	cmd, out = startHook(t, command, payload("smoke-expire"))
	ap = waitApprovalCard(t, a, f)
	if got := waitHook(t, cmd, out); got != "" {
		t.Fatalf("expired hook printed %q, want nothing", got)
	}
	if ap.state != approvalExpired {
		t.Fatalf("card state %v, want expired", ap.state)
	}
}
