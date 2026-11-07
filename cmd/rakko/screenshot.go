//go:build darwin

package main

// The screenshot overlay (⌃⌥A): every display is frozen into a static
// picture, one borderless window per display shows it, and the whole
// capture — selection, annotations, crop — happens on that picture. The
// system owns no part of this flow: the windows are the app's own, so the
// freeze must be taken before they open (screencapture runs first) and
// the panel is folded away first.
//
// The session is single-owner and main-thread only after the freeze:
// pointer and key events mutate it, a frame draws it, and every output
// action (copy, save, recognize, pin) leaves through a seam so headless
// tests can exercise the same path.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	stdpng "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
)

// Seams: the display list, the cursor, the capture command, the window
// list, the clipboard, the save directory and the clock. Production uses
// the real ones; headless tests replace them and never touch a window
// server, a pasteboard or the user's Desktop.
var (
	shotDisplays       = func() []mygo.Display { return mygo.Screen.Displays() }
	shotCursorPoint    = func() mygo.Point { return mygo.Screen.CursorScreenPoint() }
	shotCaptureDisplay = captureShotDisplayFile
	shotNormalWindow   = mac.NormalWindows
	shotDispatch       = mygo.RunOnMain
	shotWork           = func(fn func()) { go fn() }
	shotClock          = time.Now
	shotFrontPID       = mac.FrontmostPID
	shotActivateSelf   = mac.ActivateSelf
	shotActivateApp    = mac.ActivateApp
	shotPreflight      = mac.ScreenCapturePreflight
	shotClipboard      = writeShotClipboard
	shotReadSaveDir    = readScreenCaptureLocation
	shotWriteFile      = os.WriteFile
	shotAfter          = time.AfterFunc
	shotOpenWindow     = func(s *shotSession, di int) { s.openWindow(di) }
	shotCleanTemp      = cleanShotTempDir
	shotPanelSettle    = durClose + 80*time.Millisecond
)

// shotDim is the veil outside the selection: black at 45%, which reads as
// a shadow on any wallpaper without hiding what is being chosen.
var shotDim = ui.RGBA(0, 0, 0, 0.45)

// The toolbar's look: pure black glass with a hairline, as the app's other
// floating surfaces.
var (
	shotBarFill = ui.RGBA(20, 20, 22, 0.97)
	shotBarEdge = ui.RGBA(255, 255, 255, 0.16)
	shotBarHot  = ui.RGBA(255, 255, 255, 0.10)
)

// shotMode is the pointer gesture in flight.
type shotMode int

const (
	shotModeIdle shotMode = iota
	shotModeSelect
	shotModeMove
	shotModeResize
	shotModeDraw
)

// shotDisplay is one frozen screen: its bitmap for drawing, its pixels for
// the crop, and the overlay window that shows it.
type shotDisplay struct {
	shotTarget
	img    *image.RGBA
	pixW   int
	pixH   int
	bitmap *ui.Bitmap
	win    *mygo.Window
}

// shotCapture is one display's freeze result, ready to be assigned.
type shotCapture struct {
	index  int
	target shotTarget
	img    *image.RGBA
	err    error
}

// shotTextEdit is the text tool's live line.
type shotTextEdit struct {
	at      mygo.Point
	text    string
	compose string
}

// shotSession is one screenshot from the hotkey to the output.
type shotSession struct {
	a         *shelf
	displays  []*shotDisplay
	owner     int // display that owns the selection, -1 for none
	hasSel    bool
	sel       mygo.Rectangle
	dragging  bool
	mode      shotMode
	press     mygo.Point
	pressWin  mygo.Rectangle
	pressHit  bool
	moveFrom  mygo.Rectangle
	resizeH   shotHandle
	tool      shotTool
	color     ui.Color
	width     int
	hist      shotHistory
	pre       pre
	text      *shotTextEdit
	nextNum   int
	bar       shotToolbar
	cursor    mygo.Point
	cursorDpy int
	hover     mygo.Rectangle
	hoverOK   bool
	windows   []mac.ScreenWindow
	windowsAt time.Time
	prevPID   int
	captureMS int64
	installed bool
	closed    bool
}

// captureShotDisplayFile freezes one display with the system tool. -D
// picks the display (1-based); -x keeps the shutter sound off. The system
// excludes the pointer itself.
func captureShotDisplayFile(index int, path string) error {
	return exec.Command("/usr/sbin/screencapture", "-x", "-D", strconv.Itoa(index), path).Run()
}

// startScreenshot is the one entry for the ⌃⌥A shortcut, the tray menu
// and the tools page's card. A second press while a session runs cancels
// it; while the permission card waits, a press does nothing.
func (a *shelf) startScreenshot() {
	shotDispatch(func() { a.startScreenshotOnMain() })
}

func (a *shelf) startScreenshotOnMain() {
	if a.shot != nil {
		a.shot.close(true)
		return
	}
	if a.ocrSheet {
		return
	}
	if !shotPreflight() {
		a.ocrSheet = true
		a.ocrSheetShot = true
		if !a.visible {
			a.show(false)
		} else if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	displays := shotDisplays()
	if len(displays) == 0 {
		return
	}
	shotCleanTemp()
	targets := make([]shotTarget, 0, len(displays))
	for _, d := range displays {
		targets = append(targets, shotTarget{ID: d.ID, Bounds: d.Bounds, Scale: d.ScaleFactor})
	}
	s := &shotSession{
		a: a, owner: -1, cursorDpy: -1,
		color: shotAnnotationColors[0], width: shotAnnotationWidths[1],
		nextNum: 1,
	}
	s.prevPID = shotFrontPID()
	s.cursor = shotCursorPoint()
	a.shot = s
	wasVisible := a.visible
	if wasVisible {
		a.hide(hideExplicit)
	}
	shotWork(func() {
		if wasVisible {
			// Let the panel's 220 ms fold finish before the freeze: the
			// closing shell is still drawn in the window and must not be
			// part of the picture. A press while this waits cancels the
			// session before it captures anything.
			time.Sleep(shotPanelSettle)
		}
		s.freeze(targets)
	})
}

// freeze captures every display in parallel off the UI thread, decodes the
// PNGs and hands the pictures back to the main thread. The files are
// removed at once; the session keeps only pixels.
func (s *shotSession) freeze(targets []shotTarget) {
	start := time.Now()
	results := make([]shotCapture, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t shotTarget) {
			defer wg.Done()
			path := filepath.Join(shotTempDir(), fmt.Sprintf("freeze-%d-%d.png", os.Getpid(), i))
			err := shotCaptureDisplay(i+1, path)
			var img *image.RGBA
			if err == nil {
				img, err = decodeShotPNG(path)
			}
			os.Remove(path)
			results[i] = shotCapture{index: i, target: t, img: img, err: err}
		}(i, t)
	}
	wg.Wait()
	elapsed := time.Since(start).Milliseconds()
	shotDispatch(func() { s.install(results, elapsed) })
}

