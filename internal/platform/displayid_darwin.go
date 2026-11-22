//go:build darwin

package platform

// StableDisplayID returns the display's persistent identity. A Mac's
// CGDirectDisplayID is already stable across launches, so the answer is the
// id itself.
func StableDisplayID(id int64) int64 { return id }

// DisplayFriendlyNames returns per-display friendly names keyed by stable
// display id when the window toolkit's Display.Label does not already carry
// them. On macOS, mygo already reads NSScreen.localizedName into Display.Label.
func DisplayFriendlyNames() map[int64]string { return nil }
