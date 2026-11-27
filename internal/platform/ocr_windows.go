//go:build windows

package platform

// Text recognition through Windows.Media.Ocr. The engine is built for the
// first installed recognizer among Chinese (Simplified, then Traditional),
// then the user's profile languages — a Chinese engine reads Latin text
// too, so one engine covers the mixed screen text the Mac build hands to
// Vision. Recognition runs on the caller's goroutine inside its own COM
// apartment, so several conversions can recognize in sequence without
// sharing state.

import (
	"errors"
	"os"
	"unsafe"
)

// OCR identifiers (windows.media.ocr.h, windows.globalization.h).
var (
	wiIIDOcrEngineStatics              = wiParseGUID("5bffa85a-3384-3540-9940-699120d428a8")
	wiIIDOcrEngine                     = wiParseGUID("5a14bc41-5b76-3140-b680-8825562683ac")
	wiIIDOcrResult                     = wiParseGUID("9bd235b2-175b-3d6a-92e2-388c206e2f63")
	wiIIDOcrLine                       = wiParseGUID("0043a16f-e31f-3a24-899c-d444bd088124")
	wiIIDBitmapDecoderStatics          = wiParseGUID("438ccb26-bcef-4e95-bad6-23a822e58d01")
	wiIIDBitmapFrameWithSoftwareBitmap = wiParseGUID("fe287c9a-420c-4963-87ad-691436e08383")
	wiIIDLanguageFactory               = wiParseGUID("9b0252ac-0c27-44f8-b792-9793fb66c63e")
)

// Vtable slots, after IInspectable's six.
const (
	ocrStaticsMaxImageDimension    = 6
	ocrStaticsTryCreateFromLang    = 9
	ocrStaticsTryCreateFromProfile = 10

	bitmapDecoderCreate  = 14
	bitmapFrameGetBitmap = 6

	ocrEngineRecognize = 6
	ocrResultGetLines  = 6
	ocrLineText        = 7

	languageFactoryCreate = 6

	// IVectorView<OcrLine>: GetAt, Size, IndexOf, GetMany. First lives on
	// the IIterable interface it also answers.
	vectorViewGetAt = 6
	vectorViewSize  = 7
)

// recognizeMaxSide bounds the pixels handed to the recognizer; the engine's
// own MaxImageDimension is the harder limit for the installed language.
const recognizeMaxSide = 6144

