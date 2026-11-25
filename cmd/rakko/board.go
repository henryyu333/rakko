package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/fonts"
	"rakko/internal/board/geo"
	"rakko/internal/layout"
	"rakko/internal/platform"
	"rakko/locales"
)

const (
	boardSaveDelay    = 400 * time.Millisecond
	boardImageMaxByte = 32 << 20
	boardImageMaxPx   = 100_000_000
	boardImageDisplay = 2048
	boardW            = 1040
	boardH            = 680
	boardBarH         = 30
	boardMinW         = 480
	boardMinH         = 360
	boardDarkBg       = "#1C1C1E"
	boardLightBg      = "#F9FAFB"
)

var (
	boardSymbol         = symbol(`<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4M7 10.5l3-2.5 3 2.5 4-3.5"/>`)
	boardChevronSymbol  = symbol(`<path d="m6 9 6 6 6-6"/>`)
	boardMoreSymbol     = symbol(`<circle cx="5" cy="12" r="1.4" fill="currentColor"/><circle cx="12" cy="12" r="1.4" fill="currentColor"/><circle cx="19" cy="12" r="1.4" fill="currentColor"/>`)
	boardSelectSymbol   = symbol(`<path d="M4.5 4.5l6.2 15 2.4-5.7 5.7-2.4z"/>`)
	boardLassoSymbol    = symbol(`<path d="M7 22a5 5 0 0 1-2-4"/><path d="M3.3 14A6.8 6.8 0 0 1 2 10c0-4.4 4.5-8 10-8s10 3.6 10 8-4.5 8-10 8a12 12 0 0 1-5-1"/><circle cx="5" cy="16" r="2"/>`)
	boardHandSymbol     = symbol(`<path d="M18 11V6a2 2 0 0 0-4 0v1M14 10V4a2 2 0 0 0-4 0v2M10 10.5V6a2 2 0 0 0-4 0v8"/><path d="M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15"/>`)
	boardHiliteSymbol   = symbol(`<path d="m9 11-6 6v3h9l3-3"/><path d="m22 12-4.6 4.6a2 2 0 0 1-2.8 0l-5.2-5.2a2 2 0 0 1 0-2.8L14 4"/>`)
	boardEraserSymbol   = symbol(`<path d="m7 21-4.3-4.3c-1-1-1-2.5 0-3.4l9.6-9.6c1-1 2.5-1 3.4 0l5.6 5.6c1 1 1 2.5 0 3.4L13 21"/><path d="M22 21H7M5 11l9 9"/>`)
	boardArrowSymbol    = symbol(`<path d="M7 17 17 7M7 7h10v10"/>`)
	boardLineSymbol     = symbol(`<path d="M5 19 19 5"/>`)
	boardFrameSymbol    = symbol(`<path d="M6 3v18M18 3v18M3 6h18M3 18h18"/>`)
	boardFitSymbol      = symbol(`<path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/>`)
	boardMinimapSymbol  = symbol(`<rect x="3" y="4" width="18" height="16" rx="2"/><rect x="11" y="10" width="7" height="6" rx="1"/>`)
	boardToolLockSymbol = symbol(`<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>`)
	boardFocusSymbol    = symbol(`<path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6-10-6-10-6Z"/><circle cx="12" cy="12" r="2.5"/>`)
	boardLaserSymbol    = symbol(`<path d="M9 15 4 20"/><circle cx="15" cy="9" r="3"/><path d="M15 2v2M15 14v2M22 9h-2M10 9H8"/>`)
	boardExportSymbol   = symbol(`<path d="M12 15V3M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5"/>`)
	boardWheelSymbol    = symbol(`<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/>`)

	boardAlignLeftSymbol   = symbol(`<path d="M4 6h16M4 10h10M4 14h16M4 18h10"/>`)
	boardAlignCenterSymbol = symbol(`<path d="M4 6h16M7 10h10M4 14h16M7 18h10"/>`)
	boardAlignRightSymbol  = symbol(`<path d="M4 6h16M10 10h10M4 14h16M10 18h10"/>`)
	boardBoldSymbol        = symbol(`<path d="M7 5h6a4 4 0 0 1 0 8H7zM7 13h7a4 4 0 0 1 0 8H7z"/>`)
	boardItalicSymbol      = symbol(`<path d="M10 5h8M6 19h8M15 5l-5 14"/>`)
	boardUnderlineSymbol   = symbol(`<path d="M7 5v6a5 5 0 0 0 10 0V5M5 20h14"/>`)

	// Seams around app/OS-level calls so headless tests can drive boardUI
	// without a window server or touching the real OS Trash/dialogs/clipboard.
	boardActivate        = platform.ActivateSelf
	boardReveal          = platform.RevealInFileManager
	boardTrash           = platform.TrashFile
	boardOpenImageDialog = func(w *mygo.Window) ([]string, error) {
		return mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:   w,
			Multiple: true,
			Filters:  []mygo.FileFilter{{Name: "Images", Extensions: []string{"png", "jpg", "jpeg", "gif", "webp"}}},
		})
	}
	boardReadClipboardImage = func() []byte { return mygo.Clipboard.ReadImage() }
	boardShow               = func(w *mygo.Window) { w.Show() }
	boardFocus              = func(w *mygo.Window) { w.Focus() }
	boardClose              = func(w *mygo.Window) { w.Close() }
	boardNewWindow          = func(opts mygo.WindowOptions) *mygo.Window { return mygo.NewWindow(opts) }
	boardConfigureWindow    = platform.ConfigureFramedWindow
	boardPinRedraw          = func(it *pinItem) {
		if it.win != nil {
			it.win.Invalidate()
		}
	}
)

