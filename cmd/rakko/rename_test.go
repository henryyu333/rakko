//go:build darwin

package main

// Tests for the 重新命名 window: the headless view drives the real controls
// (mode segments, inputs, preview, 改名, 撤销) against a real store and
// real files in a temporary directory; the menu tests prove where the
// entry appears and when it is greyed.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/store"
)

// renameShelf is a headless shelf whose tray holds the named files in
// display order (first frontmost), plus the real store behind it.
func renameShelf(t *testing.T, names ...string) (*shelf, *ui.Tester, string) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	dir := t.TempDir()
	for i := len(names) - 1; i >= 0; i-- {
		p := filepath.Join(dir, names[i])
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
	}
	a := &shelf{store: s, dataDir: s.Root,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	return a, ui.NewTester(func(c *ui.Context) { a.renameView(c) }, renameWidth, renameHeight), dir
}

// openHeadlessRename starts a rename session without a native window:
// the view is what a real open renders, so tests drive it directly.
func openHeadlessRename(t *testing.T, a *shelf) *renameUI {
	t.Helper()
	if a.rn == nil {
		a.rn = &renameUI{host: a}
	}
	r := a.rn
	r.items = a.visibleItems()
	r.mode, r.find, r.replace = int(modeReplace), "", ""
	r.addText, r.addSeg = "", 1
	r.fmtName, r.fmtStartText, r.digitsSeg, r.posSeg = "", "1", 0, 1
	r.done, r.err, r.booted = nil, "", false
	return r
}

func selectAllTray(t *testing.T, a *shelf) {
	t.Helper()
	var ids []string
	for _, it := range a.visibleItems() {
		ids = append(ids, it.ID)
	}
	a.selection.onlyAll(ids)
}

// The menu carries 重新命名 … before the copy group, enabled only when
// every target is a real file still on disk; ShelfU's own items grey it.
func TestRenameMenuItem(t *testing.T) {
	a, _, _ := renameShelf(t, "a.txt", "b.txt")
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.ensurePages()
	view.Frame()
	if err := view.RightClick("a.txt"); err != nil {
		t.Fatal(err)
	}
	menu := view.Menu()
	i := slices.Index(menu, "重新命名…")
	if i < 0 {
		t.Fatalf("single file menu lacks 重新命名…: %v", menu)
	}
	if copyIdx := slices.Index(menu, "复制路径"); copyIdx >= 0 && copyIdx < i {
		t.Fatalf("rename must come before the copy group: %v", menu)
	}
	view.CloseMenu()

	selectAllTray(t, a)
	view.Frame()
	if err := view.RightClick("a.txt"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), "重新命名 2 项…") {
		t.Fatalf("multi-selection label: %v", view.Menu())
	}
	view.CloseMenu()

	// A text item disables the entry: mixed selections never rename half.
	if err := a.store.AddText("hello"); err != nil {
		t.Fatal(err)
	}
	selectAllTray(t, a)
	view.Frame()
	if err := view.RightClick("hello"); err != nil {
		t.Fatal(err)
	}
	view.CloseMenu()
	if err := view.RightClick("a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("重新命名 3 项…"); err == nil {
		t.Fatal("a mixed selection must not start a rename")
	}
	view.CloseMenu()
}

// Only plain on-disk files qualify: screenshots, clipboard images,
// text and converted outputs are ShelfU-managed.
func TestRenameableExcludesManagedItems(t *testing.T) {
	a, _, _ := renameShelf(t, "keep.txt")
	if !a.renameable(a.visibleItems()[0]) {
		t.Fatal("a real file must be renameable")
	}
	shot := store.Item{Kind: store.KindImage, Path: filepath.Join(a.dataDir, "images", "x.png")}
	if a.renameable(shot) {
		t.Fatal("a managed screenshot must not be renameable")
	}
	if a.renameable(store.Item{Kind: store.KindText, Text: "hi"}) {
		t.Fatal("text must not be renameable")
	}
	conv := store.Item{Kind: store.KindFile, Path: filepath.Join(a.dataDir, "converted", "x.jpg"), Converted: true}
	if a.renameable(conv) {
		t.Fatal("a converted output must not be renameable")
	}
	inside := store.Item{Kind: store.KindFile, Path: filepath.Join(a.dataDir, "state.json")}
	if a.renameable(inside) {
		t.Fatal("a file inside the data directory must not be renameable")
	}
	if a.renameable(store.Item{Kind: store.KindFile, Path: filepath.Join(a.dataDir, "a.txt"), Missing: true}) {
		t.Fatal("a missing file must not be renameable")
	}
}