// RecognizeText runs the system OCR engine on the upright image at src and
// returns the recognized lines in reading order. No recognized text yields
// (nil, nil). A machine without a recognizer language pack reports an error
// naming the missing feature.
func RecognizeText(src string) (lines []string, err error) {
	err = wiApartment(func() error {
		img, e := decodeImage(src)
		if e != nil {
			return e
		}
		engine, limit, e := ocrEngine()
		if e != nil {
			return e
		}
		defer wiRelease(engine)
		if limit <= 0 {
			limit = recognizeMaxSide
		}
		img = scaleImage(img, min(limit, recognizeMaxSide))
		png, e := encodeImageData(img, FormatPNG, 0)
		if e != nil {
			return e
		}
		tmp, e := os.CreateTemp("", "rakko-ocr-*.png")
		if e != nil {
			return e
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if _, e := tmp.Write(png); e != nil {
			tmp.Close()
			return e
		}
		tmp.Close()
		bitmap, e := ocrSoftwareBitmap(tmpPath)
		if e != nil {
			return e
		}
		defer wiRelease(bitmap)
		var op uintptr
		hr := wiInvoke(engine, ocrEngineRecognize, bitmap, uintptr(unsafe.Pointer(&op)))
		if wiFailed(hr) {
			return wiErr("OcrEngine.RecognizeAsync", hr)
		}
		result, e := wiAwaitResult(op, "OcrEngine.RecognizeAsync")
		if e != nil {
			return e
		}
		defer wiRelease(result)
		lines = ocrResultLines(result)
		return nil
	})
	return lines, err
}

// ocrEngine builds a recognizer: Chinese first so Latin and Chinese screen
// text share one engine, then whatever the user's languages offer. It also
// reports the largest image side the engine accepts.
func ocrEngine() (uintptr, int, error) {
	factory, err := wiClassFactory("Windows.Media.Ocr.OcrEngine", wiIIDOcrEngineStatics)
	if err != nil {
		return 0, 0, err
	}
	defer wiRelease(factory)
	var maxDim uint32
	wiInvoke(factory, ocrStaticsMaxImageDimension, uintptr(unsafe.Pointer(&maxDim)))
	langFactory, err := wiClassFactory("Windows.Globalization.Language", wiIIDLanguageFactory)
	if err != nil {
		return 0, int(maxDim), err
	}
	defer wiRelease(langFactory)
	for _, tag := range []string{"zh-Hans", "zh-Hant"} {
		lang, err := ocrLanguage(langFactory, tag)
		if err != nil {
			continue
		}
		var engine uintptr
		hr := wiInvoke(factory, ocrStaticsTryCreateFromLang, lang, uintptr(unsafe.Pointer(&engine)))
		wiRelease(lang)
		if wiFailed(hr) || engine == 0 {
			continue // that language pack is not installed
		}
		return engine, int(maxDim), nil
	}
	var engine uintptr
	hr := wiInvoke(factory, ocrStaticsTryCreateFromProfile, uintptr(unsafe.Pointer(&engine)))
	if wiFailed(hr) {
		return 0, int(maxDim), wiErr("OcrEngine.TryCreateFromUserProfileLanguages", hr)
	}
	if engine == 0 {
		return 0, int(maxDim), errors.New("windows: no OCR language pack is installed (add one in Settings › Time & Language › Language)")
	}
	return engine, int(maxDim), nil
}

// ocrLanguage makes a Windows.Globalization.Language for tag.
func ocrLanguage(factory uintptr, tag string) (uintptr, error) {
	h, err := wiNewHString(tag)
	if err != nil {
		return 0, err
	}
	defer wiDeleteHString(h)
	var lang uintptr
	hr := wiInvoke(factory, languageFactoryCreate, h, uintptr(unsafe.Pointer(&lang)))
	if wiFailed(hr) {
		return 0, wiErr("Language.CreateLanguage("+tag+")", hr)
	}
	return lang, nil
}

// ocrSoftwareBitmap decodes the picture at path into the SoftwareBitmap the
// recognizer takes: the imaging stack reads it, so no pixel buffer has to
// cross the WinRT boundary by hand.
func ocrSoftwareBitmap(path string) (uintptr, error) {
	stream, err := openFileStream(path, fileAccessModeRead)
	if err != nil {
		return 0, err
	}
	defer wiRelease(stream)
	factory, err := wiClassFactory("Windows.Graphics.Imaging.BitmapDecoder", wiIIDBitmapDecoderStatics)
	if err != nil {
		return 0, err
	}
	defer wiRelease(factory)
	var op uintptr
	hr := wiInvoke(factory, bitmapDecoderCreate, stream, uintptr(unsafe.Pointer(&op)))
	if wiFailed(hr) {
		return 0, wiErr("BitmapDecoder.CreateAsync", hr)
	}
	decoder, err := wiAwaitResult(op, "BitmapDecoder.CreateAsync")
	if err != nil {
		return 0, err
	}
	defer wiRelease(decoder)
	var frame uintptr
	hr = wiQueryInterface(decoder, &wiIIDBitmapFrameWithSoftwareBitmap, &frame)
	if wiFailed(hr) || frame == 0 {
		return 0, wiErr("BitmapDecoder as IBitmapFrameWithSoftwareBitmap", hr)
	}
	defer wiRelease(frame)
	hr = wiInvoke(frame, bitmapFrameGetBitmap, uintptr(unsafe.Pointer(&op)))
	if wiFailed(hr) {
		return 0, wiErr("BitmapFrame.GetSoftwareBitmapAsync", hr)
	}
	return wiAwaitResult(op, "BitmapFrame.GetSoftwareBitmapAsync")
}

// ocrResultLines reads the result's lines in reading order.
func ocrResultLines(result uintptr) []string {
	// GetResults hands back the class object; ask for IOcrResult itself so
	// the slots below are the interface's own.
	var io uintptr
	hr := wiQueryInterface(result, &wiIIDOcrResult, &io)
	if wiFailed(hr) || io == 0 {
		return nil
	}
	defer wiRelease(io)
	var vec uintptr
	hr = wiInvoke(io, ocrResultGetLines, uintptr(unsafe.Pointer(&vec)))
	if wiFailed(hr) || vec == 0 {
		return nil
	}
	defer wiRelease(vec)
	var n uint32
	hr = wiInvoke(vec, vectorViewSize, uintptr(unsafe.Pointer(&n)))
	if wiFailed(hr) || n == 0 {
		return nil
	}
	lines := make([]string, 0, n)
	for i := range int(n) {
		var line uintptr
		hr = wiInvoke(vec, vectorViewGetAt, uintptr(i), uintptr(unsafe.Pointer(&line)))
		if wiFailed(hr) || line == 0 {
			continue
		}
		var text uintptr
		var ioLine uintptr
		if hr := wiQueryInterface(line, &wiIIDOcrLine, &ioLine); !wiFailed(hr) && ioLine != 0 {
			hr = wiInvoke(ioLine, ocrLineText, uintptr(unsafe.Pointer(&text)))
			wiRelease(ioLine)
		}
		if !wiFailed(hr) && text != 0 {
			lines = append(lines, ocrCompactCJK(wiHString(text)))
			wiDeleteHString(text)
		}
		wiRelease(line)
	}
	return lines
}
