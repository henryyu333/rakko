package platform

// The NetEase Cloud Music source for the Windows now-playing host has two
// halves. This file is the half that is pure data — the shapes of the client's
// queue files, the window title's "song - artist" form, the name of the
// client's own cover cache entry, the sanity check applied to every memory
// sample and the rule that merges this source with SMTC. None of it touches
// Windows, so it is tested on any machine, and the Mac build only carries the
// declarations it never calls.
//
// The Win32 half (window, process memory, image files, the orpheus://
// protocol, the audio-session fallback) is nowplaying_netease_windows.go.

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// neApp is the App field of a NetEase snapshot: the bundle-id equivalent the
// card keys its app icon on.
const neApp = "netease"

// neTrack is one song as the client's queue files describe it, in the units
// the card wants (seconds, not the client's milliseconds).
type neTrack struct {
	ID       string
	Title    string
	Artist   string
	Album    string
	PicURL   string
	Duration float64
}

// neSplitTitle splits the client's window title, "<song> - <artist>". The
// title carries no state and no app suffix; a song whose name contains " - "
// splits at its first separator, which is why the queue files (joined by the
// song id) are preferred whenever they can name the track.
func neSplitTitle(s string) (title, artist string) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " - "); i >= 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len(" - "):])
	}
	return s, ""
}

// neID accepts the client's song ids in either spelling: the queue files
// write them as strings today, and a catalogue that starts writing numbers
// must not take the whole file down with it.
type neID string

func (id *neID) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*id = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*id = neID(s)
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*id = neID(strconv.FormatInt(int64(f), 10))
	return nil
}

// neQueueTrack is the subset of a queue entry this source needs. The client
// writes dozens of other fields; encoding/json ignores them.
type neQueueTrack struct {
	ID       neID    `json:"id"`
	Name     string  `json:"name"`
	Duration float64 `json:"duration"` // milliseconds
	Artists  []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Name   string `json:"name"`
		PicURL string `json:"picUrl"`
	} `json:"album"`
}

// neFMPlay is webdata\file\fmPlay: the 私人FM queue, the only queue file that
// names its current entry.
type neFMPlay struct {
	CurrentIndex int            `json:"currentIndex"`
	Queue        []neQueueTrack `json:"queue"`
}

// nePlayingList is webdata\file\playingList: the normal play queue. It has no
// current index, so the song id read from the client is what finds the track.
type nePlayingList struct {
	List []struct {
		Track neQueueTrack `json:"track"`
	} `json:"list"`
}

func (t neQueueTrack) track() neTrack {
	names := make([]string, 0, len(t.Artists))
	for _, a := range t.Artists {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	dur := 0.0
	if t.Duration > 0 {
		dur = t.Duration / 1000
	}
	return neTrack{
		ID:       string(t.ID),
		Title:    strings.TrimSpace(t.Name),
		Artist:   strings.Join(names, " / "),
		Album:    strings.TrimSpace(t.Album.Name),
		PicURL:   strings.TrimSpace(t.Album.PicURL),
		Duration: dur,
	}
}

func neTracks(ts []neQueueTrack) []neTrack {
	out := make([]neTrack, 0, len(ts))
	for _, t := range ts {
		if tr := t.track(); tr.ID != "" || tr.Title != "" {
			out = append(out, tr)
		}
	}
	return out
}

// neParseFMPlay reads the FM queue: its current entry when the index is in
// range, and every entry for lookups.
func neParseFMPlay(data []byte) (cur neTrack, tracks []neTrack, ok bool) {
	var q neFMPlay
	if err := json.Unmarshal(data, &q); err != nil {
		return neTrack{}, nil, false
	}
	tracks = neTracks(q.Queue)
	if q.CurrentIndex >= 0 && q.CurrentIndex < len(q.Queue) {
		cur = q.Queue[q.CurrentIndex].track()
	}
	return cur, tracks, true
}

// neParsePlayingList reads the normal queue's tracks.
func neParsePlayingList(data []byte) (tracks []neTrack, ok bool) {
	var q nePlayingList
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, false
	}
	ts := make([]neQueueTrack, 0, len(q.List))
	for _, e := range q.List {
		ts = append(ts, e.Track)
	}
	return neTracks(ts), true
}

// neFindTrack finds the track with the given song id.
func neFindTrack(tracks []neTrack, id string) (neTrack, bool) {
	if id == "" {
		return neTrack{}, false
	}
	for _, t := range tracks {
		if t.ID == id {
			return t, true
		}
	}
	return neTrack{}, false
}

// neFindTitled finds the track the window title names, for the fallback tier
// that cannot read the song id. Comparing both halves keeps a title match from
// picking the wrong entry of a queue that holds the same song twice.
func neFindTitled(tracks []neTrack, title, artist string) (neTrack, bool) {
	title, artist = strings.TrimSpace(title), strings.TrimSpace(artist)
	if title == "" {
		return neTrack{}, false
	}
	for _, t := range tracks {
		if t.Title != title {
			continue
		}
		if artist == "" || t.Artist == artist {
			return t, true
		}
	}
	return neTrack{}, false
}

