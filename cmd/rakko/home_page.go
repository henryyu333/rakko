//go:build darwin

package main

// 首页（design/explore/home-v3.html、home-v4.html）：点刘海仍是搜索条，页面
// 胶囊最左边的「首页」进这里。它只看「此刻」：有正在播放时就是播放器，左下
// 角的歌词按钮开着时加宽到 640，右边滚动歌词（找不到时写一行原因）；没有
// 播放时是海獭陪着：今天的日期和几行状态。没有方格、不接受拖入、没有编辑
// 模式。

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/lyrics"
	"rakko/internal/mac"
	"rakko/internal/store"
	"rakko/locales"
)

const (
	homeLyricsW     = 640              // the page beside the lyrics column
	homePlayerCol   = 300              // the player's column there
	homeLineH       = 28               // one lyric line
	homeLyricsBtn   = 30               // the player's lyrics button
	homeOtterW      = 112              // the idle page's otter
	homeLyricsWait  = 10 * time.Second // lrclib, its retries and netease
	homeLyricsRetry = 30 * time.Second // before a failed track is asked again
)

var (
	// homeSymbol is the house glyph in the capsule.
	homeSymbol = symbol(`<path d="M4 11l8-6 8 6v8H4z"/><path d="M10 19v-5h4v5"/>`)
	// lyricsSymbol is the player's lyrics button: lines of text and a note.
	lyricsSymbol = symbol(`<path d="M4 6h11M4 10h11M4 14h7"/><circle cx="16.5" cy="17" r="2.2"/><path d="M18.7 17V9l2.3 1.2"/>`)

	// homeFetch is the network seam: the app asks lrclib.net, then NetEase;
	// tests replace it (TestMain makes it answer "no lyrics" so no test goes
	// online).
	homeFetch = func(ctx context.Context, t lyrics.Track) (lyrics.Result, error) {
		f := lyrics.Fetcher{UserAgent: fmt.Sprintf("Rakko/%s (https://github.com/henryyu333/rakko)", appVersion())}
		return f.Fetch(ctx, t)
	}
	// homeGo and homeOnMain move the request off the UI thread and the
	// answer back onto it; tests run both inline.
	homeGo     = func(fn func()) { go fn() }
	homeOnMain = mygo.RunOnMain
)

// homePage is the home page. The lyric state belongs to the page, not the
// now-playing module: it depends on the track that is loaded, and a new
// track starts a new lookup while the old answer stays in the cache.
type homePage struct {
	host *shelf

	key      string // the track the state below belongs to
	lines    []lyrics.Line
	synced   string
	none     bool // a completed lookup with nothing to show
	loading  bool
	failed   bool      // this track's lookup failed: stay on the player
	failedAt time.Time // when it failed; a retry waits homeLyricsRetry
	fetched  bool      // the fetch for key was started
	shown    int       // lyric line the scroll was last moved to
	scrollTo int       // line to centre once the scroll has a measured range

	lscroll ui.ScrollState
}

func (p *homePage) id() pageID                        { return pageHome }
func (p *homePage) label() string                     { return locales.PageHome() }
func (p *homePage) labelIn(l locales.Messages) string { return l.PageHome() }
func (p *homePage) title() string                     { return locales.PageHome() }
func (p *homePage) titleIn(l locales.Messages) string { return l.PageHome() }
func (p *homePage) enabled() bool                     { return true }
func (p *homePage) fullBleed() bool                   { return true }
func (p *homePage) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, homeSymbol)
}
func (p *homePage) actions(c *ui.Context) []ui.Element { return nil }

// width is the shell width this page needs now: 640 while the lyrics
// column is open beside a track, otherwise the sheet's 440. layout calls it
// before the body, so the shell, the window frame and the content agree on
// one width.
func (p *homePage) width() float32 {
	p.prepare(false)
	if _, ok := p.track(); !ok || !p.host.settings.LyricsOn() {
		return sheetW
	}
	return homeLyricsW
}

// track is the system's current sound, when the now-playing feature is on.
func (p *homePage) track() (mac.NowPlayingState, bool) {
	if p.host.np == nil || !p.host.np.hasTrack() {
		return mac.NowPlayingState{}, false
	}
	return p.host.np.track()
}

// body draws the state for what is playing now.
func (p *homePage) body(c *ui.Context, _ bool, live bool) ui.Element {
	p.prepare(live)
	w := pageBoxW(p)
	st, ok := p.track()
	if !ok {
		return p.idleBody(c, w)
	}
	if p.host.settings.LyricsOn() {
		return p.lyricsBody(c, w, st)
	}
	return p.playerBody(c, w, st)
}

