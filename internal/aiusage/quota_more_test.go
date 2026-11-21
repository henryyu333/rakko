package aiusage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func testEnv(home string) Env {
	return Env{
		Home: home,
		HTTP: &http.Client{Timeout: 5 * time.Second},
		Now:  func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) },
	}
}

func TestFetchGLMStatuses(t *testing.T) {
	var mode string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch mode {
		case "ok":
			_, _ = w.Write([]byte(`{"success":true,"data":{"level":"pro","limits":[
				{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":12,"nextResetTime":1795096800000},
				{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":40,"nextResetTime":1795200000000},
				{"type":"TIME_LIMIT","unit":5,"number":1,"percentage":3,"nextResetTime":1795200000000}
			]}}`))
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
		case "429":
			w.WriteHeader(http.StatusTooManyRequests)
		case "limit":
			_, _ = w.Write([]byte(`{"success":false,"code":1113,"msg":"insufficient balance"}`))
		}
	}))
	defer srv.Close()

	// Not logged in
	env := testEnv(t.TempDir())
	env.GLMBaseURL = srv.URL
	if q := FetchGLM(context.Background(), env); q.Status != QuotaNotLoggedIn {
		t.Fatalf("no key status = %v, want QuotaNotLoggedIn", q.Status)
	}

	// From ~/.claude/settings.json
	claudeDir := filepath.Join(env.Home, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"),
		[]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"glm-key-1","ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic"}}`), 0o600)

	mode = "ok"
	q := FetchGLM(context.Background(), env)
	if q.Status != QuotaOK || q.Plan != "Pro" || len(q.Windows) != 2 {
		t.Fatalf("GLM ok = %+v", q)
	}
	if gotAuth != "glm-key-1" {
		t.Fatalf("GLM Authorization = %q, want bare key without Bearer", gotAuth)
	}
	if q.Windows[0].UsedPct != 12 || q.Windows[1].UsedPct != 40 {
		t.Fatalf("GLM windows = %+v", q.Windows)
	}

	mode = "401"
	if q := FetchGLM(context.Background(), env); q.Status != QuotaExpired {
		t.Fatalf("GLM 401 status = %v, want QuotaExpired", q.Status)
	}
	mode = "429"
	if q := FetchGLM(context.Background(), env); q.Status != QuotaRateLimited {
		t.Fatalf("GLM 429 status = %v, want QuotaRateLimited", q.Status)
	}
	mode = "limit"
	if q := FetchGLM(context.Background(), env); q.Status != QuotaLimitReached {
		t.Fatalf("GLM limit status = %v, want QuotaLimitReached", q.Status)
	}
}

// A GLM window without a percentage is unknown data, not 0% used: the
// card must not wear a healthy ring the vendor never sent. An answer
// where no window can be understood is a failure, so the card says the
// reading did not come back instead of freezing the last good one.
func TestFetchGLMUnknownWindowsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"level":"pro","limits":[` +
			`{"type":"TOKENS_LIMIT","unit":3,"number":5,"nextResetTime":1795096800000}]}}`))
	}))
	defer srv.Close()
	env := testEnv(t.TempDir())
	env.GLMBaseURL = srv.URL
	claudeDir := filepath.Join(env.Home, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"),
		[]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"glm-key-1","ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic"}}`), 0o600)

	q := FetchGLM(context.Background(), env)
	if q.Status != QuotaFailed || q.Err != "no usage windows" || len(q.Windows) != 0 {
		t.Fatalf("missing percentage = %+v; want QuotaFailed with no usage windows", q)
	}
}

// A numeric timestamp keeps its year whichever magnitude the vendor sent
// it in: an inflated far-future reset time would fail to save, and the
// reading would be dropped.
func TestParseFlexibleTimeHandlesEveryMagnitude(t *testing.T) {
	cases := []struct {
		name string
		v    float64
	}{
		{"seconds", 1.739e9},
		{"milliseconds", 1.739e12},
		{"microseconds", 1.739e15},
		{"nanoseconds", 1.739e18},
	}
	for _, c := range cases {
		got := parseFlexibleTime(c.v)
		if got.Year() < 2020 || got.Year() > 2100 {
			t.Errorf("%s: parseFlexibleTime(%v) = %s (year %d)", c.name, c.v, got, got.Year())
		}
		w := Window{Name: "5 hours", ResetsAt: got}
		if _, err := json.Marshal(w); err != nil {
			t.Errorf("%s: marshal fails: %v", c.name, err)
		}
	}
}

func TestFetchKimiStatuses(t *testing.T) {
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "ok":
			_, _ = w.Write([]byte(`{
				"usage":{"limit":"100","used":"25","resetTime":"2026-11-26T00:00:00Z"},
				"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"100","remaining":"88","resetTime":"2026-11-19T17:00:00Z"}}]
			}`))
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
		case "429":
			w.WriteHeader(http.StatusTooManyRequests)
		case "402":
			w.WriteHeader(http.StatusPaymentRequired)
		}
	}))
	defer srv.Close()

	env := testEnv(t.TempDir())
	env.KimiBaseURL = srv.URL
	if q := FetchKimi(context.Background(), env); q.Status != QuotaNotLoggedIn {
		t.Fatalf("no key status = %v, want QuotaNotLoggedIn", q.Status)
	}

	env.Secrets = func(tool Tool) string {
		if tool == ToolKimi {
			return "kimi-secret"
		}
		return ""
	}
	mode = "ok"
	q := FetchKimi(context.Background(), env)
	if q.Status != QuotaOK || len(q.Windows) != 2 {
		t.Fatalf("Kimi ok = %+v", q)
	}
	if q.Windows[0].UsedPct != 12 || q.Windows[1].UsedPct != 25 {
		t.Fatalf("Kimi windows = %+v", q.Windows)
	}
	mode = "401"
	if q := FetchKimi(context.Background(), env); q.Status != QuotaExpired {
		t.Fatalf("Kimi 401 = %v", q.Status)
	}
	mode = "429"
	if q := FetchKimi(context.Background(), env); q.Status != QuotaRateLimited {
		t.Fatalf("Kimi 429 = %v", q.Status)
	}
	mode = "402"
	if q := FetchKimi(context.Background(), env); q.Status != QuotaLimitReached {
		t.Fatalf("Kimi 402 = %v", q.Status)
	}
}

func TestFetchOpenCodeGoAndCommandCodeStatuses(t *testing.T) {
	var ocMode, ccMode string
	ocSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch ocMode {
		case "ok":
			_, _ = w.Write([]byte(`{"usage":{"rolling":{"status":"ok","percent":37,"resetsAt":"2026-11-19T16:00:00Z"},"weekly":{"status":"ok","percent":52,"resetsAt":"2026-11-25T00:00:00Z"}}}`))
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
		case "429":
			w.WriteHeader(http.StatusTooManyRequests)
		case "402":
			w.WriteHeader(http.StatusPaymentRequired)
		}
	}))
	defer ocSrv.Close()

	ccSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch ccMode {
		case "ok":
			_, _ = w.Write([]byte(`{"credits":{"planId":"individual-pro"},"windowLimits":{"fiveHour":{"used":3,"cap":10,"resetAt":1795096800000},"weekly":{"used":12,"cap":40,"resetAt":1795200000000}}}`))
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
		case "429":
			w.WriteHeader(http.StatusTooManyRequests)
		case "402":
			w.WriteHeader(http.StatusPaymentRequired)
		}
	}))
	defer ccSrv.Close()

	env := testEnv(t.TempDir())
	env.OpenCodeGoURL = ocSrv.URL
	env.CommandCodeBaseURL = ccSrv.URL
	if q := FetchOpenCodeGo(context.Background(), env); q.Status != QuotaNotLoggedIn {
		t.Fatalf("opencode no key = %v", q.Status)
	}
	if q := FetchCommandCode(context.Background(), env); q.Status != QuotaNotLoggedIn {
		t.Fatalf("commandcode no key = %v", q.Status)
	}

	// Write local auth files
	ocDir := filepath.Join(env.Home, ".local", "share", "opencode")
	_ = os.MkdirAll(ocDir, 0o755)
	_ = os.WriteFile(filepath.Join(ocDir, "auth.json"), []byte(`{"opencode-go":{"type":"api","key":"oc-key"}}`), 0o600)

	ccDir := filepath.Join(env.Home, ".commandcode")
	_ = os.MkdirAll(ccDir, 0o755)
	_ = os.WriteFile(filepath.Join(ccDir, "auth.json"), []byte(`{"apiKey":"cc-key"}`), 0o600)

	ocMode, ccMode = "ok", "ok"
	oq := FetchOpenCodeGo(context.Background(), env)
	if oq.Status != QuotaOK || len(oq.Windows) != 2 || oq.Windows[0].UsedPct != 37 || oq.Windows[1].UsedPct != 52 {
		t.Fatalf("opencode ok = %+v", oq)
	}
	cq := FetchCommandCode(context.Background(), env)
	if cq.Status != QuotaOK || cq.Plan != "Pro" || len(cq.Windows) != 2 || cq.Windows[0].UsedPct != 30 || cq.Windows[1].UsedPct != 30 {
		t.Fatalf("commandcode ok = %+v", cq)
	}

	for _, tc := range []struct {
		mode string
		want QuotaStatus
	}{{"401", QuotaExpired}, {"429", QuotaRateLimited}, {"402", QuotaLimitReached}} {
		ocMode, ccMode = tc.mode, tc.mode
		if got := FetchOpenCodeGo(context.Background(), env).Status; got != tc.want {
			t.Fatalf("opencode %s = %v, want %v", tc.mode, got, tc.want)
		}
		if got := FetchCommandCode(context.Background(), env).Status; got != tc.want {
			t.Fatalf("commandcode %s = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestFetchDeepSeekBalanceAndLastQuotas(t *testing.T) {
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "ok":
			_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[
				{"currency":"USD","total_balance":"5.00","granted_balance":"0.00","topped_up_balance":"5.00"},
				{"currency":"CNY","total_balance":"86.40","granted_balance":"6.40","topped_up_balance":"80.00"}
			]}`))
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
		case "429":
			w.WriteHeader(http.StatusTooManyRequests)
		case "402":
			w.WriteHeader(http.StatusPaymentRequired)
		}
	}))
	defer srv.Close()

	env := testEnv(t.TempDir())
	env.DeepSeekBaseURL = srv.URL
	if q := FetchDeepSeek(context.Background(), env); q.Status != QuotaNotLoggedIn {
		t.Fatalf("deepseek no key = %v", q.Status)
	}
	env.Secrets = func(tool Tool) string {
		if tool == ToolDeepSeek {
			return "ds-key"
		}
		return ""
	}
	mode = "ok"
	q := FetchDeepSeek(context.Background(), env)
	if q.Status != QuotaOK || q.Balance == nil {
		t.Fatalf("deepseek ok = %+v", q)
	}
	if q.Balance.Text != "¥ 86.40" || q.Balance.TopUp != "80" || q.Balance.Granted != "6.40" {
		t.Fatalf("deepseek balance = %+v", q.Balance)
	}

	// LastQuotas round-trip preserves balance card
	dataDir := t.TempDir()
	if err := SaveLastQuota(dataDir, q); err != nil {
		t.Fatalf("SaveLastQuota: %v", err)
	}
	loaded := LoadLastQuotas(dataDir)
	if len(loaded) != 1 || loaded[0].Tool != ToolDeepSeek || loaded[0].Balance == nil || loaded[0].Balance.Text != "¥ 86.40" {
		t.Fatalf("LoadLastQuotas = %+v", loaded)
	}

	mode = "401"
	if got := FetchDeepSeek(context.Background(), env).Status; got != QuotaExpired {
		t.Fatalf("deepseek 401 = %v", got)
	}
	mode = "429"
	if got := FetchDeepSeek(context.Background(), env).Status; got != QuotaRateLimited {
		t.Fatalf("deepseek 429 = %v", got)
	}
	mode = "402"
	if got := FetchDeepSeek(context.Background(), env).Status; got != QuotaLimitReached {
		t.Fatalf("deepseek 402 = %v", got)
	}
}

