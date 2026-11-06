//go:build darwin

package mac

import (
	"testing"
)

// The stale-record reset runs on the first failed preflight, before the
// user can grant — never on a 「打开系统设置」 click:
// CGPreflightScreenCaptureAccess caches its false answer until relaunch,
// so a click after the user granted in Settings still reads false, and a
// reset there would wipe the grant it was meant to repair. The seams
// stub every spawned binary and the CG calls.
func TestScreenCaptureResetRunsOnFailedPreflight(t *testing.T) {
	var resets, opens, requests int
	oldReset, oldOpen, oldID := tccutilReset, openURL, requestBundleID
	oldPre, oldReq := cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess
	oldTitles := windowTitlesProveGrant
	oldDone := tccutilResetDone.Swap(true)
	preflightOnce.Do(func() {}) // consume the registration once; the stubs below stand in
	tccutilReset = func(string) { resets++ }
	openURL = func(string) { opens++ }
	requestBundleID = func() string { return "com.test.shelf" }
	cgPreflightScreenCaptureAccess = func() bool { return false }
	cgRequestScreenCaptureAccess = func() bool { requests++; return false }
	windowTitlesProveGrant = func() bool { return false }
	tccutilResetDone.Store(false)
	t.Cleanup(func() {
		tccutilReset, openURL, requestBundleID = oldReset, oldOpen, oldID
		cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess = oldPre, oldReq
		windowTitlesProveGrant = oldTitles
		tccutilResetDone.Store(oldDone)
	})

	if ScreenCaptureAllowed() {
		t.Fatal("untrusted stub reported trusted")
	}
	if resets != 1 {
		t.Fatalf("failed preflight did not reset the stale record: resets=%d", resets)
	}
	// Same process, user grants in Settings then clicks 打开系统设置:
	// preflight still answers false (process cache) — the request asks
	// and opens the pane but never resets again.
	if RequestScreenCapture() {
		t.Fatal("untrusted stub reported trusted")
	}
	if resets != 1 {
		t.Fatalf("the grant button reset the fresh grant: resets=%d", resets)
	}
	if requests != 1 || opens != 1 {
		t.Fatalf("request skipped request/open: requests=%d opens=%d", requests, opens)
	}
}

// A grant made in System Settings after this process's first (cached)
// preflight must survive. WindowServer handing over other apps' window
// titles proves the grant is live, so the stale-record reset would delete
// the very grant it was meant to repair: no reset runs, and the caller is
// told the grant is not usable yet (the cached answer is stale) so it
// shows 「重新打开 ShelfU 后生效」.
func TestScreenCaptureResetSparesLiveGrant(t *testing.T) {
	var resets, opens, requests int
	oldReset, oldOpen, oldID := tccutilReset, openURL, requestBundleID
	oldPre, oldReq := cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess
	oldTitles := windowTitlesProveGrant
	oldDone := tccutilResetDone.Swap(true)
	preflightOnce.Do(func() {})
	tccutilReset = func(string) { resets++ }
	openURL = func(string) { opens++ }
	requestBundleID = func() string { return "com.test.shelf" }
	// The grant landed after an earlier read-only preflight: this
	// process's cached answer is still false.
	cgPreflightScreenCaptureAccess = func() bool { return false }
	cgRequestScreenCaptureAccess = func() bool { requests++; return true }
	windowTitlesProveGrant = func() bool { return true }
	tccutilResetDone.Store(false)
	t.Cleanup(func() {
		tccutilReset, openURL, requestBundleID = oldReset, oldOpen, oldID
		cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess = oldPre, oldReq
		windowTitlesProveGrant = oldTitles
		tccutilResetDone.Store(oldDone)
	})

	if ScreenCaptureAllowed() {
		t.Fatal("a request path must not report the cached-false answer as allowed")
	}
	if resets != 0 {
		t.Fatalf("the live grant was cleared: resets=%d", resets)
	}
	// Nothing needed the one-shot reset, so it stays armed: a genuinely
	// stale record later in this process can still be cleared once.
	if tccutilResetDone.Load() {
		t.Fatal("the reset was consumed although the grant was proven live")
	}

	// The same call from the request path: no reset, no second request —
	// the grant exists — but the pane still opens so the user sees the
	// switch is on.
	if RequestScreenCapture() {
		t.Fatal("a live-but-cached grant reported the request as already active")
	}
	if resets != 0 {
		t.Fatalf("the request path cleared the live grant: resets=%d", resets)
	}
	if requests != 0 {
		t.Fatalf("the request path asked for a grant that is already live: requests=%d", requests)
	}
	if opens != 1 {
		t.Fatalf("the request path did not open the 屏幕录制 pane: opens=%d", opens)
	}
}

// The read-only preflight used by the Settings/menu status display never
// clears a record, even while it reports false.
func TestScreenCapturePreflightHasNoSideEffects(t *testing.T) {
	var resets, opens, requests, probes int
	oldReset, oldOpen := tccutilReset, openURL
	oldPre, oldReq := cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess
	oldTitles := windowTitlesProveGrant
	preflightOnce.Do(func() {})
	tccutilReset = func(string) { resets++ }
	openURL = func(string) { opens++ }
	cgPreflightScreenCaptureAccess = func() bool { return false }
	cgRequestScreenCaptureAccess = func() bool { requests++; return false }
	windowTitlesProveGrant = func() bool { probes++; return true }
	t.Cleanup(func() {
		tccutilReset, openURL = oldReset, oldOpen
		cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess = oldPre, oldReq
		windowTitlesProveGrant = oldTitles
	})

	if ScreenCapturePreflight() {
		t.Fatal("untrusted stub reported trusted")
	}
	if resets != 0 || opens != 0 || requests != 0 || probes != 0 {
		t.Fatalf("preflight ran side effects: resets=%d opens=%d requests=%d probes=%d",
			resets, opens, requests, probes)
	}
}

// An already-trusted process never resets, asks or opens anything.
func TestRequestScreenCaptureTrustedShortCircuits(t *testing.T) {
	var resets, opens, requests int
	oldReset, oldOpen := tccutilReset, openURL
	oldPre, oldReq := cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess
	oldTitles := windowTitlesProveGrant
	preflightOnce.Do(func() {})
	tccutilReset = func(string) { resets++ }
	openURL = func(string) { opens++ }
	cgPreflightScreenCaptureAccess = func() bool { return true }
	cgRequestScreenCaptureAccess = func() bool { requests++; return true }
	windowTitlesProveGrant = func() bool { requests++; return false }
	t.Cleanup(func() {
		tccutilReset, openURL = oldReset, oldOpen
		cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess = oldPre, oldReq
		windowTitlesProveGrant = oldTitles
	})

	if !RequestScreenCapture() {
		t.Fatal("trusted stub reported untrusted")
	}
	if resets != 0 || requests != 0 || opens != 0 {
		t.Fatalf("trusted path ran side effects: resets=%d requests=%d opens=%d", resets, requests, opens)
	}
}
