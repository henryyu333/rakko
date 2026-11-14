//go:build windows

package platform

// The app paths that do not need a foreign app: this process's own
// windows stand in for the enumeration rules.

import "testing"

// TestTopWindowEnumerationReusesOneCallback runs far more enumerations
// than the Go callback table holds. A callback registered per call leaks
// its thunk — Go never releases one — so the old code ran the table dry
// and crashed here.
func TestTopWindowEnumerationReusesOneCallback(t *testing.T) {
	for range 3000 {
		topWindowOf(OwnPID())
	}
}
