//go:build darwin

//go:generate go run ./nowplaying/npbuild

package mac

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// System Now Playing through MediaRemote.
//
// Since macOS 15.4 MediaRemote answers only clients whose code is
// Apple-signed, so ShelfU itself cannot read the system's now-playing
// state in-process. The workaround here is the well-known Apple-signed
// host trick: the app loads a small C bundle (built from
// internal/mac/nowplaying/helper.c, shipped in the app's resources) into
// /usr/bin/perl, which is Apple-signed, and the bundle does all
// MediaRemote work inside that process, streaming JSON lines on stdout.
// Every line of the bundle and of this client is ShelfU's own code.
//
// Everything here is read-only except SendNowPlayingCommand, which the
// UI calls on an explicit button press.

const (
	// npHelperBundle is the bundle inside the app's resource directory.
	npHelperBundle = "RakkoNowPlaying.bundle"
	// npHelperEntry is the C function the perl host calls.
	npHelperEntry = "shelf_np_run"
	// npPerlScript loads the bundle and calls shelf_np_run; the bundle
	// path is the first argument (perl -e puts extra arguments in @ARGV,
	// with $0 set to "-e"). Print buffering is off so JSON lines arrive
	// at once.
	npPerlScript = `use strict; use warnings; use DynaLoader;
$| = 1;
my $bundle = $ARGV[0] or die "usage: bundle path\n";
my $lib = DynaLoader::dl_load_file($bundle) or die "load: " . DynaLoader::dl_error() . "\n";
my $sym = DynaLoader::dl_find_symbol($lib, "` + npHelperEntry + `") or die "symbol\n";
my $run = DynaLoader::dl_install_xsub("main::shelf_np_run", $sym);
$run->();
`
)

// NowPlayingCommand is a remote command sent to the now-playing app.
// The values are the MediaRemote command numbers.
type NowPlayingCommand int

const (
	NPPlayPause NowPlayingCommand = iota + 1
	NPNext
	NPPrevious
)

// commandName maps a command to the helper's stdin word.
func commandName(c NowPlayingCommand) string {
	switch c {
	case NPPlayPause:
		return "toggle"
	case NPNext:
		return "next"
	case NPPrevious:
		return "prev"
	}
	return ""
}

// NowPlayingState is the latest snapshot the helper reported, reconciled.
// A new state is only published once a full line parsed.
type NowPlayingState struct {
	// Playing is reconciled from MediaRemote's two truth signals — the
	// app's playback state and its IsPlaying flag — never from the info
	// dict's rate, which some apps (NetEase) leave at 0 while playing.
	Playing bool
	Title   string
	Artist  string
	Album   string
	// Duration is the track length in seconds; 0 when unknown (a live
	// stream or a source that does not report one).
	Duration float64
	// Elapsed is the playback position the publisher's info dict last
	// named, and InfoAt the wall-clock time that position referred to
	// (the dict's own Timestamp). InfoAt is zero when the dict carries
	// none; then Received — the local time the line arrived — anchors
	// the extrapolation instead.
	Elapsed  float64
	InfoAt   time.Time
	Received time.Time
	// Rate is the effective playback rate for extrapolating Position:
	// the reported rate while playing, 0 while paused, and 1 when a
	// playing source publishes no rate (or a frozen 0).
	Rate float64
	// App is the bundle id of the app that is playing, when known.
	App string
	// ArtID identifies the current track's artwork; Art is its PNG
	// bytes, sent once per track. ArtMissing marks a track whose artwork
	// will not arrive.
	ArtID      string
	Art        []byte
	ArtMissing bool
}

