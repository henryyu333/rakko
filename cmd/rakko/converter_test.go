//go:build darwin

package main

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/docx"
	"rakko/internal/mac"
	"rakko/internal/store"
)

// convertFakes replaces the native converters with fakes that write a
// small marker file, and runs jobs synchronously.
type convertFakes struct {
	formats  []mac.ImageFormat
	pdfSrcs  [][]string
	docText  [][]string
	lines    []string
	failFor  string // convertImage fails for this source
	onConver func()
	// PDFs: page count and per-page text (nil page = scanned, no text
	// layer); rendered pages and the pages written to Word.
	pages    int
	pageText [][]string
	rendered []int
	docPages [][][]string
	// Resizing: the longer-side caps and formats asked for, and the
	// pixel size every image reports.
	resized []int
	size    [2]int
	// Compressing: the (src, format) pairs asked for, per-source alpha,
	// and the bytes the fake encoder writes (a size over the limit
	// reports over).
	fit      [][2]string
	alpha    map[string]bool
	fitBytes int
}

func fakeConverters(t *testing.T) *convertFakes {
	t.Helper()
	f := &convertFakes{}
	oImage, oToPDF, oOCR, oSize := convertImage, imagesToPDF, recognizeText, imageSize
	oCount, oText, oRender, oResize := pdfPageCount, pdfText, renderPDFPage, resizeImage
	oDocx, oPages, oWork, oDispatch := writeDocx, writeDocxPages, convertWork, convertDispatch
	oFit, oAlpha := fitImage, imageAlpha
	t.Cleanup(func() {
		convertImage, imagesToPDF, recognizeText, imageSize = oImage, oToPDF, oOCR, oSize
		pdfPageCount, pdfText, renderPDFPage, resizeImage = oCount, oText, oRender, oResize
		writeDocx, writeDocxPages, convertWork, convertDispatch = oDocx, oPages, oWork, oDispatch
		fitImage, imageAlpha = oFit, oAlpha
	})
	fitImage = func(src, dst string, format mac.ImageFormat, limit int64) (bool, error) {
		f.fit = append(f.fit, [2]string{src, dst})
		f.formats = append(f.formats, format)
		n := f.fitBytes
		if n <= 0 {
			n = 1
		}
		return int64(n) > limit, os.WriteFile(dst, make([]byte, n), 0o600)
	}
	imageAlpha = func(src string) (bool, error) {
		return f.alpha[filepath.Base(src)], nil
	}
	f.size = [2]int{40, 20}
	convertImage = func(src, dst string, format mac.ImageFormat) error {
		if f.onConver != nil {
			f.onConver()
		}
		if src == f.failFor {
			return errors.New("cannot read")
		}
		f.formats = append(f.formats, format)
		return os.WriteFile(dst, []byte("img"), 0o600)
	}
	imagesToPDF = func(srcs []string, dst string) error {
		f.pdfSrcs = append(f.pdfSrcs, srcs)
		return os.WriteFile(dst, []byte("%PDF"), 0o600)
	}
	recognizeText = func(string) ([]string, error) { return slices.Clone(f.lines), nil }
	imageSize = func(string) (int, int, error) { return f.size[0], f.size[1], nil }
	resizeImage = func(src, dst string, format mac.ImageFormat, px int) error {
		f.resized = append(f.resized, px)
		f.formats = append(f.formats, format)
		return os.WriteFile(dst, []byte("s"), 0o600)
	}
	pdfPageCount = func(string) (int, error) { return f.pages, nil }
	pdfText = func(string) ([][]string, error) {
		out := make([][]string, len(f.pageText))
		for i, p := range f.pageText {
			out[i] = slices.Clone(p)
		}
		return out, nil
	}
	renderPDFPage = func(src string, page int, dst string, format mac.ImageFormat) error {
		f.rendered = append(f.rendered, page)
		f.formats = append(f.formats, format)
		return os.WriteFile(dst, []byte("page"), 0o600)
	}
	writeDocx = func(dst string, paragraphs []string, pic *docx.Picture) error {
		f.docText = append(f.docText, paragraphs)
		if pic == nil || len(pic.Data) == 0 {
			return errors.New("no picture")
		}
		return os.WriteFile(dst, []byte("docx"), 0o600)
	}
	writeDocxPages = func(dst string, pages [][]string) error {
		f.docPages = append(f.docPages, pages)
		return os.WriteFile(dst, []byte("docx"), 0o600)
	}
	convertWork = func(fn func()) { fn() }
	convertDispatch = func(fn func()) { fn() }
	return f
}