func TestFetchCursor(t *testing.T) {
	var curMode string
	curSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch curMode {
		case "ok":
			_, _ = w.Write([]byte(`{"enabled":true,"billingCycleEnd":"1795200000000","planUsage":{"totalPercentUsed":42.5}}`))
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
		case "429":
			w.WriteHeader(http.StatusTooManyRequests)
		case "402":
			w.WriteHeader(http.StatusPaymentRequired)
		}
	}))
	defer curSrv.Close()

	env := testEnv(t.TempDir())
	env.CursorBaseURL = curSrv.URL

	if q := FetchCursor(context.Background(), env); q.Status != QuotaNotLoggedIn {
		t.Fatalf("cursor no creds = %v", q.Status)
	}

	curDir := filepath.Join(env.Home, ".cursor")
	_ = os.MkdirAll(curDir, 0o755)
	_ = os.WriteFile(filepath.Join(curDir, "auth.json"), []byte(`{"accessToken":"cur-tok"}`), 0o600)

	curMode = "ok"
	cq := FetchCursor(context.Background(), env)
	if cq.Status != QuotaOK || len(cq.Windows) != 1 || cq.Windows[0].UsedPct != 42.5 {
		t.Fatalf("cursor ok = %+v", cq)
	}

	for _, tc := range []struct {
		mode string
		want QuotaStatus
	}{{"401", QuotaExpired}, {"429", QuotaRateLimited}, {"402", QuotaLimitReached}} {
		curMode = tc.mode
		if got := FetchCursor(context.Background(), env).Status; got != tc.want {
			t.Fatalf("cursor %s = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestCodexResetCreditsReadAndConsume(t *testing.T) {
	var hasCredits bool
	var consumeCalls atomic.Int32
	var gotCreditID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wham/usage":
			_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":83,"limit_window_seconds":18000,"reset_at":1795096800},"secondary_window":{"used_percent":68,"limit_window_seconds":604800,"reset_at":1795200000}}}`))
		case "/wham/rate-limit-reset-credits":
			if !hasCredits {
				_, _ = w.Write([]byte(`{"available_count":0,"credits":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"available_count":2,"credits":[
				{"id":"cr-later","status":"available","expires_at":"2026-12-15T00:00:00Z"},
				{"id":"cr-soon","status":"available","expires_at":"2026-11-30T00:00:00Z"}
			]}`))
		case "/wham/rate-limit-reset-credits/consume":
			consumeCalls.Add(1)
			var body struct {
				CreditID string `json:"credit_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotCreditID = body.CreditID
			_, _ = w.Write([]byte(`{"code":"reset","windows_reset":2}`))
		}
	}))
	defer srv.Close()

	env := testEnv(t.TempDir())
	env.CodexBaseURL = srv.URL
	codexDir := filepath.Join(env.Home, ".codex")
	_ = os.MkdirAll(codexDir, 0o755)
	_ = os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"tokens":{"access_token":"tok","account_id":"acct-1"}}`), 0o600)

	// 1. No credits: no consume POST sent
	hasCredits = false
	out, err := UseCodexReset(context.Background(), env)
	if err != nil || out.Code != "no_credit" {
		t.Fatalf("UseCodexReset empty = %+v, err=%v", out, err)
	}
	if consumeCalls.Load() != 0 {
		t.Fatalf("consumeCalls = %d, want 0 when no credits", consumeCalls.Load())
	}

	// 2. With credits: FetchCodexResetCredits picks count=2 and soonest date, UseCodexReset spends soonest
	hasCredits = true
	q := FetchCodex(context.Background(), env)
	rc, err := FetchCodexResetCredits(context.Background(), env)
	if q.Status != QuotaOK || err != nil || rc == nil || rc.Count != 2 || rc.FirstID != "cr-soon" {
		t.Fatalf("FetchCodexResetCredits = %+v, err=%v", rc, err)
	}
	out, err = UseCodexReset(context.Background(), env)
	if err != nil || out.Code != "reset" || out.Windows != 2 {
		t.Fatalf("UseCodexReset = %+v, err=%v", out, err)
	}
	if consumeCalls.Load() != 1 || gotCreditID != "cr-soon" {
		t.Fatalf("consumeCalls=%d gotCreditID=%q, want 1 / cr-soon", consumeCalls.Load(), gotCreditID)
	}
}
