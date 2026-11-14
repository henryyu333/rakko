//go:build windows

package platform

// Geometry, display identity and click-through tests. They run headless on
// the Windows test machine: the display lists are synthetic, and the two
// window-style cases use a real STATIC window, which needs no app loop.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func testMonitor(handle uintptr, left, top, width, height int32, dpi int, device string) screenMonitor {
	return screenMonitor{
		handle: handle,
		bounds: winRect{Left: left, Top: top, Right: left + width, Bottom: top + height},
		work:   winRect{Left: left, Top: top, Right: left + width, Bottom: top + height},
		dpi:    dpi,
		device: device,
	}
}

func TestDIPToPixelsRoundTrip(t *testing.T) {
	at96 := []screenMonitor{testMonitor(1, 0, 0, 1920, 1080, 96, `\\.\DISPLAY1`)}
	at150 := []screenMonitor{testMonitor(1, 0, 0, 2560, 1440, 144, `\\.\DISPLAY1`)}
	for _, tt := range []struct {
		name   string
		ms     []screenMonitor
		r      Rect
		pixels winRect
	}{
		{"96", at96, Rect{X: 100, Y: 200, Width: 400, Height: 300}, winRect{100, 200, 500, 500}},
		{"150", at150, Rect{X: 100, Y: 100, Width: 300, Height: 200}, winRect{150, 150, 600, 450}},
	} {
		if got := dipToPixelsIn(tt.ms, tt.r); got != tt.pixels {
			t.Errorf("%s: dipToPixelsIn(%+v) = %+v, want %+v", tt.name, tt.r, got, tt.pixels)
		}
		if got := pixelsToDIPIn(tt.ms, tt.pixels); got != tt.r {
			t.Errorf("%s: pixelsToDIPIn(%+v) = %+v, want %+v", tt.name, tt.pixels, got, tt.r)
		}
	}
}

// TestSecondDisplayConversion checks the anchor mygo's DIP space uses for
// a display that does not start at the origin, at a different scale from
// the primary one.
func TestSecondDisplayConversion(t *testing.T) {
	ms := []screenMonitor{
		testMonitor(1, 0, 0, 1920, 1080, 96, `\\.\DISPLAY1`),
		testMonitor(2, 1920, 0, 2560, 1440, 144, `\\.\DISPLAY2`),
	}
	r := Rect{X: 2000, Y: 100, Width: 200, Height: 100}
	want := winRect{Left: 2040, Top: 150, Right: 2340, Bottom: 300}
	if got := dipToPixelsIn(ms, r); got != want {
		t.Fatalf("dipToPixelsIn = %+v, want %+v", got, want)
	}
	if got := pixelsToDIPIn(ms, want); got != r {
		t.Fatalf("pixelsToDIPIn = %+v, want %+v", got, r)
	}
}

// TestPixelRectBelongsToContainingDisplay checks which display a physical
// window rectangle is scaled by. A window that starts on the primary display
// just beside a higher-DPI neighbour must use the primary's DPI; the old
// corner-distance rule blamed the neighbour and mis-scaled its rectangle.
func TestPixelRectBelongsToContainingDisplay(t *testing.T) {
	ms := []screenMonitor{
		testMonitor(1, 0, 0, 1920, 1080, 96, `\\.\DISPLAY1`),
		testMonitor(2, 1920, 0, 2560, 1440, 144, `\\.\DISPLAY2`),
	}
	edge := winRect{Left: 1800, Top: 100, Right: 1900, Bottom: 300}
	if got, want := pixelsToDIPIn(ms, edge), (Rect{X: 1800, Y: 100, Width: 100, Height: 200}); got != want {
		t.Fatalf("window at the primary's right edge = %+v, want %+v", got, want)
	}
	// A window spanning both belongs to the display it overlaps most: 420 px
	// on the primary against 380 px on the neighbour.
	spanning := winRect{Left: 1500, Top: 100, Right: 2300, Bottom: 300}
	if got, want := pixelsToDIPIn(ms, spanning), (Rect{X: 1500, Y: 100, Width: 800, Height: 200}); got != want {
		t.Fatalf("spanning window = %+v, want %+v", got, want)
	}
	// Past the right edge of the desktop the nearest display decides.
	offscreen := winRect{Left: 4600, Top: 0, Right: 4700, Bottom: 100}
	dpi := 144.0
	want := Rect{X: 1920 + 2680*96/dpi, Y: 0, Width: 100 * 96 / dpi, Height: 100 * 96 / dpi}
	if got := pixelsToDIPIn(ms, offscreen); got != want {
		t.Fatalf("off-screen window = %+v, want %+v", got, want)
	}
}

