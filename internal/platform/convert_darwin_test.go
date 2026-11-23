//go:build darwin

package platform

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Test-only bindings for fixture rendering and verification: CoreText for
// drawing text into a bitmap, CGPDFDocument for reading page boxes back,
// and pixel access for colour assertions.
var (
	cfStringCreate  func(alloc uintptr, cstr *byte, enc uint32) uintptr
	ctFontCreate    func(name uintptr, size float64, mtx uintptr) uintptr
	ctLineCreate    func(attrString uintptr) uintptr
	ctLineDraw      func(line uintptr, ctx uintptr)
	cfAttrStrCreate func(alloc uintptr, s uintptr, attrs uintptr) uintptr
	kCTFontAttr     id

	testOnce sync.Once
	testErr  error
)

const kCFStringEncodingUTF8 = 0x08000100

// loadTest binds the fixture/verification symbols once.
func loadTest() error {
	testOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				testErr = fmt.Errorf("test setup: %v", r)
			}
		}()
		if err := loadConvert(); err != nil {
			testErr = err
			return
		}
		libCT, err := purego.Dlopen("/System/Library/Frameworks/CoreText.framework/CoreText", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			testErr = err
			return
		}
		purego.RegisterLibFunc(&cfStringCreate, purego.RTLD_DEFAULT, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&cfAttrStrCreate, purego.RTLD_DEFAULT, "CFAttributedStringCreate")
		purego.RegisterLibFunc(&ctFontCreate, libCT, "CTFontCreateWithName")
		purego.RegisterLibFunc(&ctLineCreate, libCT, "CTLineCreateWithAttributedString")
		purego.RegisterLibFunc(&ctLineDraw, libCT, "CTLineDraw")
		addr, err := purego.Dlsym(libCT, "kCTFontAttributeName")
		if err != nil {
			testErr = err
			return
		}
		var copySymbol func(dst *id, src uintptr, size uintptr) uintptr
		purego.RegisterLibFunc(&copySymbol, purego.RTLD_DEFAULT, "memcpy")
		copySymbol(&kCTFontAttr, addr, unsafe.Sizeof(kCTFontAttr))
	})
	return testErr
}

// cfStr returns a CFString (Create rule; caller CFReleases).
func cfStr(s string) uintptr {
	b := append([]byte(s), 0)
	return cfStringCreate(0, &b[0], kCFStringEncodingUTF8)
}

// nsInt returns an autoreleased NSNumber wrapping an int.
func nsInt(v int) id {
	return send(class("NSNumber"), "numberWithInt:", uintptr(v))
}

// rgba returns a CGBitmapContext writing into buf (dw*dh*4 bytes,
// RGBA premultiplied) or 0 on failure. The caller CFReleases the context;
// buf is written synchronously by draws.
func rgba(buf []byte, w, h int) uintptr {
	space := cgColorSpaceDeviceRGB()
	if space == 0 {
		return 0
	}
	defer cfRelease(space)
	return cgBitmapCreate(unsafe.Pointer(&buf[0]), w, h, 8, w*4, space, uint32(1|4<<12))
}

// pixelsOf decodes a CGImage into RGBA bytes (w*h*4), top row first.
func pixelsOf(t *testing.T, img uintptr, w, h int) []byte {
	t.Helper()
	px := make([]byte, w*h*4)
	ctx := rgba(px, w, h)
	if ctx == 0 {
		t.Fatal("pixelsOf: no bitmap context")
	}
	defer cfRelease(ctx)
	cgCtxDrawImage(ctx, NSRect{Size: NSSize{Width: float64(w), Height: float64(h)}}, img)
	return px
}

// decodePixels reads an image file and returns its raw stored pixels
// (RGBA, top row first) plus the stored dimensions. Orientation is NOT
// applied — this inspects the encoded bitmap.
func decodePixels(t *testing.T, path string) (px []byte, w, h int) {
	t.Helper()
	withPool(func() {
		isrc, err := imageSource(path)
		if err != nil {
			t.Fatal(err)
		}
		defer cfRelease(isrc)
		img := imgSrcCreateImageAt(isrc, 0, 0)
		if img == 0 {
			t.Fatalf("cannot decode %s", path)
		}
		defer cfRelease(img)
		w, h = cgImgGetWidth(img), cgImgGetHeight(img)
		px = pixelsOf(t, img, w, h)
	})
	return px, w, h
}

