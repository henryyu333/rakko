//go:build darwin

package main

// Real-window evidence for the home bar's search modes: the bar opens on
// the saved default mode (全部 unless the user chose another), an empty bar
// takes a real Tab and cycles 全部 → 应用 → 文件 → 全部 from there, each mode
// asks the index for its own scope and draws only its own groups, a typed
// term gives Tab back to focus, and closing then reopening the panel comes
// back on the default. Run with RAKKO_BASKET_SMOKE=1 through the native
// gate; the shots land in /tmp/rakko-search/shots.

import (
	"os"
	"testing"
	"time"

	"github.com/ebitengine/purego"

	"rakko/internal/mac"
	"rakko/locales"
)

// postTab posts one real Tab key press to the session's event stream, the
// same path a keyboard would take.
func postTab(t *testing.T) bool {
	t.Helper()
	var front bool
	onMain(func() { front = mac.FrontmostPID() == mac.OwnPID() })
	if !front {
		return false
	}
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatal(err)
	}
	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		t.Fatal(err)
	}
	var (
		create  func(source uintptr, key uint16, down bool) uintptr
		post    func(tap uint32, ev uintptr)
		release func(cf uintptr)
	)
	purego.RegisterLibFunc(&create, lib, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	purego.RegisterLibFunc(&release, libCF, "CFRelease")
	for _, down := range []bool{true, false} {
		ev := create(0, 48 /* kVK_Tab */, down)
		if ev == 0 {
			t.Fatal("CGEventCreateKeyboardEvent returned nil")
		}
		post(0, ev)
		release(ev)
		time.Sleep(40 * time.Millisecond)
	}
	return true
}

// scopeOf reads the bar's mode on the main thread.
func scopeOf(a *shelf) mac.SearchScope {
	var s mac.SearchScope
	onMain(func() { s = a.homeScope })
	return s
}

// waitScope waits for the bar's mode to become want.
func waitScope(t *testing.T, a *shelf, want mac.SearchScope, what string) {
	t.Helper()
	if !waitMain(2*time.Second, func() bool { return scopeOf(a) == want }) {
		t.Fatalf("%s: mode is %v, want %v", what, scopeOf(a), want)
	}
}

// modeGroups lists the groups the sheet draws now, in order.
func modeGroups(a *shelf) []string {
	var groups []string
	onMain(func() {
		for _, h := range a.homeHits() {
			if len(groups) == 0 || groups[len(groups)-1] != h.group {
				groups = append(groups, h.group)
			}
		}
	})
	return groups
}

// TestSmokeHomeModes drives the modes through real Tab presses in the real
// window, and proves each mode's sheet carries only its own groups.
func TestSmokeHomeModes(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := redesignShelf(t)
	mac.WarmSpotlight()
	time.Sleep(1200 * time.Millisecond)
	var def mac.SearchScope
	onMain(func() { def = a.defaultHomeScope() })
	onMain(func() { a.show(false) })
	time.Sleep(2 * time.Second)

	waitScope(t, a, def, "a fresh bar")
	spotShot(t, a, "modes-all")

	// An empty bar takes a real Tab and walks the cycle from the default:
	// 全部 → 应用 → 文件 → 全部.
	next := func(s mac.SearchScope) mac.SearchScope { return (s + 1) % 3 }
	for _, shot := range []string{"modes-apps-empty", "modes-files-empty", "modes-all-cycled"} {
		want := next(scopeOf(a))
		if postTab(t) {
			time.Sleep(250 * time.Millisecond) // at least one frame
		} else {
			t.Log("the test app was not frontmost: cycling the mode directly")
			onMain(func() { a.cycleHomeScope() })
		}
		waitScope(t, a, want, "a Tab in the empty bar")
		spotShot(t, a, shot)
	}
	if got := scopeOf(a); got != def {
		t.Fatalf("three Tabs from the default %v ended on %v", def, got)
	}

	// 应用: a term lists the 应用 group alone.
	l := locales.In("zh-Hans")
	onMain(func() { a.setHomeScope(mac.ScopeApps) })
	waitScope(t, a, mac.ScopeApps, "the 应用 mode")
	if !typeIntoHome(t, "rakko") {
		t.Log("the test app was not frontmost: setting the field directly")
		onMain(func() {
			a.homeQuery = "rakko"
			a.homeQueryChanged()
		})
	}
	if !waitMain(15*time.Second, func() bool {
		return a.homeSpot.done && a.homeSpot.scope == mac.ScopeApps
	}) {
		t.Fatalf("the 应用 query never finished: %+v", a.homeSpot)
	}
	groups := modeGroups(a)
	t.Logf("应用 groups: %v", groups)
	for _, g := range groups {
		if g != l.HomeGroupApps() {
			t.Fatalf("应用 mode drew %q, want only %q (%v)", g, l.HomeGroupApps(), groups)
		}
	}
	if len(groups) == 0 {
		t.Fatal("应用 mode found no app rows for rakko")
	}
	time.Sleep(400 * time.Millisecond)
	spotShot(t, a, "modes-apps-term")

	// A typed term gives Tab back to focus: the mode stays put.
	before := scopeOf(a)
	if postTab(t) {
		time.Sleep(250 * time.Millisecond)
	}
	if after := scopeOf(a); after != before {
		t.Fatalf("Tab moved the mode with text in the bar: %v → %v", before, after)
	}

	// 文件: the same term lists documents, never an app nor an action.
	onMain(func() { a.setHomeScope(mac.ScopeFiles) })
	waitScope(t, a, mac.ScopeFiles, "the 文件 mode")
	if !waitMain(15*time.Second, func() bool {
		return a.homeSpot.done && a.homeSpot.scope == mac.ScopeFiles
	}) {
		t.Fatalf("the 文件 query never finished: %+v", a.homeSpot)
	}
	groups = modeGroups(a)
	t.Logf("文件 groups: %v", groups)
	for _, g := range groups {
		if g == l.HomeGroupApps() {
			t.Fatalf("文件 mode drew the apps group (%v)", groups)
		}
		if g == l.HomeGroupActions() {
			t.Fatalf("文件 mode drew the actions group (%v)", groups)
		}
	}
	if !waitMain(2*time.Second, func() bool { return a.fx.resultsH > 1 }) {
		t.Fatalf("the 文件 sheet never opened: resultsH=%v", a.fx.resultsH)
	}
	spotShot(t, a, "modes-files-term")

	// Closing and reopening comes back on the default with an empty bar.
	onMain(func() { a.hide(hideExplicit) })
	time.Sleep(600 * time.Millisecond)
	onMain(func() { a.show(false) })
	time.Sleep(1200 * time.Millisecond)
	waitScope(t, a, def, "a reopened bar")
	var query string
	onMain(func() { query = a.homeQuery })
	if query != "" {
		t.Fatalf("the reopened bar kept %q", query)
	}
	spotShot(t, a, "modes-reopened")

	onMain(func() { a.hide(hideExplicit) })
}
