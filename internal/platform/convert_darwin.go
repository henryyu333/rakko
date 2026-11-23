//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Image conversion, multi-page PDF export and Vision text recognition,
// driven through ImageIO/CoreGraphics/Vision with purego. Unlike the rest
// of the package these entry points are safe on any goroutine: they touch
// no AppKit state and every call runs inside its own autorelease pool.

// CGAffineTransform mirrors CoreGraphics' 3×2 affine matrix.
type CGAffineTransform struct {
	A, B, C, D, Tx, Ty float64
}

var (
	libImageIO, libVision uintptr

	cfDataCreate          func(alloc uintptr, bytes unsafe.Pointer, length int) uintptr
	cfDictGetValueC       func(dict, key id) id
	imgSrcCreateWithURL   func(url uintptr, opts uintptr) uintptr
	imgSrcCopyPropsAt     func(src uintptr, idx int, opts uintptr) uintptr
	imgSrcCreateImageAt   func(src uintptr, idx int, opts uintptr) uintptr
	imgSrcCreateThumbAt   func(src uintptr, idx int, opts uintptr) uintptr
	imgDstCreateWithURL   func(url uintptr, uti id, count int, opts uintptr) uintptr
	imgDstAddImage        func(dst uintptr, image uintptr, props uintptr)
	imgDstFinalize        func(dst uintptr) bool
	cgImgGetWidth         func(image uintptr) int
	cgImgGetHeight        func(image uintptr) int
	cgColorSpaceDeviceRGB func() uintptr
	cgBitmapCreate        func(data unsafe.Pointer, w, h, bpc, bpr int, space uintptr, info uint32) uintptr
	cgBitmapCreateImage   func(ctx uintptr) uintptr
	cgPDFCtxCreateWithURL func(url uintptr, mediaBox *NSRect, aux uintptr) uintptr
	cgPDFCtxClose         func(ctx uintptr)
	cgCtxBeginPDFPage     func(ctx uintptr, info uintptr)
	cgCtxEndPDFPage       func(ctx uintptr)
	cgCtxConcatCTM        func(ctx uintptr, t CGAffineTransform)
	cgCtxDrawImage        func(ctx uintptr, rect NSRect, image uintptr)
	cgCtxDrawPDFPage      func(ctx uintptr, page uintptr)
	cgCtxSetFill          func(ctx uintptr, r, g, b, a float64)
	cgCtxFillRect         func(ctx uintptr, r NSRect)
	cgCtxSetTextPos       func(ctx uintptr, x, y float64)

	// CGPDFDocument reading: page counting and page boxes for rendering.
	pdfDocCreateWithURL func(url uintptr) uintptr
	pdfDocGetNumPages   func(doc uintptr) int
	pdfDocGetPage       func(doc uintptr, page int) uintptr
	pdfPageGetBoxRect   func(page uintptr, box int32) NSRect
	pdfPageGetRotation  func(page uintptr) int
	pdfPageDrawingXform func(page uintptr, box int32, rect NSRect, rotate int32, preserveAspect bool) CGAffineTransform
	cgCtxClipToRect     func(ctx uintptr, r NSRect)

	// objc_msgSend variants for signatures that move values through the
	// FP registers, which SyscallN cannot: numberWithDouble: takes a
	// float64 argument, doubleValue returns one.
	msgDouble    func(obj id, sel objc.SEL, v float64) id
	msgRetDouble func(obj id, sel objc.SEL) float64

	// Loaded constant values (bridged CFString dictionary keys).
	kImgPropOrientation id
	kImgPropPixelWidth  id
	kImgPropPixelHeight id
	kImgPropDPIWidth    id
	kImgPropDPIHeight   id
	kThumbWithTransform id
	kThumbCreateAlways  id
	kThumbMaxPixelSize  id
	kDstLossyQuality    id
	kPDFCtxMediaBox     id

	// Destination UTIs, held permanently (alloc'd, never autoreleased).
	utiJPEG, utiPNG, utiHEIC id

	convOnce sync.Once
	convErr  error
)

