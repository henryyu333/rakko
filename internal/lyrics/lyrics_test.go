package lyrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLRC(t *testing.T) {
	src := `[ar:周杰伦]
[ti:夜曲]
[offset:+250]
[00:01.00]第一句
[00:03.50][00:07.25]副歌
[00:05.5]半分秒
[00:09.123]毫秒
[00:10.00]
`
	got := ParseLRC(src)
	want := []struct {
		at   time.Duration
		text string
	}{
		{1250 * time.Millisecond, "第一句"},
		{3750 * time.Millisecond, "副歌"},
		{5750 * time.Millisecond, "半分秒"},
		{7500 * time.Millisecond, "副歌"},
		{9373 * time.Millisecond, "毫秒"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].At != w.at || got[i].Text != w.text {
			t.Errorf("line %d = %v %q, want %v %q", i, got[i].At, got[i].Text, w.at, w.text)
		}
	}
}

func TestParseLRCClampsNegativeOffset(t *testing.T) {
	got := ParseLRC("[offset:-5000]\n[00:01.00]early")
	if len(got) != 1 || got[0].At != 0 {
		t.Fatalf("negative offset = %+v, want one line at zero", got)
	}
}

func TestLineAt(t *testing.T) {
	lines := []Line{{At: time.Second}, {At: 2 * time.Second}, {At: 3 * time.Second}}
	for _, c := range []struct {
		pos  time.Duration
		want int
	}{{0, 0}, {1500 * time.Millisecond, 0}, {2 * time.Second, 1}, {9 * time.Second, 2}} {
		if got := LineAt(lines, c.pos); got != c.want {
			t.Errorf("LineAt(%v) = %d, want %d", c.pos, got, c.want)
		}
	}
	if got := LineAt(nil, time.Second); got != -1 {
		t.Errorf("LineAt(no lines) = %d, want -1", got)
	}
}

func TestFetchExactMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/get" {
			t.Errorf("path = %s, want /api/get", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); got != "Rakko/1.0 (test)" {
			t.Errorf("User-Agent = %q", got)
		}
		q := r.URL.Query()
		if q.Get("track_name") != "夜曲" || q.Get("artist_name") != "周杰伦" || q.Get("duration") != "227" {
			t.Errorf("query = %v", q)
		}
		json.NewEncoder(w).Encode(apiTrack{SyncedLyrics: "[00:01.00]hi", Duration: 227})
	}))
	defer srv.Close()
	f := Fetcher{BaseURL: srv.URL, UserAgent: "Rakko/1.0 (test)"}
	got, err := f.Fetch(context.Background(), Track{Title: "夜曲", Artist: "周杰伦", Duration: 227})
	if err != nil {
		t.Fatal(err)
	}
	if got.Synced != "[00:01.00]hi" || got.None {
		t.Fatalf("result = %+v", got)
	}
}

func TestFetchFallsBackToClosestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/get":
			http.NotFound(w, r)
		case "/api/search":
			json.NewEncoder(w).Encode([]apiTrack{
				{SyncedLyrics: "[00:01.00]wrong", Duration: 300},
				{SyncedLyrics: "[00:02.00]right", Duration: 202},
				{Instrumental: true, Duration: 203},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := Fetcher{BaseURL: srv.URL}
	got, err := f.Fetch(context.Background(), Track{Title: "T", Artist: "A", Duration: 200})
	if err != nil {
		t.Fatal(err)
	}
	if got.Synced != "[00:02.00]right" {
		t.Fatalf("closest = %+v, want the 202s entry", got)
	}
}

func TestFetchPlainOrInstrumentalIsNone(t *testing.T) {
	for _, answer := range []apiTrack{
		{PlainLyrics: "words", Duration: 100},
		{Instrumental: true, Duration: 100},
		{SyncedLyrics: "  ", Duration: 100},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(answer)
		}))
		got, err := Fetcher{BaseURL: srv.URL}.Fetch(context.Background(), Track{Title: "T", Duration: 100})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !got.None || got.Synced != "" {
			t.Errorf("answer %+v gave %+v, want None", answer, got)
		}
	}
}

func TestFetchNetworkError(t *testing.T) {
	f := Fetcher{BaseURL: "http://127.0.0.1:1", Client: &http.Client{Timeout: time.Second}}
	if _, err := f.Fetch(context.Background(), Track{Title: "T"}); err == nil {
		t.Fatal("a refused connection must be an error, not an empty answer")
	}
}

func TestCacheRoundTripAndExpiry(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	c := NewCache(dir)
	c.now = func() time.Time { return now }
	key := Key(Track{Title: " 夜曲 ", Artist: "周杰伦", Duration: 227})
	c.Put(key, Result{Synced: "[00:01.00]hi"})

	if data, err := os.ReadFile(filepath.Join(dir, cacheFileName)); err != nil || !strings.Contains(string(data), "hi") {
		t.Fatalf("cache file = %q, %v", data, err)
	}
	again := NewCache(dir)
	again.now = func() time.Time { return now }
	got, ok := again.Get(key)
	if !ok || got.Synced != "[00:01.00]hi" {
		t.Fatalf("reloaded cache = %+v, %v", got, ok)
	}
	// The key folds case and spacing.
	if key != Key(Track{Title: "夜曲", Artist: " 周杰伦 ", Duration: 227}) {
		t.Fatalf("key is not normalised: %q", key)
	}
	now = now.Add(CacheTTL + time.Second)
	if _, ok := again.Get(key); ok {
		t.Fatal("expired cache entry still answered")
	}
}

func TestCacheKeepsNone(t *testing.T) {
	c := NewCache("")
	key := Key(Track{Title: "T", Duration: 1})
	c.Put(key, Result{None: true})
	if got, ok := c.Get(key); !ok || !got.None {
		t.Fatalf("no-lyrics answer = %+v, %v", got, ok)
	}
}
