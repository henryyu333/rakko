package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"rakko/internal/netproxy"
)

// DefaultBaseURL is LRCLIB's public API.
const DefaultBaseURL = "https://lrclib.net"

// lyricsHTTPTimeout bounds one lookup when Fetcher.Client is nil.
const lyricsHTTPTimeout = 5 * time.Second

// Track identifies one song for a lyrics lookup. Duration is in seconds;
// LRCLIB matches on it, so a rounded value is expected.
type Track struct {
	Title    string
	Artist   string
	Album    string
	Duration int
}

// Result is what a service knows: the synced lyrics text, or nothing. None
// means the lookup completed and produced no synced lyrics (no match, only
// plain lyrics, or an instrumental); Instrumental is kept apart so the
// cache can say why.
type Result struct {
	Synced       string
	Instrumental bool
	None         bool
}

// Fetcher asks LRCLIB, then NetEase Cloud Music, for a track. Client
// defaults to a five-second http.Client; BaseURL and NeteaseBaseURL
// default to the services' public addresses. UserAgent is required by
// LRCLIB and should name the app and a contact URL; NetEase is asked with
// a browser's.
type Fetcher struct {
	Client         *http.Client
	BaseURL        string
	NeteaseBaseURL string
	UserAgent      string
}

type apiTrack struct {
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
	Duration     float64 `json:"duration"`
}

// Fetch looks one track up. LRCLIB answers first, with the exact match,
// then a search, then a search of the title without its trailing version
// tag; NetEase answers when LRCLIB has an error or no synced lyrics. Both
// services missing the song is None; only both failing is an error.
func (f Fetcher) Fetch(ctx context.Context, t Track) (Result, error) {
	if strings.TrimSpace(t.Title) == "" {
		return Result{None: true}, nil
	}
	lr, lerr := f.fetchLRCLIB(ctx, t)
	if lerr == nil && !lr.None {
		return lr, nil
	}
	nr, nerr := f.fetchNetease(ctx, t)
	switch {
	case nerr == nil && !nr.None:
		return nr, nil
	case nerr == nil:
		// NetEase agrees there are no words; keep LRCLIB's reason if it
		// had one.
		if lerr == nil {
			return lr, nil
		}
		return nr, nil
	case lerr == nil:
		// LRCLIB answered; NetEase only failed on the way.
		return lr, nil
	default:
		return Result{}, fmt.Errorf("lyrics: lrclib: %v; netease: %w", lerr, nerr)
	}
}

// fetchLRCLIB asks LRCLIB: the exact track, a search for it, and a search
// for the bare title when the version tag hid the recording. Only synced
// lyrics are used: plain lyrics and instrumentals answer None.
func (f Fetcher) fetchLRCLIB(ctx context.Context, t Track) (Result, error) {
	title := strings.TrimSpace(t.Title)
	artist := strings.TrimSpace(t.Artist)
	base := strings.TrimRight(f.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	vals := url.Values{}
	vals.Set("track_name", title)
	if artist != "" {
		vals.Set("artist_name", artist)
	}
	if album := strings.TrimSpace(t.Album); album != "" {
		vals.Set("album_name", album)
	}
	vals.Set("duration", strconv.Itoa(t.Duration))
	api, status, err := f.get(ctx, base+"/api/get?"+vals.Encode())
	if err != nil {
		return Result{}, err
	}
	switch status {
	case http.StatusOK:
		return resultOf(api), nil
	case http.StatusNotFound:
		// Fall through to the search.
	default:
		return Result{}, fmt.Errorf("lyrics: lrclib answered %d", status)
	}

	names := []string{title}
	if bare := baseTitle(title); bare != "" && bare != title {
		names = append(names, bare)
	}
	for _, name := range names {
		vals = url.Values{}
		vals.Set("track_name", name)
		if artist != "" {
			vals.Set("artist_name", artist)
		}
		items, err := f.search(ctx, base+"/api/search?"+vals.Encode())
		if err != nil {
			return Result{}, err
		}
		if best := closest(items, t.Duration); best != nil {
			return resultOf(*best), nil
		}
	}
	return Result{None: true}, nil
}

// get reads one track; a 404 is an answer (status), not an error.
func (f Fetcher) get(ctx context.Context, u string) (apiTrack, int, error) {
	body, status, err := f.readRetry(ctx, u)
	if err != nil || status != http.StatusOK {
		return apiTrack{}, status, err
	}
	var t apiTrack
	if err := json.Unmarshal(body, &t); err != nil {
		return apiTrack{}, status, fmt.Errorf("lyrics: reading lrclib answer: %w", err)
	}
	return t, status, nil
}

// search reads the search list; a 404 is an empty list.
func (f Fetcher) search(ctx context.Context, u string) ([]apiTrack, error) {
	body, status, err := f.readRetry(ctx, u)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("lyrics: lrclib search answered %d", status)
	}
	var out []apiTrack
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("lyrics: reading lrclib search: %w", err)
	}
	return out, nil
}

