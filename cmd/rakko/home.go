//go:build darwin

package main

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/note"
	"rakko/internal/store"
)

// The home (design/explore/notch-v3.html): a click on the notch grows a
// black search bar out of it and the keyboard goes to its field; typing
// opens a results sheet under the bar; a capsule of page buttons hangs
// under whatever the panel shows, and a page grows out of the notch in
// the bar's place. The sheet and the capsule are drawn in the panel's own
// window with mygo's glass, which frosts what the window itself painted
// under them (nothing, so it reads as a dark pane): no native view has to
// follow the shapes.

const (
	homeBarH      = 44  // the search row under the menu-bar band
	homeW         = 520 // the bar at rest
	homeTypingW   = 560 // the bar and its results while there is a query
	homeRadius    = 20  // the bar's bottom corners
	resultsGap    = 8   // bar → results sheet
	resultsRadius = 22
	resultsPad    = 12
	resultsRowH   = 44
	resultsSecH   = 21 // a group's caption row
	resultsActGap = 6  // results → the action bar
	resultsActBar = 28 // the action bar inside the sheet's bottom edge
	resultsActH   = resultsActGap + resultsActBar
	resultsMaxH   = 330

	navGap    = 10 // panel → page capsule
	navH      = 36
	navBtnW   = 36
	navBtnH   = 28
	navBtnGap = 2
	navPadX   = 5
	navSepW   = 9 // the 1pt divider and its margins
)

// paneRim is the hairline round the glass panes hung under the shell.
var paneRim = ui.RGBA(255, 255, 255, 0.14)

// homeHit is one search result: a row of a group, and what ⏎ does with it.
// A Spotlight row names its file instead of carrying a drawn icon; the
// Finder icon is resolved when the row is painted (see spotIcon).
type homeHit struct {
	group  string
	icon   *ui.SVG
	path   string
	title  string
	sub    string
	run    func()
	reveal func()
}

// bitmap is the row's Finder icon, or nil for rows that draw a glyph.
func (h homeHit) bitmap(a *shelf) *ui.Bitmap {
	if h.path == "" {
		return nil
	}
	return a.spotIcon(h.path)
}

// homeHits is what the query finds, grouped tray → clipboard → 便利贴 →
// 应用 → 文件夹 → 文件 → actions, three rows per group and no more than the
// sheet's 330 pt holds. Tray rows open the file, clipboard rows copy the
// text, note rows bring the note up, Spotlight rows open the file and
// action rows run the tool; each closes the panel like the page's own ⏎
// does. The notes come from the in-memory index (see notesearch.go), so
// their group is there with the clipboard's on the first keystroke.
func (a *shelf) homeHits() []homeHit {
	q := strings.ToLower(strings.TrimSpace(a.homeQuery))
	if q == "" {
		return nil
	}
	l := a.appLoc()
	now := time.Now()
	var tray, clip, acts []homeHit
	var trayPaths []string
	if a.store != nil {
		for _, it := range a.store.Items {
			title := itemTitleIn(l, it)
			hay := strings.ToLower(title + " " + it.Text + " " + it.OCRText)
			if !strings.Contains(hay, q) {
				continue
			}
			it := it
			switch {
			case it.Source == store.SourceClipboard && it.Kind == store.KindText && !it.OffClip:
				clip = append(clip, homeHit{group: l.HomeGroupClipboard(), icon: clipNavSymbol, title: title,
					sub: a.clipRowMeta(l, it), run: func() {
						if a.copyItem(it) {
							a.hide(hideExplicit)
						}
					}})
			case it.Kind != store.KindText && !it.OffTray && !it.Missing:
				trayPaths = append(trayPaths, it.Path)
				tray = append(tray, homeHit{group: l.HomeGroupTray(), icon: fileSymbol, path: it.Path, title: title,
					sub: relativeTime(l, it.CreatedAt, now), run: func() {
						if err := mygo.Shell.OpenPath(it.Path); err != nil {
							a.report(err)
							return
						}
						a.hide(hideExplicit)
					}})
			}
		}
	}
	// The notes' text is indexed in memory: no file is read while typing.
	docs := a.noteDocs()
	notes := a.noteHits(docs, q)
	// The index repeats what the tray already shows: those rows stay in
	// the tray group only — and a note file never shows twice, once as a
	// 便利贴 row and once as a Spotlight file.
	shown := make(map[string]bool, len(tray)+len(docs))
	for i, path := range trayPaths {
		if i == homeGroupMax {
			break
		}
		shown[path] = true
	}
	for _, d := range docs {
		shown[filepath.Join(a.dataDir, note.DirName, d.ID+note.FileExt)] = true
	}
	var apps, folders, files []homeHit
	if items := a.homeSpotItems(); len(items) > 0 {
		ag, fg, fh := spotGroupHits(q, items, shown)
		for _, hit := range ag {
			apps = append(apps, a.spotHitRow(l.HomeGroupApps(), hit))
		}
		for _, hit := range fg {
			folders = append(folders, a.spotHitRow(l.HomeGroupFolders(), hit))
		}
		for _, hit := range fh {
			files = append(files, a.spotHitRow(l.HomeGroupFiles(), hit))
		}
	}
	for _, act := range []struct {
		name string
		icon *ui.SVG
		run  func()
	}{
		{l.CardOcr(), ocrSymbol, func() { a.hide(hideExplicit); a.startScreenOCR() }},
		{l.PanelSettings(), gearSymbol, func() { a.hide(hideExplicit); a.openSettings() }},
	} {
		if strings.Contains(strings.ToLower(act.name), q) {
			acts = append(acts, homeHit{group: l.HomeGroupActions(), icon: act.icon, title: act.name, run: act.run})
		}
	}
	var out []homeHit
	for _, g := range [][]homeHit{tray, clip, notes, apps, folders, files, acts} {
		for i, h := range g {
			if i == homeGroupMax {
				break
			}
			out = append(out, h)
		}
	}
	// The sheet clips at 330 pt; drop whole rows from the end (the least
	// important group) until the rest fits.
	for len(out) > 0 && resultsRawHeight(out) > resultsMaxH {
		out = out[:len(out)-1]
	}
	return out
}

