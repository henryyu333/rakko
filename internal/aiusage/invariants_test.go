package aiusage

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// hermesOracle tracks the exact mathematical specification of Hermes's
// cumulative-cycle ledger model to verify invariants across deterministic and
// randomized snapshot sequences.
type hermesOracle struct {
	rows map[string]*hermesOracleRow
}

type hermesOracleEpoch struct {
	calls  int64
	in     int64
	out    int64
	cr     int64
	cw     int64
	cost   float64
	priced bool
}

type hermesOracleRow struct {
	model  string
	epochs []hermesOracleEpoch
}

func (o *hermesOracle) step(rows []hermesRow) {
	if o.rows == nil {
		o.rows = map[string]*hermesOracleRow{}
	}
	seen := map[string]bool{}
	for _, r := range rows {
		key := r.SessionID + "\x1f" + r.Model + "\x1f" + r.BillingProvider + "\x1f" + r.BillingBaseURL + "\x1f" + r.BillingMode + "\x1f" + r.Task
		seen[key] = true
		curCost, curPriced := hermesRowCost(r)
		cur := hermesOracleEpoch{
			calls:  max(int64(0), r.APICallCount),
			in:     max(int64(0), r.InputTokens),
			out:    max(int64(0), r.OutputTokens),
			cr:     max(int64(0), r.CacheRead),
			cw:     max(int64(0), r.CacheWrite),
			cost:   curCost,
			priced: curPriced,
		}
		orow, ok := o.rows[key]
		if !ok {
			if cur.in == 0 && cur.out == 0 && cur.cr == 0 && cur.cw == 0 && cur.calls == 0 && !curPriced {
				o.rows[key] = &hermesOracleRow{model: r.Model, epochs: []hermesOracleEpoch{{}}}
				continue
			}
			at := int64(math.Round(r.FirstTS * 1000))
			if at == 0 {
				at = int64(math.Round(r.LastTS * 1000))
			}
			if at == 0 {
				continue
			}
			o.rows[key] = &hermesOracleRow{model: r.Model, epochs: []hermesOracleEpoch{cur}}
			continue
		}
		prev := &orow.epochs[len(orow.epochs)-1]
		reset := r.InputTokens < prev.in ||
			r.OutputTokens < prev.out ||
			r.CacheRead < prev.cr ||
			r.CacheWrite < prev.cw ||
			r.APICallCount < prev.calls
		if reset {
			orow.epochs = append(orow.epochs, cur)
		} else {
			prev.calls = cur.calls
			prev.in = cur.in
			prev.out = cur.out
			prev.cr = cur.cr
			prev.cw = cur.cw
			// Invariant 3: once a cycle has a source cost, the source owns it;
			// a later zero/absent source cost is a correction to $0, not a
			// fallback to the price table.
			if cur.priced {
				prev.priced = true
				prev.cost = cur.cost
			} else if prev.priced {
				prev.cost = 0
			}
		}
	}
	// Invariant 2: deleted rows vanish completely.
	for k := range o.rows {
		if !seen[k] {
			delete(o.rows, k)
		}
	}
}

func (o *hermesOracle) expectedTotals() Totals {
	var tot Totals
	for _, r := range o.rows {
		for _, ep := range r.epochs {
			tot.Input += ep.in
			tot.Output += ep.out
			tot.CacheRead += ep.cr
			tot.CacheWrite += ep.cw
			hasTokens := ep.in > 0 || ep.out > 0 || ep.cr > 0 || ep.cw > 0
			var reqs int64
			if ep.calls > 0 {
				reqs = ep.calls
			} else if hasTokens {
				reqs = 1
			}
			tot.Requests += reqs
			if ep.priced {
				tot.Cost += ep.cost
			} else if hasTokens || reqs > 0 {
				if c, ok := estimateCost(r.model, ep.in, ep.out, ep.cr, ep.cw); ok {
					tot.Cost += c
				} else {
					tot.Unpriced += reqs
				}
			}
		}
	}
	return tot
}

