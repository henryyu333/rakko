package aiusage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCodexResetVendorFieldsAndUUIDv4(t *testing.T) {
	uuidV4Re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	var gotRedeemID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wham/rate-limit-reset-credits":
			_, _ = w.Write([]byte(`{"available_count":1,"credits":[{"id":"cr-1","status":"available","expires_at":"2026-12-01T00:00:00Z"}]}`))
		case "/wham/rate-limit-reset-credits/consume":
			var body struct {
				CreditID        string `json:"credit_id"`
				RedeemRequestID string `json:"redeem_request_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotRedeemID = body.RedeemRequestID
			_, _ = w.Write([]byte(`{"code":"reset","windows_reset":2}`))
		}
	}))
	defer srv.Close()

	env := testEnv(t.TempDir())
	env.CodexBaseURL = srv.URL
	codexDir := filepath.Join(env.Home, ".codex")
	_ = os.MkdirAll(codexDir, 0o755)
	_ = os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"tokens":{"access_token":"tok","account_id":"acct-1"}}`), 0o600)

	out, err := UseCodexReset(context.Background(), env)
	if err != nil {
		t.Fatalf("UseCodexReset err: %v", err)
	}
	if !uuidV4Re.MatchString(gotRedeemID) {
		t.Fatalf("redeem_request_id = %q, want UUID v4", gotRedeemID)
	}
	if out.Code != "reset" || out.Windows != 2 || !out.PostSent {
		t.Fatalf("ResetOutcome = %+v, want Code=reset Windows=2 PostSent=true", out)
	}

	// Verify PostSent=true even when POST fails with HTTP 500.
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wham/rate-limit-reset-credits":
			_, _ = w.Write([]byte(`{"available_count":1,"credits":[{"id":"cr-1","status":"available","expires_at":"2026-12-01T00:00:00Z"}]}`))
		case "/wham/rate-limit-reset-credits/consume":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srvErr.Close()
	env.CodexBaseURL = srvErr.URL
	outErr, err := UseCodexReset(context.Background(), env)
	if err == nil || !outErr.PostSent {
		t.Fatalf("on 500 after POST: out=%+v err=%v, want err!=nil and PostSent=true", outErr, err)
	}
}

