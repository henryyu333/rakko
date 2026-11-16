//go:build !windows

package aiusage

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func querySQLiteJSON(ctx context.Context, tag, dbPath, query string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, sqlite3Path, "-json", dbPath, query)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s: sqlite3: %s", tag, msg)
	}
	body := bytes.TrimSpace(stdout.Bytes())
	if len(body) == 0 || bytes.Equal(body, []byte("[]")) {
		return nil, nil
	}
	return body, nil
}
