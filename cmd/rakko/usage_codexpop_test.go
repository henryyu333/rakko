//go:build darwin

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"rakko/internal/aiusage"
)

// slideTo moves the pointer from (x0,y0) to (x1,y1) in small steps, rendering
// a frame at each so the hover state machine sees every intermediate position.
func slideTo(view *ui.Tester, x0, y0, x1, y1 float32) {
	const steps = 40
	for i := 1; i <= steps; i++ {
		k := float32(i) / steps
		view.Move(x0+(x1-x0)*k, y0+(y1-y0)*k)
		view.Frame()
	}
	view.Frame()
}

func stubCodexReset(f *usageFeature) {
	f.deps.useCodexReset = func(context.Context) (aiusage.ResetOutcome, error) {
		return aiusage.ResetOutcome{Code: "reset", Windows: 2}, nil
	}
}

func center(r ui.Rect) (float32, float32) { return r.X + r.W/2, r.Y + r.H/2 }

func TestCodexPopTwoCardUseButtonReachable(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.Local)
	cards := []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude}
	a, f := makeV1170Shelf(t, "zh-Hans", cards, now)
	stubCodexReset(f)
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.SetLocale("zh-Hans")
	view.Move(sheetW-4, sheetH-4)
	view.Frame()
	if view.HasText("使用") || !view.HasText("重置卡 2") {
		t.Fatalf("unhovered: want reset line without button; texts=%v", view.Texts())
	}
	codex, ok := view.Find("Codex")
	if !ok {
		t.Fatal("missing Codex card")
	}
	cx, cy := center(codex)
	view.Move(cx, cy)
	view.Frame()
	view.Frame()
	btn, ok := view.Find("使用")
	if !ok {
		t.Fatalf("hover popover has no 使用 button; texts=%v", view.Texts())
	}
	bx, by := center(btn)
	if bx <= usageLeftW {
		t.Fatalf("button x=%.0f should lie beyond the left column (%d)", bx, usageLeftW)
	}
	slideTo(view, cx, cy, bx, by)
	if f.hoverTool != aiusage.ToolCodex || !view.HasText("使用") {
		t.Fatalf("popover closed while moving to the button; hover=%q", f.hoverTool)
	}
	if err := view.Click("使用"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if !f.codexResetConfirm {
		t.Fatal("expected confirmation dialog")
	}
}

func TestCodexPopTwoCardLeavesCleanly(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.Local)
	cards := []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude}
	a, f := makeV1170Shelf(t, "zh-Hans", cards, now)
	stubCodexReset(f)
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.SetLocale("zh-Hans")
	view.Frame()
	codex, _ := view.Find("Codex")
	claude, ok := view.Find("Claude Code")
	if !ok {
		t.Fatal("missing Claude card")
	}
	cx, cy := center(codex)
	view.Move(cx, cy)
	view.Frame()
	view.Frame()
	btn, _ := view.Find("使用")
	bx, by := center(btn)
	slideTo(view, cx, cy, bx, by)

	// Out to the empty right side: the popover closes.
	slideTo(view, bx, by, sheetW-4, by)
	if view.HasText("使用") || f.hoverTool != "" {
		t.Fatalf("popover stuck after leaving to the right; hover=%q", f.hoverTool)
	}

	// Back onto the Codex card and its button: the popover reopens and holds.
	slideTo(view, sheetW-4, by, cx, cy)
	slideTo(view, cx, cy, bx, by)
	if !view.HasText("使用") {
		t.Fatal("popover did not reopen")
	}
	// The popover covers the Claude card, so Claude is reached by leaving
	// the popover first, then entering the card from the side.
	slideTo(view, bx, by, sheetW-4, by)
	clx, cly := center(claude)
	slideTo(view, sheetW-4, by, sheetW-4, cly)
	slideTo(view, sheetW-4, cly, clx, cly)
	if f.hoverTool != aiusage.ToolClaude || view.HasText("使用") {
		t.Fatalf("want Claude popover, got hover=%q", f.hoverTool)
	}
}

func TestCodexPopWideCrossesColumnGap(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.Local)
	for _, n := range []int{4, 8} {
		all := []aiusage.Tool{
			aiusage.ToolCodex, aiusage.ToolClaude, aiusage.ToolGLM, aiusage.ToolDeepSeek,
			aiusage.ToolCursor, aiusage.ToolKimi, aiusage.ToolOpencode, aiusage.ToolCommandCode,
		}
		a, f := makeV1170Shelf(t, "zh-Hans", all[:n], now)
		stubCodexReset(f)
		view := ui.NewTester(a.view, int(usageWideSheetW), sheetH)
		view.SetLocale("zh-Hans")
		view.Move(usageWideSheetW-4, sheetH-4)
		view.Frame()
		if view.HasText("使用") {
			t.Fatalf("%d cards: unhovered shows 使用", n)
		}
		codex, ok := view.Find("Codex")
		if !ok {
			t.Fatalf("%d cards: missing Codex", n)
		}
		cx, cy := center(codex)
		view.Move(cx, cy)
		view.Frame()
		view.Frame()
		btn, ok := view.Find("使用")
		if !ok {
			t.Fatalf("%d cards: no 使用 in popover", n)
		}
		bx, by := center(btn)
		if bx <= usageLeftW {
			t.Fatalf("%d cards: popover should sit in the right column, button x=%.0f", n, bx)
		}
		// Slow sweep straight across the old 6pt seam.
		slideTo(view, cx, cy, bx, cy)
		slideTo(view, bx, cy, bx, by)
		if f.hoverTool != aiusage.ToolCodex || !view.HasText("使用") {
			t.Fatalf("%d cards: popover dropped crossing the seam; hover=%q", n, f.hoverTool)
		}
		if err := view.Click("使用"); err != nil {
			t.Fatal(err)
		}
		view.Frame()
		if !f.codexResetConfirm {
			t.Fatalf("%d cards: expected confirmation dialog", n)
		}
	}
}

func TestCodexPopTwoCardScreenshots(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.Local)
	a, _ := makeV1170Shelf(t, "zh-Hans", []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude}, now)
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.SetLocale("zh-Hans")
	dir := t.TempDir()
	view.SetScale(2)
	view.Move(sheetW-4, sheetH-4)
	view.Frame()
	view.Frame()
	saveTestPNG(t, filepath.Join(dir, "two-normal.png"), view)
	codex, _ := view.Find("Codex")
	cx, cy := center(codex)
	view.Move(cx, cy)
	view.Frame()
	view.Frame()
	saveTestPNG(t, filepath.Join(dir, "two-hover-codex.png"), view)
}
