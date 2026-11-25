//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
	"rakko/internal/platform"
)

func seg2WarpCursor(x, y float64) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		return
	}
	var warp func(p platform.CGPoint) int32
	purego.RegisterLibFunc(&warp, lib, "CGWarpMouseCursorPosition")
	warp(platform.CGPoint{X: x, Y: y})
}

func seg2ShotPath(name string) string {
	root := os.Getenv("RAKKO_SHOTS")
	if root == "" {
		return ""
	}
	return filepath.Join(root, "seg2", name)
}

func seg2CaptureRegion(t *testing.T, r mygo.Rectangle, name string) {
	t.Helper()
	outPath := seg2ShotPath(name)
	if outPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("screencapture", "-x", "-R", fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height), outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screencapture %s failed: %v (%s)", name, err, out)
	}
	t.Logf("saved %s", outPath)
}

func seg2CaptureWindow(t *testing.T, win *mygo.Window, name string) {
	t.Helper()
	if win == nil {
		t.Fatalf("%s: nil window", name)
	}
	var num int
	onMain(func() { num = platform.WindowNumber(win.NativeHandle()) })
	if num == 0 {
		t.Fatalf("%s: zero CGWindowID", name)
	}
	outPath := seg2ShotPath(name)
	if outPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screencapture -l %s failed: %v (%s)", name, err, out)
	}
	t.Logf("saved %s", outPath)
}

func TestWindowButtonsSmokeShotPathUsesEnv(t *testing.T) {
	t.Setenv("RAKKO_SHOTS", "")
	if got := seg2ShotPath("shot.png"); got != "" {
		t.Fatalf("seg2ShotPath without RAKKO_SHOTS = %q, want empty", got)
	}
	t.Setenv("RAKKO_SHOTS", "/tmp/custom-shots")
	want := filepath.Join("/tmp/custom-shots", "seg2", "shot.png")
	if got := seg2ShotPath("shot.png"); got != want {
		t.Fatalf("seg2ShotPath with RAKKO_SHOTS = %q, want %q", got, want)
	}
}

