//go:build windows

package platform

// The app paths that do not need a foreign app: this process's own
// windows stand in for the enumeration rules.

import (
	"testing"
	"time"
)

// TestTopWindowEnumerationReusesOneCallback runs far more enumerations
// than the Go callback table holds. A callback registered per call leaks
// its thunk — Go never releases one — so the old code ran the table dry
// and crashed here.
func TestTopWindowEnumerationReusesOneCallback(t *testing.T) {
	for range 3000 {
		topWindowOf(OwnPID())
	}
}

// TestAppIDOfKeepsEachAppsOwnID pins why a package family is not an
// identity: one family can publish several apps, and each process must
// keep its own AppUserModelID rather than the family's first entry.
func TestAppIDOfKeepsEachAppsOwnID(t *testing.T) {
	const family = "Microsoft.WindowsCalculator_8wekyb3d8bbwe"
	ids := map[int]string{
		101: family + "!App",
		202: family + "!Secondary",
	}
	old := appAUMID
	defer func() { appAUMID = old }()
	appAUMID = func(pid int) string { return ids[pid] }

	first, second := appIDOf(101), appIDOf(202)
	if first != family+"!App" || second != family+"!Secondary" {
		t.Fatalf("appIDOf = %q and %q, want each process's own AUMID", first, second)
	}
	if first == second {
		t.Fatal("two apps of one package collapsed into one identity")
	}
	if id := appIDOf(OwnPID()); id == "" {
		t.Fatal("a desktop process lost its executable identity")
	}
}

// TestIdentityLookupCost measures the identity lookups the weekly brief's
// once-a-second foreground poll makes: the first call per process asks the
// system, the rest are one creation-time query and a map hit.
func TestIdentityLookupCost(t *testing.T) {
	const calls = 20000
	start := time.Now()
	for range calls {
		if id := processAUMID(OwnPID()); id != "" {
			t.Fatalf("the test binary reported the AppUserModelID %q", id)
		}
	}
	cached := time.Since(start) / calls
	t.Logf("processAUMID, memoized: %v per call (%d calls)", cached, calls)

	start = time.Now()
	for range 5000 {
		FrontmostApp()
	}
	perCall := time.Since(start) / 5000
	t.Logf("FrontmostApp: %v per call (5000 calls)", perCall)
	if cached > time.Millisecond || perCall > time.Millisecond {
		t.Fatalf("identity lookups cost %v cached and %v through FrontmostApp; a once-a-second poll cannot afford that", cached, perCall)
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
