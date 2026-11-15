//go:build windows

package platform

// System Now Playing through the System Media Transport Controls: the
// GlobalSystemMediaTransportControlsSessionManager reports what every
// media app publishes (title, artist, album, artwork, position and play
// state) and accepts the transport commands the card and the media keys
// send. There are no events to subscribe to without a COM delegate, so the
// host polls three times a second on a thread with its own COM apartment;
// the UI still only wakes on a revision change.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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

// maxSeekSeconds bounds a seek position; far beyond any track, so a
// larger value is a caller's arithmetic slip rather than a real position.
const maxSeekSeconds = 24 * 60 * 60

// npPoll is how often the session snapshot is read. SMTC has no event
// source without a delegate, and a third of a second keeps the card's
// clock and buttons feeling live while costing one COM read.
const npPoll = 350 * time.Millisecond

// npMaxArt bounds the artwork a session may hand over; no player publishes
// a cover anywhere near this.
const npMaxArt = 4 << 20

// npArtGrace is how long a track's missing artwork is asked for again.
const npArtGrace = 5 * time.Second

// NowPlayingHost feeds the latest Now Playing state to the UI. Start
// begins the poller; Stop ends it. Hosts are not reused after Stop.
type NowPlayingHost struct {
	mu      sync.Mutex
	state   NowPlayingState
	have    bool
	rev     atomic.Uint64
	updates chan struct{}
	done    chan struct{}
	stop    chan struct{}
	once    sync.Once
	cmds    chan npAction
}

// npAction is one command for the poller's thread, where the session
// interface lives.
type npAction struct {
	command NowPlayingCommand
	seek    float64
	isSeek  bool
}

// NewNowPlayingHost returns an idle host that reads the system sessions
// through SMTC.
func NewNowPlayingHost() *NowPlayingHost {
	return &NowPlayingHost{updates: make(chan struct{}, 1), done: make(chan struct{})}
}

// NewNowPlayingHostWithSource keeps the Mac constructor's signature for
// the tests that drive the protocol with a stub child.
func NewNowPlayingHostWithSource(source func(ctx context.Context) (io.WriteCloser, io.ReadCloser, func() error, error)) *NowPlayingHost {
	return NewNowPlayingHost()
}

// Start begins reading the playing session. A nil error means the host is
// running; SMTC itself reports "no session" as an empty state, never as a
// failure.
func (h *NowPlayingHost) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stop != nil {
		return nil
	}
	h.stop = make(chan struct{})
	h.done = make(chan struct{})
	h.updates = make(chan struct{}, 1)
	h.cmds = make(chan npAction, 8)
	h.once = sync.Once{} // a new run gets its own one-shot stop
	go h.run(h.stop)
	return nil
}

// Stop ends the poller.
func (h *NowPlayingHost) Stop() {
	h.mu.Lock()
	stop, done := h.stop, h.done
	h.mu.Unlock()
	if stop == nil {
		return
	}
	h.once.Do(func() { close(stop) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	h.mu.Lock()
	h.stop = nil
	h.mu.Unlock()
}

// Dead reports whether the poller is gone, so the caller can restart it.
func (h *NowPlayingHost) Dead() bool {
	h.mu.Lock()
	done := h.done
	stopped := h.stop == nil
	h.mu.Unlock()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	default:
		return stopped
	}
}

// Done is closed when the host exits.
func (h *NowPlayingHost) Done() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.done
}

// Updates receives one token per snapshot.
func (h *NowPlayingHost) Updates() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.updates
}

// Revision changes whenever a new snapshot lands.
func (h *NowPlayingHost) Revision() uint64 { return h.rev.Load() }

// State returns the latest snapshot and whether one has arrived.
func (h *NowPlayingHost) State() (NowPlayingState, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state, h.have
}

// Send asks the playing app to run command; a command that cannot be sent
// (nothing playing, the app refuses) is dropped, as on the Mac.
func (h *NowPlayingHost) Send(command NowPlayingCommand) {
	h.enqueue(npAction{command: command})
}

