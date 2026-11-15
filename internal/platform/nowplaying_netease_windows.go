//go:build windows

package platform

// NetEase Cloud Music never registers a System Media Transport Controls
// session: its CEF processes start with MediaSessionService and
// HardwareMediaKeyHandling disabled, so SMTC sees nothing at all. This file
// makes the client a second source for the Now Playing host, using only what
// the client itself exposes:
//
//   - the class "OrpheusBrowserHost" top-level window, whose title is
//     "<song> - <artist>" (metadata only: it does not change with playback);
//   - webdata\file\fmPlay and playingList in %LOCALAPPDATA%\NetEase\CloudMusic,
//     which name the track, its album, its cover URL and its length;
//   - the client's own cover cache, Statics\<md5(url)>.dat, so no download;
//   - two doubles and a song-id token in cloudmusic.dll, for the position and
//     the play state (position frozen = paused), version-gated and validated
//     on every sample;
//   - the client's own global shortcuts (Ctrl+Alt+P / Left / Right) as the
//     transport, and its orpheus:// URL protocol for the seek and as the
//     fallback for a shortcut that is not registered.
//
// Everything read here is read-only: no injection, no hooks, no writes into
// the client. The memory tier is a capability that has to keep proving itself;
// when it cannot (unknown client version, elevated client, refused handle,
// samples that disagree with the queue files) the source falls back to title,
// artist, transport and an audio-session play state, and says so through the
// fields it leaves empty.

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// neWindowClass is the only top-level window class the client has.
	neWindowClass = "OrpheusBrowserHost"
	// neWindowTitleProbe bounds the title read; the client's titles are far
	// shorter.
	neWindowTitleProbe = 512
	// neDLL is the module the playback fields live in.
	neDLL = "cloudmusic.dll"

	// neIDGrace is how long after a track change the memory sample is
	// distrusted: the client swaps position and duration over a second or so,
	// so a sample taken right then reads the previous track's numbers.
	neIDGrace = 1500 * time.Millisecond
	// neSeekGrace ignores position jumps right after a seek (ours or the
	// user's) instead of reading them as playback.
	neSeekGrace = 1200 * time.Millisecond
	// neMaxFailures is how many rejected samples in a row turn the memory
	// tier off for the rest of this client's run.
	neMaxFailures = 2
	// neMetaRetry is how long to wait before re-reading the queue files for a
	// song id they did not name.
	neMetaRetry = 2 * time.Second
	// neArtRetry is how long a missing cover is asked for again: the client
	// writes the file shortly after the track starts.
	neArtRetry = 5 * time.Second
	// neAudioProbe rate-limits the audio-session fallback.
	neAudioProbe = 400 * time.Millisecond
	// neAudioHold keeps the fallback on "playing" across a quiet passage,
	// which a peak meter cannot tell from a pause.
	neAudioHold = 2500 * time.Millisecond
	// neSilenceFloor is the peak below which a session counts as silent.
	neSilenceFloor = 0.0005
	// neSettleWait is how long a transport command gets to show an effect
	// before the source falls back to the URL protocol.
	neSettleWait = 1200 * time.Millisecond
	// neBringBackWindow is how long the URL protocol's window restore is
	// undone for. The handler is a fresh cloudmusic.exe that forwards the URL
	// to the running client, and on 3.1.41 that client brings its window back
	// about 1.2 s after the launch returns — long after ShellExecute itself,
	// which is why the guard outlives it instead of racing it.
	neBringBackWindow = 2500 * time.Millisecond
)

// The client's default global shortcuts (设置 → 快捷键), which are the transport
// path: they are registered system-wide, they work while the client is
// minimised, and — unlike the orpheus:// protocol — they do not bring its
// window back or take the foreground.
const (
	neVKAlt    = 0x12
	neVKToggle = 0x50 // Ctrl+Alt+P: play/pause
	neVKPrev   = 0x25 // Ctrl+Alt+Left
	neVKNext   = 0x27 // Ctrl+Alt+Right
)

// swMinimize is the ShowWindow command the window guard folds the client back
// down with.
const swMinimize = 6

// neOffsets is one validated set of field offsets inside cloudmusic.dll,
// tied to the client builds that were measured with them.
type neOffsets struct {
	// versions lists every spelling of the client version this table was
	// measured on. The client's version resource carries two: the fixed
	// information ("3.1.41.8956", what neFileVersion reads) and the string
	// table the Explorer shows ("3.1.41.205564"). Both name one binary.
	versions []string
	pos      uintptr   // float64: playback position, seconds
	dur      uintptr   // float64: total duration, seconds
	token    uintptr   // pointer root of the "<songid>_<play token>" string
	chain    []uintptr // pointer offsets from the root to the string
}

