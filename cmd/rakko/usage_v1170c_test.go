//go:build darwin

package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/aiusage"
	"rakko/locales"
)

func TestSettingsAIUsageSwitchToggleHoverAndDrag(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	a.settings.AIUsage.Enabled = true
	a.usage = newUsageFeature(a, usageDeps{})
	openSettingsPage(t, view, "AI")
	a.settingsScroll.Y = 120
	view.Frame()

	l := locales.In(a.settings.Language)
	glmSwitch := settingsQuotaToolTitle(aiusage.ToolGLM, l) + " " + l.SettingsAIQuotaGroup()

	// 1. Clicking the switch toggles GLM on and off.
	if a.settings.AIUsage.QuotaCardEnabled(aiusage.ToolGLM) {
		t.Fatal("expected GLM initially off")
	}
	if err := view.Click(glmSwitch); err != nil {
		t.Fatalf("click GLM switch on %q: %v", glmSwitch, err)
	}
	view.Frame()
	if !a.settings.AIUsage.QuotaCardEnabled(aiusage.ToolGLM) {
		t.Fatal("expected GLM enabled after clicking switch")
	}
	if err := view.Click(glmSwitch); err != nil {
		t.Fatalf("click GLM switch off %q: %v", glmSwitch, err)
	}
	view.Frame()
	if a.settings.AIUsage.QuotaCardEnabled(aiusage.ToolGLM) {
		t.Fatal("expected GLM disabled after clicking switch again")
	}

	// 2. Hovering other rows after clicking switch must not change order.
	before := slices.Clone(a.settings.AIUsage.OrderedQuotaTools())
	for _, target := range []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude, aiusage.ToolKimi} {
		r, ok := view.Find(settingsQuotaToolTitle(target, l))
		if !ok {
			t.Fatalf("missing row %s", target)
		}
		view.Move(r.X+r.W/2, r.Y+r.H/2)
		view.Frame()
	}
	if !slices.Equal(before, a.settings.AIUsage.OrderedQuotaTools()) {
		t.Fatalf("order changed after clicking switch and hovering: before=%v after=%v", before, a.settings.AIUsage.OrderedQuotaTools())
	}

	// 3. Enabling 5+ tools is allowed (no 4-card cap).
	four := []string{"codex", "claude", "glm", "deepseek"}
	a.settings.AIUsage.QuotaCards = &four
	view.Frame()
	kimiSwitch := settingsQuotaToolTitle(aiusage.ToolKimi, l) + " " + l.SettingsAIQuotaGroup()
	if err := view.Click(kimiSwitch); err != nil {
		t.Fatalf("click 5th switch (Kimi) %q: %v", kimiSwitch, err)
	}
	view.Frame()
	if !a.settings.AIUsage.QuotaCardEnabled(aiusage.ToolKimi) {
		t.Fatal("expected 5th switch (Kimi) to enable when 4 cards are already enabled")
	}
	if len(a.settings.AIUsage.SelectedQuotaTools()) != 5 {
		t.Fatalf("selected count = %d, want 5", len(a.settings.AIUsage.SelectedQuotaTools()))
	}

	// 4. Dragging the handle still reorders rows.
	held := false
	origLeftDown := dragLeftDown
	dragLeftDown = func() bool { return held }
	t.Cleanup(func() { dragLeftDown = origLeftDown })

	codexRect, ok := view.Find(settingsQuotaToolTitle(aiusage.ToolCodex, l))
	if !ok {
		t.Fatal("missing Codex row")
	}
	glmRect, ok := view.Find(settingsQuotaToolTitle(aiusage.ToolGLM, l))
	if !ok {
		t.Fatal("missing GLM row")
	}
	held = true
	view.Press(glmRect.X+40, glmRect.Y+glmRect.H/2)
	view.Frame()
	view.Move(codexRect.X+40, codexRect.Y+codexRect.H/2)
	view.Frame()
	held = false
	view.Release(codexRect.X+40, codexRect.Y+codexRect.H/2)
	view.Frame()
	if slices.Equal(before, a.settings.AIUsage.OrderedQuotaTools()) {
		t.Fatalf("expected order to change after dragging GLM onto Codex, still %v", a.settings.AIUsage.OrderedQuotaTools())
	}
}

