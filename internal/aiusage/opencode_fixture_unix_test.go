//go:build !windows

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
	db := opencodeDBPath(home)
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

// TestOpencodeReadsWhenTheUserDirectoryIsReadOnly: the copy removes the need
// for the database's folder to be writable (sqlite3's -shm would fail there).
func TestOpencodeReadsWhenTheUserDirectoryIsReadOnly(t *testing.T) {
	home := t.TempDir()
	db := opencodeTestDB(t, home)
	runSQLite(t, db, `PRAGMA journal_mode=WAL;`)
	dir := filepath.Dir(db)
	if err := os.Remove(filepath.Join(dir, "opencode.db-shm")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := s.Summary(RangeToday, fixtureNow()).ByTool[ToolOpencode]; got.Requests != 2 {
		t.Fatalf("opencode records through a read-only folder: %+v", got)
	}
}
