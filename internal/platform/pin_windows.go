//go:build windows

package platform

// Pinned cards across virtual desktops. Windows has no "show on all
// desktops" window bit (SetVisibleOnAllWorkspaces is a no-op in mygo), so a
// pinned card follows the user the other way round: when the desktop under
// the foreground changes, the card is moved onto it. The public
// IVirtualDesktopManager does exactly that, and keeping it here means every
// pin window ConfigurePinWindow prepares is followed without the shared
// pin code knowing about Windows.

import (
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	clsidVirtualDesktopManager = "aa509086-5ca9-4c25-8f95-589d3c07b48a"
	iidIVirtualDesktopManager  = "a5cd92ff-29be-454c-8d04-d82879fb3f1b"

	virtualDesktopManagerIsWindowOnCurrent   = 3
	virtualDesktopManagerGetWindowDesktopID  = 4
	virtualDesktopManagerMoveWindowToDesktop = 5
)

var (
	procCoCreateInstance = sysProc(ole32, "CoCreateInstance")

	virtualDesktopOnce sync.Once
	virtualDesktop     uintptr // IVirtualDesktopManager, 0 when unavailable
)

// virtualDesktopManager returns the process's IVirtualDesktopManager, or 0
// on a system without one. Not thread-safe; callers hold the caller's
// lock or use it on the main thread.
func virtualDesktopManager() uintptr {
	virtualDesktopOnce.Do(func() {
		clsid := clsidFromString(clsidVirtualDesktopManager)
		iid := clsidFromString(iidIVirtualDesktopManager)
		var out uintptr
		// CLSCTX_INPROC_SERVER|CLSCTX_LOCAL_SERVER = 1|4.
		if r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsid)), 0, 5, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&out))); int32(r) < 0 {
			return
		}
		virtualDesktop = out
	})
	return virtualDesktop
}

// clsidFromString parses a GUID in registry spelling into its ABI layout.
func clsidFromString(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		return windows.GUID{}
	}
	return g
}

// currentDesktopID answers the desktop id of the window the user is on:
// the foreground window's when that window is not ours, otherwise any
// visible window that is on the current desktop. ok is false when none
// could be named, in which case the caller leaves the card where it is.
func currentDesktopID(vdm uintptr) (windows.GUID, bool) {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg != 0 && windowPID(fg) != ownProcessID() {
		if id, ok := windowDesktopID(vdm, fg); ok {
			return id, true
		}
	}
	var id windows.GUID
	found := false
	cb := windowsEnumProc(func(h uintptr) bool {
		if r, _, _ := procIsWindowVisible.Call(h); r == 0 || windowPID(h) == ownProcessID() {
			return true
		}
		var on int32
		if r := comMethod(vdm, virtualDesktopManagerIsWindowOnCurrent, h, uintptr(unsafe.Pointer(&on))); int32(r) < 0 || on == 0 {
			return true
		}
		var d windows.GUID
		if r := comMethod(vdm, virtualDesktopManagerGetWindowDesktopID, h, uintptr(unsafe.Pointer(&d))); int32(r) < 0 {
			return true
		}
		id, found = d, true
		return false
	})
	enumTopLevel(cb)
	return id, found
}

// comMethod calls method index of the COM interface object. Pointers
// converted to uintptr in the call stay valid until it returns.
//
//go:uintptrescapes
func comMethod(obj uintptr, index int, args ...uintptr) uintptr {
	var objPtr unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&objPtr)) = obj
	vtbl := *(*uintptr)(objPtr)
	var vtblPtr unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&vtblPtr)) = vtbl
	fn := *(*uintptr)(unsafe.Add(vtblPtr, index*int(unsafe.Sizeof(uintptr(0)))))
	var call [8]uintptr
	call[0] = obj
	n := copy(call[1:], args)
	r, _, _ := syscall.SyscallN(fn, call[:n+1]...)
	return r
}

func windowDesktopID(vdm uintptr, h uintptr) (windows.GUID, bool) {
	var id windows.GUID
	if r := comMethod(vdm, virtualDesktopManagerGetWindowDesktopID, h, uintptr(unsafe.Pointer(&id))); int32(r) < 0 {
		return windows.GUID{}, false
	}
	return id, true
}

// followCurrentDesktop moves window onto the desktop the user is on, and
// keeps doing so while the window lives. The poll is cheap (one COM call
// per active window) and ends itself when the window is destroyed, so the
// pin code needs no teardown hook.
func followCurrentDesktop(window uintptr) {
	if window == 0 {
		return
	}
	if vdm := virtualDesktopManager(); vdm == 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if r, _, _ := procIsWindow.Call(window); r == 0 {
				return
			}
			// The manager is an apartment-threaded shell object; ask the
			// main thread, which created it, to make the call.
			WakeMain(func() { moveToCurrentDesktop(window) })
		}
	}()
}

// moveToCurrentDesktop moves window onto the desktop the user is on,
// unless it is already there. Main thread only.
func moveToCurrentDesktop(window uintptr) {
	if r, _, _ := procIsWindow.Call(window); r == 0 {
		return
	}
	vdm := virtualDesktopManager()
	if vdm == 0 {
		return
	}
	var on int32
	if r := comMethod(vdm, virtualDesktopManagerIsWindowOnCurrent, window, uintptr(unsafe.Pointer(&on))); int32(r) >= 0 && on != 0 {
		return
	}
	current, ok := currentDesktopID(vdm)
	if !ok {
		return
	}
	// MoveWindowToDesktop answers S_FALSE when the window was already
	// there; both are fine.
	comMethod(vdm, virtualDesktopManagerMoveWindowToDesktop, window, uintptr(unsafe.Pointer(&current)))
}
