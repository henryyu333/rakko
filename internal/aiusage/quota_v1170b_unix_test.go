//go:build !windows

package aiusage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCursorStateDBReadOnly(t *testing.T) {
	if _, err := os.Stat(sqlite3Path); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	paths := cursorStateDBPaths(home)
	if len(paths) == 0 {
		t.Skip("no cursor state db path for platform")
	}
	db := paths[0]
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	sqlText := `CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT);
INSERT INTO ItemTable VALUES ('cursorAuth/accessToken', 'cur_tok_readonly');
INSERT INTO ItemTable VALUES ('cursorAuth/stripeMembershipType', 'pro');`
	cmd := exec.Command(sqlite3Path, db, sqlText)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	// Make both DB and folder read-only so any writable open fails or cannot create journals.
	if err := os.Chmod(db, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(db), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Dir(db), 0o755)
		_ = os.Chmod(db, 0o644)
	})

	tok, plan, ok := readCursorCreds(context.Background(), Env{Home: home})
	if !ok || tok != "cur_tok_readonly" || plan != "Pro" {
		t.Fatalf("readCursorCreds = (%q, %q, %v), want (cur_tok_readonly, Pro, true)", tok, plan, ok)
	}
}
