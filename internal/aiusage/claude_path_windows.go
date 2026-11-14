//go:build windows

package aiusage

import (
	"os"
	"path/filepath"
)

// claudeCandidates lists the names and install locations Windows uses: the
// native installer's .exe under %USERPROFILE%\.local\bin, an npm global
// install's claude.cmd under %APPDATA%\npm, and the Bun and Claude-local
// installs. exec.LookPath already applies PATHEXT for the PATH lookup.
func claudeCandidates(env Env) []string {
	dirs := []string{
		joinHome(env.Home, ".local", "bin"),
		joinHome(env.Home, ".claude", "local"),
		joinHome(env.Home, ".bun", "bin"),
	}
	if appData, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(appData, "npm"))
	}
	names := []string{"claude.exe", "claude.cmd", "claude.bat"}
	out := make([]string, 0, len(dirs)*len(names))
	for _, dir := range dirs {
		for _, name := range names {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// executableFile reports whether fi is a file this platform can execute:
// Windows has no execute bit, and os/exec runs a .cmd or .bat through
// cmd.exe.
func executableFile(fi os.FileInfo) bool {
	return fi.Mode().IsRegular()
}
