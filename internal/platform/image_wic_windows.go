//go:build windows

package platform

// Windows Imaging Component (WIC) access: decoding every format the system
// has a codec for — HEIC included, which no Go decoder reads — and encoding
// HEIC, which the standard library cannot write. PNG and JPEG are encoded
// with the standard library instead (deterministic, no COM). The encoder
// and decoder GUIDs are the Windows SDK's; the vtable slot numbers are
// commented next to their calls.

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WIC identifiers (wincodec.h).
var (
	wiCLSIDWICImagingFactory    = wiParseGUID("cacaf262-9370-4615-a13b-9f5539da4c0a")
	wiIIDWICImagingFactory      = wiParseGUID("ec5ec8a9-c395-4314-9c77-54d7a935ff70")
	wiIIDWICBitmapFrameDecode   = wiParseGUID("3b16811b-6a43-4ec9-a813-3d930c13b940")
	wiIIDWICFormatConverter     = wiParseGUID("00000301-a8f2-4877-ba0a-fd2b6645fb94")
	wiIIDWICStream              = wiParseGUID("135ff860-22b7-4ddf-b0f6-218f4f299a43")
	wiIIDWICBitmapEncoder       = wiParseGUID("00000103-a8f2-4877-ba0a-fd2b6645fb94")
	wiIIDWICBitmapFrameEncode   = wiParseGUID("00000105-a8f2-4877-ba0a-fd2b6645fb94")
	wiIIDWICMetadataQueryReader = wiParseGUID("30989668-e1c9-4597-b395-458eedb808df")
	wiGUIDPixelBGRA             = wiParseGUID("6fddc324-4e03-4bfe-b185-3d77768dc90f")
	wiGUIDPixelPBGRA            = wiParseGUID("6fddc324-4e03-4bfe-b185-3d77768dc910")
	wiGUIDPixel24BGR            = wiParseGUID("6fddc324-4e03-4bfe-b185-3d77768dc90c")
	wiGUIDPixel32BGR            = wiParseGUID("6fddc324-4e03-4bfe-b185-3d77768dc90e")
	wiGUIDContainerHeif         = wiParseGUID("e1e62521-6787-405b-a339-500715b5763f")
)

// wiCLSCTXInProcServer: WIC's codecs live in-process.
const wiCLSCTXInProcServer = 1

// wicErrCodecMissing is WINCODEC_ERR_COMPONENTNOTFOUND, which WIC answers
// when no codec claims the file (a HEIC file without the HEIF extension).
const wicErrCodecMissing = 0x88982F50

// IWICImagingFactory vtable slots after IUnknown's three.
const (
	wicFactoryCreateDecoderFromFilename = 3
	wicFactoryCreateEncoder             = 8
	wicFactoryCreateFormatConverter     = 10
	wicFactoryCreateStream              = 14
)

// IWICBitmapDecoder vtable slots.
const (
	wicDecoderGetMetadataQueryReader = 8
	wicDecoderGetFrame               = 13
)

// IWICBitmapSource (IWICBitmapFrameDecode/IWICFormatConverter base) slots.
const (
	wicSourceGetSize    = 3
	wicSourceCopyPixels = 7
	wicConverterInit    = 8
)

// IWICStream inherits IStream, whose twelve slots follow IUnknown's.
const wicStreamInitializeFromFilename = 15

// IWICBitmapEncoder slots.
const (
	wicEncoderInitialize     = 3
	wicEncoderCreateNewFrame = 10
	wicEncoderCommit         = 11
)

// IWICBitmapFrameEncode slots.
const (
	wicFrameInitialize     = 3
	wicFrameSetSize        = 4
	wicFrameSetPixelFormat = 6
	wicFrameWritePixels    = 10
	wicFrameCommit         = 12
)

// IWICMetadataQueryReader::GetMetadataByName.
const wicMetadataGetByName = 5

// wicFactory creates the imaging factory; the caller releases it.
func wicFactory() (uintptr, error) {
	var f uintptr
	hr, _, _ := wiProcs.coCreateInstance.Call(
		uintptr(unsafe.Pointer(&wiCLSIDWICImagingFactory)), 0, wiCLSCTXInProcServer,
		uintptr(unsafe.Pointer(&wiIIDWICImagingFactory)), uintptr(unsafe.Pointer(&f)))
	if wiFailed(hr) {
		return 0, wiErr("CoCreateInstance(WICImagingFactory)", hr)
	}
	return f, nil
}

