package aiusage

import (
	"context"
	"fmt"
	"math"
)

// Hermes Agent usage: $HERMES_HOME/state.db and $HERMES_HOME/profiles/*/state.db
// (default ~/.hermes/...).
//
// session_model_usage aggregates token counts per (session_id, model,
// billing_provider, billing_base_url, billing_mode, task). Timestamps are
// Unix seconds (REAL).

const hermesQuery = `
SELECT u.session_id,
       u.model,
       u.billing_provider,
       u.billing_base_url,
       u.billing_mode,
       u.task,
       u.input_tokens,
       u.output_tokens,
       u.cache_read_tokens,
       u.cache_write_tokens,
       u.estimated_cost_usd,
       u.actual_cost_usd,
       COALESCE(u.last_seen, u.first_seen, s.last_activity_at, s.ended_at, s.started_at, 0) AS ts
FROM session_model_usage u
LEFT JOIN sessions s ON s.id = u.session_id
WHERE u.input_tokens > 0 OR u.output_tokens > 0 OR u.cache_read_tokens > 0 OR u.cache_write_tokens > 0`

type hermesRow struct {
	SessionID       string   `json:"session_id"`
	Model           string   `json:"model"`
	BillingProvider string   `json:"billing_provider"`
	BillingBaseURL  string   `json:"billing_base_url"`
	BillingMode     string   `json:"billing_mode"`
	Task            string   `json:"task"`
	InputTokens     int64    `json:"input_tokens"`
	OutputTokens    int64    `json:"output_tokens"`
	CacheRead       int64    `json:"cache_read_tokens"`
	CacheWrite      int64    `json:"cache_write_tokens"`
	EstimatedCost   *float64 `json:"estimated_cost_usd"`
	ActualCost      *float64 `json:"actual_cost_usd"`
	TS              float64  `json:"ts"`
}

func readHermesDB(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) ([]rec, error) {
	var rows []hermesRow
	if err := readSQLiteJSON(ctx, "hermes", dbPath, cacheDir, hermesQuery, &rows); err != nil {
		return nil, err
	}
	out := make([]rec, 0, len(rows))
	for _, row := range rows {
		at := int64(math.Round(row.TS * 1000))
		if at == 0 {
			continue
		}
		r := rec{
			at:    at,
			model: intern(pool, row.Model),
			in:    row.InputTokens,
			out:   row.OutputTokens,
			cr:    row.CacheRead,
			cw:    row.CacheWrite,
			key:   fmt.Sprintf("hermes:%s|%s|%s|%s|%s|%s", row.SessionID, row.Model, row.BillingProvider, row.BillingBaseURL, row.BillingMode, row.Task),
		}
		if row.ActualCost != nil && *row.ActualCost > 0 {
			r.cost, r.priced = *row.ActualCost, true
		} else if row.EstimatedCost != nil && *row.EstimatedCost > 0 {
			r.cost, r.priced = *row.EstimatedCost, true
		}
		out = append(out, r)
	}
	return out, nil
}
