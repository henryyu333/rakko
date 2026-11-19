//go:build darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/aiusage"
	"rakko/locales"
)

func TestSettingsAIUsageCheckboxDoesNotStickDrag(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	a.settings.AIUsage.Enabled = true
	a.usage = newUsageFeature(a, usageDeps{})
	openSettingsPage(t, view, "AI")
	a.settingsScroll.Y = 120
	view.Frame()

	l := locales.In(a.settings.Language)
	before := slices.Clone(a.settings.AIUsage.OrderedQuotaTools())
	glmLabel := settingsQuotaToolTitle(aiusage.ToolGLM, l) + " " + l.SettingsAIQuotaGroup()
	if err := view.Click(glmLabel); err != nil {
		t.Fatalf("click GLM panel switch %q: %v", glmLabel, err)
	}
	view.Frame()

	for _, target := range []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude, aiusage.ToolKimi} {
		r, ok := view.Find(settingsQuotaToolTitle(target, l))
		if !ok {
			t.Fatalf("missing row %s", target)
		}
		view.Move(r.X+r.W/2, r.Y+r.H/2)
		view.Frame()
	}

	after := a.settings.AIUsage.OrderedQuotaTools()
	if !slices.Equal(before, after) {
		t.Fatalf("order changed after clicking checkbox and hovering: before=%v after=%v", before, after)
	}
}

func TestSettingsAIUsageDragHandleReorders(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	a.settings.AIUsage.Enabled = true
	a.usage = newUsageFeature(a, usageDeps{})
	openSettingsPage(t, view, "AI")
	a.settingsScroll.Y = 120
	view.Frame()

	held := false
	origLeftDown := dragLeftDown
	dragLeftDown = func() bool { return held }
	t.Cleanup(func() { dragLeftDown = origLeftDown })

	l := locales.In(a.settings.Language)
	before := slices.Clone(a.settings.AIUsage.OrderedQuotaTools())
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
	// Small jitter below slop must not reorder.
	view.Move(glmRect.X+42, glmRect.Y+glmRect.H/2+2)
	view.Frame()
	if !slices.Equal(before, a.settings.AIUsage.OrderedQuotaTools()) {
		t.Fatalf("reordered below drag slop: before=%v after=%v", before, a.settings.AIUsage.OrderedQuotaTools())
	}

	// Move across to Codex row to reorder.
	view.Move(codexRect.X+40, codexRect.Y+codexRect.H/2)
	view.Frame()

	held = false
	view.Release(codexRect.X+40, codexRect.Y+codexRect.H/2)
	view.Frame()

	reordered := slices.Clone(a.settings.AIUsage.OrderedQuotaTools())
	if slices.Equal(before, reordered) {
		t.Fatalf("expected order to change after dragging GLM onto Codex, still %v", reordered)
	}

	// Hovering another row after release must not reorder further.
	kimiRect, ok := view.Find(settingsQuotaToolTitle(aiusage.ToolKimi, l))
	if !ok {
		t.Fatal("missing Kimi row")
	}
	view.Move(kimiRect.X+10, kimiRect.Y+kimiRect.H/2)
	view.Frame()
	if !slices.Equal(reordered, a.settings.AIUsage.OrderedQuotaTools()) {
		t.Fatalf("order changed while hovering after release: want=%v got=%v", reordered, a.settings.AIUsage.OrderedQuotaTools())
	}
}

// usageTestSeams swaps the goroutine and main-dispatch seams for a test and
// restores them only once the goroutines they started have finished: the
// refresher reads both seams again when it hands off to the main thread, so
// restoring them while one is still in flight races with that read.
func usageTestSeams(t *testing.T, mainQ chan func()) {
	t.Helper()
	oldGoWork, oldDispatch := goWork, dispatchMain
	var work atomic.Int64
	goWork = func(fn func()) {
		work.Add(1)
		go func() {
			defer work.Add(-1)
			fn()
		}()
	}
	dispatchMain = func(fn func()) { mainQ <- fn }
	t.Cleanup(func() {
		// Run the queued main-thread work while waiting, so a goroutine
		// blocked handing off against a full queue still gets through.
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if work.Load() == 0 && len(mainQ) == 0 {
				break
			}
			select {
			case fn := <-mainQ:
				fn()
			case <-time.After(2 * time.Millisecond):
			}
		}
		goWork, dispatchMain = oldGoWork, oldDispatch
	})
}

