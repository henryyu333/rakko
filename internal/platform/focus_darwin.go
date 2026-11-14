//go:build darwin

package platform

// IsKeyWindow reports whether the window (a native NSWindow handle) is the
// key window, i.e. the one that receives keyboard events. The clipboard
// search flow checks it after focusing the shelf: an NSPanel with the
// nonactivating style can be key without its app being active, so this is
// the value that tells whether typing will reach the search field.
// Read-only. Main thread only.
func IsKeyWindow(window uintptr) bool {
	if window == 0 {
		return false
	}
	if err := Init(); err != nil {
		return false
	}
	var key bool
	withPool(func() {
		key = sendInt(id(window), "isKeyWindow") != 0
	})
	return key
}

// OnMainThread reports whether the caller is on the process's main thread
// (NSThread.isMainThread). AppKit allows window work only there; the
// headless test harness runs tests on a different thread, so callers that
// would otherwise create AppKit objects check this first.
func OnMainThread() bool {
	if err := Init(); err != nil {
		return false
	}
	return sendInt(class("NSThread"), "isMainThread") != 0
}
