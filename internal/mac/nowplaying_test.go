//go:build darwin

package mac

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDecodeNPLine(t *testing.T) {
	full := `{"ok":true,"isPlaying":true,"appState":1,"title":"Title","artist":"Artist","album":"Album",` +
		`"duration":300.5,"elapsed":42.25,"hasRate":true,"rate":1,"at":1700000000.5,` +
		`"infoAt":1700000000,"app":"com.apple.Music",` +
		`"artID":"id-1","art":"` + base64.StdEncoding.EncodeToString([]byte("PNGBYTES")) + `"}`
	u, ok := decodeNPLine(full)
	if !ok {
		t.Fatal("a full helper line must decode")
	}
	s := u.state
	if !s.Playing || s.Title != "Title" || s.Artist != "Artist" || s.Album != "Album" {
		t.Errorf("strings decoded wrong: %+v", s)
	}
	if s.Duration != 300.5 || s.Elapsed != 42.25 || s.Rate != 1 {
		t.Errorf("numbers decoded wrong: %+v", s)
	}
	if s.App != "com.apple.Music" || s.ArtID != "id-1" {
		t.Errorf("ids decoded wrong: %+v", s)
	}
	if string(s.Art) != "PNGBYTES" || s.ArtMissing {
		t.Errorf("artwork decoded wrong: %+v", s)
	}
	if want := time.Unix(1700000000, 500000000); !s.Received.Equal(want) {
		t.Errorf("at: got %v, want %v", s.Received, want)
	}
	if want := time.Unix(1700000000, 0); !s.InfoAt.Equal(want) {
		t.Errorf("infoAt: got %v, want %v", s.InfoAt, want)
	}
}

func TestDecodeNPLineVariants(t *testing.T) {
	cases := []struct {
		name string
		line string
		ok   bool
		err  string
	}{
		{"error line", `{"error":"bad command"}`, true, "bad command"},
		{"empty object", `{}`, true, ""},
		{"missing fields default", `{"isPlaying":false,"appState":2}`, true, ""},
		{"unknown fields are ignored", `{"future":"x","isPlaying":true,"appState":1}`, true, ""},
		{"not json", `hello`, false, ""},
		{"json array", `[1,2]`, false, ""},
		{"json null", `null`, false, ""},
		{"empty line", ``, false, ""},
		{"truncated", `{"isPlaying":`, false, ""},
		{"string not object", `"hello"`, false, ""},
		{"number not object", `42`, false, ""},
	}
	for _, c := range cases {
		u, ok := decodeNPLine(c.line)
		if ok != c.ok {
			t.Errorf("%s: decode(%q) ok = %v, want %v", c.name, c.line, ok, c.ok)
			continue
		}
		if ok && u.err != c.err {
			t.Errorf("%s: err = %q, want %q", c.name, u.err, c.err)
		}
	}
}

func TestDecodeNPLineBadArtworkIsEmptyNotFatal(t *testing.T) {
	u, ok := decodeNPLine(`{"isPlaying":true,"appState":1,"artID":"x","art":"!!not base64!!"}`)
	if !ok {
		t.Fatal("line must still decode")
	}
	if u.state.Art != nil {
		t.Errorf("bad base64 must leave Art nil, got %d bytes", len(u.state.Art))
	}
	if u.state.ArtID != "x" {
		t.Errorf("artID must survive a bad art blob, got %q", u.state.ArtID)
	}
}

func TestPositionExtrapolatesWhilePlaying(t *testing.T) {
	recv := time.Unix(1000, 0)
	playing := NowPlayingState{Playing: true, Elapsed: 10, Rate: 1, Duration: 300, Received: recv}
	if got := playing.Position(recv.Add(2500 * time.Millisecond)); got != 12.5 {
		t.Errorf("playing position = %v, want 12.5", got)
	}
	paused := NowPlayingState{Playing: false, Elapsed: 10, Rate: 1, Duration: 300, Received: recv}
	if got := paused.Position(recv.Add(time.Hour)); got != 10 {
		t.Errorf("paused position = %v, want 10 (frozen)", got)
	}
	// Never past the end, never negative.
	over := NowPlayingState{Playing: true, Elapsed: 299, Rate: 1, Duration: 300, Received: recv}
	if got := over.Position(recv.Add(time.Minute)); got != 300 {
		t.Errorf("position past the end = %v, want 300", got)
	}
	neg := NowPlayingState{Playing: false, Elapsed: -5, Received: recv}
	if got := neg.Position(recv); got != 0 {
		t.Errorf("negative position = %v, want 0", got)
	}
	// A stream (no duration) still advances.
	live := NowPlayingState{Playing: true, Elapsed: 1, Rate: 1, Received: recv}
	if got := live.Position(recv.Add(4 * time.Second)); got != 5 {
		t.Errorf("position without duration = %v, want 5", got)
	}
}