// decodeShotPNG reads a captured PNG into an RGBA picture: the crop and
// the mosaic need pixels, and the overlay draws the same pixels.
func decodeShotPNG(path string) (*image.RGBA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, err := stdpng.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if r, ok := img.(*image.RGBA); ok {
		return r, nil
	}
	r := image.NewRGBA(img.Bounds())
	draw.Draw(r, r.Bounds(), img, img.Bounds().Min, draw.Src)
	return r, nil
}

// install turns the captured pictures into displays and opens their
// overlays. A capture with no usable picture ends the session quietly: the
// usual cause is a refused capture, which the preflight already screened.
func (s *shotSession) install(results []shotCapture, elapsedMS int64) {
	if s.closed || s.a.shot != s {
		return
	}
	s.captureMS = elapsedMS
	s.displays = assignShotDisplays(results)
	if len(s.displays) == 0 {
		s.close(true)
		s.a.report(errors.New(s.a.appLoc().ShotFailed()))
		return
	}
	for i, d := range s.displays {
		d.pixW, d.pixH = d.img.Bounds().Dx(), d.img.Bounds().Dy()
		d.bitmap = ui.NewBitmap(d.img).Pixelated()
		shotOpenWindow(s, i)
	}
	active := 0
	for i, d := range s.displays {
		if shotPointInRect(s.cursor, d.Bounds) {
			active = i
		}
	}
	s.cursorDpy = active
	s.installed = true
	s.refreshWindows()
	shotActivateSelf()
	if w := s.displays[active].win; w != nil {
		w.Focus()
	}
	s.invalidate()
}

// assignShotDisplays matches captured pictures to displays by pixel size,
// so a WindowServer order that differs from -D's cannot put one screen's
// picture in another screen's window. The enumeration index is the tie
// breaker, and a size that matches nothing still falls back to the index.
func assignShotDisplays(results []shotCapture) []*shotDisplay {
	used := make([]bool, len(results))
	var out []*shotDisplay
	for i := range results {
		t := results[i].target
		pick := -1
		// Same index and plausible size.
		if !used[i] && results[i].img != nil && results[i].err == nil && shotSizeMatches(results[i].img, t) {
			pick = i
		}
		if pick < 0 {
			for j := range results {
				if used[j] || results[j].img == nil || results[j].err != nil {
					continue
				}
				if shotSizeMatches(results[j].img, t) {
					pick = j
					break
				}
			}
		}
		if pick < 0 && !used[i] && results[i].img != nil && results[i].err == nil {
			// The sizes disagree by more than rounding: trust the index.
			pick = i
		}
		if pick < 0 {
			continue
		}
		used[pick] = true
		img := results[pick].img
		out = append(out, &shotDisplay{shotTarget: t, img: img, pixW: img.Bounds().Dx(), pixH: img.Bounds().Dy()})
	}
	return out
}

// shotSizeMatches reports whether img has the target's expected pixel size,
// within the couple of pixels a rounded backing scale can differ by.
func shotSizeMatches(img *image.RGBA, t shotTarget) bool {
	w, h := t.expectedPixels()
	return abs(img.Bounds().Dx()-w) <= 2 && abs(img.Bounds().Dy()-h) <= 2
}

// openWindow creates one display's overlay: full display, above the menu
// bar and the Dock, transparent content that draws the frozen picture.
func (s *shotSession) openWindow(di int) {
	d := s.displays[di]
	b := d.Bounds
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: s.a.appLoc().ShotPinTitle(), Width: b.Width, Height: b.Height,
		X: b.X, Y: b.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(func(c *ui.Context) { s.overlayView(c, di) }),
	})
	if w == nil {
		return
	}
	w.SetVisibleOnAllWorkspaces(true)
	h := w.NativeHandle()
	mac.ConfigureCaptureWindow(h)
	mac.SetWindowFrame(h, b.X, b.Y, b.Width, b.Height)
	w.ShowInactive()
	d.win = w
}

