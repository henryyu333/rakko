//go:build windows

package platform

// Image decoding, encoding and scaling for the Windows build: WIC for the
// formats the system reads (HEIC) and the standard library for PNG, JPEG
// and the rest of the formats Go decodes. Every decode bakes the EXIF
// orientation into the pixels, so an encoded copy is upright and carries no
// orientation flag — the same contract as the Mac build.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"

	_ "golang.org/x/image/bmp"
	stddraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// convertQuality is the lossy quality ConvertImage writes (the Mac build's
// kCGImageDestinationLossyCompressionQuality).
const convertQuality = 0.85

// decodeImage decodes path to upright RGBA. WIC goes first so the system's
// codecs (HEIC through the HEIF extension, TIFF, AVIF) are used; the Go
// decoders are the fallback for formats WIC lacks (WebP on older Windows).
func decodeImage(path string) (*image.RGBA, error) {
	if img, err := wicDecode(path); err == nil {
		return img, nil
	} else if img, gerr := goDecode(path); gerr == nil {
		return img, nil
	} else {
		return nil, err
	}
}

// goDecode decodes path with the Go decoders and bakes the JPEG EXIF
// orientation in.
func goDecode(path string) (*image.RGBA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	img := rgbaOf(src)
	if format == "jpeg" {
		img = upright(img, exifOrientation(data))
	}
	return img, nil
}

// goDecodeScaled is goDecode for a thumbnail: the header is read first, so
// an image past thumbMaxPixels is refused before any pixel buffer exists,
// and the result is scaled to maxPx.
func goDecodeScaled(path string, maxPx int) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, errNoPixels
	}
	if int64(cfg.Width)*int64(cfg.Height) > thumbMaxPixels {
		return nil, fmt.Errorf("windows: %s is %d×%d, above the %d-pixel thumbnail limit",
			filepath.Base(path), cfg.Width, cfg.Height, int64(thumbMaxPixels))
	}
	img, err := goDecode(path)
	if err != nil {
		return nil, err
	}
	return scaleImage(img, maxPx), nil
}

// rgbaOf copies any image to upright premultiplied RGBA, the layout
// image.RGBA defines.
func rgbaOf(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// upright returns img with EXIF orientation o applied (1 is a no-op, 0 is
// treated as 1). Orientation 5–8 swaps the dimensions.
func upright(img *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return img
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			copy(dst.Pix[y*dst.Stride+x*4:y*dst.Stride+x*4+4], img.Pix[sy*img.Stride+sx*4:])
		}
	}
	return dst
}

// exifOrientation reads the orientation tag of a JPEG's EXIF block (1 when
// absent or malformed). WIC normally answers this; the parser serves the
// Go fallback decoder.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0xD9 || marker == 0xDA {
			return 1
		}
		size := int(data[i+2])<<8 | int(data[i+3])
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

// tiffOrientation walks the TIFF directory inside an Exif block for tag
// 0x0112 (Orientation).
func tiffOrientation(b []byte) int {
	if len(b) < 8 {
		return 1
	}
	var bo binaryByteOrder
	switch {
	case b[0] == 'I' && b[1] == 'I':
		bo = littleEndian{}
	case b[0] == 'M' && b[1] == 'M':
		bo = bigEndian{}
	default:
		return 1
	}
	if bo.u16(b[2:]) != 42 {
		return 1
	}
	off := int(bo.u32(b[4:]))
	if off < 8 || off+2 > len(b) {
		return 1
	}
	n := int(bo.u16(b[off:]))
	for k := range n {
		e := off + 2 + k*12
		if e+12 > len(b) {
			return 1
		}
		if bo.u16(b[e:]) != 0x0112 {
			continue
		}
		if bo.u16(b[e+2:]) != 3 { // SHORT
			return 1
		}
		o := int(bo.u16(b[e+8:]))
		if o >= 1 && o <= 8 {
			return o
		}
		return 1
	}
	return 1
}

// binaryByteOrder reads the TIFF block's numbers.
type binaryByteOrder interface {
	u16([]byte) uint16
	u32([]byte) uint32
}

type littleEndian struct{}
type bigEndian struct{}

func (littleEndian) u16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
func (littleEndian) u32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
func (bigEndian) u16(b []byte) uint16 { return uint16(b[1]) | uint16(b[0])<<8 }
func (bigEndian) u32(b []byte) uint32 {
	return uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
}

// encodeImageData encodes img in format at quality (0.5 when the format is
// lossless). HEIC goes through WIC's encoder via a temporary file.
func encodeImageData(img *image.RGBA, format ImageFormat, quality float64) ([]byte, error) {
	switch format {
	case FormatPNG:
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case FormatJPEG:
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: qualityPercent(quality)}); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case FormatHEIC:
		f, err := os.CreateTemp("", "rakko-heic-*.heic")
		if err != nil {
			return nil, err
		}
		path := f.Name()
		f.Close()
		defer os.Remove(path)
		if err := wicEncodeHEIC(img, path, quality); err != nil {
			return nil, err
		}
		return os.ReadFile(path)
	}
	return nil, fmt.Errorf("windows: unknown image format %d", int(format))
}

// qualityPercent maps a 0..1 quality to the standard library's 1..100.
func qualityPercent(q float64) int {
	n := int(q*100 + 0.5)
	return min(max(n, 1), 100)
}

// writeImageAtomic encodes img to a temporary file beside dst and renames
// it into place, so a failed encode never leaves a partial output.
func writeImageAtomic(dst string, img *image.RGBA, format ImageFormat, quality float64) error {
	data, err := encodeImageData(img, format, quality)
	if err != nil {
		return err
	}
	return writeAtomicFile(dst, data)
}

// scaleImage returns img scaled so its longer side is at most maxPx; a
// smaller image is returned as it is.
func scaleImage(img *image.RGBA, maxPx int) *image.RGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	long := max(w, h)
	if maxPx < 1 || long <= maxPx {
		return img
	}
	nw := max(1, w*maxPx/long)
	nh := max(1, h*maxPx/long)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	stddraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), stddraw.Src, nil)
	return dst
}

// errNoPixels is a decode that produced nothing.
var errNoPixels = errors.New("windows: image has no pixels")
