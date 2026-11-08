//go:build darwin

package main

import (
	"sync"
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

// clipPollInterval is the pasteboard poll. macOS has no change
// notification for the general pasteboard, so the recorder reads
// changeCount on a timer; an idle wake every 100 ms is far cheaper than
// the 30 ms monitoring wake it replaces, and content still lands within
// one poll.
const clipPollInterval = 100 * time.Millisecond

// clipActivation is one app switch seen between two polls. prev is the app
// that was frontmost before it: a copy in that window belongs to prev,
// because the user switched after copying.
type clipActivation struct {
	at         time.Time
	prev, next string
}

// clipActivationHistory bounds what a source decision has to consider. A
// burst of app switches is enough to find the app before the first one;
// older events cannot describe a copy this poll has not seen yet.
const clipActivationHistory = 16

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

	// lastPoll is the previous change observation; activations after it and
	// before a change decide the source app of that change.
	lastPoll time.Time
	front    string
	active   []clipActivation
	// stopObserve ends the activation observer; nil when none was installed
	// (tests build the module without AppKit).
	stopObserve func()
	// background is set when the app's poller goroutine owns changeCount;
	// tests drive tick directly and keep the every-call semantics.
	background bool
	bgMu       sync.Mutex
	bgCount    int64
	bgAt       time.Time
	bgPending  bool
	// tracked is true when the activation observer installed. Without it
	// (headless shelves, tests) the source is read at each copy.
	tracked bool
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
	now := time.Now()
	m := &clipboardModule{
		host: host, board: board, count: board.changeCount(),
		lastPoll: now, bgCount: board.changeCount(),
	}
	if host.win != nil {
		m.tracked = true
		m.front = board.sourceApp()
		m.stopObserve = mac.ObserveActiveApp(func() { m.noteActivation(time.Now()) })
	}
	return m
}

// stop ends the module's system observer. Called once as the app quits.
func (m *clipboardModule) stop() {
	if m.stopObserve != nil {
		m.stopObserve()
		m.stopObserve = nil
	}
}

// noteActivation records one app switch with the app that was frontmost
// before it. Runs on the main thread from NSWorkspace's notification.
func (m *clipboardModule) noteActivation(now time.Time) {
	bundleID, _ := mac.FrontmostApp()
	bundleID = captureSource(mac.FrontmostPID(), mac.OwnPID(), bundleID)
	if bundleID == m.front {
		return
	}
	m.active = append(m.active, clipActivation{at: now, prev: m.front, next: bundleID})
	if len(m.active) > clipActivationHistory {
		m.active = m.active[len(m.active)-clipActivationHistory:]
	}
	m.front = bundleID
}

// sourceFor answers which app owned the copy discovered at now. An
// activation since the previous poll means the user switched after
// copying, so the app before the first such switch owns it; otherwise the
// frontmost app does. The board fallback keeps a module built without the
// observer (tests, early launch) on the old behaviour.
func (m *clipboardModule) sourceFor(now time.Time) string {
	if !m.tracked {
		return m.board.sourceApp()
	}
	for _, ev := range m.active {
		if ev.at.After(m.lastPoll) && !ev.at.After(now) {
			// prev may be "" when Rakko itself was frontmost: the copy
			// then has no source app, exactly like the old direct read.
			return ev.prev
		}
	}
	if m.front != "" {
		return m.front
	}
	return m.board.sourceApp()
}

// advancePoll closes the current observation window: later switches belong
// to the next copy. Events at or before now can no longer describe one.
func (m *clipboardModule) advancePoll(now time.Time) {
	m.lastPoll = now
	kept := m.active[:0]
	for _, ev := range m.active {
		if ev.at.After(now) {
			kept = append(kept, ev)
		}
	}
	m.active = kept
}

// nextTick is a pending change's retry deadline. With the background poller
// there is no main-thread poll clock at all.
func (m *clipboardModule) nextTick(now time.Time) time.Time {
	if p := m.pending; p != nil {
		return p.seen.Add(clipPollInterval)
	}
	return time.Time{}
}

// startBackground starts the 100 ms changeCount poller. Only the general
// pasteboard's change counter is read off the main thread; types, text,
// images and source attribution are still handled on the main thread.
func (m *clipboardModule) startBackground() {
	if m.background {
		return
	}
	m.background = true
	board, stop := m.board, m.host.stop
	go func() {
		t := time.NewTicker(clipPollInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				count := board.changeCount()
				m.bgMu.Lock()
				changed := count != m.bgCount
				if changed {
					m.bgCount, m.bgAt, m.bgPending = count, now, true
				}
				m.bgMu.Unlock()
				if changed {
					m.host.wakeMonitor()
				}
			}
		}
	}()
}

// takeBackground hands the latest background observation to the main
// thread, if one is waiting.
func (m *clipboardModule) takeBackground() (int64, time.Time, bool) {
	m.bgMu.Lock()
	defer m.bgMu.Unlock()
	if !m.bgPending {
		return 0, time.Time{}, false
	}
	m.bgPending = false
	return m.bgCount, m.bgAt, true
}

// tick reads the pasteboard on the 100 ms clock. The changeCount read is
// cheap, and the activation history attributes a copy to the app that was
// frontmost when it happened even if the user switched immediately after.
//
// A tick can land in the gap between the changeCount bump and the
// pasteboard publishing its types (see clipPublishWait). Such a change is
// kept pending and retried on later ticks instead of being marked handled
// as an empty copy, so a screenshot whose bytes arrive two ticks after the
// bump is still recorded exactly once. A newer change supersedes a pending
// one and is attributed to the app frontmost at its own bump.
func (m *clipboardModule) tick(now time.Time) {
	var count int64
	var changeAt time.Time
	if m.background {
		var ok bool
		count, changeAt, ok = m.takeBackground()
		if !ok {
			if p := m.pending; p != nil && now.Sub(p.seen) < clipPublishWait {
				m.record(now, p.source)
			} else if p != nil {
				m.pending = nil
			}
			return
		}
	} else {
		count, changeAt = m.board.changeCount(), now
	}
	source := m.sourceFor(changeAt)
	m.advancePoll(changeAt)
	if count != m.count {
		m.pending = nil
		m.count = count
		a := m.host
		if a.store == nil {
			return
		}
		// 记录复制发生的瞬间哪个应用在最前。ShelfU 自己处于前台（搜索面板
		// 拿了键盘）时不记来源；被用户排除的应用整条跳过。
		m.record(now, source)
		return
	}
	if p := m.pending; p != nil {
		if now.Sub(p.seen) >= clipPublishWait {
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