// loadConvert resolves ImageIO, the extra CoreFoundation/CoreGraphics
// entry points and Vision once. cfRelease is shared with drag_guard.go /
// overlay_hit.go; re-registering the var is idempotent.
func loadConvert() error {
	convOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				convErr = fmt.Errorf("mac: convert setup failed: %v", r)
			}
		}()
		if err := Init(); err != nil {
			convErr = err
			return
		}
		dlopen := func(path string) uintptr {
			h, e := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW)
			if e != nil {
				convErr = fmt.Errorf("mac: cannot load %s: %w", path, e)
			}
			return h
		}
		libCF := dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		libImageIO = dlopen("/System/Library/Frameworks/ImageIO.framework/ImageIO")
		libVision = dlopen("/System/Library/Frameworks/Vision.framework/Vision")
		// Quartz.framework provides PDFKit (PDFDocument/PDFPage) for text
		// extraction; it must be loaded before class("PDFDocument") resolves.
		dlopen("/System/Library/Frameworks/Quartz.framework/Quartz")
		if convErr != nil {
			return
		}
		purego.RegisterLibFunc(&cfRelease, libCF, "CFRelease")
		purego.RegisterLibFunc(&cfDataCreate, libCF, "CFDataCreate")
		purego.RegisterLibFunc(&cfDictGetValueC, libCF, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&imgSrcCreateWithURL, libImageIO, "CGImageSourceCreateWithURL")
		purego.RegisterLibFunc(&imgSrcCopyPropsAt, libImageIO, "CGImageSourceCopyPropertiesAtIndex")
		purego.RegisterLibFunc(&imgSrcCreateImageAt, libImageIO, "CGImageSourceCreateImageAtIndex")
		purego.RegisterLibFunc(&imgSrcCreateThumbAt, libImageIO, "CGImageSourceCreateThumbnailAtIndex")
		purego.RegisterLibFunc(&imgDstCreateWithURL, libImageIO, "CGImageDestinationCreateWithURL")
		purego.RegisterLibFunc(&imgDstAddImage, libImageIO, "CGImageDestinationAddImage")
		purego.RegisterLibFunc(&imgDstFinalize, libImageIO, "CGImageDestinationFinalize")
		purego.RegisterLibFunc(&cgImgGetWidth, libCG, "CGImageGetWidth")
		purego.RegisterLibFunc(&cgImgGetHeight, libCG, "CGImageGetHeight")
		purego.RegisterLibFunc(&cgColorSpaceDeviceRGB, libCG, "CGColorSpaceCreateDeviceRGB")
		purego.RegisterLibFunc(&cgBitmapCreate, libCG, "CGBitmapContextCreate")
		purego.RegisterLibFunc(&cgBitmapCreateImage, libCG, "CGBitmapContextCreateImage")
		purego.RegisterLibFunc(&cgPDFCtxCreateWithURL, libCG, "CGPDFContextCreateWithURL")
		purego.RegisterLibFunc(&cgCtxBeginPDFPage, libCG, "CGPDFContextBeginPage")
		purego.RegisterLibFunc(&cgCtxEndPDFPage, libCG, "CGPDFContextEndPage")
		purego.RegisterLibFunc(&cgCtxConcatCTM, libCG, "CGContextConcatCTM")
		purego.RegisterLibFunc(&cgCtxDrawImage, libCG, "CGContextDrawImage")
		purego.RegisterLibFunc(&cgCtxDrawPDFPage, libCG, "CGContextDrawPDFPage")
		purego.RegisterLibFunc(&cgCtxSetFill, libCG, "CGContextSetRGBFillColor")
		purego.RegisterLibFunc(&cgCtxFillRect, libCG, "CGContextFillRect")
		purego.RegisterLibFunc(&cgCtxSetTextPos, libCG, "CGContextSetTextPosition")
		purego.RegisterLibFunc(&pdfDocCreateWithURL, libCG, "CGPDFDocumentCreateWithURL")
		purego.RegisterLibFunc(&pdfDocGetNumPages, libCG, "CGPDFDocumentGetNumberOfPages")
		purego.RegisterLibFunc(&pdfDocGetPage, libCG, "CGPDFDocumentGetPage")
		purego.RegisterLibFunc(&pdfPageGetBoxRect, libCG, "CGPDFPageGetBoxRect")
		purego.RegisterLibFunc(&pdfPageGetRotation, libCG, "CGPDFPageGetRotationAngle")
		purego.RegisterLibFunc(&pdfPageDrawingXform, libCG, "CGPDFPageGetDrawingTransform")
		purego.RegisterLibFunc(&cgCtxClipToRect, libCG, "CGContextClipToRect")
		purego.RegisterFunc(&msgDouble, msgSendAddr)
		purego.RegisterFunc(&msgRetDouble, msgSendAddr)
		purego.RegisterLibFunc(&cgPDFCtxClose, libCG, "CGPDFContextClose")
		var copySymbol func(dst *id, src uintptr, size uintptr) uintptr
		purego.RegisterLibFunc(&copySymbol, purego.RTLD_DEFAULT, "memcpy")
		// ImageIO owns most keys; kCGPDFContextMediaBox is a CoreGraphics
		// export, so fall back to libCG for it.
		for name, dst := range map[string]*id{
			"kCGImagePropertyOrientation":                  &kImgPropOrientation,
			"kCGImagePropertyPixelWidth":                   &kImgPropPixelWidth,
			"kCGImagePropertyPixelHeight":                  &kImgPropPixelHeight,
			"kCGImagePropertyDPIWidth":                     &kImgPropDPIWidth,
			"kCGImagePropertyDPIHeight":                    &kImgPropDPIHeight,
			"kCGImageSourceCreateThumbnailWithTransform":   &kThumbWithTransform,
			"kCGImageSourceCreateThumbnailFromImageAlways": &kThumbCreateAlways,
			"kCGImageSourceThumbnailMaxPixelSize":          &kThumbMaxPixelSize,
			"kCGImageDestinationLossyCompressionQuality":   &kDstLossyQuality,
			"kCGPDFContextMediaBox":                        &kPDFCtxMediaBox,
		} {
			addr, err := purego.Dlsym(libImageIO, name)
			if err != nil {
				addr, err = purego.Dlsym(libCG, name)
			}
			if err != nil {
				convErr = fmt.Errorf("mac: %s: %w", name, err)
				return
			}
			copySymbol(dst, addr, unsafe.Sizeof(*dst))
		}
		utiJPEG = permString("public.jpeg")
		utiPNG = permString("public.png")
		utiHEIC = permString("public.heic")
	})
	return convErr
}

