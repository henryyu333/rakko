//go:build windows

package main

// The Windows counterpart of the "添加到 Rakko" shell service. Explorer's
// context-menu entry is part of the services batch; the tray keeps working
// through drag, paste and the clipboard module meanwhile.

// takeServiceOwnership ends a service flash's ownership of the panel. No
// service can flash it on Windows yet, so the counter only exists to keep
// the shared gesture path compiling.
func (a *shelf) takeServiceOwnership() { serviceFlashGen++ }

// serviceFlashGen is the generation counter a service flash would bump.
var serviceFlashGen int

// installServices registers the Explorer entry. WINDOWS-NEXT(services)
func (a *shelf) installServices() {}
