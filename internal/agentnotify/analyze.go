package agentnotify

// What a permission request is about, in three layers: the agent's own
// words, the risks the command matches, and the raw thing being approved.
// The hook client uses NeedsHuman to decide whether a B-class call is worth
// a card; the app uses the same analysis to draw one. Nothing here is a
// security boundary: a risk tag only picks the card's colour and its hold,
// and the full command is always one click away.

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"rakko/locales"
)

// RiskKind is one kind of risk a command can match.
type RiskKind int

const (
	RiskDelete RiskKind = iota
	RiskHistory
	RiskAdmin
	RiskPipeShell
	RiskPublish
	RiskNetwork
	RiskInstall
)

// riskOrder is the table's order within one severity; the card keeps it.
var riskOrder = []RiskKind{RiskDelete, RiskHistory, RiskAdmin, RiskPipeShell, RiskPublish, RiskNetwork, RiskInstall}

// Risk is one matched rule.
type Risk struct {
	Kind   RiskKind
	Danger bool
}

// Label is the tag's wording.
func (r Risk) Label(l locales.Messages) string {
	switch r.Kind {
	case RiskDelete:
		return l.RiskDelete()
	case RiskHistory:
		return l.RiskHistory()
	case RiskAdmin:
		return l.RiskAdmin()
	case RiskPipeShell:
		return l.RiskPipeShell()
	case RiskPublish:
		return l.RiskPublish()
	case RiskNetwork:
		return l.RiskNetwork()
	case RiskInstall:
		return l.RiskInstall()
	}
	return ""
}

// DetailKind says what the expanded layer shows.
type DetailKind int

const (
	DetailCommand DetailKind = iota
	DetailFiles
	DetailURL
)

// FileChange is one file a patch or edit names.
type FileChange struct {
	Path        string
	Add, Del    int
	CountsKnown bool
}

// Detail is the raw thing the request approves, untruncated.
type Detail struct {
	Kind    DetailKind
	Command string
	Files   []FileChange
	More    int
	URL     string
}

// SentenceKind is the generic first line used when the agent wrote none.
type SentenceKind int

const (
	SentenceRun SentenceKind = iota
	SentenceEditFile
	SentenceEditFiles
	SentenceRead
	SentenceFetch
	SentenceFetchAny
	SentenceSearch
	SentenceMCP
	SentenceSubtask
	SentencePermission
	SentenceUse
)

// Analysis is one request, read.
type Analysis struct {
	// ToolName is the tool name the harness itself sent, as it sent it.
	ToolName string
	// Description is the agent's own sentence, verbatim apart from
	// whitespace and a display-width cut. Empty when the payload has none.
	Description string
	Kind        SentenceKind
	Arg         string // host, MCP server, or tool name for SentenceUse
	Count       int    // files, when known
	Risks       []Risk
	Detail      Detail
}

// Sentence is the card's first plain line: the agent's own words when it
// wrote any, else a generic sentence built only from payload values.
func (a Analysis) Sentence(l locales.Messages) string {
	if a.Description != "" {
		return a.Description
	}
	switch a.Kind {
	case SentenceEditFiles:
		return l.AskEditFiles(a.Count)
	case SentenceEditFile:
		return l.AskEditFile()
	case SentenceRead:
		return l.AskRead()
	case SentenceFetch:
		return l.AskFetch(a.Arg)
	case SentenceFetchAny:
		return l.AskFetchAny()
	case SentenceSearch:
		return l.AskSearch()
	case SentenceMCP:
		return l.AskMCP(a.Arg)
	case SentenceSubtask:
		return l.AskSubtask()
	case SentencePermission:
		return l.AskPermission()
	case SentenceUse:
		return l.AskUse(a.Arg)
	default:
		return l.AskRunCommand()
	}
}

// HasRisk reports whether any tag was matched.
func (a Analysis) HasRisk() bool { return len(a.Risks) > 0 }

// MaxRisks is how many tags the card shows; the rest are cut but their
// severity still colours the card.
const MaxRisks = 4

