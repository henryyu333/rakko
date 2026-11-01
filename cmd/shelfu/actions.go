//go:build darwin

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
)

// targets is what an action on item applies to: the whole selection when
// item is part of a multi-item selection, otherwise item alone, which then
// becomes the selection, as in Finder.
func (a *shelf) targets(item store.Item) []store.Item {
	if a.selection.has(item.ID) && a.selection.count() > 1 {
		return a.selection.chosen(a.visibleItems())
	}
	a.selection.only(item.ID)
	return []store.Item{item}
}

// usable splits items into the paths of files and images that still exist
// and the texts; moved files are left out.
func usable(items []store.Item) (paths, texts []string) {
	for _, it := range items {
		switch {
		case it.Kind == store.KindText:
			texts = append(texts, it.Text)
		case !it.Missing:
			paths = append(paths, it.Path)
		}
	}
	return paths, texts
}

// itemMenu is the right-click menu of a row. Its items depend only on the
// targets, so the menu is built the same way when it opens and when an
// item is chosen.
func (a *shelf) itemMenu(m *ui.Menu, item store.Item) {
	l := a.appLoc()
	items := a.targets(item)
	paths, texts := usable(items)
	files := len(paths) > 0
	// 复制图片只支持单张：截图/剪贴板图片或磁盘上的图片文件。
	image := len(items) == 1 && convertible(items[0])
	if m.Item(l.ItemOpen()).Disabled(!files).Chosen() {
		for _, p := range paths {
			if err := mygo.Shell.OpenPath(p); err != nil {
				a.report(err)
				return
			}
		}
	}
	if m.Item(l.ItemShowInFinder()).Disabled(!files).Chosen() {
		for _, p := range paths {
			mygo.Shell.ShowItemInFolder(p)
		}
	}
	if m.Item(l.ItemQuickLook()).Disabled(!files).Chosen() {
		a.quickLook(paths)
	}
	if m.Item(l.ItemAirDrop()).Disabled(!files && len(texts) == 0).Chosen() {
		if err := mac.AirDrop(paths, texts); err != nil {
			a.report(err)
		}
	}
	a.convertMenu(m, items)
	// 批量改名：全部目标都还存在于磁盘上的真实文件/文件夹时可用
	//（ShelfU 自己管的截图、剪贴板图片、文字项和转换产物不可改名）。
	allRenameable := len(items) > 0
	for _, it := range items {
		if !a.renameable(it) {
			allRenameable = false
			break
		}
	}
	renameLabel := l.ItemRename()
	if len(items) > 1 {
		renameLabel = l.ItemRenameMany(len(items))
	}
	if m.Item(renameLabel).Disabled(!allRenameable).Chosen() {
		a.openRename(items)
	}
	m.Separator()
	copyLabel := l.ItemCopyText()
	if files {
		copyLabel = l.ItemCopyPath()
	}
	if m.Item(copyLabel).Disabled(!files && len(texts) == 0).Chosen() {
		lines := texts
		if files {
			lines = paths
		}
		a.copyText(strings.Join(lines, "\n"))
	}
	// 图片多一种复制方式：把图片数据放进剪贴板，而不是路径。
	if m.Item(l.ItemCopyImage()).Disabled(!image).Chosen() {
		a.copyItem(items[0])
	}
	// 单张图片（截图或图片文件）：复用已识别的结果或当场识别。
	if image {
		if m.Item(l.ItemRecognizeText()).Disabled(a.ocrBusy(items[0])).Chosen() {
			a.copyRecognizedText(items[0])
		}
	}
	// 置顶：作用于右键的行（或多选的全部）；全部已置顶时改为取消置顶。
	pinned := len(items) > 0
	for _, it := range items {
		if !it.Pinned {
			pinned = false
			break
		}
	}
	pinLabel := l.ItemPin()
	if pinned {
		pinLabel = l.ItemUnpin()
	}
	if m.Item(pinLabel).Chosen() {
		a.setPinned(items, !pinned)
	}
	// 剪贴板来源：一键把这个应用加入不再记录列表。ShelfU 自己识别出的
	// 文字没有可排除的应用（clipSourceOCR 不是 bundle id）。
	if item.Source == store.SourceClipboard && item.SourceApp != "" && item.SourceApp != clipSourceOCR {
		if m.Item(l.ItemExcludeApp(a.appName(item.SourceApp))).Disabled(a.settingsBroken).Chosen() {
			a.excludeClipboardApp(item.SourceApp)
		}
	}
	m.Separator()
	removeLabel := l.ItemRemove()
	if len(items) > 1 {
		removeLabel = l.ItemRemoveMany(len(items))
	}
	if m.Item(removeLabel).Chosen() {
		a.remove(items)
	}
}