func TestWindowButtonsMacSmokeAndScreenshots(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run native window button smoke tests")
	}
	origCursor := mygo.Screen.CursorScreenPoint()
	t.Cleanup(func() {
		seg2WarpCursor(float64(origCursor.X), float64(origCursor.Y))
	})

	a := smokeShelf
	work := mygo.Screen.DisplayNearestPoint(origCursor).WorkArea

	// 1. Sticky note: traffic lights, green button hover menu (Move & Resize / Fill & Arrange), red close saves draft.
	var noteBounds mygo.Rectangle
	onMainCheck(t, func() string {
		a.note = nil
		n := a.noteSession()
		if n == nil {
			return "no note session"
		}
		n.text = "# 便利贴系统按钮验收\n第一行内容\n"
		n.draft = n.text
		n.seen = n.text
		n.frame = mygo.Rectangle{X: work.X + 160, Y: work.Y + 120, Width: noteW, Height: noteH}
		if !n.open() {
			return "note open failed"
		}
		noteBounds = n.win.Bounds()
		return ""
	})
	t.Cleanup(func() {
		onMain(func() {
			if a.note != nil {
				if a.note.win != nil {
					a.note.close()
				}
				closeNoteWatch(a.note)
				a.note = nil
			}
		})
	})
	time.Sleep(500 * time.Millisecond)
	onMain(func() { noteBounds = a.note.win.Bounds() })
	seg2CaptureWindow(t, a.note.win, "mac-note-traffic-lights.png")

	// Hover green button (X=12, Y=8 -> close ~19, min ~39, green ~59, y ~15).
	ngx := float64(noteBounds.X) + 59
	ngy := float64(noteBounds.Y) + 15
	postMouse(t, ngx-20, ngy+40, 5)
	time.Sleep(150 * time.Millisecond)
	postMouse(t, ngx, ngy, 5)
	time.Sleep(1600 * time.Millisecond)
	seg2CaptureRegion(t, mygo.Rectangle{
		X: max(0, noteBounds.X-40), Y: max(0, noteBounds.Y-30),
		Width: noteBounds.Width + 240, Height: noteBounds.Height + 180,
	}, "mac-note-green-hover.png")

	// Move off green button (click bottom-right of note body, well clear of the popover), edit draft, click red close button.
	postMouse(t, float64(noteBounds.X+noteBounds.Width-24), float64(noteBounds.Y+noteBounds.Height-24), 5, 1, 2)
	time.Sleep(200 * time.Millisecond)
	wantNote := "# 便利贴系统按钮验收\n红色按钮关窗保存的内容\n"
	onMain(func() {
		a.note.draft = wantNote
	})
	nrx := float64(noteBounds.X) + 19
	nry := float64(noteBounds.Y) + 15
	postMouse(t, nrx, nry, 5, 1, 2)
	time.Sleep(400 * time.Millisecond)
	onMainCheck(t, func() string {
		if a.note.win != nil {
			return "note window stayed open after clicking red close button"
		}
		got, err := a.note.store.Read()
		if err != nil {
			return err.Error()
		}
		if got != wantNote {
			return fmt.Sprintf("note disk = %q, want %q", got, wantNote)
		}
		return ""
	})

	// 2. Whiteboard: traffic lights, green button hover menu (with Full Screen), fullscreen layout, half-screen tiled layout, red close saves.
	var boardBounds mygo.Rectangle
	onMainCheck(t, func() string {
		a.board = nil
		b := a.boardSession()
		if b == nil {
			return "no board session"
		}
		b.ed.SetGeoTool("rectangle")
		b.ed.PointerDown(160, 140, 0, 1, editor.Mods{}, 0.1)
		b.ed.PointerMove(360, 260, editor.Mods{}, 0.2)
		b.ed.PointerUp(360, 260, editor.Mods{}, 0.3)
		b.ed.SetTool(editor.ToolSelect)
		b.ed.SelectNone()
		b.frame = mygo.Rectangle{X: work.X + 100, Y: work.Y + 80, Width: 960, Height: 620}
		if !b.open() {
			return "board open failed"
		}
		boardBounds = b.win.Bounds()
		return ""
	})
	t.Cleanup(func() {
		onMain(func() {
			if a.board != nil && a.board.win != nil {
				if a.board.win.IsFullScreen() {
					a.board.win.SetFullScreen(false)
				}
				a.board.close()
			}
			a.board = nil
		})
	})
	time.Sleep(600 * time.Millisecond)
	onMain(func() { boardBounds = a.board.win.Bounds() })
	seg2CaptureWindow(t, a.board.win, "mac-board-traffic-lights.png")

	// Hover green button on whiteboard.
	bgx := float64(boardBounds.X) + 59
	bgy := float64(boardBounds.Y) + 15
	postMouse(t, bgx-20, bgy+40, 5)
	time.Sleep(150 * time.Millisecond)
	postMouse(t, bgx, bgy, 5)
	time.Sleep(1600 * time.Millisecond)
	seg2CaptureRegion(t, mygo.Rectangle{
		X: max(0, boardBounds.X-40), Y: max(0, boardBounds.Y-30),
		Width: 420, Height: 420,
	}, "mac-board-green-hover.png")

	// Enter macOS full screen, verify IsFullScreen + stage adapts, capture full display, exit full screen.
	postMouse(t, float64(boardBounds.X+boardBounds.Width-40), float64(boardBounds.Y+boardBounds.Height-40), 5, 1, 2)
	time.Sleep(200 * time.Millisecond)
	onMain(func() {
		a.board.win.SetFullScreen(true)
	})
	for i := 0; i < 30; i++ {
		var fs bool
		onMain(func() { fs = a.board.win != nil && a.board.win.IsFullScreen() })
		if fs {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(800 * time.Millisecond)
	var fsVW, fsVH float64
	onMain(func() {
		fsVW, fsVH = a.board.ed.VW, a.board.ed.VH
		t.Logf("board fullscreen VW=%.0f VH=%.0f stageRect=%+v", fsVW, fsVH, a.board.stageRect)
	})
	if fsVW <= float64(boardBounds.Width) {
		t.Errorf("board fullscreen viewport did not expand: VW=%.0f <= prev=%d", fsVW, boardBounds.Width)
	}
	seg2CaptureWindow(t, a.board.win, "mac-board-fullscreen.png")

	onMain(func() {
		a.board.win.SetFullScreen(false)
	})
	for i := 0; i < 30; i++ {
		var fs bool
		onMain(func() { fs = a.board.win != nil && a.board.win.IsFullScreen() })
		if !fs {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(800 * time.Millisecond)

	// Tile to half-screen width and capture layout.
	halfRect := mygo.Rectangle{X: work.X, Y: work.Y, Width: work.Width / 2, Height: work.Height}
	onMain(func() {
		a.board.win.SetBounds(halfRect)
	})
	time.Sleep(500 * time.Millisecond)
	onMain(func() {
		boardBounds = a.board.win.Bounds()
		t.Logf("board half-tiled bounds=%+v stageRect=%+v VW=%.0f VH=%.0f", boardBounds, a.board.stageRect, a.board.ed.VW, a.board.ed.VH)
	})
	seg2CaptureWindow(t, a.board.win, "mac-board-half-tiled.png")

	// Add an inline text shape, click red close button, verify disk save.
	var textID string
	onMain(func() {
		textID = string(a.board.ed.CreateText(geo.V(240, 220), "红按钮关窗保存"))
	})
	brx := float64(boardBounds.X) + 19
	bry := float64(boardBounds.Y) + 15
	postMouse(t, brx, bry, 5, 1, 2)
	time.Sleep(500 * time.Millisecond)
	onMainCheck(t, func() string {
		if a.board.win != nil {
			return "board window stayed open after clicking red close button"
		}
		d, err := a.board.store.Read()
		if err != nil {
			return err.Error()
		}
		if d.Shapes[doc.ID(textID)] == nil {
			return "new shape missing on disk after red close"
		}
		return ""
	})
}