func summarizeHermesState(st *fileState, now time.Time) Summary {
	s := &Scanner{
		home: "/virtual",
		files: map[string]*fileState{
			"/virtual/.hermes/state.db": st,
		},
	}
	return s.Summary(RangeAll, now)
}

func TestHermesInvariantsTableDriven(t *testing.T) {
	day1 := float64(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC).Unix())
	day2 := float64(time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC).Unix())
	day3 := float64(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC).Unix())
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	// Priced model present in LiteLLM prices.json.gz (claude-sonnet-4-5-20250929).
	const pricedModel = "claude-sonnet-4-5-20250929"

	cases := []struct {
		name  string
		steps [][]hermesRow
		check func(t *testing.T, final Totals, sum Summary, cacheBytes []byte)
	}{
		{
			name: "counter_zero_reset_100_to_0_to_60_equals_160",
			steps: [][]hermesRow{
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 100, ActualCost: new(0.10), FirstTS: day1, LastTS: day1}},
				// Zeroing all four token counters must start a new epoch (0) rather than deleting the row!
				{{SessionID: "s1", Model: pricedModel, APICallCount: 0, InputTokens: 0, ActualCost: new(0.0), FirstTS: day1, LastTS: day2}},
				{{SessionID: "s1", Model: pricedModel, APICallCount: 1, InputTokens: 60, ActualCost: new(0.06), FirstTS: day1, LastTS: day3}},
			},
			check: func(t *testing.T, final Totals, _ Summary, _ []byte) {
				if final.Input != 160 || final.Requests != 3 || math.Abs(final.Cost-0.16) > 1e-9 {
					t.Fatalf("got %+v, want in=160 reqs=3 cost=0.16", final)
				}
			},
		},
		{
			name: "counter_rollback_100_to_40_to_60_equals_160",
			steps: [][]hermesRow{
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 100, ActualCost: new(0.10), FirstTS: day1, LastTS: day1}},
				{{SessionID: "s1", Model: pricedModel, APICallCount: 1, InputTokens: 40, ActualCost: new(0.04), FirstTS: day1, LastTS: day2}},
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 60, ActualCost: new(0.06), FirstTS: day1, LastTS: day3}},
			},
			check: func(t *testing.T, final Totals, _ Summary, _ []byte) {
				if final.Input != 160 || final.Requests != 4 || math.Abs(final.Cost-0.16) > 1e-9 {
					t.Fatalf("got %+v, want in=160 reqs=4 cost=0.16", final)
				}
			},
		},
		{
			name: "delete_and_rebuild_same_key",
			steps: [][]hermesRow{
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 100, ActualCost: new(0.10), FirstTS: day1, LastTS: day1}},
				{}, // Row deleted from DB -> records must vanish completely.
				{{SessionID: "s1", Model: pricedModel, APICallCount: 1, InputTokens: 50, ActualCost: new(0.05), FirstTS: day3, LastTS: day3}},
			},
			check: func(t *testing.T, final Totals, _ Summary, _ []byte) {
				if final.Input != 50 || final.Requests != 1 || math.Abs(final.Cost-0.05) > 1e-9 {
					t.Fatalf("got %+v, want in=50 reqs=1 cost=0.05", final)
				}
			},
		},
		{
			name: "late_cost_arrival_upgrades_prior_unpriced_records_without_double_pricing",
			steps: [][]hermesRow{
				// Step 1: 100,000 input tokens, no source cost -> LiteLLM would price at $0.30.
				{{SessionID: "s1", Model: pricedModel, APICallCount: 1, InputTokens: 100_000, FirstTS: day1, LastTS: day1}},
				// Step 2: grows to 200,000 input tokens, still no source cost.
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 200_000, FirstTS: day1, LastTS: day2}},
				// Step 3: actual_cost_usd arrives at $0.20 with no new tokens/calls!
				// Total cost MUST be exactly $0.20 (not $0.30+$0.30+$0.20 = $0.80), and Requests MUST stay 2.
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 200_000, ActualCost: new(0.20), FirstTS: day1, LastTS: day3}},
			},
			check: func(t *testing.T, final Totals, _ Summary, _ []byte) {
				if final.Input != 200_000 || final.Requests != 2 || final.Unpriced != 0 || math.Abs(final.Cost-0.20) > 1e-9 {
					t.Fatalf("got %+v, want in=200000 reqs=2 unpriced=0 cost=0.20", final)
				}
			},
		},
		{
			name: "cost_up_and_down_and_actual_overrides_estimated",
			steps: [][]hermesRow{
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 1000, EstimatedCost: new(0.12), FirstTS: day1, LastTS: day1}},
				// Actual cost arrives lower than estimated (0.08 < 0.12), calls unchanged.
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 1000, EstimatedCost: new(0.12), ActualCost: new(0.08), FirstTS: day1, LastTS: day2}},
				// Actual cost adjusted upward to 0.25, calls unchanged.
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 1000, EstimatedCost: new(0.12), ActualCost: new(0.25), FirstTS: day1, LastTS: day3}},
			},
			check: func(t *testing.T, final Totals, _ Summary, cacheBytes []byte) {
				if final.Requests != 2 || math.Abs(final.Cost-0.25) > 1e-9 {
					t.Fatalf("got %+v, want reqs=2 cost=0.25", final)
				}
				// Verify single-copy cache invariant: the ledger snapshot in "h" has no "recs" field.
				if strings.Contains(string(cacheBytes), `"recs"`) {
					t.Fatalf("cache JSON still duplicates recs inside the ledger snapshot: %s", string(cacheBytes))
				}
			},
		},
		{
			name: "source_cost_cleared_to_zero_reconciles_cycle_to_zero",
			steps: [][]hermesRow{
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 1000, ActualCost: new(0.20), EstimatedCost: new(0.0), FirstTS: day1, LastTS: day1}},
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 1000, ActualCost: new(0.0), EstimatedCost: new(0.0), FirstTS: day1, LastTS: day2}},
			},
			check: func(t *testing.T, final Totals, _ Summary, _ []byte) {
				if final.Input != 1000 || final.Requests != 2 || final.Unpriced != 0 || math.Abs(final.Cost) > 1e-9 {
					t.Fatalf("got %+v, want in=1000 reqs=2 unpriced=0 cost=0", final)
				}
			},
		},
		{
			name: "first_seen_uses_started_at_and_deltas_use_last_seen",
			steps: [][]hermesRow{
				{{SessionID: "s1", Model: pricedModel, APICallCount: 1, InputTokens: 100, ActualCost: new(0.01), FirstTS: day1, LastTS: day2}},
				{{SessionID: "s1", Model: pricedModel, APICallCount: 2, InputTokens: 170, ActualCost: new(0.02), FirstTS: day1, LastTS: day3}},
			},
			check: func(t *testing.T, _ Totals, sum Summary, _ []byte) {
				if len(sum.Buckets) != 3 {
					t.Fatalf("buckets = %d, want 3 (day1..day3)", len(sum.Buckets))
				}
				if sum.Buckets[0].Tokens[ToolHermes] != 100 || sum.Buckets[1].Tokens[ToolHermes] != 0 || sum.Buckets[2].Tokens[ToolHermes] != 70 {
					t.Fatalf("bucket tokens = [%d, %d, %d], want [100, 0, 70]",
						sum.Buckets[0].Tokens[ToolHermes], sum.Buckets[1].Tokens[ToolHermes], sum.Buckets[2].Tokens[ToolHermes])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cacheDir := t.TempDir()
			var st *fileState
			var oracle hermesOracle
			var lastCache []byte

			for stepIdx, rows := range tc.steps {
				oracle.step(rows)
				pool := map[string]*string{}
				recs, snap := applyHermesRows(rows, pool, st, now.UnixMilli())
				st = &fileState{
					tool:       ToolHermes,
					size:       int64(stepIdx + 1),
					modNano:    int64(stepIdx + 1),
					recs:       recs,
					ledgerPrev: snap,
				}
				// Round-trip through on-disk cache at every step to prove persistence!
				sc := NewScanner("/virtual", cacheDir)
				sc.saveCache(context.Background(), map[string]*fileState{"/virtual/.hermes/state.db": st})
				var err error
				lastCache, err = os.ReadFile(filepath.Join(cacheDir, cacheName))
				if err != nil {
					t.Fatal(err)
				}
				sc2 := NewScanner("/virtual", cacheDir)
				sc2.loadCache(context.Background())
				st = sc2.files["/virtual/.hermes/state.db"]
				if st == nil {
					t.Fatal("cache round-trip lost fileState")
				}

				got := summarizeHermesState(st, now).ByTool[ToolHermes]
				want := oracle.expectedTotals()
				if got.Input != want.Input || got.Output != want.Output || got.CacheRead != want.CacheRead ||
					got.CacheWrite != want.CacheWrite || got.Requests != want.Requests ||
					got.Unpriced != want.Unpriced || math.Abs(got.Cost-want.Cost) > 1e-9 {
					t.Fatalf("step %d mismatch:\n got:  %+v\n want: %+v", stepIdx, got, want)
				}
			}
			sum := summarizeHermesState(st, now)
			tc.check(t, sum.ByTool[ToolHermes], sum, lastCache)
		})
	}
}

