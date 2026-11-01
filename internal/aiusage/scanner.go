// Scanner and its incremental file cache. The scanner walks every supported
// reader's session-log directory under home, parses each file into compact
// records, and persists per-file state (size, consumed offset, records) into
// one JSON file under cacheDir so later scans only read new bytes. Readers
// are always all on: an agent that is not installed contributes nothing.
package aiusage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"
)

// rec is one usage record in the scanner's compact form. at is Unix
// milliseconds; key is the dedupe key ("" = never deduplicated).
type rec struct {
	at              int64
	model           *string // interned; nil = unknown
	in, out, cr, cw int64
	cost            float64
	priced          bool
	key             string
}

func (r rec) tokens() int64 { return r.in + r.out + r.cr + r.cw }

// codexTail is the parser state needed to resume a Codex file mid-way: the
// current model (last turn_context) and the previous token_count event's
// total_token_usage signature for duplicate suppression.
type codexTail struct {
	model    string
	prev     [6]int64
	havePrev bool
}

// fileState is what the cache keeps per scanned file. offset is the count
// of consumed bytes: everything up to and including the last complete
// '\n'-terminated line. tailStart/tailSum identify that last complete line
// so a later scan can tell "file grew by appending" apart from "file was
// replaced with a bigger one".
type fileState struct {
	tool      Tool
	size      int64 // file size observed right after the read that produced offset
	offset    int64
	modNano   int64 // ModTime of that observation
	tailStart int64 // offset of the last complete line; -1 when none
	tailSum   uint64
	recs      []rec
	tail      codexTail
}

// Scanner reads local AI-tool session logs and summarizes their token
// usage. Construct with NewScanner, populate with Scan, read with Summary.
//
// Locking: files (and the rec slices inside each fileState) are guarded by
// mu and swapped atomically at the end of every Scan; rec slices are never
// mutated in place. Summary takes RLock, so it is safe to call it from
// another goroutine while no Scan runs — and even during a Scan it keeps
// seeing the previous consistent snapshot.
type Scanner struct {
	home, cacheDir string

	mu    sync.RWMutex
	files map[string]*fileState
}

// NewScanner makes a Scanner for the given home directory (the parent of
// .omp/.claude/.codex/.pi/.grok) and cache directory. Every supported reader
// is scanned; see agentSpecs.
func NewScanner(home, cacheDir string) *Scanner {
	return &Scanner{home: home, cacheDir: cacheDir, files: map[string]*fileState{}}
}

// wholeParser decodes a file that is one JSON document, not JSONL.
type wholeParser func([]byte, map[string]*string) ([]rec, error)

// fileSpec is how one collected file is parsed.
type fileSpec struct {
	tool  Tool
	whole bool
	parse wholeParser
}

// cacheName is the single cache file inside cacheDir.
const cacheName = "usage-scan-cache.json"

// progressEvery throttles progress callbacks to ~4 MiB of read bytes.
const progressEvery = 4 << 20

