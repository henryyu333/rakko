//go:build darwin

package main

// The home bar's Spotlight search: every keystroke cancels the query in
// flight and starts a new one once typing pauses, so the index is never
// asked for text the user has moved past. Batches land on the UI thread
// and the sheet is redrawn as soon as the first one arrives; the rows keep
// the sheet's existing groups, adding 应用 / 文件夹 / 文件 between the
// clipboard and the actions.

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

// homeDebounce is how long typing has to pause before the index is asked.
const homeDebounce = 80 * time.Millisecond

// homeAfterFunc schedules the debounce; tests replace it to fire the wait
// by hand instead of from another goroutine.
var homeAfterFunc = time.AfterFunc

// homeGroupMax caps one group's rows; the sheet's 330 pt also caps the
// total (see homeHits).
const homeGroupMax = 3

// homeSpotState is the bar's Spotlight query and what it found so far.
// query names the text the items answer: a batch that arrives after the
// field moved on is dropped by the sequence number, and hits whose query
// no longer matches are never shown.
type homeSpotState struct {
	query   string
	items   []mac.SpotlightHit
	done    bool
	seq     int
	typedAt time.Time // when the field last changed, for the latency log
	painted time.Time // when the first row of this query was drawn
	timer   *time.Timer
	cancel  func()
}

// spotlightWarm primes the index connection; a headless shelf has no
// window to type into and skips it.
var spotlightWarm = mac.WarmSpotlight

// warmSpotlight pays the index's per-process setup before the user's first
// keystroke, once per launch.
var spotWarmed sync.Once

func (a *shelf) warmSpotlight() {
	if a.win == nil {
		return
	}
	spotWarmed.Do(spotlightWarm)
}

// spotlightStart runs one query; tests replace it with a fake that records
// the term and delivers batches by hand.
var spotlightStart = func(term string, limit int, emit func([]mac.SpotlightHit), done func()) func() {
	return mac.StartSpotlight(term, limit, emit, done).Cancel
}

// spotDebug logs the first batch's latency when RAKKO_SPOTLIGHT_DEBUG is
// set; the gate's timing runs read it instead of guessing from a video.
var spotDebug = os.Getenv("RAKKO_SPOTLIGHT_DEBUG") != ""

// homeQueryChanged runs after the field's text changed: the previous
// query stops at once, the results it left are cleared, and a new query
// starts when typing pauses.
func (a *shelf) homeQueryChanged() {
	q := strings.TrimSpace(a.homeQuery)
	a.homeSel = 0
	a.stopHomeSpot()
	a.homeSpot.query, a.homeSpot.typedAt = q, time.Now()
	a.homeSpot.painted = time.Time{}
	if q == "" {
		a.invalidateHome()
		return
	}
	seq := a.homeSpot.seq
	a.homeSpot.timer = homeAfterFunc(homeDebounce, func() {
		a.onUI(func() {
			if a.homeSpot.seq != seq || strings.TrimSpace(a.homeQuery) != q {
				return
			}
			a.homeSpot.timer = nil
			a.startHomeSpot(q, seq)
		})
	})
	a.invalidateHome()
}

// startHomeSpot asks the index for q; batches append to the state on the
// UI thread and redraw, so the sheet grows while the index is still
// gathering.
func (a *shelf) startHomeSpot(q string, seq int) {
	started := time.Now()
	if spotDebug {
		log.Printf("spotlight %q: query started %v after the last keystroke",
			q, time.Since(a.homeSpot.typedAt).Round(time.Millisecond))
	}
	a.homeSpot.cancel = spotlightStart(q, 0, func(batch []mac.SpotlightHit) {
		if spotDebug {
			log.Printf("spotlight %q: %d hits arrived %v after the query started",
				q, len(batch), time.Since(started).Round(time.Millisecond))
		}
		a.onUI(func() {
			if a.homeSpot.seq != seq {
				return
			}
			if spotDebug && len(a.homeSpot.items) == 0 && len(batch) > 0 {
				log.Printf("spotlight %q: first batch of %d after %v",
					q, len(batch), time.Since(a.homeSpot.typedAt).Round(time.Millisecond))
			}
			a.homeSpot.items = append(a.homeSpot.items, batch...)
			a.invalidateHome()
		})
	}, func() {
		a.onUI(func() {
			if a.homeSpot.seq != seq {
				return
			}
			a.homeSpot.done = true
			if spotDebug {
				log.Printf("spotlight %q: done after %v, %d hits",
					q, time.Since(a.homeSpot.typedAt).Round(time.Millisecond), len(a.homeSpot.items))
			}
			a.invalidateHome()
		})
	})
}

