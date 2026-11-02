//go:build darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// ocrSeams swaps every injectable of screenocr.go and puts them back when
// the test ends: nothing touches tcc, screencapture or a real goroutine.
func ocrSeams(t *testing.T) {
	t.Helper()
	p, g, c, w, d, r, af, inv := ocrPreflight, ocrGrant, ocrCapture, ocrWork, ocrDispatch, ocrRecognize, ocrAfterFunc, ocrInvalidate
	t.Cleanup(func() {
		ocrPreflight, ocrGrant, ocrCapture, ocrWork, ocrDispatch, ocrRecognize, ocrAfterFunc, ocrInvalidate = p, g, c, w, d, r, af, inv
	})
	ocrWork = func(fn func()) { fn() }
	ocrDispatch = func(fn func()) { fn() }
	ocrAfterFunc = func(d time.Duration, fn func()) *time.Timer { return time.NewTimer(time.Hour) }
}

func writePixelPNG(t *testing.T, path string) {
	t.Helper()
	// The recognizer is stubbed in these tests; the file only has to exist
	// and be non-empty for the flow to read it as a captured region.
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// recordClipFunc adapts a func to the clipWriter seam.
type recordClipFunc func(text string) error

func (f recordClipFunc) writeText(text string) error { return f(text) }
func (f recordClipFunc) writeImage([]byte) error     { return nil }

// 无权限 → 说明卡：预检 false 时展开面板、出说明卡，绝不调用截屏或申请。
func TestScreenOCRWithoutPermissionShowsSheet(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return false }
	called := false

	ocrCapture = func(string) error { called = true; return nil }
	a := &shelf{store: nil}
	a.startScreenOCR()
	if !a.ocrSheet {
		t.Fatal("permission card not armed")
	}
	if !a.visible {
		t.Fatal("panel did not open for the permission card")
	}
	if called {
		t.Fatal("capture ran without permission")
	}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	for _, want := range []string{"屏幕录制", "打开系统设置", "取消"} {
		if !view.HasText(want) {
			t.Fatalf("sheet missing %q", want)
		}
	}
}

// 「取消」关掉说明卡；再次触发仍是无权限路径，重新出卡。
func TestScreenOCRSheetCancel(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return false }
	a := &shelf{}
	a.startScreenOCR()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	if err := view.Click("取消"); err != nil {
		t.Fatalf("cancel click: %v", err)
	}
	if a.ocrSheet {
		t.Fatal("cancel left the card up")
	}
}

// 「打开系统设置」走到用户主动申请那一处；授权前卡不消失。
func TestScreenOCRSheetGrantButton(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return false }
	asked := false
	ocrGrant = func() bool { asked = true; return false }
	a := &shelf{}
	a.startScreenOCR()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	if err := view.Click("打开系统设置"); err != nil {
		t.Fatalf("grant click: %v", err)
	}
	if !asked {
		t.Fatal("grant button did not request the permission")
	}
	if !a.ocrSheet {
		t.Fatal("card dismissed before the grant actually landed")
	}
}

// 有权限 → 收起面板（若开着）→ 系统框选 → 识别 → 复制，胶囊文案带字数。
func TestScreenOCRCopiesRecognizedText(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	ocrCapture = func(path string) error {
		writePixelPNG(t, path)
		return nil
	}
	ocrRecognize = func(string) ([]string, error) { return []string{"会议纪要", "第二阶段"}, nil }
	var copied atomic.Value
	a := &shelf{visible: true, tab: pageTray}
	a.write = recordClipFunc(func(text string) error { copied.Store(text); return nil })
	a.startScreenOCR()
	if a.visible {
		t.Fatal("panel stayed open over the region pick")
	}
	if a.ocrScan {
		t.Fatal("round did not finish")
	}
	if got, _ := copied.Load().(string); got != "会议纪要\n第二阶段" {
		t.Fatalf("clipboard got %q", got)
	}
	if a.status != "已复制识别出的文字 · 9 字" {
		t.Fatalf("status = %q", a.status)
	}
	if a.statusKind != statusOK {
		t.Fatalf("status kind = %v", a.statusKind)
	}
}

// Esc 取消（框选成功但没落盘、或框选本身失败）什么都不提示。
func TestScreenOCRCancelStaysSilent(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	for name, capture := range map[string]func(string) error{
		"no file":      func(string) error { return nil },
		"capture fail": func(string) error { return errors.New("denied") },
		"empty file": func(path string) error {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return nil
		},
	} {
		ocrCapture = capture
		a := &shelf{}
		a.startScreenOCR()
		if a.status != "" {
			t.Fatalf("%s: cancel reported %q", name, a.status)
		}
		if a.ocrScan {
			t.Fatalf("%s: round stuck busy", name)
		}
	}
}

