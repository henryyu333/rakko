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
// Variants like "-fast" and "-slow" are distinct priced models in LiteLLM and
// must not be stripped as effort tiers.
var effortSuffixes = []string{
	"-thinking",
	"-xhigh",
	"-high",
	"-medium",
	"-low",
	"-minimal",
}

// priceResult is one model's answer, a hit or a miss: the scanners ask for
// the same names for every request in every week, so a miss is worth as much
// as a hit.
type priceResult struct {
	price modelPrice
	found bool
}

// priceCache remembers lookupPrice's answer per name as it arrived from the
// logs. The local usage scan and the weekly report each scan on their own
// goroutine, so the map is concurrent.
var priceCache sync.Map // model name -> priceResult

// lookupPrice finds the per-token rate for model in the embedded LiteLLM
// table, normalizing provider prefixes, effort tiers, and date suffixes. The
// normalization and candidate search run once per distinct name.
func lookupPrice(model string) (modelPrice, bool) {
	if v, ok := priceCache.Load(model); ok {
		r := v.(priceResult)
		return r.price, r.found
	}
	p, ok := lookupPriceUncached(model)
	priceCache.Store(model, priceResult{price: p, found: ok})
	return p, ok
}

// lookupPriceUncached is the normalization itself, run for a name the cache
// has not seen.
func lookupPriceUncached(model string) (modelPrice, bool) {
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

	// Try exact matches across all prefix candidates first, then date-stripped
	// matches, and only fall back to stripping effort suffixes if none match.
	// This prevents "vertex_ai/claude-opus-5-5-fast" from stripping "-fast"
	// before matching bare "claude-opus-5-5-fast".
	for _, c := range candidates {
		if p, ok := m[c]; ok {
			return p, true
		}
	}
	for _, c := range candidates {
		if noDate := stripDateSuffix(c); noDate != c {
			if p, ok := m[noDate]; ok {
				return p, true
			}
		}
	}
	for _, c := range candidates {
		if p, ok := matchWithEffortSuffix(m, c); ok {
			return p, true
		}
	}
	return modelPrice{}, false
}

func matchWithEffortSuffix(m map[string]modelPrice, s string) (modelPrice, bool) {
	noEffort := stripEffortSuffix(s)
	if noEffort != s {
		if p, ok := m[noEffort]; ok {
			return p, true
		}
		if noDate := stripDateSuffix(noEffort); noDate != noEffort {
			if p, ok := m[noDate]; ok {
				return p, true
			}
		}
	}
	if noDate := stripDateSuffix(s); noDate != s {
		if ne := stripEffortSuffix(noDate); ne != noDate {
			if p, ok := m[ne]; ok {
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