// resultsHeight is the sheet's height for hits: its padding, a caption per
// group, a row per hit and the hint row; one row's height for "nothing".
func resultsHeight(hits []homeHit) float32 {
	return min(resultsRawHeight(hits), resultsMaxH)
}

// resultsRawHeight is the sheet's height before the 330 pt cap: trimming
// and the drawn height both read it.
func resultsRawHeight(hits []homeHit) float32 {
	if len(hits) == 0 {
		return 2*resultsPad + resultsRowH
	}
	h := float32(2*resultsPad + resultsActH)
	group := ""
	for _, hit := range hits {
		if hit.group != group {
			group = hit.group
			h += resultsSecH
		}
		h += resultsRowH
	}
	return h
}

// homeBoxW is the bar's target width: wider while a query is typed.
func (a *shelf) homeBoxW() float32 {
	if strings.TrimSpace(a.homeQuery) != "" {
		return homeTypingW
	}
	return homeW
}

// resultsTargetH is the results sheet's target height: none without a
// query or away from the home.
func (a *shelf) resultsTargetH() float32 {
	if !a.visible || a.currentLevel() != 1 || strings.TrimSpace(a.homeQuery) == "" || a.snap.panel || a.snap.drawn {
		return 0
	}
	hits := a.homeHits()
	// Nothing found is only known once the index says so: while the query
	// is still gathering the sheet stays shut instead of flashing
	// 「没有找到」 between batches.
	if len(hits) == 0 && a.homeSearching() {
		return 0
	}
	return resultsHeight(hits)
}

// navW is the page capsule's width for its buttons.
func (a *shelf) navW() float32 {
	n := len(a.navPages()) + 2 // the pages, then settings and collapse
	return 2*navPadX + float32(n)*navBtnW + float32(n)*navBtnGap + navSepW
}

// navPages are the pages the capsule offers, in the registry's order: the
// now-playing detail page has no seat.
func (a *shelf) navPages() []panelPage {
	var out []panelPage
	out = append(out, a.enabledPages()...)
	return out
}

// belowBoxes are the shapes the panel hangs under the shell, in the
// window's coordinates relative to the shell's top-left corner: the results
// sheet (h may be 0) and the page capsule. shellW/shellH are the drawn
// shell, resultsH the drawn sheet, navK how far the capsule is shown.
func (a *shelf) belowBoxes(shellW, shellH, resultsH float32) (results, nav ui.Rect) {
	results = ui.Rect{X: 0, Y: shellH + resultsGap, W: shellW, H: resultsH}
	navTop := shellH + navGap
	if resultsH > 0.5 {
		navTop = results.Y + resultsH + navGap
	}
	w := a.navW()
	nav = ui.Rect{X: (shellW - w) / 2, Y: navTop, W: w, H: navH}
	return results, nav
}

