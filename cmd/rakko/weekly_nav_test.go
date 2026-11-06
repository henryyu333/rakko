package main

import (
	"slices"
	"testing"
	"time"

	"rakko/internal/settings"
	"rakko/internal/weekly"
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
