//go:build windows

package main

// What the Windows build offers: no system-permission walk (Windows has no
// equivalent grants, so the page, its tray item and its guide step are
// absent), and no move lock — mygo's Windows backend answers
// DisableMove:true by making the window immovable for the program too.
const (
	permissionsSupported = false
	disableMoveOverlay   = false
)