// The preview updates live from the inputs and the same controls apply
// the batch: replace text, then the tray paths follow the disk.
func TestRenameViewReplaceApplies(t *testing.T) {
	a, view, dir := renameShelf(t, "IMG 1.jpg", "IMG 2.jpg")
	selectAllTray(t, a)
	view.Frame()
	r := openHeadlessRename(t, a)
	r.win = &mygo.Window{}
	view.Frame()
	if !view.HasText("IMG 1.jpg") || !view.HasText("IMG 2.jpg") {
		t.Fatalf("preview lacks the old names: %v", view.Texts())
	}
	if !view.HasText("名字没有变化") {
		t.Fatalf("an empty find must report no change: %v", view.Texts())
	}
	// The order of the segment labels decides default placement (after).
	if err := view.Click("要替换的文字"); err != nil {
		t.Fatal(err)
	}
	view.Type("IMG")
	view.Frame()
	if err := view.Click("替换后的文字"); err != nil {
		t.Fatal(err)
	}
	view.Type("照片")
	view.Frame()
	if !view.HasText("照片 1.jpg") || !view.HasText("将重新命名 2 项") {
		t.Fatalf("live preview did not update: %v", view.Texts())
	}
	if err := view.Click("应用改名"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"照片 1.jpg", "照片 2.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("disk lacks %s: %v", n, err)
		}
	}
	got := []string{a.store.Items[1].Path, a.store.Items[0].Path}
	want := []string{filepath.Join(dir, "照片 2.jpg"), filepath.Join(dir, "照片 1.jpg")}
	if !slices.Equal(got, want) {
		t.Fatalf("tray paths = %v, want %v", got, want)
	}
	view.Frame()
	if !view.HasText("已重新命名 2 项") || !view.HasText("撤销") {
		t.Fatalf("done view missing: %v", view.Texts())
	}
	// Undo puts the disk and the tray back.
	if err := view.Click("撤销这次改名"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"IMG 1.jpg", "IMG 2.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("undo lost %s: %v", n, err)
		}
	}
	if data, _ := os.ReadFile(a.store.Items[0].Path); string(data) != "x" || !strings.HasSuffix(a.store.Items[0].Path, "IMG 1.jpg") {
		t.Fatalf("undo did not restore the tray path: %q", a.store.Items[0].Path)
	}
}

