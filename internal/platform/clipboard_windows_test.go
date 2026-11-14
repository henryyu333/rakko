//go:build windows

package platform

import (
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestClassifyClipboardWindows checks the Windows format names the
// recorder classifies: the CF_ constants, the registered names browsers
// and Office use, the privacy markers, and the preference order between a
// text and an image representation.
func TestClassifyClipboardWindows(t *testing.T) {
	cases := []struct {
		name  string
		types []string
		want  ClipboardKind
	}{
		{"empty waits", nil, ClipboardNotReady},
		{"unicode text", []string{"CF_UNICODETEXT"}, ClipboardText},
		{"legacy text", []string{"CF_TEXT"}, ClipboardText},
		{"plain text registered", []string{"text/plain"}, ClipboardText},
		{"dib", []string{"CF_DIB"}, ClipboardImage},
		{"dibv5 before dib", []string{"CF_DIB", "CF_DIBV5"}, ClipboardImage},
		{"png", []string{"image/png"}, ClipboardImage},
		{"png before text", []string{"image/png", "CF_UNICODETEXT"}, ClipboardImage},
		{"text before png", []string{"CF_UNICODETEXT", "image/png"}, ClipboardText},
		{"files are skipped", []string{"CF_HDROP"}, ClipboardSkip},
		{"file descriptors are skipped", []string{"FileGroupDescriptorW"}, ClipboardSkip},
		{"shell items are skipped", []string{"Shell IDList Array"}, ClipboardSkip},
		{"exclude marker wins", []string{"CF_UNICODETEXT", "ExcludeClipboardContentFromMonitorProcessing"}, ClipboardSkip},
		{"history marker wins", []string{"CF_UNICODETEXT", "CanIncludeInClipboardHistory"}, ClipboardSkip},
		{"viewer marker wins", []string{"CF_UNICODETEXT", "Clipboard Viewer Ignore"}, ClipboardSkip},
		{"nothing usable", []string{"Rich Text Format"}, ClipboardSkip},
	}
	for _, c := range cases {
		if got := ClassifyClipboard(c.types); got != c.want {
			t.Errorf("%s: ClassifyClipboard(%q) = %v, want %v", c.name, c.types, got, c.want)
		}
	}
}

// TestDragFormatsWindows checks the Windows format names the drag probes
// recognizes when a caller does supply declared types.
func TestDragFormatsWindows(t *testing.T) {
	if !DragHasFiles([]string{"CF_HDROP"}) {
		t.Error("DragHasFiles(CF_HDROP) = false")
	}
	if !DragHasFiles([]string{"FileGroupDescriptorW"}) {
		t.Error("DragHasFiles(FileGroupDescriptorW) = false")
	}
	if DragHasFiles([]string{"CF_UNICODETEXT"}) {
		t.Error("DragHasFiles(text) = true")
	}
	if !DragHasImages([]string{"image/png"}) {
		t.Error("DragHasImages(image/png) = false")
	}
	if DragHasImages([]string{"CF_HDROP"}) {
		t.Error("DragHasImages(files) = true")
	}
	if DragHasFilePromise([]string{"whatever"}) {
		t.Error("DragHasFilePromise fired on Windows")
	}
}

// TestClipboardFormatNamesWindows checks the names ClipboardTypes gives
// the predefined formats, and that ids the classifier does not use stay
// out.
func TestClipboardFormatNamesWindows(t *testing.T) {
	cases := map[uint16]string{
		cfText:        "CF_TEXT",
		cfOEMText:     "CF_OEMTEXT",
		cfDIB:         "CF_DIB",
		cfUnicodeText: "CF_UNICODETEXT",
		cfHDROP:       "CF_HDROP",
		cfDIBV5:       "CF_DIBV5",
		2:             "", // CF_BITMAP: the classifier reads no icon bitmap
	}
	for id, want := range cases {
		if got := clipboardFormatName(id); got != want {
			t.Errorf("clipboardFormatName(%d) = %q, want %q", id, got, want)
		}
	}
}

// --- live clipboard behaviour ---------------------------------------------

var (
	procTestSetText    = sysProc(user32, "SetClipboardData")
	procTestEmpty      = sysProc(user32, "EmptyClipboard")
	procTestOpen       = sysProc(user32, "OpenClipboard")
	procTestClose      = sysProc(user32, "CloseClipboard")
	procTestRegister   = sysProc(user32, "RegisterClipboardFormatW")
	procTestAlloc      = sysProc(kernel32, "GlobalAlloc")
	procTestLock       = sysProc(kernel32, "GlobalLock")
	procTestUnlock     = sysProc(kernel32, "GlobalUnlock")
	procTestGetData    = sysProc(user32, "GetClipboardData")
	procTestGlobalSize = sysProc(kernel32, "GlobalSize")
)

// withClipboard runs fn with the clipboard open, restoring the clipboard's
// previous text afterwards (the test machine is shared, and the platform
// rule says a test that writes the clipboard puts it back).
func withClipboard(t *testing.T, fn func()) {
	t.Helper()
	before, hadBefore := clipboardText()
	if r, _, _ := procTestOpen.Call(0); r == 0 {
		t.Skip("clipboard held by another process")
	}
	procTestEmpty.Call()
	fn()
	procTestClose.Call()
	if hadBefore {
		setClipboardText(before)
	} else {
		if r, _, _ := procTestOpen.Call(0); r != 0 {
			procTestEmpty.Call()
			procTestClose.Call()
		}
	}
}

func clipboardText() (string, bool) {
	h, _, _ := procTestGetData.Call(uintptr(cfUnicodeText))
	if h == 0 {
		return "", false
	}
	p, _, _ := procTestLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer procTestUnlock.Call(h)
	n, _, _ := procTestGlobalSize.Call(h)
	var ptr unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&ptr)) = p
	return windows.UTF16ToString(unsafe.Slice((*uint16)(ptr), n/2)), true
}

