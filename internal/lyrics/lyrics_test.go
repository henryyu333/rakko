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
	ne := emptyNetease(t)
	defer ne.Close()
	for _, answer := range []apiTrack{
		{PlainLyrics: "words", Duration: 100},
		{Instrumental: true, Duration: 100},
		{SyncedLyrics: "  ", Duration: 100},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(answer)
		}))
		got, err := Fetcher{BaseURL: srv.URL, NeteaseBaseURL: ne.URL}.Fetch(context.Background(), Track{Title: "T", Duration: 100})
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
	f := Fetcher{
		BaseURL:        "http://127.0.0.1:1",
		NeteaseBaseURL: "http://127.0.0.1:1",
		Client:         &http.Client{Timeout: time.Second},
	}
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

// emptyNetease answers every search with no songs, so a test that only
// exercises LRCLIB never reaches the network.
func emptyNetease(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search/get/web" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(neteaseSearchAnswer{})
	}))
}

func TestFetchRetriesOverloadedLRCLIB(t *testing.T) {
	var calls int
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/get" {
			t.Errorf("path = %s, want /api/get", r.URL.Path)
		}
		calls++
		if calls <= 2 {
			// What lrclib.net answered when it was overloaded.
			http.Error(w, `{"code":503,"name":"ServerOverloaded"}`, http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(apiTrack{SyncedLyrics: "[00:01.00]hi", Duration: 100})
	}))
	defer lr.Close()
	ne := emptyNetease(t)
	defer ne.Close()

	got, err := Fetcher{BaseURL: lr.URL, NeteaseBaseURL: ne.URL}.Fetch(
		context.Background(), Track{Title: "T", Artist: "A", Duration: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got.Synced != "[00:01.00]hi" {
		t.Fatalf("result = %+v, want the answer after two 503s", got)
	}
	if calls != 3 {
		t.Fatalf("get asked %d times, want 3 (two retries)", calls)
	}
}

func TestFetchNeteaseAfterLRCLIBOverloaded(t *testing.T) {
	var lrCalls int
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lrCalls++
		http.Error(w, `{"code":503,"name":"ServerOverloaded"}`, http.StatusServiceUnavailable)
	}))
	defer lr.Close()
	var agent, path string
	ne := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		agent = r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/api/search/get/web":
			if q := r.URL.Query(); q.Get("s") != "分手总要在雨天(Live) 张学友" || q.Get("limit") != "10" {
				t.Errorf("search query = %v", q)
			}
			var ans neteaseSearchAnswer
			ans.Result.Songs = []neteaseSong{{
				ID: 188172, Name: "分手总要在雨天(Live)", Duration: 292853,
				Artists: []neteaseArtist{{Name: "张学友"}},
				Album:   neteaseAlbum{Name: "张学友音乐之旅Live演唱会"},
			}}
			json.NewEncoder(w).Encode(ans)
		case "/api/song/lyric":
			if r.URL.Query().Get("id") != "188172" {
				t.Errorf("lyric id = %s, want 188172", r.URL.Query().Get("id"))
			}
			var ans neteaseLyricAnswer
			ans.LRC.Lyric = "[00:13.630]晨曦细雨重临在这大地"
			json.NewEncoder(w).Encode(ans)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ne.Close()

	got, err := Fetcher{BaseURL: lr.URL, NeteaseBaseURL: ne.URL}.Fetch(context.Background(), Track{
		Title: "分手总要在雨天(Live)", Artist: "张学友", Album: "张学友音乐之旅Live演唱会", Duration: 293,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Synced != "[00:13.630]晨曦细雨重临在这大地" {
		t.Fatalf("result = %+v, want the NetEase LRC", got)
	}
	if lrCalls != 3 {
		t.Fatalf("lrclib asked %d times, want 3", lrCalls)
	}
	if path != "/api/song/lyric" {
		t.Fatalf("last NetEase path = %q", path)
	}
	if !strings.HasPrefix(agent, "Mozilla/5.0") {
		t.Fatalf("NetEase User-Agent = %q, want a browser's", agent)
	}
}

func TestFetchNeteaseWhenLRCLIBHasNoSyncedLyrics(t *testing.T) {
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(apiTrack{PlainLyrics: "words", Duration: 100})
	}))
	defer lr.Close()
	ne := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ans neteaseSearchAnswer
		if r.URL.Path == "/api/search/get/web" {
			ans.Result.Songs = []neteaseSong{{
				ID: 7, Duration: 100000, Artists: []neteaseArtist{{Name: "A"}},
			}}
			json.NewEncoder(w).Encode(ans)
			return
		}
		var lyric neteaseLyricAnswer
		lyric.LRC.Lyric = "[00:01.000]from netease"
		json.NewEncoder(w).Encode(lyric)
	}))
	defer ne.Close()

	got, err := Fetcher{BaseURL: lr.URL, NeteaseBaseURL: ne.URL}.Fetch(
		context.Background(), Track{Title: "T", Artist: "A", Duration: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got.Synced != "[00:01.000]from netease" {
		t.Fatalf("result = %+v, want the NetEase LRC", got)
	}
}

func TestFetchStripsTrailingVersionTag(t *testing.T) {
	var searched []string
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get" {
			http.NotFound(w, r)
			return
		}
		name := r.URL.Query().Get("track_name")
		searched = append(searched, name)
		if name == "分手总要在雨天" {
			json.NewEncoder(w).Encode([]apiTrack{{SyncedLyrics: "[00:01.00]雨", Duration: 293}})
			return
		}
		json.NewEncoder(w).Encode([]apiTrack{})
	}))
	defer lr.Close()
	ne := emptyNetease(t)
	defer ne.Close()

	got, err := Fetcher{BaseURL: lr.URL, NeteaseBaseURL: ne.URL}.Fetch(
		context.Background(), Track{Title: "分手总要在雨天(Live)", Artist: "张学友", Duration: 293})
	if err != nil {
		t.Fatal(err)
	}
	if got.Synced != "[00:01.00]雨" {
		t.Fatalf("result = %+v, want the bare-title match", got)
	}
	if strings.Join(searched, "|") != "分手总要在雨天(Live)|分手总要在雨天" {
		t.Fatalf("searches = %v", searched)
	}
}

