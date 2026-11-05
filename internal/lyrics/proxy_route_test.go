package lyrics

// The production lyrics client (no injected Fetcher.Client) goes through the
// app's proxy rules: the lookup must reach a local forward proxy, not resolve
// the service's host itself. The service here is a fake local endpoint.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestFetchThroughProxyRules(t *testing.T) {
	for _, k := range []string{
		"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY",
		"no_proxy", "NO_PROXY", "ALL_PROXY", "all_proxy", "REQUEST_METHOD",
	} {
		t.Setenv(k, "")
	}
	var mu sync.Mutex
	var host, path string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		host, path = r.Host, r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"syncedLyrics":"[00:01.00]proxy answer","duration":200}`))
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)

	f := Fetcher{
		BaseURL:        "http://lyrics.example.test",
		NeteaseBaseURL: "http://netease.example.test",
		UserAgent:      "Rakko/test",
	}
	res, err := f.Fetch(context.Background(), Track{Title: "Song", Artist: "Artist", Duration: 200})
	if err != nil {
		t.Fatalf("fetch through the proxy: %v", err)
	}
	if res.Synced != "[00:01.00]proxy answer" {
		t.Fatalf("result %+v: the lookup did not go through the proxy", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if host != "lyrics.example.test" || path != "/api/get" {
		t.Fatalf("the proxy saw %s%s, want the service's host and path", host, path)
	}
}
