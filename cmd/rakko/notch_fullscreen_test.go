package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func fullscreenTestShelf() *shelf {
	return &shelf{
		level:        2,
		notchX:       870,
		notchY:       0,
		notchW:       180,
		notchH:       32,
		x:            680,
		y:            0,
		dispBounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080},
		winH:         1,
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
}

func TestForegroundFullscreenSuppressesIdleHoverAndPointerGate(t *testing.T) {
	origFS := foregroundFullscreen
	defer func() { foregroundFullscreen = origFS }()

	now := time.Now()
	topCenter := mygo.Point{X: 960, Y: 1}

	a := &shelf{
		notchX:     870,
		notchY:     0,
		notchW:     180,
		notchH:     32,
		dispBounds: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080},
		winH:       1, // non-zero so overlayAcceptsMouse exercises the pointer gate
	}
	a.pointerTick, a.pointerTickValid = topCenter, true

	foregroundFullscreen = func() bool { return false }
	if !a.overHoverAnchor(topCenter, now) {
		t.Fatal("expected overHoverAnchor to trigger when not fullscreen")
	}
	if !a.pointerNearZone(topCenter) {
		t.Fatal("expected pointerNearZone to trigger when not fullscreen")
	}

	foregroundFullscreen = func() bool { return true }
	if a.overHoverAnchor(topCenter, now) {
		t.Fatal("overHoverAnchor must not trigger while a fullscreen app is foreground")
	}
	if a.pointerNearZone(topCenter) {
		t.Fatal("pointerNearZone must not wake monitor while a fullscreen app is foreground")
	}
	if a.overlayAcceptsMouse() {
		t.Fatal("idle overlayAcceptsMouse must stay false while a fullscreen app is foreground")
	}

	// Island mode at rest also stays click-through under fullscreen.
	a.island = true
	a.fx.w, a.fx.h = 120, 30
	if a.overlayAcceptsMouse() {
		t.Fatal("idle island overlayAcceptsMouse must stay false while a fullscreen app is foreground")
	}

	// Explicit open (hotkey / tray icon) still works when fullscreen.
	a.visible = true
	a.fx.w, a.fx.h, a.fx.openK = 520, 260, 1
	if !a.overlayAcceptsMouse() {
		t.Fatal("open panel must still accept mouse over its shell")
	}
}

func TestForegroundFullscreenSuppressesLiveActivityAndPeek(t *testing.T) {
	origFS := foregroundFullscreen
	origCursor := cursorPoint
	origDragState := dragState
	origDragLeft := dragLeftDown
	defer func() {
		foregroundFullscreen = origFS
		cursorPoint = origCursor
		dragState = origDragState
		dragLeftDown = origDragLeft
	}()

	now := time.Now()
	topCenter := mygo.Point{X: 960, Y: 1}
	cursorPoint = func() mygo.Point { return topCenter }
	dragState = func() (bool, int64) { return false, 0 }
	dragLeftDown = func() bool { return false }

	a := &shelf{
		notchX:     870,
		notchY:     0,
		notchW:     180,
		notchH:     32,
		dispBounds: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080},
		winH:       1,
	}
	a.pointerTick, a.pointerTickValid = topCenter, true
	a.live.shown = true
	a.fb.drawn = true

	foregroundFullscreen = func() bool { return true }
	if a.overHoverAnchor(topCenter, now) {
		t.Fatal("overHoverAnchor must not trigger for closed live activity under fullscreen")
	}
	if a.pointerNearZone(topCenter) {
		t.Fatal("pointerNearZone must not wake monitor for closed live activity under fullscreen")
	}
	if a.overlayAcceptsMouse() {
		t.Fatal("overlayAcceptsMouse must stay false for closed live activity under fullscreen")
	}

	a.monitorDrag(now)
	if a.peeking {
		t.Fatal("monitorDrag must not peek for closed live activity under fullscreen")
	}

	// Pre-existing peek also drops when a fullscreen app comes to front.
	a.peeking = true
	a.monitorDrag(now)
	if a.peeking {
		t.Fatal("monitorDrag must clear pre-existing peek under fullscreen")
	}

	// Explicit open still works even with live activity and fullscreen.
	a.visible = true
	a.fx.w, a.fx.h, a.fx.openK = 520, 260, 1
	if !a.overHoverAnchor(topCenter, now) {
		t.Fatal("explicitly open panel must keep overHoverAnchor under fullscreen")
	}
	if !a.pointerNearZone(topCenter) {
		t.Fatal("explicitly open panel must keep pointerNearZone under fullscreen")
	}
	if !a.overlayAcceptsMouse() {
		t.Fatal("explicitly open panel must accept mouse under fullscreen")
	}
}