// Scan walks every supported reader's data location and updates the
// scanner's records and the on-disk cache. Files unchanged since the last
// scan are skipped; grown JSONL files are read from the last complete line;
// shrunk or replaced files are reread; deleted files are dropped. Whole-JSON
// files (Grok's usage.json) and the opencode database are reread and
// replaced as a unit when they change. progress, when non-nil, receives
// bytes read so far and the total bytes this scan plans to read. A reader
// that fails is skipped for this pass: its records are left out of the
// snapshot, so the page's one total only covers readers that answered, and
// every other reader still counts. Only ctx cancellation makes Scan return
// an error — the progress already made is still committed, but the on-disk
// cache is only written for a finished pass.
func (s *Scanner) Scan(ctx context.Context, progress func(done, total int64)) error {
	// Honour a cancel that arrived before the scan even started: no cache
	// read, no tree walk, no stat — and, below, no cache write.
	if err := ctx.Err(); err != nil {
		return err
	}
	s.loadCache(ctx)

	specs := agentSpecs(s.home)
	want := map[string]fileSpec{}
	failed := map[Tool]bool{}
	dbPath := ""
	if s.home == "" {
		s.mu.Lock()
		s.files = map[string]*fileState{}
		s.mu.Unlock()
		return nil
	}
	for _, sp := range specs {
		if sp.db != "" {
			dbPath = sp.db
			continue
		}
		for _, root := range sp.roots {
			paths, err := collectLogs(ctx, root.dir, root.match)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				failed[sp.tool] = true
				continue
			}
			for _, p := range paths {
				spec := fileSpec{tool: sp.tool, whole: root.whole}
				if root.whole && sp.tool == ToolGrok {
					spec.parse = parseGrokUsageFile
				}
				want[p] = spec
			}
		}
	}

	s.mu.RLock()
	prev := s.files
	s.mu.RUnlock()

	// Carry the opencode database state (handled below) and drop every
	// other cached file not seen in this walk. Every parsed record stays in
	// its own file's state — duplicates are removed when Summary
	// aggregates, so a surviving copy still counts when the file that "won"
	// a key disappears.
	files := make(map[string]*fileState, len(prev))
	for p, st := range prev {
		if st == nil {
			continue
		}
		if _, ok := want[p]; ok {
			continue
		}
		if dbPath != "" && p == dbPath {
			continue
		}
	}

	type job struct {
		path string
		spec fileSpec
		from int64
		st   *fileState
	}
	var jobs []job
	var total int64
	var scanErr error
	for path, spec := range want {
		if err := ctx.Err(); err != nil { // stop the stat walk on cancel
			scanErr = err
			break
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue // vanished between walk and stat: drop
		}
		old := prev[path]
		if spec.whole {
			if old != nil && fi.Size() == old.size && fi.ModTime().UnixNano() == old.modNano {
				files[path] = old
				continue
			}
			jobs = append(jobs, job{path, spec, 0, nil})
			total += fi.Size()
			continue
		}
		switch {
		case old == nil:
			jobs = append(jobs, job{path, spec, 0, nil})
			total += fi.Size()
		case fi.Size() == old.size && fi.ModTime().UnixNano() == old.modNano:
			files[path] = old // exactly what the last read observed: unchanged
		case fi.Size() <= old.offset:
			// Shrunk, same size but touched, or replaced down to the last
			// consumed offset (a partial tail makes old.size > old.offset):
			// the content changed, reread everything.
			jobs = append(jobs, job{path, spec, 0, nil})
			total += fi.Size()
		default:
			// Grown past the last complete line: verify the old last line
			// still sits where it was (append vs. replace check), then
			// read the tail.
			from := old.offset
			if old.tailStart >= 0 && !tailIntact(path, old) {
				from = 0
			}
			if from == 0 {
				jobs = append(jobs, job{path, spec, 0, nil})
				total += fi.Size()
			} else {
				st := &fileState{tool: spec.tool, recs: slices.Clone(old.recs), tail: old.tail,
					tailStart: old.tailStart, tailSum: old.tailSum}
				files[path] = st
				jobs = append(jobs, job{path, spec, from, st})
				total += fi.Size() - from
			}
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].path < jobs[j].path })

	if progress != nil {
		progress(0, total)
	}
	var done, lastReport int64
	pool := map[string]*string{}
