//go:build windows

package platform

// Moving another app's window (the drag-to-the-notch split). The window
// under the press is found with WindowFromPoint over the screen in public
// DIP coordinates, and arranged with SetWindowPos. No accessibility grant
// exists on Windows; a window of another integrity level (an elevated app)
// simply refuses the move, which the shared flow reports.

import (
	"errors"
	"fmt"
)

// ErrAppWindowResize is returned when a window accepted the move but
// refused the size change.
var ErrAppWindowResize = errors.New("windows: the window cannot be resized")

// AppWindow owns one foreign window handle. The zero value is inert.
type AppWindow struct {
	handle uintptr
}

var procIsZoomed = sysProc(user32, "IsZoomed")

// shellWindows are the desktop surfaces behind every application: a press
// on the wallpaper or the taskbar is not a window drag.
var shellWindows = map[string]bool{
	"Progman":                true,
	"WorkerW":                true,
	"Shell_TrayWnd":          true,
	"Shell_SecondaryTrayWnd": true,
}

// AppWindowAt returns the window containing the screen point, with its
// owner pid. The point is in the public DIP coordinates the rest of the
// package uses. ok is false over the desktop, the taskbar, one of this
// app's own overlay windows, or a window that cannot be moved.
func AppWindowAt(x, y float64) (w AppWindow, pid int, ok bool) {
	p := dipToPixels(Rect{X: x, Y: y, Width: 1, Height: 1})
	pt := uintptr(uint32(p.Left)) | uintptr(uint32(p.Top))<<32
	h, _, _ := procWindowFromPoint.Call(pt)
	if h == 0 {
		return AppWindow{}, 0, false
	}
	if root, _, _ := procGetAncestor.Call(h, gaRoot); root != 0 {
		h = root
	}
	if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
		return AppWindow{}, 0, false
	}
	if r, _, _ := procIsIconic.Call(h); r != 0 {
		return AppWindow{}, 0, false
	}
	if isToolWindow(h) || shellWindows[windowClass(h)] {
		return AppWindow{}, 0, false
	}
	pid = windowPID(h)
	if pid == 0 {
		return AppWindow{}, 0, false
	}
	return AppWindow{handle: h}, pid, true
}

// Valid reports whether the ref holds a window.
func (w AppWindow) Valid() bool {
	return w.handle != 0 && isWindow(w.handle)
}

func isWindow(h uintptr) bool {
	if h == 0 {
		return false
	}
	r, _, _ := procIsWindow.Call(h)
	return r != 0
}

// Release releases the reference. A window handle is borrowed, not
// reference counted, so there is nothing to give back.
func (w AppWindow) Release() {}

// Frame reads the window's position and size in public DIP coordinates.
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

// Settable reports whether the window can be moved and resized. Every
// top-level window can be moved with SetWindowPos; only a window with a
// sizing frame accepts a new size.
func (w AppWindow) Settable() (move, resize bool) {
	if !w.Valid() {
		return false, false
	}
	const (
		wsCaption    = 0x00C00000
		wsThickFrame = 0x00040000
	)
	style := getWindowLong(w.handle, gwlStyle)
	move = true
	resize = style&wsThickFrame != 0
	return move, resize
}

// swpAsyncWindowPos posts SetWindowPos to the window's thread instead of
// waiting for it. A window whose app is hung would otherwise block this
// call — and with it the UI thread's snap release — until that app
// answered.
const swpAsyncWindowPos = 0x4000

var procShowWindowAsync = sysProc(user32, "ShowWindowAsync")

// windowThread is the id of the thread that owns h.
func windowThread(h uintptr) uintptr {
	tid, _, _ := procGetWindowThreadPID.Call(h)
	return tid
}

// currentThreadID is the calling thread's id.
func currentThreadID() uintptr {
	tid, _, _ := procGetCurrentThreadID.Call()
	return tid
}

// MoveResize sets the window's position, then its size when resize is
// true. A maximized or minimized window is restored first: SetWindowPos on
// a maximized window would only move its restore rectangle while the
// window kept filling the screen. Position first, so a window resized at
// its old origin cannot push its title bar under the taskbar before the
// move lands.
//
// Both the restore and the move are posted rather than waited on: the
// window belongs to another application, which may be hung. The result is
// read back only when this thread owns the window; for a foreign window
// the caller's confirmation tick verifies the frame once the owner has
// processed the move. ErrAppWindowResize therefore only describes a
// window this thread owns.
func (w AppWindow) MoveResize(r Rect, resize bool) error {
	if !w.Valid() {
		return errors.New("windows: the window is gone")
	}
	h := w.handle
	if r, _, _ := procIsIconic.Call(h); r != 0 {
		procShowWindowAsync.Call(h, 9 /* SW_RESTORE */)
	} else if z, _, _ := procIsZoomed.Call(h); z != 0 {
		procShowWindowAsync.Call(h, 9 /* SW_RESTORE */)
	}
	p := dipToPixels(r)
	flags := uintptr(swpNoZOrder | swpNoActivate | swpAsyncWindowPos)
	if !resize {
		flags |= swpNoSize
	}
	if res, _, err := procSetWindowPos.Call(h, 0,
		uintptr(p.Left), uintptr(p.Top), uintptr(p.width()), uintptr(p.height()), flags); res == 0 {
		return fmt.Errorf("windows: moving the window failed: %w", err)
	}
	if windowThread(h) != currentThreadID() {
		// Posted: the move has not happened yet, so reading the frame
		// would only see the old one. ArrangementError reports the verdict
		// once the confirmation window has run out.
		return nil
	}
	got, ok := windowRect(h)
	if !ok {
		return nil // the window went away; nothing left to check
	}
	return arrangementError(got, p, resize)
}

// ArrangementError reports why a posted arrangement did not land. The
// caller checks it once its confirmation window has run out: a posted move
// cannot fail synchronously, so this is where a window that refuses the
// position or the size still gets its say. A window that has gone away, or
// whose thread has not processed the move at all, reports nothing — it may
// still be on its way.
func (w AppWindow) ArrangementError(target Rect, resize bool) error {
	if !w.Valid() {
		return nil
	}
	got, ok := windowRect(w.handle)
	if !ok {
		return nil
	}
	return arrangementError(got, dipToPixels(target), resize)
}

// arrangementError classifies a frame against the arrangement asked for:
// ErrAppWindowResize when only the size differs, a move error when the
// position does, and nil when the arrangement landed.
func arrangementError(got, want winRect, resize bool) error {
	if abs32(got.Left-want.Left) > 2 || abs32(got.Top-want.Top) > 2 {
		return fmt.Errorf("windows: the window refused the move (at %d,%d, want %d,%d)",
			got.Left, got.Top, want.Left, want.Top)
	}
	if resize && (abs32(got.width()-want.width()) > 2 || abs32(got.height()-want.height()) > 2) {
		return ErrAppWindowResize
	}
	return nil
}
