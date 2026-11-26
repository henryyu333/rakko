//go:build windows

package platform

// Clipboard: the change counter, the declared formats, and the clipboard's
// privacy markers. The Win32 clipboard is a shared, transient resource, so
// every read opens it, copies what it needs, and closes it; a failure to
// open (another process holds it) reports "not ready" and the recorder
// retries on its next tick, exactly as macOS' mid-publish pasteboard does.

import (
	"encoding/binary"
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32's predefined clipboard formats that the classifier names itself.
const (
	cfText        = 1
	cfOEMText     = 7
	cfDIB         = 8
	cfUnicodeText = 13
	cfHDROP       = 15
	cfDIBV5       = 17
	cfRegistered  = 0xC000 // the first id RegisterClipboardFormat assigns
)

var (
	// privateClipTypes are the markers password managers and similar
	// sources set to ask clipboard history tools not to record a copy.
	// ExcludeClipboardContentFromMonitorProcessing and Clipboard Viewer
	// Ignore are presence flags; CanIncludeInClipboardHistory and
	// CanUploadToCloudClipboard are DWORDs a source may also set to 1 to
	// opt back in, which ClipboardTypes resolves before classification.
	privateClipTypes = []string{
		"ExcludeClipboardContentFromMonitorProcessing",
		"CanIncludeInClipboardHistory",
		"CanUploadToCloudClipboard",
		"Clipboard Viewer Ignore",
	}
	// optInClipFormats are the marker formats whose DWORD value decides
	// whether the content is private: 0 asks managers to leave it out, any
	// other value explicitly allows it.
	optInClipFormats = []string{
		"CanIncludeInClipboardHistory",
		"CanUploadToCloudClipboard",
	}
	clipTextTypes  = []string{"CF_UNICODETEXT", "CF_TEXT", "CF_OEMTEXT", "text/plain"}
	clipImageTypes = []string{"CF_DIBV5", "CF_DIB", "PNG", "image/png"}
	clipFileTypes  = []string{"CF_HDROP", "FileGroupDescriptorW", "FileGroupDescriptor", "Shell IDList Array", "text/uri-list", "application/x-mygo-file-list"}
)

var (
	procClipboardSeq   = sysProc(user32, "GetClipboardSequenceNumber")
	procOpenClipboard  = sysProc(user32, "OpenClipboard")
	procCloseClipboard = sysProc(user32, "CloseClipboard")
	procEmptyClipboard = sysProc(user32, "EmptyClipboard")
	procEnumFormats    = sysProc(user32, "EnumClipboardFormats")
	procFormatName     = sysProc(user32, "GetClipboardFormatNameW")
	procFormatAvail    = sysProc(user32, "IsClipboardFormatAvailable")
	procClipboardData  = sysProc(user32, "GetClipboardData")
	procGlobalLock     = sysProc(kernel32, "GlobalLock")
	procGlobalUnlock   = sysProc(kernel32, "GlobalUnlock")
	procGlobalSize     = sysProc(kernel32, "GlobalSize")
	procSendInput      = sysProc(user32, "SendInput")
)

// clipFormatIs reports whether name is one of set. Windows registers
// clipboard format names without regard to case, so a source that registered
// "excludeclipboardcontentfrommonitorprocessing" owns the same format as the
// documented spelling, and a case-sensitive comparison would let its content
// through.
func clipFormatIs(name string, set []string) bool {
	for _, s := range set {
		if strings.EqualFold(name, s) {
			return true
		}
	}
	return false
}

// ClassifyClipboard decides what a clipboard change should add, from the
// declared formats in the source's order of preference.
func ClassifyClipboard(types []string) ClipboardKind {
	return classifyClipboard(types, clipFormatIs, privateClipTypes, clipFileTypes, clipTextTypes, clipImageTypes)
}

// ClipboardChangeCount is the sequence number that increments for every
// clipboard mutation system-wide. It is readable from any thread and does
// not open the clipboard, so the recorder can poll it cheaply.
func ClipboardChangeCount() int64 {
	n, _, _ := procClipboardSeq.Call()
	return int64(n)
}

// ClipboardChangeCountFast reads the same counter from a worker thread.
// GetClipboardSequenceNumber takes no locks and needs no apartment, so
// there is nothing to prime.
func ClipboardChangeCountFast() int64 { return ClipboardChangeCount() }

// PrimeClipboardChangeCount prepares the worker-thread read.
func PrimeClipboardChangeCount() {}

// ClipboardTypes lists the formats the clipboard declares, in the order
// Windows enumerates them. Custom formats carry their registered names,
// the predefined ones the CF_ names the classifier knows. A clipboard that
// another process holds open reports no types: the change is still being
// published and the recorder waits for its next tick.
//
// CanIncludeInClipboardHistory / CanUploadToCloudClipboard are resolved
// here: their presence alone is not private, only a DWORD of 0 is.
func ClipboardTypes() []string {
	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		return nil
	}
	defer procCloseClipboard.Call()

	var out []string
	for format := uintptr(0); ; {
		format, _, _ = procEnumFormats.Call(format)
		if format == 0 {
			break
		}
		if name := clipboardFormatName(uint16(format)); name != "" {
			out = append(out, name)
		}
	}
	// Windows synthesizes Unicode text and DIBs for older clipboard
	// owners; mygo reads those too, so a source offering only CF_TEXT is
	// still recordable. The enumeration above already covers an owner
	// that declares them, so only add a synthesized one that is really
	// available.
	for _, id := range []uint16{cfUnicodeText, cfDIB} {
		if r, _, _ := procFormatAvail.Call(uintptr(id)); r == 0 {
			continue
		}
		if name := clipboardFormatName(id); name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	// Resolve the opt-in markers: drop a marker whose DWORD says the
	// content may be recorded, keeping it only when it asks to stay out.
	kept := out[:0]
	for _, name := range out {
		if clipFormatIs(name, optInClipFormats) && clipboardDWORD(name) != 0 {
			continue
		}
		kept = append(kept, name)
	}
	return slices.Clip(kept)
}

// clipboardFormatName is the classifier's name for an enumerated format:
// a CF_ constant for the predefined ids, the registered name otherwise.
func clipboardFormatName(format uint16) string {
	switch format {
	case cfText:
		return "CF_TEXT"
	case cfOEMText:
		return "CF_OEMTEXT"
	case cfDIB:
		return "CF_DIB"
	case cfUnicodeText:
		return "CF_UNICODETEXT"
	case cfHDROP:
		return "CF_HDROP"
	case cfDIBV5:
		return "CF_DIBV5"
	}
	if format < cfRegistered {
		return "" // a predefined format the classifier does not use
	}
	var buf [256]uint16
	n, _, _ := procFormatName.Call(uintptr(format), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// clipboardDWORD reads a registered format's DWORD payload, 0 when the
// clipboard has no such format or does not hold a DWORD. The clipboard
// must already be open.
func clipboardDWORD(name string) uint32 {
	var id uint16
	for format := uintptr(0); ; {
		format, _, _ = procEnumFormats.Call(format)
		if format == 0 {
			return 0
		}
		if strings.EqualFold(clipboardFormatName(uint16(format)), name) {
			id = uint16(format)
			break
		}
	}
	h, _, _ := procClipboardData.Call(uintptr(id))
	if h == 0 {
		return 0
	}
	n, _, _ := procGlobalSize.Call(h)
	if n < 4 {
		return 0
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return 0
	}
	defer procGlobalUnlock.Call(h)
	var ptr unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&ptr)) = p
	return binary.LittleEndian.Uint32(unsafe.Slice((*byte)(ptr), 4))
}

// keybdInput is INPUT's keyboard union member. INPUT is 40 bytes on
// 64-bit Windows: the type, one alignment word, this 24-byte member and
// its trailing alignment word.
type keybdInput struct {
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	extra uintptr
}

type input struct {
	typ uint32
	_   uint32
	ki  keybdInput
	_   [8]byte
}

const (
	keyEventKeyUp       = 0x0002
	keyEventExtendedKey = 0x0001
	vkControl           = 0x11
	vkV                 = 0x56
	vkLShift            = 0xA0
	vkRShift            = 0xA1
	vkLMenu             = 0xA4
	vkRMenu             = 0xA5
	vkLWin              = 0x5B
	vkRWin              = 0x5C
)

// pasteHeldKey is one key a shortcut may still hold down: the left and
// right variants are virtual keys of their own, and the right Alt has to be
// released as the extended key it went down as.
type pasteHeldKey struct {
	vk    uint16
	flags uint32
}

// pasteHeldModifiers are the keys a global shortcut may still be holding
// when the paste runs: Ctrl+Alt+V fires on the press, and injecting Ctrl+V
// while the physical Alt is down types AltGr+V — or nothing — instead. A
// right-hand AltGr is a key of its own, so the left and right variants are
// checked and released separately.
var pasteHeldModifiers = []pasteHeldKey{
	{vk: vkLMenu},
	{vk: vkRMenu, flags: keyEventExtendedKey},
	{vk: vkLShift},
	{vk: vkRShift},
	{vk: vkLWin},
	{vk: vkRWin},
}

// pasteEvents builds the input for Ctrl+V, releasing any of the shortcut's
// modifiers that are still physically down. All of it goes into one
// SendInput, in order, so no sleep or wait loop is needed; a keyup for a key
// that is up is simply not sent. Ctrl goes down before the releases: Alt
// released on its own opens the target window's menu bar, Alt released with
// Ctrl held does not. Pure; unit-tested.
func pasteEvents(down func(vk uint16) bool) []input {
	events := make([]input, 0, 4+len(pasteHeldModifiers))
	events = append(events, input{typ: 1, ki: keybdInput{vk: vkControl}})
	for _, k := range pasteHeldModifiers {
		if down(k.vk) {
			events = append(events, input{typ: 1, ki: keybdInput{vk: k.vk, flags: keyEventKeyUp | k.flags}})
		}
	}
	return append(events,
		input{typ: 1, ki: keybdInput{vk: vkV}},
		input{typ: 1, ki: keybdInput{vk: vkV, flags: keyEventKeyUp}},
		input{typ: 1, ki: keybdInput{vk: vkControl, flags: keyEventKeyUp}},
	)
}

// SendPasteShortcut sends Ctrl+V to the process pid. It refuses when pid
// is not the foreground process: the copy already happened, and a paste
// into whatever window has the keyboard would be worse than none.
//
// SendInput is blocked by UIPI from a normal process to an elevated one;
// then the injected count is 0 and the caller reports 已复制, the accepted
// Windows fallback for administrator windows. Main thread only.
func SendPasteShortcut(pid int) bool {
	if pid <= 0 {
		return false
	}
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == 0 || windowPID(fg) != pid {
		return false
	}
	events := pasteEvents(func(vk uint16) bool {
		r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
		return r&0x8000 != 0
	})
	sent, _, _ := procSendInput.Call(uintptr(len(events)), uintptr(unsafe.Pointer(&events[0])), unsafe.Sizeof(events[0]))
	return sent == uintptr(len(events))
}
