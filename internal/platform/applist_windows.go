//go:build windows

package platform

// The shell side of applications: the Apps folder the Start Menu and
// `shell:AppsFolder` enumerate, the known-folder paths the search fallback
// scans, and the shell item icons the rows draw.

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/image/draw"
	"golang.org/x/sys/windows"
)

var (
	procSHGetKnownFolderItem    = sysProc(shell32, "SHGetKnownFolderItem")
	procSHCreateItemFromParsing = sysProc(shell32, "SHCreateItemFromParsingName")
)

var (
	folderIDAppsFolder = windows.GUID{
		Data1: 0x1E87508D, Data2: 0x89C2, Data3: 0x42F0,
		Data4: [8]byte{0x8A, 0x7E, 0x64, 0x5A, 0x0F, 0x50, 0xCA, 0x58},
	}
	bhidEnumItems = windows.GUID{
		Data1: 0x94F60519, Data2: 0x2850, Data3: 0x4924,
		Data4: [8]byte{0xAA, 0x5A, 0xD1, 0x5E, 0x84, 0x86, 0x80, 0x39},
	}
	iidIShellItem = windows.GUID{
		Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE,
		Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE},
	}
	iidIEnumShellItems = windows.GUID{
		Data1: 0x70629033, Data2: 0xE363, Data3: 0x4A28,
		Data4: [8]byte{0xA5, 0x67, 0x0D, 0xB7, 0x80, 0x06, 0xE6, 0xD7},
	}
	iidIShellItemImageFactory = windows.GUID{
		Data1: 0xBCC18B79, Data2: 0xBA16, Data3: 0x442F,
		Data4: [8]byte{0x80, 0xC4, 0x8A, 0x59, 0xC3, 0x0C, 0x46, 0x3B},
	}
)

const (
	sigdnNormalDisplay          = 0x00000000
	sigdnFileSysPath            = 0x80058000
	sigdnDesktopAbsoluteParsing = 0x80028000
	siigbfBiggerSizeOk          = 0x1
	siigbfIconOnly              = 0x4

	knownFolderFlagDefault = 0
)

// appsFolderEntries enumerates the Apps folder: every application the
// Start Menu shows, packaged and desktop alike.
func appsFolderEntries() ([]appEntry, error) {
	// The enumeration is one COM apartment's work: keep it on one thread
	// and give the apartment back when it ends, so the thread can join
	// another mode's apartment when Go reuses it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	leave, _ := enterCOMApartment(coinitApartmentThreaded)
	defer leave()
	var folder unsafe.Pointer
	hr, _, _ := procSHGetKnownFolderItem.Call(
		uintptr(unsafe.Pointer(&folderIDAppsFolder)), knownFolderFlagDefault, 0,
		uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&folder)))
	if failedHR(hr) || folder == nil {
		return nil, errors.New("platform: the Apps folder is unavailable")
	}
	defer winCOMRelease(folder)
	var enum unsafe.Pointer
	hr = winCOMCall(folder, 3, 0,
		uintptr(unsafe.Pointer(&bhidEnumItems)),
		uintptr(unsafe.Pointer(&iidIEnumShellItems)),
		uintptr(unsafe.Pointer(&enum)))
	if failedHR(hr) || enum == nil {
		return nil, errors.New("platform: the Apps folder cannot be enumerated")
	}
	defer winCOMRelease(enum)
	seen := map[string]bool{}
	var out []appEntry
	for {
		var child unsafe.Pointer
		var fetched uintptr
		hr := winCOMCall(enum, 3, 1, uintptr(unsafe.Pointer(&child)), uintptr(unsafe.Pointer(&fetched)))
		if failedHR(hr) || fetched == 0 || child == nil {
			break
		}
		name := shellDisplayName(child, sigdnNormalDisplay)
		target := shellDisplayName(child, sigdnDesktopAbsoluteParsing)
		// A desktop app often answers its executable path here, which the
		// shell opens directly; a packaged app has none.
		if fsPath := shellDisplayName(child, sigdnFileSysPath); fsPath != "" {
			target = fsPath
		}
		winCOMRelease(child)
		if name == "" || !appTargetUsable(target) {
			continue
		}
		target = appTargetPath(target)
		if seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, appEntry{Name: name, Target: target})
	}
	if len(out) == 0 {
		return nil, errors.New("platform: the Apps folder listed nothing")
	}
	return out, nil
}

// shellDisplayName reads one SIGDN name of a shell item.
func shellDisplayName(item unsafe.Pointer, form uintptr) string {
	var p unsafe.Pointer
	hr := winCOMCall(item, 5, form, uintptr(unsafe.Pointer(&p)))
	if failedHR(hr) || p == nil {
		return ""
	}
	defer windows.CoTaskMemFree(p)
	return windows.UTF16PtrToString((*uint16)(p))
}

