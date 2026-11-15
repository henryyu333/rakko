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
	// A carried otter returns to the notch only when its own box reaches the
	// notch's zone: the pointer being over the notch is not enough, because
	// the Mac's work area stops the window where the old rule already
	// answered, and widening it would stow an otter the user dropped lower.
	petPointerReturnSupported = false
	// A note whose anchor leaves no room above or below opens above it and
	// is clamped to the top of the work area, as it always has.
	noteStaysBelowTallAnchor = false
)
