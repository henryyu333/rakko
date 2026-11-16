package aiusage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanAntigravityDedupeAndIgnoreCopies(t *testing.T) {
	home := t.TempDir()
	convDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "conv-123", ".system_generated", "logs")
	line1 := `{"step_index":1,"type":"PLANNER_RESPONSE","model":"gemini-4-hydrogen-high","input_tokens":1000,"cache_read_tokens":5000,"output_tokens":200,"created_at":"2026-11-16T08:00:00Z"}` + "\n"
	line2 := `{"step_index":2,"type":"PLANNER_RESPONSE","model":"claude-opus-5-5-high","input_tokens":500,"cache_read_tokens":0,"output_tokens":100,"created_at":"2026-11-16T09:00:00Z"}` + "\n"
	userLine := `{"step_index":0,"type":"USER_INPUT","created_at":"2026-11-16T07:59:00Z"}` + "\n"

	// transcript_full.jsonl (with a duplicate step_index=1)
	writeTestFile(t, filepath.Join(convDir, "transcript_full.jsonl"), []byte(userLine+line1+line1+line2))
	// transcript.jsonl and chunks/ must be ignored
	writeTestFile(t, filepath.Join(convDir, "transcript.jsonl"), []byte(line1+line2))
	writeTestFile(t, filepath.Join(convDir, "chunks", "transcript_full", "0.jsonl"), []byte(line1))

	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	sum := s.Summary(RangeToday, now)
	agy := sum.ByTool[ToolAntigravity]
	if agy.Requests != 2 || agy.Input != 1500 || agy.CacheRead != 5000 || agy.Output != 300 {
		t.Fatalf("agy totals wrong: %+v", agy)
	}
	if agy.Unpriced != 0 || agy.Cost <= 0 {
		t.Fatalf("agy should be priced via LiteLLM: %+v", agy)
	}
}

func TestScanDSHZstdAndPlain(t *testing.T) {
	home := t.TempDir()
	sdir := filepath.Join(home, ".dsh", "sessions", "proj", "session-abc")
	plain := `{"type":"request/context","seq":1,"time":1794800000000,"data":{"model":"deepseek-chat"}}` + "\n" +
		`{"type":"assistant/message","seq":2,"time":1794800001000,"data":{"usage":{"inputTokens":400,"outputTokens":50,"cacheReadTokens":1000,"cacheWriteTokens":0}}}` + "\n"

	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = zw.Write([]byte(plain))
	_ = zw.Close()

	writeTestFile(t, filepath.Join(sdir, "session.v4.jsonl.zstd"), buf.Bytes())

	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sum := s.Summary(RangeAll, time.UnixMilli(1794800005000))
	dsh := sum.ByTool[ToolDsh]
	if dsh.Requests != 1 || dsh.Input != 400 || dsh.Output != 50 || dsh.CacheRead != 1000 {
		t.Fatalf("dsh totals wrong: %+v", dsh)
	}
}

func TestScanGeminiQwenAndEditors(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

	// Gemini CLI
	writeTestFile(t, filepath.Join(home, ".gemini", "tmp", "proj1", "chats", "session-1.json"), []byte(`{
		"sessionId": "g1",
		"messages": [
			{"id":"m1","timestamp":"2026-11-16T10:00:00Z","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":200,"output":40,"cached":600,"thoughts":10}}
		]
	}`))

	// Qwen Code
	writeTestFile(t, filepath.Join(home, ".qwen", "projects", "p1", "sess.jsonl"), []byte(
		`{"type":"assistant","uuid":"q1","timestamp":"2026-11-16T10:05:00Z","message":{"id":"qm1","model":"qwen3-coder-plus","usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":50}}}`+"\n",
	))

	// Droid
	writeTestFile(t, filepath.Join(home, ".factory", "sessions", "s1.jsonl"), []byte(
		`{"id":"d1","timestamp":"2026-11-16T10:10:00Z","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":300,"output_tokens":60}}`+"\n",
	))

	// Auggie
	writeTestFile(t, filepath.Join(home, ".augment", "sessions", "a1.json"), []byte(`{
		"sessionId":"au1",
		"chatHistory":[{"id":"c1","timestamp":"2026-11-16T10:15:00Z","model":"claude-sonnet-4-5-20250929","tokenUsage":{"inputTokens":150,"outputTokens":30}}]
	}`))

	// Copilot OTEL
	writeTestFile(t, filepath.Join(home, ".copilot", "otel", "run.jsonl"), []byte(
		`{"id":"cp1","timestamp":"2026-11-16T10:20:00Z","model":"gpt-5","usage":{"prompt_tokens":120,"completion_tokens":25}}`+"\n",
	))

	// Kiro
	writeTestFile(t, filepath.Join(home, ".kiro", "sessions", "cli", "k1.jsonl"), []byte(
		`{"id":"kr1","timestamp":"2026-11-16T10:25:00Z","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":80,"output_tokens":15}}`+"\n",
	))

	// Cline (.cline/data/sessions)
	writeTestFile(t, filepath.Join(home, ".cline", "data", "sessions", "cl1.jsonl"), []byte(
		`{"id":"cl1","timestamp":"2026-11-16T10:30:00Z","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":90,"output_tokens":10}}`+"\n",
	))

	// OpenClaw
	writeTestFile(t, filepath.Join(home, ".openclaw", "agents", "main", "sessions", "oc1.jsonl"), []byte(
		`{"type":"message","id":"ocm1","timestamp":"2026-11-16T10:35:00Z","message":{"role":"assistant","model":"gpt-5","usage":{"input":70,"output":20,"cost":{"total":0.01}}}}`+"\n",
	))

	// Kimi
	writeTestFile(t, filepath.Join(home, ".kimi", "sessions", "km1.jsonl"), []byte(
		`{"id":"km1","timestamp":"2026-11-16T10:40:00Z","model":"kimi-k2","usage":{"prompt_tokens":60,"completion_tokens":15}}`+"\n",
	))

	// Junie
	writeTestFile(t, filepath.Join(home, ".junie", "sessions", "j1", "events.jsonl"), []byte(
		`{"id":"jn1","timestamp":"2026-11-16T10:45:00Z","model":"gpt-5","usage":{"input_tokens":50,"output_tokens":10}}`+"\n",
	))

	// LM Studio ($0 local inference)
	writeTestFile(t, filepath.Join(home, ".lmstudio", "server-logs", "srv.log"), []byte(
		`{"id":"lm1","timestamp":"2026-11-16T10:50:00Z","model":"local-qwen","usage":{"prompt_tokens":500,"completion_tokens":100}}`+"\n",
	))

	// Cursor installed (unreadable)
	writeTestFile(t, filepath.Join(home, ".cursor", "cli-config.json"), []byte(`{}`))

	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sum := s.Summary(RangeToday, now)
	wantTools := []Tool{
		ToolGemini, ToolQwen, ToolDroid, ToolAugment, ToolCopilot,
		ToolKiro, ToolCline, ToolOpenClaw, ToolKimi, ToolJunie, ToolLMStudio,
	}
	for _, tl := range wantTools {
		if sum.ByTool[tl].Requests != 1 {
			t.Errorf("%s requests = %d, want 1", tl, sum.ByTool[tl].Requests)
		}
	}
	if lm := sum.ByTool[ToolLMStudio]; lm.Unpriced != 0 || lm.Cost != 0 {
		t.Errorf("lmstudio should be $0 priced, got %+v", lm)
	}
	unreadable := s.UnreadableTools()
	if !slices.Contains(unreadable, ToolCursor) {
		t.Fatalf("UnreadableTools() = %v, want %v", unreadable, ToolCursor)
	}
}

