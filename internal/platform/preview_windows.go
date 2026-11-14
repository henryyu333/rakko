//go:build windows

package platform

// Quick preview on Windows: a floating window of our own hosting the same
// IPreviewHandler component Explorer's preview pane uses.
//
// The host is a plain Win32 window rather than a mygo panel because a
// preview handler parents its child window to an HWND we own, and the host
// has to see the raw window messages (WM_SIZE, Esc/Space/arrow keys) that a
// mygo widget never exposes. WS_EX_TOOLWINDOW keeps the panel out of the
// taskbar and Alt+Tab, the standard rule for tool windows, and
// WS_EX_TOPMOST puts it in the band the shelf itself uses.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// previewHandlerClass is the preview-handler shell extension GUID both
	// the registry lookup and the IPreviewHandler IID use.
	previewHandlerClass = `{8895b1c6-b41f-4c1c-a562-0d564250836f}`

	previewClassText = "RakkoPreviewPanel"
	previewDefaultW  = 900
	previewDefaultH  = 640
	previewMargin    = 40

	// WS_EX_TOPMOST | WS_EX_TOOLWINDOW.
	previewExStyle = 0x00000008 | 0x00000080
	// WS_OVERLAPPEDWINDOW: caption, thick frame, system menu, min/max boxes.
	previewStyle = 0x00CF0000

	pvSWShownormal = 1
	pvIDCArrow     = 32512

	// CLSCTX_INPROC_SERVER | CLSCTX_LOCAL_SERVER: handlers may live in an
	// out-of-process server (the Photos and PDF ones do).
	pvClsctx = 0x1 | 0x4

	// STGM_READ. Preview handlers only want to read the item.
	pvStgmRead = 0

	pvWMSize    = 0x0005
	pvWMClose   = 0x0010
	pvWMDestroy = 0x0002
	pvWMKeyDown = 0x0100

	pvVKEscape = 0x1B
	pvVKSpace  = 0x20
	pvVKLeft   = 0x25
	pvVKUp     = 0x26
	pvVKRight  = 0x27
	pvVKDown   = 0x28

	// Methods of IPreviewHandler, after the three IUnknown slots.
	pvMethodQueryInterface = 0
	pvMethodRelease        = 2
	pvMethodSetWindow      = 3
	pvMethodSetRect        = 4
	pvMethodDoPreview      = 5
	pvMethodUnload         = 6
	pvMethodInitialize     = 3 // first method after IUnknown on the init interfaces
)

// Procedures and COM entry points. Their names are prefixed so the other
// Windows files can add their own without colliding.
var (
	procPVRegisterClassEx = sysProc(user32, "RegisterClassExW")
	procPVDefWindowProc   = sysProc(user32, "DefWindowProcW")
	procPVGetModuleHandle = sysProc(kernel32, "GetModuleHandleW")
	procPVLoadCursor      = sysProc(user32, "LoadCursorW")
	procPVSetFocus        = sysProc(user32, "SetFocus")
	procPVSetWindowText   = sysProc(user32, "SetWindowTextW")

	procPVCoCreateInstance = sysProc(ole32, "CoCreateInstance")

	procPVSHCreateItemFromParsingName = sysProc(shell32, "SHCreateItemFromParsingName")

	shlwapi = windows.NewLazySystemDLL("shlwapi.dll")

	procPVSHCreateStreamOnFileEx = sysProc(shlwapi, "SHCreateStreamOnFileEx")
)