// cancelHomeSpot stops the timer and the query in flight; a later batch
// belongs to a dead sequence and is dropped.
func (a *shelf) cancelHomeSpot() {
	a.homeSpot.seq++
	if a.homeSpot.timer != nil {
		a.homeSpot.timer.Stop()
		a.homeSpot.timer = nil
	}
	if a.homeSpot.cancel != nil {
		a.homeSpot.cancel()
		a.homeSpot.cancel = nil
	}
}

// stopHomeSpot retires the query in flight and the hits it left.
func (a *shelf) stopHomeSpot() {
	a.cancelHomeSpot()
	a.homeSpot.query, a.homeSpot.items, a.homeSpot.done = "", nil, false
}

// noteHomePainted logs, under RAKKO_SPOTLIGHT_DEBUG, how long the current
// query's first row took to reach the screen after the last keystroke: the
// latency the gate's evidence reads.
func (a *shelf) noteHomePainted(hits []homeHit) {
	if !spotDebug || !a.homeSpot.painted.IsZero() || len(hits) == 0 {
		return
	}
	for _, hit := range hits {
		if hit.path == "" {
			continue
		}
		log.Printf("spotlight %q: first row painted %v after the last keystroke",
			a.homeSpot.query, time.Since(a.homeSpot.typedAt).Round(time.Millisecond))
		a.homeSpot.painted = time.Now()
		return
	}
}

// homeSearching reports whether the bar is still waiting on the index for
// what it now shows: the sheet keeps 「没有找到」 for the end of a query.
func (a *shelf) homeSearching() bool {
	q := strings.TrimSpace(a.homeQuery)
	if q == "" {
		return false
	}
	return !a.homeSpot.done || !strings.EqualFold(strings.TrimSpace(a.homeSpot.query), q)
}

// homeSpotItems are the hits that answer the bar's current text.
func (a *shelf) homeSpotItems() []mac.SpotlightHit {
	if len(a.homeSpot.items) == 0 {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(a.homeSpot.query), strings.TrimSpace(a.homeQuery)) {
		return nil
	}
	return a.homeSpot.items
}

// onUI runs fn where the shelf's view state lives: the window's update
// queue when there is a window, inline in tests.
func (a *shelf) onUI(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(fn)
}

func (a *shelf) invalidateHome() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// resetHomeSearch clears the bar, its selection and its query.
func (a *shelf) resetHomeSearch() {
	a.homeQuery, a.homeSel = "", 0
	a.stopHomeSpot()
	a.invalidateHome()
}

// spotRank orders one group: a name that starts with the query first, then
// a name that contains it, then a hit only its text matched. q is lower
// case and trimmed.
func spotRank(name, q string) int {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, q):
		return 0
	case strings.Contains(n, q):
		return 1
	default:
		return 2
	}
}

