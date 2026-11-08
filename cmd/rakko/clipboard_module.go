//go:build darwin

package main

import (
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
)

// The recorder reads the system pasteboard in production; tests can supply
// copy events without writing non-private fixtures to the user's pasteboard.
type clipReader interface {
	changeCount() int64
	sourceApp() string
	types() []string
	text() string
	image() []byte
}

type systemClipReader struct{}

func (systemClipReader) changeCount() int64 { return mac.ClipboardChangeCount() }
func (systemClipReader) types() []string    { return mac.ClipboardTypes() }
func (systemClipReader) text() string       { return mygo.Clipboard.ReadText() }
func (systemClipReader) image() []byte      { return mygo.Clipboard.ReadImage() }
func (systemClipReader) sourceApp() string {
	bundleID, _ := mac.FrontmostApp()
	return captureSource(mac.FrontmostPID(), mac.OwnPID(), bundleID)
}

// clipPublishWait bounds how long a change may stay unreadable before the
// recorder gives up on it. macOS bumps the changeCount before it publishes
// the representations: measured with a 1 ms read-only probe, the clipboard
// showed no declared types at the bump and gained them 20-54 ms later, with
// the PNG bytes of a screenshot arriving still later and without another
// bump. The gap is far below this bound, so a normal copy costs a couple of
// retries while a change that never fills in is dropped within ~1 s.
const clipPublishWait = time.Second

// clipboardModule records copied text and images throughout the app session.
type clipboardModule struct {
	host  *shelf
	board clipReader
	count int64 // pasteboard changeCount seen; pending may still be unreadable

	// pending is the change at count whose content the pasteboard had not
	// published yet. It keeps the source app seen at the change itself and
	// the first sighting, so later ticks retry the same change instead of
	// attributing it to whatever app is frontmost by then.
	pending *clipPending
}

// clipPending is one counted but not yet readable change.
type clipPending struct {
	source string
	seen   time.Time
}

// newClipboardModule starts from the current clipboard, so whatever was
// copied before launch is not recorded.
func newClipboardModule(host *shelf) *clipboardModule {
	board := systemClipReader{}
	return &clipboardModule{host: host, board: board, count: board.changeCount()}
}

// tick runs on every monitoring poll (~30 ms): the changeCount read is
// cheap and keeps latency low, which also lets the frontmost app be
// attributed to the app that copied, not whatever is frontmost when a
// slower poll happens to run.
//
// A tick can land in the gap between the changeCount bump and the
// pasteboard publishing its types (see clipPublishWait). Such a change is
// kept pending and retried on later ticks instead of being marked handled
// as an empty copy, so a screenshot whose bytes arrive two ticks after the
// bump is still recorded exactly once. A newer change supersedes a pending
// one and is attributed to the app frontmost at its own bump.
func (m *clipboardModule) tick(now time.Time) {
	count := m.board.changeCount()
	if count != m.count {
		m.pending = nil
		m.count = count
		a := m.host
		if a.store == nil {
			return
		}
		// 记录复制发生的瞬间哪个应用在最前。ShelfU 自己处于前台（搜索面板
		// 拿了键盘）时不记来源；被用户排除的应用整条跳过。
		m.record(now, m.board.sourceApp())
		return
	}
	if p := m.pending; p != nil {
		if now.Sub(p.seen) >= clipPublishWait {
			// The pasteboard never published anything usable for this change:
			// drop it for good so later ticks cost nothing.
			m.pending = nil
			return
		}
		m.record(now, p.source)
	}
}

// record handles one clipboard change: it adds the copy once the pasteboard
// has published it, and otherwise keeps the change pending (with the source
// app captured at the change itself) until clipPublishWait runs out.
func (m *clipboardModule) record(now time.Time, source string) {
	a := m.host
	types := m.board.types()
	if len(types) == 0 {
		// No declared type yet: the content is still on its way, and reading
		// it — above all the image bytes — would only be wasted work.
		m.await(now, source)
		return
	}
	// 每次变化只收一条：按来源声明的首选表示取文字或图片，避免双条目。
	switch clipCapture(types, source, a.settings.ExcludedApps) {
	case clipText:
		text := m.board.text()
		if text == "" {
			m.await(now, source)
			return
		}
		m.pending = nil
		a.changed(a.store.AddClipText(text, source))
		a.warmClipApp(source)
	case clipImage:
		png := m.board.image()
		if len(png) == 0 {
			m.await(now, source)
			return
		}
		m.pending = nil
		a.changed(a.store.AddClipImage(png, source))
		if o := a.ocr(); o != nil {
			o.wake()
		}
		a.warmClipApp(source)
	default:
		// Private or concealed markers, Finder files, unknown types, or an
		// excluded app: nothing will ever be recorded, so handle it now.
		m.pending = nil
	}
}

// await keeps the change pending for the next tick; only the first sighting
// fixes the deadline, so retries cannot extend the wait.
func (m *clipboardModule) await(now time.Time, source string) {
	if m.pending == nil {
		m.pending = &clipPending{source: source, seen: now}
	}
}

// warmClipApp resolves the new row's app name and icon now, on the tick
// that recorded the copy, instead of on the frame that first draws the
// clipboard page: the name lookup is a several-millisecond workspace call
// (measured ~8ms cold) and would otherwise land inside a page switch.
func (a *shelf) warmClipApp(bundle string) {
	if bundle == "" || bundle == clipSourceOCR || a.store == nil {
		return
	}
	a.appName(bundle)
	a.clipAppIcon(bundle)
}

// skipCurrent marks the clipboard's current contents as handled; used after
// ShelfU itself writes to the clipboard so the copy is not recorded again.
// A change still pending from before the write is dropped with it.
// Safe on a nil module in shelves that have not started monitoring yet.
func (m *clipboardModule) skipCurrent() {
	if m != nil {
		m.count = m.board.changeCount()
		m.pending = nil
	}
}
