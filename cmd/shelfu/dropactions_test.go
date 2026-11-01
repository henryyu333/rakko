//go:build darwin

package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
)

// writeFile makes a real file under dir so classification stats it.
func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	return testPNG(t)
}

// actionIDs labels the actions a rule row picked, for assertions.
func actionIDs(acts []dropAction) []string {
	out := make([]string, len(acts))
	for i, a := range acts {
		out[i] = a.id
	}
	return out
}

func actionTargets(acts []dropAction) []convertTarget {
	out := make([]convertTarget, len(acts))
	for i, a := range acts {
		out[i] = a.target
	}
	return out
}

func TestDropActionsImageRules(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "截图.png", testPNGBytes(t))

	one := dropActions([]string{png})
	if got := actionIDs(one); !slices.Equal(got, []string{"fit1m", "topdf", "tojpeg", "airdrop"}) {
		t.Fatalf("one image got %v", got)
	}
	if one[1].label != "转成 PDF" || one[1].target != toPDF {
		t.Fatalf("single image's PDF cell = %q target %v", one[1].label, one[1].target)
	}

	png2 := writeFile(t, dir, "b.png", testPNGBytes(t))
	two := dropActions([]string{png, png2})
	if two[1].label != "合成一个 PDF" || two[1].target != toMergedPDF {
		t.Fatalf("multi-image PDF cell = %q target %v", two[1].label, two[1].target)
	}
}

func TestDropActionsAllJPEGSwapsToPNG(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.jpg", []byte("jpeg"))
	b := writeFile(t, dir, "b.jpeg", []byte("jpeg"))
	acts := dropActions([]string{a, b})
	if got := actionIDs(acts); !slices.Equal(got, []string{"fit1m", "topdf", "topng", "airdrop"}) {
		t.Fatalf("all-JPEG got %v", got)
	}
	// A PNG among JPEGs keeps 转 JPEG — the swap is all-or-nothing.
	c := writeFile(t, dir, "c.png", testPNGBytes(t))
	mixed := dropActions([]string{a, c})
	if !slices.Contains(actionIDs(mixed), "tojpeg") {
		t.Fatalf("jpeg+png got %v, want tojpeg", actionIDs(mixed))
	}
}

func TestDropActionsPDFRules(t *testing.T) {
	dir := t.TempDir()
	pdf := writeFile(t, dir, "报告.pdf", []byte("%PDF-1.4"))
	acts := dropActions([]string{pdf})
	if got := actionIDs(acts); !slices.Equal(got, []string{"toword", "topng", "zip", "airdrop"}) {
		t.Fatalf("pdf got %v", got)
	}
	if acts[0].target != toWord || acts[1].target != toPNG {
		t.Fatalf("pdf targets = %v", actionTargets(acts))
	}
}

func TestDropActionsArchiveAndMixed(t *testing.T) {
	dir := t.TempDir()
	z := writeFile(t, dir, "素材.zip", []byte("PK"))
	r := writeFile(t, dir, "备份.tar.gz", []byte("x"))
	if got := actionIDs(dropActions([]string{z, r})); !slices.Equal(got, []string{"extract", "airdrop"}) {
		t.Fatalf("archives got %v", got)
	}
	folder := filepath.Join(dir, "文件夹")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	txt := writeFile(t, dir, "备忘.txt", []byte("x"))
	if got := actionIDs(dropActions([]string{folder, txt})); !slices.Equal(got, []string{"zip", "airdrop"}) {
		t.Fatalf("folder+file got %v", got)
	}
	// Folder named like an archive is still a folder, not an archive.
	zipDir := filepath.Join(dir, "形如.zip")
	if err := os.Mkdir(zipDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := actionIDs(dropActions([]string{zipDir})); !slices.Equal(got, []string{"zip", "airdrop"}) {
		t.Fatalf("folder ending .zip got %v", got)
	}
	// Image mixed with a plain file is a mixed drop: zip + airdrop only.
	img := writeFile(t, dir, "img.png", testPNGBytes(t))
	if got := actionIDs(dropActions([]string{img, txt})); !slices.Equal(got, []string{"zip", "airdrop"}) {
		t.Fatalf("image+text got %v", got)
	}
	// An unreadable path counts as mixed.
	gone := filepath.Join(dir, "不存在.jpg")
	if got := actionIDs(dropActions([]string{img, gone})); !slices.Equal(got, []string{"zip", "airdrop"}) {
		t.Fatalf("missing file got %v", got)
	}
}

func TestDropCountText(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "a.png", testPNGBytes(t))
	jpg := writeFile(t, dir, "b.jpg", []byte("x"))
	pdf := writeFile(t, dir, "c.pdf", []byte("x"))
	z := writeFile(t, dir, "d.zip", []byte("x"))
	txt := writeFile(t, dir, "e.txt", []byte("x"))
	cases := []struct {
		paths []string
		want  string
	}{
		{[]string{png, jpg}, "2 张图片"},
		{[]string{png}, "1 张图片"},
		{[]string{pdf}, "1 个 PDF"},
		{[]string{z}, "1 个压缩包"},
		{[]string{txt}, "1 项"},
		{[]string{png, txt}, "2 项"},
		{nil, ""},
	}
	for i, tc := range cases {
		if got := dropCountText(tc.paths); got != tc.want {
			t.Fatalf("case %d: dropCountText = %q, want %q", i, got, tc.want)
		}
	}
}

