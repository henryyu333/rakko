package doc

import "strings"

// Fractional indexing keeps ordered strings between any two existing keys
// using base-62 digits ("0-9A-Za-z") with a length-prefixed integer part
// ('a'..'z' upward, 'Z'..'A' downward).

const indexDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// FirstIndex is the initial fractional index for a first item.
const FirstIndex = "a1"

func intPartLen(head byte) int {
	switch {
	case head >= 'a' && head <= 'z':
		return int(head-'a') + 2
	case head >= 'A' && head <= 'Z':
		return int('Z'-head) + 2
	}
	return 0
}

func splitIntPart(key string) (intPart, fracPart string) {
	if key == "" {
		return "", ""
	}
	n := intPartLen(key[0])
	if n == 0 || n > len(key) {
		return key, ""
	}
	return key[:n], key[n:]
}

func incIntPart(x string) string {
	b := []byte(x)
	head := b[0]
	body := b[1:]
	carry := true
	for i := len(body) - 1; carry && i >= 0; i-- {
		pos := strings.IndexByte(indexDigits, body[i]) + 1
		if pos == len(indexDigits) {
			body[i] = '0'
		} else {
			body[i] = indexDigits[pos]
			carry = false
		}
	}
	if !carry {
		return string(b)
	}
	if head == 'Z' {
		return "a0"
	}
	if head == 'z' {
		return ""
	}
	head++
	if head > 'a' {
		body = append(body, '0')
	} else if len(body) > 0 {
		body = body[:len(body)-1]
	}
	return string(head) + string(body)
}

func decIntPart(x string) string {
	b := []byte(x)
	head := b[0]
	body := b[1:]
	borrow := true
	for i := len(body) - 1; borrow && i >= 0; i-- {
		pos := strings.IndexByte(indexDigits, body[i]) - 1
		if pos < 0 {
			body[i] = indexDigits[len(indexDigits)-1]
		} else {
			body[i] = indexDigits[pos]
			borrow = false
		}
	}
	if !borrow {
		return string(b)
	}
	if head == 'a' {
		return "Z" + string(indexDigits[len(indexDigits)-1])
	}
	if head == 'A' {
		return ""
	}
	head--
	if head < 'Z' {
		body = append(body, indexDigits[len(indexDigits)-1])
	} else if len(body) > 0 {
		body = body[:len(body)-1]
	}
	return string(head) + string(body)
}

func midpoint(a, b string) string {
	if b != "" && a >= b {
		return a + "V"
	}
	if b != "" {
		n := 0
		for n < len(b) {
			ca := byte('0')
			if n < len(a) {
				ca = a[n]
			}
			if ca != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			restA := ""
			if n < len(a) {
				restA = a[n:]
			}
			return b[:n] + midpoint(restA, b[n:])
		}
	}
	da := 0
	if a != "" {
		da = strings.IndexByte(indexDigits, a[0])
	}
	db := len(indexDigits)
	if b != "" {
		db = strings.IndexByte(indexDigits, b[0])
	}
	if db-da > 1 {
		return string(indexDigits[(da+db+1)/2])
	}
	if b != "" && len(b) > 1 {
		return b[:1]
	}
	restA := ""
	if len(a) > 1 {
		restA = a[1:]
	}
	return string(indexDigits[da]) + midpoint(restA, "")
}

// IndexBetween returns a fractional index key sorting lexicographically between a and b.
// Either bound may be empty to mean unbounded on that side.
func IndexBetween(a, b string) string {
	switch {
	case a == "" && b == "":
		return FirstIndex
	case a == "":
		ib, fb := splitIntPart(b)
		if ib == "A"+strings.Repeat("0", 26) {
			return ib + midpoint("", fb)
		}
		if ib < b {
			return ib
		}
		if d := decIntPart(ib); d != "" {
			return d
		}
		return ib + midpoint("", fb)
	case b == "":
		ia, fa := splitIntPart(a)
		if inc := incIntPart(ia); inc != "" {
			return inc
		}
		return ia + midpoint(fa, "")
	}
	ia, fa := splitIntPart(a)
	ib, fb := splitIntPart(b)
	if ia == ib {
		return ia + midpoint(fa, fb)
	}
	if inc := incIntPart(ia); inc != "" && inc < b {
		return inc
	}
	return ia + midpoint(fa, "")
}

// IndicesBetween returns n strictly increasing keys between a and b.
func IndicesBetween(a, b string, n int) []string {
	if n <= 0 {
		return nil
	}
	out := make([]string, 0, n)
	cur := a
	for i := 0; i < n; i++ {
		cur = IndexBetween(cur, b)
		out = append(out, cur)
	}
	return out
}
