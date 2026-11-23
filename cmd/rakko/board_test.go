package main

import (
	"bytes"
	"image"
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
