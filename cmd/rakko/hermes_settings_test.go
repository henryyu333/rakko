//go:build darwin

package main

// The Hermes row in 设置 → AI: the group switches install the plugin and its
// config lines, and switching both off puts config.yaml back byte for byte.
// The screenshot is gated by RAKKO_HERMES_SHOT=1 and lands in the task's
// evidence directory.

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
)

func installHermes(t *testing.T, home string) {
	t.Helper()
	write := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".hermes", "config.yaml"),
		"plugins:\n  enabled:\n    - agent-importers\n  disabled: []\nmodel:\n  default: x\n")
	write(filepath.Join(home, ".hermes", "hermes-agent", "cli.py"), "")
}

func TestHermesRowsInstallAndCleanup(t *testing.T) {
	a, home, _ := agentShelf(t)
	installHermes(t, home)
	path := filepath.Join(home, ".hermes", "config.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	a.setAgentNotify(true)
	if st, note, _ := a.agentFeature().settingRow(agentnotify.ToolHermes); st != agentnotify.StateInjected {
		t.Fatalf("completion row = %v %s", st, note)
	}
	a.setApproval(true)
	if st, note := a.approvalFeature().settingRow(agentnotify.ToolHermes); st != agentnotify.StateInjected {
		t.Fatalf("approval row = %v %s", st, note)
	}

	a.setApproval(false)
	if st, _ := a.approvalFeature().settingRow(agentnotify.ToolHermes); st != agentnotify.StateNotInjected {
		t.Fatalf("approval row after its switch = %v", st)
	}
	if st, _, _ := a.agentFeature().settingRow(agentnotify.ToolHermes); st != agentnotify.StateInjected {
		t.Fatalf("completion row went with approval: %v", st)
	}
	a.setAgentNotify(false)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("config.yaml changed:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(home, ".hermes", "plugins", "rakko")); !os.IsNotExist(err) {
		t.Fatalf("plugin directory survived: %v", err)
	}
}

// TestHermesSettingsScreenshot renders the two settings rows with no window.
func TestHermesSettingsScreenshot(t *testing.T) {
	if os.Getenv("RAKKO_HERMES_SHOT") != "1" {
		t.Skip("set RAKKO_HERMES_SHOT=1 to write the Hermes settings render")
	}
	a, home, _ := agentShelf(t)
	installHermes(t, home)
	a.setAgentNotify(true)
	a.setApproval(true)
	view := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	view.Frame()
	openSettingsPage(t, view, "AI")
	view.SetScale(2)
	view.Frame()

	const dir = "/tmp/Rakko-hermes"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, shot := range []string{"hermes-completion-row", "hermes-approval-row"} {
		for range 40 {
			if r, ok := view.Find("Hermes"); ok && r.W > 0 && r.H > 0 &&
				r.Y >= 0 && r.Y+r.H <= settingsHeight {
				break
			}
			view.Scroll(settingsWidth/2, settingsHeight/2, 0, 120)
			view.Frame()
		}
		view.Frame()
		file, err := os.Create(filepath.Join(dir, shot+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, view.Image()); err != nil {
			file.Close()
			t.Fatalf("%s encode: %v", shot, err)
		}
		file.Close()
		// Scroll past the completion row to the approval row.
		view.Scroll(settingsWidth/2, settingsHeight/2, 0, 320)
		view.Frame()
	}
}

func TestHermesRowHiddenWithoutHermes(t *testing.T) {
	a, home, _ := agentShelf(t)
	for _, tool := range a.agentFeature().detected() {
		if tool == agentnotify.ToolHermes {
			t.Fatal("a home without ~/.hermes still has a Hermes row")
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".hermes")); !os.IsNotExist(err) {
		t.Fatalf("the test wrote ~/.hermes: %v", err)
	}
}