// prepare keeps the lyric state for the loaded track: a new track resets
// it and reads the cache; a live page asks for one lookup when the cache
// has nothing. It is safe to call from width() (live false) and the body.
func (p *homePage) prepare(live bool) {
	st, ok := p.track()
	if !ok {
		p.key, p.lines, p.none, p.loading, p.failed, p.failedAt, p.fetched, p.shown, p.scrollTo = "", nil, false, false, false, time.Time{}, false, -1, -1
		return
	}
	key := homeTrackKey(st)
	if key != p.key {
		p.key, p.lines, p.none, p.loading, p.failed, p.failedAt, p.fetched, p.shown, p.scrollTo = key, nil, false, false, false, time.Time{}, false, -1, -1
		if r, ok := p.host.lyricsCache().Get(key); ok {
			p.adopt(r)
		}
	}
	if p.failed && !p.failedAt.IsZero() && time.Since(p.failedAt) >= homeLyricsRetry {
		// The service was busy or down; ask once more after a pause
		// instead of blacklisting the track for as long as it plays.
		p.failed, p.fetched = false, false
	}
	if !live || !p.host.settings.LyricsOn() || p.loading || p.fetched || p.failed || len(p.lines) > 0 || p.none {
		return
	}
	p.fetched, p.loading = true, true
	t := lyrics.Track{
		Title: st.Title, Artist: st.Artist, Album: st.Album,
		Duration: int(math.Round(st.Duration)),
	}
	homeGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), homeLyricsWait)
		defer cancel()
		res, err := homeFetch(ctx, t)
		homeOnMain(func() { p.apply(key, res, err) })
	})
}

// adopt folds one answer into the page's lines: only synced words count,
// and an instrumental or empty answer leaves the player up.
func (p *homePage) adopt(r lyrics.Result) {
	lines := lyrics.ParseLRC(r.Synced)
	if r.Instrumental || len(lines) == 0 {
		r = lyrics.Result{None: true, Instrumental: r.Instrumental}
		lines = nil
	}
	p.lines, p.none = lines, r.None
}

// apply stores a lookup answer and asks for the frame that widens the page
// to its lyrics. Errors leave the player up.
func (p *homePage) apply(key string, r lyrics.Result, err error) {
	if key != p.key {
		return // the track changed while the answer was on its way
	}
	p.loading = false
	if err != nil {
		p.failed, p.failedAt = true, time.Now()
		return
	}
	p.adopt(r)
	p.host.lyricsCache().Put(key, r)
	p.host.homeChanged()
}

// homeTrackKey is the cache key for a track: title, artist and the
// rounded duration LRCLIB matches on.
func homeTrackKey(st mac.NowPlayingState) string {
	return lyrics.Key(lyrics.Track{Title: st.Title, Artist: st.Artist, Duration: int(math.Round(st.Duration))})
}

// openHomePage opens the home page (the tools page's music card, or a
// caller that wants the player). The notch's expand gestures still open the
// search bar.
func (a *shelf) openHomePage() {
	if !a.visible {
		a.show(false)
	}
	a.openPage(pageHome)
}

