package platform

import (
	"errors"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// DecodeGB18030 decodes b as GB18030 (GBK superset), reporting whether every
// byte decoded cleanly without replacement characters.
func DecodeGB18030(b []byte) (s string, ok bool) {
	out, _, err := transform.String(simplifiedchinese.GB18030.NewDecoder(), string(b))
	if err != nil && !errors.Is(err, transform.ErrShortSrc) {
		return "", false
	}
	if strings.ContainsRune(out, '\uFFFD') {
		return "", false
	}
	return out, true
}

// NormalizeText applies Unicode compatibility composition (NFKC) so CJK
// compatibility and Kangxi radical code points become ordinary ideographs.
func NormalizeText(s string) string {
	return norm.NFKC.String(s)
}

// pdfLines splits extracted page text into lines: every line-break form counts
// as a break, trailing whitespace (including NBSP) is trimmed, and blank lines
// at page edges are dropped while inner empty lines survive as "".
func pdfLines(raw string) []string {
	if raw == "" {
		return nil
	}
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\v', '\f', '\u0085', '\u2028', '\u2029':
			return '\n'
		}
		return r
	}, s)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\u00a0")
	}
	lo, hi := 0, len(lines)
	for lo < hi && lines[lo] == "" {
		lo++
	}
	for hi > lo && lines[hi-1] == "" {
		hi--
	}
	if lo >= hi {
		return nil
	}
	return lines[lo:hi]
}
