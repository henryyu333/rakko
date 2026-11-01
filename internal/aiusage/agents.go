package aiusage

// The local readers this package knows and where each one keeps its data.
// Every reader is always on: a tool that is not installed contributes
// nothing, and a reader that fails is skipped for that scan.

import "path/filepath"

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
	roots []rootSpec
	db    string
}

// agentSpecs lists every supported reader. The order is stable so a scan's
// progress and cache remain deterministic.
func agentSpecs(home string) []agentSpec {
	join := func(p string) string { return filepath.Join(home, p) }
	return []agentSpec{
		{
			tool:  ToolOmp,
			roots: []rootSpec{{join(".omp/agent/sessions"), "*.jsonl", false}},
		},
		{
			tool:  ToolClaude,
			roots: []rootSpec{{join(".claude/projects"), "*.jsonl", false}},
		},
		{
			tool: ToolCodex,
			roots: []rootSpec{
				{join(".codex/sessions"), "rollout-*.jsonl", false},
				{join(".codex/archived_sessions"), "*.jsonl", false},
			},
		},
		{
			tool:  ToolPi,
			roots: []rootSpec{{join(".pi/agent/sessions"), "*.jsonl", false}},
		},
		{
			tool: ToolOpencode,
			db:   join(".local/share/opencode/opencode.db"),
		},
		{
			tool: ToolGrok,
			roots: []rootSpec{
				{join(".grok/sessions"), "usage.json", true},
				{join(".grok/sessions"), "updates.jsonl", false},
			},
		},
	}
}