// renderFixture paints a w×h bitmap, runs draw against its context and
// returns the CGImage (caller CFReleases). The context's buffer is
// context-owned (data=nil): CGBitmapContextCreateImage references it, so
// a Go-slice buffer would dangle once the GC moves on.
func renderFixture(t *testing.T, w, h int, draw func(ctx uintptr)) uintptr {
	t.Helper()
	space := cgColorSpaceDeviceRGB()
	if space == 0 {
		t.Fatal("renderFixture: no colorspace")
	}
	defer cfRelease(space)
	ctx := cgBitmapCreate(nil, w, h, 8, 0, space, uint32(1|4<<12))
	if ctx == 0 {
		t.Fatal("renderFixture: no bitmap context")
	}
	defer cfRelease(ctx)
	// opaque white background
	cgCtxSetFill(ctx, 1, 1, 1, 1)
	cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: float64(w), Height: float64(h)}})
	draw(ctx)
	img := cgBitmapCreateImage(ctx)
	if img == 0 {
		t.Fatal("renderFixture: no image")
	}
	return img
}

// writeEncoded encodes img to path with the given UTI ("public.jpeg" …)
// and optional EXIF orientation (0 = none).
func writeEncoded(t *testing.T, path string, img uintptr, uti string, orientation int) {
	t.Helper()
	withPool(func() {
		var utiID id
		switch uti {
		case "public.jpeg":
			utiID = utiJPEG
		case "public.png":
			utiID = utiPNG
		case "public.heic":
			utiID = utiHEIC
		default:
			t.Fatalf("writeEncoded: unknown uti %s", uti)
		}
		dst := imgDstCreateWithURL(uintptr(cfURL(path)), utiID, 1, 0)
		if dst == 0 {
			t.Fatalf("writeEncoded: cannot create destination for %s", path)
		}
		defer cfRelease(dst)
		var props id
		if orientation != 0 {
			props = newDict(kImgPropOrientation, nsInt(orientation))
		}
		imgDstAddImage(dst, img, uintptr(props))
		if !imgDstFinalize(dst) {
			t.Fatalf("writeEncoded: finalize failed for %s", path)
		}
	})
}

// orientationOf reports the file's stored EXIF orientation (0 if absent).
func orientationOf(t *testing.T, path string) int {
	t.Helper()
	var o int
	withPool(func() {
		isrc, err := imageSource(path)
		if err != nil {
			t.Fatal(err)
		}
		defer cfRelease(isrc)
		props := sourceProps(isrc)
		if props == 0 {
			return
		}
		defer cfRelease(props)
		o = numInt(dictGet(props, kImgPropOrientation))
	})
	return o
}

// textImage renders text lines with CoreText into a PNG: white
// background, dark text, each entry {x,y,string} with y measured up from
// the bottom (Quartz coordinates), so larger y is the upper line.
func textImage(t *testing.T, path string, w, h int, fontSize float64, lines [][3]any) {
	t.Helper()
	img := renderFixture(t, w, h, func(ctx uintptr) {
		cfName := cfStr("Helvetica")
		font := ctFontCreate(cfName, fontSize, 0)
		cfRelease(cfName)
		if font == 0 {
			t.Fatal("textImage: no font")
		}
		defer cfRelease(font)
		cgCtxSetFill(ctx, 0, 0, 0, 1)
		for _, ln := range lines {
			x, y := ln[0].(float64), ln[1].(float64)
			s := ln[2].(string)
			cfs := cfStr(s)
			attrs := newDict(kCTFontAttr, id(font))
			as := cfAttrStrCreate(0, cfs, uintptr(attrs))
			cfRelease(cfs)
			if as == 0 {
				t.Fatal("textImage: no attributed string")
			}
			line := ctLineCreate(as)
			cfRelease(as)
			if line == 0 {
				t.Fatal("textImage: no line")
			}
			cgCtxSetTextPos(ctx, x, y)
			ctLineDraw(line, ctx)
			cfRelease(line)
		}
	})
	defer cfRelease(img)
	writeEncoded(t, path, img, "public.png", 0)
}

// nearRGBA reports whether the channel values at px[i:] are within tol of
// the reference colour (JPEG/HEIC need slack; PNG is exact with tol 0).
func nearRGBA(px []byte, i int, r, g, b byte, tol int) bool {
	if i < 0 || i+3 >= len(px) {
		return false
	}
	d := func(a, b byte) int {
		d := int(a) - int(b)
		if d < 0 {
			d = -d
		}
		return d
	}
	return d(px[i], r) <= tol && d(px[i+1], g) <= tol && d(px[i+2], b) <= tol
}