// wicFrame opens path's first frame. The caller releases decoder and frame.
func wicFrame(factory uintptr, path string) (decoder, frame uintptr, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	const (
		genericRead               = 0x80000000
		wicDecodeMetadataOnDemand = 0
	)
	hr := wiInvoke(factory, wicFactoryCreateDecoderFromFilename,
		uintptr(unsafe.Pointer(p)), 0, genericRead, wicDecodeMetadataOnDemand,
		uintptr(unsafe.Pointer(&decoder)))
	if wiFailed(hr) {
		if uint32(hr) == wicErrCodecMissing {
			return 0, 0, fmt.Errorf("windows: no codec for %s (HEIC needs the HEIF Image Extensions)", path)
		}
		return 0, 0, wiErr("WIC decoder for "+path, hr)
	}
	hr = wiInvoke(decoder, wicDecoderGetFrame, 0, uintptr(unsafe.Pointer(&frame)))
	if wiFailed(hr) {
		wiRelease(decoder)
		return 0, 0, wiErr("WIC first frame of "+path, hr)
	}
	return decoder, frame, nil
}

// wicInfo is a file's stored pixel size and EXIF orientation.
type wicInfo struct {
	w, h        int
	orientation int
}

// wicReadInfo reads the size and the EXIF orientation without decoding
// the pixels.
func wicReadInfo(factory uintptr, path string) (wicInfo, error) {
	decoder, frame, err := wicFrame(factory, path)
	if err != nil {
		return wicInfo{}, err
	}
	defer wiRelease(frame)
	defer wiRelease(decoder)
	var w, h uint32
	hr := wiInvoke(frame, wicSourceGetSize, uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)))
	if wiFailed(hr) {
		return wicInfo{}, wiErr("WIC GetSize of "+path, hr)
	}
	return wicInfo{w: int(w), h: int(h), orientation: wicOrientation(frame)}, nil
}

// wicOrientation reads the EXIF orientation tag (1 when absent): the
// metadata path is the same one ImageIO exposes, in WIC's query syntax.
func wicOrientation(frame uintptr) int {
	var reader uintptr
	hr := wiInvoke(frame, wicDecoderGetMetadataQueryReader, uintptr(unsafe.Pointer(&reader)))
	if wiFailed(hr) || reader == 0 {
		return 1
	}
	defer wiRelease(reader)
	name, err := windows.UTF16PtrFromString("/app1/ifd/{ushort=274}")
	if err != nil {
		return 1
	}
	// PROPVARIANT: vt at offset 0, the value union at offset 8.
	var prop [4]uintptr
	hr = wiInvoke(reader, wicMetadataGetByName, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&prop[0])))
	if wiFailed(hr) {
		return 1
	}
	if *(*uint16)(unsafe.Pointer(&prop[0])) != 18 { // VT_UI2
		return 1
	}
	o := int(*(*uint16)(unsafe.Pointer(&prop[1])))
	if o < 1 || o > 8 {
		return 1
	}
	return o
}

// wicDecode decodes path's first frame to upright RGBA: 32bpp BGRA from
// the system's converter, then the EXIF orientation is baked in so the
// result has no orientation tag of its own.
func wicDecode(path string) (*image.RGBA, error) {
	factory, err := wicFactory()
	if err != nil {
		return nil, err
	}
	defer wiRelease(factory)
	decoder, frame, err := wicFrame(factory, path)
	if err != nil {
		return nil, err
	}
	defer wiRelease(frame)
	defer wiRelease(decoder)
	var w, h uint32
	hr := wiInvoke(frame, wicSourceGetSize, uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)))
	if wiFailed(hr) {
		return nil, wiErr("WIC GetSize of "+path, hr)
	}
	if w == 0 || h == 0 {
		return nil, errors.New("windows: image has no pixels: " + path)
	}
	var converter uintptr
	hr = wiInvoke(factory, wicFactoryCreateFormatConverter, uintptr(unsafe.Pointer(&converter)))
	if wiFailed(hr) {
		return nil, wiErr("WIC format converter", hr)
	}
	defer wiRelease(converter)
	// The alpha threshold is a C double, which syscall.SyscallN cannot
	// place in an XMM register; WIC ignores it for 32bppBGRA, the only
	// format asked for here.
	hr = wiInvoke(converter, wicConverterInit, frame,
		uintptr(unsafe.Pointer(&wiGUIDPixelBGRA)), 0 /*WICBitmapDitherTypeNone*/, 0, 0, 0 /*WICBitmapPaletteTypeCustom*/)
	if wiFailed(hr) {
		return nil, wicDecodeError(path, hr)
	}
	stride := int(w) * 4
	buf := make([]byte, stride*int(h))
	hr = wiInvoke(converter, wicSourceCopyPixels, 0, uintptr(stride), uintptr(len(buf)),
		uintptr(unsafe.Pointer(&buf[0])))
	if wiFailed(hr) {
		return nil, wicDecodeError(path, hr)
	}
	img := &image.RGBA{Pix: buf, Stride: stride, Rect: image.Rect(0, 0, int(w), int(h))}
	for i := 0; i < len(buf); i += 4 {
		buf[i], buf[i+2] = buf[i+2], buf[i]
	}
	return upright(img, wicOrientation(frame)), nil
}

