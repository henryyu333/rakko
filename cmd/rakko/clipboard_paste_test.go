//go:build darwin

package main

// 剪贴板一键粘贴（Issue #4）的行为：⌘1..⌘9 按当前显示顺序粘贴、↑↓⏎ 走同一条
// 流程、目标应用必须是打开面板前的那个、权限与失败时只说复制，快速连续操作
// 取消旧尝试。全部通过 headless 的 ui.Tester 与替换过的平台接缝，不碰真实
// 剪贴板、真实按键和真实应用。

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/store"
)

// pasteRecorder records what the flow would send and where.
type pasteRecorder struct {
	sent []int
}

// pasteHooks replaces every platform seam a paste touches and restores them
// after the test. work queues the background wait instead of running it, so a
// test decides when (and whether) it completes.
type pasteHooks struct {
	pastes    *pasteRecorder
	work      []func()
	front     int
	trusted   bool
	activated []int
}

func newPasteHooks(t *testing.T) *pasteHooks {
	t.Helper()
	h := &pasteHooks{pastes: &pasteRecorder{}, trusted: true}
	origTrusted, origFront, origSend := pasteTrusted, pasteFrontmost, pasteSend
	origActivate := pasteActivate
	origWork, origMain := goWork, dispatchMain
	origWait, origPoll := pasteFocusWait, pastePollInterval
	pasteTrusted = func() bool { return h.trusted }
	pasteFrontmost = func() int { return h.front }
	pasteActivate = func(pid int) bool {
		h.activated = append(h.activated, pid)
		return true
	}
	pasteSend = func(pid int) bool {
		h.pastes.sent = append(h.pastes.sent, pid)
		return true
	}
	goWork = func(fn func()) { h.work = append(h.work, fn) }
	dispatchMain = func(fn func()) { fn() }
	pasteFocusWait, pastePollInterval = time.Millisecond, time.Millisecond
	t.Cleanup(func() {
		pasteTrusted, pasteFrontmost, pasteSend = origTrusted, origFront, origSend
		pasteActivate = origActivate
		goWork, dispatchMain = origWork, origMain
		pasteFocusWait, pastePollInterval = origWait, origPoll
	})
	return h
}

// runWork runs the queued background waits in order: the focus check and the
// paste itself run inline here.
func (h *pasteHooks) runWork() {
	for len(h.work) > 0 {
		fn := h.work[0]
		h.work = h.work[1:]
		fn()
	}
}

// pasteShelf is clipShelf with a remembered source app and the shortcuts'
// page selected.
func pasteShelf(t *testing.T, texts ...string) (*shelf, *ui.Tester, *recordClip) {
	t.Helper()
	a, view, rec := clipShelf(t, texts...)
	a.setTab(pageClipboard)
	a.focusMode = true
	a.prevAppPID = 4242
	if items := a.visibleItems(); len(items) > 0 {
		a.selection.only(items[0].ID) // ⏎ 的对象；⌘N 不看选中项
	}
	return a, view, rec
}

// ⌘3 粘贴筛选后显示顺序的第三项，不是原始顺序或选中项。
func TestPasteShortcutUsesFilteredOrder(t *testing.T) {
	h := newPasteHooks(t)
	h.front = 4242
	a, _, rec := pasteShelf(t, "match one", "match two", "match three", "unrelated")
	a.search = "match"
	items := a.visibleItems()
	if len(items) != 3 {
		t.Fatalf("the filter shows %d items, want 3", len(items))
	}
	want := items[2].Text

	a.selection.only(items[0].ID) // 选中第一项：⌘3 必须忽略它
	a.pasteClipIndex(3)
	if rec.text != want {
		t.Fatalf("wrote %q, want the third displayed item %q", rec.text, want)
	}
	if got := items[2].ID; !a.selection.has(got) {
		t.Fatal("the pasted row is not the selected one")
	}
	h.runWork()
	if len(h.pastes.sent) != 1 || h.pastes.sent[0] != 4242 {
		t.Fatalf("pasted to %v, want the remembered app 4242", h.pastes.sent)
	}
	if a.visible {
		t.Fatal("the panel stayed open")
	}
}

