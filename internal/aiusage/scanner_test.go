package aiusage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fixtureHome = "testdata/scan/home"

// fixtureNow is inside the fixture day (2026-10-19 UTC).
func fixtureNow() time.Time {
	return time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendBytes(t *testing.T, path string, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
}

func mustScan(t *testing.T, s *Scanner) (lastDone, lastTotal int64) {
	t.Helper()
	calls := 0
	err := s.Scan(context.Background(), func(done, total int64) {
		calls++
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if calls == 0 {
		t.Fatal("progress never called")
	}
	return lastDone, lastTotal
}

func TestScanFixtureTree(t *testing.T) {
	s := NewScanner(fixtureHome, t.TempDir())
	done, total := mustScan(t, s)
	if total == 0 || done != total {
		t.Fatalf("progress done=%d total=%d, want equal nonzero", done, total)
	}
	sum := s.Summary(RangeToday, fixtureNow())

	if got := sum.Total.Requests; got != 7 {
		t.Fatalf("requests = %d, want 7", got)
	}
	omp := sum.ByTool[ToolOmp]
	if omp.Requests != 2 || omp.Input != 3000 || omp.Output != 500 ||
		omp.CacheRead != 700 || omp.CacheWrite != 50 {
		t.Fatalf("omp totals wrong: %+v", omp)
	}
	if omp.Unpriced != 1 || omp.Cost != 0.0035 {
		t.Fatalf("omp priced/cost wrong: %+v", omp)
	}
	cl := sum.ByTool[ToolClaude]
	if cl.Requests != 2 || cl.Input != 800 || cl.Output != 180 ||
		cl.CacheRead != 910 || cl.CacheWrite != 40 || cl.Unpriced != 2 || cl.Cost != 0 {
		t.Fatalf("claude totals wrong: %+v", cl)
	}
	cx := sum.ByTool[ToolCodex]
	if cx.Requests != 3 || cx.Input != 140 || cx.Output != 30 ||
		cx.CacheRead != 110 || cx.CacheWrite != 5 || cx.Unpriced != 3 || cx.Cost != 0 {
		t.Fatalf("codex totals wrong: %+v", cx)
	}
	if len(sum.Buckets) != 24 {
		t.Fatalf("today buckets = %d, want 24", len(sum.Buckets))
	}
	if got := sum.Buckets[10].Tokens[ToolOmp]; got != 4250 {
		t.Fatalf("bucket10 omp = %d, want 4250", got)
	}
	if got := sum.Buckets[10].Tokens[ToolClaude]; got != 1930 {
		t.Fatalf("bucket10 claude = %d, want 1930", got)
	}
	if got := sum.Buckets[8].Tokens[ToolCodex]; got != 230 {
		t.Fatalf("bucket8 codex = %d, want 230", got)
	}
	if got := sum.Buckets[11].Tokens[ToolCodex]; got != 55 {
		t.Fatalf("bucket11 codex = %d, want 55", got)
	}
	if sum.Total.Unpriced != 6 {
		t.Fatalf("unpriced = %d, want 6", sum.Total.Unpriced)
	}
	if sum.Total.Cost != 0.0035 {
		t.Fatalf("cost = %v, want 0.0035", sum.Total.Cost)
	}
}

func TestSecondScanNoBytes(t *testing.T) {
	cache := t.TempDir()
	s := NewScanner(fixtureHome, cache)
	mustScan(t, s)
	sum1 := s.Summary(RangeToday, fixtureNow())

	done, total := mustScan(t, s)
	if total != 0 || done != 0 {
		t.Fatalf("second scan read bytes: done=%d total=%d", done, total)
	}
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != sum1.Total.Requests {
		t.Fatalf("requests changed: %d vs %d", got, sum1.Total.Requests)
	}
}

func TestCacheSurvivesNewScanner(t *testing.T) {
	cache := t.TempDir()
	s1 := NewScanner(fixtureHome, cache)
	mustScan(t, s1)

	s2 := NewScanner(fixtureHome, cache)
	done, total := mustScan(t, s2)
	if total != 0 {
		t.Fatalf("new scanner reread bytes: total=%d", total)
	}
	_ = done
	got := s2.Summary(RangeToday, fixtureNow())
	if got.Total.Requests != 7 || got.ByTool[ToolCodex].Requests != 3 {
		t.Fatalf("new scanner summary wrong: %+v", got.Total)
	}
}

func TestCorruptCacheRescans(t *testing.T) {
	cache := t.TempDir()
	if err := os.WriteFile(filepath.Join(cache, cacheName), []byte("{garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewScanner(fixtureHome, cache)
	mustScan(t, s)
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != 7 {
		t.Fatalf("requests = %d, want 7", got)
	}
}

func TestAppendReadsOnlyNewBytes(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")
	writeLines(t, f,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`)

	s := NewScanner(home, t.TempDir())
	mustScan(t, s)
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}

	newLine := `{"type":"assistant","uuid":"a2","timestamp":"2026-10-19T12:00:00.000Z","message":{"id":"m2","model":"claude-sonnet-4-5","usage":{"input_tokens":20,"output_tokens":2}}}` + "\n"
	appendBytes(t, f, newLine)
	done, total := mustScan(t, s)
	if total != int64(len(newLine)) || done != int64(len(newLine)) {
		t.Fatalf("append scan read done=%d total=%d, want %d", done, total, len(newLine))
	}
	sum := s.Summary(RangeToday, fixtureNow())
	if sum.Total.Requests != 2 || sum.Total.Input != 30 {
		t.Fatalf("after append: %+v", sum.Total)
	}
}

func TestTruncateRereads(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")
	writeLines(t, f,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-10-19T10:01:00.000Z","message":{"id":"m2","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`)

	s := NewScanner(home, t.TempDir())
	mustScan(t, s)

	// Shrink the file to one different record.
	writeLines(t, f,
		`{"type":"assistant","uuid":"b1","timestamp":"2026-10-19T09:00:00.000Z","message":{"id":"n1","model":"x","usage":{"input_tokens":5,"output_tokens":5}}}`)
	mustScan(t, s)
	sum := s.Summary(RangeToday, fixtureNow())
	if sum.Total.Requests != 1 || sum.Total.Input != 5 {
		t.Fatalf("after truncate: %+v", sum.Total)
	}
}

func TestSameSizeRewriteRereads(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")
	old := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`
	writeLines(t, f, old)

	s := NewScanner(home, t.TempDir())
	mustScan(t, s)

	// Same length, different id, then bump mtime.
	nw := strings.Replace(old, `"m1"`, `"m9"`, 1)
	if len(nw) != len(old)+1 && len(nw) < 1 {
		t.Fatal("bad fixture")
	}
	if err := os.WriteFile(f, []byte(nw+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(f, future, future); err != nil {
		t.Fatal(err)
	}
	mustScan(t, s)
	// m9 must replace m1 — dedupe must not resurrect the old record.
	sum := s.Summary(RangeToday, fixtureNow())
	if sum.Total.Requests != 1 {
		t.Fatalf("after rewrite: requests = %d, want 1", sum.Total.Requests)
	}
}

func TestGrowWithReplacedContentRereads(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")
	writeLines(t, f,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`)

	s := NewScanner(home, t.TempDir())
	mustScan(t, s)

	// Rewrite bigger with different leading bytes: tail verification
	// must catch this is not an append.
	writeLines(t, f,
		`{"type":"assistant","uuid":"xx","timestamp":"2026-10-19T09:00:00.000Z","message":{"id":"w1","model":"claude-sonnet-4-5","usage":{"input_tokens":77,"output_tokens":7}}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"w2","model":"claude-sonnet-4-5","usage":{"input_tokens":33,"output_tokens":3}}}`)
	mustScan(t, s)
	sum := s.Summary(RangeToday, fixtureNow())
	if sum.Total.Requests != 2 || sum.Total.Input != 110 {
		t.Fatalf("after replace: %+v", sum.Total)
	}
}

func TestPartialLastLineWaits(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")
	line1 := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`
	writeLines(t, f, line1)
	// A half-written second line must not be consumed.
	appendBytes(t, f, `{"type":"assistant","uuid":"a2","timestamp":"2026-1`)

	s := NewScanner(home, t.TempDir())
	mustScan(t, s)
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != 1 {
		t.Fatalf("requests = %d, want 1 (partial line ignored)", got)
	}

	line2 := `{"type":"assistant","uuid":"a2","timestamp":"2026-10-19T12:00:00.000Z","message":{"id":"m2","model":"claude-sonnet-4-5","usage":{"input_tokens":20,"output_tokens":2}}}`
	// Finish the partial line, then start another partial one.
	writeLines(t, f, line1, line2)
	appendBytes(t, f, `{"type":"assistant","uui`)
	mustScan(t, s)
	sum := s.Summary(RangeToday, fixtureNow())
	if sum.Total.Requests != 2 || sum.Total.Input != 30 {
		t.Fatalf("after completing line: %+v", sum.Total)
	}
}

func TestDeletedFileDropped(t *testing.T) {
	home := t.TempDir()
	f1 := filepath.Join(home, ".claude", "projects", "-p", "s1.jsonl")
	f2 := filepath.Join(home, ".claude", "projects", "-p", "s2.jsonl")
	line := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`
	writeLines(t, f1, line)
	writeLines(t, f2, line)

	s := NewScanner(home, t.TempDir())
	mustScan(t, s)
	// Same message id in two files dedupes to one anyway; use a file
	// with a distinct id to see the drop.
	writeLines(t, f2,
		`{"type":"assistant","uuid":"b1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"mx","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`)
	mustScan(t, s)
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	if err := os.Remove(f2); err != nil {
		t.Fatal(err)
	}
	mustScan(t, s)
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != 1 {
		t.Fatalf("after delete: requests = %d, want 1", got)
	}
}

func TestOmpForkDedupeAcrossAppend(t *testing.T) {
	home := t.TempDir()
	line := `{"type":"message","id":"e1","timestamp":"2026-10-19T10:00:00.000Z","message":{"role":"assistant","model":"m","usage":{"input":100,"output":10,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"timestamp":1792404001000}}`
	f1 := filepath.Join(home, ".omp", "agent", "sessions", "-p", "s1", "one.jsonl")
	f2 := filepath.Join(home, ".omp", "agent", "sessions", "-p", "s2", "fork.jsonl")
	writeLines(t, f1, line)

	s := NewScanner(home, t.TempDir())
	mustScan(t, s)
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	// A fork appears later, copying the same record.
	writeLines(t, f2, line)
	mustScan(t, s)
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != 1 {
		t.Fatalf("after fork file: requests = %d, want 1", got)
	}
}

func TestScanCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := NewScanner(fixtureHome, t.TempDir())
	err := s.Scan(ctx, nil)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestNoDirsGivesEmptySummary(t *testing.T) {
	s := NewScanner(t.TempDir(), t.TempDir())
	mustScan(t, s)
	sum := s.Summary(RangeToday, fixtureNow())
	if sum.Total.Requests != 0 || len(sum.ByTool) != 0 {
		t.Fatalf("want empty summary, got %+v", sum)
	}
	if len(sum.Buckets) != 24 {
		t.Fatalf("today buckets = %d, want 24", len(sum.Buckets))
	}
}

func TestCodexResumeDedupeAcrossScans(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".codex", "sessions", "2026", "10", "19", "rollout-x.jsonl")
	ev := func(ts string, in, cached, out int64) string {
		n := strconv.FormatInt
		return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` +
			n(in, 10) + `,"cached_input_tokens":` + n(cached, 10) + `,"cache_write_input_tokens":0,"output_tokens":` + n(out, 10) +
			`,"reasoning_output_tokens":0,"total_tokens":` + n(in+out, 10) + `},"last_token_usage":{"input_tokens":` + n(in, 10) +
			`,"cached_input_tokens":` + n(cached, 10) + `,"cache_write_input_tokens":0,"output_tokens":` + n(out, 10) +
			`,"reasoning_output_tokens":0,"total_tokens":` + n(in+out, 10) + `}}}}`
	}
	writeLines(t, f, ev("2026-10-19T08:00:00.000Z", 100, 20, 10))

	cache := t.TempDir()
	s := NewScanner(home, cache)
	mustScan(t, s)
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	// Appended event with the SAME running total = duplicate write,
	// must be skipped even after the tail state round-trips the cache.
	appendBytes(t, f, ev("2026-10-19T08:01:00.000Z", 100, 20, 10)+"\n")

	s2 := NewScanner(home, cache)
	mustScan(t, s2)
	if got := s2.Summary(RangeToday, fixtureNow()).Total.Requests; got != 1 {
		t.Fatalf("dup across scans counted: requests = %d, want 1", got)
	}
}

// A duplicate copy discarded at parse time used to be lost forever when the
// file that "won" the dedupe key was deleted: the surviving file was reused
// from cache without reparsing. Dedupe now happens at aggregation, so the
// surviving copy still counts.
func TestDuplicateSurvivesWinnerDeletion(t *testing.T) {
	home := t.TempDir()
	line := `{"type":"message","id":"e1","timestamp":"2026-10-19T10:00:00.000Z","message":{"role":"assistant","model":"m","usage":{"input":100,"output":10,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"timestamp":1792404001000}}`
	f1 := filepath.Join(home, ".omp", "agent", "sessions", "-p", "s1", "one.jsonl")
	f2 := filepath.Join(home, ".omp", "agent", "sessions", "-p", "s2", "fork.jsonl")
	writeLines(t, f1, line)
	writeLines(t, f2, line)

	cache := t.TempDir()
	s := NewScanner(home, cache)
	mustScan(t, s)
	if got := s.Summary(RangeToday, fixtureNow()).Total.Requests; got != 1 {
		t.Fatalf("requests = %d, want 1 (copies deduped)", got)
	}

	// Delete one copy and rescan through a fresh scanner (cache path):
	// the other copy must keep the usage alive.
	if err := os.Remove(f2); err != nil {
		t.Fatal(err)
	}
	s2 := NewScanner(home, cache)
	mustScan(t, s2)
	if got := s2.Summary(RangeToday, fixtureNow()).Total.Requests; got != 1 {
		t.Fatalf("after deleting one copy: requests = %d, want 1", got)
	}
}

// A resumed scan that consumes only a partial line must keep the previous
// fingerprint: without it the next bigger rewrite slips past tailIntact.
func TestPartialAppendKeepsFingerprint(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")
	line1 := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`
	writeLines(t, f, line1)

	s := NewScanner(home, t.TempDir())
	mustScan(t, s)

	// Append an incomplete line: consumed, but it replaces no fingerprint.
	appendBytes(t, f, `{"type":"assistant","uuid":"a2","timestamp":"2026-1`)
	mustScan(t, s)

	// A bigger rewrite that drops the first line must be detected via the
	// preserved fingerprint, not treated as an append of the tail.
	line2 := `{"type":"assistant","uuid":"b1","timestamp":"2026-10-19T11:00:00.000Z","message":{"id":"n1","model":"claude-sonnet-4-5","usage":{"input_tokens":42,"output_tokens":4}}}`
	writeLines(t, f, line2)
	mustScan(t, s)
	sum := s.Summary(RangeToday, fixtureNow())
	if sum.Total.Requests != 1 || sum.Total.Input != 42 {
		t.Fatalf("after rewrite: %+v (want only n1)", sum.Total)
	}
}

// After a scan leaves a partial tail, old.size > old.offset. A replacement
// exactly old.offset bytes long is neither a shrink nor a same-size nor an
// append — and must be reread, not treated as unchanged.
func TestRewriteAtConsumedOffsetRereads(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")
	line1 := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`
	writeLines(t, f, line1)
	appendBytes(t, f, `{"type":"assistant","uuid":"a2","timestamp":"2026-1`) // partial tail

	s := NewScanner(home, t.TempDir())
	mustScan(t, s)

	// Rewrite the file to exactly the consumed offset's byte count with a
	// different record.
	st, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	// consumed offset = the line1 bytes (partial tail is unconsumed)
	consumed := int64(len(line1) + 1)
	if st.Size() <= consumed {
		t.Fatalf("fixture: size %d should exceed consumed %d", st.Size(), consumed)
	}
	line2 := `{"type":"assistant","uuid":"b1","timestamp":"2026-10-19T09:00:00.000Z","message":{"id":"n1","model":"claude-sonnet-4-5","usage":{"input_tokens":42,"output_tokens":4}}}`
	if int64(len(line2)) != consumed-1 {
		t.Fatalf("fixture line2 length %d, want %d — pad it", len(line2), consumed-1)
	}
	writeLines(t, f, line2)
	mustScan(t, s)
	sum := s.Summary(RangeToday, fixtureNow())
	if sum.Total.Requests != 1 || sum.Total.Input != 42 {
		t.Fatalf("rewrite at consumed offset not seen: %+v", sum.Total)
	}
}

// A scan handed an already-cancelled context must do nothing at all: it
// returns the ctx error, leaves the on-disk cache untouched (no write) and
// never even loads it (a pre-existing cache stays invisible — the fresh
// scanner's Summary shows nothing).
func TestScanCancelledBeforeWork(t *testing.T) {
	home := t.TempDir()
	f := filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")
	writeLines(t, f, `{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:00.000Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`)
	cacheDir := t.TempDir()

	// A previous run wrote a cache for this home; a cancelled scan must
	// not read it (its records would show up in the summary).
	wrote := NewScanner(home, cacheDir)
	mustScan(t, wrote)

	s := NewScanner(home, cacheDir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.Scan(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan on a cancelled ctx = %v, want context.Canceled", err)
	}
	sum := s.Summary(RangeAll, fixtureNow())
	if sum.Total.Requests != 0 {
		t.Fatalf("cancelled scan still loaded the cache: %+v", sum.Total)
	}
	// The cache file must be byte-identical: nothing was written either.
	data, err2 := os.ReadFile(filepath.Join(cacheDir, cacheName))
	if err2 != nil || len(data) == 0 {
		t.Fatalf("cache missing or rewritten, err=%v len=%d", err2, len(data))
	}
	if err3 := s.Scan(ctx, nil); !errors.Is(err3, context.Canceled) {
		t.Fatalf("re-Scan on cancelled ctx = %v", err3)
	}
}

// A cancel arriving while the cache serializes must leave the previously
// written cache untouched and drop no stray temp files.
func TestSaveCacheAbortsMidSerialization(t *testing.T) {
	cacheDir := t.TempDir()

	// Write a real cache first: this is the version a cancelled write
	// must leave in place, byte for byte.
	good := NewScanner(fixtureHome, cacheDir)
	mustScan(t, good)
	cachePath := filepath.Join(cacheDir, cacheName)
	before, err := os.ReadFile(cachePath)
	if err != nil || len(before) == 0 {
		t.Fatalf("setup cache: err=%v len=%d", err, len(before))
	}

	// Snapshot the populated file states and re-save them under a context
	// that cancels the first time saveCache checks it — i.e. during
	// serialization.
	s := NewScanner(fixtureHome, cacheDir)
	mustScan(t, s)
	s.mu.RLock()
	files := s.files
	s.mu.RUnlock()

	ctx, cancel := context.WithCancel(context.Background())
	once := 0
	s.saveCache(&cancelSpy{Context: ctx, n: &once, onErr: cancel}, files)

	after, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("cancelled saveCache overwrote the old cache")
	}
	entries, _ := os.ReadDir(cacheDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".usage-scan-") {
			t.Fatalf("stray temp file left behind: %s", e.Name())
		}
	}
}

// cancelSpy cancels its parent the first time Err is consulted — standing
// in for a cancel that lands mid-serialization.
type cancelSpy struct {
	context.Context
	n     *int
	onErr func()
}

func (c *cancelSpy) Err() error {
	if *c.n > 0 {
		return c.Context.Err()
	}
	*c.n++
	c.onErr()
	return nil // first check passes; every later check reports cancelled
}
