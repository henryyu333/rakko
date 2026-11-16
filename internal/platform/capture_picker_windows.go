//go:build windows

package platform

// The region picker behind 「框选文字」: every display is frozen, a
// borderless topmost window per display shows its picture, and a drag
// chooses the rectangle that is cropped into the file the caller named.
// Esc cancels quietly, and a click without a drag takes the whole display —
// the behaviour macOS's screencapture -i gives the same flow.
//
// The picker runs on the main thread (mygo windows and the UI belong
// there); PickScreenRegion hands it there and waits for the outcome.

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// pickDisplay is one frozen display with its overlay window.
type pickDisplay struct {
	bounds mygo.Rectangle // DIP, global
	img    *image.RGBA
	pixW   int
	pixH   int
	bitmap *ui.Bitmap
	win    *mygo.Window
}

// picker is the interactive session; every field is main-thread only.
type picker struct {
	path     string
	done     chan error
	displays []*pickDisplay
	owner    int // display the selection lives on, -1 for none
	hasSel   bool
	dragging bool
	press    mygo.Point
	sel      mygo.Rectangle
	finished bool
}

// openRegionPicker freezes the displays and opens their overlays. It
// returns an error only when nothing could be shown; the outcome of the
// pick itself arrives on done.
func openRegionPicker(path string, done chan error) error {
	displays := mygo.Screen.Displays()
	if len(displays) == 0 {
		return fmt.Errorf("windows: no display to capture")
	}
	p := &picker{path: path, done: done, owner: -1}
	for i, d := range displays {
		img, err := captureDisplayPixels(i + 1)
		if err != nil {
			p.closeWindows()
			return err
		}
		disp := &pickDisplay{
			bounds: d.Bounds, img: img,
			pixW: img.Rect.Dx(), pixH: img.Rect.Dy(),
			bitmap: ui.NewBitmap(img).Pixelated(),
		}
		p.displays = append(p.displays, disp)
	}
	for i := range p.displays {
		p.openWindow(i)
		if p.displays[i].win == nil {
			p.closeWindows()
			return fmt.Errorf("windows: cannot show the picker")
		}
	}
	// The display under the pointer takes the keyboard.
	active := 0
	cursor, _ := cursorScreenPoint()
	for i, d := range p.displays {
		if cursor.X >= d.bounds.X && cursor.X < d.bounds.X+d.bounds.Width &&
			cursor.Y >= d.bounds.Y && cursor.Y < d.bounds.Y+d.bounds.Height {
			active = i
		}
	}
	if w := p.displays[active].win; w != nil {
		w.Focus()
	}
	return nil
}

// captureDisplayPixels freezes display index into pixels.
func captureDisplayPixels(index int) (*image.RGBA, error) {
	ms := enumMonitors()
	if index < 1 || index > len(ms) {
		return nil, fmt.Errorf("windows: display %d not found", index)
	}
	return grabScreen(ms[index-1].bounds)
}

// cursorScreenPoint is the pointer in DIP coordinates.
func cursorScreenPoint() (mygo.Point, bool) {
	var pt winPoint
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return mygo.Point{}, false
	}
	ms := enumMonitors()
	x, y := int(pt.X), int(pt.Y)
	// The screen APIs report physical pixels; the overlay works in DIPs,
	// scaled by the DPI of the display the point is on.
	if i := displayAt(ms, x, y); i >= 0 {
		nx, ny := dipOrigin(ms, i)
		return mygo.Point{
			X: nx + scaleBy(x-int(ms[i].bounds.Left), 96, ms[i].dpi),
			Y: ny + scaleBy(y-int(ms[i].bounds.Top), 96, ms[i].dpi),
		}, true
	}
	return mygo.Point{}, false
}

// openWindow creates one display's overlay.
func (p *picker) openWindow(di int) {
	d := p.displays[di]
	b := d.bounds
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: "Rakko", Width: b.Width, Height: b.Height,
		X: b.X, Y: b.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(func(c *ui.Context) { p.overlayView(c, di) }),
	})
	if w == nil {
		return
	}
	h := w.NativeHandle()
	ConfigureCaptureWindow(h)
	SetWindowFrame(h, b.X, b.Y, b.Width, b.Height)
	w.ShowInactive()
	SetWindowLayer(h, LayerCapture)
	d.win = w
}

// overlayView draws and handles one display's overlay.
func (p *picker) overlayView(c *ui.Context, di int) {
	c.Root().Background(ui.Transparent)
	ui.Box(c.Key("pick")).Fill().Focusable().AutoFocus().Cursor(ui.CursorCrosshair).
		HandleInput(func(ev ui.InputEvent) bool { return p.input(ev, di) }).
		Draw(func(painter *ui.Painter, r ui.Rect) { p.draw(painter, r, di) })
}

