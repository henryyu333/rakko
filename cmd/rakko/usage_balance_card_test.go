package main

import (
	"context"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/aiusage"
	"rakko/internal/settings"
)

type balanceFakeScanner struct{}

func (balanceFakeScanner) Scan(context.Context, func(int64, int64)) error { return nil }
func (balanceFakeScanner) Summary(r aiusage.Range, _ time.Time) aiusage.Summary {
	return aiusage.Summary{Range: r}
}
func (balanceFakeScanner) UnreadableTools() []aiusage.Tool { return nil }

func balanceCardFixture(t *testing.T, cards []aiusage.Tool, now time.Time) (*shelf, *usageFeature) {
	t.Helper()
	cfg := settings.Default()
	cfg.Language = "zh-Hans"
	cfg.AIUsage.Enabled = true
	raw := make([]string, len(cards))
	for i, c := range cards {
		raw[i] = string(c)
	}
	cfg.AIUsage.QuotaCards = &raw

	a := &shelf{
		dataDir: t.TempDir(), level: 2, tab: pageUsage, visible: true, locale: "zh-Hans",
		settings:   cfg,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	noop := func(context.Context) aiusage.Quota { return aiusage.Quota{} }
	f := newUsageFeature(a, usageDeps{
		fetchCodex:  noop,
		fetchClaude: noop,
		fetchMore: map[aiusage.Tool]func(context.Context) aiusage.Quota{
			aiusage.ToolGLM:      noop,
			aiusage.ToolDeepSeek: noop,
			aiusage.ToolCursor:   noop,
		},
		newScanner: func() usageScanner { return balanceFakeScanner{} },
		pace:       aiusage.Pace,
		now:        func() time.Time { return now },
	})
	a.registerPage(f)

	samples := []aiusage.Quota{
		{
			Tool: aiusage.ToolCodex, Plan: "Plus", Status: aiusage.QuotaOK, FetchedAt: now,
			Windows: []aiusage.Window{
				{Name: "5 hours", UsedPct: 55, Length: 5 * time.Hour, ResetsAt: now.Add(2 * time.Hour)},
				{Name: "7 days", UsedPct: 40, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(48 * time.Hour)},
			},
		},
		{
			Tool: aiusage.ToolClaude, Plan: "Pro", Status: aiusage.QuotaOK, FetchedAt: now,
			Windows: []aiusage.Window{
				{Name: "5 hours", UsedPct: 2, Length: 5 * time.Hour, ResetsAt: now.Add(4 * time.Hour)},
				{Name: "7 days", UsedPct: 55, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(72 * time.Hour)},
			},
		},
		{
			Tool: aiusage.ToolGLM, Plan: "Pro", Status: aiusage.QuotaOK, FetchedAt: now,
			Windows: []aiusage.Window{
				{Name: "5 hours", UsedPct: 12, Length: 5 * time.Hour, ResetsAt: now.Add(2 * time.Hour)},
				{Name: "7 days", UsedPct: 35, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(80 * time.Hour)},
			},
		},
		{
			Tool: aiusage.ToolDeepSeek, Plan: "API", Status: aiusage.QuotaOK, FetchedAt: now,
			Balance: &aiusage.BalanceInfo{
				Currency: "¥", Amount: 49.31, Text: "¥ 49.31", TopUp: "40.00", Granted: "9.31",
			},
		},
		{
			Tool: aiusage.ToolCursor, Plan: "Pro", Status: aiusage.QuotaOK, FetchedAt: now,
			Windows: []aiusage.Window{
				{Name: "5 hours", UsedPct: 30, Length: 5 * time.Hour, ResetsAt: now.Add(2 * time.Hour)},
				{Name: "7 days", UsedPct: 50, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(90 * time.Hour)},
			},
		},
	}
	for _, q := range samples {
		f.quotaState(q.Tool).latest = q
		f.quotaState(q.Tool).good = q
	}
	return a, f
}

func saveBalanceShot(t *testing.T, path string, view *ui.Tester) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, view.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestDeepSeekBalanceCardLogoOnly(t *testing.T) {
	now := time.Date(2026, 11, 23, 16, 0, 0, 0, time.Local)
	shotDir := os.Getenv("RAKKO_BAL_SHOT_DIR")

	// 1. Normal card (50pt slot) & full 4-card page.
	four := []aiusage.Tool{aiusage.ToolClaude, aiusage.ToolDeepSeek, aiusage.ToolCodex, aiusage.ToolGLM}
	a4, f4 := balanceCardFixture(t, four, now)
	dsQuota, _ := f4.quotaShown(aiusage.ToolDeepSeek)

	normalCardView := ui.NewTester(func(c *ui.Context) {
		c.Root().Background(surface)
		f4.quotaCard(c, aiusage.ToolDeepSeek, usageLeftW)
	}, usageLeftW, usageCardH)
	normalCardView.SetLocale("zh-Hans")
	normalCardView.SetScale(2)
	normalCardView.Frame()
	normalCardView.Frame()
	if shotDir != "" {
		saveBalanceShot(t, filepath.Join(shotDir, "normal-card.png"), normalCardView)
	}

	page4View := ui.NewTester(a4.view, int(usageWideSheetW), sheetH)
	page4View.SetLocale("zh-Hans")
	page4View.SetScale(2)
	page4View.Frame()
	page4View.Frame()
	if shotDir != "" {
		saveBalanceShot(t, filepath.Join(shotDir, "page-4cards.png"), page4View)
	}

	// 2. Compact card (28pt slot) & 5-card compact page.
	five := []aiusage.Tool{aiusage.ToolCodex, aiusage.ToolClaude, aiusage.ToolGLM, aiusage.ToolDeepSeek, aiusage.ToolCursor}
	a5, f5 := balanceCardFixture(t, five, now)

	compactCardView := ui.NewTester(func(c *ui.Context) {
		c.Root().Background(surface)
		f5.quotaCompactCard(c, aiusage.ToolDeepSeek, usageLeftW)
	}, usageLeftW, usageCompactRowH)
	compactCardView.SetLocale("zh-Hans")
	compactCardView.SetScale(2)
	compactCardView.Frame()
	compactCardView.Frame()
	if shotDir != "" {
		saveBalanceShot(t, filepath.Join(shotDir, "compact-card.png"), compactCardView)
	}

	page5View := ui.NewTester(a5.view, int(usageWideSheetW), sheetH)
	page5View.SetLocale("zh-Hans")
	page5View.SetScale(2)
	page5View.Frame()
	page5View.Frame()
	if shotDir != "" {
		saveBalanceShot(t, filepath.Join(shotDir, "page-5cards.png"), page5View)
	}

	// Verify neither normal nor compact card renders the "余额"/"Balance" text.
	for _, view := range []*ui.Tester{normalCardView, compactCardView} {
		if view.HasText("余额") || view.HasText("Balance") {
			t.Fatalf("balance card still renders 余额 label; texts=%v", view.Texts())
		}
		if !view.HasText("DeepSeek") || !view.HasText("¥ 49.31") {
			t.Fatalf("balance card lost title or balance text; texts=%v", view.Texts())
		}
	}

	// Verify both normal (50pt) and compact (28pt) slots:
	// - No grey ring track on the outer perimeter
	// - Brand-blue (#4D6BFE) logo centered in the slot, scaled proportionally to 30 * ring / 50
	for _, tc := range []struct {
		name      string
		ring      float32
		wantMinW  float64
		wantMaxW  float64
		wantMinH  float64
		wantMaxH  float64
		outerRing float64
	}{
		{"normal", usageRingSize, 24, 32, 18, 25, 19.5},
		{"compact", usageCompactRingSize, 13, 19, 9, 14, 10.8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sz := int(tc.ring)
			v := ui.NewTester(func(c *ui.Context) {
				c.Root().Background(ui.RGB(0, 0, 0))
				f4.quotaRingSized(c, aiusage.ToolDeepSeek, dsQuota, false, tc.ring)
			}, sz, sz)
			v.Frame()
			img := v.Image()
			b := img.Bounds()
			cx, cy := float64(tc.ring)/2, float64(tc.ring)/2
			var sx, sy, n float64
			minX, minY := b.Max.X, b.Max.Y
			maxX, maxY := b.Min.X, b.Min.Y
			var ringTrackPx int
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					r, g, bl, _ := img.At(x, y).RGBA()
					r, g, bl = r>>8, g>>8, bl>>8
					px, py := float64(x-b.Min.X)+0.5, float64(y-b.Min.Y)+0.5
					dist := math.Hypot(px-cx, py-cy)
					// Brand blue (#4D6BFE): blue channel dominates red and green.
					if bl > r+40 && bl > 150 {
						sx += float64(x - b.Min.X)
						sy += float64(y - b.Min.Y)
						n++
						if x-b.Min.X < minX {
							minX = x - b.Min.X
						}
						if x-b.Min.X > maxX {
							maxX = x - b.Min.X
						}
						if y-b.Min.Y < minY {
							minY = y - b.Min.Y
						}
						if y-b.Min.Y > maxY {
							maxY = y - b.Min.Y
						}
						continue
					}
					// Outer perimeter where the old fill3 track lived: must stay pure black.
					if dist >= tc.outerRing && (r > 12 || g > 12 || bl > 12) {
						ringTrackPx++
					}
				}
			}
			if ringTrackPx > 0 {
				t.Fatalf("%s: found %d ring-track pixels on outer perimeter (r>=%.1f), ring should be removed", tc.name, ringTrackPx, tc.outerRing)
			}
			if n == 0 {
				t.Fatalf("%s: no brand-blue DeepSeek logo pixels found", tc.name)
			}
			gotCX, gotCY := sx/n, sy/n
			if math.Abs(gotCX-cx) > 2.0 || math.Abs(gotCY-cy) > 2.0 {
				t.Fatalf("%s: logo centroid (%.1f, %.1f) not centred at (%.1f, %.1f)", tc.name, gotCX, gotCY, cx, cy)
			}
			w := float64(maxX - minX + 1)
			h := float64(maxY - minY + 1)
			if w < tc.wantMinW || w > tc.wantMaxW || h < tc.wantMinH || h > tc.wantMaxH {
				t.Fatalf("%s: logo bounds %.0f×%.0f outside expected w=[%.0f, %.0f] h=[%.0f, %.0f]", tc.name, w, h, tc.wantMinW, tc.wantMaxW, tc.wantMinH, tc.wantMaxH)
			}
		})
	}
}
