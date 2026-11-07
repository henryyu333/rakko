//go:build darwin

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/aiusage"
)

// signedOutClaude puts an answered "not signed in" Claude reading on the
// page without a fetch, for drawing the card's states.
func signedOutClaude(fx *usageFixture, source aiusage.ClaudeSource) {
	fx.a.tab, fx.a.level = pageUsage, 2
	fx.f.quota[quotaIndex(aiusage.ToolClaude)].latest = aiusage.Quota{
		Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn,
		FetchedAt: *fx.now, ClaudeSource: source,
	}
}

// The signed-out Claude card offers Rakko's own sign-in and never tells the
// user to run a command.
func TestUsageClaudeSignedOutOffersSignIn(t *testing.T) {
	fx := newUsageFixture(t, true)
	signedOutClaude(fx, aiusage.ClaudeSourceUser)
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

// Signing in runs the injected process, opens the printed link in the
// browser and reads Claude again once the process ends.
func TestUsageClaudeSignInOpensLinkAndReads(t *testing.T) {
	var opened string
	loginURL := "https://claude.com/cai/oauth/authorize?code=true&state=test"
	fx := newUsageFixtureWith(t, true, func(d *usageDeps) {
		d.openURL = func(u string) { opened = u }
		d.loginClaude = func(ctx context.Context, onURL func(string), code <-chan string) error {
			onURL(loginURL)
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
	if opened != loginURL {
		t.Fatalf("opened %q, want the printed link %q", opened, loginURL)
	}
	if fx.claude.calls != 2 {
		t.Fatalf("claude fetches = %d, want the sign-in's own read", fx.claude.calls)
	}
	fx.view.Frame()
	if !fx.view.HasText("Pro") {
		t.Fatalf("the card did not show the fresh plan; texts=%v", fx.view.Texts())
	}
}

// A pasted code goes to the waiting process, and the card says the
// exchange is running.
func TestUsageClaudeSignInSubmitsCode(t *testing.T) {
	fx := newUsageFixture(t, true)
	signedOutClaude(fx, aiusage.ClaudeSourceUser)
	codes := make(chan string, 1)
	fx.f.claudeLogin = usageClaudeLogin{busy: true, code: " CODE-9 ", codes: codes}
	fx.view.Frame()
	if !fx.view.HasText("粘贴授权码") {
		t.Fatalf("missing the code box; texts=%v", fx.view.Texts())
	}
	fx.f.submitClaudeCode()
	select {
	case got := <-codes:
		if got != "CODE-9" {
			t.Fatalf("code = %q, want CODE-9", got)
		}
	default:
		t.Fatal("the code never reached the process")
	}
	if !fx.f.claudeLogin.submitted || fx.f.claudeLogin.code != "" {
		t.Fatalf("state = %+v, want submitted and cleared", fx.f.claudeLogin)
	}
	fx.view.Frame()
	if !fx.view.HasText("正在完成登录…") {
		t.Fatalf("missing the completing message; texts=%v", fx.view.Texts())
	}
}

// Cancelling a sign-in leaves a visible result, not a silent return.
func TestUsageClaudeSignInCancelledVisible(t *testing.T) {
	fx := newUsageFixture(t, true)
	signedOutClaude(fx, aiusage.ClaudeSourceUser)
	fx.f.claudeLogin = usageClaudeLogin{cancelled: true}
	fx.view.Frame()
	if !fx.view.HasText("已取消登录") || !fx.view.HasText("登录") {
		t.Fatalf("missing the cancellation result or retry; texts=%v", fx.view.Texts())
	}
}

// A failed sign-in stays visible on the card and offers another try.
func TestUsageClaudeSignInFailureStaysVisible(t *testing.T) {
	fx := newUsageFixture(t, true)
	signedOutClaude(fx, aiusage.ClaudeSourceUser)
	codes := make(chan string, 1)
	fx.f.claudeLogin = usageClaudeLogin{busy: true, codes: codes}
	fx.f.gotClaudeLogin(errors.New("process exited 1"), codes)
	if !fx.f.claudeLogin.failed {
		t.Fatalf("state = %+v, want failed", fx.f.claudeLogin)
	}
	fx.view.Frame()
	if !fx.view.HasText("登录未完成") || !fx.view.HasText("登录") {
		t.Fatalf("missing the failure result or retry; texts=%v", fx.view.Texts())
	}
}

// settingsAccountShelf is the AI 用量 settings page with a working usage
// feature and no real agent home.
func settingsAccountShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	stubSettingsSeams(t, false, nil, nil)
	stubKeyboard(t)
	stubPermissions(t)
	a, _, _ := agentShelf(t)
	a.settings.AIUsage.Enabled = true
	attachGalleryUsage(a, time.Now())
	a.settingsPage = settingsPageAIUsage
	view := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	view.Frame()
	view.Frame()
	return a, view
}

// The settings row names the source the card reads from and offers the
// matching action.
func TestSettingsClaudeAccountRow(t *testing.T) {
	cases := []struct {
		name       string
		q          aiusage.Quota
		busy       bool
		submitted  bool
		failed     bool
		logoutBusy bool
		cancelled  bool
		wantSource string
		wantAction string
	}{
		{name: "rakko", q: okQuota(aiusage.ToolClaude, "Pro", time.Now()),
			wantSource: "Rakko 登录", wantAction: "退出"},
		{name: "user", q: func() aiusage.Quota {
			q := okQuota(aiusage.ToolClaude, "Pro", time.Now())
			q.ClaudeSource = aiusage.ClaudeSourceUser
			return q
		}(), wantSource: "使用本机 Claude Code", wantAction: "登录"},
		{name: "signed-out", q: aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn,
			FetchedAt: time.Now(), ClaudeSource: aiusage.ClaudeSourceUser},
			wantSource: "未登录", wantAction: "登录"},
		{name: "waiting", busy: true, wantSource: "在浏览器中完成登录…", wantAction: "取消"},
		{name: "failed", failed: true, wantSource: "登录未完成", wantAction: "登录"},
		{name: "cancelled", cancelled: true, wantSource: "已取消登录", wantAction: "登录"},
		{name: "signing-out", q: okQuota(aiusage.ToolClaude, "Pro", time.Now()),
			logoutBusy: true, wantSource: "正在退出…", wantAction: "正在退出…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, view := settingsAccountShelf(t)
			q := tc.q
			if tc.name == "rakko" {
				q.ClaudeSource = aiusage.ClaudeSourceRakko
			}
			a.usage.quota[quotaIndex(aiusage.ToolClaude)].latest = q
			a.usage.claudeLogin = usageClaudeLogin{
				busy: tc.busy, submitted: tc.submitted, failed: tc.failed,
				cancelled: tc.cancelled, codes: make(chan string, 1),
			}
			a.usage.claudeLogoutBusy = tc.logoutBusy
			view.Frame()
			if !view.HasText(tc.wantSource) {
				t.Fatalf("missing source %q; texts=%v", tc.wantSource, view.Texts())
			}
			if !view.HasText(tc.wantAction) {
				t.Fatalf("missing action %q; texts=%v", tc.wantAction, view.Texts())
			}
		})
	}
}

// The account row is part of the AI 用量 page and stays hidden elsewhere.
func TestSettingsClaudeAccountRowOnAIUsagePageOnly(t *testing.T) {
	a, view := settingsAccountShelf(t)
	if !view.HasText("Claude 登录") {
		t.Fatalf("missing the account group on AI 用量; texts=%v", view.Texts())
	}
	a.settingsPage = settingsPageGeneral
	view.Frame()
	if view.HasText("Claude 登录") {
		t.Fatal("the account group leaked onto another page")
	}
}
