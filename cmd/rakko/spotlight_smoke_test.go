//go:build darwin

package main

// Real-window evidence for the home bar's Spotlight search: the sheet is
// painted from the index's first batch, the groups carry Finder icons and
// the folder as secondary text, a finished empty query says 「没有找到」,
// and both languages render. Run with RAKKO_BASKET_SMOKE=1 through the
// native gate; the shots land in /tmp/rakko-search/shots.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"

	"rakko/internal/mac"
)

const spotShotDir = "/tmp/rakko-search/shots"

// spotShot screenshots the notch window to the evidence folder.
func spotShot(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	var num int
	onMain(func() { num = mac.WindowNumber(a.win.NativeHandle()) })
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	if err := os.MkdirAll(spotShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(spotShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
	return shot
}

// typeIntoHome posts real keystrokes for text, one every 35 ms, once the
// test app is frontmost — never into another app. It reports whether the
// keys were posted.
func typeIntoHome(t *testing.T, text string) bool {
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
		createKey func(source uintptr, key uint16, down bool) uintptr
		setUni    func(ev uintptr, n uint64, chars *uint16)
		post      func(tap uint32, ev uintptr)
		release   func(cf uintptr)
	)
	purego.RegisterLibFunc(&createKey, lib, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&setUni, lib, "CGEventKeyboardSetUnicodeString")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	purego.RegisterLibFunc(&release, libCF, "CFRelease")
	for _, r := range text {
		chars := []uint16{uint16(r)}
		for _, down := range []bool{true, false} {
			ev := createKey(0, 0, down)
			if ev == 0 {
				t.Fatal("CGEventCreateKeyboardEvent returned nil")
			}
			setUni(ev, 1, &chars[0])
			post(0, ev)
			release(ev)
		}
		time.Sleep(35 * time.Millisecond)
	}
	return true
}

// waitMain polls cond on the app's main thread until it holds or d passes.
func waitMain(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		var ok bool
		onMain(func() { ok = cond() })
		if ok {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestSmokeHomeSpotlight drives the real bar: it types a term, waits for
// the index, records the rows and the first-batch latency, and shoots the
// sheet in Chinese and English.
func TestSmokeHomeSpotlight(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := redesignShelf(t)
	// Production warms the index at launch; the harness has no launch path
	// of its own, so it does the same and lets the query finish before the
	// bar opens.
	mac.WarmSpotlight()
	time.Sleep(1200 * time.Millisecond)
	onMain(func() {
		a.show(false)
	})
	// Let the panel finish opening and the first frame settle: a user
	// opens the notch, then types.
	time.Sleep(2 * time.Second)

	start := time.Now()
	typed := typeIntoHome(t, "rakko")
	if typed {
		t.Log("typed rakko with real key events")
		// The keys have to land in the field; if the window lost the
		// keyboard between the frontmost check and the first key, set the
		// text the way the input would have.
		if !waitMain(1500*time.Millisecond, func() bool { return a.homeQuery != "" }) {
			t.Log("the keys did not reach the field: setting the text directly")
			onMain(func() {
				a.homeQuery = "rakko"
				a.homeQueryChanged()
			})
		}
	} else {
		t.Log("the test app was not frontmost: setting the field directly")
		onMain(func() {
			a.homeQuery = "rakko"
			a.homeQueryChanged()
		})
	}
	if !waitMain(8*time.Second, func() bool { return a.homeSpot.done }) {
		t.Fatalf("the query never finished: %+v", a.homeSpot)
	}
	var field string
	onMain(func() { field = a.homeQuery })
	if field != "rakko" {
		t.Fatalf("the field says %q, want rakko", field)
	}
	t.Logf("query %q: %d hits in %v", a.homeSpot.query, len(a.homeSpot.items), time.Since(start).Round(time.Millisecond))

	var rows []string
	onMain(func() {
		for _, h := range a.homeHits() {
			rows = append(rows, fmt.Sprintf("%s | %s | %s", h.group, h.title, h.sub))
		}
	})
	for _, r := range rows {
		t.Logf("row %s", r)
	}
	if len(rows) == 0 {
		t.Fatalf("no rows for rakko; the index found %d hits", len(a.homeSpot.items))
	}
	// The sheet must be open on a real frame before the shot.
	if !waitMain(2*time.Second, func() bool { return a.fx.resultsH > 1 }) {
		t.Fatalf("the sheet never opened: resultsH=%v", a.fx.resultsH)
	}
	spotShot(t, a, "spotlight-zh")

	// ⌘⏎ reveals the selection in Finder; keep Finder out of the shot and
	// only prove the row carries a reveal action.
	onMain(func() {
		for _, h := range a.homeHits() {
			if h.path != "" && h.reveal == nil {
				t.Fatalf("row %q has no reveal action", h.title)
			}
		}
	})

	// A finished empty query says 「没有找到」; while it gathers the sheet
	// stays shut. The term is random because the index also holds this
	// source file: a literal would match its own text.
	noMatch := fmt.Sprintf("qqzx%dv", time.Now().UnixNano())
	onMain(func() {
		a.homeQuery = noMatch
		a.homeQueryChanged()
	})
	var during float32
	onMain(func() { during = a.resultsTargetH() })
	if during != 0 {
		t.Fatalf("the sheet opened at %v pt while the query was still gathering", during)
	}
	if !waitMain(8*time.Second, func() bool { return a.homeSpot.done }) {
		t.Fatal("the empty query never finished")
	}
	onMain(func() {
		if n := len(a.homeSpot.items); n != 0 {
			t.Fatalf("%q matched %d hits", noMatch, n)
		}
	})
	var after float32
	onMain(func() { after = a.resultsTargetH() })
	if after <= 0 {
		t.Fatalf("the no-match sheet stayed shut (%v pt)", after)
	}
	time.Sleep(400 * time.Millisecond)
	spotShot(t, a, "spotlight-none-zh")

	// English. Switching the language saves it as the app's choice unless
	// MYGO_LOCALE pins this run (mygo's documented one-run override), so the
	// shot is taken only under that pin: a smoke test must not change the
	// user's language setting.
	if os.Getenv("MYGO_LOCALE") == "" {
		t.Log("MYGO_LOCALE is not set: skipping the English shot so the language choice is not saved")
		onMain(func() { a.hide(hideExplicit) })
		return
	}
	onMain(func() {
		mygo.App.SetLocale("en")
		a.locale = "en"
		a.homeQuery = "rakko"
		a.homeQueryChanged()
	})
	if !waitMain(8*time.Second, func() bool { return a.homeSpot.done }) {
		t.Fatal("the English query never finished")
	}
	if !waitMain(2*time.Second, func() bool { return a.fx.resultsH > 1 }) {
		t.Fatalf("the English sheet never opened: resultsH=%v", a.fx.resultsH)
	}
	spotShot(t, a, "spotlight-en")

	var groups []string
	onMain(func() {
		for _, h := range a.homeHits() {
			if len(groups) == 0 || groups[len(groups)-1] != h.group {
				groups = append(groups, h.group)
			}
		}
	})
	t.Logf("English groups: %v", groups)

	onMain(func() {
		mygo.App.SetLocale("zh-Hans")
		a.hide(hideExplicit)
	})
}
