package aiusage

import (
	"context"
	"encoding/json"
	"fmt"
)

// Devin CLI usage: ~/.local/share/devin/cli/sessions.db
// (Windows: %LOCALAPPDATA%\devin\cli\sessions.db or ~/.local/share/devin/cli/sessions.db).
//
// message_nodes stores JSON chat_message with metadata.metrics.{input_tokens,
// output_tokens, cache_read_tokens, cache_creation_tokens}. sessions.model
// gives the session's model.

const devinQuery = `
SELECT m.session_id,
       m.node_id,
       m.created_at AS node_created_at,
       s.model AS session_model,
       m.chat_message
FROM message_nodes m
LEFT JOIN sessions s ON s.id = m.session_id
WHERE m.chat_message LIKE '%"metrics"%'`

type devinRow struct {
	SessionID     string `json:"session_id"`
	NodeID        int64  `json:"node_id"`
	NodeCreatedAt int64  `json:"node_created_at"`
	SessionModel  string `json:"session_model"`
	ChatMessage   string `json:"chat_message"`
}

// devinMetrics is Devin's own metrics block: besides the shared keys its CLI
// writes cache_creation_tokens, which no other tool's usage block carries,
// so only this parser reads it. The field must not be named after the
// embedded key it would shadow (CacheCreationTokens reads
// cache_creation_input_tokens).
type devinMetrics struct {
	flexUsageJSON
	CacheCreationAlt int64 `json:"cache_creation_tokens"`
}

func (m *devinMetrics) toCounts() (in, out, cr, cw int64) {
	in, out, cr, cw = m.flexUsageJSON.toCounts()
	cw = max(cw, m.CacheCreationAlt)
	return in, out, cr, cw
}

type devinChatMsg struct {
	Role     string `json:"role"`
	Metadata *struct {
		CreatedAt json.RawMessage `json:"created_at"`
		Model     string          `json:"model"`
		Metrics   *devinMetrics   `json:"metrics"`
	} `json:"metadata"`
}

func readDevinDB(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) ([]rec, error) {
	var rows []devinRow
	if err := withSQLiteCopy(ctx, "devin", dbPath, cacheDir, func(db sqliteCopy) error {
		if err := db.requireTable(ctx, "message_nodes"); err != nil {
			return err
		}
		return db.queryJSON(ctx, devinQuery, &rows)
	}); err != nil {
		return nil, err
	}
	out := make([]rec, 0, len(rows))
	for _, row := range rows {
		var msg devinChatMsg
		if json.Unmarshal([]byte(row.ChatMessage), &msg) != nil || msg.Metadata == nil || msg.Metadata.Metrics == nil {
			continue
		}
		met := msg.Metadata.Metrics
		in, outTok, cr, cw := met.toCounts()
		if in == 0 && outTok == 0 && cr == 0 && cw == 0 {
			continue
		}
		at := parseMillis(msg.Metadata.CreatedAt)
		if at == 0 && row.NodeCreatedAt > 0 {
			at = row.NodeCreatedAt * 1000
		}
		if at == 0 {
			continue
		}
		model := msg.Metadata.Model
		if model == "" {
			model = row.SessionModel
		}
		out = append(out, rec{
			at:    at,
			model: intern(pool, model),
			in:    in,
			out:   outTok,
			cr:    cr,
			cw:    cw,
			key:   fmt.Sprintf("devin:%s:%d", row.SessionID, row.NodeID),
		})
	}
	return out, nil
}