// homeChanged re-asks the window for the page's new width and one frame.
func (a *shelf) homeChanged() {
	a.syncWindowFrame()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// setLyrics opens or closes the lyrics column (the player's lyrics button).
// The page reads it before the next layout, so closing narrows the page at
// once and stops any later request; a lookup already in flight is ignored.
func (a *shelf) setLyrics(on bool) {
	next := a.settings
	next.SetLyrics(on)
	if !a.applySettings(next) {
		return
	}
	if p, ok := a.page(pageHome).(*homePage); ok {
		p.shown = -1
	}
	a.homeChanged()
}

// lyricsCache is the per-shelf cache in the data directory.
func (a *shelf) lyricsCache() *lyrics.Cache {
	if a.lyrCache == nil {
		a.lyrCache = lyrics.NewCache(a.dataDir)
	}
	return a.lyrCache
}

// playerBody is state ②: cover, title/artist, equalizer, the seek rail and
// the transport, centred in the page.
func (p *homePage) playerBody(c *ui.Context, w float32, st mac.NowPlayingState) ui.Element {
	ui.Column(c.Key("home-player")).Width(w).Height(pageBodyH).Justify(ui.Center).Children(func() {
		p.player(c, w, st)
	})
	return ui.Box(c).Size(0, 0)
}

// player draws the player in w points: one row of artwork and names, the
// seek row, and the three transport buttons.
func (p *homePage) player(c *ui.Context, w float32, st mac.NowPlayingState) {
	m := p.host.np
	l := loc(c)
	now := c.Now()
	dur, pos := st.Duration, st.Position(now)
	if dur > 0 {
		pos = min(max(pos, 0), dur)
	}
	frac := float32(0)
	if dur > 0 {
		frac = float32(pos / dur)
	}
	if p.host.music.seek.drag && dur > 0 {
		frac = p.host.music.seek.frac
		pos = float64(frac) * dur
	}
	ui.Row(c.Key("home-art-row")).Width(w).AlignItems(ui.Center).Gap(14).Children(func() {
		m.coverArt(c, "home-art", 64, 12)
		ui.Column(c.Key("home-names")).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c.Key("home-title"), st.Title).SingleLine().FontSize(15).FontWeight(600).TextColor(ink)
			if st.Artist != "" {
				ui.Text(c.Key("home-artist"), st.Artist).SingleLine().FontSize(13).TextColor(ink2)
			}
		})
		if st.Playing {
			ui.Box(c.Key("home-eq")).Width(34).Children(func() { m.eqBars(c, true) })
		}
	})
	ui.Row(c.Key("home-seek")).Margin(14, 0, 0, 0).Width(w).AlignItems(ui.Center).Gap(8).Children(func() {
		rem := float64(0)
		if dur > 0 {
			rem = max(0, dur-pos)
		}
		nowLabel, remLabel := mac.FormatClock(pos), "-"+mac.FormatClock(rem)
		nw, rw := clockLabelW(c, nowLabel, 11.5), clockLabelW(c, remLabel, 11.5)
		panelLabel(c, nil, nowLabel, 11.5, 500).TextColor(ink3).Width(nw)
		p.host.seekRail(c, "home-progress", frac, dur, 14, 5).Grow(1).MinWidth(0)
		panelLabel(c, nil, remLabel, 11.5, 500).TextColor(ink3).Width(rw)
	})
	// The lyrics button sits at the left and an empty slot of the same
	// width at the right, so the transport stays centred.
	ui.Row(c.Key("home-ctrl")).Margin(16, 0, 0, 0).Width(w).Justify(ui.SpaceBetween).
		AlignItems(ui.Center).Children(func() {
		p.lyricsButton(c)
		ui.Row(c.Key("home-transport")).AlignItems(ui.Center).Gap(26).Children(func() {
			m.controlButton(c, "home-prev", prevFill, l.PreviousTrack(), mac.NPPrevious, 30, 17)
			m.controlButton(c, "home-toggle", toggleSymbol(st.Playing), playPauseText(l, st.Playing), mac.NPPlayPause, 38, 22)
			m.controlButton(c, "home-next", nextFill, l.NextTrack(), mac.NPNext, 30, 17)
		})
		ui.Box(c.Key("home-ctrl-pad")).Size(homeLyricsBtn, homeLyricsBtn)
	})
	if st.Playing {
		c.After(time.Second) // the clock and the lyric line move on
	}
}

// lyricsButton opens and closes the lyrics column; lit while it is open.
func (p *homePage) lyricsButton(c *ui.Context) {
	on := p.host.settings.LyricsOn()
	b := ui.ButtonBase(c.Key("home-lyrics-btn")).Size(homeLyricsBtn, homeLyricsBtn).
		Radius(homeLyricsBtn / 2).Center().Label(loc(c).HomeLyrics())
	switch {
	case on:
		b.Background(fill2)
	case b.Hovered():
		b.Background(fill1)
	}
	b.Children(func() {
		col := ink2
		if on {
			col = ink
		}
		ui.Icon(c, lyricsSymbol).Size(17, 17).TextColor(col)
	})
	if b.Clicked() {
		p.host.setLyrics(!on)
	}
}

// lyricsBody is state ①: the player in a 300pt column, a rule, and the
// scrolling lyrics beside it.
func (p *homePage) lyricsBody(c *ui.Context, w float32, st mac.NowPlayingState) ui.Element {
	right := w - homePlayerCol - 1 - 2*18
	ui.Row(c.Key("home-with-lyrics")).Width(w).Height(pageBodyH).Gap(18).Children(func() {
		ui.Column(c.Key("home-left")).Width(homePlayerCol).Height(pageBodyH).
			Justify(ui.Center).Children(func() {
			p.player(c, homePlayerCol, st)
		})
		ui.Box(c.Key("home-rule")).Width(1).Height(150).Background(ui.RGBA(255, 255, 255, 0.09))
		ui.Column(c.Key("home-lyrics-col")).Width(right).Height(pageBodyH).Children(func() {
			if len(p.lines) == 0 {
				p.lyricsNote(c, right)
				return
			}
			p.lyricsView(c, st, right)
		})
	})
	return ui.Box(c).Size(0, 0)
}