// boardSaveJob is one snapshot handed to the save worker. A nil drawing is a
// barrier that lets flush wait for everything queued before it.
type boardSaveJob struct {
	id   string
	d    *doc.Drawing
	done chan error
}

// boardUI holds the active whiteboard store, headless editor state, and the
// floating framed window while it is open.
type boardUI struct {
	host  *shelf
	store *doc.Store
	ed    *editor.Editor

	win     *mygo.Window
	top     bool
	prevPID int
	frame   mygo.Rectangle

	rev       uint64
	seenRev   uint64
	since     time.Time
	dirtyFlag bool
	locked    bool

	saveCh  chan boardSaveJob
	saveMu  sync.Mutex
	saveErr error

	clipStamp         string
	boards            []doc.BoardInfo
	renaming          bool
	renameDraft       string
	renameBoot        bool
	confirmDelete     bool
	confirmPageDelete bool

	spaceHeld         bool
	panDrag           bool
	panLastX          float32
	panLastY          float32
	stageRect         ui.Rect
	titleMenu         bool
	pageMenu          bool
	zoomMenu          bool
	moreMenu          bool
	shapeMenu         bool
	toolMenu          bool
	eraserMenu        bool
	drawMenu          bool
	propFillTab       bool
	propGeoMenu       bool
	propHeadStartMenu bool
	propHeadEndMenu   bool
	propScroll        ui.ScrollState
	lastLightInk      bool
	focusMode         bool
	minimap           bool
	miniDrag          bool
	renamingPage      bool
	renamePageDraft   string
	renamePageBoot    bool
	ctxPt             geo.Vec
	hoverScreenPt     geo.Vec
	radialOn          bool
	radialPt          geo.Vec
	wasMenuOpen       bool
	canvasFocus       bool
	editKey           int
	lastEditing       doc.ID
	t0                time.Time
	bitmaps           map[doc.ID]*ui.Bitmap
}

type boardMeasureKey struct {
	text, font  string
	size, width float32
	weight      int
	italic      bool
}

var (
	boardMeasureMu    sync.Mutex
	boardMeasureCache = map[boardMeasureKey]editor.TextBlock{}
)

func boardFontFamily(font string) string {
	fonts.Register()
	switch font {
	case "sketch", "draw":
		return "Shantell Sans, Yozai, cursive"
	case "serif":
		return "Georgia, Yozai, serif"
	case "mono":
		return "monospace, Yozai"
	}
	return "system-ui, Yozai"
}

