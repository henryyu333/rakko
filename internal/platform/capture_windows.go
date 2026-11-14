//go:build windows

package platform

// Screen capture and preview. Both are their own later batches; the calls
// stay explicit failures so the UI reports the missing feature instead of
// silently doing nothing. WINDOWS-NEXT(capture), WINDOWS-NEXT(preview)

// CaptureDisplay freezes display index as a PNG at path.
// WINDOWS-NEXT(capture)
func CaptureDisplay(index int, path string) error { return errDomain("capture") }

// PickScreenRegion runs the system's region picker. WINDOWS-NEXT(capture)
func PickScreenRegion(path string) error { return errDomain("capture") }

// Preview shows the quick preview panel for paths. WINDOWS-NEXT(preview)
func Preview(paths []string) error { return errDomain("preview") }

// PreviewVisible reports whether the preview panel is open.
// WINDOWS-NEXT(preview)
func PreviewVisible() bool { return false }

// ClosePreview closes the preview panel. WINDOWS-NEXT(preview)
func ClosePreview() {}

// ShareNearby opens the system share sheet for files and texts.
// WINDOWS-NEXT(share)
func ShareNearby(paths []string, texts []string) error { return errDomain("share") }