// Seek asks the playing app to jump to seconds into the current track.
func (h *NowPlayingHost) Seek(seconds float64) {
	if !(seconds >= 0 && seconds <= maxSeekSeconds) {
		return
	}
	h.enqueue(npAction{seek: seconds, isSeek: true})
}

func (h *NowPlayingHost) enqueue(a npAction) {
	h.mu.Lock()
	cmds := h.cmds
	running := h.stop != nil
	h.mu.Unlock()
	if !running || cmds == nil {
		return
	}
	select {
	case cmds <- a:
	default: // a full queue means the player is not answering; dropping is right
	}
}

// publish stores a snapshot and wakes the UI.
func (h *NowPlayingHost) publish(st NowPlayingState) {
	h.mu.Lock()
	prev, had := h.state, h.have
	h.state, h.have = st, true
	updates := h.updates
	h.mu.Unlock()
	// A snapshot that repeats the last one's visible fields (and is at
	// most a second away from the reported position) needs no revision:
	// the UI extrapolates the clock itself.
	if had && npSameSnapshot(prev, st) {
		return
	}
	h.rev.Add(1)
	select {
	case updates <- struct{}{}:
	default:
	}
}

// npSameSnapshot reports whether two snapshots would draw the same card.
func npSameSnapshot(a, b NowPlayingState) bool {
	return a.Playing == b.Playing && a.Title == b.Title && a.Artist == b.Artist &&
		a.Album == b.Album && a.Duration == b.Duration && a.App == b.App &&
		a.ArtID == b.ArtID && len(a.Art) == len(b.Art) && a.ArtMissing == b.ArtMissing &&
		abs64(a.Elapsed-b.Elapsed) < 1.5
}

