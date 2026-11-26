//go:build windows

package platform

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestIdleCapsuleWindowFitsItsBox: the resting island capsule is drawn at
// 120 × 30. Windows enforces SM_CXMINTRACK × SM_CYMINTRACK (136 × 39 on the
// test machine) on a captioned window, so the capsule used to be 39pt tall
// and its black pixels ran 9pt past the shell, over the top of a note placed
// 6pt below it. ConfigureIdleCapsuleWindow drops the caption for a popup,
// which keeps the requested box.
func TestIdleCapsuleWindowFitsItsBox(t *testing.T) {
	// The window and its style changes belong to one OS thread: SetWindowLong
	// sends a message to the thread that created the window.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("rakko-idle-capsule-test")
	// The style mygo creates a frameless panel with: visible, clipping, and
	// WS_CAPTION from the overlapped window styles.
	const captionedFrameless = 0x16C00000
	h, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		captionedFrameless, 0, 0, 120, 30, 0, 0, 0, 0)
	if h == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	defer procDestroyWindow.Call(h)

	ConfigureIdleCapsuleWindow(h)
	st := getWindowLong(h, gwlStyle)
	if st&wsCaption != 0 {
		t.Fatalf("capsule kept WS_CAPTION: style %#x", st)
	}
	if st&wsPopup == 0 {
		t.Fatalf("capsule is not WS_POPUP: style %#x", st)
	}
	if windowExStyle(h)&wsExToolWindow == 0 {
		t.Fatal("capsule left the tool-window band")
	}

	procSetWindowPos.Call(h, 0, 100, 700, 120, 30, swpNoZOrder|swpNoActivate)
	r, ok := windowRect(h)
	if !ok {
		t.Fatal("GetWindowRect failed")
	}
	if r.width() != 120 || r.height() != 30 {
		t.Fatalf("capsule %dx%d, want 120x30", r.width(), r.height())
	}
}

func TestOverlayWindowDropsCaptionForPopup(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("rakko-overlay-test")
	const captionedFrameless = 0x16C00000
	h, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		captionedFrameless, 0, 0, 520, 400, 0, 0, 0, 0)
	if h == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	defer procDestroyWindow.Call(h)

	ConfigureOverlayWindow(h)
	st := getWindowLong(h, gwlStyle)
	if st&wsCaption != 0 {
		t.Fatalf("overlay kept WS_CAPTION: style %#x", st)
	}
	if st&wsPopup == 0 {
		t.Fatalf("overlay is not WS_POPUP: style %#x", st)
	}
	if windowExStyle(h)&wsExToolWindow == 0 {
		t.Fatal("overlay left the tool-window band")
	}
}