// belowExtent is how far below the shell's bottom the hung shapes reach at
// the given drawn values, for the window frame.
func (a *shelf) belowExtent(shellH, resultsH float32) float32 {
	// The capsule is part of the frame for a settled open panel even
	// before its first drawn frame, and for the whole close fade once it
	// was drawn; the arrangement panel never draws it.
	wanted := a.visible && !(a.snap.panel || a.snap.drawn)
	if !wanted && a.fx.navK <= 0.01 {
		return 0
	}
	_, nav := a.belowBoxes(0, shellH, resultsH)
	return nav.Y + nav.H + 2 - shellH
}

// overBelow reports whether p (screen, top-left origin) is on the results
// sheet or the page capsule the last frame drew.
func (a *shelf) overBelow(p mygo.Point) bool {
	if !a.visible || a.fx.w <= 0 {
		return false
	}
	top := float32(a.frameTop() + a.sheetTop())
	left := float32(a.shellCentreX(a.fx.openK)) - a.fx.w/2
	results, nav := a.belowBoxes(a.fx.w, a.fx.h, a.fx.resultsH)
	x, y := float32(p.X)-left, float32(p.Y)-top
	in := func(r ui.Rect) bool { return r.H > 0 && x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }
	return in(results) || (a.fx.navK > 0.5 && in(nav))
}

// homeBar is the level-1 content: the menu-bar band (black, empty) and the
// search row. The field holds the keyboard while the panel is open on it.
func (a *shelf) homeBar(c *ui.Context, w float32) {
	l := loc(c)
	ui.Column(c.Key("home")).Width(w).Height(float32(bandH + homeBarH)).Children(func() {
		ui.Box(c.Key("home-band")).Height(bandH)
		row := ui.Row(c.Key("home-row")).Height(homeBarH).FillWidth().Padding(4, 16, 10, 18).Gap(10).AlignItems(ui.Center)
		row.Children(func() {
			ui.Icon(c, searchSymbol).Size(17, 17).TextColor(ink3)
			in := ui.TextInputBase(c.Key("home-query"), &a.homeQuery).Grow(1).MinWidth(0).
				Placeholder(l.HomeSearch()).Label(l.HomeSearch()).FontSize(15).TextColor(ink).
				OnChange(func() { a.homeQueryChanged() })
			if a.visible && a.focusMode && a.currentLevel() == 1 {
				in.Focus()
			}
			in.OnTextKey(func(k ui.TextKey) bool {
				switch k.Key {
				case ui.KeyUp:
					a.homeSel = max(a.homeSel-1, 0)
				case ui.KeyDown:
					a.homeSel++
				case ui.KeyEnter:
					if k.Mods&ui.Cmd != 0 {
						a.revealHomeHit()
					} else {
						a.runHomeHit()
					}
				case ui.KeyEscape:
					a.escape()
				default:
					return false
				}
				if a.win != nil {
					a.win.Invalidate()
				}
				return true
			})
			if a.homeQuery != "" {
				clr := ui.ButtonBase(c.Key("home-clear")).Size(18, 18).Radius(9).Center().
					Background(ui.RGBA(255, 255, 255, 0.22)).Label(l.HomeClear())
				clr.Children(func() { ui.Icon(c, closeSymbol).Size(9, 9).TextColor(surface) })
				if clr.Clicked() {
					a.resetHomeSearch()
				}
			}
		})
	})
}

// runHomeHit is ⏎ in the search field: the selected result acts.
func (a *shelf) runHomeHit() {
	hits := a.homeHits()
	if len(hits) == 0 {
		return
	}
	hits[min(a.homeSel, len(hits)-1)].run()
}

// revealHomeHit is ⌘⏎: the selected result shows in Finder, and rows that
// live outside the file system act as plain ⏎.
func (a *shelf) revealHomeHit() {
	hits := a.homeHits()
	if len(hits) == 0 {
		return
	}
	hit := hits[min(a.homeSel, len(hits)-1)]
	if hit.reveal != nil {
		hit.reveal()
		return
	}
	hit.run()
}

