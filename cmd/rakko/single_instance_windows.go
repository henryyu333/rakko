//go:build windows

package main

// Single instance on Windows: mygo's named lock makes the second launch
// hand its arguments to the running app and exit, and the running app gets
// the same "open the panel" the Mac build gets from AppKit's reopen event.

import "github.com/egoist/mygo"

func requestSingleInstanceLock() bool { return mygo.App.RequestSingleInstanceLock() }

func registerSecondInstance(fn func()) {
	mygo.App.OnSecondInstance(func(args []string, wd string) {
		mygo.RunOnMain(fn)
	})
}