loop:
	for _, jb := range jobs {
		if err := ctx.Err(); err != nil {
			scanErr = err
			break
		}
		st := jb.st
		if st == nil {
			st = &fileState{tool: jb.spec.tool}
		}
		var err error
		if jb.spec.whole {
			err = scanWholeFile(ctx, jb.path, st, jb.spec.parse, pool, func(n int64) {
				done += n
				if progress != nil && done-lastReport >= progressEvery {
					lastReport = done
					progress(done, total)
				}
			})
		} else {
			err = scanFile(ctx, jb.path, jb.from, st, pool, func(n int64) {
				done += n
				if progress != nil && done-lastReport >= progressEvery {
					lastReport = done
					progress(done, total)
				}
			})
		}
		if err != nil {
			if ctx.Err() != nil {
				scanErr = ctx.Err()
				break loop
			}
			failed[jb.spec.tool] = true
			continue
		}
		if fi, err2 := os.Stat(jb.path); err2 == nil {
			st.size, st.modNano = fi.Size(), fi.ModTime().UnixNano()
		} else {
			st.size, st.modNano = st.offset, time.Now().UnixNano()
		}
		files[jb.path] = st
	}
	if progress != nil {
		progress(done, total)
	}

	// opencode: replace the whole record set when the database (or its WAL)
	// changed. A read failure marks the reader failed; its records are
	// dropped below so the total only covers readers that answered.
	if dbPath != "" {
		if size, mod, ok := dbSignature(dbPath); ok {
			old := prev[dbPath]
			if old != nil && old.size == size && old.modNano == mod {
				files[dbPath] = old
			} else if recs, err := readOpencode(ctx, dbPath, pool); err != nil {
				if ctx.Err() != nil {
					scanErr = ctx.Err()
				}
				failed[ToolOpencode] = true
			} else {
				files[dbPath] = &fileState{tool: ToolOpencode, size: size, modNano: mod, recs: recs}
			}
		}
	}

	// A reader that failed anywhere in this pass is skipped as a whole: the
	// page shows one total, and a partial reader would silently undercount.
	if len(failed) > 0 {
		for p, st := range files {
			if st != nil && failed[st.tool] {
				delete(files, p)
			}
		}
	}

	s.mu.Lock()
	s.files = files
	s.mu.Unlock()
	// A cancelled pass commits its in-memory progress but must not touch
	// the disk: saveCache re-checks ctx through serialization and again
	// before it writes, so the previous cache survives any cancellation.
	s.saveCache(ctx, files)
	return scanErr
}

// dbSignature reports the opencode database's size and newest modification
// time, including the WAL: SQLite may append there without touching the main
// file until a checkpoint.
func dbSignature(path string) (size, mod int64, ok bool) {
	for _, p := range []string{path, path + "-wal"} {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		ok = true
		size += fi.Size()
		if m := fi.ModTime().UnixNano(); m > mod {
			mod = m
		}
	}
	return size, mod, ok
}