func TestFormatClock(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "0:00"},
		{9.4, "0:09"},
		{59.5, "1:00"},
		{60, "1:00"},
		{61, "1:01"},
		{599, "9:59"},
		{3599, "59:59"},
		{3600, "1:00:00"},
		{3661, "1:01:01"},
		{-3, "0:00"},
	}
	for _, c := range cases {
		if got := FormatClock(c.sec); got != c.want {
			t.Errorf("FormatClock(%v) = %q, want %q", c.sec, got, c.want)
		}
	}
}

// fakeHostScript writes a small shell script that speaks just enough of
// the helper protocol: it prints one state line, echoes commands, and
// quits on demand. Tests exercise the host's plumbing with it instead of
// perl, so they run anywhere.
func fakeHostScript(t *testing.T, state string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fakehelper")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' '" + state + "'\n" +
		"while IFS= read -r line; do\n" +
		"  printf 'echo:%s\\n' \"$line\"\n" +
		"  [ \"$line\" = quit ] && exit 0\n" +
		"done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNowPlayingHostReadsAndSends(t *testing.T) {
	state := `{"ok":true,"isPlaying":true,"appState":1,"title":"T","artist":"A","duration":100,"elapsed":5,"hasRate":true,"rate":1}`
	path := fakeHostScript(t, state)
	h := NewNowPlayingHost()
	h.start = func(ctx context.Context) (io.WriteCloser, io.ReadCloser, func() error, error) {
		return startCommand(ctx, path)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		st, ok := h.State()
		if ok {
			if !st.Playing || st.Title != "T" || st.Artist != "A" || st.Duration != 100 || st.Elapsed != 5 {
				t.Fatalf("state decoded wrong: %+v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no state arrived")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.Send(NPPlayPause)
	// The fake echoes commands; the state stays the last one parsed, and
	// the echo lines are not states, so the host must stay alive.
	time.Sleep(50 * time.Millisecond)
	if h.Dead() {
		t.Fatal("host died on a command echo")
	}
	h.Send(NPNext)
	h.Send(NPPrevious)
	time.Sleep(50 * time.Millisecond)
	if h.Dead() {
		t.Fatal("host died after commands")
	}
}

// TestNowPlayingHostSeek: Seek writes the helper's seek command and a
// negative or NaN position clamps to 0. A pipe pair stands in for the
// helper's stdin, so the exact bytes the host wrote are read back.
func TestNowPlayingHostSeek(t *testing.T) {
	cmdsR, cmdsW := io.Pipe()   // host writes commands, the test reads them
	stateR, stateW := io.Pipe() // stub stdout: never yields a line
	h := NewNowPlayingHostWithSource(func(context.Context) (io.WriteCloser, io.ReadCloser, func() error, error) {
		return cmdsW, stateR, nil, nil
	})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stateW.Close() // end the reader loop, so Stop returns at once
		h.Stop()
		cmdsR.Close()
		stateR.Close()
	}()
	// The loop is up once Start has handed the host its pipes.
	deadline := time.Now().Add(2 * time.Second)
	for h.cmd == nil || h.done == nil {
		if time.Now().After(deadline) {
			t.Fatal("host loop never started")
		}
		time.Sleep(time.Millisecond)
	}
	lines := make(chan string, 4)
	go func() {
		br := bufio.NewReader(cmdsR)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			lines <- line
		}
	}()
	next := func() string {
		t.Helper()
		select {
		case line := <-lines:
			return line
		case <-time.After(2 * time.Second):
			t.Fatal("no seek command arrived")
			return ""
		}
	}
	for _, tc := range []struct {
		name string
		sec  float64
		want string
	}{
		{"fractional", 73.25, "seek 73.25\n"},
		{"zero", 0, "seek 0\n"},
		{"one day", 86400, "seek 86400\n"},
	} {
		h.Seek(tc.sec)
		if got := next(); got != tc.want {
			t.Errorf("%s: wrote %q, want %q", tc.name, got, tc.want)
		}
	}
	// A position the UI cannot mean is dropped, not clamped or sent: the
	// helper would answer it with an error line the app does not show, and
	// a nonsensical position must never reach the player.
	for _, tc := range []struct {
		name string
		sec  float64
	}{
		{"negative", -1},
		{"nan", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
		{"beyond one day", 86400.5},
	} {
		h.Seek(tc.sec)
		select {
		case line := <-lines:
			t.Errorf("%s: wrote %q, want the command dropped", tc.name, line)
		case <-time.After(50 * time.Millisecond):
		}
	}
	// A host that never started must ignore Seek instead of panicking.
	var idle NowPlayingHost
	idle.Seek(12)
}

func TestNowPlayingHostDeadAfterHelperExit(t *testing.T) {
	path := fakeHostScript(t, `{"ok":true,"isPlaying":false,"appState":2}`)
	h := NewNowPlayingHost()
	h.start = func(ctx context.Context) (io.WriteCloser, io.ReadCloser, func() error, error) {
		return startCommand(ctx, path)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	// Closing our side of stdin makes the fake exit; the host must notice.
	if in, ok := h.cmd.(io.Closer); ok {
		in.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for !h.Dead() {
		if time.Now().After(deadline) {
			t.Fatal("host did not notice the helper exit")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNowPlayingHostStartFailureIsReported(t *testing.T) {
	h := NewNowPlayingHost()
	h.start = func(ctx context.Context) (io.WriteCloser, io.ReadCloser, func() error, error) {
		return nil, nil, nil, os.ErrNotExist
	}
	if err := h.Start(); err == nil {
		t.Fatal("Start must report the failure")
	}
	if !h.Dead() {
		t.Fatal("a host that never started must read as dead")
	}
	h.Stop() // must be safe on a host that never started
}

// TestHelperReadsWholeCommandLines drives the real helper.c line reader:
// the harness includes the C source and feeds it a pipe, so the test sees
// exactly the lines the bundle would hand to handleCommand. An overlong
// line must be dropped whole — the first 255 bytes of an overlong seek
// are a valid decimal and would otherwise seek to a position the user
// never asked for, with the tail arriving as a second bogus command. It
// is skipped only when there is no C compiler, which the helper build
// needs anyway.
func TestHelperReadsWholeCommandLines(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skipf("clang not available: %v", err)
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.c")
	// printf comes from helper.c's own <stdio.h>; the harness only adds
	// the driver loop around readCommandLine.
	const src = `
#include "helper.c"

int main(void) {
  char line[256];
  int status;
  while ((status = readCommandLine(stdin, line, sizeof line)) != 0) {
    if (status == 2) printf("overlong\n");
    else printf("line:%s\n", line);
  }
  return 0;
}
`
	if err := os.WriteFile(harness, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "harness")
	build := exec.Command(clang, "-fblocks", "-I", "nowplaying", "-o", exe, harness,
		"-framework", "CoreFoundation", "-framework", "Foundation")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile helper harness: %v\n%s", err, out)
	}
	// 255 bytes of "seek 0…" with no newline: every byte is a valid
	// decimal digit, so the old reader accepted the truncated prefix as
	// a position and then read the tail as a command of its own.
	overlong := "seek " + strings.Repeat("0", 260)
	run := exec.Command(exe)
	run.Stdin = strings.NewReader(overlong + "\nplay\nseek 1.5\nbogus\n")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	want := "overlong\nline:play\nline:seek 1.5\nline:bogus\n"
	if got := string(out); got != want {
		t.Fatalf("line reader output = %q, want %q", got, want)
	}
}

func TestCommandName(t *testing.T) {
	cases := map[NowPlayingCommand]string{
		NPPlayPause: "toggle",
		NPNext:      "next",
		NPPrevious:  "prev",
	}
	for cmd, want := range cases {
		if got := commandName(cmd); got != want {
			t.Errorf("commandName(%d) = %q, want %q", cmd, got, want)
		}
	}
	if got := commandName(NowPlayingCommand(99)); got != "" {
		t.Errorf("unknown command must map to the empty string, got %q", got)
	}
}

// TestLiveNowPlayingProbe reports what the system is playing right now,
// read through the Apple-signed perl host. It is read-only (no commands)
// and only runs with RAKKO_NP_PROBE=1, since it starts a real process and
// reflects the user's live media state.
func TestLiveNowPlayingProbe(t *testing.T) {
	if os.Getenv("RAKKO_NP_PROBE") != "1" {
		t.Skip("set RAKKO_NP_PROBE=1 to probe the live Now Playing state")
	}
	bundle, err := helperBundlePath()
	if err != nil {
		t.Skipf("helper bundle not built (run go generate ./internal/mac/nowplaying): %v", err)
	}
	t.Logf("helper bundle: %s", bundle)
	h := NewNowPlayingHost()
	if err := h.Start(); err != nil {
		t.Fatalf("start perl host: %v", err)
	}
	defer h.Stop()
	deadline := time.Now().Add(5 * time.Second)
	var st NowPlayingState
	for {
		if s, ok := h.State(); ok {
			st = s
			break
		}
		if h.Dead() {
			t.Fatal("host exited before reporting state")
		}
		if time.Now().After(deadline) {
			t.Fatal("no state line within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("playing=%v app=%q title=%q artist=%q album=%q duration=%.1f elapsed=%.1f rate=%.2f artwork=%d bytes artID=%q artMissing=%v",
		st.Playing, st.App, st.Title, st.Artist, st.Album, st.Duration, st.Elapsed, st.Rate,
		len(st.Art), st.ArtID, st.ArtMissing)
	if st.Playing && st.Title == "" {
		t.Logf("note: a playing source reported no title (protected or metadata-less source)")
	}
}

// startCommand starts path with its stdin/stdout pipes, the same shape
// startPerlHost returns.
func startCommand(ctx context.Context, path string) (io.WriteCloser, io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, path)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return in, out, cmd.Wait, nil
}

func TestDecodeNPLineRoundTripFromHelperSource(t *testing.T) {
	// Guard against the C emitter's exact field names drifting: every
	// field the Go decoder reads must appear in helper.c.
	src, err := os.ReadFile("nowplaying/helper.c")
	if err != nil {
		t.Skipf("helper source not found: %v", err)
	}
	for _, field := range []string{"isPlaying", "appState", "title", "artist", "album", "duration",
		"elapsed", "hasRate", "rate", "infoAt", "at", "app", "artID", "artMissing", "art", "error"} {
		if !strings.Contains(string(src), `"`+field+`\"`) && !strings.Contains(string(src), `\"`+field) {
			t.Errorf("helper.c does not mention field %q", field)
		}
	}
}

// TestHelperSourceContracts guards the C source against small but
// load-bearing mistakes: the snapshot frees what it overwrites, the
// baseline and the artwork-sent marker exist, oversized art reports as
// missing, and there is no shared semaphore.
func TestHelperSourceContracts(t *testing.T) {
	src, err := os.ReadFile("nowplaying/helper.c")
	if err != nil {
		t.Skipf("helper source not found: %v", err)
	}
	for _, want := range []string{
		// Each request has its own semaphore; a late answer can never
		// desync a shared one.
		"NPRequest", "dispatch_semaphore_create(0)",
		// The snapshot frees what it overwrites (the leak fix).
		"freeSnap(s);",
		// Oversized or empty artwork must report missing, not merely be
		// skipped, or the reader keeps waiting for it.
		"artMissing = 1;",
		// Artwork is not re-encoded once this track's was sent.
		"sentArtID",
		// The baseline tracks whose artwork already went out.
		"firstPublish",
		// The source app is named, so the reader can fall back to its icon.
		"MRMediaRemoteGetNowPlayingClient",
		// A title-first sample does not seal the artwork as missing.
		"artGrace",
		// A command makes the helper sample hot so the new state lands fast,
		// and the window is atomic across the stdin thread and timer queue.
		"cmdPollUntil",
		"static _Atomic double cmdPollUntil",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("helper.c lacks %q", want)
		}
	}
	if strings.Contains(string(src), "workSem") {
		t.Error("helper.c still uses a shared semaphore")
	}
}

// TestReconcilePlaying covers the truth-signal matrix. The bug this
// guards: NetEase playing "Fashion Killa" reported IsPlaying=true while
// its minutes-stale info dict still said rate 0 — folding rate into the
// verdict showed a paused glyph on playing audio.
func TestReconcilePlaying(t *testing.T) {
	cases := []struct {
		name             string
		isPlaying, known bool
		appState         int
		want             bool
	}{
		{"both true", true, true, mrStatePlaying, true},
		{"both false", false, true, mrStatePaused, false},
		// The NetEase playing bug: IsPlaying true, state Playing, and the
		// dict's rate is irrelevant — the verdict must be playing.
		{"playing overrides lagging isPlaying flag", false, true, mrStatePlaying, true},
		{"paused vetoes lagging isPlaying flag", true, true, mrStatePaused, false},
		{"stopped vetoes", true, true, mrStateStopped, false},
		{"interrupted vetoes", true, true, mrStateInterrupted, false},
		// Unknown or absent state: IsPlaying alone decides.
		{"unknown state trusts isPlaying", true, true, mrStateUnknown, true},
		{"unknown state trusts isPlaying false", false, true, mrStateUnknown, false},
		{"state timeout trusts isPlaying", true, true, -1, true},
		{"symbol absent trusts isPlaying", false, true, -2, false},
		{"seeking forward keeps isPlaying", true, true, 5, true},
		{"isPlaying timeout is not playing", false, false, mrStateUnknown, false},
	}
	for _, c := range cases {
		if got := reconcilePlaying(c.isPlaying, c.known, c.appState); got != c.want {
			t.Errorf("%s: reconcilePlaying(%v,%v,%d) = %v, want %v",
				c.name, c.isPlaying, c.known, c.appState, got, c.want)
		}
	}
}

// TestReconcileRate: the rate is a clock speed, never a verdict.
func TestReconcileRate(t *testing.T) {
	cases := []struct {
		name       string
		playing    bool
		hasRate    bool
		rate, want float64
	}{
		{"paused freezes", false, true, 1.5, 0},
		{"paused ignores rate", false, false, 0, 0},
		{"playing uses reported rate", true, true, 1.0, 1.0},
		{"playing half speed", true, true, 0.5, 0.5},
		{"playing rewind", true, true, -1.0, -1.0},
		// NetEase playing with a frozen 0 rate: the clock still moves.
		{"playing rateless moves", true, false, 0, 1},
		{"playing frozen zero moves", true, true, 0, 1},
	}
	for _, c := range cases {
		if got := reconcileRate(c.playing, c.hasRate, c.rate); got != c.want {
			t.Errorf("%s: reconcileRate = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDecodeNetEasePlayingTrace replays the exact line shape the live
// probe recorded while a NetEase-profile publisher played: IsPlaying 1,
// playback state Playing, but the info dict pinned at rate 0 with a
// frozen elapsed and a Timestamp that only updates on state changes.
// The reconciled state must say playing and the clock must still move.
func TestDecodeNetEasePlayingTrace(t *testing.T) {
	u, ok := decodeNPLine(`{"ok":true,"isPlaying":true,"appState":1,` +
		`"title":"Fashion Killa","artist":"A$AP Rocky","duration":236.33,` +
		`"elapsed":37.279,"hasRate":true,"rate":0,` +
		`"infoAt":1700000000,"at":1700000060,"app":"com.netease.163music"}`)
	if !ok {
		t.Fatal("netease-shaped line must decode")
	}
	s := u.state
	if !s.Playing {
		t.Error("rate 0 in a stale dict must not veto playing")
	}
	if s.Rate != 1 {
		t.Errorf("a playing source without a real rate extrapolates at 1, got %v", s.Rate)
	}
	// The position anchors at the dict's Timestamp, not at receipt: the
	// 60 s since the dict was written must count.
	if got := s.Position(time.Unix(1700000060, 0)); got != 97.279 {
		t.Errorf("position = %v, want 37.279 + 60 = 97.279", got)
	}
}

// TestDecodeNetEasePausedTrace: paused keeps reporting the frozen dict,
// and a lagging IsPlaying must not resurrect it as playing.
func TestDecodeNetEasePausedTrace(t *testing.T) {
	u, ok := decodeNPLine(`{"ok":true,"isPlaying":false,"appState":2,` +
		`"title":"Fashion Killa","artist":"A$AP Rocky","duration":236.33,` +
		`"elapsed":37.279,"hasRate":true,"rate":0,` +
		`"infoAt":1700000000,"at":1700005000,"app":"com.netease.163music"}`)
	if !ok {
		t.Fatal("paused line must decode")
	}
	if u.state.Playing || u.state.Rate != 0 {
		t.Errorf("paused must stay paused and frozen: %+v", u.state)
	}
	if got := u.state.Position(time.Unix(1700005000, 0)); got != 37.279 {
		t.Errorf("paused position = %v, want frozen 37.279", got)
	}

	// The stale-IsPlaying window the original rate hack tried to paper
	// over: appState Paused still wins.
	u2, ok := decodeNPLine(`{"ok":true,"isPlaying":true,"appState":2,"rate":0,"elapsed":37.279}`)
	if !ok || u2.state.Playing {
		t.Error("a lagging IsPlaying against an explicit pause must read paused")
	}
}

// TestDecodeIsPlayingTimeout: a null isPlaying (timed-out read) decodes
// as unknown, and an unknown flag with an unknown state is not playing.
func TestDecodeIsPlayingTimeout(t *testing.T) {
	u, ok := decodeNPLine(`{"ok":true,"isPlaying":null,"appState":-1,"title":"T"}`)
	if !ok {
		t.Fatal("null isPlaying must decode")
	}
	if u.state.Playing {
		t.Error("a timed-out isPlaying read must not report playing")
	}
}