// clipWriter is where ShelfU's own copies go. Production uses the system
// clipboard; tests substitute a recorder so copying can be exercised
// without a running app (mygo's clipboard needs App.Run).
type clipWriter interface {
	writeText(text string) error
	writeImage(png []byte) error
}

// systemClipboard is the production clipWriter.
type systemClipboard struct{}

func (systemClipboard) writeText(text string) error {
	return mygo.Clipboard.Write(transfer.TextData(text))
}

func (systemClipboard) writeImage(png []byte) error { return mygo.Clipboard.WriteImage(png) }

// writer is where a copy goes: the system clipboard unless a test
// substituted a recorder (a.write).
func (a *shelf) writer() clipWriter {
	if a.write != nil {
		return a.write
	}
	return systemClipboard{}
}

// clipSourceOCR is the SourceApp marker of clipboard rows ShelfU produced
// itself by recognizing text (screen OCR): recognized text is new content,
// so it belongs in the clipboard page, unlike text re-copied from a row.
// It is not a bundle id; appName and the row icon special-case it instead
// of resolving an app for it.
const clipSourceOCR = "shelf:ocr"

// ocrClipLabel is what an OCR row's source reads, in the row meta line and
// the toolbar's app name lookup.
func (a *shelf) ocrClipLabel() string { return a.appLoc().ItemCaptureText() }

// copyText puts text on the clipboard without recording it again and
// reports whether the write went through; callers that hide the shelf
// afterwards use it to stay open when the copy failed.
func (a *shelf) copyText(text string) bool {
	if err := a.writer().writeText(text); err != nil {
		a.report(err)
		return false
	}
	a.clip.skipCurrent()
	a.toastStatus(a.appLoc().ItemCopied(), time.Now().Add(3*time.Second), statusOK)
	return true
}

// ocrCopy is what copying recognized text achieved. The pasteboard write
// and the clipboard-page record fail independently: a record failure
// leaves the user with the text but without the row, so the two outcomes
// must not collapse into one bool that the success line then covers.
type ocrCopy struct {
	copied    bool  // the text reached the system clipboard
	recordErr error // why the clipboard page has no row; nil when recorded
}

// copyOCRText is copyText for text ShelfU just recognized: after the write it
// also records the text as a clipboard item, so the recognized words appear
// at the front of the clipboard page with 「框选文字」 as their source. The
// watcher is skipped first (copyText), so the capture is this one record,
// not a second copy of ShelfU's own pasteboard write. Re-copying an existing
// row still goes through copyText and records nothing. A failed record is
// returned, not reported: the caller knows the copy itself succeeded and
// says so (toastOCRCopy).
func (a *shelf) copyOCRText(text string) ocrCopy {
	if !a.copyText(text) {
		return ocrCopy{}
	}
	if a.store == nil {
		return ocrCopy{copied: true}
	}
	if err := a.store.AddClipText(text, clipSourceOCR); err != nil {
		return ocrCopy{copied: true, recordErr: err}
	}
	a.changed(nil)
	return ocrCopy{copied: true}
}

// toastOCRCopy reports how copying recognized text ended. A copy that
// never reached the pasteboard was already reported by copyText, and the
// success line — rune count, green dot — only appears when both the copy
// and its record went through. A lost record keeps the 已复制 part and
// names the failure, with the error dot, so the user knows the words are
// on the pasteboard but not in the clipboard page.
func (a *shelf) toastOCRCopy(text string, r ocrCopy) {
	switch {
	case !r.copied:
	case r.recordErr != nil:
		a.toastStatus(a.appLoc().ItemOCRCopyRecordFailed(r.recordErr.Error()), time.Time{}, statusErr)
	default:
		a.toastStatus(a.appLoc().ItemOCRCopied(utf8.RuneCountInString(text)),
			time.Now().Add(3*time.Second), statusOK)
	}
}