func boardMeasure(text, font string, size, width float64, weight int, italic bool) editor.TextBlock {
	k := boardMeasureKey{text: text, font: font, size: float32(size), width: float32(width), weight: weight, italic: italic}
	boardMeasureMu.Lock()
	if b, ok := boardMeasureCache[k]; ok {
		boardMeasureMu.Unlock()
		return b
	}
	boardMeasureMu.Unlock()

	const lineMult = 1.3
	f := ui.Font{Family: boardFontFamily(font), Size: float32(size), Weight: weight, Italic: italic, LineHeight: lineMult}
	tl := ui.ShapeText(text, f, float32(width))
	lineH := size * lineMult
	units := utf16.Encode([]rune(text))
	lines := tl.Lines()
	out := editor.TextBlock{Line: lineH}
	for i, ln := range lines {
		end := min(ln.End, len(units))
		start := min(ln.Start, end)
		s := string(utf16.Decode(units[start:end]))
		trimmed := strings.TrimRight(s, " \t\r\n")
		w := float64(ln.Rect.W)
		if trimmed != s {
			c := tl.Caret(start + len(utf16.Encode([]rune(trimmed))))
			w = float64(c.X - ln.Rect.X)
		}
		out.Lines = append(out.Lines, editor.TextLine{Text: trimmed, Y: float64(i) * lineH, W: w})
		if float64(ln.Rect.W) > out.W {
			out.W = float64(ln.Rect.W)
		}
	}
	if len(out.Lines) == 0 || (strings.HasSuffix(text, "\n") && (len(lines) == 0 || lines[len(lines)-1].End <= len(units))) {
		if len(out.Lines) == 0 || out.Lines[len(out.Lines)-1].Text != "" || strings.HasSuffix(text, "\n\n") || text == "\n" {
			out.Lines = append(out.Lines, editor.TextLine{Y: float64(len(out.Lines)) * lineH})
		}
	}
	out.H = float64(len(out.Lines)) * lineH

	boardMeasureMu.Lock()
	if len(boardMeasureCache) > 4096 {
		clear(boardMeasureCache)
	}
	boardMeasureCache[k] = out
	boardMeasureMu.Unlock()
	return out
}

// boardSession lazily initializes the whiteboard store and editor under dataDir.
func (a *shelf) boardSession() *boardUI {
	if a.board != nil {
		return a.board
	}
	if a.dataDir == "" {
		return nil
	}
	l := a.appLoc()
	st, err := doc.Open(a.dataDir, l.BoardDefaultName())
	if err != nil {
		a.report(err)
		return nil
	}
	cleanBoardDragTemps(boardDragTempDir, time.Now(), 24*time.Hour)
	_, _ = st.CleanAssets(time.Now(), 7*24*time.Hour)
	b := &boardUI{host: a, store: st, t0: time.Now(), bitmaps: map[doc.ID]*ui.Bitmap{}}
	b.ensureSaver()
	if err := b.load(); err != nil {
		a.report(err)
	}
	b.refreshBoards()
	a.board = b
	return b
}

func (b *boardUI) seconds(t time.Time) float64 {
	if t.IsZero() {
		return time.Since(b.t0).Seconds()
	}
	return t.Sub(b.t0).Seconds()
}

func (b *boardUI) pickImage() {
	if b.locked {
		return
	}
	paths, err := boardOpenImageDialog(b.win)
	if err != nil || len(paths) == 0 {
		return
	}
	at := b.ed.ToPage(geo.V(b.ed.VW/2, b.ed.VH/2))
	for i, p := range paths {
		offset := float64(i * 24)
		b.insertImageFile(p, geo.V(at.X+offset, at.Y+offset))
	}
}

func (b *boardUI) pasteImage(formats []transfer.Format) bool {
	if b.locked || boardClipboardHasText(formats) {
		return false
	}
	data := boardReadClipboardImage()
	if len(data) == 0 {
		return false
	}
	at := b.ed.ToPage(geo.V(b.ed.VW/2, b.ed.VH/2))
	return b.insertImageBytes(data, "png", "clipboard.png", at)
}

// boardClipboardHasText reports whether a paste should go to text rather than
// image insertion: text, rich text, a URL list, or files are not a pure image.
func boardClipboardHasText(formats []transfer.Format) bool {
	for _, f := range formats {
		switch f {
		case transfer.Text, transfer.HTML, transfer.Markdown, transfer.URIList:
			return true
		}
	}
	return false
}

