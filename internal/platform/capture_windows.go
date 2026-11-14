//go:build windows

package platform

// Screen capture for the Windows build. A display is frozen with GDI into
// an RGBA bitmap: the screenshot overlay and the OCR region picker both
// draw that bitmap, so no capture API (and no consent prompt) is involved.
// Preview and sharing stay with their own later batches.

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
)

var (
	procGetDC         = sysProc(user32, "GetDC")
	procReleaseDC     = sysProc(user32, "ReleaseDC")
	procCreateDIBSect = sysProc(gdi32, "CreateDIBSection")
	procBitBlt        = sysProc(gdi32, "BitBlt")
)

// biRGB is the DIB compression of an uncompressed 32bpp bitmap.
const biRGB = 0

// bitmapInfo is BITMAPINFO with room for one palette entry.
type bitmapInfo struct {
	header bitmapInfoHeader
	colors [1]uint32
}

// CaptureDisplay freezes display index as a PNG at path. index is the
// display's one-based position in the monitor enumeration order — the same
// order the screen APIs hand out — and the picture is the display's full
// pixel rectangle.
func CaptureDisplay(index int, path string) error {
	ms := enumMonitors()
	if index < 1 || index > len(ms) {
		return fmt.Errorf("windows: display %d not found", index)
	}
	img, err := grabScreen(ms[index-1].bounds)
	if err != nil {
		return err
	}
	return writeAtomicFile(path, encodePNG(img))
}

// grabScreen copies r — in physical pixels — off the screen into RGBA.
func grabScreen(r winRect) (*image.RGBA, error) {
	w, h := int(r.width()), int(r.height())
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("windows: empty display rectangle")
	}
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return nil, fmt.Errorf("windows: no screen device context")
	}
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	if mem == 0 {
		return nil, fmt.Errorf("windows: no capture device context")
	}
	defer procDeleteDC.Call(mem)
	// Negative height asks for a top-down bitmap, which matches image.RGBA's
	// row order.
	info := bitmapInfo{header: bitmapInfoHeader{
		biWidth: int32(w), biHeight: -int32(h), biPlanes: 1, biBitCount: 32, biCompression: biRGB,
	}}
	info.header.biSize = uint32(unsafe.Sizeof(info.header))
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSect.Call(screen, uintptr(unsafe.Pointer(&info)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		return nil, fmt.Errorf("windows: cannot allocate the capture bitmap")
	}
	defer procDeleteObject.Call(hbm)
	old, _, _ := procSelectObject.Call(mem, hbm)
	defer procSelectObject.Call(mem, old)
	const (
		srcCopy    = 0x00CC0020
		captureBlt = 0x40000000 // include layered windows
	)
	ok, _, err := procBitBlt.Call(mem, 0, 0, uintptr(w), uintptr(h), screen,
		uintptr(uint32(int32(r.Left))), uintptr(uint32(int32(r.Top))), srcCopy|captureBlt)
	if ok == 0 {
		return nil, fmt.Errorf("windows: screen copy failed: %v", err)
	}
	bgra := unsafe.Slice((*byte)(bits), w*h*4)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, bgra)
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+2] = img.Pix[i+2], img.Pix[i]
		img.Pix[i+3] = 0xFF
	}
	return img, nil
}

// encodePNG encodes img for a capture file; the freeze is a temporary
// picture, so speed matters more than its size.
func encodePNG(img *image.RGBA) []byte {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		// png.Encoder only fails when the writer does; a buffer cannot.
		panic(err)
	}
	return buf.Bytes()
}

// PickScreenRegion runs a region picker over frozen displays and writes the
// chosen rectangle as a PNG to path. A cancelled pick (Esc) writes nothing,
// so a missing file means the user cancelled — the same contract as
// macOS's screencapture -i. It blocks until the user picks or cancels and
// may be called from any goroutine.
func PickScreenRegion(path string) error {
	// The caller folds its panel away just before asking; the fold runs on
	// the main thread, so give it the same moment the screenshot overlay
	// waits before freezing.
	time.Sleep(pickPanelSettle)
	done := make(chan error, 1)
	mygo.RunOnMain(func() {
		if err := openRegionPicker(path, done); err != nil {
			done <- err
		}
	})
	return <-done
}