func TestFetchOnlyBothSourcesFailingIsAnError(t *testing.T) {
	broken := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}
	lr := httptest.NewServer(http.HandlerFunc(broken))
	defer lr.Close()
	ne := httptest.NewServer(http.HandlerFunc(broken))
	defer ne.Close()
	f := Fetcher{BaseURL: lr.URL, NeteaseBaseURL: ne.URL}
	if _, err := f.Fetch(context.Background(), Track{Title: "T", Artist: "A", Duration: 100}); err == nil {
		t.Fatal("both services failing must be an error")
	}

	// One completed miss is enough to answer None, even if the other failed.
	lrOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(apiTrack{Duration: 100})
	}))
	defer lrOK.Close()
	got, err := Fetcher{BaseURL: lrOK.URL, NeteaseBaseURL: ne.URL}.Fetch(
		context.Background(), Track{Title: "T", Artist: "A", Duration: 100})
	if err != nil || !got.None {
		t.Fatalf("lrclib miss + netease failure = %+v, %v; want None", got, err)
	}
}

func TestNeteaseClosest(t *testing.T) {
	for _, c := range []struct {
		name  string
		songs []neteaseSong
		want  Track
		id    int64 // 0 for no pick
	}{
		{
			name: "album wins over a nearer length",
			songs: []neteaseSong{
				{ID: 1, Duration: 100000, Artists: []neteaseArtist{{Name: "A"}}, Album: neteaseAlbum{Name: "Other"}},
				{ID: 2, Duration: 102500, Artists: []neteaseArtist{{Name: "A"}}, Album: neteaseAlbum{Name: "Want"}},
			},
			want: Track{Artist: "A", Album: "want", Duration: 100},
			id:   2,
		},
		{
			name: "nearest length without a matching album",
			songs: []neteaseSong{
				{ID: 1, Duration: 102500, Artists: []neteaseArtist{{Name: "A"}}, Album: neteaseAlbum{Name: "Other"}},
				{ID: 2, Duration: 100400, Artists: []neteaseArtist{{Name: "A"}}, Album: neteaseAlbum{Name: "Other"}},
			},
			want: Track{Artist: "A", Album: "Want", Duration: 100},
			id:   2,
		},
		{
			name:  "a different artist is skipped",
			songs: []neteaseSong{{ID: 1, Duration: 100000, Artists: []neteaseArtist{{Name: "B"}}}},
			want:  Track{Artist: "A", Duration: 100},
		},
		{
			name:  "a length outside three seconds is skipped",
			songs: []neteaseSong{{ID: 1, Duration: 104000, Artists: []neteaseArtist{{Name: "A"}}}},
			want:  Track{Artist: "A", Duration: 100},
		},
		{
			name: "one matching credit is enough",
			songs: []neteaseSong{{
				ID: 1, Duration: 100000,
				Artists: []neteaseArtist{{Name: "B"}, {Name: " A "}},
			}},
			want: Track{Artist: "a", Duration: 100},
			id:   1,
		},
		{
			name: "a live stream takes the first artist match",
			songs: []neteaseSong{
				{ID: 1, Duration: 900000, Artists: []neteaseArtist{{Name: "B"}}},
				{ID: 2, Duration: 800000, Artists: []neteaseArtist{{Name: "A"}}},
			},
			want: Track{Artist: "A"},
			id:   2,
		},
	} {
		got := neteaseClosest(c.songs, c.want)
		switch {
		case c.id == 0 && got != nil:
			t.Errorf("%s: picked %d, want none", c.name, got.ID)
		case c.id != 0 && (got == nil || got.ID != c.id):
			t.Errorf("%s: picked %+v, want id %d", c.name, got, c.id)
		}
	}
}

