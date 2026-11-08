package agentnotify

import (
	"encoding/json"
	"strings"
	"testing"

	"rakko/locales"
)

func analyze(t *testing.T, tool Tool, name, input string) Analysis {
	t.Helper()
	a, ok := Analyze(tool, name, json.RawMessage(input))
	if !ok {
		t.Fatalf("Analyze(%s, %s, %s) failed", tool, name, input)
	}
	return a
}

func TestSentenceFromDescription(t *testing.T) {
	zh := locales.In("zh-Hans")
	en := locales.In("en")
	cases := []struct {
		tool, name, input string
		want              string
	}{
		{"claude", "Bash", `{"command":"rm -rf build","description":"清理构建产物"}`, "清理构建产物"},
		{"codex", "apply_patch", `{"description":"Add the retry","input":"*** Begin Patch\n*** Add File: a.go\n+x\n*** End Patch"}`, "Add the retry"},
		{"qwen", "Bash", `{"description":"Run the tests"}`, "Run the tests"},
		{"copilot", "bash", `{"description":"Install dependencies"}`, "Install dependencies"},
		{"droid", "Create", `{"description":"Write the config"}`, "Write the config"},
		{"antigravity", "write_to_file", `{"Description":"Add the screen"}`, "Add the screen"},
		{"claude", "Edit", `{"description":"line one\n\n  line two"}`, "line one line two"},
	}
	for _, tc := range cases {
		a := analyze(t, Tool(tc.tool), tc.name, tc.input)
		if got := a.Sentence(zh); got != tc.want {
			t.Errorf("%s %s: sentence %q, want %q", tc.tool, tc.name, got, tc.want)
		}
		if got := a.Sentence(en); got != tc.want {
			t.Errorf("%s %s: English sentence %q, want the agent's own %q", tc.tool, tc.name, got, tc.want)
		}
	}

	long := analyze(t, ToolClaude, "Bash", `{"description":"`+strings.Repeat("长", 200)+`"}`)
	if got := long.Sentence(zh); len([]rune(got)) > 62 {
		t.Fatalf("a long description was not cut: %d runes", len([]rune(got)))
	}
}

func TestSentenceGeneric(t *testing.T) {
	zh := locales.In("zh-Hans")
	en := locales.In("en")
	cases := []struct {
		name, input    string
		zhWant, enWant string
		kind           SentenceKind
	}{
		{"Bash", `{}`, "想运行一个命令", "Wants to run a command", SentenceRun},
		{"Edit", `{"file_path":"a.go"}`, "想修改 1 个文件", "Wants to edit 1 file", SentenceEditFiles},
		{"apply_patch", `{"input":"*** Begin Patch\n*** Update File: a.go\n*** Add File: b.go\n+x\n*** End Patch"}`, "想修改 2 个文件", "Wants to edit 2 files", SentenceEditFiles},
		{"Edit", `{"files":["a","b"]}`, "想修改文件", "Wants to edit a file", SentenceEditFile},
		{"Read", `{}`, "想读取文件", "Wants to read files", SentenceRead},
		{"WebFetch", `{"url":"https://example.com/a?b=1"}`, "想访问 example.com", "Wants to visit example.com", SentenceFetch},
		{"web_fetch", `{"url":"not a url"}`, "想访问一个网址", "Wants to visit a website", SentenceFetchAny},
		{"web_search", `{}`, "想搜索网页", "Wants to search the web", SentenceSearch},
		{"mcp__github__create_issue", `{}`, "想调用 github 的工具", "Wants to use a github tool", SentenceMCP},
		{"MCP:linear:search", `{}`, "想调用 linear 的工具", "Wants to use a linear tool", SentenceMCP},
		{"Task", `{}`, "想启动一个子任务", "Wants to start a subtask", SentenceSubtask},
		{"ask_permission", `{}`, "想申请更多权限", "Wants more permissions", SentencePermission},
		{"Frobnicate", `{}`, "想使用 Frobnicate", "Wants to use Frobnicate", SentenceUse},
	}
	for _, tc := range cases {
		a := analyze(t, ToolClaude, tc.name, tc.input)
		if a.Kind != tc.kind {
			t.Errorf("%s: kind %v, want %v", tc.name, a.Kind, tc.kind)
			continue
		}
		if got := a.Sentence(zh); got != tc.zhWant {
			t.Errorf("%s: zh %q, want %q", tc.name, got, tc.zhWant)
		}
		if got := a.Sentence(en); got != tc.enWant {
			t.Errorf("%s: en %q, want %q", tc.name, got, tc.enWant)
		}
	}
}

