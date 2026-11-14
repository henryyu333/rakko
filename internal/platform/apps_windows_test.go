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

// TestMinimizedWindowStillNamesTheApp pins the switcher predicate: an app
// whose windows are all minimized must stay in the running list, and
// activation must find its window to restore it.
func TestMinimizedWindowStillNamesTheApp(t *testing.T) {
	h := testSnapWindow(t) // skips when there is no interactive desktop
	procShowWindow.Call(h, 6 /* SW_MINIMIZE */)
	if r, _, _ := procIsIconic.Call(h); r == 0 {
		t.Skip("the test window did not minimize")
	}
	if !appWindow(h) {
		t.Fatal("a minimized app window is not listed as an app")
	}
	if ordinaryWindow(h) {
		t.Fatal("ordinaryWindow kept a minimized window; the screen-visible list must drop it")
	}
	// The window under test has to be this process's first match, not
	// another window of the test binary.
	procSetWindowPos.Call(h, hwndTop, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	if got := topWindowOf(OwnPID()); got != h {
		t.Fatalf("topWindowOf(%d) = %#x, want the minimized test window %#x", OwnPID(), got, h)
	}
}