// readRetry reads one LRCLIB answer, retrying an overloaded or failed
// service twice with a short backoff. A 404 and the other definite answers
// come back at once; the caller's deadline bounds the whole retry.
func (f Fetcher) readRetry(ctx context.Context, u string) ([]byte, int, error) {
	const attempts = 3
	var body []byte
	var status int
	var err error
	for i := range attempts {
		body, status, err = f.read(ctx, u)
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		if err == nil && !retryableStatus(status) {
			return body, status, nil
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(time.Duration(150*(i+1)) * time.Millisecond):
		}
	}
	return body, status, err
}

// retryableStatus is a transient answer: the service is busy or broken.
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func (f Fetcher) read(ctx context.Context, u string) ([]byte, int, error) {
	return f.readUA(ctx, u, f.UserAgent)
}

func (f Fetcher) readUA(ctx context.Context, u, userAgent string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	client := f.Client
	if client == nil {
		// The production path: one client over the app's proxy rules, so a
		// Finder-launched app with no proxy variables still reaches LRCLIB
		// through the macOS system proxy.
		client = netproxy.NewClient(lyricsHTTPTimeout)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var body json.RawMessage
	// A lyrics answer is a few kilobytes; never read more than a bound.
	dec := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err := dec.Decode(&body); err != nil {
		if resp.StatusCode == http.StatusOK {
			return nil, resp.StatusCode, fmt.Errorf("lyrics: reading answer: %w", err)
		}
		// An error page may not be JSON; the status is the answer.
		return nil, resp.StatusCode, nil
	}
	return body, resp.StatusCode, nil
}

// closest picks the track with the duration nearest want, within three
// seconds. A zero want (a live stream) takes the search's first entry; a
// result whose duration is unknown is not allowed to win on a guess.
func closest(items []apiTrack, want int) *apiTrack {
	if want <= 0 {
		// A live stream names no length: take the search's first ranked hit.
		for i := range items {
			return &items[i]
		}
		return nil
	}
	var best *apiTrack
	bestDiff := math.Inf(1)
	for i := range items {
		it := &items[i]
		if it.Duration <= 0 {
			continue
		}
		diff := math.Abs(it.Duration - float64(want))
		if diff > 3 {
			continue
		}
		if diff < bestDiff {
			best, bestDiff = it, diff
		}
	}
	return best
}

// resultOf turns an LRCLIB answer into a Result: instrumental tracks and
// tracks with only plain lyrics produce None.
func resultOf(t apiTrack) Result {
	if t.Instrumental {
		return Result{Instrumental: true, None: true}
	}
	if synced := strings.TrimSpace(t.SyncedLyrics); synced != "" {
		return Result{Synced: synced}
	}
	return Result{None: true}
}

// titleTail is a trailing version tag: "(Live)", "（伴奏）", "[Remix]",
// "【电影版】". Several may follow one another.
var titleTail = regexp.MustCompile(`\s*[(（\[【][^)）\]】]*[)）\]】]\s*$`)

// baseTitle drops the version tags a player reports and the title's
// trailing explanation (" - 电影《…》插曲"): the bare name LRCLIB's search
// knows, while the full title stays for the exact match.
func baseTitle(title string) string {
	s := strings.TrimSpace(title)
	if i := strings.Index(s, " - "); i > 0 {
		s = s[:i]
	}
	for {
		next := strings.TrimSpace(titleTail.ReplaceAllString(s, ""))
		if next == s || next == "" {
			break
		}
		s = next
	}
	if s == "" {
		return strings.TrimSpace(title)
	}
	return s
}
