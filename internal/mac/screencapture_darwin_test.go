//go:build darwin

package mac

import (
	"testing"
)

// The row's direct entry opens exactly the 屏幕录制 pane.
func TestOpenScreenCapturePane(t *testing.T) {
	oldOpen := openURL
	var got string
	openURL = func(url string) { got = url }
	t.Cleanup(func() { openURL = oldOpen })
	OpenScreenCapturePane()
	if got != "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture" {
		t.Fatalf("screen capture pane = %q", got)
	}
}

// The read-only preflight used by Settings, the tray and the OCR start never
// prompts, asks or clears anything, even while it reports false.
func TestScreenCapturePreflightHasNoSideEffects(t *testing.T) {
	var opens, requests, probes int
	oldOpen := openURL
	oldPre, oldReq := cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess
	oldTitles := windowTitlesProveGrant
	preflightOnce.Do(func() {})
	openURL = func(string) { opens++ }
	cgPreflightScreenCaptureAccess = func() bool { return false }
	cgRequestScreenCaptureAccess = func() bool { requests++; return false }
	windowTitlesProveGrant = func() bool { probes++; return true }
	t.Cleanup(func() {
		openURL = oldOpen
		cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess = oldPre, oldReq
		windowTitlesProveGrant = oldTitles
	})

	if ScreenCapturePreflight() {
		t.Fatal("untrusted stub reported trusted")
	}
	if opens != 0 || requests != 0 || probes != 0 {
		t.Fatalf("preflight ran side effects: opens=%d requests=%d probes=%d", opens, requests, probes)
	}
}

// A missing grant asks macOS once and opens the pane. It never resets the
// app's TCC record: that is the user's decision, and a reset could delete a
// grant made in this same session.
func TestRequestScreenCaptureAsksAndOpensWithoutReset(t *testing.T) {
	var opens, requests int
	oldOpen := openURL
	oldPre, oldReq := cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess
	oldTitles := windowTitlesProveGrant
	preflightOnce.Do(func() {})
	openURL = func(string) { opens++ }
	cgPreflightScreenCaptureAccess = func() bool { return false }
	cgRequestScreenCaptureAccess = func() bool { requests++; return false }
	windowTitlesProveGrant = func() bool { return false }
	t.Cleanup(func() {
		openURL = oldOpen
		cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess = oldPre, oldReq
		windowTitlesProveGrant = oldTitles
	})

	if RequestScreenCapture() {
		t.Fatal("untrusted stub reported trusted")
	}
	if requests != 1 || opens != 1 {
		t.Fatalf("request skipped ask/open: requests=%d opens=%d", requests, opens)
	}
	// A second request in the same process asks again rather than clearing
	// the record a first one might have created.
	if RequestScreenCapture() {
		t.Fatal("untrusted stub reported trusted")
	}
	if requests != 2 || opens != 2 {
		t.Fatalf("second request skipped ask/open: requests=%d opens=%d", requests, opens)
	}
}

// A grant proven by WindowServer is live even while this process's cached
// preflight still says no. The request must not re-register or reset it; it
// only opens the pane, and the caller's UI asks for a relaunch.
func TestScreenCaptureLiveGrantOnlyOpensPane(t *testing.T) {
	var opens, requests int
	oldOpen := openURL
	oldPre, oldReq := cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess
	oldTitles := windowTitlesProveGrant
	preflightOnce.Do(func() {})
	openURL = func(string) { opens++ }
	cgPreflightScreenCaptureAccess = func() bool { return false }
	cgRequestScreenCaptureAccess = func() bool { requests++; return true }
	windowTitlesProveGrant = func() bool { return true }
	t.Cleanup(func() {
		openURL = oldOpen
		cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess = oldPre, oldReq
		windowTitlesProveGrant = oldTitles
	})

	if RequestScreenCapture() {
		t.Fatal("a live-but-cached grant reported the request as already active")
	}
	if requests != 0 {
		t.Fatalf("the request path asked for a grant that is already live: requests=%d", requests)
	}
	if opens != 1 {
		t.Fatalf("the request path did not open the 屏幕录制 pane: opens=%d", opens)
	}
	if !ScreenCaptureGranted() {
		t.Fatal("the uncached grant proof was not reported")
	}
}

// An already-trusted process never asks or opens anything.
func TestRequestScreenCaptureTrustedShortCircuits(t *testing.T) {
	var opens, requests, probes int
	oldOpen := openURL
	oldPre, oldReq := cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess
	oldTitles := windowTitlesProveGrant
	preflightOnce.Do(func() {})
	openURL = func(string) { opens++ }
	cgPreflightScreenCaptureAccess = func() bool { return true }
	cgRequestScreenCaptureAccess = func() bool { requests++; return true }
	windowTitlesProveGrant = func() bool { probes++; return false }
	t.Cleanup(func() {
		openURL = oldOpen
		cgPreflightScreenCaptureAccess, cgRequestScreenCaptureAccess = oldPre, oldReq
		windowTitlesProveGrant = oldTitles
	})

	if !RequestScreenCapture() {
		t.Fatal("trusted stub reported untrusted")
	}
	if requests != 0 || opens != 0 || probes != 0 {
		t.Fatalf("trusted path ran side effects: requests=%d opens=%d probes=%d", requests, opens, probes)
	}
}
