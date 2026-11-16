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

type hermesRec struct {
	At     int64   `json:"a"`
	Model  string  `json:"m,omitempty"`
	In     int64   `json:"i,omitempty"`
	Out    int64   `json:"o,omitempty"`
	CR     int64   `json:"r,omitempty"`
	CW     int64   `json:"w,omitempty"`
	Cost   float64 `json:"u,omitempty"`
	Priced bool    `json:"p,omitempty"`
	Reqs   int64   `json:"n,omitempty"`
}

type hermesSnapshot struct {
	Calls  int64       `json:"c"`
	In     int64       `json:"i"`
	Out    int64       `json:"o"`
	CR     int64       `json:"r"`
	CW     int64       `json:"w"`
	Cost   float64     `json:"u,omitempty"`
	Priced bool        `json:"p,omitempty"`
	Recs   []hermesRec `json:"recs,omitempty"`
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
	if err := requireSQLiteTable(ctx, "hermes", dbPath, cacheDir, "session_model_usage"); err != nil {
		return nil, nil, err
	}
	var rows []hermesRow
	if err := readSQLiteJSON(ctx, "hermes", dbPath, cacheDir, hermesQuery, &rows); err != nil {
		return nil, nil, err
	}
	var prevSnap map[string]hermesSnapshot
	if old != nil {
		prevSnap = old.hermesPrev
	}
	var out []rec
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
		var recs []hermesRec
		if !seen || len(prev.Recs) == 0 {
			at := int64(math.Round(row.FirstTS * 1000))
			if at == 0 {
				continue
			}
			reqs := row.APICallCount
			if reqs <= 0 {
				reqs = 1
			}
			recs = []hermesRec{{
				At:     at,
				Model:  row.Model,
				In:     row.InputTokens,
				Out:    row.OutputTokens,
				CR:     row.CacheRead,
				CW:     row.CacheWrite,
				Cost:   curCost,
				Priced: curPriced,
				Reqs:   reqs,
			}}
		} else {
			recs = slices.Clone(prev.Recs)
			reset := row.InputTokens < prev.In ||
				row.OutputTokens < prev.Out ||
				row.CacheRead < prev.CR ||
				row.CacheWrite < prev.CW ||
				row.APICallCount < prev.Calls
			lastAt := int64(math.Round(row.LastTS * 1000))
			if lastAt == 0 {
				lastAt = time.Now().UnixMilli()
			}
			if reset {
				// Counter reset / rollback: preserve previously recorded usage and
				// count the new cumulative total from 0.
				reqs := row.APICallCount
				if reqs <= 0 {
					reqs = 1
				}
				recs = append(recs, hermesRec{
					At:     lastAt,
					Model:  row.Model,
					In:     row.InputTokens,
					Out:    row.OutputTokens,
					CR:     row.CacheRead,
					CW:     row.CacheWrite,
					Cost:   curCost,
					Priced: curPriced,
					Reqs:   reqs,
				})
			} else {
				dIn := row.InputTokens - prev.In
				dOut := row.OutputTokens - prev.Out
				dCR := row.CacheRead - prev.CR
				dCW := row.CacheWrite - prev.CW
				dCalls := row.APICallCount - prev.Calls
				dCost := curCost - prev.Cost
				if dIn > 0 || dOut > 0 || dCR > 0 || dCW > 0 || dCalls > 0 {
					reqs := dCalls
					if reqs <= 0 {
						reqs = 1
					}
					recs = append(recs, hermesRec{
						At:     lastAt,
						Model:  row.Model,
						In:     dIn,
						Out:    dOut,
						CR:     dCR,
						CW:     dCW,
						Cost:   dCost,
						Priced: curPriced || prev.Priced,
						Reqs:   reqs,
					})
				} else if dCost != 0 || curPriced != prev.Priced {
					// Cost-only adjustment: reconcile on the latest record without
					// adding a new request.
					last := &recs[len(recs)-1]
					last.Cost += dCost
					if curPriced || prev.Priced {
						last.Priced = true
					}
				}
			}
		}
		for _, hr := range recs {
			out = append(out, rec{
				at:     hr.At,
				model:  intern(pool, hr.Model),
				in:     hr.In,
				out:    hr.Out,
				cr:     hr.CR,
				cw:     hr.CW,
				cost:   hr.Cost,
				priced: hr.Priced,
				reqs:   hr.Reqs,
			})
		}
		nextSnap[rowKey] = hermesSnapshot{
			Calls:  row.APICallCount,
			In:     row.InputTokens,
			Out:    row.OutputTokens,
			CR:     row.CacheRead,
			CW:     row.CacheWrite,
			Cost:   curCost,
			Priced: curPriced,
			Recs:   recs,
		}
	}
	return out, nextSnap, nil
}
