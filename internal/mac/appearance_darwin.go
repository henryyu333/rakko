//go:build darwin

package mac

import "github.com/ebitengine/purego/objc"

// ReduceTransparency reports the system's Reduce Transparency setting.
func ReduceTransparency() bool {
	ws := send(class("NSWorkspace"), "sharedWorkspace")
	return send(ws, "accessibilityDisplayShouldReduceTransparency") != 0
}

// ObserveAccessibilityChange reports the system's display-option changes
// (Reduce Transparency and the other accessibility display settings) on the
// main thread. The panel otherwise polls this setting on a slow tick.
func ObserveAccessibilityChange(changed func()) (stop func()) {
	if err := Init(); err != nil {
		return func() {}
	}
	var center, token id
	withPool(func() {
		ws := send(class("NSWorkspace"), "sharedWorkspace")
		center = send(ws, "notificationCenter")
		block := objc.NewBlock(func(_ objc.Block, _ id) { changed() })
		defer block.Release()
		token = send(center, "addObserverForName:object:queue:usingBlock:",
			uintptr(nsString("NSWorkspaceAccessibilityDisplayOptionsDidChangeNotification")),
			0, uintptr(send(class("NSOperationQueue"), "mainQueue")), uintptr(block))
		send(token, "retain")
	})
	return func() {
		withPool(func() {
			if token != 0 {
				send(center, "removeObserver:", uintptr(token))
				send(token, "release")
				token = 0
			}
		})
	}
}