// boardIsSupportedImageExt reports whether ext (with or without leading dot)
// is an accepted image file extension for board image shapes.
func boardIsSupportedImageExt(ext string) bool {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	switch ext {
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "heic", "heif", "avif":
		return true
	default:
		return false
	}
}

func boardDecodeImageFile(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := boardDecodeImage(path, data)
	return img, err
}

func boardDecodeImage(path string, data []byte) (image.Image, string, error) {
	if img, format, err := image.Decode(bytes.NewReader(data)); err == nil && img != nil {
		return img, format, nil
	}
	if path != "" {
		if thumb, err := platform.Thumbnail(path, 1600); err == nil && thumb != nil {
			ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
			return thumb, ext, nil
		}
	}
	return nil, "", image.ErrFormat
}

func (b *boardUI) insertImageFile(path string, pagePt geo.Vec) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if !boardIsSupportedImageExt(ext) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > boardImageMaxByte {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	img, format, err := boardDecodeImage(path, data)
	if err != nil || img == nil {
		return false
	}
	return b.insertDecodedImage(img, data, ext, format, filepath.Base(path), pagePt)
}

func (b *boardUI) insertImageBytes(data []byte, ext, name string, pagePt geo.Vec) bool {
	if b.store == nil || b.ed == nil || len(data) == 0 || len(data) > boardImageMaxByte {
		return false
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > boardImageMaxPx {
			return false
		}
	}
	img, format, err := boardDecodeImage("", data)
	if err != nil || img == nil {
		return false
	}
	return b.insertDecodedImage(img, data, ext, format, name, pagePt)
}

func (b *boardUI) insertDecodedImage(img image.Image, data []byte, ext, format, name string, pagePt geo.Vec) bool {
	if ext == "" {
		ext = format
	}
	bounds := img.Bounds()
	w, h := float64(bounds.Dx()), float64(bounds.Dy())
	if w <= 0 || h <= 0 {
		return false
	}
	asset, err := b.store.SaveAsset(data, ext, name, "image/"+ext, w, h)
	if err != nil {
		b.host.report(err)
		return false
	}
	if b.bitmaps == nil {
		b.bitmaps = map[doc.ID]*ui.Bitmap{}
	}
	b.bitmaps[asset.ID] = ui.NewBitmap(downsampleImage(img, boardImageDisplay))
	b.ed.InsertImage(asset, pagePt)
	b.invalidate()
	return true
}

func (b *boardUI) load() error {
	d, err := b.store.Read()
	if err != nil {
		b.locked = true
		// A corrupt board gets a blank, read-only placeholder titled with its
		// file name, never the previous board's document.
		name := strings.TrimSuffix(filepath.Base(b.store.Path()), doc.FileExt)
		placeholder := doc.NewDrawing(b.store.ID(), name)
		if b.ed == nil {
			b.ed = editor.New(placeholder, boardMeasure)
		} else {
			b.ed.Replace(placeholder)
		}
		b.ed.OnChange = func(*doc.Drawing) { b.markDirty() }
		b.dirtyFlag = false
		b.rev++
		b.seenRev = b.rev
		b.since = time.Time{}
		b.syncWindowTheme()
		return err
	}
	b.locked = false
	if b.ed == nil {
		b.ed = editor.New(d, boardMeasure)
	} else {
		b.ed.Replace(d)
	}
	b.ed.OnChange = func(*doc.Drawing) {
		b.markDirty()
	}
	b.ed.FrameName = b.host.appLoc().BoardFrameDefault()
	b.dirtyFlag = false
	b.rev++
	b.seenRev = b.rev
	b.since = time.Time{}
	b.syncWindowTheme()
	b.refreshChrome()
	return nil
}

func (b *boardUI) refreshBoards() {
	if b.store == nil {
		return
	}
	list, err := b.store.List()
	if err != nil {
		return
	}
	b.boards = list
}

func (b *boardUI) markDirty() {
	b.rev++
	b.dirtyFlag = true
	b.invalidate()
}

