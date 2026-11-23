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
// The database is read from a private copy, never the user's file: macOS
// uses its /usr/bin/sqlite3, Windows the pure-Go SQLite driver that ships
// with the app. A full re-read on every change is cheap (the compact
// projection of ~4k messages is under 1 MB) and avoids missing a message
// whose tokens were finalized after its row was created.

import "context"

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
	// opencode may be writing while the files are copied, and a copy taken
	// mid-checkpoint can fail to open. withSQLiteCopy retries on transient
	// opens and cleans up the private copy directory after reading.
	var rows []opencodeRow
	if err := withSQLiteCopy(ctx, "opencode", dbPath, cacheDir, func(db sqliteCopy) error {
		if err := db.requireTable(ctx, "message"); err != nil {
			return err
		}
		return db.queryJSON(ctx, opencodeQuery, &rows)
	}); err != nil {
		return nil, err
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