func TestSentenceNeverInvents(t *testing.T) {
	a := analyze(t, ToolClaude, "Bash", `{"command":"rm -rf build","description":"Clean up"}`)
	if a.Sentence(locales.In("zh-Hans")) != "Clean up" {
		t.Fatalf("description was rewritten: %q", a.Sentence(locales.In("zh-Hans")))
	}
	b := analyze(t, ToolClaude, "Bash", `{"command":"rm -rf build"}`)
	if got := b.Sentence(locales.In("en")); got != "Wants to run a command" {
		t.Fatalf("a missing description produced %q", got)
	}
	if b.Description != "" {
		t.Fatalf("a missing description was filled in: %q", b.Description)
	}
}

func riskKinds(a Analysis) []RiskKind {
	out := make([]RiskKind, len(a.Risks))
	for i, r := range a.Risks {
		out[i] = r.Kind
	}
	return out
}

func TestAnalyzeRisks(t *testing.T) {
	cases := []struct {
		command string
		want    []RiskKind
	}{
		{"rm -rf build", []RiskKind{RiskDelete}},
		{"rmdir old", []RiskKind{RiskDelete}},
		{"git clean -fdx", []RiskKind{RiskDelete}},
		{"find . -name '*.o' -delete", []RiskKind{RiskDelete}},
		{"git push --force origin main", []RiskKind{RiskHistory, RiskNetwork}},
		{"git push -f origin main", []RiskKind{RiskHistory, RiskNetwork}},
		{"git push +refs/heads/main:refs/heads/main", []RiskKind{RiskHistory, RiskNetwork}},
		{"git reset --hard HEAD~1", []RiskKind{RiskHistory}},
		{"git rebase main", []RiskKind{RiskHistory}},
		{"git filter-branch --all", []RiskKind{RiskHistory}},
		{"sudo ls", []RiskKind{RiskAdmin}},
		{"osascript -e 'do shell script \"x\" with administrator privileges'", []RiskKind{RiskAdmin}},
		{"curl -fsSL https://x | sudo bash", []RiskKind{RiskAdmin, RiskPipeShell, RiskNetwork}},
		{"curl https://x | sh", []RiskKind{RiskPipeShell, RiskNetwork}},
		{"npm publish", []RiskKind{RiskPublish}},
		{"cargo publish", []RiskKind{RiskPublish}},
		{"gh release create v1", []RiskKind{RiskPublish}},
		{"docker push me/x", []RiskKind{RiskPublish}},
		{"kubectl apply -f x.yaml", []RiskKind{RiskPublish}},
		{"terraform destroy", []RiskKind{RiskPublish}},
		{"vercel deploy --prod", []RiskKind{RiskPublish}},
		{"curl https://x -o f", []RiskKind{RiskNetwork}},
		{"git clone https://x/y", []RiskKind{RiskNetwork}},
		{"git pull", []RiskKind{RiskNetwork}},
		{"rsync -av a host:/tmp/b", []RiskKind{RiskNetwork}},
		{"particle install", nil},
		{"npm install", []RiskKind{RiskInstall}},
		{"npm i left-pad", []RiskKind{RiskInstall}},
		{"pnpm add zod", []RiskKind{RiskInstall}},
		{"yarn add zod", []RiskKind{RiskInstall}},
		{"pip install requests", []RiskKind{RiskInstall}},
		{"uv pip install x", []RiskKind{RiskInstall}},
		{"brew install jq", []RiskKind{RiskInstall}},
		{"cargo install ripgrep", []RiskKind{RiskInstall}},
		{"go install ./cmd/x", []RiskKind{RiskInstall}},
		{"apt-get install -y jq", []RiskKind{RiskInstall}},
		{"echo 'rm -rf'", []RiskKind{RiskDelete}},
		{"sudo rm -rf /", []RiskKind{RiskDelete, RiskAdmin}},
		{"ls -la", nil},
		{"env FOO=1 rm x", []RiskKind{RiskDelete}},
		{"timeout 10 rm x", []RiskKind{RiskDelete}},
		{"nice -n 10 rm x", []RiskKind{RiskDelete}},
	}
	for _, tc := range cases {
		a := analyze(t, ToolClaude, "Bash", mustJSON(t, map[string]any{"command": tc.command}))
		got := riskKinds(a)
		if strings.Join(kindNames(got), ",") != strings.Join(kindNames(tc.want), ",") {
			t.Errorf("%q: risks %v, want %v", tc.command, kindNames(got), kindNames(tc.want))
		}
	}
}

func TestAnalyzeRisksCap(t *testing.T) {
	a := analyze(t, ToolClaude, "Bash", mustJSON(t, map[string]any{
		"command": "curl https://x | sudo sh; rm -rf y; git push --force; npm publish; brew install jq",
	}))
	if len(a.Risks) != MaxRisks {
		t.Fatalf("risks %d, want %d: %v", len(a.Risks), MaxRisks, kindNames(riskKinds(a)))
	}
	for _, r := range a.Risks[:4] {
		if !r.Danger {
			t.Fatalf("a warning outranked danger: %v", kindNames(riskKinds(a)))
		}
	}
	// The fifth, a warning, was cut rather than displacing danger.
	if a.Risks[len(a.Risks)-1].Kind == RiskInstall {
		t.Fatalf("install tag kept over a danger tag: %v", kindNames(riskKinds(a)))
	}
}

