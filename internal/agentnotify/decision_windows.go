//go:build windows

package agentnotify

// A Windows tool runs Rakko's client through cmd.exe or PowerShell, and the
// console hands a child's output to its parent in the console's code page.
// A user-typed deny reason is UTF-8; under a Chinese (936) or any other
// non-UTF-8 page the tool would read mojibake. \uXXXX escapes are ASCII, so
// the bytes survive every code page, and every JSON parser turns them back
// into the same text.

import (
	"fmt"
	"strings"
)

// platformDecision renders a decision as ASCII-only JSON on Windows.
func platformDecision(s string) string {
	if isASCII(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r <= 0xFFFF:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		}
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
