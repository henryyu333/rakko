//go:build darwin

package main

import "time"

// What the Mac build offers that the Windows skeleton does not: the system
// permission walk, and the overlay window's move lock (the island's own
// drag gesture is the only way it moves).
const (
	permissionsSupported = true
	disableMoveOverlay   = true
	// An un-peeking notched shell on macOS spans the hardware camera
	// housing (32pt + hover pad), which sits in the menu bar rather than
	// over an application's title bar.
	notchTopEdgeHoverOnly = false
	// On macOS, drag previews live in a higher NSWindowLevel above the panel.
	toolsDragKeepsMouse = false
	// macOS reads NSDragPboard directly at any time during a drag, so the
	// drop zone already knows the dragged paths before the pointer enters
	// the panel window.
	hoverFilesFromOffer = false
	// AirDrop is macOS-only; Windows omits Apple-specific sharing entry points.
	airDropSupported = true
	// macOS still opens an empty placeholder pin for unknown card IDs.
	skipUnknownPins = false
	// macOS launches screencapture -i interactively, so folding the panel needs no pre-sleep.
	ocrPanelSettle = 0 * time.Millisecond
)