// Position extrapolates the playback position at now: frozen while
// paused, advancing at Rate while playing. It anchors at the info
// dict's own timestamp (InfoAt) so a publisher that only rewrites its
// dict on state changes still shows a moving clock; without one the
// line's arrival time is the anchor. It never goes negative or past
// Duration (when known).
func (s NowPlayingState) Position(now time.Time) float64 {
	p := s.Elapsed
	anchor := s.InfoAt
	if anchor.IsZero() {
		anchor = s.Received
	}
	// Without an anchor the position stays frozen: extrapolating from
	// the zero time would pin the bar at the track's end.
	if s.Playing && s.Rate != 0 && !anchor.IsZero() {
		if d := now.Sub(anchor).Seconds(); d > 0 {
			p += d * s.Rate
		}
	}
	if p < 0 {
		p = 0
	}
	if s.Duration > 0 && p > s.Duration {
		p = s.Duration
	}
	return p
}

// FormatClock renders seconds as m:ss, or h:mm:ss past an hour.
func FormatClock(sec float64) string {
	if sec < 0 || sec != sec { // negative or NaN
		sec = 0
	}
	t := int(sec + 0.5)
	h, m, s := t/3600, (t%3600)/60, t%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// npUpdate is one decoded helper line.
type npUpdate struct {
	state NowPlayingState
	err   string // {"error":..} from the helper
}

// npReader decodes the helper's stdout protocol.
type npReader struct {
	sc *bufio.Scanner
}

func newNPReader(r io.Reader) *npReader {
	sc := bufio.NewScanner(r)
	// Artwork is sent base64 in one line; give it room.
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	return &npReader{sc: sc}
}

// read blocks for the next line and decodes it. It returns io.EOF when
// the helper is gone.
func (r *npReader) read() (npUpdate, error) {
	for r.sc.Scan() {
		u, ok := decodeNPLine(r.sc.Text())
		if !ok {
			continue // a line we do not understand is skipped, not fatal
		}
		return u, nil
	}
	if err := r.sc.Err(); err != nil {
		return npUpdate{}, err
	}
	return npUpdate{}, io.EOF
}

// decodeNPLine parses one helper line. Unknown fields and malformed
// lines are ignored (ok=false) so a protocol change can never crash the
// app.
func decodeNPLine(s string) (npUpdate, bool) {
	fields, ok := decodeJSONObject(s)
	if !ok {
		return npUpdate{}, false
	}
	if msg, isErr := fields["error"].(string); isErr {
		return npUpdate{err: msg}, true
	}
	u := npUpdate{}
	isPlaying, known := jsonBool(fields["isPlaying"])
	u.state.Playing = reconcilePlaying(isPlaying, known, int(jsonFloat(fields["appState"])))
	u.state.Title = jsonString(fields["title"])
	u.state.Artist = jsonString(fields["artist"])
	u.state.Album = jsonString(fields["album"])
	u.state.App = jsonString(fields["app"])
	u.state.ArtID = jsonString(fields["artID"])
	u.state.Duration = jsonFloat(fields["duration"])
	u.state.Elapsed = jsonFloat(fields["elapsed"])
	hasRate := fields["hasRate"] == true
	u.state.Rate = reconcileRate(u.state.Playing, hasRate, jsonFloat(fields["rate"]))
	if at := jsonFloat(fields["at"]); at > 0 {
		u.state.Received = time.Unix(int64(at), int64((at-float64(int64(at)))*1e9))
	}
	if at := jsonFloat(fields["infoAt"]); at > 0 {
		u.state.InfoAt = time.Unix(int64(at), int64((at-float64(int64(at)))*1e9))
	}
	u.state.ArtMissing = fields["artMissing"] == true
	if art := jsonString(fields["art"]); art != "" {
		if b, err := base64.StdEncoding.DecodeString(art); err == nil {
			u.state.Art = b
		}
	}
	return u, true
}

// jsonString converts a decoded JSON value to a string.
func jsonString(v any) string {
	s, _ := v.(string)
	return s
}

// jsonFloat converts a decoded JSON value to a float64.
func jsonFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

// jsonBool converts a decoded JSON value to a bool; known reports whether
// the field was an actual boolean (the helper emits null on a timed-out
// read).
func jsonBool(v any) (b, known bool) {
	b, ok := v.(bool)
	return b, ok
}

// MRPlaybackState values, as MediaRemote reports them.
const (
	mrStateUnknown     = 0
	mrStatePlaying     = 1
	mrStatePaused      = 2
	mrStateStopped     = 3
	mrStateInterrupted = 4
)

// reconcilePlaying decides the shown play state from MediaRemote's two
// truth signals. The playback state wins outright when it names a
// definite state: Playing overrides a lagging IsPlaying=false, and
// Paused/Stopped/Interrupted override a lagging IsPlaying=true (some
// apps, NetEase among them, keep reporting playing for a while after a
// pause). IsPlaying alone carries the verdict when the state is
// unknown, absent (0 or negative: timeout or missing symbol) or a
// neutral value like a seek direction. The info dict's rate is never
// consulted: probes show publishers leave PlaybackRate 0 in a
// minutes-stale dict while audio plays.
func reconcilePlaying(isPlaying, known bool, appState int) bool {
	switch appState {
	case mrStatePlaying:
		return true
	case mrStatePaused, mrStateStopped, mrStateInterrupted:
		return false
	}
	return known && isPlaying
}

// reconcileRate picks the rate Position extrapolates with. It is only a
// clock speed, never part of the play verdict: paused freezes at 0;
// playing uses the reported rate, or 1 when the source publishes none
// or left a 0 behind (the same stale dict that rate-override bugs came
// from — the clock must still move).
func reconcileRate(playing, hasRate bool, rate float64) float64 {
	if !playing {
		return 0
	}
	if hasRate && rate != 0 {
		return rate
	}
	return 1
}

// NowPlayingHost keeps the Apple-signed perl host running and feeds the
// latest Now Playing state to the UI. Start begins it; Stop ends it.
// Hosts are not reused after Stop.
type NowPlayingHost struct {
	// start launches the host and returns its stdin, its stdout and a
	// wait that reaps the process once the stdout reader ends. nil uses
	// the real perl host; tests substitute a fake.
	start func(ctx context.Context) (io.WriteCloser, io.ReadCloser, func() error, error)

	mu     sync.Mutex
	latest NowPlayingState // last state read
	have   bool
	cmd    io.WriteCloser
	cancel context.CancelFunc
	done   chan struct{}
}

// NewNowPlayingHost returns a host that will run the helper bundle from
// the app's resource directory through /usr/bin/perl.
func NewNowPlayingHost() *NowPlayingHost {
	return &NowPlayingHost{}
}

// NewNowPlayingHostWithSource builds a host whose child streams come
// from source instead of the perl host. It exists so tests can drive the
// protocol with a stub child; production code uses NewNowPlayingHost.
// The wait result reaps the stub process once its stdout ends.
func NewNowPlayingHostWithSource(source func(ctx context.Context) (io.WriteCloser, io.ReadCloser, func() error, error)) *NowPlayingHost {
	h := &NowPlayingHost{}
	if source != nil {
		h.start = source
	}
	return h
}

// Start spawns the helper host. It returns an error when neither perl
// nor the bundle is available, which callers surface once as a notice.
func (h *NowPlayingHost) Start() error {
	if h.start == nil {
		h.start = startPerlHost
	}
	ctx, cancel := context.WithCancel(context.Background())
	in, out, wait, err := h.start(ctx)
	if err != nil {
		cancel()
		return err
	}
	h.cmd = in
	h.cancel = cancel
	h.done = make(chan struct{})
	go h.loop(out, wait)
	return nil
}

// loop reads helper lines until the host exits, then reaps the process:
// a helper that dies on its own must not become a zombie, and neither
// must the CommandContext goroutine watching it.
func (h *NowPlayingHost) loop(out io.ReadCloser, wait func() error) {
	defer close(h.done)
	defer out.Close()
	if wait != nil {
		defer wait()
	}
	r := newNPReader(out)
	for {
		u, err := r.read()
		if err != nil {
			return
		}
		h.mu.Lock()
		if u.err != "" {
			// The helper rejected a command; the last good state stands.
			h.mu.Unlock()
			continue
		}
		h.latest, h.have = u.state, true
		h.mu.Unlock()
	}
}

// Stop ends the helper.
func (h *NowPlayingHost) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.cmd != nil {
		h.cmd.Close()
	}
	if h.done != nil {
		select {
		case <-h.done:
		case <-time.After(time.Second):
		}
	}
}

