package aiusage

import (
	"math"
	"sort"
	"time"
)

// Summary buckets the scanner's records over the requested range using the
// time zone of `now` (pass time.Now() for the machine's local zone, or a
// timestamp in a fixed zone in tests).
//
//   - RangeToday: 24 hourly buckets starting at today's local midnight.
//   - Range7d / Range30d: daily buckets ending today (7 or 30 of them).
//   - RangeAll: daily buckets from the first record's day to the last
//     record's day (or today if later), capped at the 365 most recent
//     days; totals still cover all records, including ones older than the
//     bucket window.
//
// Records from sources this Scanner was not built to read never appear.
// Safe to call concurrently: it reads the snapshot committed by the last
// Scan under RLock (see Scanner's locking note).
func (s *Scanner) Summary(r Range, now time.Time) Summary {
	loc := now.Location()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	var bounds []time.Time
	fromMs, toMs := int64(math.MinInt64), int64(math.MaxInt64) // toMs exclusive

	switch r {
	case RangeToday:
		// 24 elapsed-hour buckets from local midnight to local midnight;
		// AddDate lands the end on the next midnight even when a DST
		// transition makes the civil day 23 or 25 absolute hours long.
		end := midnight.AddDate(0, 0, 1)
		bounds = make([]time.Time, 25)
		for i := range 24 {
			bounds[i] = midnight.Add(time.Duration(i) * time.Hour)
		}
		bounds[24] = end
		fromMs, toMs = bounds[0].UnixMilli(), end.UnixMilli()
	case Range7d, Range30d:
		ndays := 7
		if r == Range30d {
			ndays = 30
		}
		start := midnight.AddDate(0, 0, -(ndays - 1))
		bounds = make([]time.Time, ndays+1)
		for i := range ndays + 1 {
			bounds[i] = start.AddDate(0, 0, i)
		}
		fromMs, toMs = bounds[0].UnixMilli(), bounds[ndays].UnixMilli()
	case RangeAll:
		// bounds resolved below from the records themselves.
	default:
		return Summary{Range: r, ByTool: map[Tool]Totals{}}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if r == RangeAll {
		var firstMs, lastMs int64
		seenAny := false
		for _, st := range s.files {
			if st == nil {
				continue
			}
			for _, rc := range st.recs {
				if !seenAny || rc.at < firstMs {
					firstMs = rc.at
				}
				if !seenAny || rc.at > lastMs {
					lastMs = rc.at
				}
				seenAny = true
			}
		}
		if !seenAny {
			return Summary{Range: r, ByTool: map[Tool]Totals{}, Buckets: []Bucket{}}
		}
		firstDay, endDay := dayNo(firstMs, loc), dayNo(now.UnixMilli(), loc)
		if ld := dayNo(lastMs, loc); ld > endDay {
			endDay = ld // records timestamped in the future
		}
		nb := endDay - firstDay + 1
		startDay := firstDay
		if nb > 365 {
			startDay = endDay - 364
			nb = 365
		}
		bounds = make([]time.Time, nb+1)
		for i := range nb + 1 {
			bounds[i] = civilDay(startDay+int64(i), loc)
		}
	}

	return s.summarize(r, loc, bounds, fromMs, toMs)
}

// SummarySpan aggregates the records inside [from, to) into one daily bucket
// per civil day that touches the span, in the calendar of from's location.
// It is the arbitrary-range sibling of Summary: the 每周简报 asks for one
// Monday-to-Monday week, which none of the fixed ranges names. A bucket
// always starts at its day's local midnight; totals cover only the records
// inside the span, so a span that starts mid-day leaves the earlier part of
// that first day out. The range is empty when to is not after from.
//
// Records from sources this Scanner was not built to read never appear, the
// same dedupe rule as Summary applies, and the call is safe to make
// concurrently with Scan.
func (s *Scanner) SummarySpan(from, to time.Time) Summary {
	if !to.After(from) {
		return Summary{Range: RangeSpan, ByTool: map[Tool]Totals{}}
	}
	loc := from.Location()
	start := time.Date(from.In(loc).Year(), from.In(loc).Month(), from.In(loc).Day(), 0, 0, 0, 0, loc)
	end := time.Date(to.In(loc).Year(), to.In(loc).Month(), to.In(loc).Day(), 0, 0, 0, 0, loc)
	if !end.After(start) {
		// A span inside one civil day still gets that day's bucket.
		end = start.AddDate(0, 0, 1)
	} else if !end.Equal(to) {
		// A span that ends mid-day needs the bucket for that last day.
		end = end.AddDate(0, 0, 1)
	}

	bounds := make([]time.Time, 0, 8)
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		bounds = append(bounds, d)
	}
	bounds = append(bounds, end)

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summarize(RangeSpan, loc, bounds, from.UnixMilli(), to.UnixMilli())
}

