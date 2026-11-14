//go:build windows

package platform

// The Explorer entries for "添加到 Rakko": the context-menu verb and the
// SendTo shortcut. Windows has no service provider like macOS' NSServices:
// Explorer starts this executable with the selected paths on the command
// line, and a running Rakko receives them through its single-instance
// hand-off. The registry entries and the SendTo shortcut belong to the
// installer and the uninstaller (installer/windows.nsh), so a dev or
// portable binary never rewrites the installed app's menu to point at
// itself.

import "sync"

// serviceLaunch is one launch's context-menu payload waiting for the app
// to open its store, and the receiver once ready installed it.
var serviceLaunch struct {
	mu      sync.Mutex
	pending []string
	fn      func(paths []string, text string)
}

// ConsumeServiceArgs removes a service invocation's arguments from args,
// queues the selected paths, and returns the arguments the app's own flag
// parsing should see. Explorer puts the paths after the verb and nothing
// else on the command line; a launch without the verb is returned as is.
//
// The paths are handed to the receiver at once when the app is ready (the
// second-instance hand-off), and held until InstallAddToRakko otherwise
// (a cold start, where main consumes them before the store exists).
// Main thread only, except a cold start before the app's loop starts.
func ConsumeServiceArgs(args []string) []string {
	for i, a := range args {
		if a != "--add-to-rakko" {
			continue
		}
		rest := append([]string(nil), args[:i]...)
		paths := append([]string(nil), args[i+1:]...)
		serviceLaunch.mu.Lock()
		serviceLaunch.pending = append(serviceLaunch.pending, paths...)
		var pending []string
		fn := serviceLaunch.fn
		if fn != nil {
			pending, serviceLaunch.pending = serviceLaunch.pending, nil
		}
		serviceLaunch.mu.Unlock()
		if fn != nil && len(pending) > 0 {
			fn(pending, "")
		}
		return rest
	}
	return args
}

// InstallAddToRakko installs the receiver for Explorer's invocations and
// hands it anything a cold start queued before the app was ready. The
// shell entries themselves are the installer's, so there is nothing to
// register here. fn receives the payload on the main thread.
func InstallAddToRakko(fn func(paths []string, text string)) error {
	serviceLaunch.mu.Lock()
	serviceLaunch.fn = fn
	pending := serviceLaunch.pending
	serviceLaunch.pending = nil
	serviceLaunch.mu.Unlock()
	if fn != nil && len(pending) > 0 {
		fn(pending, "")
	}
	return nil
}
