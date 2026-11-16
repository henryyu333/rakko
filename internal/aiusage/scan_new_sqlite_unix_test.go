//go:build !windows

package aiusage

import (
	"context"
	"math"
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

func bumpFileMtime(t *testing.T, path string, step int) {
	t.Helper()
	ts := time.Now().Add(time.Duration(step) * 2 * time.Second)
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatal(err)
	}
}

func TestScanHermesRowDeleteRebuildAndRollback(t *testing.T) {
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
	d1 := strconv.FormatInt(day1.Unix(), 10)
	d2 := strconv.FormatInt(day2.Unix(), 10)

	runSQLite(t, hermesDB, `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at REAL, ended_at REAL, last_activity_at REAL);
		CREATE TABLE session_model_usage (
			session_id TEXT, model TEXT, billing_provider TEXT, billing_base_url TEXT,
			billing_mode TEXT, task TEXT, api_call_count INTEGER,
			input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER,
			cache_write_tokens INTEGER, estimated_cost_usd REAL, actual_cost_usd REAL,
			first_seen REAL, last_seen REAL
		);
		INSERT INTO sessions VALUES ('s1', `+d1+`, NULL, `+d1+`);
		INSERT INTO sessions VALUES ('s2', `+d1+`, NULL, `+d1+`);
		INSERT INTO session_model_usage VALUES ('s1', 'deepseek-v4.1-flash', 'p', 'u', '', '', 2, 100, 0, 0, 0, 0.10, 0, `+d1+`, `+d1+`);
		INSERT INTO session_model_usage VALUES ('s2', 'deepseek-v4.1-flash', 'p', 'u', '', '', 2, 100, 0, 0, 0, 0.10, 0, `+d1+`, `+d1+`);
	`)

	scan := func() Summary {
		s := NewScanner(home, cacheDir)
		if err := s.Scan(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		return s.Summary(RangeAll, day2)
	}

	if h := scan().ByTool[ToolHermes]; h.Input != 200 || h.Requests != 4 {
		t.Fatalf("initial = %+v, want in=200 reqs=4", h)
	}

	// Delete s1; its 100 tokens must be removed, leaving only s2 (100).
	// Roll s2 back from 100 -> 40 (counter reset: 100 preserved + 40 new = 140).
	runSQLite(t, hermesDB, `
		DELETE FROM session_model_usage WHERE session_id = 's1';
		UPDATE session_model_usage SET api_call_count = 1, input_tokens = 40, estimated_cost_usd = 0.04, last_seen = `+d2+` WHERE session_id = 's2';
	`)
	bumpFileMtime(t, hermesDB, 1)
	if h := scan().ByTool[ToolHermes]; h.Input != 140 || h.Requests != 3 {
		t.Fatalf("after delete+rollback = %+v, want in=140 reqs=3", h)
	}

	// Rebuild s1 with 50 tokens (only 50 counted, not 100+50).
	// Grow s2 from 40 -> 60 (+20 -> 100+40+20 = 160).
	runSQLite(t, hermesDB, `
		INSERT INTO session_model_usage VALUES ('s1', 'deepseek-v4.1-flash', 'p', 'u', '', '', 1, 50, 0, 0, 0, 0.05, 0, `+d2+`, `+d2+`);
		UPDATE session_model_usage SET api_call_count = 2, input_tokens = 60, estimated_cost_usd = 0.06, last_seen = `+d2+` WHERE session_id = 's2';
	`)
	bumpFileMtime(t, hermesDB, 2)
	if h := scan().ByTool[ToolHermes]; h.Input != 210 || h.Requests != 5 {
		t.Fatalf("after rebuild+grow = %+v, want in=210 (50+160) reqs=5", h)
	}
}

func TestScanHermesCostOnlyReconcile(t *testing.T) {
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
	d1 := strconv.FormatInt(day1.Unix(), 10)

	runSQLite(t, hermesDB, `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at REAL, ended_at REAL, last_activity_at REAL);
		CREATE TABLE session_model_usage (
			session_id TEXT, model TEXT, billing_provider TEXT, billing_base_url TEXT,
			billing_mode TEXT, task TEXT, api_call_count INTEGER,
			input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER,
			cache_write_tokens INTEGER, estimated_cost_usd REAL, actual_cost_usd REAL,
			first_seen REAL, last_seen REAL
		);
		INSERT INTO sessions VALUES ('s1', `+d1+`, NULL, `+d1+`);
		INSERT INTO session_model_usage VALUES ('s1', 'deepseek-v4.1-flash', 'p', 'u', '', '', 2, 100, 20, 0, 0, 0.08, 0.10, `+d1+`, `+d1+`);
	`)

	scan := func() Totals {
		s := NewScanner(home, cacheDir)
		if err := s.Scan(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		return s.Summary(RangeAll, day1).ByTool[ToolHermes]
	}

	if h := scan(); h.Requests != 2 || math.Abs(h.Cost-0.10) > 1e-9 {
		t.Fatalf("initial = %+v, want reqs=2 cost=0.10", h)
	}

	// Upward cost-only update (0.10 -> 0.20) must not add a request.
	runSQLite(t, hermesDB, `UPDATE session_model_usage SET actual_cost_usd = 0.20 WHERE session_id = 's1';`)
	bumpFileMtime(t, hermesDB, 1)
	if h := scan(); h.Requests != 2 || math.Abs(h.Cost-0.20) > 1e-9 {
		t.Fatalf("after cost increase = %+v, want reqs=2 cost=0.20", h)
	}

	// Downward cost-only update (0.20 -> 0.05) must reconcile down without adding a request.
	runSQLite(t, hermesDB, `UPDATE session_model_usage SET actual_cost_usd = 0.05 WHERE session_id = 's1';`)
	bumpFileMtime(t, hermesDB, 2)
	if h := scan(); h.Requests != 2 || math.Abs(h.Cost-0.05) > 1e-9 {
		t.Fatalf("after cost decrease = %+v, want reqs=2 cost=0.05", h)
	}
}

func TestScanHermesSQLiteZeroResetAndLateCost(t *testing.T) {
	if _, err := os.Stat(sqlite3Path); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	cacheDir := t.TempDir()
	hermesDB := filepath.Join(home, ".hermes", "state.db")
	if err := os.MkdirAll(filepath.Dir(hermesDB), 0o755); err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	day3 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	d1 := strconv.FormatInt(day1.Unix(), 10)
	d2 := strconv.FormatInt(day2.Unix(), 10)
	d3 := strconv.FormatInt(day3.Unix(), 10)

	runSQLite(t, hermesDB, `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at REAL, ended_at REAL, last_activity_at REAL);
		CREATE TABLE session_model_usage (
			session_id TEXT, model TEXT, billing_provider TEXT, billing_base_url TEXT,
			billing_mode TEXT, task TEXT, api_call_count INTEGER,
			input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER,
			cache_write_tokens INTEGER, estimated_cost_usd REAL, actual_cost_usd REAL,
			first_seen REAL, last_seen REAL
		);
		INSERT INTO sessions VALUES ('szero', `+d1+`, NULL, `+d1+`);
		INSERT INTO sessions VALUES ('scost', `+d1+`, NULL, `+d1+`);
		INSERT INTO session_model_usage VALUES ('szero', 'claude-sonnet-4-5-20250929', 'p', 'u', '', '', 2, 100, 0, 0, 0, 0, 0.10, `+d1+`, `+d1+`);
		INSERT INTO session_model_usage VALUES ('scost', 'claude-sonnet-4-5-20250929', 'p', 'u', '', '', 1, 100000, 0, 0, 0, 0, 0, `+d1+`, `+d1+`);
	`)

	scan := func() Totals {
		s := NewScanner(home, cacheDir)
		if err := s.Scan(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		return s.Summary(RangeAll, day3).ByTool[ToolHermes]
	}

	_ = scan()

	// Step 2: szero resets all counters to 0 (row stays in DB!); scost grows 100k -> 200k still without cost.
	runSQLite(t, hermesDB, `
		UPDATE session_model_usage SET api_call_count = 0, input_tokens = 0, actual_cost_usd = 0, last_seen = `+d2+` WHERE session_id = 'szero';
		UPDATE session_model_usage SET api_call_count = 2, input_tokens = 200000, last_seen = `+d2+` WHERE session_id = 'scost';
	`)
	bumpFileMtime(t, hermesDB, 1)
	_ = scan()

	// Step 3: szero grows 0 -> 60 ($0.06) -> szero must total 160 ($0.16), NOT 60!
	// scost receives actual_cost_usd = 0.20 without token/call change -> scost must total $0.20 (NOT $0.60+$0.20)!
	runSQLite(t, hermesDB, `
		UPDATE session_model_usage SET api_call_count = 1, input_tokens = 60, actual_cost_usd = 0.06, last_seen = `+d3+` WHERE session_id = 'szero';
		UPDATE session_model_usage SET actual_cost_usd = 0.20, last_seen = `+d3+` WHERE session_id = 'scost';
	`)
	bumpFileMtime(t, hermesDB, 2)
	got := scan()
	// szero: in=160, reqs=3, cost=0.16; scost: in=200000, reqs=2, cost=0.20 -> total in=200160, reqs=5, cost=0.36.
	if got.Input != 200160 || got.Requests != 5 || got.Unpriced != 0 || math.Abs(got.Cost-0.36) > 1e-9 {
		t.Fatalf("SQLite Hermes zero-reset + late-cost = %+v, want in=200160 reqs=5 unpriced=0 cost=0.36", got)
	}
}

func TestScanCopilotDedupeDBAndOTEL(t *testing.T) {
	if _, err := os.Stat(sqlite3Path); err != nil {
		t.Skip("sqlite3 not installed")
	}

	t.Run("shared_request_id", func(t *testing.T) {
		home := t.TempDir()
		sessionStore := filepath.Join(home, ".copilot", "session-store.db")
		dataDB := filepath.Join(home, ".copilot", "data.db")
		if err := os.MkdirAll(filepath.Dir(sessionStore), 0o755); err != nil {
			t.Fatal(err)
		}
		runSQLite(t, sessionStore, `
			CREATE TABLE assistant_usage_events (
				id INTEGER PRIMARY KEY, session_id TEXT, request_id TEXT,
				created_at TEXT, model TEXT, input_tokens INTEGER, output_tokens INTEGER
			);
			INSERT INTO assistant_usage_events VALUES (1, 's1', 'req-1', '2026-11-16T10:00:00Z', 'gpt-5.3-codex', 300, 80);
		`)
		runSQLite(t, dataDB, `
			CREATE TABLE assistant_usage_events (
				id INTEGER PRIMARY KEY, session_id TEXT, request_id TEXT,
				created_at TEXT, model TEXT, input_tokens INTEGER, output_tokens INTEGER
			);
			INSERT INTO assistant_usage_events VALUES (99, 's1', 'req-1', '2026-11-16T10:00:00Z', 'gpt-5.3-codex', 300, 80);
		`)
		writeTestFile(t, filepath.Join(home, ".copilot", "otel", "events.jsonl"), []byte(
			`{"id":"req-1","timestamp":"2026-11-16T10:00:00Z","model":"gpt-5.3-codex","usage":{"input_tokens":300,"output_tokens":80}}`+"\n"+
				`{"id":"req-2","timestamp":"2026-11-16T10:05:00Z","model":"gpt-5.3-codex","usage":{"input_tokens":100,"output_tokens":20}}`+"\n",
		))
		s := NewScanner(home, t.TempDir())
		if err := s.Scan(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		got := s.Summary(RangeAll, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)).ByTool[ToolCopilot]
		if got.Requests != 2 || got.Input != 400 || got.Output != 100 {
			t.Fatalf("copilot dedupe = %+v, want reqs=2 in=400 out=100", got)
		}
	})

	t.Run("no_shared_id_prefers_db_over_otel", func(t *testing.T) {
		home := t.TempDir()
		sessionStore := filepath.Join(home, ".copilot", "session-store.db")
		if err := os.MkdirAll(filepath.Dir(sessionStore), 0o755); err != nil {
			t.Fatal(err)
		}
		runSQLite(t, sessionStore, `
			CREATE TABLE assistant_usage_events (
				id INTEGER PRIMARY KEY, session_id TEXT,
				created_at TEXT, model TEXT, input_tokens INTEGER, output_tokens INTEGER
			);
			INSERT INTO assistant_usage_events VALUES (1, 's1', '2026-11-16T10:00:00Z', 'gpt-5.3-codex', 300, 80);
		`)
		writeTestFile(t, filepath.Join(home, ".copilot", "otel", "events.jsonl"), []byte(
			`{"id":"otel-1","timestamp":"2026-11-16T10:00:00Z","model":"gpt-5.3-codex","usage":{"input_tokens":300,"output_tokens":80}}`+"\n",
		))
		s := NewScanner(home, t.TempDir())
		if err := s.Scan(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		got := s.Summary(RangeAll, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)).ByTool[ToolCopilot]
		if got.Requests != 1 || got.Input != 300 || got.Output != 80 {
			t.Fatalf("copilot db-priority = %+v, want reqs=1 in=300 out=80", got)
		}
	})

	t.Run("otel_wrapper_id_vs_db_request_id_and_mismatched_two_dbs", func(t *testing.T) {
		home := t.TempDir()
		sessionStore := filepath.Join(home, ".copilot", "session-store.db")
		dataDB := filepath.Join(home, ".copilot", "data.db")
		if err := os.MkdirAll(filepath.Dir(sessionStore), 0o755); err != nil {
			t.Fatal(err)
		}
		// session-store.db has request_id='req-shared' for s1.
		runSQLite(t, sessionStore, `
			CREATE TABLE assistant_usage_events (
				id INTEGER PRIMARY KEY, session_id TEXT, request_id TEXT,
				created_at TEXT, model TEXT, input_tokens INTEGER, output_tokens INTEGER
			);
			INSERT INTO assistant_usage_events VALUES (1, 's1', 'req-shared', '2026-11-16T10:00:00Z', 'gpt-5.3-codex', 300, 80);
		`)
		// data.db has NO request_id column at all (mismatched ID fields between DBs),
		// plus a `sessions` table or `assistant_usage_events` row for s1 AND s2!
		runSQLite(t, dataDB, `
			CREATE TABLE assistant_usage_events (
				id INTEGER PRIMARY KEY, session_id TEXT,
				created_at TEXT, model TEXT, input_tokens INTEGER, output_tokens INTEGER
			);
			INSERT INTO assistant_usage_events VALUES (99, 's1', '2026-11-16T10:00:00Z', 'gpt-5.3-codex', 300, 80);
			INSERT INTO assistant_usage_events VALUES (100, 's2', '2026-11-16T10:02:00Z', 'gpt-5.3-codex', 200, 50);
		`)
		// OTEL has top-level `id: "otel-line-1"` AND `request_id: "req-shared"` (no session_id),
		// plus an OTEL span for s2 (`gen_ai.conversation.id: "s2"`), plus OTEL-only s3.
		writeTestFile(t, filepath.Join(home, ".copilot", "otel", "events.jsonl"), []byte(
			`{"id":"otel-line-1","uuid":"u-line-1","request_id":"req-shared","timestamp":"2026-11-16T10:00:00Z","model":"gpt-5.3-codex","usage":{"input_tokens":300,"output_tokens":80}}`+"\n"+
				`{"type":"span","spanId":"sp-s2","endTime":[1794823320,0],"attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"s2","gen_ai.response.model":"gpt-5.3-codex","gen_ai.usage.input_tokens":200,"gen_ai.usage.output_tokens":50}}`+"\n"+
				`{"type":"span","spanId":"sp-s3","endTime":[1794823400,0],"attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"s3","gen_ai.response.model":"gpt-5.3-codex","gen_ai.usage.input_tokens":150,"gen_ai.usage.output_tokens":30}}`+"\n",
		))
		s := NewScanner(home, t.TempDir())
		if err := s.Scan(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		got := s.Summary(RangeAll, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)).ByTool[ToolCopilot]
		if got.Requests != 3 || got.Input != 650 || got.Output != 160 {
			t.Fatalf("copilot mismatched DBs + OTEL = %+v, want reqs=3 in=650 out=160", got)
		}
	})

	t.Run("data_db_sessions_totals_fallback", func(t *testing.T) {
		home := t.TempDir()
		sessionStore := filepath.Join(home, ".copilot", "session-store.db")
		dataDB := filepath.Join(home, ".copilot", "data.db")
		if err := os.MkdirAll(filepath.Dir(sessionStore), 0o755); err != nil {
			t.Fatal(err)
		}
		runSQLite(t, sessionStore, `
			CREATE TABLE assistant_usage_events (
				id INTEGER PRIMARY KEY, session_id TEXT,
				created_at TEXT, model TEXT, input_tokens INTEGER, output_tokens INTEGER
			);
			INSERT INTO assistant_usage_events VALUES (1, 's1', '2026-11-16T10:00:00Z', 'gpt-5.3-codex', 300, 80);
		`)
		// data.db has `sessions` aggregate table: s1 must be skipped (owned by session-store.db), s2 kept!
		runSQLite(t, dataDB, `
			CREATE TABLE sessions (
				id TEXT PRIMARY KEY, created_at TEXT, model TEXT,
				total_input_tokens INTEGER, total_output_tokens INTEGER, total_cached_tokens INTEGER
			);
			INSERT INTO sessions VALUES ('s1', '2026-11-16T10:00:00Z', 'gpt-5.3-codex', 300, 80, 0);
			INSERT INTO sessions VALUES ('s2', '2026-11-16T11:00:00Z', 'gpt-5.3-codex', 500, 60, 400);
		`)
		s := NewScanner(home, t.TempDir())
		if err := s.Scan(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		got := s.Summary(RangeAll, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)).ByTool[ToolCopilot]
		// s1: in=300, out=80; s2: in=500-400=100, out=60, cr=400 -> total in=400, out=140, cr=400, reqs=2.
		if got.Requests != 2 || got.Input != 400 || got.Output != 140 || got.CacheRead != 400 {
			t.Fatalf("copilot data.db sessions fallback = %+v, want reqs=2 in=400 out=140 cr=400", got)
		}
	})
}

func TestScanSQLiteUnsupportedSchemaVsEmptyTable(t *testing.T) {
	if _, err := os.Stat(sqlite3Path); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	cacheDir := t.TempDir()

	// Mark all 4 SQLite-backed tools installed.
	writeTestFile(t, filepath.Join(home, ".copilot", "config.json"), []byte("{}"))
	writeTestFile(t, filepath.Join(home, ".hermes", "config.yaml"), []byte("model: test\n"))
	writeTestFile(t, filepath.Join(xdgConfigHome(home), "devin", "config.json"), []byte("{}"))
	writeTestFile(t, filepath.Join(xdgConfigHome(home), "opencode", "opencode.json"), []byte("{}"))

	copilotDB := filepath.Join(home, ".copilot", "session-store.db")
	hermesDB := filepath.Join(home, ".hermes", "state.db")
	devinDB := filepath.Join(xdgDataHome(home), "devin", "cli", "sessions.db")
	opencodeDB := opencodeDBPath(home)
	for _, p := range []string{copilotDB, hermesDB, devinDB, opencodeDB} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		runSQLite(t, p, `CREATE TABLE unrelated_meta (k TEXT PRIMARY KEY);`)
	}

	s1 := NewScanner(home, cacheDir)
	if err := s1.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(s1.FailedTools()) != 0 {
		t.Fatalf("FailedTools() = %v, want empty for unsupported schema", s1.FailedTools())
	}
	for _, tl := range []Tool{ToolCopilot, ToolHermes, ToolDevin, ToolOpencode} {
		if !slices.Contains(s1.UnreadableTools(), tl) {
			t.Fatalf("UnreadableTools() = %v, want %s when DB lacks supported tables", s1.UnreadableTools(), tl)
		}
	}

	// Incremental rescan from cache must preserve the unsupported-schema status.
	s2 := NewScanner(home, cacheDir)
	if err := s2.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, tl := range []Tool{ToolCopilot, ToolHermes, ToolDevin, ToolOpencode} {
		if !slices.Contains(s2.UnreadableTools(), tl) {
			t.Fatalf("cached UnreadableTools() = %v, want %s", s2.UnreadableTools(), tl)
		}
	}

	// Now create the expected tables (still 0 rows): tools must count as having data.
	runSQLite(t, copilotDB, `CREATE TABLE assistant_usage_events (id INTEGER PRIMARY KEY, session_id TEXT, created_at TEXT, model TEXT, input_tokens INTEGER, output_tokens INTEGER);`)
	runSQLite(t, hermesDB, `CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at REAL, ended_at REAL, last_activity_at REAL); CREATE TABLE session_model_usage (session_id TEXT, model TEXT, billing_provider TEXT, billing_base_url TEXT, billing_mode TEXT, task TEXT, api_call_count INTEGER, input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER, cache_write_tokens INTEGER, estimated_cost_usd REAL, actual_cost_usd REAL, first_seen REAL, last_seen REAL);`)
	runSQLite(t, devinDB, `CREATE TABLE sessions (id TEXT PRIMARY KEY, model TEXT); CREATE TABLE message_nodes (row_id INTEGER PRIMARY KEY, session_id TEXT, node_id INTEGER, chat_message TEXT, created_at INTEGER);`)
	runSQLite(t, opencodeDB, `CREATE TABLE message (id TEXT PRIMARY KEY, time_created INTEGER, data TEXT);`)
	for _, p := range []string{copilotDB, hermesDB, devinDB, opencodeDB} {
		bumpFileMtime(t, p, 3)
	}

	s3 := NewScanner(home, cacheDir)
	if err := s3.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(s3.FailedTools()) != 0 {
		t.Fatalf("FailedTools() = %v, want empty for empty tables", s3.FailedTools())
	}
	for _, tl := range []Tool{ToolCopilot, ToolHermes, ToolDevin, ToolOpencode} {
		if slices.Contains(s3.UnreadableTools(), tl) {
			t.Fatalf("UnreadableTools() = %v, should not include %s when supported table exists (0 rows)", s3.UnreadableTools(), tl)
		}
	}
}