// resultsView draws the hits inside the sheet's box r (window-local).
func (a *shelf) resultsView(c *ui.Context, r ui.Rect, opacity float32) {
	l := loc(c)
	hits := a.homeHits()
	a.homeSel = min(a.homeSel, max(len(hits)-1, 0))
	a.noteHomePainted(hits)
	radius := min(resultsRadius, r.H/2)
	glassBase(c, "results-base", r, radius, opacity)
	box := ui.Column(c.Key("results")).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).
		Padding(resultsPad, resultsPad, resultsPad, resultsPad).Clip().Opacity(opacity).
		Radius(radius).Draw(glassPaneDraw(radius)).Border(0.5, paneRim)
	box.Children(func() {
		if len(hits) == 0 {
			// Still gathering: the sheet has no row to show yet, and
			// 「没有找到」 would be a claim the index has not made.
			if a.homeSearching() {
				return
			}
			ui.Row(c.Key("results-none")).Height(resultsRowH).Padding(0, 8, 0, 8).AlignItems(ui.Center).Children(func() {
				panelLabel(c, nil, l.HomeNoMatch(), 13, 500).TextColor(ink3)
			})
			return
		}
		group := ""
		for i, hit := range hits {
			if hit.group != group {
				group = hit.group
				ui.Row(c.Key(rowKey{"sec", i})).Height(resultsSecH).Padding(6, 8, 4, 8).Children(func() {
					panelLabel(c, nil, group, 11, 600).TextColor(ink3)
				})
			}
			row := ui.ButtonBase(c.Key(rowKey{"hit", i})).Height(resultsRowH).FillWidth().Radius(12).
				Padding(0, 8, 0, 8).Gap(10).AlignItems(ui.Center).Justify(ui.Start).Label(hit.title)
			if i == a.homeSel {
				row.Background(fill2)
			} else if row.Hovered() {
				row.Background(fill1)
			}
			row.Children(func() {
				ui.Box(c.Key(rowKey{"hit-ic", i})).Size(28, 28).Radius(7).Background(fill2).Center().Children(func() {
					bmp := hit.bitmap(a)
					switch {
					case bmp != nil:
						im := ui.Image(c.Key(rowKey{"hit-img", i}), bmp).Size(19, 19)
						if imageExts[strings.ToLower(filepath.Ext(hit.path))] {
							im.Radius(4).Fit(ui.Cover)
						}
					case hit.icon != nil:
						ui.Icon(c, hit.icon).Size(15, 15).TextColor(ink2)
					default:
						ui.Icon(c, fileSymbol).Size(15, 15).TextColor(ink2)
					}
				})
				// A row shows the name and its source beneath the group heading.
				ui.Column(c.Key(rowKey{"hit-txt", i})).Grow(1).MinWidth(0).Gap(1).Justify(ui.Center).Children(func() {
					ui.Text(c, hit.title).SingleLine().FontSize(13).FontWeight(600).TextColor(ink)
					if hit.sub != "" {
						ui.Text(c, hit.sub).SingleLine().FontSize(11.5).TextColor(ink3)
					}
				})
			})
			if row.Clicked() {
				a.homeSel = i
				hit.run()
			}
		}
		// The action bar is the sheet's last row, inside its rounded
		// corners: the results end above it instead of running under it.
		bar := ui.Row(c.Key("results-act")).Height(resultsActBar).FillWidth().Margin(resultsActGap, 0, 0, 0).
			Gap(16).AlignItems(ui.Center).Justify(ui.End)
		bar.Children(func() {
			keycap := func(name, key string) {
				ui.Box(c.Key("results-key-"+name)).Padding(2, 5, 2, 5).Radius(5).
					Background(ui.RGBA(255, 255, 255, 0.14)).Center().Children(func() {
					ui.Text(c, key).FontSize(10).TextColor(ink2)
				})
			}
			open := ui.Row(c.Key("results-open")).Gap(5).AlignItems(ui.Center)
			open.Children(func() {
				panelLabel(c, nil, l.ItemOpen(), 11.5, 600).TextColor(ink2)
				keycap("open", "↩")
			})
			// Files, folders and apps can also be shown where they live;
			// the clipboard, notes, tray rows and actions cannot.
			if hits[max(0, min(a.homeSel, len(hits)-1))].reveal != nil {
				reveal := ui.Row(c.Key("results-reveal")).Gap(5).AlignItems(ui.Center)
				reveal.Children(func() {
					panelLabel(c, nil, l.ItemShowInFinder(), 11.5, 600).TextColor(ink2)
					keycap("reveal-cmd", "⌘")
					keycap("reveal-enter", "↩")
				})
			}
		})
	})
}

// glassBase is the opaque box a glass pane is drawn over: the glass frosts
// what was painted under it in this frame, which is then a defined black
// and never the transparent window.
func glassBase(c *ui.Context, key string, r ui.Rect, radius, opacity float32) {
	ui.Box(c.Key(key)).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).
		Radius(radius).Background(surface).Opacity(opacity).PassThrough()
}

