//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// Default pinned apps on Windows: the shell and the everyday tools, by the
// same identity the running-app snapshot reports for a desktop app — the
// executable's path — so a pinned app that is already running appears once,
// in the pinned seat, exactly as on the Mac.
var defaultPinnedApps = defaultPinnedWindowsApps()

func defaultPinnedWindowsApps() []string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	candidates := []string{
		filepath.Join(root, "explorer.exe"),
		filepath.Join(root, "System32", "notepad.exe"),
		filepath.Join(root, "System32", "mspaint.exe"),
		filepath.Join(root, "System32", "calc.exe"),
	}
	out := make([]string, 0, len(candidates))
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}
