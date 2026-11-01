//go:build darwin

package main

// Real-window proof for the panel-height fix (run with SHELF_BASKET_SMOKE=1):
// a track playing and the AI 用量 page full (two quota cards, the local
// summary's chart) must fit the expanded panel in one screen — no scrolling
// inside the stage. The shot lands in /tmp/shelf-fix-height.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"shelf/internal/aiusage"
	"shelf/internal/mac"
	"shelf/internal/settings"
)

const sheetHShotDir = "/tmp/shelf-fix-height"

// smokeUsageScanner is a local scanner with a fixed week of usage, so the
// page draws its chart instead of the scan card.
type smokeUsageScanner struct{ now time.Time }

func (s *smokeUsageScanner) Scan(context.Context, func(done, total int64)) error { return nil }

func (s *smokeUsageScanner) Summary(r aiusage.Range, now time.Time) aiusage.Summary {
	buckets := make([]aiusage.Bucket, 0, 7)
	for i := 6; i >= 0; i-- {
		buckets = append(buckets, aiusage.Bucket{
			Start: now.AddDate(0, 0, -i),
			Tokens: map[aiusage.Tool]int64{
				aiusage.ToolClaude: 1_200_000,
				aiusage.ToolCodex:  640_000,
			},
		})
	}
	return aiusage.Summary{
		Range: r,
		Total: aiusage.Totals{Input: 8_400_000, Output: 1_100_000, CacheRead: 5_200_000,
			Requests: 412, Cost: 3.72},
		ByTool: map[aiusage.Tool]aiusage.Totals{
			aiusage.ToolClaude: {Input: 5_400_000, Output: 700_000, Requests: 260, Cost: 2.40},
			aiusage.ToolCodex:  {Input: 3_000_000, Output: 400_000, Requests: 152, Cost: 1.32},
		},
		Buckets: buckets,
	}
}

// shotPanelHeight screenshots the notch window to
// /tmp/shelf-fix-height/name.png.
func shotPanelHeight(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	var num int
	onMain(func() { num = mac.WindowNumber(a.win.NativeHandle()) })
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	if err := os.MkdirAll(sheetHShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // settle the height animation
	shot := filepath.Join(sheetHShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
	return shot
}

// TestSmokePanelHeightFitsMusicAndUsage opens the real panel on a playing
// track with the AI 用量 page's full content and proves the native window
// is exactly the sheet's box (420 × 250 plus ears), then screenshots it.
func TestSmokePanelHeightFitsMusicAndUsage(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := redesignShelf(t)
	now := time.Now()
	var f *usageFeature
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
		a.modules = []module{a.np}
		f = newUsageFeature(a, usageDeps{
			fetchCodex:  func(context.Context) aiusage.Quota { return okQuota(aiusage.ToolCodex, "Plus", now) },
			fetchClaude: func(context.Context) aiusage.Quota { return okQuota(aiusage.ToolClaude, "Pro", now) },
			pace:        aiusage.Pace,
			newScanner:  func() usageScanner { return &smokeUsageScanner{now: now} },
			now:         func() time.Time { return now },
		})
		a.settings.AIUsage = settings.AIUsageSettings{Enabled: true, QuotaCodex: true, QuotaClaude: true}
		a.registerPage(f)
	})
	expandShelf(t, a)
	onMain(func() {
		a.setLevel(2)
		a.focusMode = true // put the window below the menu bar for a clean shot
		a.progress, a.animationFrom = 1, 1
		a.animationStart = time.Time{}
		a.syncWindowFrame()
	})
	onMain(func() { f.tick(now) }) // quota answers + the local summary, inline fakes
	time.Sleep(700 * time.Millisecond)

	var h, w, frameH int
	onMain(func() {
		h, w = a.panelH(), sheetW
		frameH = a.win.Bounds().Height
	})
	t.Logf("panel=%d×%d frame=%d design=%d×%d", w, h, frameH, sheetW, sheetH)
	if h != sheetH || w != sheetW {
		t.Errorf("panel %d×%d, want the fixed sheet %d×%d", w, h, sheetW, sheetH)
	}
	// The window carries the page capsule hung under the sheet.
	if want := sheetH + a.sheetTop() + navGap + navH + 2; frameH != want {
		t.Errorf("native frame %d does not follow the sheet and its capsule %d", frameH, want)
	}
	shotPanelHeight(t, a, "music-usage")

	onMain(func() {
		f.stopWork()
		a.focusMode = false
		a.hide(hidePassive)
		a.usage = nil
		a.modules = nil
		a.np = nil
		a.settings.AIUsage.Enabled = false
		a.settings.IslandNowPlaying = false
		a.setTab(pageTray)
	})
}
