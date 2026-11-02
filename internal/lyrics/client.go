package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is LRCLIB's public API.
const DefaultBaseURL = "https://lrclib.net"

// Track identifies one song for a lyrics lookup. Duration is in seconds;
// LRCLIB matches on it, so a rounded value is expected.
type Track struct {
	Title    string
	Artist   string
	Album    string
	Duration int
}

// Result is what LRCLIB knows: the synced lyrics text, or nothing. None
// means the lookup completed and produced no synced lyrics (no match, only
// plain lyrics, or an instrumental); Instrumental is kept apart so the
// cache can say why.
type Result struct {
	Synced       string
	Instrumental bool
	None         bool
}

// Fetcher asks LRCLIB for a track. Client defaults to a five-second
// http.Client; BaseURL defaults to DefaultBaseURL. UserAgent is required by
// LRCLIB and should name the app and a contact URL.
type Fetcher struct {
	Client    *http.Client
	BaseURL   string
	UserAgent string
}

type apiTrack struct {
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
	Duration     float64 `json:"duration"`
}

// Fetch looks one track up. A 404 on the exact match falls back to a
// search for the closest track within three seconds. Only synced lyrics
// are used: plain lyrics and instrumentals answer None.
func (f Fetcher) Fetch(ctx context.Context, t Track) (Result, error) {
	title := strings.TrimSpace(t.Title)
	if title == "" {
		return Result{None: true}, nil
	}
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

	vals = url.Values{}
	vals.Set("track_name", title)
	if artist != "" {
		vals.Set("artist_name", artist)
	}
	items, err := f.search(ctx, base+"/api/search?"+vals.Encode())
	if err != nil {
		return Result{}, err
	}
	best := closest(items, t.Duration)
	if best == nil {
		return Result{None: true}, nil
	}
	return resultOf(*best), nil
}

// get reads one track; a 404 is an answer (status), not an error.
func (f Fetcher) get(ctx context.Context, u string) (apiTrack, int, error) {
	body, status, err := f.read(ctx, u)
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
	body, status, err := f.read(ctx, u)
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

func (f Fetcher) read(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
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
			return nil, resp.StatusCode, fmt.Errorf("lyrics: reading lrclib body: %w", err)
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