// 越界编号什么都不做：不写剪贴板、不收起、不发键。
func TestPasteShortcutOutOfRangeDoesNothing(t *testing.T) {
	h := newPasteHooks(t)
	h.front = 4242
	a, _, rec := pasteShelf(t, "only one", "only two")
	a.pasteClipIndex(5)
	if rec.text != "" || rec.png != nil {
		t.Fatal("an out-of-range shortcut wrote the clipboard")
	}
	if !a.visible {
		t.Fatal("an out-of-range shortcut collapsed the panel")
	}
	h.runWork()
	if len(h.pastes.sent) != 0 {
		t.Fatal("an out-of-range shortcut sent a paste")
	}
	a.pasteClipIndex(0)
	if rec.text != "" {
		t.Fatal("⌘0 wrote the clipboard")
	}
}

// 空结果时 ⌘1 也不做任何事。
func TestPasteShortcutEmptyListDoesNothing(t *testing.T) {
	h := newPasteHooks(t)
	a, _, rec := pasteShelf(t)
	a.pasteClipIndex(1)
	if rec.text != "" || !a.visible || len(h.work) != 0 {
		t.Fatalf("empty list: text=%q visible=%v work=%d", rec.text, a.visible, len(h.work))
	}
}

// 快捷键只在剪贴板页生效：托盘页按 ⌘1 什么都不做。
func TestPasteShortcutIsLocalToTheClipboardPage(t *testing.T) {
	newPasteHooks(t)
	a, view, rec := clipShelf(t, "clip text")
	a.setTab(pageTray)
	view.Frame()
	view.Key(ui.Cmd, ui.Key1)
	view.Frame()
	if rec.text != "" {
		t.Fatalf("⌘1 on the tray page wrote %q", rec.text)
	}
	if !a.visible {
		t.Fatal("⌘1 on the tray page collapsed the panel")
	}
}

// 面板收起后 ⌘1 不再属于它。
func TestPasteShortcutNeedsVisiblePanel(t *testing.T) {
	newPasteHooks(t)
	a, view, rec := clipShelf(t, "clip text")
	a.setTab(pageClipboard)
	a.visible = false
	view.Frame()
	view.Key(ui.Cmd, ui.Key1)
	view.Frame()
	if rec.text != "" {
		t.Fatalf("⌘1 with the panel closed wrote %q", rec.text)
	}
}

// ⏎ 走同一条流程：粘贴选中项并回到原应用。
func TestEnterPastesSelection(t *testing.T) {
	h := newPasteHooks(t)
	h.front = 4242
	a, _, rec := pasteShelf(t, "first", "second")
	a.selection.only(a.visibleItems()[1].ID)
	a.pasteClipSelection()
	if rec.text != "second" {
		t.Fatalf("wrote %q, want the selected item", rec.text)
	}
	h.runWork()
	if len(h.pastes.sent) != 1 || h.pastes.sent[0] != 4242 {
		t.Fatalf("pasted to %v, want 4242", h.pastes.sent)
	}
}

// 多选文本按行合并后粘贴，与多选复制的语义一致。
func TestEnterPastesSeveralTextsJoined(t *testing.T) {
	h := newPasteHooks(t)
	h.front = 4242
	a, _, rec := pasteShelf(t, "one", "two", "three")
	items := a.visibleItems()
	a.selection.only(items[0].ID)
	a.selection.click(items, items[1].ID, ui.Shift)
	a.pasteClipSelection()
	if rec.text != items[0].Text+"\n"+items[1].Text {
		t.Fatalf("wrote %q, want the two selected texts one per line", rec.text)
	}
	h.runWork()
	if len(h.pastes.sent) != 1 {
		t.Fatal("the joined texts were not pasted")
	}
}

// 文本与图片混选不能一起粘贴：报错、不写剪贴板、不收起、不发键。
func TestPasteMixedSelectionRefused(t *testing.T) {
	h := newPasteHooks(t)
	a, _, rec := pasteShelf(t, "text item")
	a.store.AddClipImage(testPNGBytes(t), "com.apple.screenshot")
	list := a.visibleItems()
	a.selection.only(list[0].ID)
	a.selection.click(list, list[1].ID, ui.Shift)
	a.pasteClipSelection()
	if rec.text != "" || rec.png != nil {
		t.Fatal("a mixed selection wrote the clipboard")
	}
	if !a.visible {
		t.Fatal("a mixed selection collapsed the panel")
	}
	if a.status == "" {
		t.Fatal("a mixed selection reported nothing")
	}
	h.runWork()
	if len(h.pastes.sent) != 0 {
		t.Fatal("a mixed selection sent a paste")
	}
}

