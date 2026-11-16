//go:build !windows

package aiusage

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestScanHermesAndDevinSQLite(t *testing.T) {
	if _, err := os.Stat(sqlite3Path); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()

	// Hermes state.db
	hermesDB := filepath.Join(home, ".hermes", "state.db")
	if err := os.MkdirAll(filepath.Dir(hermesDB), 0o755); err != nil {
		t.Fatal(err)
	}
	runSQLite(t, hermesDB, `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at REAL, ended_at REAL, last_activity_at REAL);
		CREATE TABLE session_model_usage (
			session_id TEXT, model TEXT, billing_provider TEXT, billing_base_url TEXT,
			billing_mode TEXT, task TEXT, api_call_count INTEGER,
			input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER,
			cache_write_tokens INTEGER, estimated_cost_usd REAL, actual_cost_usd REAL,
			first_seen REAL, last_seen REAL
		);
		INSERT INTO sessions VALUES ('s1', 1794800000, 1794800010, 1794800010);
		INSERT INTO session_model_usage VALUES ('s1', 'deepseek-v4.1-flash', 'p', 'u', '', '', 2, 1000, 200, 5000, 0, 0, 0, 1794800000, 1794800010);
	`)

	// Devin sessions.db
	devinDB := filepath.Join(home, ".local", "share", "devin", "cli", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(devinDB), 0o755); err != nil {
		t.Fatal(err)
	}
	runSQLite(t, devinDB, `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, model TEXT);
		CREATE TABLE message_nodes (row_id INTEGER PRIMARY KEY, session_id TEXT, node_id INTEGER, chat_message TEXT, created_at INTEGER);
		INSERT INTO sessions VALUES ('d1', 'swe-1-6-slow');
		INSERT INTO message_nodes VALUES (1, 'd1', 10, '{"role":"assistant","metadata":{"created_at":1794800020000,"metrics":{"input_tokens":800,"output_tokens":150,"cache_read_tokens":400,"cache_creation_tokens":50}}}', 1794800020);
	`)

	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sum := s.Summary(RangeAll, time.Unix(1794800030, 0))
	if h := sum.ByTool[ToolHermes]; h.Requests != 2 || h.Input != 1000 || h.Output != 200 || h.CacheRead != 5000 {
		t.Fatalf("hermes totals wrong: %+v", h)
	}
	if d := sum.ByTool[ToolDevin]; d.Requests != 1 || d.Input != 800 || d.Output != 150 || d.CacheRead != 400 || d.CacheWrite != 50 {
		t.Fatalf("devin totals wrong: %+v", d)
	}
}

