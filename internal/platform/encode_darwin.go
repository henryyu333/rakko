//go:build darwin

package platform

// Size-fitting image encode for 「压缩到」: re-encode at decreasing JPEG/HEIC
// quality (floor 0.4), then shrink the longer side ~0.85× at a time (floor
// 640 px) until the output fits the byte limit. PNG keeps lossless encoding
// (chosen by the caller for images that carry transparency), so it only ever
// shrinks. Every trial encodes into memory — only the final choice touches
// disk, atomically like ConvertImage. EXIF orientation is baked in the same
// way, so the output is upright and carries no orientation flag.

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	// Encode-to-memory and alpha detection, loaded by loadEncode.
	imgDstCreateWithData func(data uintptr, uti id, count int, opts uintptr) uintptr
	cfDataCreateMutable  func(alloc uintptr, capacity int) uintptr
	cfDataGetLength      func(data uintptr) int
	cfDataGetBytePtr     func(data uintptr) unsafe.Pointer
	cgImgGetAlphaInfo    func(image uintptr) uint32

	kImgPropHasAlpha id

	encOnce sync.Once
	encErr  error
)

// loadEncode resolves the extra entry points on top of loadConvert, which
// owns the shared decode functions and the lossy-quality key.
func loadEncode() error {
	if err := loadConvert(); err != nil {
		return err
	}
	encOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				encErr = fmt.Errorf("mac: encode setup failed: %v", r)
			}
		}()
		libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			encErr = fmt.Errorf("mac: cannot load CoreFoundation: %w", err)
			return
		}
		purego.RegisterLibFunc(&imgDstCreateWithData, libImageIO, "CGImageDestinationCreateWithData")
		purego.RegisterLibFunc(&cfDataCreateMutable, libCF, "CFDataCreateMutable")
		purego.RegisterLibFunc(&cfDataGetLength, libCF, "CFDataGetLength")
		purego.RegisterLibFunc(&cfDataGetBytePtr, libCF, "CFDataGetBytePtr")
		purego.RegisterLibFunc(&cgImgGetAlphaInfo, libCG, "CGImageGetAlphaInfo")
		addr, err := purego.Dlsym(libImageIO, "kCGImagePropertyHasAlpha")
		if err != nil {
			encErr = fmt.Errorf("mac: %s: %w", "kCGImagePropertyHasAlpha", err)
			return
		}
		var copySymbol func(dst *id, src uintptr, size uintptr) uintptr
		purego.RegisterLibFunc(&copySymbol, purego.RTLD_DEFAULT, "memcpy")
		copySymbol(&kImgPropHasAlpha, addr, unsafe.Sizeof(kImgPropHasAlpha))
	})
	return encErr
}

// ImageAlpha reports whether src's first image carries transparency: the
// kCGImagePropertyHasAlpha property when published, else the decoded image's
// alpha info (an opaque PNG advertises none). Safe on any goroutine.
func ImageAlpha(src string) (alpha bool, err error) {
	if err := loadEncode(); err != nil {
		return false, err
	}
	withPool(func() {
		isrc, e := imageSource(src)
		if e != nil {
			err = e
			return
		}
		defer cfRelease(isrc)
		if props := sourceProps(isrc); props != 0 {
			if v := dictGet(props, kImgPropHasAlpha); v != 0 {
				alpha = numInt(v) != 0
				cfRelease(props)
				return
			}
			cfRelease(props)
		}
		img := imgSrcCreateImageAt(isrc, 0, 0)
		if img == 0 {
			err = fmt.Errorf("mac: cannot decode image %s", src)
			return
		}
		defer cfRelease(img)
		// Opaque: kCGImageAlphaNone, NoneSkipLast, NoneSkipFirst (0, 5, 6);
		// every other info means the image has an alpha channel.
		switch a := cgImgGetAlphaInfo(img); a {
		case 0, 5, 6:
			alpha = false
		default:
			alpha = true
		}
	})
	return alpha, err
}

// encodeImageData encodes img in the format uti names at the given quality
// (ignored for PNG) and returns the bytes. Call inside withPool.
func encodeImageData(img uintptr, uti id, format ImageFormat, quality float64) ([]byte, error) {
	data := cfDataCreateMutable(0, 0)
	if data == 0 {
		return nil, fmt.Errorf("mac: cannot create data buffer")
	}
	defer cfRelease(data)
	dstRef := imgDstCreateWithData(data, uti, 1, 0)
	if dstRef == 0 {
		return nil, fmt.Errorf("mac: cannot create encoder")
	}
	defer cfRelease(dstRef)
	var props id
	if format == FormatJPEG || format == FormatHEIC {
		props = newDict(kDstLossyQuality, nsFloat(quality))
	}
	imgDstAddImage(dstRef, img, uintptr(props))
	if !imgDstFinalize(dstRef) {
		return nil, fmt.Errorf("mac: cannot encode image")
	}
	n := cfDataGetLength(data)
	out := make([]byte, n)
	copy(out, unsafe.Slice((*byte)(cfDataGetBytePtr(data)), n))
	return out, nil
}