// copyItem puts one item on the clipboard as ⌘C on it would: text as text,
// an image as PNG. It reports whether it copied; a failure is reported.
func (a *shelf) copyItem(item store.Item) bool {
	switch {
	case item.Kind == store.KindText:
		return a.copyText(item.Text)
	case convertible(item):
		png, err := itemPNG(item)
		if err != nil {
			a.report(err)
			return false
		}
		if err := a.writer().writeImage(png); err != nil {
			a.report(err)
			return false
		}
		a.clip.skipCurrent()
		a.toastStatus(a.appLoc().ItemCopiedImage(), time.Now().Add(3*time.Second), statusOK)
		return true
	}
	a.report(errors.New(a.appLoc().ItemNotCopyable()))
	return false
}

// copyTrayImage is ⌘C on the tray page: exactly one selected picture goes
// on the clipboard as PNG. Text and plain files keep the tray's existing
// single-click/⌘C behavior (text copies on click; files drag out).
func (a *shelf) copyTrayImage() {
	items := a.selection.chosen(a.visibleItems())
	if len(items) == 1 && convertible(items[0]) {
		a.copyItem(items[0])
	}
}

// pngSignature is the 8-byte PNG file header.
var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// itemPNG returns the item's picture as PNG bytes. Managed screenshots are
// PNG already, and so are PNG files; every other image format ImageIO can
// read is re-encoded once, so the clipboard always carries PNG data.
func itemPNG(item store.Item) ([]byte, error) {
	b, err := os.ReadFile(item.Path)
	if err != nil {
		return nil, err
	}
	if item.Kind == store.KindImage || bytes.HasPrefix(b, pngSignature) {
		return b, nil
	}
	tmp, err := os.CreateTemp("", "shelf-copy-*.png")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	tmp.Close()
	defer os.Remove(name)
	if err := mac.ConvertImage(item.Path, name, mac.FormatPNG); err != nil {
		return nil, err
	}
	return os.ReadFile(name)
}