// convertShelf is a headless shelf whose tray holds the named files (in
// the given order, first frontmost).
func convertShelf(t *testing.T, names ...string) (*shelf, *ui.Tester, []string) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	dir := t.TempDir()
	var paths []string
	for i := len(names) - 1; i >= 0; i-- {
		p := filepath.Join(dir, names[i])
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
		paths = append([]string{p}, paths...)
	}
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	a.conv = &converter{host: a}
	a.modules = []module{a.conv}
	return a, ui.NewTester(a.view, sheetW, panelTestH), paths
}

func selectAll(a *shelf) {
	var ids []string
	for _, it := range a.visibleItems() {
		ids = append(ids, it.ID)
	}
	a.selection.onlyAll(ids)
}

func outputNames(a *shelf) []string {
	var out []string
	for _, it := range a.store.Items {
		if it.Converted {
			out = append(out, filepath.Base(it.Path))
		}
	}
	return out
}

// Conversion is offered for images only, and never for a mixed selection
// (which would convert only part of what the user picked).
func TestConvertMenuOnlyForImages(t *testing.T) {
	fakeConverters(t)
	a, view, _ := convertShelf(t, "a.png", "note.txt")
	if err := view.RightClick("a.png"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), "转换为") {
		t.Fatalf("an image's menu lacks 转换为: %v", view.Menu())
	}
	view.CloseMenu()
	if err := view.RightClick("note.txt"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(view.Menu(), "转换为") {
		t.Fatalf("a text file must not offer conversion: %v", view.Menu())
	}
	view.CloseMenu()
	selectAll(a)
	view.Frame()
	if err := view.RightClick("a.png"); err != nil {
		t.Fatal(err)
	}
	for _, l := range view.Menu() {
		if strings.HasPrefix(l, "转换") || l == "合并为一个 PDF" {
			t.Fatalf("a mixed selection must not offer conversion: %v", view.Menu())
		}
	}
}

// Converting several images writes one output each, puts them at the
// front of the tray in display order, selects them and says so; the
// format they already have cannot be chosen.
func TestConvertSelectedImagesToJPEG(t *testing.T) {
	f := fakeConverters(t)
	a, view, paths := convertShelf(t, "a.png", "b.png")
	selectAll(a)
	view.Frame()
	if err := view.RightClick("a.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换 2 张为", "PNG（当前格式）"); err == nil && len(outputNames(a)) > 0 {
		t.Fatal("converting to the format the images already have must not run")
	}
	view.CloseMenu()
	if err := view.RightClick("a.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换 2 张为", "JPEG"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"a.jpg", "b.jpg"}) {
		t.Fatalf("outputs = %v, want a.jpg, b.jpg at the front in order", got)
	}
	if !a.store.Items[0].Converted || !a.store.Items[1].Converted {
		t.Fatal("outputs must lead the tray")
	}
	if a.selection.count() != 2 || !a.selection.has(a.store.Items[0].ID) || !a.selection.has(a.store.Items[1].ID) {
		t.Fatal("the outputs must be selected so one drag takes them")
	}
	if !slices.Equal(f.formats, []mac.ImageFormat{mac.FormatJPEG, mac.FormatJPEG}) {
		t.Fatalf("formats = %v", f.formats)
	}
	if !strings.HasPrefix(a.status, "已转换 2 项") {
		t.Fatalf("status = %q", a.status)
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err != nil || string(b) != "x" {
			t.Fatalf("original %s touched: %q %v", p, b, err)
		}
	}
	if a.conv.busy() {
		t.Fatal("the job must be over")
	}
}