func TestNeteaseResults(t *testing.T) {
	for _, c := range []struct {
		name  string
		lyric neteaseLyricAnswer
		none  bool
		inst  bool
	}{
		{name: "synced", lyric: func() neteaseLyricAnswer {
			var a neteaseLyricAnswer
			a.LRC.Lyric = "[00:01.000]hi"
			return a
		}()},
		{name: "uncollected", lyric: neteaseLyricAnswer{Uncollected: true}, none: true},
		{name: "nolyric", lyric: neteaseLyricAnswer{NoLyric: true}, none: true, inst: true},
		{name: "empty", lyric: neteaseLyricAnswer{}, none: true, inst: true},
		{name: "pure music placeholder", lyric: func() neteaseLyricAnswer {
			var a neteaseLyricAnswer
			a.LRC.Lyric = "纯音乐，请欣赏"
			return a
		}(), none: true, inst: true},
	} {
		got := neteaseResult(c.lyric)
		if got.None != c.none || got.Instrumental != c.inst {
			t.Errorf("%s = %+v, want none %v instrumental %v", c.name, got, c.none, c.inst)
		}
	}
}

func TestParseLRCNeteaseMilliseconds(t *testing.T) {
	got := ParseLRC("[00:13.630]晨曦\n[01:02.350]雨")
	want := []time.Duration{13*time.Second + 630*time.Millisecond, 62*time.Second + 350*time.Millisecond}
	if len(got) != len(want) {
		t.Fatalf("parsed %d lines, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].At != w {
			t.Errorf("line %d at %v, want %v", i, got[i].At, w)
		}
	}
}

func TestBaseTitle(t *testing.T) {
	for in, want := range map[string]string{
		"分手总要在雨天(Live)":           "分手总要在雨天",
		"分手总要在雨天（Live）":           "分手总要在雨天",
		"分手总要在雨天 (伴奏)":            "分手总要在雨天",
		"夜曲[Remix]":               "夜曲",
		"夜曲【电影版】":                 "夜曲",
		"分手总要在雨天 - 电影《分手总要在雨天》插曲": "分手总要在雨天",
		"夜曲":     "夜曲",
		"(Live)": "(Live)",
	} {
		if got := baseTitle(in); got != want {
			t.Errorf("baseTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
