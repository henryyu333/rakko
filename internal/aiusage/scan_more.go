package aiusage

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
)

// Shared flexible usage block for JSONL/SQLite/OTEL tools (Droid, Kimi, Junie,
// Copilot OTEL, Kiro, Claude, Devin, Antigravity, LM Studio) whose entries carry
// token counts under either snake_case or camelCase keys.
type flexUsageJSON struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadAlt        int64 `json:"cache_read_tokens"`
	CacheWriteAlt       int64 `json:"cache_write_tokens"`
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	InputCamel          int64 `json:"inputTokens"`
	OutputCamel         int64 `json:"outputTokens"`
	CacheReadCamel      int64 `json:"cacheReadTokens"`
	CacheWriteCamel     int64 `json:"cacheWriteTokens"`
	CachedTokens        int64 `json:"cached_tokens"`
}

func (u *flexUsageJSON) toCounts() (in, out, cr, cw int64) {
	in = max(u.InputTokens, u.PromptTokens, u.InputCamel)
	out = max(u.OutputTokens, u.CompletionTokens, u.OutputCamel)
	cr = max(u.CacheReadTokens, u.CacheReadAlt, u.CacheReadCamel, u.CachedTokens)
	cw = max(u.CacheCreationTokens, u.CacheWriteAlt, u.CacheWriteCamel)
	return in, out, cr, cw
}