// 没有辅助功能权限：只复制，明确提示，绝不把复制说成粘贴，也不申请权限。
func TestPasteWithoutAccessibilityCopies(t *testing.T) {
	h := newPasteHooks(t)
	h.trusted = false
	h.front = 4242
	a, _, rec := pasteShelf(t, "needs permission")
	a.pasteClipSelection()
	if rec.text != "needs permission" {
		t.Fatalf("wrote %q, want the selected text", rec.text)
	}
	h.runWork()
	if len(h.pastes.sent) != 0 {
		t.Fatal("pasted without Accessibility permission")
	}
	if !containsAll(a.status, "已复制", "辅助功能") {
		t.Fatalf("status %q does not say copied and why, with no paste", a.status)
	}
	if a.visible {
		t.Fatal("the panel stayed open")
	}
}

// 用户切去第三个应用：不补激活、不按键，提示内容已复制。焦点不能被抢回原目标。
func TestPasteMustNotReactivateAfterUserSwitch(t *testing.T) {
	h := newPasteHooks(t)
	h.front = 777 // 用户已经切到的第三个应用
	a, _, rec := pasteShelf(t, "must stay copied")
	a.pasteClipSelection()
	h.runWork()
	if len(h.activated) != 0 {
		t.Fatalf("user switched to PID 777, but pending paste requested activation of %v", h.activated)
	}
	if len(h.pastes.sent) != 0 {
		t.Fatalf("paste was sent after user switched away: %v", h.pastes.sent)
	}
	if rec.text != "must stay copied" {
		t.Fatal("the content was not left on the clipboard")
	}
	if !containsAll(a.status, "已复制", "⌘V") {
		t.Fatalf("status %q does not report a copy without a paste", a.status)
	}
}

// 目标应用迟迟不接回前台但本进程仍握着键盘：补激活一次之后放弃，仍然不发键。
func TestPasteGivesUpWhenFocusNeverArrives(t *testing.T) {
	h := newPasteHooks(t)
	pasteFocusWait, pastePollInterval = 80*time.Millisecond, 5*time.Millisecond
	a, _, rec := pasteShelf(t, "never front")
	pasteFrontmost = func() int { return mac.OwnPID() }
	a.pasteClipSelection()
	h.runWork()
	if len(h.pastes.sent) != 0 {
		t.Fatalf("pasted although the target never came front: %v", h.pastes.sent)
	}
	if len(h.activated) != 1 || h.activated[0] != 4242 {
		t.Fatalf("activation retries %v, want exactly one at 4242", h.activated)
	}
	if rec.text != "never front" {
		t.Fatal("the content was not left on the clipboard")
	}
	if !containsAll(a.status, "已复制", "⌘V") {
		t.Fatalf("status %q does not report a copy without a paste", a.status)
	}
}

// 快速收起再打开：重新打开的那一次让上一次还在等焦点的粘贴作废，按键不会
// 落到重新打开之后的那个目标上。
func TestReopenCancelsPendingPaste(t *testing.T) {
	h := newPasteHooks(t)
	h.front = 4242
	a, _, _ := pasteShelf(t, "older", "newer")
	a.pasteClipIndex(1)
	queued := h.work
	if len(queued) != 1 {
		t.Fatalf("the first attempt queued %d waits, want 1", len(queued))
	}
	a.cancelPaste() // 重新打开所做的事
	for _, fn := range queued {
		fn()
	}
	if len(h.pastes.sent) != 0 {
		t.Fatalf("the superseded attempt pasted to %v", h.pastes.sent)
	}
	// 重新打开后的一次选择照常粘贴一次。
	a.visible = true
	a.prevAppPID = 4242
	a.pasteClipIndex(2)
	h.runWork()
	if len(h.pastes.sent) != 1 || h.pastes.sent[0] != 4242 {
		t.Fatalf("pasted %v, want exactly the newer attempt", h.pastes.sent)
	}
}

// 面板在途时被收起（用户先重开再关掉）：等待中的尝试作废。
func TestHideCancelsPaste(t *testing.T) {
	h := newPasteHooks(t)
	h.front = 4242
	a, _, _ := pasteShelf(t, "cancel me")
	a.pasteClipIndex(1)
	queued := h.work
	a.visible = true // 重新打开后的面板
	a.hide(hideExplicit)
	for _, fn := range queued {
		fn()
	}
	h.runWork()
	if len(h.pastes.sent) != 0 {
		t.Fatalf("a closed panel pasted to %v", h.pastes.sent)
	}
}

