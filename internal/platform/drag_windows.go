//go:build windows

package platform

// The drag session probes the shelf's monitor reads, and the corridor
// filter that keeps a dragged pointer off the screen's top edge where an
// auto-hidden taskbar or a window-snap region would react.
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
	"time"
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
	procGetClassName     = sysProc(user32, "GetClassNameW")
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

// The corridor filter. Windows cannot rewrite a low-level mouse message the
// way macOS' event tap can, but SetCursorPos from a polling goroutine moves
// the hardware pointer just as well: the drag source samples the cursor on
// the next move. The poll runs only while the left button is held, so an
// idle app costs nothing.
type dragClampState struct {
	mu       sync.Mutex
	guard    DragGuardRegion
	guardOn  bool
	pin      DragGuardRegion
	pinOn    bool
	parked   bool
	running  bool
	stop     chan struct{}
	lastWarp int64
}

var dragClamp dragClampState

// StartDragGuard starts the corridor filter. On Windows it needs no
// permission and cannot fail; the returned stop ends its corridor.
func StartDragGuard(region DragGuardRegion) (func(), error) {
	dragClamp.mu.Lock()
	dragClamp.guard, dragClamp.guardOn = region, true
	dragClamp.startLocked()
	dragClamp.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			dragClamp.mu.Lock()
			dragClamp.guardOn = false
			dragClamp.maybeStopLocked()
			dragClamp.mu.Unlock()
		})
	}, nil
}

// SetDragGuardRegion updates the corridor of a running guard.
func SetDragGuardRegion(region DragGuardRegion) {
	dragClamp.mu.Lock()
	dragClamp.guard = region
	dragClamp.mu.Unlock()
}

// ParkDragGuard parks the corridor filter for the app's own island gesture.
func ParkDragGuard(parked bool) {
	dragClamp.mu.Lock()
	dragClamp.parked = parked
	dragClamp.mu.Unlock()
}

// SetDragPin keeps the cursor out of region during a file drag,
// independently of the guard: it runs while the button is held even when
// no guard is installed.
func SetDragPin(region DragGuardRegion, active bool) {
	dragClamp.mu.Lock()
	dragClamp.pin, dragClamp.pinOn = region, active
	if active {
		dragClamp.startLocked()
	} else {
		dragClamp.maybeStopLocked()
	}
	dragClamp.mu.Unlock()
}

func (c *dragClampState) startLocked() {
	if c.running {
		return
	}
	c.running = true
	c.stop = make(chan struct{})
	go c.poll(c.stop)
}

func (c *dragClampState) maybeStopLocked() {
	if c.guardOn || c.pinOn || !c.running {
		return
	}
	close(c.stop)
	c.running = false
}

func (c *dragClampState) poll(stop chan struct{}) {
	ticker := time.NewTicker(8 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			c.step()
		}
	}
}

// step clamps the pointer one row below the active corridor while it is
// inside. A warp to the row it is already on would generate another move
// event for nothing, so the target is compared first.
func (c *dragClampState) step() {
	if !leftHeld() {
		return
	}
	c.mu.Lock()
	if c.parked {
		c.mu.Unlock()
		return
	}
	region, on := c.guard, c.guardOn
	if c.pinOn {
		region, on = c.pin, true
	}
	c.mu.Unlock()
	if !on || region.Width <= 0 || region.Height <= 0 {
		return
	}
	var p winPoint
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); r == 0 {
		return
	}
	px := dipToPixels(Rect{
		X: float64(region.X), Y: float64(region.Y),
		Width: float64(region.Width), Height: float64(region.Height),
	})
	target := px.Bottom // the first row below the corridor
	if p.X < px.Left || p.X >= px.Right || p.Y < px.Top || p.Y >= target {
		return
	}
	c.mu.Lock()
	now := time.Now().UnixMilli()
	if now == c.lastWarp {
		c.mu.Unlock()
		return
	}
	c.lastWarp = now
	c.mu.Unlock()
	procSetCursorPos.Call(uintptr(p.X), uintptr(target))
}

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