// input turns pointer and key events into the selection. Coordinates are
// the window's local points.
func (p *picker) input(ev ui.InputEvent, di int) bool {
	if p.finished {
		return false
	}
	local := func(ev ui.InputEvent) mygo.Point {
		d := p.displays[di]
		return mygo.Point{X: d.bounds.X + int(ev.X+0.5), Y: d.bounds.Y + int(ev.Y+0.5)}
	}
	switch ev.Kind {
	case ui.InputKeyDown:
		if ev.Key == ui.KeyEscape {
			p.finishErr(fmt.Errorf("windows: region pick cancelled"))
			return true
		}
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		p.owner, p.press = di, local(ev)
		p.dragging, p.hasSel = false, false
		p.sel = mygo.Rectangle{}
		p.invalidate()
		return true
	case ui.InputPointerMove:
		if p.owner != di || p.press == (mygo.Point{}) && !p.hasSel && !p.dragging {
			// No press on this display yet.
			if p.owner != di {
				return false
			}
		}
		cur := local(ev)
		if abs(cur.X-p.press.X)+abs(cur.Y-p.press.Y) >= 3 {
			p.dragging = true
		}
		if p.dragging {
			p.hasSel = true
			p.sel = clampToRect(mygo.Rectangle{
				X: min(p.press.X, cur.X), Y: min(p.press.Y, cur.Y),
				Width: abs(cur.X - p.press.X), Height: abs(cur.Y - p.press.Y),
			}, p.displays[di].bounds)
			p.invalidate()
		}
		return true
	case ui.InputPointerUp:
		if ev.Button != 0 || p.owner != di {
			return false
		}
		if !p.dragging {
			// A click without a drag takes the whole display, as the system
			// picker does.
			p.sel = p.displays[di].bounds
			p.hasSel = true
		}
		p.dragging = false
		p.commit(di)
		return true
	}
	return false
}

// draw paints the frozen picture and the veil around the selection.
func (p *picker) draw(painter *ui.Painter, r ui.Rect, di int) {
	d := p.displays[di]
	painter.ImageRegion(d.bitmap, ui.Rect{W: float32(d.pixW), H: float32(d.pixH)}, r, ui.ImageOptions{})
	veil := ui.RGBA(0, 0, 0, 0.45)
	if !p.hasSel || p.owner != di {
		painter.Fill(r, veil, 0)
		return
	}
	local := mygo.Rectangle{
		X: p.sel.X - d.bounds.X, Y: p.sel.Y - d.bounds.Y,
		Width: p.sel.Width, Height: p.sel.Height,
	}
	if local.X > 0 {
		painter.Fill(ui.Rect{Y: 0, W: float32(local.X), H: float32(d.bounds.Height)}, veil, 0)
	}
	if x := local.X + local.Width; x < d.bounds.Width {
		painter.Fill(ui.Rect{X: float32(x), Y: 0, W: float32(d.bounds.Width - x), H: float32(d.bounds.Height)}, veil, 0)
	}
	if local.Y > 0 {
		painter.Fill(ui.Rect{X: float32(local.X), Y: 0, W: float32(local.Width), H: float32(local.Y)}, veil, 0)
	}
	if y := local.Y + local.Height; y < d.bounds.Height {
		painter.Fill(ui.Rect{X: float32(local.X), Y: float32(y), W: float32(local.Width), H: float32(d.bounds.Height - y)}, veil, 0)
	}
	painter.Stroke(ui.Rect{X: float32(local.X), Y: float32(local.Y), W: float32(local.Width), H: float32(local.Height)},
		ui.RGBA(90, 166, 255, 1), 0, 1.5)
}

// commit crops the selection out of the display's picture and writes it.
func (p *picker) commit(di int) {
	d := p.displays[di]
	if d.pixW < 1 || d.bounds.Width < 1 {
		p.finishErr(fmt.Errorf("windows: empty capture"))
		return
	}
	scale := float64(d.pixW) / float64(d.bounds.Width)
	x0 := int(float64(p.sel.X-d.bounds.X) * scale)
	y0 := int(float64(p.sel.Y-d.bounds.Y) * scale)
	x1 := int(float64(p.sel.X-d.bounds.X+p.sel.Width) * scale)
	y1 := int(float64(p.sel.Y-d.bounds.Y+p.sel.Height) * scale)
	r := image.Rect(x0, y0, x1, y1).Intersect(d.img.Rect)
	if r.Dx() < 1 || r.Dy() < 1 {
		p.finishErr(fmt.Errorf("windows: empty region"))
		return
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		copy(out.Pix[y*out.Stride:y*out.Stride+r.Dx()*4], d.img.Pix[(r.Min.Y+y)*d.img.Stride+r.Min.X*4:])
	}
	if err := writeAtomicFile(p.path, encodePNG(out)); err != nil {
		p.finishErr(err)
		return
	}
	p.finishErr(nil)
}

// finishErr closes every overlay and reports the outcome once.
func (p *picker) finishErr(err error) {
	if p.finished {
		return
	}
	p.finished = true
	p.closeWindows()
	p.done <- err
}

// closeWindows destroys the overlays.
func (p *picker) closeWindows() {
	for _, d := range p.displays {
		if d.win != nil {
			d.win.Destroy()
			d.win = nil
		}
	}
}

// invalidate repaints every overlay.
func (p *picker) invalidate() {
	for _, d := range p.displays {
		if d.win != nil {
			d.win.Invalidate()
		}
	}
}

// clampToRect keeps r inside b.
func clampToRect(r, b mygo.Rectangle) mygo.Rectangle {
	x0 := max(r.X, b.X)
	y0 := max(r.Y, b.Y)
	x1 := min(r.X+r.Width, b.X+b.Width)
	y1 := min(r.Y+r.Height, b.Y+b.Height)
	if x1 < x0 {
		x1 = x0
	}
	if y1 < y0 {
		y1 = y0
	}
	return mygo.Rectangle{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// abs is |v|.
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
