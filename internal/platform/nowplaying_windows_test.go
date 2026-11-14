//go:build windows

package platform

// Now Playing checks: the pure parts always, and the live SMTC reader
// when a media session is playing on the desktop.

import (
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNowPlayingPositionExtrapolation(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	playing := NowPlayingState{Playing: true, Rate: 1, Elapsed: 30, InfoAt: base, Duration: 200}
	if got := playing.Position(base.Add(5 * time.Second)); got != 35 {
		t.Fatalf("playing position = %v, want 35", got)
	}
	paused := NowPlayingState{Playing: false, Rate: 0, Elapsed: 30, InfoAt: base, Duration: 200}
	if got := paused.Position(base.Add(5 * time.Second)); got != 30 {
		t.Fatalf("paused position = %v, want 30", got)
	}
	// Clamped to the track's end, and never negative.
	if got := playing.Position(base.Add(time.Hour)); got != 200 {
		t.Fatalf("position past the end = %v, want 200", got)
	}
	neg := NowPlayingState{Playing: true, Rate: 1, Elapsed: -5, InfoAt: base}
	if got := neg.Position(base); got != 0 {
		t.Fatalf("negative position = %v, want 0", got)
	}
	// A playing source with no rate still advances at 1x.
	noRate := NowPlayingState{Playing: true, Elapsed: 10, InfoAt: base}
	if got := noRate.Position(base.Add(2 * time.Second)); got != 12 {
		t.Fatalf("position without a rate = %v, want 12", got)
	}
}

// TestNowPlayingReadsThePublishedPlaybackRate pins the slots and the
// nullable value: the card must extrapolate at the player's own speed,
// and fall back to 1× only when the player publishes no usable rate.
func TestNowPlayingReadsThePublishedPlaybackRate(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int32
		rate     float64
		haveRate bool
		want     npPlayback
	}{
		{"playing at 2x", 4, 2, true, npPlayback{playing: true, rate: 2}},
		{"playing with no rate", 4, 0, false, npPlayback{playing: true, rate: 1}},
		{"playing at a zero rate", 4, 0, true, npPlayback{playing: true, rate: 1}},
		{"paused", 5, 1.5, true, npPlayback{playing: false, rate: 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := newFakeCOM()
			ref := newFakeCOM()
			info.slot[7] = windows.NewCallback(func(_, out uintptr) uintptr {
				*(*int32)(wiPtr(out)) = tt.status
				return 0
			})
			if tt.haveRate {
				info.slot[10] = windows.NewCallback(func(_, out uintptr) uintptr {
					*(*unsafe.Pointer)(wiPtr(out)) = unsafe.Pointer(ref)
					return 0
				})
				ref.slot[6] = windows.NewCallback(func(_, out uintptr) uintptr {
					*(*float64)(wiPtr(out)) = tt.rate
					return 0
				})
			} else {
				info.slot[10] = windows.NewCallback(func(_, out uintptr) uintptr {
					*(*unsafe.Pointer)(wiPtr(out)) = nil
					return 0
				})
			}
			got, ok := npReadPlayback(unsafe.Pointer(info))
			if !ok {
				t.Fatal("the playback info could not be read")
			}
			if got != tt.want {
				t.Fatalf("playback = %+v, want %+v", got, tt.want)
			}
			if tt.want.playing && tt.haveRate && ref.releases.Load() != 1 {
				t.Fatalf("the rate reference was released %d times, want once", ref.releases.Load())
			}
		})
	}
}

// TestNowPlayingTimelineAnchorsAtItsUpdateTime drives the timeline slots
// and the conversion of the published stamp: the position is extrapolated
// from when the player last updated it, not from when Rakko polled.
func TestNowPlayingTimelineAnchorsAtItsUpdateTime(t *testing.T) {
	const ticksPerSecond = 10_000_000
	published := time.Unix(1_700_000_000, 0).UTC()
	tl := newFakeCOM()
	for slot, v := range map[int]int64{
		6:  0,                          // start
		7:  200 * ticksPerSecond,       // end
		10: 30 * ticksPerSecond,        // position
		11: filetimeUnixBase + published.Unix()*ticksPerSecond, // last update
	} {
		tl.slot[slot] = windows.NewCallback(func(_, out uintptr) uintptr {
			*(*int64)(wiPtr(out)) = v
			return 0
		})
	}
	got, ok := npReadTimeline(unsafe.Pointer(tl))
	if !ok {
		t.Fatal("the timeline could not be read")
	}
	if got.position != 30 || got.duration != 200 {
		t.Fatalf("timeline position %v duration %v, want 30 and 200", got.position, got.duration)
	}
	if !got.updated.Equal(published) {
		t.Fatalf("timeline update %v, want %v", got.updated, published)
	}
	// Ten seconds of wall time after the update is ten seconds of track at
	// 1x, computed from the stamp rather than from the poll.
	st := NowPlayingState{Playing: true, Rate: 1, Elapsed: got.position, InfoAt: got.updated, Duration: got.duration}
	if pos := st.Position(published.Add(10 * time.Second)); pos != 40 {
		t.Fatalf("position ten seconds after the update = %v, want 40", pos)
	}
	// A stamp that is not a real time degrades to the zero time.
	for _, ticks := range []int64{0, -1, filetimeUnixBase} {
		if !npDateTime(ticks).IsZero() {
			t.Errorf("npDateTime(%d) = %v, want the zero time", ticks, npDateTime(ticks))
		}
	}
	if one := npDateTime(filetimeUnixBase + ticksPerSecond); !one.Equal(time.Unix(1, 0).UTC()) {
		t.Errorf("one second after the epoch = %v", one)
	}
}

