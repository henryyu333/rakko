package aiusage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Copilot CLI / Desktop SQLite usage:
//   - ~/.copilot/session-store.db
//   - ~/.copilot/data.db
//
// Both may contain `assistant_usage_events` with per-request model, timestamp
// (`created_at` or `timestamp`), and token counts (either in
// `token_details_json` or top-level `input_tokens` / `output_tokens` /
// `cache_read_tokens` / `cache_write_tokens` columns). Databases without
// `assistant_usage_events` (e.g. session-store before telemetry was added)
// are treated as empty rather than failing.

func readCopilotDB(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) ([]rec, error) {
	var tables []struct {
		Name string `json:"name"`
	}
	const tableCheck = `SELECT name FROM sqlite_master WHERE type='table' AND name='assistant_usage_events'`
	if err := readSQLiteJSON(ctx, "copilot", dbPath, cacheDir, tableCheck, &tables); err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, nil
	}

	var rows []map[string]any
	if err := readSQLiteJSON(ctx, "copilot", dbPath, cacheDir, `SELECT rowid AS _rowid, * FROM assistant_usage_events`, &rows); err != nil {
		return nil, err
	}
	dbBase := filepath.Base(dbPath)
	out := make([]rec, 0, len(rows))
	for _, row := range rows {
		at := copilotRowMillis(row["created_at"])
		if at == 0 {
			at = copilotRowMillis(row["timestamp"])
		}
		if at == 0 {
			continue
		}
		model, _ := row["model"].(string)
		in, outTok, cr, cw := copilotRowTokens(row)
		if in+outTok+cr+cw <= 0 {
			continue
		}
		sessionID, _ := row["session_id"].(string)
		rowID := copilotInt64(row["_rowid"])
		out = append(out, rec{
			at:    at,
			model: intern(pool, model),
			in:    in,
			out:   outTok,
			cr:    cr,
			cw:    cw,
			key:   fmt.Sprintf("copilot:%s|%s|%d|%d|%s", dbBase, sessionID, rowID, at, model),
		})
	}
	return out, nil
}

func copilotRowMillis(v any) int64 {
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return 0
		}
		raw, err := json.Marshal(x)
		if err != nil {
			return 0
		}
		return parseMillis(raw)
	case float64:
		if x <= 0 {
			return 0
		}
		if x < 1e12 {
			return int64(x * 1000)
		}
		return int64(x)
	}
	return 0
}

func copilotInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	}
	return 0
}

func copilotRowTokens(row map[string]any) (in, out, cr, cw int64) {
	var details map[string]any
	switch td := row["token_details_json"].(type) {
	case string:
		if strings.TrimSpace(td) != "" {
			_ = json.Unmarshal([]byte(td), &details)
		}
	case map[string]any:
		details = td
	}
	if len(details) > 0 {
		in = firstInt64(details, "fresh_input_tokens", "uncached_input_tokens", "input_tokens", "inputTokens")
		out = firstInt64(details, "output_tokens", "outputTokens")
		cr = firstInt64(details, "cache_read_tokens", "cacheReadTokens", "cached_input_tokens", "cache_read_input_tokens")
		cw = firstInt64(details, "cache_write_tokens", "cacheWriteTokens", "cache_creation_input_tokens")
		if in+out+cr+cw > 0 {
			return in, out, cr, cw
		}
	}
	in = firstInt64(row, "fresh_input_tokens", "uncached_input_tokens", "input_tokens")
	out = firstInt64(row, "output_tokens")
	cr = firstInt64(row, "cache_read_tokens", "cached_input_tokens", "cache_read_input_tokens")
	cw = firstInt64(row, "cache_write_tokens", "cache_creation_input_tokens")
	return in, out, cr, cw
}

func firstInt64(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if n := copilotInt64(v); n > 0 {
				return n
			}
		}
	}
	return 0
}
