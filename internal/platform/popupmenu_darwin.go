//go:build darwin

package platform

// WatchOwnMenus does nothing: AppKit draws popup menus above the panel's
// window level already.
func WatchOwnMenus() {}
