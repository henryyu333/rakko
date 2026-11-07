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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo"
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
	shotKeyWindow      = mac.IsKeyWindow
	shotSetWindowLevel = mac.SetWindowLevel
	shotOrderOverlay   = mac.OrderOverlayFront
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
	handle uintptr
}

// shotCapture is one display's freeze result, ready to be assigned.
type shotCapture struct {
	index  int
	target shotTarget
	img    *image.RGBA
	err    error
}

// shotTextEdit is one text box being edited: its top-left, content width,
// the text bound to the TextArea, its current style, and the annotation it
// replaces (-1 for a new one). The control owns composing, selection and
// line breaks; this carries only what the annotation will store.
type shotTextEdit struct {
	at     mygo.Point
	width  int
	text   string
	color  ui.Color
	stroke int
	index  int
	orig   shotAnnotation
	focus  bool
}

// shotTextDraft is the box a drag with the text tool is making.
type shotTextDraft struct {
	at  mygo.Point
	cur mygo.Point
}

// shotTextRect caches one finished text's box for hit testing, in global
// points; the index names its annotation.
type shotTextRect struct {
	index int
	rect  mygo.Rectangle
}

// shotSession is one screenshot from the hotkey to the output.
type shotSession struct {
	a            *shelf
	displays     []*shotDisplay
	owner        int // display that owns the selection, -1 for none
	hasSel       bool
	sel          mygo.Rectangle
	dragging     bool
	mode         shotMode
	press        mygo.Point
	pressWin     mygo.Rectangle
	pressHit     bool
	moveFrom     mygo.Rectangle
	resizeH      shotHandle
	tool         shotTool
	color        ui.Color
	width        int
	hist         shotHistory
	pre          pre
	text         *shotTextEdit
	textDraft    *shotTextDraft
	textRects    []shotTextRect
	textBoxH     int
	textDrag     *shotTextDrag
	focusRoot    bool
	textLevelLow bool
	brushFrom    mygo.Point
	brushSeen    map[[2]int]bool
	nextNum      int
	bar          shotToolbar
	cursor       mygo.Point
	cursorDpy    int
	hover        mygo.Rectangle
	hoverOK      bool
	windows      []mac.ScreenWindow
	windowsAt    time.Time
	prevPID      int
	captureMS    int64
	installed    bool
	closed       bool
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
	if s.prevPID == mac.OwnPID() && a.prevAppPID > 0 {
		// The panel (or a settings window) was frontmost when the hotkey
		// arrived: the app to hand the keyboard back to is the one it was
		// opened from.
		s.prevPID = a.prevAppPID
	}
	s.cursor = shotCursorPoint()
	a.shot = s
	wasVisible := a.visible || a.peeking || !a.closeAt.IsZero()
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
	if s.closed || s.a.quitting || s.a.shot != s {
		return
	}
	s.captureMS = elapsedMS
	s.cursor = shotCursorPoint()
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
	// Activation can re-order the app's other windows (the notch shell at
	// its status level, a Settings window). Re-front each overlay in its
	// own (screen-saver) band before taking the keyboard; AppKit may apply
	// its own floating level once the run loop turns, so assert it again a
	// moment later.
	s.assertCaptureLevel()
	time.AfterFunc(30*time.Millisecond, func() {
		mygo.RunOnMain(func() {
			if !s.closed {
				s.assertCaptureLevel()
			}
		})
	})
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
	// mygo applies the AlwaysOnTop floating level when it orders a window
	// front; the capture band must be the last word.
	mac.SetWindowLevel(h, mac.CaptureWindowLevel)
	d.handle = h
	d.win = w
}

// assertCaptureLevel puts every overlay in the screen-saver band and brings
// it to the front of that band. Main thread only.
func (s *shotSession) assertCaptureLevel() {
	for i, d := range s.displays {
		if s.textLevelLow && i == s.owner {
			continue // an editing box keeps its display below the IM band
		}
		if d.win != nil {
			mac.SetWindowLevel(d.win.NativeHandle(), mac.CaptureWindowLevel)
			mac.OrderOverlayFront(d.win.NativeHandle())
		}
	}
}

