//go:build windows

package main

// The default pinned apps have to speak the same identity language as the
// running-app snapshot, or a running app appears twice: once in its pinned
// seat and once in the running wing.

import (
	"strings"
	"testing"

	"rakko/internal/platform"
)

func TestDefaultPinnedCalculatorUsesThePackageIdentity(t *testing.T) {
	pinnedPackage, pinnedStub := false, false
	for _, id := range defaultPinnedApps {
		switch platform.AppIDKey(id) {
		case platform.AppIDKey(calculatorAppID):
			pinnedPackage = true
		case platform.AppIDKey(`C:\Windows\System32\calc.exe`):
			pinnedStub = true
		}
	}
	if !pinnedPackage {
		t.Fatalf("the defaults do not pin %q: %v", calculatorAppID, defaultPinnedApps)
	}
	if pinnedStub {
		t.Fatalf("the defaults pin the Calculator launcher stub instead of the package: %v", defaultPinnedApps)
	}
	for _, id := range defaultPinnedApps {
		if strings.HasSuffix(strings.ToLower(id), `\calc.exe`) {
			t.Fatalf("a Calculator launcher stub reached the defaults: %q", id)
		}
	}
}

// TestDefaultCalculatorPinMatchesTheRunningApp needs Calculator running
// (the desk evidence starts it): a running Calculator must fall into its
// pinned seat rather than appear a second time.
func TestDefaultCalculatorPinMatchesTheRunningApp(t *testing.T) {
	key := platform.AppIDKey(calculatorAppID)
	running := false
	for _, r := range platform.RunningApps() {
		if platform.AppIDKey(r.AppID) == key {
			running = true
			break
		}
	}
	if !running {
		t.Skip("Calculator is not running on this desktop")
	}
	for _, id := range defaultPinnedApps {
		if platform.AppIDKey(id) == key {
			return
		}
	}
	t.Fatalf("Calculator is running as %q but no default pin matches it: %v", calculatorAppID, defaultPinnedApps)
}
