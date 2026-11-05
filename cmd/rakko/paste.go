//go:build darwin

package main

// 剪贴板页的粘贴：⏎ 与 ⌘1..⌘9 都是「写剪贴板 → 收起面板 → 把键盘交还给
// 打开面板前的应用 → 确认它确实在前台 → 向它发 ⌘V」同一条路径（Issue #4）。
//
// 粘贴失败绝不伪装成成功：写剪贴板失败时面板不收起；没有目标应用、缺少辅助
// 功能权限、目标应用没接回前台时，内容已经在剪贴板上，提示明确说「已复制」并
// 告诉用户按 ⌘V 或开启辅助功能，不自动申请或重置权限。
//
// 焦点确认在后台 goroutine 里等待（UI 线程不 sleep），每次读前台应用和发键都
// 回到主线程（AppKit 状态只能在那里读）。pasteSeq 代号让快速连续选择、关闭、
// 重开取消上一次尝试，按键绝不落到别的应用。

import (
	"errors"
	"strings"
	"time"

	"rakko/internal/mac"
	"rakko/internal/store"
)

// 粘贴路径的接缝：辅助功能信任、前台应用、按键，以及等待焦点的时间。测试
// 全部替换掉；生产走平台实现。
var (
	pasteTrusted   = mac.AccessibilityTrusted
	pasteFrontmost = mac.FrontmostPID
	pasteActivate  = mac.ActivateApp
	pasteSend      = mac.PostCommandV
	// pasteFocusWait 是收起面板后等待目标应用拿回键盘的上限，
	// pastePollInterval 是检查间隔。
	pasteFocusWait    = 250 * time.Millisecond
	pastePollInterval = 15 * time.Millisecond
)

// pasteContent 是一次选择要粘贴的内容：单项原样，多选则按行合并文本（与
// 多选复制一致）；多选里混入非文本项时拒绝，绝不发送用户没看见的组合。
// 纯函数，单元测试覆盖。
func pasteContent(items []store.Item) (joined string, many, ok bool) {
	switch len(items) {
	case 0:
		return "", false, false
	case 1:
		return "", false, true
	}
	var b strings.Builder
	for i, it := range items {
		if it.Kind != store.KindText {
			return "", false, false
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(it.Text)
	}
	return b.String(), true, true
}

// pasteClipSelection 是剪贴板页的 ⏎：把选中项（多选文本按行合并）写进剪贴板
// 并粘贴到打开面板前的应用。
func (a *shelf) pasteClipSelection() {
	a.pasteItems(a.selection.chosen(a.visibleItems()))
}

// pasteClipIndex 是剪贴板页的 ⌘1..⌘9：按当前筛选、排序后显示顺序的第 n 项，
// 不看选中项。越界不写剪贴板、不收起、不发键。
func (a *shelf) pasteClipIndex(n int) {
	items := a.visibleItems()
	if n < 1 || n > len(items) {
		return
	}
	a.selection.only(items[n-1].ID)
	a.reveal = ""
	if a.win != nil {
		a.win.Invalidate()
	}
	a.pasteItems([]store.Item{items[n-1]})
}

// pasteItems 是唯一的粘贴流程：先写剪贴板，再决定能不能真的粘贴。
func (a *shelf) pasteItems(items []store.Item) {
	text, many, ok := pasteContent(items)
	if !ok {
		a.report(errors.New(a.appLoc().PanelCopyManyFailed()))
		return
	}
	if many {
		if !a.copyText(text) {
			return // 写不进剪贴板：同一提示已经报过，面板留着
		}
	} else if !a.copyItem(items[0]) {
		return
	}
	target := a.pasteTarget()
	// 什么时候都能收起：内容已经在剪贴板上，最坏也只是让用户自己按 ⌘V。
	// 回退提示在收起前写进 status，收起时随胶囊一起出现。
	switch {
	case target <= 0:
		a.toastStatus(a.appLoc().ClipPasteManual(), time.Now().Add(4*time.Second), statusOK)
		a.hide(hideExplicit)
		return
	case !pasteTrusted():
		// 不申请、不重置权限：只说实话并给出两条可行路径。
		a.toastStatus(a.appLoc().ClipPasteNeedsAccessibility(), time.Now().Add(5*time.Second), statusOK)
		a.hide(hideExplicit)
		return
	}
	// 收起会加一代让在途尝试作废；本次尝试在那之后才取得自己的代号。
	a.hide(hideExplicit)
	gen := a.pasteSeq.Add(1)
	goWork(func() { a.finishPaste(target, gen) })
}

// pasteTarget 是粘贴要回到的应用：打开剪贴板面板前拥有键盘的那个。必须在
// hide 清掉 prevAppPID 之前读取。
func (a *shelf) pasteTarget() int {
	pid := a.prevAppPID
	if pid <= 0 || pid == mac.OwnPID() {
		return 0
	}
	return pid
}

// finishPaste 在后台等待目标应用拿回键盘，然后向它发 ⌘V。代号变了（新的选择、
// 收起、重新打开）就放弃；目标没有接回前台就只保留复制，绝不把按键发给别的
// 应用。macOS 会忽略间隔极短的两次激活请求，所以等不到前台时在窗口过半时
// 再请求一次——有界、只一次，不加重试框架。
func (a *shelf) finishPaste(target int, gen int64) {
	start := time.Now()
	deadline := start.Add(pasteFocusWait)
	retried := false
	for {
		if a.pasteSeq.Load() != gen {
			return
		}
		front := false
		dispatchMain(func() { front = pasteFrontmost() == target })
		if front {
			break
		}
		now := time.Now()
		if !retried && now.After(start.Add(pasteFocusWait/2)) {
			retried = true
			dispatchMain(func() {
				if a.pasteSeq.Load() == gen {
					pasteActivate(target)
				}
			})
		}
		if now.After(deadline) {
			a.pasteMissed(gen)
			return
		}
		time.Sleep(pastePollInterval)
	}
	dispatchMain(func() {
		if a.pasteSeq.Load() != gen {
			return
		}
		// 发送前再确认一次：等待期间用户可能已经切去别的应用。
		if pasteFrontmost() != target || !pasteSend(target) {
			a.pasteMissed(gen)
			return
		}
		a.toastStatus(a.appLoc().ItemPasted(), time.Now().Add(2*time.Second), statusOK)
	})
}

// pasteMissed 说明内容在剪贴板上、但这一次没有粘贴。
func (a *shelf) pasteMissed(gen int64) {
	if a.pasteSeq.Load() != gen {
		return
	}
	a.toastStatus(a.appLoc().ClipPasteManual(), time.Now().Add(4*time.Second), statusOK)
}

// cancelPaste 让在途的粘贴尝试作废：面板收起或重新打开时调用。
func (a *shelf) cancelPaste() { a.pasteSeq.Add(1) }
