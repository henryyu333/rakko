//go:build windows

package platform

// The image, OCR and capture paths against the real system APIs: a
// generated picture makes every step observable without a desktop session
// (the camera-free parts), and the PDF export is read back through both the
// PDF reader and Windows.Data.Pdf.

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// testImage builds a w×h picture with two distinct halves, so a wrong
// rotation or flip shows up as a wrong pixel.
func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			if x < w/2 {
				img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	return img
}

// writePNG saves img as a PNG in the test's temporary directory.
func writePNG(t *testing.T, dir, name string, img *image.RGBA) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestUprightAppliesEveryOrientation(t *testing.T) {
	// 4×2: the left half red, the right half blue; read back where each
	// corner lands for every EXIF orientation.
	src := testImage(4, 2)
	cases := []struct {
		o    int
		w, h int
		at   image.Point // a destination pixel …
		want color.RGBA  // … and the source colour that must be there
	}{
		{1, 4, 2, image.Point{0, 0}, color.RGBA{R: 255, A: 255}},
		{2, 4, 2, image.Point{3, 0}, color.RGBA{R: 255, A: 255}},
		{3, 4, 2, image.Point{3, 1}, color.RGBA{R: 255, A: 255}},
		{4, 4, 2, image.Point{0, 1}, color.RGBA{R: 255, A: 255}},
		{5, 2, 4, image.Point{0, 0}, color.RGBA{R: 255, A: 255}},
		{6, 2, 4, image.Point{1, 0}, color.RGBA{R: 255, A: 255}},
		{7, 2, 4, image.Point{1, 3}, color.RGBA{R: 255, A: 255}},
		{8, 2, 4, image.Point{0, 3}, color.RGBA{R: 255, A: 255}},
	}
	for _, c := range cases {
		got := upright(src, c.o)
		if got.Rect.Dx() != c.w || got.Rect.Dy() != c.h {
			t.Errorf("orientation %d: size %dx%d, want %dx%d", c.o, got.Rect.Dx(), got.Rect.Dy(), c.w, c.h)
			continue
		}
		if px := got.RGBAAt(c.at.X, c.at.Y); px != c.want {
			t.Errorf("orientation %d: pixel %v = %v, want %v", c.o, c.at, px, c.want)
		}
	}
	// Orientation 1 is a no-op and must not copy the pixels.
	if upright(src, 1) != src || upright(src, 0) != src {
		t.Error("orientation 1/0 should return the source unchanged")
	}
}

// exifJPEG encodes img and splices in an APP1 EXIF block carrying
// orientation o, which is what a camera writes.
func exifJPEG(t *testing.T, img *image.RGBA, o uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	data := buf.Bytes()
	// TIFF header (little-endian) + one IFD entry (0x0112 SHORT o).
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 0x01, 3, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	tiff[18] = byte(o)
	body := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1, byte((len(body) + 2) >> 8), byte(len(body) + 2)}
	app1 = append(app1, body...)
	out := append([]byte{0xFF, 0xD8}, app1...)
	out = append(out, data[2:]...)
	return out
}

func TestExifOrientationReadsTheTag(t *testing.T) {
	for _, o := range []uint16{1, 3, 6, 8} {
		data := exifJPEG(t, testImage(8, 4), o)
		if got := exifOrientation(data); got != int(o) {
			t.Errorf("orientation tag %d read as %d", o, got)
		}
	}
	if got := exifOrientation([]byte{0xFF, 0xD8, 0xFF, 0xD9}); got != 1 {
		t.Errorf("jpeg without EXIF read as %d, want 1", got)
	}
	if got := exifOrientation([]byte("not a jpeg")); got != 1 {
		t.Errorf("garbage read as %d, want 1", got)
	}
}

// TestConvertImageBakesOrientation walks the whole path: a JPEG tagged as
// orientation 6 decodes rotated, and the PNG it converts to keeps the
// upright size.
func TestConvertImageBakesOrientation(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "rotated.jpg")
	if err := os.WriteFile(src, exifJPEG(t, testImage(8, 4), 6), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "upright.png")
	if err := ConvertImage(src, dst, FormatPNG); err != nil {
		t.Fatalf("ConvertImage: %v", err)
	}
	if w, h, err := ImageSize(dst); err != nil || w != 4 || h != 8 {
		t.Fatalf("converted size %dx%d (err %v), want 4x8", w, h, err)
	}
	if w, h, err := ImageSize(src); err != nil || w != 4 || h != 8 {
		t.Fatalf("source upright size %dx%d (err %v), want 4x8", w, h, err)
	}
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatalf("output is not a PNG: %v", err)
	}
	if cfg.Width != 4 || cfg.Height != 8 {
		t.Errorf("output pixels %dx%d, want 4x8", cfg.Width, cfg.Height)
	}
}

