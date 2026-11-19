package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/aiusage"
	"rakko/internal/settings"
)

func TestUsageToolCard2x1MissingWindowCombinations(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		windows   []aiusage.Window
		wantOK    bool
		wantHero  float64
		wantWeek  float64
		wantFive  float64
		wantText  string
		wantLabel []string
	}{
		{
			name:     "no windows",
			windows:  nil,
			wantOK:   false,
			wantText: "暂无数据",
			wantLabel: []string{
				"AI 用量",
				"暂无数据",
			},
		},
		{
			name: "only 5h window",
			windows: []aiusage.Window{
				{Name: "5 hours", UsedPct: 25, Length: 5 * time.Hour, ResetsAt: now.Add(2 * time.Hour)},
			},
			wantOK:    true,
			wantHero:  75,
			wantWeek:  0,
			wantFive:  75,
			wantText:  "75%",
			wantLabel: []string{"5 小时", "75% 剩余"},
		},
		{
			name: "only 7d window",
			windows: []aiusage.Window{
				{Name: "7 days", UsedPct: 40, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(48 * time.Hour)},
			},
			wantOK:    true,
			wantHero:  60,
			wantWeek:  60,
			wantFive:  0,
			wantText:  "60%",
			wantLabel: []string{"每周", "60% 剩余"},
		},
		{
			name: "both windows tightest wins hero",
			windows: []aiusage.Window{
				{Name: "5 hours", UsedPct: 20, Length: 5 * time.Hour, ResetsAt: now.Add(2 * time.Hour)},
				{Name: "7 days", UsedPct: 65, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(48 * time.Hour)},
			},
			wantOK:    true,
			wantHero:  35,
			wantWeek:  35,
			wantFive:  80,
			wantText:  "35%",
			wantLabel: []string{"5 小时", "80% 剩余", "每周", "35% 剩余"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := settings.Default()
			cfg.AIUsage.Enabled = true
			a := &shelf{settings: cfg}
			f := newUsageFeature(a, usageDeps{
				now:        func() time.Time { return now },
				fetchCodex: func(context.Context) aiusage.Quota { return aiusage.Quota{} },
			})
			if len(tc.windows) > 0 {
				q := aiusage.Quota{
					Tool:      aiusage.ToolCodex,
					Status:    aiusage.QuotaOK,
					FetchedAt: now,
					Windows:   tc.windows,
				}
				f.quotaState(aiusage.ToolCodex).latest = q
				f.quotaState(aiusage.ToolCodex).good = q
			}

			hero, week, five, ok := a.usageWideShares()
			if ok != tc.wantOK || hero != tc.wantHero || week != tc.wantWeek || five != tc.wantFive {
				t.Fatalf("usageWideShares() = (%.1f, %.1f, %.1f, %v), want (%.1f, %.1f, %.1f, %v)",
					hero, week, five, ok, tc.wantHero, tc.wantWeek, tc.wantFive, tc.wantOK)
			}

			var label string
			view := ui.NewTester(func(c *ui.Context) {
				card := ui.Box(c.Key("card")).Size(205, 84)
				a.toolUsageBody(c, card, 2, cardPanel)
				label = a.usageCardLeftLabel(c)
			}, 220, 100)
			view.Frame()

			if !view.HasText(tc.wantText) {
				t.Fatalf("missing hero text %q; texts=%v", tc.wantText, view.Texts())
			}
			for _, sub := range tc.wantLabel {
				if !strings.Contains(label, sub) {
					t.Fatalf("label %q missing %q", label, sub)
				}
			}
		})
	}
}