// neKnownOffsets is the offset table. A version that is not listed here runs
// without the memory tier, never with a guess: the fields are read at fixed
// RVAs and a moved field would be indistinguishable from a real position.
var neKnownOffsets = []neOffsets{
	{
		// Measured on 3.1.41.205564 (x64): position advanced 1.000 s/s while
		// playing and froze exactly while paused; duration matched the queue
		// file's milliseconds; the token's prefix was the queue's song id.
		versions: []string{"3.1.41.8956", "3.1.41.205564"},
		pos:      0x21A74D8,
		dur:      0x2214778,
		token:    0x2213A70,
		chain:    []uintptr{0x10, 0, 0x10, 0x68, 0},
	},
}

var (
	procFindWindow = sysProc(user32, "FindWindowW")
	procShellOpen  = sysProc(shell32, "ShellExecuteW")
)

// neSource is the NetEase side of the poller. It lives on the poller's own
// thread and is not safe for concurrent use; the host marshals every call.
type neSource struct {
	hwnd windows.HWND
	pid  uint32
	proc windows.Handle
	base uintptr

	offsets *neOffsets
	// tierA is set once the memory tier has been ruled out (unknown version,
	// elevated client, refused handle, repeated bad samples). It lasts for
	// this client's run, so the fallback costs one attempt, not one per poll.
	tierA bool

	title string

	id     string
	idAt   time.Time
	metaID string
	metaAt time.Time
	track  neTrack
	haveTr bool

	lastPos   float64
	prevPos   float64
	prevPosAt time.Time
	still     int
	playing   bool
	hintUntil time.Time
	hintPlay  bool
	trustAt   time.Time
	failures  int
	// active is when this source last played or moved, for the merge rule.
	active time.Time

	// rawPlaying is the verdict from the position alone, before the
	// optimistic hold a command applies.
	rawPlaying bool
	pending    *nePending

	art        []byte
	artKey     string
	artAt      time.Time
	artMissing bool

	audio neAudio
}

// nePending is one transport command waiting to be confirmed: a hotkey that
// did nothing (the client's shortcuts switched off, or taken by another app)
// falls back to the URL protocol once.
type nePending struct {
	cmd  NowPlayingCommand
	want bool   // for play/pause: the state that was asked for
	id   string // for skips: the track the command started from
	at   time.Time
	done bool
}

// poll reads the client and returns its snapshot. ok is false when the client
// is not running or names no track.
func (s *neSource) poll(now time.Time) (NowPlayingState, bool) {
	if !s.ensure() {
		return NowPlayingState{}, false
	}
	raw := s.windowTitle()
	if raw == "" {
		// The window exists but names nothing (start-up, signed out, a modal
		// replacing it). There is no track to show, so another source may.
		return NowPlayingState{}, false
	}
	titleName, titleArtist := neSplitTitle(raw)
	titleChanged := raw != s.title
	s.title = raw

	pos, dur, id, sampled := s.sample()
	if sampled && id != "" && id != s.id {
		s.beginTrack(id, now)
	}
	switch {
	case id != "":
		if s.metaID != id {
			s.resolveByID(id, now)
		}
	case titleChanged:
		s.resolveByTitle(titleName, titleArtist, now)
	}

	// The position is believed only when the memory tier is on, the sample is
	// sane for the track the card is about to draw, and the client has had
	// time to finish swapping fields after a track change.
	havePos := false
	if sampled && !s.tierA && !now.Before(s.trustAt) && id == s.id && id != "" {
		if neSampleOK(pos, dur, s.track, s.haveTr) {
			havePos = true
			s.failures = 0
		} else {
			s.failures++
			if s.failures >= neMaxFailures {
				// The fields moved (a client update we do not know) or the
				// process changed under us: stop reading memory, keep the
				// rest.
				s.tierA = true
				s.dropProcess()
			}
		}
	}

	st := NowPlayingState{App: neApp, Received: now}
	if s.haveTr {
		st.Title, st.Artist, st.Album = s.track.Title, s.track.Artist, s.track.Album
	}
	if st.Title == "" {
		st.Title = titleName
	}
	if st.Artist == "" {
		st.Artist = titleArtist
	}

	switch {
	case havePos:
		s.lastPos = pos
		st.Duration, st.Elapsed, st.InfoAt = dur, pos, now
		s.playing = s.moved(pos, now)
	case s.tierA:
		// No memory tier means no clock at all: the card shows the track and
		// its transport without a progress row pretending to know where the
		// playhead is. Duration stays 0 for the same reason.
		s.playing = s.audioActive(now)
	default:
		// A sample that cannot be used yet (track change, client busy):
		// freeze the clock where it was and keep the last verdict, so the
		// card does not flicker between states.
		st.Elapsed, st.InfoAt = s.lastPos, now
		if s.haveTr {
			st.Duration = s.track.Duration
		}
	}
	s.settle(now, id)
	st.Playing = s.playing
	if st.Playing {
		st.Rate = 1
		s.active = now
	}
	if st.Title == "" {
		return NowPlayingState{}, false
	}
	st.ArtID = npArtID(neApp, st.Title, st.Artist, st.Album)
	if st.ArtID != "" {
		st.Art, st.ArtMissing = s.artFor(st.ArtID, now)
	}
	return st, true
}

