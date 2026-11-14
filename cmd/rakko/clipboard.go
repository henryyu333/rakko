package main

import (
	"strings"

	"rakko/internal/platform"
)

// The four clipboard states, named as the module reads them. The
// classification itself, including the private-content markers, belongs to
// the platform files.
const (
	clipNotReady = platform.ClipboardNotReady
	clipSkip     = platform.ClipboardSkip
	clipText     = platform.ClipboardText
	clipImage    = platform.ClipboardImage
)

// clipboardExcluded reports whether a copy is from an app the user chose
// not to record: appID is compared to the excludedApps setting without
// case, as app identifiers compare on macOS. An unknown source
// ("" — the system reported no frontmost app) is never excluded.
func clipboardExcluded(appID string, excluded []string) bool {
	if appID == "" {
		return false
	}
	for _, id := range excluded {
		if strings.EqualFold(strings.TrimSpace(id), appID) {
			return true
		}
	}
	return false
}

// clipCapture decides what one clipboard change records: nothing from an
// app the user excluded, otherwise whatever the change's declared types
// say (see platform.ClassifyClipboard).
func clipCapture(types []string, sourceApp string, excluded []string) platform.ClipboardKind {
	if clipboardExcluded(sourceApp, excluded) {
		return clipSkip
	}
	return platform.ClassifyClipboard(types)
}

// captureSource is the provenance stamped on a capture: the app ID of the
// app that was frontmost when the clipboard changed, or "" when the source
// is unknown — including when Rakko itself is frontmost, which only happens
// while the keyboard search is open and must not record itself. Pure;
// unit-tested.
func captureSource(frontPID, ownPID int, appID string) string {
	if frontPID == ownPID {
		return ""
	}
	return appID
}
