package main

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
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
	pngData := makeNoteTestPNG(40, 30, colorA, colorB)
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
	boardReadClipboardImage = func() []byte { return makeNoteTestPNG(20, 20, colorA, colorB) }
	view.SetClipboard("从表格复制的文字")
	before := b.ed.ItemCount()
	view.Key(ui.Cmd, ui.KeyV)
	view.Frame()
	if got := b.ed.ItemCount(); got != before {
		t.Fatalf("text+image clipboard inserted %d items", got-before)
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
	g := b.ed.Geometry(s)
	var block *editor.TextBlock
	for _, pr := range b.ed.Display(s, b.ed.Palette()) {
		if pr.Kind == editor.PrimText && pr.Block != nil {
			blk := *pr.Block
			block = &blk
		}
	}
	if block == nil {
		t.Fatal("no text primitive")
	}
	for _, line := range block.Lines {
		if line.X+line.W > g.Bounds.W+0.5 {
			t.Fatalf("bold line %q ends at %.1f, box width %.1f", line.Text, line.X+line.W, g.Bounds.W)
		}
	}
	if block.W > g.Bounds.W+0.5 {
		t.Fatalf("block width %.1f, box %.1f", block.W, g.Bounds.W)
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
	if err := os.WriteFile(small, makeNoteTestPNG(40, 30, colorA, colorB), 0o600); err != nil {
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
		card := ui.Column(c.Key("card")).Width(300).Height(120)
		a.noteBody(c, card, 2, cardPanel)
		a.boardBody(c, card, 2, cardPanel)
	}, 320, 240)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	if a.board != nil {
		t.Fatal("drawing the board card parsed the whole board")
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