// Analyze reads one normalized request. toolName and input are what the
// hook payload said (the inner tool_input / toolArgs / toolCall.args), so
// the app and the client see exactly the same bytes.
func Analyze(tool Tool, toolName string, input json.RawMessage) (Analysis, bool) {
	var m map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &m); err != nil {
			return Analysis{}, false
		}
	}
	if m == nil {
		m = map[string]any{}
	}
	a := Analysis{ToolName: toolName, Detail: Detail{Kind: DetailCommand}}

	if d := descriptionOf(m); d != "" {
		a.Description = clipDisplay(d, 120)
	}

	command, commandOK := commandOf(m)
	files, more, patchOK := filesOf(toolName, m)
	rawURL := stringOf(m, "url", "Url", "URL", "uri")
	if !patchOK {
		if p := stringOf(m, "file_path", "filePath", "path", "Path", "TargetFile", "target_file", "file"); p != "" {
			files = []FileChange{{Path: p}}
			patchOK = true
		}
	}
	// opencode's evaluate payload carries the affected resources instead of
	// a file path.
	if !patchOK {
		if rs, ok := m["resources"].([]any); ok {
			for _, r := range rs {
				if s, ok := r.(string); ok && s != "" {
					files = append(files, FileChange{Path: s})
				}
			}
			patchOK = len(files) > 0
		}
	}

	switch {
	case patchOK && len(files) > 0:
		a.Detail = Detail{Kind: DetailFiles, Files: files, More: more}
	case commandOK:
		a.Detail = Detail{Kind: DetailCommand, Command: command}
	case rawURL != "":
		a.Detail = Detail{Kind: DetailURL, URL: rawURL}
	}

	kind, arg := sentenceClass(toolName)
	if commandOK {
		a.Risks = risksOf(command)
	}
	if kind == SentenceFetch {
		a.Risks = appendRisk(a.Risks, RiskNetwork)
	}
	if d := detailFileCount(a.Detail); d > 0 {
		a.Count = d
	}
	if toolName == "Delete" || toolName == "delete" {
		a.Risks = appendRisk(a.Risks, RiskDelete)
	}
	a.Risks = dedupeRisks(a.Risks)

	a.Kind, a.Arg = kind, arg
	switch {
	case kind == SentenceEditFile && a.Count > 1:
		a.Kind = SentenceEditFiles
	case kind == SentenceEditFile && a.Count == 1:
		a.Kind = SentenceEditFile
	case kind == SentenceFetch:
		if host := hostOf(rawURL); host != "" {
			a.Arg = host
		} else {
			a.Kind = SentenceFetchAny
		}
	}
	if a.Kind == SentenceUse && a.Arg == "" {
		a.Arg = toolName
	}
	return a, true
}

// NeedsHuman reports whether this call is worth a card. A-class channels
// fire only when the tool would already prompt, so they always are. B-class
// channels fire on every call; only a risk, an MCP tool or a permission
// request is worth interrupting for, and everything else falls back to the
// tool's own flow.
func NeedsHuman(tool Tool, a Analysis) bool {
	switch tool {
	case ToolClaude, ToolCodex, ToolQwen, ToolDevin, ToolOpencode:
		return true
	case ToolCopilot, ToolFactory, ToolAntigravity, ToolPi:
		if len(a.Risks) > 0 || a.ToolName == "ask_permission" {
			return true
		}
		_, ok := mcpServer(a.ToolName)
		return ok
	}
	return false
}

// A-class tools are the ones whose hook only fires when the tool would have
// prompted. The card is the whole point; there is no risk filter.
func toolClassA(tool Tool) bool {
	switch tool {
	case ToolClaude, ToolCodex, ToolQwen, ToolDevin, ToolOpencode:
		return true
	}
	return false
}

// --- payload readers ---

