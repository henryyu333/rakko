//go:build darwin

package main

// Finder 右键“服务/快速操作”菜单与应用内“服务”菜单的落点：
// “添加到 Rakko”把选中的文件、文件夹或文字放进托盘。

import (
	"time"

	"github.com/egoist/mygo"

	"shelf/internal/mac"
)

// serviceFlashGen is the generation counter that owns the auto-hide timer
// started by a service flash. serviceReceive bumps it when the flash
// opens the panel; takeServiceOwnership bumps it whenever the user takes
// over — any hide (manual collapse, Esc, click outside, successful
// drag-out), any click inside the panel, or an outbound drag. A queued
// timer whose generation no longer matches does nothing, so a stale flash
// can never hide a panel the user is using.
var serviceFlashGen int

// takeServiceOwnership ends the service flash's ownership of the visible
// panel: the pending auto-hide becomes inert. Called from every user
// action that dismisses or works with the shelf. Main thread only.
func (a *shelf) takeServiceOwnership() {
	serviceFlashGen++
}

// installServices registers "添加到 Rakko" so Finder and any app's Services
// menu can send selected files or text into the shelf. A failure is a
// startup notice, not fatal: the shelf still works through drag, paste and
// the clipboard module.
func (a *shelf) installServices() {
	err := mac.InstallServiceProvider(func(paths []string, text string) {
		mygo.RunOnMain(func() { a.serviceReceive(paths, text) })
	})
	if err != nil {
		a.notice(a.appLoc().ServiceMenuUnavailable(err.Error()))
	}
}

// serviceFlashMayHide reports whether the auto-hide timer generation g is
// still allowed to collapse the panel: the flash must still own it (no
// user takeover since it armed) and no drag may be live.
func (a *shelf) serviceFlashMayHide(gen int) bool {
	return gen == serviceFlashGen && a.visible && !a.quitting && !a.dragging && !a.pointerDrag && !a.ownDrag
}

// serviceReceive stores one service payload on the main thread. Files win
// over text, matching receive(); both empty means the provider already
// faulted the request. The panel flashes briefly so the user sees the item
// land, then retracts itself unless they start using it — less intrusive
// than leaving the panel open after a menu action elsewhere, and without
// holding Esc or Space while it flashes.
func (a *shelf) serviceReceive(paths []string, text string) {
	if a.store == nil {
		return
	}
	var err error
	switch {
	case len(paths) > 0:
		err = a.store.AddFiles(paths)
	case text != "":
		err = a.store.AddText(text)
	default:
		return
	}
	if err == nil {
		a.toastStatus(a.appLoc().ServiceReceived(), time.Now().Add(3*time.Second), statusOK)
	}
	a.changed(err)
	// 托盘已显示时用户正在看它，交给现有状态栏，不再闪动。
	if a.visible {
		return
	}
	a.show(false)
	a.releaseKeys()
	serviceFlashGen++
	gen := serviceFlashGen
	time.AfterFunc(2*time.Second, func() {
		mygo.RunOnMain(func() {
			if a.serviceFlashMayHide(gen) {
				a.hide(hidePassive)
			}
		})
	})
}
