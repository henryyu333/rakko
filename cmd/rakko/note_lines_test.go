package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/richtext"
)

func layoutParas(t *testing.T, l *noteLayout) []richtext.Paragraph {
	t.Helper()
	var out []richtext.Paragraph
	for _, p := range l.text().Paragraphs() {
		out = append(out, p)
	}
	return out
}

// insertIntoPara appends s to the last run of paragraph i and commits.
func insertIntoPara(t *testing.T, src string, i int, s string) (string, bool) {
	t.Helper()
	l := newNoteLayout(src)
	ps := layoutParas(t, l)
	if i >= len(ps) {
		t.Fatalf("paragraph %d missing (have %d)", i, len(ps))
	}
	if len(ps[i].Runs) == 0 {
		ps[i].Runs = []richtext.Run{{Text: s}}
	} else {
		last := len(ps[i].Runs) - 1
		ps[i].Runs[last].Text += s
	}
	return l.commit(richtext.TextOf(ps...))
}

// oneLineDiff reports whether want and got differ on exactly one line and that
// line is want's line plus add.
func oneLineDiff(t *testing.T, want, got, add string) {
	t.Helper()
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	if len(wl) != len(gl) {
		t.Fatalf("line count %d -> %d\nwant %q\ngot  %q", len(wl), len(gl), want, got)
	}
	changed := 0
	for i := range wl {
		if wl[i] == gl[i] {
			continue
		}
		changed++
		if strings.Replace(gl[i], add, "", 1) != wl[i] {
			t.Fatalf("line %d = %q, want the original line plus %q\nwant whole %q\ngot whole  %q", i, gl[i], add, want, got)
		}
	}
	if changed != 1 {
		t.Fatalf("%d lines changed, want 1\nwant %q\ngot  %q", changed, want, got)
	}
}

func TestNoteLayoutKeepsPlainLines(t *testing.T) {
	src := "买菜\n牛奶\n鸡蛋\n"
	got, ok := insertIntoPara(t, src, 1, "和")
	if !ok {
		t.Fatal("commit rejected the edit")
	}
	oneLineDiff(t, src, got, "和")
}

func TestNoteLayoutKeepsProjectSmokeFixture(t *testing.T) {
	src := "# 周五发版前\n– 改 README 顶栏描述\n– 补 v1.13 截图\n– [ ] 桌宠姿态验收"
	got, ok := insertIntoPara(t, src, 2, "新")
	if !ok {
		t.Fatal("commit rejected the edit")
	}
	oneLineDiff(t, src, got, "新")
}

func TestNoteLayoutKeepsAngleBracketsAndCodeFence(t *testing.T) {
	src := "会议 <周三> 待定\n```bash\necho hi\n```\n"
	got, ok := insertIntoPara(t, src, 0, "改")
	if !ok {
		t.Fatal("commit rejected the edit")
	}
	oneLineDiff(t, src, got, "改")
	if !strings.Contains(got, "```bash") {
		t.Fatalf("code fence language lost: %q", got)
	}
	if !strings.Contains(got, "<周三>") {
		t.Fatalf("angle brackets lost: %q", got)
	}
}

func TestNoteLayoutKeepsLiteralMarkupLookingText(t *testing.T) {
	for _, src := range []string{"foo <bar> & baz\n", "a<b>c\n"} {
		got, ok := insertIntoPara(t, src, 0, "!")
		if !ok {
			t.Fatalf("%q: commit rejected", src)
		}
		oneLineDiff(t, src, got, "!")
		if strings.Contains(got, `\!`) || strings.Contains(got, "**") {
			t.Fatalf("%q rewritten with Markdown escapes: %q", src, got)
		}
	}
}

func TestNoteLayoutKeepsTableRows(t *testing.T) {
	src := "| 名称 | 数量 |\n| --- | --- |\n| 苹果 | 3 |\n| 香蕉 | 5 |\n"
	got, ok := insertIntoPara(t, src, 2, "根")
	if !ok {
		t.Fatal("commit rejected the edit")
	}
	oneLineDiff(t, src, got, "根")
}

