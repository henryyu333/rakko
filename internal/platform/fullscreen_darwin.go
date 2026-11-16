//go:build darwin

package platform

// ForegroundFullscreen reports whether a foreign fullscreen application owns
// the pointer's display. macOS handles fullscreen Spaces and menu-bar hover at
// the window-server level, so Rakko never suppresses top-edge hover here.
func ForegroundFullscreen() bool { return false }
