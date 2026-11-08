//go:build darwin

package main

import (
	"bytes"
	"image"
	_ "image/jpeg" // artwork formats players publish
	"image/png"
	"math"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
)

// nowPlayingModule shows what the system is playing. The closed notch stays
// bare while music plays: no wings until the pointer arrives. Hovering the
// notch peeks two small wings — the cover on the left, the equalizer on the
// right — and the expanded panel carries the full card (artwork,
// title/artist, ⏮ ⏯ ⏭, elapsed and remaining time).
//
// The state comes from mac.NowPlayingHost, which reads MediaRemote
// through an Apple-signed perl host (macOS 15.4 and later deny
// Apple-unsigned readers). The host runs for the life of the app: the
// now-playing wings, the home player and the panel card are always on.
//
// Artwork arrives in its own MediaRemote notification, often a moment
// after the title. The helper therefore waits a short grace window before
// declaring it missing; the module decodes whatever later line carries it,
// and falls back to the playing app's icon when a track never publishes
// artwork at all.
type nowPlayingModule struct {
	host *shelf

	np      *mac.NowPlayingHost
	started bool
	retryAt time.Time // next start attempt after a failure
	// seenRev is the helper revision the display was last built from, and
	// posNext the next whole-second repaint while the card is visible. Both
	// keep the idle tick from reading the helper state at all.
	seenRev uint64
	posNext time.Time

	// newHost builds the helper host; nil means the real Apple-signed perl
	// host. A seam for tests.
	newHost func() *mac.NowPlayingHost

	// last is the state the module displays; only a fully parsed helper
	// line updates it.
	last mac.NowPlayingState
	have bool

	// snap is the last state the UI was invalidated for; comparing new
	// states against it keeps the redraw rate tied to visible changes.
	snap      npSnap
	art       *ui.Bitmap // artwork or app icon of the current track
	artIsIcon bool       // art is the app icon fallback, not the cover
	artColor  ui.Color   // the artwork's average colour: EQ bars
	artDone   string     // artID decoded once (success or undecodable blob)
	iconFor   string     // artID whose app-icon fallback was looked up

	// iconPNGs caches app icons by bundle id: the fallback is looked up
	// once per app, not once per track.
	iconPNGs map[string][]byte

	// newIcon resolves a bundle id's icon; nil means mac.AppIcon. A seam
	// for tests, which must not start AppKit.
	newIcon func(bundle string) *ui.Bitmap

	// A play/pause press answers at once. Until the helper agrees (or
	// npOptimisticWindow passes), a contradicting line is treated as stale:
	// the app has not caught up yet, and the button must not flip back.
	optUntil   time.Time
	optPlaying bool
}

// npOptimisticWindow is how long a press outranks contradicting helper
// lines. It is a ceiling for slow apps, not the normal path: a helper
// line that agrees clears the override at once, and after the window
// the helper is trusted again.
const npOptimisticWindow = 2 * time.Second

// npSnap is the part of a Now Playing state the UI cares about.
type npSnap struct {
	have     bool
	playing  bool
	title    string
	artist   string
	artID    string
	position int // whole seconds, so a playing track redraws once a second
	art      int // 0 none, 1 real cover, 2 app icon fallback
}

// npArtNone / npArtCover / npArtIcon classify the picture the card shows
// in npSnap.art, so a same-track swap between cover, app icon and nothing
// still invalidates the window.
const (
	npArtNone = iota
	npArtCover
	npArtIcon
)

// npSnapOf is the pure projection from a state to the compared snapshot.
// Position counts only while the expanded panel can show it: with the
// panel closed a once-a-second redraw would wake the compositor for a
// card no one sees.
func npSnapOf(st mac.NowPlayingState, have, panelVisible bool, now time.Time) npSnap {
	if !have {
		return npSnap{}
	}
	position := 0
	if panelVisible {
		position = int(st.Position(now))
	}
	return npSnap{
		have:     true,
		playing:  st.Playing,
		title:    st.Title,
		artist:   st.Artist,
		artID:    st.ArtID,
		position: position,
	}
}

// npRetryDelay is how long the module waits before starting the helper
// again after a failed attempt (perl missing, bundle unreadable).
const npRetryDelay = 30 * time.Second

// tick keeps the helper alive and folds new helper lines into the
// displayed state.
func (m *nowPlayingModule) tick(now time.Time) {
	if !m.started {
		if !m.retryAt.IsZero() && now.Before(m.retryAt) {
			return
		}
		host := m.spawnHost()
		if err := host.Start(); err != nil {
			// perl or the bundle is missing: stay quiet and retry later
			// rather than logging on every 30 ms tick.
			m.retryAt = now.Add(npRetryDelay)
			return
		}
		m.np, m.started = host, true
		m.retryAt = time.Time{}
		m.seenRev = 0
		m.watch(host)
	} else if m.np.Dead() {
		// The helper exited (perl gone, a pipe broke). Drop the stale
		// state; a later tick starts a fresh host.
		m.np.Stop()
		m.np, m.started = nil, false
		m.retryAt = now.Add(npRetryDelay)
		m.last, m.have = mac.NowPlayingState{}, false
		m.forgetArt()
		m.artDone, m.iconFor = "", ""
		m.optUntil = time.Time{}
		m.posNext = time.Time{}
		m.invalidate(npSnap{})
		return
	}
	rev := m.np.Revision()
	if rev == m.seenRev && (m.posNext.IsZero() || now.Before(m.posNext)) {
		return
	}
	m.seenRev = rev
	m.refresh(now)
	m.posNext = m.nextPosition(now)
}

