package aiusage

import (
	"fmt"
	"math"
	"slices"
)

// Cumulative-counter ledgers
//
// Some sources expose only per-row cumulative counters: Hermes'
// session_model_usage table and Copilot's data.db `sessions` totals. A row
// never says when its tokens were spent, only how many it holds now, so the
// scanner caches the last observed snapshot per row key and attributes the
// newly added delta to the observation timestamp; a first-seen total is
// attributed to the row's start time. A counter that drops starts a new cycle
// (epoch), and a row that disappears from the table removes its records.
//
// The records live solely in fileState.recs, tagged with the row key, epoch
// and a sequence number, so a cache round-trip can rebuild each row's history
// without duplicating it in the snapshot.

// zeroReqs marks a ledger record that carries tokens or cost but no request
// (rec.reqs's zero value means one request).
const zeroReqs = int64(-1)

// cumSnapshot is one row's last-seen cumulative counters plus the ledger
// position (epoch) they belong to.
type cumSnapshot struct {
	Epoch  int     `json:"e"`
	Calls  int64   `json:"c"`
	In     int64   `json:"i"`
	Out    int64   `json:"o"`
	CR     int64   `json:"r"`
	CW     int64   `json:"w"`
	Cost   float64 `json:"u,omitempty"`
	Priced bool    `json:"p,omitempty"`
}

func (s cumSnapshot) positive() bool {
	return s.In > 0 || s.Out > 0 || s.CR > 0 || s.CW > 0 || s.Calls > 0 || s.Priced
}

// cumRow is one cumulative row as observed by this scan.
type cumRow struct {
	Key     string
	Model   *string
	FirstAt int64 // Unix millis; first-seen attribution, falling back to LastAt
	LastAt  int64 // Unix millis; delta attribution, falling back to FirstAt
	Calls   int64
	In      int64
	Out     int64
	CR      int64
	CW      int64
	Cost    float64
	Priced  bool
}

func (r cumRow) hasTokens() bool {
	return r.In > 0 || r.Out > 0 || r.CR > 0 || r.CW > 0
}

func (r cumRow) positive() bool {
	return r.hasTokens() || r.Calls > 0 || r.Priced
}

// targetReqs is the row's cumulative request count inside its current cycle:
// the call count when the source reports one, otherwise one request per row
// that has tokens, otherwise no request (a cost-only row).
func (r cumRow) targetReqs() int64 {
	return cumEpochReqs(r.Calls, r.hasTokens())
}

func cumEpochReqs(calls int64, hasTokens bool) int64 {
	if calls > 0 {
		return calls
	}
	if hasTokens {
		return 1
	}
	return zeroReqs
}

// makeLedgerRecKey names one ledger record. seq keeps every record's key
// unique for Summary's dedupe pass.
func makeLedgerRecKey(prefix, rowKey string, epoch, seq int) string {
	return fmt.Sprintf("%s%s\x00%d\x00%d", prefix, rowKey, epoch, seq)
}

func parseLedgerRecKey(prefix, k string) (rowKey string, epoch int, ok bool) {
	if len(k) <= len(prefix) || k[:len(prefix)] != prefix {
		return "", 0, false
	}
	rest := k[len(prefix):]
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
	prefixKey := rest[:lastSep]
	epochSep := -1
	for i := len(prefixKey) - 1; i >= 0; i-- {
		if prefixKey[i] == '\x00' {
			epochSep = i
			break
		}
	}
	if epochSep < 0 {
		return "", 0, false
	}
	var ep int
	if _, err := fmt.Sscanf(prefixKey[epochSep+1:], "%d", &ep); err != nil {
		return "", 0, false
	}
	return prefixKey[:epochSep], ep, true
}

