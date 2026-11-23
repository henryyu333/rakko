package main

import (
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/platform"
	"rakko/internal/settings"
	"rakko/internal/store"
	"rakko/locales"
)

// TestNowPlayingSwitchCrossPlatform verifies the Now Playing switch across
// macOS and Windows UI surfaces: default on, legacy islandNowPlaying:false
// ignored, 20 toggle cycles, home/tools/band/shortcut gating, and optional
// screenshot export when RAKKO_NP_SHOT_DIR is set.
func TestNowPlayingSwitchCrossPlatform(t *testing.T) {
	origWin := forWindows
	t.Cleanup(func() { forWindows = origWin })

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, settings.FileName), []byte(`{"islandNowPlaying": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.LiveActivities.NowPlaying {
		t.Fatal("legacy islandNowPlaying:false disabled liveActivities.nowPlaying")
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	for _, tc := range []struct {
		name string
		win  bool
	}{
		{"macOS", false},
		{"Windows", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forWindows = tc.win
			d := t.TempDir()
			a := &shelf{
				store: st, dataDir: d, settings: settings.Default(),
				notchX: 500, notchW: 180, notchH: 32,
				thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
			}
			flushToolsLayoutOnCleanup(t, a)
			a.np = &nowPlayingModule{
				host: a,
				have: true,
				last: platform.NowPlayingState{
					Playing: true, Title: "夜曲", Artist: "周杰伦",
					Duration: 227, Elapsed: 56, Rate: 1, Received: time.Now(),
				},
			}
			a.modules = []module{a.np}

			sv := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
			sv.SetPreferences(ui.Preferences{ReduceMotion: true})
			if tc.win {
				sv.SetTitleBar(ui.TitleBar{Height: 32, Right: 138})
			}
			sv.SetLocale("zh-Hans")
			sv.Frame()
			if err := sv.Click(locales.In("zh-Hans").SettingsNotchNav()); err != nil {
				t.Fatalf("open notch settings: %v", err)
			}
			sv.Frame()
			sv.Frame()
			// Scroll to bring the 实时活动 section into view for screenshots.
			if r, ok := sv.Find(locales.In("zh-Hans").SettingsLiveSection()); ok && r.Y+220 > float32(settingsHeight) {
				a.settingsScroll.Y = max(0, r.Y-settingsTopH-16)
				sv.Frame()
				sv.Frame()
			}

			if shotDir := os.Getenv("RAKKO_NP_SHOT_DIR"); shotDir != "" {
				saveSwitchShot(t, filepath.Join(shotDir, tc.name+"-settings-on.png"), sv)
			}

			// Check English label in settings ("Now Playing").
			sv.SetLocale("en")
			sv.Frame()
			if !sv.HasText(locales.In("en").NowPlaying()) {
				t.Fatalf("English settings missing %q; texts=%v", locales.In("en").NowPlaying(), sv.Texts())
			}
			if shotDir := os.Getenv("RAKKO_NP_SHOT_DIR"); shotDir != "" {
				saveSwitchShot(t, filepath.Join(shotDir, tc.name+"-settings-en-on.png"), sv)
			}
			sv.SetLocale("zh-Hans")
			sv.Frame()

			// Toggle 20 cycles OFF -> ON.
			rowLabel := locales.In("zh-Hans").NowPlaying()
			for i := range 20 {
				if err := sv.Click(rowLabel); err != nil {
					t.Fatalf("cycle %d off click: %v", i, err)
				}
				sv.Frame()
				if a.settings.LiveActivities.NowPlaying {
					t.Fatalf("cycle %d: expected NowPlaying false", i)
				}
				if a.np.hasTrack() || a.np.showsPlayer() || a.np.live() {
					t.Fatalf("cycle %d off: module still reports track", i)
				}
				if !a.inactiveWidget("playing") {
					t.Fatalf("cycle %d off: playing card still active", i)
				}
				if err := sv.Click(rowLabel); err != nil {
					t.Fatalf("cycle %d on click: %v", i, err)
				}
				sv.Frame()
				if !a.settings.LiveActivities.NowPlaying {
					t.Fatalf("cycle %d: expected NowPlaying true", i)
				}
			}

			// Turn off once and verify home, tools, liveActivities, shortcutRun.
			if err := sv.Click(rowLabel); err != nil {
				t.Fatalf("final off click: %v", err)
			}
			sv.Frame()
			if shotDir := os.Getenv("RAKKO_NP_SHOT_DIR"); shotDir != "" {
				saveSwitchShot(t, filepath.Join(shotDir, tc.name+"-settings-off.png"), sv)
			}

			saved, err := settings.Load(d)
			if err != nil || saved.LiveActivities.NowPlaying {
				t.Fatalf("persisted NowPlaying = %v, err = %v; want false", saved.LiveActivities.NowPlaying, err)
			}
			if acts := a.liveActivities(); len(acts) != 0 {
				t.Fatalf("liveActivities while off = %v, want empty", acts)
			}
			a.visible, a.level, a.tab = true, 2, pageHome
			a.ensurePages()
			testH := sheetH + navGap + navH + 2
			hv := ui.NewTester(a.view, sheetW+2*earR, testH)
			hv.Frame()
			hv.Frame()
			if hv.HasText("夜曲") {
				t.Fatalf("home page shows track while off: %v", hv.Texts())
			}
			if shotDir := os.Getenv("RAKKO_NP_SHOT_DIR"); shotDir != "" {
				saveSwitchShot(t, filepath.Join(shotDir, tc.name+"-home-off.png"), hv)
			}

			a.tab = pageTools
			tv := ui.NewTester(a.view, int(a.pageShellW())+2*earR, int(a.pageShellH())+navGap+navH+2)
			tv.Frame()
			tv.Frame()
			if tv.HasText("播放进度") {
				t.Fatalf("tools page shows music card while off: %v", tv.Texts())
			}
			if !slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == "playing" }) {
				t.Fatal("layout dropped playing card instead of keeping it inactive")
			}
			if shotDir := os.Getenv("RAKKO_NP_SHOT_DIR"); shotDir != "" {
				saveSwitchShot(t, filepath.Join(shotDir, tc.name+"-tools-off.png"), tv)
			}

			// Playback shortcut handler must not run playerShortcut when disabled.
			origPlayer := playerShortcut
			var called int
			playerShortcut = func(*shelf, platform.NowPlayingCommand) { called++ }
			t.Cleanup(func() { playerShortcut = origPlayer })
			a.shortcutRun(shortcutPlayPause)()
			a.shortcutRun(shortcutPrevious)()
			a.shortcutRun(shortcutNext)()
			if called != 0 {
				t.Fatalf("playback shortcuts fired %d times while disabled", called)
			}
		})
	}
}

func saveSwitchShot(t *testing.T, path string, view *ui.Tester) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, view.Image()); err != nil {
		t.Fatal(err)
	}
}