// overlayView draws one display. The whole overlay is a single element:
// it holds the keyboard, takes every pointer gesture and paints the frozen
// picture, the selection, the annotations and the toolbar in one pass.
func (s *shotSession) overlayView(c *ui.Context, di int) {
	c.Root().Background(ui.Transparent)
	// ⌘C / ⌘S and undo also arrive as window shortcuts on macOS: the Edit
	// menu can turn ⌘C into an InputCommand the focused element never sees.
	c.OnShortcut(ui.Cmd, ui.KeyC, func() {
		if !s.closed && s.hasSel {
			s.copy()
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyS, func() {
		if !s.closed && s.hasSel {
			s.save()
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyZ, func() {
		if !s.closed && s.text == nil && s.hist.undoLast() {
			s.invalidate()
		}
	})
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyZ, func() {
		if !s.closed && s.text == nil && s.hist.redoLast() {
			s.invalidate()
		}
	})
	cursor := s.cursorShape(di)
	ui.Box(c.Key("shot")).Fill().Focusable().AutoFocus().Cursor(cursor).
		HandleInput(func(ev ui.InputEvent) bool { return s.input(ev, di) }).
		Draw(func(p *ui.Painter, r ui.Rect) { s.draw(p, r, di) })
}

// cursorShape is the pointer over this display.
func (s *shotSession) cursorShape(di int) ui.Cursor {
	if s.closed || di != s.owner || !s.hasSel {
		return ui.CursorCrosshair
	}
	return shotCursorFor(s.sel, s.cursor, s.hasSel, s.tool)
}

// invalidate repaints every overlay.
func (s *shotSession) invalidate() {
	for _, d := range s.displays {
		if d.win != nil {
			d.win.Invalidate()
		}
	}
}

// close tears the overlays down and clears the session. restore hands the
// keyboard back to the app the user came from.
func (s *shotSession) close(restore bool) {
	if s.closed {
		return
	}
	s.closed = true
	for _, d := range s.displays {
		if d.win != nil {
			d.win.Destroy()
			d.win = nil
		}
	}
	if s.a.shot == s {
		s.a.shot = nil
	}
	if restore && s.prevPID > 0 {
		s.a.handBackApp(s.prevPID)
	}
}

// ownerDisplay returns the display that owns the selection.
func (s *shotSession) ownerDisplay() *shotDisplay {
	if s.owner < 0 || s.owner >= len(s.displays) {
		return nil
	}
	return s.displays[s.owner]
}

// refreshWindows caches the ordinary window list for a moment: hit-testing
// every pointer move would otherwise ask WindowServer dozens of times a
// second.
func (s *shotSession) refreshWindows() {
	if s.windows != nil && shotClock().Sub(s.windowsAt) < 150*time.Millisecond {
		return
	}
	s.windows = shotNormalWindow()
	s.windowsAt = shotClock()
}

// displayAt returns the index of the display containing p, or -1.
func (s *shotSession) displayAt(p mygo.Point) int {
	for i, d := range s.displays {
		if shotPointInRect(p, d.Bounds) {
			return i
		}
	}
	return -1
}

// input is the overlay's one event handler. p is in global points; the
// display is where the event arrived.
func (s *shotSession) input(ev ui.InputEvent, di int) bool {
	if s.closed || di < 0 || di >= len(s.displays) {
		return false
	}
	d := s.displays[di]
	p := mygo.Point{X: d.Bounds.X + int(ev.X+0.5), Y: d.Bounds.Y + int(ev.Y+0.5)}
	switch ev.Kind {
	case ui.InputKeyDown:
		return s.keyDown(ev)
	case ui.InputText:
		if s.text != nil {
			s.text.text += ev.Text
			s.text.compose = ""
			s.layoutBar()
			s.invalidate()
			return true
		}
	case ui.InputCompose:
		if s.text != nil {
			s.text.compose = ev.Text
			s.layoutBar()
			s.invalidate()
			return true
		}
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		s.cursor, s.cursorDpy = p, di
		return s.pointerDown(p, ev, di)
	case ui.InputPointerMove:
		s.cursor, s.cursorDpy = p, di
		return s.pointerMove(p, ev, di)
	case ui.InputPointerUp:
		if ev.Button != 0 {
			return false
		}
		s.cursor, s.cursorDpy = p, di
		return s.pointerUp(p, ev, di)
	}
	return false
}

// pointerDown starts a selection, a move, a resize or a mark.
func (s *shotSession) pointerDown(p mygo.Point, ev ui.InputEvent, di int) bool {
	if s.text != nil {
		// A click elsewhere commits the line first; a click in the text
		// area itself then starts the next line.
		s.commitText()
	}
	if s.hasSel {
		if di == s.owner {
			s.layoutBar()
			if it, ok := s.bar.shotBarAt(p); ok {
				s.actBar(it)
				return true
			}
			if shotPointInRect(p, s.sel) && ev.Clicks >= 2 {
				s.copy()
				return true
			}
			if h := shotHandleAt(s.sel, p, 6); h != shotHandleNone {
				s.mode, s.resizeH, s.press = shotModeResize, h, p
				return true
			}
			if shotPointInRect(p, s.sel) {
				if s.tool != shotToolNone {
					s.beginMark(p)
				} else {
					s.mode, s.moveFrom, s.press = shotModeMove, s.sel, p
				}
				return true
			}
		}
		s.hasSel = false
		s.bar = shotToolbar{}
	}
	s.owner = di
	s.mode = shotModeSelect
	s.dragging = false
	s.press = p
	s.refreshWindows()
	s.pressWin, s.pressHit = shotWindowAt(s.windows, p, s.displays[di].shotTarget)
	s.invalidate()
	return true
}

// pointerMove drags the selection, resizes it, draws the live mark, or
// just moves the magnifier and the window highlight.
func (s *shotSession) pointerMove(p mygo.Point, ev ui.InputEvent, di int) bool {
	d := s.displays[di]
	switch s.mode {
	case shotModeSelect:
		if !s.dragging && abs(p.X-s.press.X)+abs(p.Y-s.press.Y) >= 3 {
			s.dragging = true
		}
		if s.dragging {
			s.hasSel = true
			if ev.Mods&ui.Shift != 0 {
				s.sel = shotSquareRect(s.press, p)
			} else {
				s.sel = shotNormalizeRect(s.press, p)
			}
			s.sel = shotClampRect(s.sel, d.Bounds)
			s.owner = di
			s.layoutBar()
		}
	case shotModeMove:
		dx, dy := p.X-s.press.X, p.Y-s.press.Y
		s.sel = shotClampRect(mygo.Rectangle{
			X: s.moveFrom.X + dx, Y: s.moveFrom.Y + dy,
			Width: s.moveFrom.Width, Height: s.moveFrom.Height,
		}, d.Bounds)
		s.layoutBar()
	case shotModeResize:
		s.sel = shotResize(s.sel, s.resizeH, p, d.Bounds)
		s.layoutBar()
	case shotModeDraw:
		if s.tool == shotToolPen && s.pre.on && ev.Mods&ui.Shift == 0 {
			s.pre.ann.points = append(s.pre.ann.points, p)
		} else {
			s.pre.ann = shotDragAnnotation(s.tool, s.color, s.width, s.press, p, ev.Mods&ui.Shift != 0)
			s.pre.on = true
		}
	default:
		if !s.hasSel {
			s.refreshWindows()
			s.hover, s.hoverOK = shotWindowAt(s.windows, p, d.shotTarget)
		}
	}
	s.invalidate()
	return true
}

// pointerUp settles the gesture: a click picks the window (or the whole
// display), a drag becomes the selection, and a mark joins the history.
func (s *shotSession) pointerUp(p mygo.Point, ev ui.InputEvent, di int) bool {
	d := s.displays[di]
	switch s.mode {
	case shotModeSelect:
		if !s.dragging {
			if s.pressHit {
				s.sel = s.pressWin
			} else {
				s.sel = d.Bounds
			}
			s.hasSel = true
			s.owner = di
		}
		s.dragging = false
		s.mode = shotModeIdle
		s.layoutBar()
	case shotModeMove, shotModeResize:
		s.mode = shotModeIdle
		s.layoutBar()
	case shotModeDraw:
		s.mode = shotModeIdle
		s.finishMark()
	}
	s.invalidate()
	return true
}

// beginMark starts a live mark with the current tool. Text and numbers are
// placed on the press itself; the other tools drag.
func (s *shotSession) beginMark(p mygo.Point) {
	switch s.tool {
	case shotToolText:
		s.text = &shotTextEdit{at: p}
		s.pre = pre{}
		s.invalidate()
		return
	case shotToolNumber:
		s.hist.snap()
		s.hist.push(shotAnnotation{tool: shotToolNumber, color: s.color, width: s.width, a: p, number: s.nextNum})
		s.nextNum++
		s.invalidate()
		return
	}
	s.mode = shotModeDraw
	s.press = p
	s.pre = pre{ann: shotDragAnnotation(s.tool, s.color, s.width, p, p, false), on: true}
}

// finishMark commits the live mark, if it is big enough to be deliberate.
func (s *shotSession) finishMark() {
	an := s.pre.ann
	on := s.pre.on
	s.pre = pre{}
	if !on {
		return
	}
	switch an.tool {
	case shotToolRect, shotToolArrow, shotToolMosaic:
		if abs(an.b.X-an.a.X) < 2 || abs(an.b.Y-an.a.Y) < 2 {
			return
		}
	case shotToolPen:
		if len(an.points) < 2 {
			return
		}
	}
	if an.tool == shotToolMosaic {
		if d := s.ownerDisplay(); d != nil {
			an.mosaic = s.mosaicBitmap(d, an)
		}
	}
	s.hist.snap()
	s.hist.push(an)
}

// mosaicBitmap turns the source pixels under an's rectangle into the small
// grid the mosaic draws. The block size is in points, so the grid looks the
// same at any backing scale.
func (s *shotSession) mosaicBitmap(d *shotDisplay, an shotAnnotation) *ui.Bitmap {
	crop := shotCropRect(shotNormalizeRect(an.a, an.b), d.shotTarget, d.pixW, d.pixH)
	if crop.Empty() {
		return nil
	}
	block := int(math.Round(float64(shotMosaicBlock) * d.scale()))
	if block < 1 {
		block = 1
	}
	return ui.NewBitmap(shotPixelate(d.img, crop, block)).Pixelated()
}

// layoutBar places the toolbar for the current selection.
func (s *shotSession) layoutBar() {
	if !s.hasSel {
		s.bar = shotToolbar{}
		return
	}
	if d := s.ownerDisplay(); d != nil {
		s.bar = shotToolbarLayout(s.sel, d.Bounds)
	}
}

// actBar runs one toolbar control.
func (s *shotSession) actBar(it shotBarItem) {
	switch it.kind {
	case shotBarTool:
		if s.tool == it.tool {
			s.tool = shotToolNone
		} else {
			s.tool = it.tool
		}
	case shotBarColor:
		s.color = shotAnnotationColors[it.index]
	case shotBarWidth:
		s.width = shotAnnotationWidths[it.index]
	case shotBarUndo:
		if s.hist.undoLast() {
			s.invalidate()
		}
	case shotBarRedo:
		if s.hist.redoLast() {
			s.invalidate()
		}
	case shotBarOCR:
		s.recognize()
	case shotBarPin:
		s.pin()
	case shotBarSave:
		s.save()
	case shotBarCancel:
		s.close(true)
	case shotBarCopy:
		s.copy()
	}
}

// keyDown is the session's one key handler. Text entry takes precedence:
// while a line is being typed, ordinary keys never trigger the toolbar
// shortcuts.
func (s *shotSession) keyDown(ev ui.InputEvent) bool {
	if s.text != nil {
		switch ev.Key {
		case ui.KeyEscape, ui.KeyEnter:
			s.commitText()
			return true
		case ui.KeyBackspace, ui.KeyDelete:
			s.backspaceText()
			return true
		}
		if ev.Mods&ui.Cmd != 0 {
			return false
		}
		return true
	}
	if ev.Key == ui.KeyEscape {
		s.escape()
		return true
	}
	if !s.hasSel {
		if ev.Key == ui.KeyEnter {
			return true
		}
		if ev.Mods&ui.Cmd == 0 {
			return false
		}
	}
	if ev.Key == ui.KeyEnter {
		if s.hasSel {
			s.copy()
		}
		return true
	}
	if ev.Mods&ui.Cmd != 0 {
		switch ev.Key {
		case ui.KeyC:
			if s.hasSel {
				s.copy()
			}
			return true
		case ui.KeyS:
			if s.hasSel {
				s.save()
			}
			return true
		case ui.KeyZ:
			if ev.Mods&ui.Shift != 0 {
				if s.hist.redoLast() {
					s.invalidate()
				}
			} else if s.hist.undoLast() {
				s.invalidate()
			}
			return true
		}
	}
	if !s.hasSel {
		return false
	}
	step := 1
	if ev.Mods&ui.Shift != 0 {
		step = 10
	}
	switch ev.Key {
	case ui.KeyLeft:
		s.nudge(-step, 0)
		return true
	case ui.KeyRight:
		s.nudge(step, 0)
		return true
	case ui.KeyUp:
		s.nudge(0, -step)
		return true
	case ui.KeyDown:
		s.nudge(0, step)
		return true
	}
	return false
}

// nudge moves the selection with the arrow keys.
func (s *shotSession) nudge(dx, dy int) {
	if d := s.ownerDisplay(); d != nil {
		s.sel = shotClampRect(mygo.Rectangle{
			X: s.sel.X + dx, Y: s.sel.Y + dy,
			Width: s.sel.Width, Height: s.sel.Height,
		}, d.Bounds)
		s.layoutBar()
		s.invalidate()
	}
}

// escape is the one ladder: finish the line, drop the tool, drop the
// selection, leave.
func (s *shotSession) escape() {
	switch {
	case s.text != nil:
		s.commitText()
	case s.tool != shotToolNone:
		s.tool = shotToolNone
		s.invalidate()
	case s.hasSel:
		s.hasSel = false
		s.bar = shotToolbar{}
		s.pre = pre{}
		s.invalidate()
	default:
		s.close(true)
	}
}

// commitText adds the typed line to the history and leaves text mode.
func (s *shotSession) commitText() {
	te := s.text
	s.text = nil
	if te == nil {
		return
	}
	if strings.TrimSpace(te.text) != "" {
		s.hist.snap()
		s.hist.push(shotAnnotation{tool: shotToolText, color: s.color, width: s.width, a: te.at, text: te.text})
	}
	s.layoutBar()
	s.invalidate()
}

// backspaceText removes the last rune of the composition, then of the line.
func (s *shotSession) backspaceText() {
	if s.text == nil {
		return
	}
	if s.text.compose != "" {
		r := []rune(s.text.compose)
		s.text.compose = string(r[:len(r)-1])
	} else {
		r := []rune(s.text.text)
		if len(r) > 0 {
			s.text.text = string(r[:len(r)-1])
		}
	}
	s.layoutBar()
	s.invalidate()
}

// draw paints one display: the frozen picture, the veil, the selection,
// the marks, the toolbar and the magnifier.
func (s *shotSession) draw(p *ui.Painter, r ui.Rect, di int) {
	d := s.displays[di]
	p.ImageRegion(d.bitmap, ui.Rect{W: float32(d.pixW), H: float32(d.pixH)}, r, ui.ImageOptions{})
	if s.hasSel {
		if di != s.owner {
			p.Fill(r, shotDim, 0)
		} else {
			s.drawVeil(p, d)
			s.drawSelection(p, d)
			s.drawAnnotations(p, d)
			s.drawBar(p, d)
		}
	} else if s.hoverOK && di == s.cursorDpy && shotRectsIntersect(s.hover, d.Bounds) {
		s.drawWindowHint(p, d)
	}
	if di == s.cursorDpy {
		s.drawMagnifier(p, d)
	}
}

// drawVeil darkens everything outside the selection on its display.
func (s *shotSession) drawVeil(p *ui.Painter, d *shotDisplay) {
	local := shotLocalRect(s.sel, d.shotTarget)
	if local.X > 0 {
		p.Fill(ui.Rect{Y: 0, W: float32(local.X), H: float32(d.Bounds.Height)}, shotDim, 0)
	}
	if x := local.X + local.Width; x < d.Bounds.Width {
		p.Fill(ui.Rect{X: float32(x), Y: 0, W: float32(d.Bounds.Width - x), H: float32(d.Bounds.Height)}, shotDim, 0)
	}
	if local.Y > 0 {
		p.Fill(ui.Rect{X: float32(local.X), Y: 0, W: float32(local.Width), H: float32(local.Y)}, shotDim, 0)
	}
	if y := local.Y + local.Height; y < d.Bounds.Height {
		p.Fill(ui.Rect{X: float32(local.X), Y: float32(y), W: float32(local.Width), H: float32(d.Bounds.Height - y)}, shotDim, 0)
	}
}

// drawSelection draws the accent edge and the eight handles.
func (s *shotSession) drawSelection(p *ui.Painter, d *shotDisplay) {
	local := shotLocalRect(s.sel, d.shotTarget)
	r := ui.Rect{X: float32(local.X), Y: float32(local.Y), W: float32(local.Width), H: float32(local.Height)}
	p.Stroke(r, accent, 0, 1.5)
	const hs = 4
	for _, c := range [][2]float32{
		{r.X, r.Y}, {r.X + r.W/2, r.Y}, {r.X + r.W, r.Y},
		{r.X, r.Y + r.H/2}, {r.X + r.W, r.Y + r.H/2},
		{r.X, r.Y + r.H}, {r.X + r.W/2, r.Y + r.H}, {r.X + r.W, r.Y + r.H},
	} {
		box := ui.Rect{X: c[0] - hs, Y: c[1] - hs, W: 2 * hs, H: 2 * hs}
		p.Fill(box, ui.Hex("#FFFFFF"), 2)
		p.Stroke(box, accent, 2, 1)
	}
}

// drawWindowHint outlines the window under the pointer.
func (s *shotSession) drawWindowHint(p *ui.Painter, d *shotDisplay) {
	local := shotLocalRect(s.hover, d.shotTarget)
	r := ui.Rect{X: float32(local.X), Y: float32(local.Y), W: float32(local.Width), H: float32(local.Height)}
	p.Fill(r, accent.Alpha(0.06), 6)
	p.Stroke(r, accent, 6, 2)
}

// drawAnnotations paints every committed mark, the live one and the text
// line being typed.
func (s *shotSession) drawAnnotations(p *ui.Painter, d *shotDisplay) {
	origin := mygo.Point{X: d.Bounds.X, Y: d.Bounds.Y}
	for _, an := range s.hist.annots {
		shotDrawAnnotation(p, an, origin, 1)
	}
	if s.pre.on {
		shotDrawAnnotation(p, s.pre.ann, origin, 1)
	}
	if s.text != nil {
		an := shotAnnotation{tool: shotToolText, color: s.color, a: s.text.at,
			text: s.text.text + s.text.compose}
		shotDrawAnnotation(p, an, origin, 1)
		x, y := float32(s.text.at.X-d.Bounds.X), float32(s.text.at.Y-d.Bounds.Y)
		tw, th := p.MeasureText(0, ui.Span{Text: s.text.text + s.text.compose, Size: shotTextSize})
		if tw == 0 {
			tw = 1
		}
		p.Line(x+tw, y, x+tw, y+max(th, shotTextSize), 1.5, accent)
	}
}

// drawBar paints the toolbar in the owner display's local points.
func (s *shotSession) drawBar(p *ui.Painter, d *shotDisplay) {
	tb := s.bar
	if tb.rect.Width == 0 {
		return
	}
	ox, oy := d.Bounds.X, d.Bounds.Y
	local := func(r mygo.Rectangle) ui.Rect {
		return ui.Rect{X: float32(r.X - ox), Y: float32(r.Y - oy), W: float32(r.Width), H: float32(r.Height)}
	}
	r := local(tb.rect)
	p.Fill(r, shotBarFill, 10)
	p.Stroke(r, shotBarEdge, 10, 0.5)
	for _, it := range tb.items {
		box := local(it.rect)
		switch it.kind {
		case shotBarTool:
			if s.tool == it.tool {
				p.Fill(box, accent.Alpha(0.22), 6)
			}
			p.Icon(shotToolIcon(it.tool), ui.Rect{X: box.X + 4, Y: box.Y + 4, W: box.W - 8, H: box.H - 8}, ink)
		case shotBarColor:
			c := shotAnnotationColors[it.index]
			p.Fill(ui.Rect{X: box.X + 1, Y: box.Y + 1, W: box.W - 2, H: box.H - 2}, c, (box.W-2)/2)
			if s.color == c {
				p.Stroke(ui.Rect{X: box.X - 1, Y: box.Y - 1, W: box.W + 2, H: box.H + 2}, ink, (box.W+2)/2, 1.5)
			}
		case shotBarWidth:
			if s.width == shotAnnotationWidths[it.index] {
				p.Fill(box, shotBarHot, 6)
			}
			d := float32(shotAnnotationWidths[it.index])
			p.Fill(ui.Rect{X: box.X + box.W/2 - d/2, Y: box.Y + box.H/2 - d/2, W: d, H: d}, ink, d/2)
		case shotBarUndo:
			c := ink
			if !s.hist.canUndo() {
				c = ink3
			}
			p.Icon(shotUndoSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, c)
		case shotBarRedo:
			c := ink
			if !s.hist.canRedo() {
				c = ink3
			}
			p.Icon(shotRedoSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, c)
		case shotBarOCR:
			p.Icon(ocrSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, ink)
		case shotBarPin:
			p.Icon(shotPinSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, ink)
		case shotBarSave:
			p.Icon(shotSaveSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, ink)
		case shotBarCancel:
			p.Icon(shotCloseSymbol, ui.Rect{X: box.X + 5, Y: box.Y + 5, W: box.W - 10, H: box.H - 10}, ink)
		case shotBarCopy:
			p.Fill(box, accent, 6)
			label := s.a.appLoc().ShotCopyButton()
			span := ui.Span{Text: label, Size: 12, Weight: 600, Color: ui.Hex("#FFFFFF")}
			tw, th := p.MeasureText(0, span)
			p.RichText(box.X+(box.W-tw)/2, box.Y+(box.H-th)/2, 0, span)
		}
	}
}

// shotMagnifierAt places the magnifier beside the cursor and keeps it off
// the toolbar: below-right, then above, then left, then right, always
// clamped onto the display.
func shotMagnifierAt(cx, cy, dw, dh, bw, bh int, avoid mygo.Rectangle) (int, int) {
	const gap = 18
	try := func(x, y int) (int, int, bool) {
		x = clampInt(x, 6, max(6, dw-bw-6))
		y = clampInt(y, 6, max(6, dh-bh-6))
		r := mygo.Rectangle{X: x, Y: y, Width: bw, Height: bh}
		return x, y, !shotRectsIntersect(r, avoid)
	}
	for _, p := range [][2]int{
		{cx + gap, cy + gap},
		{cx + gap, cy - gap - bh},
		{cx - gap - bw, cy + gap},
		{cx - gap - bw, cy - gap - bh},
	} {
		if x, y, ok := try(p[0], p[1]); ok {
			return x, y
		}
	}
	x, y, _ := try(cx+gap, cy+gap)
	return x, y
}

// drawMagnifier paints the pixel magnifier and the coordinates under the
// pointer. The zoomed part is the frozen image itself, so it shows exactly
// what the crop will contain.
func (s *shotSession) drawMagnifier(p *ui.Painter, d *shotDisplay) {
	c := s.cursor
	if !shotPointInRect(c, d.Bounds) {
		return
	}
	const (
		boxW, boxH = 156, 108
		imgH       = 72
		pad        = 8
		zoom       = 8
	)
	cx0, cy0 := c.X-d.Bounds.X, c.Y-d.Bounds.Y
	avoid := mygo.Rectangle{}
	if s.bar.rect.Width > 0 {
		avoid = shotLocalRect(s.bar.rect, d.shotTarget)
	}
	x, y := shotMagnifierAt(cx0, cy0, d.Bounds.Width, d.Bounds.Height, boxW, boxH, avoid)
	r := ui.Rect{X: float32(x), Y: float32(y), W: boxW, H: boxH}
	p.Shadow(r, 10, 0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.45))
	p.Fill(r, ui.RGBA(16, 16, 18, 0.96), 10)
	p.Stroke(r, shotBarEdge, 10, 0.5)

	// The part of the frozen picture around the pointer, scaled up.
	cx, cy := c.X-d.Bounds.X, c.Y-d.Bounds.Y
	srcW, srcH := float32(boxW-2*pad)/zoom, float32(imgH-2*pad)/zoom
	src := ui.Rect{X: float32(cx) - srcW/2, Y: float32(cy) - srcH/2, W: srcW, H: srcH}
	dst := ui.Rect{X: r.X + pad, Y: r.Y + pad, W: boxW - 2*pad, H: imgH - 2*pad}
	p.Clip(dst, 0, func() {
		p.ImageRegion(d.bitmap, src, dst, ui.ImageOptions{})
	})
	p.Stroke(dst, ui.RGBA(255, 255, 255, 0.18), 0, 0.5)
	p.Line(dst.X+dst.W/2, dst.Y, dst.X+dst.W/2, dst.Y+dst.H, 1, accent)
	p.Line(dst.X, dst.Y+dst.H/2, dst.X+dst.W, dst.Y+dst.H/2, 1, accent)

	w, h := 0, 0
	if s.hasSel {
		if o := s.ownerDisplay(); o != nil {
			w, h = shotPixelSize(s.sel, o.shotTarget, o.pixW, o.pixH)
		}
	}
	label := fmt.Sprintf("%d, %d", c.X, c.Y)
	if s.hasSel {
		label = fmt.Sprintf("%d, %d · %d × %d", c.X, c.Y, w, h)
	}
	p.Text(r.X+pad, r.Y+imgH+4, label, 10.5, ink2)
}

// shotToolIcon is the toolbar's glyph for a tool.
func shotToolIcon(t shotTool) *ui.SVG {
	switch t {
	case shotToolRect:
		return shotRectSymbol
	case shotToolArrow:
		return shotArrowSymbol
	case shotToolPen:
		return pencilSymbol
	case shotToolMosaic:
		return shotMosaicSymbol
	case shotToolText:
		return shotTextSymbol
	case shotToolNumber:
		return shotNumberSymbol
	}
	return shotRectSymbol
}

// Toolbar glyphs and the screenshot card's own glyph.
var (
	shotSymbol       = symbol(`<path d="M4 8V5a1 1 0 0 1 1-1h3M16 4h3a1 1 0 0 1 1 1v3M20 16v3a1 1 0 0 1-1 1h-3M8 20H5a1 1 0 0 1-1-1v-3"/><rect x="7" y="9" width="10" height="6" rx="1"/>`)
	shotRectSymbol   = symbol(`<rect x="4" y="5" width="16" height="14" rx="2"/>`)
	shotArrowSymbol  = symbol(`<path d="M5 19L19 5M19 5h-6M19 5v6"/>`)
	shotMosaicSymbol = symbol(`<path d="M4 4h6v6H4zM14 4h6v6h-6zM9 9h6v6H9zM4 14h6v6H4zM14 14h6v6h-6z"/>`)
	shotTextSymbol   = symbol(`<path d="M5 6h14M12 6v13M8 19h8"/>`)
	shotNumberSymbol = symbol(`<circle cx="12" cy="12" r="8"/><path d="M10 10l2-1v7"/>`)
	shotUndoSymbol   = symbol(`<path d="M9 7L4 12l5 5"/><path d="M4 12h9a5 5 0 0 1 0 10h-2"/>`)
	shotRedoSymbol   = symbol(`<path d="M15 7l5 5-5 5"/><path d="M20 12h-9a5 5 0 0 0 0 10h2"/>`)
	shotPinSymbol    = symbol(`<path d="M9 3h6l-1 6 3 3v2H7v-2l3-3z"/><path d="M12 14v7"/>`)
	shotSaveSymbol   = symbol(`<path d="M5 4h11l3 3v13H5z"/><path d="M8 4v5h7V4M8 20v-6h8v6"/>`)
	shotCloseSymbol  = symbol(`<path d="M6 6l12 12M18 6L6 18"/>`)
)

// Outputs.

// outputPNG crops the frozen picture to the selection and, when asked,
// composites the annotations at the crop's own pixel size. Mosaic cells and
// strokes are scaled from points, so the export matches the preview.
func (s *shotSession) outputPNG(withAnnotations bool) ([]byte, error) {
	d := s.ownerDisplay()
	if d == nil || !s.hasSel {
		return nil, errors.New("no selection")
	}
	crop := shotCropRect(s.sel, d.shotTarget, d.pixW, d.pixH)
	w, h := crop.Dx(), crop.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty selection")
	}
	base := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(base, base.Bounds(), d.img, crop.Min, draw.Src)
	if withAnnotations && (len(s.hist.annots) > 0 || s.text != nil) {
		sx := float64(w) / float64(max(s.sel.Width, 1))
		sy := float64(h) / float64(max(s.sel.Height, 1))
		origin := mygo.Point{X: s.sel.X, Y: s.sel.Y}
		scale := float32((sx + sy) / 2)
		layer := ui.Render(func(c *ui.Context) {
			c.Root().Background(ui.Transparent)
			ui.Box(c.Key("marks")).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				for _, an := range s.hist.annots {
					shotDrawAnnotation(p, an, origin, scale)
				}
				if s.text != nil {
					shotDrawAnnotation(p, shotAnnotation{tool: shotToolText, color: s.color, a: s.text.at,
						text: s.text.text + s.text.compose}, origin, scale)
				}
			})
		}, w, h, 1)
		draw.Draw(base, base.Bounds(), layer, image.Point{}, draw.Over)
	}
	var buf bytes.Buffer
	if err := stdpng.Encode(&buf, base); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// copy puts the annotated crop on the pasteboard as PNG and as a file URL,
