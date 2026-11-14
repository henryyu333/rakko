//go:build windows

package platform

// Now Playing. The skeleton keeps the host contract so the shell's media
// card compiles and stays idle; reading other apps' sessions needs the
// System Media Transport Controls reader, which the media batch owns.

import (
	"context"
	"io"
	"sync"
	"time"
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
// advancing at Rate while playing, clamped to the track.
func (s NowPlayingState) Position(now time.Time) float64 {
	pos := s.Elapsed
	if s.Playing {
		anchor := s.InfoAt
		if anchor.IsZero() {
			anchor = s.Received
		}
		if !anchor.IsZero() {
			rate := s.Rate
			if rate <= 0 {
				rate = 1
			}
			pos += now.Sub(anchor).Seconds() * rate
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

// NowPlayingCommand is a remote command sent to the playing app.
type NowPlayingCommand int

const (
	NPPlayPause NowPlayingCommand = iota + 1
	NPNext
	NPPrevious
)

// NowPlayingHost feeds the latest Now Playing state to the UI.
// WINDOWS-NEXT(media): the SMTC reader replaces the idle host.
type NowPlayingHost struct {
	mu      sync.Mutex
	started bool
	updates chan struct{}
	done    chan struct{}
	state   NowPlayingState
	have    bool
	rev     uint64
}

// NewNowPlayingHost returns an idle host. Its channels stay open, so the
// shell's watcher waits instead of spinning.
func NewNowPlayingHost() *NowPlayingHost {
	return &NowPlayingHost{updates: make(chan struct{}, 1), done: make(chan struct{})}
}

// NewNowPlayingHostWithSource keeps the Mac constructor's signature for the
// tests that drive the protocol with a stub child.
func NewNowPlayingHostWithSource(source func(ctx context.Context) (io.WriteCloser, io.ReadCloser, func() error, error)) *NowPlayingHost {
	return NewNowPlayingHost()
}

// Start begins reading the playing session. Nothing is read yet.
func (h *NowPlayingHost) Start() error {
	h.mu.Lock()
	h.started = true
	h.mu.Unlock()
	return nil
}

// Stop ends the host.
func (h *NowPlayingHost) Stop() {
	h.mu.Lock()
	h.started = false
	h.mu.Unlock()
}

// Dead reports whether the helper is gone. The idle host never is.
func (h *NowPlayingHost) Dead() bool { return false }

// Done is closed when the host exits.
func (h *NowPlayingHost) Done() <-chan struct{} { return h.done }

// Updates receives one token per state change.
func (h *NowPlayingHost) Updates() <-chan struct{} { return h.updates }

// Revision changes whenever a new line lands.
func (h *NowPlayingHost) Revision() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rev
}

// State returns the latest snapshot.
func (h *NowPlayingHost) State() (NowPlayingState, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state, h.have
}

// Send asks the playing app to run command.
func (h *NowPlayingHost) Send(command NowPlayingCommand) {}

// Seek asks the playing app to jump to seconds.
func (h *NowPlayingHost) Seek(seconds float64) {}
