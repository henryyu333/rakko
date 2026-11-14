//go:build windows

package platform

// Image, PDF and text recognition. The skeleton keeps the conversion
// surface compiling; the WIC encoders and Windows.Data.Pdf reader arrive
// with the image batch. WINDOWS-NEXT(image)

import "image"

// ImageFormat selects the encoding a conversion writes.
type ImageFormat int

const (
	// FormatJPEG encodes JPEG.
	FormatJPEG ImageFormat = iota
	// FormatPNG encodes lossless PNG.
	FormatPNG
	// FormatHEIC encodes HEIC.
	FormatHEIC
)

// ConvertImage writes src to dst in format. WINDOWS-NEXT(image)
func ConvertImage(src, dst string, format ImageFormat) error { return errDomain("image") }

// FitImage writes src to dst at most limit bytes. WINDOWS-NEXT(image)
func FitImage(src, dst string, format ImageFormat, limit int64) (over bool, err error) {
	return false, errDomain("image")
}

// ImageAlpha reports whether src carries transparency. WINDOWS-NEXT(image)
func ImageAlpha(src string) (alpha bool, err error) { return false, errDomain("image") }

// ImageSize reports the upright pixel size of an image. WINDOWS-NEXT(image)
func ImageSize(src string) (w, h int, err error) { return 0, 0, errDomain("image") }

// ImagesToPDF writes one page per image. WINDOWS-NEXT(image)
func ImagesToPDF(srcs []string, dst string) error { return errDomain("image") }

// RenderPDFPage renders one PDF page to an image. WINDOWS-NEXT(image)
func RenderPDFPage(src string, page int, dst string, format ImageFormat) error {
	return errDomain("image")
}

// PDFPageCount reports how many pages the PDF has. WINDOWS-NEXT(image)
func PDFPageCount(src string) (n int, err error) { return 0, errDomain("image") }

// PDFText returns the text of every page. WINDOWS-NEXT(image)
func PDFText(src string) (pages [][]string, err error) { return nil, errDomain("image") }

// ResizeImage writes src scaled so its longer side is at most maxPx.
// WINDOWS-NEXT(image)
func ResizeImage(src, dst string, format ImageFormat, maxPx int) error {
	return errDomain("image")
}

// Thumbnail decodes src, upright, at most maxPx on its longer side.
// WINDOWS-NEXT(image)
func Thumbnail(src string, maxPx int) (thumb *image.RGBA, err error) {
	return nil, errDomain("image")
}

// RecognizeText runs system text recognition on the upright image.
// WINDOWS-NEXT(ocr)
func RecognizeText(src string) (lines []string, err error) { return nil, errDomain("ocr") }