// permString returns a +1 NSString that is never released: safe to stash
// in a package var without an enclosing autorelease pool.
func permString(s string) id {
	return send(send(class("NSString"), "alloc"), "initWithBytes:length:encoding:",
		uintptr(unsafe.Pointer(unsafe.StringData(s))), uintptr(len(s)), nsUTF8StringEncoding)
}

// cfURL returns an autoreleased NSURL for a filesystem path.
func cfURL(path string) id {
	return send(class("NSURL"), "fileURLWithPath:", uintptr(nsString(path)))
}

// nsBool returns an autoreleased NSNumber wrapping a bool.
func nsBool(v bool) id {
	b := uintptr(0)
	if v {
		b = 1
	}
	return send(class("NSNumber"), "numberWithBool:", b)
}

// nsFloat returns an autoreleased NSNumber wrapping v.
func nsFloat(v float64) id {
	return msgDouble(class("NSNumber"), sel("numberWithDouble:"), v)
}

// newDict builds an autoreleased NSDictionary from alternating key/value
// pairs (k0, v0, k1, v1, …).
func newDict(pairs ...id) id {
	n := len(pairs) / 2
	objs := make([]id, n)
	keys := make([]id, n)
	for i := range n {
		keys[i], objs[i] = pairs[2*i], pairs[2*i+1]
	}
	return send(class("NSDictionary"), "dictionaryWithObjects:forKeys:count:",
		uintptr(unsafe.Pointer(&objs[0])), uintptr(unsafe.Pointer(&keys[0])), uintptr(n))
}

// dictGet returns the value for key in dict (0 when absent).
func dictGet(dict uintptr, key id) id {
	if dict == 0 {
		return 0
	}
	return cfDictGetValueC(id(dict), key)
}

// numInt reads a CFNumber/NSNumber as int.
func numInt(v id) int {
	if v == 0 {
		return 0
	}
	return sendInt(v, "intValue")
}

// numDouble reads a CFNumber/NSNumber as float64.
func numDouble(v id) float64 {
	if v == 0 {
		return 0
	}
	return msgRetDouble(v, sel("doubleValue"))
}

// imageSource opens path as an image source; caller CFReleases it.
func imageSource(path string) (uintptr, error) {
	src := imgSrcCreateWithURL(uintptr(cfURL(path)), 0)
	if src == 0 {
		return 0, fmt.Errorf("mac: cannot read image %s", path)
	}
	return src, nil
}

// sourceProps returns the property dictionary of the source's first
// image (Copy rule; caller CFReleases). May return 0.
func sourceProps(src uintptr) uintptr {
	return imgSrcCopyPropsAt(src, 0, 0)
}

// exifOrientation returns the stored EXIF orientation (1 when absent).
func exifOrientation(props uintptr) int {
	o := numInt(dictGet(props, kImgPropOrientation))
	if o < 1 || o > 8 {
		return 1
	}
	return o
}

// uprightImage returns a CGImage whose pixels are the visually upright
// version of the source's first image: any EXIF orientation is baked into
// the bitmap, so the result carries no orientation flag and embedders that
// ignore EXIF (Word/docx) still show it correctly. Caller CFReleases.
func uprightImage(src uintptr) uintptr {
	// kCGImageSourceCreateThumbnailWithTransform applies the orientation
	// during decode; with no kCGImageSourceThumbnailMaxPixelSize set the
	// result is full-size.
	opts := newDict(kThumbWithTransform, nsBool(true), kThumbCreateAlways, nsBool(true))
	if img := imgSrcCreateThumbAt(src, 0, uintptr(opts)); img != 0 {
		return img
	}
	// Fallback for decoders that refuse thumbnail creation: raw decode
	// plus a manual upright draw. Same result, just slower.
	img := imgSrcCreateImageAt(src, 0, 0)
	if img == 0 {
		return 0
	}
	props := sourceProps(src)
	o := exifOrientation(props)
	if props != 0 {
		cfRelease(props)
	}
	if o == 1 {
		return img
	}
	baked := drawUpright(img, o)
	cfRelease(img)
	return baked
}

