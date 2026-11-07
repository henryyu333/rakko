//go:build darwin

package main

// Real-window evidence for the search bar's typing path: the panel is
// opened, the query is typed with HID keystrokes so the real Spotlight
// index answers with its own batches, the field is cleared with real
// backspaces, pages are switched and the panel closes. Run with
// RAKKO_BASKET_SMOKE=1 through the native gate while recording; the frame
// pacing after each keystroke lands in the test log.
//
// The scenario is the one the user's screen recording showed:
// 打开 → 打字（结果面板出现/变化）→ 清空 → 切页 → 收起. Every keystroke
// must repaint inside one fifth of a second; the shell's resizes are
// proved separately (TestSmokeOpenPanelKeepsOneFrame) and by the video.

import (
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"

	"rakko/internal/mac"
)

// typingMeter records when each frame was built, so the test can time the
// repaint that follows a keystroke.
type typingMeter struct {
	mu   sync.Mutex
	when []time.Time
}

func (m *typingMeter) frame(_, _ float32, _ mygo.Rectangle) {
	now := time.Now()
	m.mu.Lock()
	m.when = append(m.when, now)
	m.mu.Unlock()
}

// firstAfter waits until a frame built after t is recorded and reports how
// long after t it came, or -1 when none came inside d.
func (m *typingMeter) firstAfter(t time.Time, d time.Duration) time.Duration {
	deadline := time.Now().Add(d)
	for {
		var first time.Time
		m.mu.Lock()
		for _, f := range m.when {
			if f.After(t) {
				first = f
				break
			}
		}
		m.mu.Unlock()
		if !first.IsZero() {
			return first.Sub(t)
		}
		if time.Now().After(deadline) {
			return -1
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (m *typingMeter) reset() {
	m.mu.Lock()
	m.when = nil
	m.mu.Unlock()
}

// homeKeys posts real HID keyboard events for text. Each rune is sent as a
// key-down/key-up pair; "\b" is the Delete key. It reports whether the
// test app was frontmost, so keys never land in another app.
func homeKeys(t *testing.T, text string, gap time.Duration) bool {
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
		key, uni := uint16(0), uint16(r)
		if r == '\b' {
			key, uni = 51, 0 // kVK_Delete
		}
		for _, down := range []bool{true, false} {
			ev := createKey(0, key, down)
			if ev == 0 {
				t.Fatal("CGEventCreateKeyboardEvent returned nil")
			}
			if uni != 0 {
				setUni(ev, 1, &uni)
			}
			post(0, ev)
			release(ev)
		}
		time.Sleep(gap)
	}
	return true
}

// typeIntoHomeEvery is typeIntoHome (spotlight_smoke_test.go) with the gap
// between keys given: typing slowly lets the index answer between keys and
// the results sheet change size while the bar still has the keyboard.
func typeIntoHomeEvery(t *testing.T, text string, gap time.Duration) bool {
	t.Helper()
	return homeKeys(t, text, gap)
}

// takeHomeKeyboard makes sure this app is frontmost and the bar's field
// has the keyboard before a chunk of typing; activation is asynchronous
// and another app can have taken the front after the index answered.
func takeHomeKeyboard(t *testing.T, a *shelf) bool {
	t.Helper()
	front := func() bool {
		var ok bool
		onMain(func() { ok = mac.FrontmostPID() == mac.OwnPID() })
		return ok
	}
	if waitFront := waitMain(300*time.Millisecond, front); waitFront {
		return true
	}
	for range 3 {
		onMain(func() {
			mac.ActivateSelf()
			a.win.Focus()
		})
		if waitMain(700*time.Millisecond, front) {
			return true
		}
	}
	return false
}

// homeSettled reports whether the opening/closing springs and the bar's
// channels have all come to rest.
func homeSettled(a *shelf) bool {
	return a.progress >= 0.999 && !a.fx.openMoving && !a.fx.sizeMoving &&
		a.tabPhase(time.Now()) >= 0.999
}

// waitHomeQuery waits until the expected text has an answer on screen: the
// index finished (or the sheet said 没有找到) and its sheet finished
// growing.
func waitHomeQuery(t *testing.T, a *shelf, want string) bool {
	t.Helper()
	if !waitMain(8*time.Second, func() bool {
		return a.homeQuery == want && !a.homeSearching()
	}) {
		return false
	}
	return waitMain(2*time.Second, func() bool {
		return !a.fx.openMoving && !a.homeSearching()
	})
}

// typeHomeChunk types text into the field, retaking the keyboard first,
// and falls back to setting the text directly when no key could be posted
// (a focus flake must not type into another app). It reports whether the
// keys really went in.
func typeHomeChunk(t *testing.T, a *shelf, text string, gap time.Duration, state func()) bool {
	t.Helper()
	start := time.Now()
	typed := takeHomeKeyboard(t, a) && typeIntoHomeEvery(t, text, gap)
	end := time.Now()
	t.Logf("chunk %q start_us=%d end_us=%d typed=%v", text, start.UnixMicro(), end.UnixMicro(), typed)
	if typed {
		return true
	}
	t.Logf("keys %q did not reach the field: setting the state directly", text)
	if state != nil {
		onMain(state)
	}
	return false
}

func TestSmokeHomeTypingMotion(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	a := redesignShelf(t)
	// Production warms the index at launch; the harness does the same.
	mac.WarmSpotlight()
	time.Sleep(1200 * time.Millisecond)
	stop := startMotionTicker(a)
	defer stop()
	defer func() { onMain(func() { motionClosed(a) }) }()

	// First openings pay the GPU pipeline setup; warm it before the
	// measured repetitions.
	for range 3 {
		motionClosed(a)
		time.Sleep(200 * time.Millisecond)
		onMain(func() { a.show(false) })
		time.Sleep(700 * time.Millisecond)
		onMain(func() { a.hide(hidePassive) })
		time.Sleep(700 * time.Millisecond)
	}
	motionClosed(a)
	time.Sleep(300 * time.Millisecond)

	meter := &typingMeter{}
	onMain(func() { frameTrace = meter.frame })
	defer onMain(func() { frameTrace = nil })

	typed, direct := 0, 0
	const reps = 10
	setQuery := func(q string) func() {
		return func() {
			a.homeQuery = q
			a.homeQueryChanged()
		}
	}
	for rep := range reps {
		onMain(func() { a.show(false) })
		if !waitMain(2*time.Second, func() bool { return homeSettled(a) }) {
			t.Fatalf("rep %d: the panel never settled open", rep)
		}
		// One keystroke has to reach the screen inside 100 ms: the field
		// changes its text on the key event, and the bar's own width and
		// the sheet follow without waiting for the debounce.
		if takeHomeKeyboard(t, a) {
			meter.reset()
			t0 := time.Now()
			typedFirst := typeIntoHomeEvery(t, "r", 0)
			if typedFirst {
				typed++
			}
			t.Logf("chunk %q start_us=%d end_us=%d typed=%v", "r", t0.UnixMicro(), time.Now().UnixMicro(), typedFirst)
			if d := meter.firstAfter(t0, 200*time.Millisecond); d < 0 || d > 100*time.Millisecond {
				t.Errorf("rep %d: the first keystroke repainted after %v, want inside 100ms", rep, d)
			} else {
				t.Logf("rep %d: first keystroke repainted after %v", rep, d.Round(time.Millisecond))
			}
		} else {
			onMain(setQuery("r"))
			direct++
		}
		// Let the index answer between keys: the sheet appears while the
		// bar is still being typed into.
		time.Sleep(400 * time.Millisecond)
		if !typeHomeChunk(t, a, "akko", 140*time.Millisecond, setQuery("rakko")) {
			direct++
		} else {
			typed++
		}
		if !waitHomeQuery(t, a, "rakko") {
			t.Fatalf("rep %d: the long query never answered: %+v", rep, a.homeSpot)
		}
		// A pause at rest. The old code dropped its envelope here and the
		// next result change resized the drawn panel; the fix holds one
		// frame for the whole open session, so this pause changes nothing.
		time.Sleep(900 * time.Millisecond)

		// Deleting one key at a time answers new batches: each answer
		// changes the sheet while the panel is fully open.
		for _, q := range []string{"rakk", "rak"} {
			q := q
			if !typeHomeChunk(t, a, "\b", 0, setQuery(q)) {
				direct++
			} else {
				typed++
			}
			if !waitHomeQuery(t, a, q) {
				t.Fatalf("rep %d: the query %q never answered", rep, q)
			}
			time.Sleep(700 * time.Millisecond)
		}
		if !typeHomeChunk(t, a, "\b\b\b", 130*time.Millisecond, setQuery("")) {
			direct++
		} else {
			typed++
		}
		if !waitMain(3*time.Second, func() bool { return a.homeQuery == "" }) {
			t.Fatalf("rep %d: the field never cleared: %q", rep, a.homeQuery)
		}
		time.Sleep(500 * time.Millisecond)

		// A second, shorter query with a pause after each key: batches
		// arrive while the sheet is still open.
		if !typeHomeChunk(t, a, "t", 0, setQuery("t")) {
			direct++
		} else {
			typed++
		}
		time.Sleep(400 * time.Millisecond)
		if !typeHomeChunk(t, a, "r", 0, setQuery("tr")) {
			direct++
		} else {
			typed++
		}
		if !waitHomeQuery(t, a, "tr") {
			t.Fatalf("rep %d: the short query never answered", rep)
		}
		// The sheet is settled again, exactly the moment the video's
		// shrunk/grown frames appeared.
		time.Sleep(800 * time.Millisecond)
		if !typeHomeChunk(t, a, "\b\b", 130*time.Millisecond, setQuery("")) {
			direct++
		} else {
			typed++
		}
		time.Sleep(400 * time.Millisecond)

		// Pages: the tall shell replaces the bar, three tabs cross-fade,
		// then the bar returns.
		onMain(func() { a.setLevel(2) })
		time.Sleep(500 * time.Millisecond)
		for _, id := range []pageID{pageClipboard, pageHome, pageTray} {
			id := id
			onMain(func() { a.setTab(id) })
			time.Sleep(400 * time.Millisecond)
		}
		onMain(func() { a.setLevel(1) })
		time.Sleep(450 * time.Millisecond)

		onMain(func() { a.hide(hidePassive) })
		time.Sleep(750 * time.Millisecond)
	}
	if typed == 0 {
		t.Fatal("no keystroke reached the test app: it was never frontmost")
	}
	t.Logf("%d repetitions: %d typed chunks with real key events, %d set directly", reps, typed, direct)
}