// hasTrack reports whether the client is showing a track, so a command that
// arrives before the first poll still finds the right app.
func (s *neSource) hasTrack() bool {
	return s.hwnd != 0 && s.title != ""
}

// act runs one transport command. Play/pause, next and previous go through the
// client's own global shortcuts, which act in place; seek has no shortcut, so it
// goes through the orpheus:// protocol. A shortcut that has no effect (the user
// switched the client's shortcuts off) is retried once through the protocol.
func (s *neSource) act(a npAction, now time.Time) {
	switch {
	case a.isSeek:
		neRunOrpheus(s.hwnd, "seek", neSeconds(a.seek))
		s.prevPosAt = time.Time{}
		s.trustAt = now.Add(neSeekGrace)
		s.hintUntil, s.hintPlay = now.Add(neSeekGrace), s.playing
	case a.command == NPPlayPause:
		neSendShortcut(neVKToggle)
		s.playing = !s.playing
		s.hintUntil, s.hintPlay = now.Add(neSeekGrace+400*time.Millisecond), s.playing
		if s.playing {
			s.active = now
		}
		s.pending = &nePending{cmd: NPPlayPause, want: s.playing, at: now.Add(neSettleWait)}
	case a.command == NPNext:
		neSendShortcut(neVKNext)
		s.afterSkip(now)
		s.pending = &nePending{cmd: NPNext, id: s.id, at: now.Add(neSettleWait)}
	case a.command == NPPrevious:
		neSendShortcut(neVKPrev)
		s.afterSkip(now)
		s.pending = &nePending{cmd: NPPrevious, id: s.id, at: now.Add(neSettleWait)}
	}
}

// neSendShortcut sends one of the client's Ctrl+Alt chords. SendInput refuses
// to cross into a higher integrity level, so an elevated client simply never
// sees the key — which is why every transport command is confirmed and
// retried through the protocol.
func neSendShortcut(vk uint16) bool {
	events := [...]input{
		{typ: 1, ki: keybdInput{vk: vkControl}},
		{typ: 1, ki: keybdInput{vk: neVKAlt}},
		{typ: 1, ki: keybdInput{vk: vk}},
		{typ: 1, ki: keybdInput{vk: vk, flags: keyEventKeyUp}},
		{typ: 1, ki: keybdInput{vk: neVKAlt, flags: keyEventKeyUp}},
		{typ: 1, ki: keybdInput{vk: vkControl, flags: keyEventKeyUp}},
	}
	sent, _, _ := procSendInput.Call(uintptr(len(events)), uintptr(unsafe.Pointer(&events[0])), unsafe.Sizeof(events[0]))
	return sent == uintptr(len(events))
}

// afterSkip stops the position evidence until the client has swapped tracks.
func (s *neSource) afterSkip(now time.Time) {
	s.prevPosAt = time.Time{}
	s.trustAt = now.Add(neIDGrace)
}

// close releases the handles this source holds.
func (s *neSource) close() {
	s.dropProcess()
	s.audio.close()
	s.hwnd, s.pid, s.title = 0, 0, ""
	s.track, s.haveTr, s.metaID = neTrack{}, false, ""
	s.art, s.artKey, s.artMissing = nil, "", false
}

