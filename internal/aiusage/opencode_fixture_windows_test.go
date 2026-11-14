//go:build windows

package aiusage

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // the driver the Windows reader uses
)

// opencodeTestDB builds the fixture database at the path the scanner reads:
// the fixture SQL creates the message table and its rows.
func opencodeTestDB(t *testing.T, home string) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", "")
	db := opencodeDBPath(home)
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile("testdata/readers/opencode/opencode.sql")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, string(text))
	return db
}

// runSQLite applies statements to the test database through the same
// driver the reader uses.
func runSQLite(t *testing.T, db, statements string) {
	t.Helper()
	execSQL(t, db, statements)
}

func execSQL(t *testing.T, db, statements string) {
	t.Helper()
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatalf("opening %s: %v", db, err)
	}
	defer conn.Close()
	for _, stmt := range strings.Split(statements, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("running %q: %v", strings.TrimSpace(stmt), err)
		}
	}
}
