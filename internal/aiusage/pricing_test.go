package aiusage

import (
	"math"
	"sync"
	"testing"
)

func TestLookupPriceNormalization(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-5-20250929", true},
		{"anthropic/claude-sonnet-4-5-20250929", true},
		{"openrouter/anthropic/claude-sonnet-4-5-20250929", true},
		{"claude-opus-5-5-high", true},
		{"gemini-4-hydrogen-high", true},
		{"gemini-4-hydrogen-medium", true},
		{"gemini-4-argon-high", true},
		{"gpt-5.3-codex", true},
		{"openai/gpt-5.3-codex-xhigh", true},
		{"deepseek-v4.1-flash", true},
		{"deepseek-chat", true},
		{"claude-sonnet-4-5-20250929:thinking", true},
		{"gpt-5-2026-01-15", true},
		{"vertex_ai/claude-opus-5-5-fast", true},
		{"claude-sonnet-4-5-fast", false},
		{"", false},
		{"totally-unknown-model-xyz", false},
	}
	for _, tc := range cases {
		_, got := lookupPrice(tc.model)
		if got != tc.want {
			t.Errorf("lookupPrice(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestLookupPriceFastTierNotDowngraded(t *testing.T) {
	// vertex_ai/claude-opus-5-5 exists at 4e-6/2e-5, while bare
	// claude-opus-5-5-fast exists at 8e-6/4e-5. Prefix stripping must
	// find claude-opus-5-5-fast before any suffix normalization.
	p, ok := lookupPrice("vertex_ai/claude-opus-5-5-fast")
	if !ok {
		t.Fatal("expected vertex_ai/claude-opus-5-5-fast to be priced")
	}
	if math.Abs(p.input-8e-6) > 1e-12 || math.Abs(p.output-4e-5) > 1e-12 {
		t.Fatalf("got input=%v output=%v, want 8e-6 / 4e-5", p.input, p.output)
	}
	// Effort tiers like agy's -medium and -high still fall back to base price.
	base, ok := lookupPrice("gemini-4-hydrogen")
	if !ok {
		t.Fatal("expected gemini-4-hydrogen to be priced")
	}
	for _, m := range []string{"gemini-4-hydrogen-medium", "gemini-4-hydrogen-high"} {
		got, ok := lookupPrice(m)
		if !ok {
			t.Fatalf("expected %q to be priced", m)
		}
		if got != base {
			t.Fatalf("%q price = %+v, want %+v", m, got, base)
		}
	}
}

func TestEstimateCostWithCache(t *testing.T) {
	// claude-opus-5-5: in=4e-6, out=2e-5, cr=2e-7, cw=5e-6
	cost, ok := estimateCost("claude-opus-5-5-high", 1000, 500, 10000, 200)
	if !ok {
		t.Fatal("expected claude-opus-5-5-high to be priced")
	}
	want := 1000*4e-6 + 500*2e-5 + 10000*2e-7 + 200*5e-6
	if math.Abs(cost-want) > 1e-9 {
		t.Fatalf("estimateCost = %v, want %v", cost, want)
	}
	if c, ok := estimateCost("nonexistent-model", 1000, 100, 0, 0); ok || c != 0 {
		t.Fatalf("expected unpriced, got (%v, %v)", c, ok)
	}
}

// A hit and a miss both come from the cache after the first call, so the
// cached answer must equal the table's answer name for name: the entries are
// keyed by the raw name, which normalization may rewrite.
func TestLookupPriceCacheAgreesWithTheTable(t *testing.T) {
	names := []string{
		"", " ", "claude-sonnet-4-5-20250929", "Anthropic/Claude-Sonnet-4-5-20250929",
		"openrouter/anthropic/claude-sonnet-4-5-20250929", "vertex_ai/claude-opus-5-5-fast",
		"claude-opus-5-5-high", "gpt-5-3-codex-2026-01-15", "gpt-5-2026-01-15",
		"deepseek-chat", "gemini-4-hydrogen-medium", "claude-sonnet-4-5-fast",
		"totally-unknown-model-xyz", "claude-sonnet-4-5-20250929:thinking",
	}
	for name := range loadPrices() {
		names = append(names, name)
	}
	for _, name := range names {
		want, wantOK := lookupPriceUncached(name)
		for call := 1; call <= 2; call++ {
			got, gotOK := lookupPrice(name)
			if got != want || gotOK != wantOK {
				t.Fatalf("lookupPrice(%q) call %d = %+v,%v, want %+v,%v",
					name, call, got, gotOK, want, wantOK)
			}
		}
	}
}

// The local usage scan and the weekly report price records on their own
// goroutines, so the same names arrive concurrently on a cold cache. Run
// this under -race.
func TestLookupPriceConcurrentReaders(t *testing.T) {
	names := []string{
		"claude-sonnet-4-5-20250929", "openrouter/anthropic/claude-sonnet-4-5-20250929",
		"claude-opus-5-5-high", "gemini-4-hydrogen-medium", "deepseek-chat",
		"unknown-model-for-the-week", "another-unknown-model",
	}
	want := make(map[string]priceResult, len(names))
	for _, name := range names {
		p, ok := lookupPriceUncached(name)
		want[name] = priceResult{price: p, found: ok}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				for _, name := range names {
					p, ok := lookupPrice(name)
					if got := (priceResult{price: p, found: ok}); got != want[name] {
						t.Errorf("goroutine %d: lookupPrice(%q) = %+v, want %+v", g, name, got, want[name])
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
