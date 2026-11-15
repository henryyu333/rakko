// The NetEase source's data half: the queue files, the window title, the cover
// cache names, the sample check and the merge rule. The fixtures are shaped
// like the client's files but every name, id and URL is invented, except the
// one CDN URL kept as the anchor for the md5 convention (a public album cover).

package platform

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// neFMFixture is fmPlay as the client writes it: an index plus the queue.
const neFMFixture = `{"currentIndex":1,"queue":[
 {"id":"1001","name":"First Song","duration":201000,
  "artists":[{"id":"1","name":"Solo"}],
  "album":{"id":"11","name":"First Album","picUrl":"http://p4.music.126.net/AAA==/1.jpg"}},
 {"id":"1002","name":"Second Song","duration":237106,
  "artists":[{"id":"2","name":"One"},{"id":"3","name":"Two"}],
  "album":{"id":"12","name":"Second Album","picUrl":"http://p3.music.126.net/BBB==/2.jpg"}},
 {"id":1003,"name":"Third Song","duration":0,
  "artists":[],"album":{"name":"","picUrl":""}}
]}`

// nePlayingListFixture is playingList: the same track shape, no index.
const nePlayingListFixture = `{"list":[
 {"id":"2001","displayOrder":0,"track":{"id":"3001","name":"Queue Song","duration":180500,
  "artists":[{"name":"Queue Artist"}],
  "album":{"name":"Queue Album","picUrl":"http://p1.music.126.net/CCC==/3.jpg"}}},
 {"id":"2002","displayOrder":1,"track":{"id":"3002","name":"Other Song","duration":90000,
  "artists":[{"name":"Other Artist"}],"album":{"name":"Other Album","picUrl":""}}}
]}`

func TestNESplitTitle(t *testing.T) {
	cases := []struct{ in, title, artist string }{
		{"Second Song - One / Two", "Second Song", "One / Two"},
		{"Just A Name", "Just A Name", ""},
		{"  Spaced Out  -  The Artist  ", "Spaced Out", "The Artist"},
		{"", "", ""},
		{"A - B - C", "A", "B - C"}, // the first separator wins
	}
	for _, c := range cases {
		title, artist := neSplitTitle(c.in)
		if title != c.title || artist != c.artist {
			t.Errorf("neSplitTitle(%q) = %q, %q; want %q, %q", c.in, title, artist, c.title, c.artist)
		}
	}
}

func TestNEParseFMPlay(t *testing.T) {
	cur, tracks, ok := neParseFMPlay([]byte(neFMFixture))
	if !ok {
		t.Fatal("fixture did not parse")
	}
	if len(tracks) != 3 {
		t.Fatalf("tracks = %d, want 3", len(tracks))
	}
	if cur.ID != "1002" || cur.Title != "Second Song" {
		t.Fatalf("current = %+v, want 1002 Second Song", cur)
	}
	if cur.Artist != "One / Two" {
		t.Errorf("artist = %q, want %q", cur.Artist, "One / Two")
	}
	if cur.Album != "Second Album" || cur.PicURL != "http://p3.music.126.net/BBB==/2.jpg" {
		t.Errorf("album = %q %q", cur.Album, cur.PicURL)
	}
	if cur.Duration != 237.106 {
		t.Errorf("duration = %v s, want 237.106", cur.Duration)
	}
	// A numeric id must not take the file down.
	if tracks[2].ID != "1003" {
		t.Errorf("numeric id = %q, want 1003", tracks[2].ID)
	}
	if tracks[2].Duration != 0 {
		t.Errorf("zero duration = %v, want 0", tracks[2].Duration)
	}
}

func TestNEParseFMPlayBadInput(t *testing.T) {
	if _, _, ok := neParseFMPlay([]byte(`not json`)); ok {
		t.Fatal("malformed file parsed")
	}
	// An out-of-range index leaves no current track but keeps the queue: the
	// client writes the index before the queue during a swap.
	cur, tracks, ok := neParseFMPlay([]byte(`{"currentIndex":9,"queue":[{"id":"1","name":"A"}]}`))
	if !ok || len(tracks) != 1 {
		t.Fatalf("ok=%v tracks=%d", ok, len(tracks))
	}
	if cur.ID != "" {
		t.Errorf("current = %+v, want empty", cur)
	}
}