// Merging makes one PDF whose pages follow the tray's order, named after
// the first image.
func TestMergeImagesIntoOnePDF(t *testing.T) {
	f := fakeConverters(t)
	a, view, paths := convertShelf(t, "a.png", "b.jpg", "c.heic")
	selectAll(a)
	view.Frame()
	if err := view.RightClick("b.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("合并为一个 PDF"); err != nil {
		t.Fatal(err)
	}
	if len(f.pdfSrcs) != 1 || !slices.Equal(f.pdfSrcs[0], paths) {
		t.Fatalf("pages = %v, want %v", f.pdfSrcs, paths)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"a 等 3 张.pdf"}) {
		t.Fatalf("outputs = %v", got)
	}
}

// Word gets the recognized lines and the picture; with nothing recognized
// the document still holds the picture and the user is told.
func TestConvertToWord(t *testing.T) {
	f := fakeConverters(t)
	f.lines = []string{"周五下午三点开会", "4B 会议室"}
	a, view, _ := convertShelf(t, "board.png")
	if err := view.RightClick("board.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换为", "Word（识别文字）"); err != nil {
		t.Fatal(err)
	}
	if len(f.docText) != 1 || !slices.Equal(f.docText[0], f.lines) {
		t.Fatalf("document text = %v", f.docText)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"board.docx"}) {
		t.Fatalf("outputs = %v", got)
	}
	f.lines = nil
	view.Frame()
	if err := view.RightClick("board.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换为", "Word（识别文字）"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Contains(got, "board 2.docx") {
		t.Fatalf("a second output must not overwrite the first: %v", got)
	}
	if a.status != "没有识别到文字，只放入了图片" {
		t.Fatalf("status = %q", a.status)
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 2 {
		t.Fatalf("the temporary picture must not stay behind: %v", entries)
	}
}

// One image failing does not lose the others, and the summary says so.
func TestConvertFailureKeepsTheRest(t *testing.T) {
	f := fakeConverters(t)
	a, view, paths := convertShelf(t, "a.png", "b.png")
	f.failFor = paths[0]
	selectAll(a)
	view.Frame()
	if err := view.RightClick("a.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换 2 张为", "HEIC"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"b.heic"}) {
		t.Fatalf("outputs = %v", got)
	}
	if a.status != "1 项成功，1 项失败" || a.statusKind != statusErr {
		t.Fatalf("status = %q err=%v", a.status, a.statusKind == statusErr)
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 1 {
		t.Fatalf("a failed output must not stay on disk: %v", entries)
	}
}

// Cancelling keeps what is already done and stops the rest.
func TestConvertCancelKeepsFinished(t *testing.T) {
	f := fakeConverters(t)
	a, view, _ := convertShelf(t, "a.png", "b.png", "c.png")
	calls := 0
	f.onConver = func() {
		calls++
		if calls == 1 {
			a.conv.job.cancel.Store(true)
		}
	}
	selectAll(a)
	view.Frame()
	if err := view.RightClick("a.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换 3 张为", "JPEG"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"a.jpg"}) {
		t.Fatalf("outputs = %v, want only the one finished before cancelling", got)
	}
	if !strings.HasPrefix(a.status, "已取消") {
		t.Fatalf("status = %q", a.status)
	}
}

