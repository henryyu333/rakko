package aiusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// readSQLiteJSON copies a SQLite database (plus any -wal) into cacheDir and
// runs query, unmarshaling the resulting JSON rows into dst. Retries up to 3
// times to ride out mid-checkpoint WAL races.
func readSQLiteJSON(ctx context.Context, tag, dbPath, cacheDir, query string, dst any) error {
	var err error
	for range 3 {
		if err = readSQLiteJSONOnce(ctx, tag, dbPath, cacheDir, query, dst); err == nil || ctx.Err() != nil {
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

func readSQLiteJSONOnce(ctx context.Context, tag, dbPath, cacheDir, query string, dst any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	copyPath, cleanup, err := copySQLiteDB(ctx, tag, dbPath, cacheDir)
	if err != nil {
		return err
	}
	defer cleanup()
	raw, err := querySQLiteJSON(ctx, tag, copyPath, query)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s: decoding sqlite output: %w", tag, err)
	}
	return nil
}

func copySQLiteDB(ctx context.Context, tag, dbPath, cacheDir string) (string, func(), error) {
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
		if err := copyFile(src, dst+suf); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return dst, cleanup, nil
}
