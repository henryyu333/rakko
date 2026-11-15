//go:build windows

package platform

// The drag session probes the shelf's monitor reads. Windows has no
// top-edge system gesture (like macOS Spaces) that steals a drag near the
// screen's top edge, so the drag-guard corridor is a no-op here and never
// clamps or warps the user's cursor.
//
// Windows exposes no API for another process's OLE drag: the data object
// stays in the source. What it does expose is the capture window OLE puts
// on the source's thread for the duration of DoDragDrop, whose class is
// CLIPBRDWNDCLASS. A held left button plus that capture is a drag, for
// Explorer, browsers and every other OLE source alike.
//
// The formats and file paths of that drag are not readable from outside
// the source. DragTypes and DragFiles therefore stay empty before a drop;
// the window that takes the drop still learns its formats from mygo's own
// OLE target, and the drop itself carries the files.

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DragGuardRegion is the notch's protected approach corridor, in screen
// DIPs.
type DragGuardRegion struct{ X, Y, Width, Height int }

// CanSendInput reports whether this process may post synthetic input.
// Windows has no consent gate like macOS Accessibility, so it is always
// true.
func CanSendInput() bool { return true }

// RequestAccessibility is a no-op on Windows: there is no permission to
// grant, and the call only stays because the settings copy is shared.
func RequestAccessibility() bool { return true }

var (
	procGetGUIThreadInfo = sysProc(user32, "GetGUIThreadInfo")
)

type guiThreadInfo struct {
	cbSize        uint32
	flags         uint32
	hwndActive    uintptr
	hwndFocus     uintptr
	hwndCapture   uintptr
	hwndMenuOwner uintptr
	hwndMoveSize  uintptr
	hwndCaret     uintptr
	rcCaret       [4]int32
}

// dragCapture is the capture window OLE's drag loop holds. The class is
// ole32's hidden clipboard/drag window, shared by every OLE drag source.
const oleDragCaptureClass = "CLIPBRDWNDCLASS"

// captureWindow is the foreground thread's capture window, 0 when none.
// GetGUIThreadInfo with a zero thread id answers for the thread that owns
// the active window, exactly where DoDragDrop runs.
func captureWindow() uintptr {
	var gti guiThreadInfo
	gti.cbSize = uint32(unsafe.Sizeof(gti))
	if r, _, _ := procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&gti))); r == 0 {
		return 0
	}
	return gti.hwndCapture
}

func windowClass(h uintptr) string {
	var buf [128]uint16
	n, _, _ := procGetClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// dragSession tracks the OLE drag in flight. Only one drag can be active
// at a time, so the state lives on the package; serial increments once per
// drag generation, which is what the shelf uses to tell a new drag from
// the one it is already tracking.
var dragSession struct {
	mu     sync.Mutex
	active bool
	serial int64
}

// DragState snapshots whether a system drag session is in flight. Main
// thread only.
func DragState() (active bool, serial int64) {
	down := leftHeld()
	capture := captureWindow()
	dragging := down && capture != 0 && windowClass(capture) == oleDragCaptureClass
	dragSession.mu.Lock()
	defer dragSession.mu.Unlock()
	if dragging && !dragSession.active {
		dragSession.serial++
	}
	dragSession.active = dragging
	return dragSession.active, dragSession.serial
}

// DragTypes returns the drag session's declared formats. Windows keeps the
// data object inside the source process, so no format list is readable
// here; the drop target of the window under the pointer still negotiates
// them for the drop itself.
func DragTypes() []string { return nil }

// DragFiles returns the file paths of the drag session in flight. As
// DragTypes explains, the source's data object is not reachable from
// another process; the paths arrive with the drop.
func DragFiles() []string { return nil }

// DragFilesChange is the serial that marks the drag board's last rewrite.
// There is no readable drag board, so it stays 0.
func DragFilesChange() int64 { return 0 }

// winDragFileTypes are the Windows clipboard formats that declare whole
// files on a drag.
var winDragFileTypes = []string{
	"CF_HDROP",
	"FileGroupDescriptorW",
	"FileGroupDescriptor",
	"Shell IDList Array",
	"application/x-mygo-file-list",
}

// DragHasFiles reports whether the drag declares whole files.
func DragHasFiles(types []string) bool {
	for _, t := range types {
		if slicesContains(winDragFileTypes, t) {
			return true
		}
	}
	return false
}

// DragHasFilePromise reports whether the drag promises files it writes on
// drop. Windows drag sources hand real files over, so this never fires.
func DragHasFilePromise(types []string) bool { return false }

// winDragImageTypes are the formats that carry image data on a Windows
// drag.
var winDragImageTypes = []string{"PNG", "image/png", "CF_DIB", "CF_DIBV5"}

// DragHasImages reports whether the drag carries image data.
func DragHasImages(types []string) bool {
	for _, t := range types {
		if slicesContains(winDragImageTypes, t) {
			return true
		}
	}
	return false
}

// slicesContains is a small local helper: the platform files avoid
// importing slices into the drag code so the type lists above stay next to
// the functions that read them.
func slicesContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Windows has no top-edge system gesture (like macOS Spaces) to guard
// against, so the drag-protection corridor is a no-op here and never moves
// the cursor.

// StartDragGuard is a no-op on Windows because there is no top-edge system
// gesture to intercept.
func StartDragGuard(DragGuardRegion) (func(), error) {
	return func() {}, nil
}

// SetDragGuardRegion is a no-op on Windows.
func SetDragGuardRegion(DragGuardRegion) {}

// ParkDragGuard is a no-op on Windows.
func ParkDragGuard(bool) {}

// SetDragPin is a no-op on Windows.
func SetDragPin(DragGuardRegion, bool) {}

// DragMayCarryFiles reports whether a drag with these declared types may
// wake the shake-to-summon basket and the pin strips. Windows hides the
// source's formats, so a live drag is treated as possibly carrying files;
// the summoned card accepts text too, so nothing is lost when a text drag
// wakes it. A declared type, when a caller has one, is still exact.
func DragMayCarryFiles(types []string) bool {
	if DragHasFiles(types) || DragHasImages(types) {
		return true
	}
	active, _ := DragState()
	return active
}
