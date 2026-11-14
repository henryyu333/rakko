package aiusage

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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
	t.Setenv("XDG_DATA_HOME", "")
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
	db := opencodeDBPath(home)
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

// dirEntries lists a directory's file names, sorted, for the read-only checks.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestOpencodeDoesNotWriteToTheUserDirectory: a WAL database with no -shm is
// exactly the case where sqlite3 -readonly would create one beside it. The
// reader works on a private copy, so the opencode folder keeps its files —
// and the read still succeeds.
func TestOpencodeDoesNotWriteToTheUserDirectory(t *testing.T) {
	home := t.TempDir()
	db := opencodeTestDB(t, home)
	runSQLite(t, db, `PRAGMA journal_mode=WAL;`)
	dir := filepath.Dir(db)
	// Leave db + WAL and no -shm: the next read-only open would create one.
	if err := os.Remove(filepath.Join(dir, "opencode.db-shm")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	before := dirEntries(t, dir)

	cache := t.TempDir()
	s := NewScanner(home, cache)
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Summary(RangeToday, fixtureNow()).ByTool[ToolOpencode]; got.Requests == 0 {
		t.Fatalf("no opencode records read through the copy: %+v", got)
	}
	after := dirEntries(t, dir)
	if !slices.Equal(before, after) {
		t.Fatalf("the opencode directory changed: %v -> %v", before, after)
	}
	for _, name := range dirEntries(t, cache) {
		if strings.HasPrefix(name, "opencode-") {
			t.Fatalf("the read copy was left behind: %s", name)
		}
	}
}