func TestConvertImageFormats(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.png")
	// 16×8, red top half / blue bottom half.
	img := renderFixture(t, 16, 8, func(ctx uintptr) {
		cgCtxSetFill(ctx, 1, 0, 0, 1)
		cgCtxFillRect(ctx, NSRect{Origin: NSPoint{Y: 4}, Size: NSSize{Width: 16, Height: 4}})
		cgCtxSetFill(ctx, 0, 0, 1, 1)
		cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: 16, Height: 4}})
	})
	defer cfRelease(img)
	writeEncoded(t, src, img, "public.png", 0)

	for _, tc := range []struct {
		name string
		fmt  ImageFormat
		head []byte
		tol  int
	}{
		{"jpeg", FormatJPEG, []byte{0xFF, 0xD8}, 32},
		{"png", FormatPNG, []byte{0x89, 0x50, 0x4E, 0x47}, 0},
		{"heic", FormatHEIC, []byte("ftyp"), 48},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(dir, "out."+tc.name)
			if err := ConvertImage(src, out, tc.fmt); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "heic" {
				if len(data) < 12 || !bytes.Contains(data[4:12], tc.head) {
					t.Fatalf("heic output lacks ftyp box: % x", data[:16])
				}
			} else if !bytes.HasPrefix(data, tc.head) {
				t.Fatalf("bad magic: % x", data[:8])
			}
			px, w, h := decodePixels(t, out)
			if w != 16 || h != 8 {
				t.Fatalf("size = %dx%d, want 16x8", w, h)
			}
			if !nearRGBA(px, 0, 255, 0, 0, tc.tol) {
				t.Fatalf("top-left = % x, want red", px[:4])
			}
			last := (h - 1) * w * 4
			if !nearRGBA(px, last, 0, 0, 255, tc.tol) {
				t.Fatalf("bottom-left = % x, want blue", px[last:last+4])
			}
		})
	}
}

// TestConvertImageBakesOrientation is the contract's rotated fixture: a
// JPEG stored with EXIF orientation 6 must come out with upright pixels
// (swapped dimensions) and no orientation flag left, since Word embeds
// the raw bitmap.
func TestConvertImageBakesOrientation(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "rot6.jpg")
	// Stored 40×20: red top half, blue bottom half. Orientation 6
	// (displayed = stored rotated 90° CW) makes the visual image 20×40:
	// blue left half, red right half. The image is large enough that
	// JPEG chroma subsampling stays away from the sampled corners.
	img := renderFixture(t, 40, 20, func(ctx uintptr) {
		cgCtxSetFill(ctx, 1, 0, 0, 1)
		cgCtxFillRect(ctx, NSRect{Origin: NSPoint{Y: 10}, Size: NSSize{Width: 40, Height: 10}})
		cgCtxSetFill(ctx, 0, 0, 1, 1)
		cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: 40, Height: 10}})
	})
	defer cfRelease(img)
	writeEncoded(t, src, img, "public.jpeg", 6)
	if o := orientationOf(t, src); o != 6 {
		t.Fatalf("fixture orientation = %d, want 6", o)
	}
	if sw, sh, err := ImageSize(src); err != nil || sw != 20 || sh != 40 {
		t.Fatalf("ImageSize(rot6) = %dx%d, %v; want 20x40", sw, sh, err)
	}

	out := filepath.Join(dir, "upright.png")
	if err := ConvertImage(src, out, FormatPNG); err != nil {
		t.Fatal(err)
	}
	if o := orientationOf(t, out); o > 1 {
		t.Fatalf("output still carries orientation %d", o)
	}
	px, w, h := decodePixels(t, out)
	if w != 20 || h != 40 {
		t.Fatalf("output size = %dx%d, want 20x40 (upright swap)", w, h)
	}
	// Dominance checks tolerate JPEG chroma smearing: the upright
	// image's left half must be blue-dominant, right half red-dominant.
	blueish := func(i int) bool { return px[i+2] > 120 && px[i+2] > px[i]+40 }
	redish := func(i int) bool { return px[i] > 120 && px[i] > px[i+2]+40 }
	if !blueish(0) {
		t.Fatalf("upright top-left = % x, want blue-ish", px[:4])
	}
	if !redish((w - 1) * 4) {
		t.Fatalf("upright top-right = % x, want red-ish", px[(w-1)*4:])
	}
	if !blueish((h - 1) * w * 4) {
		t.Fatalf("upright bottom-left = % x, want blue-ish", px[(h-1)*w*4:])
	}
	if !redish((h-1)*w*4 + (w-1)*4) {
		t.Fatalf("upright bottom-right = % x, want red-ish", px[(h-1)*w*4+(w-1)*4:])
	}
}

