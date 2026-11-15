package main

// Parity between the closed shell and the platform that draws it. These
// tests run wherever the app runs, including Windows, because the simulated
// notch and the countdown wings are exactly the states a camera housing and
// a system-drawn black strip made invisible on macOS.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
)

// parityShelf is a closed shelf with the hardware notch's measured box. A
// placeholder window keeps view() on its production path without a window
// server.
func parityShelf() *shelf {
	return &shelf{
		level:        2,
		notchX:       600,
		notchY:       0,
		notchW:       180,
		notchH:       32,
		x:            410,
		y:            0,
		dispBounds:   mygo.Rectangle{Width: 1470, Height: 956},
		dispWork:     mygo.Rectangle{Width: 1470, Height: 956},
		settings:     settings.Default(),
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
}

func parityWindow() *shelf {
	a := parityShelf()
	a.win = &mygo.Window{}
	return a
}

// TestClosedCountdownShowsItsWings: the manual promises the running (and
// paused) countdown on the closed notch: a ring in the left wing and the
// remaining time in the right one. peekView() has always drawn them, but
// the shell only springs open (peekK) when peekTarget asks for it, and the
// timer was missing from that list — the wings were sized by peekSize()
// (so the native strip grew) and never painted.
func TestClosedCountdownShowsItsWings(t *testing.T) {
	a := parityWindow()
	a.clock = newClockModule(a)
	a.clock.playSound = func() {}
	a.modules = []module{a.clock}
	view := ui.NewTester(a.view, 640, 200)
	view.Frame() // the idle frame the wings grow out of
	_, baseH := a.anchorSize()
	if a.fx.peekK != 0 {
		t.Fatalf("the idle shell already peeks at %.2f", a.fx.peekK)
	}
	a.clock.startTimer(time.Now(), 5*time.Minute)
	for range 80 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.peekK < 0.99 {
		t.Fatalf("the closed notch never opened for the countdown: peekK=%.2f", a.fx.peekK)
	}
	if a.fx.h <= baseH {
		t.Fatalf("the shell did not grow past the anchor's %.0f: h=%.1f", baseH, a.fx.h)
	}
	wantW, _ := a.timerWingsSize()
	if a.fx.w < wantW-1 {
		t.Fatalf("the shell is %.1f wide, the wings need %.1f", a.fx.w, wantW)
	}
	if _, ok := view.Find("5:00"); !ok {
		t.Fatalf("the wings did not draw the remaining time: %v", view.Texts())
	}
}

// TestClosedWingsAskForTheNextSecond: the wings' reading is a countdown, not
// a still. The monitor tick only repaints when the panel or the hover peek
// shows a clock; the closed notch's wings were not in that list, so the
// reading froze until some other event repainted the strip.
func TestClosedWingsAskForTheNextSecond(t *testing.T) {
	a := parityShelf()
	a.clock = newClockModule(a)
	now := time.Now()
	a.clock.startTimer(now, 5*time.Minute)
	due := a.clock.nextTick(now)
	if due.IsZero() || due.After(now.Truncate(time.Second).Add(time.Second)) {
		t.Fatalf("the running wings never ask for the next second: %v", due)
	}
	a.clock.pauseTimer(now)
	if due := a.clock.nextTick(now); !due.IsZero() && due.Before(a.clock.end) {
		t.Fatalf("the paused wings keep asking for frames: %v", due)
	}
}

// TestHiddenStopwatchDoesNotRepaintTheClosedWings: a paused countdown's
// wings are a still image, and the closed notch never shows the stopwatch.
// A running stopwatch still woke the tick every second — nextTick asked for
// the next boundary and paintKey carried the hidden reading — so the whole
// strip repainted once a second for a number nobody could see. A running
// countdown's wings must keep moving exactly as before.
func TestHiddenStopwatchDoesNotRepaintTheClosedWings(t *testing.T) {
	a := parityShelf()
	a.clock = newClockModule(a)
	now := time.Now()
	a.clock.startTimer(now, 5*time.Minute)
	a.clock.pauseTimer(now)
	a.clock.startWatch(now)
	if due := a.clock.nextTick(now); !due.IsZero() {
		t.Fatalf("the hidden stopwatch asked for a frame at %v", due)
	}
	a.clock.tick(now)
	first := a.clock.lastPaint
	a.clock.tick(now.Add(time.Second))
	if a.clock.lastPaint != first {
		t.Fatalf("the closed wings repainted for a hidden second: %q → %q", first, a.clock.lastPaint)
	}

	a.clock.resumeTimer(now.Add(2 * time.Second))
	at := now.Add(3 * time.Second)
	due := a.clock.nextTick(at)
	if due.IsZero() || due.After(at.Truncate(time.Second).Add(time.Second)) {
		t.Fatalf("the running wings stopped asking for the next second: %v", due)
	}
	a.clock.tick(at)
	running := a.clock.lastPaint
	a.clock.tick(at.Add(time.Second))
	if a.clock.lastPaint == running {
		t.Fatalf("the running wings kept the reading %q across the next second", running)
	}
}

// TestCountdownReadingAsksForAFrameEachSecond: the drawn reading is a
// countdown, so the tick must ask for a new frame when the second changes.
// It compared a freshly computed key with itself and never asked, which left
// a running countdown (or stopwatch) frozen on screen until something else
// repainted the shell.
func TestCountdownReadingAsksForAFrameEachSecond(t *testing.T) {
	a := parityWindow()
	a.clock = newClockModule(a)
	now := time.Now()
	a.clock.startTimer(now, 5*time.Minute)
	a.clock.tick(now)
	first := a.clock.lastPaint
	if first == "" {
		t.Fatal("the running countdown never asked for a frame")
	}
	a.clock.tick(now.Add(time.Second))
	if a.clock.lastPaint == first {
		t.Fatalf("the reading stayed on %q across the next second", first)
	}
}
