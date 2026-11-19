package aiusage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Copilot CLI / Desktop multi-source usage:
//   - Rank 1: ~/.copilot/session-store.db (`assistant_usage_events`)
//   - Rank 2: ~/.copilot/data.db (`assistant_usage_events` or `sessions` totals)
//   - Rank 3: ~/.copilot/otel/*.jsonl (+ COPILOT_OTEL_FILE_EXPORTER_PATH)
//
// Source precedence & deduplication model:
//  1. Session ownership: for every non-empty Copilot session_id, only the
//     highest-rank source containing usage for that session contributes records;
//     lower-rank sources for that session are ignored completely (solving
//     mismatched ID columns between DBs or between DB and OTEL).
//  2. Unified event identity: within the same rank or when session_id is absent,
//     every source shares the exact same event ID priority
//     (`service_request_id` > `api_call_id` > `message_id` > `request_id` >
//     `gen_ai.response.id` > `event_id` > `uuid` > non-numeric `id` / `spanId`)
//     and canonical content signature `(session_id, at_ms, model, in, out, cr, cw)`.
//     At summary time, a lower-rank record without session_id is suppressed if
//     either its event ID or its content signature (with multiplicity) was
//     already emitted by a higher-rank source.

const copilotKeyPrefix = "copilot\x00"

func makeCopilotRecKey(sessionID, eventID string) string {
	return copilotKeyPrefix + sessionID + "\x00" + eventID
}

func parseCopilotRecKey(k string) (sessionID, eventID string, ok bool) {
	if !strings.HasPrefix(k, copilotKeyPrefix) {
		return "", "", false
	}
	rest := k[len(copilotKeyPrefix):]
	sep := strings.IndexByte(rest, '\x00')
	if sep < 0 {
		return "", "", false
	}
	return rest[:sep], rest[sep+1:], true
}

func copilotSourceRank(path string) int {
	base := strings.ToLower(path)
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	switch base {
	case "session-store.db":
		return 1
	case "data.db":
		return 2
	default:
		if strings.HasSuffix(base, ".db") {
			return 2
		}
		return 3
	}
}

func copilotContentSig(sessionID string, rc rec) string {
	m := ""
	if rc.model != nil {
		m = *rc.model
	}
	return fmt.Sprintf("%s|%d|%s|%d|%d|%d|%d", sessionID, rc.at, m, rc.in, rc.out, rc.cr, rc.cw)
}

// readCopilotDB reads a Copilot database that holds per-request
// `assistant_usage_events`. data.db builds that only keep per-session
// totals go through readCopilotDataDB instead.
func readCopilotDB(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) ([]rec, error) {
	recs, ok, err := readCopilotEvents(ctx, dbPath, cacheDir, pool)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errUnsupportedDBSchema
	}
	return recs, nil
}

// readCopilotDataDB reads ~/.copilot/data.db: per-request events when the
// build has them, otherwise the cumulative `sessions` totals, which go through
// the same cumulative ledger as Hermes so a session's earlier days keep their
// usage when it is resumed.
func readCopilotDataDB(ctx context.Context, dbPath, cacheDir string, pool map[string]*string, old *fileState) ([]rec, map[string]cumSnapshot, error) {
	recs, ok, err := readCopilotEvents(ctx, dbPath, cacheDir, pool)
	if err != nil {
		return nil, nil, err
	}
	if ok {
		return recs, nil, nil
	}
	hasSessions, err := sqliteTableExists(ctx, "copilot", dbPath, cacheDir, "sessions")
	if err != nil {
		return nil, nil, err
	}
	if !hasSessions {
		return nil, nil, errUnsupportedDBSchema
	}
	rows, supported, err := readCopilotDataSessions(ctx, dbPath, cacheDir, pool)
	if err != nil {
		return nil, nil, err
	}
	if !supported {
		return nil, nil, errUnsupportedDBSchema
	}
	ledger, oldRecs := copilotSessionsBaseline(old)
	out, snap := applyCumulativeRows(rows, ledger, oldRecs, copilotKeyPrefix, time.Now().UnixMilli())
	return out, snap, nil
}

// copilotSessionsBaseline returns the ledger to continue from. A cache
// written before data.db `sessions` went through the ledger holds one
// whole-total record per session (key copilot\x00<sid>\x00, no snapshot);
// those totals become the baseline so the first scan after the upgrade adds
// only the growth since then. Anything else without a snapshot (an events
// cache, duplicate sessions) has no baseline and the ledger starts fresh.
func copilotSessionsBaseline(old *fileState) (map[string]cumSnapshot, []rec) {
	if old == nil || old.dbTable == "events" {
		return nil, nil
	}
	if len(old.ledgerPrev) > 0 {
		return old.ledgerPrev, old.recs
	}
	// Legacy caches without dbTable ("") cannot distinguish single-event
	// sessions from `events` rows; only baseline when dbTable is "sessions".
	if old.dbTable != "sessions" || len(old.recs) == 0 {
		return nil, nil
	}
	snap := make(map[string]cumSnapshot, len(old.recs))
	recs := make([]rec, 0, len(old.recs))
	for _, rc := range old.recs {
		sid, evID, ok := parseCopilotRecKey(rc.key)
		if !ok || sid == "" || evID != "" {
			return nil, nil
		}
		key := copilotSessionLedgerKey(sid)
		if _, dup := snap[key]; dup {
			return nil, nil
		}
		snap[key] = cumSnapshot{Calls: max(rc.reqs, 0), In: rc.in, Out: rc.out, CR: rc.cr, CW: rc.cw}
		rc.key = makeLedgerRecKey(copilotKeyPrefix, key, 0, 0)
		recs = append(recs, rc)
	}
	return snap, recs
}

