package aiusage

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
)

// Antigravity session transcripts, from both the CLI (agy) and the app/IDE:
// ~/.gemini/antigravity-cli/brain/<conv>/.system_generated/logs/transcript_full.jsonl
// ~/.gemini/antigravity/brain/<conv>/.system_generated/logs/transcript_full.jsonl
//
// App lines may omit "model"; they still count tokens and requests but are
// reported as Unpriced. Conversation IDs are UUIDs, so the <conv>:<step_index>
// dedupe key is safe across both directories.
//
// Each assistant turn is a PLANNER_RESPONSE line:
//   {"step_index":1,"type":"PLANNER_RESPONSE","model":"gemini-4-hydrogen-high",
//    "input_tokens":11167,"cache_read_tokens":0,"output_tokens":109,
//    "created_at":"2026-11-16T06:26:32Z"}
//
// transcript.jsonl and logs/chunks/transcript_full/*.jsonl in the same
// directory mirror the same stream, so only transcript_full.jsonl is read and
// deduplicated by <conv>:<step_index>.

type agyLineJSON struct {
	StepIndex *int64          `json:"step_index"`
	Type      string          `json:"type"`
	Model     string          `json:"model"`
	CreatedAt json.RawMessage `json:"created_at"`
	flexUsageJSON
}

func parseAntigravityLine(path string, line []byte, pool map[string]*string) (r rec, key string, ok bool) {
	if !bytes.Contains(line, []byte(`"PLANNER_RESPONSE"`)) {
		return r, "", false
	}
	var e agyLineJSON
	if json.Unmarshal(line, &e) != nil || e.Type != "PLANNER_RESPONSE" {
		return r, "", false
	}
	at := parseMillis(e.CreatedAt)
	if at == 0 {
		return r, "", false
	}
	r.at = at
	r.model = intern(pool, e.Model)
	r.in = e.InputTokens
	r.out = e.OutputTokens
	r.cr = e.CacheReadAlt
	if e.StepIndex != nil {
		conv := agyConvID(path)
		key = conv + ":" + strconv.FormatInt(*e.StepIndex, 10)
	}
	return r, key, true
}

// agyConvID extracts <conv> from .../brain/<conv>/.system_generated/logs/transcript_full.jsonl.
func agyConvID(path string) string {
	slash := filepath.ToSlash(path)
	if idx := strings.LastIndex(slash, "/brain/"); idx >= 0 {
		rest := slash[idx+len("/brain/"):]
		if end := strings.IndexByte(rest, '/'); end > 0 {
			return rest[:end]
		}
	}
	return filepath.Base(filepath.Dir(path))
}
