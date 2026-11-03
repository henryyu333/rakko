package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// DefaultNeteaseBaseURL is NetEase Cloud Music's public web API. It is the
// second source: LRCLIB covers the community transcriptions, NetEase the
// Chinese catalog LRCLIB lacks.
const DefaultNeteaseBaseURL = "https://music.163.com"

// neteaseUserAgent is an ordinary browser's, which the public endpoint
// expects; a custom app agent is refused or answered with an empty list.
const neteaseUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

type neteaseArtist struct {
	Name string `json:"name"`
}

type neteaseAlbum struct {
	Name string `json:"name"`
}

// neteaseSong is one search hit. Duration is in milliseconds.
type neteaseSong struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Artists  []neteaseArtist `json:"artists"`
	Album    neteaseAlbum    `json:"album"`
	Duration int64           `json:"duration"`
}

type neteaseSearchAnswer struct {
	Result struct {
		Songs []neteaseSong `json:"songs"`
	} `json:"result"`
}

type neteaseLyricAnswer struct {
	LRC struct {
		Lyric string `json:"lyric"`
	} `json:"lrc"`
	NoLyric     bool `json:"nolyric"`
	Uncollected bool `json:"uncollected"`
}

// fetchNetease looks a track up at NetEase: search for the title and
// artist, pick the closest candidate, then read its LRC. A miss is None;
// only a failed request is an error.
func (f Fetcher) fetchNetease(ctx context.Context, t Track) (Result, error) {
	base := strings.TrimRight(f.NeteaseBaseURL, "/")
	if base == "" {
		base = DefaultNeteaseBaseURL
	}
	query := strings.TrimSpace(strings.TrimSpace(t.Title) + " " + strings.TrimSpace(t.Artist))
	vals := url.Values{}
	vals.Set("s", query)
	vals.Set("type", "1")
	vals.Set("limit", "10")
	body, status, err := f.readUA(ctx, base+"/api/search/get/web?"+vals.Encode(), neteaseUserAgent)
	if err != nil {
		return Result{}, fmt.Errorf("lyrics: netease search: %w", err)
	}
	if status != 200 {
		return Result{}, fmt.Errorf("lyrics: netease search answered %d", status)
	}
	var answer neteaseSearchAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return Result{}, fmt.Errorf("lyrics: reading netease search: %w", err)
	}
	song := neteaseClosest(answer.Result.Songs, t)
	if song == nil {
		return Result{None: true}, nil
	}
	vals = url.Values{}
	vals.Set("id", strconv.FormatInt(song.ID, 10))
	vals.Set("lv", "1")
	vals.Set("tv", "-1")
	body, status, err = f.readUA(ctx, base+"/api/song/lyric?"+vals.Encode(), neteaseUserAgent)
	if err != nil {
		return Result{}, fmt.Errorf("lyrics: netease lyric: %w", err)
	}
	if status != 200 {
		return Result{}, fmt.Errorf("lyrics: netease lyric answered %d", status)
	}
	var lyric neteaseLyricAnswer
	if err := json.Unmarshal(body, &lyric); err != nil {
		return Result{}, fmt.Errorf("lyrics: reading netease lyric: %w", err)
	}
	return neteaseResult(lyric), nil
}

// neteaseResult reads one lyric answer: an instrumental has no words to
// time, an uncollected or empty answer is a miss.
func neteaseResult(a neteaseLyricAnswer) Result {
	if a.Uncollected {
		return Result{None: true}
	}
	text := strings.TrimSpace(a.LRC.Lyric)
	if a.NoLyric || text == "" {
		return Result{Instrumental: true, None: true}
	}
	// NetEase marks pure music with placeholder words instead of a flag.
	if strings.Contains(text, "纯音乐") && !strings.Contains(text, "\n") {
		return Result{Instrumental: true, None: true}
	}
	return Result{Synced: a.LRC.Lyric}
}

// neteaseClosest picks the candidate to read: an artist name must match,
// and the length must be within three seconds. With a match on both, the
// same album wins over a nearer length. A zero want (a live stream) takes
// the first artist match.
func neteaseClosest(songs []neteaseSong, t Track) *neteaseSong {
	artist := normText(t.Artist)
	album := normText(t.Album)
	var best *neteaseSong
	bestAlbum := false
	bestDiff := math.Inf(1)
	for i := range songs {
		s := &songs[i]
		if artist != "" && !hasArtist(s, artist) {
			continue
		}
		if t.Duration <= 0 {
			return s
		}
		diff := math.Abs(float64(s.Duration)/1000 - float64(t.Duration))
		if diff > 3 {
			continue
		}
		sameAlbum := album != "" && normText(s.Album.Name) == album
		switch {
		case best == nil, sameAlbum && !bestAlbum, sameAlbum == bestAlbum && diff < bestDiff:
			best, bestAlbum, bestDiff = s, sameAlbum, diff
		}
	}
	return best
}

// hasArtist reports whether any credit matches the normalised name.
func hasArtist(s *neteaseSong, name string) bool {
	for _, a := range s.Artists {
		if normText(a.Name) == name {
			return true
		}
	}
	return false
}

// normText is the comparison form of a name: case-folded, with runs of
// whitespace collapsed, the same folding Key uses.
func normText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
