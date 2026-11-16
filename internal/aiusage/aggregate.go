package aiusage

import (
	"math"
	"slices"
	"sort"
	"strconv"
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
	emit := func(tool Tool, rc rec) {
		if rc.at < fromMs || rc.at >= toMs {
			return
		}
		addRecord(&summary.Total, rc)
		bt := summary.ByTool[tool]
		addRecord(&bt, rc)
		summary.ByTool[tool] = bt
		if i := idx(rc.at); i >= 0 && i < len(summary.Buckets) {
			summary.Buckets[i].Tokens[tool] += rc.tokens()
		}
	}

	seen := map[Tool]map[string]bool{}
	var copilotPaths []string
	for path, st := range s.files {
		if st == nil {
			continue
		}
		if st.tool == ToolCopilot {
			if !st.unsupportedSchema {
				copilotPaths = append(copilotPaths, path)
			}
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
			emit(st.tool, rc)
		}
	}
	if len(copilotPaths) > 0 {
		summarizeCopilot(s.files, copilotPaths, emit)
	}
	return summary
}

// summarizeCopilot resolves multi-source Copilot usage deterministically:
//  1. Source rank: session-store.db (1) > data.db (2) > OTEL JSONL (3).
//  2. Session ownership: for every non-empty session_id, only the minimum rank
//     with records for that session is kept; lower-rank sources for that
//     session are skipped completely.
//  3. Event & content deduplication: within the winning rank (or when
//     session_id is missing on a lower-rank record such as flat OTEL logs),
//     records deduplicate by unified event ID and by canonical content
//     signature `(session_id, at_ms, model, in, out, cr, cw)` with multiplicity
//     tracked per rank so genuine identical requests in one file stay intact
//     while cross-source copies collapse.
func summarizeCopilot(files map[string]*fileState, paths []string, emit func(Tool, rec)) {
	slices.SortFunc(paths, func(a, b string) int {
		ra, rb := copilotSourceRank(a), copilotSourceRank(b)
		if ra != rb {
			return ra - rb
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})

	minSessionRank := map[string]int{}
	for _, p := range paths {
		st := files[p]
		if st == nil || len(st.recs) == 0 {
			continue
		}
		rank := copilotSourceRank(p)
		for _, rc := range st.recs {
			sid, _, ok := parseCopilotRecKey(rc.key)
			if !ok || sid == "" {
				continue
			}
			if cur, exists := minSessionRank[sid]; !exists || rank < cur {
				minSessionRank[sid] = rank
			}
		}
	}

	seenEventID := map[string]bool{}
	// totalSigCounts tracks how many records with each anonymous content
	// signature `(at_ms, model, in, out, cr, cw)` have already been emitted
	// across higher-rank sources so sessionless lower-rank copies (e.g. OTEL
	// lines lacking session_id) do not double count DB events.
	emittedAnonSig := map[string]int{}

	for i := 0; i < len(paths); {
		rank := copilotSourceRank(paths[i])
		j := i + 1
		for j < len(paths) && copilotSourceRank(paths[j]) == rank {
			j++
		}

		rankAddedAnon := map[string]int{}
		rankSeenAnonOcc := map[string]bool{}
		rankSeenSessOcc := map[string]bool{}

		for _, p := range paths[i:j] {
			st := files[p]
			if st == nil {
				continue
			}
			fileAnonOcc := map[string]int{}
			fileSessOcc := map[string]int{}
			for _, rc := range st.recs {
				sid, evID, ok := parseCopilotRecKey(rc.key)
				if !ok {
					evID = rc.key
				}
				if sid != "" && minSessionRank[sid] < rank {
					continue
				}
				if evID != "" {
					if seenEventID[evID] {
						continue
					}
				}
				anonSig := copilotContentSig("", rc)
				if sid == "" {
					occ := fileAnonOcc[anonSig]
					fileAnonOcc[anonSig] = occ + 1
					if occ < emittedAnonSig[anonSig] {
						continue
					}
					if evID == "" {
						occKey := anonSig + "|" + strconv.Itoa(occ)
						if rankSeenAnonOcc[occKey] {
							continue
						}
						rankSeenAnonOcc[occKey] = true
					}
				} else if evID == "" {
					sSig := copilotContentSig(sid, rc)
					occ := fileSessOcc[sSig]
					fileSessOcc[sSig] = occ + 1
					occKey := sSig + "|" + strconv.Itoa(occ)
					if rankSeenSessOcc[occKey] {
						continue
					}
					rankSeenSessOcc[occKey] = true
				}

				if evID != "" {
					seenEventID[evID] = true
				}
				rankAddedAnon[anonSig]++
				emit(ToolCopilot, rc)
			}
		}
		for sig, n := range rankAddedAnon {
			emittedAnonSig[sig] += n
		}
		i = j
	}
}

func addRecord(t *Totals, rc rec) {
	t.Input += rc.in
	t.Output += rc.out
	t.CacheRead += rc.cr
	t.CacheWrite += rc.cw
	reqs := rc.reqs
	if reqs == zeroReqs {
		reqs = 0
	} else if reqs <= 0 {
		reqs = 1
	}
	t.Requests += reqs
	if rc.priced {
		t.Cost += rc.cost
	} else if rc.model != nil {
		if c, ok := estimateCost(*rc.model, rc.in, rc.out, rc.cr, rc.cw); ok {
			t.Cost += c
		} else {
			t.Unpriced += reqs
		}
	} else {
		t.Unpriced += reqs
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
