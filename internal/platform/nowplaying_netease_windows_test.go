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