func TestNotchRectAnchor(t *testing.T) {
	ms := []screenMonitor{testMonitor(7, 0, 0, 1920, 1080, 96, `\\.\DISPLAY1`)}
	x, y, w, h, hasNotch := notchRectIn(ms, 7, false)
	if x != 870 || y != 0 || w != 180 || h != 32 || hasNotch {
		t.Fatalf("island anchor = (%d,%d,%d,%d, %v), want (870,0,180,32,false)", x, y, w, h, hasNotch)
	}

	// A taskbar at the top pushes the island under it, and leaves no room
	// for a simulated housing.
	topBar := []screenMonitor{testMonitor(7, 0, 0, 1920, 1080, 96, `\\.\DISPLAY1`)}
	topBar[0].work.Top = 40
	x, y, _, _, hasNotch = notchRectIn(topBar, 7, true)
	if y != 40 || hasNotch {
		t.Fatalf("top taskbar anchor = y %d hasNotch %v, want y 40, false", y, hasNotch)
	}
	if x != 870 {
		t.Fatalf("top taskbar x = %d, want 870", x)
	}

	// The simulated housing hugs the display's top edge.
	x, y, _, h, hasNotch = notchRectIn(ms, 7, true)
	if !hasNotch || y != 0 || h != 32 {
		t.Fatalf("simulated notch = y %d h %d hasNotch %v, want y 0, h 32, true", y, h, hasNotch)
	}

	if x, y, w, h, hasNotch = notchRectIn(ms, 99, true); x != 0 || y != 0 || w != 0 || h != 0 || hasNotch {
		t.Fatalf("unknown display = (%d,%d,%d,%d, %v), want zero", x, y, w, h, hasNotch)
	}
}

func TestNotchRectOnSecondDisplay(t *testing.T) {
	ms := []screenMonitor{
		testMonitor(1, 0, 0, 1920, 1080, 96, `\\.\DISPLAY1`),
		testMonitor(2, 1920, 0, 2560, 1440, 144, `\\.\DISPLAY2`),
	}
	x, y, w, _, _ := notchRectIn(ms, 2, false)
	// DIP origin 1920, DIP width 2560×96/144 ≈ 1707 → centred 180 wide.
	if x != 1920+(1707-180)/2 || y != 0 || w != 180 {
		t.Fatalf("second display anchor = (%d,%d,%d), want (%d,0,180)", x, y, w, 1920+(1707-180)/2)
	}
}

func TestStableDisplayID(t *testing.T) {
	a := testMonitor(1, 0, 0, 1920, 1080, 96, `\\.\DISPLAY1`)
	b := testMonitor(2, 1920, 0, 1920, 1080, 96, `\\.\DISPLAY2`)
	idA, idB := stableDisplayID(a), stableDisplayID(b)
	if idA == 0 || idB == 0 || idA == idB {
		t.Fatalf("stable ids = %d and %d, want distinct non-zero", idA, idB)
	}
	if idA < 0 || idB < 0 {
		t.Fatalf("stable ids must stay positive: %d, %d", idA, idB)
	}
	if idA != stableDisplayID(a) {
		t.Fatal("stable id changed between calls")
	}
	ms := []screenMonitor{a, b}
	if _, i, ok := findMonitorIn(ms, idB); !ok || i != 1 {
		t.Fatalf("stable id lookup = index %d, ok %v; want 1, true", i, ok)
	}
	if _, i, ok := findMonitorIn(ms, 2); !ok || i != 1 {
		t.Fatalf("raw handle lookup = index %d, ok %v; want 1, true", i, ok)
	}
}