// tick persists dirty board changes after boardSaveDelay of inactivity.
func (b *boardUI) tick(now time.Time) bool {
	if err := b.takeSaveErr(); err != nil {
		b.host.report(err)
		b.dirtyFlag = true
		return false
	}
	if b.locked || !b.dirtyFlag {
		b.since = time.Time{}
		b.seenRev = b.rev
		return false
	}
	if b.rev != b.seenRev {
		b.seenRev, b.since = b.rev, now
	}
	if b.since.IsZero() || now.Sub(b.since) < boardSaveDelay {
		return false
	}
	if err := b.save(); err != nil {
		b.host.report(err)
		return false
	}
	b.refreshChrome()
	return true
}

// save queues the current document state for the background encoder without
// ending an in-progress inline edit. The main thread only takes a Snapshot.
func (b *boardUI) save() error {
	if b.locked || !b.dirtyFlag || b.ed == nil || b.store == nil {
		return nil
	}
	b.ensureSaver()
	job := boardSaveJob{id: b.store.ID(), d: b.ed.Doc.Snapshot()}
	select {
	case b.saveCh <- job:
		b.dirtyFlag = false
		b.seenRev = b.rev
		b.since = time.Time{}
	default:
		// The worker is still busy with an older snapshot; keep dirty and try on
		// the next tick rather than queueing a backlog.
	}
	return nil
}

// flush commits any open inline edit, then waits for every queued snapshot to
// reach disk. Quit, close, blur, board switching, and import all use it.
func (b *boardUI) flush() error {
	if b.ed != nil {
		b.ed.StopEditing()
	}
	if b.locked || b.ed == nil || b.store == nil {
		return nil
	}
	if b.dirtyFlag {
		b.ensureSaver()
		job := boardSaveJob{id: b.store.ID(), d: b.ed.Doc.Snapshot(), done: make(chan error, 1)}
		b.saveCh <- job
		if err := <-job.done; err != nil {
			return err
		}
		b.dirtyFlag = false
		b.seenRev = b.rev
		b.since = time.Time{}
		b.refreshChrome()
	}
	return b.waitSaves()
}

// waitSaves waits for snapshots queued before it and reports any background
// write failure.
func (b *boardUI) waitSaves() error {
	if b.saveCh == nil {
		return b.takeSaveErr()
	}
	done := make(chan error, 1)
	b.saveCh <- boardSaveJob{done: done}
	if err := <-done; err != nil {
		return err
	}
	return b.takeSaveErr()
}

func (b *boardUI) ensureSaver() {
	if b.saveCh != nil || b.store == nil {
		return
	}
	b.saveCh = make(chan boardSaveJob, 1)
	go b.saveLoop()
}

func (b *boardUI) saveLoop() {
	for job := range b.saveCh {
		var err error
		if job.d != nil {
			err = b.store.WriteFor(job.id, job.d)
		}
		if job.done != nil {
			job.done <- err
		} else if err != nil {
			b.saveMu.Lock()
			b.saveErr = err
			b.saveMu.Unlock()
		}
	}
}

func (b *boardUI) takeSaveErr() error {
	b.saveMu.Lock()
	err := b.saveErr
	b.saveErr = nil
	b.saveMu.Unlock()
	return err
}

func (b *boardUI) selectBoard(id string) error {
	if b.store == nil || id == b.store.ID() {
		return nil
	}
	if err := b.flush(); err != nil {
		return err
	}
	if err := b.store.Select(id); err != nil {
		return err
	}
	err := b.load()
	b.refreshBoards()
	b.renaming = false
	b.confirmDelete = false
	b.confirmPageDelete = false
	b.toolMenu = false
	b.invalidate()
	return err
}

func (b *boardUI) newBoardDoc() error {
	if b.store == nil {
		return nil
	}
	if err := b.flush(); err != nil {
		return err
	}
	b.refreshBoards()
	l := b.host.appLoc()
	name := b.nextBoardName(l)
	if _, err := b.store.New(name); err != nil {
		return err
	}
	err := b.load()
	b.refreshBoards()
	b.renaming = false
	b.confirmDelete = false
	b.confirmPageDelete = false
	b.toolMenu = false
	b.invalidate()
	return err
}

func (b *boardUI) nextBoardName(l locales.Messages) string {
	used := make(map[string]bool, len(b.boards))
	for _, info := range b.boards {
		used[info.Name] = true
	}
	for n := len(b.boards) + 1; ; n++ {
		cand := l.BoardNewName(n)
		if !used[cand] {
			return cand
		}
	}
}