// wicDecodeError words a failed decode. A HEIC file whose HEVC decoder is
// missing parses its header but cannot produce pixels (WINCODEC_ERR_...),
// which the user needs to hear as a missing codec, not as a number.
func wicDecodeError(path string, hr uintptr) error {
	switch ext := strings.ToLower(filepath.Ext(path)); uint32(hr) {
	case wicErrCodecMissing, 0x88982F07: // WINCODEC_ERR_UNSUPPORTEDPIXELFORMAT
		if ext == ".heic" || ext == ".heif" {
			return errors.New("windows: cannot decode " + filepath.Base(path) +
				": the HEVC codec is missing (install HEVC Video Extensions from the Microsoft Store)")
		}
	}
	return wiErr("WIC decode of "+path, hr)
}

// wicPixelFormats are the layouts the HEIC encoder may ask for, keyed by
// the GUID it leaves in SetPixelFormat.
type wicPixelFormat struct {
	guid    wiGUID
	stride  func(w int) int
	convert func(dst []byte, src *image.RGBA, w, h int)
}

var wicPixelFormats = []wicPixelFormat{
	{
		guid: wiGUIDPixelBGRA, stride: func(w int) int { return w * 4 },
		convert: func(dst []byte, src *image.RGBA, w, h int) {
			copy(dst, src.Pix) // WIC BGRA and a Go RGBA differ only in channel order
			for i := 0; i < len(dst); i += 4 {
				dst[i], dst[i+2] = dst[i+2], dst[i]
			}
		},
	},
	{
		guid: wiGUIDPixelPBGRA, stride: func(w int) int { return w * 4 },
		convert: func(dst []byte, src *image.RGBA, w, h int) {
			for i := 0; i < len(dst); i += 4 {
				a := uint32(src.Pix[i+3])
				put := func(j int, v uint8) {
					dst[i+j] = uint8(uint32(v) * a / 255)
				}
				put(0, src.Pix[i+2]) // B
				put(1, src.Pix[i+1]) // G
				put(2, src.Pix[i])   // R
				dst[i+3] = src.Pix[i+3]
			}
		},
	},
	{
		// 32bppBGR: three channels and a byte the decoder ignores, the
		// HEIF encoder's preferred layout.
		guid: wiGUIDPixel32BGR, stride: func(w int) int { return w * 4 },
		convert: func(dst []byte, src *image.RGBA, w, h int) {
			for i := 0; i < len(dst); i += 4 {
				dst[i], dst[i+1], dst[i+2], dst[i+3] = src.Pix[i+2], src.Pix[i+1], src.Pix[i], 0xFF
			}
		},
	},
	{
		guid: wiGUIDPixel24BGR, stride: func(w int) int { return w * 3 },
		convert: func(dst []byte, src *image.RGBA, w, h int) {
			for y := range h {
				for x := range w {
					s := src.Pix[y*src.Stride+x*4:]
					d := dst[y*w*3+x*3:]
					d[0], d[1], d[2] = s[2], s[1], s[0]
				}
			}
		},
	},
}