// TestDrawUprightFallback exercises the manual orientation path
// (orientationCTM + drawUpright) that runs when thumbnail creation is
// unavailable; it must agree with the contract for every orientation.
func TestDrawUprightFallback(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	// Stored 4×2: red top row, blue bottom row, drawn raw.
	img := renderFixture(t, 4, 2, func(ctx uintptr) {
		cgCtxSetFill(ctx, 1, 0, 0, 1)
		cgCtxFillRect(ctx, NSRect{Origin: NSPoint{Y: 1}, Size: NSSize{Width: 4, Height: 1}})
		cgCtxSetFill(ctx, 0, 0, 1, 1)
		cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: 4, Height: 1}})
	})
	defer cfRelease(img)

	// wantTL/wantTR are the expected colours at the upright image's
	// top-left and top-right corners.
	for _, tc := range []struct {
		o              int
		dw, dh         int
		wantTL, wantTR [3]byte
	}{
		{1, 4, 2, [3]byte{255, 0, 0}, [3]byte{255, 0, 0}},
		{2, 4, 2, [3]byte{255, 0, 0}, [3]byte{255, 0, 0}}, // uniform rows: mirrors invisible
		{3, 4, 2, [3]byte{0, 0, 255}, [3]byte{0, 0, 255}}, // 180°: blue on top
		{4, 4, 2, [3]byte{0, 0, 255}, [3]byte{0, 0, 255}}, // vertical mirror: blue on top
		{5, 2, 4, [3]byte{255, 0, 0}, [3]byte{0, 0, 255}}, // main-diagonal transpose: red left, blue right
		{6, 2, 4, [3]byte{0, 0, 255}, [3]byte{255, 0, 0}}, // CW: blue left, red right
		{7, 2, 4, [3]byte{0, 0, 255}, [3]byte{255, 0, 0}}, // anti-diagonal transpose: blue left, red right
		{8, 2, 4, [3]byte{255, 0, 0}, [3]byte{0, 0, 255}}, // CCW: red left, blue right
	} {
		t.Run(fmt.Sprintf("o%d", tc.o), func(t *testing.T) {
			out := drawUpright(img, tc.o)
			if out == 0 {
				t.Fatal("drawUpright returned 0")
			}
			defer cfRelease(out)
			w, h := cgImgGetWidth(out), cgImgGetHeight(out)
			if w != tc.dw || h != tc.dh {
				t.Fatalf("size = %dx%d, want %dx%d", w, h, tc.dw, tc.dh)
			}
			px := pixelsOf(t, out, w, h)
			if !nearRGBA(px, 0, tc.wantTL[0], tc.wantTL[1], tc.wantTL[2], 0) {
				t.Fatalf("TL = % x, want %v", px[:4], tc.wantTL)
			}
			if !nearRGBA(px, (w-1)*4, tc.wantTR[0], tc.wantTR[1], tc.wantTR[2], 0) {
				t.Fatalf("TR = % x, want %v", px[(w-1)*4:], tc.wantTR)
			}
		})
	}
}

func TestImagesToPDF(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var srcs []string
	for i, wh := range [][2]int{{12, 8}, {6, 14}} {
		p := filepath.Join(dir, fmt.Sprintf("p%d.png", i))
		img := renderFixture(t, wh[0], wh[1], func(ctx uintptr) {})
		defer cfRelease(img)
		writeEncoded(t, p, img, "public.png", 0)
		srcs = append(srcs, p)
	}
	out := filepath.Join(dir, "out.pdf")
	if err := ImagesToPDF(srcs, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Fatalf("not a PDF: % x", data[:8])
	}
	// Completeness: the file must be a fully-formed PDF the moment
	// ImagesToPDF returns — xref/trailer written before the rename.
	if !bytes.HasSuffix(bytes.TrimSpace(data), []byte("%%EOF")) {
		t.Fatal("PDF lacks EOF trailer (context closed after rename?)")
	}
	withPool(func() {
		doc := pdfDocCreateWithURL(uintptr(cfURL(out)))
		if doc == 0 {
			t.Fatal("CGPDFDocument cannot open output")
		}
		defer cfRelease(doc)
		if n := pdfDocGetNumPages(doc); n != 2 {
			t.Fatalf("pages = %d, want 2", n)
		}
		// kCGPDFMediaBox == 0. Pages are 1-based; default 72 DPI makes
		// page points equal pixel counts.
		want := [][2]float64{{12, 8}, {6, 14}}
		for i := range 2 {
			r := pdfPageGetBoxRect(uintptr(pdfDocGetPage(doc, i+1)), 0)
			if math.Abs(r.Size.Width-want[i][0]) > 0.5 || math.Abs(r.Size.Height-want[i][1]) > 0.5 {
				t.Fatalf("page %d box = %v, want %v", i+1, r.Size, want[i])
			}
		}
	})
}

func TestRecognizeText(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "text.png")
	// Larger y draws higher in Quartz space; line 1 is the upper row.
	textImage(t, src, 900, 300, 48, [][3]any{
		{40.0, 200.0, "Hello World"},
		{40.0, 80.0, "识别中文文本"},
	})
	lines, err := RecognizeText(src)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.ReplaceAll(strings.Join(lines, ""), " ", "")
	if !strings.Contains(joined, "HelloWorld") {
		t.Fatalf("missing English line in %q", lines)
	}
	if !strings.Contains(joined, "识别中文文本") {
		t.Fatalf("missing Chinese line in %q", lines)
	}
	// English (upper) must sort before Chinese (lower).
	if i, j := strings.Index(joined, "HelloWorld"), strings.Index(joined, "识别"); i < 0 || j < 0 || i > j {
		t.Fatalf("reading order wrong: %q", lines)
	}
}