// ensure finds the client's window, and attaches to its process once.
func (s *neSource) ensure() bool {
	if s.hwnd != 0 {
		if s.alive() {
			return true
		}
		// The client restarted or quit: forget everything about the old one,
		// including a tier A verdict, which is per process.
		s.close()
	}
	class, err := windows.UTF16PtrFromString(neWindowClass)
	if err != nil {
		return false
	}
	r, _, _ := procFindWindow.Call(uintptr(unsafe.Pointer(class)), 0)
	hwnd := windows.HWND(r)
	if hwnd == 0 {
		return false
	}
	var pid uint32
	windows.GetWindowThreadProcessId(hwnd, &pid)
	if pid == 0 {
		return false
	}
	s.hwnd, s.pid = hwnd, pid
	s.attach()
	return true
}

func (s *neSource) alive() bool {
	r, _, _ := procIsWindow.Call(uintptr(s.hwnd))
	return r != 0
}

// attach opens the client once: the process handle, the module the offsets are
// relative to, and the offset table matching its version.
func (s *neSource) attach() {
	if s.tierA {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_QUERY_INFORMATION, false, s.pid)
	if err != nil {
		// An elevated client, or one protected from a same-user reader: the
		// other tier still works, so this is not an error to report.
		s.tierA = true
		return
	}
	s.proc = h
	s.base = neModuleBase(h, neDLL)
	if s.base == 0 {
		s.tierA = true
		s.dropProcess()
		return
	}
	version := neFileVersion(neProcessPath(h))
	for i := range neKnownOffsets {
		for _, known := range neKnownOffsets[i].versions {
			if known != "" && known == version {
				s.offsets = &neKnownOffsets[i]
				return
			}
		}
	}
	// A client version this build has never measured: never read the fields.
	s.tierA = true
	s.dropProcess()
}

func (s *neSource) dropProcess() {
	if s.proc != 0 {
		windows.CloseHandle(s.proc)
		s.proc = 0
	}
	s.base, s.offsets = 0, nil
}

// beginTrack starts a new track: position evidence is dropped until the client
// has settled, and the previous track's cover is forgotten.
func (s *neSource) beginTrack(id string, now time.Time) {
	s.id, s.idAt = id, now
	s.metaID, s.metaAt, s.haveTr = "", time.Time{}, false
	s.track = neTrack{}
	s.prevPosAt, s.still = time.Time{}, 0
	s.lastPos = 0
	s.trustAt = now.Add(neIDGrace)
	s.art, s.artKey, s.artMissing = nil, "", false
}

// sample reads position, duration and the song id in one go. ok is false when
// the memory tier is off or the reads failed.
func (s *neSource) sample() (pos, dur float64, id string, ok bool) {
	if s.tierA || s.offsets == nil || s.proc == 0 || s.base == 0 {
		return 0, 0, "", false
	}
	pos, ok1 := s.readFloat(s.base + s.offsets.pos)
	dur, ok2 := s.readFloat(s.base + s.offsets.dur)
	if !ok1 || !ok2 {
		return 0, 0, "", false
	}
	return pos, dur, s.songID(), true
}

func (s *neSource) readFloat(at uintptr) (float64, bool) {
	var b [8]byte
	if !s.readBytes(at, b[:]) {
		return 0, false
	}
	v := *(*float64)(unsafe.Pointer(&b[0]))
	if v != v { // NaN: a field that is not a position
		return 0, false
	}
	return v, true
}

func (s *neSource) readPtr(at uintptr) (uintptr, bool) {
	var b [8]byte
	if !s.readBytes(at, b[:]) {
		return 0, false
	}
	v := *(*uint64)(unsafe.Pointer(&b[0]))
	if v < 0x10000 || v > 0x7FFFFFFFFFFF {
		return 0, false
	}
	return uintptr(v), true
}

func (s *neSource) readBytes(at uintptr, b []byte) bool {
	if len(b) == 0 {
		return false
	}
	var got uintptr
	err := windows.ReadProcessMemory(s.proc, at, &b[0], uintptr(len(b)), &got)
	return err == nil && got == uintptr(len(b))
}

// songID reads the song id from the client's "<songid>_<play token>" string.
// The token changes on every (re)start of the same song, so only the numeric
// prefix identifies the track.
func (s *neSource) songID() string {
	if s.offsets == nil {
		return ""
	}
	p := s.base + s.offsets.token
	for _, off := range s.offsets.chain {
		next, ok := s.readPtr(p)
		if !ok {
			return ""
		}
		p = next + off
	}
	if token := s.readStdString(p); token != "" {
		if i := strings.IndexByte(token, '_'); i > 0 {
			token = token[:i]
		}
		for _, r := range token {
			if r < '0' || r > '9' {
				return ""
			}
		}
		return token
	}
	return ""
}

