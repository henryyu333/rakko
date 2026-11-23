//go:build darwin

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"rakko/internal/note"
)

func makeNoteTestPNG(w, h int, c1, c2 color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(max(h-1, 1))
		for x := 0; x < w; x++ {
			s := float64(x) / float64(max(w-1, 1))
			k := 0.5*s + 0.5*t
			col := color.RGBA{
				R: uint8(float64(c1.R)*(1-k) + float64(c2.R)*k),
				G: uint8(float64(c1.G)*(1-k) + float64(c2.G)*k),
				B: uint8(float64(c1.B)*(1-k) + float64(c2.B)*k),
				A: 255,
			}
			// Subtle grid lines so scaling & aspect ratio are obvious on inspection.
			if x%40 == 0 || y%40 == 0 || x < 2 || y < 2 || x >= w-2 || y >= h-2 {
				col = color.RGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.SetRGBA(x, y, col)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func saveNoteSeg8Shot(t *testing.T, view *ui.Tester, name string) {
	t.Helper()
	dir := filepath.Join("/tmp/Rakko-v1186-canvas", "seg8")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, view.Image()); err != nil {
		t.Fatal(err)
	}
}

func countAttachments(t *testing.T, doc *richtext.Document) []*richtext.Attachment {
	t.Helper()
	var out []*richtext.Attachment
	for _, p := range doc.Text().Paragraphs() {
		for _, r := range p.Runs {
			if r.Attributes.Attachment != nil {
				out = append(out, r.Attributes.Attachment)
			}
		}
	}
	return out
}

func firstAttachmentOffset(doc *richtext.Document) (pos int, ok bool) {
	offset := 0
	for i, p := range doc.Text().Paragraphs() {
		if i > 0 {
			offset++ // newline between paragraphs
		}
		for _, r := range p.Runs {
			n := len([]rune(r.Text))
			if r.Attributes.Attachment != nil {
				return offset, true
			}
			offset += n
		}
	}
	return 0, false
}

// TestNoteUneditedPlainTextPreservesBytesMtimeAndInode guarantees the hard
// invariant: opening an existing plain-text Markdown note (even with formatting
// whitespace that richtext.Text.Markdown() would normalize) and closing,
// blurring, selecting, or ticking without editing must NOT rewrite the file
// (bytes, mtime, and inode remain 100% identical).
func TestNoteUneditedPlainTextPreservesBytesMtimeAndInode(t *testing.T) {
	dir := t.TempDir()
	store, err := note.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	oddRaw := "# Unedited Title\nFirst line without blank gap\n\n\n- trailing spaces   \n  \n"
	if err := store.Write(oddRaw); err != nil {
		t.Fatal(err)
	}
	// Set a distinct historical mtime so any rewrite is unmistakable.
	past := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	if err := os.Chtimes(store.Path(), past, past); err != nil {
		t.Fatal(err)
	}

	beforeBytes, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	beforeStat := beforeInfo.Sys().(*syscall.Stat_t)

	// Guard: verify richtext.ParseMarkdown().Markdown() WOULD have altered it
	// if we hadn't protected unedited writes with Document.Version().
	if norm := richtext.ParseMarkdown(oddRaw).Markdown(); norm == oddRaw {
		t.Fatalf("test fixture needs odd Markdown that normalizes differently; got equal")
	}

	a := &shelf{dataDir: dir}
	n := a.noteSession()
	if n == nil {
		t.Fatal("no note session")
	}
	t.Cleanup(func() {
		if n.watch != nil {
			n.watch.Close()
			n.watch = nil
		}
	})
	if n.dirty() {
		t.Fatal("freshly opened unedited note reported dirty")
	}

	view := ui.NewTester(a.noteView, noteW, noteH)
	view.Frame()
	view.Frame()
	// Selecting/moving caret, ticking, blurring, flushing, closing without editing.
	n.doc.SetSelection(0, 8)
	view.Frame()
	if n.dirty() {
		t.Fatal("selection change marked unedited note dirty")
	}
	if n.tick(time.Now().Add(5 * time.Second)) {
		t.Fatal("tick wrote unedited note")
	}
	n.onBlur()
	if err := n.flush(); err != nil {
		t.Fatal(err)
	}
	n.close()
	a.hide(hideExplicit)

	afterBytes, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	afterStat := afterInfo.Sys().(*syscall.Stat_t)

	if !bytes.Equal(afterBytes, beforeBytes) {
		t.Fatalf("unedited note bytes changed:\n got: %q\nwant: %q", afterBytes, beforeBytes)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("unedited note mtime changed: %v -> %v", beforeInfo.ModTime(), afterInfo.ModTime())
	}
	if afterStat.Ino != beforeStat.Ino || !os.SameFile(beforeInfo, afterInfo) {
		t.Fatalf("unedited note inode changed: %d -> %d", beforeStat.Ino, afterStat.Ino)
	}
}

// TestNoteRichTextImagesPasteDropDeleteReloadAndThumbScreenshots exercises
// the full Segment 8 rich-text + image workflow end to end and saves
// headless visual inspection screenshots to /tmp/Rakko-v1186-canvas/seg8/.
func TestNoteRichTextImagesPasteDropDeleteReloadAndThumbScreenshots(t *testing.T) {
	a, n := noteTestShelf(t)
	origReadClip := noteReadClipboardImage
	t.Cleanup(func() { noteReadClipboardImage = origReadClip })

	// 1. Wide (480x240) & square (180x180) PNG fixtures.
	pngWide := makeNoteTestPNG(480, 240, color.RGBA{R: 56, G: 132, B: 244, A: 255}, color.RGBA{R: 138, G: 72, B: 230, A: 255})
	pngDrop := makeNoteTestPNG(180, 180, color.RGBA{R: 235, G: 95, B: 75, A: 255}, color.RGBA{R: 245, G: 175, B: 65, A: 255})

	view := ui.NewTester(a.noteView, noteW, noteH)
	view.Frame()
	view.Type("#")
	view.Type(" ")
	view.Type("设计稿评审记录")
	view.Key(0, ui.KeyEnter)
	view.Type("已对齐工具栏间距与字体权重：")
	view.Key(0, ui.KeyEnter)
	view.Frame()

	// 2. Paste image from clipboard via OnCommand("paste") + deduplication check.
	noteReadClipboardImage = func() []byte { return pngWide }
	view.Command("paste")
	view.Frame()

	atts := countAttachments(t, n.doc)
	if len(atts) != 1 {
		t.Fatalf("attachments after first paste = %d, want 1", len(atts))
	}
	if !strings.HasPrefix(atts[0].URL, "assets/") || !strings.HasSuffix(atts[0].URL, ".png") {
		t.Fatalf("unexpected attachment URL %q", atts[0].URL)
	}
	if atts[0].Image == nil {
		t.Fatal("pasted attachment has nil Image")
	}

	// Capture screenshot 01 of rich note window with formatted text + scaled inline image.
	saveNoteSeg8Shot(t, view, "01-note-rich-image.png")

	// Paste the same bytes again: must deduplicate to 1 file in notes/assets/.
	view.Type("\n")
	view.Command("paste")
	view.Frame()
	entries, err := os.ReadDir(n.store.AssetsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("duplicate paste created %d asset files, want 1", len(entries))
	}

	// Empty clipboard image falls through ("paste" does not swallow plain text paste).
	noteReadClipboardImage = func() []byte { return nil }
	view.SetClipboard("补充备注一行")
	view.Command("paste")
	view.Frame()
	if !strings.Contains(n.doc.Text().PlainText(), "补充备注一行") {
		t.Fatalf("fallback text paste failed: %q", n.doc.Text().PlainText())
	}

	// 3. Flush to disk and verify relative assets/ Markdown link is written.
	if err := n.flush(); err != nil {
		t.Fatal(err)
	}
	savedMD := fileText(t, n)
	if !strings.Contains(savedMD, "](assets/") {
		t.Fatalf("saved markdown missing relative assets/ link:\n%s", savedMD)
	}

	// 4. Drop image file onto the note editor via ui.Tester.DropFiles.
	dropDir := t.TempDir()
	dropPath := filepath.Join(dropDir, "dropped-card.png")
	if err := os.WriteFile(dropPath, pngDrop, 0o600); err != nil {
		t.Fatal(err)
	}
	nonImg := filepath.Join(dropDir, "readme.txt")
	_ = os.WriteFile(nonImg, []byte("ignore"), 0o600)
	view.DropFiles(160, 160, nonImg, dropPath)
	view.Frame()
	entries, err = os.ReadDir(n.store.AssetsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 asset files after drop, got %d", len(entries))
	}

	// 5. Delete an attachment with Backspace (⌫).
	beforeAtts := len(countAttachments(t, n.doc))
	if beforeAtts != 3 {
		t.Fatalf("expected 3 inline attachments before delete, got %d", beforeAtts)
	}
	pos, ok := firstAttachmentOffset(n.doc)
	if !ok {
		t.Fatal("firstAttachmentOffset returned false")
	}
	n.doc.SetSelection(pos+1, pos+1)
	view.Key(0, ui.KeyBackspace)
	view.Frame()
	afterAtts := len(countAttachments(t, n.doc))
	if afterAtts != beforeAtts-1 {
		t.Fatalf("Backspace did not remove attachment: %d -> %d", beforeAtts, afterAtts)
	}
	if err := n.flush(); err != nil {
		t.Fatal(err)
	}

	// 6. Reopen fresh session from same directory: Markdown re-parses and binds images.
	a2 := &shelf{dataDir: a.dataDir}
	n2 := a2.noteSession()
	if n2 == nil {
		t.Fatal("failed to reopen note session")
	}
	t.Cleanup(func() {
		if n2.watch != nil {
			n2.watch.Close()
			n2.watch = nil
		}
	})
	if n2.dirty() {
		t.Fatal("fresh reopened session reported dirty")
	}
	reloadedAtts := countAttachments(t, n2.doc)
	if len(reloadedAtts) == 0 {
		t.Fatal("reopened session has 0 attachments")
	}
	for i, at := range reloadedAtts {
		if at.Image == nil {
			t.Fatalf("reopened attachment %d (%q) failed to bind bitmap", i, at.URL)
		}
	}

	// 7. Capture wide card thumbnails: image-note card vs text-only card.
	drawCard := func(s *shelf) *ui.Tester {
		h := toolRowH(1)
		tv := ui.NewTester(func(c *ui.Context) {
			c.Root().Background(surfaceRaise)
			card := ui.Column(c.Key("card")).Width(toolColW(2)).Height(h).Radius(14).Background(fill1)
			s.noteBody(c, card, 2, cardPanel)
		}, int(toolColW(2)), int(h))
		tv.SetDark(true)
		tv.Frame()
		tv.Frame()
		return tv
	}
	cardWithImg := drawCard(a2)
	if _, found := cardWithImg.Find("设计稿评审记录"); !found {
		t.Fatalf("wide image card missing title; texts: %v", cardWithImg.Texts())
	}
	if _, found := cardWithImg.Find("已对齐工具栏间距与字体权重："); !found {
		t.Fatalf("wide image card missing preview body line; texts: %v", cardWithImg.Texts())
	}
	saveNoteSeg8Shot(t, cardWithImg, "02-note-card-thumb-wide.png")

	// Text-only note preserves standard wide card layout without thumbnail shift.
	aText, nText := noteTestShelf(t)
	if err := nText.store.Write("# 纯文本便签\n– 第一行待办\n– 第二行待办\n– 第三行待办\n"); err != nil {
		t.Fatal(err)
	}
	if err := nText.load(); err != nil {
		t.Fatal(err)
	}
	cardTextOnly := drawCard(aText)
	if _, found := cardTextOnly.Find("纯文本便签"); !found {
		t.Fatal("text-only card missing title")
	}
	saveNoteSeg8Shot(t, cardTextOnly, "03-note-card-text-only.png")

	// 8. External file change with image hot-reloads cleanly when clean.
	dropRel, _ := n2.store.SaveAsset(pngDrop, "png")
	extMD := "# 外部更新\n\n![drop](" + dropRel + ")\n"
	if err := note.WriteFile(n2.store.Path(), extMD); err != nil {
		t.Fatal(err)
	}
	a2.noteOutside()
	extAtts := countAttachments(t, n2.doc)
	if len(extAtts) != 1 || extAtts[0].URL != dropRel || extAtts[0].Image == nil {
		t.Fatalf("external reload did not bind new image attachment cleanly: %+v", extAtts)
	}
}