func TestHermesOldAndCorruptCacheUpgrade(t *testing.T) {
	day1 := float64(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC).Unix())
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	rows := []hermesRow{{
		SessionID:    "s1",
		Model:        "claude-sonnet-4-5-20250929",
		APICallCount: 3,
		InputTokens:  250,
		OutputTokens: 50,
		ActualCost:   new(0.15),
		FirstTS:      day1,
		LastTS:       day1,
	}}

	t.Run("v4_cache_ignored_and_rebuilt_without_double_count", func(t *testing.T) {
		cacheDir := t.TempDir()
		v4JSON := `{"v":4,"files":{"/virtual/.hermes/state.db":{"t":"hermes","s":100,"o":0,"m":100,"ls":0,"lh":0,"h":{"s1|claude-sonnet-4-5-20250929||||":{"c":2,"i":150,"o":30,"u":0.1,"p":true}},"r":[[1794823200000,"claude-sonnet-4-5-20250929",150,30,0,0,0.1,1,"",2]]}}}`
		if err := os.WriteFile(filepath.Join(cacheDir, cacheName), []byte(v4JSON), 0o644); err != nil {
			t.Fatal(err)
		}
		sc := NewScanner("/virtual", cacheDir)
		sc.loadCache(context.Background())
		if len(sc.files) != 0 {
			t.Fatalf("expected v4 cache to be discarded on load, got %d files", len(sc.files))
		}
		pool := map[string]*string{}
		recs, snap := applyHermesRows(rows, pool, sc.files["/virtual/.hermes/state.db"], now.UnixMilli())
		st := &fileState{tool: ToolHermes, recs: recs, ledgerPrev: snap}
		got := summarizeHermesState(st, now).ByTool[ToolHermes]
		if got.Input != 250 || got.Output != 50 || got.Requests != 3 || math.Abs(got.Cost-0.15) > 1e-9 {
			t.Fatalf("rebuilt from v4 = %+v, want in=250 out=50 reqs=3 cost=0.15", got)
		}
	})

	t.Run("corrupt_unkeyed_recs_in_state_rebuilt_cleanly", func(t *testing.T) {
		corruptOld := &fileState{
			tool: ToolHermes,
			recs: []rec{{at: int64(day1 * 1000), in: 999, reqs: 9, key: ""}},
			ledgerPrev: map[string]cumSnapshot{
				"s1\x1fclaude-sonnet-4-5-20250929\x1f\x1f\x1f\x1f": {Epoch: 0, Calls: 9, In: 999},
			},
		}
		pool := map[string]*string{}
		recs, snap := applyHermesRows(rows, pool, corruptOld, now.UnixMilli())
		st := &fileState{tool: ToolHermes, recs: recs, ledgerPrev: snap}
		got := summarizeHermesState(st, now).ByTool[ToolHermes]
		if got.Input != 250 || got.Output != 50 || got.Requests != 3 || math.Abs(got.Cost-0.15) > 1e-9 {
			t.Fatalf("rebuilt from corrupt state = %+v, want in=250 out=50 reqs=3 cost=0.15", got)
		}
	})
}

