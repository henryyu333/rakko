//go:build darwin

package platform

// WatchOwnMenus does nothing: AppKit draws popup menus above the panel's
// window level already. window is the overlay's handle, which the macOS
// side never needs.
func WatchOwnMenus(window uintptr) {}