// records it in the clipboard page, and leaves the overlay.
func (s *shotSession) copy() {
	png, err := s.outputPNG(true)
	if err != nil {
		s.a.report(err)
		return
	}
	if !s.a.copyShotPNG(png) {
		return
	}
	s.close(true)
}

// copyShotPNG is the clipboard half shared by the overlay and a pinned
// screenshot: write the picture and its file URL, record the copy, say so.
func (a *shelf) copyShotPNG(png []byte) bool {
	path, err := a.writeShotTempPNG(png)
	if err != nil {
		a.report(err)
		return false
	}
	if err := shotClipboard(png, path); err != nil {
		a.report(err)
		return false
	}
	a.recordShotClip(png)
	a.toastStatus(a.appLoc().ShotCopied(), shotClock().Add(3*time.Second), statusOK)
	return true
}

// save writes the annotated crop to the system screenshot folder.
func (s *shotSession) save() {
	png, err := s.outputPNG(true)
	if err != nil {
		s.a.report(err)
		return
	}
	if _, err := s.a.saveShotPNG(png); err != nil {
		s.a.report(err)
		return
	}
	s.close(true)
	s.a.toastStatus(s.a.appLoc().ShotSaved(), shotClock().Add(3*time.Second), statusOK)
}

// recognize sends the unannotated selection through the app's one Vision
// path; the result lands on the pasteboard with the 框选文字 wording.
func (s *shotSession) recognize() {
	png, err := s.outputPNG(false)
	if err != nil {
		s.a.report(err)
		return
	}
	a := s.a
	if a.ocrScan {
		a.toastStatus(a.appLoc().OcrBusy(), shotClock().Add(3*time.Second), statusInfo)
		return
	}
	path, err := a.writeShotTempPNG(png)
	if err != nil {
		a.report(err)
		return
	}
	s.close(true)
	a.ocrScan = true
	a.ocrBusyAt = shotClock()
	a.ocrRunID++
	runID := a.ocrRunID
	timer := shotAfter(ocrProgressDelay, func() {
		if a.ocrRunID == runID {
			ocrInvalidate(a)
		}
	})
	shotWork(func() {
		defer os.Remove(path)
		lines, err := ocrRecognize(path)
		text := joinOCRLines(lines)
		shotDispatch(func() { a.finishScreenOCR(timer, text, true, err) })
	})
}