// recognizeMaxSide bounds text-recognition decode: Vision gets the upright
// image scaled to this longest side at most. 6144 covers 5K and 6K shots
// (5120 and 6016 wide) at native resolution — text stays sharp — while an
// extreme bitmap decodes to ≤ 6144² px ≈ 151 MB RGBA instead of hundreds
// of megabytes of raw pixels nobody can interrupt once in Vision.
const recognizeMaxSide = 6144

// uprightImageCapped is uprightImage limited to maxSide on the long edge:
// thumbnail decode honors kCGImageSourceThumbnailMaxPixelSize, and the
// fallback path draws the upright bitmap into a scaled context instead of
// the raw full-size one.
func uprightImageCapped(src uintptr, maxSide int) uintptr {
	opts := newDict(kThumbWithTransform, nsBool(true), kThumbCreateAlways, nsBool(true),
		kThumbMaxPixelSize, nsFloat(float64(maxSide)))
	if img := imgSrcCreateThumbAt(src, 0, uintptr(opts)); img != 0 {
		return img
	}
	img := imgSrcCreateImageAt(src, 0, 0)
	if img == 0 {
		return 0
	}
	w, h := cgImgGetWidth(img), cgImgGetHeight(img)
	scale := 1.0
	if mx := max(w, h); maxSide > 0 && mx > maxSide {
		scale = float64(maxSide) / float64(mx)
	}
	props := sourceProps(src)
	o := exifOrientation(props)
	if props != 0 {
		cfRelease(props)
	}
	baked := drawUprightScaled(img, o, scale)
	cfRelease(img)
	return baked
}

// drawUpright renders img through the orientation transform into a new
// premultiplied RGBA bitmap and returns it as a CGImage.
func drawUpright(img uintptr, o int) uintptr {
	return drawUprightScaled(img, o, 1)
}

// drawUprightScaled is drawUpright with an extra uniform scale: the output
// is the upright image at scale× its displayed size.
func drawUprightScaled(img uintptr, o int, scale float64) uintptr {
	w, h := cgImgGetWidth(img), cgImgGetHeight(img)
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dw = max(1, int(float64(dw)*scale+0.5))
	dh = max(1, int(float64(dh)*scale+0.5))
	space := cgColorSpaceDeviceRGB()
	if space == 0 {
		return 0
	}
	defer cfRelease(space)
	// kCGImageAlphaPremultipliedLast (1) | kCGBitmapByteOrder32Big (4<<12).
	ctx := cgBitmapCreate(nil, dw, dh, 8, 0, space, uint32(1|4<<12))
	if ctx == 0 {
		return 0
	}
	defer cfRelease(ctx)
	ctm := orientationCTM(o, w, h)
	ctm.A *= scale
	ctm.B *= scale
	ctm.C *= scale
	ctm.D *= scale
	ctm.Tx *= scale
	ctm.Ty *= scale
	cgCtxConcatCTM(ctx, ctm)
	cgCtxDrawImage(ctx, NSRect{Size: NSSize{Width: float64(w), Height: float64(h)}}, img)
	return cgBitmapCreateImage(ctx)
}

// orientationCTM maps the stored image's coordinate space — Quartz
// convention, (0,0) bottom-left, (w,h) top-right — into the upright
// output space for EXIF orientation o, per the EXIF corner mapping.
func orientationCTM(o, w, h int) CGAffineTransform {
	dw, dh := float64(w), float64(h)
	if o >= 5 {
		dw, dh = dh, dw
	}
	switch o {
	case 2: // horizontal mirror
		return CGAffineTransform{A: -1, D: 1, Tx: dw}
	case 3: // 180°
		return CGAffineTransform{A: -1, D: -1, Tx: dw, Ty: dh}
	case 4: // vertical mirror
		return CGAffineTransform{A: 1, D: -1, Ty: dh}
	case 5: // transpose over the main diagonal
		return CGAffineTransform{B: -1, C: -1, Tx: dw, Ty: dh}
	case 6: // rotated 90° CW when displayed
		return CGAffineTransform{B: -1, C: 1, Ty: dh}
	case 7: // transpose over the anti-diagonal
		return CGAffineTransform{B: 1, C: 1}
	case 8: // rotated 90° CCW when displayed
		return CGAffineTransform{B: 1, C: -1, Tx: dw}
	default: // 1 or unknown: already upright
		return CGAffineTransform{A: 1, D: 1}
	}
}

// atomicTemp creates a temp file in dir (same volume ⇒ atomic rename).
func atomicTemp(dir string) (path string, err error) {
	f, err := os.CreateTemp(dir, ".shelf-convert-*")
	if err != nil {
		return "", err
	}
	p := f.Name()
	err = f.Close()
	return p, err
}

