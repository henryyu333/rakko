//go:build windows

package platform

// PDF pages through Windows.Data.Pdf: the document is opened from a file
// stream, pages render as PNG at twice the page's size in points (the same
// pixel size the Mac build's Core Graphics render produces), and the PNG is
// decoded with the standard library so the requested format can be written.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"unsafe"
)

// PDF and storage identifiers (windows.data.pdf.h, windows.storage.h,
// windows.graphics.imaging.h).
var (
	wiIIDPdfDocumentStatics   = wiParseGUID("433a0b5f-c007-4788-90f2-08143d922599")
	wiIIDPdfPage              = wiParseGUID("9db4b0c8-5320-4cfc-ad76-493fdad0e594")
	wiIIDPdfRenderOptions     = wiParseGUID("3c98056f-b7cf-4c29-9a04-52d90267f425")
	wiIIDBitmapEncoderStatics = wiParseGUID("a74356a7-a4e4-4eb9-8e40-564de7e1ccb2")
	wiIIDStorageFileStatics   = wiParseGUID("5984c710-daf2-43c8-8bb4-a4d3eacfd03f")
	wiIIDStorageFile          = wiParseGUID("fa3f6186-4214-428c-a64c-14c9ac7315ea")
	wiIIDOutputStream         = wiParseGUID("905a0fe6-bc53-11df-8c49-001e4fc686da")
)

// Vtable slots, after IInspectable's six.
const (
	pdfStaticsLoadFromFile   = 6
	pdfStaticsLoadFromStream = 8
	pdfDocumentPageCount     = 7
	pdfDocumentGetPage       = 6
	pdfPageSize              = 10
	pdfPageRenderWithOptions = 7
	pdfRenderSetDestWidth    = 9
	pdfRenderSetDestHeight   = 11
	pdfRenderSetEncoderID    = 17
	bitmapEncoderPngID       = 8

	storageFileGetFromPath  = 6
	storageFileOpenAsync    = 8
	outputStreamFlush       = 7
	fileAccessModeRead      = 0
	fileAccessModeReadWrite = 1
)

// pdfDocumentFile opens src through the storage API and hands
// Windows.Data.Pdf the file. The caller releases the document.
func pdfDocumentFile(src string) (uintptr, error) {
	file, err := storageFile(src)
	if err != nil {
		return 0, err
	}
	defer wiRelease(file)
	factory, err := wiClassFactory("Windows.Data.Pdf.PdfDocument", wiIIDPdfDocumentStatics)
	if err != nil {
		return 0, err
	}
	defer wiRelease(factory)
	var op uintptr
	hr := wiInvoke(factory, pdfStaticsLoadFromFile, file, uintptr(unsafe.Pointer(&op)))
	if wiFailed(hr) {
		return 0, wiErr("PdfDocument.LoadFromFileAsync("+src+")", hr)
	}
	doc, err := wiAwaitResult(op, "PdfDocument.LoadFromFileAsync")
	if err != nil {
		return 0, fmt.Errorf("windows: cannot read PDF %s: %w", src, err)
	}
	return doc, nil
}

// storageFile opens path as a Windows.Storage.StorageFile. The caller
// releases it.
func storageFile(path string) (uintptr, error) {
	factory, err := wiClassFactory("Windows.Storage.StorageFile", wiIIDStorageFileStatics)
	if err != nil {
		return 0, err
	}
	defer wiRelease(factory)
	h, err := wiNewHString(path)
	if err != nil {
		return 0, err
	}
	var op uintptr
	hr := wiInvoke(factory, storageFileGetFromPath, h, uintptr(unsafe.Pointer(&op)))
	wiDeleteHString(h)
	if wiFailed(hr) {
		return 0, wiErr("StorageFile.GetFileFromPathAsync("+path+")", hr)
	}
	obj, err := wiAwaitResult(op, "StorageFile.GetFileFromPathAsync")
	if err != nil {
		return 0, err
	}
	defer wiRelease(obj)
	var file uintptr
	hr = wiQueryInterface(obj, &wiIIDStorageFile, &file)
	if wiFailed(hr) || file == 0 {
		return 0, wiErr("StorageFile as IStorageFile", hr)
	}
	return file, nil
}

// openFileStream opens path through the storage API as an
// IRandomAccessStream for WinRT's own writers. The caller releases it.
func openFileStream(path string, mode uintptr) (uintptr, error) {
	file, err := storageFile(path)
	if err != nil {
		return 0, err
	}
	defer wiRelease(file)
	var op uintptr
	hr := wiInvoke(file, storageFileOpenAsync, mode, uintptr(unsafe.Pointer(&op)))
	if wiFailed(hr) {
		return 0, wiErr("StorageFile.OpenAsync("+path+")", hr)
	}
	return wiAwaitResult(op, "StorageFile.OpenAsync")
}

// PDFPageCount reports how many pages the PDF at src has.
func PDFPageCount(src string) (n int, err error) {
	err = wiApartment(func() error {
		doc, e := pdfDocumentFile(src)
		if e != nil {
			return e
		}
		defer wiRelease(doc)
		var count uint32
		hr := wiInvoke(doc, pdfDocumentPageCount, uintptr(unsafe.Pointer(&count)))
		if wiFailed(hr) {
			return wiErr("PdfDocument.PageCount", hr)
		}
		n = int(count)
		return nil
	})
	return n, err
}

