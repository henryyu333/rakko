//go:build darwin

package main

// Unit tests for renamer.go: the three modes' name math, extension
// handling, every conflict the preview flags, and the two-phase
// executor's swap/case-only/rollback guarantees — all on real files in
// temporary directories (never the user's own).

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"rakko/internal/store"
)

// mkFile writes a small file and returns its path.
func mkFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fileItem wraps a path as the KindFile item a tray row would carry.
func fileItem(p string) store.Item {
	return store.Item{ID: p, Kind: store.KindFile, Path: p}
}

func namesIn(t *testing.T, dir string) map[string]bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range ents {
		got[e.Name()] = true
	}
	return got
}

// The three modes produce Finder's name math: replace swaps text inside
// the base, add glues text on either side, format numbers with the custom
// name. Extensions never take part.
func TestPlanRenameModes(t *testing.T) {
	dir := t.TempDir()
	items := []store.Item{
		fileItem(mkFile(t, dir, "照片 1.jpg")),
		fileItem(mkFile(t, dir, "照片 2.jpg")),
	}
	pl := planRename(items, renameParams{mode: modeReplace, find: "照片", replace: "图片"})
	if pl.conflicts != 0 || pl.changed != 2 {
		t.Fatalf("replace plan: %+v", pl.rows)
	}
	if pl.rows[0].newName != "图片 1.jpg" || pl.rows[1].newName != "图片 2.jpg" {
		t.Fatalf("replace names: %q %q", pl.rows[0].newName, pl.rows[1].newName)
	}

	pl = planRename(items, renameParams{mode: modeAdd, addText: "精选-", addBefore: true})
	if pl.rows[0].newName != "精选-照片 1.jpg" {
		t.Fatalf("add before: %q", pl.rows[0].newName)
	}
	pl = planRename(items, renameParams{mode: modeAdd, addText: "-精选"})
	if pl.rows[0].newName != "照片 1-精选.jpg" {
		t.Fatalf("add after: %q", pl.rows[0].newName)
	}

	pl = planRename(items, renameParams{mode: modeFormat, fmtName: "旅行", fmtStart: 3, fmtDigits: 2, fmtNumLast: true})
	if pl.rows[0].newName != "旅行 03.jpg" || pl.rows[1].newName != "旅行 04.jpg" {
		t.Fatalf("format name-first: %q %q", pl.rows[0].newName, pl.rows[1].newName)
	}
	pl = planRename(items, renameParams{mode: modeFormat, fmtName: "旅行", fmtStart: 1, fmtDigits: 1})
	if pl.rows[0].newName != "1 旅行.jpg" {
		t.Fatalf("format number-first: %q", pl.rows[0].newName)
	}
	pl = planRename(items, renameParams{mode: modeFormat, fmtStart: 1, fmtDigits: 3})
	if pl.rows[0].newName != "001.jpg" {
		t.Fatalf("format empty name: %q", pl.rows[0].newName)
	}
}

// Extensions stay out of the rules: replace touches only the base, and a
// dotfile's leading dot is part of the name, not an extension.
func TestSplitNameAndExtensionSafety(t *testing.T) {
	base, ext := splitName("archive.tar.gz")
	if base != "archive.tar" || ext != ".gz" {
		t.Fatalf("splitName: %q %q", base, ext)
	}
	if b, e := splitName(".gitignore"); b != ".gitignore" || e != "" {
		t.Fatalf("dotfile: %q %q", b, e)
	}
	dir := t.TempDir()
	items := []store.Item{fileItem(mkFile(t, dir, "a.tar.gz"))}
	pl := planRename(items, renameParams{mode: modeReplace, find: "a", replace: "X"})
	if pl.rows[0].newName != "X.tXr.gz" && pl.rows[0].newName != "X.tar.gz" {
		// replace applies to the base "a.tar" only: 'a' at position 0 and
		// inside "tar" both count.
		t.Fatalf("replace must not touch the extension: %q", pl.rows[0].newName)
	}
	pl = planRename(items, renameParams{mode: modeReplace, find: ".gz", replace: ""})
	if pl.rows[0].newName == "a.tar" {
		t.Fatal("replace must not strip the extension")
	}
}

