//go:build windows

package platform

// Window configuration on Windows. mygo creates the windows (a panel is a
// topmost tool window with a hidden owner); these calls apply the parts
// mygo's public options do not: keeping a window in the topmost band, the
// click-through switch, DWM's dark title bar, and the frame arithmetic in
// the public DIP space.

import (
	"unsafe"
)

// Init prepares COM (apartment-threaded, the one a WinRT activation and the
// shell interfaces expect) and records the main thread. It installs
// nothing and requests no permission: Windows has no equivalent of the
// macOS privacy grants.
func Init() error {
	recordMainThread()
	r, _, _ := procCoInitializeEx.Call(0, 2 /* COINIT_APARTMENTTHREADED */)
	// S_FALSE means the thread was already initialized with this model;
	// RPC_E_CHANGED_MODE means the host initialized another one. Both
	// leave the shell calls usable.
	switch int32(r) {
	case 0, 1, -2147417850:
		return nil
	}
	return errComInit
}

type platformError string

func (e platformError) Error() string { return string(e) }

// errComInit reports a COM initialization failure that leaves the shell
// interfaces unusable.
var errComInit = platformError("platform: COM initialization failed")

// ConfigureNotchWindow turns the shelf into the always-on-top overlay that
// occupies the display's top edge. The window is already a frameless
// topmost panel; this keeps it out of the taskbar and re-orders it above
// the shell chrome.
func ConfigureNotchWindow(window uintptr) {
	ConfigureOverlayWindow(window)
}

// ConfigureOverlayWindow applies the shared overlay policy: a tool window
// (out of the taskbar and Alt+Tab) in the topmost band, with Win32 caption
// bits and DWM frame chrome stripped so a transparent native-UI surface never
// shows Windows 11's rounded outline or drop shadow around its full HWND.
func ConfigureOverlayWindow(window uintptr) {
	if window == 0 {
		return
	}
	ensureToolWindow(window)
	disableTransparentFrameChrome(window)
	reorderFront(window, true)
}

// ConfigureIdleCapsuleWindow applies the overlay policy to the picture-only
// resting capsule. The capsule is 120 × 30; Windows enforces
// SM_CXMINTRACK × SM_CYMINTRACK (136 × 39) on a captioned window, which made
// the capsule draw 39pt tall and overlap the top of a note that had been
// placed just below the 30pt shell. ConfigureOverlayWindow already drops
// WS_CAPTION for WS_POPUP and strips DWM frame chrome.
func ConfigureIdleCapsuleWindow(window uintptr) {
	ConfigureOverlayWindow(window)
}

// ConfigureCaptureWindow makes the screenshot overlay take the topmost
// band with the keyboard available (Esc, Ctrl+C, text entry).
func ConfigureCaptureWindow(window uintptr) {
	if window == 0 {
		return
	}
	ensureToolWindow(window)
	disableTransparentFrameChrome(window)
	setExBits(window, wsExNoActivate, false)
	reorderFront(window, true)
}

// DisableWindowAnimation is a no-op on Windows: an ordinary window has no
// opening or closing scale animation to turn off.
func DisableWindowAnimation(window uintptr) {}

// ConfigurePinWindow makes a pinned card a non-activating topmost panel:
// a click on the card never takes the keyboard from the app being typed in.
// It also makes the card follow the user across virtual desktops, since
// Windows has no show-on-all-desktops window bit.
func ConfigurePinWindow(window uintptr) {
	if window == 0 {
		return
	}
	ensureToolWindow(window)
	disableTransparentFrameChrome(window)
	setExBits(window, wsExNoActivate, true)
	// The card may be the window a drag opened as a preview, which ignores
	// the mouse so the gesture stays with the card that started it. A
	// committed pin is a real target: clicking it runs the card and a drag
	// carries it back to the notch.
	SetWindowIgnoresMouseEvents(window, false)
	reorderFront(window, true)
	followCurrentDesktop(window)
}

// ConfigurePetWindow makes the desktop otter the same non-activating
// topmost panel.
func ConfigurePetWindow(window uintptr) {
	if window == 0 {
		return
	}
	ensureToolWindow(window)
	disableTransparentFrameChrome(window)
	setExBits(window, wsExNoActivate, true)
	reorderFront(window, true)
}

