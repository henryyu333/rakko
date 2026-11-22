package main

import (
	"slices"
	"testing"
	"time"

	"rakko/internal/aiusage"
	"rakko/internal/settings"
	"rakko/internal/weekly"

	"github.com/egoist/mygo/ui"
)

// weeklyNavShelf registers the built-in pages plus the AI usage page and
// the weekly report, as the app does at startup. It does not need a window:
// tabPages and navPages are registry questions.
func weeklyNavShelf(t *testing.T, ai bool) *shelf {
	t.Helper()
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(),
		visible: true, level: 2, tab: pageTray,
	}
	a.settings.AIUsage.Enabled = ai
	a.ensurePages()
	a.registerPage(&usageFeature{host: a})
	a.registerPage(&weeklyFeature{host: a})
	return a
}

func pageIDs(pages []panelPage) []pageID {
	out := make([]pageID, 0, len(pages))
	for _, p := range pages {
		out = append(out, p.id())
	}
	return out
}

func assertPageIDs(t *testing.T, where string, got []panelPage, want []pageID) {
	t.Helper()
	if ids := pageIDs(got); !slices.Equal(ids, want) {
		t.Fatalf("%s = %v, want %v", where, ids, want)
	}
}

func TestWeeklyReportHasNoNavigationSeat(t *testing.T) {
	for _, ai := range []bool{false, true} {
		t.Run(map[bool]string{false: "ai off", true: "ai on"}[ai], func(t *testing.T) {
			a := weeklyNavShelf(t, ai)
			want := []pageID{pageHome, pageTray, pageClipboard, pageTools}
			if ai {
				want = append(want, pageUsage)
			}

			// The registered report still warms: warming iterates all
			// enabled pages, not only the navigation seats.
			if !slices.Contains(pageIDs(a.enabledPages()), pageWeekly) {
				t.Fatal("the weekly page must stay registered and enabled")
			}
			if p := a.page(pageWeekly); p == nil || !p.enabled() {
				t.Fatal("the weekly page must be discoverable by id")
			}
			assertPageIDs(t, "band before opening", a.tabPages(), want)
			assertPageIDs(t, "capsule before opening", a.navPages(), want)

			// It opens from the home entry and becomes the page on screen.
			a.showPage(pageWeekly)
			if a.tab != pageWeekly || a.currentPage().id() != pageWeekly {
				t.Fatalf("weekly did not open: tab=%q current=%q", a.tab, a.currentPage().id())
			}
			// Being current adds no seat and replaces none, including the
			// AI usage seat when that page is enabled.
			assertPageIDs(t, "band on the report", a.tabPages(), want)
			assertPageIDs(t, "capsule on the report", a.navPages(), want)

			// Leaving to home and another page keeps the same seats.
			a.showPage(pageHome)
			if a.currentPage().id() != pageHome {
				t.Fatalf("home did not open: %q", a.currentPage().id())
			}
			assertPageIDs(t, "band after leaving", a.tabPages(), want)
			next := pageTray
			if ai {
				next = pageUsage
			}
			a.showPage(next)
			if a.currentPage().id() != next {
				t.Fatalf("%q did not open: %q", next, a.currentPage().id())
			}
			assertPageIDs(t, "band after the next page", a.tabPages(), want)
			assertPageIDs(t, "capsule after the next page", a.navPages(), want)
		})
	}
}

// TestWeeklyReportStaysOnItsWeekAcrossMidnight: the page pins the Monday it
// opened on. Recomputing an offset from "this week" would turn last week's
// report into this week's at Monday 00:00 while it is being read.
func TestWeeklyReportStaysOnItsWeekAcrossMidnight(t *testing.T) {
	store, err := weekly.Open("")
	if err != nil {
		t.Fatal(err)
	}
	sunday := time.Date(2026, 11, 8, 23, 50, 0, 0, time.Local)
	now := sunday
	a := &shelf{settings: settings.Default()}
	f := newWeeklyFeature(a, weeklyDeps{now: func() time.Time { return now }}, store, nil)
	f.openLastWeek()
	want := weekly.WeekStart(sunday).AddDate(0, 0, -7)
	if got := f.weekFor(now); !got.Equal(want) {
		t.Fatalf("opened on %s, want %s", got, want)
	}
	now = time.Date(2026, 11, 9, 0, 1, 0, 0, time.Local) // Monday, past midnight
	if got := f.weekFor(now); !got.Equal(want) {
		t.Fatalf("midnight moved the report to %s, want %s", got, want)
	}
}

// TestWeeklyReportEscapeReturnsHome: the report is Home's detail view, so
// Esc steps back to Home instead of dropping out to the search bar.
func TestWeeklyReportEscapeReturnsHome(t *testing.T) {
	a := weeklyNavShelf(t, false)
	a.showPage(pageWeekly)
	if got := a.currentPage().id(); got != pageWeekly {
		t.Fatalf("report did not open: %q", got)
	}
	a.escape()
	if got := a.currentPage().id(); got != pageHome {
		t.Fatalf("Esc from the report landed on %q, want home", got)
	}
	if lvl := a.currentLevel(); lvl != 2 {
		t.Fatalf("Esc should keep the page level, got %d", lvl)
	}
}

