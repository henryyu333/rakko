//go:build windows

// The NetEase source's play-state derivation, driven directly: it is the one
// piece of the Windows half that is pure data. Everything else there is Win32
// (a window, a process, files) and is exercised against the real client.

package platform

import (
	"testing"
	"time"
)

// step feeds one position sample a poll interval after the previous one.
func step(s *neSource, at time.Time, pos float64) bool {
	return s.moved(pos, at)
}

func TestNEMovedAdvancing(t *testing.T) {
	s := &neSource{}
	base := time.Now()
	// A first sample only records where the track is.
	if got := step(s, base, 100); got {
		t.Fatal("first sample claimed playback")
	}
	// 1× realtime over a 350 ms poll.
	if !step(s, base.Add(350*time.Millisecond), 100.35) {
		t.Fatal("an advancing position was not read as playing")
	}
	if !step(s, base.Add(700*time.Millisecond), 100.71) {
		t.Fatal("playback stopped being detected")
	}
}

func TestNEMovedFrozen(t *testing.T) {
	s := &neSource{playing: true}
	base := time.Now()
	step(s, base, 207.5)
	// One frozen sample is not a pause: the client may still be rendering
	// the tick that follows.
	if !step(s, base.Add(350*time.Millisecond), 207.5) {
		t.Fatal("one frozen sample ended playback immediately")
	}
	if step(s, base.Add(700*time.Millisecond), 207.5) {
		t.Fatal("two frozen samples did not end playback")
	}
	if s.still != 2 {
		t.Fatalf("still = %d, want 2", s.still)
	}
	// Movement resumes playback.
	if !step(s, base.Add(1050*time.Millisecond), 207.86) {
		t.Fatal("movement did not resume playback")
	}
	if s.still != 0 {
		t.Fatalf("still = %d after movement, want 0", s.still)
	}
}

func TestNEMovedSeekKeepsState(t *testing.T) {
	s := &neSource{playing: false}
	base := time.Now()
	step(s, base, 10)
	// A jump forward while paused is a seek, not playback.
	if step(s, base.Add(350*time.Millisecond), 200) {
		t.Fatal("a seek was read as playback")
	}
	// A jump backward likewise.
	s2 := &neSource{playing: true}
	step(s2, base, 200)
	if !step(s2, base.Add(350*time.Millisecond), 5) {
		t.Fatal("a backward seek lost the playing state")
	}
}

func TestNEMovedHintHoldsTheCommand(t *testing.T) {
	base := time.Now()
	s := &neSource{playing: false, hintPlay: true, hintUntil: base.Add(neSeekGrace)}
	step(s, base, 30)
	// The protocol has no toggle, so the card's optimistic flip must hold
	// until the client's position agrees; a buffering pause right after
	// "resume" must not flip it back.
	if !s.moved(30, base.Add(350*time.Millisecond)) {
		t.Fatal("the resume hint was not held")
	}
	// After the hint expires, the position decides again.
	if s.moved(30, base.Add(neSeekGrace+time.Millisecond)) {
		t.Fatal("a frozen position after the hint still reported playback")
	}
}

func TestNEMovedSlowClock(t *testing.T) {
	s := &neSource{playing: true}
	base := time.Now()
	step(s, base, 50)
	// A position that advances far slower than realtime is a stalled stream,
	// not real playback: the sample is dropped and the state is kept.
	if !step(s, base.Add(time.Second), 50.05) {
		t.Fatal("a stalled position ended playback")
	}
	// A wildly fast one (a broken field) is likewise not playback.
	s2 := &neSource{playing: false}
	step(s2, base, 0)
	if step(s2, base.Add(350*time.Millisecond), 300) {
		t.Fatal("an impossible jump was read as playback")
	}
}

func TestNESourceCloseOnEmptyHandles(t *testing.T) {
	// The host calls both on every restart, including before the client was
	// ever found.
	s := &neSource{}
	if s.hasTrack() {
		t.Fatal("an unattached source claims a track")
	}
	s.close()
	s.close()
}

