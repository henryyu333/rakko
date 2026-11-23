package aiusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// errUnsupportedDBSchema indicates a SQLite database file exists and is readable,
// but lacks the expected table(s) for that tool (e.g. a pre-telemetry DB).
var errUnsupportedDBSchema = errors.New("unsupported database schema")

// sqliteCopyHook is an optional test seam called whenever copySQLiteDB creates
// a temporary read copy.
var sqliteCopyHook func()

type sqliteCopy struct {
	tag  string
	path string
}

func (c sqliteCopy) tableExists(ctx context.Context, table string) (bool, error) {
	var tables []struct {
		Name string `json:"name"`
	}
	q := fmt.Sprintf("SELECT name FROM sqlite_master WHERE type='table' AND name='%s'", table)
	if err := c.queryJSON(ctx, q, &tables); err != nil {
		return false, err
	}
	return len(tables) > 0, nil
}

func (c sqliteCopy) requireTable(ctx context.Context, table string) error {
	ok, err := c.tableExists(ctx, table)
	if err != nil {
		return err
	}
	if !ok {
		return errUnsupportedDBSchema
	}
	return nil
}

func (c sqliteCopy) queryJSON(ctx context.Context, query string, dst any) error {
	raw, err := querySQLiteJSON(ctx, c.tag, c.path, query)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s: decoding sqlite output: %w", c.tag, err)
	}
	return nil
}

// withSQLiteCopy copies a SQLite database (plus any -wal) into cacheDir once
// per attempt and runs fn against that private copy, retrying up to 3 times to
// ride out mid-checkpoint WAL races.
func withSQLiteCopy(ctx context.Context, tag, dbPath, cacheDir string, fn func(sqliteCopy) error) error {
	var err error
	for range 3 {
		err = withSQLiteCopyOnce(ctx, tag, dbPath, cacheDir, fn)
		if err == nil || ctx.Err() != nil || errors.Is(err, errUnsupportedDBSchema) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return err
}

func withSQLiteCopyOnce(ctx context.Context, tag, dbPath, cacheDir string, fn func(sqliteCopy) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	copyPath, cleanup, err := copySQLiteDB(ctx, tag, dbPath, cacheDir)
	if err != nil {
		return err
	}
	defer cleanup()
	return fn(sqliteCopy{tag: tag, path: copyPath})
}

func copySQLiteDB(ctx context.Context, tag, dbPath, cacheDir string) (string, func(), error) {
	if sqliteCopyHook != nil {
		sqliteCopyHook()
	}
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return "", nil, fmt.Errorf("%s: making the read copy: %w", tag, err)
		}
	}
	dir, err := os.MkdirTemp(cacheDir, tag+"-")
	if err != nil {
		return "", nil, fmt.Errorf("%s: making the read copy: %w", tag, err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	dst := filepath.Join(dir, "db.sqlite")
	for _, suf := range []string{"", "-wal"} {
		if err := ctx.Err(); err != nil {
			cleanup()
			return "", nil, err
		}
		src := dbPath + suf
		if suf != "" {
			if _, err := os.Stat(src); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				cleanup()
				return "", nil, fmt.Errorf("%s: reading %s: %w", tag, src, err)
			}
		}
		if err := copyFile(tag, src, dst+suf); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return dst, cleanup, nil
}

// copyFile copies src to dst with the owner's read/write mode.
func copyFile(tag, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("%s: reading %s: %w", tag, src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("%s: writing the read copy: %w", tag, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("%s: copying %s: %w", tag, src, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("%s: copying %s: %w", tag, src, err)
	}
	return nil
}