func TestGLMErrorCodesAndShortErr(t *testing.T) {
	for _, tc := range []struct {
		code int
		want QuotaStatus
	}{
		{1000, QuotaExpired},
		{1005, QuotaExpired},
		{1304, QuotaRateLimited},
		{1350, QuotaRateLimited},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":` + string(rune('0'+tc.code/1000)) + string(rune('0'+(tc.code/100)%10)) + string(rune('0'+(tc.code/10)%10)) + string(rune('0'+tc.code%10)) + `,"msg":"vendor internal"}`))
		}))
		env := testEnv(t.TempDir())
		env.GLMBaseURL = srv.URL
		claudeDir := filepath.Join(env.Home, ".claude")
		_ = os.MkdirAll(claudeDir, 0o755)
		_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"),
			[]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"glm-key","ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic"}}`), 0o600)
		q := FetchGLM(context.Background(), env)
		srv.Close()
		if q.Status != tc.want {
			t.Fatalf("GLM code %d: status=%v, want %v", tc.code, q.Status, tc.want)
		}
	}

	srvMsg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":1999,"success":false,"msg":"vendor internal secret message"}`))
	}))
	defer srvMsg.Close()
	env := testEnv(t.TempDir())
	env.GLMBaseURL = srvMsg.URL
	claudeDir := filepath.Join(env.Home, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"),
		[]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"glm-key","ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic"}}`), 0o600)
	q := FetchGLM(context.Background(), env)
	if q.Status != QuotaFailed || strings.Contains(q.Err, "vendor internal secret message") {
		t.Fatalf("GLM failed q=%+v, must not expose raw vendor msg in Err", q)
	}
}

func TestKimiMonthLimitsAndUnits(t *testing.T) {
	// 1. limit_month_total / limit_month_code
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usages":{"limit_month_total":{"usedRatio":0.42,"reset_time":"2026-12-01T00:00:00Z"},"limit_month_code":{"usedRatio":0.10}}}`))
	}))
	defer srv1.Close()
	env1 := testEnv(t.TempDir())
	env1.KimiBaseURL = srv1.URL
	claudeDir1 := filepath.Join(env1.Home, ".claude")
	_ = os.MkdirAll(claudeDir1, 0o755)
	_ = os.WriteFile(filepath.Join(claudeDir1, "settings.json"),
		[]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"kimi-key","ANTHROPIC_BASE_URL":"https://api.kimi.com/coding"}}`), 0o600)
	q1 := FetchKimi(context.Background(), env1)
	if q1.Status != QuotaOK || len(q1.Windows) == 0 || q1.Windows[0].Name != "Month" || q1.Windows[0].UsedPct != 42 {
		t.Fatalf("Kimi limit_month_total: q=%+v, want QuotaOK Month 42%%", q1)
	}

	// 1b. fallback to limit_month_code when limit_month_total is absent
	srv1b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usages":{"limit_month_code":{"usedRatio":0.15,"reset_time":"2026-12-01T00:00:00Z"}}}`))
	}))
	defer srv1b.Close()
	env1.KimiBaseURL = srv1b.URL
	q1b := FetchKimi(context.Background(), env1)
	if q1b.Status != QuotaOK || len(q1b.Windows) == 0 || q1b.Windows[0].Name != "Month" || q1b.Windows[0].UsedPct != 15 {
		t.Fatalf("Kimi limit_month_code fallback: q=%+v, want QuotaOK Month 15%%", q1b)
	}

	// 2. TIME_UNIT_WEEK / TIME_UNIT_MONTH in limits[]
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"limits":[{"window":{"duration":1,"timeUnit":"TIME_UNIT_WEEK"},"detail":{"limit":100,"used":25}},{"window":{"duration":1,"timeUnit":"TIME_UNIT_MONTH"},"detail":{"limit":100,"used":60}}]}`))
	}))
	defer srv2.Close()
	env2 := testEnv(t.TempDir())
	env2.KimiBaseURL = srv2.URL
	claudeDir2 := filepath.Join(env2.Home, ".claude")
	_ = os.MkdirAll(claudeDir2, 0o755)
	_ = os.WriteFile(filepath.Join(claudeDir2, "settings.json"),
		[]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"kimi-key","ANTHROPIC_BASE_URL":"https://api.kimi.com/coding"}}`), 0o600)
	q2 := FetchKimi(context.Background(), env2)
	if q2.Status != QuotaOK || len(q2.Windows) != 2 || q2.Windows[0].Length != 7*24*time.Hour || q2.Windows[1].Name != "Month" {
		t.Fatalf("Kimi TIME_UNIT_WEEK/MONTH: q=%+v, want 7d + Month windows", q2)
	}
}

func TestCopilotLegacyEventsWithoutDtDoesNotBaseline(t *testing.T) {
	day15 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.Local)
	model := "gpt-5.3-codex"
	old := &fileState{
		tool: ToolCopilot,
		// Missing dbTable (e.g. legacy cache from events table without dt)
		recs: []rec{{
			at: day15.UnixMilli(), model: &model, in: 1000, reqs: 1,
			key: makeCopilotRecKey("s1", ""),
		}},
	}
	snap, _ := copilotSessionsBaseline(old)
	if snap != nil {
		t.Fatalf("copilotSessionsBaseline on legacy cache without dt returned %v, want nil", snap)
	}
}

func TestKimiHostValidation(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://api.kimi.com/coding", true},
		{"https://sub.api.kimi.com/v1", true},
		{"https://api.kimi.ai/coding", true},
		{"https://cn.api.kimi.ai", true},
		{"https://evilkimi.com/coding", false},
		{"https://kimi.com.evil.com/coding", false},
		{"https://kimi.com/coding", false},
		{"file:///etc/passwd", false},
	} {
		env := testEnv(t.TempDir())
		claudeDir := filepath.Join(env.Home, ".claude")
		_ = os.MkdirAll(claudeDir, 0o755)
		_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"),
			[]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"tok","ANTHROPIC_BASE_URL":"`+tc.url+`"}}`), 0o600)
		_, _, ok := readKimiKey(env)
		if ok != tc.want {
			t.Fatalf("readKimiKey(%q) = %v, want %v", tc.url, ok, tc.want)
		}
	}
}