func TestRecognizeTextNoText(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "blank.png")
	img := renderFixture(t, 32, 32, func(ctx uintptr) {})
	defer cfRelease(img)
	writeEncoded(t, src, img, "public.png", 0)
	lines, err := RecognizeText(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("lines = %q, want none", lines)
	}
}

func TestConvertImageMissingSource(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	err := ConvertImage(filepath.Join(dir, "nope.png"), filepath.Join(dir, "out.png"), FormatPNG)
	if err == nil {
		t.Fatal("expected error for missing source")
	}
	if !strings.HasPrefix(err.Error(), "mac: ") {
		t.Fatalf("error lacks mac: prefix: %v", err)
	}
	if _, _, err := ImageSize(filepath.Join(dir, "nope.png")); err == nil {
		t.Fatal("expected ImageSize error for missing source")
	}
	if _, err := RecognizeText(filepath.Join(dir, "nope.png")); err == nil {
		t.Fatal("expected RecognizeText error for missing source")
	}
	if err := ImagesToPDF([]string{filepath.Join(dir, "nope.png")}, filepath.Join(dir, "o.pdf")); err == nil {
		t.Fatal("expected ImagesToPDF error for missing source")
	}
}

// --- PDF reading/rendering ----------------------------------------------

// drawTextLine renders one line of text at PDF/Quartz coordinates (x, y)
// — the baseline sits y points up from the page bottom.
func drawTextLine(t *testing.T, ctx uintptr, font uintptr, x, y float64, s string) {
	t.Helper()
	cfs := cfStr(s)
	attrs := newDict(kCTFontAttr, id(font))
	as := cfAttrStrCreate(0, cfs, uintptr(attrs))
	cfRelease(cfs)
	if as == 0 {
		t.Fatal("drawTextLine: no attributed string")
	}
	line := ctLineCreate(as)
	cfRelease(as)
	if line == 0 {
		t.Fatal("drawTextLine: no line")
	}
	defer cfRelease(line)
	cgCtxSetTextPos(ctx, x, y)
	ctLineDraw(line, ctx)
}

// makeTestPDF builds a PDF with one page per spec at path: each spec is
// {w, h, draw}. The drawing runs in the page's point coordinate space.
func makeTestPDF(t *testing.T, path string, pages ...struct {
	w, h float64
	draw func(ctx uintptr)
}) {
	t.Helper()
	withPool(func() {
		ctx := cgPDFCtxCreateWithURL(uintptr(cfURL(path)), nil, 0)
		if ctx == 0 {
			t.Fatalf("makeTestPDF: cannot create %s", path)
		}
		// Close writes the xref before release; one defer so the order
		// is explicit (two defers would release first, LIFO).
		defer func() {
			cgPDFCtxClose(ctx)
			cfRelease(ctx)
		}()
		for _, pg := range pages {
			box := NSRect{Size: NSSize{Width: pg.w, Height: pg.h}}
			boxData := cfDataCreate(0, unsafe.Pointer(&box), int(unsafe.Sizeof(box)))
			if boxData == 0 {
				t.Fatal("makeTestPDF: no box data")
			}
			info := newDict(kPDFCtxMediaBox, id(boxData))
			cgCtxBeginPDFPage(ctx, uintptr(info))
			pg.draw(ctx)
			cgCtxEndPDFPage(ctx)
			cfRelease(boxData)
		}
	})
}

func TestPDFPageCountAndText(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.pdf")

	cfName := cfStr("Helvetica")
	font := ctFontCreate(cfName, 28, 0)
	cfRelease(cfName)
	if font == 0 {
		t.Fatal("no font")
	}
	defer cfRelease(font)

	// Page 1: two text lines; page 2: text plus a red bar; page 3: only a
	// filled rectangle (no text layer).
	makeTestPDF(t, src,
		struct {
			w, h float64
			draw func(ctx uintptr)
		}{600, 300, func(ctx uintptr) {
			cgCtxSetFill(ctx, 0, 0, 0, 1)
			drawTextLine(t, ctx, font, 40, 200, "Hello Page")
			drawTextLine(t, ctx, font, 40, 120, "中文页面")
		}},
		struct {
			w, h float64
			draw func(ctx uintptr)
		}{400, 200, func(ctx uintptr) {
			cgCtxSetFill(ctx, 1, 0, 0, 1)
			cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: 120, Height: 80}})
			cgCtxSetFill(ctx, 0, 0, 0, 1)
			drawTextLine(t, ctx, font, 40, 100, "Second")
		}},
		struct {
			w, h float64
			draw func(ctx uintptr)
		}{200, 200, func(ctx uintptr) {
			cgCtxSetFill(ctx, 0, 1, 0, 1)
			cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: 200, Height: 200}})
		}},
	)

	n, err := PDFPageCount(src)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("PDFPageCount = %d, want 3", n)
	}

	pages, err := PDFText(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 {
		t.Fatalf("PDFText returned %d pages, want 3", len(pages))
	}
	joined := func(ls []string) string {
		return strings.ReplaceAll(strings.Join(ls, "\n"), " ", "")
	}
	p1 := joined(pages[0])
	if !strings.Contains(p1, "HelloPage") || !strings.Contains(p1, "中文页面") {
		t.Fatalf("page 1 lines = %q", pages[0])
	}
	p2 := joined(pages[1])
	if !strings.Contains(p2, "Second") {
		t.Fatalf("page 2 lines = %q", pages[1])
	}
	if pages[2] != nil {
		t.Fatalf("page 3 should have no text layer, got %q", pages[2])
	}
}

