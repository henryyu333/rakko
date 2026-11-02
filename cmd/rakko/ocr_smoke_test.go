//go:build darwin

package main

// Real-window proof for the OCR feature (run with RAKKO_BASKET_SMOKE=1):
// a generated screenshot with Chinese and English text is recorded as a
// clipboard image, the real Vision recognizer reads it in the background,
// the clipboard search hits it, and 「识别文字并复制」 puts the text on the
// real system clipboard. The system clipboard is snapshotted and restored
// by snapshotClipboard; the store lives in the shared smoke data dir, and
// the install build never sees the image (it is never written to the
// general pasteboard — AddClipImage is called directly, the same call the
// clipboard module makes). Screenshots land in /tmp/shelf-ocr/.
//
// Clipboard writes carry the concealed marker and snapshotClipboard
// restores the user's contents after the test.

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"rakko/internal/store"
)

const ocrShotDir = "/tmp/shelf-ocr"

// ocrTextImage draws the fixture: three lines of mixed Chinese/English on
// white, rendered with the system's Arial Unicode so no font is bundled.
func ocrTextImage(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("/System/Library/Fonts/Supplemental/Arial Unicode.ttf")
	if err != nil {
		t.Skipf("no Arial Unicode font: %v", err)
	}
	f, err := opentype.Parse(data)
	if err != nil {
		t.Fatalf("font parse: %v", err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 30, DPI: 72})
	if err != nil {
		t.Fatalf("face: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 760, 210))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: face}
	lines := []string{"货架搜索验收 ShelfU OCR", "退货地址：上海市浦东新区 100 号", "Return Policy OK 2026"}
	for i, s := range lines {
		d.Dot = fixed.P(30, 60+i*60)
		d.DrawString(s)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSmokeOCRSearchAndCopy(t *testing.T) {
	a := redesignShelf(t)
	pngBytes := ocrTextImage(t)
	// 存档一张参考图到截图目录，便于人工核对识别内容。
	if err := os.MkdirAll(ocrShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ocrShotDir, "fixture.png"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	snapshotClipboard(t)
	var oldWrite clipWriter
	onMain(func() { oldWrite, a.write = a.write, fix2PrivateClipboard{} })
	t.Cleanup(func() { onMain(func() { a.write = oldWrite }) })

	onMain(func() {
		// The smoke harness never runs ready(); register the module the
		// same way main.go does, with the real recognizer and the real
		// main-thread dispatch.
		o := &ocrIndex{host: a}
		a.modules = append(a.modules, o)
		if err := a.store.AddClipImage(pngBytes, "com.example.Smoke"); err != nil {
			t.Fatalf("record capture: %v", err)
		}
		o.tick(time.Now())
	})

	// First recognition loads the Vision model: allow up to 90 s.
	var done bool
	for range 180 {
		onMain(func() { done = a.store.Items[0].OCRDone })
		if done {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !done {
		t.Fatal("background recognition never finished in 90 s")
	}
	var text string
	onMain(func() { text = a.store.Items[0].OCRText })
	t.Logf("recognized: %q", text)
	if !strings.Contains(text, "退货地址") || !strings.Contains(text, "ShelfU OCR") {
		t.Fatalf("recognized text missing expected lines: %q", text)
	}

	// The search hit: clipboard page, query 退货, the image row shows
	// 「图中文字：…」 instead of 应用 · 时间.
	onMain(func() {
		a.setTab(pageClipboard)
		a.search = "退货"
	})
	expandShelf(t, a)
	shotNotch(t, a, "ocr-search-hit")
	onMain(func() {
		var items []store.Item = a.visibleItems()
		if len(items) != 1 || items[0].Kind != store.KindImage {
			t.Fatalf("search should list exactly the image: %+v", items)
		}
	})

	// 「识别文字并复制」 on the already-recognized item copies at once via
	// the real clipboard.
	var id string
	onMain(func() { id = a.store.Items[0].ID; a.copyRecognizedText(a.store.Items[0]) })
	_ = id
	time.Sleep(300 * time.Millisecond)
	var board string
	onMain(func() { board = mygo.Clipboard.ReadText() })
	if !strings.Contains(board, "退货地址") {
		t.Fatalf("system clipboard lacks the recognized text: %q", board)
	}
	if !strings.Contains(a.status, "已复制识别出的文字") {
		t.Fatalf("missing the copy toast: %q", a.status)
	}
	shotNotch(t, a, "ocr-copied-toast")

	onMain(func() {
		a.search = ""
		a.hide(hideExplicit)
	})
}
