package aiusage

import (
	"context"
	"fmt"
	"math"
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

const hermesKeyPrefix = "hermes\x00"

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
LEFT JOIN sessions s ON s.id = u.session_id`

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

func hermesRowCost(row hermesRow) (cost float64, priced bool) {
	if row.ActualCost != nil && *row.ActualCost > 0 {
		return *row.ActualCost, true
	}
	if row.EstimatedCost != nil && *row.EstimatedCost > 0 {
		return *row.EstimatedCost, true
	}
	return 0, false
}

func readHermesDB(ctx context.Context, dbPath, cacheDir string, pool map[string]*string, old *fileState) ([]rec, map[string]cumSnapshot, error) {
	var rows []hermesRow
	if err := withSQLiteCopy(ctx, "hermes", dbPath, cacheDir, func(db sqliteCopy) error {
		if err := db.requireTable(ctx, "session_model_usage"); err != nil {
			return err
		}
		return db.queryJSON(ctx, hermesQuery, &rows)
	}); err != nil {
		return nil, nil, err
	}
	out, nextSnap := applyHermesRows(rows, pool, old, time.Now().UnixMilli())
	return out, nextSnap, nil
}

// applyHermesRows turns one snapshot of the cumulative `session_model_usage`
// table into ledger records. Hermes' row identity spans the whole billing
// tuple, so two models or billing modes of one session stay separate cycles.
func applyHermesRows(rows []hermesRow, pool map[string]*string, old *fileState, fallbackNowMs int64) ([]rec, map[string]cumSnapshot) {
	cums := make([]cumRow, 0, len(rows))
	for _, row := range rows {
		curCost, curPriced := hermesRowCost(row)
		cums = append(cums, cumRow{
			Key:     fmt.Sprintf("%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s", row.SessionID, row.Model, row.BillingProvider, row.BillingBaseURL, row.BillingMode, row.Task),
			Model:   intern(pool, row.Model),
			FirstAt: int64(math.Round(row.FirstTS * 1000)),
			LastAt:  int64(math.Round(row.LastTS * 1000)),
			Calls:   row.APICallCount,
			In:      row.InputTokens,
			Out:     row.OutputTokens,
			CR:      row.CacheRead,
			CW:      row.CacheWrite,
			Cost:    curCost,
			Priced:  curPriced,
		})
	}
	var ledger map[string]cumSnapshot
	var oldRecs []rec
	if old != nil {
		ledger = old.ledgerPrev
		oldRecs = old.recs
	}
	return applyCumulativeRows(cums, ledger, oldRecs, hermesKeyPrefix, fallbackNowMs)
}
