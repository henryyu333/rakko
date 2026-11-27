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
	"fmt"
	"log"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	clsidVirtualDesktopManager = "{aa509086-5ca9-4c25-8f95-589d3c07b48a}"
	iidIVirtualDesktopManager  = "{a5cd92ff-29be-454c-8d04-d82879fb3f1b}"

	virtualDesktopManagerIsWindowOnCurrent   = 3
	virtualDesktopManagerGetWindowDesktopID  = 4
	virtualDesktopManagerMoveWindowToDesktop = 5
)

// virtualDesktop holds the process's IVirtualDesktopManager. The
// manager's server is Explorer: a restart disconnects the object under
// the cached pointer, so the holder (com_cache.go) releases and
// recreates it when a call answers that its server went away, re-asking
// on a doubling backoff while Explorer is down rather than on every
// poll tick.
var virtualDesktop = &comObjectCache{
	create:  createVirtualDesktopManager,
	release: releaseCOM,
	isDead:  isCOMDisconnected,
	explain: virtualDesktopOutage,
}

// virtualDesktopManager returns the manager the pin code calls through,
// or 0 and the reason it could not be created. The manager is an
// apartment-threaded shell object, so ask from the main thread.
func virtualDesktopManager() (uintptr, error) {
	return virtualDesktop.get(time.Now())
}

// createVirtualDesktopManager makes the shell's manager. A malformed
// identifier, a shell without the manager and a refused activation all
// report here instead of becoming a zero GUID that creates nothing.
func createVirtualDesktopManager() (uintptr, error) {
	if err := ensureCOM(); err != nil {
		return 0, err
	}
	clsid, err := guidFromString(clsidVirtualDesktopManager)
	if err != nil {
		return 0, err
	}
	iid, err := guidFromString(iidIVirtualDesktopManager)
	if err != nil {
		return 0, err
	}
	var out uintptr
	// CLSCTX_INPROC_SERVER|CLSCTX_LOCAL_SERVER = 1|4.
	if r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsid)), 0, 5, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&out))); int32(r) < 0 {
		return 0, fmt.Errorf("IVirtualDesktopManager: CoCreateInstance failed (0x%08x)", uint32(int32(r)))
	}
	return out, nil
}

// releaseCOM releases one COM interface (IUnknown::Release, the vtable
// slot the comRelease constant names).
func releaseCOM(obj uintptr) {
	if obj != 0 {
		comMethod(obj, comRelease)
	}
}

// isCOMDisconnected reports whether a call's HRESULT says the object's
// server went away: Explorer restarted (RPC_E_DISCONNECTED) or its RPC
// endpoint died (RPC_S_SERVER_UNAVAILABLE as an HRESULT). HRESULTs are
// 32-bit; a 64-bit SyscallN result sign-extends a failing one into the
// upper half, so the comparison keeps the low word.
func isCOMDisconnected(hr uintptr) bool {
	switch uint32(hr) {
	case 0x80010108, 0x800706BA:
		return true
	}
	return false
}

// virtualDesktopOutage says an outage once and its end once, so a long
// one costs two log lines instead of one per refused poll.
func virtualDesktopOutage(down bool, err error) {
	if down {
		log.Printf("platform: pinned cards stay on their desktop: %v", err)
		return
	}
	log.Print("platform: pinned cards follow the desktop again")
}

// currentDesktopID answers the desktop id of the window the user is on:
// the foreground window's when that window is not ours, otherwise any
// visible window that is on the current desktop. ok is false when none
// could be named, in which case the caller leaves the card where it is.
func currentDesktopID() (windows.GUID, bool) {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg != 0 && windowPID(fg) != ownProcessID() {
		if id, ok := windowDesktopID(fg); ok {
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
		hr, ok := virtualDesktop.call(time.Now(), func(vdm uintptr) uintptr {
			return comMethod(vdm, virtualDesktopManagerIsWindowOnCurrent, h, uintptr(unsafe.Pointer(&on)))
		})
		if !ok || int32(hr) < 0 || on == 0 {
			return true
		}
		var d windows.GUID
		hr, ok = virtualDesktop.call(time.Now(), func(vdm uintptr) uintptr {
			return comMethod(vdm, virtualDesktopManagerGetWindowDesktopID, h, uintptr(unsafe.Pointer(&d)))
		})
		if !ok || int32(hr) < 0 {
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

func windowDesktopID(h uintptr) (windows.GUID, bool) {
	var id windows.GUID
	hr, ok := virtualDesktop.call(time.Now(), func(vdm uintptr) uintptr {
		return comMethod(vdm, virtualDesktopManagerGetWindowDesktopID, h, uintptr(unsafe.Pointer(&id)))
	})
	if !ok || int32(hr) < 0 {
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
	if vdm, _ := virtualDesktopManager(); vdm == 0 {
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
	var on int32
	hr, ok := virtualDesktop.call(time.Now(), func(vdm uintptr) uintptr {
		return comMethod(vdm, virtualDesktopManagerIsWindowOnCurrent, window, uintptr(unsafe.Pointer(&on)))
	})
	if !ok || (int32(hr) >= 0 && on != 0) {
		return
	}
	current, ok := currentDesktopID()
	if !ok {
		return
	}
	// MoveWindowToDesktop answers S_FALSE when the window was already
	// there; both are fine. A dead answer releases the stale object and
	// tries a fresh one once; a still-dead one waits for the backoff.
	virtualDesktop.call(time.Now(), func(vdm uintptr) uintptr {
		return comMethod(vdm, virtualDesktopManagerMoveWindowToDesktop, window, uintptr(unsafe.Pointer(&current)))
	})
}
