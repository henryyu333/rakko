package main

// Tests for the website update proxy and its GitHub fallback: the primary
// request carries ?v= and Rakko's User-Agent, a non-200 or a refused
// connection falls back to GitHub, and Open refuses plain HTTP off the
// loopback.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const updateSourceManifest = `{"version":"1.19.0","url":"https://example.com/app.tar.gz","signature":"c2ln"}`

func updateSourceProbe(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestUpdateSourcePrimary(t *testing.T) {
	primary, primaryHits := updateSourceProbe(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("v"); got != "1.2.3" {
			t.Errorf("primary ?v= = %q, want 1.2.3", got)
		}
		if got := r.Header.Get("User-Agent"); got != "Rakko/1.2.3" {
			t.Errorf("primary User-Agent = %q, want Rakko/1.2.3", got)
		}
		io.WriteString(w, updateSourceManifest)
	})
	fallback, fallbackHits := updateSourceProbe(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("fallback was hit while the primary answered")
	})
	s := &rakkoUpdateSource{
		primary:  primary.URL + "/update-darwin-arm64.json",
		fallback: fallback.URL + "/update-darwin-arm64.json",
		client:   primary.Client(),
		version:  func() string { return "1.2.3" },
	}

	body, err := s.Manifest(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != updateSourceManifest {
		t.Errorf("manifest = %q, want the primary body", got)
	}
	if primaryHits.Load() != 1 || fallbackHits.Load() != 0 {
		t.Errorf("hits primary=%d fallback=%d, want 1/0", primaryHits.Load(), fallbackHits.Load())
	}
}

func TestUpdateSourceFallsBackOnNon200(t *testing.T) {
	primary, _ := updateSourceProbe(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusServiceUnavailable)
	})
	fallback, fallbackHits := updateSourceProbe(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("v"); got != "1.2.3" {
			t.Errorf("fallback ?v= = %q, want 1.2.3", got)
		}
		if got := r.Header.Get("User-Agent"); got != "Rakko/1.2.3" {
			t.Errorf("fallback User-Agent = %q, want Rakko/1.2.3", got)
		}
		io.WriteString(w, updateSourceManifest)
	})
	s := &rakkoUpdateSource{
		primary:  primary.URL + "/update-darwin-arm64.json",
		fallback: fallback.URL + "/update-darwin-arm64.json",
		client:   primary.Client(),
		version:  func() string { return "1.2.3" },
	}

	body, err := s.Manifest(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != updateSourceManifest {
		t.Errorf("manifest = %q, want the fallback body", got)
	}
	if fallbackHits.Load() != 1 {
		t.Errorf("fallback hits = %d, want 1", fallbackHits.Load())
	}
}

// A proxy that never answers must not spend the check's deadline: the
// primary request has its own short one, and GitHub still gets a turn.
func TestUpdateSourceFallsBackOnTimeout(t *testing.T) {
	old := updateSourcePrimaryTimeout
	updateSourcePrimaryTimeout = 100 * time.Millisecond
	t.Cleanup(func() { updateSourcePrimaryTimeout = old })

	block := make(chan struct{})
	primary, _ := updateSourceProbe(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(block) })
	fallback, _ := updateSourceProbe(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, updateSourceManifest)
	})
	s := &rakkoUpdateSource{
		primary:  primary.URL + "/update-darwin-arm64.json",
		fallback: fallback.URL + "/update-darwin-arm64.json",
		client:   primary.Client(),
		version:  func() string { return "1.2.3" },
	}

	body, err := s.Manifest(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != updateSourceManifest {
		t.Errorf("manifest = %q, want the fallback body", got)
	}
}

func TestUpdateSourceOpen(t *testing.T) {
	srv, hits := updateSourceProbe(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "Rakko/1.2.3" {
			t.Errorf("Open User-Agent = %q, want Rakko/1.2.3", got)
		}
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "archive bytes")
	})
	s := &rakkoUpdateSource{
		client:  srv.Client(),
		version: func() string { return "1.2.3" },
	}

	body, err := s.Open(context.Background(), srv.URL+"/app.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "archive bytes" {
		t.Errorf("Open body = %q, want the archive", got)
	}

	if _, err := s.Open(context.Background(), srv.URL+"/missing"); err == nil {
		t.Error("Open of a 404 succeeded")
	}
	before := hits.Load()
	if _, err := s.Open(context.Background(), "http://example.com/app.tar.gz"); err == nil {
		t.Error("Open of plain HTTP off the loopback succeeded")
	}
	if hits.Load() != before {
		t.Error("refused URL still reached the server")
	}
}

func TestUpdateSourceDefaultURLs(t *testing.T) {
	s := newRakkoUpdateSource()
	if !strings.HasPrefix(s.primary, updateProxyBase) || !strings.HasSuffix(s.primary, ".json") {
		t.Errorf("primary = %q, want the website proxy", s.primary)
	}
	if !strings.HasPrefix(s.fallback, updateGitHubBase) || !strings.HasSuffix(s.fallback, ".json") {
		t.Errorf("fallback = %q, want the GitHub release asset", s.fallback)
	}
	if s.client == nil || s.version == nil {
		t.Error("production source is missing its client or version")
	}
}
