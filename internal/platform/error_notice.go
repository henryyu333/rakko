package platform

// The app's error notice for work that finishes after the call that started
// it. Platform calls return their own errors, but some of them finish later on
// the main thread (a share whose files are still being opened, for example);
// those failures go to the same place cmd/rakko sends the synchronous ones.

import "sync"

var errorNotice struct {
	mu sync.Mutex
	fn func(error)
}

// InstallErrorNotice registers fn as the process-wide error notice. The app
// installs it once at startup; a later call replaces it.
func InstallErrorNotice(fn func(error)) {
	errorNotice.mu.Lock()
	errorNotice.fn = fn
	errorNotice.mu.Unlock()
}

// reportError hands err to the installed notice. A platform area calls it on
// the main thread for a failure its caller can no longer return.
func reportError(err error) {
	if err == nil {
		return
	}
	errorNotice.mu.Lock()
	fn := errorNotice.fn
	errorNotice.mu.Unlock()
	if fn != nil {
		fn(err)
	}
}