func TestNowPlayingArtIDTracksTheTrack(t *testing.T) {
	a := npArtID("app", "Song", "Artist", "Album")
	if a == "" || a != npArtID("app", "Song", "Artist", "Album") {
		t.Fatal("the same track must produce the same artwork id")
	}
	if a == npArtID("app", "Song 2", "Artist", "Album") {
		t.Fatal("a different title must produce a different artwork id")
	}
	if npArtID("app", "", "Artist", "Album") != "" {
		t.Fatal("a nameless track must have no artwork id")
	}
}

// TestNowPlayingHostReadsTheSystemSession runs against a live session;
// with nothing playing it skips, and the desk evidence run starts a player
// first so the same test really reads one.
func TestNowPlayingHostReadsTheSystemSession(t *testing.T) {
	h := NewNowPlayingHost()
	if err := h.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Stop()
	deadline := time.Now().Add(6 * time.Second)
	var st NowPlayingState
	var have bool
	for time.Now().Before(deadline) {
		if st, have = h.State(); have && st.Title != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !have || st.Title == "" {
		t.Skip("no media session is playing on this desktop")
	}
	t.Logf("session: app=%q title=%q artist=%q album=%q playing=%v duration=%.1fs elapsed=%.1fs art=%dB artMissing=%v",
		st.App, st.Title, st.Artist, st.Album, st.Playing, st.Duration, st.Elapsed, len(st.Art), st.ArtMissing)
	if st.App == "" {
		t.Fatal("the session names no app")
	}
	if st.Duration < 0 {
		t.Fatalf("duration = %v", st.Duration)
	}
	if h.Revision() == 0 {
		t.Fatal("the host published a state without bumping its revision")
	}
	select {
	case <-h.Updates():
	default:
		// The token may already have been consumed by the wait above.
	}
	if !h.Dead() {
		t.Log("host is alive as expected")
	}
	// A paused or playing session must be one of the two; the state struct
	// carries no third answer.
	if !st.Playing && st.Rate != 0 {
		t.Fatalf("a paused track has rate %v", st.Rate)
	}
}

// TestNowPlayingControlsTheSession drives play/pause and a seek against
// the live player. It is opt-in: the run changes what the machine plays,
// so the desk evidence sets RAKKO_NP_CONTROL=1 for it.
func TestNowPlayingControlsTheSession(t *testing.T) {
	if os.Getenv("RAKKO_NP_CONTROL") == "" {
		t.Skip("set RAKKO_NP_CONTROL=1 to drive the playing app")
	}
	h := NewNowPlayingHost()
	if err := h.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Stop()
	st, ok := waitForTrack(t, h, 6*time.Second)
	if !ok {
		t.Skip("no media session is playing on this desktop")
	}
	if !st.Playing {
		t.Skip("the session is paused; nothing to pause")
	}
	h.Send(NPPlayPause)
	if paused, ok := waitForPlaying(t, h, false, 4*time.Second); !ok {
		t.Fatalf("pause did not take effect; last state %+v", paused)
	}
	h.Send(NPPlayPause)
	if resumed, ok := waitForPlaying(t, h, true, 4*time.Second); !ok {
		t.Fatalf("play did not take effect; last state %+v", resumed)
	}
	if st.Duration > 20 {
		h.Seek(5)
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			cur, _ := h.State()
			if cur.Elapsed >= 4 && cur.Elapsed <= 12 {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("seek did not move the position: %+v", mustState(h))
	}
}

func waitForTrack(t *testing.T, h *NowPlayingHost, d time.Duration) (NowPlayingState, bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if st, ok := h.State(); ok && st.Title != "" {
			return st, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return NowPlayingState{}, false
}

func waitForPlaying(t *testing.T, h *NowPlayingHost, want bool, d time.Duration) (NowPlayingState, bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	st, ok := h.State()
	for time.Now().Before(deadline) {
		st, ok = h.State()
		if ok && st.Playing == want {
			return st, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return st, false
}

func mustState(h *NowPlayingHost) NowPlayingState {
	st, _ := h.State()
	return st
}
