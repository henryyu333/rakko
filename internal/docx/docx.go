// Package docx writes minimal Office Open XML word-processing
// documents — a sequence of text paragraphs plus one optional inline
// picture — which ShelfU produces when exporting an image together with
// the text recognized in it. It is a small writer, not a docx toolkit,
// and uses only the standard library.
package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Picture is an embedded image. Data holds PNG or JPEG bytes and Ext
// the matching extension ("png", "jpeg" or "jpg"); W and H are its
// pixel size. The pixel size only decides the displayed dimensions —
// the aspect ratio is kept when the picture is scaled to fit the page.
type Picture struct {
	Data []byte
	Ext  string
	W, H int
}

// A4 page with 1-inch margins, in twentieths of a point (twips).
const (
	pageWidth  = 11906
	pageHeight = 16838
	pageMargin = 1440
)

// emuPerPixel converts pixels to EMU at 96 dpi (914400/96 = 9525), the
// natural size a picture is never scaled beyond.
const emuPerPixel = 914400 / 96

// emuPerTwip converts twips to English Metric Units (914400 EMU/inch).
const emuPerTwip = 914400 / 1440 // 635

// Text area in EMU: the page minus 1-inch margins on every side.
const (
	textAreaCX = (pageWidth - 2*pageMargin) * emuPerTwip  // 5731510
	textAreaCY = (pageHeight - 2*pageMargin) * emuPerTwip // 8863330
)

// Write creates a minimal valid .docx at dst: an A4 page with 1-inch
// margins; each string in paragraphs becomes one paragraph (the text is
// kept exactly, including Chinese), then, when pic is non-nil, one
// paragraph holding the picture inline. The picture is shown at its
// natural size (pixels at 96 dpi), scaled down — keeping the aspect
// ratio — when needed to fit the text width and height; it is never
// scaled up. paragraphs may be empty or
// nil, producing a picture-only document. The file is written
// atomically through a temp file in dst's directory plus rename.
func Write(dst string, paragraphs []string, pic *Picture) error {
	if pic != nil {
		if err := pic.validate(); err != nil {
			return err
		}
	}
	data, err := zipDocx(documentXML(paragraphs, pic), pic)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(dst, data, 0o600); err != nil {
		return fmt.Errorf("docx: %w", err)
	}
	return nil
}

// WritePages creates a text-only .docx at dst with the same A4 layout
// and fonts as Write. pages[i] holds the paragraphs of page i; a
// dedicated paragraph containing <w:br w:type="page"/> separates
// consecutive pages, with no break after the last one. (w:br is used
// rather than w:pageBreakBefore because the Cocoa importers behind
// Pages and Quick Look ignore pageBreakBefore.) Empty pages still
// produce their page (an empty paragraph) so page numbering matches
// the source document.
// The document carries no pictures, media part or image relationship.
// The file is written atomically through a temp file in dst's
// directory plus rename.
func WritePages(dst string, pages [][]string) error {
	data, err := zipDocx(pagesDocumentXML(pages), nil)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(dst, data, 0o600); err != nil {
		return fmt.Errorf("docx: %w", err)
	}
	return nil
}

// validate rejects pictures the package cannot embed.
func (p *Picture) validate() error {
	if len(p.Data) == 0 {
		return errors.New("docx: picture has no data")
	}
	if p.W <= 0 || p.H <= 0 {
		return fmt.Errorf("docx: picture size %dx%d is invalid", p.W, p.H)
	}
	if p.mimeType() == "" {
		return fmt.Errorf("docx: unsupported picture extension %q", p.Ext)
	}
	return nil
}

// mimeType maps the file extension to an OOXML image content type.
func (p *Picture) mimeType() string {
	switch strings.ToLower(p.Ext) {
	case "png":
		return "image/png"
	case "jpeg", "jpg":
		return "image/jpeg"
	}
	return ""
}

// fileExt is the lowercased extension used for the media file name.
func (p *Picture) fileExt() string {
	return strings.ToLower(p.Ext)
}

// mediaName is the media file name inside word/media, empty for a nil
// picture.
func (p *Picture) mediaName() string {
	if p == nil {
		return ""
	}
	return "image1." + p.fileExt()
}

