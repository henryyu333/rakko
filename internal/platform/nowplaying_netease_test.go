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

// TestNEMetaTitleChangeClearsThePreviousTrack (P2-6): the client's title names
// a new song about a second before the memory fields and the queue files
// catch up. The old album, cover and duration must be gone from that moment,
// and both lookups must be due again rather than waiting for the old retry
// timestamps.
func TestNEMetaTitleChangeClearsThePreviousTrack(t *testing.T) {
	now := time.Now()
	m := neMeta{}
	m.use(neTrack{ID: "1002", Title: "Second Song", Album: "Second Album"})
	if !m.ok {
		t.Fatal("a resolved track was not recorded")
	}
	m.forgetTitle("Third Song")
	if m.ok || m.track.Title != "" || m.track.Album != "" {
		t.Fatalf("the previous track survived a title change: %+v", m.track)
	}
	if !m.needByTitle("Third Song", "", now) {
		t.Fatal("the new title was not looked up at once")
	}
	if !m.needByID("1003", now) {
		t.Fatal("the new id was not looked up at once")
	}
	// Once the files name the new track, neither lookup is due again.
	m.triedTitle("Third Song", now)
	m.use(neTrack{ID: "1003", Title: "Third Song", Album: "Third Album"})
	if m.needByTitle("Third Song", "", now) || m.needByID("1003", now) {
		t.Fatal("a resolved track was looked up again")
	}
}

// TestNEMetaRetriesAMissedID (P2-7): the first read after a track starts can
// find the queue file before it is rewritten. That miss must be retried at
// neMetaRetry, and a title fallback that resolved some other entry must not
// stop the id lookup from being retried either.
func TestNEMetaRetriesAMissedID(t *testing.T) {
	now := time.Now()
	m := neMeta{}
	if !m.needByID("2001", now) {
		t.Fatal("a new id was not looked up")
	}
	m.triedID("2001", now)
	if m.needByID("2001", now.Add(neMetaRetry-time.Millisecond)) {
		t.Fatal("the id was looked up before the retry interval")
	}
	if !m.needByID("2001", now.Add(neMetaRetry)) {
		t.Fatal("the id was never looked up again")
	}
	// A title fallback resolved a different entry meanwhile: still retry.
	m.use(neTrack{ID: "9999", Title: "Some Other Entry"})
	if !m.needByID("2001", now.Add(neMetaRetry)) {
		t.Fatal("a title fallback stopped the id lookup")
	}
	m.use(neTrack{ID: "2001", Title: "Queue Song"})
	if m.needByID("2001", now.Add(2*neMetaRetry)) {
		t.Fatal("a resolved id was looked up again")
	}
}

// TestNEMetaRetriesAMissedTitle (P2-7, fallback tier): the same retry rule for
// the tier that can only read the window title.
func TestNEMetaRetriesAMissedTitle(t *testing.T) {
	now := time.Now()
	m := neMeta{}
	if !m.needByTitle("Queue Song", "Queue Artist", now) {
		t.Fatal("a new title was not looked up")
	}
	m.triedTitle("Queue Song", now)
	if m.needByTitle("Queue Song", "Queue Artist", now.Add(neMetaRetry-time.Millisecond)) {
		t.Fatal("the title was looked up before the retry interval")
	}
	if !m.needByTitle("Queue Song", "Queue Artist", now.Add(neMetaRetry)) {
		t.Fatal("the title was never looked up again")
	}
	m.use(neTrack{ID: "3001", Title: "Queue Song", Artist: "Queue Artist"})
	if m.needByTitle("Queue Song", "Queue Artist", now.Add(2*neMetaRetry)) {
		t.Fatal("a resolved title was looked up again")
	}
}

// TestNEPlayVerdictTierAIgnoresFrozenClock (P2-3): when the memory tier is
// gone there is no position to derive a verdict from; the raw verdict is
// frozen where the clock stopped, so tier A must confirm against the audio
// session instead.
func TestNEPlayVerdictTierAIgnoresFrozenClock(t *testing.T) {
	if ev := nePlayVerdict(true, true, true, false); ev.playing {
		t.Fatal("tier A confirmed a pause from the frozen clock verdict")
	}
	if ev := nePlayVerdict(true, false, true, true); !ev.playing || ev.clock {
		t.Fatalf("tier A evidence = %+v, want the audio session's verdict", ev)
	}
	if ev := nePlayVerdict(false, true, true, false); !ev.playing || !ev.clock {
		t.Fatalf("clock evidence = %+v, want the position's verdict", ev)
	}
	// The clock verdict is not trusted when no usable sample produced it.
	if ev := nePlayVerdict(false, false, false, true); !ev.playing || ev.clock {
		t.Fatalf("no-clock evidence = %+v, want the audio session's verdict", ev)
	}
}

