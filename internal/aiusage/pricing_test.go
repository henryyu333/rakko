package aiusage

import (
	"math"
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