func stringOf(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func descriptionOf(m map[string]any) string {
	for _, k := range []string{"description", "Description"} {
		if s, ok := m[k].(string); ok {
			s = strings.Join(strings.Fields(s), " ")
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func commandOf(m map[string]any) (string, bool) {
	for _, k := range []string{"command", "CommandLine", "commandLine"} {
		switch v := m[k].(type) {
		case string:
			if v != "" {
				return v, true
			}
		case []any:
			parts := make([]string, 0, len(v))
			for _, p := range v {
				if s, ok := p.(string); ok {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, " "), true
			}
		}
	}
	return "", false
}

func filesOf(toolName string, m map[string]any) ([]FileChange, int, bool) {
	for _, k := range []string{"input", "patch", "diff"} {
		switch v := m[k].(type) {
		case string:
			if fs, more, ok := parsePatch(v); ok {
				return fs, more, true
			}
		}
	}
	return nil, 0, false
}

func detailFileCount(d Detail) int {
	if d.Kind != DetailFiles {
		return 0
	}
	return len(d.Files) + d.More
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

// --- sentences ---

func sentenceClass(toolName string) (SentenceKind, string) {
	name := strings.TrimSpace(toolName)
	switch name {
	case "Bash", "bash", "shell", "exec", "command", "run_command", "run_shell_command",
		"Execute", "powershell", "terminal":
		return SentenceRun, ""
	case "Edit", "Write", "Create", "ApplyPatch", "apply_patch", "edit", "write",
		"str_replace_editor", "replace_file_content", "multi_replace_file_content",
		"write_to_file", "save_file", "str_replace_based_edit_tool":
		return SentenceEditFile, ""
	case "Read", "view", "view_file", "read_file", "grep", "Grep", "glob", "Glob",
		"list_dir", "find_by_name":
		return SentenceRead, ""
	case "WebFetch", "web_fetch", "webfetch", "FetchUrl", "read_url_content":
		return SentenceFetch, ""
	case "web_search", "WebSearch":
		return SentenceSearch, ""
	case "Task", "task", "invoke_subagent":
		return SentenceSubtask, ""
	case "ask_permission":
		return SentencePermission, ""
	}
	if server, ok := mcpServer(name); ok {
		return SentenceMCP, server
	}
	return SentenceUse, name
}

// mcpServer names the server in an MCP tool name: mcp__server__tool,
// MCP:server:tool, or mcp_server_tool.
func mcpServer(name string) (string, bool) {
	rest := ""
	switch {
	case strings.HasPrefix(name, "mcp__"):
		rest = name[len("mcp__"):]
	case strings.HasPrefix(name, "MCP:"):
		rest = name[len("MCP:"):]
	case strings.HasPrefix(name, "mcp_"):
		rest = name[len("mcp_"):]
	default:
		return "", false
	}
	if i := strings.Index(rest, "__"); i > 0 {
		return rest[:i], true
	}
	if i := strings.Index(rest, ":"); i > 0 {
		return rest[:i], true
	}
	if i := strings.Index(rest, "_"); i > 0 {
		return rest[:i], true
	}
	return rest, true
}

// clipDisplay shortens s to limit display columns (a wide rune counts two)
// and marks the cut with an ellipsis.
func clipDisplay(s string, limit int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r > 0x2E80 {
			w = 2
		}
		if units+w > limit {
			return strings.TrimSpace(s[:i]) + "…"
		}
		units += w
	}
	return s
}

// --- risk rules ---

func risksOf(command string) []Risk {
	var out []Risk
	segs := splitSegments(command)
	for _, seg := range segs {
		tokens := commandTokens(seg.text)
		if len(tokens) == 0 {
			continue
		}
		head, second := tokens[0], ""
		if len(tokens) > 1 {
			second = tokens[1]
		}
		// sudo (and its doas/su siblings) is kept as a risk, but the
		// command it runs is still the command being judged.
		if head == "sudo" || head == "doas" || head == "su" {
			if len(tokens) > 1 {
				head, second = tokens[1], ""
				if len(tokens) > 2 {
					second = tokens[2]
				}
			}
		}
		has := func(want string) bool {
			for _, t := range tokens {
				if t == want {
					return true
				}
			}
			return false
		}
		flagForced := false
		for _, t := range tokens[1:] {
			if t == "--force" || t == "-f" || t == "--force-with-lease" ||
				strings.HasPrefix(t, "--force-with-lease=") || strings.HasPrefix(t, "+refs/") {
				flagForced = true
			}
		}
		// A command name anywhere in the segment is enough for deletion:
		// quotes are not interpreted, so echo "rm -rf" must tag too.
		deleteHit := false
		for _, t := range tokens {
			switch strings.Trim(t, "'\"") {
			case "rm", "rmdir", "unlink", "shred":
				deleteHit = true
			}
		}
		switch {
		case deleteHit,
			head == "git" && second == "clean",
			head == "find" && has("-delete"):
			out = appendRisk(out, RiskDelete)
		}
		if head == "git" {
			switch {
			case second == "push" && flagForced:
				out = appendRisk(out, RiskHistory)
			case second == "reset" && has("--hard"):
				out = appendRisk(out, RiskHistory)
			case second == "rebase", second == "filter-branch", second == "filter-repo":
				out = appendRisk(out, RiskHistory)
			}
		}
		if tokens[0] == "sudo" || tokens[0] == "doas" || tokens[0] == "su" ||
			strings.Contains(seg.text, "with administrator privileges") {
			out = appendRisk(out, RiskAdmin)
		}
		if seg.sep == "|" {
			right := head
			if len(tokens) > 1 && (tokens[0] == "sudo" || tokens[0] == "doas") {
				right = tokens[1]
			}
			switch right {
			case "sh", "bash", "zsh", "fish", "dash", "ksh", "python", "python3", "perl", "ruby", "node":
				out = appendRisk(out, RiskPipeShell)
			}
		}
		if published(head, second, tokens) {
			out = appendRisk(out, RiskPublish)
		}
		if networked(head, second, seg.text) {
			out = appendRisk(out, RiskNetwork)
		}
		if installed(head, second, tokens) {
			out = appendRisk(out, RiskInstall)
		}
	}
	return dedupeRisks(out)
}

func published(head, second string, tokens []string) bool {
	has := func(want string) bool {
		for _, t := range tokens {
			if t == want {
				return true
			}
		}
		return false
	}
	switch head {
	case "npm":
		return second == "publish"
	case "cargo":
		return second == "publish"
	case "gh":
		return second == "release" && len(tokens) > 2 && tokens[2] == "create"
	case "docker":
		return second == "push"
	case "kubectl":
		return second == "apply" || second == "delete" || second == "replace"
	case "terraform":
		return second == "apply" || second == "destroy"
	case "vercel", "netlify", "fly", "flyctl":
		return has("deploy")
	}
	return false
}

func networked(head, second, text string) bool {
	switch head {
	case "curl", "wget", "nc", "ssh", "scp":
		return true
	}
	if head == "git" {
		switch second {
		case "clone", "fetch", "pull", "push":
			return true
		}
	}
	if head == "rsync" {
		// A remote operand is host:path or user@host:path. Local paths have
		// no colon, and option values such as -e ssh are skipped by shape.
		for _, t := range strings.Fields(text)[1:] {
			if strings.HasPrefix(t, "-") {
				continue
			}
			if i := strings.Index(t, ":"); i > 0 && !strings.Contains(t[:i], "/") {
				return true
			}
		}
	}
	return false
}

func installed(head, second string, tokens []string) bool {
	has := func(want string) bool {
		for _, t := range tokens {
			if t == want {
				return true
			}
		}
		return false
	}
	switch head {
	case "npm":
		return second == "i" || second == "install" || second == "add"
	case "pnpm":
		return second == "add" || second == "install"
	case "yarn":
		return second == "add"
	case "pip", "pip3":
		return second == "install"
	case "uv":
		return second == "pip" && has("install")
	case "brew":
		return second == "install"
	case "cargo", "go", "gem":
		return second == "install"
	case "apt", "apt-get", "dnf", "yum", "pacman", "apk":
		return has("install")
	}
	return false
}

func appendRisk(list []Risk, kind RiskKind) []Risk {
	danger := true
	switch kind {
	case RiskNetwork, RiskInstall:
		danger = false
	}
	return append(list, Risk{Kind: kind, Danger: danger})
}

// dedupeRisks keeps one tag per kind, sorts danger first while keeping the
// rule table's order inside a severity, and cuts the display at MaxRisks.
func dedupeRisks(list []Risk) []Risk {
	seen := map[RiskKind]bool{}
	out := make([]Risk, 0, len(list))
	for _, r := range list {
		if seen[r.Kind] {
			continue
		}
		seen[r.Kind] = true
		out = append(out, r)
	}
	rank := func(k RiskKind) int {
		for i, v := range riskOrder {
			if v == k {
				return i
			}
		}
		return len(riskOrder)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Danger != out[j].Danger {
			return out[i].Danger
		}
		return rank(out[i].Kind) < rank(out[j].Kind)
	})
	if len(out) > MaxRisks {
		out = out[:MaxRisks]
	}
	return out
}

// seg is one command segment and the separator that introduced it.
type seg struct {
	text string
	sep  string
}

// splitSegments splits on unescaped &&, ||, ;, | and newlines. Quotes are
// not interpreted on purpose: over-tagging is acceptable, and a rule that
// under-tags is not.
func splitSegments(command string) []seg {
	var out []seg
	var b strings.Builder
	sep := ""
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, seg{text: s, sep: sep})
		}
		b.Reset()
		sep = ""
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c == '\\' && i+1 < len(command) {
			b.WriteByte(c)
			i++
			b.WriteByte(command[i])
			continue
		}
		switch c {
		case '\n', ';':
			flush()
		case '&':
			if i+1 < len(command) && command[i+1] == '&' {
				flush()
				i++
			} else {
				b.WriteByte(c)
			}
		case '|':
			if i+1 < len(command) && command[i+1] == '|' {
				flush()
				i++
			} else {
				flush()
				sep = "|"
			}
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return out
}

// commandTokens splits a segment and drops the wrappers that only hand a
// command forward. sudo is not a wrapper: it is a risk of its own.
func commandTokens(text string) []string {
	tokens := strings.Fields(text)
	for len(tokens) > 0 {
		switch tokens[0] {
		case "env":
			tokens = tokens[1:]
			for len(tokens) > 0 && strings.Contains(tokens[0], "=") {
				tokens = tokens[1:]
			}
		case "timeout":
			tokens = tokens[1:]
			if len(tokens) > 0 {
				tokens = tokens[1:]
			}
		case "nice", "ionice", "stdbuf":
			tokens = tokens[1:]
			for len(tokens) > 0 && strings.HasPrefix(tokens[0], "-") {
				tokens = tokens[1:]
			}
		case "command":
			tokens = tokens[1:]
		default:
			return tokens
		}
	}
	return tokens
}

// parsePatch reads Codex's apply_patch text: its file headers and the added
// and removed lines under each.
func parsePatch(patch string) ([]FileChange, int, bool) {
	if !strings.Contains(patch, "*** ") {
		return nil, 0, false
	}
	var files []FileChange
	var cur *FileChange
	flush := func() {
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Update File: "),
			strings.HasPrefix(line, "*** Add File: "),
			strings.HasPrefix(line, "*** Delete File: "):
			flush()
			path := strings.TrimSpace(line[strings.Index(line, ":")+1:])
			cur = &FileChange{Path: path}
			if strings.HasPrefix(line, "*** Add File: ") {
				cur.CountsKnown = true
			}
		case strings.HasPrefix(line, "*** Move to: "):
			// The file keeps its path in the list; the move is not a
			// second file.
		case strings.HasPrefix(line, "*** End Patch"), strings.HasPrefix(line, "*** Begin Patch"):
			flush()
		case strings.HasPrefix(line, "@@"):
		case cur != nil && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			cur.Add++
			cur.CountsKnown = true
		case cur != nil && strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			cur.Del++
			cur.CountsKnown = true
		}
	}
	flush()
	if len(files) == 0 {
		return nil, 0, false
	}
	more := 0
	if len(files) > 5 {
		more = len(files) - 5
		files = files[:5]
	}
	return files, more, true
}