func TestNEAdvancingIgnoresTheCommandHint(t *testing.T) {
	base := time.Now()
	s := &neSource{playing: true}
	step(s, base, 10)
	step(s, base.Add(350*time.Millisecond), 10.35)
	// A command asks for "paused" and holds that verdict for the card...
	s.hintPlay, s.hintUntil = false, base.Add(2*time.Second)
	// ...while the raw verdict is still "the position is moving".
	if got := s.moved(10.35, base.Add(700*time.Millisecond)); got {
		t.Fatal("the hint was not shown")
	}
	if !s.rawPlaying {
		t.Fatal("the raw verdict lost the movement")
	}
}

// TestNECloseForgetsTheRun (P2-4): a client that quit and is started again is
// a new process with its own version, handle and tier. Nothing from the old
// run may survive close, or the restarted client would stay in tier A.
func TestNECloseForgetsTheRun(t *testing.T) {
	now := time.Now()
	s := &neSource{
		hwnd:        123,
		pid:         456,
		exe:         `D:\CloudMusic\cloudmusic.exe`,
		base:        0x1000,
		offsets:     &neKnownOffsets[0],
		tierA:       true,
		attachAt:    now,
		title:       "Song - Artist",
		id:          "1002",
		idAt:        now,
		idHoldUntil: now.Add(time.Minute),
		lastPos:     12,
		prevPos:     11,
		prevPosAt:   now,
		still:       2,
		playing:     true,
		rawPlaying:  true,
		haveClock:   true,
		hintUntil:   now.Add(time.Minute),
		hintPlay:    true,
		trustAt:     now.Add(time.Minute),
		failures:    1,
		active:      now,
		pending:     &nePending{want: true},
		art:         []byte{1, 2, 3},
		artKey:      "key",
		artMissing:  true,
	}
	s.meta.use(neTrack{ID: "1002", Title: "Song"})

	s.close()

	if s.hwnd != 0 || s.pid != 0 || s.exe != "" || s.tierA || s.offsets != nil || s.base != 0 {
		t.Fatalf("process identity survived close: %+v", s)
	}
	if s.title != "" || s.id != "" || !s.idAt.IsZero() || !s.idHoldUntil.IsZero() {
		t.Fatalf("track identity survived close: title=%q id=%q", s.title, s.id)
	}
	if s.meta.ok || s.meta.track.Title != "" || s.meta.id != "" || s.meta.title != "" {
		t.Fatalf("metadata survived close: %+v", s.meta)
	}
	if s.playing || s.rawPlaying || s.haveClock || s.pending != nil || s.failures != 0 {
		t.Fatal("play state survived close")
	}
	if s.art != nil || s.artKey != "" || s.artMissing || len(s.art) != 0 {
		t.Fatal("cover state survived close")
	}
}

// TestNENoteFailureDropsTheTier (P2-5): a read that is refused, a NaN field or
// a broken id pointer chain is passed to noteFailure; two in a row stop the
// memory tier and close the handle instead of retrying a dead field forever.
func TestNENoteFailureDropsTheTier(t *testing.T) {
	s := &neSource{base: 0x1000, offsets: &neKnownOffsets[0], playing: true, haveClock: true}
	s.noteFailure()
	if s.tierA || s.failures != 1 {
		t.Fatalf("one failure ended the tier: tierA=%v failures=%d", s.tierA, s.failures)
	}
	s.noteFailure()
	if !s.tierA || s.failures != 0 || s.offsets != nil || s.base != 0 {
		t.Fatalf("the second failure did not drop the tier: tierA=%v offsets=%v base=%#x", s.tierA, s.offsets != nil, s.base)
	}
	if s.haveClock {
		t.Fatal("the frozen clock verdict was kept after the tier was dropped")
	}
}

// TestNEAppID (P2-9): the card needs a real path to resolve the client's icon;
// the bundle name is only the last resort.
func TestNEAppID(t *testing.T) {
	s := &neSource{}
	if got := s.appID(); got != neApp {
		t.Fatalf("appID without a path = %q, want %q", got, neApp)
	}
	s.exe = `D:\CloudMusic\cloudmusic.exe`
	if got := s.appID(); got != s.exe {
		t.Fatalf("appID = %q, want the executable path", got)
	}
}
