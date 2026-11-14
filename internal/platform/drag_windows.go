//go:build windows

package platform

// Drag session probes and the notch's drag guard. The skeleton has the
// pointer state the shell needs (left button, drag events) but not yet the
// OLE drag session it is dragging, nor the cursor-corridor filter; the
// next batch owns this file.

// DragGuardRegion is the notch's protected approach corridor, in screen
// DIPs. The Windows build does not clamp the cursor there yet.
type DragGuardRegion struct{ X, Y, Width, Height int }

// CanSendInput reports whether this process may post synthetic input.
// Windows has no consent gate like macOS Accessibility, so it is always
// true.
func CanSendInput() bool { return true }

// RequestAccessibility is a no-op on Windows: there is no permission to
// grant, and the call only stays because the settings copy is shared.
func RequestAccessibility() bool { return true }

// StartDragGuard installs the notch's cursor-corridor filter.
// WINDOWS-NEXT(drag): the OLE drag session and the corridor clamp arrive
// with the drag-out batch; the call succeeds with an inert stop so the
// shared gesture code keeps working.
func StartDragGuard(region DragGuardRegion) (func(), error) {
	return func() {}, nil
}

// SetDragGuardRegion updates the corridor of a running guard.
// WINDOWS-NEXT(drag)
func SetDragGuardRegion(region DragGuardRegion) {}

// ParkDragGuard parks the corridor filter for the app's own island gesture.
// WINDOWS-NEXT(drag)
func ParkDragGuard(parked bool) {}

// SetDragPin keeps the cursor out of region during a file drag.
// WINDOWS-NEXT(drag)
func SetDragPin(region DragGuardRegion, active bool) {}

// DragState snapshots whether a system drag session is in flight.
// WINDOWS-NEXT(drag): the state machine over the OLE session starts with
// the drag batch.
func DragState() (active bool, serial int64) { return false, 0 }

// DragTypes returns the drag session's declared formats.
// WINDOWS-NEXT(drag)
func DragTypes() []string { return nil }

// DragFiles returns the file paths of the drag session in flight.
// WINDOWS-NEXT(drag)
func DragFiles() []string { return nil }

// DragFilesChange is the serial that marks the drag board's last rewrite.
// WINDOWS-NEXT(drag)
func DragFilesChange() int64 { return 0 }

// DragHasFiles reports whether the drag declares whole files.
// WINDOWS-NEXT(drag)
func DragHasFiles(types []string) bool { return false }

// DragHasFilePromise reports whether the drag promises files it writes on
// drop. Windows drag sources hand real files over, so this never fires.
func DragHasFilePromise(types []string) bool { return false }

// DragHasImages reports whether the drag carries image data.
// WINDOWS-NEXT(drag)
func DragHasImages(types []string) bool { return false }