// lyricsNote fills the lyrics column while there are no lines to show:
// still looking, nothing found for this track, or the service unreachable.
func (p *homePage) lyricsNote(c *ui.Context, w float32) {
	l := loc(c)
	text := l.HomeLyricsNone()
	switch {
	case p.loading || (!p.fetched && !p.none && !p.failed):
		text = l.HomeLyricsLoading()
	case p.failed:
		text = l.HomeLyricsFailed()
		// Nothing else animates on this page, so ask for the frame that
		// lets prepare ask again at the end of the pause.
		if wait := time.Until(p.failedAt.Add(homeLyricsRetry)); wait > 0 {
			c.After(wait)
		}
	}
	ui.Column(c.Key("home-lyrics-note")).Width(w).Height(pageBodyH).Center().Children(func() {
		panelLabel(c, nil, text, 13, 500).TextColor(ink3)
	})
}

// lyricsView is the scrolling lyric block: the current line bold and
// centred, the others in ink3 fading with distance and toward both edges.
// A change of line scrolls the block smoothly to centre it.
func (p *homePage) lyricsView(c *ui.Context, st mac.NowPlayingState, w float32) {
	idx := lyrics.LineAt(p.lines, time.Duration(st.Position(c.Now())*float64(time.Second)))
	if idx < 0 {
		return
	}
	viewH := float32(pageBodyH)
	pad := (viewH - homeLineH) / 2
	if idx != p.shown {
		p.shown = idx
		p.scrollTo = idx
	}
	// ScrollTo reads the range the last layout measured; on the first
	// frame of a new track it is still zero, so the request waits for the
	// frame that knows how far the block can move.
	if p.scrollTo >= 0 && p.lscroll.MaxY > 0 {
		p.lscroll.ScrollTo(0, float32(p.scrollTo)*homeLineH, ui.Smooth)
		p.scrollTo = -1
	}
	if p.scrollTo >= 0 || p.lscroll.Scrolling() {
		c.AnimationFrame() // the scroll waits for, or is on its way
	}
	ui.Scroll(c.Key("home-lyrics")).FillWidth().FillHeight().TrackScroll(&p.lscroll).Children(func() {
		ui.Column(c.Key("home-lyrics-lines")).FillWidth().Children(func() {
			ui.Box(c.Key("home-lyrics-pad-top")).Height(pad)
			for i, line := range p.lines {
				op := homeLineOpacity(i, idx, p.lscroll.Y, pad, viewH)
				ui.Row(c.Key(i)).Height(homeLineH).Width(w).Justify(ui.Center).
					AlignItems(ui.Center).Opacity(op).Children(func() {
					t := ui.Text(c, line.Text).SingleLine().TextAlign(ui.Center).
						MaxWidth(w - 8).Ellipsis("…")
					if i == idx {
						t.FontSize(16).FontWeight(700).TextColor(ink)
					} else {
						t.FontSize(14).TextColor(ink3)
					}
				})
			}
			ui.Box(c.Key("home-lyrics-pad-bottom")).Height(pad)
		})
	})
}

// homeLineOpacity fades a lyric line by its distance from the current one
// and by how near an edge it sits: 1 in the middle, about 0.2 at an edge.
func homeLineOpacity(i, cur int, scrollY, pad, viewH float32) float32 {
	d := i - cur
	if d < 0 {
		d = -d
	}
	base := float32(1)
	switch d {
	case 0:
		base = 1
	case 1:
		base = 0.62
	case 2:
		base = 0.36
	default:
		base = 0.2
	}
	y := pad + float32(i)*homeLineH - scrollY
	edge := min(1, (y+homeLineH)/44) * min(1, (viewH-y)/44)
	return base * max(0, min(1, edge))
}

