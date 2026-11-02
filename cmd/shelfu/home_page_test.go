//go:build darwin

package main

// The home page: player and lyrics states, the two idle states, the drop
// refusal, and the evidence renders. Rendering is headless (ui.Tester); no
// test asks lrclib.net unless it installs its own seam.

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/agentnotify"
	"shelf/internal/aiusage"
	"shelf/internal/lyrics"
	"shelf/internal/mac"
	"shelf/internal/settings"
	"shelf/internal/store"
)

// homeShelf builds a home page at level 2 with a track when st is not nil.
func homeShelf(t *testing.T, st *mac.NowPlayingState) (*shelf, *ui.Tester, *homePage) {
	t.Helper()
	return homeShelfWith(t, st, sheetW+2*earR, nil)
}

// homeShelfWith runs prep before the tester's first frame, so a test can
// fill the lyric cache (or the page's lines) before the page looks for them.
func homeShelfWith(t *testing.T, st *mac.NowPlayingState, width int, prep func(*shelf, *homePage)) (*shelf, *ui.Tester, *homePage) {
	t.Helper()
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(), level: 2, visible: true, tab: pageHome,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	if st != nil {
		st.Received = time.Now()
		a.np = &nowPlayingModule{host: a, have: true, last: *st}
	}
	a.setTab(pageHome)
	a.ensurePages()
	p, _ := a.page(pageHome).(*homePage)
	if prep != nil {
		prep(a, p)
	}
	view := ui.NewTester(a.view, width, panelTestH)
	return a, view, p
}

func homeTrack() mac.NowPlayingState {
	return mac.NowPlayingState{
		Title: "夜曲", Artist: "周杰伦", Album: "十一月的萧邦",
		Duration: 227, Elapsed: 56, Rate: 1, Playing: true,
	}
}

func TestHomePlayerWithoutLyrics(t *testing.T) {
	st := homeTrack()
	a, view, _ := homeShelf(t, &st)
	view.Frame()
	view.Frame()
	for _, text := range []string{"夜曲", "周杰伦", "0:56", "-2:51"} {
		if !view.HasText(text) {
			t.Fatalf("player lacks %q; texts %v", text, view.Texts())
		}
	}
	if _, ok := view.Find("播放进度"); !ok {
		t.Fatal("the player has no seek rail")
	}
	if view.HasText("托盘里 2 个文件") {
		t.Fatal("a loaded track must show the player, not the idle page")
	}
	if w := a.pageShellW(); w != sheetW {
		t.Fatalf("a player with the lyrics column closed is %.0f wide, want %.0f", w, float32(sheetW))
	}
	// The lyrics button opens the column beside the player, says when
	// nothing was found, and the choice is kept in settings.
	oldGo, oldMain := homeGo, homeOnMain
	homeGo = func(fn func()) { fn() }
	homeOnMain = func(fn func()) { fn() }
	t.Cleanup(func() { homeGo, homeOnMain = oldGo, oldMain })
	clickVisible(t, view, "歌词（来自 lrclib.net）")
	view.SetSize(homeLyricsW+2*earR, panelTestH)
	view.Frame()
	view.Frame()
	if !a.settings.LyricsOn() || a.pageShellW() != homeLyricsW {
		t.Fatalf("lyrics button: on %v, width %.0f", a.settings.LyricsOn(), a.pageShellW())
	}
	if !view.HasText("没有找到这首歌的歌词") || !view.HasText("夜曲") {
		t.Fatalf("an empty lookup must say so beside the player; texts %v", view.Texts())
	}
}

