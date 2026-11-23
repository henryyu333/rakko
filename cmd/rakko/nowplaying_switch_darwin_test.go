//go:build darwin

package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/platform"
	"rakko/internal/settings"
	"rakko/internal/store"
)

func countOwnNowPlayingHelpers(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid()), "-f", "RakkoNowPlaying.bundle").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return 0
		}
		t.Fatalf("pgrep failed: %v", err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func TestNowPlayingSwitch20CyclesProcessCount(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	a.np = &nowPlayingModule{host: a}
	t.Cleanup(func() { a.np.stop() })

	openSettingsPage(t, view, "外形与提示")
	now := time.Now()

	// Start enabled: first tick spawns exactly 1 real helper process.
	a.np.tick(now)
	if !a.np.started || a.np.np == nil {
		t.Fatal("expected helper host to start when NowPlaying is on")
	}
	if got := countOwnNowPlayingHelpers(t); got != 1 {
		t.Fatalf("initial helper process count = %d, want 1", got)
	}

	for cycle := range 20 {
		// Turn OFF -> process stops immediately (0).
		clickVisible(t, view, "正在播放")
		if a.settings.LiveActivities.NowPlaying {
			t.Fatalf("cycle %d: setting stayed on after click", cycle)
		}
		if a.np.started || a.np.np != nil {
			t.Fatalf("cycle %d: module still started after turning off", cycle)
		}
		if got := countOwnNowPlayingHelpers(t); got != 0 {
			t.Fatalf("cycle %d off: helper process count = %d, want 0", cycle, got)
		}
		// Ticking while off must not spawn a helper or request wakeups.
		a.np.tick(now.Add(time.Duration(cycle+1) * time.Second))
		if got := countOwnNowPlayingHelpers(t); got != 0 {
			t.Fatalf("cycle %d off after tick: helper process count = %d, want 0", cycle, got)
		}
		if !a.np.nextTick(now).IsZero() {
			t.Fatalf("cycle %d off: nextTick must be zero", cycle)
		}

		// Turn ON -> next tick spawns exactly 1 helper process.
		clickVisible(t, view, "正在播放")
		if !a.settings.LiveActivities.NowPlaying {
			t.Fatalf("cycle %d: setting stayed off after click", cycle)
		}
		a.np.tick(now.Add(time.Duration(cycle+1) * time.Second))
		if !a.np.started || a.np.np == nil {
			t.Fatalf("cycle %d on: helper host did not restart", cycle)
		}
		if got := countOwnNowPlayingHelpers(t); got != 1 {
			t.Fatalf("cycle %d on: helper process count = %d, want 1", cycle, got)
		}
	}
}

func TestNowPlayingSwitchHidesAndRestoresEverySurface(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, settings.FileName), []byte(`{"islandNowPlaying": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.LiveActivities.NowPlaying {
		t.Fatal("legacy islandNowPlaying:false turned off LiveActivities.NowPlaying")
	}

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	a := &shelf{
		store: s, dataDir: dir, settings: loaded,
		notchX: 500, notchW: 180, notchH: 32,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.settings.SetLyrics(true)
	if err := settings.Save(dir, a.settings); err != nil {
		t.Fatal(err)
	}
	flushToolsLayoutOnCleanup(t, a)

	stub := newStubNowPlaying(t)
	stub.mod.host = a
	a.np = stub.mod
	a.modules = []module{a.np, &retentionModule{host: a}}
	stub.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"夜曲","artist":"周杰伦","duration":227,"elapsed":56,"hasRate":true,"rate":1}`)

	// Wire global playback shortcuts.
	keys := installFakeKeys(t)
	a.settings.Shortcuts.PlayPause = "Ctrl+Alt+P"
	a.settings.Shortcuts.Previous = "Ctrl+Alt+Left"
	a.settings.Shortcuts.Next = "Ctrl+Alt+Right"
	a.registerShortcuts()

	settingsView := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	openSettingsPage(t, settingsView, "外形与提示")

	assertAllOn := func(step string) {
		t.Helper()
		if !a.np.showsPlayer() {
			t.Fatalf("%s: wings should show player when enabled", step)
		}
		if pw, _ := a.peekSize(); pw <= 220 {
			t.Fatalf("%s: peekSize width = %.0f, want wings > 220", step, pw)
		}
		// Panel band chip on tray tab.
		a.visible, a.level, a.tab = true, 2, pageTray
		if acts := a.liveActivities(); len(acts) != 1 || acts[0] != liveActivity(a.np) {
			t.Fatalf("%s: liveActivities = %v, want [np]", step, acts)
		}
		// Home page shows player + lyrics.
		a.tab = pageHome
		a.ensurePages()
		hp := a.page(pageHome).(*homePage)
		if w := hp.width(); w != homeLyricsW {
			t.Fatalf("%s: home width = %.0f, want %d (lyrics)", step, w, homeLyricsW)
		}
		hv := ui.NewTester(a.view, int(homeLyricsW)+2*earR, panelTestH)
		hv.Frame()
		hv.Frame()
		if !hv.HasText("夜曲") {
			t.Fatalf("%s: home player missing track title; texts=%v", step, hv.Texts())
		}
		// Tools page shows playing card.
		a.tab = pageTools
		if a.inactiveWidget("playing") {
			t.Fatalf("%s: playing card should be active", step)
		}
		tv := toolsTester(a)
		tv.Frame()
		tv.Frame()
		if !tv.HasText("播放进度") {
			t.Fatalf("%s: tools page missing music card; texts=%v", step, tv.Texts())
		}
	}

	assertAllOff := func(step string) {
		t.Helper()
		if a.np.started || a.np.np != nil || a.np.hasTrack() || a.np.playing() || a.np.live() || a.np.showsPlayer() {
			t.Fatalf("%s: module state not cleared/disabled", step)
		}
		// Notch wings hidden -> idle peek bulge.
		a.visible = false
		if w, _ := a.peekSize(); w != 220 {
			t.Fatalf("%s: peekSize width = %.0f, want 220 idle bulge", step, w)
		}
		// Panel band chip gone.
		a.visible, a.level, a.tab = true, 2, pageTray
		if acts := a.liveActivities(); len(acts) != 0 {
			t.Fatalf("%s: liveActivities = %v, want empty", step, acts)
		}
		// Home page shows otter (no player, no lyrics width).
		a.tab = pageHome
		a.ensurePages()
		hp := a.page(pageHome).(*homePage)
		if w := hp.width(); w != sheetW {
			t.Fatalf("%s: home width = %.0f, want %d (idle otter)", step, w, sheetW)
		}
		hv := ui.NewTester(a.view, sheetW+2*earR, panelTestH)
		hv.Frame()
		hv.Frame()
		if hv.HasText("夜曲") || hv.HasText("播放进度") {
			t.Fatalf("%s: home page still shows player while disabled; texts=%v", step, hv.Texts())
		}
		// Tools page hides music card & keeps it out of library, while layout keeps entry.
		a.tab = pageTools
		if !a.inactiveWidget("playing") {
			t.Fatalf("%s: playing card must be inactive", step)
		}
		for _, def := range a.toolsPage().libraryCards() {
			if def.id == "playing" {
				t.Fatalf("%s: libraryCards still offers playing while disabled", step)
			}
		}
		tv := toolsTester(a)
		tv.Frame()
		tv.Frame()
		if tv.HasText("播放进度") || tv.HasText("夜曲") {
			t.Fatalf("%s: tools page still draws music card; texts=%v", step, tv.Texts())
		}
		if !slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == "playing" }) {
			t.Fatalf("%s: layout dropped playing entry instead of keeping it inactive", step)
		}
		// Global playback shortcuts do not respond.
		for _, acc := range []string{"Ctrl+Alt+P", "Ctrl+Alt+Left", "Ctrl+Alt+Right"} {
			keys.press(acc)
		}
		select {
		case cmd, ok := <-stub.cmds:
			if ok {
				t.Fatalf("%s: playback shortcut sent %q while disabled", step, cmd)
			}
		default:
		}
		// Seek is a no-op while disabled.
		a.np.seekTo(42)
		select {
		case cmd, ok := <-stub.cmds:
			if ok {
				t.Fatalf("%s: seekTo sent %q while disabled", step, cmd)
			}
		default:
		}
	}

	assertAllOn("initial")

	// Playback shortcuts respond while enabled.
	keys.press("Ctrl+Alt+P")
	stub.expectCommand(t, "toggle")
	keys.press("Ctrl+Alt+Left")
	stub.expectCommand(t, "prev")
	keys.press("Ctrl+Alt+Right")
	stub.expectCommand(t, "next")

	// Turn off.
	clickVisible(t, settingsView, "正在播放")
	assertAllOff("after-off")

	// Turn back on -> tick restarts the host immediately and track returns.
	cmdR, cmdW := io.Pipe()
	lineR, lineW := io.Pipe()
	cmds2 := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(cmdR)
		for sc.Scan() {
			cmds2 <- sc.Text()
		}
		close(cmds2)
	}()
	t.Cleanup(func() {
		a.np.stop()
		lineW.Close()
		cmdW.Close()
	})
	a.np.newHost = func() *platform.NowPlayingHost {
		return platform.NewNowPlayingHostWithSource(func(context.Context) (io.WriteCloser, io.ReadCloser, func() error, error) {
			return cmdW, lineR, func() error { lineR.Close(); return nil }, nil
		})
	}
	clickVisible(t, settingsView, "正在播放")
	a.np.tick(time.Now())
	if !a.np.started || a.np.np == nil {
		t.Fatal("re-enabling did not start a fresh host on tick")
	}
	if _, err := io.WriteString(lineW, `{"ok":true,"isPlaying":true,"appState":1,"title":"夜曲","artist":"周杰伦","duration":227,"elapsed":56,"hasRate":true,"rate":1}`+"\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !a.np.hasTrack() {
		a.np.tick(time.Now())
		if time.Now().After(deadline) {
			t.Fatal("re-enabled host did not deliver track")
		}
		time.Sleep(2 * time.Millisecond)
	}
	assertAllOn("after-re-enable")
	keys.press("Ctrl+Alt+P")
	select {
	case cmd := <-cmds2:
		if cmd != "toggle" {
			t.Fatalf("after re-enable: shortcut sent %q, want toggle", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("after re-enable: playback shortcut did not fire")
	}
}