// formatUTI is the ImageIO type identifier format encodes as.
func formatUTI(format ImageFormat) (id, error) {
	switch format {
	case FormatJPEG:
		return utiJPEG, nil
	case FormatPNG:
		return utiPNG, nil
	case FormatHEIC:
		return utiHEIC, nil
	}
	return 0, fmt.Errorf("mac: unknown image format %d", int(format))
}

// ConvertImage writes src — any format ImageIO reads — to dst in format.
// The output's pixels are the visually upright image: any EXIF orientation
// is baked into the bitmap and the result carries no orientation flag.
// The write is atomic (temp file in dst's directory + rename) and src is
// never modified. Safe to call on any goroutine.
func ConvertImage(src, dst string, format ImageFormat) (err error) {
	if err := loadConvert(); err != nil {
		return err
	}
	uti, err := formatUTI(format)
	if err != nil {
		return err
	}

	withPool(func() {
		isrc, e := imageSource(src)
		if e != nil {
			err = e
			return
		}
		defer cfRelease(isrc)
		img := uprightImage(isrc)
		if img == 0 {
			err = fmt.Errorf("mac: cannot decode image %s", src)
			return
		}
		defer cfRelease(img)
		err = encodeCGImage(img, uti, format, dst)
	})
	return err
}

// encodeCGImage writes img to dst as format via a temp file + rename.
// Call inside withPool; img stays owned by the caller.
func encodeCGImage(img uintptr, uti id, format ImageFormat, dst string) (err error) {
	tmpPath, err := atomicTemp(filepath.Dir(dst))
	if err != nil {
		return fmt.Errorf("mac: %w", err)
	}
	defer func() {
		if err != nil {
			os.Remove(tmpPath)
		}
	}()
	dstRef := imgDstCreateWithURL(uintptr(cfURL(tmpPath)), uti, 1, 0)
	if dstRef == 0 {
		return fmt.Errorf("mac: cannot create encoder for %s", dst)
	}
	defer cfRelease(dstRef)
	var props id
	if format == FormatJPEG || format == FormatHEIC {
		props = newDict(kDstLossyQuality, nsFloat(0.85))
	}
	imgDstAddImage(dstRef, img, uintptr(props))
	if !imgDstFinalize(dstRef) {
		return fmt.Errorf("mac: cannot write %s", dst)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mac: %w", err)
	}
	return nil
}

// imagePage decodes the upright CGImage for the source's first frame and
// computes its PDF page size in points (pixels × 72 / DPI, default 72).
// Caller CFReleases the returned image.
func imagePage(src uintptr) (img uintptr, pw, ph float64, err error) {
	props := sourceProps(src)
	if props != 0 {
		defer cfRelease(props)
	}
	img = uprightImage(src)
	if img == 0 {
		return 0, 0, 0, errors.New("mac: cannot decode image")
	}
	dpiX := numDouble(dictGet(props, kImgPropDPIWidth))
	dpiY := numDouble(dictGet(props, kImgPropDPIHeight))
	if dpiX <= 0 {
		dpiX = 72
	}
	if dpiY <= 0 {
		dpiY = 72
	}
	return img, float64(cgImgGetWidth(img)) * 72 / dpiX,
		float64(cgImgGetHeight(img)) * 72 / dpiY, nil
}

// ImagesToPDF writes one page per source image, in order, to dst. Each
// page's media box equals the upright image's size in points and the
// image fills it. Orientation handling matches ConvertImage. Atomic
// write; safe on any goroutine.
func ImagesToPDF(srcs []string, dst string) (err error) {
	if len(srcs) == 0 {
		return errors.New("mac: no images")
	}
	if err := loadConvert(); err != nil {
		return err
	}
	tmpPath, err := atomicTemp(filepath.Dir(dst))
	if err != nil {
		return fmt.Errorf("mac: %w", err)
	}
	defer func() {
		if err != nil {
			os.Remove(tmpPath)
		}
	}()

	withPool(func() {
		ctx := cgPDFCtxCreateWithURL(uintptr(cfURL(tmpPath)), nil, 0)
		if ctx == 0 {
			err = fmt.Errorf("mac: cannot create PDF %s", dst)
			return
		}
		// CGPDFContextClose writes the xref/trailer; it must run BEFORE
		// the release, so the pair is a single ordered defer — two
		// defers would release first (LIFO).
		defer func() {
			cgPDFCtxClose(ctx)
			cfRelease(ctx)
		}()
		for _, p := range srcs {
			isrc, e := imageSource(p)
			if e != nil {
				err = e
				return
			}
			img, pw, ph, e := imagePage(isrc)
			cfRelease(isrc)
			if e != nil {
				err = fmt.Errorf("mac: %s: %w", p, e)
				return
			}
			box := NSRect{Size: NSSize{Width: pw, Height: ph}}
			// The per-page media box override takes a CFData holding the
			// CGRect by value.
			boxData := cfDataCreate(0, unsafe.Pointer(&box), int(unsafe.Sizeof(box)))
			if boxData == 0 {
				cfRelease(img)
				err = errors.New("mac: cannot build PDF page box")
				return
			}
			info := newDict(kPDFCtxMediaBox, id(boxData))
			cgCtxBeginPDFPage(ctx, uintptr(info))
			cgCtxDrawImage(ctx, box, img)
			cgCtxEndPDFPage(ctx)
			cfRelease(img)
			cfRelease(boxData)
		}
	})
	if err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mac: %w", err)
	}
	return nil
}