// readStdString reads an MSVC std::string whose object starts at p: either the
// 16-byte inline buffer or a heap pointer to the characters. Newer clients keep
// short tokens inline, which is the case that used to break readers that
// assumed a pointer.
func (s *neSource) readStdString(p uintptr) string {
	var head [16]byte
	if !s.readBytes(p, head[:]) {
		return ""
	}
	if i := indexByte(head[:], 0); i > 0 {
		if printable(head[:i]) {
			return string(head[:i])
		}
	}
	ptr := *(*uint64)(unsafe.Pointer(&head[0]))
	if ptr < 0x10000 || ptr > 0x7FFFFFFFFFFF {
		return ""
	}
	var buf [64]byte
	if !s.readBytes(uintptr(ptr), buf[:]) {
		return ""
	}
	if i := indexByte(buf[:], 0); i > 0 {
		return string(buf[:i])
	}
	return ""
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func printable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// moved is the play state the card shows: the position's own verdict, except
// right after our command, when its optimistic verdict holds until the client
// has caught up.
func (s *neSource) moved(pos float64, now time.Time) bool {
	s.rawPlaying = s.advancing(pos, now)
	if now.Before(s.hintUntil) {
		return s.hintPlay
	}
	return s.rawPlaying
}

// advancing derives the play state from the position alone: the client's
// position advances at 1× while playing and is exactly constant while paused.
func (s *neSource) advancing(pos float64, now time.Time) bool {
	prev, at := s.prevPos, s.prevPosAt
	s.prevPos, s.prevPosAt = pos, now
	if at.IsZero() {
		return s.playing
	}
	dt := now.Sub(at).Seconds()
	if dt <= 0 {
		return s.playing
	}
	d := pos - prev
	switch {
	case d > 0.5*dt && d < 3*dt+2:
		s.still = 0
		return true
	case neAbs(d) < 0.02:
		s.still++
		return s.still < 2 && s.playing
	default:
		// A jump: a seek, ours or the user's, or a track the client swapped
		// without changing the id. It says nothing about the play state.
		s.still = 0
		return s.playing
	}
}

// settle decides whether the transport command sent a moment ago took effect,
// and falls back to the URL protocol when the hotkey did nothing.
func (s *neSource) settle(now time.Time, id string) {
	p := s.pending
	if p == nil || now.Before(p.at) {
		return
	}
	switch p.cmd {
	case NPPlayPause:
		if s.rawPlaying == p.want {
			s.pending = nil
			return
		}
	case NPNext, NPPrevious:
		if id != "" && id != p.id {
			s.pending = nil
			return
		}
	}
	if !p.done {
		// The client's global shortcut is off, or another app holds the
		// combination. The protocol always works, at the cost of the
		// client's window reappearing for a few frames.
		switch p.cmd {
		case NPPlayPause:
			if p.want {
				neRunOrpheus(s.hwnd, "resume", "")
			} else {
				neRunOrpheus(s.hwnd, "pause", "")
			}
			s.hintUntil, s.hintPlay = now.Add(neSeekGrace), p.want
		case NPNext:
			neRunOrpheus(s.hwnd, "next", "")
			s.afterSkip(now)
		case NPPrevious:
			neRunOrpheus(s.hwnd, "previous", "")
			s.afterSkip(now)
		}
	}
	s.pending = nil
}

// audioActive is the fallback play state for a client the memory tier cannot
// read: the session's peak, held across quiet passages.
func (s *neSource) audioActive(now time.Time) bool {
	return s.audio.active(s.pid, now)
}

// windowTitle reads the client's title, which is "<song> - <artist>".
func (s *neSource) windowTitle() string {
	n, _, _ := procGetWindowTextLen.Call(uintptr(s.hwnd))
	if n <= 0 || n > 4096 {
		n = neWindowTitleProbe
	}
	buf := make([]uint16, n+1)
	c, _, _ := procGetWindowText.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if c == 0 {
		return ""
	}
	if int(c) > len(buf) {
		c = uintptr(len(buf))
	}
	return windows.UTF16ToString(buf[:c])
}

// --- track metadata ---------------------------------------------------------

// neMusicDir is the client's state directory.
func neMusicDir() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "NetEase", "CloudMusic")
	}
	return ""
}

