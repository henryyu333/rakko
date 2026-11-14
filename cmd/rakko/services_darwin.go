//go:build darwin

package main

// Finder 右键“服务/快速操作”菜单与应用内“服务”菜单的落点：
// “添加到 Rakko”把选中的文件、文件夹或文字放进托盘。

import (
	"github.com/egoist/mygo"

	"rakko/internal/platform"
)

// installServices registers "添加到 Rakko" so Finder and any app's Services
// menu can send selected files or text into the shelf. A failure is a
// startup notice, not fatal: the shelf still works through drag, paste and
// the clipboard module.
func (a *shelf) installServices() {
	err := platform.InstallAddToRakko(func(paths []string, text string) {
		mygo.RunOnMain(func() { a.serviceReceive(paths, text) })
	})
	if err != nil {
		a.notice(a.appLoc().ServiceMenuUnavailable(err.Error()))
	}
}
