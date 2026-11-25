//go:build windows

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/fonts"
	"rakko/internal/board/geo"
	"rakko/internal/platform"
	"rakko/internal/settings"
	"rakko/internal/store"
)

type winRect struct {
	Left, Top, Right, Bottom int32
}

type winPoint struct {
	X, Y int32
}

var (
	winUser32               = syscall.NewLazyDLL("user32.dll")
	procGetWindowRect       = winUser32.NewProc("GetWindowRect")
	procGetClientRect       = winUser32.NewProc("GetClientRect")
	procClientToScreen      = winUser32.NewProc("ClientToScreen")
	procGetDpiForWindow     = winUser32.NewProc("GetDpiForWindow")
	procSetForegroundWindow = winUser32.NewProc("SetForegroundWindow")
	procSetCursorPos        = winUser32.NewProc("SetCursorPos")
	procMouseEvent          = winUser32.NewProc("mouse_event")
	procKeybdEvent          = winUser32.NewProc("keybd_event")

	winDwmAPI            = syscall.NewLazyDLL("dwmapi.dll")
	procDwmGetWindowAttr = winDwmAPI.NewProc("DwmGetWindowAttribute")
)

const (
	winMouseEventLeftDown = 0x0002
	winMouseEventLeftUp   = 0x0004
	winMouseEventWheel    = 0x0800
	winVKControl          = 0x11
	winKeyEventKeyUp      = 0x0002
	winWheelDelta         = 120
	winDWMUseDarkMode     = 20
	winDWMCornerPref      = 33
)

// winClickCaptionClose moves the real Windows cursor onto the top-right ×
// caption button (46×32 DIPs in the custom client title bar) and clicks it.
func winClickCaptionClose(t *testing.T, hwnd uintptr) {
	t.Helper()
	var cr winRect
	var origin winPoint
	r1, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&cr)))
	r2, _, _ := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&origin)))
	if r1 == 0 || r2 == 0 || cr.Right <= 46 {
		t.Fatalf("winClickCaptionClose invalid client rect r1=%d r2=%d cr=%+v", r1, r2, cr)
	}
	dpi := uint32(96)
	if procGetDpiForWindow.Find() == nil {
		if got, _, _ := procGetDpiForWindow.Call(hwnd); got > 0 {
			dpi = uint32(got)
		}
	}
	cx := origin.X + cr.Right - int32((23*dpi)/96)
	cy := origin.Y + int32((16*dpi)/96)
	procSetForegroundWindow.Call(hwnd)
	time.Sleep(100 * time.Millisecond)
	procSetCursorPos.Call(uintptr(cx), uintptr(cy))
	time.Sleep(120 * time.Millisecond)
	procMouseEvent.Call(winMouseEventLeftDown, 0, 0, 0, 0)
	time.Sleep(50 * time.Millisecond)
	procMouseEvent.Call(winMouseEventLeftUp, 0, 0, 0, 0)
	t.Logf("clicked Windows top-right × at (%d, %d) dpi=%d client=%+v origin=%+v", cx, cy, dpi, cr, origin)
}

func waitWinCondition(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fn()
}

func winShotDesktop(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", `D:\Rakko-tools\shot.ps1`, "-Path", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shot.ps1 failed for %s: %v (%s)", path, err, string(out))
	}
	t.Logf("winShot %s: %s", filepath.Base(path), strings.TrimSpace(string(out)))
}

func makeWinVerifyPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(40 + (x*180)/max(w, 1)),
				G: uint8(110 + (y*120)/max(h, 1)),
				B: 225,
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestMain(m *testing.M) {
	if os.Getenv("RAKKO_WIN_LIVE") != "1" {
		disableNoteWatch()
		os.Exit(m.Run())
	}
	mygo.App.SetName("Rakko")
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.WhenReady(func() {
		if err := platform.Init(); err != nil {
			panic(err)
		}
		fonts.Register()
		go func() {
			code := m.Run()
			mygo.RunOnMain(func() { mygo.App.Quit() })
			os.Exit(code)
		}()
	})
	if err := mygo.App.Run(); err != nil {
		panic(err)
	}
}