func TestNormalizeText(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	// U+2F2F KANGXI RADICAL LABOR must compose to U+5DE5 工.
	if got := NormalizeText("⼯作"); got != "工作" {
		t.Fatalf("NormalizeText = %q, want %q", got, "工作")
	}
	// Latin is untouched; empty stays empty.
	if got := NormalizeText("plain ASCII"); got != "plain ASCII" {
		t.Fatalf("NormalizeText mangled ASCII: %q", got)
	}
	if got := NormalizeText(""); got != "" {
		t.Fatalf("NormalizeText(\"\") = %q", got)
	}
}

// TestPDFPageLines exercises the splitting contract directly: trailing
// spaces trimmed, leading/trailing empty lines dropped, inner empty lines
// kept as "".
func TestPDFPageLines(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"hello", []string{"hello"}},
		{"a\nb", []string{"a", "b"}},
		{"a\n\nb", []string{"a", "", "b"}},
		{"\n\na\n\n\n", []string{"a"}},
		{"a  \nb", []string{"a", "b"}},
		{"a\r\nb\rc", []string{"a", "b", "c"}},
		{"a\vb\fc\u2028d", []string{"a", "b", "c", "d"}},
		{"\u0085a", []string{"a"}},   // NEL is a break; empty first line is edge-dropped
		{"\u0085 a", []string{" a"}}, // leading space is content (only trailing trimmed)
	} {
		if got := pdfLines(tc.raw); !slices.Equal(got, tc.want) {
			t.Fatalf("pdfLines(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestRenderPDFPage(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "r.pdf")
	// Page is 300×200 pt; a red rect occupies the bottom-left quarter in
	// PDF space (y up), which renders to the image's bottom-left.
	makeTestPDF(t, src, struct {
		w, h float64
		draw func(ctx uintptr)
	}{300, 200, func(ctx uintptr) {
		cgCtxSetFill(ctx, 1, 1, 1, 1)
		cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: 300, Height: 200}})
		cgCtxSetFill(ctx, 1, 0, 0, 1)
		cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: 150, Height: 100}})
	}})

	out := filepath.Join(dir, "page.png")
	if err := RenderPDFPage(src, 1, out, FormatPNG); err != nil {
		t.Fatal(err)
	}
	px, w, h := decodePixels(t, out)
	if w != 600 || h != 400 {
		t.Fatalf("rendered size = %dx%d, want 600x400 (2x points)", w, h)
	}
	// Bottom-left quadrant is red; every other corner is white.
	bl := (h - 1) * w * 4 // pixel row h-1 = bottom-left
	if !nearRGBA(px, bl, 255, 0, 0, 0) {
		t.Fatalf("bottom-left = % x, want red", px[bl:])
	}
	if !nearRGBA(px, 0, 255, 255, 255, 0) {
		t.Fatalf("top-left = % x, want white", px[:4])
	}
	if !nearRGBA(px, (w-1)*4, 255, 255, 255, 0) {
		t.Fatalf("top-right = % x, want white", px[(w-1)*4:])
	}
	br := (h-1)*w*4 + (w-1)*4
	if !nearRGBA(px, br, 255, 255, 255, 0) {
		t.Fatalf("bottom-right = % x, want white", px[br:])
	}

	if err := RenderPDFPage(src, 2, filepath.Join(dir, "x.png"), FormatPNG); err == nil {
		t.Fatal("expected out-of-range page error")
	}
	if err := RenderPDFPage(src, 0, filepath.Join(dir, "x.png"), FormatPNG); err == nil {
		t.Fatal("expected page 0 error")
	}
}

func TestPDFMissingSource(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.pdf")
	if _, err := PDFPageCount(missing); err == nil {
		t.Fatal("expected PDFPageCount error for missing file")
	}
	if _, err := PDFText(missing); err == nil {
		t.Fatal("expected PDFText error for missing file")
	}
	if err := RenderPDFPage(missing, 1, filepath.Join(dir, "o.png"), FormatPNG); err == nil {
		t.Fatal("expected RenderPDFPage error for missing file")
	}
}

