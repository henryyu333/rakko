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
	// An un-peeking notched shell on macOS spans the hardware camera
	// housing (32pt + hover pad), which sits in the menu bar rather than
	// over an application's title bar.
	notchTopEdgeHoverOnly = false
	// Once the desktop preview window carries a dragged card, the Mac shows
	// no landing slot at all: the preview is the only copy the pointer sees,
	// and it always hid the dashed box as soon as that window came up.
	dragSlotFollowsPointer = false
	// On macOS, drag previews live in a higher NSWindowLevel above the panel.
	toolsDragKeepsMouse = false
	// Keep Mac drag preview paint/frame updates verbatim; Windows avoids 125Hz dual-window churn.
	dragPreviewSkipRedundantPaint = false
	// Interactive NTFS FlushFileBuffers stalls Windows drops; keep Mac sync writes verbatim.
	toolsInteractiveNoSync = false
	// A right click selects its tile, as Finder does. The native menu holds
	// the frame that selects it until the menu closes, so the ring appears
	// right after; Windows paints the menu's owner while it is open, and
	// keeps the selection as it was instead.
	contextMenuSelectsItem = true
)