// Every conflict the spec lists flags its row: a target already on disk,
// two rows wanting one name, and the illegal names.
func TestPlanRenameConflicts(t *testing.T) {
	dir := t.TempDir()
	a := mkFile(t, dir, "a.txt")
	taken := mkFile(t, dir, "taken.txt")

	// Target occupied by a file outside the batch.
	pl := planRename([]store.Item{fileItem(a)}, renameParams{mode: modeAdd, addText: "renamed-"})
	_ = pl
	pl = planRename([]store.Item{fileItem(a)}, renameParams{mode: modeReplace, find: "a", replace: "taken"})
	if pl.rows[0].warn != "已存在同名文件" || pl.conflicts != 1 {
		t.Fatalf("existing target must conflict: %+v", pl.rows[0])
	}
	// The file it would take is still there, byte-identical.
	if data, _ := os.ReadFile(taken); string(data) != "x" {
		t.Fatal("target file changed")
	}

	// Two rows converging on one name conflict with each other.
	x := mkFile(t, dir, "x1.txt")
	y := mkFile(t, dir, "1x.txt")
	pl = planRename([]store.Item{fileItem(x), fileItem(y)},
		renameParams{mode: modeReplace, find: "x", replace: ""})
	if pl.conflicts != 2 || pl.rows[0].warn != "和本批其他项目重名" {
		t.Fatalf("batch collision: %+v", pl.rows)
	}

	// Illegal names: emptied by replace, separators, over-long.
	pl = planRename([]store.Item{fileItem(a)}, renameParams{mode: modeReplace, find: "a", replace: ""})
	if pl.rows[0].warn != "名字不能为空" {
		t.Fatalf("empty name: %+v", pl.rows[0])
	}
	pl = planRename([]store.Item{fileItem(a)}, renameParams{mode: modeAdd, addText: "x/"})
	if pl.rows[0].warn != "名字不能包含 / 或 :" {
		t.Fatalf("slash name: %+v", pl.rows[0])
	}
	pl = planRename([]store.Item{fileItem(a)}, renameParams{mode: modeAdd, addText: "x:"})
	if pl.rows[0].warn != "名字不能包含 / 或 :" {
		t.Fatalf("colon name: %+v", pl.rows[0])
	}
	pl = planRename([]store.Item{fileItem(a)}, renameParams{mode: modeAdd, addText: strings.Repeat("长", 90)})
	if pl.rows[0].warn != "名字过长" {
		t.Fatalf("long name: %q warn=%q", pl.rows[0].newName, pl.rows[0].warn)
	}

	// A vanished source flags instead of failing mid-batch.
	ghost := fileItem(filepath.Join(dir, "ghost.txt"))
	pl = planRename([]store.Item{ghost}, renameParams{mode: modeAdd, addText: "z"})
	if pl.rows[0].warn != "原文件不存在" {
		t.Fatalf("missing source: %+v", pl.rows[0])
	}
}

// writePuts writes names with distinct contents so a move can be checked
// by what ended up where, not just by which names exist.
func writePuts(t *testing.T, dir string, puts map[string]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, body := range puts {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		out[name] = p
	}
	return out
}

// readPuts reads the named files back by name.
func readPuts(t *testing.T, dir string, names ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		out[n] = string(data)
	}
	return out
}

