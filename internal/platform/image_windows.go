//go:build windows

package platform

// Image, PDF and text recognition. Conversion runs on WIC (system codecs;
// HEIC needs the HEIF Image Extensions) with the standard library writing
// PNG and JPEG; PDF pages render through Windows.Data.Pdf, PDF text is read
// with a pure-Go parser, and the PDF export is written here. The calls are
// safe on any goroutine.

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
)

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

// ConvertImage writes src to dst in format. The output's pixels are the
// visually upright image: any EXIF orientation is baked in and the result
// carries no orientation flag. The write is atomic and src is never
// modified.
func ConvertImage(src, dst string, format ImageFormat) error {
	var err error
	err = wiApartment(func() error {
		img, e := decodeImage(src)
		if e != nil {
			return e
		}
		return writeImageAtomic(dst, img, format, convertQuality)
	})
	return err
}

// FitImage writes src to dst in format, upright, at most limit bytes.
// JPEG and HEIC trade quality down to 0.4 at each size, then the longer
// side shrinks 0.85× per step down to 640 px; PNG only ever shrinks. When
// nothing fits, the smallest result is written and over reports true.
func FitImage(src, dst string, format ImageFormat, limit int64) (over bool, err error) {
	if limit < 1 {
		return false, fmt.Errorf("windows: bad size limit %d", limit)
	}
	err = wiApartment(func() error {
		img, e := decodeImage(src)
		if e != nil {
			return e
		}
		long := max(img.Rect.Dx(), img.Rect.Dy())
		if long < 1 {
			return errNoPixels
		}
		lossy := format != FormatPNG
		orig := long
		var best, smallest []byte
		for {
			frame := img
			if long != orig {
				frame = scaleImage(img, long)
			}
			fit := false
			if lossy {
				// Highest quality that fits, binary-searched: a fitting q
				// raises the floor, an over one lowers the ceiling.
				lo, hi := fitQuality, 1.0
				for range 7 {
					q := (lo + hi) / 2
					data, e := encodeImageData(frame, format, q)
					if e != nil {
						return e
					}
					if len(smallest) == 0 || len(data) < len(smallest) {
						smallest = data
					}
					if int64(len(data)) <= limit {
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
				data, e := encodeImageData(frame, format, 0)
				if e != nil {
					return e
				}
				if len(smallest) == 0 || len(data) < len(smallest) {
					smallest = data
				}
				fit = int64(len(data)) <= limit
				best = data
			}
			if fit || long <= fitMinPx {
				break
			}
			next := int(float64(long) * 0.85)
			if next < fitMinPx {
				next = fitMinPx
			}
			if next >= long {
				break
			}
			long = next
		}
		if len(best) == 0 {
			best = smallest
		}
		if len(best) == 0 {
			return fmt.Errorf("windows: cannot encode image %s", src)
		}
		over = int64(len(best)) > limit
		return writeAtomicFile(dst, best)
	})
	return over, err
}

// ImageAlpha reports whether src carries transparency: whether any pixel is
// not fully opaque. The Mac build answers from the alpha channel's presence
// instead, so a fully opaque PNG compresses to JPEG here and stays PNG
// there.
func ImageAlpha(src string) (alpha bool, err error) {
	err = wiApartment(func() error {
		img, e := decodeImage(src)
		if e != nil {
			return e
		}
		for i := 3; i < len(img.Pix); i += 4 {
			if img.Pix[i] != 0xFF {
				alpha = true
				return nil
			}
		}
		return nil
	})
	return alpha, err
}

// ImageSize reports the upright pixel size of an image: the displayed
// dimensions, so a 90°-rotated photo reports the swapped width and height.
func ImageSize(src string) (w, h int, err error) {
	err = wiApartment(func() error {
		factory, e := wicFactory()
		if e != nil {
			return e
		}
		defer wiRelease(factory)
		info, e := wicReadInfo(factory, src)
		if e != nil {
			// A format WIC lacks (WebP on older Windows): ask the Go
			// decoders for the header alone.
			f, oerr := os.Open(src)
			if oerr != nil {
				return e
			}
			cfg, _, cerr := image.DecodeConfig(f)
			f.Close()
			if cerr != nil {
				return e
			}
			w, h = cfg.Width, cfg.Height
			return nil
		}
		w, h = info.w, info.h
		if info.orientation >= 5 && info.orientation <= 8 {
			w, h = h, w
		}
		return nil
	})
	return w, h, err
}

// ResizeImage writes src to dst in format, upright and scaled so its
// longer side is at most maxPx; a smaller image is re-encoded at its own
// size, never enlarged.
func ResizeImage(src, dst string, format ImageFormat, maxPx int) error {
	return wiApartment(func() error {
		img, err := decodeImage(src)
		if err != nil {
			return err
		}
		if maxPx < 1 {
			return fmt.Errorf("windows: bad pixel limit %d", maxPx)
		}
		return writeImageAtomic(dst, scaleImage(img, maxPx), format, convertQuality)
	})
}

// Thumbnail decodes src — any format the system reads, HEIC and TIFF
// included — upright, with its longer side at most maxPx, as straight
// alpha RGBA.
func Thumbnail(src string, maxPx int) (thumb *image.RGBA, err error) {
	err = wiApartment(func() error {
		img, e := decodeImage(src)
		if e != nil {
			return e
		}
		thumb = scaleImage(img, maxPx)
		return nil
	})
	return thumb, err
}

// fitQuality is the lowest quality 「压缩到」tries before shrinking pixels.
const fitQuality = 0.4

// fitMinPx is the smallest longer side a fitted image shrinks to.
const fitMinPx = 640

// writeAtomicFile writes data beside dst and renames it into place.
func writeAtomicFile(dst string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dst), ".rakko-image-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
