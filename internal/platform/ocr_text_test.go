package platform

// The OCR compaction (ocr_text.go), as the Windows engine's output asks
// for it: Chinese and Japanese join, Korean keeps the word spaces its
// writing depends on, Latin and fullwidth words keep every space the
// engine shaped.

import "testing"

func TestOCRCompactionKeepsLatinSpacing(t *testing.T) {
	cases := []struct{ in, want string }{
		{"框 选 文 字", "框选文字"},
		{"Rakko 框 选", "Rakko 框选"},
		{"ra kko 123", "ra kko 123"},
		{"選 択 肢", "選択肢"},
		// Korean writes word spaces: the engine's spaces between hangul
		// syllables are real words and stay.
		{"가 나 다", "가 나 다"},
		{"안녕 하세요", "안녕 하세요"},
		{"한 국 中 文", "한 국 中文"},
		{"你 好 ， 世 界 ！", "你好，世界！"},
		{"框 选 、 文 字 。", "框选、文字。"},
		{"「 引 号 」 （ 括 号 ）", "「引号」（括号）"},
		{"第 一 — 第 二 … 完", "第一—第二…完"},
		{"“ 引 用 ”", "“引用”"},
		{"Hello ， world", "Hello ， world"},
		{"Rakko 更 新 v1.19.0 已 就 绪", "Rakko 更新 v1.19.0 已就绪"},
		// Fullwidth Latin letters and digits are words of their own: the
		// engine's spaces around them stay.
		{"Ａ Ｂ Ｃ", "Ａ Ｂ Ｃ"},
		{"１ ２ ３", "１ ２ ３"},
		{"中 Ａ 文", "中 Ａ 文"},
		// A curly quote joins Han text and keeps its space against Latin
		// text and against another quote.
		{"“ A ” “ B ”", "“ A ” “ B ”"},
		{"他 说 “ 好 ” 了", "他说“好”了"},
	}
	for _, c := range cases {
		if got := ocrCompactCJK(c.in); got != c.want {
			t.Errorf("ocrCompactCJK(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
