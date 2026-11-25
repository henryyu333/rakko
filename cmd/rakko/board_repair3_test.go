package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
	"rakko/internal/layout"
	"rakko/internal/settings"
	"rakko/locales"
)

func boardTestShelfLang(t *testing.T, lang string) (*shelf, *boardUI) {
	t.Helper()
	a := &shelf{
		dataDir:      t.TempDir(),
		locale:       lang,
		settings:     settings.Default(),
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
	b := a.boardSession()
	if b == nil {
		t.Fatal("boardSession returned nil")
	}
	return a, b
}

var (
	colorA = color.RGBA{R: 10, G: 20, B: 30, A: 255}
	colorB = color.RGBA{R: 200, G: 100, B: 50, A: 255}
)

func makeBoardTestPNG(w, h int, c1, c2 color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(max(h-1, 1))
		for x := 0; x < w; x++ {
			s := float64(x) / float64(max(w-1, 1))
			k := 0.5*s + 0.5*t
			col := color.RGBA{
				R: uint8(float64(c1.R)*(1-k) + float64(c2.R)*k),
				G: uint8(float64(c1.G)*(1-k) + float64(c2.G)*k),
				B: uint8(float64(c1.B)*(1-k) + float64(c2.B)*k),
				A: 255,
			}
			if x%40 == 0 || y%40 == 0 || x < 2 || y < 2 || x >= w-2 || y >= h-2 {
				col = color.RGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.SetRGBA(x, y, col)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func addBoardRect(t *testing.T, b *boardUI) doc.ID {
	t.Helper()
	b.ed.SetGeoTool("rectangle")
	b.ed.PointerDown(200, 200, 0, 1, editor.Mods{}, 1)
	b.ed.PointerMove(320, 280, editor.Mods{}, 1.1)
	b.ed.PointerUp(320, 280, editor.Mods{}, 1.2)
	sel := b.ed.Selection()
	if len(sel) != 1 {
		t.Fatalf("selection = %v", sel)
	}
	return sel[0]
}

func TestBoardChromeFitsAllWidths(t *testing.T) {
	a, b := boardTestShelf(t)
	l := locales.In("zh-Hans")
	for _, w := range []int{480, 560, 640, 760, 860, 960, 1010, 1040, 1280} {
		view := ui.NewTester(a.boardView, w, 620)
		view.SetLocale("zh-Hans")
		view.Frame()
		view.Frame()
		for _, label := range []string{l.BoardMore(), l.BoardZoomIn(), l.BoardZoomFit(), l.BoardMinimapLabel()} {
			if err := view.Click(label); err != nil {
				t.Fatalf("w=%d: %q not clickable: %v", w, label, err)
			}
			view.CloseMenu()
			view.Frame()
		}
		if w == 760 || w == 960 {
			saveBoardSeg3Shot(t, "chrome-"+strconv.Itoa(w)+".png", view.Image())
		}
	}
	_ = b
}

func TestBoardPastePrefersInternalWhenClipboardUnchanged(t *testing.T) {
	a, b := boardTestShelf(t)
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	rect := addBoardRect(t, b)
	view.Frame()

	origStamp, origImg := boardClipboardFingerprint, boardReadClipboardImage
	t.Cleanup(func() { boardClipboardFingerprint, boardReadClipboardImage = origStamp, origImg })
	boardClipboardFingerprint = func(*ui.Context) string { return "same" }
	pngData := makeBoardTestPNG(40, 30, colorA, colorB)
	boardReadClipboardImage = func() []byte { return pngData }

	view.Key(ui.Cmd, ui.KeyC)
	before := b.ed.ItemCount()
	view.Key(ui.Cmd, ui.KeyV)
	view.Frame()
	if got := b.ed.ItemCount(); got != before+1 {
		t.Fatalf("pasted items = %d, want %d (internal clipboard)", got, before+1)
	}
	for _, id := range b.ed.Selection() {
		if s := b.ed.Doc.Shapes[id]; s != nil && s.Type == doc.TypeImage {
			t.Fatalf("pasted an image instead of the copied shape %s", rect)
		}
	}

	// A newer system clipboard wins: paste inserts the image.
	boardClipboardFingerprint = func(*ui.Context) string { return "newer" }
	view.Key(ui.Cmd, ui.KeyV)
	view.Frame()
	foundImage := false
	for _, id := range b.ed.Selection() {
		if s := b.ed.Doc.Shapes[id]; s != nil && s.Type == doc.TypeImage {
			foundImage = true
		}
	}
	if !foundImage {
		t.Fatal("a changed clipboard did not paste its image")
	}
}

func TestBoardPasteTextDoesNotInsertImage(t *testing.T) {
	a, b := boardTestShelf(t)
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	origImg := boardReadClipboardImage
	t.Cleanup(func() { boardReadClipboardImage = origImg })
	boardReadClipboardImage = func() []byte { return makeBoardTestPNG(20, 20, colorA, colorB) }
	view.SetClipboard("从表格复制的文字")
	before := b.ed.ItemCount()
	view.Key(ui.Cmd, ui.KeyV)
	view.Frame()
	if got := b.ed.ItemCount(); got != before+1 {
		t.Fatalf("text+image clipboard made %d shapes, want one text", got-before)
	}
	for _, id := range b.ed.Selection() {
		s := b.ed.Doc.Shapes[id]
		if s == nil || s.Type != doc.TypeText || s.Text != "从表格复制的文字" {
			t.Fatalf("pasted shape = %+v, want the text", s)
		}
	}
}

func TestBoardPasteTextShape(t *testing.T) {
	a, b := boardTestShelf(t)
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	view.SetClipboard("第一行\r\n第二行")
	before := b.ed.ItemCount()
	view.Key(ui.Cmd, ui.KeyV)
	view.Frame()
	if b.ed.ItemCount() != before+1 {
		t.Fatalf("items = %d, want %d", b.ed.ItemCount(), before+1)
	}
	id := b.ed.Selection()[0]
	s := b.ed.Doc.Shapes[id]
	if s == nil || s.Type != doc.TypeText || s.Text != "第一行\n第二行" {
		t.Fatalf("pasted text = %+v", s)
	}
	view.Key(ui.Cmd, ui.KeyZ)
	view.Frame()
	if b.ed.ItemCount() != before {
		t.Fatal("one undo did not remove the pasted text")
	}
	// Read-only boards do not paste.
	b.locked = true
	view.SetClipboard("只读不贴")
	view.Key(ui.Cmd, ui.KeyV)
	if b.ed.ItemCount() != before {
		t.Fatal("read-only board pasted text")
	}
	b.locked = false
	b.ed.CreateText(geo.V(120, 120), "Rakko 白板\n第一行 text\n第二行 文字")
	view.Frame()
	view.Frame()
	saveTestShot(t, "seg4", "paste-text.png", view.Image())
}

func TestBoardCompactMoreToolsMenu(t *testing.T) {
	l := locales.In("zh-Hans")
	for _, w := range []int{480, 560, 640, 760} {
		a, b := boardTestShelf(t)
		view := ui.NewTester(a.boardView, w, 620)
		view.SetLocale("zh-Hans")
		view.Frame()
		view.Frame()
		rows := []struct {
			label string
			tool  editor.Tool
		}{
			{l.BoardToolLasso(), editor.ToolLasso},
			{l.BoardToolLine(), editor.ToolLine},
			{l.BoardToolFrame(), editor.ToolFrame},
			{l.BoardToolNote(), editor.ToolNote},
		}
		for _, row := range rows {
			if err := view.Click(l.BoardMoreTools()); err != nil {
				t.Fatalf("w=%d open more tools: %v", w, err)
			}
			view.Frame()
			if err := view.Click(row.label); err != nil {
				t.Fatalf("w=%d click %q: %v", w, row.label, err)
			}
			view.Frame()
			if b.ed.Tool != row.tool {
				t.Fatalf("w=%d tool = %s, want %s", w, b.ed.Tool, row.tool)
			}
		}
		if err := view.Click(l.BoardMoreTools()); err != nil {
			t.Fatalf("w=%d open for lock: %v", w, err)
		}
		view.Frame()
		if err := view.Click(l.BoardKeepTool()); err != nil {
			t.Fatalf("w=%d click lock: %v", w, err)
		}
		if !b.ed.ToolLocked {
			t.Fatalf("w=%d lock did not toggle", w)
		}
		if w == 480 || w == 760 {
			if err := view.Click(l.BoardMoreTools()); err != nil {
				t.Fatalf("w=%d reopen for shot: %v", w, err)
			}
			view.Frame()
			saveTestShot(t, "seg4", "more-tools-"+strconv.Itoa(w)+".png", view.Image())
		}
	}
	// A wide window keeps the five tools in the toolbar itself.
	a, _ := boardTestShelf(t)
	view := ui.NewTester(a.boardView, 1040, 620)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	if err := view.Click(l.BoardMoreTools()); err == nil {
		t.Fatal("wide layout still shows the more-tools button")
	}
	for _, label := range []string{l.BoardToolLasso(), l.BoardToolLine(), l.BoardToolFrame(), l.BoardToolNote(), l.BoardKeepTool()} {
		if _, ok := view.Find(label); !ok {
			t.Fatalf("wide toolbar missing %q", label)
		}
	}
}

func TestBoardShiftDiscreteWheelPansHorizontally(t *testing.T) {
	_, b := boardTestShelf(t)
	before := b.ed.Cam()
	if !b.handleCanvasInput(nil, ui.InputEvent{Kind: ui.InputScroll, Mods: ui.Shift, DY: 50, X: 400, Y: 300}) {
		t.Fatal("Shift+wheel not consumed")
	}
	after := b.ed.Cam()
	if after.X == before.X || after.Y != before.Y {
		t.Fatalf("Shift+wheel pan = %+v, want horizontal only (before %+v)", after, before)
	}
}

func TestBoardDiscreteCmdWheelZooms(t *testing.T) {
	_, b := boardTestShelf(t)
	before := b.ed.Cam().Z
	if !b.handleCanvasInput(nil, ui.InputEvent{Kind: ui.InputScroll, Mods: ui.Cmd, DY: -40, X: 400, Y: 300}) {
		t.Fatal("Cmd+wheel not consumed")
	}
	if b.ed.Cam().Z <= before {
		t.Fatalf("Cmd+wheel zoom %v -> %v", before, b.ed.Cam().Z)
	}
}

func TestBoardPageNamesLocalizedAndUnique(t *testing.T) {
	a, b := boardTestShelfLang(t, "zh-Hans")
	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	if !view.HasText(l.BoardPageName("1")) {
		t.Fatalf("first page name not localized: %v", view.Texts())
	}
	p2 := b.ed.AddPage(b.nextPageName(l))
	p3 := b.ed.AddPage(b.nextPageName(l))
	if p2.Name != l.BoardPageName("2") || p3.Name != l.BoardPageName("3") {
		t.Fatalf("page names = %q %q", p2.Name, p3.Name)
	}
	if err := b.ed.DeletePage(p2.ID); err != nil {
		t.Fatal(err)
	}
	p4 := b.ed.AddPage(b.nextPageName(l))
	if p4.Name != l.BoardPageName("4") {
		t.Fatalf("after deleting middle page, new name = %q", p4.Name)
	}
	seen := map[string]bool{}
	for _, pg := range b.ed.Pages() {
		if seen[pg.Name] {
			t.Fatalf("duplicate page name %q", pg.Name)
		}
		seen[pg.Name] = true
	}

	// A new Frame gets the localized default name.
	b.ed.SetTool(editor.ToolFrame)
	b.ed.PointerDown(500, 300, 0, 1, editor.Mods{}, 2)
	b.ed.PointerMove(660, 420, editor.Mods{}, 2.1)
	b.ed.PointerUp(660, 420, editor.Mods{}, 2.2)
	var frame *doc.Shape
	for _, s := range b.ed.Doc.Shapes {
		if s.Type == doc.TypeFrame {
			frame = s
		}
	}
	if frame == nil || frame.Name != l.BoardFrameDefault() {
		t.Fatalf("frame name = %v, want %q", frame, l.BoardFrameDefault())
	}
}

func TestBoardCorruptFilePlaceholderTitle(t *testing.T) {
	a, b := boardTestShelf(t)
	if err := os.WriteFile(b.store.Path(), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.load(); err == nil {
		t.Fatal("expected error")
	}
	if !b.locked {
		t.Fatal("corrupt board not locked")
	}
	if b.ed.ItemCount() != 0 {
		t.Fatalf("placeholder has %d shapes", b.ed.ItemCount())
	}
	want := strings.TrimSuffix(filepath.Base(b.store.Path()), doc.FileExt)
	if got := b.ed.Doc.Name; got != want {
		t.Fatalf("placeholder title = %q, want %q", got, want)
	}
	if got := b.currentTitle(locales.In("")); got != want {
		t.Fatalf("window title = %q, want %q", got, want)
	}
	view := ui.NewTester(a.boardView, 900, 600)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	saveBoardSeg3Shot(t, "corrupt-placeholder.png", view.Image())
}

func TestBoardExportFileModes(t *testing.T) {
	a, b := boardTestShelf(t)
	addBoardRect(t, b)
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "board.png"),
		filepath.Join(dir, "board.svg"),
		filepath.Join(dir, "board.rakkoboard.json"),
	}
	i := 0
	orig := boardSaveFileDialog
	t.Cleanup(func() { boardSaveFileDialog = orig })
	boardSaveFileDialog = func(*mygo.Window, string, string, []string) (string, error) {
		p := paths[i]
		i++
		return p, nil
	}
	b.exportPNGFile(nil)
	b.exportSVGFile(nil)
	b.exportBoardFile()
	if i != 3 {
		t.Fatalf("export dialogs = %d, want 3", i)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Fatalf("%s mode = %o, want 644", filepath.Base(p), got)
		}
	}
	info, err := os.Stat(b.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("data board mode = %o, want 600", got)
	}
	_ = a
}

func TestBoardDragTempCleanup(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.png")
	fresh := filepath.Join(dir, "fresh.png")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(old, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	cleanBoardDragTemps(dir, now, 24*time.Hour)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old drag temp was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh drag temp was removed")
	}
}

func TestBoardPropTextIcons(t *testing.T) {
	a, b := boardTestShelf(t)
	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 760, 620)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	id := b.ed.CreateText(geo.V(200, 200), "图标测试")
	b.ed.Select(id)
	view.Frame()
	view.Frame()
	if err := view.Click(l.BoardPropBold()); err != nil {
		t.Fatalf("Bold icon click: %v", err)
	}
	if err := view.Click(l.BoardPropAlignCenter()); err != nil {
		t.Fatalf("Align Center icon click: %v", err)
	}
	view.Frame()
	if sh := b.ed.Doc.Shapes[id]; !sh.Bold || sh.TextAlign != "center" {
		t.Fatalf("icon clicks did not apply: bold=%v align=%q", sh.Bold, sh.TextAlign)
	}
	saveBoardSeg3Shot(t, "prop-text-icons-760.png", view.Image())
}

func TestBoardMaskOverlapViewport(t *testing.T) {
	view := geo.Box{X: 0, Y: 0, W: 100, H: 100}
	ib, ok := boardMaskOverlap(geo.Box{X: -1000, Y: -1000, W: 3000, H: 3000}, view)
	if !ok || ib != view {
		t.Fatalf("overlap = %+v ok=%v, want the viewport", ib, ok)
	}
	if _, ok := boardMaskOverlap(geo.Box{X: 200, Y: 200, W: 50, H: 50}, view); ok {
		t.Fatal("off-screen shape counted")
	}
	small, _ := boardMaskOverlap(geo.Box{X: 40, Y: 40, W: 20, H: 20}, view)
	if small.W != 20 || small.H != 20 {
		t.Fatalf("inside box clipped to %+v", small)
	}
}

func TestBoardBoldTextFitsSelectionBox(t *testing.T) {
	a, b := boardTestShelf(t)
	id := b.ed.CreateText(geo.V(200, 200), "Sketch 手写: Shantell + 悠哉 Yozai")
	b.ed.Select(id)
	b.ed.TogglePropBold()
	s := b.ed.Doc.Shapes[id]
	if !s.Bold {
		t.Fatal("text did not become bold")
	}
	regular := b.ed.Measure(s.Text, s.Font, 18, 0, 0, false)
	bold := b.ed.Measure(s.Text, s.Font, 18, 0, 700, false)
	if bold.W <= regular.W {
		t.Fatalf("bold width %.1f not wider than regular %.1f", bold.W, regular.W)
	}
	g := b.ed.Geometry(s)
	if g.Bounds.W+0.5 < bold.W {
		t.Fatalf("selection box %.1f narrower than bold text %.1f", g.Bounds.W, bold.W)
	}
	view := ui.NewTester(a.boardView, 760, 620)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	saveTestShot(t, "seg3", "bold-selection-760.png", view.Image())
}

func TestBoardInvalidateOnlyRepaintsBoardWindow(t *testing.T) {
	a, b := boardTestShelf(t)
	orig := boardPinRedraw
	redraws := 0
	boardPinRedraw = func(*pinItem) { redraws++ }
	t.Cleanup(func() { boardPinRedraw = orig })
	a.pins = []*pinItem{{cardID: "board"}}
	b.ed.SetPaper(doc.PaperGrid)
	if redraws != 0 {
		t.Fatalf("editing repainted %d pinned cards", redraws)
	}
	if err := b.flush(); err != nil {
		t.Fatal(err)
	}
	if redraws == 0 {
		t.Fatal("saving did not refresh the pinned card")
	}
}

func TestBoardImageLimitsAndBitmapLifecycle(t *testing.T) {
	a, b := boardTestShelf(t)
	big := downsampleImage(image.NewRGBA(image.Rect(0, 0, 3000, 1000)), 2048)
	if got := big.Bounds().Dx(); got != 2048 {
		t.Fatalf("downsampled width = %d", got)
	}
	dir := t.TempDir()
	huge := filepath.Join(dir, "huge.png")
	if err := os.WriteFile(huge, make([]byte, boardImageMaxByte+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if b.insertImageFile(huge, geo.V(10, 10)) {
		t.Fatal("oversized image accepted")
	}
	if b.ed.ItemCount() != 0 {
		t.Fatal("oversized image became a shape")
	}
	small := filepath.Join(dir, "small.png")
	if err := os.WriteFile(small, makeBoardTestPNG(40, 30, colorA, colorB), 0o600); err != nil {
		t.Fatal(err)
	}
	if !b.insertImageFile(small, geo.V(10, 10)) {
		t.Fatal("valid image rejected")
	}
	if len(b.bitmaps) != 1 {
		t.Fatalf("bitmaps = %d", len(b.bitmaps))
	}
	a.quitting = true
	b.onClose(nil)
	if len(b.bitmaps) != 0 {
		t.Fatalf("closing kept %d bitmaps", len(b.bitmaps))
	}
}

func TestBoardCardReadsMetaWithoutOpeningBoard(t *testing.T) {
	dir := t.TempDir()
	st, err := doc.Open(dir, "Board")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := st.Read()
	d.Name = "元数据白板"
	shape := doc.NewShape("shape:a", doc.TypeGeo)
	shape.Parent = d.ActivePage
	d.Shapes[shape.ID] = shape
	if err := st.Write(d); err != nil {
		t.Fatal(err)
	}
	a := &shelf{dataDir: dir, settings: settings.Default(), thumbnails: map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{}, appNames: map[string]string{}}
	view := ui.NewTester(func(c *ui.Context) {
		a.toolCard(c, layout.Card{ID: "board", Span: 2}, cardPanel)
	}, 320, 240)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	if a.board != nil {
		t.Fatal("rendering toolCard(board) parsed the whole board")
	}
	if _, ok := view.Find("元数据白板"); !ok {
		t.Fatalf("board card title missing from toolCard; texts=%v", view.Texts())
	}
}

func TestBoardExportDialogBeforeRenderAndDragLimit(t *testing.T) {
	_, b := boardTestShelf(t)
	// A board much wider than the drag preview limit.
	id := doc.NewRecordID("shape")
	s := doc.NewShape(id, doc.TypeGeo)
	s.Parent, s.W, s.H = b.ed.Page, 5000, 3000
	s.Index = doc.FirstIndex
	b.ed.Doc.Shapes[id] = s
	dir := t.TempDir()
	path, _, err := b.writeDragPNGTemp(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > boardDragMaxDim || cfg.Height > boardDragMaxDim {
		t.Fatalf("drag PNG = %dx%d, limit %d", cfg.Width, cfg.Height, boardDragMaxDim)
	}

	// A cancelled save dialog means no render and no file.
	orig := boardSaveFileDialog
	t.Cleanup(func() { boardSaveFileDialog = orig })
	calls := 0
	boardSaveFileDialog = func(*mygo.Window, string, string, []string) (string, error) {
		calls++
		return "", nil
	}
	b.exportPNGFile(nil)
	if calls != 1 {
		t.Fatalf("dialog calls = %d", calls)
	}
}

func TestBoardFontButtonsCompleteAtNarrowWidths(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		l := locales.In(lang)
		names := []string{l.BoardPropFontSketch(), l.BoardPropFontSans(), l.BoardPropFontSerif(), l.BoardPropFontMono()}
		for _, w := range []int{480, 760} {
			a, b := boardTestShelf(t)
			view := ui.NewTester(a.boardView, w, 620)
			view.SetLocale(lang)
			view.Frame()
			view.Frame()
			id := b.ed.CreateText(geo.V(120, 120), "字体")
			b.ed.Select(id)
			view.Frame()
			view.Frame()
			for _, name := range names {
				if _, ok := view.Find(name); !ok {
					t.Fatalf("%s w=%d: font label %q missing; texts=%v", lang, w, name, view.Texts())
				}
			}
			saveTestShot(t, "seg4", "font-row-"+strconv.Itoa(w)+"-"+lang+".png", view.Image())
		}
	}
}

func TestRenderCloneConcurrentPaintNoDataRace(t *testing.T) {
	_, b := boardTestShelf(t)
	idx := doc.FirstIndex
	for i := range 12 {
		id := doc.NewRecordID("shape")
		s := doc.NewShape(id, doc.TypeGeo)
		s.Parent, s.X, s.Y, s.W, s.H = b.ed.Page, float64(i*40), 20, 80, 60
		s.Index = idx
		idx = doc.IndexBetween(idx, "")
		b.ed.Doc.Shapes[id] = s
	}
	arrowID := doc.NewRecordID("shape")
	ar := doc.NewShape(arrowID, doc.TypeArrow)
	ar.Parent, ar.Start, ar.End = b.ed.Page, geo.V(10, 10), geo.V(300, 120)
	ar.Index = doc.IndexBetween(idx, "")
	b.ed.Doc.Shapes[arrowID] = ar
	b.ed.CreateText(geo.V(50, 200), "concurrent export")
	for _, s := range b.ed.Doc.Shapes {
		s.Cache = nil
	}

	clone := b.renderClone()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, _, _, _, err := clone.renderTiledPNG(nil); err != nil {
			t.Errorf("renderTiledPNG: %v", err)
		}
	}()

	pal := b.ed.Palette()
	for range 50 {
		for _, id := range b.ed.Children(b.ed.Page) {
			s := b.ed.Doc.Shapes[id]
			if s == nil {
				continue
			}
			b.ed.Geometry(s)
			b.ed.Display(s, pal)
		}
	}
	wg.Wait()
}

func TestBoardImagePixelLimitExifOrientAndDialogExts(t *testing.T) {
	_, b := boardTestShelf(t)
	dir := t.TempDir()

	// 1. Small-byte PNG whose dimensions exceed boardImageMaxPx (100M px).
	bomb := image.NewGray(image.Rect(0, 0, 10100, 10100))
	var bombBuf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&bombBuf, bomb); err != nil {
		t.Fatal(err)
	}
	if bombBuf.Len() >= boardImageMaxByte {
		t.Fatalf("compressed test PNG too large: %d", bombBuf.Len())
	}
	bombPath := filepath.Join(dir, "bomb.png")
	if err := os.WriteFile(bombPath, bombBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if b.insertImageFile(bombPath, geo.V(50, 50)) {
		t.Fatal("insertImageFile accepted a >100M pixel PNG")
	}
	if b.ed.ItemCount() != 0 {
		t.Fatal("pixel-bomb PNG created a shape")
	}

	// 2. Landscape 40x20 JPEG with EXIF Orientation=6 (90 deg CW -> 20x40 portrait).
	src := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			c := color.NRGBA{R: 20, G: 40, B: 220, A: 255}
			if x < 10 && y < 10 {
				c = color.NRGBA{R: 240, G: 30, B: 30, A: 255}
			}
			src.SetNRGBA(x, y, c)
		}
	}
	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, src, &jpeg.Options{Quality: 98}); err != nil {
		t.Fatal(err)
	}
	jpgBytes := jpgBuf.Bytes()
	app1 := []byte("Exif\x00\x00II\x2a\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00")
	var seg [4]byte
	seg[0], seg[1] = 0xFF, 0xE1
	binary.BigEndian.PutUint16(seg[2:4], uint16(len(app1)+2))
	var withExif bytes.Buffer
	withExif.Write(jpgBytes[:2])
	withExif.Write(seg[:])
	withExif.Write(app1)
	withExif.Write(jpgBytes[2:])

	exifPath := filepath.Join(dir, "portrait.jpg")
	if err := os.WriteFile(exifPath, withExif.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if !b.insertImageFile(exifPath, geo.V(120, 120)) {
		t.Fatal("insertImageFile rejected valid EXIF JPEG")
	}
	var imgShape *doc.Shape
	for _, s := range b.ed.Doc.Shapes {
		if s.Type == doc.TypeImage {
			imgShape = s
			break
		}
	}
	if imgShape == nil {
		t.Fatal("no image shape created")
	}
	asset := b.ed.Doc.Assets[imgShape.AssetID]
	if asset == nil || asset.W != 20 || asset.H != 40 {
		t.Fatalf("EXIF oriented asset dims = %+v, want 20x40", asset)
	}

	// 3. Dialog extension filter must match boardIsSupportedImageExt.
	for _, ext := range []string{"bmp", "tif", "tiff", "heic", "heif", "avif"} {
		if !slices.Contains(boardSupportedImageExts, ext) {
			t.Fatalf("boardSupportedImageExts missing %q", ext)
		}
	}
}

