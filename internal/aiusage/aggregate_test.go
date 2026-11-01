package aiusage

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func claudeLine(id, ts string, in, out int64) string {
	return `{"type":"assistant","uuid":"u-` + id + `","timestamp":"` + ts + `","message":{"id":"` + id +
		`","model":"claude-sonnet-4-5","usage":{"input_tokens":` + itoa64(in) + `,"output_tokens":` + itoa64(out) + `}}}`
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

func scanOne(t *testing.T, lines ...string) *Scanner {
	t.Helper()
	home := t.TempDir()
	writeLines(t, filepath.Join(home, ".claude", "projects", "-p", "s.jsonl"), lines...)
	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTodayMidnightBoundary(t *testing.T) {
	s := scanOne(t,
		claudeLine("m-prev", "2026-10-18T23:59:59.999Z", 11, 1),
		claudeLine("m-today", "2026-10-19T00:00:00.000Z", 22, 2),
	)
	now := time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)

	today := s.Summary(RangeToday, now)
	if today.Total.Requests != 1 || today.Total.Input != 22 {
		t.Fatalf("today totals wrong: %+v", today.Total)
	}
	if got := today.Buckets[0].Tokens[ToolClaude]; got != 24 {
		t.Fatalf("bucket0 = %d, want 24", got)
	}
	if today.Buckets[23].Start.Hour() != 23 || today.Buckets[0].Start.Hour() != 0 {
		t.Fatal("bucket hours wrong")
	}

	week := s.Summary(Range7d, now)
	if week.Total.Requests != 2 || week.Total.Input != 33 {
		t.Fatalf("7d totals wrong: %+v", week.Total)
	}
	if len(week.Buckets) != 7 {
		t.Fatalf("7d buckets = %d", len(week.Buckets))
	}
	if got := week.Buckets[5].Tokens[ToolClaude]; got != 12 {
		t.Fatalf("yesterday bucket = %d, want 12", got)
	}
	if got := week.Buckets[6].Tokens[ToolClaude]; got != 24 {
		t.Fatalf("today bucket = %d, want 24", got)
	}
	if week.Buckets[6].Start.Day() != 19 || week.Buckets[6].Start.Month() != 10 {
		t.Fatal("last bucket is not today")
	}
}

func TestLocalZoneBoundaries(t *testing.T) {
	s := scanOne(t, claudeLine("m1", "2026-10-19T02:00:00.000Z", 10, 1))
	// Same UTC instant in UTC+8 lands at 10:00 local on the 19th.
	now := time.Date(2026, 10, 19, 20, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	sum := s.Summary(RangeToday, now)
	if sum.Total.Requests != 1 {
		t.Fatalf("requests = %d", sum.Total.Requests)
	}
	if got := sum.Buckets[10].Tokens[ToolClaude]; got != 11 {
		t.Fatalf("bucket10 local = %d, want 11", got)
	}
	// In UTC the same record sits at 02:00.
	nowUTC := time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)
	sumUTC := s.Summary(RangeToday, nowUTC)
	if got := sumUTC.Buckets[2].Tokens[ToolClaude]; got != 11 {
		t.Fatalf("bucket2 utc = %d, want 11", got)
	}
}

func TestRange30dAndAll(t *testing.T) {
	s := scanOne(t,
		claudeLine("m-old-29", "2026-09-20T10:00:00.000Z", 7, 1),    // exactly 29 days back: inside 30d
		claudeLine("m-old-31", "2026-09-18T10:00:00.000Z", 5, 1),    // 31 days back: outside 30d
		claudeLine("m-ancient", "2025-09-20T10:00:00.000Z", 100, 1), // >365d back
		claudeLine("m-now", "2026-10-19T10:00:00.000Z", 3, 1),
	)
	now := time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)

	m30 := s.Summary(Range30d, now)
	if len(m30.Buckets) != 30 {
		t.Fatalf("30d buckets = %d", len(m30.Buckets))
	}
	if m30.Total.Requests != 2 || m30.Total.Input != 10 {
		t.Fatalf("30d totals wrong: %+v", m30.Total)
	}
	if m30.Buckets[29].Start.Day() != 19 {
		t.Fatal("last 30d bucket is not today")
	}

	all := s.Summary(RangeAll, now)
	if all.Total.Requests != 4 {
		t.Fatalf("all requests = %d, want 4 (totals are all-time)", all.Total.Requests)
	}
	if all.Total.Input != 115 {
		t.Fatalf("all input = %d, want 115", all.Total.Input)
	}
	if len(all.Buckets) != 365 {
		t.Fatalf("all buckets = %d, want capped 365", len(all.Buckets))
	}
	last := all.Buckets[len(all.Buckets)-1]
	if last.Start.Day() != 19 || last.Start.Month() != 10 || last.Start.Year() != 2026 {
		t.Fatalf("last all bucket = %v, want 2026-10-19", last.Start)
	}
}

