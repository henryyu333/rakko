//go:build darwin

package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/aiusage"
	"rakko/internal/settings"
	"rakko/internal/store"
)

// usageEvidenceDir is where the usage page's evidence screenshots go:
// under RAKKO_SHOTS when a run asks for them, and "" to write none.
func usageEvidenceDir(t *testing.T, seg string) string {
	t.Helper()
	root := os.Getenv("RAKKO_SHOTS")
	if root == "" {
		return ""
	}
	return filepath.Join(root, seg)
}

func saveTestPNG(t *testing.T, dir, name string, view *ui.Tester) {
	t.Helper()
	if dir == "" {
		return
	}
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, view.Image()); err != nil {
		t.Fatal(err)
	}
}

type v1170FakeScanner struct {
	sum aiusage.Summary
}

func (s v1170FakeScanner) Scan(context.Context, func(int64, int64)) error   { return nil }
func (s v1170FakeScanner) Summary(aiusage.Range, time.Time) aiusage.Summary { return s.sum }
func (s v1170FakeScanner) UnreadableTools() []aiusage.Tool                  { return nil }

func makeV1170Shelf(t *testing.T, lang string, cards []aiusage.Tool, now time.Time) (*shelf, *usageFeature) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := settings.Default()
	cfg.Language = lang
	cfg.AIUsage.Enabled = true
	raw := make([]string, len(cards))
	for i, c := range cards {
		raw[i] = string(c)
	}
	cfg.AIUsage.QuotaCards = &raw

	a := &shelf{
		store: s, dataDir: t.TempDir(), level: 2, tab: pageUsage, visible: true,
		locale:     lang,
		settings:   cfg,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.fx.openK = 1

	sum := aiusage.Summary{
		Range: aiusage.RangeToday,
		Total: aiusage.Totals{Input: 120000, Output: 24000, Requests: 18, Cost: 1.42},
		Buckets: []aiusage.Bucket{
			{Start: now.Add(-6 * time.Hour), Tokens: map[aiusage.Tool]int64{aiusage.ToolCodex: 18000}},
			{Start: now.Add(-4 * time.Hour), Tokens: map[aiusage.Tool]int64{aiusage.ToolClaude: 34000}},
			{Start: now.Add(-2 * time.Hour), Tokens: map[aiusage.Tool]int64{aiusage.ToolCodex: 52000}},
			{Start: now.Add(-1 * time.Hour), Tokens: map[aiusage.Tool]int64{aiusage.ToolClaude: 40000}},
		},
	}

	noop := func(context.Context) aiusage.Quota { return aiusage.Quota{} }
	f := newUsageFeature(a, usageDeps{
		fetchCodex:  noop,
		fetchClaude: noop,
		fetchMore: map[aiusage.Tool]func(context.Context) aiusage.Quota{
			aiusage.ToolGLM:         noop,
			aiusage.ToolDeepSeek:    noop,
			aiusage.ToolAntigravity: noop,
			aiusage.ToolCursor:      noop,
			aiusage.ToolKimi:        noop,
			aiusage.ToolOpencode:    noop,
			aiusage.ToolCommandCode: noop,
		},
		newScanner: func() usageScanner { return v1170FakeScanner{sum: sum} },
		pace:       aiusage.Pace,
		now:        func() time.Time { return now },
	})
	a.registerPage(f)
	f.hasSummary = true
	f.summary = sum

	// Populate realistic sample readings
	codexQ := aiusage.Quota{
		Tool: aiusage.ToolCodex, Plan: "Plus", Status: aiusage.QuotaOK, FetchedAt: now,
		Windows: []aiusage.Window{
			{Name: "5 hours", UsedPct: 83, Length: 5 * time.Hour, ResetsAt: now.Add(32 * time.Minute)},
			{Name: "7 days", UsedPct: 68, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(75 * time.Hour)},
		},
		ResetCredits: &aiusage.ResetCredits{
			Count:     2,
			SoonestAt: time.Date(2026, 11, 30, 0, 0, 0, 0, time.Local),
			FirstID:   "cr-1",
		},
	}
	claudeQ := aiusage.Quota{
		Tool: aiusage.ToolClaude, Plan: "Pro", Status: aiusage.QuotaOK, FetchedAt: now,
		Windows: []aiusage.Window{
			{Name: "5 hours", UsedPct: 45, Length: 5 * time.Hour, ResetsAt: now.Add(4*time.Hour + 31*time.Minute)},
			{Name: "7 days", UsedPct: 68, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(48 * time.Hour)},
		},
	}
	glmQ := aiusage.Quota{
		Tool: aiusage.ToolGLM, Plan: "Pro", Status: aiusage.QuotaOK, FetchedAt: now,
		Windows: []aiusage.Window{
			{Name: "5 hours", UsedPct: 12, Length: 5 * time.Hour, ResetsAt: now.Add(2*time.Hour + 10*time.Minute)},
			{Name: "7 days", UsedPct: 40, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(96 * time.Hour)},
		},
	}
	dsQ := aiusage.Quota{
		Tool: aiusage.ToolDeepSeek, Plan: "API", Status: aiusage.QuotaOK, FetchedAt: now,
		Balance: &aiusage.BalanceInfo{
			Currency: "¥", Amount: 86.40, Text: "¥ 86.40", TopUp: "80", Granted: "6.40",
		},
	}
	for _, q := range []aiusage.Quota{codexQ, claudeQ, glmQ, dsQ} {
		f.quotaState(q.Tool).latest = q
		f.quotaState(q.Tool).good = q
	}
	return a, f
}

