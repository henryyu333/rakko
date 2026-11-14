//go:build windows

package platform

// Screen capture. Capturing is its own later batch; the calls stay
// explicit failures so the UI reports the missing feature instead of
// silently doing nothing. WINDOWS-NEXT(capture)

// CaptureDisplay freezes display index as a PNG at path.
// WINDOWS-NEXT(capture)
func CaptureDisplay(index int, path string) error { return errDomain("capture") }

// PickScreenRegion runs the system's region picker. WINDOWS-NEXT(capture)
func PickScreenRegion(path string) error { return errDomain("capture") }
