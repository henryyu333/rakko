package aiusage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProbeReportsFoundNotFoundAndUnsupported(t *testing.T) {
	home := t.TempDir()
	opencodeTestDB(t, home)
	pi := filepath.Join(home, ".pi/agent/sessions/-p/s.jsonl")
	if err := os.MkdirAll(filepath.Dir(pi), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pi, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	grok := filepath.Join(home, ".grok/sessions/-p/g/usage.json")
	if err := os.MkdirAll(filepath.Dir(grok), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grok, []byte(`{"turns":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := map[Tool]AgentStatus{}
	for _, st := range Probe(home) {
		got[st.Tool] = st
	}
	for _, tool := range []Tool{ToolOmp, ToolClaude, ToolCodex} {
		if got[tool].Status != StatusNotFound {
			t.Errorf("%s status = %v, want not found", tool, got[tool].Status)
		}
	}
	for _, tool := range []Tool{ToolPi, ToolOpencode, ToolGrok} {
		if got[tool].Status != StatusFound {
			t.Errorf("%s status = %v, want found", tool, got[tool].Status)
		}
	}
	if got[ToolPi].Path != "~/.pi/agent/sessions" {
		t.Errorf("pi path = %q", got[ToolPi].Path)
	}
	if got[ToolOpencode].Path != "~/.local/share/opencode/opencode.db" {
		t.Errorf("opencode path = %q", got[ToolOpencode].Path)
	}
	if got[ToolCursor].Status != StatusUnsupported {
		t.Errorf("cursor status = %v, want unsupported", got[ToolCursor].Status)
	}
}

func TestProbeReadFailure(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".claude/projects/-p/s.jsonl")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f, 0o644) })

	for _, st := range Probe(home) {
		if st.Tool == ToolClaude {
			if st.Status != StatusReadFailed {
				t.Fatalf("claude status = %v, want read failed", st.Status)
			}
			return
		}
	}
	t.Fatal("claude status missing")
}

// An empty home must not fall back to the process's real home: tests and
// headless callers get NotFound rows without any filesystem access.
func TestProbeEmptyHome(t *testing.T) {
	for _, st := range Probe("") {
		if st.Status == StatusFound || st.Status == StatusReadFailed {
			t.Fatalf("%s probed the real home: %+v", st.Tool, st)
		}
	}
}
