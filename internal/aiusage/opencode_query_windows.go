//go:build windows

package aiusage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // the pure-Go driver: Windows has no sqlite3 shell
)

// opencodeDBPath is where opencode keeps its database on Windows. opencode
// resolves its data directory as XDG_DATA_HOME or ~/.local/share (its
// global-roots module has no Windows branch), so the default layout matches
// macOS; an explicit XDG_DATA_HOME moves it.
func opencodeDBPath(home string) string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "opencode", "opencode.db")
	}
	return joinHome(home, ".local", "share", "opencode", "opencode.db")
}

// queryOpencode runs the read-only query against a copy and returns one row
// per assistant message. The database is opened read-write because a WAL
// copy needs its -shm; only SELECT runs against it and it is thrown away
// with the copy.
func queryOpencode(ctx context.Context, dbPath string) ([]opencodeRow, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opencode: sqlite: %w", err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, opencodeQuery)
	if err != nil {
		return nil, fmt.Errorf("opencode: sqlite: %w", err)
	}
	defer rows.Close()
	var out []opencodeRow
	for rows.Next() {
		var (
			row    opencodeRow
			model  sql.NullString
			input  sql.NullInt64
			output sql.NullInt64
			cacheR sql.NullInt64
			cacheW sql.NullInt64
			cost   sql.NullFloat64
		)
		if err := rows.Scan(&row.TimeCreated, &row.ID, &model, &input, &output, &cacheR, &cacheW, &cost); err != nil {
			return nil, fmt.Errorf("opencode: sqlite: %w", err)
		}
		if model.Valid {
			row.Model = &model.String
		}
		if input.Valid {
			row.Input = &input.Int64
		}
		if output.Valid {
			row.Output = &output.Int64
		}
		if cacheR.Valid {
			row.CacheRead = &cacheR.Int64
		}
		if cacheW.Valid {
			row.CacheWrite = &cacheW.Int64
		}
		if cost.Valid {
			row.Cost = &cost.Float64
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("opencode: sqlite: %w", err)
	}
	return out, nil
}
