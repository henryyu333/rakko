//go:build windows

package platform

// PDF text extraction and export. Text comes from the PDF's own text layer
// through the pure-Go parser (github.com/ledongthuc/pdf, imported only in
// this Windows file); the export writes one page per image, each page the
// image's pixel size in points with a JPEG drawn to fill it, which is what
// the Mac build's Core Graphics context produces.

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	stdjpeg "image/jpeg"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"

	"rakko/internal/atomicfile"
)

// PDFText returns the text of every page (index = page-1), each page split
// into lines: trailing spaces trimmed, empty lines at the page's start and
// end dropped, inner empty lines kept. A page with no text layer yields nil
// for that page.
func PDFText(src string) (pages [][]string, err error) {
	f, r, err := pdf.Open(src)
	if err != nil {
		return nil, fmt.Errorf("windows: cannot read PDF %s: %w", src, err)
	}
	defer f.Close()
	pages = make([][]string, r.NumPage())
	for i := range pages {
		var text string
		if err := pdfPageText(r, i+1, &text); err != nil {
			return nil, err
		}
		pages[i] = pdfLines(NormalizeText(text))
	}
	return pages, nil
}

// pdfPageText reads page num's (1-based, as the parser numbers pages)
// plain text, turning a parser panic (a malformed content stream) into an
// error rather than a crash.
func pdfPageText(r *pdf.Reader, num int, out *string) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("windows: cannot read PDF page %d: %v", num, v)
		}
	}()
	text, err := r.Page(num).GetPlainText(nil)
	if err != nil {
		return fmt.Errorf("windows: cannot read PDF page %d: %w", num, err)
	}
	*out = text
	return nil
}

// pdfImageQuality is the JPEG quality of an exported page's picture.
const pdfImageQuality = 90

// ImagesToPDF writes one page per source image, in order, to dst. Each
// page's media box equals the upright image's pixel size in points and the
// picture fills it, composited over white (a PDF page has no transparency).
// The write is atomic.
func ImagesToPDF(srcs []string, dst string) error {
	if len(srcs) == 0 {
		return fmt.Errorf("windows: no images for the PDF")
	}
	pages := make([]pdfPage, 0, len(srcs))
	for _, src := range srcs {
		var img *image.RGBA
		err := wiApartment(func() error {
			var e error
			img, e = decodeImage(src)
			return e
		})
		if err != nil {
			return err
		}
		img = flattenOnWhite(img)
		data, err := encodePDFJPEG(img)
		if err != nil {
			return err
		}
		pages = append(pages, pdfPage{w: img.Rect.Dx(), h: img.Rect.Dy(), jpeg: data})
	}
	return atomicfile.WriteFile(filepath.Dir(dst), filepath.Base(dst), pdfDocument(pages), 0o644)
}

// pdfPage is one page of an export: the picture's size in points and its
// JPEG bytes.
type pdfPage struct {
	w, h int
	jpeg []byte
}

// pdfDocument assembles the file: object 1 is the catalog, 2 the page
// tree, and every page owns three objects (page, image, content stream).
func pdfDocument(pages []pdfPage) []byte {
	objs := 2 + 3*len(pages)
	offsets := make([]int, objs+1)
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	writeObj := func(num int, body string, stream []byte) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n", num)
		buf.WriteString(body)
		if stream != nil {
			fmt.Fprintf(&buf, "\nstream\n")
			buf.Write(stream)
			buf.WriteString("\nendstream")
		}
		buf.WriteString("\nendobj\n")
	}
	var kids strings.Builder
	for i := range pages {
		if i > 0 {
			kids.WriteByte(' ')
		}
		fmt.Fprintf(&kids, "%d 0 R", 3+3*i)
	}
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	writeObj(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), len(pages)), nil)
	for i, p := range pages {
		pageNum := 3 + 3*i
		imgNum, contNum := pageNum+1, pageNum+2
		content := fmt.Sprintf("q %d 0 0 %d 0 0 cm /Im0 Do Q", p.w, p.h)
		writeObj(pageNum, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>",
			p.w, p.h, imgNum, contNum), nil)
		writeObj(imgNum, fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>",
			p.w, p.h, len(p.jpeg)), p.jpeg)
		writeObj(contNum, fmt.Sprintf("<< /Length %d >>", len(content)), []byte(content))
	}
	start := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", objs+1)
	buf.WriteString("0000000000 65535 f \n")
	for obj := 1; obj <= objs; obj++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[obj])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", objs+1, start)
	return buf.Bytes()
}

// flattenOnWhite composites img over white, so transparent regions read as
// paper rather than black.
func flattenOnWhite(img *image.RGBA) *image.RGBA {
	out := image.NewRGBA(img.Rect)
	draw.Draw(out, out.Bounds(), &image.Uniform{C: white}, image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), img, img.Rect.Min, draw.Over)
	return out
}

var white = colorWhite{}

type colorWhite struct{}

func (colorWhite) RGBA() (r, g, b, a uint32) { return 0xffff, 0xffff, 0xffff, 0xffff }

// encodePDFJPEG encodes img for embedding with the DCTDecode filter.
func encodePDFJPEG(img *image.RGBA) ([]byte, error) {
	var buf bytes.Buffer
	if err := stdjpeg.Encode(&buf, img, &stdjpeg.Options{Quality: pdfImageQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