// execRename moves the whole batch through temp names, so a↔b and a→b,
// b→c chains land without overwriting; the tray's order is the op order.
// Each file holds its own letter so every move can be checked by content.
func TestExecRenameSwapAndChain(t *testing.T) {
	dir := t.TempDir()
	p := writePuts(t, dir, map[string]string{"a.txt": "A", "b.txt": "B"})
	if err := execRename([]renameOp{{p["a.txt"], p["b.txt"]}, {p["b.txt"], p["a.txt"]}}); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if got := readPuts(t, dir, "a.txt", "b.txt"); got["a.txt"] != "B" || got["b.txt"] != "A" {
		t.Fatalf("swap contents = %v, want a=B b=A", got)
	}
	if names := namesIn(t, dir); len(names) != 2 {
		t.Fatalf("swap left %v", names)
	}

	// Chain: a→b, b→c, c→a (a rotation; every target is a batch source).
	p["c.txt"] = writePuts(t, dir, map[string]string{"c.txt": "C"})["c.txt"]
	if err := execRename([]renameOp{
		{p["a.txt"], p["b.txt"]},
		{p["b.txt"], p["c.txt"]},
		{p["c.txt"], p["a.txt"]},
	}); err != nil {
		t.Fatalf("chain: %v", err)
	}
	if got := readPuts(t, dir, "a.txt", "b.txt", "c.txt"); got["a.txt"] != "C" || got["b.txt"] != "B" || got["c.txt"] != "A" {
		t.Fatalf("chain contents = %v, want a=C b=B c=A", got)
	}
	if names := namesIn(t, dir); len(names) != 3 {
		t.Fatalf("chain left %v", names)
	}
}

// A chain that fails midway restores every original name with its own
// content: finished targets first return to their temp names (their
// original name is still held by the next file in the chain), then all
// temps go home. Nothing may be left under a hidden temp name.
func TestExecRenameChainFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	p := writePuts(t, dir, map[string]string{
		"a.txt": "A", "xa.txt": "B", "xxa.txt": "C", "xxxa.txt": "blocked",
	})
	err := execRename([]renameOp{
		{p["a.txt"], p["xa.txt"]},
		{p["xa.txt"], p["xxa.txt"]},
		{p["xxa.txt"], p["xxxa.txt"]},
	})
	if err == nil {
		t.Fatal("the occupied chain end must fail")
	}
	if !strings.Contains(err.Error(), "已全部恢复原状") {
		t.Fatalf("error should say everything was restored: %v", err)
	}
	got := readPuts(t, dir, "a.txt", "xa.txt", "xxa.txt", "xxxa.txt")
	want := map[string]string{"a.txt": "A", "xa.txt": "B", "xxa.txt": "C", "xxxa.txt": "blocked"}
	for name, body := range want {
		if got[name] != body {
			t.Fatalf("%s = %q, want %q (all: %v)", name, got[name], body, got)
		}
	}
	for name := range namesIn(t, dir) {
		if strings.Contains(name, ".shelf-renaming-") {
			t.Fatalf("rollback left a temp name %q", name)
		}
	}
}

// A case-only rename is a real rename on APFS (case-insensitive by
// default): it plans clean and executes.
func TestExecRenameCaseOnly(t *testing.T) {
	dir := t.TempDir()
	a := mkFile(t, dir, "Photo.JPG")
	it := fileItem(a)
	pl := planRename([]store.Item{it}, renameParams{mode: modeReplace, find: "Photo", replace: "photo"})
	if pl.conflicts != 0 {
		t.Fatalf("case-only rename must not conflict: %+v", pl.rows[0])
	}
	if err := execRename(pl.ops); err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
	if names := namesIn(t, dir); !names["photo.JPG"] {
		t.Fatalf("expected photo.JPG, got %v", names)
	}
}

// A failed move rolls every finished one back: the batch lands all or
// none, and nothing is ever overwritten.
func TestExecRenameRollback(t *testing.T) {
	dir := t.TempDir()
	a := mkFile(t, dir, "a.txt")
	b := mkFile(t, dir, "b.txt")
	c := mkFile(t, dir, "c.txt")
	mkFile(t, dir, "blocker.txt")
	// Block c's target with a real file so phase 2 fails on the last op.
	ops := []renameOp{
		{src: a, dst: filepath.Join(dir, "a2.txt")},
		{src: b, dst: filepath.Join(dir, "b2.txt")},
		{src: c, dst: filepath.Join(dir, "blocker.txt")},
	}
	err := execRename(ops)
	if err == nil {
		t.Fatal("expected the occupied target to fail")
	}
	if !strings.Contains(err.Error(), "已全部恢复原状") {
		t.Fatalf("error should say everything was restored: %v", err)
	}
	// Every original is back, the blocker intact, no temp left behind.
	if names := namesIn(t, dir); len(names) != 4 ||
		!names["a.txt"] || !names["b.txt"] || !names["c.txt"] || !names["blocker.txt"] {
		t.Fatalf("rollback left %v", names)
	}
	for _, n := range []string{"a.txt", "b.txt", "c.txt", "blocker.txt"} {
		if data, _ := os.ReadFile(filepath.Join(dir, n)); string(data) != "x" {
			t.Fatalf("%s was overwritten", n)
		}
	}
}