// A clipboard screenshot converts too; its output is named after the
// screenshot without the clock's colons, and the tray comes forward.
func TestConvertClipboardScreenshot(t *testing.T) {
	fakeConverters(t)
	a, _, _ := convertShelf(t)
	if err := a.store.AddClipImage(testPNG(t), "com.apple.screencaptureui"); err != nil {
		t.Fatal(err)
	}
	a.setTab(pageClipboard)
	shot := a.store.Items[0]
	a.startConvert([]store.Item{shot}, toPDF)
	if a.tab != pageTray {
		t.Fatal("the outputs live on the tray, so the tray must come forward")
	}
	got := outputNames(a)
	if len(got) != 1 || strings.Contains(got[0], ":") || !strings.HasPrefix(got[0], "截图 ") || !strings.HasSuffix(got[0], ".pdf") {
		t.Fatalf("outputs = %v", got)
	}
}

// A PDF offers Word and per-page images; mixed with an image it offers
// nothing, so a selection is never half converted.
func TestConvertPDFMenu(t *testing.T) {
	fakeConverters(t)
	a, view, _ := convertShelf(t, "cv.pdf", "a.png")
	if err := view.RightClick("cv.pdf"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), "转换为") {
		t.Fatalf("a PDF's menu lacks 转换为: %v", view.Menu())
	}
	view.CloseMenu()
	selectAll(a)
	view.Frame()
	if err := view.RightClick("cv.pdf"); err != nil {
		t.Fatal(err)
	}
	for _, l := range view.Menu() {
		if strings.HasPrefix(l, "转换") || l == "合并为一个 PDF" {
			t.Fatalf("a PDF mixed with an image must not offer conversion: %v", view.Menu())
		}
	}
}