// pin opens a floating copy of the annotated crop on the desktop.
func (s *shotSession) pin() {
	png, err := s.outputPNG(true)
	if err != nil {
		s.a.report(err)
		return
	}
	d := s.ownerDisplay()
	if d == nil {
		return
	}
	frame := s.pinFrame(d)
	s.close(true)
	s.a.openShotPin(png, frame, d.scale())
}

// scale is the display's backing scale, or 1.
func (d *shotDisplay) scale() float64 {
	if d.Scale <= 0 {
		return 1
	}
	return d.Scale
}

// pinFrame is where a pinned crop first appears: over its selection, kept
// inside the display's work area.
func (s *shotSession) pinFrame(d *shotDisplay) mygo.Rectangle {
	w, h := shotPixelSize(s.sel, d.shotTarget, d.pixW, d.pixH)
	width := int(math.Round(float64(w) / d.scale()))
	height := int(math.Round(float64(h) / d.scale()))
	area := workAreaOf(d.ID)
	if area.Width == 0 {
		area = d.Bounds
	}
	maxW := int(float64(area.Width) * 0.9)
	maxH := int(float64(area.Height) * 0.9)
	if width > maxW || height > maxH {
		k := min(float64(maxW)/float64(max(width, 1)), float64(maxH)/float64(max(height, 1)))
		width = max(1, int(float64(width)*k))
		height = max(1, int(float64(height)*k))
	}
	f := mygo.Rectangle{X: s.sel.X, Y: s.sel.Y, Width: max(width, 32), Height: max(height, 32)}
	return shotClampRect(f, area)
}