func TestResizeAndThumbnailCapTheLongerSide(t *testing.T) {
	dir := t.TempDir()
	src := writePNG(t, dir, "big.png", testImage(400, 200))
	dst := filepath.Join(dir, "small.jpg")
	if err := ResizeImage(src, dst, FormatJPEG, 100); err != nil {
		t.Fatalf("ResizeImage: %v", err)
	}
	w, h, err := ImageSize(dst)
	if err != nil {
		t.Fatal(err)
	}
	if w != 100 || h != 50 {
		t.Errorf("resized to %dx%d, want 100x50", w, h)
	}
	thumb, err := Thumbnail(src, 64)
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	if thumb.Rect.Dx() != 64 || thumb.Rect.Dy() != 32 {
		t.Errorf("thumbnail %dx%d, want 64x32", thumb.Rect.Dx(), thumb.Rect.Dy())
	}
	// A smaller image is never enlarged.
	small := writePNG(t, dir, "small.png", testImage(10, 10))
	thumb, err = Thumbnail(small, 64)
	if err != nil {
		t.Fatal(err)
	}
	if thumb.Rect.Dx() != 10 {
		t.Errorf("small thumbnail %dpx, want 10", thumb.Rect.Dx())
	}
}

func TestImageAlphaSeesTransparency(t *testing.T) {
	dir := t.TempDir()
	clear := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(clear.Pix); i += 4 {
		clear.Pix[i+3] = 0
	}
	path := writePNG(t, dir, "clear.png", clear)
	if alpha, err := ImageAlpha(path); err != nil || !alpha {
		t.Errorf("transparent PNG: alpha=%v err=%v, want true", alpha, err)
	}
	opaque := writePNG(t, dir, "opaque.png", testImage(4, 4))
	if alpha, err := ImageAlpha(opaque); err != nil || alpha {
		t.Errorf("opaque PNG: alpha=%v err=%v, want false", alpha, err)
	}
}

