//go:build windows

package main

// What the Windows build offers: no system-permission walk (Windows has no
// equivalent grants, so the page, its tray item and its guide step are
// absent), and no move lock — mygo's Windows backend answers
// DisableMove:true by making the window immovable for the program too.
const (
	permissionsSupported = false
	disableMoveOverlay   = false
	// With no hardware housing, the idle simulated notch draws nothing;
	// requiring the pointer to touch the screen's top edge keeps an
	// invisible 36pt hot zone from covering a maximized window's tabs or
	// title bar.
	notchTopEdgeHoverOnly = true
	// Keep the notch overlay accepting mouse input while a tools card drag/resize/preview
	// is active so crossing the panel edge never toggles WS_EX_TRANSPARENT or re-layers
	// the panel in front of the drag preview HWND (which shares the single topmost band).
	toolsDragKeepsMouse = true
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
