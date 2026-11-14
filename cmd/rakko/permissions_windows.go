//go:build windows

package main

// The permission entry on Windows. Windows has no per-app grants like Full
// Disk Access or Screen Recording, so the list is empty and every surface
// that could show it (the guide's step, the Settings page, the tray item,
// the sidebar footer) is absent rather than shown empty.

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/locales"
)

// permissionStage is the guided walk's state. The Windows build never
// enters it.
type permissionStage int

const (
	permissionIdle permissionStage = iota
	permissionWaiting
	permissionRelaunch
)

// permissionFlow is the walk's state on the shelf.
type permissionFlow struct {
	stage    permissionStage
	ids      []string
	step     int
	relaunch bool
	timer    permissionTimer
}

type permissionTimer interface{ Stop() bool }

// systemPermission is one item of the unified entry.
type systemPermission struct {
	id      string
	title   string
	short   string
	gloss   string
	detail  string
	l       locales.Messages
	enabled func() bool
	request func() bool
}

// systemPermissions lists every permission the app needs: none.
func systemPermissions() []systemPermission { return nil }

// systemPermissionSet is the list in the language of l.
func systemPermissionSet(l locales.Messages) []systemPermission { return nil }

func permissionByID(id string) (systemPermission, bool) { return systemPermission{}, false }

func missingSystemPermissions() []systemPermission { return nil }

func missingPermissions(list []systemPermission) []systemPermission { return nil }

// permissionMenuItem is the tray item: absent on Windows.
func (a *shelf) permissionMenuItem() *mygo.MenuItem { return nil }

// showPermissions opens Settings, where the page does not exist; the call
// is kept for the shared gesture path.
func (a *shelf) showPermissions() { openSettingsWindow(a) }

var openSettingsWindow = func(a *shelf) { a.openSettings() }

func (a *shelf) stopPermissionFlow() { a.perm = permissionFlow{} }

func (a *shelf) startPermissionFlow() { a.stopPermissionFlow() }

func (a *shelf) startPermissionWalk(ids []string) { a.stopPermissionFlow() }

// settingsPermissionSwitch paints the group's switch; unreachable while
// the page is hidden.
func (a *shelf) settingsPermissionSwitch(c *ui.Context, permissions, missing []systemPermission) {
}

// applyPermissionSwitch is the one switch's action.
func (a *shelf) applyPermissionSwitch(on bool) {}

// relaunchShelf restarts the app after a grant. There are no grants to
// wait for on Windows.
func (a *shelf) relaunchShelf() {}

// accessibilityProbe answers whether accessibility input may be sent.
// Windows has no Accessibility permission, so it is always true; the snap
// gesture's own window access is what the platform reports.
var accessibilityProbe = func() bool { return true }
