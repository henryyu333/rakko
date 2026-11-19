package aiusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"rakko/internal/atomicfile"
)

// LastQuotasFile is the file inside the data directory that keeps the last
// good quota reading per tool, so a restart shows what was last read
// instead of an empty card. It mirrors Magpie's quotas.json.
const LastQuotasFile = "quotas.json"

type lastQuotasFile struct {
	Claude *lastQuota          `json:"claude,omitempty"`
	Codex  *lastQuota          `json:"codex,omitempty"`
	Tools  map[Tool]*lastQuota `json:"tools,omitempty"`
}

type lastQuota struct {
	At           time.Time     `json:"at"`
	Plan         string        `json:"plan,omitempty"`
	Windows      []Window      `json:"windows,omitempty"`
	Balance      *BalanceInfo  `json:"balance,omitempty"`
	ResetCredits *ResetCredits `json:"resetCredits,omitempty"`
}

func (l *lastQuota) usable() bool {
	return l != nil && (len(l.Windows) > 0 || l.Balance != nil)
}

// LoadLastQuotas reads the saved readings; a missing or unreadable file
// yields none, never an error that would block the page.
func LoadLastQuotas(dir string) []Quota {
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, LastQuotasFile))
	if err != nil {
		return nil
	}
	var file lastQuotasFile
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	entries := map[Tool]*lastQuota{}
	if file.Claude.usable() {
		entries[ToolClaude] = file.Claude
	}
	if file.Codex.usable() {
		entries[ToolCodex] = file.Codex
	}
	for t, q := range file.Tools {
		if q.usable() {
			entries[t] = q
		}
	}
	var out []Quota
	for t, q := range entries {
		out = append(out, Quota{
			Tool:         t,
			Plan:         q.Plan,
			Windows:      q.Windows,
			Balance:      q.Balance,
			ResetCredits: q.ResetCredits,
			Status:       QuotaOK,
			FetchedAt:    q.At,
		})
	}
	return out
}

// SaveLastQuota records one good reading. A failure is returned but must
// not disturb what the page shows: the data is a convenience across
// restarts, not the source of truth.
func SaveLastQuota(dir string, q Quota) error {
	if dir == "" || q.Status != QuotaOK || (len(q.Windows) == 0 && q.Balance == nil) {
		return nil
	}
	if !IsQuotaTool(q.Tool) {
		return nil
	}
	path := filepath.Join(dir, LastQuotasFile)
	var file lastQuotasFile
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &file)
	}
	entry := &lastQuota{
		At:           q.FetchedAt,
		Plan:         q.Plan,
		Windows:      q.Windows,
		Balance:      q.Balance,
		ResetCredits: q.ResetCredits,
	}
	switch q.Tool {
	case ToolClaude:
		file.Claude = entry
	case ToolCodex:
		file.Codex = entry
	default:
		if file.Tools == nil {
			file.Tools = map[Tool]*lastQuota{}
		}
		file.Tools[q.Tool] = entry
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.WriteFile(dir, LastQuotasFile, data, 0o600)
}
