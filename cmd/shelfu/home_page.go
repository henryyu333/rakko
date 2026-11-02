//go:build darwin

package main

// 首页（design/explore/home-v3.html）：点刘海仍是搜索条，页面胶囊最左边
// 的「首页」进这里。它只看「此刻」：有正在播放时就是播放器（拿到同步歌词
// 时加宽到 640，右边滚动歌词）；没有播放时是 Agent 在跑 / 刚完成与最近放
// 进托盘、最近复制的东西。没有方格、不接受拖入、没有编辑模式。

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/lyrics"
	"shelf/internal/mac"
	"shelf/internal/store"
	"shelf/locales"
)

const (
	homeLyricsW   = 640 // the page beside scrolling lyrics
	homePlayerCol = 300 // the player's column there
	homeLineH     = 28  // one lyric line
	homeRecent    = 4   // newest tray thumbnails
	homeAgentRows = 3   // running + finished rows above 最近
	homeGlyph     = 12  // the running ring and the done dot
)

var (
	// homeLyricsWide is the house glyph in the capsule.
	homeSymbol = symbol(`<path d="M4 11l8-6 8 6v8H4z"/><path d="M10 19v-5h4v5"/>`)

	// homeFetch is the network seam: the app asks lrclib.net; tests replace
	// it (TestMain makes it answer "no lyrics" so no test goes online).
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
	failed   bool // this track's lookup failed: stay on the player
	fetched  bool // the fetch for key was started
	shown    int  // lyric line the scroll was last moved to
	scrollTo int  // line to centre once the scroll has a measured range

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

// width is the shell width this page needs now: 640 while it shows lyrics,
// otherwise the sheet's 440. layout calls it before the body, so the shell,
// the window frame and the content agree on one width.
func (p *homePage) width() float32 {
	p.prepare(false) // a cache hit must widen the first frame, not the second
	if !p.host.settings.LyricsOn() {
		return sheetW
	}
	st, ok := p.track()
	if !ok || p.key != homeTrackKey(st) || len(p.lines) == 0 {
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
	if p.host.settings.LyricsOn() && len(p.lines) > 0 {
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
		p.key, p.lines, p.none, p.loading, p.failed, p.fetched, p.shown, p.scrollTo = "", nil, false, false, false, false, -1, -1
		return
	}
	key := homeTrackKey(st)
	if key != p.key {
		p.key, p.lines, p.none, p.loading, p.failed, p.fetched, p.shown, p.scrollTo = key, nil, false, false, false, false, -1, -1
		if r, ok := p.host.lyricsCache().Get(key); ok {
			p.adopt(r)
		}
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		p.failed = true
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
	ui.Row(c.Key("home-ctrl")).Margin(16, 0, 0, 0).Width(w).Justify(ui.Center).
		AlignItems(ui.Center).Gap(26).Children(func() {
		m.controlButton(c, "home-prev", prevFill, l.PreviousTrack(), mac.NPPrevious, 30, 17)
		m.controlButton(c, "home-toggle", toggleSymbol(st.Playing), playPauseText(l, st.Playing), mac.NPPlayPause, 38, 22)
		m.controlButton(c, "home-next", nextFill, l.NextTrack(), mac.NPNext, 30, 17)
	})
	if st.Playing {
		c.After(time.Second) // the clock and the lyric line move on
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
			p.lyricsView(c, st, right)
		})
	})
	return ui.Box(c).Size(0, 0)
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

// idleBody is state ③ / ③′: no track. With Agent 完成提醒 on and something
// to say, the AGENT rows and 最近; otherwise just 最近, larger, with the
// keep-awake and quota chips that are on.
func (p *homePage) idleBody(c *ui.Context, w float32) ui.Element {
	l := loc(c)
	now := c.Now()
	var running, done []agentRun
	if p.host.settings.AgentNotify.Enabled {
		running, done = p.host.agentFeature().snapshot(now)
	}
	rows := append(append([]agentRun{}, running...), done...)
	hasAgent := len(rows) > 0
	files := p.host.homeRecentFiles(homeRecent)
	clip, hasClip := p.host.homeLatestClip()
	if !hasAgent && len(files) == 0 && !hasClip {
		ui.Column(c.Key("home-empty")).Width(w).Height(pageBodyH).Center().Children(func() {
			panelLabel(c, nil, l.HomeEmpty(), 12.5, 500).TextColor(ink3)
		})
		return ui.Box(c).Size(0, 0)
	}
	if !hasAgent {
		p.compactBody(c, w, files, clip, hasClip, now)
		return ui.Box(c).Size(0, 0)
	}
	if len(rows) > homeAgentRows {
		rows = rows[:homeAgentRows]
	}
	thumb := float32(34)
	ui.Column(c.Key("home-idle")).Width(w).Height(pageBodyH).Justify(ui.Center).Gap(0).Children(func() {
		panelLabel(c, nil, l.HomeAgentSection(), 10.5, 600).TextColor(ink3)
		ui.Column(c.Key("home-agent-rows")).FillWidth().Gap(4).Margin(7, 0, 0, 0).Children(func() {
			start := len(running)
			for i, row := range rows {
				p.agentRow(c, i, row, i < start, now)
			}
		})
		ui.Box(c.Key("home-sep")).FillWidth().Height(1).Margin(10, 0, 10, 0).
			Background(ui.RGBA(255, 255, 255, 0.08))
		panelLabel(c, nil, l.HomeRecentSection(), 10.5, 600).TextColor(ink3)
		p.recentRow(c, files, clip, hasClip, thumb, 7)
	})
	if len(running) > 0 {
		c.AnimationFrame()
	}
	c.After(time.Minute)
	return ui.Box(c).Size(0, 0)
}

// compactBody is state ③′: 最近 alone, one size larger, with the chips for
// what else is on.
func (p *homePage) compactBody(c *ui.Context, w float32, files []store.Item, clip store.Item, hasClip bool, now time.Time) {
	l := loc(c)
	ui.Column(c.Key("home-compact")).Width(w).Height(pageBodyH).Justify(ui.Center).Gap(10).Children(func() {
		panelLabel(c, nil, l.HomeRecentSection(), 10.5, 600).TextColor(ink3)
		p.recentRow(c, files, clip, hasClip, 44, 0)
		p.statusChips(c, now)
	})
	c.After(time.Minute)
}

// agentRow is one AGENT line: a running ring or a done dot, the tool's
// mark, the task, and how long it ran / how long ago it finished.
func (p *homePage) agentRow(c *ui.Context, i int, row agentRun, running bool, now time.Time) {
	l := loc(c)
	ui.Row(c.Key(i)).Height(26).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		if running {
			homeRunRing(c)
		} else {
			ui.Box(c.Key("done")).Size(homeGlyph, homeGlyph).Radius(homeGlyph / 2).Background(liveGreen)
		}
		logo, tint, mark := agentLogo(row.Tool)
		settingsMark(c, logo, tint, mark)
		ui.Text(c, row.Task).SingleLine().FontSize(12).FontWeight(500).TextColor(ink).
			Grow(1).MinWidth(0).Ellipsis("…")
		var when string
		if running {
			d := now.Sub(row.At)
			if d < time.Minute {
				when = l.HomeRunningStarted()
			} else {
				when = l.HomeRunningFor(itoa(int(d / time.Minute)))
			}
		} else {
			d := now.Sub(row.At)
			if d < time.Minute {
				when = l.ClipJustNow()
			} else {
				when = l.ClipMinutesAgo(int(d / time.Minute))
			}
		}
		panelLabel(c, nil, when, 11, 500).TextColor(ink3).Shrink(0)
	})
}

