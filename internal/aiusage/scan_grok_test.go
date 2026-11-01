package aiusage

import (
	"context"
	"testing"
)

// Grok records every finished turn twice: usage.json has the per-turn
// numbers, and updates.jsonl has a turn_completed event. The reader must
// count a turn once, whichever files exist.
func TestGrokReaderDedupesUsageAndUpdates(t *testing.T) {
	s := NewScanner("testdata/readers/grok/home", t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sum := s.Summary(RangeToday, fixtureNow())
	g := sum.ByTool[ToolGrok]
	if g.Requests != 3 || g.Input != 3600 || g.Output != 600 ||
		g.CacheRead != 2400 || g.CacheWrite != 60 {
		t.Fatalf("grok totals wrong: %+v", g)
	}
	// 0.005 + 0.01 + 0.015 USD; costUsdTicks is 10^10 ticks per USD.
	if g.Unpriced != 0 || g.Cost < 0.0299 || g.Cost > 0.0301 {
		t.Fatalf("grok price wrong: %+v", g)
	}
	if sum.Total.Requests != 3 {
		t.Fatalf("grok records leaked into other tools: %+v", sum.Total)
	}
}
