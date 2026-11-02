//go:build darwin

package main

// The Agent 完成提醒 feature: task naming, the two-second merge, the panel
// wait, the settings switch's injection and removal, and one event making
// the whole trip from the forwarder to the notch's live queue.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/agentnotify"
	"shelf/internal/settings"
)

// agentShelf builds a shelf whose home and data directory are temporary, so
// no test touches a real agent configuration or the real Rakko data.
func agentShelf(t *testing.T) (*shelf, string, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rakko-agent-")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	origHome, origExe, origMain := agentNotifyHome, agentNotifyExe, agentRunOnMain
	agentNotifyHome = func() (string, error) { return home, nil }
	agentNotifyExe = func() (string, error) {
		return "/Applications/Rakko.app/Contents/MacOS/Rakko", nil
	}
	agentRunOnMain = func(fn func()) { fn() }
	t.Cleanup(func() {
		agentNotifyHome, agentNotifyExe, agentRunOnMain = origHome, origExe, origMain
		os.RemoveAll(dir)
	})
	a := &shelf{
		dataDir: dir, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{},
	}
	return a, home, dir
}

func TestAgentTaskName(t *testing.T) {
	cases := []struct {
		prompt string
		want   string
	}{
		{"Fix the failing test\nand then commit", "Fix the failing test"},
		{"  spaced \t out  ", "spaced out"},
		{"", ""},
	}
	for _, c := range cases {
		if got := agentTaskName(c.prompt); got != c.want {
			t.Errorf("agentTaskName(%q) = %q, want %q", c.prompt, got, c.want)
		}
	}
	// 20 Chinese characters fit; the 21st cuts with an ellipsis.
	zh20 := strings.Repeat("任", 20)
	if got := agentTaskName(zh20); got != zh20 {
		t.Errorf("20 Chinese characters were cut: %q", got)
	}
	zh21 := zh20 + "务"
	if got := truncateAgentTask(zh21); got != zh20+"…" {
		t.Errorf("21 Chinese characters = %q", got)
	}
	ascii40 := strings.Repeat("a", 40)
	if got := truncateAgentTask(ascii40); got != ascii40 {
		t.Errorf("40 ASCII characters were cut: %q", got)
	}
	if got := truncateAgentTask(ascii40 + "b"); got != ascii40+"…" {
		t.Errorf("41 ASCII characters = %q", got)
	}
}

func TestAgentCompletionCapsule(t *testing.T) {
	a, _, _ := agentShelf(t)
	a.settings.AgentNotify.Enabled = true
	f := a.agentFeature()
	f.accept(agentnotify.Event{Tool: agentnotify.ToolClaude, Event: "UserPromptSubmit",
		Session: "s1", Cwd: "/tmp/proj", Prompt: "Fix the failing test\nand commit"})
	f.accept(agentnotify.Event{Tool: agentnotify.ToolClaude, Event: "Stop", Session: "s1", Cwd: "/tmp/proj"})

	ev, ok := a.liveNow(time.Now(), true)
	if !ok || ev.kind != liveAgentDone {
		t.Fatalf("capsule = %+v ok=%v", ev, ok)
	}
	if ev.agent != "Claude Code" || ev.task != "Fix the failing test" {
		t.Fatalf("capsule names %q / %q", ev.agent, ev.task)
	}
	if line := ev.statusLine(); !strings.Contains(line, "搞定了") || !strings.Contains(line, "Claude Code") {
		t.Fatalf("status line = %q", line)
	}
	if w, _ := a.liveShape(ev); w <= 0 {
		t.Fatalf("capsule width = %v", w)
	}
	// It folds after its own five seconds.
	if _, ok := a.liveNow(time.Now().Add(liveHoldAgent), true); ok {
		t.Fatal("the capsule outlived its hold")
	}
}

func TestAgentCompletionCarriesPluginPrompt(t *testing.T) {
	a, _, _ := agentShelf(t)
	a.settings.AgentNotify.Enabled = true
	// The omp/pi/opencode modules keep the prompt themselves and send it
	// with the completion event: no earlier prompt event exists.
	a.agentFeature().accept(agentnotify.Event{Tool: agentnotify.ToolOmp, Event: "agent_end",
		Session: "o1", Cwd: "/tmp/proj", Prompt: "Rewrite the parser\nkeep the API"})
	ev, ok := a.liveNow(time.Now(), true)
	if !ok || ev.task != "Rewrite the parser" {
		t.Fatalf("capsule = %+v ok=%v", ev, ok)
	}
}

func TestAgentCompletionFallsBackToCwd(t *testing.T) {
	a, _, _ := agentShelf(t)
	a.settings.AgentNotify.Enabled = true
	a.agentFeature().accept(agentnotify.Event{Tool: agentnotify.ToolPi, Event: "agent_end",
		Session: "p1", Cwd: "/tmp/my-project"})
	ev, ok := a.liveNow(time.Now(), true)
	if !ok || ev.task != "my-project" || ev.agent != "pi" {
		t.Fatalf("capsule = %+v ok=%v", ev, ok)
	}
}

