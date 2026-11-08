//go:build darwin

package main

import (
	"errors"
	"log"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/store"
)

// ocrIndex recognizes text in clipboard images in the background: one at
// a time, newest first, with a pause between images so the Vision model
// never competes with the panel. The result lands on the item's OCRText
// (persisted, so it runs once per image ever) and the clipboard search
// can match it. Items added before this feature existed are picked up on
// the first tick after launch; items deleted mid-recognition drop the
// result (SetOCR reports the missing id). Everything is local — the
// recognizer is the same mac.RecognizeText the converter uses.
type ocrIndex struct {
	host *shelf
	// inflight marks item IDs a worker is recognizing now (the main
	// thread owns the map; workers only report back through dispatch).
	inflight map[string]bool
	// pending holds recognizeNow calls that arrived while a worker was
	// busy; they run before the next background item (FIFO, the menu
	// path shares the same one-worker lock).
	pending []ocrRequest
	// failed holds ids recognition or its write-back already failed for
	// this session, so a broken image is not retried every tick; the next
	// launch tries again.
	failed map[string]bool
	// nextAt gates the pace: one image per interval, never back to back.
	nextAt   time.Time
	interval time.Duration
	// idle is set after a full scan found nothing left to do. New clipboard
	// images clear it, so the idle tick no longer walks the whole history.
	// Zero value is "scan once" (captures from older launches).
	idle bool
	// Seams: tests substitute a fake recognizer, a synchronous dispatch
	// and an inline worker launch; nil falls back to the real ones.
	recognize func(path string) ([]string, error)
	dispatch  func(func())
	run       func(func())
	now       func() time.Time
}

// ocrRequest is one queued 「识别文字并复制」: the item as it stood when
// asked, and the callback the answer goes to on the main thread.
type ocrRequest struct {
	item store.Item
	cb   func(text string, err error)
}

// ocrGap is the pause between background recognitions.
const ocrGap = 2 * time.Second

// ocr returns the shelf's background recognizer, nil while it is not
// registered (unit tests that never go through ready()).
func (a *shelf) ocr() *ocrIndex {
	for _, m := range a.modules {
		if o, ok := m.(*ocrIndex); ok {
			return o
		}
	}
	return nil
}

func (o *ocrIndex) nowTime() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

// tick picks the newest clipboard image still waiting for recognition —
// needs() is the whole rule, so captures from before this feature existed
// are filled in the same pass — and starts one worker for it. It runs on
// the main thread from the monitoring loop like every module.
func (o *ocrIndex) tick(time.Time) {
	a := o.host
	if a.quitting || a.store == nil || o.idle {
		return
	}
	if len(o.pending) > 0 && len(o.inflight) == 0 {
		r := o.pending[0]
		o.pending = o.pending[1:]
		// The item may have been deleted while queued: the request is
		// cancelled, and the callback must still run — the user was told
		// 「完成后自动复制」 and needs a definite outcome.
		if idx := slices.IndexFunc(a.store.Items, func(x store.Item) bool { return x.ID == r.item.ID }); idx >= 0 {
			o.start(a.store.Items[idx], r.cb)
		} else if r.cb != nil {
			r.cb("", errItemGone)
		}
		return
	}
	// One image at a time: a running worker owns the recognizer until it
	// reports back, so a fast 30ms tick can never stack a second one on
	// top of it — Vision's model load alone would multiply memory use.
	if len(o.inflight) > 0 || o.nowTime().Before(o.nextAt) {
		return
	}
	for _, it := range a.store.Items {
		if o.needs(it) {
			o.start(it, nil)
			return
		}
	}
	o.idle = true
}

// wake asks the background pass to scan again: a new clipboard image may be
// waiting, and the idle scan would otherwise skip the history entirely.
func (o *ocrIndex) wake() {
	o.idle = false
}

// needs reports whether the background pass should recognize the item:
// clipboard-captured images only (tray files are recognized on demand
// from the menu), not yet done, not in flight, not failed this session,
// with its managed file still there. A capture hidden from the clipboard
// page (OffClip) is skipped: its text is only ever read there, and
// re-copying the image clears the flag, so the pass picks it up then.
func (o *ocrIndex) needs(it store.Item) bool {
	return it.Kind == store.KindImage && it.Source == store.SourceClipboard &&
		!it.OffClip && !it.OCRDone && !it.Missing && it.Path != "" &&
		!o.busy(it.ID) && !o.failed[it.ID]
}

// busy reports whether the item is being recognized or is queued for it.
func (o *ocrIndex) busy(id string) bool { return o.inflight[id] || o.queued(id) }

