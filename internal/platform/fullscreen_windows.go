//go:build windows

package platform

import (
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"
)

var procSHQueryUserNotificationState = sysProc(shell32, "SHQueryUserNotificationState")

const (
	qunsBusy                 uint32 = 2 // QUNS_BUSY
	qunsRunningD3DFullScreen uint32 = 3 // QUNS_RUNNING_D3D_FULL_SCREEN
	qunsPresentationMode     uint32 = 4 // QUNS_PRESENTATION_MODE
)

const (
	monitorDefaultToNearest = 2
	fullscreenCacheTTL      = 800 * time.Millisecond
)

var fullscreenCache struct {
	mu        sync.Mutex
	fg        uintptr
	mon       uintptr
	checkedAt time.Time
	result    bool
}

// ForegroundFullscreen reports whether a foreign fullscreen application owns
// the foreground or covers the display containing the pointer. When true,
// top-edge hover and idle pointer gating stay silent so exclusive fullscreen
// games are not interrupted.
func ForegroundFullscreen() bool {
	fg, _, _ := procGetForegroundWindow.Call()
	var pt winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	mon := monitorFromPoint(pt)

	now := time.Now()
	fullscreenCache.mu.Lock()
	defer fullscreenCache.mu.Unlock()
	if fg == fullscreenCache.fg && mon == fullscreenCache.mon &&
		!fullscreenCache.checkedAt.IsZero() && now.Sub(fullscreenCache.checkedAt) < fullscreenCacheTTL {
		return fullscreenCache.result
	}

	res := evalForegroundFullscreen(fg, mon)
	fullscreenCache.fg = fg
	fullscreenCache.mon = mon
	fullscreenCache.checkedAt = now
	fullscreenCache.result = res
	return res
}

func evalForegroundFullscreen(fg, mon uintptr) bool {
	fgOwn := fg != 0 && windowPID(fg) == os.Getpid()
	if fgOwn {
		return false
	}
	var quns uint32
	if hr, _, _ := procSHQueryUserNotificationState.Call(uintptr(unsafe.Pointer(&quns))); hr != 0 {
		quns = 0
	}
	var cls string
	var fgRect, monRect winRect
	var hasRects, zoomed, hasCaption, toolWindow bool
	if fg != 0 && mon != 0 {
		if vis, _, _ := procIsWindowVisible.Call(fg); vis != 0 {
			if iconic, _, _ := procIsIconic.Call(fg); iconic == 0 {
				if z, _, _ := procIsZoomed.Call(fg); z != 0 {
					zoomed = true
				}
				hasCaption = getWindowLong(fg, gwlStyle)&wsCaption == wsCaption
				toolWindow = windowExStyle(fg)&wsExToolWindow != 0
				cls = windowClass(fg)
				var ok1, ok2 bool
				fgRect, ok1 = windowRect(fg)
				monRect, ok2 = monitorRectAt(mon)
				hasRects = ok1 && ok2
			}
		}
	}
	return isFullscreenState(quns, fgOwn, zoomed, hasCaption, toolWindow, cls, fgRect, monRect, hasRects)
}

// isFullscreenState is the pure decision rule for ForegroundFullscreen:
// true for exclusive D3D fullscreen or presentation mode, or when a visible
// non-shell, non-tool, non-maximized foreign foreground window covers the
// pointer's monitor (either borderless, or paired with QUNS_BUSY).
// QUNS_BUSY alone is not trusted without a covering foreground window because
// desktop enhancers, live wallpapers, and shell helpers can keep the system in
// QUNS_BUSY while the user interacts with the desktop or ordinary windows.
func isFullscreenState(quns uint32, fgOwn, zoomed, hasCaption, toolWindow bool, fgClass string, fgRect, monRect winRect, hasRects bool) bool {
	if fgOwn {
		return false
	}
	// Presentation mode is the system saying it is on a stage: every display
	// steps aside with it.
	if quns == qunsPresentationMode {
		return true
	}
	// An exclusive full-screen game answers for its own monitor only; a
	// readable rect decides which monitor that is. Without one, keep the
	// system-wide answer as before.
	d3d := quns == qunsRunningD3DFullScreen
	hasMonitor := hasRects && monRect.Right > monRect.Left && monRect.Bottom > monRect.Top
	if d3d && !hasMonitor {
		return true
	}
	coversMonitor := hasMonitor &&
		fgRect.Left <= monRect.Left && fgRect.Top <= monRect.Top &&
		fgRect.Right >= monRect.Right && fgRect.Bottom >= monRect.Bottom
	// Windows' full-screen optimizations often present an exclusive game as
	// a maximized borderless window while still reporting QUNS_RUNNING_D3D_FULL_SCREEN.
	// The exclusive state and a covering window decide before the maximized
	// shape does: this monitor steps aside, another monitor's does not.
	if d3d && coversMonitor {
		return true
	}
	if zoomed || toolWindow || !hasRects || isShellDesktopClass(fgClass) {
		return false
	}
	if !coversMonitor {
		return false
	}
	if quns == qunsBusy {
		return true
	}
	return !hasCaption
}

func isShellDesktopClass(cls string) bool {
	switch strings.ToLower(cls) {
	case "progman", "workerw", "shell_traywnd", "shell_secondarytraywnd":
		return true
	default:
		return false
	}
}

func monitorFromPoint(pt winPoint) uintptr {
	if unsafe.Sizeof(uintptr(0)) == 4 {
		r, _, _ := procMonitorFromPoint.Call(uintptr(uint32(pt.X)), uintptr(uint32(pt.Y)), monitorDefaultToNearest)
		return r
	}
	r, _, _ := procMonitorFromPoint.Call(uintptr(uint32(pt.X))|uintptr(uint32(pt.Y))<<32, monitorDefaultToNearest)
	return r
}

func monitorRectAt(mon uintptr) (winRect, bool) {
	if mon == 0 {
		return winRect{}, false
	}
	var mi monitorInfoEx
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return winRect{}, false
	}
	return mi.rcMonitor, true
}
