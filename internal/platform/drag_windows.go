//go:build windows

package platform

// The drag session probes the shelf's monitor reads. Windows has no
// top-edge system gesture (like macOS Spaces) that steals a drag near the
// screen's top edge, so when no display sits above Rakko the guard is a
// no-op and never clamps or warps the cursor. When another display sits
// directly above Rakko's anchor segment, the shared edge barrier stops
// upward crossings (except non-file drags) at the junction until the user
// pushes 80 DIP further up.
//
// Windows exposes no API for another process's OLE drag: the data object
// stays in the source. What it does expose is the capture window OLE puts
// on the source's thread for the duration of DoDragDrop, whose class is
// CLIPBRDWNDCLASS. A held left button plus that capture is a drag, for
// Explorer, browsers and every other OLE source alike.
//
// The formats and file paths of that drag are not readable from outside
// the source before the cursor reaches Rakko's window. DragTypes and
// DragFiles therefore stay empty; once the drag enters a Rakko window,
// mygo's OLE drop target reads CF_HDROP paths without reading file contents
// and delivers them through ui.DataDragOver's transfer.Offer.Files, and the
// drop itself delivers the full transfer data.

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

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

// oleDragCaptureClass (dragCapture) is the capture window OLE's drag loop
// holds. The class is ole32's hidden clipboard/drag window, shared by every
// OLE drag source.
const oleDragCaptureClass = "CLIPBRDWNDCLASS"

func windowClass(h uintptr) string {
	var buf [128]uint16
	n, _, _ := procGetClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// isOLEDragActive reports whether the foreground thread's capture window
// belongs to OLE's drag loop (and not a window move/size loop).
// GetGUIThreadInfo with a zero thread id answers for the thread that owns
// the active window, exactly where DoDragDrop runs.
func isOLEDragActive() bool {
	var gti guiThreadInfo
	gti.cbSize = uint32(unsafe.Sizeof(gti))
	if r, _, _ := procGetGUIThreadInfo.Call(0, uintptr(unsafe.Pointer(&gti))); r == 0 {
		return false
	}
	if gti.hwndMoveSize != 0 || gti.hwndCapture == 0 {
		return false
	}
	return windowClass(gti.hwndCapture) == oleDragCaptureClass
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
	dragging := leftHeld() && isOLEDragActive()
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

// DragFiles returns the file paths of the drag session in flight. Outside
// Rakko's window the source's OLE data object is unreachable, so this
// returns nil; hover paths arrive via transfer.Offer.Files once OLE
// DragEnter reaches a Rakko window.
func DragFiles() []string { return nil }

// DragFilesChange is the serial that marks the drag board's last rewrite.
// Windows exposes no global drag pasteboard serial outside the target
// window, so this stays 0.
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

var (
	winBarrierArmed  atomic.Bool
	winBarrierParked atomic.Bool
	winBarrierRegion atomic.Pointer[DragGuardRegion]

	winEdgeMu    sync.Mutex
	winEdgeState EdgeGuardState
)

// winBarrierRegionIn converts a DIP-space DragGuardRegion into physical screen
// pixel coordinates and scaled push/reset thresholds for WH_MOUSE_LL.
func winBarrierRegionIn(ms []screenMonitor, region DragGuardRegion) DragGuardRegion {
	if !region.BarrierEnabled || region.BarrierWidth <= 0 {
		return DragGuardRegion{}
	}
	i := displayAt(ms, region.BarrierX, region.BarrierY)
	dpi := 96
	if i >= 0 && ms[i].dpi > 0 {
		dpi = ms[i].dpi
	}
	px := dipToPixelsIn(ms, Rect{
		X:      float64(region.BarrierX),
		Y:      float64(region.BarrierY),
		Width:  float64(region.BarrierWidth),
		Height: 1,
	})
	pushPt := region.BarrierPushPt
	if pushPt <= 0 {
		pushPt = EdgeBarrierPushPt
	}
	pushPx := int(roundHalf(float64(pushPt) * float64(dpi) / 96))
	marginPx := int(roundHalf(float64(edgeBarrierResetMargin) * float64(dpi) / 96))
	return DragGuardRegion{
		BarrierEnabled:     true,
		BarrierX:           int(px.Left),
		BarrierY:           int(px.Top),
		BarrierWidth:       max(0, int(px.Right-px.Left)),
		BarrierPushPt:      max(1, pushPx),
		BarrierResetMargin: max(1, marginPx),
	}
}

func applyWinBarrierRegion(region DragGuardRegion) {
	if !winBarrierArmed.Load() || !region.BarrierEnabled || region.BarrierWidth <= 0 {
		winBarrierRegion.Store(nil)
		winEdgeMu.Lock()
		winEdgeState.Reset()
		winEdgeMu.Unlock()
		return
	}
	if err := ensureMouseHook(); err != nil {
		return
	}
	pxReg := winBarrierRegionIn(enumMonitors(), region)
	if !pxReg.BarrierEnabled || pxReg.BarrierWidth <= 0 {
		winBarrierRegion.Store(nil)
		return
	}
	prev := winBarrierRegion.Load()
	if prev == nil || *prev != pxReg {
		var pt winPoint
		hasPt := false
		if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r != 0 {
			hasPt = true
		}
		winEdgeMu.Lock()
		if hasPt {
			winEdgeState.Seed(float64(pt.Y))
		} else {
			winEdgeState.Reset()
		}
		winEdgeMu.Unlock()
	}
	winBarrierRegion.Store(&pxReg)
}

// evalWinEdgeBarrier evaluates one WM_MOUSEMOVE point inside mouseHookProc
// without any slow or blocking Win32 calls.
func evalWinEdgeBarrier(pt winPoint) (winPoint, bool) {
	reg := winBarrierRegion.Load()
	if reg == nil || !reg.BarrierEnabled {
		return pt, false
	}
	winEdgeMu.Lock()
	active := winEdgeState.Active()
	passed := winEdgeState.Passed()
	winEdgeMu.Unlock()
	nearOrIn := pt.Y <= int32(reg.BarrierY+reg.BarrierResetMargin) &&
		(active || (pt.X >= int32(reg.BarrierX) && pt.X < int32(reg.BarrierX+reg.BarrierWidth)))
	if !active && !nearOrIn {
		return pt, false
	}
	// If an external SetCursorPos moved the cursor back below the junction
	// while passed was armed, re-seed from the real current cursor position so
	// the very next fast upward sweep still stops at the top edge.
	if passed && pt.Y <= int32(reg.BarrierY+reg.BarrierResetMargin) {
		var cur winPoint
		if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&cur))); r != 0 && cur.Y > int32(reg.BarrierY+reg.BarrierResetMargin) {
			winEdgeMu.Lock()
			if winEdgeState.Passed() {
				winEdgeState.Seed(float64(cur.Y))
			}
			winEdgeMu.Unlock()
		}
	}
	leftDown := leftHeld()
	fileDrag := false
	if leftDown && nearOrIn {
		fileDrag = isOLEDragActive()
	}
	parked := winBarrierParked.Load()
	winEdgeMu.Lock()
	nx, ny, changed := winEdgeState.Eval(float64(pt.X), float64(pt.Y), *reg, parked, leftDown, fileDrag, false)
	winEdgeMu.Unlock()
	if !changed {
		return pt, false
	}
	return winPoint{X: int32(nx), Y: int32(ny)}, true
}