// A mid-phase-1 failure (second source gone) rolls back the first move.
func TestExecRenamePhase1Rollback(t *testing.T) {
	dir := t.TempDir()
	a := mkFile(t, dir, "a.txt")
	gone := filepath.Join(dir, "gone.txt")
	ops := []renameOp{
		{src: a, dst: filepath.Join(dir, "a2.txt")},
		{src: gone, dst: filepath.Join(dir, "g2.txt")},
	}
	if err := execRename(ops); err == nil {
		t.Fatal("expected failure on missing source")
	}
	if names := namesIn(t, dir); len(names) != 1 || !names["a.txt"] {
		t.Fatalf("phase-1 rollback left %v", names)
	}
}

// The seam-based failure exercises the same path deterministically.
func TestExecRenameSeamFailure(t *testing.T) {
	dir := t.TempDir()
	a := mkFile(t, dir, "a.txt")
	b := mkFile(t, dir, "b.txt")
	orig := renameNoClobber
	fail := false
	renameNoClobber = func(src, dst string) error {
		if fail && filepath.Base(dst) == "b2.txt" {
			return syscall.EACCES
		}
		return orig(src, dst)
	}
	t.Cleanup(func() { renameNoClobber = orig })
	fail = true
	err := execRename([]renameOp{
		{src: a, dst: filepath.Join(dir, "a2.txt")},
		{src: b, dst: filepath.Join(dir, "b2.txt")},
	})
	if err == nil || !strings.Contains(err.Error(), "没有权限") {
		t.Fatalf("want permission wording, got %v", err)
	}
	if names := namesIn(t, dir); len(names) != 2 || !names["a.txt"] || !names["b.txt"] {
		t.Fatalf("rollback left %v", names)
	}
}

// Undo is execRename in reverse; when an original name is already taken
// the undo fails without touching the occupant.
func TestUndoOccupiedOriginal(t *testing.T) {
	dir := t.TempDir()
	a := mkFile(t, dir, "a.txt")
	newA := filepath.Join(dir, "a-new.txt")
	if err := execRename([]renameOp{{src: a, dst: newA}}); err != nil {
		t.Fatal(err)
	}
	// Another file takes the original name after the rename.
	mkFile(t, dir, "a.txt")
	if data, _ := os.ReadFile(a); string(data) != "x" {
		t.Fatal("setup")
	}
	err := execRename([]renameOp{{src: newA, dst: a}})
	if err == nil {
		t.Fatal("undo over an occupied name must fail")
	}
	if !strings.Contains(err.Error(), "已存在同名文件") {
		t.Fatalf("want 已存在同名文件, got %v", err)
	}
	// The undo moved the renamed file to a temp then back; the newer
	// a.txt must be untouched and a-new.txt back in place.
	if names := namesIn(t, dir); len(names) != 2 || !names["a.txt"] || !names["a-new.txt"] {
		t.Fatalf("undo failure left %v", names)
	}
}

// renameErr words the errno cases in Chinese.
func TestRenameErrWording(t *testing.T) {
	for errno, want := range map[syscall.Errno]string{
		syscall.EEXIST:       "已存在同名文件",
		syscall.ENOENT:       "文件不存在",
		syscall.EACCES:       "没有权限",
		syscall.EPERM:        "没有权限",
		syscall.ENAMETOOLONG: "名字过长",
	} {
		if got := renameErr(errno); got != want {
			t.Fatalf("renameErr(%v) = %q, want %q", errno, got, want)
		}
	}
	if got := renameErr(errors.New("weird")); got != "weird" {
		t.Fatalf("unknown error passes through: %q", got)
	}
}

