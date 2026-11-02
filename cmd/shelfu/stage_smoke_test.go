//go:build darwin

package main

// Shared seams for the real-window smoke evidence: one shelf with a little
// of everything, and shots of the resident window or of the desktop around
// it. Run with SHELF_BASKET_SMOKE=1; everything lands in /tmp/shelf-stagef-A.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"shelf/internal/mac"
)

const stageADir = "/tmp/shelf-stagef-A"

// stageAShotRegion captures the window and the desktop around it, so an
// appearance change can be judged against what is behind the panel.
func stageAShotRegion(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	var r mygo.Rectangle
	onMain(func() {
		if actual := a.win.Bounds(); actual.Width > 0 {
			r = actual
		} else {
			r = a.desiredFrame()
		}
	})
	time.Sleep(400 * time.Millisecond)
	if err := os.MkdirAll(stageADir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stageADir, name+".png")
	pad := 140
	out, err := exec.Command("screencapture", "-x", "-o", "-R",
		fmt.Sprintf("%d,%d,%d,%d", r.X-pad, max(r.Y-pad, 0), r.Width+2*pad, r.Height+2*pad), path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, path)
	return path
}

// stageAShot screenshots the resident window into name.png.
func stageAShot(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(400 * time.Millisecond) // let the drawn frame settle
	if err := os.MkdirAll(stageADir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stageADir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, path)
	return path
}

// stageAShelf is the shared smoke shelf with the springs at rest.
func stageAShelf(t *testing.T) *shelf {
	t.Helper()
	// The test binary's resource path is cmd/shelfu/resources; the brand
	// artwork lives in the repository's resources/otter.
	if _, err := os.Stat("../../resources/otter"); err == nil {
		otterDir = func() string { return "../../resources/otter" }
		resetOtters()
	}
	a := redesignShelf(t)
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.status, a.statusKind, a.statusUntil = "", statusInfo, time.Time{}
		a.update, a.updateInstalled, a.updateInstalling = nil, false, false
		a.conv = nil
		a.level, a.tab, a.tabFrom = 1, pageTray, pageTray
		a.visible = true
		a.progress, a.animationFrom = 1, 1
		a.animationStart = time.Time{}
		a.peeking = true
		a.peekProgress, a.peekFrom = 1, 1
		a.peekAnimStart = time.Time{}
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	return a
}

// fillShelf puts a little of everything in the store: five tray items, a
// clipboard history and a playing track.
func fillShelf(t *testing.T, a *shelf) {
	t.Helper()
	onMain(func() {
		files := t.TempDir()
		for i, name := range []string{"报告.pdf", "截图 10.42.png", "草稿.docx", "表格.xlsx", "设计稿.png"} {
			p := writeFile(t, files, name, testPNGBytes(t))
			if err := a.store.AddFiles([]string{p}); err != nil {
				t.Fatal(err)
			}
			_ = i
		}
		for i := range 12 {
			if err := a.store.AddClipText(fmt.Sprintf("剪贴板内容 %d · 一段足够长的文字用于检查省略", i), "com.apple.Safari"); err != nil {
				t.Fatal(err)
			}
		}
		fakeTrack(a, true)
		a.modules = []module{a.np}
	})
}