// Interface identifiers. The initializer IIDs are the ones the preview
// handler may expose; a handler picks exactly one of them.
var (
	iidPVIUnknown = windows.GUID{
		Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	iidPVPreviewHandler = windows.GUID{
		Data1: 0x8895b1c6, Data2: 0xb41f, Data3: 0x4c1c,
		Data4: [8]byte{0xa5, 0x62, 0x0d, 0x56, 0x42, 0x50, 0x83, 0x6f},
	}
	iidPVInitializeWithFile = windows.GUID{
		Data1: 0xb7d14566, Data2: 0x0509, Data3: 0x4cce,
		Data4: [8]byte{0xa7, 0x1f, 0x0a, 0x55, 0x42, 0x33, 0xbd, 0x9b},
	}
	iidPVInitializeWithItem = windows.GUID{
		Data1: 0x7f73be3f, Data2: 0xfb79, Data3: 0x493c,
		Data4: [8]byte{0xa6, 0xc7, 0x7e, 0xe1, 0x4e, 0x24, 0x58, 0x41},
	}
	iidPVInitializeWithStream = windows.GUID{
		Data1: 0xb824b49d, Data2: 0x22ac, Data3: 0x4161,
		Data4: [8]byte{0xac, 0x8a, 0x99, 0x16, 0xe8, 0xfa, 0x3f, 0x7f},
	}
	iidPVShellItem = windows.GUID{
		Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee,
		Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe},
	}
)

// previewWndClassEx mirrors WNDCLASSEXW.
type previewWndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type previewItem struct {
	path  string
	clsid windows.GUID
}

// previewComObject is the head of a raw COM object: its first word is the
// vtable pointer. Keeping the object as a typed pointer (rather than a
// uintptr) also keeps go vet's unsafeptr check happy.
type previewComVtable [8]uintptr

type previewComObject struct {
	vtable *previewComVtable
}

// previewPanel is the one open panel. Preview, PreviewVisible and
// ClosePreview are main-thread calls, and so is the window procedure, so no
// lock is needed.
type previewPanel struct {
	hwnd    uintptr
	items   []previewItem
	index   int
	handler *previewComObject // IPreviewHandler of items[index]
	closing bool
}

var previewState previewPanel

var previewClassName = windows.StringToUTF16Ptr(previewClassText)

var (
	previewClassOnce sync.Once
	previewClassErr  error
)

// Preview shows the quick preview panel for paths, replacing the items of
// an open panel. Missing paths and items no handler claims are skipped; a
// call left with nothing to show is an error. Main thread only.
func Preview(paths []string) error {
	if err := Init(); err != nil {
		return err
	}
	items := make([]previewItem, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		clsid, ok := previewHandlerCLSID(p, info.IsDir())
		if !ok {
			continue
		}
		items = append(items, previewItem{path: p, clsid: clsid})
	}
	if len(items) == 0 {
		return errors.New("windows: no preview handler available for the selected items")
	}

	if previewState.hwnd != 0 && !previewState.closing {
		previewState.items = items
		previewState.index = 0
		if err := previewState.showFirst(); err != nil {
			previewState.close()
			return err
		}
		return nil
	}

	if err := previewRegisterClass(); err != nil {
		return err
	}
	x, y, w, h := previewPlacement()
	hwnd, err := previewCreateWindow(previewTitle(items[0].path, 0, len(items)), x, y, w, h)
	if err != nil {
		return err
	}
	previewState.hwnd = hwnd
	previewState.items = items
	previewState.index = 0
	previewState.handler = nil
	previewState.closing = false
	if err := previewState.showFirst(); err != nil {
		previewState.close()
		return err
	}
	procShowWindow.Call(hwnd, pvSWShownormal)
	reorderFront(hwnd, true)
	procSetForegroundWindow.Call(hwnd)
	procPVSetFocus.Call(hwnd)
	return nil
}

// PreviewVisible reports whether the preview panel is open.
func PreviewVisible() bool {
	return previewState.hwnd != 0 && !previewState.closing
}

// ClosePreview closes the preview panel; no-op when it is already closed.
// Main thread only.
func ClosePreview() { previewState.close() }

// showFirst loads the first item that actually initializes. The registry
// filter already dropped items without a handler; a handler that still
// fails to start is skipped rather than aborting the whole panel.
func (p *previewPanel) showFirst() error {
	var first error
	for i := range p.items {
		err := p.load(i)
		if err == nil {
			return nil
		}
		if first == nil {
			first = fmt.Errorf("%s: %w", p.items[i].path, err)
		}
	}
	return fmt.Errorf("windows: preview handler failed to start: %w", first)
}