func TestCodexResetConfirmRecheckBusyAndInFlightDrop(t *testing.T) {
	mainQ := make(chan func(), 32)
	usageTestSeams(t, mainQ)
	drainMain := func() {
		for {
			select {
			case fn := <-mainQ:
				fn()
			default:
				return
			}
		}
	}

	// 1. Confirmation rechecks whether any window still has UsedPct > 0.
	a, _ := newSettingsShelf(t)
	var consumed atomic.Int32
	f := newUsageFeature(a, usageDeps{
		now: time.Now,
		useCodexReset: func(context.Context) (aiusage.ResetOutcome, error) {
			consumed.Add(1)
			return aiusage.ResetOutcome{Code: "reset", Windows: 2}, nil
		},
	})
	st := f.quotaState(aiusage.ToolCodex)
	st.latest = aiusage.Quota{
		Tool:         aiusage.ToolCodex,
		Status:       aiusage.QuotaOK,
		FetchedAt:    time.Now(),
		Windows:      []aiusage.Window{{Name: "5 hours", UsedPct: 50}},
		ResetCredits: &aiusage.ResetCredits{Count: 2, FirstID: "cr-1"},
	}
	f.requestUseCodexReset()
	if !f.codexResetConfirm {
		t.Fatal("expected confirmation dialog open")
	}
	// Polling clears usage before user confirms.
	st.latest.Windows = []aiusage.Window{{Name: "5 hours", UsedPct: 0}}
	f.confirmUseCodexReset()
	if consumed.Load() != 0 {
		t.Fatalf("consumed=%d when all windows became 0%% before confirm", consumed.Load())
	}

	// 2. Busy stays true until post-consume refresh arrives; in-flight pre-consume result is dropped.
	inFlightGate := make(chan struct{})
	postConsumeGate := make(chan struct{})
	var fetchCalls atomic.Int32
	f2 := newUsageFeature(a, usageDeps{
		now: time.Now,
		fetchCodex: func(context.Context) aiusage.Quota {
			n := fetchCalls.Add(1)
			if n == 1 {
				<-inFlightGate
				return aiusage.Quota{
					Tool:         aiusage.ToolCodex,
					Status:       aiusage.QuotaOK,
					FetchedAt:    time.Now(),
					Windows:      []aiusage.Window{{Name: "5 hours", UsedPct: 80}},
					ResetCredits: &aiusage.ResetCredits{Count: 2, FirstID: "cr-1"},
				}
			}
			<-postConsumeGate
			return aiusage.Quota{
				Tool:         aiusage.ToolCodex,
				Status:       aiusage.QuotaOK,
				FetchedAt:    time.Now(),
				Windows:      []aiusage.Window{{Name: "5 hours", UsedPct: 0}},
				ResetCredits: &aiusage.ResetCredits{Count: 1, FirstID: "cr-2"},
			}
		},
		useCodexReset: func(context.Context) (aiusage.ResetOutcome, error) {
			return aiusage.ResetOutcome{Code: "reset", Windows: 2}, nil
		},
	})
	st2 := f2.quotaState(aiusage.ToolCodex)
	st2.latest = aiusage.Quota{
		Tool:         aiusage.ToolCodex,
		Status:       aiusage.QuotaOK,
		FetchedAt:    time.Now(),
		Windows:      []aiusage.Window{{Name: "5 hours", UsedPct: 80}},
		ResetCredits: &aiusage.ResetCredits{Count: 2, FirstID: "cr-1"},
	}

	// Start an in-flight Codex fetch before consume.
	f2.startQuota(aiusage.ToolCodex)
	f2.requestUseCodexReset()
	f2.confirmUseCodexReset()
	// Wait for useCodexReset callback to run on main.
	for i := 0; i < 40 && fetchCalls.Load() < 2; i++ {
		drainMain()
		time.Sleep(10 * time.Millisecond)
	}
	if fetchCalls.Load() < 2 {
		t.Fatalf("expected post-consume Codex fetch to start even while pre-consume fetch was in flight; fetchCalls=%d", fetchCalls.Load())
	}
	if !f2.codexResetBusy {
		t.Fatal("expected codexResetBusy to stay true until post-consume refresh arrives")
	}

	// Release stale pre-consume fetch: must be dropped and keep codexResetBusy=true.
	close(inFlightGate)
	for i := 0; i < 15; i++ {
		drainMain()
		time.Sleep(5 * time.Millisecond)
	}
	if !f2.codexResetBusy {
		t.Fatal("stale pre-consume fetch cleared codexResetBusy")
	}

	// Release post-consume fetch: updates count to 1 and clears codexResetBusy.
	close(postConsumeGate)
	for i := 0; i < 40 && f2.codexResetBusy; i++ {
		drainMain()
		time.Sleep(10 * time.Millisecond)
	}
	if f2.codexResetBusy {
		t.Fatal("codexResetBusy stayed true after post-consume refresh arrived")
	}
	q, _ := f2.quotaShown(aiusage.ToolCodex)
	if q.ResetCredits == nil || q.ResetCredits.Count != 1 {
		t.Fatalf("after post-consume refresh, ResetCredits = %+v, want Count=1", q.ResetCredits)
	}

	// 3. When POST was sent but response failed, still refresh Codex credits/quota.
	var errRefreshes atomic.Int32
	f3 := newUsageFeature(a, usageDeps{
		now: time.Now,
		fetchCodex: func(context.Context) aiusage.Quota {
			errRefreshes.Add(1)
			return aiusage.Quota{
				Tool:         aiusage.ToolCodex,
				Status:       aiusage.QuotaOK,
				FetchedAt:    time.Now(),
				Windows:      []aiusage.Window{{Name: "5 hours", UsedPct: 0}},
				ResetCredits: &aiusage.ResetCredits{Count: 1, FirstID: "cr-2"},
			}
		},
		useCodexReset: func(context.Context) (aiusage.ResetOutcome, error) {
			return aiusage.ResetOutcome{PostSent: true}, context.DeadlineExceeded
		},
	})
	st3 := f3.quotaState(aiusage.ToolCodex)
	st3.latest = aiusage.Quota{
		Tool:         aiusage.ToolCodex,
		Status:       aiusage.QuotaOK,
		FetchedAt:    time.Now(),
		Windows:      []aiusage.Window{{Name: "5 hours", UsedPct: 60}},
		ResetCredits: &aiusage.ResetCredits{Count: 2, FirstID: "cr-1"},
	}
	f3.requestUseCodexReset()
	f3.confirmUseCodexReset()
	for i := 0; i < 40 && (errRefreshes.Load() == 0 || f3.codexResetBusy); i++ {
		drainMain()
		time.Sleep(10 * time.Millisecond)
	}
	if errRefreshes.Load() == 0 || f3.codexResetBusy {
		t.Fatalf("after PostSent error: errRefreshes=%d codexResetBusy=%v", errRefreshes.Load(), f3.codexResetBusy)
	}
}

