//go:build windows

package platform

import (
	"bytes"
	"slices"
	"strings"
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
		{"exclude marker in another case", []string{"CF_UNICODETEXT", "excludeclipboardcontentfrommonitorprocessing"}, ClipboardSkip},
		{"history marker in another case", []string{"CF_UNICODETEXT", "CANINCLUDEINCLIPBOARDHISTORY"}, ClipboardSkip},
		{"viewer marker in another case", []string{"CF_UNICODETEXT", "clipboard viewer ignore"}, ClipboardSkip},
		{"files in another case", []string{"cf_hdrop", "CF_UNICODETEXT"}, ClipboardSkip},
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
	procTestGlobalSize = sysProc(kernel32, "GlobalSize")
	procTestGlobalFree = sysProc(kernel32, "GlobalFree")
	procTestCopyImage  = sysProc(user32, "CopyImage")
	procTestDeleteObj  = sysProc(gdi32, "DeleteObject")
)

const gmemMoveable = 0x0042

// savedClip is one format the clipboard held before a test touched it. A
// custom format keeps its registered name so it can be registered again on
// restore; CF_BITMAP is a handle rather than an HGLOBAL and is copied with
// CopyImage.
type savedClip struct {
	format uint16
	name   string
	data   []byte
	bitmap uintptr
}

// saveClipboard copies every format the clipboard declares. It reports false
// when the clipboard is held open by another process or holds a format this
// test cannot duplicate (a delayed-render placeholder, a palette, a metafile):
// the test then skips rather than emptying content it cannot put back.
func saveClipboard() ([]savedClip, bool) {
	if r, _, _ := procTestOpen.Call(0); r == 0 {
		return nil, false
	}
	defer procTestClose.Call()

	var saved []savedClip
	for format := uintptr(0); ; {
		format, _, _ = procEnumFormats.Call(format)
		if format == 0 {
			break
		}
		clip := savedClip{format: uint16(format)}
		if format >= cfRegistered {
			clip.name = clipboardFormatName(uint16(format))
		}
		if clip.format == 2 /* CF_BITMAP */ {
			h, _, _ := procTestCopyImage.Call(format, 0, 0, 0)
			if h == 0 {
				return nil, false
			}
			clip.bitmap = h
		} else {
			h, _, _ := procClipboardData.Call(format)
			if h == 0 {
				return nil, false // rendered on request: no data to copy
			}
			size, _, _ := procTestGlobalSize.Call(h)
			p, _, _ := procTestLock.Call(h)
			if p == 0 {
				return nil, false
			}
			var ptr unsafe.Pointer
			*(*uintptr)(unsafe.Pointer(&ptr)) = p
			clip.data = make([]byte, size)
			copy(clip.data, unsafe.Slice((*byte)(ptr), size))
			procTestUnlock.Call(h)
		}
		saved = append(saved, clip)
	}
	return saved, true
}

// restoreClipboard puts the saved formats back, in the order the clipboard
// listed them, so the next reader sees the same preference order.
func restoreClipboard(t *testing.T, saved []savedClip) {
	t.Helper()
	if r, _, _ := procTestOpen.Call(0); r == 0 {
		t.Logf("clipboard busy: the original content was not restored")
		return
	}
	defer procTestClose.Call()
	procTestEmpty.Call()
	for i := len(saved) - 1; i >= 0; i-- {
		clip := saved[i]
		format := uintptr(clip.format)
		if clip.name != "" {
			u, err := windows.UTF16FromString(clip.name)
			if err != nil {
				continue
			}
			if id, _, _ := procTestRegister.Call(uintptr(unsafe.Pointer(&u[0]))); id != 0 {
				format = id
			}
		}
		if clip.bitmap != 0 {
			if r, _, _ := procTestSetText.Call(format, clip.bitmap); r == 0 {
				procTestDeleteObj.Call(clip.bitmap)
			}
			continue
		}
		h, _, _ := procTestAlloc.Call(gmemMoveable, uintptr(len(clip.data)))
		if h == 0 {
			t.Logf("could not restore clipboard format %d", clip.format)
			continue
		}
		p, _, _ := procTestLock.Call(h)
		if p == 0 {
			procTestGlobalFree.Call(h)
			t.Logf("could not restore clipboard format %d", clip.format)
			continue
		}
		var ptr unsafe.Pointer
		*(*uintptr)(unsafe.Pointer(&ptr)) = p
		copy(unsafe.Slice((*byte)(ptr), len(clip.data)), clip.data)
		procTestUnlock.Call(h)
		if r, _, _ := procTestSetText.Call(format, h); r == 0 {
			procTestGlobalFree.Call(h)
			t.Logf("could not restore clipboard format %d", clip.format)
		}
	}
}

