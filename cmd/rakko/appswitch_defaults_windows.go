//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// calculatorAppID is the packaged Calculator's identity — the AppUserModelID
// the running-app snapshot reports for it. System32\calc.exe is only the
// package's launcher stub: pinning that path left the running Calculator
// outside its pinned seat and listed a second time in the running wing.
const calculatorAppID = "Microsoft.WindowsCalculator_8wekyb3d8bbwe!App"

// defaultPinnedApps on Windows: the shell and the everyday tools, by the
// same identity the running-app snapshot reports for them — the
// executable's path for a desktop app, the package's AppUserModelID for a
// packaged one — so a pinned app that is already running appears once, in
// the pinned seat, exactly as on the Mac.
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
	}
	out := make([]string, 0, len(candidates)+1)
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
		}
	}
	// A packaged app has no file to stat: the switcher's own AppPath
	// resolves its Apps folder entry when it filters the defaults, and
	// drops it again when the package is not installed.
	return append(out, calculatorAppID)
}
