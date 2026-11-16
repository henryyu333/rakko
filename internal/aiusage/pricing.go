package aiusage

//go:generate go run gen_prices.go

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed prices.json.gz
var pricesGZ []byte

type modelPrice struct {
	input      float64
	output     float64
	cacheRead  float64
	cacheWrite float64
}

var (
	pricesOnce sync.Once
	pricesMap  map[string]modelPrice
)

func loadPrices() map[string]modelPrice {
	pricesOnce.Do(func() {
		gz, err := gzip.NewReader(bytes.NewReader(pricesGZ))
		if err != nil {
			pricesMap = map[string]modelPrice{}
			return
		}
		defer gz.Close()
		var raw map[string][4]float64
		if err := json.NewDecoder(gz).Decode(&raw); err != nil {
			pricesMap = map[string]modelPrice{}
			return
		}
		m := make(map[string]modelPrice, len(raw))
		for k, v := range raw {
			m[k] = modelPrice{
				input:      v[0],
				output:     v[1],
				cacheRead:  v[2],
				cacheWrite: v[3],
			}
		}
		pricesMap = m
	})
	return pricesMap
}

// effortSuffixes are reasoning/tier tags appended by tools (e.g. agy's
// "-high"/"-medium", codex's "-xhigh") that do not change per-token rates.
var effortSuffixes = []string{
	"-thinking",
	"-xhigh",
	"-high",
	"-medium",
	"-low",
	"-minimal",
	"-fast",
	"-slow",
}

// lookupPrice finds the per-token rate for model in the embedded LiteLLM
// table, normalizing provider prefixes, effort tiers, and date suffixes.
func lookupPrice(model string) (modelPrice, bool) {
	m := loadPrices()
	s := strings.ToLower(strings.TrimSpace(model))
	if s == "" {
		return modelPrice{}, false
	}
	if p, ok := m[s]; ok {
		return p, true
	}

	// Strip :variant (e.g. ":thinking", ":free") or @version.
	if i := strings.IndexAny(s, ":@"); i > 0 {
		base := s[:i]
		if p, ok := m[base]; ok {
			return p, true
		}
		s = base
	}

	// Build candidate bases by progressively stripping provider prefixes
	// (e.g. "openrouter/anthropic/claude-..." -> "anthropic/claude-..." -> "claude-...").
	var candidates []string
	candidates = append(candidates, s)
	cur := s
	for {
		i := strings.IndexByte(cur, '/')
		if i < 0 || i+1 >= len(cur) {
			break
		}
		cur = cur[i+1:]
		candidates = append(candidates, cur)
	}

	for _, c := range candidates {
		if p, ok := matchWithSuffixes(m, c); ok {
			return p, true
		}
	}
	return modelPrice{}, false
}

func matchWithSuffixes(m map[string]modelPrice, s string) (modelPrice, bool) {
	if p, ok := m[s]; ok {
		return p, true
	}
	// Strip effort suffix first, then try both as-is and without date suffix.
	noEffort := stripEffortSuffix(s)
	if noEffort != s {
		if p, ok := m[noEffort]; ok {
			return p, true
		}
	}
	if noDate := stripDateSuffix(s); noDate != s {
		if p, ok := m[noDate]; ok {
			return p, true
		}
		if ne := stripEffortSuffix(noDate); ne != noDate {
			if p, ok := m[ne]; ok {
				return p, true
			}
		}
	}
	if noEffort != s {
		if noDate := stripDateSuffix(noEffort); noDate != noEffort {
			if p, ok := m[noDate]; ok {
				return p, true
			}
		}
	}
	return modelPrice{}, false
}

func stripEffortSuffix(s string) string {
	for _, suf := range effortSuffixes {
		if strings.HasSuffix(s, suf) && len(s) > len(suf) {
			return s[:len(s)-len(suf)]
		}
	}
	return s
}

// stripDateSuffix removes trailing "-YYYYMMDD" or "-YYYY-MM-DD" when present.
func stripDateSuffix(s string) string {
	n := len(s)
	// "-YYYY-MM-DD" (11 chars)
	if n > 11 && s[n-11] == '-' && s[n-6] == '-' && s[n-3] == '-' &&
		allDigits(s[n-10:n-6]) && allDigits(s[n-5:n-3]) && allDigits(s[n-2:]) {
		return s[:n-11]
	}
	// "-YYYYMMDD" (9 chars)
	if n > 9 && s[n-9] == '-' && allDigits(s[n-8:]) {
		return s[:n-9]
	}
	return s
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// estimateCost computes USD cost for token counts using the embedded LiteLLM
// price table. Returns (0, false) when the model is not in the table.
func estimateCost(model string, in, out, cacheRead, cacheWrite int64) (float64, bool) {
	p, ok := lookupPrice(model)
	if !ok {
		return 0, false
	}
	cost := float64(in)*p.input +
		float64(out)*p.output +
		float64(cacheRead)*p.cacheRead +
		float64(cacheWrite)*p.cacheWrite
	return cost, true
}
