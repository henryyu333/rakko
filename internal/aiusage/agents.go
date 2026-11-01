package aiusage

// The local readers this package knows, where each one keeps its data, and
// the read-only probe the settings page uses to say 已识别 / 读取失败 /
// 未发现. Every reader is always on: a tool that is not installed simply
// reports NotFound, and a new install shows up on the next scan.

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Status is one reader's state, as the settings page lists it.
type Status int

const (
	// StatusFound means the reader's data location exists and is readable.
	StatusFound Status = iota
	// StatusReadFailed means the location exists but a walk or read failed.
	StatusReadFailed
	// StatusNotFound means the agent is not installed or has no logs yet.
	StatusNotFound
	// StatusUnsupported means the agent keeps usage on its servers.
	StatusUnsupported
)

// AgentStatus is one row of the settings page's 本机用量 list.
type AgentStatus struct {
	Tool   Tool
	Name   string
	Path   string // display path, "~/" shortened
	Status Status
	Err    string
}

// rootSpec is one directory a reader walks and the basename it collects.
// whole files are parsed as one JSON document; the rest are JSONL.
type rootSpec struct {
	dir   string
	match string
	whole bool
}

// agentSpec is one supported local reader. db is set instead of roots for
// readers whose data is a SQLite database.
type agentSpec struct {
	tool  Tool
	name  string
	roots []rootSpec
	db    string
}

// agentSpecs lists every supported reader. The order is the settings list's
// order: the readers that predate this task first, then the new ones.
func agentSpecs(home string) []agentSpec {
	join := func(p string) string { return filepath.Join(home, p) }
	return []agentSpec{
		{
			tool:  ToolOmp,
			name:  "omp",
			roots: []rootSpec{{join(".omp/agent/sessions"), "*.jsonl", false}},
		},
		{
			tool:  ToolClaude,
			name:  "Claude Code",
			roots: []rootSpec{{join(".claude/projects"), "*.jsonl", false}},
		},
		{
			tool: ToolCodex,
			name: "Codex",
			roots: []rootSpec{
				{join(".codex/sessions"), "rollout-*.jsonl", false},
				{join(".codex/archived_sessions"), "*.jsonl", false},
			},
		},
		{
			tool:  ToolPi,
			name:  "pi",
			roots: []rootSpec{{join(".pi/agent/sessions"), "*.jsonl", false}},
		},
		{
			tool: ToolOpencode,
			name: "opencode",
			db:   join(".local/share/opencode/opencode.db"),
		},
		{
			tool: ToolGrok,
			name: "Grok CLI",
			roots: []rootSpec{
				{join(".grok/sessions"), "usage.json", true},
				{join(".grok/sessions"), "updates.jsonl", false},
			},
		},
	}
}

// displayPath shortens a path under home to "~/…" for the settings list.
func displayPath(home, path string) string {
	if home == "" {
		return "~/" + filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return "~/" + filepath.ToSlash(rel)
}

// Probe reports each reader's state without parsing any records. It is the
// settings page's data source: cheap enough to repeat, and it never writes.
// An empty home reports every reader as NotFound instead of touching the
// process's real home.
func Probe(home string) []AgentStatus {
	specs := agentSpecs(home)
	out := make([]AgentStatus, 0, len(specs)+1)
	for _, sp := range specs {
		st := AgentStatus{Tool: sp.tool, Name: sp.name}
		switch {
		case sp.db != "":
			st.Path = displayPath(home, sp.db)
		case len(sp.roots) > 0:
			st.Path = displayPath(home, sp.roots[0].dir)
		}
		if home == "" {
			st.Status = StatusNotFound
		} else {
			st.Status, st.Err = probeAgent(sp)
		}
		out = append(out, st)
	}
	out = append(out, AgentStatus{Tool: ToolCursor, Name: "Cursor", Status: StatusUnsupported})
	return out
}

func probeAgent(sp agentSpec) (Status, string) {
	if sp.db != "" {
		return probeSQLite(sp.db)
	}
	failed := ""
	found := false
	for _, root := range sp.roots {
		hit, err := probeRoot(root)
		if err != "" {
			failed = err
			continue
		}
		if hit {
			found = true
		}
	}
	if failed != "" {
		return StatusReadFailed, failed
	}
	if found {
		return StatusFound, ""
	}
	return StatusNotFound, ""
}

// probeSQLite accepts a file with SQLite's 16-byte header. Checking the
// schema would mean running sqlite3 on every settings frame; the scan
// reports a broken schema as 读取失败 instead.
func probeSQLite(path string) (Status, string) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return StatusNotFound, ""
	}
	if err != nil {
		return StatusReadFailed, err.Error()
	}
	defer f.Close()
	var hdr [16]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return StatusReadFailed, err.Error()
	}
	if string(hdr[:]) != "SQLite format 3\x00" {
		return StatusReadFailed, "not a SQLite database"
	}
	return StatusFound, ""
}

// probeRoot reports whether root has at least one readable matching file.
// A missing root is not a failure; an unreadable directory or file is.
func probeRoot(root rootSpec) (bool, string) {
	d, err := os.Open(root.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, ""
	}
	if err != nil {
		return false, err.Error()
	}
	_, derr := d.ReadDir(1)
	d.Close()
	if derr != nil && derr != io.EOF {
		return false, derr.Error()
	}

	found := false
	failure := ""
	err = filepath.WalkDir(root.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			failure = err.Error()
			return fs.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		ok, _ := filepath.Match(root.match, d.Name())
		if !ok {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			failure = err.Error()
			return fs.SkipAll
		}
		var one [1]byte
		_, rerr := f.Read(one[:])
		f.Close()
		if rerr != nil && rerr != io.EOF {
			failure = rerr.Error()
			return fs.SkipAll
		}
		found = true
		return fs.SkipAll
	})
	if err != nil && err != fs.SkipAll && failure == "" {
		failure = err.Error()
	}
	return found, failure
}