// homeRunRing is the blue hoop that turns while a turn is running.
func homeRunRing(c *ui.Context) {
	phase := float32(c.Now().UnixMilli()%1200) / 1200
	ui.Box(c.Key("run")).Size(homeGlyph, homeGlyph).Draw(func(pt *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		rad := r.W/2 - 1
		var track ui.Path
		track.Circle(cx, cy, rad)
		pt.StrokePath(&track, 2, fill3)
		homeArc(pt, cx, cy, rad, 2, phase, accent)
		homeArc(pt, cx, cy, rad, 2, phase+0.5, accent)
	})
}

// homeArc strokes a quarter of a circle starting at frac of the way round
// from 12 o'clock, wrapping past the start.
func homeArc(p *ui.Painter, cx, cy, rad, lw, frac float32, col ui.Color) {
	start := 0.75 + frac
	for start >= 1 {
		start -= 1
	}
	end := start + 0.25
	if end <= 1 {
		var path ui.Path
		path.Circle(cx, cy, rad).Trim(start, end)
		p.StrokePath(&path, lw, col)
		return
	}
	var head, tail ui.Path
	head.Circle(cx, cy, rad).Trim(start, 1)
	tail.Circle(cx, cy, rad).Trim(0, end-1)
	p.StrokePath(&head, lw, col)
	p.StrokePath(&tail, lw, col)
}