// TestBoardAndNoteWinLive runs inside an interactive Windows desktop session
// (RAKKO_WIN_LIVE=1) using a temp data directory, opening real DWM-framed
// whiteboard and sticky-note windows to verify:
// 1. DWM rounded dark window setup and theme switching;
// 2. Real Win32 mouse wheel panning and Ctrl+wheel zooming;
// 3. Embedded Shantell Sans + Yozai Chinese handwriting font rendering;
// 4. Whiteboard PNG + SVG export;
// 5. Sticky note rich-text + image asset insertion and disk persistence.
func TestBoardAndNoteWinLive(t *testing.T) {
	if os.Getenv("RAKKO_WIN_LIVE") != "1" {
		t.Skip("set RAKKO_WIN_LIVE=1 on interactive Windows desktop")
	}
	shotDir := os.Getenv("RAKKO_WIN_SHOT_DIR")
	if shotDir == "" {
		shotDir = filepath.Join(os.TempDir(), "rakko-win-shots")
	}

	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := &shelf{
		store:        st,
		dataDir:      dataDir,
		settings:     settings.Default(),
		stop:         make(chan struct{}),
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}

	runOnMainSync := func(fn func()) {
		done := make(chan struct{})
		mygo.RunOnMain(func() {
			fn()
			close(done)
		})
		<-done
	}

	// 1. Open real whiteboard window & inspect DWM attributes.
	var b *boardUI
	var hwnd uintptr
	runOnMainSync(func() {
		b = a.boardSession()
		if b == nil {
			return
		}
		b.ed.Next.Font = "draw"
		b.ed.Next.FontSize = 24
		b.ed.Next.Stroke = editor.Swatches[3]
		b.ed.CreateText(geo.V(120, 120), "手写中英混排 Shantell + 悠哉楷体\nWindows DWM 实时渲染")

		b.ed.SetGeoTool("rounded-rect")
		b.ed.Next.Fill = "semi"
		b.ed.Next.Stroke = editor.Swatches[5]
		b.ed.PointerDown(120, 260, 0, 1, editor.Mods{}, 0.1)
		b.ed.PointerMove(360, 400, editor.Mods{}, 0.2)
		b.ed.PointerUp(360, 400, editor.Mods{}, 0.3)

		b.ed.SetGeoTool("star")
		b.ed.Next.Stroke = editor.Swatches[7]
		b.ed.PointerDown(460, 260, 0, 1, editor.Mods{}, 0.4)
		b.ed.PointerMove(620, 410, editor.Mods{}, 0.5)
		b.ed.PointerUp(620, 410, editor.Mods{}, 0.6)

		b.ed.SetTool(editor.ToolArrow)
		b.ed.Next.Stroke = editor.Swatches[1]
		b.ed.PointerDown(360, 330, 0, 1, editor.Mods{}, 0.7)
		b.ed.PointerMove(460, 330, editor.Mods{}, 0.8)
		b.ed.PointerUp(460, 330, editor.Mods{}, 0.9)
		b.ed.SetTool(editor.ToolSelect)
		b.ed.SelectNone()

		b.open()
		if b.win != nil {
			hwnd = b.win.NativeHandle()
		}
	})
	if b == nil || b.win == nil || hwnd == 0 {
		t.Fatalf("failed to open Windows board window (b=%v)", b)
	}
	time.Sleep(600 * time.Millisecond)

	var darkAttr, cornerAttr int32
	r1, _, _ := procDwmGetWindowAttr.Call(hwnd, winDWMUseDarkMode, uintptr(unsafe.Pointer(&darkAttr)), 4)
	r2, _, _ := procDwmGetWindowAttr.Call(hwnd, winDWMCornerPref, uintptr(unsafe.Pointer(&cornerAttr)), 4)
	t.Logf("DWM attrs: dark=(rc=%d val=%d) corner=(rc=%d val=%d)", r1, darkAttr, r2, cornerAttr)
	if r2 == 0 && cornerAttr != 2 {
		t.Errorf("DWM corner preference = %d, want 2 (DWMWCP_ROUND)", cornerAttr)
	}

	winShotDesktop(t, filepath.Join(shotDir, "win-01-board-zh-handwriting.png"))

	// 2. Real Win32 Mouse Wheel pan & Ctrl+Wheel zoom on canvas center.
	var wr winRect
	procSetForegroundWindow.Call(hwnd)
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
	cx := (wr.Left + wr.Right) / 2
	cy := (wr.Top + wr.Bottom) / 2
	procSetCursorPos.Call(uintptr(cx), uintptr(cy))
	procMouseEvent.Call(winMouseEventLeftDown, 0, 0, 0, 0)
	procMouseEvent.Call(winMouseEventLeftUp, 0, 0, 0, 0)
	time.Sleep(150 * time.Millisecond)

	var camBefore, camAfterPan, camAfterZoom editor.Camera
	runOnMainSync(func() { camBefore = b.ed.Cam() })

	// Scroll wheel down (pan)
	wheelDown := int32(-2 * winWheelDelta)
	procMouseEvent.Call(winMouseEventWheel, 0, 0, uintptr(uint32(wheelDown)), 0)
	time.Sleep(300 * time.Millisecond)
	runOnMainSync(func() { camAfterPan = b.ed.Cam() })
	t.Logf("Wheel pan: before=(%.2f, %.2f, z=%.3f) -> after=(%.2f, %.2f, z=%.3f)",
		camBefore.X, camBefore.Y, camBefore.Z, camAfterPan.X, camAfterPan.Y, camAfterPan.Z)
	if camAfterPan.Y == camBefore.Y && camAfterPan.X == camBefore.X {
		t.Errorf("real Windows mouse wheel did not pan canvas")
	}

	// Ctrl + Scroll wheel up (zoom)
	wheelUp := int32(3 * winWheelDelta)
	procKeybdEvent.Call(winVKControl, 0, 0, 0)
	procMouseEvent.Call(winMouseEventWheel, 0, 0, uintptr(uint32(wheelUp)), 0)
	procKeybdEvent.Call(winVKControl, 0, winKeyEventKeyUp, 0)
	time.Sleep(300 * time.Millisecond)
	runOnMainSync(func() { camAfterZoom = b.ed.Cam() })
	t.Logf("Ctrl+Wheel zoom: before z=%.3f -> after z=%.3f", camAfterPan.Z, camAfterZoom.Z)
	if camAfterZoom.Z == camAfterPan.Z {
		t.Errorf("real Windows Ctrl+Wheel did not zoom canvas")
	}

	winShotDesktop(t, filepath.Join(shotDir, "win-02-board-ctrl-zoom.png"))

	// 3. Maximize / Restore layout & Light/Dark window buttons check, then export PNG & SVG on Windows.
	var beforeSize, maxSize, restoredSize mygo.Rectangle
	var vwBefore, vwMax float64
	runOnMainSync(func() {
		beforeSize = b.win.Bounds()
		vwBefore = b.ed.VW
		b.setDarkMode(false)
		b.win.Maximize()
	})
	time.Sleep(550 * time.Millisecond)
	runOnMainSync(func() {
		maxSize = b.win.Bounds()
		vwMax = b.ed.VW
	})
	t.Logf("Windows board maximize: before=%+v (VW=%.0f) -> max=%+v (VW=%.0f)", beforeSize, vwBefore, maxSize, vwMax)
	if maxSize.Width <= beforeSize.Width || vwMax <= vwBefore {
		t.Errorf("Windows board maximize did not grow window/viewport")
	}
	winShotDesktop(t, filepath.Join(shotDir, "win-04-board-maximized-light.png"))

	runOnMainSync(func() {
		b.setDarkMode(true)
		b.win.Unmaximize()
	})
	time.Sleep(550 * time.Millisecond)
	runOnMainSync(func() {
		restoredSize = b.win.Bounds()
	})
	t.Logf("Windows board restored: %+v", restoredSize)
	if restoredSize.Width >= maxSize.Width {
		t.Errorf("Windows board unmaximize did not restore width: %d >= %d", restoredSize.Width, maxSize.Width)
	}
	winShotDesktop(t, filepath.Join(shotDir, "win-05-board-restored-dark.png"))

	var pngBytes []byte
	var expW, expH, tiles int
	var svgOut string
	var expErr error
	const wantBoardCloseText = "Win 关闭保存验证 TEXT"
	var boardBeforeCloseHasText bool
	runOnMainSync(func() {
		pngBytes, expW, expH, tiles, expErr = b.renderTiledPNG(nil)
		if expErr == nil {
			svgOut, expErr = b.ed.ExportSVG(nil, b.ed.Palette(), nil)
		}
		// Leave a dirty edit unsaved right before clicking the real × button.
		b.ed.CreateText(geo.V(140, 460), wantBoardCloseText)
		b.dirtyFlag = true
		b.invalidate()
		if diskDoc, rerr := b.store.Read(); rerr == nil && diskDoc != nil {
			for _, s := range diskDoc.Shapes {
				if s.Text == wantBoardCloseText {
					boardBeforeCloseHasText = true
				}
			}
		}
	})
	if expErr != nil {
		t.Errorf("Windows export failed: %v", expErr)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(pngBytes))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		t.Errorf("invalid exported PNG on Windows: %v (%dx%d)", err, expW, expH)
	}
	if !strings.Contains(svgOut, "<svg") || !strings.Contains(svgOut, "Shantell") {
		t.Errorf("invalid exported SVG on Windows (len=%d)", len(svgOut))
	}
	t.Logf("Windows export verified: PNG=%dx%d (%d tiles, %d bytes), SVG=%d bytes", cfg.Width, cfg.Height, tiles, len(pngBytes), len(svgOut))
	if boardBeforeCloseHasText {
		t.Fatalf("board disk already contained close verification text before × click")
	}

	// Close the whiteboard via real Win32 mouse click on top-right × button, then read disk.
	winClickCaptionClose(t, hwnd)
	boardClosed := waitWinCondition(3*time.Second, func() bool {
		var nilWin bool
		runOnMainSync(func() { nilWin = b.win == nil })
		return nilWin
	})
	if !boardClosed {
		t.Fatalf("whiteboard window did not close after real × click")
	}
	diskDoc, err := b.store.Read()
	if err != nil {
		t.Fatalf("board store.Read() after × close failed: %v", err)
	}
	var boardAfterCloseHasText bool
	for _, s := range diskDoc.Shapes {
		if s.Text == wantBoardCloseText {
			boardAfterCloseHasText = true
			break
		}
	}
	if !boardAfterCloseHasText {
		t.Errorf("board disk after × close did not persist %q (shapes=%d)", wantBoardCloseText, len(diskDoc.Shapes))
	}
	t.Logf("Windows board × close disk verified: found %q across %d shapes", wantBoardCloseText, len(diskDoc.Shapes))

	// 4. Open real Sticky Note window, edit plain text, close via real × click & read disk.
	const wantNote = "# Windows 便利贴实测\n– 第一行\n– 第二行\n"
	const wantNoteAfterClose = "# Windows 便利贴实测\n– 第一行\n– 第二行\n– 标题栏×关闭落盘验证\n"
	var gotNote string
	var noteHWND uintptr
	runOnMainSync(func() {
		n := a.noteSession()
		n.draft = wantNote
		_ = n.flush()
		gotNote = n.text
		n.open()
		if n.win != nil {
			noteHWND = n.win.NativeHandle()
		}
	})
	if gotNote != wantNote {
		t.Errorf("Windows note saved text = %q, want %q", gotNote, wantNote)
	}
	if noteHWND == 0 {
		t.Errorf("failed to open Windows note window")
	}
	time.Sleep(550 * time.Millisecond)
	winShotDesktop(t, filepath.Join(shotDir, "win-03-note.png"))

	// Make a dirty edit on the open note, verify disk has old text, then click real ×.
	var noteDirty bool
	runOnMainSync(func() {
		n := a.noteSession()
		n.draft = wantNoteAfterClose
		noteDirty = n.dirty()
		n.invalidate()
	})
	if !noteDirty {
		t.Fatalf("note did not become dirty after modifying draft")
	}
	diskBeforeNoteClose, err := a.note.store.Read()
	if err != nil {
		t.Fatalf("note store.Read() before × close failed: %v", err)
	}
	if diskBeforeNoteClose != wantNote {
		t.Fatalf("note disk before × close = %q, want %q", diskBeforeNoteClose, wantNote)
	}
	winClickCaptionClose(t, noteHWND)
	noteClosed := waitWinCondition(3*time.Second, func() bool {
		var nilWin bool
		runOnMainSync(func() { nilWin = a.note == nil || a.note.win == nil })
		return nilWin
	})
	if !noteClosed {
		t.Fatalf("sticky note window did not close after real × click")
	}
	diskAfterNoteClose, err := a.note.store.Read()
	if err != nil {
		t.Fatalf("note store.Read() after × close failed: %v", err)
	}
	if diskAfterNoteClose != wantNoteAfterClose {
		t.Errorf("note disk after × close = %q, want %q", diskAfterNoteClose, wantNoteAfterClose)
	}
	t.Logf("Windows note × close disk verified (%d bytes)", len(diskAfterNoteClose))

	// 5. Open real shelf window on Tools page on Windows to inspect updated nav icons & tools cards.
	var panelOpened bool
	runOnMainSync(func() {
		a.newNotchWindow()
		if a.win == nil {
			return
		}
		work := mygo.Screen.PrimaryDisplay().WorkArea
		a.place(mygo.Point{X: work.X + work.Width/2, Y: work.Y + work.Height/2})
		a.peeking = true
		a.peekProgress, a.peekFrom = 1, 1
		a.show(false)
		a.setLevel(2)
		a.setTab(pageTools)
		a.jumpLevel = true
		a.progress, a.animationFrom = 1, 1
		a.animationStart = time.Time{}
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
		panelOpened = a.win != nil && a.visible && a.tab == pageTools
	})
	if !panelOpened {
		t.Errorf("failed to open Windows panel on Tools page")
	}
	for _, w := range a.toolsLayout().Home {
		if w.ID == "recent" || w.ID == "clip" {
			t.Errorf("retired card %q unexpectedly present on Windows tools page", w.ID)
		}
	}
	time.Sleep(550 * time.Millisecond)
	winShotDesktop(t, filepath.Join(shotDir, "win-06-tools-nav.png"))
	runOnMainSync(func() {
		a.hide(hideExplicit)
		if a.win != nil {
			a.win.Close()
			a.win = nil
		}
	})
	time.Sleep(200 * time.Millisecond)
	_ = doc.TypeGeo
	_ = runtime.GOOS
}