// queued reports whether the item waits in the menu-request queue.
func (o *ocrIndex) queued(id string) bool {
	for _, r := range o.pending {
		if r.item.ID == id {
			return true
		}
	}
	return false
}

// anyBusy reports whether a recognition worker is running at all — the
// one-worker lock the background pass and the menu path share.
func (o *ocrIndex) anyBusy() bool { return len(o.inflight) > 0 }

// abort releases an id whose worker stopped before reporting (the app is
// quitting): the id is cleared so nothing looks stuck, and no nextAt is
// set since nothing ran.
func (o *ocrIndex) abort(id string) { delete(o.inflight, id) }

// start launches the one-at-a-time worker for item. cb (nil for the
// background pass) runs on the main thread after the write-back attempt.
// errItemGone is the cancellation a deleted image produces: the item left
// the store while its recognition was queued or in flight, so nothing is
// copied and nothing is written back.
var errItemGone = errors.New("store: item deleted before recognition finished")

func (o *ocrIndex) start(it store.Item, cb func(string, error)) {
	if o.inflight == nil {
		o.inflight = map[string]bool{}
	}
	o.inflight[it.ID] = true
	recognize := o.recognize
	if recognize == nil {
		recognize = recognizeImageText
	}
	dispatch := o.dispatch
	if dispatch == nil {
		dispatch = mygo.RunOnMain
	}
	// run is the worker launch; tests run it inline so the whole round
	// trip — recognize, write-back, callback — stays synchronous.
	run := o.run
	if run == nil {
		run = func(f func()) { go f() }
	}
	host := o.host
	id, path := it.ID, it.Path
	run(func() {
		// Model loading makes the first call take seconds; it must never
		// run on the main thread — hence the worker.
		if host.stop != nil {
			select {
			case <-host.stop:
				dispatch(func() { o.abort(id) })
				return
			default:
			}
		}
		lines, err := recognize(path)
		dispatch(func() {
			gone, werr := o.finish(id, lines, err)
			if cb != nil {
				switch {
				case gone:
					cb("", errItemGone)
				case werr != nil:
					cb("", werr)
				case err != nil:
					cb("", err)
				default:
					cb(joinOCRLines(lines), nil)
				}
			}
		})
	})
}

// finish writes the worker's result onto the item (main thread) and
// reports whether the item vanished mid-recognition. Recognition errors on
// an item that still exists are remembered for the session; a deleted item
// is a cancellation — its content id must not be marked failed, or a later
// capture of the same bytes would be skipped until restart.
func (o *ocrIndex) finish(id string, lines []string, err error) (gone bool, werr error) {
	delete(o.inflight, id)
	o.nextAt = o.nowTime().Add(o.gap())
	a := o.host
	exists := a.store != nil && slices.ContainsFunc(a.store.Items,
		func(it store.Item) bool { return it.ID == id })
	if err != nil {
		log.Printf("ocr: %v", err)
		if exists {
			o.markFailed(id)
		}
		return !exists, nil
	}
	if a.quitting || a.store == nil {
		return !exists, nil
	}
	if err := a.store.SetOCR(id, joinOCRLines(lines)); err != nil {
		if strings.Contains(err.Error(), "no item") {
			// Deleted mid-recognition: the intended drop — a cancellation,
			// not a failure, so the id is not marked failed.
			return true, nil
		}
		// A real save failure on a live item: don't retry every tick, and
		// the callback hears the error instead of copying blindly.
		o.markFailed(id)
		a.toastQuiet(a.appLoc().OcrFailed(err.Error()), time.Time{}, statusErr)
		return !exists, err
	}
	if a.win != nil {
		a.win.Invalidate() // a hit row's snippet appears without a repaint wait
	}
	return false, nil
}

func (o *ocrIndex) gap() time.Duration {
	if o.interval < 0 {
		return 0 // tests: no pacing
	}
	if o.interval > 0 {
		return o.interval
	}
	return ocrGap
}

func (o *ocrIndex) markFailed(id string) {
	if o.failed == nil {
		o.failed = map[string]bool{}
	}
	o.failed[id] = true
}

// recognizeNow is the menu path: a stored result answers at once (done
// includes "no text found"), the same item already running reports busy,
// any other running worker puts the request at the front of the queue
// (its cb fires when that item's turn comes), and an idle recognizer
// starts it now. cb is invoked on the main thread either way.
func (o *ocrIndex) recognizeNow(it store.Item, cb func(text string, err error)) bool {
	if it.OCRDone {
		cb(it.OCRText, nil)
		return true
	}
	if o.busy(it.ID) || it.Missing || it.Path == "" {
		return false
	}
	if o.anyBusy() {
		o.pending = append(o.pending, ocrRequest{item: it, cb: cb})
		return true
	}
	o.start(it, cb)
	return true
}

