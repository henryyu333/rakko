//go:build darwin

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"rakko/internal/aiusage"
)

// signedOutClaude puts an answered "not signed in" Claude reading on the
// page without a fetch, for drawing the card's states.
func signedOutClaude(fx *usageFixture) {
	fx.a.tab, fx.a.level = pageUsage, 2
	fx.f.quota[quotaIndex(aiusage.ToolClaude)].latest = aiusage.Quota{
		Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn, FetchedAt: *fx.now,
	}
}

// The signed-out Claude card offers Rakko's own sign-in and never tells the
// user to run a command.
func TestUsageClaudeSignedOutOffersSignIn(t *testing.T) {
	fx := newUsageFixture(t, true)
	signedOutClaude(fx)
	fx.view.Frame()
	if !fx.view.HasText("登录") {
		t.Fatalf("missing the login button; texts=%v", fx.view.Texts())
	}
	for _, text := range fx.view.Texts() {
		if text == "运行 claude login 后显示" || text == "Run claude login" {
			t.Fatalf("stale instruction still drawn: %q", text)
		}
	}
}

// One click starts the process; Claude Code drives the browser and its own
// callback, and the card never asks for a code or a second link.
func TestUsageClaudeSignInIsOneClick(t *testing.T) {
	var opened int
	fx := newUsageFixtureWith(t, true, func(d *usageDeps) {
		d.loginClaude = func(ctx context.Context) error {
			opened++
			return nil
		}
	})
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.qs = []aiusage.Quota{
		{Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn, FetchedAt: *fx.now},
		okQuota(aiusage.ToolClaude, "Pro", *fx.now),
	}
	if err := fx.view.Click("AI 用量"); err != nil {
		t.Fatalf("open usage: %v", err)
	}
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if !fx.view.HasText("登录") {
		t.Fatalf("missing the login button; texts=%v", fx.view.Texts())
	}

	*fx.now = fx.now.Add(usageClaudeFloor + time.Second)
	if err := fx.view.Click("登录"); err != nil {
		t.Fatalf("click login: %v", err)
	}
	if opened != 1 {
		t.Fatalf("login runs = %d, want 1", opened)
	}
	if fx.claude.calls != 2 {
		t.Fatalf("claude fetches = %d, want the sign-in's own read", fx.claude.calls)
	}
	fx.view.Frame()
	if !fx.view.HasText("Pro") {
		t.Fatalf("the card did not show the fresh plan; texts=%v", fx.view.Texts())
	}
	for _, text := range []string{"粘贴授权码", "打开登录页", "正在完成登录…"} {
		if fx.view.HasText(text) {
			t.Fatalf("%q must not exist in the one-click flow", text)
		}
	}
}

// While the browser flow waits the card shows the message and 取消 only.
func TestUsageClaudeSignInWaiting(t *testing.T) {
	fx := newUsageFixture(t, true)
	signedOutClaude(fx)
	fx.f.claudeLogin = usageClaudeLogin{busy: true, cancel: func() {}}
	fx.view.Frame()
	if !fx.view.HasText("在浏览器中完成登录…") || !fx.view.HasText("取消") {
		t.Fatalf("missing the waiting state; texts=%v", fx.view.Texts())
	}
	for _, text := range []string{"粘贴授权码", "打开登录页"} {
		if fx.view.HasText(text) {
			t.Fatalf("%q must not be drawn while waiting", text)
		}
	}
}

// A failed sign-in stays visible on the card and offers another try.
func TestUsageClaudeSignInFailureStaysVisible(t *testing.T) {
	fx := newUsageFixture(t, true)
	signedOutClaude(fx)
	fx.f.claudeLogin = usageClaudeLogin{busy: true, cancel: func() {}}
	fx.f.gotClaudeLogin(errors.New("process exited 1"), fx.f.claudeLoginGen)
	if !fx.f.claudeLogin.failed {
		t.Fatalf("state = %+v, want failed", fx.f.claudeLogin)
	}
	fx.view.Frame()
	if !fx.view.HasText("登录未完成") || !fx.view.HasText("登录") {
		t.Fatalf("missing the failure result or retry; texts=%v", fx.view.Texts())
	}
}

// Cancelling a sign-in leaves a visible result, not a silent return.
func TestUsageClaudeSignInCancelledVisible(t *testing.T) {
	fx := newUsageFixture(t, true)
	signedOutClaude(fx)
	fx.f.claudeLogin = usageClaudeLogin{cancelled: true}
	fx.view.Frame()
	if !fx.view.HasText("已取消登录") || !fx.view.HasText("登录") {
		t.Fatalf("missing the cancellation result or retry; texts=%v", fx.view.Texts())
	}
}