// readCopilotEvents reads `assistant_usage_events`; ok is false when the
// database has no such table.
func readCopilotEvents(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) (_ []rec, ok bool, _ error) {
	hasEvents, err := sqliteTableExists(ctx, "copilot", dbPath, cacheDir, "assistant_usage_events")
	if err != nil {
		return nil, false, err
	}
	if !hasEvents {
		return nil, false, nil
	}
	var rows []map[string]any
	if err := readSQLiteJSON(ctx, "copilot", dbPath, cacheDir, `SELECT * FROM assistant_usage_events`, &rows); err != nil {
		return nil, false, err
	}
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
		sessionID := copilotFirstString(row, "session_id", "sessionId", "conversation_id")
		evID := copilotEventID(row)
		out = append(out, rec{
			at:    at,
			model: intern(pool, model),
			in:    in,
			out:   outTok,
			cr:    cr,
			cw:    cw,
			key:   makeCopilotRecKey(sessionID, evID),
		})
	}
	return out, true, nil
}

// copilotSessionLedgerKey names a data.db `sessions` row in the cumulative
// ledger. The record key becomes copilot\x00<sid>\x00sessions:<sid>\x00<epoch>\x00<seq>,
// so summarizeCopilot still sees the session ID (for source ownership) and an
// event ID that is unique across sessions.
func copilotSessionLedgerKey(sessionID string) string {
	return sessionID + "\x00sessions:" + sessionID
}

// readCopilotDataSessions returns the `sessions` totals as cumulative rows;
// supported is false when the table has no token columns.
func readCopilotDataSessions(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) ([]cumRow, bool, error) {
	var cols []struct {
		Name string `json:"name"`
	}
	if err := readSQLiteJSON(ctx, "copilot", dbPath, cacheDir, `PRAGMA table_info('sessions')`, &cols); err != nil {
		return nil, false, err
	}
	hasTokenCol := false
	for _, c := range cols {
		if c.Name == "total_input_tokens" || c.Name == "input_tokens" {
			hasTokenCol = true
			break
		}
	}
	if !hasTokenCol {
		return nil, false, nil
	}
	var rows []map[string]any
	if err := readSQLiteJSON(ctx, "copilot", dbPath, cacheDir, `SELECT * FROM sessions`, &rows); err != nil {
		return nil, false, err
	}
	out := make([]cumRow, 0, len(rows))
	for _, row := range rows {
		sessionID := copilotFirstString(row, "id", "session_id")
		if sessionID == "" {
			continue
		}
		// A session first seen whole is placed at its last update, as the
		// per-request sources record it: OTEL copies without a session ID
		// are matched against it by time and tokens.
		last := copilotRowMillis(row["updated_at"])
		if last == 0 {
			last = copilotRowMillis(row["timestamp"])
		}
		if last == 0 {
			last = copilotRowMillis(row["created_at"])
		}
		cr := firstInt64(row, "total_cached_tokens", "cache_read_tokens", "cached_input_tokens")
		cw := firstInt64(row, "total_cache_write_tokens", "cache_write_tokens")
		rawIn := firstInt64(row, "total_input_tokens", "input_tokens")
		in := rawIn
		if _, hasTotal := row["total_input_tokens"]; hasTotal && rawIn >= cr+cw {
			in = rawIn - cr - cw
		}
		out = append(out, cumRow{
			Key:     copilotSessionLedgerKey(sessionID),
			Model:   intern(pool, copilotFirstString(row, "model")),
			FirstAt: last,
			LastAt:  last,
			Calls:   firstInt64(row, "total_requests", "request_count", "turn_count"),
			In:      in,
			Out:     firstInt64(row, "total_output_tokens", "output_tokens"),
			CR:      cr,
			CW:      cw,
		})
	}
	return out, true, nil
}