func TestHermesRandomizedSequenceInvariants(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261117, 1))
	models := []string{"claude-sonnet-4-5-20250929", "unknown-custom-hermes-model"}
	sessions := []string{"sess-a", "sess-b", "sess-c"}
	baseTS := float64(time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC).Unix())
	now := time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC)

	for trial := range 25 {
		cacheDir := t.TempDir()
		var st *fileState
		var oracle hermesOracle
		live := map[string]hermesRow{}

		for step := range 30 {
			ts := baseTS + float64(step*3600)
			sid := sessions[rng.IntN(len(sessions))]
			model := models[rng.IntN(len(models))]
			rk := sid + "|" + model
			cur, exists := live[rk]

			op := rng.IntN(10)
			switch {
			case !exists || op == 0:
				// Create or reset to fresh values (including possible 0-token row).
				var costPtr *float64
				if rng.IntN(2) == 0 {
					costPtr = new(float64(rng.IntN(50)+1) / 100.0)
				}
				inTok := int64(rng.IntN(5) * 100)
				calls := int64(0)
				if inTok > 0 {
					calls = int64(rng.IntN(3) + 1)
				}
				live[rk] = hermesRow{
					SessionID:    sid,
					Model:        model,
					APICallCount: calls,
					InputTokens:  inTok,
					OutputTokens: int64(rng.IntN(3) * 20),
					CacheRead:    int64(rng.IntN(2) * 50),
					ActualCost:   costPtr,
					FirstTS:      ts,
					LastTS:       ts,
				}
			case op == 1:
				// Zero out counters (row stays in DB with all zeros).
				cur.APICallCount = 0
				cur.InputTokens = 0
				cur.OutputTokens = 0
				cur.CacheRead = 0
				cur.CacheWrite = 0
				cur.ActualCost = nil
				cur.EstimatedCost = nil
				cur.LastTS = ts
				live[rk] = cur
			case op == 2:
				// Delete row from DB.
				delete(live, rk)
			case op <= 6:
				// Monotonic growth.
				cur.APICallCount += int64(rng.IntN(3))
				cur.InputTokens += int64(rng.IntN(4) * 50)
				cur.OutputTokens += int64(rng.IntN(3) * 10)
				if cur.ActualCost != nil {
					cur.ActualCost = new(*cur.ActualCost + float64(rng.IntN(20))/100.0)
				} else if rng.IntN(3) == 0 {
					// Late cost backfill!
					cur.ActualCost = new(float64(rng.IntN(80)+5) / 100.0)
				}
				cur.LastTS = ts
				live[rk] = cur
			default:
				// Cost-only adjustment: up, down, or cleared to zero while the
				// token counters stay put.
				if rng.IntN(4) == 0 {
					cur.ActualCost = new(0.0)
				} else {
					cur.ActualCost = new(float64(rng.IntN(100)+1) / 100.0)
				}
				cur.LastTS = ts
				live[rk] = cur
			}

			rows := make([]hermesRow, 0, len(live))
			for _, k := range slices.Sorted(maps.Keys(live)) {
				rows = append(rows, live[k])
			}

			oracle.step(rows)
			pool := map[string]*string{}
			recs, snap := applyHermesRows(rows, pool, st, now.UnixMilli())
			st = &fileState{
				tool:       ToolHermes,
				size:       int64(step + 1),
				modNano:    int64(step + 1),
				recs:       recs,
				ledgerPrev: snap,
			}
			// Exercise cache save/load every 3rd step.
			if step%3 == 0 {
				sc := NewScanner("/virtual", cacheDir)
				sc.saveCache(context.Background(), map[string]*fileState{"/virtual/.hermes/state.db": st})
				sc2 := NewScanner("/virtual", cacheDir)
				sc2.loadCache(context.Background())
				st = sc2.files["/virtual/.hermes/state.db"]
			}

			got := summarizeHermesState(st, now).ByTool[ToolHermes]
			want := oracle.expectedTotals()
			if got.Input != want.Input || got.Output != want.Output ||
				got.CacheRead != want.CacheRead || got.CacheWrite != want.CacheWrite ||
				got.Requests != want.Requests || got.Unpriced != want.Unpriced ||
				math.Abs(got.Cost-want.Cost) > 1e-7 {
				rawSnap, _ := json.Marshal(rows)
				t.Fatalf("trial %d step %d mismatch (rows=%s):\n got:  %+v\n want: %+v",
					trial, step, rawSnap, got, want)
			}
		}
	}
}