// parseFlexJSONLLine parses one JSONL line for tools like Droid, Kimi, Junie,
// Kiro CLI, Cline JSONL, and LM Studio server logs. When freeCost is true, local
// inference records $0 cost (priced=true) regardless of any cost field.
func parseFlexJSONLLine(prefix string, line []byte, pool map[string]*string, freeCost bool) (r rec, key string, ok bool) {
	if !bytes.Contains(line, []byte(`"usage"`)) && (freeCost || !bytes.Contains(line, []byte(`"tokens"`))) {
		return r, "", false
	}
	var e struct {
		ID        string          `json:"id"`
		UUID      string          `json:"uuid"`
		MessageID string          `json:"message_id"`
		RequestID string          `json:"request_id"`
		EventID   string          `json:"event_id"`
		Type      string          `json:"type"`
		Model     string          `json:"model"`
		Timestamp json.RawMessage `json:"timestamp"`
		CreatedAt json.RawMessage `json:"created_at"`
		Time      json.RawMessage `json:"time"`
		Created   json.RawMessage `json:"created"`
		Cost      *float64        `json:"cost"`
		Usage     *flexUsageJSON  `json:"usage"`
		Tokens    *flexUsageJSON  `json:"tokens"`
		Message   *struct {
			ID    string         `json:"id"`
			Role  string         `json:"role"`
			Model string         `json:"model"`
			Usage *flexUsageJSON `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil {
		return r, "", false
	}
	u := e.Usage
	model := e.Model
	msgID := e.ID
	if !freeCost {
		if u == nil && e.Tokens != nil {
			u = e.Tokens
		}
		if msgID == "" {
			msgID = e.UUID
		}
		if msgID == "" {
			msgID = e.MessageID
		}
		if msgID == "" {
			msgID = e.RequestID
		}
		if msgID == "" {
			msgID = e.EventID
		}
		if e.Message != nil {
			if u == nil {
				u = e.Message.Usage
			}
			if model == "" {
				model = e.Message.Model
			}
			if msgID == "" {
				msgID = e.Message.ID
			}
		}
	}
	if u == nil {
		return r, "", false
	}
	in, out, cr, cw := u.toCounts()
	if in == 0 && out == 0 && cr == 0 && cw == 0 {
		return r, "", false
	}
	at := parseMillis(e.Timestamp)
	if at == 0 && !freeCost {
		at = parseMillis(e.CreatedAt)
	}
	if at == 0 && !freeCost {
		at = parseMillis(e.Time)
	}
	if at == 0 && freeCost {
		at = parseMillis(e.Created)
	}
	if at == 0 {
		return r, "", false
	}
	r = rec{
		at:    at,
		model: intern(pool, model),
		in:    in,
		out:   out,
		cr:    cr,
		cw:    cw,
	}
	if freeCost {
		r.cost, r.priced = 0, true
	} else if e.Cost != nil {
		r.cost, r.priced = *e.Cost, true
	}
	if msgID != "" {
		key = prefix + ":" + msgID
	}
	return r, key, true
}

// parseClineUIMessagesFile parses Cline / Roo Code / Kilo VS Code extension
// ui_messages.json files, where "api_req_started" entries hold a JSON string
// in .text with tokensIn, tokensOut, cacheReads, cacheWrites, cost, and model.
func parseClineUIMessagesFile(prefix, path string, data []byte, pool map[string]*string) ([]rec, error) {
	var msgs []struct {
		TS   int64  `json:"ts"`
		Type string `json:"type"`
		Say  string `json:"say"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, err
	}
	taskID := filepath.Base(filepath.Dir(path))
	var out []rec
	for i, m := range msgs {
		if m.Say != "api_req_started" || m.TS == 0 || !strings.Contains(m.Text, "{") {
			continue
		}
		var payload struct {
			TokensIn    int64    `json:"tokensIn"`
			TokensOut   int64    `json:"tokensOut"`
			CacheReads  int64    `json:"cacheReads"`
			CacheWrites int64    `json:"cacheWrites"`
			Cost        *float64 `json:"cost"`
			Model       string   `json:"model"`
		}
		if json.Unmarshal([]byte(m.Text), &payload) != nil {
			continue
		}
		if payload.TokensIn == 0 && payload.TokensOut == 0 && payload.CacheReads == 0 && payload.CacheWrites == 0 {
			continue
		}
		r := rec{
			at:    m.TS,
			model: intern(pool, payload.Model),
			in:    payload.TokensIn,
			out:   payload.TokensOut,
			cr:    payload.CacheReads,
			cw:    payload.CacheWrites,
			key:   prefix + ":" + taskID + ":" + strconv.Itoa(i),
		}
		if payload.Cost != nil && *payload.Cost > 0 {
			r.cost, r.priced = *payload.Cost, true
		}
		out = append(out, r)
	}
	return out, nil
}

// parseAuggieSessionFile parses ~/.augment/sessions/*.json whole-session files.
func parseAuggieSessionFile(path string, data []byte, pool map[string]*string) ([]rec, error) {
	var doc struct {
		SessionID   string `json:"sessionId"`
		Model       string `json:"model"`
		ChatHistory []struct {
			ID         string          `json:"id"`
			Timestamp  json.RawMessage `json:"timestamp"`
			Model      string          `json:"model"`
			Usage      *flexUsageJSON  `json:"usage"`
			TokenUsage *flexUsageJSON  `json:"tokenUsage"`
		} `json:"chatHistory"`
		Messages []struct {
			ID         string          `json:"id"`
			Timestamp  json.RawMessage `json:"timestamp"`
			Model      string          `json:"model"`
			Usage      *flexUsageJSON  `json:"usage"`
			TokenUsage *flexUsageJSON  `json:"tokenUsage"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	sid := doc.SessionID
	if sid == "" {
		sid = filepath.Base(path)
	}
	var out []rec
	add := func(idx int, id string, ts json.RawMessage, m string, u1, u2 *flexUsageJSON) {
		u := u1
		if u == nil {
			u = u2
		}
		if u == nil {
			return
		}
		in, o, cr, cw := u.toCounts()
		if in == 0 && o == 0 && cr == 0 && cw == 0 {
			return
		}
		at := parseMillis(ts)
		if at == 0 {
			return
		}
		if m == "" {
			m = doc.Model
		}
		k := "auggie:" + sid + ":" + strconv.Itoa(idx)
		if id != "" {
			k = "auggie:" + sid + ":" + id
		}
		out = append(out, rec{
			at:    at,
			model: intern(pool, m),
			in:    in,
			out:   o,
			cr:    cr,
			cw:    cw,
			key:   k,
		})
	}
	for i, ch := range doc.ChatHistory {
		add(i, ch.ID, ch.Timestamp, ch.Model, ch.Usage, ch.TokenUsage)
	}
	for i, m := range doc.Messages {
		add(i, m.ID, m.Timestamp, m.Model, m.Usage, m.TokenUsage)
	}
	return out, nil
}
