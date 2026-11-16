//go:build darwin

package platform

// WatchOwnMenus does nothing: AppKit draws popup menus above the panel's
// window level already. window is the overlay's handle, which the macOS
// side never needs.
func WatchOwnMenus(window uintptr) {}

// MenuOpen reports whether window's UI thread is inside a native popup menu loop.
// AppKit runs NSMenu tracking in its own event-tracking mode, so callers do not
// need to suppress owner repaints there.
func MenuOpen(window uintptr) bool { return false }
