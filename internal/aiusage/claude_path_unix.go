//go:build !windows

package aiusage

import (
	"os"
	"path/filepath"
)

// claudeCandidates lists the install locations a shell on this platform
// uses, in the order they are tried after PATH.
func claudeCandidates(env Env) []string {
	dirs := []string{
		joinHome(env.Home, ".local", "bin"),
		joinHome(env.Home, ".claude", "local"),
		joinHome(env.Home, ".npm-global", "bin"),
		joinHome(env.Home, ".bun", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		out = append(out, filepath.Join(dir, "claude"))
	}
	return out
}

// executableFile reports whether fi is a file this platform can execute.
func executableFile(fi os.FileInfo) bool {
	return fi.Mode()&0o111 != 0
}