// RecognizeText runs Vision text recognition (accurate level, languages
// zh-Hans/zh-Hant/en-US, language correction on) on the upright image and
// returns the recognized lines in reading order — top to bottom, then
// left to right within a row. No recognized text yields (nil, nil).
// Safe to call on any goroutine.
func RecognizeText(src string) (lines []string, err error) {
	if err := loadConvert(); err != nil {
		return nil, err
	}
	withPool(func() {
		isrc, e := imageSource(src)
		if e != nil {
			err = e
			return
		}
		defer cfRelease(isrc)
		img := uprightImageCapped(isrc, recognizeMaxSide)
		if img == 0 {
			err = fmt.Errorf("mac: cannot decode image %s", src)
			return
		}
		defer cfRelease(img)

		emptyDict := send(class("NSDictionary"), "dictionary")
		handler := send(send(class("VNImageRequestHandler"), "alloc"),
			"initWithCGImage:options:", img, uintptr(emptyDict))
		if handler == 0 {
			err = errors.New("mac: cannot create Vision request handler")
			return
		}
		// alloc/init objects are +1: release them before the pool pops.
		defer send(handler, "release")
		req := send(send(class("VNRecognizeTextRequest"), "alloc"), "init")
		if req == 0 {
			err = errors.New("mac: cannot create Vision text request")
			return
		}
		defer send(req, "release")
		// VNRequestTextRecognitionLevelAccurate == 0.
		send(req, "setRecognitionLevel:", 0)
		langs := [3]id{nsString("zh-Hans"), nsString("zh-Hant"), nsString("en-US")}
		langArr := send(class("NSArray"), "arrayWithObjects:count:",
			uintptr(unsafe.Pointer(&langs[0])), uintptr(len(langs)))
		send(req, "setRecognitionLanguages:", uintptr(langArr))
		send(req, "setUsesLanguageCorrection:", 1)

		reqs := [1]id{req}
		reqArr := send(class("NSArray"), "arrayWithObjects:count:",
			uintptr(unsafe.Pointer(&reqs[0])), 1)
		var nsErr id
		ok := send(handler, "performRequests:error:", uintptr(reqArr),
			uintptr(unsafe.Pointer(&nsErr)))
		if ok == 0 {
			err = fmt.Errorf("mac: text recognition failed: %s",
				goString(send(nsErr, "localizedDescription")))
			return
		}
		results := send(req, "results")
		n := sendInt(results, "count")
		type obs struct {
			minX, midY, h float64
			text          string
		}
		var list []obs
		for i := range n {
			o := send(results, "objectAtIndex:", uintptr(i))
			cands := send(o, "topCandidates:", 1)
			if cands == 0 || sendInt(cands, "count") == 0 {
				continue
			}
			box := msgRect(o, sel("boundingBox"))
			list = append(list, obs{
				minX: box.Origin.X,
				midY: box.Origin.Y + box.Size.Height/2,
				h:    box.Size.Height,
				text: goString(send(send(cands, "objectAtIndex:", 0), "string")),
			})
		}
		// Reading order: observations carry normalized bounding boxes with
		// a bottom-left origin, so larger midY is the upper line. Boxes
		// whose centres differ by less than half a line height count as
		// the same row and order left to right.
		slices.SortFunc(list, func(a, b obs) int {
			dy := b.midY - a.midY
			if math.Abs(dy) > math.Min(a.h, b.h)/2 {
				if dy > 0 {
					return 1
				}
				return -1
			}
			switch {
			case a.minX < b.minX:
				return -1
			case a.minX > b.minX:
				return 1
			default:
				return 0
			}
		})
		for _, o := range list {
			if o.text != "" {
				lines = append(lines, o.text)
			}
		}
	})
	return lines, err
}