func TestNEParsePlayingList(t *testing.T) {
	tracks, ok := neParsePlayingList([]byte(nePlayingListFixture))
	if !ok {
		t.Fatal("fixture did not parse")
	}
	if len(tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(tracks))
	}
	if tracks[0].ID != "3001" || tracks[0].Title != "Queue Song" || tracks[0].Artist != "Queue Artist" {
		t.Errorf("track 0 = %+v", tracks[0])
	}
	if tracks[0].Duration != 180.5 {
		t.Errorf("duration = %v, want 180.5", tracks[0].Duration)
	}
}

func TestNEFindTrack(t *testing.T) {
	_, tracks, _ := neParseFMPlay([]byte(neFMFixture))
	if tr, ok := neFindTrack(tracks, "1002"); !ok || tr.Title != "Second Song" {
		t.Fatalf("lookup by id = %+v, %v", tr, ok)
	}
	if _, ok := neFindTrack(tracks, "9999"); ok {
		t.Fatal("unknown id matched")
	}
	if _, ok := neFindTrack(tracks, ""); ok {
		t.Fatal(`"" matched`)
	}
}

func TestNEFindTitled(t *testing.T) {
	_, tracks, _ := neParseFMPlay([]byte(neFMFixture))
	if tr, ok := neFindTitled(tracks, "Second Song", "One / Two"); !ok || tr.ID != "1002" {
		t.Fatalf("titled lookup = %+v, %v", tr, ok)
	}
	// An artist that disagrees must not match: two queues can hold one title.
	if _, ok := neFindTitled(tracks, "Second Song", "Someone Else"); ok {
		t.Fatal("mismatched artist matched")
	}
	if tr, ok := neFindTitled(tracks, "First Song", ""); !ok || tr.ID != "1001" {
		t.Fatalf("artist-less lookup = %+v, %v", tr, ok)
	}
	if _, ok := neFindTitled(tracks, "", ""); ok {
		t.Fatal("empty title matched")
	}
}

// TestNECoverHash pins the convention the client's image cache uses: the file
// name is the md5 of the whole request URL, query included. The pair below was
// read from the client's own cache index on 3.1.41.
func TestNECoverHash(t *testing.T) {
	url := "http://p4.music.126.net/OtcUikGxxfkbCyymgk97rg==/6643249255231020.jpg" +
		"?imageView=&thumbnail=108y108&type=jpg&rotate=0&tostatic=0"
	if got, want := neCoverHash(url), "a08451bce5824a2e2fc01f2dd22360ee"; got != want {
		t.Fatalf("hash = %s, want %s", got, want)
	}
	// The scheme and the query are part of the key, so the https form of the
	// same picture is a different cache entry.
	if neCoverHash(strings.Replace(url, "http://", "https://", 1)) == neCoverHash(url) {
		t.Fatal("scheme is not part of the cache key")
	}
}

func TestNECoverURLs(t *testing.T) {
	urls := neCoverURLs("http://p3.music.126.net/BBB==/2.jpg")
	if len(urls) == 0 {
		t.Fatal("no cover URLs")
	}
	// The track's own host first, at the largest size.
	want := "http://p3.music.126.net/BBB==/2.jpg" + neCoverQuery(neCoverSizes[0])
	if urls[0] != want {
		t.Fatalf("first = %q, want %q", urls[0], want)
	}
	// Every CDN host is tried, because the client rewrites the host per UI
	// surface and the cache may hold the file under any of them.
	for _, host := range neCoverHosts {
		found := false
		for _, u := range urls {
			if strings.Contains(u, "://"+host+"/") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no URL for host %s", host)
		}
	}
	// An existing query string is dropped, not appended to.
	for _, u := range neCoverURLs("http://p1.music.126.net/X==/3.jpg?param=200y200") {
		if strings.Count(u, "?") != 1 {
			t.Fatalf("query kept: %q", u)
		}
	}
	if neCoverURLs("  ") != nil {
		t.Error("empty picture URL produced candidates")
	}
}