func TestHomeLyricsWidenAndAdvance(t *testing.T) {
	st := homeTrack()
	st.Elapsed = 6
	a, view, p := homeShelfWith(t, &st, homeLyricsW+2*earR, func(a *shelf, p *homePage) {
		p.key = homeTrackKey(st)
		p.lines = lyrics.ParseLRC("[00:00.00]第一句\n[00:05.00]第二句\n[00:10.00]第三句\n[00:15.00]第四句")
		p.fetched = true
		a.settings.SetLyrics(true)
	})
	view.Frame()
	view.Frame()
	if w := a.pageShellW(); w != homeLyricsW {
		t.Fatalf("with lyrics the page is %.0f wide, want %d", w, homeLyricsW)
	}
	if f := a.desiredFrame(); f.Width < homeLyricsW+2*earR {
		t.Fatalf("the native frame is %d wide, cannot hold the %d shell and its ears", f.Width, homeLyricsW)
	}
	for _, text := range []string{"第一句", "第二句", "第三句", "第四句"} {
		if !view.HasText(text) {
			t.Fatalf("lyrics lack %q; texts %v", text, view.Texts())
		}
	}
	if p.shown != 1 {
		t.Fatalf("at 0:56 the current line is %d, want 1 (第二句)", p.shown)
	}
	view.Advance(5 * time.Second)
	view.Frame()
	if p.shown != 2 {
		t.Fatalf("five seconds later the current line is %d, want 2 (第三句)", p.shown)
	}
	// Turning lyrics off narrows the page again and hides the block.
	a.setLyrics(false)
	view.SetSize(sheetW+2*earR, panelTestH)
	view.Frame()
	view.Frame()
	if a.pageShellW() != sheetW || view.HasText("第二句") {
		t.Fatalf("lyrics off: width %.0f, lyrics shown %v", a.pageShellW(), view.HasText("第二句"))
	}
}

func TestHomeLyricsLookupFailureKeepsPlayer(t *testing.T) {
	st := homeTrack()
	oldFetch, oldGo, oldMain := homeFetch, homeGo, homeOnMain
	homeFetch = func(context.Context, lyrics.Track) (lyrics.Result, error) {
		return lyrics.Result{}, context.DeadlineExceeded
	}
	homeGo = func(fn func()) { fn() }
	homeOnMain = func(fn func()) { fn() }
	t.Cleanup(func() { homeFetch, homeGo, homeOnMain = oldFetch, oldGo, oldMain })

	a, view, p := homeShelfWith(t, &st, homeLyricsW+2*earR, func(a *shelf, _ *homePage) {
		a.settings.SetLyrics(true)
	})
	view.Frame()
	view.Frame()
	if !p.failed {
		t.Fatal("a failed lookup must be recorded for this track")
	}
	if a.pageShellW() != homeLyricsW || !view.HasText("夜曲") || !view.HasText("歌词暂时取不到") {
		t.Fatalf("a failed lookup must say so beside the player: width %.0f, texts %v", a.pageShellW(), view.Texts())
	}
	// The failure is not retried on every frame.
	calls := 0
	homeFetch = func(context.Context, lyrics.Track) (lyrics.Result, error) { calls++; return lyrics.Result{}, nil }
	view.Frame()
	if calls != 0 {
		t.Fatalf("a failed track was retried %d times", calls)
	}
}

func TestHomeCachedLyricsLoadWithoutNetwork(t *testing.T) {
	st := homeTrack()
	oldFetch := homeFetch
	homeFetch = func(context.Context, lyrics.Track) (lyrics.Result, error) {
		t.Fatal("a cached track must not go online")
		return lyrics.Result{}, nil
	}
	t.Cleanup(func() { homeFetch = oldFetch })

	a, view, p := homeShelfWith(t, &st, homeLyricsW+2*earR, func(a *shelf, _ *homePage) {
		a.settings.SetLyrics(true)
		a.lyricsCache().Put(homeTrackKey(st), lyrics.Result{Synced: "[00:00.00]缓存第一句\n[00:05.00]缓存第二句"})
	})
	view.Frame()
	view.Frame()
	if !view.HasText("缓存第一句") || p.key != homeTrackKey(st) {
		t.Fatalf("cached lyrics did not load: %v", view.Texts())
	}
	if a.pageShellW() != homeLyricsW {
		t.Fatalf("cached lyrics did not widen the page: %.0f", pageShellWidth(p))
	}
}