func TestUsagePanelDynamicWidthAndRenderEvidence(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.Local)
	evidenceDir := usageEvidenceDir(t, "Rakko-v1170")

	cardSets := []struct {
		name  string
		cards []aiusage.Tool
		wantW float32
	}{
		{"1card", []aiusage.Tool{aiusage.ToolCodex}, sheetW},
		{"2card", []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude}, sheetW},
		{"3card", []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude, aiusage.ToolGLM}, usageWideSheetW},
		{"4card", []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude, aiusage.ToolGLM, aiusage.ToolDeepSeek}, usageWideSheetW},
	}

	for _, cs := range cardSets {
		for _, lang := range []string{"zh-Hans", "en"} {
			langTag := "zh"
			if lang == "en" {
				langTag = "en"
			}
			a, f := makeV1170Shelf(t, lang, cs.cards, now)
			if got := f.width(); got != cs.wantW {
				t.Fatalf("%s/%s f.width() = %.0f, want %.0f", cs.name, langTag, got, cs.wantW)
			}
			mw, mh := f.maxSize()
			if mw != usageWideSheetW || mh != sheetH {
				t.Fatalf("maxSize() = (%.0f, %.0f), want (%.0f, %.0f)", mw, mh, float32(usageWideSheetW), float32(sheetH))
			}
			view := ui.NewTester(a.view, int(cs.wantW), sheetH)
			view.SetLocale(lang)
			view.SetScale(2)
			view.Frame()
			view.Frame()
			if cs.name == "4card" {
				if !view.HasText("¥ 86.40") {
					t.Fatalf("%s/%s missing DeepSeek balance text; texts=%v", cs.name, langTag, view.Texts())
				}
			}
			saveTestPNG(t, evidenceDir, "usage-"+cs.name+"-"+langTag+".png", view)
		}
	}
}

func TestCodexResetConfirmAndConsumeFlow(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.Local)
	a, f := makeV1170Shelf(t, "zh-Hans", []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude}, now)
	var consumed atomic.Int32
	f.deps.useCodexReset = func(context.Context) (aiusage.ResetOutcome, error) {
		consumed.Add(1)
		return aiusage.ResetOutcome{Code: "reset", Windows: 2}, nil
	}
	oldGoWork, oldDispatch := goWork, dispatchMain
	goWork = func(fn func()) { fn() }
	dispatchMain = func(fn func()) { fn() }
	t.Cleanup(func() { goWork, dispatchMain = oldGoWork, oldDispatch })

	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Move(sheetW-4, sheetH-4)
	view.Frame()
	if view.HasText("使用") {
		t.Fatalf("unhovered card must not show the 使用 button; texts=%v", view.Texts())
	}
	codexRect, ok := view.Find("Codex")
	if !ok {
		t.Fatal("missing Codex card")
	}
	view.Move(codexRect.X+codexRect.W/2, codexRect.Y+codexRect.H/2)
	view.Frame()
	view.Frame()
	if err := view.Click("使用"); err != nil {
		t.Fatalf("click 使用: %v", err)
	}
	if !f.codexResetConfirm || consumed.Load() != 0 {
		t.Fatalf("expected confirmation dialog before consume; confirm=%v consumed=%d", f.codexResetConfirm, consumed.Load())
	}
	view.Frame()
	if !view.HasText("使用 Codex 重置卡") {
		t.Fatalf("expected confirm dialog title; texts=%v", view.Texts())
	}
	if err := view.Click("确认使用"); err != nil {
		t.Fatalf("click 确认使用: %v", err)
	}
	if consumed.Load() != 1 || f.codexResetConfirm {
		t.Fatalf("after confirm: consumed=%d confirm=%v", consumed.Load(), f.codexResetConfirm)
	}
}

func TestSettingsAIUsageMaxFourAndEvidence(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	a.settings.AIUsage.Enabled = true
	four := []string{"codex", "claude", "glm", "deepseek"}
	a.settings.AIUsage.QuotaCards = &four
	a.usage = newUsageFeature(a, usageDeps{})
	a.usage.sources = map[aiusage.Tool]aiusage.QuotaSource{
		aiusage.ToolCodex:       {Kind: aiusage.SourceLocalLogin},
		aiusage.ToolClaude:      {Kind: aiusage.SourceLocalLogin},
		aiusage.ToolGLM:         {Kind: aiusage.SourceLocalKey},
		aiusage.ToolDeepSeek:    {Kind: aiusage.SourceLocalKey},
		aiusage.ToolAntigravity: {Kind: aiusage.SourceLocalLogin},
		aiusage.ToolCursor:      {Kind: aiusage.SourceLocalLogin},
		aiusage.ToolOpencode:    {Kind: aiusage.SourceLocalKey},
	}
	a.usage.sourcesAt = time.Now()

	openSettingsPage(t, view, "AI")
	a.settingsScroll.Y = 120
	view.SetScale(2)
	view.Frame()
	view.Frame()
	saveTestPNG(t, usageEvidenceDir(t, "Rakko-v1170"), "settings-aiusage-4selected.png", view)

	// Enabling a 5th card is allowed (no 4-card cap).
	if !a.settings.AIUsage.SetQuotaCard(aiusage.ToolKimi, true) {
		t.Fatal("SetQuotaCard rejected a 5th card")
	}
	if len(a.settings.AIUsage.SelectedQuotaTools()) != 5 {
		t.Fatalf("selected count = %d, want 5", len(a.settings.AIUsage.SelectedQuotaTools()))
	}
}
