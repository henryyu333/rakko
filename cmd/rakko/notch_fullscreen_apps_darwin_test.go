//go:build darwin

package main

// The suppression path needs the keyboard seams, which only the darwin
// tests define.

import (
	"testing"

	"github.com/egoist/mygo"

	"rakko/internal/settings"
)

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

// 没有鼠标按下的全屏接管（⌘Tab、游戏自己进全屏、投屏模式）也必须结束应用
// 切换：否则 ←/→ 还在动看不见的选中项，⏎ 被吞掉并把前台切走。
func TestFullscreenSuppressionEndsAppSwitchWithoutAClick(t *testing.T) {
	origFS := foregroundFullscreen
	t.Cleanup(func() { foregroundFullscreen = origFS })
	foregroundFullscreen = func() bool { return true }
	_, released, _ := stubKeyboard(t)
	a := fullscreenTestShelf()
	a.settings = settings.Default()
	a.apps.on = true
	a.apps.keys = map[string]bool{"Left": true, "Right": true, "Enter": true}
	driveMonitor(t, a, mygo.Point{X: 400, Y: 600}, false, 0, nil)
	if a.apps.on {
		t.Fatal("the app switcher kept running while the shell was suppressed")
	}
	want := map[string]bool{"Left": true, "Right": true, "Enter": true}
	for _, acc := range *released {
		delete(want, acc)
	}
	if len(want) != 0 {
		t.Fatalf("these global keys are still held: %v (released %v)", want, *released)
	}
}
