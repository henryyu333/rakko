//go:build windows

package main

// The first-launch guide on Windows: the app is always settled where the
// installer or the archive put it, so there is no move step, and Windows
// has no system-permission grants, so there is no permissions step either.
// Every install answered here is originInstalled.
func onboardingOrigin() runOrigin { return originInstalled }

// onboardingMove is the macOS bundle move. The Windows guide never offers
// the step, so the action is unreachable; it stays for the shared button
// path.
func (a *shelf) onboardingMove() {}