// homeFixtureStore fills the tray with two pictures and the clipboard with
// one line of text, with thumbnails ready to draw.
func homeFixtureStore(t *testing.T, a *shelf) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.store = st
	dir := t.TempDir()
	colors := []color.RGBA{{90, 60, 200, 255}, {220, 110, 120, 255}}
	for i, c := range colors {
		path := filepath.Join(dir, []string{"cover.png", "photo.png"}[i])
		img := image.NewRGBA(image.Rect(0, 0, 80, 80))
		for y := 0; y < 80; y++ {
			for x := 0; x < 80; x++ {
				img.Set(x, y, c)
			}
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if err := st.AddFiles([]string{path}); err != nil {
			t.Fatal(err)
		}
		bmp, err := ui.DecodeBitmap(mustPNG(t, img))
		if err != nil {
			t.Fatal(err)
		}
		a.thumbnails[st.Items[0].ID] = bmp
	}
	if err := st.AddClipText("git merge --ff-only fix 之后再继续", "com.apple.Terminal"); err != nil {
		t.Fatal(err)
	}
}

func mustPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// agentFixture puts one running and two finished sessions in the feature.
func agentFixture(a *shelf, now time.Time) {
	a.settings.AgentNotify.Enabled = true
	f := a.agentFeature()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks["codex|s1"] = agentTask{tool: agentnotify.ToolCodex, task: "整理发布说明并更新 CHANGELOG", at: now.Add(-3*time.Minute - 5*time.Second)}
	f.finished["claude|s2"] = agentTask{tool: agentnotify.ToolClaude, task: "修好解析器失败的测试", at: now.Add(-20 * time.Second)}
	f.finished["cursor|s3"] = agentTask{tool: agentnotify.ToolCursor, task: "重构设置页布局", at: now.Add(-12*time.Minute - 5*time.Second)}
}

func TestHomeIdleOtterAndStatus(t *testing.T) {
	a, view, _ := homeShelf(t, nil)
	homeFixtureStore(t, a)
	agentFixture(a, time.Now())
	view.Frame()
	view.Frame()
	l := a.appLoc()
	for _, text := range []string{l.HomeDate(time.Now()), l.HomeWeekday(time.Now()),
		"Codex 正在：整理发布说明并更新 CHANGELOG", "托盘里 2 个文件"} {
		if !view.HasText(text) {
			t.Fatalf("idle home lacks %q; texts %v", text, view.Texts())
		}
	}
	if _, ok := view.Find("0:56"); ok {
		t.Fatal("no track, but the player drew")
	}
	// With nothing running, the newest finished turn takes the line.
	f := a.agentFeature()
	f.mu.Lock()
	delete(f.tasks, "codex|s1")
	f.mu.Unlock()
	view.Frame()
	if !view.HasText("Claude Code 刚完成：修好解析器失败的测试") {
		t.Fatalf("the newest finished turn is missing; texts %v", view.Texts())
	}
}

func TestHomeIdleAwakeThenQuota(t *testing.T) {
	a, view, _ := homeHelfCompact(t)
	view.Frame()
	view.Frame()
	if !view.HasText("保持唤醒 · 剩余 42:00") || view.HasText("Claude Code 额度剩 62%") {
		t.Fatalf("keep awake must take the third line while on; texts %v", view.Texts())
	}
	if view.HasText("Codex 正在：整理发布说明并更新 CHANGELOG") {
		t.Fatal("the agent line must not appear while the switch is off")
	}
	a.awake = nil
	view.Frame()
	if !view.HasText("Claude Code 额度剩 62%") {
		t.Fatalf("with keep awake off the tightest quota shows; texts %v", view.Texts())
	}
}

func homeHelfCompact(t *testing.T) (*shelf, *ui.Tester, *homePage) {
	t.Helper()
	a, view, p := homeShelf(t, nil)
	homeFixtureStore(t, a)
	now := time.Now()
	a.awake = &awakeModule{host: a, id: mac.PowerAssertionID(1), duration: time.Hour,
		until: now.Add(42 * time.Minute), now: func() time.Time { return now }}
	a.settings.AIUsage.Enabled = true
	a.usage = &usageFeature{host: a, deps: usageDeps{
		now:         func() time.Time { return now },
		fetchClaude: func(context.Context) aiusage.Quota { return aiusage.Quota{} },
		fetchCodex:  func(context.Context) aiusage.Quota { return aiusage.Quota{} },
	}}
	a.usage.quota[quotaIndex(aiusage.ToolClaude)] = usageQuotaState{
		latest: aiusage.Quota{Status: aiusage.QuotaOK, FetchedAt: now,
			Windows: []aiusage.Window{{Name: "5h", Length: 5 * time.Hour, UsedPct: 38, ResetsAt: now.Add(2 * time.Hour)}}},
	}
	return a, view, p
}

