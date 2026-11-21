package aiusage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var fakeNow = time.Date(2027, 1, 10, 12, 0, 0, 0, time.UTC)

func makeJWT(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(fmt.Sprintf(`{"exp":%d,"sub":"test"}`, exp)))
	return header + "." + payload + ".fakesig"
}

// writeCodexAuth writes an auth.json into a fresh CODEX_HOME.
func writeCodexAuth(t *testing.T, accessToken, accountID string) {
	t.Helper()
	dir := t.TempDir()
	auth := `{"tokens":{"access_token":` + jsonString(accessToken) +
		`,"account_id":` + jsonString(accountID) +
		`,"id_token":"x","refresh_token":"x"},"auth_mode":"chatgpt"}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", dir)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// codexServer counts requests and replies with the given status/body.
func codexServer(t *testing.T, status int, body string, headers map[string]string) (*httptest.Server, *int) {
	t.Helper()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func codexEnv(srv *httptest.Server) Env {
	return Env{
		HTTP:         srv.Client(),
		CodexBaseURL: srv.URL,
		Now:          func() time.Time { return fakeNow },
	}
}

// quotaWindow finds one named window.
func quotaWindow(q Quota, name string) *Window {
	for i := range q.Windows {
		if q.Windows[i].Name == name {
			return &q.Windows[i]
		}
	}
	return nil
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "quota", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFetchCodexOK(t *testing.T) {
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, hits := codexServer(t, 200, fixture(t, "codex_usage.json"), nil)
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q", q.Status, q.Err)
	}
	if q.Tool != ToolCodex {
		t.Fatalf("tool = %v", q.Tool)
	}
	if q.Plan != "Plus" {
		t.Fatalf("plan = %q", q.Plan)
	}
	five, week := quotaWindow(q, "5 hours"), quotaWindow(q, "7 days")
	if five == nil || week == nil {
		t.Fatalf("both windows should be set, got %+v", q.Windows)
	}
	if five.UsedPct != 37 || five.Length != 5*time.Hour {
		t.Fatalf("five-hour window = %+v", five)
	}
	if week.UsedPct != 12 || week.Length != 7*24*time.Hour {
		t.Fatalf("weekly window = %+v", week)
	}
	if five.ResetsAt != time.Unix(1800000000, 0) {
		t.Fatalf("five-hour resets at %v", five.ResetsAt)
	}
	if *hits != 1 {
		t.Fatalf("server hits = %d", *hits)
	}
}

func TestFetchCodexHeaders(t *testing.T) {
	token := makeJWT(fakeNow.Add(time.Hour).Unix())
	writeCodexAuth(t, token, "acct-42")
	var gotAuth, gotAcct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAcct = r.Header.Get("chatgpt-account-id")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, fixture(t, "codex_usage.json"))
	}))
	defer srv.Close()
	env := codexEnv(srv)
	env.Home = t.TempDir()

	FetchCodex(context.Background(), env)
	if gotAuth != "Bearer "+token {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotAcct != "acct-42" {
		t.Fatalf("chatgpt-account-id = %q", gotAcct)
	}
}

func TestFetchCodexNotLoggedIn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir()) // empty dir: no auth.json
	srv, hits := codexServer(t, 200, "{}", nil)
	env := codexEnv(srv)

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaNotLoggedIn {
		t.Fatalf("status = %v", q.Status)
	}
	if *hits != 0 {
		t.Fatalf("expected no request, got %d", *hits)
	}
}

func TestFetchCodexUnreadableAuth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", dir)
	srv, hits := codexServer(t, 200, "{}", nil)

	q := FetchCodex(context.Background(), codexEnv(srv))
	if q.Status != QuotaNotLoggedIn {
		t.Fatalf("status = %v, want NotLoggedIn", q.Status)
	}
	if *hits != 0 {
		t.Fatalf("expected no request, got %d", *hits)
	}
}

func TestFetchCodexMissingTokens(t *testing.T) {
	writeCodexAuth(t, "", "acct-1")
	srv, hits := codexServer(t, 200, "{}", nil)

	q := FetchCodex(context.Background(), codexEnv(srv))
	if q.Status != QuotaNotLoggedIn {
		t.Fatalf("status = %v", q.Status)
	}
	if *hits != 0 {
		t.Fatal("expected no request")
	}
}

func TestFetchCodexExpiredJWT(t *testing.T) {
	writeCodexAuth(t, makeJWT(fakeNow.Add(-time.Hour).Unix()), "acct-1")
	srv, hits := codexServer(t, 200, "{}", nil)
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaExpired {
		t.Fatalf("status = %v", q.Status)
	}
	if *hits != 0 {
		t.Fatalf("expired token must not hit the network, got %d requests", *hits)
	}
}

func TestFetchCodex401And403(t *testing.T) {
	for _, code := range []int{401, 403} {
		writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
		srv, _ := codexServer(t, code, `{"error":"unauthorized"}`, nil)
		env := codexEnv(srv)
		env.Home = t.TempDir()

		q := FetchCodex(context.Background(), env)
		if q.Status != QuotaExpired {
			t.Fatalf("HTTP %d → status %v, want Expired", code, q.Status)
		}
	}
}

func TestFetchCodex429(t *testing.T) {
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, _ := codexServer(t, 429, `{"error":"slow down"}`,
		map[string]string{"Retry-After": "120"})
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaRateLimited {
		t.Fatalf("status = %v", q.Status)
	}
	if q.RetryAfter != 120*time.Second {
		t.Fatalf("RetryAfter = %v", q.RetryAfter)
	}
}

func TestFetchCodex429NoHeader(t *testing.T) {
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, _ := codexServer(t, 429, `{"error":"slow down"}`, nil)
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaRateLimited {
		t.Fatalf("status = %v", q.Status)
	}
	if q.RetryAfter != 5*time.Minute {
		t.Fatalf("RetryAfter fallback = %v, want 5m", q.RetryAfter)
	}
}

func TestFetchCodex500(t *testing.T) {
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, _ := codexServer(t, 500, `{"error":"boom"}`, nil)
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaFailed {
		t.Fatalf("status = %v", q.Status)
	}
	if q.Err == "" {
		t.Fatal("Err should carry a short description")
	}
}

func TestFetchCodexWeeklyOnly(t *testing.T) {
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, _ := codexServer(t, 200, fixture(t, "codex_usage_weekly_only.json"), nil)
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaOK {
		t.Fatalf("status = %v", q.Status)
	}
	if quotaWindow(q, "5 hours") != nil {
		t.Fatalf("five-hour should be absent, got %+v", q.Windows)
	}
	if week := quotaWindow(q, "7 days"); week == nil || week.UsedPct != 64 {
		t.Fatalf("weekly = %+v", week)
	}
	if q.Plan != "Pro" {
		t.Fatalf("plan = %q", q.Plan)
	}
}

func TestFetchCodexWindowsByLength(t *testing.T) {
	// Vendor sent the 7d window in primary and the 5h one in secondary:
	// classification must follow limit_window_seconds, not the slot.
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, _ := codexServer(t, 200, fixture(t, "codex_usage_swapped.json"), nil)
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaOK {
		t.Fatalf("status = %v", q.Status)
	}
	if five := quotaWindow(q, "5 hours"); five == nil || five.UsedPct != 21 {
		t.Fatalf("five-hour = %+v", five)
	}
	if week := quotaWindow(q, "7 days"); week == nil || week.UsedPct != 55 {
		t.Fatalf("weekly = %+v", week)
	}
	if q.Plan != "Pro Lite" {
		t.Fatalf("plan = %q", q.Plan)
	}
}

func TestFetchCodexMissingFields(t *testing.T) {
	// No plan_type, missing used_percent in primary, secondary window
	// without a length — a window is published only when its utilization
	// AND its duration are known; absent fields stay unknown instead of
	// fabricating a healthy 100%-left number. And an answer with no
	// recognizable window at all is a failure, not a frozen OK: the card
	// must say the reading did not come back.
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, _ := codexServer(t, 200, fixture(t, "codex_usage_missing_fields.json"), nil)
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaFailed || q.Err != "no usage windows" {
		t.Fatalf("status = %v, err %q; want QuotaFailed with no usage windows", q.Status, q.Err)
	}
	if q.Plan != "" {
		t.Fatalf("plan = %q", q.Plan)
	}
	if len(q.Windows) != 0 {
		t.Fatalf("unknown windows must not publish, got %+v", q.Windows)
	}
}

func TestFetchCodexRejectsRedirect(t *testing.T) {
	// The credential headers (Authorization, chatgpt-account-id) must never
	// reach a redirected-to host: redirects are refused outright, so the
	// target server sees nothing — not even for a plain same-scheme hop.
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirected request reached the target host with credentials in flight")
	}))
	defer target.Close()
	srv, _ := codexServer(t, http.StatusFound, "",
		map[string]string{"Location": target.URL + "/wham/usage"})
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaFailed {
		t.Fatalf("status = %v, want QuotaFailed (redirect refused)", q.Status)
	}
}

func TestFetchCodexFetchedAtIsReceipt(t *testing.T) {
	// FetchedAt is stamped when the response arrives, not when the request
	// started, so a Retry-After delay is measured from receipt.
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, _ := codexServer(t, 429, `{}`, map[string]string{"Retry-After": "600"})
	receipt := fakeNow.Add(10 * time.Second)
	calls := 0
	env := codexEnv(srv)
	env.Home = t.TempDir()
	env.Now = func() time.Time {
		calls++
		if calls > 1 {
			return receipt // the clock the fetcher reads after the exchange
		}
		return fakeNow
	}

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaRateLimited {
		t.Fatalf("status = %v", q.Status)
	}
	if !q.FetchedAt.Equal(receipt) {
		t.Fatalf("FetchedAt = %v, want receipt time %v", q.FetchedAt, receipt)
	}
}

func TestFetchCodexCancelledContext(t *testing.T) {
	// A cancelled context short-circuits before credentials are touched.
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, _ := codexServer(t, 200, `{}`, nil)
	env := codexEnv(srv)
	env.Home = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := FetchCodex(ctx, env)
	if q.Status != QuotaFailed {
		t.Fatalf("status = %v, want QuotaFailed", q.Status)
	}
}

func TestFetchCodexBadJSON(t *testing.T) {
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv, _ := codexServer(t, 200, `<html>oops</html>`, nil)
	env := codexEnv(srv)
	env.Home = t.TempDir()

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaFailed {
		t.Fatalf("status = %v", q.Status)
	}
}

func TestFetchCodexContextTimeout(t *testing.T) {
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, "{}")
	}))
	defer srv.Close()
	env := codexEnv(srv)
	env.Home = t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	q := FetchCodex(ctx, env)
	if q.Status != QuotaFailed {
		t.Fatalf("status = %v", q.Status)
	}
	if q.Err == "" {
		t.Fatal("Err should describe the failure")
	}
}