// writeRawPDF builds a one-page PDF with a valid xref table. pageExtra is
// spliced into the page dictionary (e.g. "/Rotate 90" or "/CropBox […]"),
// content is the page's drawing stream. Used because CGPDFContext offers
// no page-dictionary keys beyond the boxes.
func writeRawPDF(t *testing.T, path, pageExtra, content string) {
	t.Helper()
	var b bytes.Buffer
	var offs []int64
	obj := func(body string) {
		offs = append(offs, int64(b.Len()))
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	b.WriteString("%PDF-1.4\n")
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] " +
		pageExtra + " /Resources << >> /Contents 4 0 R >>")
	obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	xref := int64(b.Len())
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(offs)+1, xref)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRenderPDFPageRotated renders a page carrying /Rotate 90: the output
// must swap dimensions and the coloured corner must land rotated.
func TestRenderPDFPageRotated(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "rot.pdf")
	// Page space 300×200; a red 150×100 rect at the page's bottom-left.
	// Rotate 90 (clockwise) shows that corner at the top-left of a
	// 200×300 image.
	content := "1 1 1 rg 0 0 300 200 re f 1 0 0 rg 0 0 150 100 re f"
	writeRawPDF(t, src, "/Rotate 90", content)

	out := filepath.Join(dir, "rot.png")
	if err := RenderPDFPage(src, 1, out, FormatPNG); err != nil {
		t.Fatal(err)
	}
	px, w, h := decodePixels(t, out)
	if w != 400 || h != 600 {
		t.Fatalf("rendered size = %dx%d, want 400x600 (swapped)", w, h)
	}
	if !nearRGBA(px, 0, 255, 0, 0, 0) {
		t.Fatalf("top-left = % x, want red", px[:4])
	}
	if !nearRGBA(px, (w-1)*4, 255, 255, 255, 0) {
		t.Fatalf("top-right = % x, want white", px[(w-1)*4:])
	}
	bl := (h - 1) * w * 4
	if !nearRGBA(px, bl, 255, 255, 255, 0) {
		t.Fatalf("bottom-left = % x, want white", px[bl:])
	}
}

// TestRenderPDFPageCropBox renders a page whose CropBox is smaller than
// its MediaBox: output must be the crop size and content outside the
// crop must not appear.
func TestRenderPDFPageCropBox(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "crop.pdf")
	// MediaBox 300×200 all red; CropBox [50 50 250 150] (200×100) is
	// filled white with a green 60×40 block at its bottom-left.
	content := "1 0 0 rg 0 0 300 200 re f " +
		"1 1 1 rg 50 50 200 100 re f " +
		"0 1 0 rg 50 50 60 40 re f"
	writeRawPDF(t, src, "/CropBox [50 50 250 150]", content)

	out := filepath.Join(dir, "crop.png")
	if err := RenderPDFPage(src, 1, out, FormatPNG); err != nil {
		t.Fatal(err)
	}
	px, w, h := decodePixels(t, out)
	if w != 400 || h != 200 {
		t.Fatalf("rendered size = %dx%d, want 400x200 (crop box x2)", w, h)
	}
	bl := (h - 1) * w * 4 // crop-bottom-left in image = last row, first px
	if !nearRGBA(px, bl, 0, 255, 0, 0) {
		t.Fatalf("crop bottom-left = % x, want green", px[bl:])
	}
	if !nearRGBA(px, 0, 255, 255, 255, 0) {
		t.Fatalf("top-left = % x, want white", px[:4])
	}
	tr := (w - 1) * 4
	if !nearRGBA(px, tr, 255, 255, 255, 0) {
		t.Fatalf("top-right = % x, want white (media-only red clipped)", px[tr:])
	}
}

// redOverBlue writes a w×h image (red top half, blue bottom half) to
// path as uti with the given EXIF orientation.
func redOverBlue(t *testing.T, path string, w, h int, uti string, orientation int) {
	t.Helper()
	img := renderFixture(t, w, h, func(ctx uintptr) {
		cgCtxSetFill(ctx, 1, 0, 0, 1)
		cgCtxFillRect(ctx, NSRect{Origin: NSPoint{Y: float64(h / 2)}, Size: NSSize{Width: float64(w), Height: float64(h / 2)}})
		cgCtxSetFill(ctx, 0, 0, 1, 1)
		cgCtxFillRect(ctx, NSRect{Size: NSSize{Width: float64(w), Height: float64(h / 2)}})
	})
	defer cfRelease(img)
	writeEncoded(t, path, img, uti, orientation)
}

// TestThumbnailReadsHEICUpright: Go cannot decode HEIC, so the tray's
// thumbnails for it come from ImageIO — scaled to the cap and upright.
func TestThumbnailReadsHEICUpright(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "rot6.heic")
	// Stored 400×200 red over blue; orientation 6 shows it 200×400 with
	// blue on the left.
	redOverBlue(t, src, 400, 200, "public.heic", 6)
	thumb, err := Thumbnail(src, 80)
	if err != nil {
		t.Fatal(err)
	}
	if b := thumb.Bounds(); b.Dx() != 40 || b.Dy() != 80 {
		t.Fatalf("thumbnail = %v, want 40×80 (upright, longer side 80)", b)
	}
	left, right := thumb.RGBAAt(5, 40), thumb.RGBAAt(34, 40)
	if left.B < 150 || left.R > 100 || right.R < 150 || right.B > 100 {
		t.Fatalf("thumbnail not upright: left %v right %v", left, right)
	}
}

