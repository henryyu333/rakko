//go:build darwin

package main

// Single instance on macOS comes from LaunchServices and the data
// directory's lock: a second launch activates the running app instead of
// starting a process, so there is nothing to request and no second-instance
// callback to register.
func requestSingleInstanceLock() bool { return true }

func registerSecondInstance(fn func()) {}