func TestSettingsEnglishCodexClaudeSourceLabels(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	view.SetLocale("en")
	a.settings.Language = "en"
	a.settings.AIUsage.Enabled = true
	a.usage = newUsageFeature(a, usageDeps{})
	a.usage.sources = map[aiusage.Tool]aiusage.QuotaSource{
		aiusage.ToolCodex:  {Kind: aiusage.SourceLocalLogin},
		aiusage.ToolClaude: {Kind: aiusage.SourceLocalLogin},
	}
	a.usage.sourcesAt = time.Now()

	openSettingsPage(t, view, "AI")
	view.Frame()
	if !view.HasText("Local Codex login") {
		t.Fatalf("missing English Codex local login label; texts=%v", view.Texts())
	}
	if !view.HasText("Local Claude Code login") {
		t.Fatalf("missing English Claude local login label; texts=%v", view.Texts())
	}
}

func TestSettingsAntigravityPanelDashAndEvidence(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	_ = os.MkdirAll("/tmp/Rakko-v1170b", 0o755)
	_ = os.Remove(filepath.Join("/tmp/Rakko-v1170b", "settings-antigravity-risk-zh.png"))
	_ = os.Remove(filepath.Join("/tmp/Rakko-v1170b", "settings-antigravity-risk-en.png"))
	a, view := newSettingsShelf(t)
	a.settings.AIUsage.Enabled = true
	a.usage = newUsageFeature(a, usageDeps{})
	openSettingsPage(t, view, "AI")
	a.settingsScroll.Y = 120
	view.SetScale(2)
	view.Frame()
	view.Frame()

	// Antigravity must not appear in the quota group at all.
	l := locales.In("zh-Hans")
	if _, ok := view.Find(settingsQuotaToolTitle(aiusage.ToolAntigravity, l)); ok {
		t.Fatal("expected no Antigravity row in quota group")
	}
	if slices.Contains(a.settings.AIUsage.OrderedQuotaTools(), aiusage.ToolAntigravity) {
		t.Fatal("OrderedQuotaTools must not contain ToolAntigravity")
	}
	if a.settings.AIUsage.SetQuotaCard(aiusage.ToolAntigravity, true) {
		t.Fatal("SetQuotaCard(ToolAntigravity, true) must return false")
	}
	saveTestPNG(t, filepath.Join("/tmp/Rakko-v1170b", "settings-aiusage-zh.png"), view)

	// English screenshot.
	view.SetLocale("en")
	view.Frame()
	view.Frame()
	saveTestPNG(t, filepath.Join("/tmp/Rakko-v1170b", "settings-aiusage-en.png"), view)
}