// resolveByID loads the track the client's song id names from its queue files:
// the FM queue first (it also names its own current entry), then the play
// queue. A song started from search results is in neither; it keeps the title
// and the memory duration.
func (s *neSource) resolveByID(id string, now time.Time) {
	if s.metaID == id && !s.haveTr && now.Sub(s.metaAt) < neMetaRetry {
		return
	}
	s.metaID, s.metaAt = id, now
	s.track, s.haveTr = neTrack{}, false
	if dir := neMusicDir(); dir != "" {
		if tracks, ok := neReadTracks(filepath.Join(dir, "webdata", "file", "fmPlay"), true); ok {
			if t, hit := neFindTrack(tracks, id); hit {
				s.useTrack(t)
				return
			}
		}
		if tracks, ok := neReadTracks(filepath.Join(dir, "webdata", "file", "playingList"), false); ok {
			if t, hit := neFindTrack(tracks, id); hit {
				s.useTrack(t)
			}
		}
	}
}

// resolveByTitle is the fallback for a client the memory tier cannot read: the
// window title names the song, and the queues know the entry it belongs to.
func (s *neSource) resolveByTitle(title, artist string, now time.Time) {
	if title == "" || (s.haveTr && s.track.Title == title) {
		return
	}
	if now.Sub(s.metaAt) < neMetaRetry {
		return
	}
	s.metaAt = now
	if dir := neMusicDir(); dir != "" {
		if tracks, ok := neReadTracks(filepath.Join(dir, "webdata", "file", "fmPlay"), true); ok {
			if t, hit := neFindTitled(tracks, title, artist); hit {
				s.useTrack(t)
				return
			}
		}
		if tracks, ok := neReadTracks(filepath.Join(dir, "webdata", "file", "playingList"), false); ok {
			if t, hit := neFindTitled(tracks, title, artist); hit {
				s.useTrack(t)
			}
		}
	}
}

func (s *neSource) useTrack(t neTrack) {
	if t.Title == "" {
		return
	}
	if s.artKey != "" {
		s.art, s.artKey, s.artMissing = nil, "", false
	}
	s.track, s.haveTr = t, true
}

// neReadTracks parses one of the client's queue files.
func neReadTracks(path string, fm bool) ([]neTrack, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	if fm {
		_, tracks, ok := neParseFMPlay(data)
		return tracks, ok
	}
	return neParsePlayingList(data)
}

// --- cover ------------------------------------------------------------------

// artFor returns the cover of the track key from the client's own image cache.
// The cache file is keyed by the md5 of the URL the client asked for, so no
// download is needed. The second result is "this track's cover will not
// arrive", as the card expects.
func (s *neSource) artFor(key string, now time.Time) ([]byte, bool) {
	if key != s.artKey {
		s.artKey, s.artAt, s.art, s.artMissing = key, now, nil, true
	}
	if !s.artMissing {
		return s.art, false
	}
	if now.Sub(s.artAt) > neArtRetry {
		return nil, true
	}
	if s.art, s.artMissing = s.readArt(); !s.artMissing {
		return s.art, false
	}
	return nil, true
}

func (s *neSource) readArt() ([]byte, bool) {
	if !s.haveTr || s.track.PicURL == "" {
		return nil, true
	}
	dir := neMusicDir()
	if dir == "" {
		return nil, true
	}
	for _, u := range neCoverURLs(s.track.PicURL) {
		path := filepath.Join(dir, "Statics", neCoverHash(u)+".dat")
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() <= 0 || info.Size() > npMaxArt {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		return data, false
	}
	return nil, true
}

// --- commands ---------------------------------------------------------------

// neCommand is one orpheus:// payload: base64 of a compact JSON object.
type neCommand struct {
	Cmd   string `json:"cmd"`
	Value string `json:"value,omitempty"`
}

// neRunOrpheus asks the running client to run one command through its own URL
// scheme. The client's handler also brings its window back and takes the
// foreground, whatever show mode the launch asks for (measured: SW_HIDE and
// SW_SHOWNOACTIVATE both end with the window restored and focused), so the
// window state and the foreground are put back afterwards. hwnd may be 0 when
// the caller does not know the window; then nothing is restored.
func neRunOrpheus(hwnd windows.HWND, cmd, value string) {
	payload, err := json.Marshal(neCommand{Cmd: cmd, Value: value})
	if err != nil {
		return
	}
	url, err := windows.UTF16PtrFromString("orpheus://" + base64.StdEncoding.EncodeToString(payload))
	if err != nil {
		return
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return
	}
	wasIconic := hwnd != 0 && neIsIconic(hwnd)
	prev, _, _ := procGetForegroundWindow.Call()
	procShellOpen.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(url)), 0, 0, 4)
	if hwnd == 0 {
		return
	}
	// The handler restores and activates the window from its own thread, at a
	// moment the launch call does not report; undo it off the poller thread so
	// the card's clock keeps running.
	go neRestoreDesktop(hwnd, wasIconic, windows.HWND(prev))
}

