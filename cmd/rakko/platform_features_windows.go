//go:build windows

package main

// What the Windows build offers: no system-permission walk (Windows has no
// equivalent grants, so the page, its tray item and its guide step are
// absent), and no move lock — mygo's Windows backend answers
// DisableMove:true by making the window immovable for the program too.
const (
	permissionsSupported = false
	disableMoveOverlay   = false
	// The overlay's content is composited above the window stack: a
	// Settings window opened under it would show the panel's page and
	// capsule through its own, so the panel closes first.
	overlayHidesForSettings = true
	// The capsule's return zone is 41pt tall plus its hover margin, while a
	// 96pt otter parked at the work area's top edge has its centre half its
	// height below: the box alone can never reach it, so the pointer the
	// drag carried there decides too.
	petPointerReturnSupported = true
	// The open panel's window is taller than the room above or below it; a
	// note flipped above would land at the work area's top, under the
	// topmost capsule, so it stays below and is clamped instead.
	noteStaysBelowTallAnchor = true
	// With no hardware housing, the idle simulated notch draws nothing;
	// requiring the pointer to touch the screen's top edge keeps an
	// invisible 36pt hot zone from covering a maximized window's tabs or
	// title bar.
	notchTopEdgeHoverOnly = true
	// The desktop preview window carries the card past the page's content
	// box; on Windows the dashed landing slot stays as long as the pointer
	// is still over the page.
	dragSlotFollowsPointer = true
	// Keep the notch overlay accepting mouse input while a tools card drag/resize/preview
	// is active so crossing the panel edge never toggles WS_EX_TRANSPARENT or re-layers
	// the panel in front of the drag preview HWND.
	toolsDragKeepsMouse = true
	// Skip redundant SetWindowPos and tilt-unchanged repaints during 125Hz drag preview moves.
	dragPreviewSkipRedundantPaint = true
	// Move NTFS FlushFileBuffers (layout.Write) off the drop frame on Windows,
	// coalescing rapid drags on a dedicated writer and flushing before exit.
	toolsDeferLayoutWrite = true
	// Windows keeps painting a native menu's owner while the menu is open,
	// so selecting the right-clicked tile painted its accent ring behind
	// the menu; the Mac shows the ring only after the menu closes. The
	// reference has none open, so Windows leaves the selection alone.
	contextMenuSelectsItem = false
	// Windows keeps OLE drag objects in the source process until the
	// pointer enters Rakko's window; hover file paths arrive through
	// ui.DataDragOver's transfer.Offer.Files.
	hoverFilesFromOffer = true
	// Windows omits Apple-specific sharing features (AirDrop / nearby sharing)
	// from context menus, drop actions, and the tools catalog.
	airDropSupported = false
	// Skip restoring desktop pins whose card ID is absent from this platform's
	// catalog (e.g. airdrop from a Mac layout) while preserving their records.
	skipUnknownPins = true
)
