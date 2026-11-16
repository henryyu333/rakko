package aiusage

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestParseCodexLine(t *testing.T) {
	pool := map[string]*string{}
	tail := &codexTail{}

	// turn_context sets the model for later records.
	ctx := []byte(`{"timestamp":"2026-10-19T08:00:01.000Z","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`)
	if _, ok := tail.parseCodexLine(ctx, pool); ok {
		t.Fatal("turn_context must not emit a record")
	}
	if tail.model != "gpt-6.1-sol" {
		t.Fatalf("model = %q", tail.model)
	}

	ev := []byte(`{"timestamp":"2026-10-19T08:00:10.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"cache_write_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":110},"last_token_usage":{"input_tokens":100,"cached_input_tokens":20,"cache_write_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":110}}}}`)
	r, ok := tail.parseCodexLine(ev, pool)
	if !ok {
		t.Fatal("event rejected")
	}
	// input_tokens includes cached: split into Input 80 + CacheRead 20.
	if r.in != 80 || r.cr != 20 || r.out != 10 || r.cw != 0 {
		t.Fatalf("cached split wrong: %+v", r)
	}
	if r.model == nil || *r.model != "gpt-6.1-sol" {
		t.Fatal("model not carried from turn_context")
	}
	if r.priced {
		t.Fatal("codex must be unpriced")
	}

	// Same running total again → duplicate, skipped.
	evDup := []byte(`{"timestamp":"2026-10-19T08:00:11.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"cache_write_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":110},"last_token_usage":{"input_tokens":5,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0,"total_tokens":6}}}}`)
	if _, ok := tail.parseCodexLine(evDup, pool); ok {
		t.Fatal("duplicate total_token_usage must be skipped")
	}

	// A different total resumes recording.
	ev2 := []byte(`{"timestamp":"2026-10-19T08:00:12.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":200,"cached_input_tokens":20,"cache_write_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":220},"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":110}}}}`)
	r2, ok := tail.parseCodexLine(ev2, pool)
	if !ok {
		t.Fatal("next event rejected")
	}
	if r2.in != 100 || r2.cr != 0 || r2.out != 10 {
		t.Fatalf("second rec wrong: %+v", r2)
	}
}

func TestParseCodexLineSkips(t *testing.T) {
	pool := map[string]*string{}
	tail := &codexTail{}
	for name, line := range map[string]string{
		"null-info": `{"timestamp":"2026-10-19T08:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":null}}`,
		"null-last": `{"timestamp":"2026-10-19T08:00:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0,"total_tokens":2},"last_token_usage":null}}}`,
		"other-ev":  `{"timestamp":"2026-10-19T08:00:00.000Z","type":"event_msg","payload":{"type":"agent_message","token_count":"x"}}`,
		"response":  `{"timestamp":"2026-10-19T08:00:00.000Z","type":"response_item","payload":{"type":"message","token_count":"x"}}`,
		"garbage":   `{"type":"event_msg","token_count`,
		"no-ts":     `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0,"total_tokens":2},"last_token_usage":{"input_tokens":1,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0,"total_tokens":2}}}}`,
	} {
		if _, ok := tail.parseCodexLine([]byte(line), pool); ok {
			t.Fatalf("%s: should be skipped", name)
		}
	}
}

func TestScanCodexZstdAndDedupe(t *testing.T) {
	home := t.TempDir()
	cacheDir := t.TempDir()
	rolloutContent := []byte(
		`{"timestamp":"2026-11-16T08:00:00.000Z","type":"turn_context","payload":{"model":"gpt-5.3-codex"}}` + "\n" +
			`{"timestamp":"2026-11-16T08:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":500,"cached_input_tokens":200,"cache_write_input_tokens":0,"output_tokens":50,"reasoning_output_tokens":0,"total_tokens":550},"last_token_usage":{"input_tokens":500,"cached_input_tokens":200,"cache_write_input_tokens":0,"output_tokens":50,"reasoning_output_tokens":0,"total_tokens":550}}}}` + "\n",
	)
	var zbuf bytes.Buffer
	zw, err := zstd.NewWriter(&zbuf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(rolloutContent); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	// Same rollout in both .jsonl and archived .jsonl.zst must count once.
	writeTestFile(t, filepath.Join(home, ".codex", "sessions", "2026", "11", "16", "rollout-dup.jsonl"), rolloutContent)
	writeTestFile(t, filepath.Join(home, ".codex", "archived_sessions", "rollout-dup.jsonl.zst"), zbuf.Bytes())

	// Separate .jsonl.zst-only rollout in sessions must also be read.
	zstOnlyContent := []byte(
		`{"timestamp":"2026-11-16T09:00:00.000Z","type":"turn_context","payload":{"model":"gpt-5.3-codex"}}` + "\n" +
			`{"timestamp":"2026-11-16T09:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":120}}}}` + "\n",
	)
	var zbuf2 bytes.Buffer
	zw2, _ := zstd.NewWriter(&zbuf2)
	_, _ = zw2.Write(zstOnlyContent)
	_ = zw2.Close()
	writeTestFile(t, filepath.Join(home, ".codex", "sessions", "2026", "11", "16", "rollout-zstonly.jsonl.zst"), zbuf2.Bytes())

	s := NewScanner(home, cacheDir)
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sum := s.Summary(RangeAll, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	got := sum.ByTool[ToolCodex]
	// rollout-dup: in=300, cr=200, out=50; rollout-zstonly: in=100, out=20 => 2 requests total.
	if got.Requests != 2 || got.Input != 400 || got.CacheRead != 200 || got.Output != 70 {
		t.Fatalf("codex totals = %+v, want reqs=2 in=400 cr=200 out=70", got)
	}
}