func (b *boardUI) startRename() {
	if b.locked || b.ed == nil {
		return
	}
	b.renaming = true
	b.renameDraft = b.ed.Doc.Name
	b.renameBoot = false
	b.invalidate()
}

func (b *boardUI) commitRename() {
	if !b.renaming {
		return
	}
	name := strings.TrimSpace(b.renameDraft)
	b.renaming = false
	b.renameBoot = false
	if name != "" && b.ed != nil && name != b.ed.Doc.Name {
		b.ed.SetName(name)
		_ = b.flush()
	}
	b.refreshChrome()
}

func (b *boardUI) cancelRename() {
	b.renaming = false
	b.renameBoot = false
	b.invalidate()
}

var boardPageNumber = regexp.MustCompile(`(?i)^(?:page|页面)\s*(\d+)$`)

// pageTitle shows a page's default name in the current language without
// rewriting custom names.
func (b *boardUI) pageTitle(l locales.Messages, pg *doc.Page) string {
	if pg == nil {
		return ""
	}
	if m := boardPageNumber.FindStringSubmatch(pg.Name); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return l.BoardPageName(strconv.Itoa(n))
		}
	}
	return pg.Name
}

// nextPageName names a new page one past the highest existing number, so
// deleting a middle page never reuses a name.
func (b *boardUI) nextPageName(l locales.Messages) string {
	maxN := 0
	if b.ed != nil {
		for _, pg := range b.ed.Pages() {
			if m := boardPageNumber.FindStringSubmatch(pg.Name); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil && n > maxN {
					maxN = n
				}
			}
		}
	}
	return l.BoardPageName(strconv.Itoa(maxN + 1))
}

func (b *boardUI) startPageRename() {
	if b.locked || b.ed == nil {
		return
	}
	pg := b.ed.ActivePageInfo()
	if pg == nil {
		return
	}
	b.renamingPage = true
	b.renamePageDraft = b.pageTitle(b.host.appLoc(), pg)
	b.renamePageBoot = false
	b.invalidate()
}

func (b *boardUI) commitPageRename() {
	if !b.renamingPage {
		return
	}
	name := strings.TrimSpace(b.renamePageDraft)
	b.renamingPage = false
	b.renamePageBoot = false
	if name != "" && b.ed != nil {
		b.ed.RenamePage(b.ed.Page, name)
	}
	b.invalidate()
}

func (b *boardUI) cancelPageRename() {
	b.renamingPage = false
	b.renamePageBoot = false
	b.invalidate()
}

func (b *boardUI) anyMenuOpen() bool {
	return b.titleMenu || b.pageMenu || b.zoomMenu || b.moreMenu ||
		b.shapeMenu || b.toolMenu || b.propGeoMenu || b.propHeadStartMenu || b.propHeadEndMenu
}

func (b *boardUI) toggleFocusMode() {
	b.focusMode = !b.focusMode
	if b.focusMode {
		b.ed.StopEditing()
	} else {
		b.canvasFocus = false
	}
	b.invalidate()
}

func (b *boardUI) trashCurrent() error {
	if b.store == nil {
		return nil
	}
	b.confirmDelete = false
	b.confirmPageDelete = false
	if err := b.flush(); err != nil {
		return err
	}
	if err := b.store.Trash(b.store.ID(), doc.TrashFunc(boardTrash)); err != nil {
		return err
	}
	err := b.load()
	b.refreshBoards()
	b.invalidate()
	return err
}

// invalidate repaints only the board window: pointer moves and canvas edits
// must not repaint the panel and every pinned card.
func (b *boardUI) invalidate() {
	if b.win != nil {
		b.win.Invalidate()
	}
}

// refreshChrome updates the parts that follow a commit or a rename: the window
// title, the panel, and the board's pinned card.
func (b *boardUI) refreshChrome() {
	if b.win != nil {
		if b.ed != nil {
			b.win.SetTitle(b.currentTitle(b.host.appLoc()))
		}
		b.win.Invalidate()
	}
	if b.host.win != nil {
		b.host.win.Invalidate()
	}
	for _, it := range b.host.pins {
		if it.cardID == "board" {
			boardPinRedraw(it)
		}
	}
}

