//go:build darwin

package platform

// Input-method probes. The IME candidate panel is owned by the input
// method's own process, positioned by the text input client's
// firstRectForCharacterRange:actualRange: (a rect in screen coordinates)
// and drawn at kCGDockWindowLevel — so its visibility depends both on the
// rect this app reports and on the notch window's level.

// WindowFrame reads the NSWindow's frame in screen coordinates (AppKit's
// bottom-left origin, Y up). Main thread only.
func WindowFrame(window uintptr) NSRect {
	var r NSRect
	if window == 0 {
		return r
	}
	withPool(func() {
		r = msgRect(id(window), sel("frame"))
	})
	return r
}

// FirstResponderClass reports the class name of the window's current first
// responder — the view AppKit routes keys and IME composition to. Main
// thread only.
func FirstResponderClass(window uintptr) string {
	if window == 0 {
		return ""
	}
	var name string
	withPool(func() {
		name = goString(send(send(id(window), "firstResponder"), "className"))
	})
	return name
}

// IMECompositionRect asks the window's text input view for the screen rect
// of the document range at the caret — the rect the IME anchors its
// candidate panel to. ok is false while the window has no surface view or
// the client reports no usable rect for the range. Main thread only.
func IMECompositionRect(window uintptr, location, length uint) (r NSRect, ok bool) {
	if window == 0 {
		return r, false
	}
	withPool(func() {
		view := overlaySurfaceView(id(window))
		if view == 0 || msgFirstRectForRange == nil {
			return
		}
		out := msgFirstRectForRange(view, sel("firstRectForCharacterRange:actualRange:"), NSRange{Location: location, Length: length}, nil)
		if out.Size.Width > 0 && out.Size.Height > 0 {
			r, ok = out, true
		}
	})
	return r, ok
}
