package aiusage

import (
	"context"
	"testing"
)

// pi writes the same session format as omp; the reader only differs in the
// directory it walks.
func TestPiReader(t *testing.T) {
	s := NewScanner("testdata/readers/pi/home", t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sum := s.Summary(RangeToday, fixtureNow())
	pi := sum.ByTool[ToolPi]
	if pi.Requests != 2 || pi.Input != 300 || pi.Output != 60 ||
		pi.CacheRead != 90 || pi.CacheWrite != 15 {
		t.Fatalf("pi totals wrong: %+v", pi)
	}
	if pi.Unpriced != 0 || pi.Cost < 0.0269 || pi.Cost > 0.0271 {
		t.Fatalf("pi price wrong: %+v", pi)
	}
	if sum.Total.Requests != 2 {
		t.Fatalf("pi records leaked into other tools: %+v", sum.Total)
	}
	for _, st := range s.Statuses() {
		if st.Tool == ToolPi && st.Status != StatusFound {
			t.Fatalf("pi status = %v, want found", st.Status)
		}
	}
}