func (b *boardUI) syncWindowTheme() {
	if b.win == nil || b.ed == nil {
		return
	}
	dark := b.ed.DarkMode()
	bg := boardLightBg
	if dark {
		bg = boardDarkBg
	}
	_ = b.win.SetBackgroundColor(bg)
	boardConfigureWindow(b.win.NativeHandle(), dark)
}

func (b *boardUI) setDarkMode(dark bool) {
	if b.ed == nil || b.ed.DarkMode() == dark {
		return
	}
	b.ed.SetDarkMode(dark)
	b.syncWindowTheme()
	b.invalidate()
}

func (b *boardUI) setTop(on bool) {
	b.top = on
	if b.win != nil {
		b.win.SetAlwaysOnTop(on)
	}
	b.invalidate()
}

func (b *boardUI) currentTitle(l locales.Messages) string {
	if b != nil && b.ed != nil && strings.TrimSpace(b.ed.Doc.Name) != "" {
		return b.ed.Doc.Name
	}
	return l.CardBoard()
}

// toggleBoard opens the whiteboard window or closes it when already open.
func (a *shelf) toggleBoard() bool {
	b := a.boardSession()
	if b == nil {
		return false
	}
	if b.win != nil {
		b.close()
		return true
	}
	return b.open()
}

// newBoard creates a fresh board and brings the whiteboard window to the front.
func (a *shelf) newBoard() {
	b := a.boardSession()
	if b == nil {
		return
	}
	if err := b.newBoardDoc(); err != nil {
		a.report(err)
		return
	}
	a.hide(hideExplicit)
	b.showFront()
}

func (b *boardUI) open() bool {
	a := b.host
	a.releaseKeys()
	if b.locked {
		if err := b.load(); err != nil {
			a.report(err)
		}
	}
	frame := b.frame
	if b.win == nil {
		b.prevPID = 0
		if pid := frontmostPID(); pid > 0 && pid != ownPID() {
			b.prevPID = pid
		}
		if frame.Width == 0 || frame.Height == 0 {
			frame = a.freshBoardFrame()
		}
		a.hide(hideExplicit)
		dark := b.ed == nil || b.ed.DarkMode()
		bg := boardDarkBg
		if !dark {
			bg = boardLightBg
		}
		w := boardNewWindow(mygo.WindowOptions{
			Title: b.currentTitle(a.appLoc()), Width: frame.Width, Height: frame.Height,
			X: frame.X, Y: frame.Y,
			Hidden: true, BackgroundColor: bg, AlwaysOnTop: b.top,
			TitleBarStyle:     mygo.TitleBarHidden,
			HideWindowButtons: true,
			MinWidth:          boardMinW, MinHeight: boardMinH,
			Content: ui.View(a.boardView),
		})
		w.OnClose(b.onClose)
		w.OnBlur(b.onBlur)
		w.OnResize(func() { b.frame = w.Bounds() })
		w.OnMove(func() { b.frame = w.Bounds() })
		boardConfigureWindow(w.NativeHandle(), dark)
		b.win = w
		b.canvasFocus = false
	}
	b.frame = b.win.Bounds()
	boardShow(b.win)
	boardActivate()
	boardFocus(b.win)
	b.invalidate()
	return true
}

func (b *boardUI) showFront() bool {
	if b.win == nil {
		return b.open()
	}
	b.host.hide(hideExplicit)
	boardShow(b.win)
	boardActivate()
	boardFocus(b.win)
	b.invalidate()
	return true
}

func (b *boardUI) close() {
	if err := b.flush(); err != nil {
		b.host.report(err)
	}
	if b.win != nil {
		boardClose(b.win)
	}
}

func (b *boardUI) onClose(*mygo.CloseEvent) {
	a := b.host
	_ = b.flush()
	b.win = nil
	b.bitmaps = map[doc.ID]*ui.Bitmap{}
	b.toolMenu = false
	b.renaming = false
	b.confirmDelete = false
	b.confirmPageDelete = false
	b.spaceHeld = false
	b.panDrag = false
	if a.quitting {
		return
	}
	prev := b.prevPID
	b.prevPID = 0
	switch a.settingsKeyboardExit() {
	case settingsToPanel:
		if a.win != nil {
			a.win.Focus()
		}
		return
	case settingsToShelf:
		a.holdKeys()
	}
	a.handBackApp(prev)
}

