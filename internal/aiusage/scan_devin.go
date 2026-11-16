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

type devinChatMsg struct {
	Role     string `json:"role"`
	Metadata *struct {
		CreatedAt json.RawMessage `json:"created_at"`
		Model     string          `json:"model"`
		Metrics   *struct {
			InputTokens         int64 `json:"input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
			CacheReadTokens     int64 `json:"cache_read_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_tokens"`
		} `json:"metrics"`
	} `json:"metadata"`
}

func readDevinDB(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) ([]rec, error) {
	var rows []devinRow
	if err := readSQLiteJSON(ctx, "devin", dbPath, cacheDir, devinQuery, &rows); err != nil {
		return nil, err
	}
	out := make([]rec, 0, len(rows))
	for _, row := range rows {
		var msg devinChatMsg
		if json.Unmarshal([]byte(row.ChatMessage), &msg) != nil || msg.Metadata == nil || msg.Metadata.Metrics == nil {
			continue
		}
		met := msg.Metadata.Metrics
		if met.InputTokens == 0 && met.OutputTokens == 0 && met.CacheReadTokens == 0 && met.CacheCreationTokens == 0 {
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
			in:    met.InputTokens,
			out:   met.OutputTokens,
			cr:    met.CacheReadTokens,
			cw:    met.CacheCreationTokens,
			key:   fmt.Sprintf("devin:%s:%d", row.SessionID, row.NodeID),
		})
	}
	return out, nil
}