// PDF to Word keeps one Word page per PDF page: text pages as they are,
// a scanned page (no text layer) through text recognition.
func TestConvertPDFToWord(t *testing.T) {
	f := fakeConverters(t)
	f.pageText = [][]string{{"张三 · 产品经理", "工作经历"}, nil, {"Education"}}
	f.lines = []string{"扫描页的文字"}
	a, view, _ := convertShelf(t, "cv.pdf")
	if err := view.RightClick("cv.pdf"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换为", "Word（提取文字）"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"张三 · 产品经理", "工作经历"}, {"扫描页的文字"}, {"Education"}}
	if len(f.docPages) != 1 || !slices.EqualFunc(f.docPages[0], want, slices.Equal[[]string]) {
		t.Fatalf("Word pages = %v, want %v", f.docPages, want)
	}
	if !slices.Equal(f.rendered, []int{2}) {
		t.Fatalf("only the scanned page must be rendered for recognition, rendered %v", f.rendered)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"cv.docx"}) {
		t.Fatalf("outputs = %v", got)
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 1 {
		t.Fatalf("the rendered page must not stay behind: %v", entries)
	}
}

// PDF to images gives one picture per page, numbered; a one-page PDF
// keeps its plain name.
func TestConvertPDFToImagesPerPage(t *testing.T) {
	f := fakeConverters(t)
	f.pages = 3
	a, view, _ := convertShelf(t, "cv.pdf")
	if err := view.RightClick("cv.pdf"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换为", "PNG（每页一张）"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"cv 第 1 页.png", "cv 第 2 页.png", "cv 第 3 页.png"}) {
		t.Fatalf("outputs = %v", got)
	}
	if !slices.Equal(f.rendered, []int{1, 2, 3}) {
		t.Fatalf("rendered pages = %v", f.rendered)
	}
	f.pages, f.rendered = 1, nil
	view.Frame()
	if err := view.RightClick("cv.pdf"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换为", "JPEG（每页一张）"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Contains(got, "cv.jpg") {
		t.Fatalf("a one-page PDF must keep its name: %v", got)
	}
}

// Cancelling while a PDF is being read stops between its pages and keeps
// no half-made document; outputs finished before it stay.
func TestConvertPDFToWordCancelMidDocument(t *testing.T) {
	f := fakeConverters(t)
	f.pageText = [][]string{nil, nil, {"末页"}}
	a, view, _ := convertShelf(t, "a.pdf", "b.pdf")
	calls := 0
	ocr := recognizeText
	recognizeText = func(p string) ([]string, error) {
		calls++
		if calls == 3 { // first page of the second PDF
			a.conv.job.cancel.Store(true)
		}
		return ocr(p)
	}
	selectAll(a)
	view.Frame()
	if err := view.RightClick("a.pdf"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换 2 个为", "Word（提取文字）"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"a.docx"}) {
		t.Fatalf("outputs = %v, want only the document finished before cancelling", got)
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 1 {
		t.Fatalf("a cancelled document must leave nothing behind: %v", entries)
	}
	if !strings.HasPrefix(a.status, "已取消") {
		t.Fatalf("status = %q", a.status)
	}
}

// Recognized text comes out in ordinary characters, and a PDF without any
// text does not claim a picture was put into its Word file.
func TestConvertWordTextIsNormalized(t *testing.T) {
	f := fakeConverters(t)
	f.lines = []string{"⼯作经历"}
	a, view, _ := convertShelf(t, "shot.png", "scan.pdf")
	if err := view.RightClick("shot.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换为", "Word（识别文字）"); err != nil {
		t.Fatal(err)
	}
	if len(f.docText) != 1 || !slices.Equal(f.docText[0], []string{"工作经历"}) {
		t.Fatalf("image Word text = %v, want the compatibility form normalized", f.docText)
	}
	f.lines, f.pageText = nil, [][]string{nil}
	view.Frame()
	if err := view.RightClick("scan.pdf"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("转换为", "Word（提取文字）"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.status, "图片") {
		t.Fatalf("a text-only PDF document must not mention a picture: %q", a.status)
	}
}

// Resizing offers only the sizes that would make the pictures smaller,
// keeps JPEG/PNG/HEIC as they are (other formats become JPEG), and
// reports how much smaller the files got.
func TestResizeImages(t *testing.T) {
	f := fakeConverters(t)
	f.size = [2]int{2000, 1000}
	a, view, paths := convertShelf(t, "a.png", "b.webp")
	selectAll(a)
	view.Frame()
	if err := view.RightClick("a.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("缩小 2 张图片", "大（长边 2560 像素）"); err == nil && len(f.resized) > 0 {
		t.Fatal("a size larger than every picture must not run")
	}
	view.CloseMenu()
	if err := view.RightClick("a.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("缩小 2 张图片", "中（长边 1600 像素）"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.resized, []int{1600, 1600}) || !slices.Equal(f.formats, []mac.ImageFormat{mac.FormatPNG, mac.FormatJPEG}) {
		t.Fatalf("resized %v as %v, want 1600 px as PNG then JPEG", f.resized, f.formats)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"a 1600px.png", "b 1600px.jpg"}) {
		t.Fatalf("outputs = %v", got)
	}
	// The originals are one byte each ("x"), the outputs one byte too.
	if a.status != "已缩小 2 张 · 2 字节 → 2 字节" {
		t.Fatalf("status = %q", a.status)
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err != nil || string(b) != "x" {
			t.Fatalf("original %s touched: %q %v", p, b, err)
		}
	}

	f.size = [2]int{640, 480}
	view.Frame()
	if err := view.RightClick("a.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("缩小 2 张图片", "小（长边 800 像素）"); err == nil && len(f.resized) > 2 {
		t.Fatal("pictures already within every size must not be resized")
	}
}

// readZip lists an archive's entries: name → contents (a link's target),
// with the mode of each.
func readZip(t *testing.T, p string) (map[string]string, map[string]os.FileMode) {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files, modes := map[string]string{}, map[string]os.FileMode{}
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[zf.Name], modes[zf.Name] = string(b), zf.Mode()
	}
	return files, modes
}

// Zipping a folder and files together keeps the folder's structure,
// stores links as links, leaves .DS_Store out, renames a clashing name
// instead of overwriting it, and selects the archive.
func TestZipFilesAndFolders(t *testing.T) {
	fakeConverters(t)
	a, view, _ := convertShelf(t, "报告.pdf")
	project := filepath.Join(t.TempDir(), "项目")
	other := filepath.Join(t.TempDir(), "报告.pdf")
	for p, body := range map[string]string{
		filepath.Join(project, "说明.txt"):         "说明",
		filepath.Join(project, "src", "main.go"): "package main",
		filepath.Join(project, ".DS_Store"):      "finder",
		filepath.Join(project, "src", "run.sh"):  "#!/bin/sh",
		other:                                    "另一份",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(project, "src", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("说明.txt", filepath.Join(project, "链接")); err != nil {
		t.Fatal(err)
	}
	if err := a.store.AddFiles([]string{other, project}); err != nil {
		t.Fatal(err)
	}
	selectAll(a)
	view.Frame()
	if err := view.RightClick("项目"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("压缩 3 项为 zip"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"项目 等 3 项.zip"}) {
		t.Fatalf("outputs = %v", got)
	}
	if a.selection.count() != 1 || !a.selection.has(a.store.Items[0].ID) {
		t.Fatal("the archive must be selected")
	}
	if !strings.HasPrefix(a.status, "已压缩") {
		t.Fatalf("status = %q", a.status)
	}
	files, modes := readZip(t, a.store.Items[0].Path)
	want := map[string]string{
		"项目/":            "",
		"项目/说明.txt":      "说明",
		"项目/src/":        "",
		"项目/src/main.go": "package main",
		"项目/src/run.sh":  "#!/bin/sh",
		"项目/链接":          "说明.txt",
		"报告.pdf":         "另一份",
		"报告 2.pdf":       "x",
	}
	if len(files) != len(want) {
		t.Fatalf("entries = %v, want %v", files, want)
	}
	for name, body := range want {
		if got, ok := files[name]; !ok || got != body {
			t.Fatalf("entry %q = %q (present %v), want %q; all: %v", name, got, ok, body, files)
		}
	}
	if modes["项目/链接"]&os.ModeSymlink == 0 {
		t.Fatal("a link must be stored as a link")
	}
	if modes["项目/src/run.sh"].Perm() != 0o755 {
		t.Fatalf("run.sh mode = %v, want executable", modes["项目/src/run.sh"])
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 1 {
		t.Fatalf("only the archive may be left in converted/: %v", entries)
	}
}

// Cancelling a zip leaves no partial archive behind.
func TestZipCancelLeavesNothing(t *testing.T) {
	fakeConverters(t)
	a, _, _ := convertShelf(t, "a.txt", "b.txt")
	items := slices.Clone(a.store.Items)
	convertWork = func(fn func()) {
		a.conv.job.cancel.Store(true)
		fn()
	}
	a.startConvert(items, toZip)
	if len(outputNames(a)) != 0 {
		t.Fatalf("a cancelled archive must not reach the tray: %v", outputNames(a))
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 0 {
		t.Fatalf("a cancelled archive must leave nothing behind: %v", entries)
	}
}

// A file that cannot be read halfway through fails the archive and
// leaves no partial zip behind.
func TestZipFailureLeavesNothing(t *testing.T) {
	fakeConverters(t)
	a, _, _ := convertShelf(t)
	folder := filepath.Join(t.TempDir(), "资料")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(folder, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(folder, "b.txt"), 0); err != nil {
		t.Fatal(err)
	}
	if err := a.store.AddFiles([]string{folder}); err != nil {
		t.Fatal(err)
	}
	a.startConvert(slices.Clone(a.store.Items), toZip)
	if a.statusKind != statusErr || !strings.Contains(a.status, "压缩失败") {
		t.Fatalf("status = %q err=%v", a.status, a.statusKind == statusErr)
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 0 {
		t.Fatalf("a failed archive must leave nothing behind: %v", entries)
	}
}