// collectLogs returns every file under root matching the glob basename. A
// missing root is empty, not an error; an unreadable directory is reported
// so the reader's status can say 读取失败. The walk stops the moment ctx is
// cancelled.
func collectLogs(ctx context.Context, root, match string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	var firstErr error
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if ok, _ := filepath.Match(match, d.Name()); ok {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return out, firstErr
}

// scanWholeFile replaces st's records with a whole JSON file's. The file is
// small (Grok's per-session usage.json), so a change rereads all of it.
func scanWholeFile(ctx context.Context, path string, st *fileState, parse wholeParser, pool map[string]*string, advance func(int64)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if parse == nil {
		return errors.New("aiusage: no parser for whole file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	recs, err := parse(data, pool)
	if err != nil {
		return err
	}
	st.recs = recs
	advance(int64(len(data)))
	return nil
}

// tailIntact re-reads the last complete line recorded for the file and
// checks its hash: match = pure append, mismatch = replaced file.
func tailIntact(path string, st *fileState) bool {
	f, err := os.Open(path)
	if err != nil {
		return true // unreadable now; let the main read deal with it
	}
	defer f.Close()
	buf := make([]byte, st.offset-st.tailStart)
	if _, err := f.ReadAt(buf, st.tailStart); err != nil {
		return false
	}
	h := fnv.New64a()
	h.Write(buf)
	return h.Sum64() == st.tailSum
}

// scanFile reads lines of one file starting at byte offset `from`, parses
// records into st, and leaves st.offset/tail* at the last complete line.
// Resuming mid-file keeps the fingerprint the caller copied into st until
// another complete line replaces it; a full reread resets it. advance
// reports consumed complete-line bytes for progress.
func scanFile(ctx context.Context, path string, from int64, st *fileState, pool map[string]*string, advance func(int64)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return err
	}
	st.offset = from
	if from == 0 {
		st.tailStart, st.tailSum = -1, 0
	}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := r.ReadBytes('\n')
		n := len(line)
		if n > 0 && line[n-1] == '\n' {
			st.tailStart, st.tailSum = st.offset, hashLine(line)
			st.offset += int64(n)
			advance(int64(n))
			st.parseLine(line, pool)
		}
		if err != nil {
			if err == io.EOF {
				return nil // partial tail (if any) stays unconsumed
			}
			return err
		}
	}
}

func hashLine(b []byte) uint64 {
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}

// parseLine routes one complete log line to the tool's parser and appends
// the record it yields to st.recs, keeping its dedupe key: keys are applied
// at aggregation time (Summary), not while parsing, so duplicate copies
// survive in every file that holds them.
func (st *fileState) parseLine(line []byte, pool map[string]*string) {
	var (
		r   rec
		key string
		ok  bool
	)
	switch st.tool {
	case ToolOmp, ToolPi:
		r, key, ok = parseOmpLine(line, pool)
	case ToolClaude:
		r, key, ok = parseClaudeLine(line, pool)
	case ToolCodex:
		r, ok = st.tail.parseCodexLine(line, pool)
	case ToolGrok:
		r, key, ok = parseGrokUpdateLine(line, pool)
	default:
		return
	}
	if !ok {
		return
	}
	r.key = key
	st.recs = append(st.recs, r)
}

func intern(pool map[string]*string, s string) *string {
	if s == "" {
		return nil
	}
	if p, ok := pool[s]; ok {
		return p
	}
	p := new(string)
	*p = s
	pool[s] = p
	return p
}

// ---------- cache ----------

type cacheTailJSON struct {
	M string  `json:"m"`
	P []int64 `json:"p"`
	H int     `json:"h"`
}

type cacheFileJSON struct {
	T  string              `json:"t"`
	S  int64               `json:"s"`
	O  int64               `json:"o"`
	M  int64               `json:"m"`
	TS int64               `json:"ls"`
	TH uint64              `json:"lh"`
	R  [][]json.RawMessage `json:"r"`
	C  *cacheTailJSON      `json:"c,omitempty"`
}

type cacheJSON struct {
	V     int                       `json:"v"`
	Files map[string]*cacheFileJSON `json:"files"`
}

// cacheVersion identifies the cache file's format and semantics; version 1
// caches dropped duplicate copies at parse time, so bumping to 2 forces one
// full rescan that repopulates them.
const cacheVersion = 2

func (s *Scanner) cachePath() string { return filepath.Join(s.cacheDir, cacheName) }

// loadCache replaces the scanner's file states with the cache's, if it is
// readable and the right version. Called at the top of every Scan, so a
// fresh Scanner picks up a previous instance's work. A cancelled ctx skips
// the read and stops the decode midway (the partial map is discarded).
func (s *Scanner) loadCache(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	data, err := os.ReadFile(s.cachePath())
	if err != nil {
		return
	}
	var cj cacheJSON
	if json.Unmarshal(data, &cj) != nil || cj.V != cacheVersion {
		return
	}
	files := make(map[string]*fileState, len(cj.Files))
	pool := map[string]*string{}
	for path, cf := range cj.Files {
		if ctx.Err() != nil {
			return // decode interrupted: drop the partial map
		}
		if cf == nil {
			continue
		}
		st := &fileState{
			tool:      Tool(cf.T),
			size:      cf.S,
			offset:    cf.O,
			modNano:   cf.M,
			tailStart: cf.TS,
			tailSum:   cf.TH,
		}
		if cf.C != nil {
			st.tail.model = cf.C.M
			st.tail.havePrev = cf.C.H != 0
			for i := range min(len(cf.C.P), 6) {
				st.tail.prev[i] = cf.C.P[i]
			}
		}
		for _, tup := range cf.R {
			if r, ok := decodeRec(tup, pool); ok {
				st.recs = append(st.recs, r)
			}
		}
		files[path] = st
	}
	s.mu.Lock()
	s.files = files
	s.mu.Unlock()
}

// rec tuple: [atMs, model|"", in, out, cacheRead, cacheWrite, cost, priced01, key]
func decodeRec(tup []json.RawMessage, pool map[string]*string) (rec, bool) {
	var r rec
	if len(tup) < 9 {
		return r, false
	}
	at, err := rawI64(tup[0])
	if err != nil {
		return r, false
	}
	model := rawStr(tup[1])
	in, e1 := rawI64(tup[2])
	out, e2 := rawI64(tup[3])
	cr, e3 := rawI64(tup[4])
	cw, e4 := rawI64(tup[5])
	cost, e5 := rawF64(tup[6])
	priced, e6 := rawI64(tup[7])
	key := rawStr(tup[8])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil {
		return r, false
	}
	r.at, r.model = at, intern(pool, model)
	r.in, r.out, r.cr, r.cw = in, out, cr, cw
	r.cost, r.priced, r.key = cost, priced != 0, key
	return r, true
}

func rawI64(b json.RawMessage) (int64, error) {
	return strconv.ParseInt(string(bytes.TrimSpace(b)), 10, 64)
}

func rawF64(b json.RawMessage) (float64, error) {
	return strconv.ParseFloat(string(bytes.TrimSpace(b)), 64)
}

func rawStr(b json.RawMessage) string {
	if len(b) == 0 || b[0] != '"' {
		return ""
	}
	var s string
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	return s
}

// saveCache writes files to cacheDir atomically (temp file + rename).
// Errors degrade silently: the scan results stay valid, only the next
// scan's speed suffers. A cancelled ctx aborts during serialization and
// before the write; a partially written temp file is removed, the previous
// cache stays intact.
func (s *Scanner) saveCache(ctx context.Context, files map[string]*fileState) {
	if s.cacheDir == "" || ctx.Err() != nil {
		return
	}
	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return
	}
	var buf bytes.Buffer
	buf.Grow(1 << 20)
	buf.WriteString(`{"v":`)
	buf.WriteString(strconv.Itoa(cacheVersion))
	buf.WriteString(`,"files":{`)
	first := true
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if ctx.Err() != nil {
			return // serialization interrupted: nothing written yet
		}
		st := files[p]
		if st == nil {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		buf.WriteString(strconv.Quote(p))
		buf.WriteString(`:{`)
		buf.WriteString(`"t":` + strconv.Quote(string(st.tool)))
		buf.WriteString(`,"s":` + strconv.FormatInt(st.size, 10))
		buf.WriteString(`,"o":` + strconv.FormatInt(st.offset, 10))
		buf.WriteString(`,"m":` + strconv.FormatInt(st.modNano, 10))
		buf.WriteString(`,"ls":` + strconv.FormatInt(st.tailStart, 10))
		buf.WriteString(`,"lh":` + strconv.FormatUint(st.tailSum, 10))
		if st.tail.havePrev || st.tail.model != "" {
			buf.WriteString(`,"c":{"m":` + strconv.Quote(st.tail.model) + `,"p":[`)
			for i, v := range st.tail.prev {
				if i > 0 {
					buf.WriteByte(',')
				}
				buf.WriteString(strconv.FormatInt(v, 10))
			}
			buf.WriteString(`],"h":`)
			h := 0
			if st.tail.havePrev {
				h = 1
			}
			buf.WriteString(strconv.Itoa(h) + `}`)
		}
		buf.WriteString(`,"r":[`)
		for i, r := range st.recs {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteByte('[')
			buf.WriteString(strconv.FormatInt(r.at, 10))
			buf.WriteByte(',')
			m := ""
			if r.model != nil {
				m = *r.model
			}
			buf.WriteString(strconv.Quote(m))
			buf.WriteByte(',')
			buf.WriteString(strconv.FormatInt(r.in, 10))
			buf.WriteByte(',')
			buf.WriteString(strconv.FormatInt(r.out, 10))
			buf.WriteByte(',')
			buf.WriteString(strconv.FormatInt(r.cr, 10))
			buf.WriteByte(',')
			buf.WriteString(strconv.FormatInt(r.cw, 10))
			buf.WriteByte(',')
			cost := r.cost
			if math.IsNaN(cost) || math.IsInf(cost, 0) {
				cost = 0
			}
			buf.WriteString(strconv.FormatFloat(cost, 'g', -1, 64))
			buf.WriteByte(',')
			p := 0
			if r.priced {
				p = 1
			}
			buf.WriteString(strconv.Itoa(p))
			buf.WriteByte(',')
			buf.WriteString(strconv.Quote(r.key))
			buf.WriteByte(']')
		}
		buf.WriteString(`]}`)
	}
	buf.WriteString(`}}`)

	if ctx.Err() != nil {
		return // caller already stopped: leave the old cache in place
	}
	tmp, err := os.CreateTemp(s.cacheDir, ".usage-scan-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, s.cachePath()); err != nil {
		os.Remove(tmpName)
	}
}
