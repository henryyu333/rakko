package main

import "runtime"

// forWindows says the app is built for Windows. Views read it to choose the
// wording and keycaps Windows users know; the macOS wording is the default.
// It is a variable so one test build can check both wordings.
var forWindows = runtime.GOOS == "windows"

// osText returns the Windows text when built for Windows, and the macOS text
// otherwise. Both are looked up in the same catalog: each platform's wording
// lives under its own key, and macOS keeps the original entries untouched.
func osText(mac, win string) string {
	return osTextFor(forWindows, mac, win)
}

func osTextFor(windows bool, mac, win string) string {
	if windows {
		return win
	}
	return mac
}
