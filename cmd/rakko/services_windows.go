//go:build windows

package main

// The Windows counterpart of the "添加到 Rakko" shell service. Explorer's
// context-menu verb and SendTo shortcut start this executable with the
// selected paths (the entries themselves are the installer's); a running
// Rakko receives the same arguments through mygo's single-instance
// hand-off. platform.ConsumeServiceArgs turns either launch into a
// payload, and the receiver below stores it.

import (
	"github.com/egoist/mygo"

	"rakko/internal/platform"
)

// installServices installs the receiver for Explorer's launch arguments
// and drains anything a cold start queued before the store was open. A
// failure is a startup notice, not fatal: the shelf still works through
// drag, paste and the clipboard module.
func (a *shelf) installServices() {
	err := platform.InstallAddToRakko(func(paths []string, text string) {
		mygo.RunOnMain(func() { a.serviceReceive(paths, text) })
	})
	if err != nil {
		a.notice(a.appLoc().ServiceMenuUnavailable(err.Error()))
	}
}