// 编号提示跟着显示顺序，只有前 9 行带编号。
func TestClipRowsShowShortcutNumbers(t *testing.T) {
	newPasteHooks(t)
	texts := make([]string, 0, 11)
	for i := 0; i < 11; i++ {
		texts = append(texts, "row "+string(rune('a'+i)))
	}
	a, view, _ := clipShelf(t, texts...)
	a.setTab(pageClipboard)
	view.Frame()
	for _, want := range []string{"⌘1", "⌘9"} {
		if !view.HasText(want) {
			t.Fatalf("the clipboard page does not show %q", want)
		}
	}
	if view.HasText("⌘10") {
		t.Fatal("a tenth row claims a shortcut")
	}
}

// 纯函数：选择内容到可粘贴内容的映射。
func TestPasteContent(t *testing.T) {
	if _, _, ok := pasteContent(nil); ok {
		t.Fatal("an empty selection is pasteable")
	}
	texts := []store.Item{{Kind: store.KindText, Text: "a"}, {Kind: store.KindText, Text: "b"}}
	joined, many, ok := pasteContent(texts)
	if !ok || !many || joined != "a\nb" {
		t.Fatalf("two texts → %q many=%v ok=%v", joined, many, ok)
	}
	mixed := []store.Item{{Kind: store.KindText, Text: "a"}, {Kind: store.KindImage}}
	if _, _, ok := pasteContent(mixed); ok {
		t.Fatal("a text and an image are pasteable together")
	}
	if _, many, ok := pasteContent(mixed[:1]); !ok || many {
		t.Fatal("one item must pass through untouched")
	}
}

// 目标迟迟没有接回前台、但键盘仍在本进程手里（合法的交接）：补请求一次激活，
// 目标到前台后才粘贴；只补一次。
func TestPasteRetriesActivationOnce(t *testing.T) {
	h := newPasteHooks(t)
	pasteFocusWait, pastePollInterval = 300*time.Millisecond, 5*time.Millisecond
	a, _, _ := pasteShelf(t, "slow target")
	// 补发的激活之前，键盘仍在我们手里；激活之后目标才回到前台。
	pasteFrontmost = func() int {
		if len(h.activated) > 0 {
			return 4242
		}
		return mac.OwnPID()
	}
	a.pasteClipSelection()
	h.runWork()
	if len(h.activated) != 1 || h.activated[0] != 4242 {
		t.Fatalf("activation retried %v, want exactly once at 4242", h.activated)
	}
	if len(h.pastes.sent) != 1 || h.pastes.sent[0] != 4242 {
		t.Fatalf("pasted %v, want the target once it came front", h.pastes.sent)
	}
}

// 超时反馈必须回到主线程：后台等待结束时 UI 状态一个字节都不变，只有主线程
// 回调真正跑过之后才出现「已复制 · 按 ⌘V 粘贴」。
func TestPasteTimeoutReportsOnMainThread(t *testing.T) {
	h := newPasteHooks(t)
	pasteFocusWait, pastePollInterval = 40*time.Millisecond, 5*time.Millisecond
	a, _, _ := pasteShelf(t, "timeout item")
	pasteFrontmost = func() int { return mac.OwnPID() } // 交接仍属于我们，但目标一直不来
	// 主线程回调先排队，不就地执行：这样能证明后台路径没有直接改 UI。
	var queued []func()
	dispatchMain = func(fn func()) { queued = append(queued, fn) }

	a.pasteClipSelection()
	copiedStatus := a.status // copyText 在主线程写的「已复制」
	h.runWork()              // 后台等待跑完（包括超时）
	if a.status != copiedStatus {
		t.Fatalf("the background wait changed the status to %q before the main thread ran", a.status)
	}
	if len(queued) == 0 {
		t.Fatal("the timeout path never asked the main thread to report")
	}
	for len(queued) > 0 {
		fn := queued[0]
		queued = queued[1:]
		fn()
	}
	if !containsAll(a.status, "已复制", "⌘V") {
		t.Fatalf("status %q does not report a copy without a paste", a.status)
	}
	if len(h.pastes.sent) != 0 {
		t.Fatalf("pasted %v without the target in front", h.pastes.sent)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
