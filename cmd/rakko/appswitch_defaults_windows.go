//go:build windows

package main

// Default pinned apps on Windows: the shell and the everyday tools, by the
// identities the app switcher resolves (an executable name, or a path).
var defaultPinnedApps = []string{
	"explorer.exe",
	"notepad.exe",
	"mspaint.exe",
	"calc.exe",
}
