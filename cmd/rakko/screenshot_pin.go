//go:build darwin

package main

// A pinned screenshot: the annotated crop in a small borderless window on
// the desktop. It lives only for this launch (nothing is written to
// layout.json), drags with the pointer, zooms with the scroll wheel, and
// closes on a double click, Esc or its context menu. The window takes the
// keyboard when the user clicks it — unlike a tool-card pin, Esc must reach
// it — but opening it never steals focus from the app the user returns to.

import (
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
)

// shotPin is one pinned screenshot window.
type shotPin struct {
	a      *shelf
	win    *mygo.Window
	png    []byte
	bitmap *ui.Bitmap
	pixW   int
	pixH   int
	scale  float64
	frame  mygo.Rectangle
	zoom   float64

	pressed bool
	grab    mygo.Point
	from    mygo.Rectangle
}

// openShotPin shows png at frame. scale is the display's backing scale, so
// the pinned picture starts at the physical size the user selected.
func (a *shelf) openShotPin(png []byte, frame mygo.Rectangle, scale float64) {
	bm, err := ui.DecodeBitmap(png)
	if err != nil {
		a.report(err)
		return
	}
	w, h := bm.Size()
	if scale <= 0 {
		scale = 1
	}
	baseW := float64(w) / scale
	zoom := 1.0
	if baseW > 0 && frame.Width > 0 {
		zoom = float64(frame.Width) / baseW
	}
	p := &shotPin{a: a, png: png, bitmap: bm, pixW: w, pixH: h, scale: scale, frame: frame, zoom: zoom}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title: a.appLoc().ShotPinTitle(), Width: frame.Width, Height: frame.Height,
		X: frame.X, Y: frame.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(p.view),
	})
	if win == nil {
		return
	}
	p.win = win
	win.SetVisibleOnAllWorkspaces(true)
	hnd := win.NativeHandle()
	// The overlay policy gives a key-capable nonactivating panel; the
	// floating level keeps it over ordinary windows, under the menu bar.
	mac.ConfigureOverlayWindow(hnd)
	mac.SetWindowLevel(hnd, 3) // NSFloatingWindowLevel
	mac.SetWindowFrame(hnd, frame.X, frame.Y, frame.Width, frame.Height)
	win.OnClose(func(*mygo.CloseEvent) { a.removeShotPin(p) })
	win.ShowInactive()
	a.shotPins = append(a.shotPins, p)
}

// removeShotPin drops p from the launch's list.
func (a *shelf) removeShotPin(p *shotPin) {
	for i, other := range a.shotPins {
		if other == p {
			a.shotPins = append(a.shotPins[:i], a.shotPins[i+1:]...)
			return
		}
	}
}

// close destroys the pin. Idempotent.
func (p *shotPin) close() {
	if p.win != nil {
		p.win.Destroy()
	}
	p.a.removeShotPin(p)
}

// view draws the picture inside the pin's 0.5pt edge.
func (p *shotPin) view(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	// Esc reaches the window once it is key; a click that made it key is
	// also the click that starts a drag.
	c.OnShortcut(0, ui.KeyEscape, p.close)
	root := ui.Box(c.Key("shot-pin")).Fill().Radius(10).Clip().Focusable().
		HandleInput(p.input).Draw(func(painter *ui.Painter, r ui.Rect) {
		painter.ImageRegion(p.bitmap, ui.Rect{W: float32(p.pixW), H: float32(p.pixH)}, r, ui.ImageOptions{})
		painter.Stroke(r, pinEdgeColor, 10, 0.5)
	})
	root.ContextMenu(p.menu)
}

// input drags the window and closes it on a double click.
func (p *shotPin) input(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		if ev.Clicks >= 2 {
			p.close()
			return true
		}
		p.pressed = true
		p.grab = mygo.Point{X: p.frame.X + int(ev.X+0.5), Y: p.frame.Y + int(ev.Y+0.5)}
		p.from = p.frame
		return true
	case ui.InputPointerMove:
		if !p.pressed {
			return false
		}
		gx := p.frame.X + int(ev.X+0.5)
		gy := p.frame.Y + int(ev.Y+0.5)
		p.frame.X = p.from.X + gx - p.grab.X
		p.frame.Y = p.from.Y + gy - p.grab.Y
		area := workAreaOf(pinDisplayNear(p.frame))
		if area.Width > 0 {
			p.frame = shotClampRect(p.frame, area)
		}
		mac.SetWindowFrame(p.win.NativeHandle(), p.frame.X, p.frame.Y, p.frame.Width, p.frame.Height)
		return true
	case ui.InputPointerUp:
		if !p.pressed {
			return false
		}
		p.pressed = false
		return true
	case ui.InputScroll:
		p.zoomBy(math.Exp(-float64(ev.DY) * 0.06))
		return true
	case ui.InputZoom:
		p.zoomBy(float64(ev.Scale))
		return true
	case ui.InputKeyDown:
		if ev.Key == ui.KeyEscape {
			p.close()
			return true
		}
	}
	return false
}

// pinDisplayNear is the display whose work area holds p's centre.
func pinDisplayNear(r mygo.Rectangle) int64 {
	cx, cy := r.X+r.Width/2, r.Y+r.Height/2
	for _, d := range shotDisplays() {
		a := d.Bounds
		if cx >= a.X && cx < a.X+a.Width && cy >= a.Y && cy < a.Y+a.Height {
			return d.ID
		}
	}
	return 0
}

// zoomBy scales the picture around its centre, keeping the window on its
// display. The base size is the crop's own point size.
func (p *shotPin) zoomBy(k float64) {
	if k <= 0 || k == 1 {
		return
	}
	zoom := min(max(p.zoom*k, 0.1), 5)
	if zoom == p.zoom {
		return
	}
	baseW := float64(p.pixW) / p.scale
	baseH := float64(p.pixH) / p.scale
	w := max(1, int(math.Round(baseW*zoom)))
	h := max(1, int(math.Round(baseH*zoom)))
	cx, cy := p.frame.X+p.frame.Width/2, p.frame.Y+p.frame.Height/2
	f := mygo.Rectangle{X: cx - w/2, Y: cy - h/2, Width: w, Height: h}
	if area := workAreaOf(pinDisplayNear(f)); area.Width > 0 {
		f = shotClampRect(f, area)
	}
	p.zoom, p.frame = zoom, f
	mac.SetWindowFrame(p.win.NativeHandle(), f.X, f.Y, f.Width, f.Height)
}

// menu is the pin's context menu: copy, save, close.
func (p *shotPin) menu(m *ui.Menu) {
	l := p.a.appLoc()
	if m.Item(l.ShotCopyButton()).Chosen() {
		p.a.copyShotPNG(p.png)
	}
	if m.Item(l.ShotSaveButton()).Chosen() {
		if _, err := p.a.saveShotPNG(p.png); err != nil {
			p.a.report(err)
		} else {
			p.a.toastStatus(l.ShotSaved(), shotClock().Add(3*time.Second), statusOK)
		}
	}
	m.Separator()
	if m.Item(l.NoteClose()).Chosen() {
		p.close()
	}
}