// pictureExtent returns the inline extent in EMU: the picture's natural
// size (pixels at 96 dpi) scaled down — keeping the aspect ratio — to
// fit the text width and height, never scaled up beyond natural.
func pictureExtent(p *Picture) (cx, cy int64) {
	cx = int64(p.W) * emuPerPixel
	cy = int64(p.H) * emuPerPixel
	if cx > textAreaCX {
		cy = int64(p.H) * textAreaCX / int64(p.W)
		cx = textAreaCX
	}
	if cy > textAreaCY {
		cx = int64(p.W) * textAreaCY / int64(p.H)
		cy = textAreaCY
	}
	return cx, cy
}

// zipDocx assembles the whole package in memory and returns the .docx
// bytes. documentXML is the already-rendered document part; pic, when
// non-nil, adds the image relationship and media part that documentXML
// references through rId1. Media is a few megabytes at most, so
// buffering is fine.
func zipDocx(documentXML string, pic *Picture) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, xml.Header+body)
		return err
	}
	addRaw := func(name string, data []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}

	mediaName := pic.mediaName()
	if err := add("[Content_Types].xml", contentTypesXML(pic)); err != nil {
		return nil, err
	}
	if err := add("_rels/.rels", rootRelsXML); err != nil {
		return nil, err
	}
	if err := add("docProps/core.xml", corePropsXML(time.Now().UTC())); err != nil {
		return nil, err
	}
	if err := add("docProps/app.xml", appPropsXML); err != nil {
		return nil, err
	}
	if err := add("word/document.xml", documentXML); err != nil {
		return nil, err
	}
	if err := add("word/settings.xml", settingsXML); err != nil {
		return nil, err
	}
	if err := add("word/_rels/document.xml.rels", documentRelsXML(pic)); err != nil {
		return nil, err
	}
	if pic != nil {
		if err := addRaw("word/media/"+mediaName, pic.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// contentTypesXML lists the defaults and overrides for every part. The
// picture extension gets a Default entry so image1.<ext> resolves.
func contentTypesXML(pic *Picture) string {
	const open = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>`
	const overrides = `<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>` +
		`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
		`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>` +
		`</Types>`
	if pic == nil {
		return open + overrides
	}
	return open +
		fmt.Sprintf(`<Default Extension="%s" ContentType="%s"/>`, pic.fileExt(), pic.mimeType()) +
		overrides
}

// rootRelsXML points the package at the document part and the doc
// property parts.
const rootRelsXML = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>` +
	`</Relationships>`

// documentRelsXML binds the settings part and, with a picture, rId1 —
// referenced by the drawing's a:blip — to the media file.
func documentRelsXML(pic *Picture) string {
	var image string
	if pic != nil {
		image = fmt.Sprintf(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/%s"/>`, pic.mediaName())
	}
	return `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + image +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/>` +
		`</Relationships>`
}

// settingsXML sets the default tab stop (half an inch, Word's own):
// without it Pages gives every tab zero width.
const settingsXML = `<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:defaultTabStop w:val="720"/></w:settings>`

// corePropsXML stamps the file as produced by ShelfU.
func corePropsXML(now time.Time) string {
	ts := now.Format("2006-01-02T15:04:05Z")
	return `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:creator>Rakko</dc:creator><cp:lastModifiedBy>Rakko</cp:lastModifiedBy>` +
		fmt.Sprintf(`<dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created>`, ts) +
		fmt.Sprintf(`<dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>`, ts) +
		`</cp:coreProperties>`
}

const appPropsXML = `<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">` +
	`<Application>Rakko</Application>` +
	`</Properties>`

// documentOpen and documentClose wrap every generated document part;
// the namespaces cover text runs, drawings and relationships alike.
const documentOpen = `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body>`
const documentClose = `</w:body></w:document>`

// documentXML renders the body of a Write document: one w:p per
// paragraph, then the optional picture paragraph, then the section
// properties fixing the A4 layout.
func documentXML(paragraphs []string, pic *Picture) string {
	var b strings.Builder
	b.WriteString(documentOpen)
	for _, p := range paragraphs {
		appendParagraph(&b, p)
	}
	if pic != nil {
		appendPicture(&b, pic)
	}
	writeSectPr(&b)
	b.WriteString(documentClose)
	return b.String()
}

// pagesDocumentXML renders the body of a WritePages document: the
// paragraphs of each page, separated by a page-break paragraph
// (<w:p><w:r><w:br w:type="page"/></w:r></w:p>) before every page after
// the first. An empty page contributes an empty paragraph so the page
// still exists between its two breaks.
func pagesDocumentXML(pages [][]string) string {
	var b strings.Builder
	b.WriteString(documentOpen)
	for i, page := range pages {
		if i > 0 {
			appendPageBreak(&b)
		}
		if len(page) == 0 {
			appendParagraph(&b, "")
			continue
		}
		for _, p := range page {
			appendParagraph(&b, p)
		}
	}
	if len(pages) == 0 {
		appendParagraph(&b, "")
	}
	writeSectPr(&b)
	b.WriteString(documentClose)
	return b.String()
}

