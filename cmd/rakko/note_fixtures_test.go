package main

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/locales"
)

// insertAfter inserts add right after the first needle in the editor text.
func insertAfter(t *testing.T, n *noteUI, needle, add string) {
	t.Helper()
	s := n.ensureDoc().Text().String()
	i := strings.Index(s, needle)
	if i < 0 {
		t.Fatalf("needle %q missing from %q", needle, s)
	}
	pos := len([]rune(s[:i+len(needle)]))
	n.doc.SetSelection(pos, pos)
	n.doc.InsertText(add)
}

// TestNoteOldFixturesEditOneLine: an old line-based note edited in one line
// keeps every other line byte for byte, including <tags>, code fences and
// table rows.
func TestNoteOldFixturesEditOneLine(t *testing.T) {
	cases := []struct {
		name, src, needle, add string
	}{
		{"plain lines", "买菜\n牛奶\n鸡蛋\n", "牛奶", "和"},
		{"blank line", "第一行\n\n第三行\n", "第一行", "改"},
		{"project smoke", "# 周五发版前\n– 改 README 顶栏描述\n– 补 v1.13 截图\n– [ ] 桌宠姿态验收", "补 v1.13 截图", "新"},
		{"angle and code", "会议 <周三> 待定\n```bash\necho hi\n```\n", "待定", "改"},
		{"literal html", "foo <bar> & baz\n", "baz", "!"},
		{"bold-looking html", "a<b>c\n", "c", "!"},
		{"table", "| 名称 | 数量 |\n| --- | --- |\n| 苹果 | 3 |\n| 香蕉 | 5 |\n", "5", "根"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, n := noteTestShelf(t)
			if err := n.store.Write(tc.src); err != nil {
				t.Fatal(err)
			}
			if err := n.load(); err != nil {
				t.Fatal(err)
			}
			insertAfter(t, n, tc.needle, tc.add)
			if err := n.flush(); err != nil {
				t.Fatal(err)
			}
			got := fileText(t, n)
			oneLineDiff(t, tc.src, got, tc.add)
			if strings.Contains(got, `\!`) || strings.Contains(got, `\<`) {
				t.Fatalf("Markdown escapes leaked into the file: %q", got)
			}
		})
	}
}

// TestNoteBlockedSaveKeepsTheFile: an edit that cannot be read back as the same
// text must not be written; the window turns read-only and explains.
func TestNoteBlockedSaveKeepsTheFile(t *testing.T) {
	a, n := noteTestShelf(t)
	src := "买菜\n"
	if err := n.store.Write(src); err != nil {
		t.Fatal(err)
	}
	if err := n.load(); err != nil {
		t.Fatal(err)
	}
	// A lone fence typed as body text would read back as an empty code block.
	n.doc.SetSelection(len([]rune(n.doc.Text().String())), len([]rune(n.doc.Text().String())))
	n.doc.InsertText("\n```")
	if err := n.flush(); err != nil {
		t.Fatal(err)
	}
	if !n.blocked {
		t.Fatal("an unrepresentable edit did not block saving")
	}
	if got := fileText(t, n); got != src {
		t.Fatalf("blocked save changed the file: %q", got)
	}
	view := ui.NewTester(a.noteView, noteW, noteH)
	view.Frame()
	view.Frame()
	if !view.HasText(locales.In("").NoteSaveBlocked()) {
		t.Fatalf("blocked note shows no notice; texts=%v", view.Texts())
	}
}

// TestNotePasteTextBeatsImage: with text and an image on the clipboard, paste
// inserts the text instead of turning it into a picture.
func TestNotePasteTextBeatsImage(t *testing.T) {
	a, n := noteTestShelf(t)
	origRead := noteReadClipboardImage
	t.Cleanup(func() { noteReadClipboardImage = origRead })
	pngBytes := makeNoteTestPNG(120, 80, color.RGBA{R: 40, G: 80, B: 160, A: 255}, color.RGBA{R: 10, G: 10, B: 10, A: 255})
	noteReadClipboardImage = func() []byte { return pngBytes }

	view := ui.NewTester(a.noteView, noteW, noteH)
	view.Frame()
	view.Frame()
	view.SetClipboard("从表格复制的文字")
	view.Command("paste")
	view.Frame()
	if got := n.doc.Text().PlainText(); !strings.Contains(got, "从表格复制的文字") {
		t.Fatalf("pasted text missing: %q", got)
	}
	if len(countAttachments(t, n.doc)) != 0 {
		t.Fatal("text+image clipboard was pasted as an image")
	}
}

func saveReworkShot(t *testing.T, view *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("RAKKO_REWORK_SEG2_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, view.Image()); err != nil {
		t.Fatal(err)
	}
}

// TestNoteLinesRenderAsLinesAndPadded screenshots an old line-based note and a
// note with an inline image so the line breaks and 12pt side padding are
// visible.
func TestNoteLinesRenderAsLinesAndPadded(t *testing.T) {
	a, n := noteTestShelf(t)
	src := "买菜\n牛奶\n鸡蛋\n"
	if err := n.store.Write(src); err != nil {
		t.Fatal(err)
	}
	if err := n.load(); err != nil {
		t.Fatal(err)
	}
	view := ui.NewTester(a.noteView, noteW, noteH)
	view.Frame()
	view.Frame()
	if got := n.doc.Text().PlainText(); got != "买菜\n牛奶\n鸡蛋" {
		t.Fatalf("loaded text = %q, want three lines", got)
	}
	if got := n.doc.Text().ParagraphCount(); got != 3 {
		t.Fatalf("paragraphs = %d, want 3 separate lines", got)
	}
	saveReworkShot(t, view, "01-note-lines.png")

	pngBytes := makeNoteTestPNG(480, 200, color.RGBA{R: 56, G: 132, B: 244, A: 255}, color.RGBA{R: 138, G: 72, B: 230, A: 255})
	if !n.insertImageBytes(pngBytes, "png", "示意图") {
		t.Fatal("image insert failed")
	}
	view.Frame()
	view.Frame()
	saveReworkShot(t, view, "02-note-image-padding.png")
}

// TestNoteHeadingCachedByVersion: the bar's heading is recomputed only when the
// document version changes, not on every frame.
func TestNoteHeadingCachedByVersion(t *testing.T) {
	_, n := noteTestShelf(t)
	n.setDraft("# 新标题")
	if got := n.headingText(); !strings.Contains(got, "新标题") {
		t.Fatalf("heading = %q", got)
	}
	ver := n.doc.Version()
	if n.titleVer != ver {
		t.Fatalf("title cached at version %d, want %d", n.titleVer, ver)
	}
	n.title = "缓存哨兵"
	if got := n.headingText(); got != "缓存哨兵" {
		t.Fatalf("heading recomputed without a version change: %q", got)
	}
	n.doc.SetSelection(0, 0)
	n.doc.InsertText("改")
	if got := n.headingText(); got == "缓存哨兵" {
		t.Fatal("heading did not follow a new document version")
	}
}
