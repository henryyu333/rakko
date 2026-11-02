// Package lyrics reads synced lyrics (LRC) for the home page: a parser for
// the LRC text LRCLIB returns, a client for its public API, and a small
// on-disk cache keyed by track, so each song is asked for once.
package lyrics

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Line is one timed lyric line.
type Line struct {
	At   time.Duration
	Text string
}

var (
	// [mm:ss], [mm:ss.xx], [mm:ss.xxx]; a colon before the fraction is seen
	// in the wild too. Minutes may pass 99 in long files.
	lrcTime = regexp.MustCompile(`\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)
	// [offset:+123] / [offset:-123], in milliseconds.
	lrcOffset = regexp.MustCompile(`\[offset:([+-]?\d+)\]`)
)

// ParseLRC reads LRC text into timed lines, sorted by time: several
// timestamps on one line each produce a line with the same words, the
// global [offset:] shifts every line, and empty or untimed lines are
// dropped. A line whose offset would move it before zero is clamped there.
func ParseLRC(src string) []Line {
	offset := time.Duration(0)
	if m := lrcOffset.FindStringSubmatch(src); m != nil {
		if ms, err := strconv.Atoi(m[1]); err == nil {
			offset = time.Duration(ms) * time.Millisecond
		}
	}
	var out []Line
	for _, raw := range strings.Split(src, "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		tags := lrcTime.FindAllStringSubmatchIndex(raw, -1)
		if len(tags) == 0 {
			continue
		}
		text := strings.TrimSpace(raw[tags[len(tags)-1][1]:])
		if text == "" {
			continue
		}
		for _, tag := range tags {
			at := lrcStamp(raw[tag[2]:tag[3]], raw[tag[4]:tag[5]], fracOf(raw, tag))
			at += offset
			if at < 0 {
				at = 0
			}
			out = append(out, Line{At: at, Text: text})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// fracOf is the fraction group of one timestamp match ("" when absent).
func fracOf(s string, tag []int) string {
	if tag[6] < 0 {
		return ""
	}
	return s[tag[6]:tag[7]]
}

// lrcStamp is one [mm:ss.frac] in milliseconds. A one- or two-digit
// fraction is hundredths (.5 = 500ms, .50 = 500ms); three digits are
// milliseconds.
func lrcStamp(min, sec, frac string) time.Duration {
	m, _ := strconv.Atoi(min)
	s, _ := strconv.Atoi(sec)
	var ms int
	switch len(frac) {
	case 1:
		ms, _ = strconv.Atoi(frac)
		ms *= 100
	case 2:
		ms, _ = strconv.Atoi(frac)
		ms *= 10
	case 3:
		ms, _ = strconv.Atoi(frac)
	}
	return time.Duration(m)*time.Minute + time.Duration(s)*time.Second + time.Duration(ms)*time.Millisecond
}

// LineAt is the index of the line being sung at pos: the last line that
// starts at or before it, or 0 before the first line. -1 when there are no
// lines.
func LineAt(lines []Line, pos time.Duration) int {
	if len(lines) == 0 {
		return -1
	}
	i := sort.Search(len(lines), func(i int) bool { return lines[i].At > pos }) - 1
	return max(i, 0)
}
