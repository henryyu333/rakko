//go:build !windows

package aiusage

import "path/filepath"

// sqlite3Path is the system's SQLite shell, used as a read-only query tool.
const sqlite3Path = "/usr/bin/sqlite3"

// opencodeDBPath is where opencode keeps its database on this platform.
func opencodeDBPath(home string) string {
	return filepath.Join(xdgDataHome(home), "opencode", "opencode.db")
}
