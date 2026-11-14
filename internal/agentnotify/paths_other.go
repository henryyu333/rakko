//go:build !windows

package agentnotify

// Where each tool keeps its files on macOS. These paths, and the ones in
// tool.go, are what the shipping build has always used.

// hostToolPaths lists a tool's install markers that are not the shared
// home-relative ones.
func hostToolPaths(tool Tool, home string) []string {
	switch tool {
	case ToolCursor:
		return paths(home, "Library/Application Support/Cursor")
	case ToolKiro:
		return paths(home, "Library/Application Support/Kiro")
	}
	return nil
}

// devinConfigPath is Devin CLI's user configuration.
func devinConfigPath(home string) string {
	return join(home, ".config", "devin", "config.json")
}

// devinDetect lists what Devin's CLI creates when it is installed.
func devinDetect(home string) []string {
	return paths(home, ".config/devin/config.json", ".config/devin/cli")
}
