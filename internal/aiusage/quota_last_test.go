package aiusage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadLastQuotas(t *testing.T) {
	dir := t.TempDir()
	q := Quota{
		Tool:      ToolClaude,
		Plan:      "Pro",
		Status:    QuotaOK,
		FetchedAt: fakeNow,
		Windows: []Window{
			{Name: "5 hours", UsedPct: 7, Length: 5 * time.Hour, ResetsAt: fakeNow.Add(4 * time.Hour)},
			{Name: "7 days · Opus", Model: "opus", UsedPct: 12.5, Length: 7 * 24 * time.Hour},
		},
	}
	if err := SaveLastQuota(dir, q); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := LoadLastQuotas(dir)
	if len(got) != 1 {
		t.Fatalf("loaded %d readings, want 1", len(got))
	}
	g := got[0]
	if g.Tool != ToolClaude || g.Plan != "Pro" || g.Status != QuotaOK || !g.FetchedAt.Equal(fakeNow) {
		t.Fatalf("quota = %+v", g)
	}
	if len(g.Windows) != 2 || g.Windows[1].Name != "7 days · Opus" || g.Windows[1].Model != "opus" ||
		g.Windows[1].UsedPct != 12.5 || g.Windows[1].Length != 7*24*time.Hour {
		t.Fatalf("windows = %+v", g.Windows)
	}
	if !g.Windows[0].ResetsAt.Equal(fakeNow.Add(4 * time.Hour)) {
		t.Fatalf("reset = %v", g.Windows[0].ResetsAt)
	}
}

func TestSaveLastQuotaKeepsBothTools(t *testing.T) {
	dir := t.TempDir()
	for _, tool := range []Tool{ToolClaude, ToolCodex} {
		q := Quota{Tool: tool, Status: QuotaOK, FetchedAt: fakeNow,
			Windows: []Window{{Name: "5 hours", UsedPct: 10, Length: 5 * time.Hour}}}
		if err := SaveLastQuota(dir, q); err != nil {
			t.Fatal(err)
		}
	}
	if got := LoadLastQuotas(dir); len(got) != 2 {
		t.Fatalf("loaded %d readings, want 2", len(got))
	}
	// Codex fits no Claude reading, and vice versa.
	q := Quota{Tool: ToolCodex, Status: QuotaOK, FetchedAt: fakeNow,
		Windows: []Window{{Name: "7 days", UsedPct: 30, Length: 7 * 24 * time.Hour}}}
	if err := SaveLastQuota(dir, q); err != nil {
		t.Fatal(err)
	}
	got := LoadLastQuotas(dir)
	var claude, codex *Quota
	for i := range got {
		switch got[i].Tool {
		case ToolClaude:
			claude = &got[i]
		case ToolCodex:
			codex = &got[i]
		}
	}
	if claude == nil || codex == nil || codex.Windows[0].UsedPct != 30 {
		t.Fatalf("readings = %+v", got)
	}
}

func TestSaveLastQuotaRejectsFailures(t *testing.T) {
	dir := t.TempDir()
	for _, q := range []Quota{
		{Tool: ToolClaude, Status: QuotaFailed, FetchedAt: fakeNow},
		{Tool: ToolClaude, Status: QuotaExpired, FetchedAt: fakeNow},
		{Tool: ToolClaude, Status: QuotaOK, FetchedAt: fakeNow},
	} {
		if err := SaveLastQuota(dir, q); err != nil {
			t.Fatalf("save %+v: %v", q.Status, err)
		}
	}
	if got := LoadLastQuotas(dir); len(got) != 0 {
		t.Fatalf("failures were kept: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, LastQuotasFile)); !os.IsNotExist(err) {
		t.Fatalf("a rejected reading wrote %s", LastQuotasFile)
	}
}

func TestLoadLastQuotasTolerant(t *testing.T) {
	if got := LoadLastQuotas(""); got != nil {
		t.Fatalf("empty dir = %+v", got)
	}
	dir := t.TempDir()
	if got := LoadLastQuotas(dir); got != nil {
		t.Fatalf("missing file = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, LastQuotasFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadLastQuotas(dir); got != nil {
		t.Fatalf("corrupt file = %+v", got)
	}
}

func TestSaveLastQuotaLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	q := Quota{Tool: ToolClaude, Status: QuotaOK, FetchedAt: fakeNow,
		Windows: []Window{{Name: "5 hours", UsedPct: 3, Length: 5 * time.Hour}}}
	if err := SaveLastQuota(dir, q); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != LastQuotasFile {
			t.Fatalf("stray file %q", e.Name())
		}
	}
}