// load replaces the handler with items[index] and previews it. The window
// size and title follow the item.
func (p *previewPanel) load(index int) error {
	if p.hwnd == 0 || p.closing || index < 0 || index >= len(p.items) {
		return errors.New("windows: preview panel is not showing this item")
	}
	p.releaseHandler()
	it := p.items[index]
	handler, err := previewOpenHandler(it.clsid, it.path)
	if err != nil {
		return err
	}
	var rc winRect
	procGetClientRect.Call(p.hwnd, uintptr(unsafe.Pointer(&rc)))
	if hr := previewComCall(handler, pvMethodSetWindow, p.hwnd, uintptr(unsafe.Pointer(&rc))); previewFailed(hr) {
		runtime.KeepAlive(&rc)
		previewRelease(handler)
		return fmt.Errorf("windows: preview handler SetWindow failed (0x%08x)", uint32(hr))
	}
	if hr := previewComCall(handler, pvMethodSetRect, uintptr(unsafe.Pointer(&rc))); previewFailed(hr) {
		runtime.KeepAlive(&rc)
		previewRelease(handler)
		return fmt.Errorf("windows: preview handler SetRect failed (0x%08x)", uint32(hr))
	}
	runtime.KeepAlive(&rc)
	p.handler = handler
	p.index = index
	previewSetTitle(p.hwnd, previewTitle(it.path, index, len(p.items)))
	if hr := previewComCall(handler, pvMethodDoPreview); previewFailed(hr) {
		p.releaseHandler()
		return fmt.Errorf("windows: preview handler DoPreview failed (0x%08x)", uint32(hr))
	}
	// The panel itself keeps the keyboard: the handler draws the content,
	// while Space, Esc and the arrow keys belong to the host.
	procPVSetFocus.Call(p.hwnd)
	return nil
}

// step switches to the next (delta=1) or previous (delta=-1) item,
// wrapping around and skipping items that fail to load.
func (p *previewPanel) step(delta int) {
	n := len(p.items)
	if p.handler == nil || n < 2 {
		return
	}
	from := p.index
	cand := from
	for range n {
		cand = previewStep(cand, delta, n)
		if cand == from {
			break
		}
		if err := p.load(cand); err == nil {
			return
		}
	}
	// Every other item failed; put the one that was showing back.
	_ = p.load(from)
}

// resize hands the handler the new client rectangle after a window size
// change. SetWindow is only for the initial hookup.
func (p *previewPanel) resize() {
	if p.hwnd == 0 || p.handler == nil || p.closing {
		return
	}
	var rc winRect
	procGetClientRect.Call(p.hwnd, uintptr(unsafe.Pointer(&rc)))
	previewComCall(p.handler, pvMethodSetRect, uintptr(unsafe.Pointer(&rc)))
	runtime.KeepAlive(&rc)
}

// close releases the handler and destroys the window synchronously.
func (p *previewPanel) close() {
	if p.closing || (p.hwnd == 0 && p.handler == nil) {
		return
	}
	p.closing = true
	p.releaseHandler()
	hwnd := p.hwnd
	p.hwnd = 0
	p.items = nil
	p.index = 0
	if hwnd != 0 {
		procDestroyWindow.Call(hwnd)
	}
	p.closing = false
}

// releaseHandler unloads the handler, detaches it from the window and drops
// the COM reference.
func (p *previewPanel) releaseHandler() {
	handler := p.handler
	if handler == nil {
		return
	}
	p.handler = nil
	previewComCall(handler, pvMethodUnload)
	var rc winRect
	previewComCall(handler, pvMethodSetWindow, 0, uintptr(unsafe.Pointer(&rc)))
	runtime.KeepAlive(&rc)
	previewRelease(handler)
}

// --- the window -------------------------------------------------------------

