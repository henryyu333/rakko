package aiusage

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"
)

// Hermes Agent usage: $HERMES_HOME/state.db and $HERMES_HOME/profiles/*/state.db
// (default ~/.hermes/...).
//
// In Hermes's SQLite schema, `messages` only records a single `token_count`
// integer without model or input/output/cache breakdown; per-model token and
// cost counters live solely in the cumulative `session_model_usage` table
// keyed by (session_id, model, billing_provider, billing_base_url,
// billing_mode, task). Timestamps are Unix seconds (REAL).
//
// Because `session_model_usage` is cumulative, we cache the last observed
// snapshot per row key and attribute only newly added deltas to `last_seen`,
// while first-seen historical totals are attributed to the session's
// `started_at` (falling back to `first_seen` / `last_seen`).

type hermesSnapshot struct {
	Calls  int64   `json:"c"`
	In     int64   `json:"i"`
	Out    int64   `json:"o"`
	CR     int64   `json:"r"`
	CW     int64   `json:"w"`
	Cost   float64 `json:"u,omitempty"`
	Priced bool    `json:"p,omitempty"`
}

const hermesQuery = `
SELECT u.session_id,
       u.model,
       u.billing_provider,
       u.billing_base_url,
       u.billing_mode,
       u.task,
       u.api_call_count,
       u.input_tokens,
       u.output_tokens,
       u.cache_read_tokens,
       u.cache_write_tokens,
       u.estimated_cost_usd,
       u.actual_cost_usd,
       COALESCE(NULLIF(s.started_at, 0), NULLIF(u.first_seen, 0), NULLIF(u.last_seen, 0), NULLIF(s.last_activity_at, 0), NULLIF(s.ended_at, 0), 0) AS first_ts,
       COALESCE(NULLIF(u.last_seen, 0), NULLIF(s.last_activity_at, 0), NULLIF(s.ended_at, 0), NULLIF(u.first_seen, 0), NULLIF(s.started_at, 0), 0) AS last_ts
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
	APICallCount    int64    `json:"api_call_count"`
	InputTokens     int64    `json:"input_tokens"`
	OutputTokens    int64    `json:"output_tokens"`
	CacheRead       int64    `json:"cache_read_tokens"`
	CacheWrite      int64    `json:"cache_write_tokens"`
	EstimatedCost   *float64 `json:"estimated_cost_usd"`
	ActualCost      *float64 `json:"actual_cost_usd"`
	FirstTS         float64  `json:"first_ts"`
	LastTS          float64  `json:"last_ts"`
}

func readHermesDB(ctx context.Context, dbPath, cacheDir string, pool map[string]*string, old *fileState) ([]rec, map[string]hermesSnapshot, error) {
	var rows []hermesRow
	if err := readSQLiteJSON(ctx, "hermes", dbPath, cacheDir, hermesQuery, &rows); err != nil {
		return nil, nil, err
	}
	var out []rec
	var prevSnap map[string]hermesSnapshot
	if old != nil {
		out = slices.Clone(old.recs)
		prevSnap = old.hermesPrev
	}
	nextSnap := make(map[string]hermesSnapshot, len(rows))
	for _, row := range rows {
		rowKey := fmt.Sprintf("%s|%s|%s|%s|%s|%s", row.SessionID, row.Model, row.BillingProvider, row.BillingBaseURL, row.BillingMode, row.Task)
		var curCost float64
		var curPriced bool
		if row.ActualCost != nil && *row.ActualCost > 0 {
			curCost, curPriced = *row.ActualCost, true
		} else if row.EstimatedCost != nil && *row.EstimatedCost > 0 {
			curCost, curPriced = *row.EstimatedCost, true
		}

		prev, seen := prevSnap[rowKey]
		if !seen {
			at := int64(math.Round(row.FirstTS * 1000))
			if at == 0 {
				continue
			}
			reqs := row.APICallCount
			if reqs <= 0 {
				reqs = 1
			}
			out = append(out, rec{
				at:     at,
				model:  intern(pool, row.Model),
				in:     row.InputTokens,
				out:    row.OutputTokens,
				cr:     row.CacheRead,
				cw:     row.CacheWrite,
				cost:   curCost,
				priced: curPriced,
				reqs:   reqs,
			})
		} else {
			dIn := max(0, row.InputTokens-prev.In)
			dOut := max(0, row.OutputTokens-prev.Out)
			dCR := max(0, row.CacheRead-prev.CR)
			dCW := max(0, row.CacheWrite-prev.CW)
			dCalls := max(0, row.APICallCount-prev.Calls)
			dCost := max(0.0, curCost-prev.Cost)
			if dIn > 0 || dOut > 0 || dCR > 0 || dCW > 0 || dCalls > 0 {
				at := int64(math.Round(row.LastTS * 1000))
				if at == 0 {
					at = time.Now().UnixMilli()
				}
				reqs := dCalls
				if reqs <= 0 {
					reqs = 1
				}
				out = append(out, rec{
					at:     at,
					model:  intern(pool, row.Model),
					in:     dIn,
					out:    dOut,
					cr:     dCR,
					cw:     dCW,
					cost:   dCost,
					priced: curPriced,
					reqs:   reqs,
				})
			}
		}
		nextSnap[rowKey] = hermesSnapshot{
			Calls:  row.APICallCount,
			In:     row.InputTokens,
			Out:    row.OutputTokens,
			CR:     row.CacheRead,
			CW:     row.CacheWrite,
			Cost:   curCost,
			Priced: curPriced,
		}
	}
	return out, nextSnap, nil
}
