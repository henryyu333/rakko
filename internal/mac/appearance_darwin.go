//go:build darwin

package mac

// ReduceTransparency reports the system's Reduce Transparency setting.
func ReduceTransparency() bool {
	ws := send(class("NSWorkspace"), "sharedWorkspace")
	return send(ws, "accessibilityDisplayShouldReduceTransparency") != 0
}
