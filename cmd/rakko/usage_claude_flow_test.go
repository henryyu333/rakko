//go:build darwin

package main

import (
	"context"
	"errors"
	"testing"

	"rakko/internal/aiusage"
)

// A completed sign-in asks for a fresh Claude reading at once and stops
// showing the old "not signed in" answer.
func TestUsageClaudeSignInTriggersRead(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.f.quota[quotaIndex(aiusage.ToolClaude)].latest = aiusage.Quota{
		Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn, FetchedAt: *fx.now,
	}
	fx.f.claudeLogin = usageClaudeLogin{busy: true, cancel: func() {}}
	fx.f.gotClaudeLogin(nil, fx.f.claudeLoginGen)
	if fx.claude.calls != 1 {
		t.Fatalf("claude fetches = %d after a successful sign-in, want 1", fx.claude.calls)
	}
	shown, _ := fx.f.quotaShown(aiusage.ToolClaude)
	if shown.Status != aiusage.QuotaOK || shown.Plan != "Pro" {
		t.Fatalf("shown = %+v, want the fresh reading", shown)
	}
}

// Cancelling a waiting sign-in stops its context, retires its late answer
// and leaves the cancellation on the card.
func TestUsageClaudeSignInCancel(t *testing.T) {
	fx := newUsageFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	gen := fx.f.claudeLoginGen
	fx.f.claudeLogin = usageClaudeLogin{busy: true, cancel: cancel}
	fx.f.cancelClaudeLogin()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("cancel did not stop the sign-in")
	}
	if fx.f.claudeLogin.busy || fx.f.claudeLoginGen == gen {
		t.Fatalf("state = %+v gen=%d, want a retired cancelled session", fx.f.claudeLogin, fx.f.claudeLoginGen)
	}
	if !fx.f.claudeLogin.cancelled || fx.f.claudeLogin.failed {
		t.Fatalf("state = %+v, want a visible cancellation, not a failure", fx.f.claudeLogin)
	}
	// The cancelled process's late answer must not replace the result.
	fx.f.gotClaudeLogin(errors.New("late"), gen)
	if !fx.f.claudeLogin.cancelled || fx.f.claudeLogin.failed {
		t.Fatalf("a retired run changed the card: %+v", fx.f.claudeLogin)
	}
}

// An older sign-in that finishes after a newer one started is ignored.
func TestUsageClaudeSignInStaleRunIgnored(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.f.claudeLoginGen = 2
	fx.f.claudeLogin = usageClaudeLogin{busy: true, cancel: func() {}}
	fx.f.gotClaudeLogin(errors.New("stale"), 1)
	if !fx.f.claudeLogin.busy || fx.f.claudeLogin.failed {
		t.Fatalf("a stale run changed the card: %+v", fx.f.claudeLogin)
	}
}