func TestWeeklyReportShowsUnreadableToolsAndRegistryNames(t *testing.T) {
	if got := aiusage.ToolAntigravity.Name(); got != "Antigravity" {
		t.Fatalf("ToolAntigravity.Name() = %q, want Antigravity", got)
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.Local)
	a := weeklyNavShelf(t, true)
	wf := a.page(pageWeekly).(*weeklyFeature)
	start := wf.weekFor(now)
	wf.aiHave = true
	wf.aiWeek = start
	wf.aiSummary = aiusage.Summary{
		Range: aiusage.RangeSpan,
		Total: aiusage.Totals{Input: 1000, Output: 200, Requests: 2, Cost: 0.05},
		ByTool: map[aiusage.Tool]aiusage.Totals{
			aiusage.ToolAntigravity: {Input: 1000, Output: 200, Requests: 2, Cost: 0.05},
		},
	}
	wf.aiUnreadable = []aiusage.Tool{aiusage.ToolCursor}
	view := ui.NewTester(func(c *ui.Context) {
		wf.body(c, true, false)
	}, weeklyPageW, weeklyPageH)
	view.Frame()
	if !view.HasText("Antigravity") {
		t.Fatalf("expected Antigravity in weekly report; texts %v", view.Texts())
	}
	if !view.HasText("读不到：Cursor") {
		t.Fatalf("expected unreadable tools hint in weekly report; texts %v", view.Texts())
	}
}

// The unreadable-sources line speaks in the page's quiet secondary grey, as
// the local usage page does: orange is the page's warning tone, and a source
// that could not be read is an explanation under the numbers, not a warning.
func TestWeeklyUnreadableNoteIsQuietGrey(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.Local)
	a := weeklyNavShelf(t, true)
	wf := a.page(pageWeekly).(*weeklyFeature)
	wf.aiHave = true
	wf.aiWeek = wf.weekFor(now)
	wf.aiSummary = aiusage.Summary{
		Range: aiusage.RangeSpan,
		Total: aiusage.Totals{Input: 1000, Output: 200, Requests: 2, Cost: 0.05},
		ByTool: map[aiusage.Tool]aiusage.Totals{
			aiusage.ToolAntigravity: {Input: 1000, Output: 200, Requests: 2, Cost: 0.05},
		},
	}
	wf.aiUnreadable = []aiusage.Tool{aiusage.ToolCursor}
	view := ui.NewTester(func(c *ui.Context) {
		wf.body(c, true, false)
	}, weeklyPageW, weeklyPageH)
	view.Frame()
	line := loc(nil).WeeklyAiUnreadableTools(joinUsageToolNames(wf.aiUnreadable))
	r, ok := view.Find(line)
	if !ok {
		t.Fatalf("%q is not on the page; texts %v", line, view.Texts())
	}
	img := view.Image()
	if img == nil {
		t.Fatal("the tester drew no frame")
	}
	scale := float32(img.Bounds().Dx()) / float32(weeklyPageW)
	// The line's box in device pixels: every glyph pixel in it must stay
	// achromatic (the secondary grey over the card), never the warning
	// tone's orange, which is far from grey on both axes.
	orange, lucid := 0, 0
	for py := int(r.Y * scale); py < int((r.Y+r.H)*scale); py++ {
		for px := int(r.X * scale); px < int((r.X+r.W)*scale); px++ {
			cr, cg, cb, _ := img.At(px, py).RGBA()
			ri, gi, bi := int(cr>>8), int(cg>>8), int(cb>>8)
			if ri-bi > 100 && gi-bi > 60 && ri > gi {
				orange++
			}
			if max(ri, max(gi, bi))-min(ri, min(gi, bi)) <= 12 {
				lucid++
			}
		}
	}
	t.Logf("%q box %.1f,%.1f %.1fx%.1f: %d orange, %d achromatic pixels", line, r.X, r.Y, r.W, r.H, orange, lucid)
	if orange > 0 {
		t.Fatalf("%d pixels of %q are drawn in the warning orange", orange, line)
	}
	if lucid == 0 {
		t.Fatalf("%q painted no grey pixels in its box", line)
	}
}

// A week whose only usage is a cumulative ledger's growth (tokens and cost,
// no new request) is still usage: the report shows it instead of "no data".
func TestWeeklyReportShowsUsageWithoutRequests(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.Local)
	a := weeklyNavShelf(t, true)
	wf := a.page(pageWeekly).(*weeklyFeature)
	wf.aiHave = true
	wf.aiWeek = wf.weekFor(now)
	wf.aiSummary = aiusage.Summary{
		Range:  aiusage.RangeSpan,
		Total:  aiusage.Totals{Input: 1000, Cost: 0.10},
		ByTool: map[aiusage.Tool]aiusage.Totals{aiusage.ToolHermes: {Input: 1000, Cost: 0.10}},
	}
	view := ui.NewTester(func(c *ui.Context) {
		wf.body(c, true, false)
	}, weeklyPageW, weeklyPageH)
	view.Frame()
	noData := loc(nil).WeeklyAiNoData()
	if view.HasText(noData) {
		t.Fatalf("weekly report says %q for a week with 1000 tokens; texts %v", noData, view.Texts())
	}
	if !view.HasText("Hermes") {
		t.Fatalf("expected the Hermes share in weekly report; texts %v", view.Texts())
	}
}