// The shell and the content share one width, and the drop refusal keeps a
// file drag from replacing the home page with the tray's drop zone.
func TestHomeWidthAndNoDrop(t *testing.T) {
	a, view, p := homeShelf(t, nil)
	view.Frame()
	if a.pageShellW() != sheetW || pageBoxW(p) != contentW {
		t.Fatalf("plain home = shell %.0f content %.0f", a.pageShellW(), pageBoxW(p))
	}
	old := os.Getenv("SHELF_DEBUG_DROP_FILES")
	t.Setenv("SHELF_DEBUG_DROP_FILES", "/etc/hosts")
	t.Cleanup(func() { os.Setenv("SHELF_DEBUG_DROP_FILES", old) })
	view.Frame()
	view.Frame()
	if a.drop.on {
		t.Fatal("the home page opened a drop zone")
	}
}

// --- evidence renders ----------------------------------------------------

const homeShotDir = "/tmp/rakko-home"

// homeShotContent is one language's fixtures, so neither screenshot shows
// Han characters in the English set.
type homeShotContent struct {
	title, artist, clip string
	lyrics              []string
	tasks               []string
	names               [2]string
}

var homeShots = map[string]homeShotContent{
	"zh": {
		title: "夜曲", artist: "周杰伦", clip: "git merge --ff-only fix 之后再继续",
		lyrics: []string{"为你弹奏萧邦的夜曲", "纪念我死去的爱情", "跟夜风一样的声音", "心碎的很好听", "手在键盘敲很轻"},
		tasks:  []string{"整理发布说明并更新 CHANGELOG", "修好解析器失败的测试", "重构设置页布局"},
		names:  [2]string{"封面.png", "照片.png"},
	},
	"en": {
		title: "Talking on the Phone", artist: "Olivia Rodrigo", clip: "git merge --ff-only fix, then keep going",
		lyrics: []string{"But I got a feeling", "Wounds are healing", "Talking on the phone", "I know everybody changes", "But I hope that we don't"},
		tasks:  []string{"Write the release notes", "Fix the failing parser test", "Rework the settings layout"},
		names:  [2]string{"cover.png", "photo.png"},
	},
}

