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

// hermesSnapshot stores per-row cumulative state for one `session_model_usage`
// row. To avoid duplicating records in the cache, the actual usage records live
// solely in `fileState.recs` tagged with `rec.key = "hermes\x00<rowKey>\x00<epoch>\x00<seq>"`
// (where `seq` makes every record's key unique for Summary's dedupe pass) and
// `rec.reqs = zeroReqs` (-1) for 0-request cost-only reconciliation records.
type hermesSnapshot struct {
	Epoch  int     `json:"e"`
	Calls  int64   `json:"c"`
	In     int64   `json:"i"`
	Out    int64   `json:"o"`
	CR     int64   `json:"r"`
	CW     int64   `json:"w"`
	Cost   float64 `json:"u,omitempty"`
	Priced bool    `json:"p,omitempty"`
}

const (
	hermesKeyPrefix = "hermes\x00"
	zeroReqs        = int64(-1)
)

func makeHermesRecKey(rowKey string, epoch, seq int) string {
	return fmt.Sprintf("%s%s\x00%d\x00%d", hermesKeyPrefix, rowKey, epoch, seq)
}

func parseHermesRecKey(k string) (rowKey string, epoch int, ok bool) {
	if len(k) <= len(hermesKeyPrefix) || k[:len(hermesKeyPrefix)] != hermesKeyPrefix {
		return "", 0, false
	}
	rest := k[len(hermesKeyPrefix):]
	lastSep := -1
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i] == '\x00' {
			lastSep = i
			break
		}
	}
	if lastSep <= 0 {
		return "", 0, false
	}
	var seq int
	if _, err := fmt.Sscanf(rest[lastSep+1:], "%d", &seq); err != nil {
		return "", 0, false
	}
	prefix := rest[:lastSep]
	epochSep := -1
	for i := len(prefix) - 1; i >= 0; i-- {
		if prefix[i] == '\x00' {
			epochSep = i
			break
		}
	}
	if epochSep < 0 {
		return "", 0, false
	}
	var ep int
	if _, err := fmt.Sscanf(prefix[epochSep+1:], "%d", &ep); err != nil {
		return "", 0, false
	}
	return prefix[:epochSep], ep, true
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

func hermesRowPositive(row hermesRow, priced bool) bool {
	return row.InputTokens > 0 || row.OutputTokens > 0 || row.CacheRead > 0 || row.CacheWrite > 0 || row.APICallCount > 0 || priced
}

func hermesEpochReqs(calls int64, hasTokens bool) int64 {
	if calls > 0 {
		return calls
	}
	if hasTokens {
		return 1
	}
	return zeroReqs
}

func readHermesDB(ctx context.Context, dbPath, cacheDir string, pool map[string]*string, old *fileState) ([]rec, map[string]hermesSnapshot, error) {
	if err := requireSQLiteTable(ctx, "hermes", dbPath, cacheDir, "session_model_usage"); err != nil {
		return nil, nil, err
	}
	var rows []hermesRow
	if err := readSQLiteJSON(ctx, "hermes", dbPath, cacheDir, hermesQuery, &rows); err != nil {
		return nil, nil, err
	}
	out, nextSnap := applyHermesRows(rows, pool, old, time.Now().UnixMilli())
	return out, nextSnap, nil
}