// glassPaneDraw is the pane's glass, painted under its content and clipped
// back to the pane's own edge (see paintGlassPane).
func glassPaneDraw(radius float32) func(*ui.Painter, ui.Rect) {
	return func(p *ui.Painter, r ui.Rect) {
		paintGlassPane(p, r, radius, [4]float32{radius, radius, radius, radius})
	}
}

// navView draws the page capsule's buttons inside its box r (window-local):
// the pages, a divider, settings and collapse. A page button opens its
// page; on the page already shown it goes back to the search bar.
func (a *shelf) navView(c *ui.Context, r ui.Rect, opacity float32) {
	l := loc(c)
	glassBase(c, "nav-base", r, navH/2, opacity)
	row := ui.Row(c.Key("nav")).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).
		Padding(0, navPadX, 0, navPadX).Gap(navBtnGap).AlignItems(ui.Center).Opacity(opacity).
		Radius(navH/2).Draw(glassPaneDraw(navH/2)).Border(0.5, paneRim)
	if opacity < 0.5 {
		row.PassThrough()
	}
	button := func(key string, label string, on bool, icon func()) ui.Element {
		b := ui.ButtonBase(c.Key("nav-"+key)).Size(navBtnW, navBtnH).Radius(navBtnH / 2).Center().
			Label(label).Tooltip(label)
		switch {
		case on:
			b.Background(ui.RGBA(255, 255, 255, 0.16))
		case b.Hovered():
			b.Background(ui.RGBA(255, 255, 255, 0.08))
		}
		b.Children(icon)
		return b
	}
	row.Children(func() {
		for _, p := range a.navPages() {
			p := p
			on := a.currentLevel() == 2 && a.tab == p.id()
			b := button(string(p.id()), pageLabel(c, p), on, func() {
				g := p.icon(c, on).Size(16, 16)
				if on {
					g.TextColor(ink)
				} else {
					g.TextColor(ink2)
				}
				if p.id() == pageTray && a.store != nil {
					if n := len(a.itemsFor(pageTray)); n > 0 {
						ui.Row(c.Key("nav-badge")).Absolute().Top(1).Right(3).Height(14).Padding(0, 3, 0, 3).
							Radius(7).Background(ui.RGBA(58, 58, 64, 1)).Center().Children(func() {
							ui.Text(c, itoa(n)).FontSize(9.5).FontWeight(700).TextColor(ink)
						})
					}
				}
			})
			if b.Clicked() {
				if on {
					a.goHome()
				} else {
					a.openPage(p.id())
				}
			}
		}
		ui.Box(c.Key("nav-sep")).Width(1).Height(18).Margin(0, 4, 0, 4).Background(ui.RGBA(255, 255, 255, 0.18))
		if b := button("settings", l.PanelSettings(), false, func() {
			ui.Icon(c, gearSymbol).Size(16, 16).TextColor(ink2)
		}); b.Clicked() {
			a.openSettings()
		}
		if b := button("collapse", l.PanelCollapse(), false, func() {
			ui.Icon(c, chevronUpSymbol).Size(16, 16).TextColor(ink2)
		}); b.Clicked() {
			a.hide(hideExplicit)
		}
	})
}

// goHome brings the panel back to the search bar, the keyboard with it; a
// closed panel opens straight on the bar.
func (a *shelf) goHome() {
	if !a.visible {
		a.show(false)
		return
	}
	a.setLevel(1)
	a.takeKeyboard()
}

// takeKeyboard gives the home's search field the keyboard: the app comes
// forward and the window takes key, remembering the app the user came from
// so a dismiss hands it back (see shouldRestoreApp).
func (a *shelf) takeKeyboard() {
	if a.win == nil {
		a.focusMode = true
		return
	}
	if !a.focusMode {
		if pid := mac.FrontmostPID(); pid > 0 && pid != mac.OwnPID() {
			a.prevAppPID = pid
		}
	}
	a.focusMode = true
	a.noteStale() // the notes may have changed while the panel was shut
	a.syncMousePassThrough()
	mac.ActivateSelf()
	a.win.Focus()
	// The field has the keyboard: Esc and Space go to it, not to the
	// global keys.
	a.releaseKeys()
	a.win.Invalidate()
}

// rowKey keys a repeated row of the results.
type rowKey struct {
	name string
	i    int
}

// itoa is strconv.Itoa for the badge's small counts.
func itoa(n int) string {
	if n > 99 {
		return "99+"
	}
	var b [3]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}
