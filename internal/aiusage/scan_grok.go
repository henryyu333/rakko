package aiusage

// Grok CLI (xAI) usage:
//
//   ~/.grok/sessions/<encoded-cwd>/<session-id>/usage.json
//   ~/.grok/sessions/<encoded-cwd>/<session-id>/updates.jsonl
//
// usage.json is the authoritative per-turn record:
//   {"sessionId":"…","updatedAt":"…",
//    "turns":[{"turnNumber":1,"endedAt":"2026-10-02T13:27:26.935164+00:00",
//              "inputTokens":904498,"outputTokens":22965,
//              "cachedReadTokens":818304,"cacheCreationTokens":0,
//              "reasoningTokens":19324,"totalTokens":927463,
//              "costUsdTicks":2445722000,"primaryModelId":"grok-4.7-build"}]}
//
// Older sessions have no usage.json; their updates.jsonl carries the same
// numbers on a turn_completed event (params.update.usage, with
// _meta.agentTimestampMs). Both are read, and a turn present in both is
// deduplicated by its end time and token tuple.
//
// inputTokens includes cachedReadTokens, so Input excludes the cached part,
// as Codex's parser does. costUsdTicks is 10^10 ticks per USD (grok
// docs/user-guide/17-sessions.md). Reasoning tokens are not added to Output,
// matching the other readers.

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

// grokUsage is one turn's or one session's numbers; the fields are a
// subset of both shapes.
type grokUsage struct {
	Input          int64                `json:"inputTokens"`
	Output         int64                `json:"outputTokens"`
	CachedRead     int64                `json:"cachedReadTokens"`
	CacheWrite     int64                `json:"cacheCreationTokens"`
	Reasoning      int64                `json:"reasoningTokens"`
	Total          int64                `json:"totalTokens"`
	CostUsdTicks   int64                `json:"costUsdTicks"`
	PrimaryModelID string               `json:"primaryModelId"`
	ModelUsage     map[string]grokUsage `json:"modelUsage"`
}

// grokUsageFile is usage.json.
type grokUsageFile struct {
	SessionID string `json:"sessionId"`
	Turns     []struct {
		grokUsage
		TurnNumber int    `json:"turnNumber"`
		EndedAt    string `json:"endedAt"`
	} `json:"turns"`
}

// grokUpdateLine is one line of updates.jsonl; only turn_completed carries
// usage.
type grokUpdateLine struct {
	Timestamp int64  `json:"timestamp"` // epoch seconds
	Method    string `json:"method"`
	Params    struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string     `json:"sessionUpdate"`
			PromptID      string     `json:"prompt_id"`
			Usage         *grokUsage `json:"usage"`
		} `json:"update"`
		Meta struct {
			EventID          string `json:"eventId"`
			AgentTimestampMs int64  `json:"agentTimestampMs"`
		} `json:"_meta"`
	} `json:"params"`
}

// parseGrokUsageFile decodes a whole usage.json into one record per turn.
func parseGrokUsageFile(data []byte, pool map[string]*string) ([]rec, error) {
	var f grokUsageFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	out := make([]rec, 0, len(f.Turns))
	for _, t := range f.Turns {
		at, err := time.Parse(time.RFC3339Nano, t.EndedAt)
		if err != nil {
			continue
		}
		r, ok := grokRecord(t.grokUsage, at.UnixMilli(), pool)
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// parseGrokUpdateLine decodes one turn_completed line.
func parseGrokUpdateLine(line []byte, pool map[string]*string) (r rec, key string, ok bool) {
	if !bytes.Contains(line, []byte(`turn_completed`)) {
		return r, "", false
	}
	var e grokUpdateLine
	if json.Unmarshal(line, &e) != nil {
		return r, "", false
	}
	if e.Params.Update.SessionUpdate != "turn_completed" || e.Params.Update.Usage == nil {
		return r, "", false
	}
	at := e.Params.Meta.AgentTimestampMs
	if at == 0 {
		at = e.Timestamp * 1000
	}
	if at == 0 {
		return r, "", false
	}
	r, ok = grokRecord(*e.Params.Update.Usage, at, pool)
	if !ok {
		return r, "", false
	}
	return r, r.key, true
}

// grokRecord turns one turn's numbers into a record and its dedupe key.
func grokRecord(u grokUsage, at int64, pool map[string]*string) (rec, bool) {
	if at == 0 {
		return rec{}, false
	}
	in := u.Input - u.CachedRead
	if in < 0 {
		in = 0
	}
	model := u.PrimaryModelID
	if model == "" && len(u.ModelUsage) > 0 {
		keys := make([]string, 0, len(u.ModelUsage))
		for k := range u.ModelUsage {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		model = keys[0]
	}
	r := rec{
		at:     at,
		model:  intern(pool, model),
		in:     in,
		out:    u.Output,
		cr:     u.CachedRead,
		cw:     u.CacheWrite,
		cost:   float64(u.CostUsdTicks) / 1e10,
		priced: true, // Grok reports the price of every turn
	}
	// Seconds are the common precision between usage.json's microsecond
	// endedAt and updates.jsonl's agentTimestampMs; the token tuple makes a
	// same-second collision harmless in practice.
	r.key = "g:" + strconv.FormatInt(at/1000, 10) + "|" +
		strconv.FormatInt(r.in, 10) + "," + strconv.FormatInt(r.out, 10) + "," +
		strconv.FormatInt(r.cr, 10) + "," + strconv.FormatInt(r.cw, 10)
	return r, true
}