// workAreaOf is the display's visible frame, or the primary's when the
// display is gone (unplugged during the capture).
func workAreaOf(id int64) mygo.Rectangle {
	var primary mygo.Rectangle
	for _, d := range shotDisplays() {
		if d.ID == id {
			return d.WorkArea
		}
		if d.Primary || primary == (mygo.Rectangle{}) {
			primary = d.WorkArea
		}
	}
	return primary
}

// writeShotTempPNG puts png in the app's temporary screenshot folder under
// the system-style name and returns the path.
func (a *shelf) writeShotTempPNG(png []byte) (string, error) {
	dir := shotTempDir()
	name := shotUniqueName(a.shotName(shotClock()), func(n string) bool {
		_, err := os.Stat(filepath.Join(dir, n))
		return err == nil
	})
	path := filepath.Join(dir, name)
	return path, shotWriteFile(path, png, 0o600)
}

// saveShotPNG saves png into the system screenshot folder.
func (a *shelf) saveShotPNG(png []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := shotSaveDir(shotReadSaveDir(), home)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		dir = filepath.Join(home, "Desktop")
	}
	name := shotUniqueName(a.shotName(shotClock()), func(n string) bool {
		_, err := os.Stat(filepath.Join(dir, n))
		return err == nil
	})
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, png, 0o644)
}