// writeDataAtomic writes data to dst via a temp file in the same directory.
func writeDataAtomic(dst string, data []byte) (err error) {
	tmpPath, err := atomicTemp(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmpPath)
		}
	}()
	if err = os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err = os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// fitQuality is the lowest quality 「压缩到」tries before shrinking pixels.
const fitQuality = 0.4

// fitMinPx is the smallest longer side a fitted image shrinks to.
const fitMinPx = 640

// FitImage writes src to dst in format, upright, at most limit bytes.
// JPEG/HEIC binary-search quality down to fitQuality at each size, then
// shrink the longer side 0.85× per step down to fitMinPx; PNG skips
// quality and only shrinks. When nothing fits, the smallest result is
// written and over reports true. Safe on any goroutine.
func FitImage(src, dst string, format ImageFormat, limit int64) (over bool, err error) {
	if limit < 1 {
		return false, fmt.Errorf("mac: bad size limit %d", limit)
	}
	if err := loadEncode(); err != nil {
		return false, err
	}
	uti, err := formatUTI(format)
	if err != nil {
		return false, err
	}
	lossy := format != FormatPNG
	var best []byte
	withPool(func() {
		isrc, e := imageSource(src)
		if e != nil {
			err = e
			return
		}
		defer cfRelease(isrc)
		w, h := 0, 0
		if props := sourceProps(isrc); props != 0 {
			w = numInt(dictGet(props, kImgPropPixelWidth))
			h = numInt(dictGet(props, kImgPropPixelHeight))
			if o := exifOrientation(props); o >= 5 && o <= 8 {
				w, h = h, w // upright dimensions
			}
			cfRelease(props)
		}
		if w < 1 || h < 1 {
			// Readers that publish no pixel properties still decode.
			img := uprightImage(isrc)
			if img == 0 {
				err = fmt.Errorf("mac: cannot decode image %s", src)
				return
			}
			w, h = cgImgGetWidth(img), cgImgGetHeight(img)
			cfRelease(img)
		}
		maxPx := max(w, h)
		if maxPx < 1 {
			err = fmt.Errorf("mac: cannot decode image %s", src)
			return
		}
		orig := maxPx
		var smallest []byte // smallest encode so far, kept for when nothing fits
		for {
			var img uintptr
			if maxPx == orig {
				img = uprightImage(isrc) // full size, orientation baked
			} else {
				img = scaledImage(isrc, maxPx)
			}
			if img == 0 {
				err = fmt.Errorf("mac: cannot decode image %s", src)
				return
			}
			fit := false
			if lossy {
				// Highest quality that fits, binary-searched: a fitting q
				// raises the floor, an over one lowers the ceiling.
				lo, hi := fitQuality, 1.0
				for range 7 {
					q := (lo + hi) / 2
					data, e := encodeImageData(img, uti, format, q)
					if e != nil {
						cfRelease(img)
						err = e
						return
					}
					if len(smallest) == 0 || len(data) < len(smallest) {
						smallest = data
					}
					if len(data) <= int(limit) {
						best, fit = data, true
						lo = q
					} else {
						hi = q
					}
					if hi-lo < 0.02 {
						break
					}
				}
			} else {
				data, e := encodeImageData(img, uti, format, 0)
				if e != nil {
					cfRelease(img)
					err = e
					return
				}
				if len(smallest) == 0 || len(data) < len(smallest) {
					smallest = data
				}
				fit = len(data) <= int(limit)
				best = data
			}
			cfRelease(img)
			if fit || maxPx <= fitMinPx {
				break
			}
			next := int(float64(maxPx) * 0.85)
			if next < fitMinPx {
				next = fitMinPx
			}
			if next >= maxPx {
				break
			}
			maxPx = next
		}
		if len(best) == 0 {
			best = smallest
		}
	})
	if err != nil {
		return false, err
	}
	if len(best) == 0 {
		return false, fmt.Errorf("mac: cannot encode image %s", src)
	}
	over = int64(len(best)) > limit
	if err := writeDataAtomic(dst, best); err != nil {
		return false, err
	}
	return over, nil
}