// ConfigureDragPreview makes a drag preview a non-activating topmost
// tool window; the band is the topmost one, as everywhere on Windows. It
// ignores the mouse, like the preview on macOS: the press belongs to the
// card that started the drag, and a preview window that took it would end
// the gesture under the pointer the copy follows.
func ConfigureDragPreview(window uintptr, layer Layer) {
	if window == 0 {
		return
	}
	ensureToolWindow(window)
	disableTransparentFrameChrome(window)
	setExBits(window, wsExNoActivate, true)
	SetWindowIgnoresMouseEvents(window, true)
	reorderFront(window, true)
}

// ConfigureDragPreviewAbove is ConfigureDragPreview: Windows has no band
// above the topmost one for a preview to join.
func ConfigureDragPreviewAbove(window uintptr, layer Layer) {
	ConfigureDragPreview(window, layer)
}

// SetWindowFrame moves and resizes window in one SetWindowPos, in the
// public top-left DIP coordinates the rest of the app uses.
func SetWindowFrame(window uintptr, x, y, w, h int) {
	if window == 0 {
		return
	}
	p := dipToPixels(Rect{X: float64(x), Y: float64(y), Width: float64(w), Height: float64(h)})
	procSetWindowPos.Call(window, 0, uintptr(p.Left), uintptr(p.Top),
		uintptr(p.width()), uintptr(p.height()),
		swpNoZOrder|swpNoActivate)
}

// SetWindowLayer puts the window at the named band and re-orders it to the
// front of that band. Every band is the topmost band on Windows; the
// difference between them cannot be expressed without the menu-bar and
// input-method levels macOS has.
func SetWindowLayer(window uintptr, layer Layer) {
	if window == 0 {
		return
	}
	reorderFront(window, true)
}

// WindowLayerNumber is the platform's stacking number for a band. Windows
// has a single topmost band, so every layer reports the same number.
func WindowLayerNumber(layer Layer) int { return 1 }

// WindowLayer reports the band the window is in: the topmost band when the
// window has the topmost bit, floating otherwise.
func WindowLayer(window uintptr) Layer {
	if windowExStyle(window)&wsExTopmost != 0 {
		return LayerStatus
	}
	return LayerFloating
}

// OrderOverlayFront re-fronts the overlay inside the topmost band.
func OrderOverlayFront(window uintptr) {
	if window == 0 {
		return
	}
	reorderFront(window, true)
}

// SetOverlayAcceptsMouse decides whether the overlay takes mouse events.
// accept is true only while the pointer is over the drawn shell or a drag
// is in flight; otherwise the whole window passes clicks through with
// WS_EX_TRANSPARENT, which lets every click beside the shell reach the app
// or the taskbar underneath. The overlay is already topmost, so accept does
// not re-order it.
func SetOverlayAcceptsMouse(window uintptr, accept bool) {
	if window == 0 || OverlayIgnoresMouse(window) == !accept {
		return
	}
	SetWindowIgnoresMouseEvents(window, !accept)
}

// SetWindowIgnoresMouseEvents toggles the window's click-through bit. A
// non-layered window is made layered once so WS_EX_TRANSPARENT is honoured;
// subsequent toggles only flip WS_EX_TRANSPARENT without re-applying alpha or
// sending SWP_FRAMECHANGED.
func SetWindowIgnoresMouseEvents(window uintptr, ignore bool) {
	if window == 0 {
		return
	}
	if ignore && windowExStyle(window)&wsExLayered == 0 {
		setLayeredAlpha(window)
	}
	if ignore {
		// Click-through on: no frame-change notification, so a full-screen
		// window underneath is not disturbed.
		setExBitsNoFrame(window, wsExTransparent, true)
		return
	}
	// Click-through off is what makes the shell clickable again, and a
	// layered window only starts honouring that after a frame change: this
	// switch takes the notifying path or the shell keeps swallowing clicks
	// it no longer receives.
	setExBits(window, wsExTransparent, false)
}

// OverlayIgnoresMouse reads whether the window currently has both
// WS_EX_TRANSPARENT and WS_EX_LAYERED, which Windows requires together for
// mouse clicks to pass through to other applications.
func OverlayIgnoresMouse(window uintptr) bool {
	ex := windowExStyle(window)
	return ex&wsExTransparent != 0 && ex&wsExLayered != 0
}

// SetDragElevated raises the overlay above the taskbar for the duration of
// a file drag. The overlay already lives in the topmost band, so there is
// nothing to raise; the call stays for the shared drag flow.
func SetDragElevated(window uintptr, elevate bool) {
	if window == 0 {
		return
	}
	reorderFront(window, true)
}