func TestLocalUsageIncludesAllTools(t *testing.T) {
	now := time.Date(2026, 11, 19, 14, 0, 0, 0, time.Local)
	cards := []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude}
	_, f := makeV1170Shelf(t, "zh-Hans", cards, now)
	f.summary = aiusage.Summary{
		Range: aiusage.RangeToday,
		Total: aiusage.Totals{Input: 30000, Output: 6000, Requests: 6, Cost: 0.45},
		ByTool: map[aiusage.Tool]aiusage.Totals{
			aiusage.ToolCodex:       {Input: 10000, Output: 2000, Requests: 2, Cost: 0.15},
			aiusage.ToolClaude:      {Input: 10000, Output: 2000, Requests: 2, Cost: 0.15},
			aiusage.ToolAntigravity: {Input: 10000, Output: 2000, Requests: 2, Cost: 0.15},
		},
	}
	for _, tool := range []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude, aiusage.ToolAntigravity} {
		if f.summary.ByTool[tool].Input == 0 {
			t.Fatalf("expected local usage for %s to remain present", tool)
		}
	}
}

func TestV1170cEvidenceScreenshots(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	outDir := usageEvidenceDir(t, "Rakko-v1170c")

	// 1. Settings -> AI 用量 (zh-Hans, default 2 enabled) & (en) & (4 selected).
	a, view := newSettingsShelf(t)
	a.settings.AIUsage.Enabled = true
	a.usage = newUsageFeature(a, usageDeps{})
	a.usage.sources = map[aiusage.Tool]aiusage.QuotaSource{
		aiusage.ToolCodex:    {Kind: aiusage.SourceLocalLogin},
		aiusage.ToolClaude:   {Kind: aiusage.SourceLocalLogin},
		aiusage.ToolGLM:      {Kind: aiusage.SourceLocalKey},
		aiusage.ToolDeepSeek: {Kind: aiusage.SourceSavedKey},
		aiusage.ToolCursor:   {Kind: aiusage.SourceLocalLogin},
		aiusage.ToolOpencode: {Kind: aiusage.SourceLocalKey},
	}
	a.usage.sourcesAt = time.Now()
	openSettingsPage(t, view, "AI")
	a.settingsScroll.Y = 120
	view.SetScale(2)
	view.Frame()
	view.Frame()
	saveTestPNG(t, outDir, "settings-aiusage-zh.png", view)

	view.SetLocale("en")
	view.Frame()
	view.Frame()
	saveTestPNG(t, outDir, "settings-aiusage-en.png", view)

	a4, view4 := newSettingsShelf(t)
	a4.settings.AIUsage.Enabled = true
	four := []string{"codex", "claude", "glm", "deepseek"}
	a4.settings.AIUsage.QuotaCards = &four
	a4.usage = newUsageFeature(a4, usageDeps{})
	a4.usage.sources = a.usage.sources
	a4.usage.sourcesAt = a.usage.sourcesAt
	openSettingsPage(t, view4, "AI")
	a4.settingsScroll.Y = 120
	view4.SetScale(2)
	view4.Frame()
	view4.Frame()
	saveTestPNG(t, outDir, "settings-aiusage-4selected.png", view4)

	// 2. Usage page with 4 cards (GLM, DeepSeek, Codex, Claude) to inspect ring-center logos.
	now := time.Date(2026, 11, 19, 14, 0, 0, 0, time.Local)
	cards := []aiusage.Tool{aiusage.ToolGLM, aiusage.ToolDeepSeek, aiusage.ToolCodex, aiusage.ToolClaude}
	ua, _ := makeV1170Shelf(t, "zh-Hans", cards, now)
	uview := ui.NewTester(ua.view, int(usageWideSheetW), sheetH)
	uview.SetLocale("zh-Hans")
	uview.SetScale(2)
	uview.Frame()
	uview.Frame()
	saveTestPNG(t, outDir, "usage-4cards-logos.png", uview)

	// 3. Usage page with 5, 6, 8 cards in zh-Hans and en (compact rows, full names).
	allEight := []aiusage.Tool{
		aiusage.ToolCodex, aiusage.ToolClaude, aiusage.ToolGLM, aiusage.ToolDeepSeek,
		aiusage.ToolCursor, aiusage.ToolKimi, aiusage.ToolOpencode, aiusage.ToolCommandCode,
	}
	populateEight := func(f *usageFeature) {
		samples := []aiusage.Quota{
			{
				Tool: aiusage.ToolCursor, Plan: "Pro", Status: aiusage.QuotaOK, FetchedAt: now,
				Windows: []aiusage.Window{
					{Name: "5 hours", UsedPct: 30, Length: 5 * time.Hour, ResetsAt: now.Add(2 * time.Hour)},
					{Name: "7 days", UsedPct: 55, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(90 * time.Hour)},
				},
			},
			{
				Tool: aiusage.ToolKimi, Plan: "Plus", Status: aiusage.QuotaOK, FetchedAt: now,
				Windows: []aiusage.Window{
					{Name: "5 hours", UsedPct: 20, Length: 5 * time.Hour, ResetsAt: now.Add(3 * time.Hour)},
					{Name: "7 days", UsedPct: 35, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(100 * time.Hour)},
				},
			},
			{
				Tool: aiusage.ToolOpencode, Plan: "Go", Status: aiusage.QuotaOK, FetchedAt: now,
				Windows: []aiusage.Window{
					{Name: "5 hours", UsedPct: 8, Length: 5 * time.Hour, ResetsAt: now.Add(4 * time.Hour)},
					{Name: "7 days", UsedPct: 22, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(110 * time.Hour)},
				},
			},
			{
				Tool: aiusage.ToolCommandCode, Plan: "Pro", Status: aiusage.QuotaOK, FetchedAt: now,
				Windows: []aiusage.Window{
					{Name: "5 hours", UsedPct: 60, Length: 5 * time.Hour, ResetsAt: now.Add(1 * time.Hour)},
					{Name: "7 days", UsedPct: 72, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(60 * time.Hour)},
				},
			},
		}
		for _, q := range samples {
			f.quotaState(q.Tool).latest = q
			f.quotaState(q.Tool).good = q
		}
	}
	for _, count := range []int{5, 6, 8} {
		for _, lang := range []string{"zh-Hans", "en"} {
			langTag := "zh"
			if lang == "en" {
				langTag = "en"
			}
			ca, cf := makeV1170Shelf(t, lang, allEight[:count], now)
			populateEight(cf)
			if got := cf.width(); got != usageWideSheetW {
				t.Fatalf("%d-card/%s width = %.0f, want %.0f", count, langTag, got, float32(usageWideSheetW))
			}
			cview := ui.NewTester(ca.view, int(usageWideSheetW), sheetH)
			cview.SetLocale(lang)
			cview.SetScale(2)
			cview.Frame()
			cview.Frame()
			if count == 8 && !cview.HasText("Command Code") {
				t.Fatalf("8-card/%s missing full 'Command Code' name", langTag)
			}
			saveTestPNG(t, outDir, "usage-"+string(rune('0'+count))+"cards-"+langTag+".png", cview)
		}
	}
}