// shotName is the system screenshot's file name in the app's language.
func (a *shelf) shotName(t time.Time) string {
	l := a.appLoc()
	return shotFileName(t, l.ShotFileNamePrefix(), l.ShotFileNameAt())
}

// recordShotClip adds the copied crop to the clipboard page as a screenshot
// of Rakko's own: the pasteboard write also carries a file URL, which the
// ordinary watcher skips (Finder files are never recorded), so this is the
// one record.
func (a *shelf) recordShotClip(png []byte) {
	if a.store == nil {
		return
	}
	a.clip.skipCurrent()
	if err := a.store.AddClipImage(png, clipSourceShot); err != nil {
		a.report(err)
		return
	}
	a.changed(nil)
}

// shotTempDir is the folder copy's file URL lives in, under the system
// temporary directory. It is emptied at the start of each screenshot.
func shotTempDir() string {
	return filepath.Join(os.TempDir(), "rakko-shot")
}

// cleanShotTempDir empties the folder at the start of a session; a pinned
// screenshot keeps its own window and bytes, not a file.
func cleanShotTempDir() {
	dir := shotTempDir()
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	os.MkdirAll(dir, 0o700)
}

// readScreenCaptureLocation reads where macOS saves screenshots, from
// `defaults read com.apple.screencapture location`. A missing preference
// is an error, and shotSaveDir falls back to ~/Desktop.
func readScreenCaptureLocation() string {
	out, err := exec.Command("/usr/bin/defaults", "read", "com.apple.screencapture", "location").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// shotClipboardData is what a screenshot copy offers: the picture and the
// file URL as two items, so an app that wants a picture gets one and Finder
// gets a file to paste.
func shotClipboardData(png []byte, path string) (transfer.Data, error) {
	files, err := transfer.FileData(path)
	if err != nil {
		return transfer.Data{}, err
	}
	items := append([]transfer.Item{transfer.NewItem(transfer.Bytes(transfer.PNG, png))}, files.Items()...)
	return transfer.New(items...), nil
}

// writeShotClipboard puts that data on the system pasteboard.
func writeShotClipboard(png []byte, path string) error {
	data, err := shotClipboardData(png, path)
	if err != nil {
		return err
	}
	return mygo.Clipboard.Write(data)
}

// clipSourceShot is the SourceApp marker of screenshots Rakko produced.
// Like clipSourceOCR it is not a bundle id; the clipboard row names it.
const clipSourceShot = "shelf:shot"
