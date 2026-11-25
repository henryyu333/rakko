package aiusage

import (
	"encoding/json"
	"testing"
)

// Devin writes cache writes under two spellings; both must reach toCounts
// (the CLI key used to be shadowed by the embedded Claude-style field).
func TestDevinMetricsReadsBothCacheWriteKeys(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want int64
	}{
		{"cli key", `{"cache_creation_tokens":7}`, 7},
		{"claude-style key", `{"cache_creation_input_tokens":5}`, 5},
		{"both keys", `{"cache_creation_tokens":7,"cache_creation_input_tokens":5}`, 7},
	} {
		var m devinMetrics
		if err := json.Unmarshal([]byte(tt.raw), &m); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if _, _, _, cw := m.toCounts(); cw != tt.want {
			t.Errorf("%s: cache write = %d, want %d", tt.name, cw, tt.want)
		}
	}
}

// The shared parser counts only cache_creation_input_tokens: a
// cache_creation_tokens key belongs to Devin's metrics block alone.
func TestFlexUsageIgnoresDevinCacheCreationKey(t *testing.T) {
	var u flexUsageJSON
	if err := json.Unmarshal([]byte(`{"cache_creation_tokens":9}`), &u); err != nil {
		t.Fatal(err)
	}
	if _, _, _, cw := u.toCounts(); cw != 0 {
		t.Fatalf("generic cache write = %d, want 0", cw)
	}
	if err := json.Unmarshal([]byte(`{"cache_creation_input_tokens":4}`), &u); err != nil {
		t.Fatal(err)
	}
	if _, _, _, cw := u.toCounts(); cw != 4 {
		t.Fatalf("claude-style cache write = %d, want 4", cw)
	}

	line := []byte(`{"id":"x","model":"m","timestamp":1794800020000,"usage":{"input_tokens":10,"output_tokens":2,"cache_creation_tokens":9}}`)
	r, _, ok := parseFlexJSONLLine("devin-test", line, map[string]*string{}, false)
	if !ok {
		t.Fatal("the line did not parse")
	}
	if r.cw != 0 || r.in != 10 {
		t.Fatalf("parsed cache write = %d, input = %d; want 0 and 10", r.cw, r.in)
	}
}