func TestFitImageStaysUnderTheBudget(t *testing.T) {
	dir := t.TempDir()
	// Noise defeats compression, so the ladder must actually walk down.
	noisy := image.NewRGBA(image.Rect(0, 0, 600, 400))
	seed := uint32(1)
	for i := 0; i < len(noisy.Pix); i += 4 {
		seed = seed*1664525 + 1013904223
		noisy.Pix[i] = byte(seed >> 24)
		noisy.Pix[i+1] = byte(seed >> 16)
		noisy.Pix[i+2] = byte(seed >> 8)
		noisy.Pix[i+3] = 0xFF
	}
	src := writePNG(t, dir, "noise.png", noisy)
	dst := filepath.Join(dir, "fit.jpg")
	const limit = 200_000
	over, err := FitImage(src, dst, FormatJPEG, limit)
	if err != nil {
		t.Fatalf("FitImage: %v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if over || fi.Size() > limit {
		t.Fatalf("output %d bytes for a %d byte budget (over=%v)", fi.Size(), limit, over)
	}
	if w, h, err := ImageSize(dst); err != nil || w < 1 || h < 1 {
		t.Errorf("fitted image unreadable: %dx%d %v", w, h, err)
	}
	// A budget nothing can reach still leaves the smallest attempt on disk
	// and says so, as the Mac build does.
	over, err = FitImage(src, dst, FormatJPEG, 1_000)
	if err != nil {
		t.Fatalf("FitImage(impossible): %v", err)
	}
	if !over {
		t.Error("an unreachable budget should report over")
	}
	if fi, err := os.Stat(dst); err != nil || fi.Size() == 0 {
		t.Errorf("impossible fit wrote no file: %v", err)
	}
}

func TestImagesToPDFAndBack(t *testing.T) {
	dir := t.TempDir()
	first := writePNG(t, dir, "a.png", testImage(120, 80))
	second := writePNG(t, dir, "b.png", testImage(60, 90))
	pdfPath := filepath.Join(dir, "out.pdf")
	if err := ImagesToPDF([]string{first, second}, pdfPath); err != nil {
		t.Fatalf("ImagesToPDF: %v", err)
	}
	n, err := PDFPageCount(pdfPath)
	if err != nil {
		t.Fatalf("PDFPageCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("PDF has %d pages, want 2", n)
	}
	pages, err := PDFText(pdfPath)
	if err != nil {
		t.Fatalf("PDFText: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("PDFText returned %d pages, want 2", len(pages))
	}
	for i, lines := range pages {
		if len(lines) != 0 {
			t.Errorf("page %d (a picture, no text layer) has text %q", i+1, lines)
		}
	}
	// The first page's media box is the picture's size in points, so the
	// render at twice the points is 240×160.
	out := filepath.Join(dir, "page1.png")
	if err := RenderPDFPage(pdfPath, 1, out, FormatPNG); err != nil {
		t.Fatalf("RenderPDFPage: %v", err)
	}
	w, h, err := ImageSize(out)
	if err != nil {
		t.Fatal(err)
	}
	if w != 240 || h != 160 {
		t.Errorf("page render %dx%d, want 240x160", w, h)
	}
	if err := RenderPDFPage(pdfPath, 3, out, FormatPNG); err == nil {
		t.Error("page 3 of a two-page PDF should fail")
	}
}

func TestPDFTextReadsTheTextLayer(t *testing.T) {
	// A minimal PDF whose one page draws "Hello" with a built-in font.
	body := "q BT /F1 24 Tf 72 720 Td (Hello) Tj ET Q"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length " + itoa(len(body)) + " >>\nstream\n" + body + "\nendstream",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = buf.Len()
		buf.WriteString(strings.Join([]string{itoa(i + 1), "0 obj", body, "endobj"}, "\n"))
		buf.WriteString("\n")
	}
	start := buf.Len()
	buf.WriteString("xref\n0 " + itoa(len(objs)+1) + "\n0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		buf.WriteString(pad10(offsets[i]) + " 00000 n \n")
	}
	buf.WriteString("trailer\n<< /Size " + itoa(len(objs)+1) + " /Root 1 0 R >>\nstartxref\n" + itoa(start) + "\n%%EOF\n")
	dir := t.TempDir()
	path := filepath.Join(dir, "text.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	pages, err := PDFText(path)
	if err != nil {
		t.Fatalf("PDFText: %v", err)
	}
	if len(pages) != 1 || len(pages[0]) == 0 || !strings.Contains(pages[0][0], "Hello") {
		t.Fatalf("PDFText returned %q, want one page containing Hello", pages)
	}
}

func TestPDFLinesTrimsEdgesKeepsInnerBlanks(t *testing.T) {
	got := pdfLines("\n\none  \r\ntwo\n\nthree\v\n\n")
	want := []string{"one", "two", "", "three"}
	if len(got) != len(want) {
		t.Fatalf("lines %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lines %q, want %q", got, want)
		}
	}
	if pdfLines("   \n\n") != nil {
		t.Error("a page of blanks should read as no text")
	}
}

// itoa and pad10 keep the fixture above readable.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func pad10(n int) string {
	s := itoa(n)
	return strings.Repeat("0", 10-len(s)) + s
}

// --- OCR ------------------------------------------------------------------

var (
	testCreateFont = sysProc(gdi32, "CreateFontW")
	testDrawText   = sysProc(user32, "DrawTextW")
	testTextColor  = sysProc(gdi32, "SetTextColor")
	testBkMode     = sysProc(gdi32, "SetBkMode")
)

// drawTextPNG renders text with GDI into a white bitmap and saves it as a
// PNG, giving the recognizer a real picture of real glyphs.
func drawTextPNG(t *testing.T, dir, text string, w, h, pointSize int) string {
	t.Helper()
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		t.Fatal("no screen device context")
	}
	defer procReleaseDC.Call(0, screen)
	dc, _, _ := procCreateCompatibleDC.Call(screen)
	if dc == 0 {
		t.Fatal("no device context")
	}
	defer procDeleteDC.Call(dc)
	info := bitmapInfo{header: bitmapInfoHeader{
		biWidth: int32(w), biHeight: -int32(h), biPlanes: 1, biBitCount: 32, biCompression: biRGB,
	}}
	info.header.biSize = uint32(unsafe.Sizeof(info.header))
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSect.Call(screen, uintptr(unsafe.Pointer(&info)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		t.Fatal("no bitmap")
	}
	defer procDeleteObject.Call(hbm)
	old, _, _ := procSelectObject.Call(dc, hbm)
	defer procSelectObject.Call(dc, old)
	pix := unsafe.Slice((*byte)(bits), w*h*4)
	for i := range pix {
		pix[i] = 0xFF // white paper
	}
	face, err := windows.UTF16PtrFromString("Microsoft YaHei")
	if err != nil {
		t.Fatal(err)
	}
	// Height, width, escapement, orientation, weight, italic, underline,
	// strikeout, charset, out precision, clip precision, quality, pitch.
	font, _, _ := testCreateFont.Call(uintptr(pointSize), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(face)))
	if font == 0 {
		t.Fatal("no font")
	}
	defer procDeleteObject.Call(font)
	procSelectObject.Call(dc, font)
	testBkMode.Call(dc, 2) // TRANSPARENT
	testTextColor.Call(dc, 0x000000)
	text16, err := windows.UTF16PtrFromString(text)
	if err != nil {
		t.Fatal(err)
	}
	type rect struct{ left, top, right, bottom int32 }
	r := rect{0, 0, int32(w), int32(h)}
	const dtCenter, dtVCenter, dtSingleLine = 0x1, 0x4, 0x20
	testDrawText.Call(dc, uintptr(unsafe.Pointer(text16)), ^uintptr(0), uintptr(unsafe.Pointer(&r)),
		dtCenter|dtVCenter|dtSingleLine)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, pix)
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+2] = img.Pix[i+2], img.Pix[i]
		img.Pix[i+3] = 0xFF
	}
	return writePNG(t, dir, "text.png", img)
}

