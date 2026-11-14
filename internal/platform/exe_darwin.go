//go:build darwin

package platform

// Which binary may be written into an agent tool's configuration. A
// development build runs from a temporary directory (or a checkout), so its
// path would stop working the moment the build ends; only a binary inside an
// installed app bundle is stable enough to inject.

import "strings"

// StableExePath reports whether exe is the executable inside an installed
// app bundle and returns the path to write into a tool's configuration.
func StableExePath(exe string) (string, bool) {
	if !strings.Contains(exe, ".app/Contents/MacOS/") {
		return "", false
	}
	return exe, true
}
