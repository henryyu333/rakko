//go:build windows

package agentnotify

// Where each tool keeps its files on Windows. The home-relative paths in
// tool.go are the same everywhere; these are the ones each tool's
// documentation gives for Windows.

// hostToolPaths lists a tool's install markers that are not the shared
// home-relative ones.
func hostToolPaths(tool Tool, home string) []string {
	switch tool {
	case ToolCursor:
		// Cursor keeps its settings under %APPDATA%\Cursor (and its cache
		// under %LOCALAPPDATA%\Cursor); there is no Application Support on
		// Windows.
		return paths(home, "AppData/Roaming/Cursor", "AppData/Local/Cursor")
	case ToolKiro:
		// Kiro CLI 2.0 and later run on Windows and keep their per-user
		// files under %APPDATA%\Kiro.
		return paths(home, "AppData/Roaming/Kiro", "AppData/Local/Kiro")
	}
	return nil
}

// devinConfigPath is Devin CLI's user configuration on Windows. Its
// documentation is explicit that this is %APPDATA%\devin\config.json, not
// ~/.config/devin/.
func devinConfigPath(home string) string {
	return join(home, "AppData", "Roaming", "devin", "config.json")
}

// devinDetect lists what Devin's CLI creates when it is installed.
func devinDetect(home string) []string {
	return paths(home, "AppData/Roaming/devin/config.json")
}