func TestRangeAllEmpty(t *testing.T) {
	s := scanOne(t)
	sum := s.Summary(RangeAll, fixtureNow())
	if sum.Total.Requests != 0 || len(sum.Buckets) != 0 {
		t.Fatalf("empty all: %+v buckets=%d", sum.Total, len(sum.Buckets))
	}
}

// RangeAll buckets are civil days in now's zone: a record dated Oct 19 in
// UTC+8 must land in the Oct 19 bucket even though its UTC date is Oct 18.
func TestRangeAllLocalDays(t *testing.T) {
	s := scanOne(t, claudeLine("tz1", "2026-10-18T17:00:00.000Z", 11, 1)) // Oct 19 01:00 in UTC+8
	loc := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 10, 19, 5, 0, 0, 0, loc)

	all := s.Summary(RangeAll, now)
	if all.Total.Requests != 1 {
		t.Fatalf("requests = %d", all.Total.Requests)
	}
	if len(all.Buckets) != 1 {
		t.Fatalf("buckets = %d, want 1 (the local Oct 19 day)", len(all.Buckets))
	}
	if got := all.Buckets[0].Tokens[ToolClaude]; got != 12 {
		t.Fatalf("record missing from its local-day bucket: tokens=%d", got)
	}
	y, m, d := all.Buckets[0].Start.Date()
	if y != 2026 || m != 10 || d != 19 {
		t.Fatalf("bucket starts %v, want local 2026-10-19", all.Buckets[0].Start)
	}
}

// Today's end boundary is the next local midnight, not midnight+24h: on a
// fall-back day (25 wall hours) the last wall hour still counts; on a
// spring-forward day (23 wall hours) the next day leaks in for an hour.
func TestTodayDSTBoundaries(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdb:", err)
	}

	// Fall back 2026-11-01: the civil day spans 25 absolute hours, so the
	// window [00:00 EDT, 00:00 EST) ends one hour after midnight+24h.
	s := scanOne(t,
		claudeLine("fall-late", "2026-11-02T04:30:00.000Z", 7, 1), // 23:30 EST Nov 1 — the extra hour
		claudeLine("fall-next", "2026-11-02T05:30:00.000Z", 4, 1), // 00:30 EST Nov 2 — the next day
	)
	now := time.Date(2026, 11, 1, 23, 45, 0, 0, ny)
	today := s.Summary(RangeToday, now)
	if today.Total.Requests != 1 || today.Total.Input != 7 {
		t.Fatalf("fall-back totals wrong: %+v (the extra hour must count)", today.Total)
	}
	if len(today.Buckets) != 24 {
		t.Fatalf("today buckets = %d", len(today.Buckets))
	}
	var lastBucketTokens int64
	for _, v := range today.Buckets[23].Tokens {
		lastBucketTokens += v
	}
	if lastBucketTokens != 8 {
		t.Fatalf("the extra hour lands in the last display bucket: %d", lastBucketTokens)
	}

	// Spring forward 2026-03-08: the day spans 23 absolute hours; records
	// past local midnight must stay out even though midnight+24h would
	// still include them.
	s3 := scanOne(t,
		claudeLine("in", "2026-03-09T01:30:00.000Z", 9, 1),  // 20:30 EST Mar 8 — inside
		claudeLine("out", "2026-03-09T05:30:00.000Z", 4, 1), // 00:30 EST Mar 9 — the next day
	)
	t3 := s3.Summary(RangeToday, time.Date(2026, 3, 8, 23, 0, 0, 0, ny))
	if t3.Total.Requests != 1 || t3.Total.Input != 9 {
		t.Fatalf("spring-forward must end at local midnight: %+v", t3.Total)
	}
}

func TestSummaryConcurrentWithScan(t *testing.T) {
	home := t.TempDir()
	writeLines(t, filepath.Join(home, ".claude", "projects", "-p", "s.jsonl"),
		claudeLine("m1", "2026-10-19T10:00:00.000Z", 5, 1))
	s := NewScanner(home, t.TempDir())

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_ = s.Summary(RangeToday, fixtureNow())
				_ = s.Summary(RangeAll, fixtureNow())
			}
		}()
	}
	for range 10 {
		_ = s.Scan(context.Background(), nil)
	}
	wg.Wait()
}