// ImageSize reports the upright pixel size of an image file: the
// displayed dimensions, so a 90°-rotated photo reports the swapped width
// and height. Safe on any goroutine.
func ImageSize(src string) (w, h int, err error) {
	if err := loadConvert(); err != nil {
		return 0, 0, err
	}
	withPool(func() {
		isrc, e := imageSource(src)
		if e != nil {
			err = e
			return
		}
		defer cfRelease(isrc)
		props := sourceProps(isrc)
		if props == 0 {
			err = fmt.Errorf("mac: cannot read properties of %s", src)
			return
		}
		defer cfRelease(props)
		w = numInt(dictGet(props, kImgPropPixelWidth))
		h = numInt(dictGet(props, kImgPropPixelHeight))
		if w == 0 || h == 0 {
			err = fmt.Errorf("mac: no pixel size for %s", src)
			return
		}
		if o := exifOrientation(props); o >= 5 {
			w, h = h, w
		}
	})
	return w, h, err
}

// scaledImage returns the source's first image upright and scaled so its
// longer side is at most maxPx; a smaller image keeps its size. Caller
// CFReleases it; 0 when the source cannot be decoded.
func scaledImage(src uintptr, maxPx int) uintptr {
	opts := newDict(kThumbWithTransform, nsBool(true), kThumbCreateAlways, nsBool(true),
		kThumbMaxPixelSize, nsFloat(float64(maxPx)))
	return imgSrcCreateThumbAt(src, 0, uintptr(opts))
}

// Thumbnail decodes src — any format ImageIO reads, HEIC and TIFF
// included — upright, with its longer side at most maxPx, as
// premultiplied RGBA. Safe on any goroutine.
func Thumbnail(src string, maxPx int) (thumb *image.RGBA, err error) {
	if maxPx < 1 {
		return nil, fmt.Errorf("mac: bad thumbnail size %d", maxPx)
	}
	if err := loadConvert(); err != nil {
		return nil, err
	}
	withPool(func() {
		isrc, e := imageSource(src)
		if e != nil {
			err = e
			return
		}
		defer cfRelease(isrc)
		img := scaledImage(isrc, maxPx)
		if img == 0 {
			err = fmt.Errorf("mac: cannot decode image %s", src)
			return
		}
		defer cfRelease(img)
		w, h := cgImgGetWidth(img), cgImgGetHeight(img)
		if w <= 0 || h <= 0 {
			err = fmt.Errorf("mac: empty image %s", src)
			return
		}
		space := cgColorSpaceDeviceRGB()
		if space == 0 {
			err = errors.New("mac: no RGB color space")
			return
		}
		defer cfRelease(space)
		out := image.NewRGBA(image.Rect(0, 0, w, h))
		// kCGImageAlphaPremultipliedLast (1) | kCGBitmapByteOrder32Big
		// (4<<12): byte order R, G, B, A — image.RGBA's layout. The
		// context draws into out.Pix and is released before returning.
		ctx := cgBitmapCreate(unsafe.Pointer(&out.Pix[0]), w, h, 8, out.Stride, space, uint32(1|4<<12))
		if ctx == 0 {
			err = fmt.Errorf("mac: cannot draw image %s", src)
			return
		}
		cgCtxDrawImage(ctx, NSRect{Size: NSSize{Width: float64(w), Height: float64(h)}}, img)
		cfRelease(ctx)
		runtime.KeepAlive(out.Pix)
		thumb = out
	})
	return thumb, err
}

// ResizeImage writes src to dst in format, upright and scaled so its
// longer side is at most maxPx; a smaller image is re-encoded at its own
// size, never enlarged. Atomic like ConvertImage; src is never modified.
// Safe on any goroutine.
func ResizeImage(src, dst string, format ImageFormat, maxPx int) (err error) {
	if maxPx < 1 {
		return fmt.Errorf("mac: bad size %d", maxPx)
	}
	if err := loadConvert(); err != nil {
		return err
	}
	uti, err := formatUTI(format)
	if err != nil {
		return err
	}
	withPool(func() {
		isrc, e := imageSource(src)
		if e != nil {
			err = e
			return
		}
		defer cfRelease(isrc)
		img := scaledImage(isrc, maxPx)
		if img == 0 {
			err = fmt.Errorf("mac: cannot decode image %s", src)
			return
		}
		defer cfRelease(img)
		err = encodeCGImage(img, uti, format, dst)
	})
	return err
}

// --- PDF reading and rendering -----------------------------------------

// pdfDocument opens the PDF at src via CGPDFDocument (thread-safe, no
// PDFKit). Caller CFReleases it.
func pdfDocument(src string) (uintptr, error) {
	doc := pdfDocCreateWithURL(uintptr(cfURL(src)))
	if doc == 0 {
		return 0, fmt.Errorf("mac: cannot read PDF %s", src)
	}
	return doc, nil
}

// PDFPageCount reports how many pages the PDF at src has.
// Safe on any goroutine.
func PDFPageCount(src string) (n int, err error) {
	if err := loadConvert(); err != nil {
		return 0, err
	}
	withPool(func() {
		doc, e := pdfDocument(src)
		if e != nil {
			err = e
			return
		}
		defer cfRelease(doc)
		n = pdfDocGetNumPages(doc)
	})
	return n, err
}