func TestForegroundFullscreenSuppressesClosedPanelClicks(t *testing.T) {
	origFS := foregroundFullscreen
	origCursor := cursorPoint
	defer func() {
		foregroundFullscreen = origFS
		cursorPoint = origCursor
	}()
	cursorPoint = func() mygo.Point { return mygo.Point{} }

	// 1. Notification capsule shown while fullscreen: click must not open panel.
	a := fullscreenTestShelf()
	a.toastStatus("已复制", time.Now().Add(3*time.Second), statusOK)
	a.fb.drawn = true
	r := a.peekPillRect()
	pillCenter := mygo.Point{X: r.X + r.Width/2, Y: r.Y + r.Height - 6}

	foregroundFullscreen = func() bool { return true }
	a.mouseDown(false, 0, pillCenter)
	if a.visible {
		t.Fatal("clicking notification capsule under fullscreen must not open panel")
	}

	// Same during retracting.
	a.fb.drawn = false
	a.fb.retracting = true
	a.mouseDown(false, 0, pillCenter)
	if a.visible {
		t.Fatal("clicking retracting notification capsule under fullscreen must not open panel")
	}

	// Non-fullscreen contrast: clicking notification capsule still opens panel.
	foregroundFullscreen = func() bool { return false }
	a.fb.drawn = true
	a.fb.retracting = false
	a.mouseDown(false, 0, pillCenter)
	if !a.visible {
		t.Fatal("clicking notification capsule when not fullscreen must open panel")
	}

	// 2. Peek, idle island, and apps-mode clicks under fullscreen must not act.
	foregroundFullscreen = func() bool { return true }
	b := fullscreenTestShelf()
	b.peeking = true
	b.peekProgress = 1
	pr := b.peekPillRect()
	b.mouseDown(false, 0, mygo.Point{X: pr.X + pr.Width/2, Y: pr.Y + pr.Height/2})
	if b.visible {
		t.Fatal("clicking peek pill under fullscreen must not open panel")
	}

	c := fullscreenTestShelf()
	c.island = true
	c.fx.w, c.fx.h = islandIdleW, islandIdleH
	ir := c.islandRect()
	c.mouseDown(false, 0, mygo.Point{X: ir.X + ir.Width/2, Y: ir.Y + ir.Height/2})
	if c.islandPress.active || c.visible {
		t.Fatal("clicking idle island under fullscreen must not arm island press or open panel")
	}

	d := fullscreenTestShelf()
	d.apps.on = true
	d.mouseDown(false, 0, mygo.Point{X: d.notchX + d.notchW/2, Y: d.notchY + d.notchH/2})
	if d.visible || !d.apps.on {
		t.Fatal("clicking anchor in apps mode under fullscreen must not open panel or mutate state")
	}

	// 3. Explicitly open panel still handles outside click to hide under fullscreen.
	e := fullscreenTestShelf()
	e.visible = true
	e.fx.w, e.fx.h, e.fx.openK = 520, 260, 1
	e.mouseDown(false, 0, mygo.Point{X: e.dispBounds.X + 20, Y: e.dispBounds.Y + 400})
	if e.visible {
		t.Fatal("clicking outside an explicitly open panel under fullscreen must still hide it")
	}
}

// Fullscreen suppression folds the wings, so it must end the app switcher
// with them: otherwise ←/→/⏎ keep steering an invisible selection.
func TestFullscreenSuppressionEndsAppSwitch(t *testing.T) {
	origFS := foregroundFullscreen
	t.Cleanup(func() { foregroundFullscreen = origFS })
	foregroundFullscreen = func() bool { return true }
	stubKeyboard(t)
	a := fullscreenTestShelf()
	a.apps.on = true
	a.apps.keys = map[string]bool{"Left": true, "Right": true, "Enter": true}
	a.mouseDown(false, 0, mygo.Point{X: 100, Y: 500})
	if a.apps.on {
		t.Fatal("the app switcher survived the suppression that folded its wings")
	}
}