func TestNESeconds(t *testing.T) {
	cases := map[float64]string{0: "0", 60: "60", 217.05: "217.05", 237.106666666667: "237.106666666667", -5: "0"}
	for in, want := range cases {
		if got := neSeconds(in); got != want {
			t.Errorf("neSeconds(%v) = %q, want %q", in, got, want)
		}
	}
	if nan := neSeconds(math.NaN()); nan != "0" {
		t.Errorf("neSeconds(NaN) = %q, want \"0\"", nan)
	}
}

func TestNESampleOK(t *testing.T) {
	track := neTrack{Duration: 237.106}
	cases := []struct {
		name  string
		pos   float64
		dur   float64
		tr    neTrack
		have  bool
		valid bool
	}{
		{"live sample", 217.05, 237.106, track, true, true},
		{"at the start", 0, 237.106, track, true, true},
		{"at the end", 237.1, 237.106, track, true, true},
		{"one second past the end", 238.2, 237.106, track, true, false},
		{"negative position", -1, 237.106, track, true, false},
		{"no duration", 10, 0, track, true, false},
		{"stale duration", 10, 100, track, true, false},
		{"no track to check", 10, 999, neTrack{}, false, true},
		{"track without duration", 10, 999, neTrack{Title: "x"}, true, true},
	}
	for _, c := range cases {
		if got := neSampleOK(c.pos, c.dur, c.tr, c.have); got != c.valid {
			t.Errorf("%s: neSampleOK = %v, want %v", c.name, got, c.valid)
		}
	}
}

func TestNPChoose(t *testing.T) {
	now := time.Now()
	smtcPlaying := NowPlayingState{Playing: true, Title: "Browser"}
	smtcPaused := NowPlayingState{Title: "Browser"}
	nePlaying := NowPlayingState{Playing: true, Title: "Client"}
	nePaused := NowPlayingState{Title: "Client"}

	cases := []struct {
		name string
		smtc npCandidate
		ne   npCandidate
		want string
		src  npSource
	}{
		{
			name: "a playing SMTC session outranks a paused client",
			smtc: npCandidate{st: smtcPlaying, ok: true, active: now.Add(-time.Minute)},
			ne:   npCandidate{st: nePaused, ok: true, active: now},
			want: "Browser", src: npSourceSMTC,
		},
		{
			name: "a playing client outranks a paused SMTC session",
			smtc: npCandidate{st: smtcPaused, ok: true, active: now},
			ne:   npCandidate{st: nePlaying, ok: true, active: now.Add(-time.Minute)},
			want: "Client", src: npSourceNetEase,
		},
		{
			name: "both paused: the one that moved last",
			smtc: npCandidate{st: smtcPaused, ok: true, active: now.Add(-time.Minute)},
			ne:   npCandidate{st: nePaused, ok: true, active: now},
			want: "Client", src: npSourceNetEase,
		},
		{
			name: "both paused: a fresher SMTC session wins",
			smtc: npCandidate{st: smtcPaused, ok: true, active: now},
			ne:   npCandidate{st: nePaused, ok: true, active: now.Add(-time.Minute)},
			want: "Browser", src: npSourceSMTC,
		},
		{
			name: "both paused and equally stale: the client wins the tie",
			smtc: npCandidate{st: smtcPaused, ok: true, active: now},
			ne:   npCandidate{st: nePaused, ok: true, active: now},
			want: "Client", src: npSourceNetEase,
		},
		{
			name: "only SMTC", smtc: npCandidate{st: smtcPaused, ok: true},
			want: "Browser", src: npSourceSMTC,
		},
		{
			name: "only the client", ne: npCandidate{st: nePaused, ok: true},
			want: "Client", src: npSourceNetEase,
		},
		{
			name: "nothing", want: "", src: npSourceNone,
		},
	}
	for _, c := range cases {
		st, src := npChoose(c.smtc, c.ne)
		if st.Title != c.want || src != c.src {
			t.Errorf("%s: got %q/%v, want %q/%v", c.name, st.Title, src, c.want, c.src)
		}
	}
}

// TestNEFixturesAreRedacted keeps the fixtures honest: they must not carry a
// real account name, playlist id or cover path from the test machine.
func TestNEFixturesAreRedacted(t *testing.T) {
	for _, fixture := range []string{neFMFixture, nePlayingListFixture} {
		var v any
		if err := json.Unmarshal([]byte(fixture), &v); err != nil {
			t.Fatalf("fixture is not JSON: %v", err)
		}
	}
}
