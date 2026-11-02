//go:build darwin

package main

// Regression: recognized text was copied to the system clipboard but never
// recorded, because every copy went through copyText (which skips the
// clipboard watcher so re-copying a row does not duplicate it). Recognized
// text is new content: it must land at the front of the clipboard page with
// 「框选文字」 as its source. Each of the three OCR entries is driven here
// through its own seam; no screen capture runs.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/store"
)

// ocrRow asserts the newest store item is the recognized text, captured
// from ShelfU's own recognition.
func ocrRow(t *testing.T, a *shelf, want string) store.Item {
	t.Helper()
	if len(a.store.Items) == 0 {
		t.Fatal("no item recorded for the recognized text")
	}
	it := a.store.Items[0]
	if it.Kind != store.KindText || it.Text != want {
		t.Fatalf("newest item = %q (%v), want the recognized text", it.Text, it.Kind)
	}
	if it.Source != store.SourceClipboard {
		t.Fatalf("recognized text source = %q, want the clipboard page's %q", it.Source, store.SourceClipboard)
	}
	if it.SourceApp != clipSourceOCR {
		t.Fatalf("recognized text source app = %q, want the recognition marker %q", it.SourceApp, clipSourceOCR)
	}
	return it
}

// ocrRowShown asserts the clipboard page draws the row with 「框选文字」 as
// its source, never a raw bundle id.
func ocrRowShown(t *testing.T, a *shelf, want string) {
	t.Helper()
	a.setTab(pageClipboard)
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	if !view.HasText("框选文字") {
		t.Fatalf("clipboard page does not name the source 框选文字; texts=%v", view.Texts())
	}
	if want != "" && !view.HasText(firstLine(want)) {
		t.Fatalf("recognized row %q missing from the clipboard page; texts=%v", want, view.Texts())
	}
	for _, text := range view.Texts() {
		if text == clipSourceOCR {
			t.Fatalf("clipboard page shows the raw source marker %q", clipSourceOCR)
		}
	}
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// ⌃⌥O / 菜单 / 工具页卡片 all end in finishScreenOCR: the recognized text
// goes on the clipboard and into the clipboard page.
func TestScreenOCRRecordsRecognizedText(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	ocrCapture = func(path string) error { writePixelPNG(t, path); return nil }
	ocrRecognize = func(string) ([]string, error) { return []string{"会议纪要", "第二阶段"}, nil }
	a, _, rec := clipShelf(t)
	a.startScreenOCR()
	want := "会议纪要\n第二阶段"
	if rec.text != want {
		t.Fatalf("clipboard got %q", rec.text)
	}
	if a.status != "已复制识别出的文字 · 9 字" {
		t.Fatalf("status = %q", a.status)
	}
	ocrRow(t, a, want)
	ocrRowShown(t, a, want)
}

// Dragging an image onto the 框选文字 card runs copyDroppedText.
func TestDroppedImageOCRRecordsRecognizedText(t *testing.T) {
	ocrSeams(t)
	ocrRecognize = func(string) ([]string, error) { return []string{"截图里的字"}, nil }
	a, _, rec := clipShelf(t)
	img := filepath.Join(t.TempDir(), "shot.png")
	writePixelPNG(t, img)
	a.copyDroppedText([]string{img})
	if rec.text != "截图里的字" {
		t.Fatalf("clipboard got %q", rec.text)
	}
	if a.ocrScan {
		t.Fatal("batch still marked busy")
	}
	ocrRow(t, a, "截图里的字")
	ocrRowShown(t, a, "截图里的字")
}

// 右键图片「识别文字并复制」 runs copyRecognizedText.
func TestRecognizeAndCopyRecordsRecognizedText(t *testing.T) {
	a, o, rec := testOCRShelf(t)
	item := addClipImage(t, a, 30)
	lines := []string{"发票号码", "A-1024"}
	o.recognize = func(string) ([]string, error) { return lines, nil }
	a.copyRecognizedText(item)
	want := "发票号码\nA-1024"
	if rec.text != want {
		t.Fatalf("clipboard got %q", rec.text)
	}
	if a.status != "已复制识别出的文字 · 11 字" {
		t.Fatalf("status = %q", a.status)
	}
	ocrRow(t, a, want)
	ocrRowShown(t, a, want)
}

// Copying an existing row still records nothing: the recognized row's own
// 复制 goes through copyText, so the list neither grows nor reorders.
func TestCopyingAnExistingRowAddsNoRecord(t *testing.T) {
	a, _, rec := testOCRShelf(t)
	a.copyOCRText("已有的识别结果")
	it := ocrRow(t, a, "已有的识别结果")
	before := len(a.store.Items)
	if !a.copyItem(it) {
		t.Fatal("copying the recorded row failed")
	}
	if rec.text != it.Text {
		t.Fatalf("clipboard got %q, want %q", rec.text, it.Text)
	}
	if after := len(a.store.Items); after != before {
		t.Fatalf("re-copying a row recorded it again: %d -> %d items", before, after)
	}
	if got := a.store.Items[0].ID; got != it.ID {
		t.Fatal("re-copying a row reordered the list")
	}
}

// Recognizing the same text again follows AddClipText's rule: one row,
// moved to the front.
func TestRecognizedTextDedupsLikeAClipboardCapture(t *testing.T) {
	a, _, _ := testOCRShelf(t)
	if err := a.store.AddClipText("另一条", "com.example.App"); err != nil {
		t.Fatal(err)
	}
	a.copyOCRText("重复的识别结果")
	if err := a.store.AddClipText("另一条", "com.example.App"); err != nil {
		t.Fatal(err)
	}
	a.copyOCRText("重复的识别结果")
	n := 0
	for _, it := range a.store.Items {
		if it.Kind == store.KindText && it.Text == "重复的识别结果" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("recognized text stored %d times, want the one deduped row", n)
	}
	if got := a.store.Items[0].Text; got != "重复的识别结果" {
		t.Fatalf("newest row = %q, want the re-recognized text moved to the front", got)
	}
	if got := a.store.Items[0].SourceApp; got != clipSourceOCR {
		t.Fatalf("deduped row source app = %q, want %q", got, clipSourceOCR)
	}
}

// breakStoreSave makes the store's Save fail for the rest of the test: the
// data directory goes read-only, so a new row cannot be persisted. The
// directory is writable again before the test's own cleanups run.
func breakStoreSave(t *testing.T, a *shelf) {
	t.Helper()
	if err := os.Chmod(a.dataDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(a.dataDir, 0o755); err != nil {
			t.Errorf("restore %s: %v", a.dataDir, err)
		}
	})
}

