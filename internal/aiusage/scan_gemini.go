package aiusage

import (
	"encoding/json"
)

// Gemini CLI (and Qwen Code's tmp/*/chats/*.json) session files:
//   $GEMINI_CLI_HOME/tmp/<hash>/chats/session-*.json
//
// Whole-JSON format:
//   {"sessionId":"...", "messages":[{"id":"...", "timestamp":"...",
//     "type":"gemini", "model":"gemini-2.5-pro",
//     "tokens":{"input":100,"output":50,"cached":200,"thoughts":10}}]}

type geminiChatJSON struct {
	SessionID string `json:"sessionId"`
	Messages  []struct {
		ID        string          `json:"id"`
		Timestamp json.RawMessage `json:"timestamp"`
		Type      string          `json:"type"`
		Model     string          `json:"model"`
		Tokens    *struct {
			Input    int64 `json:"input"`
			Output   int64 `json:"output"`
			Cached   int64 `json:"cached"`
			Thoughts int64 `json:"thoughts"`
		} `json:"tokens"`
	} `json:"messages"`
}

func parseGeminiChatFile(path string, data []byte, pool map[string]*string) ([]rec, error) {
	var doc geminiChatJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var out []rec
	for _, m := range doc.Messages {
		if m.Tokens == nil {
			continue
		}
		if m.Tokens.Input == 0 && m.Tokens.Output == 0 && m.Tokens.Cached == 0 && m.Tokens.Thoughts == 0 {
			continue
		}
		at := parseMillis(m.Timestamp)
		if at == 0 {
			continue
		}
		var key string
		if doc.SessionID != "" && m.ID != "" {
			key = "gemini:" + doc.SessionID + ":" + m.ID
		}
		out = append(out, rec{
			at:    at,
			model: intern(pool, m.Model),
			in:    m.Tokens.Input,
			out:   m.Tokens.Output + m.Tokens.Thoughts,
			cr:    m.Tokens.Cached,
			key:   key,
		})
	}
	return out, nil
}
