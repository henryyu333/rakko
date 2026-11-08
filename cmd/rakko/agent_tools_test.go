//go:build darwin

package main

// The per-tool switches of the Agent 完成提醒 group: a tool switched off on
// its own row gets no entry, the other tools' files stay byte for byte, and
// the group switch still cleans up everything when it goes off.

import (
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
	"rakko/internal/settings"
)

// agentInstall makes two tools look installed in the temporary home.
func agentInstall(t *testing.T, home string, tools ...agentnotify.Tool) {
	t.Helper()
	dirs := map[agentnotify.Tool]string{
		agentnotify.ToolClaude: ".claude/projects",
		agentnotify.ToolCodex:  ".codex/sessions",
		agentnotify.ToolGemini: ".gemini/tmp",
	}
	for _, tool := range tools {
		if err := os.MkdirAll(filepath.Join(home, dirs[tool]), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func readOrEmpty(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAgentMasterOnLeavesToolsSwitchedOffAlone: with one tool switched off,
// turning the group on installs entries for the others only, and a tool that
// was injected before the group was switched off is cleaned when it comes
// back on with its own switch off.
func TestAgentMasterOnLeavesToolsSwitchedOffAlone(t *testing.T) {
	a, home, _ := agentShelf(t)
	agentInstall(t, home, agentnotify.ToolClaude, agentnotify.ToolCodex)

	a.setAgentNotify(true)
	codex := agentnotify.ConfigPath(home, agentnotify.ToolCodex)
	if !settingsHasHook(t, codex) {
		t.Fatal("Codex was not injected while its own switch was on")
	}
	a.setAgentNotify(false)

	next := a.settings
	next.AgentNotify.SetToolOn(string(agentnotify.ToolCodex), false)
	if !a.applySettings(next) {
		t.Fatal("the setting did not save")
	}
	a.setAgentNotify(true)
	if body := readOrEmpty(t, codex); body != "" {
		t.Fatalf("Codex was injected although its own switch is off:\n%s", body)
	}
	if st, _, _ := a.agentFeature().settingRow(agentnotify.ToolCodex); st != agentnotify.StateNotInjected {
		t.Fatalf("Codex row = %v, want 未注入", st)
	}
	claude := agentnotify.ConfigPath(home, agentnotify.ToolClaude)
	if !settingsHasHook(t, claude) {
		t.Fatal("Claude Code lost its entry because another tool was switched off")
	}
	if st, _, _ := a.agentFeature().settingRow(agentnotify.ToolClaude); st != agentnotify.StateInjected {
		t.Fatalf("Claude Code row = %v, want 已注入", st)
	}
}

func settingsHasHook(t *testing.T, path string) bool {
	t.Helper()
	return containsMarker(readOrEmpty(t, path))
}

func containsMarker(body string) bool {
	return strings.Contains(body, "--agent-hook")
}

// TestAgentToolSwitchTouchesOneToolOnly: switching one tool off removes its
// entry, records the choice, and leaves the other tool's file byte for byte.
func TestAgentToolSwitchTouchesOneToolOnly(t *testing.T) {
	a, home, _ := agentShelf(t)
	agentInstall(t, home, agentnotify.ToolClaude, agentnotify.ToolGemini)
	geminiPath := agentnotify.ConfigPath(home, agentnotify.ToolGemini)
	user := "{\n  \"theme\": \"GitHub\"\n}\n"
	if err := os.MkdirAll(filepath.Dir(geminiPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(geminiPath, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	a.setAgentNotify(true)
	claudePath := agentnotify.ConfigPath(home, agentnotify.ToolClaude)
	claudeBefore := readOrEmpty(t, claudePath)
	if !containsMarker(claudeBefore) {
		t.Fatal("Claude Code was not injected")
	}

	a.setAgentNotifyTool(agentnotify.ToolGemini, false)
	if body := readOrEmpty(t, geminiPath); body != user {
		t.Fatalf("Gemini's file did not come back byte for byte:\n%s", body)
	}
	if body := readOrEmpty(t, claudePath); body != claudeBefore {
		t.Fatalf("switching Gemini off changed Claude Code's file:\n%s", body)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.AgentNotify.Off, []string{"gemini"}) {
		t.Fatalf("settings off = %v, want [gemini]", saved.AgentNotify.Off)
	}
	if st, _, _ := a.agentFeature().settingRow(agentnotify.ToolGemini); st != agentnotify.StateNotInjected {
		t.Fatalf("Gemini row = %v, want 未注入", st)
	}

	// Back on: its entry returns and the list empties again.
	a.setAgentNotifyTool(agentnotify.ToolGemini, true)
	if !containsMarker(readOrEmpty(t, geminiPath)) {
		t.Fatal("Gemini was not injected when its own switch went back on")
	}
	if len(a.settings.AgentNotify.Off) != 0 {
		t.Fatalf("settings off after switching back on = %v", a.settings.AgentNotify.Off)
	}
}

// TestAgentToolRowSwitchInSettings: the row's switch is the same action — one
// click, one tool.
func TestAgentToolRowSwitchInSettings(t *testing.T) {
	a, home, _ := agentShelf(t)
	agentInstall(t, home, agentnotify.ToolClaude, agentnotify.ToolGemini)
	a.setAgentNotify(true)
	view := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	view.Frame()
	openSettingsPage(t, view, "AI")
	clickVisible(t, view, "Gemini CLI") // the click that flips the row
	view.Frame()
	if !a.settings.AgentNotify.ToolOff(string(agentnotify.ToolGemini)) {
		t.Fatalf("the row's switch did not store the choice: %v", a.settings.AgentNotify.Off)
	}
	if body := readOrEmpty(t, agentnotify.ConfigPath(home, agentnotify.ToolGemini)); containsMarker(body) {
		t.Fatalf("Gemini's entry survived its row being switched off:\n%s", body)
	}
	if st, _, _ := a.agentFeature().settingRow(agentnotify.ToolGemini); st != agentnotify.StateNotInjected {
		t.Fatalf("Gemini row = %v, want 未注入", st)
	}
	if body := readOrEmpty(t, agentnotify.ConfigPath(home, agentnotify.ToolClaude)); !containsMarker(body) {
		t.Fatal("Claude Code's entry went with Gemini's switch")
	}
}

// TestAgentToolRowsLockedWithGroupSwitch: with the group switch off the rows
// stay visible but their switches do nothing.
func TestAgentToolRowsLockedWithGroupSwitch(t *testing.T) {
	a, home, _ := agentShelf(t)
	agentInstall(t, home, agentnotify.ToolGemini)
	view := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	view.Frame()
	openSettingsPage(t, view, "AI")
	clickVisible(t, view, "Gemini CLI") // a locked row: the click must do nothing
	view.Frame()
	if a.settings.AgentNotify.Enabled {
		t.Fatal("the locked row switched the group on")
	}
	if len(a.settings.AgentNotify.Off) != 0 {
		t.Fatalf("a locked row stored %v", a.settings.AgentNotify.Off)
	}
	if body := readOrEmpty(t, agentnotify.ConfigPath(home, agentnotify.ToolGemini)); body != "" {
		t.Fatalf("a locked row wrote a file:\n%s", body)
	}
}

// TestAgentSettingsScreenshot renders the AI page at the Agent 完成提醒
// group: three detected tools, one of them switched off on its own row.
// Gated by RAKKO_AGENT_SHOT=1; the PNG lands in /tmp/rakko-risks/.
func TestAgentSettingsScreenshot(t *testing.T) {
	if os.Getenv("RAKKO_AGENT_SHOT") != "1" {
		t.Skip("set RAKKO_AGENT_SHOT=1 to write the Agent settings render")
	}
	a, home, _ := agentShelf(t)
	agentInstall(t, home, agentnotify.ToolClaude, agentnotify.ToolCodex, agentnotify.ToolGemini)
	a.setAgentNotify(true)
	a.setAgentNotifyTool(agentnotify.ToolGemini, false)
	view := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	view.Frame()
	openSettingsPage(t, view, "AI")
	for range 30 {
		if r, ok := view.Find("Gemini CLI"); ok && r.W > 0 && r.H > 0 &&
			r.Y >= 0 && r.Y+r.H <= settingsHeight {
			break
		}
		view.Scroll(settingsWidth/2, settingsHeight/2, 0, 120)
		view.Frame()
	}
	view.SetScale(2)
	view.Frame()
	view.Frame()

	const out = "/tmp/rakko-risks/agent-settings.png"
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, view.Image()); err != nil {
		t.Fatalf("%s encode: %v", out, err)
	}
	t.Logf("wrote %s", out)
}