// watch wakes the monitoring loop on each helper line and on the helper's
// exit, so the module never needs a polling tick of its own.
func (m *nowPlayingModule) watch(h *mac.NowPlayingHost) {
	a := m.host
	go func() {
		for {
			select {
			case <-a.stop:
				return
			case <-h.Updates():
				a.wakeMonitor()
			case <-h.Done():
				a.wakeMonitor()
				return
			}
		}
	}()
}

// nextPosition is when the visible card's elapsed clock next crosses a
// whole second; zero while nothing on screen shows the position.
func (m *nowPlayingModule) nextPosition(now time.Time) time.Time {
	if !m.host.visible || !m.have || !m.last.Playing {
		return time.Time{}
	}
	pos := m.last.Position(now)
	frac := pos - math.Floor(pos)
	if frac <= 0 || frac >= 1 {
		return now.Add(time.Second)
	}
	return now.Add(time.Duration((1 - frac) * float64(time.Second)))
}

// nextTick is the helper restart delay or the visible card's position
// clock; a running helper with nothing on screen has no deadline (new
// lines and its exit wake the loop themselves).
func (m *nowPlayingModule) nextTick(now time.Time) time.Time {
	if !m.started {
		if m.retryAt.IsZero() {
			return now
		}
		return m.retryAt
	}
	return m.posNext
}

// spawnHost builds the helper host through the test seam when one is set.
func (m *nowPlayingModule) spawnHost() *mac.NowPlayingHost {
	if m.newHost != nil {
		return m.newHost()
	}
	return mac.NewNowPlayingHost()
}

// refresh pulls the latest state from the host and invalidates the
// window when what is shown changed (track, playing state, artwork, or
// the once-a-second moving position).
func (m *nowPlayingModule) refresh(now time.Time) {
	st, have := m.np.State()
	if have && !m.optUntil.IsZero() {
		switch {
		case st.Playing == m.optPlaying:
			// The helper caught up: the press is no longer an assumption.
			m.optUntil = time.Time{}
		case now.Before(m.optUntil):
			// The app has not processed the command yet (or emitted a stale
			// line): keep the user's answer and its clock behaviour.
			st.Playing = m.optPlaying
			if st.Playing {
				if st.Rate <= 0 {
					st.Rate = 1
				}
			} else {
				st.Rate = 0
			}
			// Keep the clock pinned where the press left it: a line that
			// still contradicts is stale, and its frozen elapsed or old
			// info timestamp would jump the position.
			st.Elapsed, st.Received, st.InfoAt = m.last.Elapsed, m.last.Received, m.last.InfoAt
		default:
			m.optUntil = time.Time{}
		}
	}
	if have && st.ArtID != m.last.ArtID {
		// A new track starts without last track's picture: the card must
		// not keep showing stale art while the new one is being read.
		m.forgetArt()
	}
	m.last, m.have = st, have
	if have {
		m.decodeArt(st)
	}
	next := npSnapOf(st, have, m.host.visible, now)
	if have {
		next.art = m.artState()
	}
	m.invalidate(next)
}

// invalidate marks snap as current and redraws when it differs from what
// the UI was last told.
func (m *nowPlayingModule) invalidate(next npSnap) {
	if next == m.snap {
		return
	}
	m.snap = next
	if m.host.win != nil {
		m.host.win.Invalidate()
	}
}

// artState classifies what the card shows, so swapping between a cover,
// an app icon and nothing invalidates the window even when the track id
// is unchanged.
func (m *nowPlayingModule) artState() int {
	switch {
	case m.art != nil && m.artIsIcon:
		return npArtIcon
	case m.art != nil:
		return npArtCover
	}
	return npArtNone
}