func TestCopilotMultiSourcePrecedenceAndIdentity(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

	t.Run("otel_span_and_flat_jsonl_only", func(t *testing.T) {
		home := t.TempDir()
		otelPath := filepath.Join(home, ".copilot", "otel", "copilot-otel.jsonl")
		spanLine := `{"type":"span","traceId":"t1","spanId":"sp1","name":"chat claude-sonnet-4-5-20250929","startTime":[1794823200,0],"endTime":[1794823202,500000000],"attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"sess-otel","gen_ai.response.model":"claude-sonnet-4-5-20250929","gen_ai.response.id":"msg_abc","gen_ai.usage.input_tokens":1300,"gen_ai.usage.output_tokens":80,"gen_ai.usage.cache_read.input_tokens":1000,"gen_ai.usage.cache_creation.input_tokens":50}}`
		toolSpan := `{"type":"span","traceId":"t1","spanId":"sp2","name":"execute_tool bash","startTime":[1794823203,0],"endTime":[1794823204,0],"attributes":{"gen_ai.operation.name":"execute_tool","gen_ai.usage.input_tokens":999}}`
		flatLine := `{"id":"wrapper-99","message_id":"msg_def","session_id":"sess-otel","timestamp":"2026-11-16T10:05:00Z","model":"gpt-5","usage":{"input_tokens":200,"output_tokens":40}}`
		// Duplicate of spanLine in another rotated OTEL file -> must count once.
		writeTestFile(t, otelPath, []byte(spanLine+"\n"+toolSpan+"\n"+flatLine+"\n"))
		writeTestFile(t, filepath.Join(home, ".copilot", "otel", "copy.jsonl"), []byte(spanLine+"\n"))

		s := NewScanner(home, t.TempDir())
		if err := s.Scan(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		got := s.Summary(RangeAll, now).ByTool[ToolCopilot]
		// spanLine uncached input = 1300 - 1000 - 50 = 250; plus flatLine 200 = 450.
		if got.Requests != 2 || got.Input != 450 || got.Output != 120 || got.CacheRead != 1000 || got.CacheWrite != 50 {
			t.Fatalf("otel-only totals = %+v, want reqs=2 in=450 out=120 cr=1000 cw=50", got)
		}
	})

	t.Run("otel_exporter_file_env_var", func(t *testing.T) {
		home := t.TempDir()
		exportDir := t.TempDir()
		exportFile := filepath.Join(exportDir, "custom-trace.jsonl")
		otherFile := filepath.Join(exportDir, "unrelated.jsonl")
		writeTestFile(t, exportFile, []byte(`{"id":"e1","timestamp":"2026-11-16T10:00:00Z","model":"gpt-5","usage":{"input_tokens":110,"output_tokens":22}}`+"\n"))
		writeTestFile(t, otherFile, []byte(`{"id":"e2","timestamp":"2026-11-16T10:01:00Z","model":"gpt-5","usage":{"input_tokens":999,"output_tokens":99}}`+"\n"))
		t.Setenv("COPILOT_OTEL_FILE_EXPORTER_PATH", exportFile)

		s := NewScanner(home, t.TempDir())
		if err := s.Scan(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		got := s.Summary(RangeAll, now).ByTool[ToolCopilot]
		if got.Requests != 1 || got.Input != 110 || got.Output != 22 {
			t.Fatalf("COPILOT_OTEL_FILE_EXPORTER_PATH file totals = %+v, want reqs=1 in=110 out=22", got)
		}
	})

	t.Run("cross_source_precedence_and_mismatched_id_fields_in_memory", func(t *testing.T) {
		// Portable cross-OS test (runs on Windows too without sqlite3 CLI):
		// - Session "s1": in session-store.db (has request_id="req-1") AND data.db (no request_id, only numeric id) AND OTEL (has wrapper id="otel-wrap-1", request_id="req-1").
		//   Must count ONCE from session-store.db!
		// - Session "s2": only in data.db (no request_id) AND OTEL (spanId="sp-2", gen_ai.conversation.id="s2").
		//   Must count ONCE from data.db!
		// - Session "s3": only in OTEL -> must still be counted!
		// - Sessionless OTEL line with wrapper id="otel-wrap-anon" + message_id="msg-s1" matching session-store's message_id="msg-s1" -> deduplicated by unified ID priority!
		pool := map[string]*string{}
		ts := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC).UnixMilli()
		m := intern(pool, "gpt-5.3-codex")

		s := &Scanner{
			home: "/virtual",
			files: map[string]*fileState{
				"/virtual/.copilot/session-store.db": {
					tool: ToolCopilot,
					recs: []rec{
						{at: ts, model: m, in: 300, out: 80, key: makeCopilotRecKey("s1", "msg-s1")},
					},
				},
				"/virtual/.copilot/data.db": {
					tool: ToolCopilot,
					recs: []rec{
						// Same event in s1 but data.db lacked request_id/message_id!
						{at: ts, model: m, in: 300, out: 80, key: makeCopilotRecKey("s1", "")},
						// Session s2 only in data.db and OTEL.
						{at: ts + 1000, model: m, in: 200, out: 50, key: makeCopilotRecKey("s2", "")},
					},
				},
				"/virtual/.copilot/otel/copilot.jsonl": {
					tool: ToolCopilot,
					recs: func() []rec {
						var out []rec
						for _, line := range []string{
							// OTEL line with both top-level wrapper `id` AND `message_id`: unified ID must pick `msg-s1`, not `otel-wrap-1`!
							`{"id":"otel-wrap-1","uuid":"u-999","message_id":"msg-s1","timestamp":"2026-11-16T10:00:00Z","model":"gpt-5.3-codex","usage":{"input_tokens":300,"output_tokens":80}}`,
							// OTEL span for s2 with different spanId: suppressed because data.db owns s2!
							`{"type":"span","spanId":"sp-2","endTime":[1794823201,0],"attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"s2","gen_ai.response.model":"gpt-5.3-codex","gen_ai.usage.input_tokens":200,"gen_ai.usage.output_tokens":50}}`,
							// OTEL-only session s3: preserved!
							`{"type":"span","spanId":"sp-3","endTime":[1794823205,0],"attributes":{"gen_ai.operation.name":"chat","gen_ai.conversation.id":"s3","gen_ai.response.model":"gpt-5.3-codex","gen_ai.usage.input_tokens":150,"gen_ai.usage.output_tokens":30}}`,
						} {
							rc, k, ok := parseCopilotOTELLine("copilot.jsonl", []byte(line), pool)
							if !ok {
								t.Fatalf("failed to parse OTEL line: %s", line)
							}
							rc.key = k
							out = append(out, rc)
						}
						return out
					}(),
				},
			},
		}

		got := s.Summary(RangeAll, now).ByTool[ToolCopilot]
		// Expected: s1 (300/80) + s2 (200/50) + s3 (150/30) = 3 reqs, 650 in, 160 out.
		if got.Requests != 3 || got.Input != 650 || got.Output != 160 {
			t.Fatalf("multi-source copilot = %+v, want reqs=3 in=650 out=160", got)
		}
	})
}