// summarize aggregates the snapshot's records into the given civil
// boundaries, keeping totals and per-tool totals over [fromMs, toMs) while
// bucket starts stay the boundaries' local midnights. Callers hold mu.
func (s *Scanner) summarize(r Range, loc *time.Location, bounds []time.Time, fromMs, toMs int64) Summary {
	summary := Summary{
		Range:   r,
		ByTool:  map[Tool]Totals{},
		Buckets: make([]Bucket, len(bounds)-1),
	}
	for i := range summary.Buckets {
		summary.Buckets[i] = Bucket{Start: bounds[i], Tokens: map[Tool]int64{}}
	}

	// idx = last boundary ≤ at; bounds are monotonic.
	idx := func(atMs int64) int {
		return sort.Search(len(bounds), func(i int) bool {
			return bounds[i].UnixMilli() > atMs
		}) - 1
	}

	// Cross-file and intra-file dedupe lives here (not at parse time) so a
	// duplicate record survives in every file that holds it: whichever copy
	// shows up first wins, and deleting the "winner" can't erase the usage.
	seen := map[Tool]map[string]bool{}
	for _, st := range s.files {
		if st == nil {
			continue
		}
		keys := seen[st.tool]
		if keys == nil {
			keys = map[string]bool{}
			seen[st.tool] = keys
		}
		for _, rc := range st.recs {
			if rc.key != "" {
				if keys[rc.key] {
					continue
				}
				keys[rc.key] = true
			}
			if rc.at < fromMs || rc.at >= toMs {
				continue
			}
			addRecord(&summary.Total, rc)
			bt := summary.ByTool[st.tool]
			addRecord(&bt, rc)
			summary.ByTool[st.tool] = bt
			if i := idx(rc.at); i >= 0 && i < len(summary.Buckets) {
				summary.Buckets[i].Tokens[st.tool] += rc.tokens()
			}
		}
	}
	return summary
}

func addRecord(t *Totals, rc rec) {
	t.Input += rc.in
	t.Output += rc.out
	t.CacheRead += rc.cr
	t.CacheWrite += rc.cw
	t.Requests++
	if rc.priced {
		t.Cost += rc.cost
	} else {
		t.Unpriced++
	}
}

// dayNo returns the day number of a Unix-millis timestamp in loc's calendar
// — the same numbering civilDay uses, so a record always lands inside the
// bucket built from its local date.
func dayNo(ms int64, loc *time.Location) int64 {
	y, m, d := time.UnixMilli(ms).In(loc).Date()
	return floorDayMs(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).UnixMilli())
}

// floorDayMs returns the UTC day number of a Unix-millis timestamp — civil
// date arithmetic that stays consistent across DST shifts.
func floorDayMs(ms int64) int64 {
	const dayMs = int64(24 * 60 * 60 * 1000)
	d := ms / dayMs
	if ms < 0 && ms%dayMs != 0 {
		d--
	}
	return d
}

// civilDay converts a day number back to local midnight in loc.
func civilDay(day int64, loc *time.Location) time.Time {
	y, m, d := time.UnixMilli(day * 24 * 60 * 60 * 1000).In(time.UTC).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}