func TestAgentCompletionWaitsForOpenPanel(t *testing.T) {
	a, _, _ := agentShelf(t)
	a.settings.AgentNotify.Enabled = true
	a.visible = true
	a.agentFeature().accept(agentnotify.Event{Tool: agentnotify.ToolCodex, Event: "Stop",
		Session: "c1", Cwd: "/tmp/proj"})
	if _, ok := a.liveNow(time.Now(), a.liveAllow()); ok {
		t.Fatal("a completion showed over the open panel")
	}
	// The panel closes much later: the completion still shows, because the
	// wait bound does not apply to it.
	a.visible = false
	ev, ok := a.liveNow(time.Now().Add(20*time.Second), true)
	if !ok || ev.kind != liveAgentDone {
		t.Fatalf("the waiting completion was lost: %+v ok=%v", ev, ok)
	}
}

func TestAgentCompletionMergesRepeats(t *testing.T) {
	a, _, _ := agentShelf(t)
	a.settings.AgentNotify.Enabled = true
	f := a.agentFeature()
	f.accept(agentnotify.Event{Tool: agentnotify.ToolOmp, Event: "agent_end", Session: "o1", Cwd: "/tmp/proj"})
	f.accept(agentnotify.Event{Tool: agentnotify.ToolOmp, Event: "agent_end", Session: "o1", Cwd: "/tmp/proj"})
	if got := len(a.live.queued); got != 1 {
		t.Fatalf("two finishes in the same window queued %d capsules", got)
	}
}

func TestAgentSwitchInjectsAndRemoves(t *testing.T) {
	a, home, _ := agentShelf(t)
	for _, dir := range []string{".claude/projects", ".codex/sessions"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	a.setAgentNotify(true)
	if !a.settings.AgentNotify.Enabled {
		t.Fatal("the switch did not stick in settings")
	}
	claude := agentnotify.ConfigPath(home, agentnotify.ToolClaude)
	body, err := os.ReadFile(claude)
	if err != nil {
		t.Fatalf("Claude Code's settings were not written: %v", err)
	}
	if !strings.Contains(string(body), "--agent-hook --tool claude") {
		t.Fatalf("no Rakko entry in %s:\n%s", claude, body)
	}
	for _, tool := range []agentnotify.Tool{agentnotify.ToolClaude, agentnotify.ToolCodex} {
		if st, note, ok := a.agentFeature().settingRow(tool); !ok || st != agentnotify.StateInjected {
			t.Fatalf("%s row = %v %q ok=%v", tool, st, note, ok)
		}
	}
	// Turning it on again writes nothing more.
	a.setAgentNotify(true)
	if again, err := os.ReadFile(claude); err != nil {
		t.Fatal(err)
	} else if string(again) != string(body) {
		t.Fatalf("a second switch-on changed the file:\n%s", again)
	}
	a.setAgentNotify(false)
	a.setAgentNotify(false)
	if _, err := os.Stat(claude); !os.IsNotExist(err) {
		t.Fatalf("Claude Code's Rakko-created settings survived: %v", err)
	}
	if _, err := os.Stat(agentnotify.ConfigPath(home, agentnotify.ToolCodex)); !os.IsNotExist(err) {
		t.Fatal("Codex's hooks file survived the switch")
	}
	if st, _, _ := a.agentFeature().settingRow(agentnotify.ToolClaude); st != agentnotify.StateNotInjected {
		t.Fatalf("row after off = %v", st)
	}
}

func TestAgentSwitchKeepsUserHookBytes(t *testing.T) {
	a, home, _ := agentShelf(t)
	path := agentnotify.ConfigPath(home, agentnotify.ToolGemini)
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	user := "{\n  \"theme\": \"GitHub\"\n}\n"
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	a.setAgentNotify(true)
	a.setAgentNotify(false)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != user {
		t.Fatalf("Gemini's file did not come back byte for byte:\n%s", body)
	}
}

// The forwarder, the socket and the live queue in one go: the exact path a
// Claude Code hook takes once the switch is on.
func TestAgentEventReachesTheNotch(t *testing.T) {
	a, home, dir := agentShelf(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	a.setAgentNotify(true)
	defer a.agentFeature().stop()
	if f := a.agentFeature(); f.node != "" || f.srv == nil {
		t.Fatalf("socket did not start: node=%q srv=%v", f.node, f.srv)
	}
	// Production delivers on the main thread; the test plays that thread,
	// so the queue it reads is never touched from another goroutine.
	queue := make(chan func(), 16)
	agentRunOnMain = func(fn func()) { queue <- fn }
	pump := func() {
		for {
			select {
			case fn := <-queue:
				fn()
			default:
				return
			}
		}
	}

	args := []string{"--agent-hook", "--tool", "claude", "--data-dir", dir}
	if handled := agentnotify.RunHookCLI(args, strings.NewReader(
		`{"session_id":"real-1","hook_event_name":"UserPromptSubmit","cwd":"/tmp/app","prompt":"Ship the report"}`)); !handled {
		t.Fatal("the forwarder did not handle the prompt event")
	}
	agentnotify.RunHookCLI(args, strings.NewReader(
		`{"session_id":"real-1","hook_event_name":"Stop","cwd":"/tmp/app"}`))

	deadline := time.Now().Add(2 * time.Second)
	for {
		pump()
		if ev, ok := a.liveNow(time.Now(), true); ok {
			if ev.kind != liveAgentDone || ev.agent != "Claude Code" || ev.task != "Ship the report" {
				t.Fatalf("capsule = %+v", ev)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no completion reached the notch")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
