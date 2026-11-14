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
)
