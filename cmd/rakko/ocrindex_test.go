//go:build darwin

package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/internal/store"
)

// testImagePNG is a small real PNG; hue distinguishes captures so the
// content-addressed store keeps them as separate items.
func testImagePNG(t *testing.T, hue uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.SetRGBA(x, y, color.RGBA{R: hue, G: uint8(16 * x), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testOCRShelf builds a headless shelf with a clipboard image capture and
// an ocrIndex whose recognizer and dispatch are fully synchronous.
func testOCRShelf(t *testing.T) (*shelf, *ocrIndex, *recordClip) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	rec := &recordClip{}
	a := &shelf{
		store: s, dataDir: dir, visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{}, write: rec,
	}
	o := &ocrIndex{host: a, interval: -1}
	o.dispatch = func(f func()) { f() }
	o.run = func(f func()) { f() }
	a.modules = append(a.modules, o)
	return a, o, rec
}

// addClipImage records a clipboard image in the store the way the
// clipboard module does.
func addClipImage(t *testing.T, a *shelf, hue uint8) store.Item {
	t.Helper()
	if err := a.store.AddClipImage(testImagePNG(t, hue), "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	return a.store.Items[0]
}

// TestOCRBackfillRecognizesOnce covers the background pass: an old
// clipboard image is recognized on tick, the result is stored, and a
// recognized item is never asked again.
func TestOCRBackfillRecognizesOnce(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	addClipImage(t, a, 30)
	calls := 0
	o.recognize = func(path string) ([]string, error) {
		calls++
		return []string{"货架 Hello"}, nil
	}
	o.tick(time.Now())
	if calls != 1 {
		t.Fatalf("tick should recognize the pending image once, got %d", calls)
	}
	if a.store.Items[0].OCRText != "货架 Hello" || !a.store.Items[0].OCRDone {
		t.Fatalf("result not stored: %+v", a.store.Items[0])
	}
	o.tick(time.Now())
	if calls != 1 {
		t.Fatalf("recognized image must not be re-recognized")
	}
}

// TestOCRNewestFirst: a fresh capture is recognized before older pending
// ones.
func TestOCRNewestFirst(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	addClipImage(t, a, 30) // older
	addClipImage(t, a, 60) // newer ends up at Items[0]
	var order []string
	o.recognize = func(path string) ([]string, error) {
		order = append(order, path)
		return nil, nil
	}
	o.tick(time.Now())
	if len(order) != 1 {
		t.Fatalf("one image per tick, got %d", len(order))
	}
	if order[0] != a.store.Items[0].Path {
		t.Fatalf("newest item must be recognized first")
	}
	o.tick(time.Now())
	if len(order) != 2 || order[1] != a.store.Items[1].Path {
		t.Fatalf("then the older one: %v", order)
	}
}

// TestOCRGapPacesRecognition: without spacing a second tick in the same
// interval must not launch another worker.
func TestOCRGapPacesRecognition(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	addClipImage(t, a, 30)
	addClipImage(t, a, 60)
	now := time.Now()
	o.interval = 2 * time.Second
	o.now = func() time.Time { return now }
	calls := 0
	o.recognize = func(string) ([]string, error) { calls++; return nil, nil }
	o.tick(now)
	o.tick(now)
	if calls != 1 {
		t.Fatalf("the gap must hold the second image back, got %d", calls)
	}
	now = now.Add(3 * time.Second)
	o.tick(now)
	if calls != 2 {
		t.Fatalf("after the gap the next image runs, got %d", calls)
	}
}

// TestOCRRecognizeErrorSessionOnly: a failed recognition is not retried
// in the same session but the item stays un-done for the next launch.
func TestOCRRecognizeErrorSessionOnly(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	addClipImage(t, a, 30)
	calls := 0
	o.recognize = func(string) ([]string, error) { calls++; return nil, errors.New("vision down") }
	o.tick(time.Now())
	o.tick(time.Now())
	if calls != 1 {
		t.Fatalf("failure must not hot-retry, got %d", calls)
	}
	if a.store.Items[0].OCRDone {
		t.Fatal("a failed recognition must not be marked done")
	}
}

// TestOCRDeletedWhileRecognizing: the item is removed between start and
// finish; the result is dropped without error and without a crash.
func TestOCRDeletedWhileRecognizing(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	it := addClipImage(t, a, 30)
	// dispatch is captured so the result lands after the removal.
	var pending func()
	o.dispatch = func(f func()) { pending = f }
	o.recognize = func(string) ([]string, error) { return []string{"文字"}, nil }
	o.tick(time.Now())
	if err := a.store.Remove(it.ID); err != nil {
		t.Fatal(err)
	}
	pending()
	if len(a.store.Items) != 0 {
		t.Fatal("the removed item must stay removed")
	}
	if a.statusKind == statusErr {
		t.Fatalf("a dropped result is not an error: %q", a.status)
	}
}

// TestClipboardSearchHitsOCRText is the search side: an image whose
// recognized text contains the query is listed, before and after.
func TestClipboardSearchHitsOCRText(t *testing.T) {
	a, _, _ := testOCRShelf(t)
	it := addClipImage(t, a, 30)
	p := &clipboardPage{host: a}
	a.search = "退货"
	if got := p.items(); len(got) != 0 {
		t.Fatalf("unrecognized image must not match: %v", got)
	}
	if err := a.store.SetOCR(it.ID, "请支持七天无理由退货 policy ABC"); err != nil {
		t.Fatal(err)
	}
	if got := p.items(); len(got) != 1 || got[0].ID != it.ID {
		t.Fatalf("OCR text must match the search: %v", got)
	}
	a.search = "POLICY" // case-insensitive on the stored text
	if got := p.items(); len(got) != 1 {
		t.Fatalf("matching is case-insensitive: %v", got)
	}
}

// TestOCRSnippet covers hit position, ~12-rune context and ellipsis on
// both ends, including a CJK query.
func TestOCRSnippet(t *testing.T) {
	text := strings.Repeat("甲", 20) + "货架 ShelfU" + strings.Repeat("乙", 20)
	before, hit, after, ok := ocrSnippet(text, "ShelfU")
	if !ok || hit != "ShelfU" {
		t.Fatalf("hit missed: %q %q %q", before, hit, after)
	}
	if !strings.HasPrefix(before, "…") || !strings.HasSuffix(after, "…") {
		t.Fatalf("long text must ellipsize both sides: %q / %q", before, after)
	}
	if len([]rune(before)) != 13 || len([]rune(after)) != 13 { // 12 runes + …
		t.Fatalf("context should be ~12 runes: %q / %q", before, after)
	}
	// Start of the text: no leading ellipsis.
	before, hit, after, ok = ocrSnippet("货架上的商品", "货架")
	if !ok || before != "" || hit != "货架" || !strings.HasPrefix(after, "上") {
		t.Fatalf("start hit: %q %q %q", before, hit, after)
	}
	// Case-insensitive on ASCII, unchanged for CJK.
	if _, hit, _, ok = ocrSnippet("TypeScript Notes", "types"); !ok || hit != "TypeS" {
		t.Fatalf("case-insensitive hit: %q", hit)
	}
	if _, _, _, ok = ocrSnippet("没有任何命中", "不存在"); ok {
		t.Fatal("a miss must report ok=false")
	}
	// Newlines flatten for the single-line row.
	before, hit, _, ok = ocrSnippet("第一行\n第二行 hit here", "hit")
	if !ok || !strings.HasSuffix(before, "二行 ") {
		t.Fatalf("multi-line flattening: %q %q", before, hit)
	}
}

// TestRecognizeCopyMenuItem: the entry exists only for a single image,
// copies stored results without recognizing, and for an unrecognized
// image runs the recognizer then copies.
func TestRecognizeCopyMenuItem(t *testing.T) {
	a, o, rec := testOCRShelf(t)
	it := addClipImage(t, a, 30)
	if err := a.store.SetOCR(it.ID, "已识别的文字"); err != nil {
		t.Fatal(err)
	}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.setTab(pageClipboard)
	view.Frame()
	if err := view.RightClick(itemTitle(it)); err != nil {
		t.Fatal(err)
	}
	menu := view.Menu()
	found := false
	for _, l := range menu {
		if l == "识别文字并复制" {
			found = true
		}
	}
	if !found {
		t.Fatalf("image row lacks the item: %v", menu)
	}
	if err := view.ChooseMenuItem("识别文字并复制"); err != nil {
		t.Fatal(err)
	}
	if rec.text != "已识别的文字" {
		t.Fatalf("stored text should be copied, got %q", rec.text)
	}
	if !strings.Contains(a.status, "已复制识别出的文字 · 6 字") {
		t.Fatalf("toast: %q", a.status)
	}
	if o.busy(it.ID) {
		t.Fatal("a stored result must not start a worker")
	}
}

// TestRecognizeCopyRunsRecognizer covers the cold path: the item is
// recognized on demand, copied, and the result persisted for next time.
func TestRecognizeCopyRunsRecognizer(t *testing.T) {
	a, o, rec := testOCRShelf(t)
	addClipImage(t, a, 30)
	calls := 0
	o.recognize = func(string) ([]string, error) { calls++; return []string{"新鲜识别 Fresh"}, nil }
	a.copyRecognizedText(a.store.Items[0])
	if calls != 1 {
		t.Fatalf("recognizer should run once, got %d", calls)
	}
	if rec.text != "新鲜识别 Fresh" {
		t.Fatalf("copied %q", rec.text)
	}
	// The recognized text is also a clipboard capture now, so it sits at
	// the front; the image keeps its written-back result below it.
	if got := a.store.Items[0]; got.Kind != store.KindText || got.Text != "新鲜识别 Fresh" || got.SourceApp != clipSourceOCR {
		t.Fatalf("recognized text must land at the front of the clipboard page: %+v", got)
	}
	img := slices.IndexFunc(a.store.Items, func(x store.Item) bool { return x.Kind == store.KindImage })
	if img < 0 || a.store.Items[img].OCRText != "新鲜识别 Fresh" || !a.store.Items[img].OCRDone {
		t.Fatalf("menu recognition must persist: %+v", a.store.Items)
	}
}

// TestRecognizeCopyNoText shows the 「没有识别到文字」 toast and copies
// nothing when the recognizer finds nothing.
func TestRecognizeCopyNoText(t *testing.T) {
	a, o, rec := testOCRShelf(t)
	addClipImage(t, a, 30)
	o.recognize = func(string) ([]string, error) { return nil, nil }
	a.copyRecognizedText(a.store.Items[0])
	if rec.text != "" {
		t.Fatalf("nothing must be copied, got %q", rec.text)
	}
	if a.status != "没有识别到文字" {
		t.Fatalf("toast: %q", a.status)
	}
}

// TestRecognizeCopyMenuOnlyForImages: text rows and multi-selections do
// not offer the entry.
func TestRecognizeCopyMenuOnlyForImages(t *testing.T) {
	a, _, _ := testOCRShelf(t)
	if err := a.store.AddClipText("只是文字", "com.example.App"); err != nil {
		t.Fatal(err)
	}
	a.setTab(pageClipboard)
	view := ui.NewTester(a.view, sheetW, panelTestH)
	if err := view.RightClick("只是文字"); err != nil {
		t.Fatal(err)
	}
	for _, l := range view.Menu() {
		if l == "识别文字并复制" {
			t.Fatalf("a text row must not offer it: %v", view.Menu())
		}
	}
}

// TestOCRNeverTwoConcurrent: while one worker is still running the tick
// must not launch another, no matter how many images wait — a 30ms tick
// would otherwise stack every pending recognition at once.
func TestOCRNeverTwoConcurrent(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	addClipImage(t, a, 30)
	addClipImage(t, a, 60)
	addClipImage(t, a, 90)
	// Capture the worker instead of running it: the item stays in flight.
	var workers []func()
	o.run = func(f func()) { workers = append(workers, f) }
	o.recognize = func(string) ([]string, error) { return []string{"字"}, nil }
	for i := range 5 {
		o.tick(time.Now().Add(time.Duration(i) * 3 * time.Second))
	}
	if len(workers) != 1 {
		t.Fatalf("one worker may run at a time, got %d queued launches", len(workers))
	}
	workers[0]()
	if !a.store.Items[0].OCRDone {
		t.Fatal("the first worker must have recognized the newest image")
	}
	o.tick(time.Now().Add(30 * time.Second))
	if len(workers) != 2 {
		t.Fatalf("after the first finishes the next may start, got %d", len(workers))
	}
}

// TestOCRMenuWaitsForTheWorker: 「识别文字并复制」 while another
// recognition runs queues ahead of the background backlog, tells the
// user, and copies once its turn comes — never silently dropped.
func TestOCRMenuWaitsForTheWorker(t *testing.T) {
	a, o, rec := testOCRShelf(t)
	addClipImage(t, a, 30)        // the background item (Items[1])
	it2 := addClipImage(t, a, 60) // the menu's item (Items[0])
	addClipImage(t, a, 90)        // more backlog (Items[0] after this)
	var workers []func()
	o.run = func(f func()) { workers = append(workers, f) }
	var order []string
	o.recognize = func(path string) ([]string, error) {
		order = append(order, path)
		return []string{"识别到的菜单文字"}, nil
	}
	o.tick(time.Now())
	if len(workers) != 1 {
		t.Fatalf("first tick starts the background worker, got %d", len(workers))
	}
	a.copyRecognizedText(it2)
	if len(workers) != 1 {
		t.Fatalf("the menu request must queue behind the running worker, not start a second")
	}
	if !strings.Contains(a.status, "正在识别") {
		t.Fatalf("a queued request must tell the user, status=%q", a.status)
	}
	// The background worker finishes; the queued copy runs before the
	// backlog's next item and copies to the clipboard.
	workers[0]()
	o.tick(time.Now())
	if len(workers) != 2 {
		t.Fatalf("the queued item must be next, got %d workers", len(workers))
	}
	workers[1]()
	if order[1] != it2.Path {
		t.Fatalf("the queued request must precede the backlog: %v", order)
	}
	if rec.text != "识别到的菜单文字" {
		t.Fatalf("the queued copy must land on the clipboard, got %q", rec.text)
	}
	if !strings.Contains(a.status, "已复制识别出的文字") {
		t.Fatalf("completion toast missing: %q", a.status)
	}
}

// TestOCRBusySaysSo: the same item asked while its own worker runs gets
// the busy toast instead of silence.
func TestOCRBusySaysSo(t *testing.T) {
	a, o, rec := testOCRShelf(t)
	it := addClipImage(t, a, 30)
	var workers []func()
	o.run = func(f func()) { workers = append(workers, f) }
	o.recognize = func(string) ([]string, error) { return []string{"字"}, nil }
	o.tick(time.Now())
	a.copyRecognizedText(it)
	if a.status != "正在识别，请稍候" {
		t.Fatalf("a busy item must show the busy hint, got %q", a.status)
	}
	if rec.text != "" || len(workers) != 1 {
		t.Fatal("nothing may copy or run while the item is mid-recognition")
	}
}

// TestOCRAbortClearsInflight: a worker that sees the stop channel before
// recognizing releases the item's in-flight mark.
func TestOCRAbortClearsInflight(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	addClipImage(t, a, 30)
	a.stop = make(chan struct{})
	close(a.stop)
	var workers []func()
	o.run = func(f func()) { workers = append(workers, f) }
	calls := 0
	o.recognize = func(string) ([]string, error) { calls++; return nil, nil }
	o.tick(time.Now())
	if len(workers) != 1 {
		t.Fatal("the worker must have been launched")
	}
	workers[0]()
	if calls != 0 {
		t.Fatal("a stopped app must not run the recognizer")
	}
	if o.inflight[a.store.Items[0].ID] {
		t.Fatal("an aborted worker must drop its in-flight mark")
	}
}

// TestOCRSearchMatchesFoldedText: the matcher and the snippet share one
// normalization, so a query with extra spaces hits the folded text and a
// snippet is always produced for a listed row.
func TestOCRSearchMatchesFoldedText(t *testing.T) {
	text := "货架 图片    文字"
	query := "图片    文字" // consecutive spaces in the query
	if !clipboardItemMatches(store.Item{Kind: store.KindImage, OCRDone: true, OCRText: text},
		strings.ToLower(query)) {
		t.Fatal("the matcher must fold whitespace like the snippet does")
	}
	// Extra spaces in the text fold to the query's single space too.
	if !clipboardItemMatches(store.Item{Kind: store.KindImage, OCRDone: true, OCRText: "货架 图片    文字"},
		"图片 文字") {
		t.Fatal("extra spaces in the recognized text must not break a plain query")
	}
	if _, hit, _, ok := ocrSnippet(text, query); !ok || hit == "" {
		t.Fatal("a matched query must produce a snippet, not an empty 「图中文字：」")
	}
	// And the other direction: a miss keeps ok=false so the row falls
	// back to 「应用 · 时间」.
	if _, _, _, ok := ocrSnippet(text, "不存在的内容"); ok {
		t.Fatal("a non-match must report ok=false")
	}
}

// TestOCRQueuedItemDeletedSaysCancelled: a queued 「识别文字并复制」 whose
// image is deleted before its turn must call the callback so the user
// hears a definite outcome — never the promised copy hanging forever
// (PhaseOcr: the queue dropped it silently and the status line kept
// 「正在识别，完成后自动复制」).
func TestOCRQueuedItemDeletedSaysCancelled(t *testing.T) {
	a, o, rec := testOCRShelf(t)
	addClipImage(t, a, 30) // background item, recognized first below
	var workers []func()
	o.run = func(f func()) { workers = append(workers, f) }
	o.recognize = func(string) ([]string, error) { return []string{"字"}, nil }
	o.tick(time.Now()) // the background worker occupies the recognizer
	it2 := addClipImage(t, a, 60)
	a.copyRecognizedText(it2) // queues behind the running worker
	if len(o.pending) != 1 {
		t.Fatalf("the request must queue, pending=%d", len(o.pending))
	}
	if err := a.store.Remove(it2.ID); err != nil {
		t.Fatal(err)
	}
	workers[0]()
	o.tick(time.Now())
	if !strings.Contains(a.status, "未复制") {
		t.Fatalf("the queued request died silently: status=%q", a.status)
	}
	if rec.text != "" {
		t.Fatalf("a deleted image's text must not be copied, got %q", rec.text)
	}
}

// TestOCRDeletedMidRecognitionDoesNotCopy: the image is removed while its
// worker runs; the callback must not fall through to the copy path with
// the dead item's text (PhaseOcr: SetOCR's "no item" was swallowed, the
// callback saw err==nil and the clipboard took a deleted image's words).
func TestOCRDeletedMidRecognitionDoesNotCopy(t *testing.T) {
	a, o, rec := testOCRShelf(t)
	addClipImage(t, a, 30)
	it := addClipImage(t, a, 60)
	var workers []func()
	o.run = func(f func()) { workers = append(workers, f) }
	o.recognize = func(string) ([]string, error) { return []string{"不应复制的文字"}, nil }
	a.copyRecognizedText(it)
	if len(workers) != 1 {
		t.Fatalf("the menu request starts one worker, got %d", len(workers))
	}
	if err := a.store.Remove(it.ID); err != nil {
		t.Fatal(err)
	}
	workers[0]()
	if rec.text != "" {
		t.Fatalf("a deleted image's text was copied anyway: %q", rec.text)
	}
	if !strings.Contains(a.status, "未复制") {
		t.Fatalf("the deletion must be reported, not the success toast: %q", a.status)
	}
	if _, err := os.Stat(it.Path); err == nil {
		t.Fatal("the managed file is gone with its item — the result path must not read it")
	}
}

// TestOCRDeletedItemIsNotRememberedFailed: deleting an image mid-run is a
// cancellation, not a failure — the next capture of the same bytes (same
// content id) must be recognized, not skipped for the rest of the session.
func TestOCRDeletedItemIsNotRememberedFailed(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	it := addClipImage(t, a, 30)
	var workers []func()
	o.run = func(f func()) { workers = append(workers, f) }
	o.recognize = func(string) ([]string, error) { return []string{"字"}, nil }
	a.copyRecognizedText(it)
	if err := a.store.Remove(it.ID); err != nil {
		t.Fatal(err)
	}
	workers[0]()
	if o.failed[it.ID] {
		t.Fatal("a deleted item is a cancellation, not a recognition failure")
	}
	// A new capture of the same content gets the same id; it must be picked up.
	if err := a.store.AddClipImage(testImagePNG(t, 30), "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	if len(a.store.Items) == 0 || !o.needs(a.store.Items[0]) {
		t.Fatal("the re-added image must need recognition again")
	}
}

// TestOCRDeletedAndErroredIsNotRememberedFailed: a recognition that errors
// AND finds its item gone (user deleted it while the decoder held a bad
// file) must not poison the id either — only genuine failures on a live
// item belong in failed[].
func TestOCRDeletedAndErroredIsNotRememberedFailed(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	it := addClipImage(t, a, 30)
	var workers []func()
	o.run = func(f func()) { workers = append(workers, f) }
	o.recognize = func(string) ([]string, error) { return nil, errors.New("vision lost") }
	a.copyRecognizedText(it)
	if err := a.store.Remove(it.ID); err != nil {
		t.Fatal(err)
	}
	workers[0]()
	if o.failed[it.ID] {
		t.Fatal("a deleted item's failure is still a cancellation for retry purposes")
	}
}

// TestOCRWorkerAndWordConvertNeverOverlap: the menu/background recognizer
// and the Word converter share one Vision call site — at most one
// recognition runs at a time, whichever caller launched it.
func TestOCRWorkerAndWordConvertNeverOverlap(t *testing.T) {
	a, o, _ := testOCRShelf(t)
	it := addClipImage(t, a, 30)
	var inFlight int64
	var maxSeen int64
	release := make(chan struct{})
	old := recognizeText
	recognizeText = func(string) ([]string, error) {
		n := atomic.AddInt64(&inFlight, 1)
		if n > maxSeen {
			maxSeen = n
		}
		<-release
		atomic.AddInt64(&inFlight, -1)
		return []string{"字"}, nil
	}
	t.Cleanup(func() { recognizeText = old })

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); wordFromImage(it.Path, filepath.Join(t.TempDir(), "w.docx")) }()
	go func() { defer wg.Done(); o.start(it, nil) }()
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if maxSeen != 1 {
		t.Fatalf("two Vision calls ran at once (max %d); the paths must serialize", maxSeen)
	}
}