// Dead reports whether the helper process is gone (its stdout reached
// EOF or failed), so the caller can restart it.
func (h *NowPlayingHost) Dead() bool {
	if h.done == nil {
		return true
	}
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// State returns the latest snapshot and whether one has arrived.
func (h *NowPlayingHost) State() (NowPlayingState, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.latest, h.have
}

// Send asks the playing app to run command. Errors are ignored by the
// UI: a command can fail whenever nothing is playing.
func (h *NowPlayingHost) Send(command NowPlayingCommand) {
	name := commandName(command)
	if name == "" || h.cmd == nil {
		return
	}
	_, _ = io.WriteString(h.cmd, name+"\n")
}

// maxSeekSeconds bounds a seek position. Twenty-four hours is far beyond
// any track, so a larger value is a caller's arithmetic slip rather than
// a position worth sending to the playing app.
const maxSeekSeconds = 24 * 60 * 60

// Seek asks the playing app to jump to seconds into the current track.
// A position the UI cannot mean — NaN, ±Inf, negative, or beyond
// maxSeekSeconds — is dropped instead of sent: the helper answers such a
// line with an error the app never shows, and the one line it could
// misread (a truncated decimal) must not reach the player at all. Like
// Send, it is a no-op when the helper is not running and its errors are
// ignored: not every app or source can seek.
func (h *NowPlayingHost) Seek(seconds float64) {
	if h.cmd == nil {
		return
	}
	// NaN fails both comparisons, so this one test rejects it too.
	if !(seconds >= 0 && seconds <= maxSeekSeconds) {
		return
	}
	_, _ = io.WriteString(h.cmd, "seek "+strconv.FormatFloat(seconds, 'f', -1, 64)+"\n")
}

// startPerlHost runs the helper bundle inside the Apple-signed perl.
// The returned wait reaps the process; it must be called once the
// stdout reader ends or the child lingers as a zombie.
func startPerlHost(ctx context.Context) (io.WriteCloser, io.ReadCloser, func() error, error) {
	perl := "/usr/bin/perl"
	if _, err := os.Stat(perl); err != nil {
		return nil, nil, nil, fmt.Errorf("now playing needs %s: %w", perl, err)
	}
	bundle, err := helperBundlePath()
	if err != nil {
		return nil, nil, nil, err
	}
	cmd := exec.CommandContext(ctx, perl, "-e", npPerlScript, bundle)
	// The host prints JSON on stdout only; perl warnings and loader noise
	// go to stderr, which the app does not need.
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return in, out, cmd.Wait, nil
}

// helperBundlePath locates the shipped helper bundle. In a built app it
// sits at the top of the resource directory (mygo installs the
// darwin-arm64 platform directory's contents there). Under `go run` and
// `go test` the resource directory is the project's resources/ as it is,
// so the platform directory (where the generate step writes) is tried
// too, and as a last resort the repository tree is searched upward.
func helperBundlePath() (string, error) {
	var candidates []string
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
		candidates = append(candidates,
			filepath.Join(dir, npHelperBundle),
			filepath.Join(dir, "darwin-arm64", npHelperBundle))
	}
	if wd, err := os.Getwd(); err == nil {
		for dir, i := wd, 0; i < 6; dir, i = filepath.Dir(dir), i+1 {
			candidates = append(candidates,
				filepath.Join(dir, "resources", "darwin-arm64", npHelperBundle),
				filepath.Join(dir, "resources", npHelperBundle))
			if parent := filepath.Dir(dir); parent == dir {
				break
			}
		}
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("now playing helper %s not found (run go generate ./...)", npHelperBundle)
}

// decodeJSONObject parses a flat JSON object of scalars into a map. It
// is deliberately small and strict: the helper emits exactly that shape,
// and anything else is skipped by the caller.
func decodeJSONObject(s string) (map[string]any, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}
