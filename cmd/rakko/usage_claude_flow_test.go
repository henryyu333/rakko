//go:build darwin

package main

import (
	"context"
	"errors"
	"testing"

	"rakko/internal/aiusage"
)

// Logging out runs the injected process, then reads Claude again so the
// card says whether the Mac's own sign-in remains.
func TestUsageClaudeLogoutRunsAndReads(t *testing.T) {
	var calls int
	fx := newUsageFixtureWith(t, true, func(d *usageDeps) {
		d.logoutClaude = func(ctx context.Context) error {
			calls++
			return nil
		}
	})
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.f.startClaudeLogout()
	if calls != 1 {
		t.Fatalf("logout calls = %d, want 1", calls)
	}
	if fx.f.claudeLogoutBusy || fx.f.claudeLogoutErr {
		t.Fatalf("logout state busy=%v err=%v, want finished", fx.f.claudeLogoutBusy, fx.f.claudeLogoutErr)
	}
	if fx.claude.calls != 1 {
		t.Fatalf("claude fetches = %d after logout, want 1", fx.claude.calls)
	}

	fx.f.claudeLogoutBusy = false
	fx.f.deps.logoutClaude = func(ctx context.Context) error { return errors.New("denied") }
	fx.f.startClaudeLogout()
	if !fx.f.claudeLogoutErr {
		t.Fatal("a failed logout must stay visible")
	}
}

// A completed sign-in asks for a fresh Claude reading at once.
func TestUsageClaudeSignInTriggersRead(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	codes := make(chan string, 1)
	fx.f.claudeLogin = usageClaudeLogin{busy: true, codes: codes}
	fx.f.gotClaudeLogin(nil, codes)
	if fx.claude.calls != 1 {
		t.Fatalf("claude fetches = %d after a successful sign-in, want 1", fx.claude.calls)
	}
	shown, _ := fx.f.quotaShown(aiusage.ToolClaude)
	if shown.Status != aiusage.QuotaOK || shown.Plan != "Pro" {
		t.Fatalf("shown = %+v, want the fresh reading", shown)
	}
}

// Cancelling a waiting sign-in stops its context and forgets the state,
// so no failure is reported.
func TestUsageClaudeSignInCancel(t *testing.T) {
	fx := newUsageFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	fx.f.claudeLogin = usageClaudeLogin{busy: true, codes: make(chan string, 1), cancel: cancel}
	fx.f.cancelClaudeLogin()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("cancel did not stop the sign-in")
	}
	if fx.f.claudeLogin.busy || fx.f.claudeLogin.codes != nil {
		t.Fatalf("state = %+v, want the session stopped", fx.f.claudeLogin)
	}
	if !fx.f.claudeLogin.cancelled || fx.f.claudeLogin.failed {
		t.Fatalf("state = %+v, want a visible cancellation, not a failure", fx.f.claudeLogin)
	}
}