// decodeArt decodes the artwork of a newly seen track once, on the main
// thread (the view's thread), and drops the previous track's bitmap. A
// late line that finally carries the cover replaces an earlier app-icon
// fallback for the same track. Players publish their own encoding (NetEase
// sends WebP), so any registered image format is accepted; artwork that
// cannot be decoded falls back to the app icon instead of a blank glyph.
// rememberArt computes the average colour once per artwork.
func (m *nowPlayingModule) decodeArt(st mac.NowPlayingState) {
	if st.ArtID == "" || st.ArtID == m.artDone {
		return
	}
	if m.art != nil && !(m.artIsIcon && m.iconFor == st.ArtID) {
		// A new track: never show the previous track's picture.
		m.forgetArt()
	}
	if len(st.Art) > 0 {
		if img, _, err := image.Decode(bytes.NewReader(st.Art)); err == nil {
			m.artDone = st.ArtID
			m.forgetArt()
			m.rememberArt(img, false)
			return
		}
		if st.App == "" {
			// Retry once a line names the app whose icon can stand in.
			return
		}
		m.artDone = st.ArtID
	} else if !st.ArtMissing {
		// The helper has not given up on the artwork yet.
		return
	}
	if st.App == "" || m.iconFor == st.ArtID {
		// This line cannot name an app to draw, or the fallback is already
		// in place. A later line naming the bundle must still get its chance.
		return
	}
	m.iconFor = st.ArtID
	if icon := m.icon(st.App); icon != nil {
		// 测试缝提供了现成位图：没有可平均的 PNG，均衡条用默认色。
		m.art, m.artIsIcon = icon, true
		return
	}
	if pngBytes := m.appIconPNG(st.App); len(pngBytes) > 0 {
		if img, err := png.Decode(bytes.NewReader(pngBytes)); err == nil {
			m.rememberArt(img, true)
		}
	}
}

// appIconPNG returns the playing app's icon PNG, cached per bundle id:
// NetEase and other apps that never publish artwork still identify
// themselves, so the card shows a real app icon instead of a generic
// music glyph.
func (m *nowPlayingModule) appIconPNG(bundle string) []byte {
	if bundle == "" {
		return nil
	}
	if m.iconPNGs == nil {
		m.iconPNGs = make(map[string][]byte)
	}
	if b, ok := m.iconPNGs[bundle]; ok {
		return b
	}
	m.iconPNGs[bundle] = mac.AppIcon(bundle, 64)
	return m.iconPNGs[bundle]
}

// icon resolves an app icon through the test seam when one is set.
func (m *nowPlayingModule) icon(bundle string) *ui.Bitmap {
	if m.newIcon == nil {
		return nil
	}
	return m.newIcon(bundle)
}

// stop ends the helper and forgets its state; called when the setting is
// off, so a disabled feature costs no process at all.
func (m *nowPlayingModule) stop() {
	if m.np != nil {
		m.np.Stop()
		m.np = nil
	}
	m.started = false
	m.retryAt = time.Time{}
	m.last, m.have = mac.NowPlayingState{}, false
	m.forgetArt()
	m.artDone, m.iconFor = "", ""
	m.optUntil = time.Time{}
	m.invalidate(npSnap{})
}

// track returns the current state while it names something the card can
// show, playing or paused (the controls are most useful while paused).
func (m *nowPlayingModule) track() (mac.NowPlayingState, bool) {
	if !m.have || m.last.Title == "" {
		return mac.NowPlayingState{}, false
	}
	return m.last, true
}

// hasTrack reports whether a now-playing surface may be shown: the source
// names a track, paused or not.
func (m *nowPlayingModule) hasTrack() bool {
	_, ok := m.track()
	return ok
}

// playing reports whether something is playing right now.
func (m *nowPlayingModule) playing() bool {
	return m.have && m.last.Playing
}

// seekTo jumps to seconds in the current track. The widget shows the
// dragged position at once; the helper's next reported position is the
// truth (the plan: 松手发一次命令，之后以助手回报为准).
func (m *nowPlayingModule) seekTo(seconds float64) {
	if m.np == nil {
		return
	}
	if seconds < 0 || seconds != seconds {
		seconds = 0
	}
	if m.have && m.last.Duration > 0 {
		seconds = min(seconds, m.last.Duration)
	}
	m.np.Seek(seconds)
	now := time.Now()
	m.last.Elapsed = seconds
	m.last.Received = now
	m.last.InfoAt = time.Time{}
	next := npSnapOf(m.last, m.have, m.host.visible, now)
	next.art = m.artState()
	m.invalidate(next)
}

// send forwards a transport command to the playing app. Play/pause flips
// the shown state at once — the next helper line (the helper samples hot
// for a moment after a command) reconciles it with the truth, so a slow or
// missing source notification cannot leave the button on the old glyph.
func (m *nowPlayingModule) send(command mac.NowPlayingCommand) {
	if m.np == nil {
		return
	}
	m.np.Send(command)
	if command != mac.NPPlayPause || !m.have {
		return
	}
	now := time.Now()
	// Freeze or resume from where the track is now, not from the last
	// helper anchor: the button press itself must not move the clock.
	m.last.Elapsed = m.last.Position(now)
	m.last.Received = now
	m.last.InfoAt = time.Time{} // the pin replaces the dict's timestamp
	m.last.Playing = !m.last.Playing
	if m.last.Playing {
		if m.last.Rate <= 0 {
			m.last.Rate = 1
		}
	} else {
		m.last.Rate = 0
	}
	m.optUntil, m.optPlaying = now.Add(npOptimisticWindow), m.last.Playing

	next := npSnapOf(m.last, true, m.host.visible, now)
	next.art = m.artState()
	m.invalidate(next)
}