// TestStableDisplayIDUsesMonitorPath checks that a display's saved identity
// comes from the physical monitor rather than the adapter output: the same
// monitor replugged onto another output keeps its id, and a key saved by an
// earlier build (the GDI name's hash) still resolves.
func TestStableDisplayIDUsesMonitorPath(t *testing.T) {
	m := testMonitor(1, 0, 0, 1920, 1080, 96, `\\.\DISPLAY1`)
	m.target = `\\?\DISPLAY#DEL4098#5&2f8a1b1c&0&UID4352#{e6f07b5f-ee97-4a90-b076-33f57bf4eaa7}`
	if stableDisplayID(m) == legacyDisplayID(m) {
		t.Fatal("the stable id must hash the monitor's path, not the adapter output's name")
	}
	replugged := m
	replugged.device = `\\.\DISPLAY2`
	if stableDisplayID(replugged) != stableDisplayID(m) {
		t.Fatal("the same monitor on another output changed its id")
	}
	if legacyDisplayID(replugged) == legacyDisplayID(m) {
		t.Fatal("the legacy id must follow the GDI name")
	}
	ms := []screenMonitor{replugged}
	if _, i, ok := findMonitorIn(ms, stableDisplayID(m)); !ok || i != 0 {
		t.Fatalf("current id lookup = index %d, ok %v; want 0, true", i, ok)
	}
	// A layout saved by an earlier build still resolves while the output
	// keeps the GDI name that build hashed.
	if _, i, ok := findMonitorIn(ms, legacyDisplayID(replugged)); !ok || i != 0 {
		t.Fatalf("a layout saved under the old id no longer resolves: index %d, ok %v", i, ok)
	}
	// A display without a monitor path (no display configuration answer)
	// still gets a stable id, the GDI name's.
	plain := testMonitor(2, 1920, 0, 1920, 1080, 96, `\\.\DISPLAY2`)
	if stableDisplayID(plain) != legacyDisplayID(plain) {
		t.Fatal("without a monitor path the id must fall back to the GDI name")
	}
}

// TestDisplayTargetPathsQuery checks the display configuration query on the
// machine: every active output must resolve to a GDI name and a monitor
// device path, and the enumerated displays must pick the path up. A session
// without a display configuration (a headless remote session) has no paths
// to check.
func TestDisplayTargetPathsQuery(t *testing.T) {
	targets := queryDisplayTargetPaths()
	if len(targets) == 0 {
		t.Skip("this session has no active display configuration")
	}
	for name, path := range targets {
		if len(name) < len(`\\.\DISPLAY`) || name[:len(`\\.\DISPLAY`)] != `\\.\DISPLAY` {
			t.Errorf("output name %q, want a GDI device name", name)
		}
		if path == "" {
			t.Errorf("%s has an empty monitor path", name)
		}
	}
	matched := 0
	for _, m := range enumMonitors() {
		if m.target == "" {
			continue
		}
		matched++
		if stableDisplayID(m) == legacyDisplayID(m) {
			t.Errorf("%s kept the adapter-output id despite monitor path %q", m.device, m.target)
		}
	}
	if matched == 0 {
		t.Errorf("no enumerated display resolved to a monitor path; targets = %v", targets)
	}
}

// TestRepeatedEnumerationKeepsCallbackSlots checks that resolving displays
// and listing windows reuse the package's fixed enumeration callbacks. Each
// call used to create a fresh Windows callback and the runtime never frees
// them (its table holds 2000), so a few thousand display queries — the DIP
// conversions make one each — crashed the app.
func TestRepeatedEnumerationKeepsCallbackSlots(t *testing.T) {
	for range 2500 {
		if len(enumMonitors()) == 0 {
			t.Fatal("no displays reported")
		}
	}
	seen := 0
	enumTopLevel(func(uintptr) bool {
		seen++
		return true
	})
	if seen == 0 {
		t.Fatal("EnumWindows reported no windows")
	}
}

// TestPressClassification checks the global observer's inside/outside
// answer: a press inside a window that takes the mouse is the app's, even
// when WindowFromPoint answers the desktop; a click-through window reports
// the press as the other app's.
func TestPressClassification(t *testing.T) {
	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("rakko-press-test")
	h, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		0x80000000 /* WS_POPUP */, 120, 160, 240, 120, 0, 0, 0, 0)
	if h == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	defer procDestroyWindow.Call(h)

	r, ok := windowRect(h)
	if !ok {
		t.Fatal("GetWindowRect failed")
	}
	center := winPoint{X: (r.Left + r.Right) / 2, Y: (r.Top + r.Bottom) / 2}
	if inside, report := pressAt(center, h); !inside || !report {
		t.Fatalf("press inside the window: inside=%v report=%v, want true true", inside, report)
	}
	if inside, _ := pressAt(winPoint{X: r.Right + 50, Y: r.Bottom + 50}, h); inside {
		t.Fatal("a press outside the window reported inside")
	}

	SetOverlayAcceptsMouse(h, false)
	if inside, report := pressAt(center, h); inside || !report {
		t.Fatalf("press through a click-through window: inside=%v report=%v, want false true", inside, report)
	}
}