// joinOCRLines stores recognized lines as one text with normalized CJK
// compatibility forms (the same normalization the Word converter applies).
func joinOCRLines(lines []string) string {
	return strings.Join(normalizeLines(lines), "\n")
}

// copyRecognizedText is 「识别文字并复制」: the recognized text of one
// image goes on the clipboard (copyText, so ShelfU never records its own
// copy). A recognized item answers immediately; while the recognizer is
// busy elsewhere the request is queued and the copy lands when it
// finishes; a fresh one starts right away.
func (a *shelf) copyRecognizedText(item store.Item) {
	o := a.ocr()
	if o == nil {
		// No registered module (a shelf built outside ready()): serve the
		// copy through a throwaway recognizer instead of failing.
		o = &ocrIndex{host: a}
	}
	if item.Missing || item.Path == "" {
		a.toastStatus(a.appLoc().OcrImageUnavailable(), time.Now().Add(3*time.Second), statusErr)
		return
	}
	queued := !item.OCRDone && o.anyBusy() && !o.busy(item.ID)
	if !o.recognizeNow(item, func(text string, err error) {
		switch {
		case errors.Is(err, errItemGone):
			a.toastStatus(a.appLoc().OcrImageGone(), time.Now().Add(3*time.Second), statusInfo)
		case err != nil:
			a.report(err)
		case text == "":
			a.toastStatus(a.appLoc().OcrNoText(), time.Now().Add(3*time.Second), statusInfo)
		default:
			a.toastOCRCopy(text, a.copyOCRText(text))
		}
	}) {
		a.toastProgress(a.appLoc().OcrBusy(), time.Now().Add(3*time.Second))
		return
	}
	if queued {
		a.toastProgress(a.appLoc().OcrQueued(), time.Now().Add(3*time.Second))
	}
}

// ocrBusy backs the menu's disabled state: while the item is being
// recognized or queued the entry stays grey, so it cannot be triggered
// twice; a missing image file has nothing to recognize.
func (a *shelf) ocrBusy(item store.Item) bool {
	if item.Missing || item.Path == "" {
		return true
	}
	o := a.ocr()
	return o != nil && o.busy(item.ID)
}

// ocrSnippet finds query (case-insensitive, already trimmed by the
// caller's search) inside the image's recognized text and returns the
// parts the clipboard row shows: 命中词 plus about 12 runes of context on
// each side, ellipsized at the text's ends. ok is false when the query
// does not hit (the row then shows its ordinary meta line).
func ocrSnippet(text, query string) (before, hit, after string, ok bool) {
	q := ocrFold(query)
	if q == "" {
		return "", "", "", false
	}
	// The matcher and the snippet both look in the whitespace-folded text
	// (newlines and runs of spaces collapse to single spaces): what the
	// row shows is always exactly what matched.
	flat := ocrFold(text)
	// strings.ToLower maps rune by rune (no expansions), so a byte hit in
	// the lowered string converts to rune offsets usable on the original.
	low := strings.ToLower(flat)
	lq := strings.ToLower(q)
	i := strings.Index(low, lq)
	if i < 0 {
		return "", "", "", false
	}
	runes := []rune(flat)
	lo := utf8.RuneCountInString(low[:i])
	hi := lo + utf8.RuneCountInString(lq)
	from := max(0, lo-12)
	to := min(len(runes), hi+12)
	before, hit, after = string(runes[from:lo]), string(runes[lo:hi]), string(runes[hi:to])
	if from > 0 {
		before = "…" + before
	}
	if to < len(runes) {
		after += "…"
	}
	return before, hit, after, true
}

// ocrFold is the single normalization both matching and the snippet use:
// runs of whitespace (including the stored newlines) collapse to one
// space, edges trimmed.
func ocrFold(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ocrSnippetLine draws the 「图中文字：…」 line of a clipboard row whose
// recognized text matched the search: context dim, the hit in ink.
func ocrSnippetLine(c *ui.Context, before, hit, after string) ui.Element {
	return ui.RichText(c,
		ui.Span{Text: loc(c).OcrSnippetPrefix(), Color: ink2},
		ui.Span{Text: before, Color: ink2},
		ui.Span{Text: hit, Color: ink, Weight: 600},
		ui.Span{Text: after, Color: ink2},
	).SingleLine().FontSize(11)
}