// clipboardText reads the clipboard's Unicode text, false when there is
// none.
func clipboardText() (string, bool) {
	if r, _, _ := procTestOpen.Call(0); r == 0 {
		return "", false
	}
	defer procTestClose.Call()
	h, _, _ := procClipboardData.Call(uintptr(cfUnicodeText))
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

// withClipboard empties the clipboard for fn and puts every format it found
// back afterwards, including when fn calls t.Fatal. The test skips when the
// clipboard cannot be saved in full: the machine is shared and destroying a
// user's copy is worse than a skipped test.
func withClipboard(t *testing.T, fn func()) {
	t.Helper()
	saved, ok := saveClipboard()
	if !ok {
		t.Skip("clipboard holds content this test cannot restore, so it is left alone")
	}
	t.Cleanup(func() { restoreClipboard(t, saved) })
	if r, _, _ := procTestOpen.Call(0); r == 0 {
		t.Skip("clipboard held by another process")
	}
	procTestEmpty.Call()
	procTestClose.Call()
	fn()
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
	// The DWORD markers are found by name whatever case they were
	// registered with.
	withClipboard(t, func() {
		setClipboardText("case")
		setClipboardDWORD(t, "CanIncludeInClipboardHistory", 1)
		if r, _, _ := procTestOpen.Call(0); r == 0 {
			t.Fatal("clipboard held")
		}
		got := clipboardDWORD("cAnInClUdEiNcLiPbOaRdHiStOrY")
		procTestClose.Call()
		if got != 1 {
			t.Errorf("clipboardDWORD with another case = %d, want 1", got)
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

// setClipboardBytes puts a registered format with arbitrary bytes on the
// clipboard, next to whatever is there already.
func setClipboardBytes(t *testing.T, name string, data []byte) {
	t.Helper()
	u, err := windows.UTF16FromString(name)
	if err != nil {
		t.Fatal(err)
	}
	id, _, _ := procTestRegister.Call(uintptr(unsafe.Pointer(&u[0])))
	if id == 0 {
		t.Fatalf("register %q failed", name)
	}
	h, _, _ := procTestAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		t.Fatal("GlobalAlloc failed")
	}
	p, _, _ := procTestLock.Call(h)
	if p == 0 {
		t.Fatal("GlobalLock failed")
	}
	var ptr unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&ptr)) = p
	copy(unsafe.Slice((*byte)(ptr), len(data)), data)
	procTestUnlock.Call(h)
	if r, _, _ := procTestOpen.Call(0); r == 0 {
		t.Fatal("clipboard held")
	}
	if r, _, _ := procTestSetText.Call(id, h); r == 0 {
		procTestGlobalFree.Call(h)
		t.Fatal("SetClipboardData failed")
	}
	procTestClose.Call()
}

// clipboardFormatBytes reads a registered format's payload, nil when it is
// not on the clipboard.
func clipboardFormatBytes(t *testing.T, name string) []byte {
	t.Helper()
	if r, _, _ := procTestOpen.Call(0); r == 0 {
		t.Fatal("clipboard held")
	}
	defer procTestClose.Call()
	var id uintptr
	for format := uintptr(0); ; {
		format, _, _ = procEnumFormats.Call(format)
		if format == 0 {
			return nil
		}
		if strings.EqualFold(clipboardFormatName(uint16(format)), name) {
			id = format
			break
		}
	}
	h, _, _ := procClipboardData.Call(id)
	if h == 0 {
		return nil
	}
	size, _, _ := procTestGlobalSize.Call(h)
	p, _, _ := procTestLock.Call(h)
	if p == 0 {
		return nil
	}
	defer procTestUnlock.Call(h)
	var ptr unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&ptr)) = p
	out := make([]byte, size)
	copy(out, unsafe.Slice((*byte)(ptr), size))
	return out
}

// TestClipboardRestoreKeepsEveryFormat checks the helper the other live tests
// rely on: the clipboard goes back with the text and the custom binary format
// it had, not just the text. A restore that only handled text left custom
// formats behind for the next reader.
func TestClipboardRestoreKeepsEveryFormat(t *testing.T) {
	original, ok := saveClipboard()
	if !ok {
		t.Skip("clipboard holds content this test cannot restore, so it is left alone")
	}
	t.Cleanup(func() { restoreClipboard(t, original) })

	blob := []byte{0x01, 0x00, 0x02, 0x7f, 0xfe}
	withClipboard(t, func() {
		setClipboardText("rakko round trip")
		setClipboardBytes(t, "RakkoClipboardRoundTrip", blob)
	})

	saved, ok := saveClipboard()
	if !ok {
		t.Skip("clipboard holds content this test cannot restore, so it is left alone")
	}
	setClipboardText("clobbered")
	restoreClipboard(t, saved)

	if got, ok := clipboardText(); !ok || got != "rakko round trip" {
		t.Fatalf("text after restore = %q (present %v), want %q", got, ok, "rakko round trip")
	}
	if got := clipboardFormatBytes(t, "RakkoClipboardRoundTrip"); !bytes.Equal(got, blob) {
		t.Fatalf("custom format after restore = %v, want %v", got, blob)
	}
}