func TestScanHermesDeltaAcrossDays(t *testing.T) {
	if _, err := os.Stat(sqlite3Path); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	cacheDir := t.TempDir()
	hermesDB := filepath.Join(home, ".hermes", "state.db")
	if err := os.MkdirAll(filepath.Dir(hermesDB), 0o755); err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.Local)

	runSQLite(t, hermesDB, `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at REAL, ended_at REAL, last_activity_at REAL);
		CREATE TABLE session_model_usage (
			session_id TEXT, model TEXT, billing_provider TEXT, billing_base_url TEXT,
			billing_mode TEXT, task TEXT, api_call_count INTEGER,
			input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER,
			cache_write_tokens INTEGER, estimated_cost_usd REAL, actual_cost_usd REAL,
			first_seen REAL, last_seen REAL
		);
		INSERT INTO sessions VALUES ('s1', `+strconv.FormatInt(day1.Unix(), 10)+`, NULL, `+strconv.FormatInt(day1.Unix(), 10)+`);
		INSERT INTO session_model_usage VALUES ('s1', 'deepseek-v4.1-flash', 'p', 'u', '', '', 2, 100, 20, 0, 0, 0.10, 0, `+strconv.FormatInt(day1.Unix(), 10)+`, `+strconv.FormatInt(day1.Unix(), 10)+`);
	`)

	s1 := NewScanner(home, cacheDir)
	if err := s1.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	// Same row grows on day 2 from 100 -> 150 input tokens and 2 -> 3 calls.
	// Ensure mtime/size changes so dbSignature triggers a reread.
	runSQLite(t, hermesDB, `
		UPDATE sessions SET last_activity_at = `+strconv.FormatInt(day2.Unix(), 10)+` WHERE id = 's1';
		UPDATE session_model_usage
		SET api_call_count = 3, input_tokens = 150, output_tokens = 35, estimated_cost_usd = 0.16, last_seen = `+strconv.FormatInt(day2.Unix(), 10)+`
		WHERE session_id = 's1';
	`)

	// New Scanner loading from cacheDir must preserve day 1's 100 tokens and
	// attribute only the +50 delta (+1 call) to day 2.
	s2 := NewScanner(home, cacheDir)
	if err := s2.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	sumToday := s2.Summary(RangeToday, day2)
	hToday := sumToday.ByTool[ToolHermes]
	if hToday.Input != 50 || hToday.Output != 15 || hToday.Requests != 1 {
		t.Fatalf("hermes today delta = %+v, want in=50 out=15 reqs=1", hToday)
	}

	sum7d := s2.Summary(Range7d, day2)
	h7d := sum7d.ByTool[ToolHermes]
	if h7d.Input != 150 || h7d.Output != 35 || h7d.Requests != 3 {
		t.Fatalf("hermes 7d totals = %+v, want in=150 out=35 reqs=3", h7d)
	}
	if len(sum7d.Buckets) < 2 {
		t.Fatalf("expected 7d buckets, got %d", len(sum7d.Buckets))
	}
	// Last two buckets are day1 and day2.
	bDay1 := sum7d.Buckets[len(sum7d.Buckets)-2].Tokens[ToolHermes]
	bDay2 := sum7d.Buckets[len(sum7d.Buckets)-1].Tokens[ToolHermes]
	if bDay1 != 120 || bDay2 != 65 {
		t.Fatalf("day1=%d day2=%d, want 120 and 65", bDay1, bDay2)
	}
}

func TestScanCopilotSQLite(t *testing.T) {
	if _, err := os.Stat(sqlite3Path); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	sessionStore := filepath.Join(home, ".copilot", "session-store.db")
	dataDB := filepath.Join(home, ".copilot", "data.db")
	if err := os.MkdirAll(filepath.Dir(sessionStore), 0o755); err != nil {
		t.Fatal(err)
	}
	runSQLite(t, sessionStore, `
		CREATE TABLE assistant_usage_events (
			id INTEGER PRIMARY KEY,
			session_id TEXT,
			created_at TEXT,
			model TEXT,
			token_details_json TEXT
		);
		INSERT INTO assistant_usage_events VALUES (
			1, 's1', '2026-11-16T10:00:00Z', 'claude-sonnet-4-5-20250929',
			'{"fresh_input_tokens":300,"output_tokens":80,"cache_read_tokens":1000,"cache_write_tokens":50}'
		);
	`)
	runSQLite(t, dataDB, `
		CREATE TABLE assistant_usage_events (
			id INTEGER PRIMARY KEY,
			session_id TEXT,
			timestamp TEXT,
			model TEXT,
			input_tokens INTEGER,
			output_tokens INTEGER,
			cache_read_tokens INTEGER,
			cache_write_tokens INTEGER
		);
		INSERT INTO assistant_usage_events VALUES (
			1, 's2', '2026-11-16T11:00:00Z', 'gpt-5.3-codex',
			200, 40, 0, 0
		);
	`)

	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sum := s.Summary(RangeAll, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	got := sum.ByTool[ToolCopilot]
	if got.Requests != 2 || got.Input != 500 || got.Output != 120 || got.CacheRead != 1000 || got.CacheWrite != 50 {
		t.Fatalf("copilot totals = %+v, want reqs=2 in=500 out=120 cr=1000 cw=50", got)
	}
	if slices.Contains(s.UnreadableTools(), ToolCopilot) {
		t.Fatalf("UnreadableTools() = %v, should not include copilot when SQLite data exists", s.UnreadableTools())
	}
}