// SetWindowAppearance pins a window to the dark or light title bar. The
// native UI draws its own colours; this covers the window chrome DWM draws
// around a framed window and does nothing for a frameless panel.
func SetWindowAppearance(window uintptr, dark bool) {
	if window == 0 {
		return
	}
	v := int32(0)
	if dark {
		v = 1
	}
	// DWMWA_USE_IMMERSIVE_DARK_MODE: 20 since Windows 10 20H1, 19 before.
	if r, _, _ := procDwmSetWindowAttr.Call(window, 20, uintptr(unsafe.Pointer(&v)), 4); r != 0 {
		procDwmSetWindowAttr.Call(window, 19, uintptr(unsafe.Pointer(&v)), 4)
	}
}

// DWM frame attributes used to strip Windows 11's rounded border and shadow
// from transparent frameless overlays, or restore them on framed windows.
const (
	dwmwaNCRenderingPolicy      = 2
	dwmncrpDisabled             = 1
	dwmwaWindowCornerPreference = 33
	dwmcpDoNotRound             = 1
	dwmcpRound                  = 2
	dwmwaBorderColor            = 34
	dwmwaColorNone              = 0xFFFFFFFE
)

// disableTransparentFrameChrome removes Win32's caption bit (switching to
// WS_POPUP) and turns off DWM's non-client border, rounded corners and shadow
// on a transparent frameless overlay. mygo creates Panel windows with
// WS_CAPTION and DWMWCP_ROUND, and skips DWMNCRP_DISABLED for native-UI
// surfaces (opts.Surface == true), which leaves a visible rounded 1px outline
// and shadow around the entire transparent HWND on Windows 11.
func disableTransparentFrameChrome(window uintptr) {
	if window == 0 {
		return
	}
	updateStyleBits(window, wsPopup, wsCaption)
	nc := int32(dwmncrpDisabled)
	procDwmSetWindowAttr.Call(window, dwmwaNCRenderingPolicy, uintptr(unsafe.Pointer(&nc)), 4)
	pref := int32(dwmcpDoNotRound)
	procDwmSetWindowAttr.Call(window, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&pref)), 4)
	border := uint32(dwmwaColorNone)
	procDwmSetWindowAttr.Call(window, dwmwaBorderColor, uintptr(unsafe.Pointer(&border)), 4)
}

// dwmMargins is DWM's MARGINS, in pixels, for DwmExtendFrameIntoClientArea.
type dwmMargins struct{ Left, Right, Top, Bottom int32 }

var procDwmExtendFrame = sysProc(dwmapi, "DwmExtendFrameIntoClientArea")

// ConfigureFramedWindow finishes a window with a hidden title bar — the
// Settings, note, rename and update windows — as Windows draws a finished
// window: rounded corners on Windows 11, and the caption of a dark window
// where the page is dark, so the glyphs MyGo draws over the page's
// top-right corner are white on it. The frame is extended a pixel into the
// client area, which is what DWM casts a shadow around: a window whose
// client area reaches its edges, with nothing of the system frame left,
// gets none. Windows 10 has no corner preference and keeps its square
// windows.
func ConfigureFramedWindow(window uintptr, dark bool) {
	if window == 0 {
		return
	}
	pref := int32(dwmcpRound)
	procDwmSetWindowAttr.Call(window, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&pref)), 4)
	m := dwmMargins{Top: 1}
	procDwmExtendFrame.Call(window, uintptr(unsafe.Pointer(&m)))
	if dark {
		SetWindowAppearance(window, true)
	}
}

// IsKeyWindow reports whether the window has the keyboard: it is the
// foreground window (or the active one inside this thread).
func IsKeyWindow(window uintptr) bool {
	if window == 0 {
		return false
	}
	if fg, _, _ := procGetForegroundWindow.Call(); fg == window {
		return true
	}
	active, _, _ := procGetActiveWindow.Call()
	return active == window
}

// WindowOccluded reports whether the window draws nothing: hidden, an icon,
// cloaked by DWM (another virtual desktop or minimized), or the lock screen
// is up. Windows offers no cheap query for "fully covered by another
// window", so a covered window reports false, the safe answer: the caller
// keeps animating rather than freezing.
func WindowOccluded(window uintptr) bool {
	if window == 0 {
		return true
	}
	if r, _, _ := procIsWindowVisible.Call(window); r == 0 {
		return true
	}
	if r, _, _ := procIsIconic.Call(window); r != 0 {
		return true
	}
	var cloaked uint32
	if r, _, _ := procDwmGetWindowAttr.Call(window, 14 /* DWMWA_CLOAKED */, uintptr(unsafe.Pointer(&cloaked)), 4); r == 0 && cloaked != 0 {
		return true
	}
	return false
}
