//go:build darwin

package main

// What the Mac build offers that the Windows skeleton does not: the system
// permission walk, and the overlay window's move lock (the island's own
// drag gesture is the only way it moves).
const (
	permissionsSupported = true
	disableMoveOverlay   = true
	// AppKit keeps the Settings window above the panel's level, so the
	// panel stays open behind it, as it always has.
	overlayHidesForSettings = false
)
