//go:build !windows

package aiusage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// sqlite3Path is the system's SQLite shell, used as a read-only query tool.
const sqlite3Path = "/usr/bin/sqlite3"

// opencodeDBPath is where opencode keeps its database on this platform.
func opencodeDBPath(home string) string {
	return filepath.Join(xdgDataHome(home), "opencode", "opencode.db")
}

// queryOpencode runs the read-only query against a copy and returns one row
// per assistant message. No -readonly: the connection must be able to create
// the copy's -shm.
func queryOpencode(ctx context.Context, dbPath string) ([]opencodeRow, error) {
	cmd := exec.CommandContext(ctx, sqlite3Path, "-json", dbPath, opencodeQuery)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("opencode: sqlite3: %s", msg)
	}
	body := bytes.TrimSpace(stdout.Bytes())
	if len(body) == 0 || bytes.Equal(body, []byte("[]")) {
		return nil, nil
	}
	var rows []opencodeRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("opencode: decoding sqlite3 output: %w", err)
	}
	return rows, nil
}