func TestUsageCompactCodexHoverPopoverReset(t *testing.T) {
	now := time.Date(2026, 11, 19, 14, 0, 0, 0, time.Local)
	allEight := []aiusage.Tool{
		aiusage.ToolCodex, aiusage.ToolClaude, aiusage.ToolGLM, aiusage.ToolDeepSeek,
		aiusage.ToolCursor, aiusage.ToolKimi, aiusage.ToolOpencode, aiusage.ToolCommandCode,
	}
	a, f := makeV1170Shelf(t, "zh-Hans", allEight, now)
	f.deps.useCodexReset = func(context.Context) (aiusage.ResetOutcome, error) {
		return aiusage.ResetOutcome{Code: "reset", Windows: 2}, nil
	}
	view := ui.NewTester(a.view, int(usageWideSheetW), sheetH)
	view.SetLocale("zh-Hans")
	view.Frame()

	codexRect, ok := view.Find("Codex")
	if !ok {
		t.Fatal("missing Codex compact row")
	}
	view.Move(codexRect.X+codexRect.W/2, codexRect.Y+codexRect.H/2)
	view.Frame()
	view.Frame()
	if f.hoverTool != aiusage.ToolCodex {
		t.Fatalf("hoverTool = %q, want codex", f.hoverTool)
	}
	if err := view.Click("使用"); err != nil {
		t.Fatalf("click 使用 inside compact hover popover: %v", err)
	}
	view.Frame()
	if !f.codexResetConfirm {
		t.Fatal("expected codexResetConfirm dialog after clicking 使用 in hover popover")
	}
}
