//go:build darwin

package main

import (
	"os"
	"testing"
	"time"
)

// onMainCheck runs fn on the app's main thread and fails the test with what
// it reports. t.Fatal cannot run inside onMain: it abandons the goroutine
// that would have closed the handshake, leaving the caller waiting forever.
func onMainCheck(t *testing.T, fn func() string) {
	t.Helper()
	var problem string
	onMain(func() { problem = fn() })
	if problem != "" {
		t.Fatal(problem)
	}
}

// TestNoteWindowKeepsOnTop: 置顶 is the note's own state, and the window is
// rebuilt on every open — so a window built after the flag was set must come
// up on top, exactly as the bar draws it, and a plain one must not. A real
// window, because the flag reaches the window server only through one; the
// smoke harness is what runs a real app loop.
func TestNoteWindowKeepsOnTop(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to open a real note window")
	}
	a := smokeShelf
	onMainCheck(t, func() string {
		a.note = nil
		n := a.noteSession()
		if n == nil {
			return "no note session"
		}
		n.setTop(true)
		if !n.open() {
			return "the note did not open"
		}
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	onMainCheck(t, func() string {
		if w := a.note.win; w == nil || !w.IsAlwaysOnTop() {
			return "a window built over a lit 置顶 is not on top"
		}
		a.note.close()
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	onMainCheck(t, func() string {
		n := a.note
		n.setTop(false)
		if !n.open() {
			return "the note did not reopen"
		}
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	onMainCheck(t, func() string {
		n := a.note
		problem := ""
		if w := n.win; w != nil && w.IsAlwaysOnTop() {
			problem = "a window built without 置顶 came up on top"
		}
		n.close()
		if n.watch != nil {
			closeNoteWatch(n)
		}
		n.watch = nil
		a.note = nil
		return problem
	})
}

// TestNoteCardShape: the card is a widget with the 1 × 1 and 2 × 1 sizes,
// and a pinned note is wide because its body needs two columns.