func TestBoardContextMenuPasteUsesSystemClipboard(t *testing.T) {
	a, b := boardTestShelf(t)
	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	// Blank canvas with empty internal clipboard + external clipboard text.
	view.SetClipboard("右键粘贴文字")
	view.RightClickAt(320, 260)
	view.Frame()
	if err := view.ChooseMenuItem(l.BoardCtxPaste()); err != nil {
		t.Fatalf("context menu paste with external text failed: %v", err)
	}
	view.Frame()
	if b.ed.ItemCount() != 1 {
		t.Fatalf("items after blank right-click paste = %d, want 1", b.ed.ItemCount())
	}
	pasted := b.ed.Doc.Shapes[b.ed.Selection()[0]]
	if pasted == nil || pasted.Type != doc.TypeText || pasted.Text != "右键粘贴文字" {
		t.Fatalf("pasted shape = %+v, want text 右键粘贴文字", pasted)
	}

	// Copy internal shape, then overwrite system clipboard externally:
	// right-click paste on selection must paste the newer external text.
	r := b.ed.CreateNote(geo.V(120, 120), "旧便签")
	b.ed.Select(r)
	view.Frame()
	view.Key(ui.Cmd, ui.KeyC)
	view.Frame()
	view.SetClipboard("外部最新文本")
	view.RightClickAt(150, 150)
	view.Frame()
	if err := view.ChooseMenuItem(l.BoardCtxPaste()); err != nil {
		t.Fatalf("selection context menu paste failed: %v", err)
	}
	view.Frame()
	sel := b.ed.Selection()
	if len(sel) == 0 {
		t.Fatal("nothing selected after paste")
	}
	if s := b.ed.Doc.Shapes[sel[0]]; s == nil || s.Type != doc.TypeText || s.Text != "外部最新文本" {
		t.Fatalf("selection right-click paste got %+v, want 外部最新文本", s)
	}
}

func TestBoardPageMenusAndDeleteConfirmUseLocalizedTitle(t *testing.T) {
	a, b := boardTestShelf(t)
	b.ed.AddPage("")
	p1 := b.ed.Pages()[0]
	b.ed.SwitchPage(p1.ID)
	id := b.ed.CreateNote(geo.V(100, 100), "测试")
	b.ed.Select(id)

	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	view.RightClickAt(140, 140)
	view.Frame()
	if err := view.ChooseMenuItem(l.BoardPageMoveTo(), "页面 2"); err != nil {
		t.Fatalf("MoveTo submenu did not show localized '页面 2': %v", err)
	}
	view.Frame()
	if len(b.ed.Pages()) < 2 || b.ed.Doc.Shapes[id].Parent != b.ed.Pages()[1].ID {
		t.Fatal("shape was not moved to page 2")
	}

	b.ed.SwitchPage(p1.ID)
	b.confirmPageDelete = true
	view.Frame()
	view.Frame()
	wantConfirm := l.BoardPageDeleteConfirm("页面 1")
	if _, ok := view.Find(wantConfirm); !ok {
		t.Fatalf("delete confirm modal missing localized %q; texts=%v", wantConfirm, view.Texts())
	}
}