// neRestoreDesktop folds the client's window back down when the protocol
// handler brought it up, and hands the foreground back to whoever had it.
func neRestoreDesktop(hwnd windows.HWND, wasIconic bool, prev windows.HWND) {
	deadline := time.Now().Add(neBringBackWindow)
	for time.Now().Before(deadline) {
		if wasIconic && !neIsIconic(hwnd) {
			procShowWindowAsync.Call(uintptr(hwnd), swMinimize)
		}
		time.Sleep(15 * time.Millisecond)
	}
	if wasIconic {
		procShowWindowAsync.Call(uintptr(hwnd), swMinimize)
	}
	if prev != 0 && prev != hwnd {
		procSetForegroundWindow.Call(uintptr(prev))
	}
}

func neIsIconic(hwnd windows.HWND) bool {
	r, _, _ := procIsIconic.Call(uintptr(hwnd))
	return r != 0
}

// --- Win32 plumbing ---------------------------------------------------------

// neModuleBase finds the base address of a module in another process; a
// module's HMODULE is its base address.
func neModuleBase(proc windows.Handle, name string) uintptr {
	mods := make([]windows.Handle, 1024)
	var needed uint32
	size := uint32(len(mods) * int(unsafe.Sizeof(mods[0])))
	if err := windows.EnumProcessModulesEx(proc, &mods[0], size, &needed, windows.LIST_MODULES_ALL); err != nil {
		return 0
	}
	n := int(needed / uint32(unsafe.Sizeof(mods[0])))
	if n > len(mods) {
		n = len(mods)
	}
	var buf [windows.MAX_PATH]uint16
	for _, m := range mods[:n] {
		if err := windows.GetModuleBaseName(proc, m, &buf[0], uint32(len(buf))); err != nil {
			continue
		}
		if strings.EqualFold(windows.UTF16ToString(buf[:]), name) {
			return uintptr(m)
		}
	}
	return 0
}

