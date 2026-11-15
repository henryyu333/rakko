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
//   - the client's own orpheus:// URL protocol for the transport, which runs
//     the command inside the client and injects no input into the system.
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
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
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
	// neSettleWait is how long a play/pause command gets to show an effect
	// before it is sent once more. Pause and resume are idempotent; a skip
	// would not be, so it is never repeated.
	neSettleWait = 1200 * time.Millisecond
	// neBringBackWindow is how long the URL protocol's window restore is
	// undone for. The handler is a fresh cloudmusic.exe that forwards the URL
	// to the running client, and on 3.1.41 that client brings its window back
	// about 1.2 s after the launch returns — long after ShellExecute itself,
	// which is why the guard outlives it instead of racing it.
	neBringBackWindow = 2500 * time.Millisecond
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
// The one exception is neGuard.run, which only touches its own mutexed fields
// and the client's window.
type neSource struct {
	hwnd windows.HWND
	pid  uint32
	proc windows.Handle
	base uintptr
	// exe is the client's executable, resolved once per run. It is the
	// identity the card resolves the app icon through (AppPath).
	exe string

	offsets *neOffsets
	// tierA is set once the memory tier has been ruled out (unknown version,
	// elevated client, refused handle, repeated bad samples). It lasts for
	// this client's run, so the fallback costs one attempt, not one per poll.
	tierA bool
	// attachAt is when the process was opened, so start-up samples that are
	// not filled in yet are not counted as failures.
	attachAt time.Time

	title string
	// idHoldUntil ignores the sampled song id for a moment after the title
	// names a different track: the client writes the title about a second
	// before it swaps the memory fields, so the old id must not resolve the
	// new title's card.
	idHoldUntil time.Time

	id   string
	idAt time.Time
	// prevID is the song id that named the track before the title moved.
	// Until the memory fields name a different one, the client has not
	// finished swapping and that id must not resolve the new title.
	prevID string
	meta   neMeta

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
	// haveClock says the verdict above came from the position on this poll.
	haveClock bool
	// pending is a play/pause command waiting for the client to agree.
	pending *nePending

	art        []byte
	artKey     string
	artAt      time.Time
	artMissing bool

	audio neAudio
	guard neGuard
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
	if raw != s.title {
		// The window names a different track. The album, cover and length
		// resolved for the previous one are wrong from this moment: drop
		// them and draw the window title until the queue files catch up.
		s.title = raw
		s.prevID = s.id
		s.meta.forgetTitle(raw)
		s.art, s.artKey, s.artMissing = nil, "", false
		s.idHoldUntil = now.Add(neIDGrace)
		s.trustAt = now.Add(neIDGrace)
		s.prevPosAt, s.still = time.Time{}, 0
	}

	pos, dur, id, sampled := s.sample()
	heldID := id != "" && (now.Before(s.idHoldUntil) || id == s.prevID)
	if heldID {
		// The title moved but the memory fields have not finished swapping:
		// the id they still name is the previous track's, and resolving it
		// would put the old album and cover back under the new title.
		id = ""
	}
	if sampled && id != "" && id != s.id {
		s.beginTrack(id, now)
	}
	if id != "" {
		s.resolveByID(id, now)
	}
	if !s.meta.ok {
		// The id is unreadable, or its queue file did not name it yet: the
		// title can still resolve the entry, on its own retry clock.
		s.resolveByTitle(titleName, titleArtist, now)
	}

	// A sample is usable only when the memory tier is on, it is sane for the
	// track about to be drawn, and the client has finished swapping fields.
	havePos := false
	if sampled && !s.tierA && !now.Before(s.trustAt) && id != "" && id == s.id {
		havePos = neSampleOK(pos, dur, s.meta.track, s.meta.ok)
	}
	if s.memoryOn() && now.After(s.attachAt.Add(neIDGrace)) {
		switch {
		case !sampled:
			// A refused read, a NaN field or a broken id pointer chain. Only
			// outside a track change, where the client legitimately clears
			// the fields for a moment.
			if !now.Before(s.trustAt) {
				s.noteFailure()
			}
		case heldID:
			// A slow track swap is not a broken field: the id is readable,
			// it just still names the previous track.
		case havePos:
			s.failures = 0
		case !now.Before(s.trustAt):
			// Readable but impossible for this track: a field that moved.
			s.noteFailure()
		}
	}

	st := NowPlayingState{App: s.appID(), Received: now}
	if s.meta.ok {
		st.Title, st.Artist, st.Album = s.meta.track.Title, s.meta.track.Artist, s.meta.track.Album
	}
	if st.Title == "" {
		st.Title = titleName
	}
	if st.Artist == "" {
		st.Artist = titleArtist
	}

	s.haveClock = false
	switch {
	case havePos:
		s.lastPos = pos
		st.Duration, st.Elapsed, st.InfoAt = dur, pos, now
		s.playing = s.moved(pos, now)
		s.haveClock = true
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
		if s.meta.ok {
			st.Duration = s.meta.track.Duration
		}
	}
	s.settle(now)
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

// act runs one transport command through the client's own URL protocol. The
// protocol needs no focus, no keystrokes and no global input: the command is a
// payload the running client executes in place, minimised or not. Its handler
// does restore and focus the client's window about a second later, which is
// what the guard undoes.
//
// Play/pause carries a pending confirmation because pause and resume are
// idempotent: a command the client ignored is sent once more. A skip is not
// repeated — that would move two tracks — so it is sent exactly once.
func (s *neSource) act(a npAction, now time.Time) {
	switch {
	case a.isSeek:
		s.control("seek", neSeconds(a.seek))
		s.prevPosAt = time.Time{}
		s.trustAt = now.Add(neSeekGrace)
		s.hintUntil, s.hintPlay = now.Add(neSeekGrace), s.playing
	default:
		want := s.playing
		if a.command == NPPlayPause {
			want = !s.playing
		}
		verb, confirm := neTransportVerb(a.command, want)
		if verb == "" {
			return
		}
		s.control(verb, "")
		switch {
		case a.command == NPPlayPause:
			s.playing = want
			s.hintUntil, s.hintPlay = now.Add(neSeekGrace+400*time.Millisecond), want
			if want {
				s.active = now
			}
			if confirm {
				s.pending = &nePending{want: want, at: now.Add(neSettleWait)}
			}
		default:
			// A skip is never repeated: one click, one command.
			s.pending = nil
			s.afterSkip(now)
		}
	}
}

// afterSkip stops the position evidence until the client has swapped tracks.
func (s *neSource) afterSkip(now time.Time) {
	s.prevPosAt = time.Time{}
	s.trustAt = now.Add(neIDGrace)
}

// close releases the handles this source holds and forgets everything read
// from it: a client that quit and is started again must be read from scratch,
// including the tier its predecessor ended up in.
func (s *neSource) close() {
	s.dropProcess()
	s.audio.close()
	s.guard.cancel()
	s.hwnd, s.pid, s.exe = 0, 0, ""
	s.title = ""
	s.id, s.idAt, s.idHoldUntil, s.prevID = "", time.Time{}, time.Time{}, ""
	s.meta = neMeta{}
	s.lastPos, s.prevPos, s.prevPosAt, s.still = 0, 0, time.Time{}, 0
	s.playing, s.rawPlaying, s.haveClock = false, false, false
	s.hintUntil, s.trustAt, s.attachAt = time.Time{}, time.Time{}, time.Time{}
	s.failures, s.tierA = 0, false
	s.pending = nil
	s.art, s.artKey, s.artMissing = nil, "", false
	s.active = time.Time{}
}

// memoryOn reports whether the memory tier is still reading this process.
func (s *neSource) memoryOn() bool {
	return !s.tierA && s.offsets != nil && s.proc != 0 && s.base != 0
}

// noteFailure counts one unusable sample and drops the memory tier when they
// stop being isolated: a refused handle, a NaN field or a broken id pointer
// chain that repeats is a field the offsets no longer describe.
func (s *neSource) noteFailure() {
	s.failures++
	if s.failures < neMaxFailures {
		return
	}
	s.tierA = true
	s.failures = 0
	s.haveClock = false
	s.pending = nil
	s.dropProcess()
}

// ensure finds the client's window, and attaches to its process once.
func (s *neSource) ensure() bool {
	if s.hwnd != 0 {
		if s.alive() && s.windowPID() == s.pid {
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
	s.exe = neExePath(pid)
	s.attach()
	return true
}

func (s *neSource) windowPID() uint32 {
	var pid uint32
	windows.GetWindowThreadProcessId(s.hwnd, &pid)
	return pid
}

// appID is the identity the card resolves the app icon through. The client's
// own executable resolves to its shell icon; the "netease" bundle has no path
// of its own and would only draw the generic music glyph.
func (s *neSource) appID() string {
	if s.exe != "" {
		return s.exe
	}
	return neApp
}

// neExePath reads the client's executable without the memory permission: a
// query-only handle is enough, and when even that is refused (an elevated
// client) the orpheus:// handler still names the same binary.
func neExePath(pid uint32) string {
	if pid == 0 {
		return ""
	}
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		defer windows.CloseHandle(h)
		if p := neProcessPath(h); p != "" {
			return p
		}
	}
	return neHandlerPath()
}

// neHandlerPath is the executable registered for the orpheus:// scheme.
func neHandlerPath() string {
	k, err := registry.OpenKey(registry.CLASSES_ROOT, `orpheus\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	cmd, _, err := k.GetStringValue("")
	if err != nil {
		return ""
	}
	return neCommandPath(cmd)
}

func (s *neSource) alive() bool {
	r, _, _ := procIsWindow.Call(uintptr(s.hwnd))
	return r != 0
}

// attach opens the client once: the process handle, the module the offsets are
// relative to, and the offset table matching its version.
func (s *neSource) attach() {
	s.attachAt = time.Now()
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
// has settled, the previous track's metadata and cover are forgotten, and both
// lookups become due for the new one.
func (s *neSource) beginTrack(id string, now time.Time) {
	s.id, s.idAt, s.prevID = id, now, ""
	s.meta = neMeta{}
	s.prevPosAt, s.still = time.Time{}, 0
	s.lastPos = 0
	s.trustAt = now.Add(neIDGrace)
	s.art, s.artKey, s.artMissing = nil, "", false
}

// sample reads position, duration and the song id in one go. ok is false when
// the memory tier is off or any of the three reads failed: a refused read, a
// NaN field and a broken pointer chain are all the same signal here, and the
// caller counts them.
func (s *neSource) sample() (pos, dur float64, id string, ok bool) {
	if !s.memoryOn() {
		return 0, 0, "", false
	}
	pos, ok1 := s.readFloat(s.base + s.offsets.pos)
	dur, ok2 := s.readFloat(s.base + s.offsets.dur)
	if !ok1 || !ok2 {
		return 0, 0, "", false
	}
	id = s.songID()
	if id == "" {
		return 0, 0, "", false
	}
	return pos, dur, id, true
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

// settle confirms the play/pause command sent a moment ago and sends it once
// more when the client did not act on it. Pause and resume are idempotent, so
// the repeat cannot undo anything; the verdict it is confirmed against is the
// one the tier's evidence can actually provide — the audio session in tier A,
// never the position verdict frozen when the clock was lost.
func (s *neSource) settle(now time.Time) {
	p := s.pending
	if p == nil {
		return
	}
	ev := nePlayVerdict(s.tierA, s.haveClock, s.rawPlaying, s.audioActive(now))
	switch neSettle(p, ev, now) {
	case neVerdictWait:
		return
	case neVerdictDone:
		s.pending = nil
		return
	}
	if p.done {
		s.pending = nil
		return
	}
	p.done = true
	verb := "pause"
	if p.want {
		verb = "resume"
	}
	s.control(verb, "")
	s.hintUntil, s.hintPlay = now.Add(neSeekGrace), p.want
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
	if !s.meta.needByID(id, now) {
		return
	}
	s.meta.triedID(id, now)
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
	if !s.meta.needByTitle(title, artist, now) {
		return
	}
	s.meta.triedTitle(title, now)
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
	s.meta.use(t)
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
	if !s.meta.ok || s.meta.track.PicURL == "" {
		return nil, true
	}
	dir := neMusicDir()
	if dir == "" {
		return nil, true
	}
	for _, u := range neCoverURLs(s.meta.track.PicURL) {
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

// control asks the running client to run one command through its own URL
// scheme. The payload is executed in place, so no keystroke is injected into
// the system and no other program can receive it. The client's handler does
// bring its window back and take the foreground (measured: SW_HIDE and
// SW_SHOWNOACTIVATE both end with the window restored and focused), so the
// window is put back by the guard afterwards, off the poller's thread.
func (s *neSource) control(cmd, value string) {
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
	hwnd := s.hwnd
	wasIconic := hwnd != 0 && neIsIconic(hwnd)
	prev, _, _ := procGetForegroundWindow.Call()
	procShellOpen.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(url)), 0, 0, 4)
	if hwnd == 0 {
		return
	}
	s.guard.arm(hwnd, windows.HWND(prev), wasIconic)
}

// neGuard is the one worker that undoes the URL handler's window restore.
// Commands coalesce into a single deadline: two in quick succession must not
// leave two guards folding the window and pulling the foreground back and
// forth, and a guard that is already running picks the later deadline up.
type neGuard struct {
	mu        sync.Mutex
	running   bool
	canceled  bool
	hwnd      windows.HWND
	prev      windows.HWND
	wasIconic bool
	until     time.Time
	input     uint32
}

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// neInputTick is the system's last-input timestamp. Any input after a command
// is the user's own, and cancels the guard instead of being undone.
func neInputTick() uint32 {
	var li lastInputInfo
	li.cbSize = uint32(unsafe.Sizeof(li))
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li))); r == 0 {
		return 0
	}
	return li.dwTime
}

func (g *neGuard) arm(hwnd, prev windows.HWND, wasIconic bool) {
	g.mu.Lock()
	g.hwnd, g.prev, g.wasIconic = hwnd, prev, wasIconic
	g.until = time.Now().Add(neBringBackWindow)
	g.input = neInputTick()
	g.canceled = false
	start := !g.running
	g.running = true
	g.mu.Unlock()
	if start {
		go g.run()
	}
}

// cancel ends the guard without touching the window: the source is being
// closed, so the window it would fold no longer belongs to it.
func (g *neGuard) cancel() {
	g.mu.Lock()
	g.canceled = true
	g.mu.Unlock()
}

func (g *neGuard) run() {
	for {
		g.mu.Lock()
		hwnd, prev, wasIconic, until, armed := g.hwnd, g.prev, g.wasIconic, g.until, g.input
		if g.canceled {
			g.running, g.canceled = false, false
			g.mu.Unlock()
			return
		}
		g.mu.Unlock()
		fg, _, _ := procGetForegroundWindow.Call()
		plan := neGuardDecide(wasIconic, neIsIconic(hwnd), windows.HWND(fg) == hwnd, prev != 0,
			armed, neInputTick(), time.Now(), until)
		if plan.Minimize {
			procShowWindowAsync.Call(uintptr(hwnd), swMinimize)
		}
		if !plan.Done {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if plan.Focus {
			procSetForegroundWindow.Call(uintptr(prev))
		}
		g.mu.Lock()
		// A command that arrived while this one was finishing keeps the
		// worker alive with the newer deadline.
		if g.until.After(until) {
			g.mu.Unlock()
			continue
		}
		g.running = false
		g.mu.Unlock()
		return
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