func previewRegisterClass() error {
	previewClassOnce.Do(func() {
		instance, _, _ := procPVGetModuleHandle.Call(0)
		cursor, _, _ := procPVLoadCursor.Call(0, pvIDCArrow)
		wc := previewWndClassEx{
			style:         0x0003, // CS_HREDRAW | CS_VREDRAW
			lpfnWndProc:   windows.NewCallback(previewWndProc),
			hInstance:     instance,
			hCursor:       cursor,
			hbrBackground: 6, // COLOR_WINDOW + 1
			lpszClassName: previewClassName,
		}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		if r, _, err := procPVRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			previewClassErr = fmt.Errorf("windows: preview window class: %w", err)
		}
	})
	return previewClassErr
}

func previewCreateWindow(title string, x, y, w, h int32) (uintptr, error) {
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0, err
	}
	instance, _, _ := procPVGetModuleHandle.Call(0)
	hwnd, _, callErr := procCreateWindowEx.Call(
		previewExStyle, uintptr(unsafe.Pointer(previewClassName)), uintptr(unsafe.Pointer(titlePtr)),
		previewStyle, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, instance, 0)
	runtime.KeepAlive(titlePtr)
	if hwnd == 0 {
		return 0, fmt.Errorf("windows: preview window: %w", callErr)
	}
	// Dark title bar, matching the shelf.
	SetWindowAppearance(hwnd, true)
	return hwnd, nil
}

// previewPlacement sizes the panel at about 900x640 DIPs, clamped to the
// work area of the display under the pointer and centered there. The DIP
// rectangle goes through the shared display conversion so a 150% display
// gets the same physical size the rest of the app would.
func previewPlacement() (x, y, w, h int32) {
	ms := enumMonitors()
	var pt winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	cur := pixelsToDIPIn(ms, winRect{Left: pt.X, Top: pt.Y, Right: pt.X + 1, Bottom: pt.Y + 1})
	work := Rect{Width: previewDefaultW, Height: previewDefaultH}
	if i := displayAt(ms, int(cur.X), int(cur.Y)); i >= 0 {
		work = pixelsToDIPIn(ms, ms[i].work)
	}
	place := previewWindowRect(work, previewDefaultW, previewDefaultH)
	p := dipToPixelsIn(ms, place)
	return p.Left, p.Top, p.width(), p.height()
}

// previewWindowRect centers a width x height window in work, shrinking it
// to leave previewMargin on each side of a small display.
func previewWindowRect(work Rect, width, height float64) Rect {
	if work.Width <= 0 || work.Height <= 0 {
		return Rect{Width: width, Height: height}
	}
	w, h := width, height
	if limit := work.Width - 2*previewMargin; w > limit {
		w = math.Max(limit, 1)
	}
	if limit := work.Height - 2*previewMargin; h > limit {
		h = math.Max(limit, 1)
	}
	return Rect{
		X:      work.X + (work.Width-w)/2,
		Y:      work.Y + (work.Height-h)/2,
		Width:  w,
		Height: h,
	}
}

func previewWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case pvWMSize:
		previewState.resize()
		return 0
	case pvWMKeyDown:
		switch wparam {
		case pvVKEscape, pvVKSpace:
			previewState.close()
			return 0
		case pvVKLeft, pvVKUp:
			previewState.step(-1)
			return 0
		case pvVKRight, pvVKDown:
			previewState.step(1)
			return 0
		}
	case pvWMClose:
		previewState.close()
		return 0
	case pvWMDestroy:
		if previewState.hwnd == hwnd {
			previewState.hwnd = 0
		}
		return 0
	}
	r, _, _ := procPVDefWindowProc.Call(hwnd, msg, wparam, lparam)
	return r
}