// idleBody is the page with nothing playing (design/explore/home-v4.html,
// B): the otter on the left — standing, or asleep after a quiet spell —
// and today's date with a few lines of what is going on beside it.
func (p *homePage) idleBody(c *ui.Context, w float32) ui.Element {
	l := loc(c)
	now := c.Now()
	pose := petIdle
	if a := p.host; !a.lastAct.IsZero() && now.Sub(a.lastAct) > otterSleepyAfter {
		pose = petSleep
	}
	otter := otterImage(pose)
	if otter == nil {
		otter = otterImage(petIdle)
	}
	ui.Row(c.Key("home-idle")).Width(w).Height(pageBodyH).AlignItems(ui.Center).Gap(20).Children(func() {
		if otter != nil {
			ui.Row(c.Key("home-otter")).Width(homeOtterW + 18).Justify(ui.Center).Children(func() {
				ui.Image(c.Key("home-otter-img"), otter).Width(homeOtterW).Height(homeOtterW * 640 / 491).Fit(ui.Contain)
			})
		}
		ui.Column(c.Key("home-today")).Grow(1).MinWidth(0).Gap(12).Children(func() {
			ui.Column(c.Key("home-date")).Gap(2).Children(func() {
				ui.Text(c, l.HomeDate(now)).SingleLine().FontSize(26).FontWeight(700).TextColor(ink)
				ui.Text(c, l.HomeWeekday(now)).SingleLine().FontSize(13).TextColor(ink2)
			})
			p.statusLines(c, now)
		})
	})
	c.After(time.Minute) // the date, the otter's pose and the times move on
	return ui.Box(c).Size(0, 0)
}

// statusLines are at most three lines under the date: the newest agent
// turn (running or just finished), how much the tray holds, and keep
// awake's remaining time or else the tightest AI quota. A line only
// appears when it has something to say.
func (p *homePage) statusLines(c *ui.Context, now time.Time) {
	l := loc(c)
	a := p.host
	line := func(key string, lead func(), text string) {
		ui.Row(c.Key(key)).FillWidth().Gap(9).AlignItems(ui.Center).Children(func() {
			ui.Row(c.Key("lead")).Size(16, 16).Center().Children(lead)
			ui.Text(c, text).SingleLine().FontSize(12.5).TextColor(ink2).Grow(1).MinWidth(0).Ellipsis("…")
		})
	}
	if a.settings.AgentNotify.Enabled {
		running, done := a.agentFeature().snapshot(now)
		var row agentRun
		text := ""
		switch {
		case len(running) > 0:
			row = running[0]
			text = l.HomeAgentRunning(row.Tool.Name(), row.Task)
		case len(done) > 0:
			row = done[0]
			text = l.HomeAgentDone(row.Tool.Name(), row.Task)
		}
		if text != "" {
			line("home-agent", func() {
				logo, tint, mark := agentLogo(row.Tool)
				box := mark * 16 / settingsRowMark
				ui.Box(c.Key("mark")).Size(16, 16).Children(func() {
					off := (16 - box) / 2
					ui.Icon(c, logo).Size(box, box).TextColor(tint).Absolute().Left(off).Top(off)
				})
			}, text)
		}
	}
	if n := a.homeTrayCount(); n > 0 {
		line("home-tray", func() { ui.Icon(c, trayNavSymbol).Size(14, 14).TextColor(ink3) }, l.HomeTrayCount(n))
	}
	if m := a.awake; m != nil && m.active() {
		line("home-awake", func() { ui.Icon(c, cupSymbol).Size(14, 14).TextColor(ink3) },
			l.AwakeLine(l.AwakeTitle(), homeAwakeRemain(l, m, now)))
		return
	}
	if !a.settings.AIUsage.Enabled || a.usage == nil {
		return
	}
	five, week, tool, ok := a.usage.leastWindow()
	win := five
	if win == nil {
		win = week
	}
	if !ok || win == nil {
		return
	}
	pct := int(math.Round(win.LeftPct()))
	line("home-quota", func() {
		ui.Box(c.Key("ring")).Size(13, 13).Draw(func(pt *ui.Painter, r ui.Rect) {
			a.usage.drawWindowRing(pt, r, r.W/2-1.5, 2.5, win, usageFive, false, false)
		})
	}, l.HomeQuotaLeft(toolName(tool), strconv.Itoa(pct)))
}

// homeAwakeRemain is awakeModule.remainText in the language of the page:
// the native menu's wording would follow the app's language instead.
func homeAwakeRemain(l locales.Messages, m *awakeModule, now time.Time) string {
	if m.until.IsZero() {
		return l.AwakeUntilOff()
	}
	rem := m.until.Sub(now)
	if rem < 0 {
		rem = 0
	}
	return l.AwakeRemaining(mac.FormatClock(rem.Seconds()))
}

// homeTrayCount is how many files and folders the tray holds now.
func (a *shelf) homeTrayCount() int {
	if a.store == nil {
		return 0
	}
	n := 0
	for _, it := range a.store.Items {
		if it.OffTray || it.Missing || it.Kind == store.KindText || a.isLeaving(it) {
			continue
		}
		n++
	}
	return n
}
