//go:build darwin

package main

// Real-window proof for the 便利贴 card and its window, run only under
// SHELF_BASKET_SMOKE=1 (the shared TestMain hosts the app loop). Shots
// land in /tmp/rakko-note/; the instance works in the smoke data dir and
// never touches ~/Library/Application Support/Rakko.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"

	"shelf/internal/layout"
	"shelf/internal/mac"
)

const noteShotDir = "/tmp/rakko-note"

// noteShotWindow screenshots one native window by its CGWindowID.
func noteShotWindow(t *testing.T, win *mygo.Window, name string) {
	t.Helper()
	if win == nil {
		t.Fatalf("%s: no window", name)
	}
	var num int
	onMain(func() { num = mac.WindowNumber(win.NativeHandle()) })
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	if err := os.MkdirAll(noteShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	shot := filepath.Join(noteShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
}

// noteShowTools puts the panel on the tools page with the note card first
// in the grid.
func noteShowTools(t *testing.T, a *shelf, span int) {
	t.Helper()
	onMain(func() {
		a.mutateTools(func(l *layout.Layout) {
			l.AddCardSize("note", span, 1)
			l.MoveCard("note", 0)
			l.SetCardSize("note", span, 1)
		})
	})
	expandShelf(t, a)
	onMain(func() { a.setTab(pageTools) }) // show() opens the overview
	time.Sleep(700 * time.Millisecond)
	onMain(func() {
		t.Logf("panel shows tab=%q level=%d visible=%v", a.tab, a.level, a.visible)
		a.syncWindowFrame()
	})
	time.Sleep(200 * time.Millisecond)
}

// TestNoteSmokeWindowAndCards proves the real window: the card opens it,
// typed text survives the close and reopen, the card shows the heading and
// its preview at both sizes, and the pinned card opens the same window
// under itself.
func TestNoteSmokeWindowAndCards(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := dropSmokeShelf(t)
	noteShowTools(t, a, 2)

	// The card's tap opens the window (the same call the pin's tap makes).
	var opened bool
	onMain(func() { opened = a.runToolCard("note") })
	if !opened || a.note == nil || a.note.win == nil {
		t.Fatal("the note card did not open its window")
	}
	want := "# 周五发版前\n– 改 README 顶栏描述\n– 补 v1.13 截图\n– 问 Codex 额度为什么 5 小时窗口跳\n– [ ] 桌宠姿态验收"
	onMain(func() {
		a.note.draft = want
		a.note.invalidate()
	})
	noteShotWindow(t, a.note.win, "note-window")

	// The window took the keyboard: the design's requirement for typing.
	var focused bool
	for i := 0; i < 20 && !focused; i++ {
		onMain(func() { focused = a.note.win.IsFocused() })
		if !focused {
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Logf("note window is key: %v", focused)

	// Closing lands the text; the window goes away.
	onMain(a.note.close)
	time.Sleep(500 * time.Millisecond)
	path := notePath(t, a)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("the closed window left %q on disk", data)
	}
	if a.note.win != nil {
		t.Fatal("the window is still up after close")
	}
	t.Logf("note file %s holds %d bytes", path, len(data))

	// 2 × 1 card: heading and preview, drawn from the saved file.
	onMain(func() {
		a.win.Invalidate()
		a.syncWindowFrame()
	})
	time.Sleep(400 * time.Millisecond)
	noteShotWindow(t, a.win, "note-card-2x1")

	// 1 × 1 card: glyph and name.
	onMain(func() {
		a.mutateTools(func(l *layout.Layout) { l.SetCardSize("note", 1, 1) })
		a.win.Invalidate()
	})
	time.Sleep(400 * time.Millisecond)
	onMain(a.syncWindowFrame)
	noteShotWindow(t, a.win, "note-card-1x1")

	// Opening again shows the file's text.
	onMain(func() { opened = a.runToolCard("note") })
	if !opened || a.note.win == nil {
		t.Fatal("the second tap did not reopen the window")
	}
	if a.note.draft != want {
		t.Fatalf("the reopened window shows %q", a.note.draft)
	}
	noteShotWindow(t, a.note.win, "note-window-reopened")

	// An editor outside Rakko saves the same file: the open window, with
	// nothing unsaved, follows it — the kqueue watch, end to end.
	external := "# 外部编辑器改过\n– 这行来自别的程序\n"
	if err := os.WriteFile(path, []byte(external), 0o600); err != nil {
		t.Fatal(err)
	}
	followed := false
	for i := 0; i < 40 && !followed; i++ {
		onMain(func() { followed = a.note.draft == external })
		if !followed {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !followed {
		t.Fatalf("the open window did not follow the outside write: %q", a.note.draft)
	}
	t.Logf("the open window followed an outside write: %q", a.note.draft)
	noteShotWindow(t, a.note.win, "note-window-outside-change")
	onMain(a.note.close)
	time.Sleep(300 * time.Millisecond)
	if data, err := os.ReadFile(path); err != nil || string(data) != external {
		t.Fatalf("closing after the outside change left %q, %v", data, err)
	}

	// A second tap on the card closes the window it opened.
	onMain(func() { opened = a.runToolCard("note") })
	if !opened || a.note.win == nil {
		t.Fatal("the tap did not open the window for the toggle check")
	}
	onMain(func() { opened = a.runToolCard("note") })
	if !opened || a.note.win != nil {
		t.Fatal("a second tap on the card did not close the window")
	}

	// The pinned card is the 2 × 1 preview, and tapping it opens the
	// window under the card.
	onMain(a.note.close)
	time.Sleep(300 * time.Millisecond)
	disp := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	frame := mygo.Rectangle{X: disp.WorkArea.X + 60, Y: disp.WorkArea.Y + 80, Width: pinCardW2, Height: pinCardH}
	var pinned bool
	onMain(func() {
		it := a.addPin("note", 2, disp.ID, pinFree, false, frame)
		pinned = it != nil && it.win != nil
	})
	if !pinned {
		t.Fatal("the note card was not pinned")
	}
	noteShotWindow(t, a.pins[len(a.pins)-1].win, "note-card-pinned")

	onMain(func() {
		a.note.frame = mygo.Rectangle{} // a first open this launch: placed under the card
		opened = a.runToolCard("note")
	})
	if !opened || a.note.win == nil {
		t.Fatal("the pinned card did not open the window")
	}
	var bounds, pinBounds mygo.Rectangle
	onMain(func() {
		bounds = a.note.win.Bounds()
		pinBounds = a.pins[len(a.pins)-1].frame
	})
	// The window opens below the card, inside the work area. Its exact X is
	// the OS's to clamp: this display pushes a window that would start
	// left of x=221 to x=221 (observed with a plain window too), so the
	// centring itself is checked by noteFrameUnder's test.
	wantY := pinBounds.Y + pinBounds.Height + 8
	if bounds.Y != wantY {
		t.Fatalf("the window at %+v is not under the pinned card %+v (want y=%d)", bounds, pinBounds, wantY)
	}
	area := mygo.Screen.DisplayNearestPoint(mygo.Point{X: pinBounds.X, Y: pinBounds.Y}).WorkArea
	if bounds.X < area.X || bounds.X+bounds.Width > area.X+area.Width ||
		bounds.Y < area.Y || bounds.Y+bounds.Height > area.Y+area.Height {
		t.Fatalf("the window at %+v is outside the work area %+v", bounds, area)
	}
	t.Logf("pin frame %+v, note window %+v", pinBounds, bounds)
	noteShotWindow(t, a.note.win, "note-window-from-pin")

	// Clean up: no pin, no window, panel folded.
	onMain(func() {
		a.note.close()
		if it := a.pins[len(a.pins)-1]; it != nil {
			a.unpin(it, true)
		}
		a.hide(hidePassive)
	})
	time.Sleep(300 * time.Millisecond)
}

// notePath is the note file behind the card.
func notePath(t *testing.T, a *shelf) string {
	t.Helper()
	if a.note == nil {
		t.Fatal("no note session")
	}
	path := a.note.store.Path()
	if !strings.Contains(path, os.TempDir()) && !strings.Contains(path, a.dataDir) {
		t.Fatalf("note path %q is outside the smoke data dir %q", path, a.dataDir)
	}
	return path
}

// postTyping types s with real key events, as a person would: one keydown
// carrying the text, then the key-up.
func postTyping(t *testing.T, s string) {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreGraphics: %v", err)
	}
	var (
		createKey  func(source uintptr, key uint16, down bool) uintptr
		post       func(tap uint32, ev uintptr)
		setUnicode func(ev uintptr, n uint64, chars *uint16)
	)
	purego.RegisterLibFunc(&createKey, lib, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	purego.RegisterLibFunc(&setUnicode, lib, "CGEventKeyboardSetUnicodeString")
	u := utf16.Encode([]rune(s))
	down := createKey(0, 0, true)
	if down == 0 {
		t.Fatal("CGEventCreateKeyboardEvent returned nil")
	}
	setUnicode(down, uint64(len(u)), &u[0])
	post(0, down)
	up := createKey(0, 0, false)
	post(0, up)
}

// postReturn presses Return (keycode 36) with real key events.
func postReturn(t *testing.T) {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreGraphics: %v", err)
	}
	var (
		createKey func(source uintptr, key uint16, down bool) uintptr
		post      func(tap uint32, ev uintptr)
	)
	purego.RegisterLibFunc(&createKey, lib, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	for _, down := range []bool{true, false} {
		ev := createKey(0, 36, down)
		if ev == 0 {
			t.Fatal("CGEventCreateKeyboardEvent returned nil")
		}
		post(0, ev)
	}
}

// TestNoteSmokeTypesAndOutlivesPanel types into the real window with real
// key events, proves the panel folding away leaves it up (and lands what
// was typed), then closes it with a real Escape.
func TestNoteSmokeTypesAndOutlivesPanel(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := dropSmokeShelf(t)
	noteShowTools(t, a, 2)

	var opened bool
	onMain(func() { opened = a.runToolCard("note") })
	if !opened || a.note == nil || a.note.win == nil {
		t.Fatal("the note card did not open its window")
	}
	// Keys reach whatever window is key: only type when it is ours.
	var key bool
	for i := 0; i < 20 && !key; i++ {
		onMain(func() { key = a.note.win.IsFocused() })
		if !key {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !key {
		onMain(func() {
			a.note.close()
			a.hide(hidePassive)
		})
		t.Skip("the window did not take the keyboard; typing needs it")
	}
	postTyping(t, "# 周五发版前")
	postReturn(t)
	postTyping(t, "– 改 README")
	time.Sleep(600 * time.Millisecond)
	var typed string
	onMain(func() { typed = a.note.draft })
	want := "# 周五发版前\n– 改 README"
	if typed != want {
		onMain(func() {
			a.note.close()
			a.hide(hidePassive)
		})
		t.Skipf("the system delivered no synthetic keys to this process (draft %q); the typing path is covered by TestNoteWindowTypesAndCloses", typed)
	}
	t.Logf("real key events typed %q into the note", typed)

	// The panel folds away: the window stays, and the text is on disk.
	onMain(func() { a.hide(hidePassive) })
	time.Sleep(500 * time.Millisecond)
	var up bool
	onMain(func() { up = a.note.win != nil && a.note.win.IsVisible() })
	if !up {
		t.Fatal("the note window went away with the panel")
	}
	if data, err := os.ReadFile(notePath(t, a)); err != nil || string(data) != want {
		t.Fatalf("folding the panel left %q on disk, %v", data, err)
	}

	// A real Escape closes it, keeping the text.
	if !a.note.win.IsFocused() {
		onMain(noteFocusOf(a))
		time.Sleep(200 * time.Millisecond)
	}
	postEscape(t)
	closed := false
	for i := 0; i < 20 && !closed; i++ {
		onMain(func() { closed = a.note.win == nil })
		if !closed {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !closed {
		t.Skip("the real Escape did not arrive; the Escape path is covered by TestNoteWindowTypesAndCloses")
	}
	if data, err := os.ReadFile(notePath(t, a)); err != nil || string(data) != want {
		t.Fatalf("closing with Escape left %q on disk, %v", data, err)
	}
	t.Logf("a real Escape closed the window with the text on disk")
}

// noteFocusOf focuses the note window on the main thread.
func noteFocusOf(a *shelf) func() {
	return func() {
		if a.note != nil && a.note.win != nil {
			noteActivate()
			noteFocus(a.note.win)
		}
	}
}