// NormalizeText applies Unicode compatibility composition (NFKC) to s —
// the same mapping NSString's precomposedStringWithCompatibilityMapping
// PDFText returns the text of every page of the PDF at src (index =
// page-1), each page's string split into lines (trailing spaces trimmed,
// empty lines at page start/end dropped, inner empty lines kept as "").
// Text is normalized with NFKC so CJK compatibility / Kangxi radical code
// points become the ordinary characters. A page with no text layer yields
// nil for that page. Extraction goes through PDFKit (PDFDocument /
// PDFPage.string), which is safe off the main thread — unlike the rest of
// AppKit, PDFKit's data model needs no run loop. Safe on any goroutine.
func PDFText(src string) (pages [][]string, err error) {
	if err := loadConvert(); err != nil {
		return nil, err
	}
	withPool(func() {
		doc := send(send(class("PDFDocument"), "alloc"),
			"initWithURL:", uintptr(cfURL(src)))
		if doc == 0 {
			err = fmt.Errorf("mac: cannot read PDF %s", src)
			return
		}
		defer send(doc, "release")
		n := sendInt(doc, "pageCount")
		pages = make([][]string, n)
		for i := range n {
			page := send(doc, "pageAtIndex:", uintptr(i))
			if page == 0 {
				continue
			}
			raw := send(page, "string")
			if raw == 0 {
				continue
			}
			pages[i] = pdfLines(NormalizeText(goString(raw)))
		}
	})
	return pages, err
}

// RenderPDFPage renders page (1-based) of the PDF at src to an image file
// at dst in format, at scale 2 — the page's crop box in points (media box
// when unset) times 2 pixels (144 dpi), honouring the page's /Rotate — on
// a white background, drawn upright. The write is atomic like
// ConvertImage. Safe on any goroutine.
func RenderPDFPage(src string, page int, dst string, format ImageFormat) (err error) {
	if err := loadConvert(); err != nil {
		return err
	}
	var uti id
	switch format {
	case FormatJPEG:
		uti = utiJPEG
	case FormatPNG:
		uti = utiPNG
	case FormatHEIC:
		uti = utiHEIC
	default:
		return fmt.Errorf("mac: unknown image format %d", int(format))
	}
	withPool(func() {
		doc, e := pdfDocument(src)
		if e != nil {
			err = e
			return
		}
		defer cfRelease(doc)
		if page < 1 || page > pdfDocGetNumPages(doc) {
			err = fmt.Errorf("mac: page %d out of range", page)
			return
		}
		p := pdfDocGetPage(doc, page)
		if p == 0 {
			err = fmt.Errorf("mac: page %d out of range", page)
			return
		}
		// kCGPDFCropBox (1) falls back to the media box when unset; the
		// rendered size is the crop box after the page's /Rotate.
		crop := pdfPageGetBoxRect(p, 1)
		rot := pdfPageGetRotation(p)
		dw, dh := crop.Size.Width, crop.Size.Height
		if rot%180 != 0 {
			dw, dh = dh, dw
		}
		w := int(math.Ceil(dw * 2))
		h := int(math.Ceil(dh * 2))
		if w <= 0 || h <= 0 {
			err = fmt.Errorf("mac: bad page box for page %d", page)
			return
		}
		space := cgColorSpaceDeviceRGB()
		if space == 0 {
			err = errors.New("mac: no colorspace")
			return
		}
		defer cfRelease(space)
		ctx := cgBitmapCreate(nil, w, h, 8, 0, space, uint32(1|4<<12))
		if ctx == 0 {
			err = errors.New("mac: no bitmap context")
			return
		}
		defer cfRelease(ctx)
		cgCtxSetFill(ctx, 1, 1, 1, 1)
		cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: float64(w), Height: float64(h)}})
		// GetDrawingTransform fits the crop box into the target rect —
		// shrinking and centering but never upscaling — and applies the
		// page's /Rotate when rotate is 0. Passing the rotated crop size
		// in points yields the scale-1 upright mapping; the ×2 pixel
		// scale is concatenated in front of it.
		target := NSRect{Size: NSSize{Width: dw, Height: dh}}
		xform := pdfPageDrawingXform(p, 1, target, 0, false)
		cgCtxConcatCTM(ctx, CGAffineTransform{A: 2, D: 2})
		cgCtxConcatCTM(ctx, xform)
		// Clip to the crop box (in page space, post-transform) so content
		// bleeding outside it can't paint over the white margin.
		cgCtxClipToRect(ctx, crop)
		cgCtxDrawPDFPage(ctx, p)
		img := cgBitmapCreateImage(ctx)
		if img == 0 {
			err = errors.New("mac: render produced no image")
			return
		}
		defer cfRelease(img)
		err = encodeCGImage(img, uti, format, dst)
	})
	return err
}
