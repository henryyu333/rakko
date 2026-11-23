package platform

import "time"

// maxSeekSeconds bounds a seek position; far beyond any track, so a larger
// value is a caller's arithmetic slip rather than a real position.
const maxSeekSeconds = 24 * 60 * 60

// NowPlayingCommand is a remote command sent to the playing app.
type NowPlayingCommand int

const (
	NPPlayPause NowPlayingCommand = iota + 1
	NPNext
	NPPrevious
)

// NowPlayingState is one snapshot of the playing track.
type NowPlayingState struct {
	Playing    bool
	Title      string
	Artist     string
	Album      string
	Duration   float64
	Elapsed    float64
	InfoAt     time.Time
	Received   time.Time
	Rate       float64
	App        string
	ArtID      string
	Art        []byte
	ArtMissing bool
}

// Position extrapolates the playback position at now: frozen while paused,
// advancing at Rate (defaulting to 1x when playing without a reported rate)
// while playing, anchored at InfoAt or Received, and clamped to [0, Duration].
func (s NowPlayingState) Position(now time.Time) float64 {
	pos := s.Elapsed
	anchor := s.InfoAt
	if anchor.IsZero() {
		anchor = s.Received
	}
	if s.Playing && !anchor.IsZero() {
		rate := s.Rate
		if rate <= 0 {
			rate = 1
		}
		if d := now.Sub(anchor).Seconds(); d > 0 {
			pos += d * rate
		}
	}
	if pos < 0 {
		pos = 0
	}
	if s.Duration > 0 && pos > s.Duration {
		pos = s.Duration
	}
	return pos
}