// TestHomeScreenshots renders the four states in both languages at 2× into
// /tmp/rakko-home, and audits that no label on the page is cut or outside
// the shell. Run with SHELF_HOME_SHOTS=1.
func TestHomeScreenshots(t *testing.T) {
	if os.Getenv("SHELF_HOME_SHOTS") != "1" {
		t.Skip("set SHELF_HOME_SHOTS=1 to write the home page renders")
	}
	if err := os.MkdirAll(homeShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"zh", "en"} {
		c := homeShots[lang]
		for _, state := range []struct {
			name string
			sel  func(t *testing.T, c homeShotContent) (*shelf, *ui.Tester, *homePage)
			w    int
		}{
			{"1-lyrics", homeShotLyrics, homeLyricsW + 2*earR},
			{"2-player", homeShotPlayer, sheetW + 2*earR},
			{"3-agent", homeShotAgent, sheetW + 2*earR},
			{"3p-recent", homeShotCompact, sheetW + 2*earR},
		} {
			a, view, p := state.sel(t, c)
			view.SetLocale(map[string]string{"zh": "zh-Hans", "en": "en"}[lang])
			view.SetSize(state.w, panelTestH)
			view.SetScale(2)
			view.Frame()
			view.Frame()
			view.Advance(700 * time.Millisecond) // let the lyric scroll settle
			view.Frame()
			auditHome(t, a, view, lang, state.name)
			img := view.Image()
			f, err := os.Create(filepath.Join(homeShotDir, "home-"+state.name+"-"+lang+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				t.Fatal(err)
			}
			f.Close()
			if pageShellWidth(p) != float32(state.w-2*earR) {
				t.Errorf("%s/%s: page width %.0f in a %d window", lang, state.name, pageShellWidth(p), state.w)
			}
		}
	}
}

// shotArt is the gradient cover the evidence renders show; the real app
// decodes whatever the player publishes.
func shotArt() *ui.Bitmap {
	img := image.NewRGBA(image.Rect(0, 0, 160, 160))
	for y := range 160 {
		for x := range 160 {
			img.Set(x, y, color.RGBA{uint8(90 + x/3), uint8(50 + y/5), uint8(190 - x/4), 255})
		}
	}
	return ui.NewBitmap(img)
}

func homeShotLyrics(t *testing.T, c homeShotContent) (*shelf, *ui.Tester, *homePage) {
	t.Helper()
	st := homeTrack()
	st.Title, st.Artist, st.Elapsed = c.title, c.artist, 10.2
	a, view, p := homeShelfWith(t, &st, homeLyricsW+2*earR, func(a *shelf, p *homePage) {
		a.settings.SetLyrics(true)
		p.key = homeTrackKey(st)
		var b []string
		for i, line := range c.lyrics {
			b = append(b, "["+[]string{"00:00", "00:05", "00:10", "00:15", "00:20"}[i]+".00]"+line)
		}
		p.lines = lyrics.ParseLRC(stJoin(b, "\n"))
		p.fetched = true
	})
	a.np.art, a.np.artColor = shotArt(), ui.Hex("#D86B9E")
	return a, view, p
}

func homeShotPlayer(t *testing.T, c homeShotContent) (*shelf, *ui.Tester, *homePage) {
	t.Helper()
	st := homeTrack()
	st.Title, st.Artist = c.title, c.artist
	a, view, p := homeShelf(t, &st)
	a.np.art, a.np.artColor = shotArt(), ui.Hex("#D86B9E")
	return a, view, p
}

func homeShotAgent(t *testing.T, c homeShotContent) (*shelf, *ui.Tester, *homePage) {
	t.Helper()
	a, view, p := homeShelf(t, nil)
	homeShotFixtureStore(t, a, c)
	now := time.Now()
	a.settings.AgentNotify.Enabled = true
	f := a.agentFeature()
	f.mu.Lock()
	f.tasks["codex|s1"] = agentTask{tool: agentnotify.ToolCodex, task: c.tasks[0], at: now.Add(-3*time.Minute - 5*time.Second)}
	f.finished["claude|s2"] = agentTask{tool: agentnotify.ToolClaude, task: c.tasks[1], at: now.Add(-20 * time.Second)}
	f.finished["cursor|s3"] = agentTask{tool: agentnotify.ToolCursor, task: c.tasks[2], at: now.Add(-12*time.Minute - 5*time.Second)}
	f.mu.Unlock()
	return a, view, p
}

func homeShotCompact(t *testing.T, c homeShotContent) (*shelf, *ui.Tester, *homePage) {
	t.Helper()
	a, view, p := homeHelfCompactWith(t, c)
	return a, view, p
}

// homeHelfCompactWith is the ③′ fixture for one language.
func homeHelfCompactWith(t *testing.T, c homeShotContent) (*shelf, *ui.Tester, *homePage) {
	t.Helper()
	a, view, p := homeShelf(t, nil)
	homeShotFixtureStore(t, a, c)
	now := time.Now()
	a.awake = &awakeModule{host: a, id: mac.PowerAssertionID(1), duration: time.Hour,
		until: now.Add(42 * time.Minute), now: func() time.Time { return now }}
	a.settings.AIUsage.Enabled = true
	a.usage = &usageFeature{host: a, deps: usageDeps{
		now:         func() time.Time { return now },
		fetchClaude: func(context.Context) aiusage.Quota { return aiusage.Quota{} },
		fetchCodex:  func(context.Context) aiusage.Quota { return aiusage.Quota{} },
	}}
	a.usage.quota[quotaIndex(aiusage.ToolClaude)] = usageQuotaState{
		latest: aiusage.Quota{Status: aiusage.QuotaOK, FetchedAt: now,
			Windows: []aiusage.Window{{Name: "5h", Length: 5 * time.Hour, UsedPct: 38, ResetsAt: now.Add(2 * time.Hour)}}},
	}
	return a, view, p
}

func homeShotFixtureStore(t *testing.T, a *shelf, c homeShotContent) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.store = st
	dir := t.TempDir()
	for i, col := range []color.RGBA{{90, 60, 200, 255}, {220, 110, 120, 255}} {
		path := filepath.Join(dir, c.names[i])
		img := image.NewRGBA(image.Rect(0, 0, 80, 80))
		for y := range 80 {
			for x := range 80 {
				img.Set(x, y, col)
			}
		}
		data := mustPNG(t, img)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := st.AddFiles([]string{path}); err != nil {
			t.Fatal(err)
		}
		bmp, err := ui.DecodeBitmap(data)
		if err != nil {
			t.Fatal(err)
		}
		a.thumbnails[st.Items[0].ID] = bmp
	}
	if err := st.AddClipText(c.clip, "com.apple.Terminal"); err != nil {
		t.Fatal(err)
	}
}

