package aiusage

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Claude Code session entries: ~/.claude/projects/**/*.jsonl
//
// Relevant lines look like:
//   {"type":"assistant","uuid":"…","timestamp":"2026-10-08T16:44:28.550Z",
//    "message":{"id":"msg_…","model":"claude-…","role":"assistant",
//               "usage":{"input_tokens":4,"output_tokens":120,
//                        "cache_creation_input_tokens":0,
//                        "cache_read_input_tokens":8123}}}
//
// Entries with message.model == "<synthetic>" are placeholders, not real
// calls. The same message.id can be logged more than once (retries /
// resumed writes); each counts once. Claude Code records no price.

type claudeEntryJSON struct {
	Type      string          `json:"type"`
	UUID      string          `json:"uuid"`
	Timestamp json.RawMessage `json:"timestamp"`
	Message struct {
		ID    string         `json:"id"`
		Model string         `json:"model"`
		Usage *flexUsageJSON `json:"usage"`
	} `json:"message"`
}

// parseClaudeLine decodes one line; ok=false skips it. key is the dedupe
// key (message.id, or the entry uuid as a weaker fallback).
func parseClaudeLine(line []byte, pool map[string]*string) (r rec, key string, ok bool) {
	if !bytes.Contains(line, []byte(`"usage"`)) {
		return r, "", false
	}
	var e claudeEntryJSON
	if json.Unmarshal(line, &e) != nil {
		return r, "", false
	}
	u := e.Message.Usage
	if e.Type != "assistant" || u == nil || e.Message.Model == "<synthetic>" {
		return r, "", false
	}
	at := parseMillis(e.Timestamp)
	if at == 0 {
		return r, "", false
	}
	r.at = at
	r.model = intern(pool, e.Message.Model)
	r.in = u.InputTokens
	r.out = u.OutputTokens
	r.cr = u.CacheReadTokens
	r.cw = u.CacheCreationTokens
	key = e.Message.ID
	if key == "" {
		// Entries without a message id are still records; the entry
		// uuid + timestamp keeps accidental copy-catches rare.
		key = "u:" + e.UUID + "|" + strconv.FormatInt(at, 10)
		if e.UUID == "" {
			key = ""
		}
	}
	return r, key, true
}