// recentRow is the newest tray files (or folders) and the newest copied
// text: thumbs drag out like the tray's tiles and open on a click; the
// text copies on a click.
func (p *homePage) recentRow(c *ui.Context, files []store.Item, clip store.Item, hasClip bool, size, gap float32) {
	ui.Row(c.Key("home-recent")).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
		for _, it := range files {
			p.host.homeThumb(c, it, size)
		}
		if hasClip {
			title := itemTitle(clip)
			row := ui.Row(c.Key("home-clip")).Grow(1).MinWidth(0).Height(size).
				Padding(0, 6, 0, 6).Radius(7).AlignItems(ui.Center).Label(title)
			if row.Hovered() {
				row.Background(fill1)
			}
			row.Children(func() {
				ui.Text(c, "“"+title+"”").SingleLine().FontSize(12).TextColor(ink2).Grow(1).MinWidth(0).Ellipsis("…")
			})
			if row.Clicked() {
				p.host.copyText(clip.Text)
			}
		}
		if gap > 0 {
			ui.Box(c).Width(gap)
		}
	})
}

// homeThumb is one recent file: its tray thumbnail (or a file glyph), a
// drag-out like the tray tile, and a click that opens it.
func (a *shelf) homeThumb(c *ui.Context, it store.Item, size float32) {
	el := ui.Row(c.Key("hthumb-"+it.ID)).Size(size, size).Radius(8).Center().Clip().
		Label(itemTitle(it))
	if !it.Missing {
		el.DragDataFrom(func() transfer.Data {
			a.beginOwnDrag()
			a.takeServiceOwnership()
			if a.win != nil {
				a.win.Invalidate()
			}
			a.dragOutIDs = nil
			return a.dragOut(it)
		}, transfer.DragOptions{Operations: transfer.Copy, Done: a.completed})
	}
	if b := a.thumbnail(it); b != nil {
		el.Children(func() { ui.Image(c.Key("bmp"), b).Size(size, size).Fit(ui.Cover) })
	} else {
		el.Background(fill2).Children(func() {
			ui.Icon(c, fileSymbol).Size(size*0.45, size*0.45).TextColor(ink3)
		})
	}
	if el.Clicked() && !it.Missing {
		if err := mygo.Shell.OpenPath(it.Path); err != nil {
			a.report(err)
		}
	}
}

// statusChips is the ③′ footer: 保持唤醒 with its remaining time while on,
// and the tightest AI quota while AI 用量 is on.
func (p *homePage) statusChips(c *ui.Context, now time.Time) {
	l := loc(c)
	ui.Row(c.Key("home-chips")).Gap(6).AlignItems(ui.Center).Children(func() {
		if m := p.host.awake; m != nil && m.active() {
			chip := ui.Row(c.Key("home-awake")).Height(22).Padding(0, 9, 0, 9).Radius(11).
				Background(fill2).Gap(6).AlignItems(ui.Center)
			chip.Children(func() {
				ui.Icon(c, cupSymbol).Size(12, 12).TextColor(ink2)
				ui.Text(c, l.AwakeLine(l.AwakeTitle(), homeAwakeRemain(l, m, now))).SingleLine().
					FontSize(11.5).TextColor(ink2)
			})
		}
		if !p.host.settings.AIUsage.Enabled || p.host.usage == nil {
			return
		}
		five, week, tool, ok := p.host.usage.leastWindow()
		if !ok {
			return
		}
		win := five
		if win == nil {
			win = week
		}
		if win == nil {
			return
		}
		pct := int(math.Round(win.LeftPct()))
		chip := ui.Row(c.Key("home-quota")).Height(22).Padding(0, 9, 0, 9).Radius(11).
			Background(fill2).Gap(6).AlignItems(ui.Center)
		chip.Children(func() {
			ui.Box(c.Key("ring")).Size(13, 13).Draw(func(pt *ui.Painter, r ui.Rect) {
				p.host.usage.drawWindowRing(pt, r, r.W/2-1.5, 2.5, win, usageFive, false, false)
			})
			ui.Text(c, l.HomeQuotaLeft(toolName(tool), itoa(pct))).SingleLine().
				FontSize(11.5).TextColor(ink2)
		})
	})
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

// homeRecentFiles is the newest tray entries, files and folders only: the
// clipboard's text and what was removed from the tray stay out.
func (a *shelf) homeRecentFiles(n int) []store.Item {
	if a.store == nil {
		return nil
	}
	var out []store.Item
	for _, it := range a.store.Items {
		if len(out) == n {
			break
		}
		if it.OffTray || it.Missing || it.Kind == store.KindText || a.isLeaving(it) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// homeLatestClip is the newest text copied into the clipboard history.
func (a *shelf) homeLatestClip() (store.Item, bool) {
	if a.store == nil {
		return store.Item{}, false
	}
	for _, it := range a.store.Items {
		if it.Source == store.SourceClipboard && !it.OffClip && it.Kind == store.KindText &&
			strings.TrimSpace(it.Text) != "" {
			return it, true
		}
	}
	return store.Item{}, false
}