// setPinned pins or unpins every target; each item is saved on its own
// (the store's API), and the first failure stops the rest and is reported.
func (a *shelf) setPinned(items []store.Item, pinned bool) {
	for _, it := range items {
		if err := a.store.SetPinned(it.ID, pinned); err != nil {
			a.report(err)
			return
		}
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// appName resolves and caches the display name of a bundle identifier. The
// context menu's exclude item must read the same when the menu opens and
// when it is chosen, so a name resolved once cannot change under it, and
// the lookup (running apps, then the bundle on disk) runs once per app.
func (a *shelf) appName(bundleID string) string {
	if bundleID == clipSourceOCR {
		return a.ocrClipLabel()
	}
	if name, ok := a.appNames[bundleID]; ok {
		return name
	}
	name := mac.AppNameForBundleID(bundleID)
	if name == "" {
		name = bundleID
	}
	if a.appNames == nil {
		a.appNames = map[string]string{}
	}
	a.appNames[bundleID] = name
	return name
}

// excludeClipboardApp stops recording copies from bundleID: the identifier
// is appended to the excludedApps setting and saved at once, so the
// clipboard module skips it from the next copy on. Disabled while
// settings.json could not be read, so a broken file is never overwritten.
func (a *shelf) excludeClipboardApp(bundleID string) {
	if bundleID == "" || a.settingsBroken {
		return
	}
	name := a.appName(bundleID)
	if !slices.Contains(a.settings.ExcludedApps, bundleID) {
		next := a.settings
		next.ExcludedApps = append(slices.Clone(a.settings.ExcludedApps), bundleID)
		if !a.applySettings(next) {
			return
		}
	}
	a.toastStatus(a.appLoc().ItemExcludedApp(name), time.Now().Add(3*time.Second), statusOK)
}

// removeExcludedApp takes bundleID out of the excludedApps setting — the
// per-row remove button of the Settings window's 排除的应用 list — and
// saves at once, so the clipboard module records the app from the next
// copy on. Every entry equal to it goes (case-insensitive, as Launch
// Services compares bundle identifiers). Disabled while settings.json
// could not be read, so a broken file is never overwritten.
func (a *shelf) removeExcludedApp(bundleID string) {
	if bundleID == "" || a.settingsBroken {
		return
	}
	next := a.settings
	next.ExcludedApps = slices.DeleteFunc(slices.Clone(a.settings.ExcludedApps),
		func(id string) bool { return strings.EqualFold(strings.TrimSpace(id), bundleID) })
	if slices.Equal(next.ExcludedApps, a.settings.ExcludedApps) {
		return
	}
	a.applySettings(next)
}

// remove takes items off the shelf; original files stay. A clipboard
// capture is only hidden from the page it is removed on while it still
// shows on the other one: the tray's 移除/清空 hides it from the tray
// (OffTray) and the clipboard's hides it from the clipboard (OffClip),
// each leaving the other page alone. Everything else — an item living on
// this page only, a capture already hidden from the other page, and a
// plain file — is deleted outright.
func (a *shelf) remove(items []store.Item) { a.removeFrom(items, a.currentPage().id()) }

// removeFrom is remove as done from page id.
func (a *shelf) removeFrom(items []store.Item, id pageID) {
	// What the tray page is showing is the one rule for "still reachable
	// from the tray" — its filter (OffTray, a drag-out in its grace,
	// unpinned clipboard text) is not copied here.
	onTray := make(map[string]bool, len(items))
	for _, it := range a.itemsFor(pageTray) {
		onTray[it.ID] = true
	}
	var hideTray, hideClip, del []string
	for _, it := range items {
		switch {
		case id == pageTray && it.Source == store.SourceClipboard && !it.OffClip:
			hideTray = append(hideTray, it.ID)
		case id == pageClipboard && !it.OffClip && onTray[it.ID]:
			hideClip = append(hideClip, it.ID)
		default:
			// A clipboard capture hidden from both pages would linger
			// invisible forever, so it is deleted like any other item.
			del = append(del, it.ID)
		}
	}
	hidden := 0
	if len(hideTray) > 0 {
		n, err := a.store.HideFromTray(hideTray)
		if err != nil {
			a.report(err)
		}
		hidden += n
	}
	if len(hideClip) > 0 {
		n, err := a.store.HideFromClipboard(hideClip)
		if err != nil {
			a.report(err)
		}
		hidden += n
	}
	n := 0
	if len(del) > 0 {
		var err error
		n, err = a.store.RemoveMany(del)
		if err != nil {
			// n > 0: removed and saved, but an image file could not be deleted.
			a.report(err)
		}
	}
	if hidden == 0 && n == 0 {
		return
	}
	for _, id := range del {
		a.invalidateThumbnail(id)
	}
	a.selection.keepExisting(a.visibleItems())
	if a.win != nil {
		a.win.Invalidate()
	}
	a.syncCopyKey()
}

// quickLook previews paths in the system Quick Look panel.
func (a *shelf) quickLook(paths []string) {
	if len(paths) == 0 {
		return
	}
	if err := mac.QuickLook(paths); err != nil {
		a.report(err)
	}
}

// toggleQuickLook is Space while the shelf is in use: it closes an open
// preview, or previews the selected files and images.
func (a *shelf) toggleQuickLook() {
	if mac.QuickLookVisible() {
		mac.CloseQuickLook()
		return
	}
	if a.store == nil || !a.visible {
		return
	}
	paths, _ := usable(a.selection.chosen(a.visibleItems()))
	a.quickLook(paths)
}

// dragItems is what dragging item carries: every selected item still on
// disk when item is part of a multi-item selection, otherwise item alone.
func (a *shelf) dragItems(item store.Item) []store.Item {
	if !a.selection.has(item.ID) || a.selection.count() < 2 {
		return []store.Item{item}
	}
	var items []store.Item
	for _, it := range a.selection.chosen(a.visibleItems()) {
		if !it.Missing {
			items = append(items, it)
		}
	}
	return items
}

// dragOut is the transfer for dragging item (see dragItems).
func (a *shelf) dragOut(item store.Item) transfer.Data {
	// Sweep the scratch folder once per drag, before any readable name is
	// minted for this session: cleaning inside dragData would let a later
	// item delete the URL just offered to a sibling item, or a receiver
	// read a link a previous drag left fresh.
	if a.dataDir != "" {
		cleanStaleDragOuts(filepath.Join(a.dataDir, dragOutDir), time.Now().Add(-dragOutLife))
	}
	items := a.dragItems(item)
	if len(items) == 1 {
		return a.dragData(items[0])
	}
	var all []transfer.Item
	for _, it := range items {
		all = append(all, a.dragData(it).Items()...)
	}
	if len(all) == 0 {
		a.report(errors.New(a.appLoc().ItemUnreadable()))
		return transfer.Data{}
	}
	return transfer.New(all...)
}