// lowerForText puts the owner display's overlay below the input method's
// candidate band while a text box edits: a candidate panel belongs to the
// input method's process and is drawn at kCGDockWindowLevel (20), so a
// screenshot overlay at the capture level would hide it completely. The
// other displays stay in the capture band.
func (s *shotSession) lowerForText() {
	if s.textLevelLow {
		return
	}
	d := s.ownerDisplay()
	if d == nil || d.handle == 0 {
		return
	}
	shotSetWindowLevel(d.handle, mac.SearchKeyboardLevel)
	s.textLevelLow = true
}

// restoreTextLevel puts the owner display's overlay back in the capture
// band and re-fronts it. It runs when editing ends and when the session
// closes; the other displays are untouched.
func (s *shotSession) restoreTextLevel() {
	if !s.textLevelLow {
		return
	}
	s.textLevelLow = false
	if d := s.ownerDisplay(); d != nil && d.handle != 0 {
		shotSetWindowLevel(d.handle, mac.CaptureWindowLevel)
		shotOrderOverlay(d.handle)
	}
}

// overlayView draws one display. The whole overlay is a single element:
// it holds the keyboard, takes every pointer gesture and paints the frozen
// picture, the selection, the annotations and the toolbar in one pass.
func (s *shotSession) overlayView(c *ui.Context, di int) {
	c.Root().Background(ui.Transparent)
	// ⌘C / ⌘S and undo also arrive as window shortcuts on macOS: the Edit
	// menu can turn ⌘C into an InputCommand the focused element never sees.
	c.OnShortcut(ui.Cmd, ui.KeyC, func() {
		// While a box edits, ⌘C copies its selection, not the screenshot.
		if !s.closed && s.hasSel && s.text == nil {
			s.copy()
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyS, func() {
		if !s.closed && s.hasSel {
			s.finishText()
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
	if s.hasSel && di == s.owner {
		s.measureTexts(c)
	}
	cursor := s.cursorShape(di)
	root := ui.Box(c.Key("shot")).Fill().Focusable().AutoFocus().Cursor(cursor).
		HandleInput(func(ev ui.InputEvent) bool { return s.input(ev, di) }).
		Draw(func(p *ui.Painter, r ui.Rect) { s.draw(p, r, di) })
	if s.focusRoot {
		root.Focus()
		s.focusRoot = false
	}
	if s.hasSel && di == s.owner {
		if s.text != nil {
			s.buildTextEditor(c, s.displays[di])
		}
		s.buildBar(c, s.displays[di])
	}
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
	// Leaving the screenshot always puts the owner display back in the
	// capture band, even when it closes from under an editing box.
	s.restoreTextLevel()
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
		// A press outside the box (the control takes its own) completes
		// the edit before whatever the press was meant for.
		s.finishText()
	}
	if s.hasSel {
		if di == s.owner {
			s.layoutBar()
			if s.bar.contains(p) {
				if it, ok := s.bar.shotBarAt(p); ok {
					s.actBar(it)
				}
				return true
			}
			if idx, ok := s.textAt(p); ok {
				if ev.Clicks >= 2 {
					s.editText(idx)
					return true
				}
				s.startTextMove(idx, p)
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
				if s.tool == shotToolText {
					s.startTextDraft(p)
				} else if s.tool != shotToolNone {
					s.beginMark(p, di)
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
	if s.textDraft != nil {
		s.textDraft.cur = p
		s.invalidate()
		return true
	}
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
		if s.textDrag != nil && s.textDrag.index >= 0 {
			s.textDragTo(p, d.Bounds)
			return true
		}
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
		switch {
		case s.tool == shotToolPen && s.pre.on && ev.Mods&ui.Shift == 0:
			s.pre.ann.points = append(s.pre.ann.points, p)
		case s.tool == shotToolMosaic && s.pre.on:
			s.paintMosaic(s.brushFrom, p)
			s.brushFrom = p
		default:
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
	if s.textDraft != nil {
		s.textDraft.cur = p
		s.createTextFromDraft()
		s.textDraft = nil
		s.mode = shotModeIdle
		s.invalidate()
		return true
	}
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
		s.textDrag = nil
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
func (s *shotSession) beginMark(p mygo.Point, di int) {
	switch s.tool {
	case shotToolNumber:
		s.hist.snap()
		s.hist.push(shotAnnotation{tool: shotToolNumber, color: s.color, width: s.width, a: p, number: s.nextNum})
		s.nextNum++
		s.invalidate()
		return
	case shotToolMosaic:
		s.mode = shotModeDraw
		s.press = p
		s.pre = pre{ann: shotAnnotation{tool: shotToolMosaic, color: s.color, width: s.width}, on: true}
		s.brushFrom, s.brushSeen = p, map[[2]int]bool{}
		s.paintMosaic(p, p)
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
	case shotToolRect, shotToolArrow:
		if abs(an.b.X-an.a.X) < 2 || abs(an.b.Y-an.a.Y) < 2 {
			return
		}
	case shotToolMosaic:
		if len(an.cells) == 0 {
			return
		}
	case shotToolPen:
		if len(an.points) < 2 {
			return
		}
	}
	s.hist.snap()
	s.hist.push(an)
}

// paintMosaic adds the cells a brush stroke passes between two pointer
// samples. Cells are shared by the whole stroke, so a slow drag does not
// paint the same block twice.
func (s *shotSession) paintMosaic(from, to mygo.Point) {
	d := s.ownerDisplay()
	if d == nil || !s.pre.on {
		return
	}
	if s.brushSeen == nil {
		s.brushSeen = map[[2]int]bool{}
	}
	width := s.pre.ann.width
	shotMosaicCells(d.img, d.shotTarget, shotMosaicBlockPixels(width), shotMosaicBrushRadius(width), from, to,
		func(col, row int, cell shotMosaicCell) {
			if !shotRectsIntersect(cell.rect, s.sel) {
				return
			}
			key := [2]int{col, row}
			if s.brushSeen[key] {
				return
			}
			s.brushSeen[key] = true
			s.pre.ann.cells = append(s.pre.ann.cells, cell)
		})
}

// layoutBar places the toolbar for the current selection.
func (s *shotSession) layoutBar() {
	if !s.hasSel {
		s.bar = shotToolbar{}
		return
	}
	if d := s.ownerDisplay(); d != nil {
		s.bar = shotToolbarLayout(s.sel, d.Bounds, s.tool)
	}
}

// actBar runs one toolbar control. Colour and width changes apply to the
// box being edited at once; every other control completes the edit first.
func (s *shotSession) actBar(it shotBarItem) {
	if s.text != nil && (it.kind == shotBarColor || it.kind == shotBarWidth) {
		if it.kind == shotBarColor {
			s.color = shotAnnotationColors[it.index]
			s.text.color = s.color
		} else {
			s.width = shotAnnotationWidths[it.index]
			s.text.stroke = s.width
		}
		s.invalidate()
		return
	}
	s.finishText()
	switch it.kind {
	case shotBarTool:
		if s.tool == it.tool {
			s.tool = shotToolNone
		} else {
			s.tool = it.tool
		}
		s.layoutBar()
		s.invalidate()
	case shotBarColor:
		s.color = shotAnnotationColors[it.index]
		s.invalidate()
	case shotBarWidth:
		s.width = shotAnnotationWidths[it.index]
		s.invalidate()
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

// escape is the one ladder: abandon the line, drop the tool, drop the
// selection, leave.
func (s *shotSession) escape() {
	switch {
	case s.text != nil:
		s.finishText()
	case s.tool != shotToolNone:
		s.tool = shotToolNone
		s.layoutBar()
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