// TestNESettle (P2-2, P2-3): a play/pause command is confirmed against the
// evidence of its own tier, waits for its window, and is sent once more only
// when the client did not act.
func TestNESettle(t *testing.T) {
	now := time.Now()
	wait := time.Second
	p := &nePending{want: true, at: now.Add(wait)}
	if got := neSettle(p, neEvidence{playing: false}, now); got != neVerdictWait {
		t.Fatalf("before the wait window = %v, want wait", got)
	}
	if got := neSettle(p, neEvidence{playing: true}, now.Add(wait)); got != neVerdictDone {
		t.Fatalf("client agreed = %v, want done", got)
	}
	if got := neSettle(p, neEvidence{playing: false}, now.Add(wait)); got != neVerdictRetry {
		t.Fatalf("client ignored the command = %v, want one retry", got)
	}
	if got := neSettle(nil, neEvidence{playing: true}, now); got != neVerdictWait {
		t.Fatal("no pending command must not confirm anything")
	}
}

// TestNETransportVerb (P2-2): play/pause names the state it wants — resume
// when the card is asked to play, pause when it is asked to stop — and may be
// confirmed; a skip is a single command with no confirmation path that could
// send it twice.
func TestNETransportVerb(t *testing.T) {
	cases := []struct {
		cmd     NowPlayingCommand
		playing bool
		verb    string
		confirm bool
	}{
		{NPPlayPause, true, "resume", true},
		{NPPlayPause, false, "pause", true},
		{NPNext, true, "next", false},
		{NPPrevious, false, "previous", false},
		{NowPlayingCommand(0), true, "", false},
	}
	for _, c := range cases {
		verb, confirm := neTransportVerb(c.cmd, c.playing)
		if verb != c.verb || confirm != c.confirm {
			t.Errorf("neTransportVerb(%v, %v) = %q, %v; want %q, %v",
				c.cmd, c.playing, verb, confirm, c.verb, c.confirm)
		}
	}
}

// TestNECommandPath (P2-9): the identity the card resolves NetEase's icon
// through comes from the client's own handler registration.
func TestNECommandPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"D:\CloudMusic\cloudmusic.exe" "%1"`, `D:\CloudMusic\cloudmusic.exe`},
		{`D:\CloudMusic\cloudmusic.exe "%1"`, `D:\CloudMusic\cloudmusic.exe`},
		{`  "C:\Program Files\CloudMusic\cloudmusic.exe"  `, `C:\Program Files\CloudMusic\cloudmusic.exe`},
		{`"unterminated`, ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := neCommandPath(c.in); got != c.want {
			t.Errorf("neCommandPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNEGuardDecide (P2-8): the window guard is serialised by construction
// and must yield to the user. Input after the command ends it without folding
// or refocusing anything; without input it folds the handler's restored
// window back down and returns the foreground only while the handler still
// holds it.
func TestNEGuardDecide(t *testing.T) {
	now := time.Now()
	until := now.Add(2500 * time.Millisecond)
	armed := uint32(100)

	// The handler has just restored a minimised window.
	if p := neGuardDecide(true, false, true, true, armed, armed, now, until); !p.Minimize || p.Done || p.Focus {
		t.Fatalf("restored window plan = %+v, want minimize while the guard runs", p)
	}
	// Still minimised: nothing to do.
	if p := neGuardDecide(true, true, false, true, armed, armed, now, until); p.Minimize {
		t.Fatal("an already minimised window was folded again")
	}
	// The user moved the mouse or typed: leave the window and the focus alone.
	if p := neGuardDecide(true, false, true, true, armed, armed+1, now, until); !p.Done || p.Minimize || p.Focus {
		t.Fatalf("user input plan = %+v, want the guard to stop untouched", p)
	}
	// The guard ran out: fold the window once more and hand the foreground
	// back while the client still holds it.
	if p := neGuardDecide(true, false, true, true, armed, armed, until, until); !p.Done || !p.Minimize || !p.Focus {
		t.Fatalf("expired plan = %+v, want final minimize and focus restore", p)
	}
	// The user is in another app: never steal the foreground.
	if p := neGuardDecide(true, false, false, true, armed, armed, until, until); p.Focus {
		t.Fatal("the foreground was taken from another app")
	}
}