// wicEncodeHEIC writes img as a HEIC file at path through the system's
// HEIF encoder, without transparency.
func wicEncodeHEIC(img *image.RGBA, path string, quality float64) error {
	factory, err := wicFactory()
	if err != nil {
		return err
	}
	defer wiRelease(factory)
	var encoder uintptr
	hr := wiInvoke(factory, wicFactoryCreateEncoder, uintptr(unsafe.Pointer(&wiGUIDContainerHeif)), 0,
		uintptr(unsafe.Pointer(&encoder)))
	if wiFailed(hr) {
		return fmt.Errorf("windows: HEIC encoder unavailable (install the HEIF Image Extensions): 0x%08X", uint32(hr))
	}
	defer wiRelease(encoder)
	var stream uintptr
	hr = wiInvoke(factory, wicFactoryCreateStream, uintptr(unsafe.Pointer(&stream)))
	if wiFailed(hr) {
		return wiErr("WIC stream", hr)
	}
	defer wiRelease(stream)
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	const genericWrite = 0x40000000
	hr = wiInvoke(stream, wicStreamInitializeFromFilename, uintptr(unsafe.Pointer(p)), genericWrite)
	if wiFailed(hr) {
		return wiErr("WIC stream for "+path, hr)
	}
	hr = wiInvoke(encoder, wicEncoderInitialize, stream, 0 /*WICBitmapEncoderNoCache*/)
	if wiFailed(hr) {
		return wiErr("WIC encoder", hr)
	}
	var frame, props uintptr
	hr = wiInvoke(encoder, wicEncoderCreateNewFrame, uintptr(unsafe.Pointer(&frame)), uintptr(unsafe.Pointer(&props)))
	if wiFailed(hr) {
		return wiErr("WIC encoder frame", hr)
	}
	defer wiRelease(frame)
	if props != 0 {
		wicSetQuality(props, quality)
		defer wiRelease(props)
	}
	hr = wiInvoke(frame, wicFrameInitialize, props)
	if wiFailed(hr) {
		return wiErr("WIC frame", hr)
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	hr = wiInvoke(frame, wicFrameSetSize, uintptr(w), uintptr(h))
	if wiFailed(hr) {
		return wiErr("WIC frame size", hr)
	}
	// Ask for no alpha first; the encoder may replace the GUID with the
	// layout it wants, which wicPixelFormats converts to.
	format := wiGUIDPixel24BGR
	hr = wiInvoke(frame, wicFrameSetPixelFormat, uintptr(unsafe.Pointer(&format)))
	if wiFailed(hr) {
		return wiErr("WIC frame pixel format", hr)
	}
	pf := wicPickPixelFormat(format)
	if pf == nil {
		return fmt.Errorf("windows: HEIC encoder wants pixel format %s", format.String())
	}
	stride := pf.stride(w)
	buf := make([]byte, stride*h)
	pf.convert(buf, img, w, h)
	hr = wiInvoke(frame, wicFrameWritePixels, uintptr(h), uintptr(stride), uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	if wiFailed(hr) {
		return wiErr("WIC WritePixels", hr)
	}
	hr = wiInvoke(frame, wicFrameCommit)
	if wiFailed(hr) {
		return wicEncodeError(hr)
	}
	hr = wiInvoke(encoder, wicEncoderCommit)
	if wiFailed(hr) {
		return wicEncodeError(hr)
	}
	return nil
}

// wicEncodeError words a HEIC write failure. The HEIF container encoder is
// part of the HEIF Image Extensions, but the HEVC bitstream encoder is a
// separate download: without it the commit fails with a codec-not-found
// code, which must not surface as a bare HRESULT.
func wicEncodeError(hr uintptr) error {
	switch uint32(hr) {
	case 0xC00D5212: // MF_E_TOPO_CODEC_NOT_FOUND
		return errors.New("windows: HEIC encoding needs the HEVC Video Extensions (install it from the Microsoft Store)")
	case wicErrCodecMissing:
		return errors.New("windows: HEIC encoding needs the HEIF Image Extensions")
	}
	return wiErr("WIC HEIC encode", hr)
}

// wicSetQuality writes the encoder option "ImageQuality" into the frame's
// property bag. A bag that refuses it keeps the encoder's default.
func wicSetQuality(bag uintptr, quality float64) {
	// IPropertyBag2::Write is slot 7; PROPBAG2 is the one property below.
	type propBag2 struct {
		dwType   uint32
		vt       uint16
		cfType   uint16
		dwHint   uint32
		pstrName *uint16
		clsid    wiGUID
		_        [4]byte
	}
	name, err := windows.UTF16PtrFromString("ImageQuality")
	if err != nil {
		return
	}
	pb := propBag2{dwType: 0 /*PROPBAG2_TYPE_DATA*/, vt: 4 /*VT_R4*/, pstrName: name}
	// PROPVARIANT: vt, three reserved words, then the value union.
	var value [4]uintptr
	*(*uint16)(unsafe.Pointer(&value[0])) = 4 // VT_R4
	*(*float32)(unsafe.Pointer(&value[1])) = float32(quality)
	// A refusal here only means the encoder keeps its default quality.
	wiInvoke(bag, 7, 1, uintptr(unsafe.Pointer(&pb)), uintptr(unsafe.Pointer(&value[0])))
}

// wicPickPixelFormat finds the converter for the GUID the HEIC encoder
// settled on.
func wicPickPixelFormat(g wiGUID) *wicPixelFormat {
	for i := range wicPixelFormats {
		if wicPixelFormats[i].guid == g {
			return &wicPixelFormats[i]
		}
	}
	return nil
}