// appTargetUsable keeps the entries that name an application: a shell
// namespace item (the Recycle Bin, a Control Panel page), a web link or a
// document shortcut is not one.
func appTargetUsable(target string) bool {
	if target == "" || strings.HasPrefix(target, "::{") || strings.Contains(target, "://") {
		return false
	}
	if strings.Contains(target, `:\`) || strings.HasPrefix(target, `\\`) {
		switch strings.ToLower(filepath.Ext(target)) {
		case ".exe", ".lnk", ".bat", ".cmd", ".com", ".msc":
			return true
		}
		return false
	}
	return true // an AppUserModelID
}

// appTargetPath is what the shell opens for the entry: a file path as it
// is, a packaged app through the Apps folder.
func appTargetPath(target string) string {
	if strings.HasPrefix(target, "shell:") || strings.Contains(target, `:\`) || strings.HasPrefix(target, `\\`) {
		return target
	}
	return `shell:AppsFolder\` + target
}

// knownFolderPath resolves one known folder to its file system path;
// "" when it does not exist (a deleted Downloads folder).
func knownFolderPath(id *windows.GUID) string {
	var p unsafe.Pointer
	r, _, _ := procSHGetKnownFolderPath.Call(
		uintptr(unsafe.Pointer(id)), knownFolderFlagDefault, 0, uintptr(unsafe.Pointer(&p)))
	if failedHR(r) || p == nil {
		return ""
	}
	defer windows.CoTaskMemFree(p)
	path := windows.UTF16PtrToString((*uint16)(p))
	if st, err := statPath(path); err != nil || !st {
		return ""
	}
	return path
}

// statPath reports whether path exists as a directory.
func statPath(path string) (bool, error) {
	if path == "" {
		return false, errors.New("empty path")
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false, err
	}
	const fileAttributeDirectory = 0x10
	return attrs&fileAttributeDirectory != 0, nil
}

// shellItemIconPNG asks the shell for the icon of a parsing name (a file
// path, a .lnk, or `shell:AppsFolder\…`) and returns it as PNG bytes.
func shellItemIconPNG(parsingName string, size int) []byte {
	if size <= 0 {
		size = 32
	}
	name16 := windows.StringToUTF16(parsingName)
	var factory unsafe.Pointer
	hr, _, _ := procSHCreateItemFromParsing.Call(
		uintptr(unsafe.Pointer(&name16[0])), 0,
		uintptr(unsafe.Pointer(&iidIShellItemImageFactory)),
		uintptr(unsafe.Pointer(&factory)))
	if failedHR(hr) || factory == nil {
		return nil
	}
	defer winCOMRelease(factory)
	var hbm uintptr
	// SIZE is two LONGs passed by value in one register.
	sizePacked := uintptr(uint32(size)) | uintptr(uint32(size))<<32
	hr = winCOMCall(factory, 3, sizePacked,
		siigbfBiggerSizeOk|siigbfIconOnly, uintptr(unsafe.Pointer(&hbm)))
	if failedHR(hr) || hbm == 0 {
		return nil
	}
	defer procDeleteObject.Call(hbm)
	return hbitmapPNG(hbm, size)
}

// hbitmapPNG converts a 32-bit HBITMAP into PNG bytes scaled to size.
func hbitmapPNG(hbm uintptr, size int) []byte {
	var bm winBitmap
	if r, _, _ := procGetObject.Call(hbm, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil
	}
	w, h := int(bm.bmWidth), int(bm.bmHeight)
	if w <= 0 || h <= 0 {
		return nil
	}
	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return nil
	}
	defer procDeleteDC.Call(dc)
	old, _, _ := procSelectObject.Call(dc, hbm)
	defer procSelectObject.Call(dc, old)
	bi := bitmapInfoHeader{
		biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:       int32(w),
		biHeight:      -int32(h),
		biPlanes:      1,
		biBitCount:    32,
		biCompression: 0,
	}
	buf := make([]byte, w*h*4)
	if r, _, _ := procGetDIBits.Call(dc, hbm, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0); r == 0 {
		return nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	anyAlpha := false
	for i := 3; i < len(buf); i += 4 {
		if buf[i] != 0 {
			anyAlpha = true
			break
		}
	}
	for i := range w * h {
		b, g, r, a := buf[i*4], buf[i*4+1], buf[i*4+2], buf[i*4+3]
		if !anyAlpha {
			a = 0xff
		}
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = r, g, b, a
	}
	var out image.Image = img
	if w != size || h != size {
		dst := image.NewNRGBA(image.Rect(0, 0, size, size))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
		out = dst
	}
	var enc bytes.Buffer
	if err := png.Encode(&enc, out); err != nil {
		return nil
	}
	return enc.Bytes()
}