func TestRefreshSourcesForceQueuesWhileRunning(t *testing.T) {
	mainQ := make(chan func(), 32)
	usageTestSeams(t, mainQ)
	drainMain := func() {
		for {
			select {
			case fn := <-mainQ:
				fn()
			default:
				return
			}
		}
	}

	a, _ := newSettingsShelf(t)
	firstGate := make(chan struct{})
	var hasSavedKey atomic.Bool
	var glmProbes atomic.Int32
	f := newUsageFeature(a, usageDeps{
		now: time.Now,
		detectSource: func(tool aiusage.Tool) aiusage.QuotaSource {
			if tool == aiusage.ToolGLM {
				n := glmProbes.Add(1)
				if n == 1 {
					<-firstGate
				}
				if hasSavedKey.Load() {
					return aiusage.QuotaSource{Kind: aiusage.SourceSavedKey}
				}
			}
			return aiusage.QuotaSource{Kind: aiusage.SourceNone}
		},
		saveSecret: func(tool aiusage.Tool, key string) error {
			hasSavedKey.Store(key != "")
			return nil
		},
	})

	// 1. Start initial probe (blocks on firstGate).
	f.refreshSources(false)
	for i := 0; i < 40 && glmProbes.Load() < 1; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if glmProbes.Load() != 1 {
		t.Fatalf("first probe did not start: glmProbes=%d", glmProbes.Load())
	}

	// 2. Save API key while initial probe is in flight.
	f.saveAPIKey(aiusage.ToolGLM, "glm-new-key")
	for i := 0; i < 40 && !hasSavedKey.Load(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	drainMain()

	// 3. Release initial probe (returns SourceNone); queued force refresh must run next.
	close(firstGate)
	for i := 0; i < 60 && (glmProbes.Load() < 2 || f.sourceOf(aiusage.ToolGLM).Kind != aiusage.SourceSavedKey); i++ {
		drainMain()
		time.Sleep(10 * time.Millisecond)
	}
	if glmProbes.Load() < 2 || f.sourceOf(aiusage.ToolGLM).Kind != aiusage.SourceSavedKey {
		t.Fatalf("expected queued force refresh after saveAPIKey: glmProbes=%d source=%v", glmProbes.Load(), f.sourceOf(aiusage.ToolGLM))
	}
}

func TestCodexResetBusyAndAfterEvidence(t *testing.T) {
	_ = os.MkdirAll("/tmp/Rakko-v1170b", 0o755)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.Local)
	a, f := makeV1170Shelf(t, "zh-Hans", []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude}, now)
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.SetLocale("zh-Hans")
	view.SetScale(2)

	// Busy state: "使用" button is disabled while consuming and waiting for post-consume refresh.
	f.codexResetBusy = true
	view.Frame()
	view.Frame()
	saveTestPNG(t, filepath.Join("/tmp/Rakko-v1170b", "codex-reset-busy.png"), view)

	// After post-consume refresh: count decremented from 2 to 1, windows reset to 0%, busy cleared.
	f.codexResetBusy = false
	st := f.quotaState(aiusage.ToolCodex)
	st.latest.Windows = []aiusage.Window{
		{Name: "5 hours", UsedPct: 0, Length: 5 * time.Hour, ResetsAt: now.Add(5 * time.Hour)},
		{Name: "7 days", UsedPct: 0, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(7 * 24 * time.Hour)},
	}
	st.latest.ResetCredits = &aiusage.ResetCredits{
		Count:     1,
		SoonestAt: time.Date(2026, 12, 15, 0, 0, 0, 0, time.Local),
		FirstID:   "cr-2",
	}
	st.good = st.latest
	view.Frame()
	view.Frame()
	saveTestPNG(t, filepath.Join("/tmp/Rakko-v1170b", "codex-reset-after.png"), view)
}