func abs64(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// run polls until Stop closes stop. All WinRT objects live on this
// goroutine's thread, which holds its own apartment.
func (h *NowPlayingHost) run(stop chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(h.done)
	// A multithreaded apartment: this thread never pumps messages, and an
	// apartment that does not pump would leave a broker-delivered async
	// completion (the artwork stream does) stuck at Started forever.
	leave, ok := enterCOMApartment(coinitMultithreaded)
	if !ok {
		// The thread was handed to us already living in another apartment
		// (an STA a search worker left behind, or a library that made it
		// one). There is no way to turn that into the MTA this worker
		// needs, so the host reports itself dead and the caller starts a
		// new one on a fresh thread instead of stalling here.
		return
	}
	defer leave()

	mgr := npOpenManager(stop)
	if mgr == nil {
		return
	}
	defer winCOMRelease(mgr)
	p := &npPoller{host: h, mgr: mgr}
	tick := time.NewTicker(npPoll)
	defer tick.Stop()
	p.poll()
	for {
		select {
		case <-stop:
			if p.session != nil {
				winCOMRelease(p.session)
				p.session = nil
			}
			p.ne.close()
			return
		case a := <-h.cmds:
			p.act(a)
		case <-tick.C:
			p.poll()
		}
	}
}

// npPoller is the poller's thread-local state.
type npPoller struct {
	host    *NowPlayingHost
	mgr     unsafe.Pointer
	session unsafe.Pointer
	app     string
	artKey  string
	art     []byte
	artAt   time.Time
	missing bool

	// ne is the NetEase Cloud Music source. The client publishes no SMTC
	// session (its CEF disables MediaSessionService), so it is read the way
	// it does publish: its window title, its queue files, its own cover
	// cache and a validated memory sample. Commands go through its global
	// shortcuts, or its orpheus:// protocol for the seek and as a fallback.
	ne neSource
	// src is the source the last published snapshot came from, so a
	// transport command goes back to the same app.
	src npSource
	// smtcActive is when SMTC last played or moved, for the merge rule.
	smtcActive time.Time
	smtcOK     bool
	smtcLast   NowPlayingState
}

// poll reads both sources and publishes the one the card should draw.
func (p *npPoller) poll() {
	now := time.Now()
	smtc, smtcOK := p.smtcSnapshot(now)
	netEase, neOK := p.ne.poll(now)
	st, src := npChoose(
		npCandidate{st: smtc, ok: smtcOK, active: p.smtcActive},
		npCandidate{st: netEase, ok: neOK, active: p.ne.active},
	)
	p.src = src
	if src == npSourceNone {
		p.host.publish(NowPlayingState{})
		return
	}
	st.Received = now
	p.host.publish(st)
}

// smtcSnapshot reads the current SMTC session. ok is false when there is no
// session, or when the session names nothing (an app that opened the media
// controls without a track).
func (p *npPoller) smtcSnapshot(now time.Time) (NowPlayingState, bool) {
	session := p.currentSession()
	if session == nil {
		p.smtcOK = false
		return NowPlayingState{}, false
	}
	st := NowPlayingState{App: p.app, Received: now}
	if info := p.readPlaybackInfo(session); info != nil {
		st.Playing = info.playing
		st.Rate = info.rate
	}
	if timeline := p.readTimeline(session); timeline != nil {
		st.Elapsed = timeline.position
		st.Duration = timeline.duration
		if !timeline.updated.IsZero() && !timeline.updated.After(now) {
			// Anchor the position where the player published it. A player
			// that only updates the timeline on state changes would
			// otherwise have the same stale position re-anchored to every
			// poll, freezing the card's clock or jumping it back.
			st.InfoAt = timeline.updated
		}
	}
	if st.InfoAt.IsZero() {
		st.InfoAt = now
	}
	st.Received = now
	if props := p.readMediaProperties(session); props != nil {
		st.Title, st.Artist, st.Album = props.title, props.artist, props.album
		st.ArtID = npArtID(p.app, props.title, props.artist, props.album)
		if st.ArtID != "" {
			st.Art, st.ArtMissing = p.artFor(st.ArtID, props.thumbRef)
		}
		winCOMRelease(props.thumbRef)
	}
	if st.Title == "" && st.Duration == 0 && !st.Playing {
		// A session that exists but names nothing (an app that opened the
		// media controls without a track) is no track at all.
		p.smtcOK = false
		return NowPlayingState{}, false
	}
	// The merge rule needs to know when this source last did something: a
	// playing session, or one whose position moved.
	if st.Playing || (p.smtcOK && abs64(st.Elapsed-p.smtcLast.Elapsed) > 0.3) {
		p.smtcActive = now
	}
	p.smtcOK, p.smtcLast = true, st
	return st, true
}

// currentSession returns the current session, opening it when the
// manager's answer changed.
func (p *npPoller) currentSession() unsafe.Pointer {
	var session unsafe.Pointer
	hr := winCOMCall(p.mgr, 6, uintptr(unsafe.Pointer(&session)))
	if failedHR(hr) || session == nil {
		if p.session != nil {
			winCOMRelease(p.session)
			p.session, p.app = nil, ""
		}
		return nil
	}
	// The same session is a different interface pointer each call; compare
	// the app identity, which is stable for the session's life.
	app := npCallString(session, 6)
	if p.session != nil && app == p.app {
		winCOMRelease(session)
		return p.session
	}
	if p.session != nil {
		winCOMRelease(p.session)
	}
	p.session, p.app = session, app
	p.art, p.artKey, p.missing = nil, "", false
	return p.session
}

type npPlayback struct {
	playing bool
	rate    float64
}

// npMaxRate bounds the rate the card extrapolates with: a player that
// reports something wilder is not reporting a play speed.
const npMaxRate = 100

// npPlaybackRate is the rate the card advances with: the player's own rate
// when it reports a usable one, and 1× otherwise.
func npPlaybackRate(reported float64, ok bool) float64 {
	if !ok || !(reported > 0 && reported <= npMaxRate) || math.IsNaN(reported) {
		return 1
	}
	return reported
}

func (p *npPoller) readPlaybackInfo(session unsafe.Pointer) *npPlayback {
	var info unsafe.Pointer
	if hr := winCOMCall(session, 9, uintptr(unsafe.Pointer(&info))); failedHR(hr) || info == nil {
		return nil
	}
	defer winCOMRelease(info)
	out, ok := npReadPlayback(info)
	if !ok {
		return nil
	}
	return &out
}

// npReadPlayback reads IGlobalSystemMediaTransportControlsSessionPlaybackInfo:
// slot 7 is the playback status, slot 10 the nullable playback rate.
func npReadPlayback(info unsafe.Pointer) (npPlayback, bool) {
	var status int32
	if hr := winCOMCall(info, 7, uintptr(unsafe.Pointer(&status))); failedHR(hr) {
		return npPlayback{}, false
	}
	// GlobalSystemMediaTransportControlsSessionPlaybackStatus:
	// 4 is playing, 5 is paused; every other value is not playback.
	playing := status == 4
	out := npPlayback{playing: playing}
	if !playing {
		return out, true
	}
	rate, ok := npReadRate(info)
	out.rate = npPlaybackRate(rate, ok)
	return out, true
}

// npReadRate reads the PlaybackRate property: slot 10 returns a nullable
// IReference<double>, whose Value is slot 6. A player that publishes no
// rate (a livestream, a browser tab) answers a null reference.
func npReadRate(info unsafe.Pointer) (float64, bool) {
	var ref unsafe.Pointer
	if hr := winCOMCall(info, 10, uintptr(unsafe.Pointer(&ref))); failedHR(hr) || ref == nil {
		return 0, false
	}
	defer winCOMRelease(ref)
	var v float64
	if hr := winCOMCall(ref, 6, uintptr(unsafe.Pointer(&v))); failedHR(hr) {
		return 0, false
	}
	return v, true
}

type npTimeline struct {
	position float64
	duration float64
	// updated is when the player last published position, from the
	// timeline's own LastUpdatedTime. The zero time means the player did
	// not publish one.
	updated time.Time
}

func (p *npPoller) readTimeline(session unsafe.Pointer) *npTimeline {
	var tl unsafe.Pointer
	if hr := winCOMCall(session, 8, uintptr(unsafe.Pointer(&tl))); failedHR(hr) || tl == nil {
		return nil
	}
	defer winCOMRelease(tl)
	out, ok := npReadTimeline(tl)
	if !ok {
		return nil
	}
	return &out
}

// npReadTimeline reads IGlobalSystemMediaTransportControlsSessionTimelineProperties:
// slots 6 and 7 are the start and end times, 10 the position, and 11 when
// the player last published that position.
func npReadTimeline(tl unsafe.Pointer) (npTimeline, bool) {
	var start, end, pos int64
	if hr := winCOMCall(tl, 6, uintptr(unsafe.Pointer(&start))); failedHR(hr) {
		return npTimeline{}, false
	}
	if hr := winCOMCall(tl, 7, uintptr(unsafe.Pointer(&end))); failedHR(hr) {
		return npTimeline{}, false
	}
	if hr := winCOMCall(tl, 10, uintptr(unsafe.Pointer(&pos))); failedHR(hr) {
		return npTimeline{}, false
	}
	const ticksPerSecond = 10_000_000
	out := npTimeline{}
	if end > start {
		out.duration = float64(end-start) / ticksPerSecond
	}
	if pos > start {
		out.position = float64(pos-start) / ticksPerSecond
	}
	var updated int64
	if hr := winCOMCall(tl, 11, uintptr(unsafe.Pointer(&updated))); !failedHR(hr) {
		out.updated = npDateTime(updated)
	}
	return out, true
}

// npDateTime converts a Windows.Foundation.DateTime — 100ns ticks since
// 1601 UTC — into a Go time. A value that cannot be a real timestamp
// comes back as the zero time, so the caller anchors the position itself.
func npDateTime(ticks int64) time.Time {
	delta := ticks - filetimeUnixBase
	// 100ns ticks to nanoseconds: refuse values whose conversion would
	// overflow rather than wrapping into a plausible-looking date.
	if delta <= 0 || delta > math.MaxInt64/100 {
		return time.Time{}
	}
	return time.Unix(0, delta*100).UTC()
}

type npProps struct {
	title    string
	artist   string
	album    string
	thumbRef unsafe.Pointer // the artwork reference, read on demand
}

func (p *npPoller) readMediaProperties(session unsafe.Pointer) *npProps {
	var op unsafe.Pointer
	if hr := winCOMCall(session, 7, uintptr(unsafe.Pointer(&op))); failedHR(hr) || op == nil {
		return nil
	}
	defer winCOMRelease(op)
	result, ok := npWait(op, 2*time.Second)
	if !ok || result == nil {
		return nil
	}
	props := result
	defer winCOMRelease(props)
	out := &npProps{
		title:  npCallString(props, 6),
		artist: npCallString(props, 9),
		album:  npCallString(props, 10),
	}
	var thumb unsafe.Pointer
	if hr := winCOMCall(props, 15, uintptr(unsafe.Pointer(&thumb))); !failedHR(hr) {
		out.thumbRef = thumb
	}
	return out
}

// artFor returns the artwork of the track key, downloading it once and
// asking again for a short grace period after the track starts: a player
// often publishes the properties before the cover exists.
func (p *npPoller) artFor(key string, ref unsafe.Pointer) ([]byte, bool) {
	if key != p.artKey {
		p.artKey, p.artAt, p.art, p.missing = key, time.Now(), nil, true
	}
	if !p.missing {
		return p.art, false
	}
	if time.Since(p.artAt) > npArtGrace {
		return nil, true
	}
	if ref != nil {
		if art, failed := npReadThumbnail(ref); len(art) > 0 && !failed {
			p.art, p.missing = art, false
			return art, false
		}
	}
	return nil, true
}

// act runs one queued command on the poller's thread, against the app the
// card is showing.
func (p *npPoller) act(a npAction) {
	if p.src == npSourceNetEase || (p.src == npSourceNone && p.ne.hasTrack()) {
		p.ne.act(a, time.Now())
		return
	}
	session := p.currentSession()
	if session == nil {
		return
	}
	var op unsafe.Pointer
	var hr uintptr
	switch {
	case a.isSeek:
		hr = winCOMCall(session, 24, uintptr(int64(a.seek*10_000_000)), uintptr(unsafe.Pointer(&op)))
	case a.command == NPPlayPause:
		hr = winCOMCall(session, 20, uintptr(unsafe.Pointer(&op)))
	case a.command == NPNext:
		hr = winCOMCall(session, 16, uintptr(unsafe.Pointer(&op)))
	case a.command == NPPrevious:
		hr = winCOMCall(session, 17, uintptr(unsafe.Pointer(&op)))
	}
	if failedHR(hr) || op == nil {
		return
	}
	_ = npWaitDone(op, 2*time.Second)
	winCOMRelease(op)
	p.poll()
}

// --- WinRT plumbing ---------------------------------------------------------

var (
	npRoGetFactory  = sysProc(combase, "RoGetActivationFactory")
	npWindowsCreate = sysProc(combase, "WindowsCreateString")
	npWindowsDelete = sysProc(combase, "WindowsDeleteString")
	npWindowsRawBuf = sysProc(combase, "WindowsGetStringRawBuffer")
)

var (
	iidNPSessionManagerStatics = windows.GUID{
		Data1: 0x2050C4EE, Data2: 0x11A0, Data3: 0x57DE,
		Data4: [8]byte{0xAE, 0xD7, 0xC9, 0x7C, 0x70, 0x33, 0x82, 0x45},
	}
	iidNPSessionManager = windows.GUID{
		Data1: 0xCACE8EAC, Data2: 0xE86E, Data3: 0x504A,
		Data4: [8]byte{0xAB, 0x31, 0x5F, 0xF8, 0xFF, 0x1B, 0xCE, 0x49},
	}
	iidNPSession = windows.GUID{
		Data1: 0x7148C835, Data2: 0x9B14, Data3: 0x5AE2,
		Data4: [8]byte{0xAB, 0x85, 0xDC, 0x9B, 0x1C, 0x14, 0xE1, 0xA8},
	}
	iidNPIAsyncInfo = windows.GUID{
		Data1: 0x00000036, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	iidNPRandomAccessStreamReference = windows.GUID{
		Data1: 0x33EE3134, Data2: 0x1DD6, Data3: 0x4E3A,
		Data4: [8]byte{0x80, 0x67, 0xD1, 0xC1, 0x62, 0xE8, 0x64, 0x2B},
	}
	iidNPRandomAccessStream = windows.GUID{
		Data1: 0x905A0FE1, Data2: 0xBC53, Data3: 0x11DF,
		Data4: [8]byte{0x8C, 0x49, 0x00, 0x1E, 0x4F, 0xC6, 0x86, 0xDA},
	}
	iidNPDataReaderFactory = windows.GUID{
		Data1: 0xD7527847, Data2: 0x57DA, Data3: 0x4E15,
		Data4: [8]byte{0x91, 0x4C, 0x06, 0x80, 0x66, 0x99, 0xA0, 0x98},
	}
	iidNPDataReader = windows.GUID{
		Data1: 0xE2B50029, Data2: 0xB4C1, Data3: 0x4314,
		Data4: [8]byte{0xA4, 0xB8, 0xFB, 0x81, 0x3A, 0x2F, 0x27, 0x5E},
	}
)

// npHString creates an HSTRING the caller deletes.
func npHString(s string) uintptr {
	u := windows.StringToUTF16(s)
	var h uintptr
	if len(u) == 0 {
		return 0
	}
	if hr, _, _ := npWindowsCreate.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h))); failedHR(hr) {
		return 0
	}
	return h
}