// 识别不出文字 → 白点「没有识别到文字」，剪贴板不动。
func TestScreenOCRNoText(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	ocrCapture = func(path string) error { writePixelPNG(t, path); return nil }
	ocrRecognize = func(string) ([]string, error) { return nil, nil }
	a := &shelf{}
	a.startScreenOCR()
	if a.status != "没有识别到文字" || a.statusKind != statusInfo {
		t.Fatalf("status = %q kind %v", a.status, a.statusKind)
	}
}

// 识别出错 → 走 report() 的错误胶囊，剪贴板不动。
func TestScreenOCRRecognizeError(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	ocrCapture = func(path string) error { writePixelPNG(t, path); return nil }
	ocrRecognize = func(string) ([]string, error) { return nil, errors.New("vision busy") }
	a := &shelf{}
	a.startScreenOCR()
	if !strings.Contains(a.status, "vision busy") || a.statusKind != statusErr {
		t.Fatalf("status = %q kind %v", a.status, a.statusKind)
	}
}

// 一轮进行中再触发被忽略；说明卡在场时也不起新一轮。
func TestScreenOCRIgnoresReentrancy(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	var runs atomic.Int32
	ocrWork = func(fn func()) { runs.Add(1) /* worker never runs: round stays busy */ }
	a := &shelf{}
	a.startScreenOCR()
	a.startScreenOCR()
	if runs.Load() != 1 {
		t.Fatalf("second trigger started a second round (%d)", runs.Load())
	}
	// Sheet up → re-trigger ignored even with permission.
	a2 := &shelf{ocrSheet: true}
	a2.startScreenOCR()
	if runs.Load() != 1 {
		t.Fatal("sheet-open trigger started a round")
	}
}

// 等待中（后台索引占着识别锁、识别慢）超过 250ms 才出「正在识别…」，
// 快于它的轮次一句话不出。
func TestScreenOCRProgressDelay(t *testing.T) {
	ocrSeams(t)
	start := time.Now()
	a := &shelf{ocrScan: true, ocrBusyAt: start}
	if text, _, live := a.ocrLiveStatus(start.Add(100 * time.Millisecond)); live || text != "" {
		t.Fatalf("progress showed at 100ms: %q", text)
	}
	text, token, live := a.ocrLiveStatus(start.Add(ocrProgressDelay))
	if !live || text != "正在识别…" || token == nil {
		t.Fatalf("progress missing at 250ms: %q %v %v", text, token, live)
	}
	// Done round: nothing.
	a.ocrScan = false
	if _, _, live := a.ocrLiveStatus(start.Add(2 * ocrProgressDelay)); live {
		t.Fatal("progress outlived the round")
	}
}

// follow() 的进行中胶囊：排队超过 250ms 的框选让刘海长出橙点呼吸行；
// 结果落地后 live 清空，判定胶囊不受影响。
func TestScreenOCRCapsuleFollowsTheRound(t *testing.T) {
	ocrSeams(t)
	start := time.Now()
	a := &shelf{}
	now := start.Add(ocrProgressDelay + time.Millisecond)
	a.fb.follow(a, now) // nothing running: capsule empty
	if a.fb.text != "" {
		t.Fatalf("idle follow said %q", a.fb.text)
	}
	a.ocrScan, a.ocrBusyAt, a.ocrRunID = true, start, 3
	a.fb.follow(a, now)
	if !a.fb.live || a.fb.text != "正在识别…" {
		t.Fatalf("progress not live: %q live=%v", a.fb.text, a.fb.live)
	}
	if a.fb.color() != fbOrange || a.fb.glyph() != "…" {
		t.Fatalf("progress not the breathing orange dot")
	}
	// A toast already up keeps the capsule for its span.
	b := &shelf{ocrScan: true, ocrBusyAt: start, ocrRunID: 3}
	b.fb.show("已复制", statusOK, now.Add(3*time.Second), now)
	b.fb.follow(b, now)
	if b.fb.text != "已复制" || b.fb.live {
		t.Fatalf("toast lost the capsule: %q live=%v", b.fb.text, b.fb.live)
	}
	// Round over → live line clears on the next follow.
	a.ocrScan = false
	a.fb.follow(a, now.Add(time.Second))
	if a.fb.live || a.fb.text != "" {
		t.Fatalf("progress outlived the round: %q", a.fb.text)
	}
}

