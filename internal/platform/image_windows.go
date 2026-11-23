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

	"rakko/internal/atomicfile"
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
		orig := max(img.Rect.Dx(), img.Rect.Dy())
		if orig < 1 {
			return errNoPixels
		}
		best, e := fitImageData(orig, format != FormatPNG, limit, func(maxPx int) (any, func(), error) {
			if maxPx == orig {
				return img, nil, nil
			}
			return scaleImage(img, maxPx), nil, nil
		}, func(frame any, q float64) ([]byte, error) {
			return encodeImageData(frame.(*image.RGBA), format, q)
		})
		if e != nil {
			return e
		}
		if len(best) == 0 {
			return fmt.Errorf("windows: cannot encode image %s", src)
		}
		over = int64(len(best)) > limit
		return atomicfile.WriteFile(filepath.Dir(dst), filepath.Base(dst), best, 0o644)
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
// included — upright, with its longer side at most maxPx, as premultiplied
// RGBA. The WIC pipeline scales before the output buffer is allocated and
// both decoders refuse a source past thumbMaxPixels, so listing a huge
// picture cannot cost its full-size pixels.
func Thumbnail(src string, maxPx int) (thumb *image.RGBA, err error) {
	err = wiApartment(func() error {
		img, e := wicDecodeScaled(src, maxPx)
		if e == nil {
			thumb = img
			return nil
		}
		// A format WIC lacks (WebP on older Windows): the Go decoders,
		// bounded the same way.
		img, gerr := goDecodeScaled(src, maxPx)
		if gerr != nil {
			return e
		}
		thumb = img
		return nil
	})
	return thumb, err
}