func (b *boardUI) onBlur() {
	if b.renaming {
		b.commitRename()
	}
	if b.renamingPage {
		b.commitPageRename()
	}
	_ = b.flush()
}

func (a *shelf) freshBoardFrame() mygo.Rectangle {
	anchor := a.frame
	for _, it := range a.pins {
		if it.cardID == "board" && it.win != nil {
			anchor = it.frame
			break
		}
	}
	area := mygo.Screen.DisplayNearestPoint(mygo.Point{
		X: anchor.X + anchor.Width/2,
		Y: anchor.Y + anchor.Height/2,
	}).WorkArea
	w := min(boardW, max(boardMinW, area.Width-48))
	h := min(boardH, max(boardMinH, area.Height-48))
	return a.noteClearShell(centeredFrame(w, h, area), area)
}

type boardModule struct{ host *shelf }

func (boardModule) live() bool { return false }

func (m boardModule) tick(now time.Time) {
	b := m.host.board
	if b == nil {
		return
	}
	if b.win != nil {
		m.host.releaseKeys()
	}
	b.tick(now)
}

func (m boardModule) nextTick(now time.Time) time.Time {
	b := m.host.board
	if b == nil || b.locked || !b.dirtyFlag {
		return time.Time{}
	}
	if b.since.IsZero() {
		return now
	}
	return b.since.Add(boardSaveDelay)
}

func (a *shelf) boardFlush() {
	if a.board == nil {
		return
	}
	if err := a.board.flush(); err != nil {
		a.report(err)
	}
}

func (a *shelf) boardLabel(l locales.Messages) string {
	b := a.boardSession()
	if b != nil && b.ed != nil && strings.TrimSpace(b.ed.Doc.Name) != "" {
		return b.ed.Doc.Name
	}
	return l.CardBoard()
}

func (a *shelf) boardBody(c *ui.Context, card ui.Element, span int, surf toolCardSurface) {
	card.Children(func() {
		l := loc(c)
		def := toolCardDefFor("board")
		wide := span > 1 || surf == cardDesktop
		name := def.name(l)
		sub := l.BoardEmpty()
		count := 0
		if b := a.board; b != nil && b.ed != nil {
			if strings.TrimSpace(b.ed.Doc.Name) != "" {
				name = b.ed.Doc.Name
			}
			count = b.ed.ItemCount()
		} else if a.dataDir != "" {
			// The card's first frame reads the metadata cache; opening the whole
			// board here would parse it just to draw a title.
			name, count = doc.CardInfo(a.dataDir, name)
		}
		if count > 0 {
			sub = l.BoardItemCount(count)
		}
		if !wide {
			a.cardGlyphLead(c, card, "board-lead", def.icon, 20, 20, ink2).
				Absolute().Left(cardPad).Top(cardPad)
			fitText(c, def.name(l), 12, 600, toolColW(1)-2*cardPad).TextColor(ink).
				Absolute().Left(cardPad).Bottom(cardPad)
			return
		}
		w := cardW(layout.Card{ID: "board", Span: span}, surf)
		top := ui.Row(c.Key("board-head")).Absolute().Left(cardPad).Right(cardPad).
			Top(cardPad).Height(18).AlignItems(ui.Center).Gap(6)
		top.Children(func() {
			ui.Icon(c, def.icon).Size(16, 16).TextColor(ink2)
			fitText(c, name, 12, 600, w-2*cardPad-22).TextColor(ink).Label(name)
		})
		ui.Text(c, sub).SingleLine().FontSize(11).TextColor(ink3).
			Absolute().Left(cardPad).Right(cardPad).Bottom(cardPad).Label(sub)
	})
}

func boardCountText(l locales.Messages, n int) string {
	if n == 0 {
		return l.BoardEmpty()
	}
	return fmt.Sprintf("%s", l.BoardItemCount(n))
}

// downsampleImage shrinks an image to max on its longer side for display; a
// 48-megapixel photo does not stay as ~190 MB of RGBA for the app's lifetime.
func downsampleImage(img image.Image, maxDim int) image.Image {
	if img == nil || maxDim <= 0 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return img
	}
	scale := float64(maxDim) / float64(max(w, h))
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	return dst
}
