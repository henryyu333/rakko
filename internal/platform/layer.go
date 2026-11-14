package platform

// Layer is a window's stacking band, named for what sits there rather than
// for one system's window-level constants. Shared code picks a band; the
// platform files map it to their own numbers.
type Layer int

const (
	// LayerStatus rests above the menu bar so the shelf can occupy the
	// screen's top edge.
	LayerStatus Layer = iota

	// LayerBelowStatus is the idle island capsule, just under the shelf.
	LayerBelowStatus

	// LayerSearchKeyboard is where the overlay sits while the clipboard
	// search has the keyboard. Input-method candidate panels are drawn by
	// the input method's own process low in the stack; any band above them
	// leaves the candidates completely behind the opaque panel they are
	// anchored inside, so this band sits below them yet above torn-off
	// menus and floating palettes.
	LayerSearchKeyboard

	// LayerPopUpMenu is used while a file drag needs the overlay above the
	// menu bar's exclusive strip.
	LayerPopUpMenu

	// LayerCapture covers the menu bar and the Dock, not just ordinary
	// windows.
	LayerCapture

	// LayerFloating is the ordinary floating palette band.
	LayerFloating
)