func setClipboardText(s string) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	h, _, _ := procTestAlloc.Call(0x0042 /* GMEM_MOVEABLE */, uintptr(len(u)*2))
	if h == 0 {
		return
	}
	p, _, _ := procTestLock.Call(h)
	if p == 0 {
		return
	}
	var ptr unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&ptr)) = p
	copy(unsafe.Slice((*uint16)(ptr), len(u)), u)
	procTestUnlock.Call(h)
	if r, _, _ := procTestOpen.Call(0); r == 0 {
		return
	}
	procTestEmpty.Call()
	procTestSetText.Call(uintptr(cfUnicodeText), h)
	procTestClose.Call()
}

// setClipboardDWORD writes a registered format carrying a DWORD, next to
// the text already on the clipboard.
func setClipboardDWORD(t *testing.T, name string, v uint32) {
	t.Helper()
	u, err := windows.UTF16FromString(name)
	if err != nil {
		t.Fatal(err)
	}
	id, _, _ := procTestRegister.Call(uintptr(unsafe.Pointer(&u[0])))
	if id == 0 {
		t.Fatalf("register %q failed", name)
	}
	h, _, _ := procTestAlloc.Call(0x0042, 4)
	if h == 0 {
		t.Fatal("GlobalAlloc failed")
	}
	p, _, _ := procTestLock.Call(h)
	if p == 0 {
		t.Fatal("GlobalLock failed")
	}
	var ptr unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&ptr)) = p
	*(*uint32)(ptr) = v
	procTestUnlock.Call(h)
	if r, _, _ := procTestOpen.Call(0); r == 0 {
		t.Fatal("clipboard held")
	}
	procTestSetText.Call(id, h)
	procTestClose.Call()
}

// TestClipboardLiveWindows exercises the real clipboard: the sequence
// number moves on a write, the declared formats classify as text, and the
// privacy markers are resolved by their DWORD value.
func TestClipboardLiveWindows(t *testing.T) {
	before := ClipboardChangeCount()
	withClipboard(t, func() {
		setClipboardText("rakko clipboard test")
		types := ClipboardTypes()
		if !slices.Contains(types, "CF_UNICODETEXT") {
			t.Fatalf("types = %v, want CF_UNICODETEXT", types)
		}
		if got := ClassifyClipboard(types); got != ClipboardText {
			t.Errorf("ClassifyClipboard(%v) = %v, want text", types, got)
		}
		if after := ClipboardChangeCount(); after <= before {
			t.Errorf("change count did not move: %d -> %d", before, after)
		}

		// A marker asking history to stay out.
		setClipboardDWORD(t, "ExcludeClipboardContentFromMonitorProcessing", 0)
		if got := ClassifyClipboard(ClipboardTypes()); got != ClipboardSkip {
			t.Errorf("exclude marker: got %v, want skip", got)
		}
	})
	// The opt-in markers only exclude with a zero value.
	withClipboard(t, func() {
		setClipboardText("hist")
		setClipboardDWORD(t, "CanIncludeInClipboardHistory", 0)
		if got := ClassifyClipboard(ClipboardTypes()); got != ClipboardSkip {
			t.Errorf("history=0: got %v, want skip", got)
		}
		setClipboardDWORD(t, "CanIncludeInClipboardHistory", 1)
		if got := ClassifyClipboard(ClipboardTypes()); got != ClipboardText {
			t.Errorf("history=1: got %v, want text", got)
		}
	})
}
