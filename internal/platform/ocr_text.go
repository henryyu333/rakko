package platform

// The OCR text cleanup, shared by every platform that can test it even
// though only the Windows engine needs it: Windows.Media.Ocr shapes one
// word per glyph for text written without word spaces, so a Chinese line
// comes back as 「框 选 文 字」 while the Mac build returns 「框选文字」.
// The compaction below puts those words back together. Korean is written
// WITH word spaces, so hangul keeps the engine's spaces; only Han and
// kana join.

// ocrCompactCJK removes the spaces the engine puts between Chinese and
// Japanese characters: its words are single ideographs. The same spaces
// surround CJK punctuation — 「你好 ， 世界 ！」 — so a space goes when the
// characters on both sides are written without word spaces, or when a
// curly quote meets one of them. Spaces inside Latin words, between
// Latin words, around numbers, and around the fullwidth forms of Latin
// letters and digits stay as the engine shaped them.
func ocrCompactCJK(s string) string {
	runes := []rune(s)
	out := make([]rune, 0, len(runes))
	for i, r := range runes {
		if r == ' ' && i > 0 && i+1 < len(runes) && joinsWithoutSpace(runes[i-1], runes[i+1]) {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// joinsWithoutSpace reports whether a and b are written against each other
// with no word space between them: two characters the language writes
// without spaces, or a curly quote against one of them. A quote keeps the
// space it has against Latin text or another quote, so 「“ A ” “ B ”」 does
// not collapse into 「“ A ”“ B ”」.
func joinsWithoutSpace(a, b rune) bool {
	if isCJKUnspaced(a) && isCJKUnspaced(b) {
		return true
	}
	return (isCurlyQuote(a) && isCJKIdeograph(b)) || (isCJKIdeograph(a) && isCurlyQuote(b))
}

// isCurlyQuote reports whether r is one of the curly quotes the engine
// spaces per glyph.
func isCurlyQuote(r rune) bool {
	switch r {
	case 0x2018, 0x2019, 0x201C, 0x201D:
		return true
	}
	return false
}

// isCJKUnspaced reports whether r joins its neighbours without a word
// space: a Han or kana character, or the punctuation written with them.
func isCJKUnspaced(r rune) bool {
	if isCJKIdeograph(r) {
		return true
	}
	switch {
	case r >= 0x3000 && r <= 0x303F: // CJK symbols and punctuation: 、。「」【】
		return true
	case r >= 0xFE10 && r <= 0xFE1F: // vertical forms
		return true
	case r >= 0xFE30 && r <= 0xFE4F: // CJK compatibility forms
		return true
	case r >= 0xFE50 && r <= 0xFE6F: // small form variants
		return true
	// Fullwidth punctuation only: ，！？：；（）［］｛｝ and its kin. The
	// fullwidth Latin letters and digits around them — ＡＢＣ, １２３ —
	// are words of their own and keep their spaces.
	case r >= 0xFF01 && r <= 0xFF0F,
		r >= 0xFF1A && r <= 0xFF20,
		r >= 0xFF3B && r <= 0xFF40,
		r >= 0xFF5B && r <= 0xFF65:
		return true
	}
	switch r {
	case 0x2014, 0x2026:
		// Em dash and ellipsis join CJK text the same way they join Latin
		// text, but the engine spaces them per glyph.
		return true
	}
	return false
}

// isCJKIdeograph reports whether r is written without word spaces: Han
// and kana. Hangul is deliberately NOT here: Korean writes word spaces,
// so the spaces the engine puts between hangul syllables are real words
// and stay.
func isCJKIdeograph(r rune) bool {
	switch {
	case r >= 0x3040 && r <= 0x30FF: // kana
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK extension A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK unified ideographs
		return true
	case r >= 0xF900 && r <= 0xFAFF: // compatibility ideographs
		return true
	case r >= 0x20000 && r <= 0x2FA1F: // extensions B and up
		return true
	}
	return false
}