// appendParagraph writes one w:p holding text; an empty text produces
// an empty paragraph. A tab becomes <w:tab/> (inside w:t Word and Pages
// do not treat it as one). The East Asian font hint keeps Chinese
// readable in Word.
func appendParagraph(b *strings.Builder, text string) {
	if text == "" {
		b.WriteString(`<w:p/>`)
		return
	}
	b.WriteString(`<w:p><w:r><w:rPr><w:rFonts w:eastAsia="PingFang SC"/></w:rPr>`)
	for i, seg := range strings.Split(text, "\t") {
		if i > 0 {
			b.WriteString(`<w:tab/>`)
		}
		if seg != "" {
			b.WriteString(`<w:t xml:space="preserve">`)
			appendEscapedText(b, seg)
			b.WriteString(`</w:t>`)
		}
	}
	b.WriteString(`</w:r></w:p>`)
}

// appendPageBreak writes the paragraph that ends the current page.
func appendPageBreak(b *strings.Builder) {
	b.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
}

// appendPicture writes the w:p holding pic as an inline drawing sized
// by pictureExtent, referencing the image relationship rId1.
func appendPicture(b *strings.Builder, pic *Picture) {
	cx, cy := pictureExtent(pic)
	mediaName := pic.mediaName()
	fmt.Fprintf(b, `<w:p><w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0">`+
		`<wp:extent cx="%d" cy="%d"/><wp:effectExtent l="0" t="0" r="0" b="0"/>`+
		`<wp:docPr id="1" name="%s" descr="%s"/><wp:cNvGraphicFramePr/>`+
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic>`+
		`<pic:nvPicPr><pic:cNvPr id="0" name="%s"/><pic:cNvPicPr/></pic:nvPicPr>`+
		`<pic:blipFill><a:blip r:embed="rId1"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`+
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm>`+
		`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>`+
		`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`,
		cx, cy, mediaName, mediaName, mediaName, cx, cy)
}

// writeSectPr writes the section properties fixing the A4 page size
// and 1-inch margins; it is the last body element in every document.
func writeSectPr(b *strings.Builder) {
	fmt.Fprintf(b, `<w:sectPr><w:pgSz w:w="%d" w:h="%d"/>`+
		`<w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="720" w:footer="720" w:gutter="0"/>`+
		`</w:sectPr>`,
		pageWidth, pageHeight, pageMargin, pageMargin, pageMargin, pageMargin)
}

// appendEscapedText writes s to b sanitized and XML-escaped. Runes that
// are not legal XML 1.0 characters — controls below 0x20 other than
// \t \n \r — are dropped; newline sequences (\r\n, \n, \r) become a
// single space so a paragraph stays on one line. (appendParagraph has
// already turned tabs into <w:tab/>.) Invalid
// UTF-8 decodes to U+FFFD, which is kept (Go's decoder also maps the
// noncharacters U+FFFE/U+FFFF to U+FFFD rather than surfacing them).
// The sanitized string is escaped with xml.EscapeText; raw text is
// never written.
func appendEscapedText(b *strings.Builder, s string) {
	var clean strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '\n' || r == '\r':
			if r == '\r' && i < len(s) && s[i] == '\n' {
				i++ // \r\n is one line break
			}
			clean.WriteByte(' ')
		case isXMLChar(r):
			clean.WriteRune(r)
		}
	}
	// strings.Builder never fails; the error return is unreachable.
	_ = xml.EscapeText(b, []byte(clean.String()))
}

// isXMLChar reports whether r is a legal XML 1.0 Char:
// #x9 | #xA | #xD | [#x20-#xD7FF] | [#xE000-#xFFFD] | [#x10000-#x10FFFF].
// Callers handle \n and \r before calling, so only \t is kept below 0x20.
func isXMLChar(r rune) bool {
	return r == '\t' ||
		r >= 0x20 && r <= 0xD7FF ||
		r >= 0xE000 && r != 0xFFFE && r != 0xFFFF
}

// writeFileAtomic writes data to dst through a temp file in the same
// directory plus rename, so a crash never leaves a half-written .docx.
func writeFileAtomic(dst string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}
