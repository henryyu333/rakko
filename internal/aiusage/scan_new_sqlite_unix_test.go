//go:build !windows

package aiusage

import (
	"context"
	"os"
	"path/filepath"
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
	if h := sum.ByTool[ToolHermes]; h.Requests != 1 || h.Input != 1000 || h.Output != 200 || h.CacheRead != 5000 {
		t.Fatalf("hermes totals wrong: %+v", h)
	}
	if d := sum.ByTool[ToolDevin]; d.Requests != 1 || d.Input != 800 || d.Output != 150 || d.CacheRead != 400 || d.CacheWrite != 50 {
		t.Fatalf("devin totals wrong: %+v", d)
	}
}