// assertOCRRecordFailure asserts the user is told the copy worked and the
// clipboard-page record did not — with the error dot — instead of the
// success line, and that no half-written row was kept.
func assertOCRRecordFailure(t *testing.T, a *shelf) {
	t.Helper()
	const want = "已复制，但没能记进剪贴板页："
	if !strings.Contains(a.status, want) {
		t.Fatalf("status = %q, want the record-failure line %q", a.status, want)
	}
	if a.statusKind != statusErr {
		t.Fatalf("status kind = %d, want the error kind (%d)", a.statusKind, statusErr)
	}
	for _, it := range a.store.Items {
		if it.SourceApp == clipSourceOCR {
			t.Fatalf("a row whose record failed was kept: %+v", it)
		}
	}
}

// A record that cannot be saved must not be reported as success: the
// success line 「已复制识别出的文字 · N 字」 covered the failure, so the
// user believed the text was in the clipboard page when it was not.
func TestScreenOCRRecordFailureIsReported(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	ocrCapture = func(path string) error { writePixelPNG(t, path); return nil }
	ocrRecognize = func(string) ([]string, error) { return []string{"会议纪要"}, nil }
	a, _, rec := clipShelf(t)
	breakStoreSave(t, a)
	a.startScreenOCR()
	if rec.text != "会议纪要" {
		t.Fatalf("the clipboard write was skipped: got %q", rec.text)
	}
	assertOCRRecordFailure(t, a)
}

// Same for a batch dropped on the 框选文字 card.
func TestDroppedImageRecordFailureIsReported(t *testing.T) {
	ocrSeams(t)
	ocrRecognize = func(string) ([]string, error) { return []string{"截图里的字"}, nil }
	a, _, rec := clipShelf(t)
	img := filepath.Join(t.TempDir(), "shot.png")
	writePixelPNG(t, img)
	breakStoreSave(t, a)
	a.copyDroppedText([]string{img})
	if rec.text != "截图里的字" {
		t.Fatalf("the clipboard write was skipped: got %q", rec.text)
	}
	assertOCRRecordFailure(t, a)
}

// And for 右键「识别文字并复制」 on an image row. The item's result is
// already stored (the common repeat case), so the failure here can only
// be the clipboard-page record — a first-time recognition would fail on
// its own result write before the copy is reached.
func TestRecognizeAndCopyRecordFailureIsReported(t *testing.T) {
	a, _, rec := testOCRShelf(t)
	item := addClipImage(t, a, 30)
	if err := a.store.SetOCR(item.ID, "发票号码"); err != nil {
		t.Fatal(err)
	}
	item = a.store.Items[0]
	breakStoreSave(t, a)
	a.copyRecognizedText(item)
	if rec.text != "发票号码" {
		t.Fatalf("the clipboard write was skipped: got %q", rec.text)
	}
	assertOCRRecordFailure(t, a)
}
