//go:build windows

package platform

// Moving another app's window (the drag-to-the-notch split). The skeleton
// has no way to address a foreign window without UI Automation, which the
// window batch owns; the call reports "not movable" so snap declines.
// WINDOWS-NEXT(snap)

import "errors"

// ErrAppWindowResize is returned when a window accepted the move but
// refused the size change.
var ErrAppWindowResize = errors.New("platform: the window cannot be resized")

// AppWindow owns one foreign window handle. The zero value is inert.
type AppWindow struct {
	handle uintptr
}

// AppWindowAt returns the window containing the screen point.
// WINDOWS-NEXT(snap)
func AppWindowAt(x, y float64) (w AppWindow, pid int, ok bool) {
	return AppWindow{}, 0, false
}

// Valid reports whether the ref holds a window.
func (w AppWindow) Valid() bool { return w.handle != 0 }

// Release releases the reference (nothing is held on Windows).
func (w AppWindow) Release() {}

// Frame reads the window's position and size.
func (w AppWindow) Frame() (Rect, bool) {
	if w.handle == 0 {
		return Rect{}, false
	}
	r, ok := windowRect(w.handle)
	if !ok {
		return Rect{}, false
	}
	return pixelsToDIP(r), true
}

// Settable reports whether the window can be moved and resized. Foreign
// windows are not addressed yet.
func (w AppWindow) Settable() (move, resize bool) { return false, false }

// MoveResize sets the window's position, then its size. WINDOWS-NEXT(snap)
func (w AppWindow) MoveResize(r Rect, resize bool) error { return errDomain("snap") }
