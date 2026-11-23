package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
	"rakko/internal/layout"
	"rakko/internal/settings"
	"rakko/internal/store"
	"rakko/locales"
)

func boardTestShelf(t *testing.T) (*shelf, *boardUI) {
	t.Helper()
	a := &shelf{
		dataDir:    t.TempDir(),
		settings:   settings.Default(),
		thumbnails: map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:   map[string]string{},
	}
	b := a.boardSession()
	if b == nil {
		t.Fatal("boardSession returned nil")
	}
	return a, b
}

func saveBoardShot(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	dir := os.Getenv("RAKKO_SEG1_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestBoardSaveDebounceAndFlush(t *testing.T) {
	_, b := boardTestShelf(t)
	base := time.Now()

	b.ed.SetPaper(doc.PaperGrid)
	if !b.dirty() {
		t.Fatal("expected board to be dirty after SetPaper")
	}
	if b.tick(base) {
		t.Fatal("wrote on first tick before debounce elapsed")
	}
	if b.tick(base.Add(300 * time.Millisecond)) {
		t.Fatal("wrote at 300ms before 400ms debounce")
	}

	// Another change resets the 400ms timer.
	b.ed.SetDarkMode(false)
	b.tick(base.Add(350 * time.Millisecond))
	if b.tick(base.Add(650 * time.Millisecond)) {
		t.Fatal("wrote 300ms after second change")
	}
	if !b.tick(base.Add(800 * time.Millisecond)) {
		t.Fatal("did not write 450ms after last change")
	}
	if b.dirty() {
		t.Fatal("board remained dirty after debounced save")
	}

	// Reload from disk verifies persistence.
	reloaded, err := b.store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Paper != doc.PaperGrid || reloaded.Theme != doc.ThemeLight {
		t.Fatalf("persisted paper=%q theme=%q, want grid/light", reloaded.Paper, reloaded.Theme)
	}

	// Flush on blur/close writes immediately without waiting 400ms.
	b.ed.SetPaper(doc.PaperLines)
	b.onBlur()
	if b.dirty() {
		t.Fatal("onBlur left dirty changes")
	}
	reloaded, err = b.store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Paper != doc.PaperLines {
		t.Fatalf("after onBlur paper=%q, want lines", reloaded.Paper)
	}
}

func TestBoardCorruptFileLocksEditing(t *testing.T) {
	a, b := boardTestShelf(t)
	corrupt := []byte("{not-json")
	if err := os.WriteFile(b.store.Path(), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.load(); err == nil {
		t.Fatal("expected error loading corrupt board")
	}
	if !b.locked {
		t.Fatal("expected board to be locked on read failure")
	}
	view := ui.NewTester(a.boardView, 800, 500)
	view.Frame()
	if !view.HasText(locales.In("").BoardUnreadable()) {
		t.Fatalf("locked view missing unreadable notice; texts=%v", view.Texts())
	}
	got, _ := os.ReadFile(b.store.Path())
	if !bytes.Equal(got, corrupt) {
		t.Fatal("corrupt board file was overwritten")
	}
}

func TestBoardSwitcherNewRenameAndTrash(t *testing.T) {
	origTrash := boardTrash
	var trashed []string
	boardTrash = func(p string) (string, error) {
		trashed = append(trashed, p)
		return p, os.Remove(p)
	}
	t.Cleanup(func() { boardTrash = origTrash })

	a, b := boardTestShelf(t)
	firstID := b.store.ID()
	l := locales.In("")

	// Create a second board via UI / store flow.
	if err := b.newBoardDoc(); err != nil {
		t.Fatal(err)
	}
	secondID := b.store.ID()
	if secondID == firstID {
		t.Fatal("newBoardDoc did not switch to a new board")
	}
	if len(b.boards) != 2 {
		t.Fatalf("boards len = %d, want 2", len(b.boards))
	}

	view := ui.NewTester(a.boardView, 900, 600)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	// Rename via startRename + typing Enter.
	b.startRename()
	view.Frame()
	view.Frame()
	view.Key(ui.Cmd, ui.KeyA)
	view.Type("架构草图")
	view.Key(0, ui.KeyEnter)
	view.Frame()
	if b.ed.Doc.Name != "架构草图" {
		t.Fatalf("renamed board = %q, want 架构草图", b.ed.Doc.Name)
	}
	reloaded, err := b.store.Read()
	if err != nil || reloaded.Name != "架构草图" {
		t.Fatalf("saved board name = %q, err=%v", reloaded.Name, err)
	}

	// Switch back to first board via switcher dropdown.
	if err := view.Click("架构草图"); err != nil {
		t.Fatalf("open switcher menu: %v", err)
	}
	view.Frame()
	if err := view.ChooseMenuItem(l.BoardDefaultName()); err != nil {
		t.Fatalf("choose first board in menu: %v (menu=%v)", err, view.Menu())
	}
	view.Frame()
	if b.store.ID() != firstID {
		t.Fatalf("active board after menu switch = %s, want %s", b.store.ID(), firstID)
	}

	// Delete first board via switcher menu + confirmation modal.
	if err := view.Click(l.BoardDefaultName()); err != nil {
		t.Fatalf("reopen switcher menu: %v", err)
	}
	view.Frame()
	if err := view.ChooseMenuItem(l.BoardDelete(l.BoardDefaultName())); err != nil {
		t.Fatalf("choose delete in menu: %v", err)
	}
	view.Frame()
	if !b.confirmDelete {
		t.Fatal("expected delete confirmation modal to open")
	}
	if err := view.Click(osText(l.BoardMoveToTrash(), l.BoardMoveToTrashWin())); err != nil {
		t.Fatalf("confirm trash: %v", err)
	}
	view.Frame()
	if len(trashed) != 1 || filepath.Base(trashed[0]) != firstID+doc.FileExt {
		t.Fatalf("trashed = %v, want %s", trashed, firstID)
	}
	if b.store.ID() != secondID {
		t.Fatalf("after trash active board = %s, want %s", b.store.ID(), secondID)
	}
}

func TestBoardPanAndZoomInput(t *testing.T) {
	a, b := boardTestShelf(t)
	view := ui.NewTester(a.boardView, 960, 640)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	// Scroll pans the canvas.
	view.Scroll(480, 320, 80, 60)
	view.Frame()
	camAfterPan := b.ed.Cam()
	if camAfterPan.X == 0 || camAfterPan.Y == 0 {
		t.Fatalf("Scroll did not pan camera: %+v", camAfterPan)
	}

	// Pinch (macOS trackpad / touchscreen InputZoom) zooms around pointer.
	view.Pinch(480, 320, 1.5)
	view.Frame()
	if math.Abs(b.ed.Cam().Z-1.5) > 0.02 {
		t.Fatalf("Pinch zoom = %v, want ~1.5", b.ed.Cam().Z)
	}

	// Ctrl+Wheel (Windows zoom gesture via InputScroll with Ctrl modifier) zooms around pointer.
	zBefore := b.ed.Cam().Z
	if !b.handleCanvasInput(nil, ui.InputEvent{Kind: ui.InputScroll, Mods: ui.Ctrl, X: 480, Y: 320, DY: -40}) {
		t.Fatal("handleCanvasInput did not consume Ctrl+InputScroll")
	}
	view.Frame()
	if b.ed.Cam().Z <= zBefore {
		t.Fatalf("Ctrl+Wheel zoom did not zoom in: before=%v after=%v", zBefore, b.ed.Cam().Z)
	}

	// Plain mouse wheel without modifier pans vertically.
	cWheelBefore := b.ed.Cam()
	view.Wheel(480, 320, 0, 50)
	view.Frame()
	if b.ed.Cam().Y == cWheelBefore.Y {
		t.Fatalf("Wheel did not pan camera: %+v", b.ed.Cam())
	}

	// Space + drag pans the canvas.
	cBefore := b.ed.Cam()
	view.KeyDown(ui.KeySpace)
	view.Press(400, 300)
	view.Move(460, 340)
	view.Release(460, 340)
	view.KeyUp(ui.KeySpace)
	view.Frame()
	if b.ed.Cam().X == cBefore.X || b.ed.Cam().Y == cBefore.Y {
		t.Fatalf("Space+drag did not pan: before=%+v after=%+v", cBefore, b.ed.Cam())
	}

	// Zoom reset button / menu resets to 100%.
	if err := view.Click(locales.In("").BoardZoomLabel()); err != nil {
		t.Fatalf("click zoom pill: %v", err)
	}
	view.Frame()
	if err := view.ChooseMenuItem(locales.In("").BoardZoomReset()); err != nil {
		t.Fatalf("choose zoom reset: %v", err)
	}
	view.Frame()
	if b.ed.Cam().Z != 1.0 {
		t.Fatalf("zoom after reset = %v, want 1.0", b.ed.Cam().Z)
	}
}

func TestBoardPapersThemesAndScreenshots(t *testing.T) {
	a, b := boardTestShelf(t)
	view := ui.NewTester(a.boardView, 960, 620)
	view.SetLocale("zh-Hans")

	papers := []string{doc.PaperDots, doc.PaperGrid, doc.PaperLines, doc.PaperBlank}
	themes := []struct {
		name string
		dark bool
	}{
		{"dark", true},
		{"light", false},
	}

	pixMap := map[string][]byte{}
	for _, th := range themes {
		b.setDarkMode(th.dark)
		for _, p := range papers {
			b.ed.SetPaper(p)
			view.Frame()
			view.Frame()
			img := view.Image()
			key := th.name + "-" + p
			pixMap[key] = bytes.Clone(img.Pix)
			saveBoardShot(t, "board-"+key+".png", img)
		}
	}

	// All 4 papers in dark theme must produce distinct pixels, and dark != light.
	for i := 0; i < len(papers); i++ {
		for j := i + 1; j < len(papers); j++ {
			aKey := "dark-" + papers[i]
			bKey := "dark-" + papers[j]
			if bytes.Equal(pixMap[aKey], pixMap[bKey]) {
				t.Fatalf("dark papers %s and %s rendered identical pixels", papers[i], papers[j])
			}
		}
		if bytes.Equal(pixMap["dark-"+papers[i]], pixMap["light-"+papers[i]]) {
			t.Fatalf("paper %s dark and light rendered identical pixels", papers[i])
		}
	}

	// Capture titlebar switcher dropdown menu screenshot.
	b.setDarkMode(true)
	b.ed.SetPaper(doc.PaperDots)
	view.Frame()
	if err := view.Click(b.currentTitle(locales.In("zh-Hans"))); err != nil {
		t.Fatalf("open title switcher: %v", err)
	}
	view.Frame()
	saveBoardShot(t, "board-title-menu.png", view.Image())
	view.CloseMenu()

	// Render tool card in 1x1, 2x1, and pinned desktop forms.
	cardView := ui.NewTester(func(c *ui.Context) {
		ui.Column(c.Key("board-cards-demo")).Fill().Padding(16).Gap(12).Background(surface).Children(func() {
			ui.Row(c).Gap(12).Children(func() {
				a.toolCard(c, layout.Card{ID: "board", Span: 1, Rows: 1}, cardPanel, "p1").Width(toolColW(1)).Height(84)
				a.toolCard(c, layout.Card{ID: "board", Span: 2, Rows: 1}, cardPanel, "p2").Width(toolColW(2)).Height(84)
			})
			a.toolCard(c, layout.Card{ID: "board", Span: 2, Rows: 1}, cardDesktop, "desk").Width(pinCardW2).Height(84)
		})
	}, 420, 230)
	cardView.SetLocale("zh-Hans")
	cardView.Frame()
	cardView.Frame()
	saveBoardShot(t, "tools-card-board.png", cardView.Image())
}

func TestBoardRakkoEntriesAndShortcut(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &shelf{
		dataDir:    dir,
		store:      st,
		settings:   settings.Default(),
		thumbnails: map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:   map[string]string{},
	}

	// 1. Tool card catalog entry.
	def := toolCardDefFor("board")
	if def == nil {
		t.Fatal("board card missing from toolCatalog")
	}
	if got := def.name(locales.In("zh-Hans")); got != "白板" {
		t.Fatalf("zh-Hans card name = %q", got)
	}
	if got := def.name(locales.In("en")); got != "Whiteboard" {
		t.Fatalf("en card name = %q", got)
	}
	if sizes := cardSizes("board"); !slices.Equal(sizes, [][2]int{{1, 1}, {2, 1}}) {
		t.Fatalf("cardSizes(board) = %v", sizes)
	}

	// 2. Home search "新建白板" action.
	origOpen := boardNewWindow
	origShow, origAct, origFocus, origCfg := boardShow, boardActivate, boardFocus, boardConfigureWindow
	var createdOpts *mygo.WindowOptions
	boardNewWindow = func(opts mygo.WindowOptions) *mygo.Window {
		createdOpts = &opts
		return nil
	}
	boardShow = func(*mygo.Window) {}
	boardActivate = func() {}
	boardFocus = func(*mygo.Window) {}
	boardConfigureWindow = func(uintptr, bool) {}
	t.Cleanup(func() {
		boardNewWindow = origOpen
		boardShow = origShow
		boardActivate = origAct
		boardFocus = origFocus
		boardConfigureWindow = origCfg
	})
	// Avoid nil window dereference in open() when boardNewWindow returns nil in this seam test:
	// test newBoardDoc + homeHits separately, and test toggleBoard via seam.
	a.homeQuery = "新建白板"
	hits := a.computeHomeHits()
	foundAction := false
	for _, h := range hits {
		if h.group == a.appLoc().HomeGroupActions() && h.title == a.appLoc().HomeActionNewBoard() {
			foundAction = true
			break
		}
	}
	if !foundAction {
		t.Fatalf("homeHits(%q) missing %q action: %+v", a.homeQuery, a.appLoc().HomeActionNewBoard(), hits)
	}

	// 3. Settings shortcut defaults to empty and maps to Settings.Shortcuts.Board.
	if defAcc := settings.Default().Shortcuts.Board; defAcc != "" {
		t.Fatalf("default Board shortcut = %q, want empty", defAcc)
	}
	if shortcutBoard.field(&a.settings.Shortcuts) != &a.settings.Shortcuts.Board {
		t.Fatal("shortcutBoard.field mismatch")
	}
	if a.shortcutRun(shortcutBoard) == nil {
		t.Fatal("shortcutBoard action is nil")
	}

	// 4. Settings Shortcuts page displays the New Board shortcut row.
	a.settingsPage = settingsPageShortcuts
	sv := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	sv.SetLocale("zh-Hans")
	sv.Frame()
	for range 30 {
		sv.Scroll(settingsWidth/2, float32(settingsHeight)/2, 0, 400)
		sv.Frame()
	}
	if !sv.HasText(a.appLoc().SettingsShortcutNewBoard()) {
		t.Fatalf("settings shortcuts page missing %q; texts=%v", a.appLoc().SettingsShortcutNewBoard(), sv.Texts())
	}
	_ = createdOpts
	_ = strings.Contains
}

func saveBoardSeg2Shot(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	dir := os.Getenv("RAKKO_SEG2_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{R: 59, G: 130, B: 246, A: 255}
			if (x/16+y/16)%2 == 0 {
				c = color.RGBA{R: 96, G: 165, B: 250, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBoardSeg2ToolsUndoImageAndScreenshots(t *testing.T) {
	a, b := boardTestShelf(t)
	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	// 1. Click Draw toolbar button, switch pen kind in sub-bar, and draw a stroke.
	if err := view.Click(l.BoardToolDraw()); err != nil {
		t.Fatalf("click Draw tool: %v", err)
	}
	view.Frame()
	if b.ed.Tool != editor.ToolDraw {
		t.Fatalf("tool after Draw click = %s", b.ed.Tool)
	}
	if err := view.Click(l.BoardPenFountain()); err != nil {
		t.Fatalf("click PenFountain: %v", err)
	}
	view.Frame()
	if b.ed.Next.PenKind != "pen" {
		t.Fatalf("PenKind = %q, want pen", b.ed.Next.PenKind)
	}
	view.Press(200, 260)
	view.Move(300, 260)
	view.Move(420, 260)
	view.Release(420, 260)
	view.Frame()
	if b.ed.ItemCount() != 1 {
		t.Fatalf("ItemCount after stroke = %d, want 1", b.ed.ItemCount())
	}

	// Capture Draw sub-bar screenshot while visible before undo/erase test.
	saveBoardSeg2Shot(t, "board-tools-draw-subbar.png", view.Image())

	// 2. Eraser Partial mode splits the horizontal stroke in two, Undo/Redo restores/re-applies.
	if err := view.Click(l.BoardToolEraser()); err != nil {
		t.Fatalf("click Eraser tool: %v", err)
	}
	view.Frame()
	if err := view.Click(l.BoardEraserPartial()); err != nil {
		t.Fatalf("click Partial eraser: %v", err)
	}
	view.Frame()
	view.Press(310, 220)
	view.Move(310, 300)
	view.Release(310, 300)
	view.Frame()
	if b.ed.ItemCount() != 2 {
		t.Fatalf("ItemCount after partial erase split = %d, want 2", b.ed.ItemCount())
	}
	if err := view.Click(l.BoardUndo()); err != nil {
		t.Fatalf("click Undo: %v", err)
	}
	view.Frame()
	if b.ed.ItemCount() != 1 {
		t.Fatalf("ItemCount after Undo button = %d, want 1", b.ed.ItemCount())
	}
	if err := view.Click(l.BoardRedo()); err != nil {
		t.Fatalf("click Redo: %v", err)
	}
	view.Frame()
	if b.ed.ItemCount() != 2 {
		t.Fatalf("ItemCount after Redo button = %d, want 2", b.ed.ItemCount())
	}

	// 3. Shape picker popover lists and chooses among all 27 geometry shapes.
	if err := view.Click(l.BoardToolShape()); err != nil {
		t.Fatalf("open Shape popover: %v", err)
	}
	view.Frame()
	if !b.shapeMenu {
		t.Fatal("expected shapeMenu popover to open")
	}
	saveBoardSeg2Shot(t, "board-tools-shape-menu.png", view.Image())
	if err := view.Click(l.BoardShapeDiamond()); err != nil {
		t.Fatalf("choose Diamond: %v", err)
	}
	view.Frame()
	if b.ed.Tool != editor.ToolGeo || b.ed.Next.Geo != "diamond" {
		t.Fatalf("tool=%s geo=%s after picking diamond", b.ed.Tool, b.ed.Next.Geo)
	}
	view.Press(520, 200)
	view.Move(640, 300)
	view.Release(640, 300)
	view.Frame()
	if b.ed.ItemCount() != 3 {
		t.Fatalf("ItemCount after creating diamond = %d, want 3", b.ed.ItemCount())
	}

	// 4. Keyboard shortcuts (Shift+D highlight, R rect, Cmd+Z undo, Cmd+V image paste, Cmd+U file dialog).
	view.Key(ui.Shift, ui.KeyD)
	view.Frame()
	if b.ed.Tool != editor.ToolHighlight {
		t.Fatalf("Shift+D tool = %s, want highlight", b.ed.Tool)
	}

	pngBytes := makeTestPNG(t, 128, 96)
	origClip := boardReadClipboardImage
	boardReadClipboardImage = func() []byte { return pngBytes }
	t.Cleanup(func() { boardReadClipboardImage = origClip })
	view.Key(ui.Cmd, ui.KeyV)
	view.Frame()
	if b.ed.ItemCount() != 4 {
		t.Fatalf("ItemCount after Cmd+V image paste = %d, want 4", b.ed.ItemCount())
	}

	tmpImg := filepath.Join(t.TempDir(), "sample.png")
	if err := os.WriteFile(tmpImg, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	origPick := boardOpenImageDialog
	boardOpenImageDialog = func(*mygo.Window) ([]string, error) { return []string{tmpImg}, nil }
	t.Cleanup(func() { boardOpenImageDialog = origPick })
	view.Key(ui.Cmd, ui.KeyU)
	view.Frame()
	if b.ed.ItemCount() != 5 {
		t.Fatalf("ItemCount after Cmd+U image insert = %d, want 5", b.ed.ItemCount())
	}
	if len(b.ed.Doc.Assets) != 1 {
		t.Fatalf("identical pasted & opened image should deduplicate to 1 asset, got %d", len(b.ed.Doc.Assets))
	}

	// 5. Render Seg 2 showcase screenshots if requested.
	if os.Getenv("RAKKO_SEG2_SHOTS") != "" {
		// Showcase A: All 27 geometry shapes laid out cleanly in a 7x4 grid.
		a2, b2 := boardTestShelf(t)
		v2 := ui.NewTester(a2.boardView, 1040, 680)
		v2.SetLocale("zh-Hans")
		const cols = 7
		const cellW, cellH = 128.0, 122.0
		const padX, padY = 66.0, 56.0
		fills := []string{"semi", "solid", "pattern", "none"}
		strokes := []string{"#60A5FA", "#34D399", "#F59E0B", "#EC4899", "#A78BFA"}
		for i, kind := range editor.GeoKinds {
			col := i % cols
			row := i / cols
			x := padX + float64(col)*cellW
			y := padY + float64(row)*cellH
			b2.ed.SetGeoTool(kind)
			b2.ed.Next.Fill = fills[i%len(fills)]
			b2.ed.Next.FillColor = strokes[i%len(strokes)]
			b2.ed.Next.Stroke = strokes[i%len(strokes)]
			b2.ed.Next.Size = "m"
			b2.ed.PointerDown(x, y, 0, 1, editor.Mods{}, float64(i)*0.1)
			b2.ed.PointerMove(x+96, y+68, editor.Mods{}, float64(i)*0.1+0.02)
			b2.ed.PointerUp(x+96, y+68, editor.Mods{}, float64(i)*0.1+0.04)
		}
		b2.ed.SelectNone()
		b2.ed.SetTool(editor.ToolSelect)
		v2.Frame()
		v2.Frame()
		saveBoardSeg2Shot(t, "board-all-27-shapes.png", v2.Image())

		// Showcase B: Brushes, highlight layer order, partial erase split, frame clip, sticky note, text, image.
		a3, b3 := boardTestShelf(t)
		v3 := ui.NewTester(a3.boardView, 1040, 680)
		v3.SetLocale("zh-Hans")

		// Frame with overflowing clipped child.
		b3.ed.SetTool(editor.ToolFrame)
		b3.ed.PointerDown(60, 70, 0, 1, editor.Mods{}, 0.1)
		b3.ed.PointerMove(390, 290, editor.Mods{}, 0.2)
		b3.ed.PointerUp(390, 290, editor.Mods{}, 0.3)

		b3.ed.SetGeoTool("rounded-rect")
		b3.ed.Next.Fill = "semi"
		b3.ed.PointerDown(250, 130, 0, 1, editor.Mods{}, 0.4)
		b3.ed.PointerMove(440, 240, editor.Mods{}, 0.5)
		b3.ed.PointerUp(440, 240, editor.Mods{}, 0.6)

		// Inserted image inside Frame.
		b3.insertImageBytes(pngBytes, "png", "badge.png", geo.V(160, 175))

		// Highlight under a star shape.
		b3.ed.Next.Fill = "solid"
		b3.ed.SetGeoTool("star")
		b3.ed.PointerDown(520, 100, 0, 1, editor.Mods{}, 0.7)
		b3.ed.PointerMove(640, 220, editor.Mods{}, 0.8)
		b3.ed.PointerUp(640, 220, editor.Mods{}, 0.9)

		b3.ed.SetTool(editor.ToolHighlight)
		b3.ed.PointerDown(490, 160, 0, 1, editor.Mods{}, 1.0)
		b3.ed.PointerMove(670, 160, editor.Mods{}, 1.1)
		b3.ed.PointerUp(670, 160, editor.Mods{}, 1.2)

		// Sticky Note + Text.
		b3.ed.CreateNote(geo.V(820, 165), "Frame & Highlight & Partial Erase")
		b3.ed.CreateText(geo.V(720, 320), "Rakko v1186 Canvas")

		// 3 Pen kinds + Shift straight polyline + auto-closed loop.
		pens := []string{"normal", "pen", "marker"}
		for pi, pk := range pens {
			y0 := 370.0 + float64(pi)*55.0
			b3.ed.SetTool(editor.ToolDraw)
			b3.ed.SetPenKind(pk)
			b3.ed.PointerDownPressure(90, y0, 0, 1, 0.3, true, editor.Mods{}, 2.0+float64(pi))
			b3.ed.PointerMovePressure(220, y0-18, 0.9, true, editor.Mods{}, 2.1+float64(pi))
			b3.ed.PointerMovePressure(360, y0+14, 0.5, true, editor.Mods{}, 2.2+float64(pi))
			b3.ed.PointerUp(360, y0+14, editor.Mods{}, 2.3+float64(pi))
		}
		// Partial erase splits the marker & pen strokes vertically.
		b3.ed.SetTool(editor.ToolEraser)
		b3.ed.SetEraserMode(editor.EraserPartial)
		b3.ed.PointerDown(225, 340, 0, 1, editor.Mods{}, 5.0)
		b3.ed.PointerMove(225, 510, editor.Mods{}, 5.1)
		b3.ed.PointerUp(225, 510, editor.Mods{}, 5.2)

		b3.ed.SetTool(editor.ToolSelect)
		b3.ed.AgeTrails(6.0)
		v3.Frame()
		v3.Frame()
		saveBoardSeg2Shot(t, "board-brushes-partial-erase.png", v3.Image())
	}
}

func saveBoardSeg3Shot(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	dir := os.Getenv("RAKKO_SEG3_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestBoardSeg3SelectionSnapLabelsAndScreenshots(t *testing.T) {
	a, b := boardTestShelf(t)
	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	// 1. Create two rectangles, click Lasso button & verify S shortcut, select via Lasso.
	b.ed.SetGeoTool("rectangle")
	b.ed.PointerDown(120, 140, 0, 1, editor.Mods{}, 0.1)
	b.ed.PointerMove(260, 230, editor.Mods{}, 0.2)
	b.ed.PointerUp(260, 230, editor.Mods{}, 0.3)
	rectA := b.ed.Selection()[0]

	b.ed.SetGeoTool("arrow-right")
	b.ed.PointerDown(420, 140, 0, 1, editor.Mods{}, 0.4)
	b.ed.PointerMove(540, 230, editor.Mods{}, 0.5)
	b.ed.PointerUp(540, 230, editor.Mods{}, 0.6)
	arrowShape := b.ed.Selection()[0]

	view.Key(0, ui.KeyS)
	view.Frame()
	if b.ed.Tool != editor.ToolLasso {
		t.Fatalf("S shortcut tool = %s, want lasso", b.ed.Tool)
	}

	// Sweep lasso around arrowShape while capturing mid-drag lasso screenshot.
	view.Press(400, 120)
	view.Move(560, 120)
	view.Move(560, 250)
	view.Move(400, 250)
	view.Frame()
	saveBoardSeg3Shot(t, "board-lasso.png", view.Image())
	view.Release(400, 250)
	view.Frame()
	if !b.ed.IsSelected(arrowShape) || b.ed.Tool != editor.ToolSelect {
		t.Fatalf("after lasso sel=%v tool=%s", b.ed.Selection(), b.ed.Tool)
	}

	// 2. Press Enter to edit arrowShape label inline via TextAreaBase, type text, commit with Cmd+Enter.
	view.Key(0, ui.KeyEnter)
	view.Frame()
	view.Frame()
	if b.ed.Editing != arrowShape {
		t.Fatalf("Enter did not start editing arrowShape: Editing=%s", b.ed.Editing)
	}
	view.Type("Flow Step")
	view.Frame()
	saveBoardSeg3Shot(t, "board-inline-edit.png", view.Image())
	view.Key(ui.Cmd, ui.KeyEnter)
	view.Frame()
	if b.ed.Editing != "" || b.ed.Doc.Shapes[arrowShape].Text != "Flow Step" {
		t.Fatalf("Cmd+Enter did not commit edit: Editing=%q Text=%q", b.ed.Editing, b.ed.Doc.Shapes[arrowShape].Text)
	}

	// 3. Arrow key nudge (1px and Shift 10px) + Escape layered exit.
	x0, y0 := b.ed.Doc.Shapes[arrowShape].X, b.ed.Doc.Shapes[arrowShape].Y
	view.Key(ui.Shift, ui.KeyRight)
	view.Key(0, ui.KeyDown)
	view.Frame()
	if b.ed.Doc.Shapes[arrowShape].X != x0+10 || b.ed.Doc.Shapes[arrowShape].Y != y0+1 {
		t.Fatalf("Arrow nudge pos = (%v,%v), want (%v,%v)", b.ed.Doc.Shapes[arrowShape].X, b.ed.Doc.Shapes[arrowShape].Y, x0+10, y0+1)
	}
	view.Key(0, ui.KeyEscape)
	view.Frame()
	if len(b.ed.Selection()) != 0 {
		t.Fatalf("Escape did not clear selection: %v", b.ed.Selection())
	}

	// 4. Cmd+' toggles Grid snap and More menu shows Always Snap / Snap to Grid.
	view.Key(ui.Cmd, ui.KeyQuote)
	view.Frame()
	if !b.ed.Grid {
		t.Fatal("Cmd+' did not toggle Grid snap on")
	}
	if err := view.Click(l.BoardMore()); err != nil {
		t.Fatalf("open More menu: %v", err)
	}
	view.Frame()
	if !view.HasText(l.BoardSnapAlways()) || !view.HasText(l.BoardSnapGrid()) {
		t.Fatalf("More menu missing snap items: %v", view.Menu())
	}
	view.CloseMenu()
	view.Key(ui.Cmd, ui.KeyQuote)
	view.Frame()

	// 5. Render showcase screenshots for Selection+Handles+Badge+Flip+GrowY and Smart Guides.
	if os.Getenv("RAKKO_SEG3_SHOTS") != "" {
		// Showcase A: Selected rotated shape (8 handles + size badge) + flipped arrow-right + GrowY geo/note + Frame title.
		b.ed.Select(rectA)
		b.ed.StartEditing(rectA)
		b.ed.EditText = "Auto Grow\nLine 2\nLine 3\nLine 4\nLine 5"
		b.ed.EditChanged()
		b.ed.StopEditing()

		// Flip arrowShape horizontally via negative resize and rotate rectA 18 deg.
		b.ed.Select(arrowShape)
		ab := b.ed.PageBounds(arrowShape)
		b.ed.PointerDown(ab.MaxX(), ab.Center().Y, 0, 1, editor.Mods{}, 2.0)
		b.ed.PointerMove(ab.X-ab.W, ab.Center().Y, editor.Mods{}, 2.1)
		b.ed.PointerUp(ab.X-ab.W, ab.Center().Y, editor.Mods{}, 2.2)

		// Frame with custom name and sticky note with GrowY.
		b.ed.SetTool(editor.ToolFrame)
		b.ed.PointerDown(560, 100, 0, 1, editor.Mods{}, 3.0)
		b.ed.PointerMove(900, 360, editor.Mods{}, 3.1)
		b.ed.PointerUp(900, 360, editor.Mods{}, 3.2)
		frameID := b.ed.Selection()[0]
		b.ed.StartEditing(frameID)
		b.ed.EditText = "Labeled Frame"
		b.ed.EditChanged()
		b.ed.StopEditing()

		noteID := b.ed.CreateNote(geo.V(730, 240), "")
		b.ed.StartEditing(noteID)
		b.ed.EditText = "Sticky Note\nAuto GrowY\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7"
		b.ed.EditChanged()
		b.ed.StopEditing()

		// Select rectA and rotate it 15 deg so screenshot shows rotated box, 8 rotated handles, and WxH badge.
		b.ed.Select(rectA)
		rb := b.ed.PageBounds(rectA)
		b.ed.PointerDown(rb.MaxX()+10, rb.Y-10, 0, 1, editor.Mods{}, 4.0)
		b.ed.PointerMove(rb.Center().X+80, rb.Center().Y-20, editor.Mods{Shift: true}, 4.1)
		b.ed.PointerUp(rb.Center().X+80, rb.Center().Y-20, editor.Mods{Shift: true}, 4.2)
		b.ed.Hovered = ""

		view.Frame()
		view.Frame()
		saveBoardSeg3Shot(t, "board-selection-handles-badge.png", view.Image())

		// Showcase B: Active drag with smart alignment guides (Snap=true).
		b.ed.Snap = true
		b.ed.SetGeoTool("rounded-rect")
		b.ed.PointerDown(160, 360, 0, 1, editor.Mods{}, 5.0)
		b.ed.PointerMove(300, 440, editor.Mods{}, 5.1)
		b.ed.PointerUp(300, 440, editor.Mods{}, 5.2)
		mover := b.ed.Selection()[0]
		b.ed.Select(mover)
		// Drag mover so its left edge aligns with rectA's left edge (or arrowShape's).
		mb := b.ed.PageBounds(mover)
		rb2 := b.ed.PageBounds(arrowShape)
		b.ed.PointerDown(mb.Center().X, mb.Center().Y, 0, 1, editor.Mods{}, 6.0)
		b.ed.PointerMove(rb2.Center().X+2, mb.Center().Y, editor.Mods{}, 6.1)
		view.Frame()
		saveBoardSeg3Shot(t, "board-smart-guides.png", view.Image())
		b.ed.PointerUp(rb2.Center().X+2, mb.Center().Y, editor.Mods{}, 6.2)
	}
}

func saveBoardSeg4Shot(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	dir := os.Getenv("RAKKO_SEG4_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestBoardSeg4ArrowsLinesVerticesSmartDrawAndScreenshots(t *testing.T) {
	a, b := boardTestShelf(t)
	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	// 1. Toolbar & shortcuts A (Arrow) and L (Line).
	if err := view.Click(l.BoardToolArrow()); err != nil {
		t.Fatalf("click Arrow tool: %v", err)
	}
	view.Frame()
	if b.ed.Tool != editor.ToolArrow {
		t.Fatalf("tool after Arrow click = %s, want arrow", b.ed.Tool)
	}
	view.Key(0, ui.KeyL)
	view.Frame()
	if b.ed.Tool != editor.ToolLine {
		t.Fatalf("L shortcut tool = %s, want line", b.ed.Tool)
	}

	// 2. Create 2 boxes, connect with an arrow via UI mouse drag, move boxB, verify arrow follows.
	b.ed.SetGeoTool("rounded-rect")
	b.ed.Next.Stroke = "#60A5FA"
	b.ed.Next.Fill = "semi"
	b.ed.Next.FillColor = "#60A5FA"
	b.ed.PointerDown(100, 100, 0, 1, editor.Mods{}, 0.1)
	b.ed.PointerMove(230, 170, editor.Mods{}, 0.2)
	b.ed.PointerUp(230, 170, editor.Mods{}, 0.3)
	boxA := b.ed.Selection()[0]

	b.ed.SetGeoTool("diamond")
	b.ed.Next.Stroke = "#34D399"
	b.ed.Next.FillColor = "#34D399"
	b.ed.PointerDown(420, 95, 0, 1, editor.Mods{}, 0.4)
	b.ed.PointerMove(560, 175, editor.Mods{}, 0.5)
	b.ed.PointerUp(560, 175, editor.Mods{}, 0.6)
	boxB := b.ed.Selection()[0]

	view.Key(0, ui.KeyA)
	view.Frame()
	view.Press(165, 165)
	view.Move(490, 165)
	view.Frame()
	view.Release(490, 165)
	view.Frame()
	if len(b.ed.Selection()) != 1 {
		t.Fatalf("expected arrow selected after draw, got %v", b.ed.Selection())
	}
	boundArrow := b.ed.Selection()[0]
	if len(b.ed.BindingsTo(boxA)) != 1 || len(b.ed.BindingsTo(boxB)) != 1 {
		t.Fatalf("expected bindings on boxA and boxB")
	}

	// Move boxB down and verify arrow follows.
	b.ed.Select(boxB)
	b.ed.Nudge(0, 90)
	infoMoved := b.ed.Arrow(b.ed.Doc.Shapes[boundArrow])
	endPage := b.ed.PageMat(boundArrow).Apply(infoMoved.End)
	if endPage.Y < 190 {
		t.Fatalf("bound arrow End page Y = %v after moving boxB down", endPage.Y)
	}

	if os.Getenv("RAKKO_SEG4_SHOTS") != "" {
		// Showcase A: Bound following arrow (curved with label), elbow arrow, 8 arrowhead gallery, cubic spline line.
		b.ed.Next.Fill = "none"
		// Bend the bound arrow (boxA -> boxB) into an upward arc and add label.
		b.ed.Select(boundArrow)
		midPage := b.ed.PageMat(boundArrow).Apply(infoMoved.Mid)
		b.ed.PointerDown(midPage.X, midPage.Y, 0, 1, editor.Mods{}, 1.0)
		b.ed.PointerMove(midPage.X, midPage.Y-70, editor.Mods{}, 1.1)
		b.ed.PointerUp(midPage.X, midPage.Y-70, editor.Mods{}, 1.2)
		b.ed.StartEditing(boundArrow)
		b.ed.EditText = "curved + bound"
		b.ed.EditChanged()
		b.ed.StopEditing()

		// Add an elbow arrow from boxA bottom to a third shape boxC.
		b.ed.SetGeoTool("cylinder")
		b.ed.Next.Stroke = "#FBBF24"
		b.ed.Next.Fill = "semi"
		b.ed.Next.FillColor = "#FBBF24"
		b.ed.PointerDown(320, 340, 0, 1, editor.Mods{}, 1.3)
		b.ed.PointerMove(440, 420, editor.Mods{}, 1.4)
		b.ed.PointerUp(440, 420, editor.Mods{}, 1.5)
		boxC := b.ed.Selection()[0]

		b.ed.Next.ArrowKind = "elbow"
		b.ed.Next.Stroke = "#FBBF24"
		b.ed.SetTool(editor.ToolArrow)
		b.ed.PointerDown(165, 135, 0, 1, editor.Mods{}, 1.6)
		b.ed.PointerMove(380, 380, editor.Mods{}, 1.7)
		b.ed.PointerUp(380, 380, editor.Mods{}, 1.8)
		elbowID := b.ed.Selection()[0]
		b.ed.StartEditing(elbowID)
		b.ed.EditText = "elbow"
		b.ed.EditChanged()
		b.ed.StopEditing()

		// Add a cubic spline line from boxC to boxB and pull its midpoint into a smooth curve.
		b.ed.Next.Spline = "cubic"
		b.ed.Next.Stroke = "#A78BFA"
		b.ed.SetTool(editor.ToolLine)
		b.ed.PointerDown(380, 380, 0, 1, editor.Mods{}, 1.9)
		b.ed.PointerMove(490, 225, editor.Mods{}, 2.0)
		b.ed.PointerUp(490, 225, editor.Mods{}, 2.1)
		lineID := b.ed.Selection()[0]
		var lineVirt geo.Vec
		for _, h := range b.ed.Handles() {
			if h.Kind == editor.HandleLinePoint && h.Virtual {
				lineVirt = h.Point
				break
			}
		}
		b.ed.PointerDown(lineVirt.X, lineVirt.Y, 0, 1, editor.Mods{}, 2.2)
		b.ed.PointerMove(lineVirt.X+80, lineVirt.Y+30, editor.Mods{}, 2.3)
		b.ed.PointerUp(lineVirt.X+80, lineVirt.Y+30, editor.Mods{}, 2.4)
		_ = boxC
		_ = lineID

		// Right-side gallery: all 8 arrowhead styles (HeadStart + HeadEnd) + 3 routes.
		b.ed.Next.ArrowKind = "arc"
		for i, head := range editor.ArrowHeads {
			y := 90.0 + float64(i)*64.0
			b.ed.Next.HeadStart = head
			b.ed.Next.HeadEnd = head
			b.ed.Next.Stroke = "#E8E8EA"
			b.ed.SetTool(editor.ToolArrow)
			b.ed.PointerDown(650, y, 0, 1, editor.Mods{Cmd: true}, 3.0+float64(i)*0.1)
			b.ed.PointerMove(880, y, editor.Mods{Cmd: true}, 3.02+float64(i)*0.1)
			b.ed.PointerUp(880, y, editor.Mods{Cmd: true}, 3.04+float64(i)*0.1)
			aid := b.ed.Selection()[0]
			b.ed.StartEditing(aid)
			b.ed.EditText = head
			b.ed.EditChanged()
			b.ed.StopEditing()
		}
		b.ed.Select(boundArrow)
		view.Frame()
		view.Frame()
		saveBoardSeg4Shot(t, "board-arrows-routes-heads.png", view.Image())

		// Showcase B: Polygon & Curve vertex editing + smart recognized shapes.
		a2, b2 := boardTestShelf(t)
		v2 := ui.NewTester(a2.boardView, 1040, 680)
		v2.SetLocale("zh-Hans")
		b2.ed.SetGeoTool("curve")
		b2.ed.Next.Stroke = "#22D3EE"
		b2.ed.PointerDown(100, 140, 0, 1, editor.Mods{}, 0.1)
		b2.ed.PointerMove(420, 320, editor.Mods{}, 0.2)
		b2.ed.PointerUp(420, 320, editor.Mods{}, 0.3)
		curveID := b2.ed.Selection()[0]

		b2.ed.SetGeoTool("polygon")
		b2.ed.Next.Stroke = "#F472B6"
		b2.ed.Next.Fill = "semi"
		b2.ed.Next.FillColor = "#F472B6"
		b2.ed.PointerDown(540, 120, 0, 1, editor.Mods{}, 0.4)
		b2.ed.PointerMove(760, 340, editor.Mods{}, 0.5)
		b2.ed.PointerUp(760, 340, editor.Mods{}, 0.6)
		polyID := b2.ed.Selection()[0]

		// Smart dwell-recognized circle at bottom left preserving thick PenWidth=5.
		b2.ed.SetTool(editor.ToolDraw)
		b2.ed.Next.PenWidth = 5
		b2.ed.Next.Stroke = "#4ADE80"
		b2.ed.PointerDown(280, 460, 0, 1, editor.Mods{}, 1.0)
		for i := 1; i <= 24; i++ {
			ang := 2 * math.Pi * float64(i) / 24
			b2.ed.PointerMove(210+70*math.Cos(ang), 460+70*math.Sin(ang), editor.Mods{}, 1.0+float64(i)*0.02)
		}
		b2.ed.PointerMove(280, 460, editor.Mods{Shift: true}, 2.0)
		b2.ed.PointerUp(280, 460, editor.Mods{Shift: true}, 2.05)

		b2.ed.EditVertices(polyID)
		var polyVirt geo.Vec
		for _, h := range b2.ed.Handles() {
			if h.Kind == editor.HandleGeoVertex && h.Virtual {
				polyVirt = h.Point
				break
			}
		}
		b2.ed.PointerDown(polyVirt.X, polyVirt.Y, 0, 1, editor.Mods{}, 2.2)
		b2.ed.PointerMove(polyVirt.X+40, polyVirt.Y-35, editor.Mods{}, 2.3)
		b2.ed.PointerUp(polyVirt.X+40, polyVirt.Y-35, editor.Mods{}, 2.4)
		_ = curveID
		v2.Frame()
		v2.Frame()
		saveBoardSeg4Shot(t, "board-vertex-editing-smartdraw.png", v2.Image())
	}
}

func saveBoardSeg5Shot(t *testing.T, name string, img image.Image) {
	t.Helper()
	dir := filepath.Join("/tmp/Rakko-v1186-canvas", "seg5")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestBoardSeg5PropertyPanelLooksFontsAndScreenshots(t *testing.T) {
	a, b := boardTestShelf(t)
	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()

	// Build a showcase canvas covering shapes (hatch/dashed/solid), bound curved arrow,
	// fountain pen stroke, and 4 fonts with EN+CN mixed strings.
	b.ed.SetLook(doc.LookSketch)

	// 1. Geo diamond with diagonal pattern hatch + dashed border + EN/CN label.
	b.ed.SetGeoTool("diamond")
	b.ed.PointerDown(80, 90, 0, 1, editor.Mods{}, 0.1)
	b.ed.PointerMove(260, 230, editor.Mods{}, 0.2)
	b.ed.PointerUp(260, 230, editor.Mods{}, 0.3)
	diamondID := b.ed.Selection()[0]
	b.ed.StartEditing(diamondID)
	b.ed.EditText = "Hello 世界"
	b.ed.EditChanged()
	b.ed.StopEditing()

	b.ed.Select(diamondID)
	view.Frame()
	// Drive Shape Property Panel controls via UI clicks: switch color swatch, Fill tab, Fill=pattern, Dash=dash, Width=L.
	if err := view.Click("#38BDF8"); err != nil {
		t.Fatalf("click swatch #38BDF8: %v", err)
	}
	if err := view.Click(l.BoardPropFillTab()); err != nil {
		t.Fatalf("click Fill tab: %v", err)
	}
	view.Frame()
	if err := view.Click("#4ADE80"); err != nil {
		t.Fatalf("click fill swatch #4ADE80: %v", err)
	}
	if err := view.Click(l.BoardPropFillPattern()); err != nil {
		t.Fatalf("click Fill=pattern: %v", err)
	}
	if err := view.Click(l.BoardPropDashDashed()); err != nil {
		t.Fatalf("click Dash=dash: %v", err)
	}
	if err := view.Click("L"); err != nil {
		t.Fatalf("click Width=L: %v", err)
	}
	view.Frame()
	if sh := b.ed.Doc.Shapes[diamondID]; sh.Color != "#38BDF8" || sh.FillColor != "#4ADE80" || sh.Fill != "pattern" || sh.Dash != "dash" || sh.Size != "l" {
		t.Fatalf("UI property panel clicks mismatch shape state: %+v", sh)
	}

	// Second sketch rounded-rect for arrow target & contrast.
	b.ed.SetGeoTool("rounded-rect")
	b.ed.Next.Fill = "semi"
	b.ed.Next.Stroke = "#F472B6"
	b.ed.Next.FillColor = "#F472B6"
	b.ed.Next.Dash = "solid"
	b.ed.Next.Size = "m"
	b.ed.PointerDown(420, 100, 0, 1, editor.Mods{}, 0.4)
	b.ed.PointerMove(640, 220, editor.Mods{}, 0.5)
	b.ed.PointerUp(640, 220, editor.Mods{}, 0.6)
	targetRect := b.ed.Selection()[0]
	b.ed.StartEditing(targetRect)
	b.ed.EditText = "Rakko 白板 · Sketch"
	b.ed.EditChanged()
	b.ed.StopEditing()

	// Add 4 font comparison text items at bottom-left.
	fontLabels := []struct {
		font string
		text string
		y    float64
	}{
		{"sketch", "Sketch 手写: Shantell + 悠哉 Yozai", 310},
		{"sans", "Sans 无衬线: System UI · 混合排版", 355},
		{"serif", "Serif 衬线: Georgia · 经典衬线体", 400},
		{"mono", "Mono 等宽: Code 123 · 编程等宽", 445},
	}
	var textIDs []doc.ID
	for i, fl := range fontLabels {
		b.ed.SetTool(editor.ToolText)
		b.ed.Next.Stroke = "#E8E8EA"
		b.ed.Next.Font = fl.font
		b.ed.Next.FontSize = 20
		b.ed.PointerDown(80, fl.y, 0, 1, editor.Mods{}, 0.7+float64(i)*0.05)
		b.ed.PointerUp(80, fl.y, editor.Mods{}, 0.71+float64(i)*0.05)
		tid := b.ed.Editing
		b.ed.EditText = fl.text
		b.ed.EditChanged()
		b.ed.StopEditing()
		textIDs = append(textIDs, tid)
	}

	// Capture 1: Geo shape selected -> Shape Property Panel.
	b.ed.Select(diamondID)
	b.propFillTab = false
	view.Frame()
	view.Frame()
	saveBoardSeg5Shot(t, "01-prop-shape-sketch.png", view.Image())

	// Capture 2: Arrow bound between diamond & rounded-rect -> Arrow Property Panel.
	b.ed.Next.Font = "sketch"
	b.ed.SetTool(editor.ToolArrow)
	b.ed.Next.Stroke = "#FBBF24"
	b.ed.PointerDown(250, 160, 0, 1, editor.Mods{}, 1.0)
	b.ed.PointerMove(430, 160, editor.Mods{}, 1.05)
	b.ed.PointerUp(430, 160, editor.Mods{}, 1.1)
	arrowID := b.ed.Selection()[0]
	view.Frame()
	if err := view.Click(l.BoardPropRouteCurve()); err != nil {
		t.Fatalf("click Curve route: %v", err)
	}
	view.Frame()
	if b.ed.Doc.Shapes[arrowID].Bend == 0 {
		t.Fatalf("expected Curve click to set Bend")
	}
	view.Frame()
	saveBoardSeg5Shot(t, "02-prop-arrow-curve.png", view.Image())

	// Capture 3: Fountain Pen freehand stroke -> Draw Property Panel.
	b.ed.SetTool(editor.ToolDraw)
	b.ed.Next.PenKind = "pen"
	b.ed.Next.PenWidth = 5
	b.ed.Next.Stroke = "#22D3EE"
	b.ed.PointerDown(470, 320, 0, 1, editor.Mods{}, 1.2)
	for i := 1; i <= 14; i++ {
		tStep := float64(i) / 14.0
		b.ed.PointerMove(470+tStep*220, 320+32*math.Sin(tStep*math.Pi*2), editor.Mods{}, 1.2+float64(i)*0.02)
	}
	b.ed.PointerUp(690, 320, editor.Mods{}, 1.5)
	kids := b.ed.Children(b.ed.Page)
	drawID := kids[len(kids)-1]
	b.ed.SetTool(editor.ToolSelect)
	b.ed.Select(drawID)
	view.Frame()
	if err := view.Click("8"); err != nil {
		t.Fatalf("click PenWidth 8: %v", err)
	}
	view.Frame()
	if b.ed.Doc.Shapes[drawID].PenWidth != 8 {
		t.Fatalf("expected PenWidth 8 after UI click, got %v", b.ed.Doc.Shapes[drawID].PenWidth)
	}
	saveBoardSeg5Shot(t, "03-prop-draw-pen.png", view.Image())

	// Capture 4: 4 Fonts + Text Property Panel.
	b.ed.Select(textIDs[0])
	view.Frame()
	if err := view.Click("B"); err != nil {
		t.Fatalf("click Bold toggle: %v", err)
	}
	view.Frame()
	if !b.ed.Doc.Shapes[textIDs[0]].Bold {
		t.Fatalf("expected Bold=true after UI click")
	}
	saveBoardSeg5Shot(t, "04-prop-text-fonts.png", view.Image())

	// Capture 5: Eraser tool -> Eraser Property Panel + Object/Partial toggle.
	if err := view.Click(l.BoardToolEraser()); err != nil {
		t.Fatalf("click Eraser tool: %v", err)
	}
	view.Frame()
	if err := view.Click(l.BoardEraserPartial()); err != nil {
		t.Fatalf("click Partial eraser toggle: %v", err)
	}
	view.Frame()
	if b.ed.Next.EraserMode != editor.EraserPartial {
		t.Fatalf("EraserMode = %q, want partial", b.ed.Next.EraserMode)
	}
	saveBoardSeg5Shot(t, "05-prop-eraser.png", view.Image())

	// Capture 6 & 7: Switch to Clean and Mono looks via More menu.
	b.ed.SetTool(editor.ToolSelect)
	b.ed.SelectNone()
	view.Frame()
	for _, lk := range []struct {
		item string
		want string
		file string
	}{
		{l.BoardLookSection() + " · " + l.BoardLookClean(), doc.LookClean, "06-look-clean.png"},
		{l.BoardLookSection() + " · " + l.BoardLookMono(), doc.LookMono, "07-look-mono.png"},
	} {
		if err := view.Click(l.BoardMore()); err != nil {
			t.Fatalf("open More menu: %v", err)
		}
		view.Frame()
		if err := view.Click(lk.item); err != nil {
			t.Fatalf("click Look %s: %v", lk.item, err)
		}
		view.Frame()
		view.Frame()
		if b.ed.Look() != lk.want {
			t.Fatalf("Look() = %q, want %q", b.ed.Look(), lk.want)
		}
		saveBoardSeg5Shot(t, lk.file, view.Image())
	}
}

func TestBoardHighDensityMaskAtlasGuard(t *testing.T) {
	a, b := boardTestShelf(t)
	l := locales.In("zh-Hans")
	view := ui.NewTester(a.boardView, 1040, 680)
	view.SetLocale("zh-Hans")

	// Insert 1550 visible fountain-pen strokes so mask area budget (6_000_000) overflows
	// and verify graceful Polyline degradation keeps all items & toolbar intact.
	const count = 1550
	b.ed.SetTool(editor.ToolDraw)
	b.ed.Next.PenKind = "pen"
	b.ed.Next.PenWidth = 3
	for i := 0; i < count; i++ {
		col := float64(i%31)*28 + 60
		row := float64(i/31)*11 + 40
		b.ed.Next.Stroke = editor.Swatches[i%len(editor.Swatches)]
		b.ed.PointerDown(col, row, 0, 1, editor.Mods{}, float64(i)*0.01)
		b.ed.PointerMove(col+180, row+24, editor.Mods{}, float64(i)*0.01+0.002)
		b.ed.PointerMove(col+240, row+48, editor.Mods{}, float64(i)*0.01+0.004)
		b.ed.PointerUp(col+240, row+48, editor.Mods{}, float64(i)*0.01+0.006)
	}
	b.ed.SetTool(editor.ToolSelect)
	b.ed.SelectNone()

	view.Frame()
	view.Frame()

	if !b.lastLightInk {
		t.Fatalf("expected high-density board (%d strokes) to trigger mask atlas budget lightInk guard", count)
	}
	if b.ed.ItemCount() != count {
		t.Fatalf("ItemCount = %d, want %d", b.ed.ItemCount(), count)
	}
	// Verify bottom toolbar still renders & responds cleanly under high density.
	if err := view.Click(l.BoardToolArrow()); err != nil {
		t.Fatalf("toolbar Arrow button unusable under high density: %v", err)
	}
	view.Frame()
	if b.ed.Tool != editor.ToolArrow {
		t.Fatalf("expected Arrow tool active after click under high density, got %s", b.ed.Tool)
	}
	saveBoardSeg5Shot(t, "08-high-density-1550.png", view.Image())
}