// applyCumulativeRows folds one scan's observations into the cached ledger.
// oldRecs are the records that belong to ledger; every returned record is the
// complete, new history (the caller replaces its record set with it).
func applyCumulativeRows(rows []cumRow, ledger map[string]cumSnapshot, oldRecs []rec, prefix string, fallbackNowMs int64) ([]rec, map[string]cumSnapshot) {
	prevSnap := ledger
	byRow := map[string][]rec{}
	if len(prevSnap) > 0 {
		for _, rc := range oldRecs {
			rk, _, ok := parseLedgerRecKey(prefix, rc.key)
			if !ok {
				// Corrupt or pre-ledger record lacking ownership key: drop the
				// cached state and rebuild cleanly from the current snapshot.
				prevSnap = nil
				byRow = map[string][]rec{}
				break
			}
			byRow[rk] = append(byRow[rk], rc)
		}
	}

	out := make([]rec, 0, len(rows))
	nextSnap := make(map[string]cumSnapshot, len(rows))
	for _, row := range rows {
		prev, seen := prevSnap[row.Key]
		prevRecs := byRow[row.Key]
		// A positive snapshot without owned records (e.g. a corrupt cache) is
		// treated as unseen and rebuilt from the current snapshot.
		if seen && len(prevRecs) == 0 && prev.positive() {
			seen = false
		}

		if !seen {
			if !row.positive() {
				nextSnap[row.Key] = cumSnapshot{Epoch: 0}
				continue
			}
			at := row.FirstAt
			if at == 0 {
				at = row.LastAt
			}
			if at == 0 {
				continue
			}
			out = append(out, rec{
				at:     at,
				model:  row.Model,
				in:     max(int64(0), row.In),
				out:    max(int64(0), row.Out),
				cr:     max(int64(0), row.CR),
				cw:     max(int64(0), row.CW),
				cost:   row.Cost,
				priced: row.Priced,
				key:    makeLedgerRecKey(prefix, row.Key, 0, 0),
				reqs:   row.targetReqs(),
			})
			nextSnap[row.Key] = cumSnapshot{
				Epoch:  0,
				Calls:  max(int64(0), row.Calls),
				In:     max(int64(0), row.In),
				Out:    max(int64(0), row.Out),
				CR:     max(int64(0), row.CR),
				CW:     max(int64(0), row.CW),
				Cost:   row.Cost,
				Priced: row.Priced,
			}
			continue
		}

		recs := slices.Clone(prevRecs)
		epoch := prev.Epoch
		reset := row.In < prev.In ||
			row.Out < prev.Out ||
			row.CR < prev.CR ||
			row.CW < prev.CW ||
			row.Calls < prev.Calls

		at := row.LastAt
		if at == 0 {
			at = row.FirstAt
		}
		if at == 0 {
			at = fallbackNowMs
		}

		var nextPriced bool
		var nextCost float64
		if reset {
			epoch++
			nextPriced = row.Priced
			nextCost = row.Cost
			if row.positive() {
				recs = append(recs, rec{
					at:     at,
					model:  row.Model,
					in:     max(int64(0), row.In),
					out:    max(int64(0), row.Out),
					cr:     max(int64(0), row.CR),
					cw:     max(int64(0), row.CW),
					cost:   row.Cost,
					priced: row.Priced,
					key:    makeLedgerRecKey(prefix, row.Key, epoch, len(recs)),
					reqs:   row.targetReqs(),
				})
			}
		} else {
			nextPriced = prev.Priced || row.Priced
			switch {
			case row.Priced:
				nextCost = row.Cost
			case prev.Priced:
				// The source already priced this cycle, so a now-zero/absent
				// source cost is a correction to $0. Keeping the old cost (or
				// falling back to the price table) would make one cycle flip
				// between source and estimated cost.
				nextCost = 0
			default:
				nextCost = prev.Cost
			}

			// If this epoch transitioned from unpriced to source-priced,
			// upgrade every earlier record of the current epoch to priced ($0)
			// so those tokens are not double-priced via LiteLLM alongside
			// nextCost.
			var epochCostSum float64
			var epochReqsSum int64
			lastEpochIdx := -1
			for i := range recs {
				_, recEpoch, ok := parseLedgerRecKey(prefix, recs[i].key)
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

			dIn := row.In - prev.In
			dOut := row.Out - prev.Out
			dCR := row.CR - prev.CR
			dCW := row.CW - prev.CW
			dCalls := row.Calls - prev.Calls
			dTokens := dIn > 0 || dOut > 0 || dCR > 0 || dCW > 0
			dCost := nextCost - epochCostSum
			targetEpochReqs := row.targetReqs()
			if targetEpochReqs == zeroReqs {
				targetEpochReqs = 0
			}

			if dTokens || dCalls > 0 {
				reqs := max(int64(0), targetEpochReqs-epochReqsSum)
				if reqs == 0 {
					reqs = zeroReqs
				}
				recs = append(recs, rec{
					at:     at,
					model:  row.Model,
					in:     dIn,
					out:    dOut,
					cr:     dCR,
					cw:     dCW,
					cost:   dCost,
					priced: nextPriced,
					key:    makeLedgerRecKey(prefix, row.Key, epoch, len(recs)),
					reqs:   reqs,
				})
			} else if nextPriced && math.Abs(dCost) > 1e-15 {
				if lastEpochIdx >= 0 {
					recs[lastEpochIdx].cost += dCost
					recs[lastEpochIdx].priced = true
				} else {
					recs = append(recs, rec{
						at:     at,
						model:  row.Model,
						cost:   dCost,
						priced: true,
						key:    makeLedgerRecKey(prefix, row.Key, epoch, len(recs)),
						reqs:   zeroReqs,
					})
				}
			}
		}

		out = append(out, recs...)
		nextSnap[row.Key] = cumSnapshot{
			Epoch:  epoch,
			Calls:  max(int64(0), row.Calls),
			In:     max(int64(0), row.In),
			Out:    max(int64(0), row.Out),
			CR:     max(int64(0), row.CR),
			CW:     max(int64(0), row.CW),
			Cost:   nextCost,
			Priced: nextPriced,
		}
	}
	return out, nextSnap
}