// Mode 添加文字 adds on the chosen side; 名称加序号 numbers with the
// requested start and padding. Extensions never enter the name math.
func TestRenameViewAddAndFormat(t *testing.T) {
	a, view, dir := renameShelf(t, "a.txt", "b.txt")
	selectAllTray(t, a)
	view.Frame()
	r := openHeadlessRename(t, a)
	r.win = &mygo.Window{}
	view.Frame()
	if err := view.Click("添加文字"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := view.Click("要添加的文字"); err != nil {
		t.Fatal(err)
	}
	view.Type("新-")
	r.addSeg = 0 // 名称前
	view.Frame()
	if !view.HasText("新-a.txt") {
		t.Fatalf("add-before preview: %v", view.Texts())
	}
	r.addSeg = 1 // 名称后
	view.Frame()
	if !view.HasText("a新-.txt") {
		t.Fatalf("add-after preview: %v", view.Texts())
	}
	if err := view.Click("名称加序号"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := view.Click("自定名称（可留空）"); err != nil {
		t.Fatal(err)
	}
	view.Type("旅行")
	if err := view.Click("1"); err != nil { // the start input's placeholder
		t.Fatal(err)
	}
	// Select all and retype the start number.
	view.Command("selectAll")
	view.Type("7")
	if err := view.Click("2"); err != nil { // two digits
		t.Fatal(err)
	}
	view.Frame()
	if !view.HasText("旅行 07.txt") || !view.HasText("旅行 08.txt") {
		t.Fatalf("format preview: %v", view.Texts())
	}
	if err := view.Click("应用改名"); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	for _, n := range []string{"旅行 07.txt", "旅行 08.txt"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("disk lacks %s: %v (have %v, err=%q)", n, err, got, r.err)
		}
	}
}

// A conflict shows its reason in the preview, 改名 stays disabled and
// nothing on disk moves.
func TestRenameViewConflictBlocksApply(t *testing.T) {
	a, view, dir := renameShelf(t, "a.txt")
	r := openHeadlessRename(t, a)
	r.win = &mygo.Window{}
	selectAllTray(t, a)
	view.Frame()
	// An occupied target: another file already named a-new.txt.
	if err := os.WriteFile(filepath.Join(dir, "taken.txt"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.find, r.replace = "a", "taken"
	view.Frame()
	if !view.HasText("已存在同名文件") || !view.HasText("有 1 项冲突") {
		t.Fatalf("conflict row missing: %v", view.Texts())
	}
	// The button is disabled: a click is ignored, so nothing applies.
	if err := view.Click("应用改名"); err == nil {
		if r.done != nil {
			t.Fatal("apply must be disabled while a row conflicts")
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "x" {
		t.Fatal("a blocked apply moved a file")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "taken.txt")); string(data) != "y" {
		t.Fatal("a blocked apply touched the occupied target")
	}
	// Fixing the conflict re-enables the apply.
	r.replace = "renamed"
	view.Frame()
	if !view.HasText("将重新命名 1 项") {
		t.Fatalf("resolved conflict must re-enable: %v", view.Texts())
	}
}

// Undo refuses to overwrite a name taken since the rename, and says so.
func TestRenameUndoOccupied(t *testing.T) {
	a, view, dir := renameShelf(t, "a.txt")
	r := openHeadlessRename(t, a)
	r.win = &mygo.Window{}
	r.find, r.replace = "a", "b"
	view.Frame()
	r.apply()
	if r.done == nil {
		t.Fatalf("apply failed: %s", r.err)
	}
	// Somebody takes the original name after the rename.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	r.undo()
	if r.done == nil {
		t.Fatal("a failed undo must keep the batch undoable")
	}
	// With the window open the message stays inline, naming the file.
	if !strings.Contains(r.err, "已被别的文件占用") || !strings.Contains(r.err, "a.txt") {
		t.Fatalf("undo error = %q", r.err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "new" {
		t.Fatal("undo overwrote the newer file")
	}
}

// Cancel and Enter are the window's keys; the view registers them on the
// window's shortcut scope (proven through the headless shortcuts).
func TestRenameTitleAndFrame(t *testing.T) {
	if got := renameTitle(1); got != "重新命名" {
		t.Fatalf("single title = %q", got)
	}
	if got := renameTitle(3); got != "重新命名 3 项" {
		t.Fatalf("plural title = %q", got)
	}
	work := rectHelper(0, 0, 1440, 900)
	f := centeredFrame(renameWidth, renameHeight, work)
	if f.Width != renameWidth || f.Height != renameHeight ||
		f.X != (1440-renameWidth)/2 || f.Y != (900-renameHeight)/2 {
		t.Fatalf("frame = %+v", f)
	}
	// Clamped into a small display's work area, never off-screen.
	small := centeredFrame(renameWidth, renameHeight, rectHelper(0, 0, 300, 200))
	if small.X != 0 || small.Y != 0 {
		t.Fatalf("small frame = %+v", small)
	}
}

// Renaming a folder carries the tray paths of items inside it, so a later
// refresh never flags them missing; undo puts both back, and the cached
// thumbnails are dropped so they decode at the new path.
func TestRenameNestedItemsFollowFolder(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	root := t.TempDir()
	folder := filepath.Join(root, "资料 夹")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(folder, "里面.txt")
	if err := os.WriteFile(inner, []byte("in"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{inner, folder}); err != nil { // folder newest
		t.Fatal(err)
	}
	a := &shelf{store: s, dataDir: s.Root,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	var folderItem, innerItem store.Item
	for _, it := range s.Items {
		if filepath.Base(it.Path) == "资料 夹" {
			folderItem = it
		} else {
			innerItem = it
		}
	}
	view := ui.NewTester(func(c *ui.Context) { a.renameView(c) }, renameWidth, renameHeight)
	r := openHeadlessRename(t, a)
	r.win = &mygo.Window{}
	r.items = []store.Item{folderItem} // only the folder is renamed
	r.find, r.replace = "资料", "资料A"
	view.Frame()
	a.thumbnails[innerItem.ID] = &ui.Bitmap{}
	r.apply()
	if r.done == nil {
		t.Fatalf("apply failed: %s", r.err)
	}
	folderNew := filepath.Join(root, "资料A 夹")
	innerNew := filepath.Join(folderNew, "里面.txt")
	if _, err := os.Stat(innerNew); err != nil {
		t.Fatalf("nested file did not follow the folder: %v", err)
	}
	var gotFolder, gotInner string
	for _, it := range s.Items {
		switch it.ID {
		case folderItem.ID:
			gotFolder = it.Path
		case innerItem.ID:
			gotInner = it.Path
		}
	}
	if gotFolder != folderNew || gotInner != innerNew {
		t.Fatalf("tray paths = %q, %q; want %q, %q", gotFolder, gotInner, folderNew, innerNew)
	}
	if a.thumbnails[innerItem.ID] != nil {
		t.Fatal("the nested item's cached thumbnail must be dropped")
	}
	r.undo()
	if _, err := os.Stat(inner); err != nil {
		t.Fatalf("undo did not restore the nested file: %v", err)
	}
	for _, it := range s.Items {
		if it.ID == innerItem.ID && it.Path != inner {
			t.Fatalf("undo left the nested tray path at %q", it.Path)
		}
	}
}

// Enter goes through apply, which refuses exactly what the button
// refuses: an unparsable 起始号 must not start a batch.
func TestRenameApplyGuardBadStart(t *testing.T) {
	a, view, dir := renameShelf(t, "a.txt")
	selectAllTray(t, a)
	view.Frame()
	r := openHeadlessRename(t, a)
	r.win = &mygo.Window{}
	r.mode, r.fmtStartText = int(modeFormat), "abc"
	view.Frame()
	if !view.HasText("起始号需要是数字") {
		t.Fatalf("bad start must show a hint: %v", view.Texts())
	}
	r.apply()
	if r.done != nil {
		t.Fatal("apply must refuse an unparsable start")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("a refused apply moved a file: %v", err)
	}
}

// The completion pill lives in the panel, so 撤销 outlives the window and
// still undoes the batch after it closes.
func TestRenameUndoPillOutlivesWindow(t *testing.T) {
	a, view, dir := renameShelf(t, "a.txt")
	selectAllTray(t, a)
	view.Frame()
	r := openHeadlessRename(t, a)
	r.win = &mygo.Window{}
	r.find, r.replace = "a", "b"
	view.Frame()
	r.apply()
	if r.done == nil {
		t.Fatalf("apply failed: %s", r.err)
	}
	a.renameClosed()
	if r.win != nil {
		t.Fatal("closing must drop the window pointer")
	}
	if r.undoUntil.Before(time.Now().Add(9 * time.Second)) {
		t.Fatalf("closing must restart the pill's clock: %v", r.undoUntil)
	}
	panel := ui.NewTester(a.view, sheetW, panelTestH)
	panel.Frame()
	if !panel.HasText("已重新命名 1 项") || !panel.HasText("撤销重新命名") {
		t.Fatalf("panel lacks the undo pill: %v", panel.Texts())
	}
	if err := panel.Click("撤销重新命名"); err != nil {
		t.Fatal(err)
	}
	panel.Frame()
	if r.done != nil {
		t.Fatal("undo must consume the batch")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("undo did not restore the file: %v", err)
	}
	if panel.HasText("撤销重新命名") {
		t.Fatal("the pill must go once it was used")
	}
}

// The pill retires itself after renameUndoLife.
func TestRenameUndoPillExpires(t *testing.T) {
	a := &shelf{}
	r := &renameUI{host: a, done: &renameDone{ops: []renameOp{{src: "x", dst: "y"}}},
		undoUntil: time.Now().Add(-time.Second)}
	a.rn = r
	renameModule{host: a}.tick(time.Now())
	if !r.undoUntil.IsZero() {
		t.Fatalf("expired pill deadline = %v, want zero", r.undoUntil)
	}
}

// While the rename window is up the shelf must not grab the global
// Esc/Space: a click on the panel would otherwise steal them and hide it.
func TestRenameWindowKeepsPanelKeys(t *testing.T) {
	a := &shelf{}
	if !a.keysAreOurs() {
		t.Fatal("a shelf with no windows may hold the keys")
	}
	a.rn = &renameUI{host: a, win: &mygo.Window{}}
	if a.keysAreOurs() {
		t.Fatal("the rename window must keep Esc/Space to itself")
	}
	a.rn.win = nil
	if !a.keysAreOurs() {
		t.Fatal("closing the window gives the keys back")
	}
}

// Re-opening an existing window must not forget the app the keyboard goes
// back to on close.
func TestOpenRenameKeepsRememberedApp(t *testing.T) {
	a, _, _ := renameShelf(t, "a.txt")
	origFront, origActivate, origShow, origFocus := frontmostPID, renameActivate, renameShow, renameFocus
	frontmostPID = func() int { return 777 }
	renameActivate = func() {}
	renameShow = func(*mygo.Window) {}
	renameFocus = func(*mygo.Window) {}
	t.Cleanup(func() {
		frontmostPID, renameActivate, renameShow, renameFocus = origFront, origActivate, origShow, origFocus
	})
	a.rn = &renameUI{host: a, win: &mygo.Window{}, prevPID: 4242}
	a.openRename(a.visibleItems())
	if a.rn.prevPID != 4242 {
		t.Fatalf("re-opening lost the remembered app: %d", a.rn.prevPID)
	}
}

// The data directory check looks at the path as given, not only at its
// symlink-resolved spelling.
func TestRenameableDataDirSpellings(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "data-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	a := &shelf{dataDir: root}
	cases := []struct {
		name string
		p    string
		want bool
	}{
		{"direct", filepath.Join(root, "state.json"), false},
		{"unresolved dot-dot", filepath.Join(root, "sub", "..", "state.json"), false},
		{"through symlink", filepath.Join(link, "images", "x.png"), false},
		{"outside", filepath.Join(outside, "keep.txt"), true},
	}
	for _, c := range cases {
		it := store.Item{Kind: store.KindFile, Path: c.p}
		if got := a.renameable(it); got != c.want {
			t.Fatalf("%s: renameable(%q) = %v, want %v", c.name, c.p, got, c.want)
		}
	}
}

// 撤销 from the panel pill can fail after the window closed (an original
// name was taken since). The failure must reach the user through the
// panel's error pill, and the file holding the name must stay untouched.
func TestRenameUndoPillReportsTakenName(t *testing.T) {
	a, view, dir := renameShelf(t, "a.txt")
	selectAllTray(t, a)
	view.Frame()
	r := openHeadlessRename(t, a)
	r.win = &mygo.Window{}
	r.find, r.replace = "a", "b"
	view.Frame()
	r.apply()
	if r.done == nil {
		t.Fatalf("apply failed: %s", r.err)
	}
	a.renameClosed() // the pill is the only surface left
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.undo()
	if a.statusKind != statusErr || !strings.Contains(a.status, "已被别的文件占用") {
		t.Fatalf("status = %q err=%v, want the panel error naming the taken name", a.status, a.statusKind == statusErr)
	}
	if !strings.Contains(a.status, "a.txt") {
		t.Fatalf("status %q must name the file", a.status)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "new" {
		t.Fatal("the failed undo overwrote the file that holds the original name")
	}
	if r.done == nil {
		t.Fatal("a failed undo must keep the batch undoable")
	}
}
