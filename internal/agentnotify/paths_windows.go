//go:build windows

package agentnotify

// Where each tool keeps its files on Windows. The home-relative paths in
// tool.go are the same everywhere; these are the ones each tool's
// documentation gives for Windows, and the AppData folders are the real
// ones (an administrator can redirect them, so the profile's default
// spelling is only a fallback).

import (
	"os"
	"path/filepath"
)

// roamingAppData and localAppData are the user's AppData folders. They are
// variables so tests can point them at a temporary profile.
var (
	roamingAppData = func() string { return os.Getenv("APPDATA") }
	localAppData   = func() string { return os.Getenv("LOCALAPPDATA") }
)

// appDataPath resolves one path under a user's AppData folder, falling back
// to the profile-relative spelling when the environment does not name the
// folder.
func appDataPath(home, folder string, parts ...string) string {
	base := localAppData()
	if folder == "Roaming" {
		base = roamingAppData()
	}
	if base == "" {
		base = join(home, "AppData", folder)
	}
	return filepath.Join(append([]string{base}, parts...)...)
}

// hostToolPaths lists a tool's install markers that are not the shared
// home-relative ones.
func hostToolPaths(tool Tool, home string) []string {
	switch tool {
	case ToolCursor:
		// Cursor keeps its settings under %APPDATA%\Cursor (and its cache
		// under %LOCALAPPDATA%\Cursor); there is no Application Support on
		// Windows.
		return []string{
			appDataPath(home, "Roaming", "Cursor"),
			appDataPath(home, "Local", "Cursor"),
		}
	case ToolKiro:
		// Kiro CLI 2.0 and later run on Windows and keep their per-user
		// files under %APPDATA%\Kiro.
		return []string{
			appDataPath(home, "Roaming", "Kiro"),
			appDataPath(home, "Local", "Kiro"),
		}
	}
	return nil
}

// devinConfigPath is Devin CLI's user configuration on Windows. Its
// documentation is explicit that this is %APPDATA%\devin\config.json, not
// ~/.config/devin/.
func devinConfigPath(home string) string {
	return appDataPath(home, "Roaming", "devin", "config.json")
}

// devinDetect lists what Devin's CLI creates when it is installed.
func devinDetect(home string) []string {
	return []string{devinConfigPath(home)}
}
