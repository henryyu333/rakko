package aiusage

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func opencodeTestDB(t *testing.T, home string) string {
	t.Helper()
	if _, err := os.Stat(sqlite3Path); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(home, opencodeDB)
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("testdata/readers/opencode/opencode.sql")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sqlite3Path, db)
	cmd.Stdin = bytes.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating test db: %v: %s", err, out)
	}
	return db
}

func runSQLite(t *testing.T, db, sql string) {
	t.Helper()
	cmd := exec.Command(sqlite3Path, db, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
}

func TestOpencodeReader(t *testing.T) {
	home := t.TempDir()
	db := opencodeTestDB(t, home)
	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sum := s.Summary(RangeToday, fixtureNow())
	o := sum.ByTool[ToolOpencode]
	if o.Requests != 2 || o.Input != 300 || o.Output != 30 ||
		o.CacheRead != 60 || o.CacheWrite != 6 {
		t.Fatalf("opencode totals wrong: %+v", o)
	}
	if o.Unpriced != 0 || o.Cost < 0.0299 || o.Cost > 0.0301 {
		t.Fatalf("opencode price wrong: %+v", o)
	}

	// A message that lands after the first scan is picked up by replacing
	// the whole set (not by a time cursor, which would miss updates).
	runSQLite(t, db, `INSERT INTO message VALUES ('msg_5','ses_1',1792412400000,1792412401000,
		'{"role":"assistant","modelID":"deepseek-v4-flash","tokens":{"input":7,"output":3,"cache":{"read":0,"write":0}},"cost":0.001}')`)
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sum = s.Summary(RangeToday, fixtureNow())
	o = sum.ByTool[ToolOpencode]
	if o.Requests != 3 || o.Input != 307 || o.Output != 33 {
		t.Fatalf("opencode after append wrong: %+v", o)
	}
}

func TestOpencodeFailureIsIsolated(t *testing.T) {
	home := t.TempDir()
	// A readable pi session proves the other readers keep working.
	pi := filepath.Join(home, ".pi/agent/sessions/-p/s.jsonl")
	if err := os.MkdirAll(filepath.Dir(pi), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pi, []byte(`{"type":"message","id":"p1","timestamp":"2026-10-19T10:00:00.000Z","message":{"role":"assistant","model":"m","timestamp":1792404000000,"usage":{"input":10,"output":1}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file with the database's name but not SQLite's contents.
	db := filepath.Join(home, opencodeDB)
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatalf("a broken reader must not fail the scan: %v", err)
	}
	sum := s.Summary(RangeToday, fixtureNow())
	if sum.ByTool[ToolPi].Requests != 1 {
		t.Fatalf("pi records lost: %+v", sum.Total)
	}
	if got := sum.ByTool[ToolOpencode].Requests; got != 0 {
		t.Fatalf("broken opencode returned %d records", got)
	}
	if sum.Total.Requests != 1 || sum.Total.Input != 10 {
		t.Fatalf("a failed reader must not reach the total: %+v", sum.Total)
	}
}
