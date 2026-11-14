//go:build windows

package main

// newBundleName is what the guide calls the app the user runs: on Windows
// the executable, not a .app bundle. The in-app rename of an old bundle is
// a macOS-only repair.
const newBundleName = "Rakko.exe"

// selfRenameBundle is a no-op on Windows: NSIS installs land at their final
// name, and an update replaces files in place.
func selfRenameBundle() {}
