//go:build darwin

package main

// Real-window evidence for the Agent 完成提醒 capsule: the installed hook
// command is read back out of Claude Code's settings in a temporary home,
// run through /bin/sh with a real Claude Code Stop payload, and the notch
// shows the capsule. A second session finishes while the first capsule is
// up, so the queue is exercised too. Run with SHELF_BASKET_SMOKE=1; the
// PNGs land in /tmp/rakko-agent/.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shelf/internal/agentnotify"
)

const agentShotDir = "/tmp/rakko-agent"

// buildHookBinary builds the app's own binary inside a throwaway .app so
// the installed command (which only runs from a bundle) can be executed for
// real. The forwarder mode exits before any window or data work.
func buildHookBinary(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(agentShotDir, "Rakko.app", "Contents", "MacOS", "Rakko")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", exe, "./cmd/shelfu")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the hook binary: %v\n%s", err, out)
	}
	return exe
}

// claudeHookCommand reads Rakko's Stop command back from the settings file
// the switch wrote.
func claudeHookCommand(t *testing.T, path string) string {
	t.Helper()
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
		t.Fatalf("%s does not parse: %v", path, err)
	}
	for _, group := range doc.Hooks["Stop"] {
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "--agent-hook --tool claude") {
				return h.Command
			}
		}
	}
	t.Fatalf("no Rakko Stop command in %s:\n%s", path, data)
	return ""
}

// runHook runs the installed command exactly as Claude Code does: through
// the shell, with the payload on stdin.
func runHook(t *testing.T, command, payload string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Stdin = strings.NewReader(payload)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the installed hook failed: %v (%s)", err, out)
	} else if len(out) != 0 {
		t.Fatalf("the installed hook wrote to stdout: %q", out)
	}
}

// waitAgentTask reports whether the prompt event reached the feature's
// session table.
func waitAgentTask(t *testing.T, a *shelf, key string, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		f := a.agentFeature()
		f.mu.Lock()
		_, ok := f.tasks[key]
		f.mu.Unlock()
		if ok {
			return true
		}
		time.Sleep(40 * time.Millisecond)
	}
	return false
}

// waitAgentCapsule waits until the capsule on screen names task.
func waitAgentCapsule(t *testing.T, a *shelf, task string, within time.Duration) liveEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		var got liveEvent
		var ok bool
		onMain(func() {
			if a.live.shown {
				got, ok = a.live.cur, true
			}
		})
		if ok && got.kind == liveAgentDone && got.task == task {
			return got
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("no capsule for %q within %v", task, within)
	return liveEvent{}
}

func TestAgentNotifySmoke(t *testing.T) {
	a := liveSmokeShelf(t)
	if err := os.MkdirAll(agentShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	exe := buildHookBinary(t)
	origHome, origExe := agentNotifyHome, agentNotifyExe
	agentNotifyHome = func() (string, error) { return home, nil }
	agentNotifyExe = func() (string, error) { return exe, nil }
	t.Cleanup(func() {
		agentNotifyHome, agentNotifyExe = origHome, origExe
		onMain(func() { a.agentFeature().stop() })
	})

	onMain(func() {
		a.settings.AgentNotify.Enabled = true
		a.agentApply(true)
	})
	path := agentnotify.ConfigPath(home, agentnotify.ToolClaude)
	command := claudeHookCommand(t, path)
	t.Logf("installed command: %s", command)

	// Session 1: the prompt names the task, Stop finishes the turn.
	runHook(t, command, `{"session_id":"smoke-1","hook_event_name":"UserPromptSubmit","cwd":"/tmp/rakko-agent/proj","prompt":"Fix the failing parser test\nand then rerun the suite"}`)
	if !waitAgentTask(t, a, "claude|smoke-1", 3*time.Second) {
		t.Fatal("the prompt event never reached the app")
	}
	runHook(t, command, `{"session_id":"smoke-1","hook_event_name":"Stop","cwd":"/tmp/rakko-agent/proj"}`)
	ev := waitAgentCapsule(t, a, "Fix the failing parser test", 3*time.Second)
	if ev.task != "Fix the failing parser test" {
		t.Fatalf("capsule task = %q", ev.task)
	}
	shot := liveShot(t, a, "agent-claude-done")
	copyShot(t, shot, "agent-claude-done.png")

	// Session 2 finishes while the first is still up: it waits its turn.
	runHook(t, command, `{"session_id":"smoke-2","hook_event_name":"UserPromptSubmit","cwd":"/tmp/rakko-agent/notes","prompt":"Ship the release notes"}`)
	runHook(t, command, `{"session_id":"smoke-2","hook_event_name":"Stop","cwd":"/tmp/rakko-agent/notes"}`)
	second := waitAgentCapsule(t, a, "Ship the release notes", 10*time.Second)
	if second.task != "Ship the release notes" {
		t.Fatalf("second capsule task = %q", second.task)
	}
	shot = liveShot(t, a, "agent-claude-queued")
	copyShot(t, shot, "agent-claude-queued.png")
	onMain(func() { a.clearLive() })
}

// copyShot keeps the evidence in the task's own directory; liveShot writes
// into the shared live-activity folder.
func copyShot(t *testing.T, from, name string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentShotDir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
}
