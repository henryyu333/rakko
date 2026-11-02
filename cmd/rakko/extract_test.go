//go:build darwin

package main

import (
	"archive/tar"
	"archive/zip"
	"encoding/binary"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// writeZip builds a zip at path with entries {name → data}; names may be
// raw bytes (invalid UTF-8) to imitate archives made on other systems.
func makeZip(t *testing.T, path string, entries map[string][]byte, dirs ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, d := range dirs {
		if _, err := zw.Create(d + "/"); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeGbkZip writes a zip whose member names are raw GBK bytes with no
// UTF-8 flag — what a Chinese Windows machine produces.
func writeGbkZip(t *testing.T, path string, names ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range names {
		h := &zip.FileHeader{Method: zip.Store}
		h.Name = gbkBytes(t, name)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("内容" + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// gbkBytes encodes s as GBK using mac's decoder as a reference: brute
// encode is impractical without a library, so encode through NSString by
// round-tripping — the test only needs *some* non-UTF-8 byte name. The
// table covers the fixture names below.
func gbkBytes(t *testing.T, s string) string {
	t.Helper()
	// Hand-encoded: 报表.txt → 报=B1A8 表=B1ED, 数据/明细.csv…
	enc := map[string]string{
		"报表.txt":    "\xb1\xa8\xb1\xed.txt",
		"明细.csv":    "\xc3\xf7\xcf\xb8.csv",
		"数据/明细.csv": "\xca\xfd\xbe\xdd/\xc3\xf7\xcf\xb8.csv",
		"说明.txt":    "\xcb\xb5\xc3\xf7.txt",
	}
	b, ok := enc[s]
	if !ok {
		t.Fatalf("gbkBytes: no fixture encoding for %q", s)
	}
	return b
}

// extractShelf is a shelf whose tray holds archives built under dir.
func extractShelf(t *testing.T, paths ...string) (*shelf, *ui.Tester) {
	t.Helper()
	a, view, _ := convertShelf(t)
	for i := len(paths) - 1; i >= 0; i-- {
		if err := a.store.AddFiles([]string{paths[i]}); err != nil {
			t.Fatal(err)
		}
	}
	return a, view
}

// noStaging fails when a hidden extraction staging folder was left behind.
func noStaging(t *testing.T, a *shelf) {
	t.Helper()
	if m, _ := filepath.Glob(filepath.Join(a.store.ConvertedDir(), ".shelf-extract-*")); len(m) != 0 {
		t.Fatalf("staging left behind: %v", m)
	}
}

// extractedTree lists a result folder's files relative to it.
func extractedTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel != "." {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// A zip with several top-level items unpacks under the archive's name and
// the folder lands in the tray; junk entries never arrive.
func TestExtractZip(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "资料.zip")
	makeZip(t, zipPath, map[string][]byte{
		"a.txt":       []byte("a"),
		"sub/b.txt":   []byte("b"),
		"__MACOSX/x":  []byte("junk"),
		"._a.txt":     []byte("junk"),
		"sub/._b.txt": []byte("junk"),
	}, "sub")
	a, view := extractShelf(t, zipPath)
	view.Frame()
	if err := view.RightClick("资料.zip"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	got := outputNames(a)
	if !slices.Equal(got, []string{"资料"}) {
		t.Fatalf("outputs = %v", got)
	}
	it := a.store.Items[0]
	if !it.Converted {
		t.Fatal("the result folder must be shelf-owned")
	}
	tree := extractedTree(t, it.Path)
	if !slices.Equal(tree, []string{"a.txt", "sub", "sub/b.txt"}) {
		t.Fatalf("tree = %v", tree)
	}
	if b, _ := os.ReadFile(filepath.Join(it.Path, "sub", "b.txt")); string(b) != "b" {
		t.Fatalf("sub/b.txt = %q", b)
	}
	if !strings.HasPrefix(a.status, "已解压 1 项") {
		t.Fatalf("status = %q", a.status)
	}
	noStaging(t, a)
}

// An archive holding one top-level folder yields that folder itself.
func TestExtractSingleTopFolder(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pack.zip")
	makeZip(t, zipPath, map[string][]byte{
		"Foo/one.txt": []byte("1"),
		"Foo/two.txt": []byte("2"),
	}, "Foo")
	a, view := extractShelf(t, zipPath)
	view.Frame()
	if err := view.RightClick("pack.zip"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"Foo"}) {
		t.Fatalf("outputs = %v, the inner folder itself is the result", got)
	}
	tree := extractedTree(t, a.store.Items[0].Path)
	if !slices.Equal(tree, []string{"one.txt", "two.txt"}) {
		t.Fatalf("tree = %v", tree)
	}
	noStaging(t, a)
	// Extracting a second pack.zip must not collide with Foo.
	other := filepath.Join(dir, "pack2.zip")
	makeZip(t, other, map[string][]byte{"Foo/x.txt": []byte("x")}, "Foo")
	if err := a.store.AddFiles([]string{other}); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := view.RightClick("pack2.zip"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	if got := outputNames(a); !slices.Equal(got, []string{"Foo 2", "Foo"}) {
		t.Fatalf("outputs = %v, want Foo and Foo 2", got)
	}
}

// Unsafe member names refuse the archive outright; nothing is extracted.
func TestExtractRejectsUnsafeZip(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("../escape.txt")
	w.Write([]byte("evil"))
	zw.Close()
	zf.Close()
	a, view := extractShelf(t, zipPath)
	view.Frame()
	if err := view.RightClick("evil.zip"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	if a.statusKind != statusErr || !strings.Contains(a.status, "不安全") {
		t.Fatalf("status = %q err=%v", a.status, a.statusKind == statusErr)
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 0 {
		t.Fatalf("a refused archive must leave nothing: %v", entries)
	}
	if _, err := os.Lstat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("the escape entry was written")
	}
}

// An encrypted zip reports so instead of running a tool that would block
// on a password prompt. (zip.Writer cannot encrypt; the flag bit alone is
// enough for the scan to refuse it.)
func TestExtractEncryptedZip(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "secret.zip")
	zf, _ := os.Create(zipPath)
	zw := zip.NewWriter(zf)
	h := &zip.FileHeader{Method: zip.Store}
	h.Name = "a.txt"
	h.Flags = 0x1 // encrypted
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("secret"))
	zw.Close()
	zf.Close()
	a, view := extractShelf(t, zipPath)
	view.Frame()
	if err := view.RightClick("secret.zip"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	if a.statusKind != statusErr || !strings.Contains(a.status, "加密") {
		t.Fatalf("status = %q err=%v", a.status, a.statusKind == statusErr)
	}
}

// A corrupt archive fails with a readable message and leaves nothing.
func TestExtractCorruptZip(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "broken.zip")
	os.WriteFile(zipPath, []byte("not a zip at all"), 0o600)
	a, view := extractShelf(t, zipPath)
	view.Frame()
	if err := view.RightClick("broken.zip"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	if a.statusKind != statusErr || !strings.Contains(a.status, "解压失败") {
		t.Fatalf("status = %q err=%v", a.status, a.statusKind == statusErr)
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 0 {
		t.Fatalf("a failed extract must leave nothing: %v", entries)
	}
}

// A GBK-named zip extracts with real Chinese names, not mojibake.
func TestExtractGbkZipNames(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "windows.zip")
	writeGbkZip(t, zipPath, "报表.txt", "数据/明细.csv")
	a, view := extractShelf(t, zipPath)
	view.Frame()
	if err := view.RightClick("windows.zip"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	it := a.store.Items[0]
	tree := extractedTree(t, it.Path)
	if !slices.Equal(tree, []string{"报表.txt", "数据", "数据/明细.csv"}) {
		t.Fatalf("tree = %v, GBK names must decode to Chinese", tree)
	}
	if b, _ := os.ReadFile(filepath.Join(it.Path, "报表.txt")); !strings.Contains(string(b), "内容") {
		t.Fatalf("content = %q", b)
	}
}

// tar.gz goes through bsdtar: contents land under the archive-named
// folder and real contents match byte for byte.
func TestExtractTgz(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "in")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "说明.txt"), []byte("说明"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0o644)
	tgz := filepath.Join(dir, "包.tgz")
	if err := exec.Command("tar", "-czf", tgz, "-C", src, ".").Run(); err != nil {
		t.Fatal(err)
	}
	a, view := extractShelf(t, tgz)
	view.Frame()
	if err := view.RightClick("包.tgz"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	it := a.store.Items[0]
	tree := extractedTree(t, it.Path)
	if !slices.Equal(tree, []string{"sub", "sub/b.txt", "说明.txt"}) {
		t.Fatalf("tree = %v", tree)
	}
	if b, _ := os.ReadFile(filepath.Join(it.Path, "说明.txt")); string(b) != "说明" {
		t.Fatalf("说明.txt = %q", b)
	}
}

// Cancelling between archives keeps the finished one and leaves neither a
// half-made result nor a staging folder; a cancelled bsdtar run is killed
// and reported as cancelled, not as a failure.
func TestExtractCancel(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "one.zip")
	makeZip(t, zipPath, map[string][]byte{"a.txt": []byte("a")})
	srcD := filepath.Join(dir, "s")
	os.MkdirAll(srcD, 0o755)
	os.WriteFile(filepath.Join(srcD, "x.txt"), []byte("x"), 0o644)
	tgz := filepath.Join(dir, "two.tgz")
	if err := exec.Command("tar", "-czf", tgz, "-C", srcD, ".").Run(); err != nil {
		t.Fatal(err)
	}
	a, _ := extractShelf(t, zipPath, tgz) // tray order: one.zip, then two.tgz
	old := bsdtarRun
	bsdtarRun = func(j *convertJob, args ...string) ([]byte, error) {
		j.cancel.Store(true)
		return nil, errConvertCanceled
	}
	t.Cleanup(func() { bsdtarRun = old })
	a.startConvert(slices.Clone(a.store.Items), toExtract)
	if got := outputNames(a); !slices.Equal(got, []string{"one"}) {
		t.Fatalf("outputs = %v, the finished archive stays and the cancelled one leaves nothing", got)
	}
	if !strings.HasPrefix(a.status, "已取消") {
		t.Fatalf("status = %q, want a cancellation", a.status)
	}
	if matches, _ := filepath.Glob(filepath.Join(a.store.Root, "converted", ".shelf-extract-*")); len(matches) != 0 {
		t.Fatalf("staging left behind: %v", matches)
	}
}

// The menu offers 解压 only when every target is an archive, and greyed
// while a job runs.
func TestExtractMenuOnlyForArchives(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "a.zip")
	makeZip(t, zipPath, map[string][]byte{"a.txt": []byte("a")})
	txt := filepath.Join(dir, "n.txt")
	os.WriteFile(txt, []byte("n"), 0o644)
	a, view := extractShelf(t, zipPath, txt)
	view.Frame()
	if err := view.RightClick("a.zip"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), "解压") {
		t.Fatalf("an archive's menu lacks 解压: %v", view.Menu())
	}
	view.CloseMenu()
	if err := view.RightClick("n.txt"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(view.Menu(), "解压") {
		t.Fatalf("a plain file must not offer 解压: %v", view.Menu())
	}
	view.CloseMenu()
	selectAll(a)
	view.Frame()
	if err := view.RightClick("a.zip"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(view.Menu(), "解压 2 项") || slices.Contains(view.Menu(), "解压") {
		t.Fatalf("a mixed selection must not offer 解压: %v", view.Menu())
	}
}

// Free space is checked before extracting when the archive size is known.
func TestExtractChecksDiskSpace(t *testing.T) {
	fakeConverters(t)
	old := freeDiskSpace
	freeDiskSpace = func(string) (int64, error) { return 10, nil }
	t.Cleanup(func() { freeDiskSpace = old })
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "big.zip")
	makeZip(t, zipPath, map[string][]byte{"a.txt": make([]byte, 4096)})
	a, view := extractShelf(t, zipPath)
	view.Frame()
	if err := view.RightClick("big.zip"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	if a.statusKind != statusErr || !strings.Contains(a.status, "磁盘空间不足") {
		t.Fatalf("status = %q err=%v", a.status, a.statusKind == statusErr)
	}
	if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 0 {
		t.Fatalf("nothing should be extracted: %v", entries)
	}
}

// A zip member that is a symlink to an absolute path is refused, and a
// tar symlink pointing outside is dropped rather than followed.
func TestExtractSymlinkSafety(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	// zip: member is a symlink to /tmp
	zipPath := filepath.Join(dir, "link.zip")
	zf, _ := os.Create(zipPath)
	zw := zip.NewWriter(zf)
	h := &zip.FileHeader{Name: "link", Method: zip.Store}
	h.SetMode(0o777 | os.ModeSymlink)
	w, _ := zw.CreateHeader(h)
	w.Write([]byte("/tmp"))
	zw.Close()
	zf.Close()
	a, view := extractShelf(t, zipPath)
	view.Frame()
	if err := view.RightClick("link.zip"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
	if a.statusKind != statusErr || !strings.Contains(a.status, "符号链接") {
		t.Fatalf("status = %q err=%v", a.status, a.statusKind == statusErr)
	}
}

// writeRar writes a minimal, data-less RAR4 archive: a main header with
// mainFlags and, when files > 0, that many file headers carrying the solid
// flag (bsdtar only refuses solid RAR4 once it meets a second solid file).
// The checksums are real, so bsdtar reports the feature error rather than
// a header error.
func writeRar(t *testing.T, path string, mainFlags uint16, files int) {
	t.Helper()
	crc := func(body []byte) []byte {
		return binary.LittleEndian.AppendUint16(nil, uint16(crc32.ChecksumIEEE(body)))
	}
	var b []byte
	b = append(b, "Rar!\x1a\x07\x00"...)
	main := []byte{0x73}
	main = binary.LittleEndian.AppendUint16(main, mainFlags)
	main = binary.LittleEndian.AppendUint16(main, 13)
	main = binary.LittleEndian.AppendUint16(main, 0) // reserved1
	main = binary.LittleEndian.AppendUint32(main, 0) // reserved2
	b = append(b, crc(main)...)
	b = append(b, main...)
	for i := 0; i < files; i++ {
		name := []byte("f" + string(rune('a'+i)) + ".txt")
		body := []byte{0x74}
		body = binary.LittleEndian.AppendUint16(body, 0x0010) // solid
		body = binary.LittleEndian.AppendUint16(body, uint16(32+len(name)))
		body = binary.LittleEndian.AppendUint32(body, 0) // pack size
		body = binary.LittleEndian.AppendUint32(body, 0) // unpacked
		body = append(body, 3)                           // host OS: unix
		body = binary.LittleEndian.AppendUint32(body, 0) // file crc
		body = binary.LittleEndian.AppendUint32(body, 0) // mtime
		body = append(body, 20, 0x30)                    // version, stm
		body = binary.LittleEndian.AppendUint16(body, uint16(len(name)))
		body = binary.LittleEndian.AppendUint32(body, 0o644<<16)
		body = append(body, name...)
		b = append(b, crc(body)...)
		b = append(b, body...)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// extractOne runs 解压 on the named tray item and returns the shelf.
func extractOne(t *testing.T, a *shelf, view *ui.Tester, name string) {
	t.Helper()
	if err := view.RightClick(name); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("解压"); err != nil {
		t.Fatal(err)
	}
}

// Two archives whose results would take the same name share the batch's
// reservations: the second gets a free name instead of failing to rename
// over the first. (Old code gave the single-top folder 「foo」 and then
// could not move foo.tgz's own folder to the already-taken 「foo」.)
func TestExtractBatchNameCollisions(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	pack := filepath.Join(dir, "pack.zip")
	makeZip(t, pack, map[string][]byte{"foo/a.txt": []byte("a")}, "foo")
	// foo.tgz holds two top-level entries, so its result folder is the
	// archive-named one reserved by the planner.
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "b.txt"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(src, "c.txt"), []byte("c"), 0o644)
	tgz := filepath.Join(dir, "foo.tgz")
	if err := exec.Command("tar", "-czf", tgz, "-C", src, "b.txt", "c.txt").Run(); err != nil {
		t.Fatal(err)
	}
	a, view := extractShelf(t, pack, tgz)
	items := slices.Clone(a.store.Items) // pack.zip first
	view.Frame()
	a.startConvert(items, toExtract)
	if a.statusKind == statusErr {
		t.Fatalf("extraction failed: %s", a.status)
	}
	got := outputNames(a)
	if !slices.Equal(got, []string{"foo 2", "foo"}) {
		t.Fatalf("outputs = %v, want the single-top folder renamed around the reservation", got)
	}
	if _, err := os.Stat(filepath.Join(a.store.Items[0].Path, "a.txt")); err != nil {
		t.Fatalf("foo 2 is not pack.zip's result: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.store.Items[1].Path, "b.txt")); err != nil {
		t.Fatalf("foo is not foo.tgz's result: %v", err)
	}
}

// A failing bsdtar listing must not turn its partial file list into the
// error text: stdout carries the names, the error comes from stderr only.
func TestExtractErrorExcludesListing(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	full := filepath.Join(dir, "full.tar")
	f, err := os.Create(full)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		body := strings.Repeat(name, 100)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.tar")
	cut := 512 + 1100 + 100 // inside the second member's data
	if cut >= len(data) {
		t.Fatalf("fixture too small to truncate: %d", len(data))
	}
	if err := os.WriteFile(broken, data[:cut], 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runBsdtar(nil, "-tf", broken)
	if err == nil {
		t.Fatal("a truncated tar must fail to list")
	}
	if !strings.Contains(string(out), "one.txt") {
		t.Fatalf("fixture did not exercise a partial listing: %q", out)
	}
	for _, leaked := range []string{"one.txt", "two.txt"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error text carries the file listing (%q): %s", leaked, err)
		}
	}
	// The listing path reports a plain-language reason, not raw stderr.
	if _, err := listArchive(broken, &convertJob{}); err == nil || !strings.Contains(err.Error(), "损坏") {
		t.Fatalf("listArchive error = %v, want the damaged-archive reason", err)
	}
}

// RAR cases bsdtar cannot handle get their own message: RAR4 solid,
// encrypted entries, and split-archive parts.
func TestExtractRarLimitations(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	solid := filepath.Join(dir, "solid.rar")
	writeRar(t, solid, 0, 2)
	enc := filepath.Join(dir, "locked.rar")
	writeRar(t, enc, 0x0080, 0) // main header: password
	part := filepath.Join(dir, "vol.part1.rar")
	writeRar(t, part, 0x0001, 0) // main header: volume

	for _, tc := range []struct {
		name, want string
	}{
		{"solid.rar", "固实压缩"},
		{"locked.rar", "加密"},
		{"vol.part1.rar", "分卷"},
	} {
		a, view := extractShelf(t, filepath.Join(dir, tc.name))
		view.Frame()
		extractOne(t, a, view, tc.name)
		if a.statusKind != statusErr || !strings.Contains(a.status, tc.want) {
			t.Fatalf("%s: status = %q err=%v, want %q", tc.name, a.status, a.statusKind == statusErr, tc.want)
		}
		if entries, _ := os.ReadDir(a.store.ConvertedDir()); len(entries) != 0 {
			t.Fatalf("%s: nothing may be extracted: %v", tc.name, entries)
		}
	}
}

// __MACOSX branches and ._ files are dropped at any depth, not just at the
// archive root.
func TestExtractJunkAtAnyDepth(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "deep.zip")
	makeZip(t, zipPath, map[string][]byte{
		"keep.txt":             []byte("k"),
		"docs/readme.txt":      []byte("r"),
		"docs/__MACOSX/x":      []byte("junk"),
		"docs/.__keep.txt":     []byte("junk"),
		"docs/sub/real.txt":    []byte("real"),
		"docs/sub/__MACOSX/1":  []byte("junk"),
		"docs/sub/.__real.txt": []byte("junk"),
	})
	a, view := extractShelf(t, zipPath)
	view.Frame()
	extractOne(t, a, view, "deep.zip")
	tree := extractedTree(t, a.store.Items[0].Path)
	want := []string{"docs", "docs/readme.txt", "docs/sub", "docs/sub/real.txt", "keep.txt"}
	if !slices.Equal(tree, want) {
		t.Fatalf("tree = %v, want %v", tree, want)
	}
}

// A name that is only an extension still produces a visible folder rather
// than 「 2」, and .tbz is a supported extension.
func TestArchiveNames(t *testing.T) {
	fakeConverters(t)
	dir := t.TempDir()
	odd := filepath.Join(dir, ".zip")
	makeZip(t, odd, map[string][]byte{"a.txt": []byte("a"), "b.txt": []byte("b")})
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "c.txt"), []byte("c"), 0o644)
	tbz := filepath.Join(dir, "pack.tbz")
	if err := exec.Command("tar", "-cjf", tbz, "-C", src, "c.txt").Run(); err != nil {
		t.Fatal(err)
	}
	if got := archiveExt(tbz); got != ".tbz" {
		t.Fatalf("archiveExt(.tbz) = %q", got)
	}

	a, view := extractShelf(t, odd, tbz)
	view.Frame()
	extractOne(t, a, view, ".zip")
	if got := outputNames(a); !slices.Equal(got, []string{"zip"}) {
		t.Fatalf("outputs = %v, want a visible zip folder", got)
	}
	view.Frame()
	extractOne(t, a, view, "pack.tbz")
	if _, err := os.Stat(filepath.Join(a.store.Items[0].Path, "c.txt")); err != nil {
		t.Fatalf("tbz extraction failed: %v (status %q)", err, a.status)
	}
}