// neProcessPath reads the executable path of a process we already hold.
func neProcessPath(proc windows.Handle) string {
	var buf [windows.MAX_LONG_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(proc, 0, &buf[0], &size); err != nil || size == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// neFileVersion reads a file's version resource as "a.b.c.d", which is what
// gates the offset table.
func neFileVersion(path string) string {
	if path == "" {
		return ""
	}
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return ""
	}
	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])); err != nil {
		return ""
	}
	var ffi *neFixedFileInfo
	var n uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\`, unsafe.Pointer(&ffi), &n); err != nil || ffi == nil || n < 13 {
		return ""
	}
	return strconv.FormatUint(uint64(ffi.fileVersionMS>>16), 10) + "." +
		strconv.FormatUint(uint64(ffi.fileVersionMS&0xffff), 10) + "." +
		strconv.FormatUint(uint64(ffi.fileVersionLS>>16), 10) + "." +
		strconv.FormatUint(uint64(ffi.fileVersionLS&0xffff), 10)
}

// neFixedFileInfo is VS_FIXEDFILEINFO; only the version words are used.
type neFixedFileInfo struct {
	signature        uint32
	strucVersion     uint32
	fileVersionMS    uint32
	fileVersionLS    uint32
	productVersionMS uint32
	productVersionLS uint32
	fileFlagsMask    uint32
	fileFlags        uint32
	fileOS           uint32
	fileType         uint32
	fileSubtype      uint32
	fileDateMS       uint32
	fileDateLS       uint32
}

// --- audio session fallback -------------------------------------------------

const audioSessionStateActive = 1

var (
	iidAudioSessionManager2 = windows.GUID{
		Data1: 0x77AA99A0, Data2: 0x1BD6, Data3: 0x484F,
		Data4: [8]byte{0x8B, 0xC7, 0x2C, 0x65, 0x4C, 0x9A, 0x9B, 0x6F},
	}
	iidAudioMeterInformation = windows.GUID{
		Data1: 0xC02216F6, Data2: 0x8C67, Data3: 0x4B5B,
		Data4: [8]byte{0x9D, 0x00, 0xD0, 0x08, 0xE7, 0x3E, 0x00, 0x64},
	}
	iidAudioSessionControl2 = windows.GUID{
		Data1: 0xBFB7FF88, Data2: 0x7239, Data3: 0x4FC9,
		Data4: [8]byte{0x8F, 0xA2, 0x07, 0xC9, 0x50, 0xBE, 0x9C, 0x6D},
	}
)

// neAudio reads the client's audio session: the peak it renders is the only
// play-state evidence left when the memory tier is unavailable.
type neAudio struct {
	mgr    unsafe.Pointer
	last   time.Time
	held   bool
	heldAt time.Time
}

func (a *neAudio) close() {
	if a.mgr != nil {
		winCOMRelease(a.mgr)
		a.mgr = nil
	}
	a.held, a.last = false, time.Time{}
}

// active reports whether the client is rendering sound now, holding the
// verdict across a quiet passage.
func (a *neAudio) active(pid uint32, now time.Time) bool {
	if a.last.After(time.Time{}) && now.Sub(a.last) < neAudioProbe {
		return a.held
	}
	a.last = now
	if a.mgr == nil {
		a.open()
		if a.mgr == nil {
			return a.held
		}
	}
	if a.sessionsPeak(pid) > neSilenceFloor {
		a.held, a.heldAt = true, now
		return true
	}
	if a.held && now.Sub(a.heldAt) > neAudioHold {
		a.held = false
	}
	return a.held
}

func (a *neAudio) open() {
	enum := newDeviceEnumerator()
	if enum == nil {
		return
	}
	defer winCOMRelease(enum)
	var dev unsafe.Pointer
	if hr := winCOMCall(enum, 4, eRender, eConsole, uintptr(unsafe.Pointer(&dev))); failedHR(hr) || dev == nil {
		return
	}
	defer winCOMRelease(dev)
	var mgr unsafe.Pointer
	hr := winCOMCall(dev, 3, uintptr(unsafe.Pointer(&iidAudioSessionManager2)), clsctxAll, 0, uintptr(unsafe.Pointer(&mgr)))
	if failedHR(hr) || mgr == nil {
		return
	}
	a.mgr = mgr
}

// sessionsPeak returns the loudest peak among the sessions of the client's
// processes.
func (a *neAudio) sessionsPeak(pid uint32) float32 {
	var enum unsafe.Pointer
	if hr := winCOMCall(a.mgr, 5, uintptr(unsafe.Pointer(&enum))); failedHR(hr) || enum == nil {
		a.close()
		return 0
	}
	defer winCOMRelease(enum)
	var n int32
	if hr := winCOMCall(enum, 3, uintptr(unsafe.Pointer(&n))); failedHR(hr) || n <= 0 {
		return 0
	}
	var best float32
	for i := int32(0); i < n; i++ {
		var ctl unsafe.Pointer
		if hr := winCOMCall(enum, 4, uintptr(i), uintptr(unsafe.Pointer(&ctl))); failedHR(hr) || ctl == nil {
			continue
		}
		if peak := a.peak(ctl, pid); peak > best {
			best = peak
		}
		winCOMRelease(ctl)
	}
	return best
}

func (a *neAudio) peak(ctl unsafe.Pointer, pid uint32) float32 {
	c2 := winCOMQI(ctl, &iidAudioSessionControl2)
	if c2 == nil {
		return 0
	}
	var got uint32
	hr := winCOMCall(c2, 14, uintptr(unsafe.Pointer(&got)))
	winCOMRelease(c2)
	if failedHR(hr) || got != pid {
		return 0
	}
	var state int32
	if hr := winCOMCall(ctl, 3, uintptr(unsafe.Pointer(&state))); failedHR(hr) || state != audioSessionStateActive {
		return 0
	}
	meter := winCOMQI(ctl, &iidAudioMeterInformation)
	if meter == nil {
		return 0
	}
	defer winCOMRelease(meter)
	var peak float32
	if hr := winCOMCall(meter, 3, uintptr(unsafe.Pointer(&peak))); failedHR(hr) {
		return 0
	}
	return peak
}