// auditHome fails when a text on the page is cut or drawn outside the
// shell, and (English) when the page says Chinese.
func auditHome(t *testing.T, a *shelf, view *ui.Tester, lang, state string) {
	t.Helper()
	w := a.pageShellW()
	var texts []string
	for _, text := range view.Texts() {
		r, ok := view.Find(text)
		if !ok || r.Y >= sheetH {
			continue // the page capsule below the shell is not the page
		}
		texts = append(texts, text)
		if r.X < -0.5 || r.Y < -0.5 || r.X+r.W > w+0.5 || r.Y+r.H > sheetH+0.5 {
			t.Errorf("%s/%s: %q at %+v leaves the %.0f×%d shell", lang, state, text, r, w, sheetH)
		}
	}
	if lang == "en" {
		assertNoHan(t, state, texts)
	}
}

func stJoin(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// The music card's tap opens the home page, not a second player page.
func TestMusicCardOpensHome(t *testing.T) {
	a := &shelf{settings: settings.Default()}
	p := &toolsPage{host: a}
	p.toolCardTapped("playing")
	if a.tab != pageHome {
		t.Fatalf("the music card opened %q, want the home page", a.tab)
	}
	if a.page(pageHome) == nil {
		t.Fatal("the home page is not registered")
	}
}

// Leaving the home page and coming back keeps the lyric state and both
// widths; the shell target follows the page on screen.
func TestHomeSwitchAwayAndBack(t *testing.T) {
	st := homeTrack()
	st.Elapsed = 6
	a, view, p := homeShelfWith(t, &st, homeLyricsW+2*earR, func(a *shelf, p *homePage) {
		a.settings.SetLyrics(true)
		p.key = homeTrackKey(st)
		p.lines = lyrics.ParseLRC("[00:00.00]第一句\n[00:05.00]第二句\n[00:10.00]第三句")
		p.fetched = true
	})
	view.Frame()
	if a.pageShellW() != homeLyricsW {
		t.Fatalf("home with lyrics = %.0f, want %d", a.pageShellW(), homeLyricsW)
	}
	a.setTab(pageTray)
	view.SetSize(sheetW+2*earR, panelTestH)
	view.Frame()
	view.Frame()
	if a.pageShellW() != sheetW {
		t.Fatalf("tray page = %.0f, want %d", a.pageShellW(), sheetW)
	}
	a.openHomePage()
	view.SetSize(homeLyricsW+2*earR, panelTestH)
	view.Frame()
	view.Frame()
	if a.pageShellW() != homeLyricsW || !view.HasText("第二句") {
		t.Fatalf("back home: width %.0f, lyrics %v", a.pageShellW(), view.HasText("第二句"))
	}
	if p.shown < 0 {
		t.Fatal("the lyric line was lost on the way back")
	}
}

// A paused track keeps the player with the play glyph.
func TestHomePlayerPaused(t *testing.T) {
	st := homeTrack()
	st.Playing = false
	_, view, _ := homeShelf(t, &st)
	view.Frame()
	view.Frame()
	if !view.HasText("夜曲") || !view.HasText("播放") {
		t.Fatalf("paused player: %v", view.Texts())
	}
	if view.HasText("暂停") {
		t.Fatal("a paused track must offer play, not pause")
	}
}