// RenderPDFPage renders page (1-based) of the PDF at src to an image file
// at dst in format, at twice the page's size in points — the Mac build's
// 144 dpi render, on a white background. The write is atomic.
func RenderPDFPage(src string, page int, dst string, format ImageFormat) error {
	return wiApartment(func() error {
		doc, err := pdfDocumentFile(src)
		if err != nil {
			return err
		}
		defer wiRelease(doc)
		var count uint32
		hr := wiInvoke(doc, pdfDocumentPageCount, uintptr(unsafe.Pointer(&count)))
		if wiFailed(hr) {
			return wiErr("PdfDocument.PageCount", hr)
		}
		if page < 1 || page > int(count) {
			return fmt.Errorf("windows: page %d out of range", page)
		}
		var pg uintptr
		hr = wiInvoke(doc, pdfDocumentGetPage, uintptr(page-1), uintptr(unsafe.Pointer(&pg)))
		if wiFailed(hr) || pg == 0 {
			return fmt.Errorf("windows: page %d out of range", page)
		}
		defer wiRelease(pg)
		width, height, err := pdfPagePixelSize(pg)
		if err != nil {
			return err
		}
		options, err := pdfRenderOptions(width, height)
		if err != nil {
			return err
		}
		defer wiRelease(options)
		tmp, err := os.CreateTemp("", "rakko-pdfpage-*.png")
		if err != nil {
			return err
		}
		pngPath := tmp.Name()
		tmp.Close()
		defer os.Remove(pngPath)
		out, err := openFileStream(pngPath, fileAccessModeReadWrite)
		if err != nil {
			return err
		}
		var op uintptr
		hr = wiInvoke(pg, pdfPageRenderWithOptions, out, options, uintptr(unsafe.Pointer(&op)))
		if wiFailed(hr) {
			wiRelease(out)
			return wiErr("PdfPage.RenderToStreamAsync", hr)
		}
		if err := wiAwait(op, "PdfPage.RenderToStreamAsync"); err != nil {
			wiRelease(out)
			return err
		}
		// Flush the buffered stream before the file is read back.
		var sink uintptr
		if hr := wiQueryInterface(out, &wiIIDOutputStream, &sink); !wiFailed(hr) && sink != 0 {
			var flushOp uintptr
			if hr := wiInvoke(sink, outputStreamFlush, uintptr(unsafe.Pointer(&flushOp))); !wiFailed(hr) {
				wiAwait(flushOp, "IOutputStream.FlushAsync")
				wiRelease(flushOp)
			}
			wiRelease(sink)
		}
		wiRelease(out)
		data, err := os.ReadFile(pngPath)
		if err != nil {
			return err
		}
		img, err := pngDecode(data)
		if err != nil {
			return fmt.Errorf("windows: page render produced no picture: %w", err)
		}
		return writeImageAtomic(dst, img, format, convertQuality)
	})
}

// pdfPagePixelSize is the page's size in points times two: PdfPage.Size is
// in DIPs (96 per inch), and a point is 72 DIPs per inch, so ×1.5 turns it
// into the 144 dpi size the Mac build renders.
func pdfPagePixelSize(page uintptr) (int, int, error) {
	// Windows.Foundation.Size is two floats.
	var size [2]float32
	hr := wiInvoke(page, pdfPageSize, uintptr(unsafe.Pointer(&size)))
	if wiFailed(hr) {
		return 0, 0, wiErr("PdfPage.Size", hr)
	}
	w := int(math.Round(float64(size[0]) * 1.5))
	h := int(math.Round(float64(size[1]) * 1.5))
	if w <= 0 || h <= 0 {
		return 0, 0, errors.New("windows: bad page box")
	}
	return w, h, nil
}

// pdfRenderOptions asks for a PNG of exactly width×height on white.
func pdfRenderOptions(width, height int) (uintptr, error) {
	opts, err := wiActivate("Windows.Data.Pdf.PdfPageRenderOptions")
	if err != nil {
		return 0, err
	}
	wiInvoke(opts, pdfRenderSetDestWidth, uintptr(width))
	wiInvoke(opts, pdfRenderSetDestHeight, uintptr(height))
	encoder, err := pngEncoderID()
	if err != nil {
		wiRelease(opts)
		return 0, err
	}
	wiInvoke(opts, pdfRenderSetEncoderID, uintptr(unsafe.Pointer(&encoder)))
	return opts, nil
}

// pngEncoderID asks the imaging stack for the PNG encoder's GUID.
func pngEncoderID() (wiGUID, error) {
	factory, err := wiClassFactory("Windows.Graphics.Imaging.BitmapEncoder", wiIIDBitmapEncoderStatics)
	if err != nil {
		return wiGUID{}, err
	}
	defer wiRelease(factory)
	var g wiGUID
	hr := wiInvoke(factory, bitmapEncoderPngID, uintptr(unsafe.Pointer(&g)))
	if wiFailed(hr) {
		return wiGUID{}, wiErr("BitmapEncoder.PngEncoderId", hr)
	}
	return g, nil
}

// pngDecode decodes the renderer's PNG bytes.
func pngDecode(data []byte) (*image.RGBA, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return rgbaOf(src), nil
}
