package aiusage

// opencode usage: ~/.local/share/opencode/opencode.db
//
// opencode 2.x keeps messages in a SQLite database. An assistant message's
// data JSON carries the final numbers:
//   {"role":"assistant","modelID":"deepseek-v4-flash","providerID":"deepseek",
//    "cost":0.000950236,
//    "tokens":{"total":8595,"input":6601,"output":30,"reasoning":44,
//              "cache":{"write":0,"read":1920}}}
//
// The database is read through macOS's /usr/bin/sqlite3 in read-only mode;
// this avoids a SQLite dependency and never writes to the file. A full
// re-read on every change is cheap (the compact projection of ~4k messages
// is under 1 MB) and avoids missing a message whose tokens were finalized
// after its row was created.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	opencodeDB  = ".local/share/opencode/opencode.db"
	sqlite3Path = "/usr/bin/sqlite3"
)

const opencodeQuery = `
SELECT time_created,
       id,
       json_extract(data, '$.modelID') AS model,
       json_extract(data, '$.tokens.input') AS input,
       json_extract(data, '$.tokens.output') AS output,
       json_extract(data, '$.tokens.cache.read') AS cache_read,
       json_extract(data, '$.tokens.cache.write') AS cache_write,
       json_extract(data, '$.cost') AS cost
FROM message
WHERE json_extract(data, '$.role') = 'assistant'
  AND json_extract(data, '$.tokens') IS NOT NULL
ORDER BY time_created`

// opencodeRow is one compact query result. Nullable columns are pointers so
// a missing price stays unpriced rather than becoming $0.
type opencodeRow struct {
	TimeCreated int64    `json:"time_created"`
	ID          string   `json:"id"`
	Model       *string  `json:"model"`
	Input       *int64   `json:"input"`
	Output      *int64   `json:"output"`
	CacheRead   *int64   `json:"cache_read"`
	CacheWrite  *int64   `json:"cache_write"`
	Cost        *float64 `json:"cost"`
}

func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// readOpencode runs the read-only query and returns one record per assistant
// message. The caller replaces the whole opencode record set with the result.
//
// The query runs against a private copy. opencode keeps its database in WAL
// mode, and a read-only SQLite connection cannot create the -shm a WAL
// database needs — sqlite3 -readonly fails on it, and a writable open of
// the user's file would create -shm/-wal beside it. The copy and the files
// SQLite makes for it live in the scanner's cache directory and are removed
// afterwards; only SELECT runs against it.
func readOpencode(ctx context.Context, dbPath, cacheDir string, pool map[string]*string) ([]rec, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	copyPath, cleanup, err := copyOpencodeDB(ctx, dbPath, cacheDir)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	// No -readonly: the connection must be able to create the copy's -shm.
	cmd := exec.CommandContext(ctx, sqlite3Path, "-json", copyPath, opencodeQuery)
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
	out := make([]rec, 0, len(rows))
	for _, row := range rows {
		if row.TimeCreated == 0 {
			continue
		}
		r := rec{
			at:    row.TimeCreated,
			model: intern(pool, derefStr(row.Model)),
			in:    derefInt(row.Input),
			out:   derefInt(row.Output),
			cr:    derefInt(row.CacheRead),
			cw:    derefInt(row.CacheWrite),
			key:   row.ID,
		}
		if row.Cost != nil {
			r.cost, r.priced = *row.Cost, true
		}
		out = append(out, r)
	}
	return out, nil
}

// copyOpencodeDB copies the database and an existing WAL into a private
// directory under cacheDir (the system temporary directory when the shell
// has no cache directory) and returns the database path there. cleanup
// removes the whole directory: the database, its WAL and the -shm/-wal
// SQLite created for the copy.
func copyOpencodeDB(ctx context.Context, dbPath, cacheDir string) (string, func(), error) {
	if cacheDir != "" {
		// The first scan of a new install runs before the scan cache has
		// created this directory.
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return "", nil, fmt.Errorf("opencode: making the read copy: %w", err)
		}
	}
	dir, err := os.MkdirTemp(cacheDir, "opencode-")
	if err != nil {
		return "", nil, fmt.Errorf("opencode: making the read copy: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	dst := filepath.Join(dir, "opencode.db")
	for _, name := range []string{"", "-wal"} {
		if err := ctx.Err(); err != nil {
			cleanup()
			return "", nil, err
		}
		src := dbPath + name
		if name != "" {
			if _, err := os.Stat(src); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				cleanup()
				return "", nil, fmt.Errorf("opencode: reading %s: %w", src, err)
			}
		}
		if err := copyFile(src, dst+name); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return dst, cleanup, nil
}

// copyFile copies src to dst with the owner's read/write mode.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opencode: reading %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("opencode: writing the read copy: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("opencode: copying %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("opencode: copying %s: %w", src, err)
	}
	return nil
}
