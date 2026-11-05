package aiusage

// The production quota client goes through the app's proxy rules: with no
// client injected, the request must reach a local forward proxy rather than
// resolving the vendor's host itself. The endpoint here is a fake account
// with a fake token; nothing leaves the machine.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY",
		"no_proxy", "NO_PROXY", "ALL_PROXY", "all_proxy", "REQUEST_METHOD",
	} {
		t.Setenv(k, "")
	}
}

func TestFetchCodexThroughProxyRules(t *testing.T) {
	clearProxyEnv(t)
	writeCodexAuth(t, makeJWT(fakeNow.Add(time.Hour).Unix()), "acct-1")

	var mu sync.Mutex
	var host, path, auth string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		host, path, auth = r.Host, r.URL.Path, r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixture(t, "codex_usage.json")))
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)

	env := Env{Home: t.TempDir(), CodexBaseURL: "http://codex.example.test",
		Now: func() time.Time { return fakeNow }}

	q := FetchCodex(context.Background(), env)
	if q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q: the request did not go through the proxy", q.Status, q.Err)
	}
	mu.Lock()
	defer mu.Unlock()
	if host != "codex.example.test" || path != "/wham/usage" {
		t.Fatalf("the proxy saw %s%s, want the vendor's host and path", host, path)
	}
	if auth != "Bearer "+makeJWT(fakeNow.Add(time.Hour).Unix()) {
		t.Fatalf("the proxy saw Authorization %q", auth)
	}
}