func TestNoteLayoutKeepsCodeBlockLineUntouched(t *testing.T) {
	src := "# 标题\n```go\nfmt.Println(\"hi\")\n```\n尾行\n"
	got, ok := insertIntoPara(t, src, 0, "改")
	if !ok {
		t.Fatal("commit rejected the edit")
	}
	oneLineDiff(t, src, got, "改")

	// Editing inside the block keeps the fence and info string too.
	got2, ok := insertIntoPara(t, src, 1, "!")
	if !ok {
		t.Fatal("commit rejected the code edit")
	}
	if !strings.Contains(got2, "```go") || !strings.Contains(got2, "```\n尾行") {
		t.Fatalf("code fence damaged: %q", got2)
	}
	if !strings.Contains(got2, `fmt.Println("hi")!`) {
		t.Fatalf("code edit lost: %q", got2)
	}
}

func TestNoteLayoutRoundTripRejectsUnrepresentableText(t *testing.T) {
	// A line that is itself a closing fence cannot live inside that fenced
	// block: writing it changes the block's structure when read back.
	src := "```\na\n```\n"
	l := newNoteLayout(src)
	ps := layoutParas(t, l)
	ps[0].Runs = []richtext.Run{{Text: "```"}}
	if _, ok := l.commit(richtext.TextOf(ps...)); ok {
		t.Fatal("commit accepted a fence line inside a fenced block")
	}
}

// TestNoteLayoutPlainMarkdownLookingTextSaves covers R1: text that looks like
// Markdown but is a plain paragraph must save (escaped if needed) instead of
// tripping the round-trip guard.
func TestNoteLayoutPlainMarkdownLookingTextSaves(t *testing.T) {
	semantic := []string{
		"**重要**", "- 买菜", "1. 第一", "# 不是标题",
		"用 *星号* 强调", "    缩进四格", "3*4*5",
	}
	literal := []string{"!", "<周三>", `C:\temp`, "snake_case"}

	appendPlain := func(src, text string) (string, bool) {
		l := newNoteLayout(src)
		ps := layoutParas(t, l)
		ps = append(ps, richtext.Paragraph{Runs: []richtext.Run{{Text: text}}})
		return l.commit(richtext.TextOf(ps...))
	}

	for _, text := range semantic {
		out, ok := appendPlain("a\n", text)
		if !ok {
			t.Fatalf("%q: commit rejected plain Markdown-looking text", text)
		}
		ps := layoutParas(t, newNoteLayout(out))
		if len(ps) != 2 {
			t.Fatalf("%q: reread has %d paragraphs, want 2", text, len(ps))
		}
		if got := ps[1].Style; got != (richtext.ParagraphStyle{}) {
			t.Fatalf("%q: reread became %+v, want a plain paragraph", text, got)
		}
		if got := richtext.TextOf(ps[1]).String(); got != text {
			t.Fatalf("%q: reread text = %q", text, got)
		}
	}
	for _, text := range literal {
		out, ok := appendPlain("a\n", text)
		if !ok {
			t.Fatalf("%q: commit rejected literal text", text)
		}
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(lines) != 2 || lines[1] != text {
			t.Fatalf("%q: literal text was escaped: %q", text, out)
		}
	}
}

// TestNoteLayoutKeepsPrefixOnEditedLine covers R2: an edited line keeps its
// original indentation and block marker, so the diff is only the typed
// character.
func TestNoteLayoutKeepsPrefixOnEditedLine(t *testing.T) {
	cases := []string{
		"  - 子项",
		"    缩进代码",
		"\t制表缩进",
		"1. 一",
		"> 引用",
		"- [ ] 待办",
		"**粗** 普通",
	}
	for _, line := range cases {
		src := line + "\nb\n"
		l := newNoteLayout(src)
		ps := layoutParas(t, l)
		last := len(ps[0].Runs) - 1
		ps[0].Runs[last].Text += "X"
		out, ok := l.commit(richtext.TextOf(ps...))
		if !ok {
			t.Fatalf("%q: commit rejected the edit", line)
		}
		got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(got) != 2 || got[0] != line+"X" || got[1] != "b" {
			t.Fatalf("%q: saved %q, want %q", line, got, line+"X")
		}
	}
}

// paraTextEnd returns the rune offset just before paragraph i's newline.
func paraTextEnd(doc *richtext.Document, i int) int {
	off := 0
	for k, p := range doc.Text().Paragraphs() {
		if k == i {
			for _, r := range p.Runs {
				off += len([]rune(r.Text))
			}
			return off
		}
		for _, r := range p.Runs {
			off += len([]rune(r.Text))
		}
		off++
	}
	return off
}
