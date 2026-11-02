//go:build darwin

package main

import (
	"testing"

	"github.com/egoist/mygo"
)

// 回归序列：服务闪现托盘 → 用户收起 → 手动重新打开 → 点击其他应用菜单栏
// （只归还 Esc，不收起）→ 旧定时器不得再收起用户的托盘。
func TestServiceFlashOwnership(t *testing.T) {
	a := &shelf{}
	// 服务闪现：serviceReceive 中 show 之后登记的这一代。
	a.show(false)
	serviceFlashGen++
	gen := serviceFlashGen
	if !a.serviceFlashMayHide(gen) {
		t.Fatal("fresh flash timer should be allowed to hide")
	}
	// 用户收起，再手动打开：旧代立即作废。
	a.hide(hideExplicit)
	a.show(false)
	if a.serviceFlashMayHide(gen) {
		t.Error("stale flash generation must not hide a reopened panel")
	}
	// 面板内点击取得所有权：hide 之外的路径也要取消自动收起。
	// escHeld/spaceHeld 预置让 holdKeys 跳过真实全局快捷键注册（无 App.Run）。
	a.escHeld, a.spaceHeld = true, true
	serviceFlashGen++
	gen = serviceFlashGen
	a.mouseDown(true, 0, mygo.Point{})
	if a.serviceFlashMayHide(gen) {
		t.Error("a click inside the panel must take ownership")
	}
	a.escHeld, a.spaceHeld = false, false
	// 拖出取得所有权。
	serviceFlashGen++
	gen = serviceFlashGen
	a.takeServiceOwnership()
	if a.serviceFlashMayHide(gen) {
		t.Error("drag-out takeover must cancel the auto-hide")
	}
	// 定时器的那一代在拖拽会话期间不许收起。
	serviceFlashGen++
	gen = serviceFlashGen
	a.dragging = true
	if a.serviceFlashMayHide(gen) {
		t.Error("must not hide while a drag session is live")
	}
	a.dragging = false
	a.ownDrag = true
	if a.serviceFlashMayHide(gen) {
		t.Error("must not hide while an outbound drag is live")
	}
	a.ownDrag = false
	a.hide(hideExplicit)
}