// PhysicalUpperTouchesSegment checks Windows monitors in physical pixels so
// mixed-DPI layouts (whose DIP rectangles may not touch due to min-DPI origin
// scaling) still report flush upper-display junctions accurately.
func PhysicalUpperTouchesSegment(segX, segY, segW int) (bx, by, bw int, ok bool) {
	return physicalUpperTouchesSegmentIn(enumMonitors(), segX, segY, segW)
}

func physicalUpperTouchesSegmentIn(ms []screenMonitor, segX, segY, segW int) (bx, by, bw int, ok bool) {
	if segW <= 0 || len(ms) < 2 {
		return 0, 0, 0, false
	}
	lowerIdx := displayAt(ms, segX+segW/2, segY)
	if lowerIdx < 0 {
		return 0, 0, 0, false
	}
	pxSeg := dipToPixelsIn(ms, Rect{
		X:      float64(segX),
		Y:      float64(segY),
		Width:  float64(segW),
		Height: 1,
	})
	pxLeft, pxRight := int(pxSeg.Left), int(pxSeg.Right)
	if pxRight <= pxLeft {
		return 0, 0, 0, false
	}
	var hit bool
	minX, maxX := pxRight, pxLeft
	for idx, m := range ms {
		if idx == lowerIdx {
			continue
		}
		diff := int(m.bounds.Bottom) - int(pxSeg.Top)
		if diff < -1 || diff > 1 {
			continue
		}
		x0 := max(int(m.bounds.Left), pxLeft)
		x1 := min(int(m.bounds.Right), pxRight)
		if x1 <= x0 {
			continue
		}
		hit = true
		if x0 < minX {
			minX = x0
		}
		if x1 > maxX {
			maxX = x1
		}
	}
	if !hit || maxX <= minX {
		return 0, 0, 0, false
	}
	m := ms[lowerIdx]
	ox, _ := dipOrigin(ms, lowerIdx)
	bx = ox + scaleBy(minX-int(m.bounds.Left), 96, m.dpi)
	bw = scaleBy(maxX-minX, 96, m.dpi)
	if bw <= 0 {
		return 0, 0, 0, false
	}
	return bx, segY, bw, true
}

// StartDragGuard arms top-edge upper-display barrier protection on Windows.
// When region.BarrierEnabled is false, no cursor clamping is performed.
func StartDragGuard(region DragGuardRegion) (func(), error) {
	winBarrierArmed.Store(true)
	applyWinBarrierRegion(region)
	var once sync.Once
	return func() {
		once.Do(func() {
			winBarrierArmed.Store(false)
			winBarrierRegion.Store(nil)
			winEdgeMu.Lock()
			winEdgeState.Reset()
			winEdgeMu.Unlock()
		})
	}, nil
}

// SetDragGuardRegion updates the Windows upper-display barrier region.
func SetDragGuardRegion(region DragGuardRegion) {
	if !winBarrierArmed.Load() {
		return
	}
	applyWinBarrierRegion(region)
}

// ParkDragGuard temporarily suspends upper-display clamping during Rakko's own gestures.
func ParkDragGuard(parked bool) {
	winBarrierParked.Store(parked)
}

// SetDragPin is a no-op on Windows because WH_MOUSE_LL already enforces the barrier.
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
