//go:build windows

package aiusage

import (
	"os"
	"path/filepath"
)

// opencodeDBPath is where opencode keeps its database on Windows. opencode
// resolves its data directory as XDG_DATA_HOME or ~/.local/share (its
// global-roots module has no Windows branch), so the default layout matches
// macOS; an explicit XDG_DATA_HOME moves it.
func opencodeDBPath(home string) string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "opencode", "opencode.db")
	}
	return joinHome(home, ".local", "share", "opencode", "opencode.db")
}
