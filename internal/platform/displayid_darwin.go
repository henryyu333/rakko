//go:build darwin

package platform

// StableDisplayID returns the display's persistent identity. A Mac's
// CGDirectDisplayID is already stable across launches, so the answer is the
// id itself.
func StableDisplayID(id int64) int64 { return id }