// 后台索引占着识别锁时框选照样排队：worker 真的并发跑，识别桩在锁内
// 阻塞 → 结果照旧落到剪贴板。
func TestScreenOCRQueuesBehindTheIndex(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	ocrCapture = func(path string) error { writePixelPNG(t, path); return nil }
	entered := make(chan struct{})
	release := make(chan struct{})
	ocrRecognize = func(string) ([]string, error) {
		close(entered)
		<-release
		return []string{"排队也到"}, nil
	}
	ocrWork = func(fn func()) { go fn() }
	ocrDispatch = func(fn func()) { go fn() }
	done := make(chan struct{})
	var copied atomic.Value
	a := &shelf{}
	a.write = recordClipFunc(func(text string) error { copied.Store(text); close(done); return nil })
	a.startScreenOCR()
	<-entered
	select {
	case <-done:
		t.Fatal("recognition result raced ahead of the lock holder")
	case <-time.After(80 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("queued round never finished")
	}
	if got, _ := copied.Load().(string); got != "排队也到" {
		t.Fatalf("clipboard got %q", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for a.ocrScan && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.ocrScan {
		t.Fatal("round stuck busy")
	}
}

// 截屏的临时文件用完即删。
func TestScreenOCRCleansTheShot(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	var seen string
	ocrCapture = func(path string) error {
		seen = path
		writePixelPNG(t, path)
		return nil
	}
	ocrRecognize = func(string) ([]string, error) { return []string{"x"}, nil }
	a := &shelf{write: recordClipFunc(func(string) error { return nil })}
	a.startScreenOCR()
	if seen == "" || !strings.HasPrefix(seen, filepath.Join(os.TempDir(), "shelf-ocr-")) {
		t.Fatalf("shot path = %q", seen)
	}
	if _, err := os.Stat(seen); !os.IsNotExist(err) {
		t.Fatalf("shot file left behind: %v", err)
	}
}

// 框选进行中把图片拖到「框选文字」卡：不开第二轮、不把进行中的
// 标志提前清掉。旧代码里两轮共用 ocrScan，先结束的那轮清旗后另一轮
// 仍在跑——进度胶囊提前消失、下一轮框选也提前放闸。
func TestCopyDroppedTextDuringScanIsRefused(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	var workers, dispatched []func()
	ocrWork = func(fn func()) { workers = append(workers, fn) }
	ocrDispatch = func(fn func()) { dispatched = append(dispatched, fn) }
	ocrCapture = func(path string) error {
		writePixelPNG(t, path)
		return nil
	}
	ocrRecognize = func(string) ([]string, error) { return []string{"字"}, nil }
	a := &shelf{write: recordClipFunc(func(string) error { return nil })}
	drain := func() {
		for _, fn := range dispatched {
			fn()
		}
		dispatched = dispatched[:0]
	}
	a.startScreenOCR() // 第一轮框选：worker 被扣住，ocrScan 挂起
	drain()
	if a.ocrRunID != 1 || len(workers) != 1 || !a.ocrScan {
		t.Fatalf("first round did not arm: runID=%d workers=%d scan=%v", a.ocrRunID, len(workers), a.ocrScan)
	}
	a.copyDroppedText([]string{"/tmp/drop.png"})
	if len(workers) != 1 || a.ocrRunID != 1 {
		t.Fatalf("a dropped image started a second round: workers=%d runID=%d", len(workers), a.ocrRunID)
	}
	if !a.ocrScan {
		t.Fatal("the in-flight round lost its busy flag")
	}
	if a.status != "正在识别，请稍候" {
		t.Fatalf("refused drop gave %q, want the busy hint", a.status)
	}
	// 放行第一轮：旧代码下被拖入起出的第二个 worker 此时仍排队，
	// finishScreenOCR 会把标志清掉而留下一个在跑的轮次。
	workers[0]()
	drain()
	if len(workers) != 1 {
		t.Fatalf("refused drop still queued a worker: %d", len(workers))
	}
	if a.ocrScan {
		t.Fatal("the one round that ran could not finish")
	}
}

// Esc 取消的静默收尾也要重画：进行中胶囊已经画出的那帧，没有 toast
// 就得多留一拍呼吸（约 400ms）。取消分支补一次 Invalidate。
func TestScreenOCRCancelRepaints(t *testing.T) {
	ocrSeams(t)
	paints := 0
	ocrInvalidate = func(*shelf) { paints++ }
	a := &shelf{}
	a.finishScreenOCR(time.NewTimer(time.Hour), "", false, nil) // Esc 取消
	if paints != 1 {
		t.Fatalf("silent finish repainted %d times, want 1", paints)
	}
}