// neCoverSizes are the thumbnail sizes the client asks the image CDN for, in
// the order this source prefers them: the largest the client has usually
// cached is still small, and any that is missing falls through to the next.
var neCoverSizes = []int{336, 283, 214, 142, 108}

// neCoverQuery is the query the client appends to a cover URL before caching
// it. The cache is keyed by the md5 of the whole string, query included, so
// this must stay byte-identical to what the client sends.
func neCoverQuery(size int) string {
	return "?imageView=&thumbnail=" + strconv.Itoa(size) + "y" + strconv.Itoa(size) + "&type=jpg&rotate=0&tostatic=0"
}

// neCoverHosts are the CDN hosts that serve the same content-addressed image;
// the client rewrites the host per UI surface, so a cached file may have been
// fetched from any of them.
var neCoverHosts = []string{"p1.music.126.net", "p2.music.126.net", "p3.music.126.net", "p4.music.126.net"}

// neCoverURLs lists the cache URLs that could hold picURL, most preferred
// first: every size at the track's own host, then the same sizes at the other
// CDN hosts.
func neCoverURLs(picURL string) []string {
	picURL = strings.TrimSpace(picURL)
	if picURL == "" {
		return nil
	}
	base := picURL
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base = base[:i]
	}
	hosts := []string{""}
	if u, err := parseCoverURL(base); err == nil {
		hosts[0] = u.host
		for _, h := range neCoverHosts {
			if h != u.host {
				hosts = append(hosts, h)
			}
		}
	}
	out := make([]string, 0, len(hosts)*len(neCoverSizes))
	for _, host := range hosts {
		u := base
		if host != "" {
			u = swapHost(base, host)
		}
		for _, size := range neCoverSizes {
			out = append(out, u+neCoverQuery(size))
		}
	}
	return out
}

type coverParts struct {
	scheme string
	host   string
	path   string
}

func parseCoverURL(s string) (coverParts, error) {
	var p coverParts
	i := strings.Index(s, "://")
	if i < 0 {
		return p, errCoverURL
	}
	p.scheme = s[:i]
	rest := s[i+3:]
	j := strings.IndexByte(rest, '/')
	if j < 0 {
		return p, errCoverURL
	}
	p.host = rest[:j]
	p.path = rest[j:]
	if p.host == "" || p.path == "" {
		return p, errCoverURL
	}
	return p, nil
}

func swapHost(s, host string) string {
	p, err := parseCoverURL(s)
	if err != nil {
		return s
	}
	return p.scheme + "://" + host + p.path
}

var errCoverURL = errors.New("platform: not a cover URL")

// neCoverHash is the cache file name for one cover URL: the md5 of the whole
// URL in lower-case hex, which is exactly what the client names the file.
func neCoverHash(url string) string {
	sum := md5.Sum([]byte(url))
	return hex.EncodeToString(sum[:])
}

func neAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// neSampleOK reports whether a memory sample can be believed for the track the
// card is about to draw. The offsets are per client version, so a sample that
// disagrees with the track the queue files name is a stale or moved field, not
// a playback state.
func neSampleOK(pos, dur float64, track neTrack, haveTrack bool) bool {
	if !(dur > 1) || !(pos >= 0) || pos > dur+1 {
		return false
	}
	if haveTrack && track.Duration > 0 && neAbs(dur-track.Duration) > 2 {
		return false
	}
	return true
}

// neSeconds formats a position the way the orpheus:// protocol wants it: no
// exponent, no trailing zeros.
func neSeconds(v float64) string {
	if v < 0 || v != v {
		v = 0
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// npSource says which app a snapshot came from, so a transport command goes
// back to the same one.
type npSource int

const (
	npSourceNone npSource = iota
	npSourceSMTC
	npSourceNetEase
)

// npCandidate is one source's snapshot plus when it was last seen doing
// something (playing, or moving its position).
type npCandidate struct {
	st     NowPlayingState
	ok     bool
	active time.Time
}

// npChoose merges the two sources into the card's one snapshot. A source that
// is playing always wins; otherwise the one that was active last keeps the
// card, with NetEase winning a tie because it is the app the user runs on
// purpose rather than a browser tab left paused in the background.
func npChoose(smtc, ne npCandidate) (NowPlayingState, npSource) {
	switch {
	case smtc.ok && smtc.st.Playing:
		return smtc.st, npSourceSMTC
	case ne.ok && ne.st.Playing:
		return ne.st, npSourceNetEase
	case smtc.ok && ne.ok:
		if smtc.active.After(ne.active) {
			return smtc.st, npSourceSMTC
		}
		return ne.st, npSourceNetEase
	case smtc.ok:
		return smtc.st, npSourceSMTC
	case ne.ok:
		return ne.st, npSourceNetEase
	}
	return NowPlayingState{}, npSourceNone
}
