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

// TestAppIDForFamilyPicksTheAppsFolderEntry pins the process→package→
// identity bridge: the family name must match only the entry whose
// AppUserModelID belongs to that exact package, not a longer sibling.
func TestAppIDForFamilyPicksTheAppsFolderEntry(t *testing.T) {
	entries := []appEntry{
		{Name: "计算器", Target: `shell:AppsFolder\Microsoft.WindowsCalculator_8wekyb3d8bbwe!App`},
		{Name: "计算器预览", Target: `shell:AppsFolder\Microsoft.WindowsCalculatorBeta_8wekyb3d8bbwe!App`},
		{Name: "notepad", Target: `C:\Windows\System32\notepad.exe`},
	}
	for _, tt := range []struct{ family, want string }{
		{"Microsoft.WindowsCalculator_8wekyb3d8bbwe", "Microsoft.WindowsCalculator_8wekyb3d8bbwe!App"},
		{"microsoft.windowscalculator_8wekyb3d8bbwe", "Microsoft.WindowsCalculator_8wekyb3d8bbwe!App"},
		{"Microsoft.WindowsCalculatorBeta_8wekyb3d8bbwe", "Microsoft.WindowsCalculatorBeta_8wekyb3d8bbwe!App"},
		{"Microsoft.WindowsCalculator", ""},
		{"", ""},
	} {
		if got := appIDForFamily(entries, tt.family); got != tt.want {
			t.Errorf("appIDForFamily(%q) = %q, want %q", tt.family, got, tt.want)
		}
	}
}

// TestRunningAppsNamesAHostedPackagedApp needs a packaged app running: it
// starts no app, because the desk evidence starts Calculator first. A UWP
// app's window belongs to ApplicationFrameHost, so the snapshot must still
// report the app's own package identity rather than the frame host.
func TestRunningAppsNamesAHostedPackagedApp(t *testing.T) {
	const calculator = "Microsoft.WindowsCalculator_8wekyb3d8bbwe!App"
	for _, r := range RunningApps() {
		if r.AppID != calculator {
			continue
		}
		if r.Name == "" || r.PID == 0 {
			t.Fatalf("Calculator is listed as %+v", r)
		}
		if h := topWindowOf(r.PID); h == 0 {
			t.Fatalf("topWindowOf(%d) found no window for Calculator", r.PID)
		}
		return
	}
	t.Skip("Calculator is not running on this desktop")
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