// previewSetTitle shows the file name and the position in the set.
func previewSetTitle(hwnd uintptr, title string) {
	p, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	procPVSetWindowText.Call(hwnd, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}

func previewTitle(path string, index, count int) string {
	return fmt.Sprintf("%s — %d / %d", filepath.Base(path), index+1, count)
}

// previewStep moves index by delta inside [0, count) and wraps.
func previewStep(index, delta, count int) int {
	if count <= 0 {
		return 0
	}
	return ((index+delta)%count + count) % count
}

// --- the handler ------------------------------------------------------------

// previewRegistryProbe is one registration key to test for a handler.
type previewRegistryProbe struct {
	root registry.Key
	key  string
}

// previewHandlerCLSID finds the handler Explorer would use: the per-user
// registration first, then HKCR, then the SystemFileAssociations form of
// both, and finally the file's perceived type ("text", "image", ...),
// which is where the inbox handlers for whole classes of files live.
func previewHandlerCLSID(path string, isDir bool) (windows.GUID, bool) {
	suffix := `\ShellEx\` + previewHandlerClass
	for _, ext := range previewExtensions(path, isDir) {
		for _, probe := range [...]previewRegistryProbe{
			{registry.CURRENT_USER, `Software\Classes\` + ext + suffix},
			{registry.CLASSES_ROOT, ext + suffix},
			{registry.CURRENT_USER, `Software\Classes\SystemFileAssociations\` + ext + suffix},
			{registry.CLASSES_ROOT, `SystemFileAssociations\` + ext + suffix},
		} {
			if clsid, ok := previewCLSIDAt(probe); ok {
				return clsid, true
			}
		}
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return windows.GUID{}, false
	}
	perceived := previewPerceivedType(ext)
	if perceived == "" {
		return windows.GUID{}, false
	}
	for _, probe := range [...]previewRegistryProbe{
		{registry.CURRENT_USER, `Software\Classes\SystemFileAssociations\` + perceived + suffix},
		{registry.CLASSES_ROOT, `SystemFileAssociations\` + perceived + suffix},
	} {
		if clsid, ok := previewCLSIDAt(probe); ok {
			return clsid, true
		}
	}
	return windows.GUID{}, false
}

func previewCLSIDAt(probe previewRegistryProbe) (windows.GUID, bool) {
	value := previewStringValue(probe.root, probe.key, "")
	if value == "" {
		return windows.GUID{}, false
	}
	clsid, err := previewParseCLSID(value)
	if err != nil {
		return windows.GUID{}, false
	}
	return clsid, true
}

// previewPerceivedType reads the perceived type of an extension, from its
// own registration or from the ProgID the extension points at.
func previewPerceivedType(ext string) string {
	for _, probe := range [...]previewRegistryProbe{
		{registry.CURRENT_USER, `Software\Classes` + ext},
		{registry.CLASSES_ROOT, ext},
	} {
		if v := previewStringValue(probe.root, probe.key, "PerceivedType"); v != "" {
			return strings.ToLower(v)
		}
	}
	progID := previewStringValue(registry.CLASSES_ROOT, ext, "")
	if progID != "" {
		if v := previewStringValue(registry.CLASSES_ROOT, progID, "PerceivedType"); v != "" {
			return strings.ToLower(v)
		}
	}
	return ""
}

func previewStringValue(root registry.Key, key, name string) string {
	k, err := registry.OpenKey(root, key, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	value, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return value
}

// previewExtensions lists the registry key names to try for path. The
// lower-case extension comes first; "*" is the all-files registration.
func previewExtensions(path string, isDir bool) []string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" {
		return []string{ext, "*"}
	}
	if isDir {
		return []string{"Directory"}
	}
	return []string{"*"}
}

func previewParseCLSID(value string) (windows.GUID, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") {
		value = "{" + value + "}"
	}
	return windows.GUIDFromString(value)
}

// previewOpenHandler creates the handler, initializes it with the item and
// returns its IPreviewHandler.
func previewOpenHandler(clsid windows.GUID, path string) (*previewComObject, error) {
	var obj *previewComObject
	hr, _, _ := procPVCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsid)), 0, pvClsctx,
		uintptr(unsafe.Pointer(&iidPVIUnknown)), uintptr(unsafe.Pointer(&obj)))
	runtime.KeepAlive(&clsid)
	if previewFailed(hr) || obj == nil {
		return nil, fmt.Errorf("windows: preview handler invocation failed (0x%08x)", uint32(hr))
	}
	defer previewRelease(obj)

	if err := previewInitialize(obj, path); err != nil {
		return nil, err
	}
	handler := previewQueryInterface(obj, &iidPVPreviewHandler)
	if handler == nil {
		return nil, errors.New("windows: preview handler lacks IPreviewHandler")
	}
	return handler, nil
}

// previewInitialize calls the first initializer interface the object
// supports, in the order the shell documents for preview handlers.
func previewInitialize(obj *previewComObject, path string) error {
	if init := previewQueryInterface(obj, &iidPVInitializeWithFile); init != nil {
		defer previewRelease(init)
		return previewInitializeWithFile(init, path)
	}
	if init := previewQueryInterface(obj, &iidPVInitializeWithItem); init != nil {
		defer previewRelease(init)
		return previewInitializeWithItem(init, path)
	}
	if init := previewQueryInterface(obj, &iidPVInitializeWithStream); init != nil {
		defer previewRelease(init)
		return previewInitializeWithStream(init, path)
	}
	return errors.New("windows: preview handler exposes no initializer")
}

func previewInitializeWithFile(init *previewComObject, path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	hr := previewComCall(init, pvMethodInitialize, uintptr(unsafe.Pointer(p)), pvStgmRead)
	runtime.KeepAlive(p)
	return previewCheck("IInitializeWithFile", hr)
}

func previewInitializeWithItem(init *previewComObject, path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var item *previewComObject
	hr, _, _ := procPVSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&iidPVShellItem)), uintptr(unsafe.Pointer(&item)))
	runtime.KeepAlive(p)
	if previewFailed(hr) || item == nil {
		return fmt.Errorf("windows: preview item creation failed (0x%08x)", uint32(hr))
	}
	defer previewRelease(item)
	return previewCheck("IInitializeWithItem",
		previewComCall(init, pvMethodInitialize, uintptr(unsafe.Pointer(item)), 0))
}

func previewInitializeWithStream(init *previewComObject, path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var stream *previewComObject
	hr, _, _ := procPVSHCreateStreamOnFileEx.Call(
		uintptr(unsafe.Pointer(p)), pvStgmRead, 0, 0, 0, uintptr(unsafe.Pointer(&stream)))
	runtime.KeepAlive(p)
	if previewFailed(hr) || stream == nil {
		return fmt.Errorf("windows: preview stream creation failed (0x%08x)", uint32(hr))
	}
	defer previewRelease(stream)
	return previewCheck("IInitializeWithStream",
		previewComCall(init, pvMethodInitialize, uintptr(unsafe.Pointer(stream)), 0))
}

func previewCheck(what string, hr uintptr) error {
	if previewFailed(hr) {
		return fmt.Errorf("windows: preview %s failed (0x%08x)", what, uint32(hr))
	}
	return nil
}

func previewFailed(hr uintptr) bool { return int32(hr) < 0 }

// previewComCall calls method of the COM object p (a vtable call).
func previewComCall(p *previewComObject, method int, args ...uintptr) uintptr {
	fn := p.vtable[method]
	call := make([]uintptr, 0, len(args)+1)
	call = append(call, uintptr(unsafe.Pointer(p)))
	call = append(call, args...)
	r, _, _ := syscall.SyscallN(fn, call...)
	runtime.KeepAlive(p)
	return r
}

func previewQueryInterface(p *previewComObject, iid *windows.GUID) *previewComObject {
	var out *previewComObject
	hr := previewComCall(p, pvMethodQueryInterface,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(iid)
	if previewFailed(hr) {
		return nil
	}
	return out
}

func previewRelease(p *previewComObject) {
	if p != nil {
		previewComCall(p, pvMethodRelease)
	}
}
