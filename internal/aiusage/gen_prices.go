//go:build ignore

// gen_prices trims LiteLLM's model_prices_and_context_window.json down to the
// four token-rate fields Rakko needs and writes prices.json.gz alongside
// pricing.go.
//
// Usage:
//
//	go run gen_prices.go [-input /path/to/model_prices_and_context_window.json]
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

const litellmURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

type rawEntry struct {
	Mode       string  `json:"mode"`
	Input      float64 `json:"input_cost_per_token"`
	Output     float64 `json:"output_cost_per_token"`
	CacheRead  float64 `json:"cache_read_input_token_cost"`
	CacheWrite float64 `json:"cache_creation_input_token_cost"`
}

func main() {
	inPath := flag.String("input", "", "local path to model_prices_and_context_window.json")
	outPath := flag.String("output", "prices.json.gz", "output gzipped JSON path")
	flag.Parse()

	var data []byte
	var err error
	if *inPath != "" {
		data, err = os.ReadFile(*inPath)
	} else {
		resp, gerr := http.Get(litellmURL)
		if gerr != nil {
			fmt.Fprintf(os.Stderr, "fetch %s: %v\n", litellmURL, gerr)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "fetch %s: status %s\n", litellmURL, resp.Status)
			os.Exit(1)
		}
		data, err = io.ReadAll(resp.Body)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "read input: %v\n", err)
		os.Exit(1)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "parse json: %v\n", err)
		os.Exit(1)
	}

	type candidate struct {
		key  string
		rank int
		rate [4]float64
	}
	var all []candidate
	for k, v := range raw {
		var e rawEntry
		if err := json.Unmarshal(v, &e); err != nil {
			continue
		}
		switch e.Mode {
		case "", "chat", "responses", "completion":
		default:
			continue
		}
		if e.Input <= 0 && e.Output <= 0 {
			continue
		}
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "" {
			continue
		}
		all = append(all, candidate{
			key:  lk,
			rank: providerRank(lk),
			rate: [4]float64{e.Input, e.Output, e.CacheRead, e.CacheWrite},
		})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].rank != all[j].rank {
			return all[i].rank < all[j].rank
		}
		return all[i].key < all[j].key
	})

	out := make(map[string][4]float64, len(all))
	for _, c := range all {
		out[c.key] = c.rate
	}
	// Add bare model names (after last '/') when not already present, preferring
	// canonical providers (earlier in sorted order).
	for _, c := range all {
		if idx := strings.LastIndexByte(c.key, '/'); idx >= 0 && idx+1 < len(c.key) {
			bare := c.key[idx+1:]
			if _, exists := out[bare]; !exists {
				out[bare] = c.rate
			}
		}
	}

	f, err := os.Create(*outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", *outPath, err)
		os.Exit(1)
	}
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		f.Close()
		fmt.Fprintf(os.Stderr, "gzip: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(gz)
	if err := enc.Encode(out); err != nil {
		gz.Close()
		f.Close()
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	if err := gz.Close(); err != nil {
		f.Close()
		fmt.Fprintf(os.Stderr, "close gzip: %v\n", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close file: %v\n", err)
		os.Exit(1)
	}
	fi, _ := os.Stat(*outPath)
	fmt.Printf("wrote %d models to %s (%d bytes)\n", len(out), *outPath, fi.Size())
}

func providerRank(k string) int {
	if !strings.Contains(k, "/") {
		return 0
	}
	for i, p := range []string{
		"anthropic/", "openai/", "gemini/", "google/", "deepseek/", "xai/",
		"vertex_ai/", "bedrock/", "azure/", "azure_ai/", "github_copilot/",
	} {
		if strings.HasPrefix(k, p) {
			return i + 1
		}
	}
	return 100
}