// testHitWindow creates a visible, topmost window that hit-tests its client
// area, or skips the test when this logon session has no desktop to show it
// on. A BUTTON class window is used because a STATIC control answers
// HTTRANSPARENT over its client area and WindowFromPoint skips it.
func testHitWindow(t *testing.T) uintptr {
	t.Helper()
	class, _ := windows.UTF16PtrFromString("BUTTON")
	title, _ := windows.UTF16PtrFromString("rakko-hit-test")
	h, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		0x80000000 /* WS_POPUP */, 300, 260, 200, 120, 0, 0, 0, 0)
	if h == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(h) })
	procShowWindow.Call(h, 4 /* SW_SHOWNOACTIVATE */)
	if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
		// A logon session with no attached desktop (an SSH-only test run)
		// cannot show or hit-test a window: the same test runs through
		// rw desk, where the desktop is real.
		t.Skip("no interactive desktop: the test window cannot be shown")
	}
	// The shared desktop may keep other windows over that spot; a topmost
	// test window is the one the hit test must find.
	reorderFront(h, true)
	return h
}

// TestWindowFromPointAtPacksTheCoordinates checks the physical-point hit test
// the global observer classifies presses with. WindowFromPoint takes the point
// by value: the old call passed its address, so x64 used the pointer's bits as
// coordinates and answered no window, and only the rectangle fallback kept the
// observer working.
func TestWindowFromPointAtPacksTheCoordinates(t *testing.T) {
	h := testHitWindow(t)
	r, ok := windowRect(h)
	if !ok {
		t.Fatal("GetWindowRect failed")
	}
	center := winPoint{X: (r.Left + r.Right) / 2, Y: (r.Top + r.Bottom) / 2}
	if got := windowFromPointAt(center); got != h {
		t.Fatalf("WindowFromPoint at the window's centre = %#x, want %#x", got, h)
	}
	far := winPoint{X: r.Right + 400, Y: r.Bottom + 400}
	if got := windowFromPointAt(far); got == h {
		t.Fatal("WindowFromPoint outside the window answered the window itself")
	}
}

// TestFileOperations checks the shell calls the rename, trash, free-space
// and icon paths depend on, against a temporary directory.
func TestFileOperations(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	dst := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenameNoReplace(src, dst); !errors.Is(err, ErrExists) {
		t.Fatalf("RenameNoReplace onto an existing name = %v, want ErrExists", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("the failed rename lost the source: %v", err)
	}
	other := filepath.Join(dir, "renamed.txt")
	if err := RenameNoReplace(src, other); err != nil {
		t.Fatalf("RenameNoReplace: %v", err)
	}
	if _, ok := CreationTime(other); !ok {
		t.Fatal("CreationTime failed for a fresh file")
	}
	if free, err := FreeSpace(dir); err != nil || free <= 0 {
		t.Fatalf("FreeSpace = %d, %v; want a positive number", free, err)
	}
	if icon := FileIcon(filepath.Join(os.Getenv("SystemRoot"), "System32", "notepad.exe"), 32); len(icon) == 0 {
		t.Fatal("FileIcon(notepad.exe) returned no PNG")
	}
	text := filepath.Join(dir, "trash-me.txt")
	if err := os.WriteFile(text, []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := TrashFile(text); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if _, err := os.Stat(text); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the trashed file is still there: %v", err)
	}
}

// TestOverlayClickThroughStyle checks the WS_EX_TRANSPARENT toggling on a
// real window: the bit the overlay uses to let clicks reach whatever is
// under it.
func TestOverlayClickThroughStyle(t *testing.T) {
	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("rakko-overlay-test")
	h, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		0x80000000 /* WS_POPUP */, 0, 0, 200, 100, 0, 0, 0, 0)
	if h == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	defer procDestroyWindow.Call(h)

	if OverlayIgnoresMouse(h) {
		t.Fatal("a fresh window must take the mouse")
	}
	SetOverlayAcceptsMouse(h, false)
	if !OverlayIgnoresMouse(h) {
		t.Fatal("SetOverlayAcceptsMouse(false) did not set WS_EX_TRANSPARENT")
	}
	if windowExStyle(h)&wsExLayered == 0 {
		t.Fatal("the click-through window must be layered")
	}
	SetOverlayAcceptsMouse(h, true)
	if OverlayIgnoresMouse(h) {
		t.Fatal("SetOverlayAcceptsMouse(true) did not clear WS_EX_TRANSPARENT")
	}
}