// planRename ops skip rows that do not change and rows that conflict, in
// display order.
func TestPlanOpsOnlyChangedCleanRows(t *testing.T) {
	dir := t.TempDir()
	same := mkFile(t, dir, "same.txt")
	two := mkFile(t, dir, "two.txt")
	items := []store.Item{fileItem(same), fileItem(two)}
	pl := planRename(items, renameParams{mode: modeAdd, addText: ""})
	if pl.changed != 0 || len(pl.ops) != 0 {
		t.Fatalf("empty add changes nothing: changed=%d ops=%v", pl.changed, pl.ops)
	}
	pl = planRename(items, renameParams{mode: modeReplace, find: "two", replace: "2"})
	if pl.changed != 1 || len(pl.ops) != 1 || pl.ops[0].dst != filepath.Join(dir, "2.txt") {
		t.Fatalf("ops = %+v", pl.ops)
	}
}

// A folder's dots are part of its name: the add mode never splits
// 「v1.2」 into a base and an extension.
func TestPlanRenameFolderKeepsDots(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "v1.2")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	pl := planRename([]store.Item{fileItem(sub)}, renameParams{mode: modeAdd, addText: "新-", addBefore: true})
	if pl.rows[0].newName != "新-v1.2" {
		t.Fatalf("folder name = %q, want 新-v1.2", pl.rows[0].newName)
	}
}

// A directory in the batch renames like a file (folders are legal
// targets, as in Finder).
func TestExecRenameFolder(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "folder")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	mkFile(t, sub, "inner.txt")
	dst := filepath.Join(dir, "renamed-folder")
	if err := execRename([]renameOp{{src: sub, dst: dst}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "inner.txt")); err != nil {
		t.Fatalf("contents must follow the folder: %v", err)
	}
}

// A folder and an item inside it cannot be renamed together: the folder's
// move takes the inner path away, so the batch would strand a file under
// its temp name.
func TestPlanRenameRejectsFolderAndInnerItem(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "资料 夹")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := mkFile(t, folder, "里面.txt")
	pl := planRename([]store.Item{fileItem(folder), fileItem(inner)},
		renameParams{mode: modeAdd, addText: "新-"})
	if pl.conflicts != 2 || len(pl.ops) != 0 {
		t.Fatalf("folder + inner must conflict: %+v", pl.rows)
	}
	const want = "不能同时改文件夹和它里面的项目"
	for _, row := range pl.rows {
		if row.warn != want {
			t.Fatalf("%s: warn = %q, want %q", row.oldName, row.warn, want)
		}
	}
	// Siblings in the same folder still rename fine.
	sib := mkFile(t, folder, "旁边.txt")
	pl = planRename([]store.Item{fileItem(inner), fileItem(sib)},
		renameParams{mode: modeAdd, addText: "新-"})
	if pl.conflicts != 0 || pl.changed != 2 {
		t.Fatalf("siblings must plan clean: %+v", pl.rows)
	}
}

// Names are compared in NFC: two rows whose targets differ only by how a
// character is composed are the same name to APFS, so they conflict.
func TestPlanRenameNormalizedDuplicate(t *testing.T) {
	dir := t.TempDir()
	// Both rows keep their own spelling and shed the same tail, so their
	// targets are byte-different but the same name to APFS.
	nfd := fileItem(filepath.Join(dir, "cafe\u0301 1.txt"))
	nfc := fileItem(filepath.Join(dir, "café 1.txt"))
	pl := planRename([]store.Item{nfd, nfc}, renameParams{mode: modeReplace, find: " 1", replace: ""})
	if pl.conflicts != 2 {
		t.Fatalf("NFC/NFD targets must conflict: %+v", pl.rows)
	}
	if pl.rows[0].warn != "和本批其他项目重名" || pl.rows[1].warn != "和本批其他项目重名" {
		t.Fatalf("warnings = %q %q", pl.rows[0].warn, pl.rows[1].warn)
	}
}