func TestRecognizeTextReadsScreenText(t *testing.T) {
	dir := t.TempDir()
	path := drawTextPNG(t, dir, "Rakko 框选文字 123", 900, 200, 72)
	lines, err := RecognizeText(path)
	if err != nil {
		t.Fatalf("RecognizeText: %v", err)
	}
	joined := strings.Join(lines, "")
	// The engine's Latin word boxes can split a word ("Ra kko"); the
	// letters and the digits must still all be there, in order.
	for _, want := range []string{"Rakko", "123"} {
		if !strings.Contains(strings.ReplaceAll(joined, " ", ""), want) {
			t.Errorf("recognized text %q lacks %q", strings.Join(lines, "\n"), want)
		}
	}
	// Chinese characters arrive as single-character words; the line is
	// compacted into the run the user typed. That needs a Chinese OCR
	// language, which hosted CI runners lack.
	requireChineseOCR(t)
	if !strings.Contains(joined, "框选文字") {
		t.Errorf("recognized text %q does not read 框选文字", strings.Join(lines, "\n"))
	}
}

func TestRecognizeTextOnBlankPictureFindsNothing(t *testing.T) {
	dir := t.TempDir()
	path := drawTextPNG(t, dir, "", 300, 120, 40)
	lines, err := RecognizeText(path)
	if err != nil {
		t.Fatalf("RecognizeText: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("blank picture recognized as %q", lines)
	}
}

// TestCaptureDisplayWritesTheDisplay covers the freeze the screenshot
// overlay and the region picker start from: one PNG whose pixel size is the
// display's own.
func TestCaptureDisplayWritesTheDisplay(t *testing.T) {
	ms := enumMonitors()
	if len(ms) == 0 {
		t.Fatal("no display enumerated")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "display1.png")
	if err := CaptureDisplay(1, path); err != nil {
		if strings.Contains(err.Error(), "Access is denied") {
			// No interactive desktop (an SSH session): Windows refuses the
			// screen copy. The desktop run of this test covers the capture.
			t.Skip("no interactive desktop in this session")
		}
		t.Fatalf("CaptureDisplay: %v", err)
	}
	w, h, err := ImageSize(path)
	if err != nil {
		t.Fatalf("captured picture: %v", err)
	}
	if w != int(ms[0].bounds.width()) || h != int(ms[0].bounds.height()) {
		t.Errorf("captured %dx%d, display is %dx%d", w, h, ms[0].bounds.width(), ms[0].bounds.height())
	}
	if err := CaptureDisplay(len(ms)+1, path); err == nil {
		t.Error("a missing display should fail")
	}
}

func TestPDFTextNormalizesCompatibilityChars(t *testing.T) {
	// Map single-byte codes A, B, C via ToUnicode CMap to compatibility forms:
	// A -> U+FF21 (fullwidth 'Ａ'), B -> U+FB01 ('ﬁ'), C -> U+2F00 (CJK radical '⼀').
	cmap := `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
<00> <FF>
endcodespacerange
3 beginbfchar
<41> <FF21>
<42> <FB01>
<43> <2F00>
endbfchar
endcmap
CMapName currentdict /CMap defineresource pop
end
end`
	body := "q BT /F1 12 Tf 72 720 Td (ABC) Tj ET Q"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length " + itoa(len(body)) + " >>\nstream\n" + body + "\nendstream",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 6 0 R >>",
		"<< /Length " + itoa(len(cmap)) + " >>\nstream\n" + cmap + "\nendstream",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, objBody := range objs {
		offsets[i+1] = buf.Len()
		buf.WriteString(strings.Join([]string{itoa(i + 1), "0 obj", objBody, "endobj"}, "\n"))
		buf.WriteString("\n")
	}
	start := buf.Len()
	buf.WriteString("xref\n0 " + itoa(len(objs)+1) + "\n0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		buf.WriteString(pad10(offsets[i]) + " 00000 n \n")
	}
	buf.WriteString("trailer\n<< /Size " + itoa(len(objs)+1) + " /Root 1 0 R >>\nstartxref\n" + itoa(start) + "\n%%EOF\n")

	path := filepath.Join(t.TempDir(), "compat.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	pages, err := PDFText(path)
	if err != nil {
		t.Fatalf("PDFText: %v", err)
	}
	if len(pages) != 1 || len(pages[0]) != 1 || pages[0][0] != "Afi一" {
		t.Fatalf("PDFText = %q, want [[\"Afi一\"]]", pages)
	}
}