// parseCopilotOTELLine parses both flat JSONL usage events and standard OTel
// GenAI `type:"span"` chat lines exported via COPILOT_OTEL_FILE_EXPORTER_PATH,
// using the exact same identity priority and key format as readCopilotDB.
func parseCopilotOTELLine(_ string, line []byte, pool map[string]*string) (r rec, key string, ok bool) {
	var m map[string]any
	if json.Unmarshal(line, &m) != nil || len(m) == 0 {
		return r, "", false
	}
	attrs, _ := m["attributes"].(map[string]any)
	msg, _ := m["message"].(map[string]any)

	in, outTok, cr, cw := copilotOTELTokens(m, attrs, msg)
	if in+outTok+cr+cw <= 0 {
		return r, "", false
	}

	at := copilotOTELMillis(m)
	if at == 0 {
		return r, "", false
	}

	model := copilotFirstString(m, "model")
	if model == "" && attrs != nil {
		model = copilotFirstString(attrs, "gen_ai.response.model", "gen_ai.request.model", "model")
	}
	if model == "" && msg != nil {
		model = copilotFirstString(msg, "model")
	}

	sessionID := copilotFirstString(m, "session_id", "sessionId", "conversation_id")
	if sessionID == "" && attrs != nil {
		sessionID = copilotFirstString(attrs, "gen_ai.conversation.id", "github.copilot.session_id", "session_id")
	}

	evID := copilotUnifiedID(m, attrs, msg)
	r = rec{
		at:    at,
		model: intern(pool, model),
		in:    in,
		out:   outTok,
		cr:    cr,
		cw:    cw,
	}
	if c, hasCost := copilotFloat64(m["cost"]); hasCost {
		r.cost, r.priced = c, true
	}
	return r, makeCopilotRecKey(sessionID, evID), true
}

func copilotOTELTokens(m, attrs, msg map[string]any) (in, out, cr, cw int64) {
	for _, container := range []map[string]any{m, msg} {
		if container == nil {
			continue
		}
		for _, field := range []string{"usage", "tokens"} {
			if sub, ok := container[field].(map[string]any); ok {
				raw, err := json.Marshal(sub)
				if err == nil {
					var fu flexUsageJSON
					if json.Unmarshal(raw, &fu) == nil {
						in, out, cr, cw = fu.toCounts()
						if in+out+cr+cw > 0 {
							return in, out, cr, cw
						}
					}
				}
			}
		}
	}
	if attrs != nil {
		if op := copilotFirstString(attrs, "gen_ai.operation.name"); op != "" && op != "chat" {
			return 0, 0, 0, 0
		}
		cr = firstInt64(attrs, "gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_read_input_tokens")
		cw = firstInt64(attrs, "gen_ai.usage.cache_creation.input_tokens", "gen_ai.usage.cache_write_input_tokens")
		rawIn := firstInt64(attrs, "gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens")
		in = rawIn
		if rawIn >= cr+cw && (cr > 0 || cw > 0) {
			in = rawIn - cr - cw
		}
		out = firstInt64(attrs, "gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens")
	}
	return in, out, cr, cw
}

func copilotOTELMillis(m map[string]any) int64 {
	for _, k := range []string{"timestamp", "created_at", "time"} {
		if at := copilotRowMillis(m[k]); at > 0 {
			return at
		}
	}
	for _, k := range []string{"endTime", "startTime"} {
		if arr, ok := m[k].([]any); ok && len(arr) >= 1 {
			sec := copilotInt64(arr[0])
			var nsec int64
			if len(arr) >= 2 {
				nsec = copilotInt64(arr[1])
			}
			if sec > 0 {
				return sec*1000 + nsec/1e6
			}
		}
	}
	return 0
}

func copilotFloat64(v any) (float64, bool) {
	if f, ok := v.(float64); ok {
		return f, true
	}
	return 0, false
}

func copilotFirstString(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	return ""
}

// copilotEventID and copilotUnifiedID apply the single shared priority order
// across SQLite rows and OTEL events so identical requests yield the same ID
// even when OTEL also carries a wrapper `id` / `spanId` or `uuid`.
func copilotEventID(row map[string]any) string {
	return copilotUnifiedID(row, nil, nil)
}

func copilotUnifiedID(top, attrs, msg map[string]any) string {
	// 1. Canonical cross-source request / message IDs first.
	if s := copilotFirstString(top, "service_request_id", "serviceRequestId"); s != "" {
		return s
	}
	if s := copilotFirstString(attrs, "github.copilot.service_request_id", "service_request_id"); s != "" {
		return s
	}
	if s := copilotFirstString(top, "api_call_id", "apiCallId", "message_id", "messageId", "request_id", "requestId"); s != "" {
		return s
	}
	if s := copilotFirstString(attrs, "gen_ai.response.id", "message_id", "request_id"); s != "" {
		return s
	}
	if msg != nil {
		if s := copilotFirstString(msg, "id", "message_id", "request_id"); s != "" && !isAllDigits(s) {
			return s
		}
	}
	// 2. Generic event / span identifiers when canonical message/request IDs are absent.
	if s := copilotFirstString(top, "event_id", "eventId", "uuid"); s != "" {
		return s
	}
	if s := copilotFirstString(top, "id"); s != "" && !isAllDigits(s) {
		return s
	}
	if s := copilotFirstString(top, "spanId", "span_id"); s != "" {
		return s
	}
	return ""
}

func isAllDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
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