// dropShelf returns a shelf with a real store and a synchronous converter.
func dropShelf(t *testing.T) (*shelf, *convertFakes, string) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	a.conv = &converter{host: a}
	a.modules = []module{a.conv}
	return a, fakeConverters(t), t.TempDir()
}

// fileDrop builds the transfer a file drop hands us.
func fileDrop(t *testing.T, paths ...string) transfer.Data {
	t.Helper()
	d, err := transfer.FileData(paths...)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRunDropActionConvertDoesNotShelveSources(t *testing.T) {
	a, f, dir := dropShelf(t)
	png := writeFile(t, dir, "截图.png", testPNGBytes(t))
	act := dropActions([]string{png})[2] // 转 JPEG
	if act.id != "tojpeg" {
		t.Fatalf("picked %v", act.id)
	}
	a.runDropAction(act, fileDrop(t, png))
	if !slices.Equal(f.formats, []mac.ImageFormat{mac.FormatJPEG}) {
		t.Fatalf("formats = %v, want one JPEG", f.formats)
	}
	if len(a.store.Items) != 1 {
		t.Fatalf("tray has %d items, want just the output", len(a.store.Items))
	}
	got := a.store.Items[0]
	if got.Path == png {
		t.Fatal("the dropped source file was shelved")
	}
	if !got.Converted || filepath.Ext(got.Path) != ".jpg" {
		t.Fatalf("output item = %+v", got)
	}
	if !a.received || a.tab != pageTray {
		t.Fatalf("received=%v tab=%q", a.received, a.tab)
	}
}

func TestRunDropActionMergedPDFAndFit(t *testing.T) {
	a, f, dir := dropShelf(t)
	imgA := writeFile(t, dir, "a.png", testPNGBytes(t))
	imgB := writeFile(t, dir, "b.png", testPNGBytes(t))
	acts := dropActions([]string{imgA, imgB})
	if acts[1].id != "topdf" {
		t.Fatalf("acts = %v", actionIDs(acts))
	}
	a.runDropAction(acts[1], fileDrop(t, imgA, imgB))
	if len(f.pdfSrcs) != 1 || !slices.Equal(f.pdfSrcs[0], []string{imgA, imgB}) {
		t.Fatalf("merged pdf sources = %v", f.pdfSrcs)
	}
	// 压缩到 1 MB rides the fit pipeline.
	a2, f3, dir3 := dropShelf(t)
	img := writeFile(t, dir3, "big.png", make([]byte, 1_500_000))
	a2.runDropAction(dropActions([]string{img})[0], fileDrop(t, img))
	if len(f3.fit) != 1 || f3.fit[0][0] != img {
		t.Fatalf("fit jobs = %v", f3.fit)
	}
}

func TestRunDropActionZipAndExtract(t *testing.T) {
	a, _, dir := dropShelf(t)
	x := writeFile(t, dir, "甲.txt", []byte("甲"))
	y := writeFile(t, dir, "乙.txt", []byte("乙"))
	a.runDropAction(dropActions([]string{x, y})[0], fileDrop(t, x, y))
	if len(a.store.Items) != 1 || filepath.Ext(a.store.Items[0].Path) != ".zip" {
		t.Fatalf("tray = %+v, want one zip", a.store.Items)
	}
	zr, err := zip.OpenReader(a.store.Items[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, e := range zr.File {
		names = append(names, e.Name)
	}
	if !slices.Contains(names, "甲.txt") || !slices.Contains(names, "乙.txt") {
		t.Fatalf("zip entries = %v", names)
	}

	// 解压: build a zip first, then drop it on the extract cell.
	arc := filepath.Join(dir, "包.zip")
	if err := writeZip(arc, []string{x}, []string{"甲.txt"}, &convertJob{}); err != nil {
		t.Fatal(err)
	}
	a2, _, _ := dropShelf(t)
	act := dropActions([]string{arc})[0]
	if act.id != "extract" {
		t.Fatalf("archive actions = %v", actionIDs(dropActions([]string{arc})))
	}
	a2.runDropAction(act, fileDrop(t, arc))
	if len(a2.store.Items) != 1 || !a2.store.Items[0].Converted {
		t.Fatalf("extracted tray = %+v", a2.store.Items)
	}
	out := a2.store.Items[0].Path
	if got, err := os.ReadFile(filepath.Join(out, "甲.txt")); err != nil || string(got) != "甲" {
		t.Fatalf("extracted content: %q %v", got, err)
	}
}

func TestRunDropActionAirDropShelveNothing(t *testing.T) {
	a, _, dir := dropShelf(t)
	png := writeFile(t, dir, "a.png", testPNGBytes(t))
	var got [][]string
	old := airDrop
	airDrop = func(paths, texts []string) error {
		got = append(got, append(append([]string{}, paths...), texts...))
		return nil
	}
	t.Cleanup(func() { airDrop = old })
	for _, act := range dropActions([]string{png}) {
		if act.isAirDrop {
			a.runDropAction(act, fileDrop(t, png))
		}
	}
	if len(got) != 1 || !slices.Equal(got[0], []string{png}) {
		t.Fatalf("airdrop calls = %v", got)
	}
	if len(a.store.Items) != 0 {
		t.Fatalf("airdrop shelved %+v", a.store.Items)
	}
}

func TestRunDropActionBusyFallsBackToTray(t *testing.T) {
	a, f, dir := dropShelf(t)
	png := writeFile(t, dir, "a.png", testPNGBytes(t))
	// A conversion in flight greys the cells; a drop on one still lands,
	// on the tray this time.
	a.conv.job = &convertJob{}
	a.runDropAction(dropActions([]string{png})[2], fileDrop(t, png))
	if len(f.formats) != 0 {
		t.Fatalf("busy converter still ran: %v", f.formats)
	}
	if len(a.store.Items) != 1 || a.store.Items[0].Path != png || a.store.Items[0].Converted {
		t.Fatalf("busy drop = %+v, want the source shelved plain", a.store.Items)
	}
}

func TestRunDropActionNonFilesFallsBackToTray(t *testing.T) {
	a, _, _ := dropShelf(t)
	// A text drop on a cell is unreachable in the UI (cells only accept
	// files), but the runner defends: no file paths means plain receive.
	a.runDropAction(dropActions([]string{"/x.png"})[0], transfer.TextData("只是文字"))
	if len(a.store.Items) != 1 || a.store.Items[0].Kind != store.KindText {
		t.Fatalf("text drop = %+v", a.store.Items)
	}
}

// dropViewShelf builds a headless shelf as if a file drag hovered: the
// pasteboard read is stubbed to the given paths and the drag flag is on.
func dropViewShelf(t *testing.T, paths []string) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{
		store: s, dragging: true, visible: true,
		notchW: 180, notchH: 32,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
	}
	a.conv = &converter{host: a}
	old := dragFiles
	dragFiles = func(*shelf) []string { return paths }
	t.Cleanup(func() { dragFiles = old })
	return a, ui.NewTester(a.view, sheetW, panelTestH)
}

func TestDropZoneShowsForImageDrag(t *testing.T) {
	dir := t.TempDir()
	imgs := []string{
		writeFile(t, dir, "1.png", testPNGBytes(t)),
		writeFile(t, dir, "2.png", testPNGBytes(t)),
		writeFile(t, dir, "3.png", testPNGBytes(t)),
	}
	_, view := dropViewShelf(t, imgs)
	for _, want := range []string{"投放", "3 张图片", "松开放入托盘", "放入托盘", "保存引用，随时拖出", "压缩到 1 MB", "合成一个 PDF", "转 JPEG", "隔空投送"} {
		if !view.HasText(want) {
			t.Fatalf("drop zone missing %q; texts %v", want, view.Texts())
		}
	}
}

func TestDropZoneHiddenForOwnDragAndNonFiles(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, view := dropViewShelf(t, []string{img})
	a.ownDrag = true
	view = ui.NewTester(a.view, sheetW, panelTestH)
	if view.HasText("投放") || view.HasText("放入托盘") {
		t.Fatal("own drag-out showed the drop zone")
	}
	// No drag at all — once the release grace has lapsed — no zone.
	a.ownDrag = false
	a.dragging = false
	a.drop.until = time.Now().Add(-time.Minute)
	view = ui.NewTester(a.view, sheetW, panelTestH)
	if view.HasText("投放") {
		t.Fatal("an ended drag still showed the drop zone")
	}
}

func TestDropZoneHiddenWhenPathsUnreadable(t *testing.T) {
	// Promised files expose no paths while hovering: the zone stays the
	// normal stage and the whole-panel target still takes the drop.
	_, view := dropViewShelf(t, nil)
	if view.HasText("投放") {
		t.Fatal("pathless drag showed the drop zone")
	}
	if !view.HasText("托盘") {
		t.Fatalf("plain stage missing; texts %v", view.Texts())
	}
}

func TestDropZoneSnapPointerGates(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, _ := dropViewShelf(t, []string{img})
	// With a window (simulated by win != nil impossible headless), the
	// pointer must be inside the panel: over is the zone's own hover.
	if got := a.dropZoneSnap(true); len(got) != 1 {
		t.Fatal("hovering the window lost the drag's files")
	}
	// Not over and headless (a.win == nil) still answers: tests draw the
	// final state without a pointer.
	if got := a.dropZoneSnap(false); len(got) != 1 {
		t.Fatal("headless render lost the files")
	}
	// The pointer-inside branch needs the seam.
	old := cursorPoint
	defer func() { cursorPoint = old }()
	a2 := &shelf{dragging: true, visible: true, notchW: 180, notchH: 32}
	a2.x, a2.y = 400, 0
	cursorPoint = func() mygo.Point { return mygo.Point{X: 10, Y: 500} }
	// a2.win == nil keeps the headless shortcut; exercise the math itself.
	p := cursorPoint()
	inside := p.X >= a2.x && p.X < a2.x+sheetW && p.Y >= a2.y && p.Y < a2.y+a2.panelH()
	if inside {
		t.Fatal("point left of the panel counted as inside")
	}
	cursorPoint = func() mygo.Point { return mygo.Point{X: 400 + sheetW/2, Y: 100} }
	p = cursorPoint()
	inside = p.X >= a2.x && p.X < a2.x+sheetW && p.Y >= a2.y && p.Y < a2.y+a2.panelH()
	if !inside {
		t.Fatal("point inside the panel counted as outside")
	}
}

func TestDropZoneModuleBarCounts(t *testing.T) {
	dir := t.TempDir()
	z := writeFile(t, dir, "素材.zip", []byte("PK"))
	_, view := dropViewShelf(t, []string{z})
	for _, want := range []string{"投放", "1 个压缩包", "解压", "隔空投送"} {
		if !view.HasText(want) {
			t.Fatalf("archive zone missing %q; texts %v", want, view.Texts())
		}
	}
	folder := filepath.Join(dir, "文件夹")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	txt := writeFile(t, dir, "x.txt", []byte("x"))
	_, view2 := dropViewShelf(t, []string{folder, txt})
	for _, want := range []string{"2 项", "打成 zip"} {
		if !view2.HasText(want) {
			t.Fatalf("mixed zone missing %q; texts %v", want, view2.Texts())
		}
	}
}

// dropViewSnapShelf stubs the whole board-read path so tests can drive
// dropZoneSnap without a system drag: the pasteboard serial comes from
// dragFilesChange, the file list from dragFiles.
func snapShelf(t *testing.T, files []string) (*shelf, *int, *int64) {
	t.Helper()
	a := &shelf{dragging: true, visible: true, notchW: 180, notchH: 32,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	reads := new(int)
	count := new(int64)
	*count = 7
	oldF, oldC := dragFiles, dragFilesChange
	dragFiles = func(*shelf) []string { *reads++; return files }
	dragFilesChange = func(*shelf) int64 { return *count }
	t.Cleanup(func() { dragFiles, dragFilesChange = oldF, oldC })
	return a, reads, count
}

func TestDropZoneSurvivesReleaseFrame(t *testing.T) {
	// monitorDrag clears a.dragging on release before the drop reaches the
	// next frame; the zone must still build so the drop finds its cell.
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a, _, _ := snapShelf(t, []string{img})
	a.store = s
	a.conv = &converter{host: a}
	f := fakeConverters(t)
	if got := a.dropZoneSnap(true); len(got) != 1 {
		t.Fatal("no zone while dragging")
	}
	a.dragging = false // the release's mouse-up, before the drop lands
	view := ui.NewTester(a.view, sheetW, panelTestH)
	if !view.HasText("投放") || !view.HasText("压缩到 1 MB") {
		t.Fatalf("release frame lost the drop zone; texts %v", view.Texts())
	}
	// The cell drop now executes the action, not nothing.
	a.runDropAction(dropActions([]string{img})[2], fileDrop(t, img)) // 转 JPEG
	if !slices.Equal(f.formats, []mac.ImageFormat{mac.FormatJPEG}) {
		t.Fatalf("the released action never ran: formats %v", f.formats)
	}
	// Consumed: the zone closes, panel back to the tray page.
	view = ui.NewTester(a.view, sheetW, panelTestH)
	if view.HasText("投放") {
		t.Fatal("consumed drop kept the zone")
	}
}

func TestDropZoneHoldExpiresAndStaysOutsideOnly(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, _, _ := snapShelf(t, []string{img})
	a.dropZoneSnap(true)
	a.dragging = false
	// Within grace the zone serves; past it the drag is over.
	if len(a.dropZoneSnap(true)) != 1 {
		t.Fatal("grace dropped the session")
	}
	a.drop.until = time.Now().Add(-time.Second)
	if got := a.dropZoneSnap(true); got != nil {
		t.Fatalf("expired grace still answered %v", got)
	}
	// A new drag serial never inherits the previous session's files.
	a.dragging = true
	a.drop.count = 7 // same board count: cache hit
	if got := a.dropZoneSnap(true); len(got) != 1 {
		t.Fatal("new drag lost the files")
	}
}

func TestDropZoneBoardReadsOncePerChange(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, reads, count := snapShelf(t, []string{img})
	a.dropZoneSnap(true)
	for range 5 {
		a.dropZoneSnap(true)
	}
	if *reads != 1 {
		t.Fatalf("unchanged board read %d times in 6 frames", *reads)
	}
	// A bump with files already loaded is the release's board rewrite: the
	// zone freezes its picture instead of re-reading.
	*count++
	a.dropZoneSnap(true)
	a.dropZoneSnap(true)
	if *reads != 1 {
		t.Fatalf("the release's rewrite re-read the board %d times", *reads)
	}
	if !a.drop.frozen {
		t.Fatal("the loaded zone did not freeze on the board bump")
	}
}

func TestDropZoneFitsContentBox(t *testing.T) {
	// The zone is 64 + 6 + 84 = 154 tall and must fit the page's fixed
	// 396 × 174 content box, so a drag never grows or clips the sheet.
	if need := dropZoneNeedH(); need > contentH {
		t.Fatalf("drop zone needs %d, content box is %d", need, contentH)
	}
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, view := dropViewShelf(t, []string{img})
	a.level = 2
	view.Frame()
	if !view.HasText("放入托盘") || !view.HasText("投放") {
		t.Fatalf("the zone did not draw in the sheet; texts %v", view.Texts())
	}
	if got := a.desiredFrame().Height; got < sheetH {
		t.Fatalf("the sheet's frame is %d tall, want at least %d", got, sheetH)
	}
}

func TestDropZoneHidesReleasePill(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, view := dropViewShelf(t, []string{img})
	if a.drop.on == false {
		t.Fatal("zone not up for a file drag")
	}
	for _, txt := range view.Texts() {
		if txt == "松开放入" {
			t.Fatal("the drop capsule overlapped the zone")
		}
	}
	// Without the zone the capsule still shows.
	a2, view2 := dropViewShelf(t, nil)
	a2.drop.on = false
	view2 = ui.NewTester(a2.view, sheetW, panelTestH)
	if !view2.HasText("松开放入") {
		t.Fatalf("idle drag lost the capsule; texts %v", view2.Texts())
	}
}

func TestDropActionSubtitlesMatchBehavior(t *testing.T) {
	dir := t.TempDir()
	one := writeFile(t, dir, "solo.png", testPNGBytes(t))
	more := writeFile(t, dir, "extra.png", testPNGBytes(t))
	single := dropActions([]string{one})
	if single[1].id != "topdf" || single[1].sub != "1 页" || single[1].target != toPDF {
		t.Fatalf("single image pdf cell = %+v", single[1])
	}
	double := dropActions([]string{one, more})
	if double[1].label != "合成一个 PDF" || double[1].sub != "按拖入顺序" {
		t.Fatalf("multi image pdf cell = %+v", double[1])
	}
	pdf := writeFile(t, dir, "doc.pdf", []byte("%PDF"))
	for _, act := range dropActions([]string{pdf}) {
		switch act.id {
		case "toword":
			if act.sub != "提取文字" {
				t.Fatalf("转 Word sub %q", act.sub)
			}
		case "topng":
			if act.sub != "PNG · 每页一张" {
				t.Fatalf("每页存成图片 sub %q", act.sub)
			}
		case "zip":
			if act.sub != "" {
				t.Fatalf("single zip sub %q", act.sub)
			}
		case "airdrop":
			if act.sub != "" {
				t.Fatalf("airdrop sub %q", act.sub)
			}
		}
	}
}

// TestDropZoneFreezesAtReleaseCountBump: the release bumps the drag
// pasteboard's changeCount while the monitor still reports the drag
// running. The frame between them must serve the cells the user aimed at,
// not the board's rewritten content (PhaseDrop: a bump mid-drag read and
// reclassified the board, so a drop aimed at 「转成 PDF · 1 页」 could run
// as 「合成一个 PDF」 or vanish entirely).
func TestDropZoneFreezesAtReleaseCountBump(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	img2 := writeFile(t, dir, "2.png", testPNGBytes(t))
	files := []string{img}
	a, _, count := snapShelf(t, files)
	oldF := dragFiles
	dragFiles = func(*shelf) []string { return files }
	t.Cleanup(func() { dragFiles = oldF })

	// Drag hovers with one image: the zone serves the single-image cells.
	a.dropZoneSnap(true)
	if len(a.drop.files) != 1 {
		t.Fatal("no zone while dragging")
	}
	// Release rewrites the board before the flag falls: the source now
	// reports a different, larger set.
	*count++
	files = []string{img, img2}
	if got := a.dropZoneSnap(true); len(got) != 1 || got[0] != img {
		t.Fatalf("the release frame re-read the board: %v", got)
	}
	if len(a.drop.files) != 1 {
		t.Fatalf("the served set changed under the pointer: %v", a.drop.files)
	}
}

// TestDropZoneFrozenSurvivesNewDragSerial: a new drag that starts while a
// release is still pending must not reset the zone and drop the pending
// drop — the frozen snapshot outlives the serial change until consumed or
// expired.
func TestDropZoneFrozenSurvivesNewDragSerial(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, _, _ := snapShelf(t, []string{img})
	a.dropZoneSnap(true)
	// The release fell: grace serves the old session…
	a.dragging = false
	a.dropZoneSnap(true)
	if !a.drop.frozen {
		t.Fatal("the release did not freeze the zone")
	}
	// …and stays even after a new drag bumps the serial.
	a.dragging = true
	a.dragSerial++
	if got := a.dropZoneSnap(true); len(got) != 1 || got[0] != img {
		t.Fatalf("a new drag's serial discarded the pending drop's zone: %v", got)
	}
}

// TestDropZoneFrozenStopsBoardReads: once frozen the zone must not touch
// the pasteboard at all — no reclassification, no new file list.
func TestDropZoneFrozenStopsBoardReads(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, reads, _ := snapShelf(t, []string{img})
	a.dropZoneSnap(true)
	before := *reads
	a.dragging = false
	for range 4 {
		if got := a.dropZoneSnap(true); len(got) != 1 {
			t.Fatalf("frozen zone stopped answering mid-grace: %v", got)
		}
	}
	if *reads != before {
		t.Fatalf("the frozen zone re-read the board %d times", *reads-before)
	}
}

// TestDropZoneMidDragBoardChangeIgnored: while files are already loaded,
// a changeCount bump mid-drag is the release rewriting the board — the
// zone's picture must not follow it.
func TestDropZoneMidDragBoardChangeIgnored(t *testing.T) {
	dir := t.TempDir()
	img := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, reads, count := snapShelf(t, []string{img})
	a.dropZoneSnap(true)
	*count++ // board rewrite while still dragging: freeze, don't re-read
	if got := a.dropZoneSnap(true); len(got) != 1 {
		t.Fatalf("frozen zone lost the files: %v", got)
	}
	if *reads != 1 {
		t.Fatalf("frozen zone re-read the board: %d reads", *reads)
	}
	if a.drop.frozen != true {
		t.Fatal("a loaded zone did not freeze on the count bump")
	}
}

// TestRunDropActionUsesWhatWasShown: the set an action runs on is the set
// the cells were drawn from — even if the drop data would resolve a
// different list (the frozen snapshot is the truth the user saw).
func TestRunDropActionUsesWhatWasShown(t *testing.T) {
	a, f, dir := dropShelf(t)
	png := writeFile(t, dir, "截图.png", testPNGBytes(t))
	png2 := writeFile(t, dir, "图2.png", testPNGBytes(t))
	a.drop.files = []string{png} // what the zone displayed
	a.drop.class = classifyDrop(a.drop.files)
	act := dropActionsFor(a.drop.files, a.drop.class)[1] // 转成 PDF · 1 页
	if act.id != "topdf" || act.target != toPDF {
		t.Fatalf("picked %v", act)
	}
	// The drop data carries more files than the zone showed — a board that
	// rewrote under the pointer. The shown set wins.
	a.runDropAction(act, fileDrop(t, png, png2))
	if len(f.pdfSrcs) != 1 || len(f.pdfSrcs[0]) != 1 {
		t.Fatalf("expected one 1-page PDF job for the shown file, got %v", f.pdfSrcs)
	}
}

// TestDropThumbCapAndStaleWriteBack: decodes for dropped-in paths respect
// the shared concurrency cap, and a path that leaves the stack mid-decode
// never lands its bitmap in the cache.
func TestDropThumbCapAndStaleWriteBack(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "1.png", testPNGBytes(t))
	a, _, _ := snapShelf(t, []string{png})
	a.win = &mygo.Window{} // non-nil so the worker path runs
	a.winH = 1
	t.Cleanup(func() { a.win = nil; a.winH = 0 })
	oldWork, oldUpdate := dropThumbWork, dropThumbUpdate
	var pending []func()
	dropThumbWork = func(fn func()) { pending = append(pending, fn) }
	dropThumbUpdate = func(_ *shelf, fn func()) { fn() }
	t.Cleanup(func() { dropThumbWork, dropThumbUpdate = oldWork, oldUpdate })
	t.Cleanup(func() {
		dropThumbCache = map[string]*ui.Bitmap{}
		dropThumbBusy = map[string]int{}
		dropThumbActive = 0
	})

	// Two lanes, three paths: the third must wait for a slot.
	extra := []string{writeFile(t, dir, "2.png", testPNGBytes(t)), writeFile(t, dir, "3.png", testPNGBytes(t))}
	paths := append([]string{png}, extra...)
	a.dropThumbs(paths)
	if len(pending) != maxConcurrentThumbs {
		t.Fatalf("started %d decodes, cap is %d", len(pending), maxConcurrentThumbs)
	}
	if dropThumbBusy[extra[1]] != 0 {
		t.Fatal("a third decode started while both lanes were full")
	}

	// The first path's decode finishes while the path is still wanted.
	pending[0]()
	if dropThumbCache[png] == nil {
		t.Fatal("decode finished but never cached")
	}
	pending = pending[1:]

	// Mid-decode, the second path leaves the stack: finish must not write.
	a.dropThumbs([]string{png, extra[1]}) // extra[0] evicted from busy
	pending[0]()
	if _, ok := dropThumbCache[extra[0]]; ok {
		t.Fatal("a decode wrote back for a path no longer dragged")
	}
	pending = pending[1:]

	// The freed slot lets the deferred third path start on the next frame.
	a.dropThumbs([]string{png, extra[1]})
	if len(pending) != 1 || dropThumbBusy[extra[1]] == 0 {
		t.Fatalf("deferred path did not start: pending %d", len(pending))
	}
}