// spotGroupHits splits the index's hits into the sheet's three Spotlight
// groups and orders each: rank, then the most recently used, then name.
// Paths already shown (the tray's rows) are skipped.
func spotGroupHits(q string, items []mac.SpotlightHit, shown map[string]bool) (apps, folders, files []mac.SpotlightHit) {
	seen := make(map[string]bool, len(items))
	for _, hit := range items {
		if hit.Path == "" || seen[hit.Path] || shown[hit.Path] {
			continue
		}
		seen[hit.Path] = true
		switch {
		case hit.App:
			apps = append(apps, hit)
		case hit.Folder:
			folders = append(folders, hit)
		default:
			files = append(files, hit)
		}
	}
	for _, g := range [][]mac.SpotlightHit{apps, folders, files} {
		sort.SliceStable(g, func(i, j int) bool {
			ri, rj := spotRank(g[i].Name, q), spotRank(g[j].Name, q)
			if ri != rj {
				return ri < rj
			}
			if !g[i].LastUsed.Equal(g[j].LastUsed) {
				return g[i].LastUsed.After(g[j].LastUsed)
			}
			return g[i].Name < g[j].Name
		})
	}
	return apps, folders, files
}

// spotHitRow is one index hit as a sheet row: ⏎ opens it, ⌘⏎ reveals it in
// Finder, and the secondary text is its folder with the home directory
// written as ~.
func (a *shelf) spotHitRow(group string, hit mac.SpotlightHit) homeHit {
	path := hit.Path
	return homeHit{
		group: group,
		path:  path,
		title: hit.Name,
		sub:   shortDir(path),
		run: func() {
			if err := mygo.Shell.OpenPath(path); err != nil {
				a.report(err)
				return
			}
			a.hide(hideExplicit)
		},
		reveal: func() {
			mac.RevealInFinder(path)
			a.hide(hideExplicit)
		},
	}
}

// shortDir renders a hit's folder with the home directory as ~.
func shortDir(path string) string {
	dir := filepath.Dir(path)
	if home := userHome(); home != "" {
		switch {
		case dir == home:
			return "~"
		case strings.HasPrefix(dir, home+"/"):
			return "~/" + dir[len(home)+1:]
		}
	}
	return dir
}

var userHome = sync.OnceValue(func() string {
	home, _ := os.UserHomeDir()
	return home
})

// spotIcon is the row's Finder icon, cached per path. The first paint of a
// new row starts the icon in the background and draws the file glyph until
// it arrives: NSWorkspace answers the first icon of a session in ~100 ms,
// and the sheet must not wait for it. The cache is bounded and rebuilt on
// demand, as the clipboard's app icons are.
func (a *shelf) spotIcon(path string) *ui.Bitmap {
	if a.spotIcons == nil {
		a.spotIcons = make(map[string]*ui.Bitmap)
	}
	if b, ok := a.spotIcons[path]; ok {
		return b
	}
	// A headless shelf has no window to redraw and no icon services.
	if a.win == nil || a.spotPending[path] || len(a.spotPending) >= spotIconLoads {
		return nil
	}
	if len(a.spotIcons) >= spotIconLimit {
		a.spotIcons = make(map[string]*ui.Bitmap)
	}
	if a.spotPending == nil {
		a.spotPending = make(map[string]bool)
	}
	a.spotPending[path] = true
	go func() {
		// The Finder icon is an AppKit call: ask for it on the main thread,
		// then decode and scale the PNG here, off the main thread.
		var png []byte
		runOnMain(func() { png = fileIcon(path, 40) })
		var b *ui.Bitmap
		if len(png) > 0 {
			b, _ = ui.DecodeBitmap(png)
		}
		a.onUI(func() {
			delete(a.spotPending, path)
			if a.quitting {
				return
			}
			a.spotIcons[path] = b
			a.invalidateHome()
		})
	}()
	return nil
}

// fileIcon and runOnMain are seams: NSWorkspace's icon may only be asked
// for on the main thread, and the tests check that it is.
var (
	fileIcon  = mac.FileIcon
	runOnMain = mygo.RunOnMain
)

const (
	spotIconLimit = 64 // result-icon cache entries
	spotIconLoads = 6  // icons rendered at once, one per visible row
)