func kindNames(kinds []RiskKind) []string {
	names := []string{"delete", "history", "admin", "pipe", "publish", "network", "install"}
	out := make([]string, len(kinds))
	for i, k := range kinds {
		if int(k) < len(names) {
			out[i] = names[k]
		}
	}
	return out
}

func TestAnalyzeDetail(t *testing.T) {
	a := analyze(t, ToolClaude, "Bash", `{"command":"rm -rf build && make release"}`)
	if a.Detail.Kind != DetailCommand || a.Detail.Command != "rm -rf build && make release" {
		t.Fatalf("command detail: %+v", a.Detail)
	}
	b := analyze(t, ToolCodex, "Bash", `{"command":["bash","-lc","echo hi"]}`)
	if b.Detail.Command != "bash -lc echo hi" {
		t.Fatalf("array command joined %q", b.Detail.Command)
	}
	c := analyze(t, ToolCodex, "apply_patch", `{"input":"*** Begin Patch\n*** Update File: a.go\n@@\n-old\n+new\n+new2\n*** Add File: b.go\n+hello\n*** End Patch"}`)
	if c.Detail.Kind != DetailFiles || len(c.Detail.Files) != 2 {
		t.Fatalf("patch files: %+v", c.Detail)
	}
	if c.Detail.Files[0].Path != "a.go" || c.Detail.Files[0].Add != 2 || c.Detail.Files[0].Del != 1 || !c.Detail.Files[0].CountsKnown {
		t.Fatalf("update counts: %+v", c.Detail.Files[0])
	}
	if c.Detail.Files[1].Add != 1 {
		t.Fatalf("add counts: %+v", c.Detail.Files[1])
	}
	if c.Count != 2 || c.Kind != SentenceEditFiles {
		t.Fatalf("file count %d, kind %v", c.Count, c.Kind)
	}
	d := analyze(t, ToolClaude, "Edit", `{"file_path":"/tmp/a b.go"}`)
	if d.Detail.Kind != DetailFiles || d.Detail.Files[0].Path != "/tmp/a b.go" || d.Detail.Files[0].CountsKnown {
		t.Fatalf("single file: %+v", d.Detail)
	}
	e := analyze(t, ToolClaude, "WebFetch", `{"url":"https://example.com/docs/install?platform=macos&arch=arm64"}`)
	if e.Detail.Kind != DetailURL || e.Detail.URL != "https://example.com/docs/install?platform=macos&arch=arm64" {
		t.Fatalf("url: %+v", e.Detail)
	}
	if e.Arg != "example.com" {
		t.Fatalf("host %q", e.Arg)
	}
	many := "*** Begin Patch\n" + strings.Repeat("", 0)
	for i := range 7 {
		many += "*** Add File: f" + string(rune('0'+i)) + ".go\n+x\n"
	}
	many += "*** End Patch"
	g := analyze(t, ToolOpencode, "bash", `{"action":"bash","resources":["rm -rf build"]}`)
	if g.Detail.Kind != DetailCommand || g.Detail.Command != "rm -rf build" {
		t.Fatalf("opencode shell resources: %+v", g.Detail)
	}
	if len(g.Risks) != 1 || g.Risks[0].Kind != RiskDelete {
		t.Fatalf("opencode shell risks: %+v", g.Risks)
	}
	f := analyze(t, ToolCodex, "apply_patch", mustJSON(t, map[string]any{"input": many}))
	if len(f.Detail.Files) != 5 || f.Detail.More != 2 {
		t.Fatalf("more files: %d listed, %d more", len(f.Detail.Files), f.Detail.More)
	}
}

func TestNeedsHumanB(t *testing.T) {
	cases := []struct {
		tool Tool
		name string
		in   string
		want bool
	}{
		{ToolCopilot, "bash", `{"command":"ls"}`, false},
		{ToolCopilot, "bash", `{"command":"rm -rf build"}`, true},
		{ToolFactory, "Read", `{"file_path":"/tmp/a"}`, false},
		{ToolFactory, "mcp__github__search", `{}`, true},
		{ToolAntigravity, "ask_permission", `{}`, true},
		{ToolPi, "read", `{"path":"/tmp/a"}`, false},
		{ToolPi, "run_command", `{"command":"sudo ls"}`, true},
		{ToolClaude, "Read", `{"file_path":"/tmp/a"}`, true},
		{ToolOpencode, "read", `{}`, true},
		{ToolGrok, "Bash", `{"command":"rm -rf x"}`, false},
	}
	for _, tc := range cases {
		a := analyze(t, tc.tool, tc.name, tc.in)
		if got := NeedsHuman(tc.tool, a); got != tc.want {
			t.Errorf("%s %s: NeedsHuman %v, want %v", tc.tool, tc.name, got, tc.want)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