// TestResizeImageCapsTheLongerSideNeverEnlarges covers both sides of
// the cap: a large picture shrinks to it, upright, and a small one keeps
// its own size.
func TestResizeImageCapsTheLongerSideNeverEnlarges(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	big := filepath.Join(dir, "big.jpg")
	redOverBlue(t, big, 1200, 600, "public.jpeg", 6)
	out := filepath.Join(dir, "small.jpg")
	if err := ResizeImage(big, out, FormatJPEG, 300); err != nil {
		t.Fatal(err)
	}
	if o := orientationOf(t, out); o > 1 {
		t.Fatalf("output still carries orientation %d", o)
	}
	px, w, h := decodePixels(t, out)
	if w != 150 || h != 300 {
		t.Fatalf("resized = %dx%d, want 150x300", w, h)
	}
	if mid := (h/2*w + 5) * 4; px[mid+2] < 150 || px[mid] > 100 {
		t.Fatalf("resized left edge = % x, want blue (upright)", px[mid:mid+4])
	}

	tiny := filepath.Join(dir, "tiny.png")
	redOverBlue(t, tiny, 64, 32, "public.png", 0)
	same := filepath.Join(dir, "same.png")
	if err := ResizeImage(tiny, same, FormatPNG, 300); err != nil {
		t.Fatal(err)
	}
	if _, w, h := decodePixels(t, same); w != 64 || h != 32 {
		t.Fatalf("small image became %dx%d, want its own 64x32", w, h)
	}
}

// TestRecognizeTextCapsTheDecode: a bitmap wider than recognizeMaxSide is
// downscaled before Vision sees it, and a 5K-wide image stays native —
// the cap exists so an extreme screenshot cannot pin a few hundred MB of
// RGBA inside an uninterruptible decode.
func TestRecognizeTextCapsTheDecode(t *testing.T) {
	if err := loadTest(); err != nil {
		t.Skip(err)
	}
	big := filepath.Join(t.TempDir(), "big.png")
	// 8000×400 solid is cheap to encode and far over the cap.
	textImage(t, big, 8000, 400, 40, [][3]any{{40.0, 200.0, "CAP"}})
	withPool(func() {
		isrc, err := imageSource(big)
		if err != nil {
			t.Fatal(err)
		}
		defer cfRelease(isrc)
		img := uprightImageCapped(isrc, recognizeMaxSide)
		if img == 0 {
			t.Fatal("capped decode failed")
		}
		defer cfRelease(img)
		if w, h := cgImgGetWidth(img), cgImgGetHeight(img); w != recognizeMaxSide || h != 307 {
			t.Fatalf("over-cap decode = %dx%d, want %dx%d", w, h, recognizeMaxSide, 307)
		}
	})
	// A 5K-class shot (under the cap) decodes at native size.
	fiveK := filepath.Join(t.TempDir(), "5k.png")
	textImage(t, fiveK, 5120, 288, 24, [][3]any{{20.0, 140.0, "NATIVE"}})
	withPool(func() {
		isrc, err := imageSource(fiveK)
		if err != nil {
			t.Fatal(err)
		}
		defer cfRelease(isrc)
		img := uprightImageCapped(isrc, recognizeMaxSide)
		if img == 0 {
			t.Fatal("5K decode failed")
		}
		defer cfRelease(img)
		if w, h := cgImgGetWidth(img), cgImgGetHeight(img); w != 5120 || h != 288 {
			t.Fatalf("under-cap decode = %dx%d, want 5120x288", w, h)
		}
	})
	// The scaled fallback path (thumbnail decode declined) draws the same
	// capped geometry: rotation swaps axes before the scale applies.
	small := filepath.Join(t.TempDir(), "small.png")
	textImage(t, small, 400, 200, 24, [][3]any{{20.0, 100.0, "x"}})
	withPool(func() {
		isrc, err := imageSource(small)
		if err != nil {
			t.Fatal(err)
		}
		defer cfRelease(isrc)
		img := imgSrcCreateImageAt(isrc, 0, 0)
		if img == 0 {
			t.Fatal("decode failed")
		}
		defer cfRelease(img)
		out := drawUprightScaled(img, 6, 0.5)
		if out == 0 {
			t.Fatal("scaled upright draw failed")
		}
		defer cfRelease(out)
		if w, h := cgImgGetWidth(out), cgImgGetHeight(out); w != 100 || h != 200 {
			t.Fatalf("rotated+scaled draw = %dx%d, want 100x200", w, h)
		}
	})
}