func applyHermesRows(rows []hermesRow, pool map[string]*string, old *fileState, fallbackNowMs int64) ([]rec, map[string]hermesSnapshot) {
	var prevSnap map[string]hermesSnapshot
	byRow := map[string][]rec{}
	if old != nil && len(old.hermesPrev) > 0 {
		prevSnap = old.hermesPrev
		for _, rc := range old.recs {
			rk, _, ok := parseHermesRecKey(rc.key)
			if !ok {
				// Corrupt or pre-v5 record lacking ownership key: drop cached
				// state and rebuild cleanly from the current snapshot.
				prevSnap = nil
				byRow = map[string][]rec{}
				break
			}
			byRow[rk] = append(byRow[rk], rc)
		}
	}

	var out []rec
	nextSnap := make(map[string]hermesSnapshot, len(rows))
	for _, row := range rows {
		rowKey := fmt.Sprintf("%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s",
			row.SessionID, row.Model, row.BillingProvider, row.BillingBaseURL, row.BillingMode, row.Task)
		curCost, curPriced := hermesRowCost(row)
		hasTokens := row.InputTokens > 0 || row.OutputTokens > 0 || row.CacheRead > 0 || row.CacheWrite > 0
		isPositive := hermesRowPositive(row, curPriced)

		prev, seen := prevSnap[rowKey]
		prevRecs := byRow[rowKey]
		// If a positive snapshot existed without any owned records (e.g. corrupt
		// cache), treat as unseen and rebuild from the current snapshot.
		if seen && len(prevRecs) == 0 && (prev.In > 0 || prev.Out > 0 || prev.CR > 0 || prev.CW > 0 || prev.Calls > 0 || prev.Priced) {
			seen = false
		}

		if !seen {
			if !isPositive {
				nextSnap[rowKey] = hermesSnapshot{Epoch: 0}
				continue
			}
			at := int64(math.Round(row.FirstTS * 1000))
			if at == 0 {
				at = int64(math.Round(row.LastTS * 1000))
			}
			if at == 0 {
				continue
			}
			rc := rec{
				at:     at,
				model:  intern(pool, row.Model),
				in:     max(int64(0), row.InputTokens),
				out:    max(int64(0), row.OutputTokens),
				cr:     max(int64(0), row.CacheRead),
				cw:     max(int64(0), row.CacheWrite),
				cost:   curCost,
				priced: curPriced,
				key:    makeHermesRecKey(rowKey, 0, 0),
				reqs:   hermesEpochReqs(row.APICallCount, hasTokens),
			}
			out = append(out, rc)
			nextSnap[rowKey] = hermesSnapshot{
				Epoch:  0,
				Calls:  max(int64(0), row.APICallCount),
				In:     max(int64(0), row.InputTokens),
				Out:    max(int64(0), row.OutputTokens),
				CR:     max(int64(0), row.CacheRead),
				CW:     max(int64(0), row.CacheWrite),
				Cost:   curCost,
				Priced: curPriced,
			}
			continue
		}

		recs := slices.Clone(prevRecs)
		epoch := prev.Epoch
		reset := row.InputTokens < prev.In ||
			row.OutputTokens < prev.Out ||
			row.CacheRead < prev.CR ||
			row.CacheWrite < prev.CW ||
			row.APICallCount < prev.Calls

		lastAt := int64(math.Round(row.LastTS * 1000))
		if lastAt == 0 {
			lastAt = int64(math.Round(row.FirstTS * 1000))
		}
		if lastAt == 0 {
			lastAt = fallbackNowMs
		}

		var nextPriced bool
		var nextCost float64
		if reset {
			epoch++
			nextPriced = curPriced
			nextCost = curCost
			if isPositive {
				recs = append(recs, rec{
					at:     lastAt,
					model:  intern(pool, row.Model),
					in:     max(int64(0), row.InputTokens),
					out:    max(int64(0), row.OutputTokens),
					cr:     max(int64(0), row.CacheRead),
					cw:     max(int64(0), row.CacheWrite),
					cost:   curCost,
					priced: curPriced,
					key:    makeHermesRecKey(rowKey, epoch, len(recs)),
					reqs:   hermesEpochReqs(row.APICallCount, hasTokens),
				})
			}
		} else {
			nextPriced = prev.Priced || curPriced
			if curPriced {
				nextCost = curCost
			} else {
				nextCost = prev.Cost
			}

			// If this epoch transitioned from unpriced to source-priced, upgrade
			// every earlier record of the current epoch to priced ($0) so those
			// tokens are not double-priced via LiteLLM alongside curCost.
			var epochCostSum float64
			var epochReqsSum int64
			lastEpochIdx := -1
			for i := range recs {
				_, recEpoch, ok := parseHermesRecKey(recs[i].key)
				if !ok || recEpoch != epoch {
					continue
				}
				if nextPriced && !recs[i].priced {
					recs[i].priced = true
					recs[i].cost = 0
				}
				epochCostSum += recs[i].cost
				if recs[i].reqs != zeroReqs {
					if recs[i].reqs > 0 {
						epochReqsSum += recs[i].reqs
					} else {
						epochReqsSum++
					}
				}
				lastEpochIdx = i
			}

			dIn := row.InputTokens - prev.In
			dOut := row.OutputTokens - prev.Out
			dCR := row.CacheRead - prev.CR
			dCW := row.CacheWrite - prev.CW
			dCalls := row.APICallCount - prev.Calls
			dTokens := dIn > 0 || dOut > 0 || dCR > 0 || dCW > 0
			dCost := nextCost - epochCostSum
			targetEpochReqs := hermesEpochReqs(row.APICallCount, hasTokens)
			if targetEpochReqs == zeroReqs {
				targetEpochReqs = 0
			}

			if dTokens || dCalls > 0 {
				reqs := max(int64(0), targetEpochReqs-epochReqsSum)
				if reqs == 0 {
					reqs = zeroReqs
				}
				recs = append(recs, rec{
					at:     lastAt,
					model:  intern(pool, row.Model),
					in:     dIn,
					out:    dOut,
					cr:     dCR,
					cw:     dCW,
					cost:   dCost,
					priced: nextPriced,
					key:    makeHermesRecKey(rowKey, epoch, len(recs)),
					reqs:   reqs,
				})
			} else if nextPriced && math.Abs(dCost) > 1e-15 {
				if lastEpochIdx >= 0 {
					recs[lastEpochIdx].cost += dCost
					recs[lastEpochIdx].priced = true
				} else {
					recs = append(recs, rec{
						at:     lastAt,
						model:  intern(pool, row.Model),
						cost:   dCost,
						priced: true,
						key:    makeHermesRecKey(rowKey, epoch, len(recs)),
						reqs:   zeroReqs,
					})
				}
			}
		}

		out = append(out, recs...)
		nextSnap[rowKey] = hermesSnapshot{
			Epoch:  epoch,
			Calls:  max(int64(0), row.APICallCount),
			In:     max(int64(0), row.InputTokens),
			Out:    max(int64(0), row.OutputTokens),
			CR:     max(int64(0), row.CacheRead),
			CW:     max(int64(0), row.CacheWrite),
			Cost:   nextCost,
			Priced: nextPriced,
		}
	}
	return out, nextSnap
}