// npGoString reads an HSTRING.
func npGoString(h uintptr) string {
	if h == 0 {
		return ""
	}
	var n uint32
	p, _, _ := npWindowsRawBuf.Call(h, uintptr(unsafe.Pointer(&n)))
	if p == 0 || n == 0 {
		return ""
	}
	buf := make([]uint16, n)
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), p, uintptr(n)*2)
	return windows.UTF16ToString(buf)
}

func npDeleteString(h uintptr) {
	if h != 0 {
		npWindowsDelete.Call(h)
	}
}

// npCallString reads a method that returns an HSTRING and releases it.
func npCallString(obj unsafe.Pointer, index int) string {
	var h uintptr
	if hr := winCOMCall(obj, index, uintptr(unsafe.Pointer(&h))); failedHR(hr) {
		return ""
	}
	defer npDeleteString(h)
	return npGoString(h)
}

// npOpenManager activates the session manager's static factory and waits
// for the manager. A failure leaves the host idle instead of retrying in a
// loop the UI would never see.
func npOpenManager(stop chan struct{}) unsafe.Pointer {
	class := npHString("Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager")
	if class == 0 {
		return nil
	}
	defer npDeleteString(class)
	var factory unsafe.Pointer
	hr, _, _ := npRoGetFactory.Call(class, uintptr(unsafe.Pointer(&iidNPSessionManagerStatics)), uintptr(unsafe.Pointer(&factory)))
	if failedHR(hr) || factory == nil {
		return nil
	}
	defer winCOMRelease(factory)
	var op unsafe.Pointer
	for attempt := range 3 {
		select {
		case <-stop:
			return nil
		default:
		}
		hr = winCOMCall(factory, 6, uintptr(unsafe.Pointer(&op)))
		if !failedHR(hr) && op != nil {
			break
		}
		op = nil
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	if op == nil {
		return nil
	}
	defer winCOMRelease(op)
	result, ok := npWait(op, 5*time.Second)
	if !ok || result == nil {
		return nil
	}
	mgr := winCOMQI(result, &iidNPSessionManager)
	winCOMRelease(result)
	return mgr
}

// npWaitDone blocks until the async operation completes. It is for the
// operations whose result is a scalar (a count, a boolean): the ABI hands
// those back in a value slot, not as an interface to release.
func npWaitDone(op unsafe.Pointer, timeout time.Duration) bool {
	info := winCOMQI(op, &iidNPIAsyncInfo)
	if info == nil {
		return false
	}
	defer winCOMRelease(info)
	deadline := time.Now().Add(timeout)
	for {
		var status int32
		if hr := winCOMCall(info, 7, uintptr(unsafe.Pointer(&status))); failedHR(hr) {
			return false
		}
		switch status {
		case 1:
			return true
		case 2, 3:
			return false
		}
		if time.Now().After(deadline) {
			winCOMCall(info, 9)
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// npWait blocks until the async operation completes and returns its
// interface result. The status is read through IAsyncInfo, which the
// operation implements as a separate interface.
func npWait(op unsafe.Pointer, timeout time.Duration) (unsafe.Pointer, bool) {
	info := winCOMQI(op, &iidNPIAsyncInfo)
	if info == nil {
		return nil, false
	}
	defer winCOMRelease(info)
	deadline := time.Now().Add(timeout)
	for {
		var status int32
		if hr := winCOMCall(info, 7, uintptr(unsafe.Pointer(&status))); failedHR(hr) {
			return nil, false
		}
		switch status {
		case 1: // AsyncStatus.Completed
			var out unsafe.Pointer
			if hr := winCOMCall(op, 8, uintptr(unsafe.Pointer(&out))); failedHR(hr) {
				return nil, false
			}
			return out, true
		case 2, 3: // Canceled, Error
			return nil, false
		}
		if time.Now().After(deadline) {
			winCOMCall(info, 9) // Cancel
			return nil, false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// npReadThumbnail pulls the artwork bytes out of a stream reference.
func npReadThumbnail(ref unsafe.Pointer) ([]byte, bool) {
	var op unsafe.Pointer
	if hr := winCOMCall(ref, 6, uintptr(unsafe.Pointer(&op))); failedHR(hr) || op == nil {
		return nil, true
	}
	defer winCOMRelease(op)
	result, ok := npWait(op, 3*time.Second)
	if !ok || result == nil {
		return nil, true
	}
	defer winCOMRelease(result)
	stream := winCOMQI(result, &iidNPRandomAccessStream)
	if stream == nil {
		return nil, true
	}
	defer winCOMRelease(stream)
	var size uint64
	if hr := winCOMCall(stream, 6, uintptr(unsafe.Pointer(&size))); failedHR(hr) || size == 0 || size > npMaxArt {
		return nil, true
	}
	var input unsafe.Pointer
	if hr := winCOMCall(stream, 8, 0, uintptr(unsafe.Pointer(&input))); failedHR(hr) || input == nil {
		return nil, true
	}
	defer winCOMRelease(input)
	reader := npNewDataReader(input)
	if reader == nil {
		return nil, true
	}
	defer winCOMRelease(reader)
	var loadOp unsafe.Pointer
	if hr := winCOMCall(reader, 29, uintptr(uint32(size)), uintptr(unsafe.Pointer(&loadOp))); failedHR(hr) || loadOp == nil {
		return nil, true
	}
	ok = npWaitDone(loadOp, 3*time.Second)
	winCOMRelease(loadOp)
	if !ok {
		return nil, true
	}
	buf := make([]byte, size)
	if hr := winCOMCall(reader, 14, uintptr(uint32(size)), uintptr(unsafe.Pointer(&buf[0]))); failedHR(hr) {
		return nil, true
	}
	return buf, false
}

// npNewDataReader creates a DataReader over an input stream.
func npNewDataReader(input unsafe.Pointer) unsafe.Pointer {
	class := npHString("Windows.Storage.Streams.DataReader")
	if class == 0 {
		return nil
	}
	defer npDeleteString(class)
	var factory unsafe.Pointer
	hr, _, _ := npRoGetFactory.Call(class, uintptr(unsafe.Pointer(&iidNPDataReaderFactory)), uintptr(unsafe.Pointer(&factory)))
	if failedHR(hr) || factory == nil {
		return nil
	}
	defer winCOMRelease(factory)
	var reader unsafe.Pointer
	hr = winCOMCall(factory, 6, uintptr(input), uintptr(unsafe.Pointer(&reader)))
	if failedHR(hr) {
		return nil
	}
	return reader
}

// npArtID identifies one track's artwork: the same identity the Mac host
// sends, so the module's per-track decode cache behaves the same.
func npArtID(app, title, artist, album string) string {
	if title == "" {
		return ""
	}
	sum := sha1.Sum([]byte(app + "\x00" + title + "\x00" + artist + "\x00" + album))
	return hex.EncodeToString(sum[:8])
}