func TestInstalledWithoutDataFilesAreUnreadable(t *testing.T) {
	for _, entry := range registry {
		if entry.detect == nil {
			t.Errorf("registry entry %q (%s) missing detect probe", entry.tool, entry.name)
		}
	}

	home := t.TempDir()
	// Copilot CLI installed via config.json, no .copilot/otel/*.jsonl.
	writeTestFile(t, filepath.Join(home, ".copilot", "config.json"), []byte(`{}`))
	// Claude Code installed via .claude.json, no .claude/projects/*.jsonl.
	writeTestFile(t, filepath.Join(home, ".claude.json"), []byte(`{}`))
	// Hermes installed via config.yaml, no state.db.
	writeTestFile(t, filepath.Join(home, ".hermes", "config.yaml"), []byte("model: test\n"))
	// Codex installed AND has a valid data file from last month (unused today).
	writeTestFile(t, filepath.Join(home, ".codex", "auth.json"), []byte(`{}`))
	writeTestFile(t, filepath.Join(home, ".codex", "sessions", "rollout-old.jsonl"), []byte(
		`{"timestamp":"2026-10-01T10:00:00Z","type":"turn_context","payload":{"model":"gpt-5.3-codex"}}`+"\n"+
			`{"timestamp":"2026-10-01T10:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120}}}}`+"\n",
	))

	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	unreadable := s.UnreadableTools()
	for _, want := range []Tool{ToolCopilot, ToolClaude, ToolHermes} {
		if !slices.Contains(unreadable, want) {
			t.Errorf("UnreadableTools() = %v, want %s included", unreadable, want)
		}
	}
	for _, unwanted := range []Tool{ToolCodex, ToolOmp} {
		if slices.Contains(unreadable, unwanted) {
			t.Errorf("UnreadableTools() = %v, should not include %s", unreadable, unwanted)
		}
	}
}

func TestSkillsOnlyDirsNotReportedAsInstalled(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{
		filepath.Join(home, ".junie", "skills", "s1", "SKILL.md"),
		filepath.Join(home, ".openclaw", "skills", "s1", "SKILL.md"),
		filepath.Join(home, ".kimi", "skills", "s1", "SKILL.md"),
		filepath.Join(home, ".cline", "skills", "s1", "SKILL.md"),
		filepath.Join(home, ".warp", "skills", "s1", "SKILL.md"),
		filepath.Join(home, ".capy", "skills", "s1", "SKILL.md"),
		filepath.Join(home, ".trae", "skills", "s1", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "s1", "SKILL.md"),
		filepath.Join(home, ".claude", "AGENTS.md"),
		filepath.Join(home, ".codex", "skills", "s1", "SKILL.md"),
	} {
		writeTestFile(t, p, []byte("# skill\n"))
	}
	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	got := s.UnreadableTools()
	for _, tl := range []Tool{
		ToolJunie, ToolOpenClaw, ToolKimi, ToolCline,
		ToolWarp, ToolCapy, ToolTrae, ToolClaude, ToolCodex,
	} {
		if slices.Contains(got, tl) {
			t.Fatalf("UnreadableTools() = %v, should not include skills-only %s", got, tl)
		}
	}
}
