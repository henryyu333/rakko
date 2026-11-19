//go:build windows

package aiusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func querySQLiteJSON(ctx context.Context, tag, dbPath, query string) ([]byte, error) {
	return runSQLiteJSON(ctx, tag, dbPath, query, false)
}

func querySQLiteJSONReadOnly(ctx context.Context, tag, dbPath, query string) ([]byte, error) {
	return runSQLiteJSON(ctx, tag, dbPath, query, true)
}

func runSQLiteJSON(ctx context.Context, tag, dbPath, query string, readOnly bool) ([]byte, error) {
	dsn := dbPath
	if readOnly {
		p := filepath.ToSlash(dbPath)
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		dsn = (&url.URL{
			Scheme:   "file",
			Path:     p,
			RawQuery: "mode=ro&_pragma=busy_timeout(3000)&_pragma=query_only(1)",
		}).String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: sqlite: %w", tag, err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%s: sqlite: %w", tag, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("%s: sqlite: %w", tag, err)
	}
	var out []map[string]any
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range ptrs {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("%s: sqlite: %w", tag, err)
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
			}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: sqlite: %w", tag, err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return json.Marshal(out)
}
