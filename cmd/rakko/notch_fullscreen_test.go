package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
)

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
