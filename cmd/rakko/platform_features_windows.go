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
	// A Windows display has no camera housing, so the notch style's
	// measured anchor is a simulated one: the shell draws the notch itself
	// in the closed state, where a Mac leans on the system's own black.
	simulatedNotchHousing = true
	// The desktop preview window carries the card past the page's content
	// box; on Windows the dashed landing slot stays as long as the pointer
	// is still over the page.
	dragSlotFollowsPointer = true
	// Windows keeps painting a native menu's owner while the menu is open,
	// so selecting the right-clicked tile painted its accent ring behind
	// the menu; the Mac shows the ring only after the menu closes. The
	// reference has none open, so Windows leaves the selection alone.
	contextMenuSelectsItem = false
)
