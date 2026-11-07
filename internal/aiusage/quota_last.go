package aiusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// LastQuotasFile is the file inside the data directory that keeps the last
// good quota reading per tool, so a restart shows what was last read
// instead of an empty card. It mirrors Magpie's quotas.json.
const LastQuotasFile = "quotas.json"

type lastQuotasFile struct {
	Claude *lastQuota `json:"claude,omitempty"`
	Codex  *lastQuota `json:"codex,omitempty"`
}

type lastQuota struct {
	At      time.Time `json:"at"`
	Plan    string    `json:"plan,omitempty"`
	Windows []Window  `json:"windows"`
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
	var out []Quota
	for _, entry := range []struct {
		tool Tool
		q    *lastQuota
	}{
		{ToolClaude, file.Claude},
		{ToolCodex, file.Codex},
	} {
		if entry.q == nil || len(entry.q.Windows) == 0 {
			continue
		}
		out = append(out, Quota{
			Tool:      entry.tool,
			Plan:      entry.q.Plan,
			Windows:   entry.q.Windows,
			Status:    QuotaOK,
			FetchedAt: entry.q.At,
		})
	}
	return out
}

// SaveLastQuota records one good reading. A failure is returned but must
// not disturb what the page shows: the data is a convenience across
// restarts, not the source of truth.
func SaveLastQuota(dir string, q Quota) error {
	if dir == "" || q.Status != QuotaOK || len(q.Windows) == 0 {
		return nil
	}
	path := filepath.Join(dir, LastQuotasFile)
	var file lastQuotasFile
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &file)
	}
	entry := &lastQuota{At: q.FetchedAt, Plan: q.Plan, Windows: q.Windows}
	switch q.Tool {
	case ToolClaude:
		file.Claude = entry
	case ToolCodex:
		file.Codex = entry
	default:
		return nil
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "."+LastQuotasFile+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return nil
}