// A target name that fits but makes the whole path too long is refused:
// PATH_MAX is 1024 bytes including the terminator.
func TestPlanRenameFullPathTooLong(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, strings.Repeat("d", 200), strings.Repeat("e", 200),
		strings.Repeat("f", 200), strings.Repeat("g", 200))
	if len(deep) < 800 {
		t.Fatalf("test path unexpectedly short: %d", len(deep))
	}
	item := fileItem(filepath.Join(deep, strings.Repeat("n", 220)+".txt"))
	pl := planRename([]store.Item{item}, renameParams{mode: modeReplace, find: "x", replace: "y"})
	if pl.rows[0].warn != "路径太长" {
		t.Fatalf("warn = %q, want 路径太长 (path %d bytes)", pl.rows[0].warn, len(pl.rows[0].newPath))
	}
	// A path just under the limit plans on (the file itself is absent, so
	// the row reports that instead — proof the length check passed).
	short := fileItem(filepath.Join(dir, strings.Repeat("n", 200)+".txt"))
	pl = planRename([]store.Item{short}, renameParams{mode: modeReplace, find: "x", replace: "y"})
	if pl.rows[0].warn != "原文件不存在" {
		t.Fatalf("short path warn = %q, want 原文件不存在", pl.rows[0].warn)
	}
}

// A target already on disk is no conflict when it is a batch item that
// is moving away, even when the two spellings differ only in Unicode
// normalization: APFS resolves both to the same file.
func TestDiskConflictRecognizesMovingBatchItem(t *testing.T) {
	dir := t.TempDir()
	nfd := filepath.Join(dir, "cafe\u0301.txt")
	if err := os.WriteFile(nfd, []byte("nfd"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "y.txt"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	nfc := filepath.Join(dir, "café.txt") // same file as nfd on APFS
	if _, err := os.Lstat(nfc); err != nil {
		t.Skipf("this volume does not equate NFC and NFD: %v", err)
	}
	rows := []renameRow{
		{oldPath: nfd, newName: "gone.txt", newPath: filepath.Join(dir, "gone.txt")},
		{oldPath: filepath.Join(dir, "y.txt"), newName: "café.txt", newPath: nfc},
	}
	if got := diskConflict(rows, 1); got != "" {
		t.Fatalf("a target that is the batch's own moving file must be free, got %q", got)
	}
}

// The phase-1 temp name is longer than most targets, so the preview must
// refuse a path that would only fail once the rename starts.
func TestPlanRenameTempPathTooLong(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 985 {
		need := 985 - len(dir) - 1
		if need > 250 {
			need = 250
		}
		if need < 1 {
			break
		}
		dir = filepath.Join(dir, strings.Repeat("q", need))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Skipf("cannot build the boundary directory: %v", err)
		}
	}
	if n := len(dir); n < 982 || n > 1010 {
		t.Skipf("boundary directory is %d bytes, outside 982–1010", n)
	}
	f := mkFile(t, dir, "a.txt")
	pl := planRename([]store.Item{fileItem(f)}, renameParams{mode: modeReplace, find: "a", replace: "b"})
	if pl.rows[0].warn != "路径太长" || pl.conflicts != 1 || len(pl.ops) != 0 {
		t.Fatalf("temp path must be refused: warn=%q path=%d temp=%d",
			pl.rows[0].warn, len(pl.rows[0].newPath), len(dir)+1+renameTempNameLen(0))
	}
	if len(pl.rows[0].newPath) > renamePathMax {
		t.Fatal("the target itself already exceeded the limit; test setup is wrong")
	}
	// A directory with room for the temp name plans clean.
	short := mkFile(t, t.TempDir(), "a.txt")
	pl = planRename([]store.Item{fileItem(short)}, renameParams{mode: modeReplace, find: "a", replace: "b"})
	if pl.conflicts != 0 || pl.changed != 1 {
		t.Fatalf("a short path must plan: %+v", pl.rows[0])
	}
}

// The preview's temp-name length must be the real thing: the executor
// builds prefix + index, and renameTempNameLen sizes the check.
func TestRenameTempNameLengthMatches(t *testing.T) {
	prefix := renameTempPrefix()
	for _, i := range []int{0, 9, 10, 99, 999} {
		if got, want := len(prefix+strconv.Itoa(i)), renameTempNameLen(i); got != want {
			t.Fatalf("temp name %d is %d bytes, preview sizes %d", i, got, want)
		}
	}
}
